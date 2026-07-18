package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dynatrace-oss/dtctl/pkg/config"
	"github.com/dynatrace-oss/dtctl/pkg/output"
	"github.com/dynatrace-oss/dtctl/pkg/recipes"
)

// resolveCmd exposes environment knowledge as callables (scoping first).
var resolveCmd = &cobra.Command{
	Use:   "resolve",
	Short: "Resolve environment knowledge into usable expressions",
	Long: `Resolve environment knowledge from the recipe book into expressions you
can paste into queries. See 'dtctl resolve scope --help'.`,
	RunE: requireSubcommand,
}

// resolveScopeCmd prints the filter expression that references an entity in
// one signal, using the recipe book's scoping rules (including topology-hop
// widening — the mechanics agents otherwise re-derive by trial and error).
var resolveScopeCmd = &cobra.Command{
	Use:   "scope <entity-id-or-name>",
	Short: "Print the filter expression that references an entity in a signal",
	Long: `Resolve how to reference a smartscape entity in one signal (logs, spans,
problems) on THIS environment, using the recipe book's measured scoping
rules. Where direct entity stamping is partial, the filter is widened via
the topology (e.g. a service's pods) so results are complete.

A display name that matches several entities (e.g. one service deployed per
environment) resolves to a combined filter over all instances.

Examples:
  dtctl resolve scope SERVICE-ABC123 --for logs
  dtctl resolve scope my-backend --for logs
  dtctl resolve scope SERVICE-AAA SERVICE-BBB SERVICE-CCC --for logs
  dtctl query --recipe entity-logs --set scope="$(dtctl resolve scope my-backend --for logs)"
`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		signal, _ := cmd.Flags().GetString("for")
		if signal == "" {
			return fmt.Errorf("--for <signal> is required (e.g. --for logs)")
		}

		cfg, c, err := SetupClient()
		if err != nil {
			return err
		}
		lib, err := recipes.LoadLibrary(config.ConfigDir(), cfg.CurrentContext)
		if err != nil {
			return err
		}

		executor := NewDQLExecutorFromConfig(cfg, c)
		runner := &discoverRunner{executor: executor, scanLimitGB: 5}
		res, err := resolveScopeMulti(runner, lib, args, signal)
		if err != nil {
			return err
		}

		if outputFormat == "table" && !agentMode {
			// The bare filter goes to stdout so it is directly substitutable
			// (--set scope="$(dtctl resolve scope ...)"); context goes to stderr.
			fmt.Println(res.Filter)
			fmt.Fprintf(os.Stderr, "strategy: %s, entities: %d", res.Strategy, len(res.Entities))
			if res.Coverage != nil {
				fmt.Fprintf(os.Stderr, ", coverage: %.2f", *res.Coverage)
			}
			fmt.Fprintln(os.Stderr)
			for _, n := range res.Notes {
				output.PrintWarning("%s", n)
			}
			return nil
		}
		printer := NewPrinter()
		if ap := enrichAgent(printer, "resolve", "scope"); ap != nil {
			ap.SetSuggestions([]string{
				fmt.Sprintf("# use it: dtctl query --recipe entity-%s --set scope='%s'", signal, res.Filter),
				fmt.Sprintf("# or inline: dtctl query 'fetch %s, from:now()-2h | filter %s | limit 100'", signal, res.Filter),
			})
			ap.SetWarnings(res.Notes)
		}
		return printer.Print(res)
	},
}

// resolveScopeMulti resolves each argument and OR-combines the filters, so a
// set of entities (e.g. every instance of a multi-deployed service) costs one
// call instead of one per entity. Per-entity failures become notes as long as
// at least one entity resolves; only total failure is an error.
func resolveScopeMulti(runner recipes.Runner, lib *recipes.Library, ids []string, signal string) (*recipes.ScopeResult, error) {
	var merged *recipes.ScopeResult
	var filters, failures []string
	for _, id := range ids {
		res, err := recipes.ResolveScope(context.Background(), runner, lib.Book, id, signal)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		filters = append(filters, res.Filter)
		if merged == nil {
			merged = res
			continue
		}
		merged.Entities = append(merged.Entities, res.Entities...)
		merged.Targets = append(merged.Targets, res.Targets...)
		merged.Notes = append(merged.Notes, res.Notes...)
		if merged.Strategy != res.Strategy {
			merged.Strategy = "mixed"
		}
		if res.Coverage == nil || (merged.Coverage != nil && *res.Coverage < *merged.Coverage) {
			merged.Coverage = res.Coverage
		}
	}
	if merged == nil {
		return nil, fmt.Errorf("no scope resolved: %s", strings.Join(failures, "; "))
	}
	if len(filters) > 1 {
		for i, f := range filters {
			filters[i] = "(" + f + ")"
		}
		merged.Filter = strings.Join(filters, " or ")
	}
	merged.Notes = append(merged.Notes, failures...)
	merged.Notes = dedupeStrings(merged.Notes)
	return merged, nil
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := in[:0]
	for _, s := range in {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

func init() {
	rootCmd.AddCommand(resolveCmd)
	resolveCmd.AddCommand(resolveScopeCmd)
	resolveScopeCmd.Flags().String("for", "", "target signal: logs, spans, or problems (required)")
	_ = resolveScopeCmd.MarkFlagRequired("for")
}
