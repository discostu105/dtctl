package webui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Query activity: what is queued, running and recently finished, so the UI
// can show why things are slow, and so abandoned work never holds a slot.
//
// Every distinct query runs as one "flight" shared by all callers that want
// it (single-flight). A flight is reference-counted: when the last caller
// goes away (the tab navigated, the timeframe changed and the browser
// aborted its batch), the flight is cancelled. Cancelling the context stops
// waiting for a slot, stops polling Grail and sends Grail a best-effort
// cancel, so stale work never blocks the next page.

const flightTimeout = 2 * time.Minute

// ActiveQuery describes one queued or running flight.
type ActiveQuery struct {
	Key      string    `json:"key"`
	Query    string    `json:"query"`
	From     string    `json:"from,omitempty"`
	To       string    `json:"to,omitempty"`
	State    string    `json:"state"` // queued | running
	QueuedAt time.Time `json:"queuedAt"`
	Started  time.Time `json:"startedAt,omitempty"`
	Waiters  int       `json:"waiters"`
}

// FinishedQuery is one entry of the recent-activity log.
type FinishedQuery struct {
	Query        string    `json:"query"`
	At           time.Time `json:"at"`
	ElapsedMs    int64     `json:"elapsedMs"`
	QueuedMs     int64     `json:"queuedMs,omitempty"`
	ExecutionMs  int64     `json:"executionMs,omitempty"`
	ScannedBytes int64     `json:"scannedBytes,omitempty"`
	Records      int       `json:"records"`
	Outcome      string    `json:"outcome"` // ok | cached | shared | error | cancelled
	Error        string    `json:"error,omitempty"`
}

// ActivityTotals are counters since the server started (or the last tenant switch).
type ActivityTotals struct {
	Requested int64 `json:"requested"`
	Executed  int64 `json:"executed"`
	Cached    int64 `json:"cached"`
	Shared    int64 `json:"shared"`
	Errors    int64 `json:"errors"`
	Cancelled int64 `json:"cancelled"`
	Scanned   int64 `json:"scannedBytes"`
}

// Activity is the GET /api/activity payload.
type Activity struct {
	MaxConcurrent int             `json:"maxConcurrent"`
	Running       []ActiveQuery   `json:"running"`
	Queued        []ActiveQuery   `json:"queued"`
	Recent        []FinishedQuery `json:"recent"`
	Totals        ActivityTotals  `json:"totals"`
}

type flight struct {
	done    chan struct{}
	v       any
	err     error
	waiters int
	cancel  context.CancelFunc
	info    ActiveQuery
}

// queryCache is a tiny TTL cache with cancellable single-flight. Entries are
// evicted lazily once they are older than cacheMaxAge.
type queryCache struct {
	now func() time.Time

	mu       sync.Mutex
	entries  map[string]cacheEntry
	inflight map[string]*flight
	recent   []FinishedQuery
	totals   ActivityTotals
}

type cacheEntry struct {
	v  any
	at time.Time
}

const (
	cacheMaxAge = 10 * time.Minute
	recentMax   = 60
)

func newQueryCache(now func() time.Time) *queryCache {
	return &queryCache{now: now, entries: map[string]cacheEntry{}, inflight: map[string]*flight{}}
}

func (c *queryCache) get(key string, ttl time.Duration) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || c.now().Sub(e.at) > ttl {
		return nil, false
	}
	return e.v, true
}

func (c *queryCache) put(key string, v any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.putLocked(key, v)
}

func (c *queryCache) putLocked(key string, v any) {
	now := c.now()
	c.entries[key] = cacheEntry{v: v, at: now}
	if len(c.entries) > 2000 {
		for k, e := range c.entries {
			if now.Sub(e.at) > cacheMaxAge {
				delete(c.entries, k)
			}
		}
	}
}

