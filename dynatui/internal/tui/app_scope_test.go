package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
)

func TestPinScopesCommandBarJumps(t *testing.T) {
	a := testApp(t, "pods")
	seedRows(t, a, []map[string]any{podRow("checkout-1", "shop", "Running", 0)})

	press(a, key("."))
	if a.pin == nil || a.pin.ID != "K8S_POD-checkout-1" {
		t.Fatalf("pin = %+v", a.pin)
	}

	// A command-bar jump to a signal view inherits the pin.
	press(a, key(":"))
	for _, r := range "logs" {
		press(a, key(string(r)))
	}
	press(a, key("enter"))
	logs := a.top().(*tableView)
	if logs.scope.Entity == nil || logs.scope.Entity.ID != "K8S_POD-checkout-1" {
		t.Fatalf("pinned jump not scoped: %+v", logs.scope.Entity)
	}
	if !strings.Contains(logs.dql, `k8s.pod.name == "checkout-1"`) {
		t.Errorf("pinned logs dql missing pod name filter:\n%s", logs.dql)
	}

	// ctrl+x unpins.
	press(a, key("ctrl+x"))
	if a.pin != nil {
		t.Error("ctrl+x should unpin")
	}
}

// Finding #1: pinning an entity no scope filter composes (a PROCESS on
// :pods) must NOT scope the pod list — and the crumb must not claim it does.
func TestIncompatiblePinIsNotAppliedOrClaimed(t *testing.T) {
	a := testApp(t, "processes")
	seedRows(t, a, []map[string]any{{"id": "PROCESS-1", "name": "engine", "type": "PROCESS"}})
	press(a, key(".")) // pin the process
	if a.pin == nil || a.pin.Type != "PROCESS" {
		t.Fatalf("pin = %+v", a.pin)
	}

	press(a, key("4")) // hotkey → pods
	pods, ok := a.top().(*tableView)
	if !ok || pods.spec.Name != "pods" {
		t.Fatalf("hotkey 4 → %v", a.top().Crumb())
	}
	if pods.scope.Entity != nil {
		t.Errorf("PROCESS pin must not be injected into pods scope: %+v", pods.scope.Entity)
	}
	if strings.Contains(pods.dql, "PROCESS") || strings.Contains(pods.dql, "engine") {
		t.Errorf("pods query must not be filtered by an incompatible pin:\n%s", pods.dql)
	}
	if strings.Contains(pods.Crumb(), "engine") {
		t.Errorf("crumb must not claim a scope that was not applied: %q", pods.Crumb())
	}
	if a.status == "" || !a.statusErr {
		t.Errorf("user should be told the pin was ignored, status = %q", a.status)
	}
}

// A SERVICE pin on :pods composes since the deployment surface landed: the
// pod list joins through the service's runs_on edges, and the crumb claims
// the applied scope.
func TestServicePinScopesPodsViaEdgeJoin(t *testing.T) {
	a := testApp(t, "services")
	seedRows(t, a, []map[string]any{serviceRow()})
	press(a, key(".")) // pin the service
	press(a, key("4")) // hotkey → pods
	pods, ok := a.top().(*tableView)
	if !ok || pods.spec.Name != "pods" {
		t.Fatalf("hotkey 4 → %v", a.top().Crumb())
	}
	if pods.scope.Entity == nil || pods.scope.Entity.ID != "SERVICE-1" {
		t.Fatalf("service pin should scope pods: %+v", pods.scope.Entity)
	}
	for _, want := range []string{
		`source_id == toSmartscapeId("SERVICE-1") and type == "runs_on"`,
		"| fieldsRemove right.target_id",
	} {
		if !strings.Contains(pods.dql, want) {
			t.Errorf("pods dql missing %q:\n%s", want, pods.dql)
		}
	}
	if !strings.Contains(pods.Crumb(), "checkout") {
		t.Errorf("crumb should claim the applied scope: %q", pods.Crumb())
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

// CanScope stays honest on the vulnerabilities view: SERVICE/HOST/PROCESS
// pins compose an ENTITY-level filter now, while K8s types — whose ids don't
// match the related_entities id era — must still refuse the pin.
func TestVulnsScopeClaimsMatchTheFilter(t *testing.T) {
	spec := catalog.Lookup("vulnerabilities")
	tf := catalog.Timeframe{Label: "2h", Dur: 2 * time.Hour}
	for _, e := range []catalog.Entity{
		{ID: "HOST-1", Type: "HOST"},
		{ID: "SERVICE-1", Type: "SERVICE"},
		{ID: "PROCESS-AB12", Type: "PROCESS"},
	} {
		if !spec.CanScope(tf, e) {
			t.Errorf("vulnerabilities must accept a %s pin now", e.Type)
		}
	}
	if spec.CanScope(tf, catalog.Entity{ID: "K8S_DEPLOYMENT-1", Name: "checkout", Type: "K8S_DEPLOYMENT"}) {
		t.Error("K8s ids don't match the related_entities id era — the pin must be refused")
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
