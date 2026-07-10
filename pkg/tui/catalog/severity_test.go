package catalog

import (
	"strings"
	"testing"
)

func TestSeverityBadgeAndClass(t *testing.T) {
	cases := []struct{ in, badge, class string }{
		{"1", "SEV1", "error"},
		{"2", "SEV2", "error"},
		{"3", "SEV3", "warn"},
		{"4", "SEV4", "warn"},
		{"5", "SEV5", "dim"},
		{"", "", ""},
		{"weird", "weird", ""},
	}
	for _, c := range cases {
		if got := SeverityBadge(c.in); got != c.badge {
			t.Errorf("SeverityBadge(%q) = %q, want %q", c.in, got, c.badge)
		}
		if got := ClassSeverityBadge(SeverityBadge(c.in)); got != c.class {
			t.Errorf("ClassSeverityBadge(SEV %q) = %q, want %q", c.in, got, c.class)
		}
	}
}

func TestClassLevelSharedWords(t *testing.T) {
	cases := map[string]string{
		"ERROR": "error", "FATAL": "error", "FAILED": "error",
		"WARN": "warn", "warning": "warn",
		"INFO": "ok", "SUCCEEDED": "ok",
		"DEBUG": "dim", "TRACE": "dim",
		"ACTIVE": "", "anything": "",
	}
	for in, want := range cases {
		if got := ClassLevel(in); got != want {
			t.Errorf("ClassLevel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSeverityFieldClass(t *testing.T) {
	cases := []struct{ field, val, want string }{
		{"loglevel", "ERROR", "error"},
		{"status", "WARN", "warn"},           // dt.system.events status
		{"event.status", "ACTIVE", "error"},  // davis event/problem status
		{"event.status", "CLOSED", "dim"},
		{"event.severity", "4", "warn"},      // raw ordinal
		{"event.severity", "SEV4", "warn"},   // already badged (table cells)
		{"name", "ERROR", ""},                // non-severity fields stay plain
	}
	for _, c := range cases {
		if got := SeverityFieldClass(c.field, c.val); got != c.want {
			t.Errorf("SeverityFieldClass(%q, %q) = %q, want %q", c.field, c.val, got, c.want)
		}
	}
	if !SeverityField("loglevel") || SeverityField("content") {
		t.Error("SeverityField predicate misclassifies")
	}
}

func TestEventsLenses(t *testing.T) {
	spec := Lookup("events")
	if spec == nil || len(spec.Lenses) != 5 {
		t.Fatalf("events spec should carry 5 lenses, got %+v", spec)
	}
	tf := Timeframe{Label: "2h"}

	// Default lens: the Davis events table, entity-scopable.
	all := spec.Query(Scope{Timeframe: tf})
	if !strings.Contains(all, "fetch events") || strings.Contains(all, "dt.system.events") {
		t.Errorf("all lens query:\n%s", all)
	}
	scoped := spec.Query(Scope{Timeframe: tf, Entity: &Entity{ID: "HOST-1", Type: "HOST"}})
	if !strings.Contains(scoped, "dt.smartscape.host") {
		t.Errorf("davis lenses must compose the entity scope:\n%s", scoped)
	}

	// alerts drops the SEV5 chatter.
	alerts := spec.Query(Scope{Timeframe: tf, Lens: 1})
	if !strings.Contains(alerts, "event.severity <= 4") {
		t.Errorf("alerts lens query:\n%s", alerts)
	}

	// changes reuses the change-event filter.
	changes := spec.Query(Scope{Timeframe: tf, Lens: 2})
	if !strings.Contains(changes, "SDLC_EVENT") || !strings.Contains(changes, "CUSTOM_DEPLOYMENT") {
		t.Errorf("changes lens query:\n%s", changes)
	}

	// system and audit swap the table to dt.system.events; the entity scope
	// deliberately does not compose (those records carry no entity fields).
	system := spec.Query(Scope{Timeframe: tf, Lens: 3, Entity: &Entity{ID: "HOST-1", Type: "HOST"}})
	if !strings.Contains(system, "fetch dt.system.events") ||
		!strings.Contains(system, `not(in(event.kind, {"AUDIT_EVENT", "QUERY_EXECUTION_EVENT"}))`) ||
		strings.Contains(system, "dt.smartscape.host") {
		t.Errorf("system lens query:\n%s", system)
	}
	audit := spec.Query(Scope{Timeframe: tf, Lens: 4})
	if !strings.Contains(audit, `event.kind == "AUDIT_EVENT"`) {
		t.Errorf("audit lens query:\n%s", audit)
	}
	for _, q := range []string{all, alerts, changes, system, audit} {
		if !strings.Contains(q, "sort timestamp desc") {
			t.Errorf("every events lens sorts newest-first:\n%s", q)
		}
	}

	// The default columns carry colored severity and status.
	var sevCol, statusCol *Column
	for i := range spec.Columns {
		switch spec.Columns[i].Title {
		case "SEV":
			sevCol = &spec.Columns[i]
		case "STATUS":
			statusCol = &spec.Columns[i]
		}
	}
	if sevCol == nil || sevCol.Class == nil || sevCol.Class("SEV3") != "warn" {
		t.Error("events SEV column must class by severity badge")
	}
	if statusCol == nil || statusCol.Class == nil || statusCol.Class("ACTIVE") != "error" {
		t.Error("events STATUS column must class ACTIVE as error")
	}

	// System lens columns treat the log-shaped status words like log levels.
	sys := spec.LensAt(3)
	if sys.Columns == nil {
		t.Fatal("system lens must carry its own columns")
	}
	for _, c := range sys.Columns {
		if c.Title == "STATUS" {
			if c.Class == nil || c.Class("WARN") != "warn" || c.Class("SUCCEEDED") != "ok" {
				t.Error("system STATUS column must class like a log level")
			}
		}
	}
}

func TestBizeventPriorityFields(t *testing.T) {
	rec := map[string]any{
		"event.kind":     "BIZ_EVENT",
		"event.type":     "standardised-slo",
		"event.provider": "sre-mapper",
		"timestamp":      "2026-07-10T12:00:00Z",
		"slo_name":       "availability",
	}
	fields := PriorityFields(rec)
	joined := strings.Join(fields, " ")
	for _, want := range []string{"event.type", "event.provider", "timestamp"} {
		if !strings.Contains(joined, want) {
			t.Errorf("bizevent priority fields missing %s: %v", want, fields)
		}
	}
	// Records with an event.kind (Davis) must not take the bizevent branch.
	if f := PriorityFields(map[string]any{"event.kind": "DAVIS_EVENT", "event.provider": "x"}); f[0] != "event.name" {
		t.Errorf("davis event misrouted to bizevent fields: %v", f)
	}
}

func TestAWSCensusIsEntitiesPreset(t *testing.T) {
	aws, entities := Lookup("aws"), Lookup("entities")
	awsQ, entQ := aws.Query(Scope{}), entities.Query(Scope{})
	if !strings.Contains(awsQ, `startsWith(type, "AWS_")`) {
		t.Errorf("aws census filter missing:\n%s", awsQ)
	}
	if strings.Contains(entQ, "AWS_") {
		t.Errorf("entities census must stay unfiltered:\n%s", entQ)
	}
	if aws.EnterTarget != "resources" || entities.EnterTarget != "resources" {
		t.Error("census views should enter the generic resources browser")
	}
}