// do runs fn once per key for all concurrent callers. fn gets the flight's
// own context (detached from any single caller, bounded by flightTimeout)
// and a markRunning callback to call once it holds an execution slot. A
// caller whose ctx ends stops waiting; the last one to leave cancels fn.
// shared reports whether this caller joined an existing flight.
func (c *queryCache) do(ctx context.Context, key string, info ActiveQuery, fn func(ctx context.Context, markRunning func()) (any, error)) (v any, shared bool, err error) {
	c.mu.Lock()
	f, ok := c.inflight[key]
	if ok {
		f.waiters++
		f.info.Waiters = f.waiters
		c.totals.Shared++
		c.mu.Unlock()
	} else {
		fctx, cancel := context.WithTimeout(context.Background(), flightTimeout)
		info.Key, info.State, info.QueuedAt, info.Waiters = key, "queued", c.now(), 1
		f = &flight{done: make(chan struct{}), waiters: 1, cancel: cancel, info: info}
		c.inflight[key] = f
		c.mu.Unlock()
		go func() {
			defer cancel()
			v, err := fn(fctx, func() {
				c.mu.Lock()
				f.info.State, f.info.Started = "running", c.now()
				c.mu.Unlock()
			})
			c.mu.Lock()
			f.v, f.err = v, err
			if err == nil {
				c.putLocked(key, v)
			}
			if c.inflight[key] == f {
				delete(c.inflight, key)
			}
			c.mu.Unlock()
			close(f.done)
		}()
	}
	select {
	case <-f.done:
		return f.v, ok, f.err
	case <-ctx.Done():
		c.mu.Lock()
		f.waiters--
		f.info.Waiters = f.waiters
		if f.waiters <= 0 {
			f.cancel()
			if c.inflight[key] == f {
				delete(c.inflight, key)
			}
		}
		c.mu.Unlock()
		return nil, ok, ctx.Err()
	}
}

// cancelKey force-cancels a flight (the UI's "cancel" button).
func (c *queryCache) cancelKey(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	f, ok := c.inflight[key]
	if !ok {
		return false
	}
	f.cancel()
	delete(c.inflight, key)
	return true
}

// reset drops every cached result and cancels all flights (tenant switch).
func (c *queryCache) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, f := range c.inflight {
		f.cancel()
		delete(c.inflight, k)
	}
	c.entries = map[string]cacheEntry{}
	c.recent = nil
	c.totals = ActivityTotals{}
}

func (c *queryCache) record(q FinishedQuery, executed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.totals.Requested++
	switch q.Outcome {
	case "cached":
		c.totals.Cached++
	case "error":
		c.totals.Errors++
	case "cancelled":
		c.totals.Cancelled++
	}
	if executed {
		c.totals.Executed++
		c.totals.Scanned += q.ScannedBytes
	}
	// cache hits are cheap and frequent: keep them out of the log, count them only
	if q.Outcome == "cached" {
		return
	}
	c.recent = append(c.recent, q)
	if len(c.recent) > recentMax {
		c.recent = c.recent[len(c.recent)-recentMax:]
	}
}

func (c *queryCache) snapshot(maxConcurrent int) Activity {
	c.mu.Lock()
	defer c.mu.Unlock()
	a := Activity{MaxConcurrent: maxConcurrent, Running: []ActiveQuery{}, Queued: []ActiveQuery{}, Totals: c.totals}
	for _, f := range c.inflight {
		if f.info.State == "running" {
			a.Running = append(a.Running, f.info)
		} else {
			a.Queued = append(a.Queued, f.info)
		}
	}
	sort.Slice(a.Running, func(i, j int) bool { return a.Running[i].Started.Before(a.Running[j].Started) })
	sort.Slice(a.Queued, func(i, j int) bool { return a.Queued[i].QueuedAt.Before(a.Queued[j].QueuedAt) })
	a.Recent = make([]FinishedQuery, 0, len(c.recent))
	for i := len(c.recent) - 1; i >= 0; i-- {
		a.Recent = append(a.Recent, c.recent[i])
	}
	return a
}

func (s *Server) handleActivity(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.cache.snapshot(s.opts.MaxConcurrent))
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil || body.Key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key required"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"cancelled": s.cache.cancelKey(body.Key)})
}
