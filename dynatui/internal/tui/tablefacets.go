// The table's facet manager ('f'): a two-stage in-view overlay that adds
// server-side attribute filters — the first stage lists the active
// searches/facets (edit/remove) above attribute candidates derived from
// the fetched records, the value stage picks from the server's
// fieldsSummary.
package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
	"github.com/dynatrace-oss/dynatui/internal/tui/theme"
)

// facetStage is the facet picker's overlay state.
type facetStage int

const (
	facetOff facetStage = iota
	facetFieldStage
	facetValueStage
)

// facetEntry is one selectable row of the manager stage: an active search
// term or facet (edit/remove), or an attribute to add a new facet by.
type facetEntry struct {
	kind facetEntryKind
	idx  int    // index into searches/facets (active kinds)
	attr string // attribute name (entryAttr)
}

type facetEntryKind int

const (
	entrySearch facetEntryKind = iota
	entryFacet
	entryAttr
)

// facetOwner tags the facet picker's fieldsSummary query; field pins the
// result to the attribute it was requested for (two in-flight explorations
// must not cross).
type facetOwner struct {
	v     *tableView
	field string
}

// openFacets opens the manager: active filters (edit/remove) above the
// attribute candidates from the fetched records' keys.
func (v *tableView) openFacets() tea.Cmd {
	if v.spec.API != "" {
		// Facet exploration pairs the fetched records' attributes with
		// fieldsSummary over the view's query — on an API view the records
		// are NOT the query's rows (patterns are analyzer output over a
		// logs pipeline), so interactive faceting would filter on fields
		// the pipeline never carries. Facets inherited from the source
		// list (the 'a' drill) still apply; '/' filters client-side.
		return statusErr("facets need a DQL-backed view — / filters " + v.spec.Name + " client-side")
	}
	v.facetFields = v.facetCandidates()
	if len(v.facetFields) == 0 && len(v.searches)+len(v.facets) == 0 {
		return statusErr("no attributes to facet on (no rows fetched)")
	}
	v.facetMode = facetFieldStage
	// Adding is the common case: start on the first attribute; the active
	// filters sit above, one ↑ away.
	v.facetSel = len(v.searches) + len(v.facets)
	v.facetEdit = -1
	v.facetErr = nil
	v.facetInput.SetValue("")
	v.facetInput.Focus()
	return textinput.Blink
}

// clearFacets drops every server search and facet, refetching unnarrowed.
func (v *tableView) clearFacets() tea.Cmd {
	if len(v.searches) == 0 && len(v.facets) == 0 {
		return status("no active facets")
	}
	v.searches = nil
	v.facets = nil
	return tea.Batch(v.Refresh(), markHistory, status("facets cleared"))
}

// addSearch stacks (or, replacing, swaps in) a server-side search term.
func (v *tableView) addSearch(term string, replace bool) tea.Cmd {
	if replace {
		v.searches = []string{term}
		return tea.Batch(v.Refresh(), markHistory, status(fmt.Sprintf("server search %q — f manages · F clears", term)))
	}
	for _, s := range v.searches {
		if s == term {
			return status(fmt.Sprintf("search %q already active", term))
		}
	}
	v.searches = append(v.searches, term)
	return tea.Batch(v.Refresh(), markHistory, status(fmt.Sprintf("server search %q added — f manages · F clears", term)))
}

// addFacet appends a facet (deduplicated) and refetches — shared by the
// picker's value stage and the inspector's cross-view 'f'.
func (v *tableView) addFacet(f catalog.Facet) tea.Cmd {
	for _, existing := range v.facets {
		if existing == f {
			return status("facet " + f.Label() + " already active")
		}
	}
	v.facets = append(v.facets, f)
	return tea.Batch(v.Refresh(), markHistory, status("facet "+f.Label()+" — f manages · F clears"))
}

// hasField reports whether any fetched record carries the key — the guard
// that keeps a cross-view facet from silently emptying the list.
func (v *tableView) hasField(field string) bool {
	for _, rec := range v.all {
		if _, ok := rec[field]; ok {
			return true
		}
	}
	return false
}

