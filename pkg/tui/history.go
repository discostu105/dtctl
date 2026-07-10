package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
	"github.com/dynatrace-oss/dtctl/pkg/tui/theme"
)

// Navigation history: every navigate() snapshots the breadcrumb stack as a
// serializable trail, persisted to disk so 'H' can restore a page — including
// where the user left off in a previous session. Only view identity is stored
// (spec name, entity, filter, DQL, trace id); data is refetched on restore.

// maxHistoryEntries bounds the persisted history across all contexts.
const maxHistoryEntries = 50

// pageRef describes one view on the stack with just enough identity to
// rebuild it fresh.
type pageRef struct {
	Kind   string `json:"kind"`
	Crumb  string `json:"crumb,omitempty"`  // display label at snapshot time
	View   string `json:"view,omitempty"`   // catalog spec name (table)
	Filter string `json:"filter,omitempty"` // table's incremental filter
	// Search is the pre-multi-search field, still read for old history files.
	Search   string          `json:"search,omitempty"`
	Searches []string        `json:"searches,omitempty"` // table's server-side searches
	Facets   []catalog.Facet `json:"facets,omitempty"`   // table's server-side facets
	Arg      string          `json:"arg,omitempty"`      // scope arg (census → typed list)
	Lens     int             `json:"lens,omitempty"`     // table's active lens index
	Pattern  string          `json:"pattern,omitempty"`  // logs' DPL pattern scope
	TraceID  string          `json:"traceId,omitempty"`  // waterfall / trace-scoped tables
	DQL      string          `json:"dql,omitempty"`      // query editor content
	Entity   *catalog.Entity `json:"entity,omitempty"`   // detail/metrics/relations/table scope
	Trail    []catalog.Entity `json:"trail,omitempty"`   // navigator walk path
	Title    string          `json:"title,omitempty"`    // inspector title
	Rec      map[string]any  `json:"rec,omitempty"`      // inspector record, kept verbatim
}

// historyEntry is one visited breadcrumb trail.
type historyEntry struct {
	Stack     []pageRef `json:"stack"`
	Context   string    `json:"context,omitempty"`   // dtctl context — entity ids are env-specific
	Timeframe string    `json:"timeframe,omitempty"` // global window label at visit
	Visited   time.Time `json:"visited"`
}

// trail renders the entry as its breadcrumb labels.
func (e historyEntry) trail() string {
	crumbs := make([]string, len(e.Stack))
	for i, ref := range e.Stack {
		crumbs[i] = ref.Crumb
		if crumbs[i] == "" {
			crumbs[i] = ref.Kind
		}
	}
	return strings.Join(crumbs, " › ")
}

// signature identifies a trail for MRU dedup by identity fields only, so a
// re-visit — or a crumb that learned an entity name later — replaces its
// older self instead of piling up near-duplicates.
func (e historyEntry) signature() string {
	var b strings.Builder
	b.WriteString(e.Context)
	for _, ref := range e.Stack {
		id := ""
		if ref.Entity != nil {
			id = ref.Entity.ID
		}
		fmt.Fprintf(&b, "|%s/%s/%s/%s/%s/%s/%s/%s/%s", ref.Kind, ref.View, ref.Filter, ref.Arg, ref.TraceID, ref.DQL, id, ref.Search, ref.Pattern)
		for _, s := range ref.Searches {
			fmt.Fprintf(&b, "/⌕%s", s)
		}
		for _, f := range ref.Facets {
			fmt.Fprintf(&b, "/%s", f.Label())
		}
		if ref.Kind == "inspector" || ref.Kind == "problem" {
			fmt.Fprintf(&b, "/%s/%s/%s", ref.Title, catalog.Str(ref.Rec, "timestamp"), catalog.Str(ref.Rec, "display_id"))
		}
	}
	return b.String()
}

// historyStore holds the history for every context, persisted as JSON.
// An empty path keeps it in-memory only (tests).
type historyStore struct {
	path    string
	ctx     string
	entries []historyEntry // most recent first, all contexts mixed
}

type historyFile struct {
	Version int            `json:"version"`
	Entries []historyEntry `json:"entries"`
}

