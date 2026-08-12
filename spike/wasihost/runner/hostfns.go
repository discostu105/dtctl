package runner

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/dynatrace-oss/dtctl/sdk/wasihttp"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

func randSource() io.Reader { return crand.Reader }

// hostState is the per-execution state behind the dtctl_host import module.
// It travels in the context wazero forwards to host functions, so one shared
// runtime serves concurrent instances without cross-talk.
type hostState struct {
	mu       sync.Mutex
	client   *http.Client
	allowed  map[string]bool // hostnames; empty means "environment host only"
	next     int32
	inflight map[int32]*inflightResponse
	lastErr  string
}

type inflightResponse struct {
	resp   *http.Response
	cancel context.CancelFunc
	meta   []byte
}

func newHostState(envURL string, allowedHosts []string) *hostState {
	allowed := map[string]bool{}
	if u, err := url.Parse(envURL); err == nil && u.Host != "" {
		allowed[strings.ToLower(u.Hostname())] = true
	}
	for _, h := range allowedHosts {
		allowed[strings.ToLower(h)] = true
	}
	return &hostState{
		client: &http.Client{
			// The guest (resty) sets Accept-Encoding and decompresses itself;
			// pass bodies through untouched.
			Transport: &http.Transport{DisableCompression: true},
		},
		allowed:  allowed,
		inflight: map[int32]*inflightResponse{},
	}
}

func (s *hostState) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for h, r := range s.inflight {
		_ = r.resp.Body.Close()
		r.cancel()
		delete(s.inflight, h)
	}
}

func (s *hostState) fail(format string, args ...any) int32 {
	s.mu.Lock()
	s.lastErr = fmt.Sprintf(format, args...)
	s.mu.Unlock()
	return -1
}

type hostStateKey struct{}

func withHostState(ctx context.Context, s *hostState) context.Context {
	return context.WithValue(ctx, hostStateKey{}, s)
}

func stateFrom(ctx context.Context) *hostState {
	s, _ := ctx.Value(hostStateKey{}).(*hostState)
	return s
}

// instantiateHostModule registers the dtctl_host import module. The ABI is
// defined in sdk/wasihttp (types.go); the guest counterpart is transport.go.
func instantiateHostModule(ctx context.Context, rt wazero.Runtime) error {
	_, err := rt.NewHostModuleBuilder(wasihttp.HostModule).
		NewFunctionBuilder().WithFunc(hostHTTPDo).Export("http_do").
		NewFunctionBuilder().WithFunc(hostHTTPMeta).Export("http_meta").
		NewFunctionBuilder().WithFunc(hostHTTPRead).Export("http_read").
		NewFunctionBuilder().WithFunc(hostHTTPClose).Export("http_close").
		NewFunctionBuilder().WithFunc(hostHTTPError).Export("http_error").
		Instantiate(ctx)
	return err
}