// facetCandidates returns the scalar attribute keys present in the fetched
// records, sorted — maps and arrays can't anchor a value filter, and
// enrichment keys are synthetic.
func (v *tableView) facetCandidates() []string {
	seen := map[string]bool{}
	var fields []string
	for _, rec := range v.all {
		for k, val := range rec {
			if seen[k] || strings.HasPrefix(k, "__enrich.") || k == catalog.BucketField {
				continue
			}
			switch val.(type) {
			case map[string]any, []any:
				continue
			}
			seen[k] = true
			fields = append(fields, k)
		}
	}
	sort.Strings(fields)
	// Buckets are Grail's physical data separation — the primary narrowing
	// axis — so the bucket field leads the candidates on bucket-backed views,
	// rows fetched or not (the value picker explores it server-side).
	if catalog.BucketEligible(v.spec.Query(v.scope)) {
		fields = append([]string{catalog.BucketField}, fields...)
	}
	return fields
}

func (v *tableView) handleFacetKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		if v.facetMode == facetValueStage {
			// Back to the manager, not out of the picker.
			v.facetMode = facetFieldStage
			v.facetSel = len(v.searches) + len(v.facets)
			v.facetEdit = -1
			v.facetErr = nil
			v.facetInput.SetValue("")
			return claimKey
		}
		v.closeFacets()
		return claimKey
	case "up", "ctrl+k":
		v.facetSel = clampSel(v.facetSel-1, v.facetListLen())
	case "down", "ctrl+j", "tab":
		v.facetSel = clampSel(v.facetSel+1, v.facetListLen())
	case "enter":
		if v.facetMode == facetFieldStage {
			entries := v.facetEntries()
			if len(entries) == 0 {
				return nil
			}
			return v.enterFacetEntry(entries[clampSel(v.facetSel, len(entries))])
		}
		return v.applyFacet()
	case "ctrl+x", "delete":
		if v.facetMode == facetFieldStage {
			return v.removeFacetEntry()
		}
		fallthrough
	default:
		var cmd tea.Cmd
		v.facetInput, cmd = v.facetInput.Update(msg)
		if v.facetMode == facetFieldStage {
			// Typing hunts for an attribute — jump the selection past the
			// active filters so enter adds rather than edits.
			v.facetSel = len(v.searches) + len(v.facets)
		} else {
			v.facetSel = 0
		}
		return cmd
	}
	return nil
}

// enterFacetEntry acts on a manager row: attributes explore their values,
// active filters open prefilled for editing.
func (v *tableView) enterFacetEntry(e facetEntry) tea.Cmd {
	switch e.kind {
	case entryAttr:
		v.facetEdit = -1
		return v.openFacetValues(e.attr)
	case entryFacet:
		v.facetEdit = e.idx
		cmd := v.openFacetValues(v.facets[e.idx].Field)
		v.facetInput.SetValue(v.facets[e.idx].Value)
		return cmd
	default: // entrySearch — edit the term in place, no suggestions to fetch
		v.facetMode = facetValueStage
		v.facetField = ""
		v.facetEdit = e.idx
		v.facetOptions = nil
		v.facetErr = nil
		v.facetLoading = false
		v.facetSel = 0
		v.facetInput.SetValue(v.searches[e.idx])
		return nil
	}
}

// removeFacetEntry drops the selected active filter (manager stage).
func (v *tableView) removeFacetEntry() tea.Cmd {
	entries := v.facetEntries()
	if len(entries) == 0 {
		return nil
	}
	e := entries[clampSel(v.facetSel, len(entries))]
	var label string
	switch e.kind {
	case entrySearch:
		label = "⌕" + v.searches[e.idx]
		v.searches = append(v.searches[:e.idx], v.searches[e.idx+1:]...)
	case entryFacet:
		label = v.facets[e.idx].Label()
		v.facets = append(v.facets[:e.idx], v.facets[e.idx+1:]...)
	default:
		return status("nothing to remove — select an active filter above")
	}
	v.facetSel = clampSel(v.facetSel, len(v.facetEntries()))
	return tea.Batch(v.Refresh(), markHistory, status("removed "+label))
}

