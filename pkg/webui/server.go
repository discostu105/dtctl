// Package webui serves the dtctl web UI proof of concept: a single-page app
// (embedded from dist/) plus a thin JSON API that runs DQL on behalf of the
// browser. The browser never sees the Dynatrace token; every request goes
// through this process, which reuses dtctl's configured context.
//
// The API is deliberately read-only: it runs DQL (which cannot mutate) and
// lists documents. There are no mutating endpoints, so no safety checks apply.
package webui

import (
	"bytes"
	"compress/gzip"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdkquery "github.com/dynatrace-oss/dtctl/sdk/api/query"
)

//go:embed all:dist
var distFS embed.FS

// QueryFunc executes one DQL query over the given (already resolved, RFC3339)
// timeframe. An empty from/to leaves the timeframe to the query itself.
type QueryFunc func(ctx context.Context, query, from, to string, maxRecords int64) (*sdkquery.Response, error)

// Document is the slim document shape the UI lists (dashboards, notebooks).
type Document struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	Owner      string    `json:"owner"`
	Modified   time.Time `json:"modified"`
	IsPrivate  bool      `json:"isPrivate"`
	LastOpened time.Time `json:"lastOpened,omitempty"`
}

// DocumentsFunc lists documents of one type ("dashboard", "notebook").
type DocumentsFunc func(ctx context.Context, docType string) ([]Document, error)

// Meta describes the environment the UI is connected to.
type Meta struct {
	Context     string   `json:"context"`
	Environment string   `json:"environment"`
	SafetyLevel string   `json:"safetyLevel"`
	Version     string   `json:"version"`
	UserName    string   `json:"userName,omitempty"`
	UserEmail   string   `json:"userEmail,omitempty"`
	Contexts    []string `json:"contexts,omitempty"`
	// Tenants lists the switchable contexts (no credentials).
	Tenants []Tenant `json:"tenants,omitempty"`
}

// Tenant is one configured dtctl context the UI can switch to.
type Tenant struct {
	Name        string `json:"name"`
	Environment string `json:"environment"`
	SafetyLevel string `json:"safetyLevel,omitempty"`
}

// Options configures a Server.
type Options struct {
	Query     QueryFunc
	Documents DocumentsFunc
	// Meta is resolved lazily on first request (it may call the user API).
	Meta func() Meta
	// QueryAssist forwards a DQL editor request (op is "autocomplete" or
	// "verify") to Grail and returns the raw JSON response and status.
	QueryAssist func(ctx context.Context, op string, body []byte) ([]byte, int, error)
	// SwitchContext points every later request at another configured dtctl
	// context (in this process only; the config file is not changed). Optional.
	SwitchContext func(name string) error
	// MaxConcurrent bounds concurrent DQL executions against the tenant.
	MaxConcurrent int
	// Now is injectable for tests.
	Now func() time.Time
}

// Server is the web UI HTTP handler.
type Server struct {
	opts  Options
	mux   *http.ServeMux
	sem   chan struct{}
	cache *queryCache

	metaMu sync.Mutex
	meta   *Meta
	// gen changes on every tenant switch and is part of every cache key, so
	// a result from the previous tenant can never be served after a switch.
	gen atomic.Int64

	static map[string]staticFile
}

type staticFile struct {
	body        []byte
	gz          []byte
	contentType string
	immutable   bool
}

// New builds a Server.
func New(opts Options) (*Server, error) {
	if opts.Query == nil {
		return nil, errors.New("webui: Query is required")
	}
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = 8
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	s := &Server{
		opts:  opts,
		mux:   http.NewServeMux(),
		sem:   make(chan struct{}, opts.MaxConcurrent),
		cache: newQueryCache(opts.Now),
	}
	if err := s.loadStatic(); err != nil {
		return nil, err
	}
	s.mux.HandleFunc("GET /api/meta", s.handleMeta)
	s.mux.HandleFunc("POST /api/batch", s.handleBatch)
	s.mux.HandleFunc("GET /api/documents", s.handleDocuments)
	s.mux.HandleFunc("POST /api/dql/{op}", s.handleQueryAssist)
	s.mux.HandleFunc("GET /api/activity", s.handleActivity)
	s.mux.HandleFunc("POST /api/activity/cancel", s.handleCancel)
	s.mux.HandleFunc("POST /api/context", s.handleContext)
	s.mux.HandleFunc("/", s.handleStatic)
	return s, nil
}

