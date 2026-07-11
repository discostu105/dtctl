package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dynatrace-oss/dtctl/pkg/config"
	"github.com/dynatrace-oss/dtctl/pkg/workspace"
)

func TestFindContextByEnvironment(t *testing.T) {
	cfg := &config.Config{
		Contexts: []config.NamedContext{
			{Name: "prod", Context: config.Context{Environment: "https://abc12345.apps.dynatrace.com"}},
			{Name: "dev", Context: config.Context{Environment: "https://dev67890.apps.dynatrace.com/"}},
		},
	}
	cases := []struct {
		env  string
		want string
		ok   bool
	}{
		{"https://abc12345.apps.dynatrace.com", "prod", true},
		{"https://ABC12345.apps.dynatrace.com/", "prod", true}, // case + trailing slash
		{"abc12345.apps.dynatrace.com", "prod", true},          // bare host
		{"http://dev67890.apps.dynatrace.com", "dev", true},    // scheme ignored
		{"https://other.apps.dynatrace.com", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		name, ok := findContextByEnvironment(cfg, tc.env)
		if name != tc.want || ok != tc.ok {
			t.Errorf("findContextByEnvironment(%q) = %q, %v; want %q, %v", tc.env, name, ok, tc.want, tc.ok)
		}
	}
}

func TestWorkspaceSegmentsDeterministicVariableOrder(t *testing.T) {
	ws := &workspace.Workspace{Segments: []workspace.Segment{
		{Ref: "seg-1", Variables: map[string][]string{
			"zone": {"z1"}, "env": {"prod"}, "region": {"eu"},
		}},
	}}
	refs := workspaceSegments(ws)
	if len(refs) != 1 || refs[0].Ref != "seg-1" {
		t.Fatalf("refs = %+v", refs)
	}
	var names []string
	for _, v := range refs[0].Variables {
		names = append(names, v.Name)
	}
	if got := strings.Join(names, ","); got != "env,region,zone" {
		t.Errorf("variable order = %s, want sorted env,region,zone", got)
	}
}

func TestWorkspaceNotice(t *testing.T) {
	ws, err := workspace.Load(writeWorkspaceFile(t, `
view: pods
timeframe: 24h
segments:
  - payments-prod
  - team-checkout
`))
	if err != nil {
		t.Fatal(err)
	}
	notice := workspaceNotice(ws)
	for _, want := range []string{"workspace ", "2 segments", "last 24h", "view pods"} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice %q misses %q", notice, want)
		}
	}

	empty, err := workspace.Load(writeWorkspaceFile(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if got := workspaceNotice(empty); got != "" {
		t.Errorf("empty workspace notice = %q, want none", got)
	}
}

func writeWorkspaceFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), workspace.FileName)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
