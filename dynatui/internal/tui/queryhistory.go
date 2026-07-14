package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Query history: every DQL submitted in the escape hatch is remembered
// most-recent-first and persisted, so ctrl+p / ctrl+n in the editor walk
// previous queries — across sessions (the design doc's "query history").
// Same discipline as the navigation history: an empty path keeps it
// in-memory only (tests), and a missing or corrupt file must never block
// the TUI from launching.

// maxQueryHistory bounds the persisted query list.
const maxQueryHistory = 50

type queryHistory struct {
	path    string
	queries []string // most recent first
}

type queryHistoryFile struct {
	Version int      `json:"version"`
	Queries []string `json:"queries"`
}

func loadQueryHistory(path string) *queryHistory {
	h := &queryHistory{path: path}
	if path == "" {
		return h
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return h
	}
	var f queryHistoryFile
	if json.Unmarshal(data, &f) == nil {
		h.queries = f.Queries
	}
	return h
}

// add records a submitted query MRU-style: a re-run moves to the front
// instead of piling up duplicates. Saving is best-effort.
func (h *queryHistory) add(dql string) {
	dql = strings.TrimSpace(dql)
	if dql == "" {
		return
	}
	kept := make([]string, 0, len(h.queries)+1)
	kept = append(kept, dql)
	for _, q := range h.queries {
		if q != dql {
			kept = append(kept, q)
		}
	}
	if len(kept) > maxQueryHistory {
		kept = kept[:maxQueryHistory]
	}
	h.queries = kept
	h.save()
}

func (h *queryHistory) save() {
	if h.path == "" {
		return
	}
	data, err := json.Marshal(queryHistoryFile{Version: 1, Queries: h.queries})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(h.path), 0o700); err != nil {
		return
	}
	tmp := h.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, h.path)
}
