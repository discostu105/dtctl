//go:build !wasip1

package httpclient

import "net/http"

// PlatformDefaultTransport returns nil on platforms with real sockets: the
// resty default transport is used unless WithTransport overrides it.
func PlatformDefaultTransport() http.RoundTripper { return nil }
