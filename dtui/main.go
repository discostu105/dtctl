// Command dtui is the interactive terminal UI for Dynatrace — a k9s-style
// navigator over observability primitives (see docs/TUI_DESIGN.md).
//
// dtui is a pure consumer of dtctl's configuration: contexts and credentials
// are created and managed with dtctl (`dtctl ctx create`), and dtui reads the
// same config file and keyring through the shared session layer
// (sdk/session; docs/dev/DTUI_SPLIT_DESIGN.md, Decisions 2+3). It never
// writes the shared config; a --context override is session-local. Installed
// as `dtctl-tui`, `dtctl tui` forwards here.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/adrg/xdg"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/dynatrace-oss/dtctl/pkg/aidetect"
	"github.com/dynatrace-oss/dtctl/pkg/exec"
	"github.com/dynatrace-oss/dtctl/pkg/resources/segment"
	"github.com/dynatrace-oss/dtctl/pkg/workspace"
	"github.com/dynatrace-oss/dtctl/sdk/session"

	"github.com/dynatrace-oss/dtui/internal/tui"
	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

// version is stamped by the build (see `make build-dtui`).
var version = "dev"

var (
	cfgFile     string
	contextName string
	noAgent     bool
)

var rootCmd = &cobra.Command{
	Use:     "dtui [view]",
	Version: version,
	Short:   "Interactive terminal UI for Dynatrace",
	Long: `dtui is an interactive terminal UI — a k9s-style navigator over
observability primitives: problems, services, hosts, Kubernetes (pods,
workloads, namespaces, nodes, clusters), traces with a span waterfall,
logs with Davis log-pattern clustering, events, AWS inventory, frontends
and RUM (user sessions and events with Core Web Vitals), business
events, synthetic monitors, databases, GenAI entities, security
vulnerabilities, SLOs with live evaluation, anomaly detectors, the
semantic dictionary (models and fields), a Grail data explorer
(tables, buckets, lookup files with record sampling), and the
smartscape navigator (:nav) — a topology explorer with a type census
and relationship schema, per-type entity browsing, and an ego-centric
walk mode with a breadcrumb trail (X walks from any selected entity).

Views are opened from the command bar (:) by name or alias — arguments
narrow the jump (:pods checkout, :trace <id>). Every drill-down key
(l logs, s traces, m metrics, p problems, v events, x relations,
a log patterns, u sessions) opens the target pre-scoped to the selected
entity and the active timeframe.
'.' pins an entity as the global scope, ctrl+q reveals any view's DQL in
an editable query, and o deep-links the selection into the Dynatrace UI.
H opens the navigation history — every page you visited, kept across
sessions — and restores a page with its full breadcrumb trail.
Press ? inside the TUI for the full key reference.

dtui reads dtctl's contexts and credentials; create and manage them with
dtctl (dtctl ctx create). dtui is read-only and interactive-only.`,
	Example: `  # Launch on the home triage view (default)
  dtui

  # Launch directly into a view (any alias works)
  dtui pods
  dtui traces
  dtui svc

  # Launch against a specific context (session-local, never persisted)
  dtui --context prod`,
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: viewCompletion,
	SilenceUsage:      true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !noAgent {
			if info := aidetect.Detect(); info.Detected {
				return fmt.Errorf("dtui is interactive-only and does not run in AI agent sessions (pass --no-agent if this session was misdetected)")
			}
		}
		if !term.IsTerminal(int(os.Stdout.Fd())) {
			return fmt.Errorf("dtui requires a terminal (stdout is not a TTY)")
		}

		// A committed .dynatrace.yaml supplies workspace defaults; a broken
		// one must never brick dtui — errors degrade to a startup warning.
		ws, wsWarnings := loadWorkspace()

		view := "home"
		if ws != nil && ws.View != "" {
			if isBespokeView(ws.View) || catalog.Lookup(ws.View) != nil {
				view = ws.View
			} else {
				wsWarnings = append(wsWarnings, fmt.Sprintf("unknown workspace view %q — starting on home", ws.View))
			}
		}
		if len(args) == 1 {
			view = args[0]
		}
		// Only an explicit CLI argument can still be unknown here (the
		// workspace view fell back above) — that stays a hard error.
		if !isBespokeView(view) && catalog.Lookup(view) == nil {
			return fmt.Errorf("unknown view %q (available: home, query, nav, %s)", view, strings.Join(catalog.Names(), ", "))
		}

		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		// Context precedence: --context flag > DTCTL_CONTEXT env var >
		// workspace environment match > config current-context. Session-local
		// either way — dtui never writes the shared config.
		if contextOverride() == "" && ws != nil && ws.Environment != "" {
			if name, ok := findContextByEnvironment(cfg, ws.Environment); ok {
				cfg.CurrentContext = name
			} else {
				wsWarnings = append(wsWarnings, fmt.Sprintf("no context matches workspace environment %s — using %s", ws.Environment, cfg.CurrentContext))
			}
		}
		ctxObj, err := cfg.CurrentContextObj()
		if err != nil {
			return err
		}
		// dtui ships its own client identity (DTUI_SPLIT_DESIGN.md Landmine 5):
		// tenant-side request logs must distinguish dtui from dtctl.
		c, err := session.NewClientFromConfig(cfg, session.WithUserAgentProduct("dtui", version))
		if err != nil {
			return err
		}

		opts := tui.Options{
			ContextName:   cfg.CurrentContext,
			Environment:   ctxObj.Environment,
			SafetyLevel:   string(ctxObj.GetEffectiveSafetyLevel()),
			Executor:      newDQLExecutor(cfg, c),
			Sources:       tuiSources(c),
			SegmentSource: segmentSource(segment.NewHandler(c)),
			InitialView:   view,
			HistoryPath:   historyPath(),
		}
		if ws != nil {
			opts.WorkspaceSegments = workspaceSegments(ws)
			opts.InitialTimeframe = ws.Timeframe
		}
		switch {
		case len(wsWarnings) > 0:
			opts.StartupNotice = strings.Join(wsWarnings, "; ")
			opts.StartupNoticeErr = true
		case ws != nil:
			opts.StartupNotice = workspaceNotice(ws)
		}
		return tui.Run(opts)
	},
}

