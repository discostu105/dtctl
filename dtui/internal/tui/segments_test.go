package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dtctl/pkg/exec"
)

func segStubSource(list []SegmentOption, err error) SegmentLister {
	return func(context.Context) ([]SegmentOption, error) { return list, err }
}

func testSegmentList() []SegmentOption {
	return []SegmentOption{
		{UID: "uid-a", Name: "payments-prod", Description: "payments services in prod"},
		{UID: "uid-b", Name: "team-checkout", HasVariables: true},
		{UID: "uid-c", Name: "infra-eu"},
	}
}

// varSegmentList is a picker list whose first entry defines a variable, so
// space-toggling it opens the value sub-picker.
func varSegmentList() []SegmentOption {
	return []SegmentOption{
		{UID: "uid-var", Name: "webshops", HasVariables: true,
			VariablesQuery: "fetch dt.entity.cloud_application_namespace | fields namespace = entity.name"},
		{UID: "uid-plain", Name: "plain"},
	}
}

// deliverSegVarRows feeds the value sub-picker its query result (deliver
// drops dataMsg, so tests inject it like seedRows does for tables).
func deliverSegVarRows(a *app, rows []map[string]any) {
	a.Update(dataMsg{owner: segVarOwner{}, seq: a.segVar.seq, records: rows})
}

func namespaceRows() []map[string]any {
	return []map[string]any{
		{"namespace": "astroshop"},
		{"namespace": "easytrade"},
		{"namespace": "online-boutique"},
	}
}

// testAppSeeded builds an app with workspace segments pending, mirroring
// testApp but with segment options set before Init runs.
func testAppSeeded(t *testing.T, opts Options) *app {
	t.Helper()
	previewEnabled = true
	a, err := newApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	a.ds.runFn = func(string) ([]map[string]any, error) { return nil, nil }
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	deliver(a, a.Init())
	return a
}

func TestExecOptsCarriesSegments(t *testing.T) {
	d := &dataSource{segments: []exec.FilterSegmentRef{{ID: "uid-a"}}}
	opts := d.execOpts(500)
	if opts.MaxResultRecords != 500 || opts.FetchTimeoutSeconds != 60 {
		t.Errorf("execOpts basics changed: %+v", opts)
	}
	if len(opts.Segments) != 1 || opts.Segments[0].ID != "uid-a" {
		t.Errorf("Segments = %+v, want the dataSource's", opts.Segments)
	}
	if got := (&dataSource{}).execOpts(1000).Segments; got != nil {
		t.Errorf("unsegmented execOpts carries %+v", got)
	}
}

func TestSegmentPickerToggleApplyClear(t *testing.T) {
	a := testApp(t, "logs")
	a.opts.SegmentSource = segStubSource(testSegmentList(), nil)

	press(a, key("S"))
	if !a.segPickActive {
		t.Fatal("S did not open the segment picker")
	}
	if len(a.segList) != 3 {
		t.Fatalf("picker list = %d entries, want 3", len(a.segList))
	}

	seqBefore := a.top().(*tableView).seq
	press(a, key(" ")) // toggle payments-prod
	press(a, key("j"))
	press(a, key(" ")) // toggle team-checkout
	press(a, key("enter"))
	if a.segPickActive {
		t.Fatal("enter did not close the picker")
	}
	if len(a.ds.segments) != 2 || a.ds.segments[0].ID != "uid-a" || a.ds.segments[1].ID != "uid-b" {
		t.Fatalf("ds.segments = %+v, want uid-a, uid-b in list order", a.ds.segments)
	}
	if a.top().(*tableView).seq <= seqBefore {
		t.Error("apply did not refetch the open view")
	}
	if header := a.renderHeader(); !strings.Contains(header, "◐ payments-prod +1") {
		t.Errorf("header misses segment pill: %q", header)
	}
	if echo := a.top().Echo(); !strings.Contains(echo, "-S uid-a") || !strings.Contains(echo, "-S uid-b") {
		t.Errorf("Echo misses -S flags: %q", echo)
	}

	// Re-enter without changes: no refetch storm.
	seqBefore = a.top().(*tableView).seq
	press(a, key("S"))
	press(a, key("enter"))
	if a.top().(*tableView).seq != seqBefore {
		t.Error("unchanged selection refetched views")
	}

	// esc discards the scratch selection.
	press(a, key("S"))
	press(a, key(" ")) // would deselect payments-prod
	press(a, key("esc"))
	if len(a.ds.segments) != 2 {
		t.Fatalf("esc committed the scratch selection: %+v", a.ds.segments)
	}

	// c clears; enter applies the empty set.
	press(a, key("S"))
	press(a, key("c"))
	press(a, key("enter"))
	if len(a.ds.segments) != 0 {
		t.Fatalf("clear left segments applied: %+v", a.ds.segments)
	}
	if header := a.renderHeader(); strings.Contains(header, "◐") {
		t.Errorf("header still shows a pill after clear: %q", header)
	}
}

