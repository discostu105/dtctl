package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtctl/pkg/exec"
	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
	"github.com/dynatrace-oss/dtui/internal/tui/theme"
)

// Grail filter segments are the fourth global state (after context,
// timeframe, and the pin): reusable tenant-defined filters applied
// out-of-band to every DQL query the app runs. The selection lives on the
// shared dataSource — the one place all queries are built — so views need no
// per-view plumbing; a change just refetches them. API-backed views (REST,
// not query:execute) are the one surface segments can't reach; the header
// pill dims there and jumpTo says so.

// maxSegments mirrors the Grail limit of filter segments per query.
const maxSegments = 10

// SegmentOption is one selectable filter segment (a picker row).
type SegmentOption struct {
	UID         string
	Name        string
	Description string
	// HasVariables marks segments that define variables: queries fail unless
	// bindings are supplied — the picker shows a badge and prompts for values.
	HasVariables bool
	// VariablesQuery is the segment's variable definition DQL (type "query"):
	// its result columns are the variable names, its rows the candidate
	// values. "" when the segment has no variables (or an unsupported type).
	VariablesQuery string
}

// SegmentLister fetches the tenant's filter segments. Constructed in the
// dtui main package so no HTTP lives in this package (mirrors Sources).
type SegmentLister func(ctx context.Context) ([]SegmentOption, error)

// WorkspaceSegment is a .dynatrace.yaml segment reference awaiting resolution
// against the tenant's segment list.
type WorkspaceSegment struct {
	Ref       string // name or UID as written in the file
	Variables []exec.FilterSegmentVariable
}

// segmentListMsg delivers the async segment-list fetch (picker + workspace
// seeding).
type segmentListMsg struct {
	list []SegmentOption
	err  error
}

// loadSegmentList fires the async list fetch.
func (a *app) loadSegmentList() tea.Cmd {
	src := a.opts.SegmentSource
	if src == nil {
		return func() tea.Msg { return segmentListMsg{err: fmt.Errorf("segment source not wired")} }
	}
	return func() tea.Msg {
		list, err := src(context.Background())
		return segmentListMsg{list: list, err: err}
	}
}

// handleSegmentList routes the fetched list: fills the picker cache and, when
// workspace refs are pending, resolves and applies them.
func (a *app) handleSegmentList(msg segmentListMsg) tea.Cmd {
	a.segLoading = false
	if msg.err != nil {
		a.segErr = msg.err.Error()
		if a.segPending {
			// Nothing was applied, so nothing is claimed: no header pill.
			a.segPending = false
			return statusErr("workspace segments not applied — " + msg.err.Error())
		}
		return nil
	}
	a.segErr = ""
	a.segList = msg.list
	if !a.segPending {
		return nil
	}
	a.segPending = false
	sel, vars, warnings := resolveWorkspaceSegments(a.opts.WorkspaceSegments, msg.list)
	a.segVars = vars
	var cmds []tea.Cmd
	if len(sel) > 0 {
		cmds = append(cmds, a.applySegments(sel))
	}
	if len(warnings) > 0 {
		cmds = append(cmds, statusErr("workspace: "+strings.Join(warnings, "; ")+" — continuing without"))
	} else if len(sel) > 0 {
		cmds = append(cmds, status("workspace segments: "+segmentSummary(sel)))
	}
	return tea.Batch(cmds...)
}

// resolveWorkspaceSegments matches .dynatrace.yaml refs against the tenant's
// segments: exact UID first, then exact case-insensitive name. Deliberately
// no substring matching — a committed file must resolve deterministically, so
// absence and ambiguity warn instead of guessing.
func resolveWorkspaceSegments(refs []WorkspaceSegment, list []SegmentOption) (sel []SegmentOption, vars map[string][]exec.FilterSegmentVariable, warnings []string) {
	vars = map[string][]exec.FilterSegmentVariable{}
	for _, ref := range refs {
		if byUID := findSegmentByUID(list, ref.Ref); byUID != nil {
			sel = append(sel, *byUID)
			if len(ref.Variables) > 0 {
				vars[byUID.UID] = ref.Variables
			}
			continue
		}
		var byName []SegmentOption
		for _, s := range list {
			if strings.EqualFold(s.Name, ref.Ref) {
				byName = append(byName, s)
			}
		}
		switch len(byName) {
		case 0:
			warnings = append(warnings, fmt.Sprintf("segment %q not found", ref.Ref))
		case 1:
			sel = append(sel, byName[0])
			if len(ref.Variables) > 0 {
				vars[byName[0].UID] = ref.Variables
			}
		default:
			warnings = append(warnings, fmt.Sprintf("segment name %q is ambiguous (%d matches) — use its UID", ref.Ref, len(byName)))
		}
	}
	return sel, vars, warnings
}