// loadHistory reads the persisted history; a missing or corrupt file starts
// empty — history must never block the TUI from launching.
func loadHistory(path, ctx string) *historyStore {
	h := &historyStore{path: path, ctx: ctx}
	if path == "" {
		return h
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return h
	}
	var f historyFile
	if json.Unmarshal(data, &f) == nil {
		h.entries = f.Entries
	}
	return h
}

// record snapshots a stack as the most recent entry, replacing any older
// entry with the same signature and pruning to the cap.
func (h *historyStore) record(stack []viewModel, tfLabel string) {
	var refs []pageRef
	for _, v := range stack {
		if ref, ok := pageRefOf(v); ok {
			refs = append(refs, ref)
		}
	}
	if len(refs) == 0 {
		return
	}
	entry := historyEntry{Stack: refs, Context: h.ctx, Timeframe: tfLabel, Visited: time.Now().UTC()}
	sig := entry.signature()
	kept := make([]historyEntry, 0, len(h.entries)+1)
	kept = append(kept, entry)
	for _, e := range h.entries {
		if e.signature() != sig {
			kept = append(kept, e)
		}
	}
	if len(kept) > maxHistoryEntries {
		kept = kept[:maxHistoryEntries]
	}
	h.entries = kept
}

// forContext lists the current context's entries, most recent first, hiding
// the trail the user is standing on (excludeSig).
func (h *historyStore) forContext(excludeSig string) []historyEntry {
	var out []historyEntry
	for _, e := range h.entries {
		if e.Context == h.ctx && e.signature() != excludeSig {
			out = append(out, e)
		}
	}
	return out
}

// refresh merges entries persisted by other sessions into memory, so the
// picker and the next save see trails from every open TUI.
func (h *historyStore) refresh() {
	if h.path == "" {
		return
	}
	disk := loadHistory(h.path, h.ctx)
	h.entries = mergeEntries(h.entries, disk.entries)
}

// mergeEntries unions two entry lists by signature — the newest visit wins —
// ordered most recent first and capped.
func mergeEntries(a, b []historyEntry) []historyEntry {
	bySig := map[string]historyEntry{}
	for _, e := range append(append([]historyEntry{}, a...), b...) {
		sig := e.signature()
		if prev, ok := bySig[sig]; !ok || e.Visited.After(prev.Visited) {
			bySig[sig] = e
		}
	}
	out := make([]historyEntry, 0, len(bySig))
	for _, e := range bySig {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Visited.After(out[j].Visited) })
	if len(out) > maxHistoryEntries {
		out = out[:maxHistoryEntries]
	}
	return out
}

