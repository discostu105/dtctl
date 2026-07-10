package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// tuiBinaryNames are the binaries `dtctl tui` forwards to, in lookup order.
// dtctl-tui is the kubectl-style plugin name; dtui is the packaging alias.
var tuiBinaryNames = []string{"dtctl-tui", "dtui"}

// The TUI ships as the separate dtui binary (see docs/dev/DTUI_SPLIT_DESIGN.md);
// this command only forwards to it so the `dtctl tui` muscle memory keeps
// working. Flag parsing is disabled, which in cobra means even root-level
// flags (dtctl --context prod tui) arrive here unparsed — everything is
// passed to dtui verbatim, so dtui owns the flag surface and can grow flags
// without dtctl needing to know them.
var tuiCmd = &cobra.Command{
	Use:   "tui [view]",
	Short: "Launch the interactive terminal UI (forwards to dtui)",
	Long: `Launch the interactive terminal UI — a k9s-style navigator over
observability primitives (problems, services, hosts, Kubernetes, traces,
logs, events, RUM, SLOs, the smartscape navigator, and more).

The TUI ships as a separate binary (dtui). This command finds dtctl-tui or
dtui on PATH and forwards to it; run 'dtui --help' for the full view list
and key reference.

The TUI is read-only and interactive-only: it refuses to start in agent
mode, with --plain, or when stdout is not a terminal.`,
	Example: `  # Launch on the home triage view (default)
  dtctl tui

  # Launch directly into a view (any alias works)
  dtctl tui pods

  # Launch against a specific context (session-local, never persisted)
  dtctl tui --context prod`,
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Help must work even when dtui is not installed.
		for _, a := range args {
			if a == "--help" || a == "-h" {
				return cmd.Help()
			}
		}

		// Fail fast with dtctl's structured errors before handing over the
		// terminal. Agent mode comes from auto-detection (initConfig); a raw
		// --no-agent waives it here and is forwarded so dtui's own detection
		// is waived with it. --plain is refused rather than forwarded — dtui
		// has no such flag, and the refusal message beats "unknown flag".
		if GetAgentMode() && !hasRawFlag(args, "--no-agent") {
			return fmt.Errorf("tui is interactive-only and does not run in agent mode (pass --no-agent if this session was misdetected)")
		}
		if hasRawFlag(args, "--plain") {
			return fmt.Errorf("tui requires an interactive terminal and does not support --plain")
		}
		if !term.IsTerminal(int(os.Stdout.Fd())) {
			return fmt.Errorf("tui requires a terminal (stdout is not a TTY)")
		}

		bin, err := lookupTUIBinary()
		if err != nil {
			return err
		}
		return forwardToTUI(bin, args)
	},
}

// lookupTUIBinary finds the dtui binary on PATH.
func lookupTUIBinary() (string, error) {
	for _, name := range tuiBinaryNames {
		if bin, err := exec.LookPath(name); err == nil {
			return bin, nil
		}
	}
	return "", fmt.Errorf(`the TUI ships as the separate dtui binary, and neither %s was found on PATH

Install it from the dtctl repository:
  make install-dtui

or build it directly:
  cd dtui && go build -o ~/.local/bin/dtui .`, strings.Join(tuiBinaryNames, " nor "))
}

// hasRawFlag reports whether the unparsed args carry the given long flag,
// either as "--flag" or "--flag=value".
func hasRawFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}