func TestSegmentPickerRefusesEleventh(t *testing.T) {
	list := make([]SegmentOption, maxSegments+1)
	for i := range list {
		list[i] = SegmentOption{UID: fmt.Sprintf("uid-%02d", i), Name: fmt.Sprintf("seg-%02d", i)}
	}
	a := testApp(t, "logs")
	a.opts.SegmentSource = segStubSource(list, nil)
	press(a, key("S"))
	for i := 0; i <= maxSegments; i++ {
		press(a, key(" "))
		if i < maxSegments {
			press(a, key("j")) // any key clears the transient status — keep the 11th toggle's
		}
	}
	if len(a.segChecked) != maxSegments {
		t.Fatalf("checked %d segments, want the Grail cap %d", len(a.segChecked), maxSegments)
	}
	if !a.statusErr || !strings.Contains(a.status, "at most 10") {
		t.Errorf("status = %q (err=%v), want the cap notice", a.status, a.statusErr)
	}
}

func TestSegmentsCommandOpensPicker(t *testing.T) {
	a := testApp(t, "logs")
	a.opts.SegmentSource = segStubSource(testSegmentList(), nil)
	press(a, key(":"))
	press(a, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("segments")})
	press(a, key("enter"))
	if !a.segPickActive {
		t.Fatal(":segments did not open the picker")
	}
}

func TestWorkspaceSegmentSeeding(t *testing.T) {
	a := testAppSeeded(t, Options{
		ContextName: "test", SafetyLevel: "readonly", InitialView: "logs",
		SegmentSource: segStubSource(testSegmentList(), nil),
		WorkspaceSegments: []WorkspaceSegment{
			{Ref: "uid-b", Variables: []exec.FilterSegmentVariable{{Name: "env", Values: []string{"prod"}}}},
			{Ref: "Payments-Prod"}, // by name, case-insensitive exact
		},
	})
	if a.segPending {
		t.Fatal("segPending still set after seeding")
	}
	if len(a.ds.segments) != 2 || a.ds.segments[0].ID != "uid-b" || a.ds.segments[1].ID != "uid-a" {
		t.Fatalf("ds.segments = %+v, want uid-b, uid-a in file order", a.ds.segments)
	}
	vars := a.ds.segments[0].Variables
	if len(vars) != 1 || vars[0].Name != "env" || vars[0].Values[0] != "prod" {
		t.Fatalf("variable bindings lost: %+v", vars)
	}
	if a.statusErr {
		t.Errorf("clean seeding reported an error: %q", a.status)
	}
	if header := a.renderHeader(); !strings.Contains(header, "◐ team-checkout +1") {
		t.Errorf("header misses seeded pill: %q", header)
	}
}

func TestWorkspaceSegmentSeedingWarnsUnknownAndAmbiguous(t *testing.T) {
	list := append(testSegmentList(), SegmentOption{UID: "uid-d", Name: "payments-prod"})
	a := testAppSeeded(t, Options{
		ContextName: "test", SafetyLevel: "readonly", InitialView: "logs",
		SegmentSource: segStubSource(list, nil),
		WorkspaceSegments: []WorkspaceSegment{
			{Ref: "infra-eu"},
			{Ref: "missing-segment"},
			{Ref: "payments-prod"}, // two segments share this name
		},
	})
	if len(a.ds.segments) != 1 || a.ds.segments[0].ID != "uid-c" {
		t.Fatalf("ds.segments = %+v, want just infra-eu", a.ds.segments)
	}
	if !a.statusErr {
		t.Fatalf("no warning surfaced; status = %q", a.status)
	}
	if !strings.Contains(a.status, `"missing-segment" not found`) || !strings.Contains(a.status, "ambiguous") {
		t.Errorf("warning misses causes: %q", a.status)
	}
}

