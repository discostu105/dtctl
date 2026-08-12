//go:build wasip1

package wasihttp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"time"
	"unsafe"
)

//go:wasmimport dtctl_host http_do
//go:noescape
func hostHTTPDo(reqPtr unsafe.Pointer, reqLen uint32) int32

//go:wasmimport dtctl_host http_meta
//go:noescape
func hostHTTPMeta(handle int32, bufPtr unsafe.Pointer, bufLen uint32) int32

//go:wasmimport dtctl_host http_read
//go:noescape
func hostHTTPRead(handle int32, bufPtr unsafe.Pointer, bufLen uint32) int32

//go:wasmimport dtctl_host http_close
func hostHTTPClose(handle int32)

//go:wasmimport dtctl_host http_error
//go:noescape
func hostHTTPError(bufPtr unsafe.Pointer, bufLen uint32) int32

// lastHostError fetches the host's most recent error message. The guest is
// single-threaded, so "most recent" is always ours.
func lastHostError() error {
	buf := make([]byte, 4096)
	n := hostHTTPError(unsafe.Pointer(&buf[0]), uint32(len(buf)))
	runtime.KeepAlive(buf)
	if n <= 0 {
		return fmt.Errorf("wasihttp: unknown host error")
	}
	return fmt.Errorf("wasihttp: %s", string(buf[:n]))
}

// Transport is an http.RoundTripper that delegates to the embedding host.
type Transport struct{}

// NewTransport returns the host-delegating transport.
func NewTransport() *Transport { return &Transport{} }

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Buffer the request body (spike limitation: dtctl request payloads are
	// small; responses — the large direction — stream).
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("wasihttp: buffering request body: %w", err)
		}
		body = b
	}

	hreq := Request{
		Method: req.Method,
		URL:    req.URL.String(),
		Body:   body,
	}
	for k, vs := range req.Header {
		for _, v := range vs {
			hreq.Headers = append(hreq.Headers, [2]string{k, v})
		}
	}
	if dl, ok := req.Context().Deadline(); ok {
		if ms := time.Until(dl).Milliseconds(); ms > 0 {
			hreq.TimeoutMillis = ms
		} else {
			hreq.TimeoutMillis = 1 // already expired; let the host fail fast
		}
	}

	payload, err := json.Marshal(hreq)
	if err != nil {
		return nil, fmt.Errorf("wasihttp: marshaling request: %w", err)
	}

	handle := hostHTTPDo(unsafe.Pointer(&payload[0]), uint32(len(payload)))
	runtime.KeepAlive(payload)
	if handle < 0 {
		return nil, lastHostError()
	}

	metaBuf := make([]byte, MetaBufSize)
	n := hostHTTPMeta(handle, unsafe.Pointer(&metaBuf[0]), uint32(len(metaBuf)))
	runtime.KeepAlive(metaBuf)
	if n < 0 {
		hostHTTPClose(handle)
		return nil, lastHostError()
	}
	var meta ResponseMeta
	if err := json.Unmarshal(metaBuf[:n], &meta); err != nil {
		hostHTTPClose(handle)
		return nil, fmt.Errorf("wasihttp: decoding response meta: %w", err)
	}

	header := make(http.Header, len(meta.Headers))
	for _, kv := range meta.Headers {
		header.Add(kv[0], kv[1])
	}

	return &http.Response{
		Status:        fmt.Sprintf("%d %s", meta.Status, http.StatusText(meta.Status)),
		StatusCode:    meta.Status,
		Proto:         meta.Proto,
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		ContentLength: meta.ContentLength,
		Body:          &hostBody{handle: handle},
		Request:       req,
	}, nil
}

// hostBody streams the response body from the host chunk-wise.
type hostBody struct {
	handle int32
	closed bool
}

func (b *hostBody) Read(p []byte) (int, error) {
	if b.closed {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	n := hostHTTPRead(b.handle, unsafe.Pointer(&p[0]), uint32(len(p)))
	runtime.KeepAlive(p)
	if n == 0 {
		return 0, io.EOF
	}
	if n < 0 {
		return 0, lastHostError()
	}
	return int(n), nil
}

func (b *hostBody) Close() error {
	if !b.closed {
		b.closed = true
		hostHTTPClose(b.handle)
	}
	return nil
}
