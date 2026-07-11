package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFindFrom(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("not found", func(t *testing.T) {
		if path, ok := findFrom(nested); ok {
			t.Fatalf("expected no workspace file, found %s", path)
		}
	})

	want := writeFile(t, root, "version: 1\n")

	t.Run("found in start dir", func(t *testing.T) {
		path, ok := findFrom(root)
		if !ok || path != want {
			t.Fatalf("findFrom(root) = %q, %v; want %q, true", path, ok, want)
		}
	})

	t.Run("found walking up", func(t *testing.T) {
		path, ok := findFrom(nested)
		if !ok || path != want {
			t.Fatalf("findFrom(nested) = %q, %v; want %q, true", path, ok, want)
		}
	})

	t.Run("nearest file wins", func(t *testing.T) {
		near := writeFile(t, filepath.Join(root, "a"), "version: 1\n")
		path, ok := findFrom(nested)
		if !ok || path != near {
			t.Fatalf("findFrom(nested) = %q, %v; want %q, true", path, ok, near)
		}
	})
}

func TestLoadValid(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, `
version: 1
environment: https://abc12345.apps.dynatrace.com
view: pods
timeframe: 2h
segments:
  - payments-prod
  - segment: 4lpVjcpcsjd
    variables:
      environment: [production]
      region: [us-east-1, us-west-2]
`)
	w, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if w.Path() != path {
		t.Errorf("Path() = %q, want %q", w.Path(), path)
	}
	if w.Environment != "https://abc12345.apps.dynatrace.com" || w.View != "pods" || w.Timeframe != "2h" {
		t.Errorf("unexpected fields: %+v", w)
	}
	if len(w.Segments) != 2 {
		t.Fatalf("got %d segments, want 2", len(w.Segments))
	}
	if w.Segments[0].Ref != "payments-prod" || w.Segments[0].Variables != nil {
		t.Errorf("scalar segment = %+v", w.Segments[0])
	}
	if w.Segments[1].Ref != "4lpVjcpcsjd" {
		t.Errorf("map segment ref = %q", w.Segments[1].Ref)
	}
	if got := w.Segments[1].Variables["region"]; len(got) != 2 || got[1] != "us-west-2" {
		t.Errorf("variables = %+v", w.Segments[1].Variables)
	}
}

func TestLoadUnknownKeysIgnored(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "version: 1\nfuture-key: whatever\nview: logs\n")
	w, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if w.View != "logs" {
		t.Errorf("View = %q, want logs", w.View)
	}
}

func TestLoadErrors(t *testing.T) {
	segments := func(n int) string {
		var b strings.Builder
		b.WriteString("segments:\n")
		for i := 0; i < n; i++ {
			b.WriteString("  - seg-")
			b.WriteByte(byte('a' + i))
			b.WriteString("\n")
		}
		return b.String()
	}
	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{"invalid yaml", "segments: [unclosed", "parsing"},
		{"future version", "version: 2\n", "newer than this build"},
		{"too many segments", segments(MaxSegments + 1), "at most 10"},
		{"empty ref scalar", "segments:\n  - \"\"\n", "missing segment name"},
		{"empty ref map", "segments:\n  - variables:\n      env: [prod]\n", "missing segment name"},
		{"variable without values", "segments:\n  - segment: s1\n    variables:\n      env: []\n", "has no values"},
		{"bad timeframe", "timeframe: yesterday\n", "expected <n>m"},
		{"bad timeframe unit", "timeframe: 2w\n", "expected <n>m"},
		{"bad environment scheme", "environment: ftp://host\n", "unsupported scheme"},
		{"environment with credentials", "environment: https://user:pw@host\n", "credentials"},
		{"environment with path as bare host", "environment: host/path\n", "bare hostname"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), tc.content)
			_, err := Load(path)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadValidBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"absent version", "view: pods\n"},
		{"max segments", func() string {
			var b strings.Builder
			b.WriteString("segments:\n")
			for i := 0; i < MaxSegments; i++ {
				b.WriteString("  - seg-")
				b.WriteByte(byte('a' + i))
				b.WriteString("\n")
			}
			return b.String()
		}()},
		{"bare hostname environment", "environment: abc12345.apps.dynatrace.com\n"},
		{"minutes timeframe", "timeframe: 30m\n"},
		{"days timeframe", "timeframe: 7d\n"},
		{"empty file", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), tc.content)
			if _, err := Load(path); err != nil {
				t.Fatalf("Load: %v", err)
			}
		})
	}
}

func TestDiscoverAbsent(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	w, err := Discover()
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if w != nil {
		t.Fatalf("Discover = %+v, want nil (no file anywhere above %s)", w, dir)
	}
}
