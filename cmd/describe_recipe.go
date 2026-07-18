package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dynatrace-oss/dtctl/pkg/config"
	"github.com/dynatrace-oss/dtctl/pkg/output"
	"github.com/dynatrace-oss/dtctl/pkg/recipes"
)

// describedRecipe is the structured `describe recipe` payload.
type describedRecipe struct {
	Name        string                    `json:"name" yaml:"name"`
	Description string                    `json:"description,omitempty" yaml:"description,omitempty"`
	Source      string                    `json:"source,omitempty" yaml:"source,omitempty"`
	Local       bool                      `json:"local,omitempty" yaml:"local,omitempty"`
	Disabled    *recipes.Disabled         `json:"disabled,omitempty" yaml:"disabled,omitempty"`
	Requires    []string                  `json:"requires,omitempty" yaml:"requires,omitempty"`
	DataObjects []string                  `json:"dataObjects,omitempty" yaml:"dataObjects,omitempty"`
	Params      map[string]*recipes.Param `json:"params,omitempty" yaml:"params,omitempty"`
	DQL         string                    `json:"dql" yaml:"dql"`
	Segments    []string                  `json:"segments,omitempty" yaml:"segments,omitempty"`
	Override    string                    `json:"override,omitempty" yaml:"override,omitempty"`
	LastRun     *recipes.LastRun          `json:"lastRun,omitempty" yaml:"lastRun,omitempty"`
	Note        string                    `json:"note,omitempty" yaml:"note,omitempty"`
	Followups   []string                  `json:"followups,omitempty" yaml:"followups,omitempty"`
}

// describeRecipeCmd shows one recipe in full: DQL, typed params, stamp,
// followups — the only consumer that loads a full recipe (the briefing stays
// a one-line index).
var describeRecipeCmd = &cobra.Command{
	Use:   "recipe <name>",
	Short: "Show a recipe in full: DQL, typed params, stamp, and followups",
	Long: `Show the resolved form of one recipe from the current context's recipe
book (book override > later pack > earlier pack).

Examples:
  dtctl describe recipe pods-restarting
  dtctl describe recipe service-errors -o yaml
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := LoadConfig()
		if err != nil {
			return err
		}
		lib, err := recipes.LoadLibrary(config.ConfigDir(), cfg.CurrentContext)
		if err != nil {
			return err
		}
		res, err := lib.Lookup(args[0])
		if err != nil {
			return err
		}
		r := res.Recipe
		d := &describedRecipe{
			Name:        res.Name,
			Description: r.Description,
			Source:      res.Source,
			Local:       res.Local,
			Disabled:    res.Disabled,
			Requires:    r.Requires,
			DataObjects: r.DataObjects,
			Params:      r.Params,
			DQL:         strings.TrimSpace(r.DQL),
			Segments:    r.Segments,
			Override:    r.Override,
			LastRun:     r.LastRun,
			Note:        r.Note,
			Followups:   r.Followups,
		}

		if outputFormat == "table" {
			printRecipeHuman(d)
			return nil
		}
		printer := NewPrinter()
		if ap := enrichAgent(printer, "describe", "recipe"); ap != nil {
			ap.SetSuggestions(recipeRunSuggestions(d))
		}
		return printer.Print(d)
	},
}

func recipeRunSuggestions(d *describedRecipe) []string {
	var sets []string
	for _, p := range recipes.RequiredParams(&recipes.Recipe{Params: d.Params}) {
		sets = append(sets, " --set "+p+"=<value>")
	}
	return []string{fmt.Sprintf("# execute: dtctl query --recipe %s%s", d.Name, strings.Join(sets, ""))}
}

func printRecipeHuman(d *describedRecipe) {
	const w = 14
	output.DescribeKV("Name:", w, "%s", d.Name)
	if d.Description != "" {
		output.DescribeKV("Description:", w, "%s", d.Description)
	}
	switch {
	case d.Local:
		output.DescribeKV("Source:", w, "local (defined in the recipe book)")
	case d.Source != "":
		output.DescribeKV("Source:", w, "%s", d.Source)
	}
	if d.Disabled != nil {
		output.DescribeKV("Disabled:", w, "[%s] %s", d.Disabled.Class, d.Disabled.Reason)
	}
	if len(d.Requires) > 0 {
		output.DescribeKV("Requires:", w, "%s", strings.Join(d.Requires, ", "))
	}
	if d.Override != "" {
		output.DescribeKV("Override:", w, "%s", d.Override)
	}
	if d.Note != "" {
		output.DescribeKV("Note:", w, "%s", d.Note)
	}
	if lr := d.LastRun; lr != nil {
		stamp := "at " + lr.At
		if lr.Records != nil {
			stamp = fmt.Sprintf("%d records, %.1fs, %s", *lr.Records, lr.Seconds, stamp)
		}
		if lr.LimitHit {
			stamp += " (limit hit)"
		}
		if lr.Empty != "" {
			stamp += " (empty: " + lr.Empty + ")"
		}
		if lr.Partial != "" {
			stamp += " PARTIAL: " + lr.Partial
		}
		output.DescribeKV("Last run:", w, "%s", stamp)
	}
	if len(d.Params) > 0 {
		output.DescribeSection("Params")
		names := make([]string, 0, len(d.Params))
		for n := range d.Params {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			p := d.Params[n]
			typ := p.Type
			if typ == "" {
				typ = "string"
			}
			def := "required"
			if p.Default != nil {
				def = fmt.Sprintf("default %q", *p.Default)
			}
			line := fmt.Sprintf("  %-14s %-11s %s", n, typ, def)
			if p.Description != "" {
				line += "  — " + strings.TrimSpace(p.Description)
			}
			fmt.Println(line)
		}
	}
	output.DescribeSection("DQL")
	fmt.Println(d.DQL)
	if len(d.Followups) > 0 {
		output.DescribeSection("Followups")
		fmt.Println("  " + strings.Join(d.Followups, ", "))
	}
}