// ServeHTTP applies the request guards and dispatches.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// DNS-rebinding guard: only answer requests addressed to a loopback name.
	if !isLoopbackHost(r.Host) {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		// CSRF guard: a custom header cannot be set cross-origin without a
		// CORS preflight, which this server never approves.
		if r.Header.Get("X-Dtctl-Web") != "1" {
			http.Error(w, "missing X-Dtctl-Web header", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	s.mux.ServeHTTP(w, r)
}

func isLoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) handleMeta(w http.ResponseWriter, _ *http.Request) {
	s.metaMu.Lock()
	if s.meta == nil {
		m := Meta{}
		if s.opts.Meta != nil {
			m = s.opts.Meta()
		}
		s.meta = &m
	}
	m := *s.meta
	s.metaMu.Unlock()
	writeJSON(w, http.StatusOK, m)
}

// handleQueryAssist proxies the DQL editor's read-only helpers (autocomplete,
// verify). Both are idempotent analysis calls: nothing is executed.
func (s *Server) handleQueryAssist(w http.ResponseWriter, r *http.Request) {
	op := r.PathValue("op")
	if op != "autocomplete" && op != "verify" {
		http.NotFound(w, r)
		return
	}
	if s.opts.QueryAssist == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "query assist not available"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 256<<10))
	if err != nil || !json.Valid(body) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	key := fmt.Sprintf("assist%d:%s:%s", s.gen.Load(), op, body)
	if v, ok := s.cache.get(key, 5*time.Minute); ok {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(v.([]byte))
		return
	}
	out, status, err := s.opts.QueryAssist(r.Context(), op, body)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if status == http.StatusOK {
		s.cache.put(key, out)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(out)
}

func (s *Server) handleDocuments(w http.ResponseWriter, r *http.Request) {
	if s.opts.Documents == nil {
		writeJSON(w, http.StatusOK, []Document{})
		return
	}
	docType := r.URL.Query().Get("type")
	if docType != "dashboard" && docType != "notebook" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "type must be dashboard or notebook"})
		return
	}
	key := fmt.Sprintf("docs%d:%s", s.gen.Load(), docType)
	if v, ok := s.cache.get(key, 60*time.Second); ok {
		writeJSON(w, http.StatusOK, v)
		return
	}
	docs, err := s.opts.Documents(r.Context(), docType)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Modified.After(docs[j].Modified) })
	s.cache.put(key, docs)
	writeJSON(w, http.StatusOK, docs)
}

// QueryRequest is one query in a batch.
type QueryRequest struct {
	ID         string `json:"id"`
	Query      string `json:"query"`
	From       string `json:"from,omitempty"` // "now-2h", "now", RFC3339
	To         string `json:"to,omitempty"`
	MaxRecords int64  `json:"maxRecords,omitempty"`
	// TTL in seconds a cached result may be reused (0 = default 20s).
	TTL int `json:"ttl,omitempty"`
	// Fresh bypasses the cache (the result still refreshes it).
	Fresh bool `json:"fresh,omitempty"`
}

// QueryResult is one streamed batch line.
type QueryResult struct {
	ID        string            `json:"id"`
	OK        bool              `json:"ok"`
	Error     string            `json:"error,omitempty"`
	Records   []map[string]any  `json:"records,omitempty"`
	Types     map[string]string `json:"types,omitempty"`
	Meta      *ResultMeta       `json:"meta,omitempty"`
	Cached    bool              `json:"cached,omitempty"`
	ElapsedMs int64             `json:"elapsedMs"`
}

// ResultMeta carries the Grail execution metadata the UI surfaces.
type ResultMeta struct {
	ExecutionMs    int64    `json:"executionMs"`
	ScannedRecords int64    `json:"scannedRecords"`
	ScannedBytes   int64    `json:"scannedBytes"`
	Sampled        bool     `json:"sampled,omitempty"`
	From           string   `json:"from,omitempty"`
	To             string   `json:"to,omitempty"`
	Notifications  []string `json:"notifications,omitempty"`
}

const maxBatch = 32