// save persists the history best-effort; the TUI never fails over it.
// Concurrent sessions share the file, so save merges instead of clobbering.
func (h *historyStore) save() {
	if h.path == "" {
		return
	}
	h.refresh()
	data, err := json.MarshalIndent(historyFile{Version: 1, Entries: h.entries}, "", "  ")
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

// pageRefOf describes a live view as a restorable page. Every stack view kind
// is covered; the inner views (detail tabs, the query results table) never
// sit on the stack themselves.
func pageRefOf(v viewModel) (pageRef, bool) {
	switch v := v.(type) {
	case *homeView:
		return pageRef{Kind: "home", Crumb: v.Crumb()}, true
	case *tableView:
		ref := pageRef{Kind: "table", Crumb: v.Crumb(), View: v.spec.Name,
			Filter: v.filter, Searches: v.searches, Facets: v.facets,
			Arg: v.scope.Arg, Lens: v.scope.Lens, TraceID: v.scope.TraceID,
			Pattern: v.scope.Pattern}
		if v.scope.Entity != nil {
			e := *v.scope.Entity
			ref.Entity = &e
		}
		return ref, true
	case *queryView:
		dql := v.current
		if dql == "" {
			dql = strings.TrimSpace(v.editor.Value())
		}
		return pageRef{Kind: "query", Crumb: v.Crumb(), DQL: dql}, true
	case *detailView:
		e := v.entity
		return pageRef{Kind: "detail", Crumb: v.Crumb(), Entity: &e}, true
	case *metricsView:
		e := v.entity
		return pageRef{Kind: "metrics", Crumb: v.Crumb(), Entity: &e}, true
	case *relationsView:
		e := v.entity
		return pageRef{Kind: "relations", Crumb: v.Crumb(), Entity: &e}, true
	case *navView:
		ref := pageRef{Kind: "nav", Crumb: v.Crumb(), View: navModeName(v.mode), Arg: v.typ}
		if v.mode == navWalk {
			e := v.root
			ref.Entity = &e
			ref.Trail = append([]catalog.Entity{}, v.trail...)
		}
		return ref, true
	case *waterfallView:
		return pageRef{Kind: "waterfall", Crumb: v.Crumb(), TraceID: v.traceID}, true
	case *timelineView:
		return pageRef{Kind: "timeline", Crumb: v.Crumb(), Arg: v.sessionID, Lens: v.lens}, true
	case *problemView:
		return pageRef{Kind: "problem", Crumb: v.Crumb(), Rec: v.rec}, true
	case *inspectorView:
		return pageRef{Kind: "inspector", Crumb: v.Crumb(), Title: v.title, Rec: v.rec}, true
	}
	return pageRef{}, false
}

// viewFromRef rebuilds a fresh view from a persisted page descriptor.
func (a *app) viewFromRef(ref pageRef, tf catalog.Timeframe) (viewModel, error) {
	switch ref.Kind {
	case "home":
		return newHomeView(a.ds, tf), nil
	case "table":
		spec := catalog.Lookup(ref.View)
		if spec == nil {
			return nil, fmt.Errorf("unknown view %q", ref.View)
		}
		scope := catalog.Scope{
			Timeframe: tf, Arg: ref.Arg, TraceID: ref.TraceID, Entity: ref.Entity,
			Pattern: ref.Pattern}
		if ref.Lens > 0 && ref.Lens < len(spec.Lenses) {
			scope.Lens = ref.Lens
		}
		v := newTableView(a.ds, spec, scope)
		if ref.Filter != "" {
			v.setFilter(ref.Filter)
		}
		v.searches = ref.Searches
		if ref.Search != "" { // an entry saved before searches stacked
			v.searches = append(v.searches, ref.Search)
		}
		v.facets = ref.Facets
		return v, nil
	case "query":
		v := newQueryView(a.ds, ref.DQL, tf)
		if ref.DQL != "" {
			// Arrive on results, not the editor: Init re-runs a submitted query.
			v.current = ref.DQL
			v.editing = false
			v.editor.Blur()
		}
		return v, nil
	case "detail":
		if ref.Entity == nil {
			return nil, fmt.Errorf("detail page without entity")
		}
		return newDetailView(a.ds, *ref.Entity, nil, tf), nil
	case "metrics":
		if ref.Entity == nil {
			return nil, fmt.Errorf("metrics page without entity")
		}
		return newMetricsView(a.ds, *ref.Entity, tf), nil
	case "relations":
		if ref.Entity == nil {
			return nil, fmt.Errorf("relations page without entity")
		}
		return newRelationsView(a.ds, *ref.Entity, tf), nil
	case "nav":
		switch ref.View {
		case "walk":
			if ref.Entity == nil {
				return nil, fmt.Errorf("walk page without entity")
			}
			v := newNavWalkView(a.ds, *ref.Entity, tf)
			v.trail = append([]catalog.Entity{}, ref.Trail...)
			return v, nil
		case "types":
			if ref.Arg == "" {
				return nil, fmt.Errorf("type browser without a type")
			}
			return newNavBrowserView(a.ds, ref.Arg, tf), nil
		default:
			return newNavView(a.ds, tf), nil
		}
	case "waterfall":
		return newWaterfallView(a.ds, ref.TraceID, "", tf), nil
	case "timeline":
		v := newTimelineView(a.ds, ref.Arg, nil, tf)
		if ref.Lens > 0 && ref.Lens < len(catalog.SessionTimelineLenses) {
			v.lens = ref.Lens
		}
		return v, nil
	case "problem":
		if ref.Rec == nil {
			return nil, fmt.Errorf("problem page without record")
		}
		return newProblemView(a.ds, ref.Rec, time.Now()), nil
	case "inspector":
		return newInspectorView(a.ds, ref.Title, ref.Rec), nil
	}
	return nil, fmt.Errorf("unknown page kind %q", ref.Kind)
}

// recordHistory snapshots the current stack into the persistent history.
func (a *app) recordHistory() {
	a.hist.record(a.stack, a.tf.Label)
	a.hist.save()
}

// currentSignature identifies the live stack, so the history list can hide
// the page the user is already standing on.
func (a *app) currentSignature() string {
	var refs []pageRef
	for _, v := range a.stack {
		if ref, ok := pageRefOf(v); ok {
			refs = append(refs, ref)
		}
	}
	return historyEntry{Stack: refs, Context: a.hist.ctx}.signature()
}

// openHistory opens the history picker ('H', :history).
func (a *app) openHistory() tea.Cmd {
	a.hist.refresh() // pick up trails from other open sessions
	list := a.hist.forContext(a.currentSignature())
	if len(list) == 0 {
		return status("no history yet — it fills up as you navigate")
	}
	a.histList = list
	a.histSel = 0
	a.histActive = true
	return nil
}

func (a *app) updateHistPicker(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "H":
		a.histActive = false
		return nil
	case "up", "k":
		if a.histSel > 0 {
			a.histSel--
		}
		return nil
	case "down", "j":
		if a.histSel < len(a.histList)-1 {
			a.histSel++
		}
		return nil
	case "home", "g":
		a.histSel = 0
		return nil
	case "end", "G":
		a.histSel = len(a.histList) - 1
		return nil
	case "enter":
		a.histActive = false
		if a.histSel >= 0 && a.histSel < len(a.histList) {
			return a.restoreEntry(a.histList[a.histSel])
		}
		return nil
	}
	return nil
}