func findSegmentByUID(list []SegmentOption, uid string) *SegmentOption {
	for i := range list {
		if list[i].UID == uid {
			return &list[i]
		}
	}
	return nil
}

// applySegments sets the global segment scope and refetches every live view.
// The selection lives on the shared dataSource, so a plain Refresh() re-reads
// it — no per-view SetSegments plumbing; each view's seq guard drops stale
// in-flight results (mirrors setTimeframe's broadcast).
func (a *app) applySegments(sel []SegmentOption) tea.Cmd {
	a.segApplied = sel
	a.ds.segments = a.segmentRefs(sel)
	seen := map[viewModel]bool{}
	var cmds []tea.Cmd
	for _, v := range append(append([]viewModel{}, a.stack...), a.prev...) {
		if seen[v] {
			continue
		}
		seen[v] = true
		if cmd := v.Refresh(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

// segmentRefs converts the applied options into query refs, attaching the
// workspace variable bindings by UID.
func (a *app) segmentRefs(sel []SegmentOption) []exec.FilterSegmentRef {
	if len(sel) == 0 {
		return nil
	}
	refs := make([]exec.FilterSegmentRef, 0, len(sel))
	for _, s := range sel {
		refs = append(refs, exec.FilterSegmentRef{ID: s.UID, Variables: a.segVars[s.UID]})
	}
	return refs
}

// segmentSummary is the header/status label: first name plus overflow count.
func segmentSummary(sel []SegmentOption) string {
	if len(sel) == 0 {
		return ""
	}
	name := sel[0].Name
	if name == "" {
		name = sel[0].UID
	}
	out := ansi.Truncate(name, 20, "…")
	if rest := len(sel) - 1; rest > 0 {
		out += fmt.Sprintf(" +%d", rest)
	}
	return out
}

// segmentsRejected reports whether segments are active but the named view is
// API-backed (REST, not query:execute) — jumpTo says so instead of letting
// the header pill overclaim.
func (a *app) segmentsRejected(name string) bool {
	if len(a.segApplied) == 0 {
		return false
	}
	spec := catalog.Lookup(name)
	return spec != nil && spec.API != ""
}

// rewriteSegmentVarError swaps the executor's CLI-flavored remedy (ready-made
// -S and --segments-file examples from pkg/exec) for the TUI's own. The
// executor formats this error into its message before returning (the typed
// QueryError is gone), so the match is on its stable first line.
func rewriteSegmentVarError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "segment ") || !strings.Contains(msg, "requires variable") {
		return err
	}
	firstLine, _, _ := strings.Cut(msg, "\n")
	return fmt.Errorf("%s — press S, then v on it to pick values (or set them in .dynatrace.yaml)", firstLine)
}

// --- segment picker overlay -----------------------------------------------------

// openSegmentPicker opens the multi-select overlay, seeding the scratch
// selection from what is currently applied; enter commits, esc discards.
func (a *app) openSegmentPicker() tea.Cmd {
	a.segPickActive = true
	a.segPickSel = 0
	a.segChecked = map[string]bool{}
	for _, s := range a.segApplied {
		a.segChecked[s.UID] = true
	}
	if len(a.segList) == 0 && !a.segLoading && !a.segPending {
		a.segLoading = true
		a.segErr = ""
		return a.loadSegmentList()
	}
	return nil
}

func (a *app) updateSegPicker(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "S":
		a.segPickActive = false
		return nil
	case "up", "k":
		if a.segPickSel > 0 {
			a.segPickSel--
		}
		return nil
	case "down", "j", "tab":
		if a.segPickSel < len(a.segList)-1 {
			a.segPickSel++
		}
		return nil
	case " ":
		if a.segPickSel >= len(a.segList) {
			return nil
		}
		s := a.segList[a.segPickSel]
		if a.segChecked[s.UID] {
			delete(a.segChecked, s.UID)
			return nil
		}
		if len(a.segChecked) >= maxSegments {
			return statusErr(fmt.Sprintf("Grail applies at most %d segments per query", maxSegments))
		}
		a.segChecked[s.UID] = true
		// Selecting an unbound variable segment prompts for values right
		// away (mirrors the web UI's secondary selection); esc there cancels
		// the whole toggle.
		if s.VariablesQuery != "" && len(a.segVars[s.UID]) == 0 {
			return a.openSegVarPicker(s, true)
		}
		return nil
	case "v":
		if a.segPickSel >= len(a.segList) {
			return nil
		}
		if s := a.segList[a.segPickSel]; s.VariablesQuery != "" {
			return a.openSegVarPicker(s, false)
		}
		return statusErr("segment defines no variables")
	case "c":
		a.segChecked = map[string]bool{}
		return nil
	case "r":
		a.segLoading = true
		a.segErr = ""
		return a.loadSegmentList()
	case "enter":
		a.segPickActive = false
		sel := a.checkedSegments()
		if segmentUIDsEqual(sel, a.segApplied) {
			return nil // nothing changed — don't refetch every view
		}
		note := "segments cleared"
		if len(sel) > 0 {
			note = fmt.Sprintf("segments: %s — applied to every DQL view", segmentSummary(sel))
		}
		return tea.Batch(a.applySegments(sel), status(note))
	}
	return nil
}

