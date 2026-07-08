package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
)

// Regression tests for the adversarial-review findings: a pin must never be
// claimed by a view whose query can't compose it, the timeframe is global,
// home drops stale results, and input-focused footers don't advertise dead
// keys.

func serviceRow() map[string]any {
	return map[string]any{"id": "SERVICE-1", "name": "checkout", "type": "SERVICE"}
}

// Finding #1: pinning a SERVICE then jumping to :pods must NOT scope the pod
// list (k8sScopeFilter can't compose a SERVICE) — and the crumb must not claim
// it does.
func TestIncompatiblePinIsNotAppliedOrClaimed(t *testing.T) {
	a := testApp(t, "services")
	seedRows(t, a, []map[string]any{serviceRow()})
	press(a, key(".")) // pin the service
	if a.pin == nil || a.pin.Type != "SERVICE" {
		t.Fatalf("pin = %+v", a.pin)
	}

	press(a, key("4")) // hotkey → pods
	pods, ok := a.top().(*tableView)
	if !ok || pods.spec.Name != "pods" {
		t.Fatalf("hotkey 4 → %v", a.top().Crumb())
	}
	if pods.scope.Entity != nil {
		t.Errorf("SERVICE pin must not be injected into pods scope: %+v", pods.scope.Entity)
	}
	if strings.Contains(pods.dql, "SERVICE") || strings.Contains(pods.dql, "checkout") {
		t.Errorf("pods query must not be filtered by an incompatible pin:\n%s", pods.dql)
	}
	if strings.Contains(pods.Crumb(), "checkout") {
		t.Errorf("crumb must not claim a scope that was not applied: %q", pods.Crumb())
	}
	if a.status == "" || !a.statusErr {
		t.Errorf("user should be told the pin was ignored, status = %q", a.status)
	}
}

// A compatible pin (K8S_NAMESPACE → pods) is still applied and claimed.
func TestCompatiblePinIsApplied(t *testing.T) {
	a := testApp(t, "namespaces")
	seedRows(t, a, []map[string]any{{"id": "K8S_NAMESPACE-1", "name": "shop", "type": "K8S_NAMESPACE"}})
	press(a, key("."))
	press(a, key("4")) // pods
	pods := a.top().(*tableView)
	if pods.scope.Entity == nil || pods.scope.Entity.Name != "shop" {
		t.Fatalf("namespace pin should scope pods: %+v", pods.scope.Entity)
	}
	if !strings.Contains(pods.dql, `k8s.namespace.name == "shop"`) {
		t.Errorf("pods not scoped to pinned namespace:\n%s", pods.dql)
	}
}

// Finding #2: pinning a HOST then :traces must not compose a span filter that
// matches nothing — the pin is refused and the traces list is unscoped.
func TestTracesPinGuardsNonSpanScopableTypes(t *testing.T) {
	a := testApp(t, "hosts")
	seedRows(t, a, []map[string]any{hostRow()})
	press(a, key(".")) // pin the host
	press(a, key("6")) // hotkey → traces
	tr, ok := a.top().(*tableView)
	if !ok || tr.spec.Name != "traces" {
		t.Fatalf("hotkey 6 → %v", a.top().Crumb())
	}
	if tr.scope.Entity != nil {
		t.Errorf("HOST pin must not scope traces (spans carry no host field): %+v", tr.scope.Entity)
	}
	if strings.Contains(tr.dql, "dt.smartscape.host") {
		t.Errorf("traces query must not filter by a field spans lack:\n%s", tr.dql)
	}
	if a.status == "" || !a.statusErr {
		t.Errorf("user should be told the host pin can't scope traces, status = %q", a.status)
	}
}

// CanScope is honest for a view that ignores scope entirely (vulnerabilities).
func TestVulnsIgnoreScopeSoPinIsNotClaimed(t *testing.T) {
	spec := catalog.Lookup("vulnerabilities")
	tf := catalog.Timeframe{Label: "2h", Dur: 2 * time.Hour}
	if spec.CanScope(tf, catalog.Entity{ID: "HOST-1", Type: "HOST"}) {
		t.Error("vulnerabilities query ignores the entity — CanScope must be false")
	}
}

