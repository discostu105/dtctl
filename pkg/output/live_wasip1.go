//go:build wasip1

package output

// setupResizeSignal is a no-op on wasip1 (no signals, no terminal)
func (p *LivePrinter) setupResizeSignal() {
	// wasip1 has no SIGWINCH; live output in a wasm host is non-interactive.
}

// stopResizeSignal is a no-op on wasip1
func (p *LivePrinter) stopResizeSignal() {
	// No-op on wasip1
}
