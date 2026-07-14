package catalog

import (
	"strings"
	"testing"
	"time"
)

// The vulnerabilities list has two query shapes: the VULNERABILITY-level
// rollup when unscoped, the ENTITY-level rollup (which alone carries entity
// ids) when pinned. These tests pin both shapes and the honesty of the
// scope claims.

func TestVulnsQueryUnscopedReadsVulnerabilityLevel(t *testing.T) {
	q := vulnsSpec.Query(fixtureScope(nil))
	for _, want := range []string{
		"fetch security.events, from:now() - 24h",
		`event.type == "VULNERABILITY_STATE_REPORT_EVENT" and event.level == "VULNERABILITY"`,
		"affected = takeLast(affected_entities.count)",
		"exposure = takeLast(vulnerability.davis_assessment.exposure_status)",
		"exploit = takeLast(vulnerability.davis_assessment.exploit_status)",
		"fix = takeLast(vulnerability.is_fix_available)",
		"muted = takeLast(vulnerability.mute.status)",
		"code_location = takeLast(vulnerability.code_location.name)",
		"by:{vulnerability.id}",
		`| filter status == "OPEN" and muted != "MUTED"`,
		"| sort score desc",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("unscoped vulns query missing %q:\n%s", want, q)
		}
	}
	if strings.Contains(q, "component =") {
		t.Errorf("unscoped vulns query must not roll up the per-entity component:\n%s", q)
	}
}

func TestVulnsQueryScopedReadsEntityLevel(t *testing.T) {
	cases := []struct {
		entity Entity
		arm    string
	}{
		{Entity{ID: "SERVICE-42", Type: "SERVICE"}, `in("SERVICE-42", related_entities.services.ids)`},
		{Entity{ID: "HOST-1", Type: "HOST"}, `(affected_entity.id == "HOST-1" or in("HOST-1", related_entities.hosts.ids))`},
		// A modern PROCESS id pins by its legacy PGI twin (shared hex suffix).
		{Entity{ID: "PROCESS-AB12", Type: "PROCESS"}, `in("PROCESS_GROUP_INSTANCE-AB12", affected_entity.affected_processes.ids)`},
		{Entity{ID: "PROCESS_GROUP-C3", Type: "PROCESS_GROUP"}, `affected_entity.id == "PROCESS_GROUP-C3"`},
	}
	for _, c := range cases {
		q := vulnsSpec.Query(fixtureScope(&c.entity))
		if !strings.Contains(q, `event.level == "ENTITY"`) {
			t.Errorf("%s-scoped query must read ENTITY-level reports:\n%s", c.entity.Type, q)
		}
		if !strings.Contains(q, c.arm) {
			t.Errorf("%s-scoped query missing filter arm %q:\n%s", c.entity.Type, c.arm, q)
		}
		if !strings.Contains(q, "component = takeLast(affected_entity.vulnerable_component.name)") {
			t.Errorf("%s-scoped query must roll up the vulnerable component:\n%s", c.entity.Type, q)
		}
	}
}

func TestVulnsQueryIgnoresUnscopableTypes(t *testing.T) {
	unscoped := vulnsSpec.Query(fixtureScope(nil))
	for _, typ := range []string{"K8S_DEPLOYMENT", "K8S_CLUSTER", "FRONTEND", "CONTAINER"} {
		scoped := vulnsSpec.Query(fixtureScope(&Entity{ID: typ + "-1", Type: typ}))
		if scoped != unscoped {
			t.Errorf("a %s pin must not change the vulns query", typ)
		}
		if vulnsSpec.Scopable(Entity{ID: typ + "-1", Type: typ}) {
			t.Errorf("Scopable must refuse %s", typ)
		}
	}
}

func TestVulnsLenses(t *testing.T) {
	muted := vulnsSpec.Query(Scope{Timeframe: Timeframe{Label: "2h", Dur: 2 * time.Hour}, Lens: 1})
	if !strings.Contains(muted, `| filter status == "OPEN" and muted == "MUTED"`) {
		t.Errorf("muted lens filter missing:\n%s", muted)
	}
	all := vulnsSpec.Query(Scope{Timeframe: Timeframe{Label: "2h", Dur: 2 * time.Hour}, Lens: 2})
	if strings.Contains(all, "| filter status") {
		t.Errorf("all lens must not filter by status:\n%s", all)
	}
	// The lens filter lands AFTER the summarize — it references the aliases.
	open := vulnsSpec.Query(Scope{Timeframe: Timeframe{Label: "2h", Dur: 2 * time.Hour}})
	if strings.Index(open, "summarize") > strings.Index(open, `status == "OPEN"`) {
		t.Errorf("lens filter must follow the summarize:\n%s", open)
	}
}