// restoreEntry rebuilds a trail's whole stack (so esc walks back through it)
// and refetches every level; the entry's timeframe is restored globally.
func (a *app) restoreEntry(entry historyEntry) tea.Cmd {
	tf, tfIdx := a.tf, -1
	for i, t := range catalog.Timeframes {
		if t.Label == entry.Timeframe {
			tf, tfIdx = t, i
			break
		}
	}
	var stack []viewModel
	for _, ref := range entry.Stack {
		view, err := a.viewFromRef(ref, tf)
		if err != nil {
			continue // a since-renamed view: restore the rest of the trail
		}
		stack = append(stack, view)
	}
	if len(stack) == 0 {
		return statusErr("this page can no longer be restored")
	}
	a.prev = a.stack
	a.stack = stack
	size := bodySizeMsg{width: a.width, height: a.bodyHeight()}
	var cmds []tea.Cmd
	for _, v := range stack {
		cmds = append(cmds, v.Update(size), v.Init())
	}
	if tfIdx >= 0 && tf.Label != a.tf.Label {
		a.tf, a.tfSel = tf, tfIdx
		// Covered views keep the window the header advertises (setTimeframe
		// semantics), so the toggled-away stack refreshes too.
		for _, v := range a.prev {
			if cmd := v.SetTimeframe(tf); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	a.recordHistory()
	return tea.Batch(cmds...)
}

func (a *app) renderHistory() string {
	var b strings.Builder
	b.WriteString(theme.OverlayTitle.Render("history") + "\n\n")
	ageW, trailW, tfW := 5, 58, 8
	rowW := ageW + trailW + tfW + 3
	limit := len(a.histList)
	if m := max(a.bodyHeight()-8, 4); limit > m {
		limit = m
	}
	offset := 0
	if a.histSel >= limit {
		offset = a.histSel - limit + 1
	}
	for i := offset; i < offset+limit && i < len(a.histList); i++ {
		e := a.histList[i]
		age := catalog.FormatDuration(time.Since(e.Visited))
		if i == a.histSel {
			row := pad(age, ageW) + " " + pad(e.trail(), trailW) + " " + pad("last "+e.Timeframe, tfW)
			b.WriteString(theme.Selected.Render(pad(" "+row, rowW)))
		} else {
			b.WriteString(" " + theme.Dim.Render(pad(age, ageW)) + " " +
				theme.HeaderVal.Render(pad(e.trail(), trailW)) + " " +
				theme.CrumbDim.Render(pad("last "+e.Timeframe, tfW)))
		}
		b.WriteString("\n")
	}
	if rest := len(a.histList) - offset - limit; rest > 0 {
		b.WriteString(theme.Dim.Render(fmt.Sprintf(" … %d more", rest)) + "\n")
	}
	b.WriteString("\n" + theme.Dim.Render("enter restore · j/k move · esc close — survives restarts"))
	return b.String()
}