func TestWorkspaceSegmentSeedingListFailure(t *testing.T) {
	a := testAppSeeded(t, Options{
		ContextName: "test", SafetyLevel: "readonly", InitialView: "logs",
		SegmentSource:     segStubSource(nil, errors.New("HTTP 403: missing storage:filter-segments:read")),
		WorkspaceSegments: []WorkspaceSegment{{Ref: "payments-prod"}},
	})
	if a.segPending {
		t.Fatal("segPending stuck after a failed fetch")
	}
	if len(a.ds.segments) != 0 {
		t.Fatalf("segments applied despite fetch failure: %+v", a.ds.segments)
	}
	if !a.statusErr || !strings.Contains(a.status, "workspace segments not applied") {
		t.Errorf("status = %q, want the not-applied warning", a.status)
	}
	if header := a.renderHeader(); strings.Contains(header, "◐") || strings.Contains(header, "◌") {
		t.Errorf("header claims a scope that was never applied: %q", header)
	}
}

func TestSegmentsRejectedOnAPIBackedViews(t *testing.T) {
	a := testApp(t, "logs")
	a.opts.SegmentSource = segStubSource(testSegmentList(), nil)
	press(a, key("S"))
	press(a, key(" "))
	press(a, key("enter"))

	if a.segmentsRejected("logs") {
		t.Error("segments wrongly rejected on a DQL view")
	}
	if !a.segmentsRejected("slos") {
		t.Error("slos is API-backed — segments should be rejected")
	}
	deliver(a, a.jumpTo("slos", ""))
	if !a.statusErr || !strings.Contains(a.status, "segments don't apply") {
		t.Errorf("status = %q, want the API-backed notice", a.status)
	}
}

func TestSegVarSubPickerBindsValues(t *testing.T) {
	a := testApp(t, "logs")
	a.opts.SegmentSource = segStubSource(varSegmentList(), nil)

	press(a, key("S"))
	press(a, key(" ")) // toggling the unbound vars segment opens the sub-picker
	if !a.segVar.active {
		t.Fatal("space on an unbound vars segment did not open the value sub-picker")
	}
	if !a.segVar.loading {
		t.Fatal("sub-picker not in loading state")
	}
	deliverSegVarRows(a, namespaceRows())
	if a.segVar.loading || len(a.segVar.rows) != 3 {
		t.Fatalf("rows not delivered: loading=%v rows=%d", a.segVar.loading, len(a.segVar.rows))
	}
	if len(a.segVar.cols) != 1 || a.segVar.cols[0] != "namespace" {
		t.Fatalf("cols = %v, want [namespace]", a.segVar.cols)
	}

	press(a, key("j"))
	press(a, key(" ")) // check easytrade
	press(a, key("enter"))
	if a.segVar.active {
		t.Fatal("enter did not close the sub-picker")
	}
	if !a.segPickActive {
		t.Fatal("sub-picker enter should return to the segment picker")
	}
	if !a.segChecked["uid-var"] {
		t.Fatal("segment lost its check after binding")
	}
	binds := a.segVars["uid-var"]
	if len(binds) != 1 || binds[0].Name != "namespace" || len(binds[0].Values) != 1 || binds[0].Values[0] != "easytrade" {
		t.Fatalf("bindings = %+v, want namespace=[easytrade]", binds)
	}

	press(a, key("enter")) // apply the segment selection
	if len(a.ds.segments) != 1 || a.ds.segments[0].ID != "uid-var" {
		t.Fatalf("ds.segments = %+v", a.ds.segments)
	}
	if vars := a.ds.segments[0].Variables; len(vars) != 1 || vars[0].Values[0] != "easytrade" {
		t.Fatalf("applied refs miss the binding: %+v", a.ds.segments[0])
	}
}

func TestSegVarSubPickerEscCancelsToggle(t *testing.T) {
	a := testApp(t, "logs")
	a.opts.SegmentSource = segStubSource(varSegmentList(), nil)
	press(a, key("S"))
	press(a, key(" "))
	deliverSegVarRows(a, namespaceRows())
	press(a, key("esc"))
	if a.segVar.active {
		t.Fatal("esc did not close the sub-picker")
	}
	if a.segChecked["uid-var"] {
		t.Fatal("esc from the value prompt should cancel the segment toggle")
	}
	if len(a.segVars["uid-var"]) != 0 {
		t.Fatalf("esc bound values: %+v", a.segVars["uid-var"])
	}
}

