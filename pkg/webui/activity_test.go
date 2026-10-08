package webui

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdkquery "github.com/dynatrace-oss/dtctl/sdk/api/query"
)

// blockingQuery blocks until released or its context ends, and reports how it ended.
func blockingQuery(release <-chan struct{}, cancelled chan<- struct{}) QueryFunc {
	return func(ctx context.Context, _, _, _ string, _ int64) (*sdkquery.Response, error) {
		select {
		case <-release:
			return okResponse(), nil
		case <-ctx.Done():
			close(cancelled)
			return nil, ctx.Err()
		}
	}
}

func activity(t *testing.T, s *Server) Activity {
	t.Helper()
	var a Activity
	if err := json.Unmarshal(do(s, "GET", "/api/activity", "", nil).Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAbandonedQueryIsCancelled(t *testing.T) {
	cancelled := make(chan struct{})
	s := newTestServer(t, blockingQuery(make(chan struct{}), cancelled))

	ctx, abort := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", "/api/batch", strings.NewReader(`[{"id":"a","query":"slow"}]`)).WithContext(ctx)
	req.Host = "localhost:7878"
	req.Header.Set("X-Dtctl-Web", "1")
	done := make(chan struct{})
	go func() {
		s.ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()
	waitFor(t, "query running", func() bool { return len(activity(t, s).Running) == 1 })

	abort() // the browser navigated away
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("abandoned query kept running")
	}
	<-done
	waitFor(t, "slot freed", func() bool { a := activity(t, s); return len(a.Running)+len(a.Queued) == 0 })
}

func TestActivityAndCancel(t *testing.T) {
	cancelled := make(chan struct{})
	s := newTestServer(t, blockingQuery(make(chan struct{}), cancelled))
	got := make(chan map[string]QueryResult)
	go func() { got <- readBatch(t, do(s, "POST", "/api/batch", `[{"id":"a","query":"slow"}]`, nil)) }()

	var a Activity
	waitFor(t, "query running", func() bool { a = activity(t, s); return len(a.Running) == 1 })
	if a.Running[0].Query != "slow" || a.Running[0].Waiters != 1 || a.MaxConcurrent != 8 {
		t.Fatalf("activity: %+v", a)
	}

	body, _ := json.Marshal(map[string]string{"key": a.Running[0].Key})
	if rec := do(s, "POST", "/api/activity/cancel", string(body), nil); !strings.Contains(rec.Body.String(), "true") {
		t.Fatalf("cancel: %s", rec.Body)
	}
	<-cancelled
	if r := (<-got)["a"]; r.OK || r.Error == "" {
		t.Fatalf("cancelled query should fail: %+v", r)
	}
	a = activity(t, s)
	if a.Totals.Cancelled != 1 || len(a.Recent) != 1 || a.Recent[0].Outcome != "cancelled" {
		t.Fatalf("after cancel: %+v", a)
	}
}

func TestContextSwitchDropsCache(t *testing.T) {
	var calls atomic.Int32
	var current atomic.Value
	current.Store("one")
	s, err := New(Options{
		Query: func(context.Context, string, string, string, int64) (*sdkquery.Response, error) {
			calls.Add(1)
			return okResponse(map[string]any{"tenant": current.Load()}), nil
		},
		Meta: func() Meta { return Meta{Context: current.Load().(string)} },
		SwitchContext: func(name string) error {
			current.Store(name)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	batch := `[{"id":"a","query":"q","ttl":300}]`
	readBatch(t, do(s, "POST", "/api/batch", batch, nil))
	readBatch(t, do(s, "POST", "/api/batch", batch, nil)) // cached
	if calls.Load() != 1 {
		t.Fatalf("want 1 execution before the switch, got %d", calls.Load())
	}
	if rec := do(s, "POST", "/api/context", `{"name":"two"}`, nil); rec.Code != 200 {
		t.Fatalf("switch: %d %s", rec.Code, rec.Body)
	}
	r := readBatch(t, do(s, "POST", "/api/batch", batch, nil))["a"]
	if calls.Load() != 2 || r.Records[0]["tenant"] != "two" {
		t.Fatalf("after switch: calls=%d result=%+v", calls.Load(), r)
	}
	if !strings.Contains(do(s, "GET", "/api/meta", "", nil).Body.String(), `"context":"two"`) {
		t.Error("meta not refreshed after switch")
	}
}

// The dtctl executor reports a cancelled execution as (nil, nil); that must
// never be cached as an empty successful result.
func TestCancelledExecutionIsNotCached(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{}, 4)
	s := newTestServer(t, func(ctx context.Context, _, _, _ string, _ int64) (*sdkquery.Response, error) {
		if calls.Add(1) == 1 {
			started <- struct{}{}
			<-ctx.Done()
			return nil, nil
		}
		return okResponse(map[string]any{"v": 1}), nil
	})
	ctx, abort := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", "/api/batch", strings.NewReader(`[{"id":"a","query":"q","ttl":300}]`)).WithContext(ctx)
	req.Host = "localhost:7878"
	req.Header.Set("X-Dtctl-Web", "1")
	done := make(chan struct{})
	go func() {
		s.ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()
	<-started
	abort()
	<-done
	waitFor(t, "flight gone", func() bool { a := activity(t, s); return len(a.Running)+len(a.Queued) == 0 })

	r := readBatch(t, do(s, "POST", "/api/batch", `[{"id":"a","query":"q","ttl":300}]`, nil))["a"]
	if !r.OK || r.Cached || len(r.Records) != 1 {
		t.Fatalf("second request should re-execute, got %+v", r)
	}
}