// checkedSegments returns the scratch selection in list order (deterministic
// regardless of toggle order).
func (a *app) checkedSegments() []SegmentOption {
	var out []SegmentOption
	for _, s := range a.segList {
		if a.segChecked[s.UID] {
			out = append(out, s)
		}
	}
	return out
}

func segmentUIDsEqual(a, b []SegmentOption) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].UID != b[i].UID {
			return false
		}
	}
	return true
}

func (a *app) renderSegPicker() string {
	var b strings.Builder
	b.WriteString(theme.OverlayTitle.Render("filter segments") + "  " +
		theme.Dim.Render("AND-combined · applied to every DQL view") + "\n\n")
	switch {
	case a.segLoading || a.segPending:
		b.WriteString(theme.Dim.Render(" loading segments…") + "\n")
	case a.segErr != "":
		b.WriteString(" " + theme.Error.Render("✗ "+a.segErr) + "\n")
	case len(a.segList) == 0:
		b.WriteString(theme.Dim.Render(" no segments in this environment") + "\n")
	default:
		nameW, descW := 26, 34
		limit := max(a.bodyHeight()-9, 4)
		start := 0
		if a.segPickSel >= limit {
			start = a.segPickSel - limit + 1
		}
		for i := start; i < len(a.segList) && i < start+limit; i++ {
			s := a.segList[i]
			mark := "[ ]"
			if a.segChecked[s.UID] {
				mark = "[x]"
			}
			badge := ""
			switch {
			case len(a.segVars[s.UID]) > 0:
				badge = " " + theme.Badge.Render("vars bound")
			case s.HasVariables:
				badge = " " + theme.Badge.Render("vars")
			}
			row := mark + " " + pad(ansi.Truncate(s.Name, nameW, "…"), nameW)
			if i == a.segPickSel {
				b.WriteString(theme.Selected.Render(" "+row) + " " +
					theme.Dim.Render(pad(ansi.Truncate(s.Description, descW, "…"), descW)) + badge)
			} else {
				b.WriteString(" " + theme.HeaderVal.Render(row) + " " +
					theme.CrumbDim.Render(pad(ansi.Truncate(s.Description, descW, "…"), descW)) + badge)
			}
			b.WriteString("\n")
		}
		if rest := len(a.segList) - start - limit; rest > 0 {
			b.WriteString(theme.Dim.Render(fmt.Sprintf(" … %d more", rest)) + "\n")
		}
		b.WriteString("\n" + theme.Dim.Render(fmt.Sprintf(" %d of max %d selected", len(a.segChecked), maxSegments)) + "\n")
	}
	b.WriteString("\n" + theme.Dim.Render("space toggle · v values · enter apply · c clear · r reload · esc cancel"))
	return b.String()
}

// --- variable value sub-picker --------------------------------------------------

// segVarState is the second-stage overlay for one segment's variable values:
// the variable definition DQL runs through the shared dataSource, its result
// columns are the variable names, its rows the candidate values, and the
// checked rows become FilterSegmentVariable bindings.
type segVarState struct {
	active     bool
	uid, name  string
	seq        int
	rows       []map[string]any
	cols       []string
	checked    map[int]bool // absolute row index
	sel        int          // index into the filtered view
	loading    bool
	err        string
	filter     string
	filtering  bool
	fromToggle bool // opened by the space-toggle: esc cancels the toggle
	query      string
}