func TestSegVarSubPickerEnterNeedsAValue(t *testing.T) {
	a := testApp(t, "logs")
	a.opts.SegmentSource = segStubSource(varSegmentList(), nil)
	press(a, key("S"))
	press(a, key(" "))
	deliverSegVarRows(a, namespaceRows())
	press(a, key("enter")) // nothing checked
	if !a.segVar.active {
		t.Fatal("enter with no values should keep the sub-picker open")
	}
	if !a.statusErr || !strings.Contains(a.status, "at least one value") {
		t.Errorf("status = %q, want the pick-a-value hint", a.status)
	}
}

func TestSegVarSubPickerFilter(t *testing.T) {
	a := testApp(t, "logs")
	a.opts.SegmentSource = segStubSource(varSegmentList(), nil)
	press(a, key("S"))
	press(a, key(" "))
	deliverSegVarRows(a, namespaceRows())

	press(a, key("/"))
	press(a, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("boutique")})
	press(a, key("enter")) // leave filter mode, keep the filter
	visible := a.segVarVisible()
	if len(visible) != 1 || visible[0] != 2 {
		t.Fatalf("visible = %v, want just online-boutique's row", visible)
	}
	press(a, key(" ")) // toggles the filtered row's absolute index
	if !a.segVar.checked[2] {
		t.Fatal("toggle under filter hit the wrong row")
	}
	press(a, key("enter"))
	if got := a.segVars["uid-var"][0].Values[0]; got != "online-boutique" {
		t.Fatalf("bound %q, want online-boutique", got)
	}
}

func TestSegVarVOpensWithExistingBindingsPrechecked(t *testing.T) {
	a := testApp(t, "logs")
	a.opts.SegmentSource = segStubSource(varSegmentList(), nil)
	a.segVars = map[string][]exec.FilterSegmentVariable{
		"uid-var": {{Name: "namespace", Values: []string{"astroshop"}}},
	}
	press(a, key("S"))
	press(a, key("v"))
	if !a.segVar.active {
		t.Fatal("v did not open the sub-picker")
	}
	deliverSegVarRows(a, namespaceRows())
	if !a.segVar.checked[0] || a.segVar.checked[1] {
		t.Fatalf("pre-check wrong: %+v (want only astroshop's row)", a.segVar.checked)
	}

	// v on a segment without variables refuses.
	press(a, key("esc"))
	press(a, key("j"))
	press(a, key("v"))
	if a.segVar.active {
		t.Fatal("v opened a sub-picker for a variable-less segment")
	}
	if !a.statusErr || !strings.Contains(a.status, "no variables") {
		t.Errorf("status = %q, want the no-variables notice", a.status)
	}
}

func TestSegVarSubPickerFetchError(t *testing.T) {
	a := testApp(t, "logs")
	a.opts.SegmentSource = segStubSource(varSegmentList(), nil)
	press(a, key("S"))
	press(a, key(" "))
	a.Update(dataMsg{owner: segVarOwner{}, seq: a.segVar.seq, err: errors.New("HTTP 500")})
	if a.segVar.loading || a.segVar.err == "" {
		t.Fatalf("fetch error not surfaced: loading=%v err=%q", a.segVar.loading, a.segVar.err)
	}
	// Space-toggling already checked the segment; the segment stays checked
	// unless the user esc's out — verify esc still cancels cleanly.
	press(a, key("esc"))
	if a.segChecked["uid-var"] {
		t.Fatal("esc after a fetch error left the segment checked")
	}
}

func altS() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s"), Alt: true}
}

func TestSegmentsPauseResume(t *testing.T) {
	a := testAppSeeded(t, Options{
		ContextName: "test", SafetyLevel: "readonly", InitialView: "logs",
		SegmentSource: segStubSource(testSegmentList(), nil),
		WorkspaceSegments: []WorkspaceSegment{
			{Ref: "uid-b", Variables: []exec.FilterSegmentVariable{{Name: "env", Values: []string{"prod"}}}},
		},
	})
	if len(a.ds.segments) != 1 {
		t.Fatalf("seeding failed: %+v", a.ds.segments)
	}

	seqBefore := a.top().(*tableView).seq
	press(a, altS())
	if !a.segPaused || a.ds.segments != nil {
		t.Fatalf("pause: paused=%v ds.segments=%+v", a.segPaused, a.ds.segments)
	}
	if a.top().(*tableView).seq <= seqBefore {
		t.Error("pause did not refetch the open view")
	}
	if header := a.renderHeader(); !strings.Contains(header, "◌ team-checkout off") {
		t.Errorf("header misses the off pill: %q", header)
	}
	if echo := a.top().Echo(); strings.Contains(echo, "-S") {
		t.Errorf("paused Echo still carries -S: %q", echo)
	}
	if a.segmentsRejected("slos") {
		t.Error("paused segments filter nothing — nothing to reject on API views")
	}

	press(a, altS())
	if a.segPaused || len(a.ds.segments) != 1 || a.ds.segments[0].ID != "uid-b" {
		t.Fatalf("resume: paused=%v ds.segments=%+v", a.segPaused, a.ds.segments)
	}
	if vars := a.ds.segments[0].Variables; len(vars) != 1 || vars[0].Values[0] != "prod" {
		t.Fatalf("resume lost the bindings: %+v", a.ds.segments[0])
	}
	if header := a.renderHeader(); !strings.Contains(header, "◐ team-checkout") {
		t.Errorf("header misses the restored pill: %q", header)
	}
}