func TestVulnsScopeColumnsSwapAffectedForComponent(t *testing.T) {
	scoped := vulnsSpec.ScopeColumns(Scope{Entity: &Entity{ID: "SERVICE-1", Type: "SERVICE"}})
	if scoped == nil {
		t.Fatal("SERVICE scope must swap in the component column set")
	}
	var titles []string
	for _, c := range scoped {
		titles = append(titles, c.Title)
	}
	joined := strings.Join(titles, ",")
	if !strings.Contains(joined, "COMPONENT") || strings.Contains(joined, "AFFECTED") {
		t.Errorf("scoped columns = %s, want COMPONENT instead of AFFECTED", joined)
	}
	if cols := vulnsSpec.ScopeColumns(Scope{}); cols != nil {
		t.Error("unscoped view keeps the static columns")
	}
	if cols := vulnsSpec.ScopeColumns(Scope{Entity: &Entity{ID: "K8S_POD-1", Type: "K8S_POD"}}); cols != nil {
		t.Error("an unscopable pin must not swap columns")
	}
}

func TestVulnTriageCells(t *testing.T) {
	rec := map[string]any{
		"exposure": "PUBLIC_NETWORK", "exploit": "AVAILABLE", "fix": true,
		"stack": "CODE_LIBRARY",
	}
	if got := exposureCell(rec); got != "public" || classExposure(got) != "error" {
		t.Errorf("public exposure cell = %q class %q", got, classExposure(got))
	}
	if got := exploitCell(rec); got != "avail" || classExploit(got) != "error" {
		t.Errorf("exploit cell = %q class %q", got, classExploit(got))
	}
	if got := fixCell(rec); got != "yes" {
		t.Errorf("fix cell = %q", got)
	}
	if got := vulnStack("CODE_LIBRARY"); got != "library" {
		t.Errorf("stack cell = %q", got)
	}

	// Assessed-and-clear reads as a dim dash; unassessed stays blank.
	if got := exposureCell(map[string]any{"exposure": "NOT_DETECTED"}); got != "-" || classExposure(got) != "dim" {
		t.Errorf("not-detected exposure = %q class %q", got, classExposure(got))
	}
	if got := exposureCell(map[string]any{"exposure": "NOT_AVAILABLE"}); got != "" {
		t.Errorf("unassessed exposure must stay blank, got %q", got)
	}
	if got := exploitCell(map[string]any{"exploit": "NOT_AVAILABLE"}); got != "" {
		t.Errorf("no-exploit cell must stay blank, got %q", got)
	}
	if got := fixCell(map[string]any{"fix": false}); got != "" {
		t.Errorf("no-fix cell must stay blank, got %q", got)
	}
}

// --- attacks ------------------------------------------------------------

func TestAttacksQueryComposition(t *testing.T) {
	unscoped := attacksSpec.Query(fixtureScope(nil))
	for _, want := range []string{
		"fetch security.events, from:now() - 2h", // no floor: attacks are timely events
		`event.type == "DETECTION_FINDING" and product.name == "Runtime Application Protection"`,
		"| sort timestamp desc",
	} {
		if !strings.Contains(unscoped, want) {
			t.Errorf("attacks query missing %q:\n%s", want, unscoped)
		}
	}

	// A code location (Scope.Arg — the vulnerability page's attacks tab) wins
	// over entity scoping: it IS the exact attack↔vulnerability linkage.
	located := attacksSpec.Query(Scope{Timeframe: Timeframe{Label: "2h", Dur: 2 * time.Hour},
		Arg: "Proxy.run(String):89", Entity: &Entity{ID: "HOST-1", Type: "HOST"}})
	if !strings.Contains(located, `| filter vulnerability.code_location.name == "Proxy.run(String):89"`) {
		t.Errorf("code-location arm missing:\n%s", located)
	}
	if strings.Contains(located, "dt.smartscape.host") {
		t.Errorf("code-location scope must not also compose the entity filter:\n%s", located)
	}

	scoped := attacksSpec.Query(fixtureScope(&Entity{ID: "PROCESS_GROUP-77", Type: "PROCESS_GROUP"}))
	if !strings.Contains(scoped, `dt.entity.process_group == "PROCESS_GROUP-77"`) {
		t.Errorf("PROCESS_GROUP pin must match the legacy field attacks carry:\n%s", scoped)
	}
}

