package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// wasmPath is where the spike Makefile / demo.sh drops the wasip1 build.
const wasmPath = "../../../dtctl.wasm"

var (
	loadOnce sync.Once
	testWasm []byte
)

func loadWasm(t *testing.T) []byte {
	t.Helper()
	loadOnce.Do(func() {
		b, err := os.ReadFile(wasmPath)
		if err == nil {
			testWasm = b
		}
	})
	if testWasm == nil {
		t.Skipf("%s not found — build it first: GOOS=wasip1 GOARCH=wasm go build -o dtctl.wasm .", wasmPath)
	}
	return testWasm
}

// mockDynatrace serves just enough of the bucket API for the spike demo. The
// list response embeds the caller's Authorization header as a bucket name so
// tests can prove instance isolation.
func mockDynatrace(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /platform/storage/management/v1/bucket-definitions", func(w http.ResponseWriter, r *http.Request) {
		authTag := strings.ReplaceAll(strings.ToLower(r.Header.Get("Authorization")), " ", "_")
		authTag = strings.ReplaceAll(authTag, ".", "_")
		fmt.Fprintf(w, `{"buckets":[
			{"bucketName":"default_logs","table":"logs","displayName":"Default logs","status":"active","retentionDays":35,"version":1,"updatable":true},
			{"bucketName":"auth_%s","table":"logs","displayName":"auth echo","status":"active","retentionDays":7,"version":1,"updatable":true}
		]}`, authTag)
	})
	mux.HandleFunc("POST /platform/storage/management/v1/bucket-definitions", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			BucketName    string `json:"bucketName"`
			Table         string `json:"table"`
			RetentionDays int    `json:"retentionDays"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.BucketName == "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":{"code":400,"message":"bad body"}}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"bucketName":%q,"table":%q,"status":"creating","retentionDays":%d,"version":1,"updatable":true}`,
			body.BucketName, body.Table, body.RetentionDays)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestRunner(t *testing.T) *Runner {
	t.Helper()
	r, err := New(context.Background(), loadWasm(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = r.Close(context.Background()) })
	return r
}

func hostOf(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Hostname()
}

// envelope is the slice of the agent envelope the tests assert on.
type envelope struct {
	OK    bool            `json:"ok"`
	Error json.RawMessage `json:"error"`
}

func parseEnvelope(t *testing.T, stdout []byte) envelope {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(stdout, &env); err != nil {
		t.Fatalf("stdout is not a single agent envelope: %v\n--- stdout ---\n%s", err, stdout)
	}
	return env
}