func TestSegmentsPauseWithNothingApplied(t *testing.T) {
	a := testApp(t, "logs")
	press(a, altS())
	if a.segPaused {
		t.Fatal("paused with nothing applied")
	}
	if !a.statusErr || !strings.Contains(a.status, "no segments selected") {
		t.Errorf("status = %q, want the nothing-selected hint", a.status)
	}
}

func TestSegmentPickerEnterResumesPausedSet(t *testing.T) {
	a := testApp(t, "logs")
	a.opts.SegmentSource = segStubSource(testSegmentList(), nil)
	press(a, key("S"))
	press(a, key(" "))
	press(a, key("enter"))
	press(a, altS()) // pause
	press(a, key("S"))
	press(a, key("enter")) // unchanged set, but paused — intent is resume
	if a.segPaused || len(a.ds.segments) != 1 {
		t.Fatalf("picker enter did not resume: paused=%v ds.segments=%+v", a.segPaused, a.ds.segments)
	}
}

func TestSegmentSummary(t *testing.T) {
	if got := segmentSummary(nil); got != "" {
		t.Errorf("empty summary = %q", got)
	}
	one := []SegmentOption{{UID: "u", Name: "a-very-long-segment-name-indeed"}}
	if got := segmentSummary(one); !strings.HasSuffix(got, "…") {
		t.Errorf("long name not truncated: %q", got)
	}
	three := []SegmentOption{{Name: "alpha"}, {Name: "beta"}, {Name: "gamma"}}
	if got := segmentSummary(three); got != "alpha +2" {
		t.Errorf("summary = %q, want %q", got, "alpha +2")
	}
	unnamed := []SegmentOption{{UID: "uid-x"}}
	if got := segmentSummary(unnamed); got != "uid-x" {
		t.Errorf("unnamed summary = %q, want the UID", got)
	}
}

func TestRewriteSegmentVarError(t *testing.T) {
	if rewriteSegmentVarError(nil) != nil {
		t.Error("nil error rewritten")
	}
	plain := errors.New("HTTP 500: something else")
	if rewriteSegmentVarError(plain) != plain {
		t.Error("unrelated error rewritten")
	}
	cli := fmt.Errorf("segment uid-b requires variable %q\n\n"+
		"Bind the variable inline on -S using URL-query syntax:\n\n"+
		"  dtctl query \"...\" -S \"uid-b?env=your-value-here\"", "env")
	got := rewriteSegmentVarError(cli).Error()
	if strings.Contains(got, "-S \"") || strings.Contains(got, "--segments-file") {
		t.Errorf("CLI flag examples survived: %q", got)
	}
	if !strings.Contains(got, `segment uid-b requires variable "env"`) || !strings.Contains(got, "v on it to pick values") {
		t.Errorf("rewritten error misses cause or remedy: %q", got)
	}
}

func TestInitialTimeframeFromWorkspace(t *testing.T) {
	a := testAppSeeded(t, Options{
		ContextName: "test", SafetyLevel: "readonly", InitialView: "logs",
		InitialTimeframe: "24h",
	})
	if a.tf.Label != "24h" {
		t.Errorf("tf = %q, want 24h", a.tf.Label)
	}
	if a.tfSel != 2 {
		t.Errorf("tfSel = %d, want the 24h picker index", a.tfSel)
	}
}

func TestStartupNotice(t *testing.T) {
	a := testAppSeeded(t, Options{
		ContextName: "test", SafetyLevel: "readonly", InitialView: "logs",
		StartupNotice: "workspace my-service: 1 segment, last 24h",
	})
	if a.statusErr || !strings.Contains(a.status, "workspace my-service") {
		t.Errorf("status = %q (err=%v), want the workspace notice", a.status, a.statusErr)
	}
}
