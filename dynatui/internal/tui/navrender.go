// Rendering: the context and trail lines, the row list, and the right
// pane (schema browser or entity preview).
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
	"github.com/dynatrace-oss/dynatui/internal/tui/theme"
)

// --- rendering ------------------------------------------------------------------

func (v *navView) listHeight() int {
	h := v.height - 1 // context/trail line
	if v.filterShown() {
		h--
	}
	return maxInt(h, 1)
}

func (v *navView) filterShown() bool { return v.filterActive || v.filter != "" }

func (v *navView) View(width, height int) string {
	v.width, v.height = width, height
	var b strings.Builder
	b.WriteString(ansi.Truncate(v.contextLine(width), width, "…") + "\n")
	if v.filterShown() {
		b.WriteString(ansi.Truncate(" "+v.filterInput.View(), width, "…") + "\n")
	}
	listH := v.listHeight()

	leftW := width
	var right []string
	rightW := 0
	if v.rightPaneVisible() {
		rightW = width * 2 / 5
		if rightW > 46 {
			rightW = 46
		}
		leftW = width - rightW - 3
		right = v.renderRight(rightW, listH)
	}
	left := v.renderList(leftW, listH)

	if right == nil {
		b.WriteString(strings.Join(left, "\n"))
		return b.String()
	}
	sep := theme.Rule.Render("│")
	for i := 0; i < listH; i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		b.WriteString(pad(l, leftW) + " " + sep + " " + ansi.Truncate(r, rightW, "…"))
		if i < listH-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// contextLine is the top line: a summary on the overview and browser, the
// walk trail in walk mode.
func (v *navView) contextLine(width int) string {
	switch v.mode {
	case navWalk:
		return v.trailLine(width)
	case navBrowser:
		title, unit := v.typ, "entities"
		if v.search != "" {
			title, unit = fmt.Sprintf("matches for %q", v.search), "matches"
		}
		line := " " + theme.OverlayTitle.Render(title)
		if !v.loading && v.err == nil {
			line += theme.Dim.Render(fmt.Sprintf("  %d %s", len(v.instances), unit))
			if v.probLoaded && v.search == "" {
				if n := v.probByType[v.typ]; n > 0 {
					line += theme.Error.Render(fmt.Sprintf("  ● %d with problems", n))
				}
			}
		}
		return line
	}
	line := " " + theme.OverlayTitle.Render("smartscape")
	if !v.loading && v.err == nil {
		total := 0
		for _, rec := range v.census {
			total += catalog.IntValue(rec["count"])
		}
		line += theme.Dim.Render(fmt.Sprintf("  %d types · %d entities", len(v.census), total))
		if v.probLoaded {
			active := map[string]bool{}
			for _, probs := range v.probByID {
				for _, p := range probs {
					active[catalog.Str(p, "display_id")] = true
				}
			}
			if len(active) > 0 {
				line += theme.Error.Render("  ⚠ " + plural(len(active), "active problem"))
			}
		}
	}
	return line
}

// trailLine renders the walk path, eliding the oldest hops when the line
// overflows — the current root and its immediate history matter most.
func (v *navView) trailLine(width int) string {
	sep := theme.CrumbDim.Render(" ▸ ")
	render := func(skip int) string {
		parts := []string{}
		if skip > 0 {
			parts = append(parts, theme.CrumbDim.Render(fmt.Sprintf("… +%d", skip)))
		}
		for _, e := range v.trail[skip:] {
			parts = append(parts, theme.CrumbDim.Render(entityName(e)))
		}
		parts = append(parts, theme.Crumb.Render(entityName(v.root)))
		return " " + strings.Join(parts, sep)
	}
	for skip := 0; skip < len(v.trail); skip++ {
		if line := render(skip); lipgloss.Width(line) <= width {
			return line
		}
	}
	return render(len(v.trail))
}

func (v *navView) renderList(w, h int) []string {
	switch {
	case v.loading:
		return []string{" " + theme.Spinner.Render(theme.Spin()+" "+v.loadingLabel())}
	case v.err != nil:
		return strings.Split(theme.Error.Render("✗ "+wrap(v.err.Error(), maxInt(w-2, 8))), "\n")
	}
	if v.emptyMessage() != "" && len(v.rows) <= v.emptyThreshold() {
		return []string{"", lipgloss.PlaceHorizontal(w, lipgloss.Center, theme.Dim.Render(v.emptyMessage()))}
	}
	end := v.offset + h
	if end > len(v.rows) {
		end = len(v.rows)
	}
	lines := make([]string, 0, end-v.offset)
	for i := v.offset; i < end; i++ {
		lines = append(lines, v.renderRow(v.rows[i], i == v.cursor, w))
	}
	return lines
}

func (v *navView) loadingLabel() string {
	switch v.mode {
	case navBrowser:
		if v.search != "" {
			return fmt.Sprintf("resolving %q…", v.search)
		}
		return "listing " + v.typ + "…"
	case navWalk:
		return "walking topology…"
	}
	return "surveying topology…"
}

// emptyMessage / emptyThreshold: a walk view always carries the root row, so
// "empty" means one row there.
func (v *navView) emptyThreshold() int {
	if v.mode == navWalk {
		return 1
	}
	return 0
}

func (v *navView) emptyMessage() string {
	if v.filter != "" {
		return "∅ nothing matches the filter"
	}
	switch v.mode {
	case navOverview:
		return "∅ no smartscape entities"
	case navBrowser:
		if v.search != "" {
			return fmt.Sprintf("∅ no entity named %q", v.search)
		}
		return "∅ no " + v.typ + " entities"
	}
	if v.dirFilter != navDirBoth || v.structOnly {
		return "∅ no edges pass the direction/mesh toggles (i / M reset)"
	}
	return "∅ no Smartscape edges for this entity"
}

func (v *navView) renderRow(row navRow, selected bool, w int) string {
	plain, styled := v.rowText(row, w)
	if selected {
		return theme.Gutter.Render("▌") + theme.Selected.Render(pad(plain, w-1))
	}
	return ansi.Truncate(" "+styled, w, "…")
}

// rowText renders a row twice: plain for the selected line (its row style
// paints the whole line) and styled for everything else.
func (v *navView) rowText(row navRow, w int) (plain, styled string) {
	switch row.kind {
	case navRowType:
		typ := catalog.Str(row.rec, "type")
		count := catalog.IntValue(row.rec["count"])
		probs := ""
		if v.probLoaded && v.probByType[typ] > 0 {
			probs = fmt.Sprintf("● %d", v.probByType[typ])
		}
		typW := maxInt(w-16, 12)
		plain = pad(typ, typW) + cell(fmt.Sprintf("%d", count), 7, true) + "  " + probs
		styled = pad(typ, typW) + theme.Number.Render(cell(fmt.Sprintf("%d", count), 7, true)) + "  " + theme.Error.Render(probs)
		return plain, styled

	case navRowInstance:
		e := navInstanceEntity(row.rec)
		if e == nil {
			return "", ""
		}
		dot, dotStyled := v.probDot(e.ID)
		name := entityName(*e)
		plain = dot + " " + name
		styled = dotStyled + " " + name
		if v.search != "" {
			// Name matches span types — the type disambiguates the list.
			typW := 22
			plain = dot + " " + pad(e.Type, typW) + " " + name
			styled = dotStyled + " " + theme.Dim.Render(pad(e.Type, typW)) + " " + name
		}
		if e.Name != "" {
			plain += "  " + e.ID
			styled += theme.Dim.Render("  " + e.ID)
		}
		return plain, styled

	case navRowRoot:
		dot, dotStyled := v.probDot(v.root.ID)
		rels := ""
		if !v.loading && v.err == nil {
			rels = fmt.Sprintf(" · %d relations", len(v.edges))
			// The edge query is capped; a full page means the count is a floor,
			// not a total — never let truncation read as completeness.
			if len(v.edges) >= catalog.EdgeQueryLimit {
				rels = fmt.Sprintf(" · %d+ relations (edge limit)", len(v.edges))
			}
		}
		plain = dot + " " + entityName(v.root) + "  " + v.root.Type + rels
		styled = dotStyled + " " + theme.OverlayTitle.Render(entityName(v.root)) +
			"  " + theme.Badge.Render(v.root.Type) + theme.Dim.Render(rels)
		return plain, styled

	case navRowGroup:
		fold := "▾"
		if v.collapsed[row.key] {
			fold = "▸"
		}
		verb := strings.ReplaceAll(row.key.verb, "_", " ")
		if row.key.out {
			label := fmt.Sprintf("%s → (%d)", verb, row.count)
			return fold + " " + label, fold + " " + theme.ArrowOut.Render(label)
		}
		label := fmt.Sprintf("← %s (%d)", verb, row.count)
		return fold + " " + label, fold + " " + theme.ArrowIn.Render(label)

	case navRowNeighbor:
		dot, dotStyled := v.probDot(row.edge.OtherID)
		name := v.names[row.edge.OtherID]
		nameStyled := name
		if name == "" {
			name = row.edge.OtherID
			nameStyled = theme.Dim.Render(name) // nodeless or unresolved: raw id
		}
		typW := 22
		plain = "   " + dot + " " + pad(row.edge.OtherType, typW) + " " + name
		styled = "   " + dotStyled + " " + theme.Dim.Render(pad(row.edge.OtherType, typW)) + " " + nameStyled
		return plain, styled

	case navRowMore:
		label := fmt.Sprintf("     … +%d more (enter shows all)", row.count)
		return label, theme.Dim.Render(label)
	}
	return "", ""
}

// plural renders "1 active problem" / "3 active problems".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// probDot renders a node's health dot: red with active problems, quiet dot
// otherwise, blank while the overlay hasn't loaded.
func (v *navView) probDot(id string) (plain, styled string) {
	if !v.probLoaded {
		return " ", " "
	}
	if len(v.probByID[id]) > 0 {
		return "●", theme.Error.Render("●")
	}
	return "·", theme.Dim.Render("·")
}

// renderRight renders the right pane: the schema neighborhood on the
// overview, the node preview elsewhere.
func (v *navView) renderRight(w, h int) []string {
	var lines []string
	if v.mode == navOverview {
		lines = v.schemaPane(w)
	} else {
		lines = v.previewPane(w)
	}
	if len(lines) > h {
		rest := len(lines) - h + 1
		lines = append(lines[:h-1], theme.Dim.Render(fmt.Sprintf("… +%d more", rest)))
	}
	return lines
}

// schemaPane shows the highlighted type's relationship schema: which verbs
// connect it to which peer types, with edge counts.
func (v *navView) schemaPane(w int) []string {
	row := v.selectedRow()
	if row == nil || row.kind != navRowType {
		return []string{theme.Dim.Render("select a type")}
	}
	typ := catalog.Str(row.rec, "type")
	lines := []string{
		theme.OverlayTitle.Render(typ),
		theme.Dim.Render(fmt.Sprintf("%d entities", catalog.IntValue(row.rec["count"]))) + v.typeProblemSuffix(typ),
	}
	switch {
	case v.schemaLoading:
		return append(lines, "", theme.Spinner.Render(theme.Spin()+" aggregating schema…"))
	case v.schemaErr != nil:
		return append(lines, "", theme.Dim.Render("schema unavailable: "+v.schemaErr.Error()))
	}
	var out, in []string
	for _, e := range v.schema {
		verb := strings.ReplaceAll(e.Verb, "_", " ")
		if e.SourceType == typ {
			out = append(out, "  "+theme.ArrowOut.Render(verb+" ▸")+" "+pad(e.TargetType, maxInt(w-len(verb)-14, 8))+theme.Number.Render(fmt.Sprintf("%6d", e.Count)))
		}
		if e.TargetType == typ {
			in = append(in, "  "+theme.ArrowIn.Render(verb+" ◂")+" "+pad(e.SourceType, maxInt(w-len(verb)-14, 8))+theme.Number.Render(fmt.Sprintf("%6d", e.Count)))
		}
	}
	if len(out) == 0 && len(in) == 0 {
		return append(lines, "", theme.Dim.Render("no edges touch this type"))
	}
	if len(out) > 0 {
		lines = append(lines, "", theme.Section("outgoing"))
		lines = append(lines, out...)
	}
	if len(in) > 0 {
		lines = append(lines, "", theme.Section("incoming"))
		lines = append(lines, in...)
	}
	return lines
}

func (v *navView) typeProblemSuffix(typ string) string {
	if v.probLoaded && v.probByType[typ] > 0 {
		return theme.Error.Render(fmt.Sprintf(" · ● %d problems", v.probByType[typ]))
	}
	return ""
}

// previewPane shows the highlighted node without committing a hop: identity,
// health from the overlay, and the curated key facts from the (debounced,
// cached) detail fetch.
func (v *navView) previewPane(w int) []string {
	_, e := v.Selection()
	if e == nil && v.mode == navWalk {
		root := v.root
		e = &root // group/more rows: preview the center
	}
	if e == nil {
		return []string{theme.Dim.Render("select a node")}
	}
	lines := []string{
		theme.OverlayTitle.Render(entityName(*e)),
		theme.Badge.Render(e.Type) + theme.Dim.Render(" "+e.ID),
		"",
	}
	// Health, from the shared overlay — no extra query.
	switch {
	case !v.probLoaded:
		lines = append(lines, theme.Dim.Render("… problems"))
	case len(v.probByID[e.ID]) > 0:
		probs := v.probByID[e.ID]
		lines = append(lines, theme.Error.Render("⚠ "+plural(len(probs), "active problem")))
		for i, p := range probs {
			if i == 3 {
				lines = append(lines, theme.Dim.Render(fmt.Sprintf("  … +%d more", len(probs)-3)))
				break
			}
			lines = append(lines, "  "+theme.Error.Render(catalog.Str(p, "display_id"))+" "+
				ansi.Truncate(catalog.Str(p, "event.name"), maxInt(w-12, 8), "…"))
		}
	default:
		lines = append(lines, theme.Dim.Render("no active problems (24h)"))
	}
	lines = append(lines, "", theme.Section("key facts"))
	rec, fetched := v.detail[e.ID]
	switch {
	case !fetched:
		lines = append(lines, theme.Dim.Render("  "+theme.Spin()+" fetching…"))
	case len(rec) == 0:
		lines = append(lines, theme.Dim.Render("  ∅ no node record (edge-only endpoint)"))
	default:
		shown := 0
		for _, fact := range catalog.KeyFacts(e.Type) {
			val := fact.Value(rec)
			if val == "" {
				continue
			}
			text := ansi.Truncate(val, maxInt(w-len(fact.Label)-5, 8), "…")
			if fact.Class != nil {
				if class := fact.Class(val); class != "" {
					text = theme.Class(class, text)
				}
			}
			lines = append(lines, "  "+theme.FactLabel.Render(fact.Label+":")+" "+text)
			shown++
		}
		if shown == 0 {
			lines = append(lines, theme.Dim.Render("  (no curated facts — d for the full record)"))
		}
	}
	return lines
}