// handleBatch runs every query in the request concurrently and streams each
// result as one NDJSON line the moment it completes, so the UI paints panels
// progressively from a single HTTP request (browsers cap parallel HTTP/1.1
// connections at six per host; one streamed batch sidesteps that).
func (s *Server) handleBatch(w http.ResponseWriter, r *http.Request) {
	var reqs []QueryRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&reqs); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid batch: " + err.Error()})
		return
	}
	if len(reqs) == 0 || len(reqs) > maxBatch {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("batch must hold 1..%d queries", maxBatch)})
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	results := make(chan QueryResult, len(reqs))
	for _, q := range reqs {
		go func(q QueryRequest) { results <- s.runOne(r.Context(), q) }(q)
	}
	enc := json.NewEncoder(w)
	for range reqs {
		select {
		case res := <-results:
			if err := enc.Encode(res); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) runOne(ctx context.Context, q QueryRequest) QueryResult {
	start := s.opts.Now()
	res := QueryResult{ID: q.ID}
	if strings.TrimSpace(q.Query) == "" {
		res.Error = "empty query"
		return res
	}
	ttl := time.Duration(q.TTL) * time.Second
	if ttl <= 0 {
		ttl = 20 * time.Second
	}
	key := fmt.Sprintf("q%d:%s|%s|%s|%d", s.gen.Load(), q.Query, q.From, q.To, q.MaxRecords)

	if !q.Fresh {
		if v, ok := s.cache.get(key, ttl); ok {
			out := v.(QueryResult)
			out.ID, out.Cached = q.ID, true
			out.ElapsedMs = s.opts.Now().Sub(start).Milliseconds()
			s.cache.record(FinishedQuery{Query: q.Query, At: start, ElapsedMs: out.ElapsedMs, Records: len(out.Records), Outcome: "cached"}, false)
			return out
		}
	}

	// Single-flight: identical concurrent queries (e.g. two panels, or a
	// hover-prefetch racing the click) share one Grail execution.
	// The flight is shared by every caller asking for the same query and is
	// cancelled when the last of them goes away (see activity.go).
	var queuedMs atomic.Int64
	v, shared, err := s.cache.do(ctx, key, ActiveQuery{Query: q.Query, From: q.From, To: q.To}, func(ctx context.Context, markRunning func()) (any, error) {
		queued := s.opts.Now()
		select {
		case s.sem <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer func() { <-s.sem }()
		queuedMs.Store(s.opts.Now().Sub(queued).Milliseconds())
		markRunning()

		from, to, err := resolveTimeframe(q.From, q.To, s.opts.Now())
		if err != nil {
			return nil, err
		}
		resp, err := s.opts.Query(ctx, q.Query, from, to, q.MaxRecords)
		if err != nil {
			return nil, err
		}
		// A cancelled execution may come back as (nil, nil): never let that
		// become a cached empty result.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if resp == nil {
			return nil, errors.New("query returned no response")
		}
		out := convertResponse(resp)
		out.OK = true
		if out.Meta != nil && out.Meta.From == "" {
			out.Meta.From, out.Meta.To = from, to
		}
		return out, nil
	})
	elapsed := s.opts.Now().Sub(start).Milliseconds()
	if err != nil {
		res.Error = err.Error()
		res.ElapsedMs = elapsed
		outcome := "error"
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			outcome = "cancelled"
		}
		s.cache.record(FinishedQuery{Query: q.Query, At: start, ElapsedMs: elapsed, QueuedMs: queuedMs.Load(), Outcome: outcome, Error: err.Error()}, !shared)
		return res
	}
	out := v.(QueryResult)
	out.ID = q.ID
	out.ElapsedMs = elapsed
	fq := FinishedQuery{Query: q.Query, At: start, ElapsedMs: elapsed, QueuedMs: queuedMs.Load(), Records: len(out.Records), Outcome: "ok"}
	if shared {
		fq.Outcome = "shared"
	}
	if out.Meta != nil {
		fq.ExecutionMs, fq.ScannedBytes = out.Meta.ExecutionMs, out.Meta.ScannedBytes
	}
	s.cache.record(fq, !shared)
	return out
}

func convertResponse(resp *sdkquery.Response) QueryResult {
	var out QueryResult
	if resp == nil {
		return out
	}
	var records []map[string]any
	var types []sdkquery.ColumnTypes
	md := resp.Metadata
	if resp.Result != nil {
		records = resp.Result.Records
		types = resp.Result.Types
		if resp.Result.Metadata != nil {
			md = resp.Result.Metadata
		}
	} else {
		records = resp.Records
	}
	if records == nil {
		records = []map[string]any{}
	}
	out.Records = records
	out.Types = flattenTypes(types)
	out.Meta = &ResultMeta{}
	if md != nil && md.Grail != nil {
		g := md.Grail
		out.Meta.ExecutionMs = g.ExecutionTimeMilliseconds
		out.Meta.ScannedRecords = g.ScannedRecords
		out.Meta.ScannedBytes = g.ScannedBytes
		out.Meta.Sampled = g.Sampled
		if g.AnalysisTimeframe != nil {
			out.Meta.From = g.AnalysisTimeframe.Start
			out.Meta.To = g.AnalysisTimeframe.End
		}
		for _, n := range g.Notifications {
			if n.Message != "" {
				out.Meta.Notifications = append(out.Meta.Notifications, n.Message)
			}
		}
	}
	return out
}

