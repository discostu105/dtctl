package catalog

import (
	"strings"
	"testing"
)

func TestRecordsSamplerArgForms(t *testing.T) {
	tf := Scope{Timeframe: DefaultTimeframe}
	cases := []struct{ arg, want string }{
		{"logs", "fetch logs\n| limit 200"},
		{"logs@default_logs", "fetch logs\n| filter dt.system.bucket == \"default_logs\"\n| limit 200"},
		{"load:/lookups/teams/v1", "load \"/lookups/teams/v1\"\n| limit 200"},
	}
	for _, c := range cases {
		tf.Arg = c.arg
		if got := recordsSpec.Query(tf); got != c.want {
			t.Errorf("records(%q) =\n%s\nwant\n%s", c.arg, got, c.want)
		}
	}
	// Table names compose into DQL — anything outside the safe alphabet is
	// refused and falls back to the catalog browse.
	tf.Arg = `logs" | fieldsRemove x | fetch `
	if got := recordsSpec.Query(tf); !strings.Contains(got, "dt.system.data_objects") {
		t.Errorf("hostile arg must fall back to the catalog:\n%s", got)
	}
}
func TestTablesEnterRefusesNonFetchable(t *testing.T) {
	fetchable := map[string]any{"name": "logs", "usable_with": []any{"fetch", "describe"}}
	if got := tablesSpec.EnterArg(fetchable); got != "logs" {
		t.Errorf("fetchable table EnterArg = %q", got)
	}
	snapshot := map[string]any{"name": "metrics", "usable_with": []any{"fieldsSnapshot"}}
	if got := tablesSpec.EnterArg(snapshot); got != "" {
		t.Errorf("fieldsSnapshot-only table must refuse enter, got %q", got)
	}
}
func TestBucketAndFileEnterArgs(t *testing.T) {
	bucket := map[string]any{"name": "default_logs", "dt.system.table": "logs"}
	if got := bucketsSpec.EnterArg(bucket); got != "logs@default_logs" {
		t.Errorf("bucket EnterArg = %q", got)
	}
	file := map[string]any{"name": "/lookups/teams/v1"}
	if got := filesSpec.EnterArg(file); got != "load:/lookups/teams/v1" {
		t.Errorf("file EnterArg = %q", got)
	}
}