func hostHTTPDo(ctx context.Context, mod api.Module, reqPtr, reqLen uint32) int32 {
	s := stateFrom(ctx)
	if s == nil {
		return -1
	}
	raw, ok := mod.Memory().Read(reqPtr, reqLen)
	if !ok {
		return s.fail("http_do: request out of guest memory bounds")
	}
	var hreq wasihttp.Request
	if err := json.Unmarshal(raw, &hreq); err != nil {
		return s.fail("http_do: decoding request: %v", err)
	}

	u, err := url.Parse(hreq.URL)
	if err != nil {
		return s.fail("http_do: invalid URL: %v", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return s.fail("http_do: unsupported scheme %q", u.Scheme)
	}
	if !s.allowed[strings.ToLower(u.Hostname())] {
		return s.fail("http_do: egress to %q denied (not the request's environment)", u.Hostname())
	}

	timeout := 5 * time.Minute
	if hreq.TimeoutMillis > 0 {
		timeout = time.Duration(hreq.TimeoutMillis) * time.Millisecond
	}
	// Detach from the wasm invocation ctx for the request's own deadline, but
	// closeAll() still cancels stragglers at instance teardown.
	reqCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)

	req, err := http.NewRequestWithContext(reqCtx, hreq.Method, hreq.URL, bytes.NewReader(hreq.Body))
	if err != nil {
		cancel()
		return s.fail("http_do: building request: %v", err)
	}
	for _, kv := range hreq.Headers {
		req.Header.Add(kv[0], kv[1])
	}

	resp, err := s.client.Do(req)
	if err != nil {
		cancel()
		return s.fail("http_do: %v", err)
	}

	meta := wasihttp.ResponseMeta{
		Status:        resp.StatusCode,
		Proto:         resp.Proto,
		ContentLength: resp.ContentLength,
	}
	for k, vs := range resp.Header {
		for _, v := range vs {
			meta.Headers = append(meta.Headers, [2]string{k, v})
		}
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil || len(metaJSON) > wasihttp.MetaBufSize {
		_ = resp.Body.Close()
		cancel()
		return s.fail("http_do: response meta unserializable or too large")
	}

	s.mu.Lock()
	s.next++
	handle := s.next
	s.inflight[handle] = &inflightResponse{resp: resp, cancel: cancel, meta: metaJSON}
	s.mu.Unlock()
	return handle
}

func hostHTTPMeta(ctx context.Context, mod api.Module, handle int32, bufPtr, bufLen uint32) int32 {
	s := stateFrom(ctx)
	if s == nil {
		return -1
	}
	s.mu.Lock()
	r := s.inflight[handle]
	s.mu.Unlock()
	if r == nil {
		return s.fail("http_meta: unknown handle %d", handle)
	}
	if uint32(len(r.meta)) > bufLen {
		return s.fail("http_meta: buffer too small (%d < %d)", bufLen, len(r.meta))
	}
	if !mod.Memory().Write(bufPtr, r.meta) {
		return s.fail("http_meta: buffer out of guest memory bounds")
	}
	return int32(len(r.meta))
}

func hostHTTPRead(ctx context.Context, mod api.Module, handle int32, bufPtr, bufLen uint32) int32 {
	s := stateFrom(ctx)
	if s == nil {
		return -1
	}
	s.mu.Lock()
	r := s.inflight[handle]
	s.mu.Unlock()
	if r == nil {
		return s.fail("http_read: unknown handle %d", handle)
	}
	if bufLen == 0 {
		return 0
	}
	// Cap host-side chunk size; the guest loops.
	if bufLen > 256*1024 {
		bufLen = 256 * 1024
	}
	chunk := make([]byte, bufLen)
	n, err := r.resp.Body.Read(chunk)
	if n > 0 {
		if !mod.Memory().Write(bufPtr, chunk[:n]) {
			return s.fail("http_read: buffer out of guest memory bounds")
		}
		return int32(n)
	}
	if err == nil || err == io.EOF {
		return 0
	}
	return s.fail("http_read: %v", err)
}

func hostHTTPClose(ctx context.Context, mod api.Module, handle int32) {
	s := stateFrom(ctx)
	if s == nil {
		return
	}
	s.mu.Lock()
	r := s.inflight[handle]
	delete(s.inflight, handle)
	s.mu.Unlock()
	if r != nil {
		_ = r.resp.Body.Close()
		r.cancel()
	}
}

func hostHTTPError(ctx context.Context, mod api.Module, bufPtr, bufLen uint32) int32 {
	s := stateFrom(ctx)
	if s == nil {
		return -1
	}
	s.mu.Lock()
	msg := s.lastErr
	s.mu.Unlock()
	if msg == "" {
		return 0
	}
	b := []byte(msg)
	if uint32(len(b)) > bufLen {
		b = b[:bufLen]
	}
	if !mod.Memory().Write(bufPtr, b) {
		return -1
	}
	return int32(len(b))
}
