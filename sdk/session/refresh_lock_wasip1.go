//go:build wasip1

package session

// acquireRefreshLock is a no-op on wasip1. The cross-process flock exists to
// serialise OAuth refresh-token rotation between concurrent dtctl processes
// on a shared machine; a wasm instance is single-threaded, has no shared temp
// directory, and in the service deployment never performs OAuth refresh at
// all (tokens are supplied per request by the host).
var acquireRefreshLock = func(environment, tokenName string) (unlock func(), err error) {
	return func() {}, nil
}