// flattenTypes merges the per-range type mappings into one column→type map
// (first range wins). Good enough for rendering: the UI uses it to coerce
// longs (sent as strings) and to pick formatters for timestamps/durations.
func flattenTypes(types []sdkquery.ColumnTypes) map[string]string {
	if len(types) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, t := range types {
		for col, ct := range t.Mappings {
			if _, seen := out[col]; !seen {
				out[col] = ct.Type
			}
		}
	}
	return out
}

// resolveTimeframe turns UI timeframe expressions into RFC3339 strings.
// Accepted forms: "" (unset), "now", "now-15m" / "now-2h" / "now-7d" / "now-30s",
// or an RFC3339 timestamp.
func resolveTimeframe(from, to string, now time.Time) (string, string, error) {
	f, err := resolveInstant(from, now)
	if err != nil {
		return "", "", fmt.Errorf("from: %w", err)
	}
	t, err := resolveInstant(to, now)
	if err != nil {
		return "", "", fmt.Errorf("to: %w", err)
	}
	if f != "" && t == "" {
		t = now.UTC().Format(time.RFC3339Nano)
	}
	return f, t, nil
}

func resolveInstant(expr string, now time.Time) (string, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "", nil
	}
	if expr == "now" {
		return now.UTC().Format(time.RFC3339Nano), nil
	}
	if rest, ok := strings.CutPrefix(expr, "now-"); ok {
		d, err := parseDuration(rest)
		if err != nil {
			return "", err
		}
		return now.Add(-d).UTC().Format(time.RFC3339Nano), nil
	}
	ts, err := time.Parse(time.RFC3339Nano, expr)
	if err != nil {
		return "", fmt.Errorf("unsupported time %q", expr)
	}
	return ts.UTC().Format(time.RFC3339Nano), nil
}

func parseDuration(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	unit := map[byte]time.Duration{'s': time.Second, 'm': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour}[s[len(s)-1]]
	if unit == 0 {
		return 0, fmt.Errorf("invalid duration unit in %q", s)
	}
	return time.Duration(n) * unit, nil
}

// --- static assets --------------------------------------------------------

func (s *Server) loadStatic() error {
	s.static = map[string]staticFile{}
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return err
	}
	return fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := fs.ReadFile(sub, p)
		if err != nil {
			return err
		}
		ct := mime.TypeByExtension(path.Ext(p))
		if ct == "" {
			ct = http.DetectContentType(body)
		}
		f := staticFile{body: body, contentType: ct, immutable: strings.HasPrefix(p, "assets/")}
		// Pre-compress text assets once at startup; serving is then a memcpy.
		if len(body) > 1024 && compressible(ct) {
			var buf bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
			_, _ = zw.Write(body)
			_ = zw.Close()
			if buf.Len() < len(body) {
				f.gz = buf.Bytes()
			}
		}
		s.static["/"+p] = f
		return nil
	})
}

func compressible(ct string) bool {
	for _, p := range []string{"text/", "application/javascript", "application/json", "image/svg"} {
		if strings.HasPrefix(ct, p) {
			return true
		}
	}
	return false
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	f, ok := s.static[r.URL.Path]
	if !ok {
		// SPA fallback: unknown paths are client-side routes.
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			http.NotFound(w, r)
			return
		}
		f, ok = s.static["/index.html"]
		if !ok {
			http.Error(w, "web UI assets not built (run: make build-webui)", http.StatusNotFound)
			return
		}
	}
	h := w.Header()
	h.Set("Content-Type", f.contentType)
	if f.immutable {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	h.Add("Vary", "Accept-Encoding")
	body := f.body
	if f.gz != nil && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		h.Set("Content-Encoding", "gzip")
		body = f.gz
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// --- cache ----------------------------------------------------------------

// handleContext switches the tenant this server queries. Cached results and
// running queries of the previous tenant are dropped.
func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	if s.opts.SwitchContext == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "switching is not supported"})
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&body); err != nil || body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	if err := s.opts.SwitchContext(body.Name); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.gen.Add(1)
	s.cache.reset()
	s.metaMu.Lock()
	s.meta = nil
	s.metaMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"context": body.Name})
}
