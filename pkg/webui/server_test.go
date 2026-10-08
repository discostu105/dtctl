package webui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdkquery "github.com/dynatrace-oss/dtctl/sdk/api/query"
)

func newTestServer(t *testing.T, q QueryFunc) *Server {
	t.Helper()
	s, err := New(Options{
		Query: q,
		Meta:  func() Meta { return Meta{Context: "test", Environment: "https://env.example.invalid"} },
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func okResponse(records ...map[string]any) *sdkquery.Response {
	return &sdkquery.Response{State: "SUCCEEDED", Result: &sdkquery.Result{Records: records}}
}

func do(s *Server, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "localhost:7878"
	req.Header.Set("X-Dtctl-Web", "1")
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func readBatch(t *testing.T, rec *httptest.ResponseRecorder) map[string]QueryResult {
	t.Helper()
	out := map[string]QueryResult{}
	sc := bufio.NewScanner(rec.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var r QueryResult
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("bad ndjson line %q: %v", sc.Text(), err)
		}
		out[r.ID] = r
	}
	return out
}

func TestGuards(t *testing.T) {
	s := newTestServer(t, func(context.Context, string, string, string, int64) (*sdkquery.Response, error) {
		return okResponse(), nil
	})

	if rec := do(s, "GET", "/api/meta", "", map[string]string{"X-Dtctl-Web": ""}); rec.Code != http.StatusForbidden {
		t.Errorf("missing CSRF header: got %d, want 403", rec.Code)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "attacker.example.invalid"
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("foreign Host: got %d, want 403", rec.Code)
	}

	for _, h := range []string{"localhost", "localhost:1", "127.0.0.1:7878", "[::1]:7878", "app.localhost:9"} {
		if !isLoopbackHost(h) {
			t.Errorf("isLoopbackHost(%q) = false", h)
		}
	}
	for _, h := range []string{"example.invalid", "10.0.0.1:7878", "localhost.example.invalid"} {
		if isLoopbackHost(h) {
			t.Errorf("isLoopbackHost(%q) = true", h)
		}
	}

	if rec := do(s, "GET", "/api/meta", "", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"context":"test"`) {
		t.Errorf("meta: %d %s", rec.Code, rec.Body.String())
	}
}

func TestBatchStreamsResultsAndErrors(t *testing.T) {
	s := newTestServer(t, func(_ context.Context, q, from, to string, _ int64) (*sdkquery.Response, error) {
		if q == "boom" {
			return nil, errors.New("query failed (PARSE_ERROR): nope")
		}
		if from == "" || to == "" {
			return nil, errors.New("timeframe not resolved")
		}
		return okResponse(map[string]any{"q": q}), nil
	})
	rec := do(s, "POST", "/api/batch", `[{"id":"a","query":"fetch logs","from":"now-2h"},{"id":"b","query":"boom","from":"now-1h"},{"id":"c","query":"  "}]`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	got := readBatch(t, rec)
	if len(got) != 3 {
		t.Fatalf("want 3 results, got %d", len(got))
	}
	if !got["a"].OK || got["a"].Records[0]["q"] != "fetch logs" {
		t.Errorf("a: %+v", got["a"])
	}
	if got["b"].OK || !strings.Contains(got["b"].Error, "PARSE_ERROR") {
		t.Errorf("b should carry the query error: %+v", got["b"])
	}
	if got["c"].OK || got["c"].Error != "empty query" {
		t.Errorf("c: %+v", got["c"])
	}
}

func TestBatchValidation(t *testing.T) {
	s := newTestServer(t, func(context.Context, string, string, string, int64) (*sdkquery.Response, error) {
		return okResponse(), nil
	})
	if rec := do(s, "POST", "/api/batch", `not json`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid json: %d", rec.Code)
	}
	if rec := do(s, "POST", "/api/batch", `[]`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("empty batch: %d", rec.Code)
	}
	big := "[" + strings.TrimSuffix(strings.Repeat(`{"id":"x","query":"q"},`, maxBatch+1), ",") + "]"
	if rec := do(s, "POST", "/api/batch", big, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("oversized batch: %d", rec.Code)
	}
}

func TestCacheAndFresh(t *testing.T) {
	var calls atomic.Int32
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var mu sync.Mutex
	s, err := New(Options{
		Query: func(context.Context, string, string, string, int64) (*sdkquery.Response, error) {
			calls.Add(1)
			return okResponse(map[string]any{"n": 1}), nil
		},
		Now: func() time.Time { mu.Lock(); defer mu.Unlock(); return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	body := `[{"id":"a","query":"fetch logs","from":"now-2h","ttl":30}]`
	r1 := readBatch(t, do(s, "POST", "/api/batch", body, nil))
	r2 := readBatch(t, do(s, "POST", "/api/batch", body, nil))
	if calls.Load() != 1 {
		t.Fatalf("second identical query should be cached; calls=%d", calls.Load())
	}
	if r1["a"].Cached || !r2["a"].Cached {
		t.Errorf("cached flags: first=%v second=%v", r1["a"].Cached, r2["a"].Cached)
	}

	readBatch(t, do(s, "POST", "/api/batch", `[{"id":"a","query":"fetch logs","from":"now-2h","ttl":30,"fresh":true}]`, nil))
	if calls.Load() != 2 {
		t.Errorf("fresh must bypass the cache; calls=%d", calls.Load())
	}

	mu.Lock()
	now = now.Add(31 * time.Second)
	mu.Unlock()
	readBatch(t, do(s, "POST", "/api/batch", body, nil))
	if calls.Load() != 3 {
		t.Errorf("expired entry must re-run; calls=%d", calls.Load())
	}
}

func TestSingleFlight(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	s := newTestServer(t, func(context.Context, string, string, string, int64) (*sdkquery.Response, error) {
		calls.Add(1)
		<-release
		return okResponse(), nil
	})
	// Two identical queries in one batch share a single execution.
	done := make(chan map[string]QueryResult)
	go func() {
		done <- readBatch(t, do(s, "POST", "/api/batch", `[{"id":"a","query":"q"},{"id":"b","query":"q"}]`, nil))
	}()
	time.Sleep(50 * time.Millisecond)
	close(release)
	got := <-done
	if !got["a"].OK || !got["b"].OK {
		t.Fatalf("results: %+v", got)
	}
	if calls.Load() != 1 {
		t.Errorf("want 1 execution, got %d", calls.Load())
	}
}

func TestResolveTimeframe(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		from, to, wantFrom, wantTo string
		err                        bool
	}{
		{"", "", "", "", false},
		{"now-2h", "", "2026-10-08T10:00:00Z", "2026-10-08T12:00:00Z", false},
		{"now-15m", "now", "2026-10-08T11:45:00Z", "2026-10-08T12:00:00Z", false},
		{"now-7d", "now-1d", "2026-10-01T12:00:00Z", "2026-10-07T12:00:00Z", false},
		{"2026-10-08T08:00:00Z", "2026-10-08T09:00:00.5Z", "2026-10-08T08:00:00Z", "2026-10-08T09:00:00.5Z", false},
		{"now-2x", "", "", "", true},
		{"yesterday", "", "", "", true},
	}
	for _, c := range cases {
		f, to, err := resolveTimeframe(c.from, c.to, now)
		if (err != nil) != c.err {
			t.Errorf("%q..%q: err=%v", c.from, c.to, err)
			continue
		}
		if f != c.wantFrom || to != c.wantTo {
			t.Errorf("%q..%q: got %q..%q want %q..%q", c.from, c.to, f, to, c.wantFrom, c.wantTo)
		}
	}
}

func TestStaticSPAFallback(t *testing.T) {
	s := newTestServer(t, func(context.Context, string, string, string, int64) (*sdkquery.Response, error) {
		return okResponse(), nil
	})
	if _, ok := s.static["/index.html"]; !ok {
		t.Skip("web UI assets not built")
	}
	rec := do(s, "GET", "/services/some/deep/route", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<div id=\"root\">") {
		t.Errorf("SPA fallback: %d", rec.Code)
	}
	if rec := do(s, "GET", "/assets/missing.js", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing asset should 404, got %d", rec.Code)
	}
	if rec := do(s, "GET", "/api/nope", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown api route should 404, got %d", rec.Code)
	}
}
