// Package wasihttp bridges HTTP across the wasm boundary on GOOS=wasip1.
//
// wasip1 has no sockets, so outbound HTTPS cannot work in-guest. Instead, the
// guest serializes each request and hands it to the embedding host through a
// small imported function ABI (wasm module "dtctl_host"); the host performs
// the real network call and streams the response body back chunk-wise. All
// dtctl network traffic already flows through sdk/httpclient, so installing
// this transport there covers the entire CLI.
//
// EXPERIMENTAL: this package exists for the dtctl-as-a-service S0 spike (see
// dtctl-contrib dev/DTCTL_AS_A_SERVICE_DESIGN.md). The ABI is not stable.
//
// ABI (all pointers are guest linear memory; guest is single-threaded, so one
// call is in flight at a time):
//
//	http_do(reqPtr, reqLen) -> handle       // >=0 handle, <0 error
//	http_meta(handle, bufPtr, bufLen) -> n  // response meta JSON, <0 error
//	http_read(handle, bufPtr, bufLen) -> n  // >0 bytes, 0 EOF, <0 error
//	http_close(handle)
//	http_error(bufPtr, bufLen) -> n         // last error message text
//
// The request body is fully buffered before crossing the boundary (dtctl
// request payloads are small JSON/YAML documents); the response body streams.
// Deadlines are enforced host-side via Request.TimeoutMillis — a single-
// threaded guest cannot interrupt a blocking host call, so in-guest context
// cancellation does not fire mid-call. This is a documented spike finding.
package wasihttp

// Request is the JSON payload passed to the host's http_do import.
type Request struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	// Headers preserves order and repeated keys, unlike a map.
	Headers [][2]string `json:"headers,omitempty"`
	// Body is base64-encoded by encoding/json.
	Body []byte `json:"body,omitempty"`
	// TimeoutMillis is the host-enforced deadline for the whole exchange,
	// derived from the guest request context; 0 means the host default.
	TimeoutMillis int64 `json:"timeoutMillis,omitempty"`
}

// ResponseMeta is the JSON payload returned by the host's http_meta import.
// The body is not part of the meta; it streams through http_read.
type ResponseMeta struct {
	Status        int         `json:"status"`
	Proto         string      `json:"proto"`
	Headers       [][2]string `json:"headers,omitempty"`
	ContentLength int64       `json:"contentLength"`
}

// HostModule is the wasm import module name the guest expects.
const HostModule = "dtctl_host"

// MetaBufSize is the guest-side buffer for response meta JSON. The host must
// reject responses whose serialized meta exceeds this (huge header sets).
const MetaBufSize = 64 * 1024