// segVarOwner tags the value fetch; the app routes its result into the
// sub-picker instead of a view (like dictOwner).
type segVarOwner struct{}

// segVarMaxRows caps the candidate list — variable queries enumerate
// entities (namespaces, clusters) and stay small; a runaway one must not.
const segVarMaxRows = 500

// openSegVarPicker opens the value sub-picker for one segment and fires its
// variable definition query.
func (a *app) openSegVarPicker(s SegmentOption, fromToggle bool) tea.Cmd {
	a.segVar = segVarState{
		active:     true,
		uid:        s.UID,
		name:       s.Name,
		seq:        a.segVar.seq + 1,
		checked:    map[int]bool{},
		loading:    true,
		fromToggle: fromToggle,
		query:      s.VariablesQuery,
	}
	return a.ds.queryCapped(segVarOwner{}, a.segVar.seq, s.VariablesQuery, segVarMaxRows)
}

// handleSegVarData routes the value fetch into the sub-picker, pre-checking
// rows already covered by existing bindings (workspace or a previous visit).
func (a *app) handleSegVarData(msg dataMsg) tea.Cmd {
	if !a.segVar.active || msg.seq != a.segVar.seq {
		return nil
	}
	a.segVar.loading = false
	if msg.err != nil {
		a.segVar.err = msg.err.Error()
		return nil
	}
	a.segVar.rows = msg.records
	a.segVar.cols = segVarColumns(msg.records)
	bound := map[string]map[string]bool{}
	for _, v := range a.segVars[a.segVar.uid] {
		vals := map[string]bool{}
		for _, val := range v.Values {
			vals[val] = true
		}
		bound[v.Name] = vals
	}
	for i, row := range a.segVar.rows {
		for _, col := range a.segVar.cols {
			if bound[col][catalog.Str(row, col)] {
				a.segVar.checked[i] = true
				break
			}
		}
	}
	return nil
}

