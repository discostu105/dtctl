package tui

import "testing"

func TestGenAIBodyViewJSONPayload(t *testing.T) {
	args := `{"path": "/tmp/x", "recursive": true}`
	v := genaiBodyView(args, 80, true)
	if v.doc == nil {
		t.Fatalf("JSON payload must decode into a navigable doc")
	}
	if v.raw != args {
		t.Errorf("raw = %q, want the untouched payload", v.raw)
	}
	if !v.block {
		t.Errorf("small expanded JSON payload must render as a block")
	}
}

func TestGenAIBodyViewProse(t *testing.T) {
	v := genaiBodyView("Summarize the incident below.", 80, false)
	if v.doc != nil {
		t.Errorf("prose must not decode as JSON")
	}
	if v.block {
		t.Errorf("collapsed message must not expand by default")
	}
	if v.raw != "Summarize the incident below." {
		t.Errorf("raw = %q, want the untouched text", v.raw)
	}
}