// facetListLen is the length of whichever list the picker currently shows.
func (v *tableView) facetListLen() int {
	if v.facetMode == facetFieldStage {
		return len(v.facetEntries())
	}
	return len(v.facetValueMatches())
}

// openFacetValues advances to the value stage: the server's top values for
// field (v.facetEdit >= 0 means the chosen value replaces that facet).
func (v *tableView) openFacetValues(field string) tea.Cmd {
	v.facetMode = facetValueStage
	v.facetField = field
	v.facetOptions = nil
	v.facetErr = nil
	v.facetLoading = true
	v.facetSel = 0
	v.facetInput.SetValue("")
	dql := catalog.FieldsSummaryQuery(v.spec.Query(v.scope), field, v.searches, v.facets)
	return v.ds.query(facetOwner{v: v, field: field}, v.seq, dql)
}

// applyFacet commits the value-stage choice: input containing '*' applies as
// a wildcard pattern, a highlighted suggestion applies exactly, and
// free-typed text without suggestions applies as an exact value. In edit
// mode the result replaces the entry it was opened from.
func (v *tableView) applyFacet() tea.Cmd {
	input := strings.TrimSpace(v.facetInput.Value())
	if v.facetField == "" { // editing a search term
		if input == "" {
			return statusErr("empty term — esc goes back, ctrl+x in the manager removes")
		}
		v.searches[v.facetEdit] = input
		v.closeFacets()
		return tea.Batch(v.Refresh(), markHistory, status(fmt.Sprintf("search %q updated", input)))
	}
	matches := v.facetValueMatches()
	var value string
	switch {
	case strings.Contains(input, "*"):
		value = input
	case len(matches) > 0:
		value = matches[clampSel(v.facetSel, len(matches))].Value
	case input != "":
		value = input
	default:
		return nil
	}
	f := catalog.Facet{Field: v.facetField, Value: value}
	if v.facetEdit >= 0 {
		v.facets[v.facetEdit] = f
		v.closeFacets()
		return tea.Batch(v.Refresh(), markHistory, status("facet "+f.Label()+" updated"))
	}
	v.closeFacets()
	return v.addFacet(f)
}

func (v *tableView) closeFacets() {
	v.facetMode = facetOff
	v.facetLoading = false
	v.facetEdit = -1
	v.facetInput.Blur()
	v.facetInput.SetValue("")
}

// facetEntries lists the manager rows: active searches, active facets, then
// the attribute candidates narrowed by the picker input (active filters stay
// pinned — they are few, and removal must not require clearing the input).
func (v *tableView) facetEntries() []facetEntry {
	entries := make([]facetEntry, 0, len(v.searches)+len(v.facets)+len(v.facetFields))
	for i := range v.searches {
		entries = append(entries, facetEntry{kind: entrySearch, idx: i})
	}
	for i := range v.facets {
		entries = append(entries, facetEntry{kind: entryFacet, idx: i})
	}
	for _, f := range v.facetFieldMatches() {
		entries = append(entries, facetEntry{kind: entryAttr, attr: f})
	}
	return entries
}

// facetFieldMatches filters attribute candidates by the picker input.
func (v *tableView) facetFieldMatches() []string {
	needle := strings.ToLower(strings.TrimSpace(v.facetInput.Value()))
	if needle == "" {
		return v.facetFields
	}
	var out []string
	for _, f := range v.facetFields {
		if strings.Contains(strings.ToLower(f), needle) {
			out = append(out, f)
		}
	}
	return out
}

// facetValueMatches filters suggestions by the picker input. Input holding a
// '*' is pattern syntax headed for applyFacet, not a suggestion filter.
func (v *tableView) facetValueMatches() []catalog.FacetValue {
	needle := strings.ToLower(strings.TrimSpace(v.facetInput.Value()))
	if needle == "" || strings.Contains(needle, "*") {
		return v.facetOptions
	}
	var out []catalog.FacetValue
	for _, fv := range v.facetOptions {
		if strings.Contains(strings.ToLower(fv.Value), needle) {
			out = append(out, fv)
		}
	}
	return out
}