// Finding #3: the timeframe picker applies the new window to every view on the
// stack, not only the top one.
func TestTimeframeAppliesToWholeStack(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{problemRow()})
	press(a, key("l")) // drill to logs (depth 2)
	if len(a.stack) != 2 {
		t.Fatalf("expected depth 2, got %d", len(a.stack))
	}

	press(a, key("t"))
	press(a, key("3")) // 24h
	if a.tf.Label != "24h" {
		t.Fatalf("tf = %s", a.tf.Label)
	}
	// The covered problems view (below logs) must also have adopted 24h.
	problems := a.stack[0].(*tableView)
	if problems.scope.Timeframe.Label != "24h" {
		t.Errorf("covered view kept old window: %s", problems.scope.Timeframe.Label)
	}
	if !strings.Contains(problems.dql, "now() - 24h") {
		t.Errorf("covered view query not refetched with new window:\n%s", problems.dql)
	}
}

// Finding #4: a stale in-flight home-panel result (older seq) must not
// overwrite a newer refresh.
func TestHomePanelDropsStaleResults(t *testing.T) {
	a := testApp(t, "home")
	hv := a.top().(*homeView)
	staleSeq := hv.seq

	// A new refresh bumps the generation.
	deliver(a, hv.Refresh())
	if hv.seq == staleSeq {
		t.Fatal("Refresh should bump seq")
	}

	// A late result tagged with the old seq is ignored.
	hv.Update(dataMsg{owner: panelOwner{v: hv, idx: 1}, seq: staleSeq,
		records: []map[string]any{{"svc": "ghost"}}})
	if len(hv.panels[1].records) != 0 {
		t.Errorf("stale result should be dropped, got %d records", len(hv.panels[1].records))
	}

	// A current-seq result is accepted.
	hv.Update(dataMsg{owner: panelOwner{v: hv, idx: 1}, seq: hv.seq,
		records: []map[string]any{{"svc": "real"}}})
	if len(hv.panels[1].records) != 1 {
		t.Errorf("current result should be accepted, got %d", len(hv.panels[1].records))
	}
}

// Finding #6: with empty command-bar input, enter opens the highlighted
// suggestion rather than silently closing.
func TestCommandBarEmptyEnterOpensHighlighted(t *testing.T) {
	a := testApp(t, "problems")
	press(a, key(":"))
	// Cycle to the second suggestion, then enter with no text typed. The
	// palette lists the bespoke screens first (home, query, nav), so index 1
	// is "query" — the crumb, not the view type, carries the assertion.
	press(a, tea.KeyMsg{Type: tea.KeyTab})
	want := a.cmdMatches[a.cmdSel].Name
	press(a, key("enter"))
	if a.cmdActive {
		t.Fatal("enter should close the command bar")
	}
	if a.top().Crumb() != want {
		t.Fatalf("empty enter should open highlighted %q, got %v", want, a.top().Crumb())
	}
}

// Finding #7: '0' → home works from a detail page (only 1..N switch tabs).
func TestDetailPageZeroHotkeyReachesHome(t *testing.T) {
	a := testApp(t, "hosts")
	seedRows(t, a, []map[string]any{hostRow()})
	press(a, key("enter")) // open host detail
	if _, ok := a.top().(*detailView); !ok {
		t.Fatalf("expected detail page, got %T", a.top())
	}
	press(a, key("0")) // hotkey home — must not be swallowed as a tab
	if _, ok := a.top().(*homeView); !ok {
		t.Fatalf("'0' on a detail page should jump home, got %T", a.top())
	}
}

// Finding #8: while a filter input is focused, the footer shows input-mode
// hints, not the global keys that would just type characters.
func TestFooterHintsAdaptToInputFocus(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{problemRow()})
	press(a, key("/")) // focus the filter
	footer := a.renderFooter()
	if strings.Contains(footer, "quit") || strings.Contains(footer, "timeframe") {
		t.Errorf("footer must not advertise global keys while typing:\n%s", footer)
	}
	if !strings.Contains(footer, "server search") {
		t.Errorf("footer should show input-mode hints:\n%s", footer)
	}
}
