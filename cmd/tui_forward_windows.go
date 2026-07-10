//go:build windows

package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// forwardToTUI runs the dtui binary as a child with inherited stdio — Windows
// has no process replacement. The child's exit code is propagated verbatim
// via os.Exit so scripts see dtui's code, not a cobra-wrapped error.
func forwardToTUI(bin string, argv []string) error {
	cmd := exec.Command(bin, argv...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		return fmt.Errorf("failed to launch %s: %w", bin, err)
	}
	return nil
}
