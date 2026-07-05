package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/dynatrace-oss/dtctl/pkg/tui"
	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
)

var tuiCmd = &cobra.Command{
	Use:   "tui [view]",
	Short: "Launch the interactive terminal UI",
	Long: `Launch the interactive terminal UI — a k9s-style navigator over
observability primitives (problems, services, hosts, logs, events).

Views are opened from the command bar (:) by name or alias, and every
drill-down key (l logs, m metrics, p problems, v events) opens the target
view pre-scoped to the selected entity and the active timeframe. Press ?
inside the TUI for the full key reference.

The TUI is read-only and interactive-only: it refuses to start in agent
mode, with --plain, or when stdout is not a terminal.`,
	Example: `  # Launch on the problems view (default)
  dtctl tui

  # Launch directly into a view (any alias works)
  dtctl tui hosts
  dtctl tui svc

  # Launch against a specific context
  dtctl tui --context prod`,
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: tuiViewCompletion,
	RunE: func(cmd *cobra.Command, args []string) error {
		if GetAgentMode() {
			return fmt.Errorf("tui is interactive-only and does not run in agent mode (pass --no-agent if this session was misdetected)")
		}
		if GetPlainMode() {
			return fmt.Errorf("tui requires an interactive terminal and does not support --plain")
		}
		if !term.IsTerminal(int(os.Stdout.Fd())) {
			return fmt.Errorf("tui requires a terminal (stdout is not a TTY)")
		}

		view := "problems"
		if len(args) == 1 {
			view = args[0]
		}
		if catalog.Lookup(view) == nil {
			return fmt.Errorf("unknown view %q (available: %s)", view, strings.Join(catalog.Names(), ", "))
		}

		cfg, c, err := SetupClient()
		if err != nil {
			return err
		}
		ctxObj, err := cfg.CurrentContextObj()
		if err != nil {
			return err
		}

		return tui.Run(tui.Options{
			ContextName: cfg.CurrentContext,
			Environment: ctxObj.Environment,
			SafetyLevel: string(ctxObj.GetEffectiveSafetyLevel()),
			Executor:    NewDQLExecutorFromConfig(cfg, c),
			InitialView: view,
		})
	},
}

func tuiViewCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return catalog.Names(), cobra.ShellCompDirectiveNoFileComp
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}