// clampSel bounds a selection index against a (possibly shrunken) list.
func clampSel(sel, n int) int {
	if sel >= n {
		sel = n - 1
	}
	if sel < 0 {
		return 0
	}
	return sel
}

// renderFacetPicker renders the overlay: the manager (active filters +
// attribute list), or the value stage for the explored attribute.
func (v *tableView) renderFacetPicker() string {
	const valueW, countW = 44, 10
	var b strings.Builder
	if v.facetMode == facetFieldStage {
		b.WriteString(theme.OverlayTitle.Render("facets — "+v.spec.Name) + "\n\n")
		b.WriteString(" " + v.facetInput.View() + "\n\n")
		entries := v.facetEntries()
		sel := clampSel(v.facetSel, len(entries))
		active := len(v.searches) + len(v.facets)
		if active > 0 {
			b.WriteString(theme.Section("active — enter edits · ctrl+x removes") + "\n")
		}
		v.renderFacetList(&b, len(entries), sel, func(i int) string {
			e := entries[i]
			switch e.kind {
			case entrySearch:
				return pad("⌕ "+v.searches[e.idx], valueW) + " " + cell("search", countW, true)
			case entryFacet:
				return pad(v.facets[e.idx].Label(), valueW) + " " + cell("facet", countW, true)
			default:
				if e.attr == catalog.BucketField {
					// Pinned first on bucket-backed views; the tag says why.
					return pad(e.attr, valueW) + " " + cell("bucket", countW, true)
				}
				return pad(e.attr, valueW+countW+1)
			}
		}, func(i int) string {
			// The attribute block gets its own header once the actives end.
			if i == active && active > 0 {
				return theme.Section("add by attribute")
			}
			return ""
		})
		b.WriteString("\n" + theme.Dim.Render("type to narrow · enter add/edit · esc close"))
		return b.String()
	}

	title := "facet — " + v.facetField
	if v.facetField == "" {
		title = "search — full text (any field)"
	}
	b.WriteString(theme.OverlayTitle.Render(title) + "\n\n")
	b.WriteString(" " + v.facetInput.View() + "\n\n")
	switch {
	case v.facetField == "":
		b.WriteString(theme.Dim.Render(" matches records containing the term in any field") + "\n")
	case v.facetLoading:
		b.WriteString(" " + theme.Spinner.Render(theme.Spin()) + theme.Dim.Render(" exploring top values…") + "\n")
	case v.facetErr != nil:
		b.WriteString(" " + theme.Error.Render("✗ "+wrap(v.facetErr.Error(), valueW+countW)) + "\n")
	default:
		matches := v.facetValueMatches()
		sel := clampSel(v.facetSel, len(matches))
		if len(matches) == 0 {
			b.WriteString(theme.Dim.Render(" no values (type one — enter applies it)") + "\n")
		}
		v.renderFacetList(&b, len(matches), sel, func(i int) string {
			return pad(matches[i].Value, valueW) + " " + cell(matches[i].Count, countW, true)
		}, nil)
	}
	b.WriteString("\n" + theme.Dim.Render("enter apply · payment* / *ayment* patterns · esc back"))
	return b.String()
}

// renderFacetList writes a windowed, selection-highlighted list of n rows;
// header (optional) injects a section line before row i.
func (v *tableView) renderFacetList(b *strings.Builder, n, sel int, row func(i int) string, header func(i int) string) {
	limit := max(v.height-12, 4)
	offset := 0
	if sel >= limit {
		offset = sel - limit + 1
	}
	end := offset + limit
	if end > n {
		end = n
	}
	for i := offset; i < end; i++ {
		if header != nil {
			if h := header(i); h != "" {
				b.WriteString(h + "\n")
			}
		}
		if i == sel {
			b.WriteString(theme.Selected.Render(" " + row(i) + " "))
		} else {
			b.WriteString(" " + row(i) + " ")
		}
		b.WriteString("\n")
	}
	if rest := n - end; rest > 0 {
		b.WriteString(theme.Dim.Render(fmt.Sprintf(" … %d more", rest)) + "\n")
	}
}