// loadConfig loads the shared dtctl config. The context override (--context
// flag or DTCTL_CONTEXT env var) is applied in memory only — dtui never
// writes the shared config, so an open TUI cannot repoint scripts and agents
// using dtctl on the same machine.
func loadConfig() (*session.Config, error) {
	var cfg *session.Config
	var err error
	if cfgFile != "" {
		cfg, err = session.LoadFrom(cfgFile)
	} else {
		cfg, err = session.Load()
	}
	if err != nil {
		return nil, err
	}
	if o := contextOverride(); o != "" {
		cfg.CurrentContext = o
	}
	return cfg, nil
}

// contextOverride returns the session-local context override: the --context
// flag if given, else the DTCTL_CONTEXT env var (the same override dtctl
// honors, so `DTCTL_CONTEXT=x dtctl ...` and `DTCTL_CONTEXT=x dtui` agree).
// An override also suppresses the workspace environment match.
func contextOverride() string {
	if contextName != "" {
		return contextName
	}
	return os.Getenv("DTCTL_CONTEXT")
}

// newDQLExecutor creates a DQL executor with OAuth token refresh support.
// When the OAuth token expires during a long-running query poll (which can
// exceed the 5-minute token lifetime), the executor automatically fetches a
// fresh token and retries without aborting the query.
func newDQLExecutor(cfg *session.Config, c *session.Client) *exec.DQLExecutor {
	executor := exec.NewDQLExecutor(c)
	if session.IsOAuthStorageAvailable() {
		ctx, err := cfg.CurrentContextObj()
		if err == nil && ctx.TokenRef != "" {
			tokenRef := ctx.TokenRef
			executor = executor.WithTokenRefresher(func() (string, error) {
				return session.GetTokenWithOAuthSupport(cfg, tokenRef)
			})
		}
	}
	return executor
}

