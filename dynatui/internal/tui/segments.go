package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtctl/pkg/exec"
	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
	"github.com/dynatrace-oss/dynatui/internal/tui/theme"
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
// dynatui main package so no HTTP lives in this package (mirrors Sources).
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
	a.segPaused = false
	a.ds.segments = a.segmentRefs(sel)
	return a.refreshAllViews()
}

// refreshAllViews refetches every live view on both stacks (deduped) so a
// dataSource-level scope change reaches covered views too.
func (a *app) refreshAllViews() tea.Cmd {
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

// toggleSegmentsPaused suspends or restores the applied segment set (alt+s):
// one keypress to see the unfiltered picture, one to get the exact same
// scope back — selection and variable bindings intact. Restoring a
// workspace-seeded set needs no trip through the picker.
func (a *app) toggleSegmentsPaused() tea.Cmd {
	if len(a.segApplied) == 0 {
		return statusErr("no segments selected — S picks some")
	}
	a.segPaused = !a.segPaused
	if a.segPaused {
		a.ds.segments = nil
		return tea.Batch(a.refreshAllViews(),
			status(fmt.Sprintf("segments off — alt+s restores %s", segmentSummary(a.segApplied))))
	}
	a.ds.segments = a.segmentRefs(a.segApplied)
	return tea.Batch(a.refreshAllViews(),
		status(fmt.Sprintf("segments on: %s", segmentSummary(a.segApplied))))
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
// the header pill overclaim. A paused set filters nothing, so nothing is
// rejected.
func (a *app) segmentsRejected(name string) bool {
	if len(a.segApplied) == 0 || a.segPaused {
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

// segmentPicker is the 'S' multi-select overlay; checked is the scratch
// selection until enter commits (esc discards). While one segment's variable
// values are being bound, the sub-picker (vars) covers it and owns the keys.
type segmentPicker struct {
	app     *app
	sel     int
	checked map[string]bool
	vars    *segVarPicker
}

// openSegmentPicker opens the multi-select overlay, seeding the scratch
// selection from what is currently applied.
func (a *app) openSegmentPicker() tea.Cmd {
	p := &segmentPicker{app: a, checked: map[string]bool{}}
	for _, s := range a.segApplied {
		p.checked[s.UID] = true
	}
	a.overlay = p
	if len(a.segList) == 0 && !a.segLoading && !a.segPending {
		a.segLoading = true
		a.segErr = ""
		return a.loadSegmentList()
	}
	return nil
}

func (p *segmentPicker) Hints() []keyHint {
	if p.vars != nil {
		return p.vars.Hints()
	}
	return []keyHint{{"space", "toggle"}, {"v", "values"}, {"enter", "apply"}, {"c", "clear"}, {"esc", "cancel"}}
}

func (p *segmentPicker) HandleKey(msg tea.KeyMsg) tea.Cmd {
	if p.vars != nil {
		return p.vars.HandleKey(msg)
	}
	a := p.app
	key := msg.String()
	switch key {
	case "esc", "S":
		a.overlay = nil
		return nil
	case " ":
		if p.sel >= len(a.segList) {
			return nil
		}
		s := a.segList[p.sel]
		if p.checked[s.UID] {
			delete(p.checked, s.UID)
			return nil
		}
		if len(p.checked) >= maxSegments {
			return statusErr(fmt.Sprintf("Grail applies at most %d segments per query", maxSegments))
		}
		p.checked[s.UID] = true
		// Selecting an unbound variable segment prompts for values right
		// away (mirrors the web UI's secondary selection); esc there cancels
		// the whole toggle.
		if s.VariablesQuery != "" && len(a.segVars[s.UID]) == 0 {
			return p.openVarPicker(s, true)
		}
		return nil
	case "v":
		if p.sel >= len(a.segList) {
			return nil
		}
		if s := a.segList[p.sel]; s.VariablesQuery != "" {
			return p.openVarPicker(s, false)
		}
		return statusErr("segment defines no variables")
	case "c":
		p.checked = map[string]bool{}
		return nil
	case "r":
		a.segLoading = true
		a.segErr = ""
		return a.loadSegmentList()
	case "enter":
		a.overlay = nil
		sel := p.checkedSegments()
		// Applying the unchanged set is a no-op — unless it is paused, where
		// re-applying is the intent (resume).
		if !a.segPaused && segmentUIDsEqual(sel, a.segApplied) {
			return nil
		}
		note := "segments cleared"
		if len(sel) > 0 {
			note = fmt.Sprintf("segments: %s — applied to every DQL view", segmentSummary(sel))
		}
		return tea.Batch(a.applySegments(sel), status(note))
	}
	moveSel(key, &p.sel, len(a.segList))
	return nil
}

// checkedSegments returns the scratch selection in list order (deterministic
// regardless of toggle order).
func (p *segmentPicker) checkedSegments() []SegmentOption {
	var out []SegmentOption
	for _, s := range p.app.segList {
		if p.checked[s.UID] {
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

func (p *segmentPicker) View(width, height int) string {
	if p.vars != nil {
		return p.vars.View(width, height)
	}
	a := p.app
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
		limit := max(height-9, 4)
		overlayRows(&b, len(a.segList), p.sel, limit, func(i int) string {
			s := a.segList[i]
			mark := "[ ]"
			if p.checked[s.UID] {
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
			if i == p.sel {
				return theme.Selected.Render(" "+row) + " " +
					theme.Dim.Render(pad(ansi.Truncate(s.Description, descW, "…"), descW)) + badge
			}
			return " " + theme.HeaderVal.Render(row) + " " +
				theme.CrumbDim.Render(pad(ansi.Truncate(s.Description, descW, "…"), descW)) + badge
		})
		b.WriteString("\n" + theme.Dim.Render(fmt.Sprintf(" %d of max %d selected", len(p.checked), maxSegments)) + "\n")
	}
	b.WriteString("\n" + theme.Dim.Render("space toggle · v values · enter apply · c clear · r reload · esc cancel"))
	return centerOverlay(width, height, b.String())
}

// --- variable value sub-picker --------------------------------------------------

// segVarPicker is the second-stage overlay for one segment's variable values:
// the variable definition DQL runs through the shared dataSource, its result
// columns are the variable names, its rows the candidate values, and the
// checked rows become FilterSegmentVariable bindings. It is always opened
// from (and returns to) the segment picker; the picker instance itself is
// the dataMsg owner, so results for a closed picker die unrouted.
type segVarPicker struct {
	parent     *segmentPicker
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

// segVarMaxRows caps the candidate list — variable queries enumerate
// entities (namespaces, clusters) and stay small; a runaway one must not.
const segVarMaxRows = 500

// openVarPicker opens the value sub-picker for one segment and fires its
// variable definition query.
func (p *segmentPicker) openVarPicker(s SegmentOption, fromToggle bool) tea.Cmd {
	p.vars = &segVarPicker{
		parent:     p,
		uid:        s.UID,
		name:       s.Name,
		seq:        1,
		checked:    map[int]bool{},
		loading:    true,
		fromToggle: fromToggle,
		query:      s.VariablesQuery,
	}
	return p.app.ds.queryCapped(p.vars, p.vars.seq, s.VariablesQuery, segVarMaxRows)
}

// handleData routes the value fetch into the sub-picker, pre-checking rows
// already covered by existing bindings (workspace or a previous visit).
func (v *segVarPicker) handleData(msg dataMsg) tea.Cmd {
	if msg.seq != v.seq {
		return nil
	}
	v.loading = false
	if msg.err != nil {
		v.err = msg.err.Error()
		return nil
	}
	v.rows = msg.records
	v.cols = segVarColumns(msg.records)
	bound := map[string]map[string]bool{}
	for _, b := range v.parent.app.segVars[v.uid] {
		vals := map[string]bool{}
		for _, val := range b.Values {
			vals[val] = true
		}
		bound[b.Name] = vals
	}
	for i, row := range v.rows {
		for _, col := range v.cols {
			if bound[col][catalog.Str(row, col)] {
				v.checked[i] = true
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

// visible returns the absolute indices of rows matching the filter.
func (v *segVarPicker) visible() []int {
	idx := make([]int, 0, len(v.rows))
	needle := strings.ToLower(v.filter)
	for i, row := range v.rows {
		if needle == "" {
			idx = append(idx, i)
			continue
		}
		for _, col := range v.cols {
			if strings.Contains(strings.ToLower(catalog.Str(row, col)), needle) {
				idx = append(idx, i)
				break
			}
		}
	}
	return idx
}

func (v *segVarPicker) Hints() []keyHint {
	return []keyHint{{"space", "toggle"}, {"/", "filter"}, {"enter", "bind"}, {"esc", "back"}}
}

func (v *segVarPicker) HandleKey(msg tea.KeyMsg) tea.Cmd {
	a := v.parent.app
	if v.filtering {
		switch msg.String() {
		case "enter":
			v.filtering = false
			return nil
		case "esc":
			v.filtering = false
			v.filter = ""
			v.sel = 0
			return nil
		case "backspace":
			if v.filter != "" {
				r := []rune(v.filter)
				v.filter = string(r[:len(r)-1])
			}
			v.sel = 0
			return nil
		}
		if msg.Type == tea.KeyRunes {
			v.filter += string(msg.Runes)
			v.sel = 0
		}
		return nil
	}
	visible := v.visible()
	key := msg.String()
	switch key {
	case "esc":
		v.parent.vars = nil
		if v.fromToggle {
			// The gesture was "select this segment, then pick its values" —
			// backing out of the values cancels the selection too.
			delete(v.parent.checked, v.uid)
		}
		return nil
	case "/":
		v.filtering = true
		v.filter = ""
		return nil
	case " ":
		if v.sel < len(visible) {
			i := visible[v.sel]
			if v.checked[i] {
				delete(v.checked, i)
			} else {
				v.checked[i] = true
			}
		}
		return nil
	case "r":
		v.loading = true
		v.err = ""
		v.rows, v.cols = nil, nil
		v.checked = map[int]bool{}
		v.seq++
		return a.ds.queryCapped(v, v.seq, v.query, segVarMaxRows)
	case "enter":
		binds := v.bindings()
		if len(binds) == 0 {
			return statusErr("pick at least one value (esc cancels the segment)")
		}
		if a.segVars == nil {
			a.segVars = map[string][]exec.FilterSegmentVariable{}
		}
		a.segVars[v.uid] = binds
		v.parent.checked[v.uid] = true
		v.parent.vars = nil
		return status(fmt.Sprintf("%s: values bound — enter in the picker applies", v.name))
	}
	moveSel(key, &v.sel, len(visible))
	return nil
}

// bindings turns the checked rows into per-column variable bindings.
func (v *segVarPicker) bindings() []exec.FilterSegmentVariable {
	var binds []exec.FilterSegmentVariable
	for _, col := range v.cols {
		seen := map[string]bool{}
		var vals []string
		for i, row := range v.rows {
			if !v.checked[i] {
				continue
			}
			val := catalog.Str(row, col)
			if val == "" || seen[val] {
				continue
			}
			seen[val] = true
			vals = append(vals, val)
		}
		if len(vals) > 0 {
			binds = append(binds, exec.FilterSegmentVariable{Name: col, Values: vals})
		}
	}
	return binds
}

func (v *segVarPicker) View(width, height int) string {
	var b strings.Builder
	b.WriteString(theme.OverlayTitle.Render("values — "+v.name) + "  " +
		theme.Dim.Render(strings.Join(v.cols, " · ")) + "\n\n")
	switch {
	case v.loading:
		b.WriteString(theme.Dim.Render(" loading values…") + "\n")
	case v.err != "":
		b.WriteString(" " + theme.Error.Render("✗ "+v.err) + "\n")
	case len(v.rows) == 0:
		b.WriteString(theme.Dim.Render(" the variable query returned no values") + "\n")
	default:
		visible := v.visible()
		rowW := 64
		limit := max(height-9, 4)
		overlayRows(&b, len(visible), v.sel, limit, func(pos int) string {
			i := visible[pos]
			mark := "[ ]"
			if v.checked[i] {
				mark = "[x]"
			}
			vals := make([]string, 0, len(v.cols))
			for _, col := range v.cols {
				vals = append(vals, catalog.Str(v.rows[i], col))
			}
			row := mark + " " + ansi.Truncate(strings.Join(vals, " · "), rowW, "…")
			if pos == v.sel {
				return theme.Selected.Render(" " + pad(row, rowW+5))
			}
			return " " + theme.HeaderVal.Render(row)
		})
		if len(visible) == 0 {
			b.WriteString(theme.Dim.Render(" no value matches the filter") + "\n")
		}
		b.WriteString("\n" + theme.Dim.Render(fmt.Sprintf(" %d selected", len(v.checked))))
		if v.filter != "" || v.filtering {
			b.WriteString(theme.Dim.Render(" · /" + v.filter))
			if v.filtering {
				b.WriteString(theme.Crumb.Render("▌"))
			}
		}
		b.WriteString("\n")
	}
	if v.filtering {
		b.WriteString("\n" + theme.Dim.Render("type to filter · enter done · esc clear"))
	} else {
		b.WriteString("\n" + theme.Dim.Render("space toggle · / filter · enter bind · esc back"))
	}
	return centerOverlay(width, height, b.String())
}
