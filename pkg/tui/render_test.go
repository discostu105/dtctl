package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// Styles render as plain text without a TTY, so assertions here are on
// content and classification, not escape codes.

func TestRenderValueClassifiesEntityIDs(t *testing.T) {
	v := renderValue("dt.smartscape.host", "HOST-0011223344556677", 80)
	if v.entity == nil || v.entity.ID != "HOST-0011223344556677" || v.entity.Type != "HOST" {
		t.Fatalf("entity = %+v, want HOST link", v.entity)
	}

	v = renderValue("dt.entity.process_group_instance", "PROCESS_GROUP_INSTANCE-00AA11BB22CC33DD", 80)
	if v.entity == nil || v.entity.Type != "PROCESS_GROUP_INSTANCE" {
		t.Fatalf("underscored type not derived: %+v", v.entity)
	}

	// display_id shapes must not classify as entities.
	if v := renderValue("display_id", "P-100", 80); v.entity != nil {
		t.Errorf("P-100 wrongly classified as entity: %+v", v.entity)
	}
}

func TestRenderValueTraceAndDuration(t *testing.T) {
	trace := strings.Repeat("ab", 16)
	v := renderValue("trace_id", trace, 80)
	if v.trace != trace {
		t.Fatalf("trace = %q, want %q", v.trace, trace)
	}
	// span ids are opaque, not navigable
	if v := renderValue("span_id", "aabbccdd11223344", 80); v.trace != "" || v.entity != nil {
		t.Errorf("span id must not be navigable: %+v", v)
	}

	v = renderValue("duration", "4845165", 80)
	if got := ansi.Strip(v.lines[0]); !strings.Contains(got, "4.8ms") {
		t.Errorf("duration line = %q, want humanized ns", got)
	}
}

func TestRenderValueTimestampAndNumbers(t *testing.T) {
	iso := time.Now().Add(-23 * time.Minute).UTC().Format(time.RFC3339Nano)
	v := renderValue("timestamp", iso, 80)
	if got := ansi.Strip(v.lines[0]); !strings.Contains(got, "23m ago") {
		t.Errorf("timestamp line = %q, want relative age", got)
	}

	if got := ansi.Strip(renderValue("n", "5194272", 80).lines[0]); got != "5,194,272" {
		t.Errorf("number grouping = %q", got)
	}
	if got := ansi.Strip(renderValue("n", float64(1234), 80).lines[0]); got != "1234" {
		t.Errorf("short numbers must not group: %q", got)
	}
}

func TestRenderValueJSONBlocks(t *testing.T) {
	rec := map[string]any{
		"phase":    "Running",
		"restarts": float64(3),
		"ids":      []any{"a", "b"},
	}
	v := renderValue("k8s.meta", rec, 80)
	text := ansi.Strip(strings.Join(v.lines, "\n"))
	for _, want := range []string{"phase:", `"Running"`, "restarts: 3", `["a", "b"]`} {
		if !strings.Contains(text, want) {
			t.Errorf("json block missing %q:\n%s", want, text)
		}
	}
	if !strings.HasPrefix(v.raw, "{") {
		t.Errorf("raw yank text should be compact JSON, got %q", v.raw)
	}

	// A string holding a JSON document renders as a block too.
	v = renderValue("content", `{"level":"error","code":500}`, 80)
	text = ansi.Strip(strings.Join(v.lines, "\n"))
	if !strings.Contains(text, "level:") || !strings.Contains(text, "code: 500") {
		t.Errorf("json string not highlighted:\n%s", text)
	}
	if v.raw != `{"level":"error","code":500}` {
		t.Errorf("raw must stay verbatim, got %q", v.raw)
	}
}

func TestEntityIDListExplodesOnlyPureIDArrays(t *testing.T) {
	ids := entityIDList([]any{"HOST-0011223344556677", "SERVICE-8899AABBCCDDEEFF"})
	if len(ids) != 2 {
		t.Fatalf("ids = %v", ids)
	}
	if entityIDList([]any{"HOST-0011223344556677", "not-an-id"}) != nil {
		t.Error("mixed arrays must not explode")
	}
	if entityIDList([]any{}) != nil {
		t.Error("empty arrays must not explode")
	}
}