func TestAttacksScopable(t *testing.T) {
	for _, typ := range []string{"SERVICE", "HOST", "PROCESS", "CONTAINER", "PROCESS_GROUP", "K8S_POD", "K8S_DEPLOYMENT"} {
		if !attacksSpec.Scopable(Entity{Type: typ}) {
			t.Errorf("attacks must accept a %s pin", typ)
		}
	}
	for _, typ := range []string{"FRONTEND", "DB_INSTANCE_POSTGRES", "GENAI_MODEL"} {
		if attacksSpec.Scopable(Entity{Type: typ}) {
			t.Errorf("attacks must refuse a %s pin", typ)
		}
	}
}

func TestShortCodeLocation(t *testing.T) {
	cases := map[string]string{
		"org.dynatrace.ssrfservice.ProxyController.proxyUrlWithCurl(String):163": "ProxyController.proxyUrlWithCurl(String):163",
		// .NET async state machines keep their +<Method>d__N segment.
		"MembershipService.Controllers.MembershipController+<GetMembershipStatus>d__3.MoveNext()": "MembershipController+<GetMembershipStatus>d__3.MoveNext()",
		"Class.method():5": "Class.method():5",
		"noDotsAtAll":      "noDotsAtAll",
		"":                 "",
	}
	for in, want := range cases {
		if got := ShortCodeLocation(in); got != want {
			t.Errorf("ShortCodeLocation(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassAttackAction(t *testing.T) {
	if classAttackAction("Blocked") != "ok" || classAttackAction("Audited") != "warn" || classAttackAction("") != "" {
		t.Error("attack action classes: Blocked→ok, Audited→warn, else none")
	}
}

// --- vulnerability page tabs ---------------------------------------------

func TestVulnEntitiesSpecQuery(t *testing.T) {
	q := VulnEntitiesSpec.Query(Scope{Arg: "111", Timeframe: Timeframe{Label: "2h", Dur: 2 * time.Hour}})
	for _, want := range []string{
		"from:now() - 24h", // state-report floor
		`event.level == "ENTITY" and vulnerability.id == "111"`,
		"| dedup affected_entity.id, sort:{timestamp desc}",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("entities query missing %q:\n%s", want, q)
		}
	}
}

func TestVulnAffectedEntity(t *testing.T) {
	host := VulnAffectedEntity(map[string]any{
		"affected_entity.id": "HOST-1", "affected_entity.name": "web-01", "affected_entity.type": "HOST"})
	if host == nil || host.ID != "HOST-1" || host.Type != "HOST" {
		t.Errorf("host entity = %+v", host)
	}

	// A process-group row drills through its first process instance, modern id.
	pg := VulnAffectedEntity(map[string]any{
		"affected_entity.id":                       "PROCESS_GROUP-AA",
		"affected_entity.name":                     "checkout *",
		"affected_entity.type":                     "PROCESS_GROUP",
		"affected_entity.affected_processes.ids":   []any{"PROCESS_GROUP_INSTANCE-BB12"},
		"affected_entity.affected_processes.names": []any{"checkout (pod-1)"},
	})
	if pg == nil || pg.ID != "PROCESS-BB12" || pg.Type != "PROCESS" || pg.Name != "checkout (pod-1)" {
		t.Errorf("process entity = %+v", pg)
	}

	if e := VulnAffectedEntity(map[string]any{
		"affected_entity.id": "PROCESS_GROUP-AA", "affected_entity.type": "PROCESS_GROUP"}); e != nil {
		t.Errorf("no process instances → no drill, got %+v", e)
	}
	if e := VulnAffectedEntity(map[string]any{
		"affected_entity.id": "KUBERNETES_NODE-1", "affected_entity.type": "KUBERNETES_NODE"}); e != nil {
		t.Errorf("k8s node ids don't cross the era divide → no drill, got %+v", e)
	}
}

func TestVulnAttacksSpecStaysHonestWithoutEntities(t *testing.T) {
	tf := Timeframe{Label: "2h", Dur: 2 * time.Hour}
	empty := VulnAttacksSpec.Query(Scope{Arg: "111", Timeframe: tf})
	if !strings.Contains(empty, "| filter false") {
		t.Errorf("no resolved entities must keep the tab empty, not tenant-wide:\n%s", empty)
	}
	scoped := VulnAttacksSpec.Query(Scope{Arg: "111", Timeframe: tf,
		Entities: []Entity{{ID: "PROCESS_GROUP-77", Type: "PROCESS_GROUP"}}})
	if !strings.Contains(scoped, `dt.entity.process_group == "PROCESS_GROUP-77"`) {
		t.Errorf("hop-resolved entities must scope the detections:\n%s", scoped)
	}
	if strings.Contains(scoped, "filter false") {
		t.Errorf("scoped query must not carry the empty guard:\n%s", scoped)
	}
}

func TestVulnAttackHop(t *testing.T) {
	if q := VulnAttackHopQuery(Scope{Arg: "111"}); !strings.Contains(q, `vulnerability.id == "111"`) {
		t.Errorf("hop query missing the id filter:\n%s", q)
	}
	if q := VulnAttackHopQuery(Scope{Arg: "111", Entities: []Entity{{ID: "X"}}}); q != "" {
		t.Errorf("an already-scoped view needs no hop, got:\n%s", q)
	}
	if q := VulnAttackHopQuery(Scope{}); q != "" {
		t.Errorf("no vulnerability id → no hop, got:\n%s", q)
	}

	ents := VulnAttackHopEntities(Scope{}, []map[string]any{
		{"affected_entity.id": "PROCESS_GROUP-1", "affected_entity.type": "PROCESS_GROUP", "affected_entity.name": "pg"},
		{"affected_entity.id": "PROCESS_GROUP-1", "affected_entity.type": "PROCESS_GROUP"}, // dupe
		{"affected_entity.id": "HOST-2", "affected_entity.type": "HOST"},
		{"affected_entity.id": "KUBERNETES_NODE-3", "affected_entity.type": "KUBERNETES_NODE"}, // era mismatch
	})
	if len(ents) != 2 || ents[0].ID != "PROCESS_GROUP-1" || ents[1].ID != "HOST-2" {
		t.Errorf("hop entities = %+v", ents)
	}
}

func TestVulnEntryPointsSpecQuery(t *testing.T) {
	q := VulnEntryPointsSpec.Query(Scope{Arg: "111", Timeframe: Timeframe{Label: "2h", Dur: 2 * time.Hour}})
	for _, want := range []string{
		"| expand entry_point = entry_points.entry_point_jsons",
		"| fields affected_entity.name, entry_point",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("entry points query missing %q:\n%s", want, q)
		}
	}
}

func TestParseEntryPoint(t *testing.T) {
	ep := ParseEntryPoint(map[string]any{"entry_point": `{"entry_point.payload":"SELECT * FROM t WHERE id = *****","entry_point.url.path":"/svc/10","entry_point.user_controlled_inputs":[{"user_controlled_input.is_malicious":false},{"user_controlled_input.is_malicious":true}]}`})
	if ep.Path != "/svc/10" || !strings.HasPrefix(ep.Payload, "SELECT") || ep.Inputs != 2 || !ep.Malicious {
		t.Errorf("parsed entry point = %+v", ep)
	}
	if ep := ParseEntryPoint(map[string]any{"entry_point": "{not json"}); ep != (EntryPoint{}) {
		t.Errorf("malformed JSON must yield the zero value, got %+v", ep)
	}
	if ep := ParseEntryPoint(map[string]any{}); ep != (EntryPoint{}) {
		t.Errorf("missing field must yield the zero value, got %+v", ep)
	}
}

func TestVulnTimelineSpecQuery(t *testing.T) {
	q := VulnTimelineSpec.Query(Scope{Arg: "111", Timeframe: Timeframe{Label: "2h", Dur: 2 * time.Hour}})
	for _, want := range []string{
		"from:now() - 30d", // transitions are sparse
		`in(event.type, {"VULNERABILITY_STATUS_CHANGE_EVENT", "VULNERABILITY_ASSESSMENT_CHANGE_EVENT"})`,
		`vulnerability.id == "111"`,
	} {
		if !strings.Contains(q, want) {
			t.Errorf("timeline query missing %q:\n%s", want, q)
		}
	}
	if classTransition("CLOSE") != "ok" || classTransition("REOPEN") != "warn" || classTransition("MUTE") != "dim" {
		t.Error("transition classes: CLOSE→ok, REOPEN→warn, MUTE→dim")
	}
}

func TestPGIIDPrefixSwap(t *testing.T) {
	if got := pgiID("PROCESS-EBC4A25674545389"); got != "PROCESS_GROUP_INSTANCE-EBC4A25674545389" {
		t.Errorf("pgiID = %q", got)
	}
	// Legacy-era ids pass through untouched.
	if got := pgiID("PROCESS_GROUP_INSTANCE-EBC4A25674545389"); got != "PROCESS_GROUP_INSTANCE-EBC4A25674545389" {
		t.Errorf("pgiID legacy passthrough = %q", got)
	}
}
