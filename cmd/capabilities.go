package cmd

import "fmt"

// Capabilities enumerates the process-level abilities the host grants the
// command tree. Every path that would spawn a subprocess (or hand the process
// over entirely, as Unix plugin dispatch does via exec) is gated on one of
// these, so an embedding caller — the service engine, `dtctl serve`, tests —
// can make those paths structurally unreachable instead of relying on
// configuration. The CLI binary grants everything; embedded callers grant
// nothing (see docs/dev/SERVICE_ENGINE_DESIGN.md).
type Capabilities struct {
	// PluginDispatch allows unknown commands to exec dtctl-* binaries from
	// PATH (full process replacement on Unix).
	PluginDispatch bool
	// ShellAliases allows shell-form aliases ("!...") to run via sh -c.
	ShellAliases bool
	// ApplyHooks allows configured pre-/post-apply hook commands to run.
	ApplyHooks bool
	// Editor allows `edit` commands to spawn $EDITOR.
	Editor bool
	// BrowserOpen allows `open` to spawn the OS URL handler.
	BrowserOpen bool
}

// AllCapabilities is the CLI default: everything granted.
func AllCapabilities() Capabilities {
	return Capabilities{
		PluginDispatch: true,
		ShellAliases:   true,
		ApplyHooks:     true,
		Editor:         true,
		BrowserOpen:    true,
	}
}

// caps holds the granted set for this process. Defaults to the full CLI set;
// embedded callers override via SetCapabilities before executing commands.
// (Folded into the per-execution Runtime by the E1 refactor.)
var caps = AllCapabilities()

// SetCapabilities replaces the granted capability set. It returns the
// previous set so tests can restore it.
func SetCapabilities(c Capabilities) Capabilities {
	prev := caps
	caps = c
	return prev
}

// CapabilityError reports a feature that is not granted in this environment.
// In agent mode it renders as a structured envelope with code
// "capability_disabled".
type CapabilityError struct {
	// Feature is the human-readable name of the blocked ability.
	Feature string
}

func (e *CapabilityError) Error() string {
	return fmt.Sprintf("%s is not available in this environment", e.Feature)
}
