//go:build !windows

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// forwardToTUI replaces the dtctl process with the dtui binary so the TUI
// owns the terminal end to end (raw mode, signals, exit code) with no parent
// in between. It only returns on failure to exec. The in-flight OTel span is
// intentionally abandoned — interactive TUI sessions are not traced.
func forwardToTUI(bin string, argv []string) error {
	if err := syscall.Exec(bin, append([]string{filepath.Base(bin)}, argv...), os.Environ()); err != nil {
		return fmt.Errorf("failed to launch %s: %w", bin, err)
	}
	return nil
}