// historyPath returns dtui's navigation-history file. UI state lives in
// dtui's own namespace — never in dtctl's — so the first run migrates the
// history written by the pre-split `dtctl tui`.
func historyPath() string {
	path := filepath.Join(xdg.StateHome, "dtui", "history.json")
	if _, err := os.Stat(path); err == nil {
		return path
	}
	old := filepath.Join(session.StateDir(), "tui-history.json")
	if data, err := os.ReadFile(old); err == nil {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
			_ = os.WriteFile(path, data, 0o600)
		}
	}
	return path
}

// loadWorkspace discovers the project's .dynatrace.yaml. Errors degrade to a
// warning and a nil workspace: a broken committed file must never brick dtui.
func loadWorkspace() (*workspace.Workspace, []string) {
	ws, err := workspace.Discover()
	if err != nil {
		return nil, []string{"workspace file ignored: " + err.Error()}
	}
	return ws, nil
}

// workspaceSegments maps the file's segment refs into the TUI's resolution
// input, flattening the variables map into deterministic (sorted) bindings.
func workspaceSegments(ws *workspace.Workspace) []tui.WorkspaceSegment {
	out := make([]tui.WorkspaceSegment, 0, len(ws.Segments))
	for _, s := range ws.Segments {
		ref := tui.WorkspaceSegment{Ref: s.Ref}
		names := make([]string, 0, len(s.Variables))
		for name := range s.Variables {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			ref.Variables = append(ref.Variables, exec.FilterSegmentVariable{Name: name, Values: s.Variables[name]})
		}
		out = append(out, ref)
	}
	return out
}

// workspaceNotice announces that a committed file is steering the session —
// e.g. "workspace my-service: 2 segments, last 24h, view pods".
func workspaceNotice(ws *workspace.Workspace) string {
	var parts []string
	switch n := len(ws.Segments); {
	case n == 1:
		parts = append(parts, "1 segment")
	case n > 1:
		parts = append(parts, fmt.Sprintf("%d segments", n))
	}
	if ws.Timeframe != "" {
		parts = append(parts, "last "+ws.Timeframe)
	}
	if ws.View != "" {
		parts = append(parts, "view "+ws.View)
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("workspace %s: %s", filepath.Base(filepath.Dir(ws.Path())), strings.Join(parts, ", "))
}

// findContextByEnvironment returns the first context whose environment URL
// matches env (case-insensitive; scheme and trailing slash ignored).
func findContextByEnvironment(cfg *session.Config, env string) (string, bool) {
	want := envKey(env)
	if want == "" {
		return "", false
	}
	for _, nc := range cfg.Contexts {
		if envKey(nc.Context.Environment) == want {
			return nc.Name, true
		}
	}
	return "", false
}

// envKey normalizes an environment URL to its comparable identity.
func envKey(env string) string {
	s := strings.ToLower(strings.TrimSpace(env))
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	return strings.TrimSuffix(s, "/")
}

// isBespokeView reports whether the name is one of the TUI's bespoke
// (non-catalog) screens.
func isBespokeView(name string) bool {
	switch name {
	case "home", "query", "dql", "nav", "smartscape", "navigator":
		return true
	}
	return false
}

func viewCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return append([]string{"home", "query", "nav"}, catalog.Names()...), cobra.ShellCompDirectiveNoFileComp
}

func main() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "path to the dtctl config file (default: ~/.config/dtctl/config)")
	rootCmd.PersistentFlags().StringVar(&contextName, "context", "", "context to use for this session (env: DTCTL_CONTEXT; not persisted)")
	rootCmd.PersistentFlags().BoolVar(&noAgent, "no-agent", false, "launch even if an AI agent environment is detected")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
