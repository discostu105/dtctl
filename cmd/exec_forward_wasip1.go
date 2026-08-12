//go:build wasip1

package cmd

import "fmt"

// execForward cannot work on wasip1: there is no process model, so a dtctl-*
// plugin binary can neither be found nor executed. Plugin dispatch is
// structurally unavailable in wasm hosts (which is the desired service-mode
// behavior — see the dtctl-as-a-service design).
func execForward(bin string, argv []string, env []string) (int, error) {
	return 1, fmt.Errorf("plugin execution is not supported on this platform (wasip1): %s", bin)
}
