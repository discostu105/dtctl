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

func TestPGIIDPrefixSwap(t *testing.T) {
	if got := pgiID("PROCESS-EBC4A25674545389"); got != "PROCESS_GROUP_INSTANCE-EBC4A25674545389" {
		t.Errorf("pgiID = %q", got)
	}
	// Legacy-era ids pass through untouched.
	if got := pgiID("PROCESS_GROUP_INSTANCE-EBC4A25674545389"); got != "PROCESS_GROUP_INSTANCE-EBC4A25674545389" {
		t.Errorf("pgiID legacy passthrough = %q", got)
	}
}
