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
observability primitives: problems, services, hosts, Kubernetes (pods,
workloads, namespaces, nodes, clusters), traces with a span waterfall,
logs, events, AWS inventory, frontends (RUM), databases, GenAI entities,
and security vulnerabilities.

Views are opened from the command bar (:) by name or alias — arguments
narrow the jump (:pods checkout, :trace <id>). Every drill-down key
(l logs, s traces, m metrics, p problems, v events, x relations) opens
the target pre-scoped to the selected entity and the active timeframe.
'.' pins an entity as the global scope, ctrl+q reveals any view's DQL in
an editable query, and o deep-links the selection into the Dynatrace UI.
Press ? inside the TUI for the full key reference.

The TUI is read-only and interactive-only: it refuses to start in agent
mode, with --plain, or when stdout is not a terminal.`,
	Example: `  # Launch on the home triage view (default)
  dtctl tui

  # Launch directly into a view (any alias works)
  dtctl tui pods
  dtctl tui traces
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

		view := "home"
		if len(args) == 1 {
			view = args[0]
		}
		if view != "home" && view != "query" && view != "dql" && catalog.Lookup(view) == nil {
			return fmt.Errorf("unknown view %q (available: home, query, %s)", view, strings.Join(catalog.Names(), ", "))
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
	return append([]string{"home", "query"}, catalog.Names()...), cobra.ShellCompDirectiveNoFileComp
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}
