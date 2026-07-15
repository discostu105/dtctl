// The navigator's data flow: the mode-specific main query, name/schema/
// problem-overlay resolution, and result routing.
package tui

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
)

// --- data flow ------------------------------------------------------------------

// fetchMain fires the mode's list query. A cached walk root skips the fetch —
// backtracking and re-visits are zero-query.
func (v *navView) fetchMain() tea.Cmd {
	v.seq++
	v.err = nil
	switch v.mode {
	case navOverview:
		v.dql = catalog.CensusQuery()
	case navBrowser:
		if v.search != "" {
			v.dql = catalog.NameSearchQuery(v.search)
		} else {
			v.dql = catalog.TypeInstancesQuery(v.typ)
		}
	case navWalk:
		v.dql = catalog.EdgesQuery(v.root.ID)
		if edges, ok := v.nodeCache[v.root.ID]; ok {
			v.loading = false
			v.edges = edges
			v.rebuildRows()
			return v.resolveNames()
		}
	}
	v.loading = true
	return v.ds.query(v, v.seq, v.dql)
}

// resolveSearch inspects a browser's fresh result set: an empty type browse
// with a pending fallback term retries it as the name search (":nav
// payments" browsed the nonexistent PAYMENTS type first), a name search's
// unique match re-shapes the view into a walk rooted there (the :nav <name>
// promise), and a multi-match stays as the disambiguation list with exact
// name hits ranked first. nil = no morph.
func (v *navView) resolveSearch() tea.Cmd {
	if v.search == "" {
		if len(v.instances) == 0 && v.searchFallback != "" {
			v.search, v.searchFallback, v.typ = v.searchFallback, "", ""
			return tea.Batch(v.fetchMain(), markHistory)
		}
		return nil
	}
	if len(v.instances) == 0 {
		return nil
	}
	if len(v.instances) == 1 {
		if e := navInstanceEntity(v.instances[0]); e != nil {
			v.mode = navWalk
			v.search = ""
			if e.Name != "" {
				v.names[e.ID] = e.Name
			}
			v.root = *e
			return tea.Batch(v.fetchMain(), v.schedulePreview(), markHistory)
		}
		return nil
	}
	term := strings.ToLower(v.search)
	sort.SliceStable(v.instances, func(i, j int) bool {
		return searchHitRank(v.instances[i], term) < searchHitRank(v.instances[j], term)
	})
	return nil
}

// searchHitRank orders name matches: exact (case-insensitive) before contains.
func searchHitRank(rec map[string]any, term string) int {
	if strings.ToLower(catalog.Str(rec, "name")) == term {
		return 0
	}
	return 1
}

func (v *navView) schemaCmd() tea.Cmd {
	v.schemaLoading = true
	v.schemaErr = nil
	return v.ds.queryCapped(navSchemaOwner{v}, v.seq, catalog.SchemaQuery(), 2000)
}

func (v *navView) problemsCmd() tea.Cmd {
	v.probSeq++
	v.probLoaded = false
	return v.ds.query(navProblemsOwner{v}, v.probSeq, catalog.ProblemOverlayQuery())
}

// resolveNames issues the batched name lookup for neighbors not yet known.
func (v *navView) resolveNames() tea.Cmd {
	seen := map[string]bool{}
	var ids []string
	for _, e := range v.edges {
		if _, known := v.names[e.OtherID]; known || seen[e.OtherID] {
			continue
		}
		seen[e.OtherID] = true
		ids = append(ids, e.OtherID)
	}
	if len(ids) == 0 {
		return nil
	}
	return v.ds.query(navNameOwner{v}, v.seq, catalog.NamesQuery(ids))
}

func (v *navView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bodySizeMsg:
		v.width, v.height = msg.width, msg.height
		return nil

	case navPreviewTickMsg:
		if msg.gen != v.previewGen {
			return nil
		}
		_, e := v.Selection()
		if e == nil || e.ID == "" || e.Type == "" {
			return nil
		}
		if _, cached := v.detail[e.ID]; cached {
			return nil
		}
		return v.ds.query(navPreviewOwner{v: v, id: e.ID}, msg.gen, catalog.DetailQuery(*e))

	case dataMsg:
		return v.handleData(msg)

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return nil
}

func (v *navView) handleData(msg dataMsg) tea.Cmd {
	switch owner := msg.owner.(type) {
	case navNameOwner:
		if owner.v != v || msg.err != nil {
			return nil // resolution failures fall back to raw ids
		}
		for _, rec := range msg.records {
			if id := catalog.Str(rec, "id"); id != "" {
				v.names[id] = catalog.Str(rec, "name")
			}
		}
		if v.root.Name == "" {
			v.root.Name = v.names[v.root.ID]
		}
		// Names don't reorder rows (BuildEdges sorts by type+id on purpose),
		// but an active filter matches on them.
		if v.filter != "" {
			v.rebuildRows()
		}
		return nil

	case navSchemaOwner:
		if owner.v != v || msg.seq != v.seq {
			return nil
		}
		v.schemaLoading = false
		v.schemaErr = msg.err
		if msg.err == nil {
			v.schema = catalog.BuildSchema(msg.records)
		}
		return nil

	case navProblemsOwner:
		if owner.v != v || msg.seq != v.probSeq {
			return nil
		}
		// Errors degrade to a blank overlay — health must never block the map.
		if msg.err != nil {
			return nil
		}
		v.probByID = map[string][]map[string]any{}
		v.probByType = map[string]int{}
		for _, rec := range catalog.ActiveProblems(msg.records) {
			typesSeen := map[string]bool{}
			for _, id := range catalog.ProblemAffectedIDs(rec) {
				v.probByID[id] = append(v.probByID[id], rec)
				if typ := entityTypeOf(id); typ != "" && !typesSeen[typ] {
					typesSeen[typ] = true
					v.probByType[typ]++
				}
			}
		}
		v.probLoaded = true
		return nil

	case navPreviewOwner:
		if owner.v != v || msg.err != nil {
			return nil
		}
		// Cache even a stale-generation result — the fetch already happened.
		if len(msg.records) > 0 {
			v.detail[owner.id] = msg.records[0]
		} else {
			// Fetched-but-empty: an edge-only endpoint with no node record.
			v.detail[owner.id] = map[string]any{}
		}
		return nil
	}

	if msg.owner != any(v) || msg.seq != v.seq {
		return nil
	}
	v.loading = false
	v.err = msg.err
	if msg.err != nil {
		return nil
	}
	switch v.mode {
	case navOverview:
		v.census = msg.records
	case navBrowser:
		v.instances = msg.records
		if cmd := v.resolveSearch(); cmd != nil {
			return cmd
		}
	case navWalk:
		v.edges = catalog.BuildEdges(v.root.ID, msg.records)
		v.nodeCache[v.root.ID] = v.edges
	}
	v.rebuildRows()
	if v.mode == navWalk {
		return tea.Batch(v.resolveNames(), v.schedulePreview())
	}
	return v.schedulePreview()
}

// entityTypeOf derives the node type from an id's prefix (both eras encode it
// there: "K8S_POD-16HEX").
func entityTypeOf(id string) string {
	if i := strings.LastIndex(id, "-"); i > 0 {
		return id[:i]
	}
	return ""
}
