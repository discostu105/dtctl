package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// hostFileAccessAllowlist records every file in cmd/ that is permitted to touch
// the host filesystem directly, with the reason it is not request state. A file
// not listed here must read and write user-supplied paths through pkg/vfs.
//
// The distinction is *whose* file it is:
//
//   - a path the user named on the command line (-f, --file, --data-file, a
//     query file) is request state — under an embedded invocation it exists
//     only in the request's virtual filesystem, so it must go through vfs
//   - a path dtctl chose itself (an editor round-trip temp file, the config
//     file, a spill buffer) is host state and belongs on the host disk
var hostFileAccessAllowlist = map[string]string{
	// The config file is host state by definition, and the whole `config`
	// command tree is unavailable in a service (engine policy).
	"config.go": "reads/writes the local dtctl config file — host state, and `config` is blocked in a service",

	// Editor round-trip files are dtctl's own scratch space: written to the
	// host, opened in the host's editor, read back. `edit` requires the Editor
	// capability and is blocked in a service, so this never runs embedded.
	"edit.go":                 "editor round-trip temp file (Editor capability, blocked in a service)",
	"edit_anomalydetector.go": "editor round-trip temp file (Editor capability, blocked in a service)",
	"edit_aws.go":             "editor round-trip temp file (Editor capability, blocked in a service)",
	"edit_azure.go":           "editor round-trip temp file (Editor capability, blocked in a service)",
	"edit_documents.go":       "editor round-trip temp file (Editor capability, blocked in a service)",
	"edit_gcp.go":             "editor round-trip temp file (Editor capability, blocked in a service)",
	"edit_segments.go":        "editor round-trip temp file (Editor capability, blocked in a service)",
	"edit_settings.go":        "editor round-trip temp file (Editor capability, blocked in a service)",
	"edit_workflows.go":       "editor round-trip temp file (Editor capability, blocked in a service)",
}

// hostFileAccessCalls are the direct host-filesystem entry points that bypass
// the vfs seam. os.Stdin/os.Stdout/os.Stderr are deliberately absent: the
// stream seam swaps those variables per invocation (see stdio.go), so using
// them is correct. Opening "/dev/stdin" as a *path* is not — that reaches the
// process's real fd 0, past the redirection, and does not exist on Windows.
var hostFileAccessCalls = []string{
	"os.ReadFile(",
	"os.WriteFile(",
	"os.Open(",
	"os.OpenFile(",
	"os.Create(",
	"os.ReadDir(",
	`"/dev/stdin"`,
}

// TestUserFilePathsGoThroughVFS is the E6 guard: user-supplied file paths must
// resolve through pkg/vfs, never through os directly. A service request's
// files exist only in the request — a direct os.ReadFile in a command silently
// reads the *server's* disk instead, which is both a broken feature and a path
// traversal into host state.
//
// Adding a file to hostFileAccessAllowlist is a deliberate act: it asserts the
// path is dtctl's own scratch/host state, not something the user named.
func TestUserFilePathsGoThroughVFS(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Clean(name))
		require.NoError(t, err)
		text := string(src)

		for _, call := range hostFileAccessCalls {
			if !strings.Contains(text, call) {
				continue
			}
			reason, allowed := hostFileAccessAllowlist[name]
			require.True(t, allowed,
				"%s calls %s directly — user-supplied paths must go through pkg/vfs "+
					"(vfs.ReadFile / vfs.WriteFile / vfs.ReadFileOrStdin) so embedded "+
					"invocations resolve them against the request's virtual files. If this "+
					"path is dtctl's own scratch or host state, add %s to "+
					"hostFileAccessAllowlist with the reason.", name, call, name)
			require.NotEmpty(t, reason)
		}
	}
}
