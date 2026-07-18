package recipes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const stampFixture = `# hand-written local book — the comment must survive stamp writes
apiVersion: dtctl.dev/v1alpha1
kind: RecipeBook
metadata:
  name: test
customKey: preserved   # unknown key, must survive
recipes:
  my-recipe:
    description: local recipe
    dql: fetch logs | limit 5
disabled:
  dead-one:
    class: probe-empty
    reason: "0 events in 24h"
`

func TestUpdateStampPreservesContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.yaml")
	if err := os.WriteFile(path, []byte(stampFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	n := 42
	err := UpdateStamp(path, "my-recipe", "", &LastRun{At: "2026-07-18T00:00:00Z", Records: &n, Seconds: 0.5, Params: ParamsProvenanceDefault}, false)
	if err != nil {
		t.Fatalf("UpdateStamp: %v", err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	for _, want := range []string{"comment must survive", "customKey: preserved", "records: 42", "dead-one:"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q after stamp write:\n%s", want, text)
		}
	}
	book, err := LoadBook(path)
	if err != nil {
		t.Fatalf("book unparseable after stamp: %v", err)
	}
	if lr := book.Recipes["my-recipe"].LastRun; lr == nil || *lr.Records != 42 {
		t.Errorf("stamp not applied: %+v", lr)
	}
}

func TestUpdateStampCreatesEntryAndResurrects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.yaml")
	if err := os.WriteFile(path, []byte(stampFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	n := 7
	err := UpdateStamp(path, "dead-one", "pack:dead-one@1", &LastRun{At: "2026-07-18T00:00:00Z", Records: &n, Params: ParamsProvenanceDefault}, true)
	if err != nil {
		t.Fatalf("UpdateStamp: %v", err)
	}
	book, err := LoadBook(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, still := book.Disabled["dead-one"]; still {
		t.Error("resurrected recipe still in disabled")
	}
	entry := book.Recipes["dead-one"]
	if entry == nil || entry.Source != "pack:dead-one@1" || entry.LastRun == nil {
		t.Errorf("resurrected entry: %+v", entry)
	}
}

func TestNewStampLimitHit(t *testing.T) {
	lr := NewStamp(25, 0.834, ParamsProvenanceDefault, "", "fetch logs | limit 100 | sort a | limit 25")
	if !lr.LimitHit {
		t.Error("expected limitHit from last `| limit 25`")
	}
	if lr.Seconds != 0.83 {
		t.Errorf("seconds rounding: %v", lr.Seconds)
	}
	lr = NewStamp(10, 0.1, "abc12345", "scan limit reached", "fetch logs | limit 25")
	if lr.LimitHit {
		t.Error("10 != 25 must not be limitHit")
	}
	if lr.Partial == "" || lr.Params != "abc12345" {
		t.Errorf("stamp fields: %+v", lr)
	}
}
