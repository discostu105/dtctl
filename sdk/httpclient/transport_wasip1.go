//go:build wasip1

package httpclient

import (
	"net/http"

	"github.com/dynatrace-oss/dtctl/sdk/wasihttp"
)

// platformDefaultTransport routes all HTTP through the embedding host on
// wasip1, where the guest has no sockets. See sdk/wasihttp.
func platformDefaultTransport() http.RoundTripper { return wasihttp.NewTransport() }