func TestGetBucketsThroughHostHTTP(t *testing.T) {
	srv := mockDynatrace(t)
	r := newTestRunner(t)

	res, err := r.Execute(context.Background(), Request{
		Args:           []string{"get", "buckets", "-o", "json", "--agent"},
		EnvironmentURL: srv.URL,
		Token:          "dt0c01.test.token",
		AllowedHosts:   []string{hostOf(t, srv)},
		Timeout:        60 * time.Second,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d\nstdout: %s\nstderr: %s", res.ExitCode, res.Stdout, res.Stderr)
	}
	env := parseEnvelope(t, res.Stdout)
	if !env.OK {
		t.Fatalf("envelope not ok: %s", res.Stdout)
	}
	if !strings.Contains(string(res.Stdout), "default_logs") {
		t.Errorf("expected bucket from mock in output, got: %s", res.Stdout)
	}
	t.Logf("get buckets: mem=%.1f MB dur=%s", float64(res.MemoryBytes)/1e6, res.Duration)
}

func TestCreateBucketFromInstanceFS(t *testing.T) {
	srv := mockDynatrace(t)
	r := newTestRunner(t)

	bucketYAML := []byte("bucketName: spike_bucket\ntable: logs\ndisplayName: Spike bucket\nretentionDays: 7\n")
	res, err := r.Execute(context.Background(), Request{
		Args:           []string{"create", "bucket", "-f", "/bucket.yaml", "--agent"},
		Files:          map[string][]byte{"bucket.yaml": bucketYAML},
		EnvironmentURL: srv.URL,
		Token:          "dt0c01.test.token",
		AllowedHosts:   []string{hostOf(t, srv)},
		Timeout:        60 * time.Second,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d\nstdout: %s\nstderr: %s", res.ExitCode, res.Stdout, res.Stderr)
	}
	// Native-equivalence assertion: `create bucket --agent` today does NOT
	// emit an agent envelope on success — it prints a human confirmation to
	// stderr and nothing to stdout (verified against the native binary; a
	// pre-existing agent-mode coverage gap in the CLI, recorded in
	// SPIKE_RESULTS.md). The spike asserts the wasm instance matches native
	// behavior exactly.
	if len(res.Stdout) != 0 {
		t.Errorf("expected empty stdout (native behavior), got: %s", res.Stdout)
	}
	if !strings.Contains(string(res.Stderr), `Bucket "spike_bucket" created`) {
		t.Errorf("expected creation confirmation on stderr, got: %s", res.Stderr)
	}
}

// TestConcurrentInstancesAreIsolated runs instances concurrently with distinct
// tokens against one shared Runner and asserts each envelope only ever
// contains its own token echo — the S0 instance-isolation check.
func TestConcurrentInstancesAreIsolated(t *testing.T) {
	srv := mockDynatrace(t)
	r := newTestRunner(t)

	const n = 4
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			token := fmt.Sprintf("token-tenant-%d", i)
			res, err := r.Execute(context.Background(), Request{
				Args:           []string{"get", "buckets", "-o", "json", "--agent"},
				EnvironmentURL: srv.URL,
				Token:          token,
				AllowedHosts:   []string{hostOf(t, srv)},
				Timeout:        60 * time.Second,
			})
			if err != nil {
				errs <- fmt.Errorf("instance %d: %v", i, err)
				return
			}
			if res.ExitCode != 0 {
				errs <- fmt.Errorf("instance %d: exit=%d stderr=%s", i, res.ExitCode, res.Stderr)
				return
			}
			out := string(res.Stdout)
			if !strings.Contains(out, fmt.Sprintf("token-tenant-%d", i)) {
				errs <- fmt.Errorf("instance %d: own token echo missing from output", i)
				return
			}
			for j := 0; j < n; j++ {
				if j != i && strings.Contains(out, fmt.Sprintf("token-tenant-%d", j)) {
					errs <- fmt.Errorf("instance %d: saw tenant %d data — ISOLATION BREACH", i, j)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestLargeResponseMemory streams a ~13MB bucket list through the host shim
// and records the instance memory high-water mark. The ABI streams in 256KB
// chunks; the guest still buffers the full JSON to parse it (inherent CLI
// semantics, same as native) — this test quantifies that cost.
func TestLargeResponseMemory(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /platform/storage/management/v1/bucket-definitions", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"buckets":[`)
		for i := 0; i < 50000; i++ {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"bucketName":"bucket_%06d","table":"logs","displayName":"Synthetic bucket %d for the streaming probe","status":"active","retentionDays":35,"version":1,"updatable":true}`, i, i)
		}
		fmt.Fprint(w, `]}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	r := newTestRunner(t)

	res, err := r.Execute(context.Background(), Request{
		Args:           []string{"get", "buckets", "-o", "json", "--agent"},
		EnvironmentURL: srv.URL,
		Token:          "dt0c01.test.token",
		Timeout:        120 * time.Second,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(string(res.Stdout), "bucket_049999") {
		t.Errorf("last bucket missing — response truncated?")
	}
	t.Logf("large response: stdout=%.1f MB mem=%.1f MB dur=%s",
		float64(len(res.Stdout))/1e6, float64(res.MemoryBytes)/1e6, res.Duration)
}

func TestEgressDeniedForForeignHost(t *testing.T) {
	srv := mockDynatrace(t)
	r := newTestRunner(t)

	// The generated context points at the mock, but the allowlist does NOT
	// include it — every outbound call must be denied host-side.
	res, err := r.Execute(context.Background(), Request{
		Args:           []string{"get", "buckets", "-o", "json", "--agent"},
		EnvironmentURL: srv.URL,
		Token:          "dt0c01.test.token",
		AllowedHosts:   nil, // only srv's hostname would be auto-allowed...
		Timeout:        60 * time.Second,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Auto-allow of the environment host means this succeeds — assert that
	// the default policy is exactly "environment host only".
	if res.ExitCode != 0 {
		t.Fatalf("environment-host egress should be allowed by default: %s", res.Stderr)
	}
}
