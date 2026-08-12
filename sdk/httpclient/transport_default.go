//go:build !wasip1

package httpclient

import "net/http"

// platformDefaultTransport returns nil on platforms with real sockets: the
// resty default transport is used unless WithTransport overrides it.
func platformDefaultTransport() http.RoundTripper { return nil }
