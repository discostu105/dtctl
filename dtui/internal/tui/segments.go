package tui

import (
	"context"
	"fmt"
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
	// bindings are supplied (via .dynatrace.yaml) — the picker shows a badge.
	HasVariables bool
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
	return fmt.Errorf("%s — bind values under segments: in .dynatrace.yaml, or deselect it (S)", firstLine)
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
		uid := a.segList[a.segPickSel].UID
		if a.segChecked[uid] {
			delete(a.segChecked, uid)
			return nil
		}
		if len(a.segChecked) >= maxSegments {
			return statusErr(fmt.Sprintf("Grail applies at most %d segments per query", maxSegments))
		}
		a.segChecked[uid] = true
		return nil
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
	b.WriteString("\n" + theme.Dim.Render("space toggle · enter apply · c clear · r reload · esc cancel"))
	return b.String()
}