// segVarColumns derives the variable names from the first result row. Maps
// carry no column order, so names sort alphabetically — cosmetic only.
func segVarColumns(rows []map[string]any) []string {
	if len(rows) == 0 {
		return nil
	}
	cols := make([]string, 0, len(rows[0]))
	for k := range rows[0] {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	return cols
}

// segVarVisible returns the absolute indices of rows matching the filter.
func (a *app) segVarVisible() []int {
	idx := make([]int, 0, len(a.segVar.rows))
	needle := strings.ToLower(a.segVar.filter)
	for i, row := range a.segVar.rows {
		if needle == "" {
			idx = append(idx, i)
			continue
		}
		for _, col := range a.segVar.cols {
			if strings.Contains(strings.ToLower(catalog.Str(row, col)), needle) {
				idx = append(idx, i)
				break
			}
		}
	}
	return idx
}

func (a *app) updateSegVarPicker(msg tea.KeyMsg) tea.Cmd {
	if a.segVar.filtering {
		switch msg.String() {
		case "enter":
			a.segVar.filtering = false
			return nil
		case "esc":
			a.segVar.filtering = false
			a.segVar.filter = ""
			a.segVar.sel = 0
			return nil
		case "backspace":
			if a.segVar.filter != "" {
				r := []rune(a.segVar.filter)
				a.segVar.filter = string(r[:len(r)-1])
			}
			a.segVar.sel = 0
			return nil
		}
		if msg.Type == tea.KeyRunes {
			a.segVar.filter += string(msg.Runes)
			a.segVar.sel = 0
		}
		return nil
	}
	visible := a.segVarVisible()
	switch msg.String() {
	case "esc":
		a.segVar.active = false
		if a.segVar.fromToggle {
			// The gesture was "select this segment, then pick its values" —
			// backing out of the values cancels the selection too.
			delete(a.segChecked, a.segVar.uid)
		}
		return nil
	case "/":
		a.segVar.filtering = true
		a.segVar.filter = ""
		return nil
	case "up", "k":
		if a.segVar.sel > 0 {
			a.segVar.sel--
		}
		return nil
	case "down", "j", "tab":
		if a.segVar.sel < len(visible)-1 {
			a.segVar.sel++
		}
		return nil
	case " ":
		if a.segVar.sel < len(visible) {
			i := visible[a.segVar.sel]
			if a.segVar.checked[i] {
				delete(a.segVar.checked, i)
			} else {
				a.segVar.checked[i] = true
			}
		}
		return nil
	case "r":
		a.segVar.loading = true
		a.segVar.err = ""
		a.segVar.rows, a.segVar.cols = nil, nil
		a.segVar.checked = map[int]bool{}
		a.segVar.seq++
		return a.ds.queryCapped(segVarOwner{}, a.segVar.seq, a.segVar.query, segVarMaxRows)
	case "enter":
		binds := a.segVarBindings()
		if len(binds) == 0 {
			return statusErr("pick at least one value (esc cancels the segment)")
		}
		if a.segVars == nil {
			a.segVars = map[string][]exec.FilterSegmentVariable{}
		}
		a.segVars[a.segVar.uid] = binds
		a.segChecked[a.segVar.uid] = true
		a.segVar.active = false
		return status(fmt.Sprintf("%s: values bound — enter in the picker applies", a.segVar.name))
	}
	return nil
}

// segVarBindings turns the checked rows into per-column variable bindings.
func (a *app) segVarBindings() []exec.FilterSegmentVariable {
	var binds []exec.FilterSegmentVariable
	for _, col := range a.segVar.cols {
		seen := map[string]bool{}
		var vals []string
		for i, row := range a.segVar.rows {
			if !a.segVar.checked[i] {
				continue
			}
			v := catalog.Str(row, col)
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			vals = append(vals, v)
		}
		if len(vals) > 0 {
			binds = append(binds, exec.FilterSegmentVariable{Name: col, Values: vals})
		}
	}
	return binds
}

func (a *app) renderSegVarPicker() string {
	var b strings.Builder
	b.WriteString(theme.OverlayTitle.Render("values — "+a.segVar.name) + "  " +
		theme.Dim.Render(strings.Join(a.segVar.cols, " · ")) + "\n\n")
	switch {
	case a.segVar.loading:
		b.WriteString(theme.Dim.Render(" loading values…") + "\n")
	case a.segVar.err != "":
		b.WriteString(" " + theme.Error.Render("✗ "+a.segVar.err) + "\n")
	case len(a.segVar.rows) == 0:
		b.WriteString(theme.Dim.Render(" the variable query returned no values") + "\n")
	default:
		visible := a.segVarVisible()
		width := 64
		limit := max(a.bodyHeight()-9, 4)
		start := 0
		if a.segVar.sel >= limit {
			start = a.segVar.sel - limit + 1
		}
		for pos := start; pos < len(visible) && pos < start+limit; pos++ {
			i := visible[pos]
			mark := "[ ]"
			if a.segVar.checked[i] {
				mark = "[x]"
			}
			vals := make([]string, 0, len(a.segVar.cols))
			for _, col := range a.segVar.cols {
				vals = append(vals, catalog.Str(a.segVar.rows[i], col))
			}
			row := mark + " " + ansi.Truncate(strings.Join(vals, " · "), width, "…")
			if pos == a.segVar.sel {
				b.WriteString(theme.Selected.Render(" " + pad(row, width+5)))
			} else {
				b.WriteString(" " + theme.HeaderVal.Render(row))
			}
			b.WriteString("\n")
		}
		if rest := len(visible) - start - limit; rest > 0 {
			b.WriteString(theme.Dim.Render(fmt.Sprintf(" … %d more", rest)) + "\n")
		}
		if len(visible) == 0 {
			b.WriteString(theme.Dim.Render(" no value matches the filter") + "\n")
		}
		b.WriteString("\n" + theme.Dim.Render(fmt.Sprintf(" %d selected", len(a.segVar.checked))))
		if a.segVar.filter != "" || a.segVar.filtering {
			b.WriteString(theme.Dim.Render(" · /" + a.segVar.filter))
			if a.segVar.filtering {
				b.WriteString(theme.Crumb.Render("▌"))
			}
		}
		b.WriteString("\n")
	}
	if a.segVar.filtering {
		b.WriteString("\n" + theme.Dim.Render("type to filter · enter done · esc clear"))
	} else {
		b.WriteString("\n" + theme.Dim.Render("space toggle · / filter · enter bind · esc back"))
	}
	return b.String()
}
