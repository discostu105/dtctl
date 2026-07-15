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

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
	"github.com/dynatrace-oss/dynatui/internal/tui/theme"
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
	Search   string           `json:"search,omitempty"`
	Searches []string         `json:"searches,omitempty"` // table's server-side searches
	Facets   []catalog.Facet  `json:"facets,omitempty"`   // table's server-side facets
	Arg      string           `json:"arg,omitempty"`      // scope arg (census → typed list)
	Lens     int              `json:"lens,omitempty"`     // table's active lens index
	Pattern  string           `json:"pattern,omitempty"`  // logs' DPL pattern scope
	TraceID  string           `json:"traceId,omitempty"`  // waterfall / trace-scoped tables
	DQL      string           `json:"dql,omitempty"`      // query editor content
	Entity   *catalog.Entity  `json:"entity,omitempty"`   // detail/metrics/relations/table scope
	Trail    []catalog.Entity `json:"trail,omitempty"`    // navigator walk path
	Title    string           `json:"title,omitempty"`    // inspector title
	Rec      map[string]any   `json:"rec,omitempty"`      // inspector record, kept verbatim
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
		if ref.Kind == "vulnerability" {
			fmt.Fprintf(&b, "/%s", catalog.Str(ref.Rec, "vulnerability.id"))
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
// historyCodec pairs one view kind's snapshot and rebuild directions, so
// adding a stack view means one entry here — the two halves can't drift
// apart in separate switches. ref snapshots a live view into its identity
// descriptor (returning false for other view types; Kind is stamped by the
// registry); make rebuilds a fresh view from a persisted descriptor.
type historyCodec struct {
	kind string
	ref  func(v viewModel) (pageRef, bool)
	make func(a *app, ref pageRef, tf catalog.Timeframe) (viewModel, error)
}

var historyCodecs = []historyCodec{
	{kind: "home",
		ref: func(v viewModel) (pageRef, bool) {
			hv, ok := v.(*homeView)
			if !ok {
				return pageRef{}, false
			}
			return pageRef{Crumb: hv.Crumb()}, true
		},
		make: func(a *app, _ pageRef, tf catalog.Timeframe) (viewModel, error) {
			return newHomeView(a.ds, tf), nil
		}},
	{kind: "table",
		ref: func(v viewModel) (pageRef, bool) {
			tv, ok := v.(*tableView)
			if !ok {
				return pageRef{}, false
			}
			ref := pageRef{Crumb: tv.Crumb(), View: tv.spec.Name,
				Filter: tv.filter, Searches: tv.searches, Facets: tv.facets,
				Arg: tv.scope.Arg, Lens: tv.scope.Lens, TraceID: tv.scope.TraceID,
				Pattern: tv.scope.Pattern}
			if tv.scope.Entity != nil {
				e := *tv.scope.Entity
				ref.Entity = &e
			}
			return ref, true
		},
		make: func(a *app, ref pageRef, tf catalog.Timeframe) (viewModel, error) {
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
		}},
	{kind: "query",
		ref: func(v viewModel) (pageRef, bool) {
			qv, ok := v.(*queryView)
			if !ok {
				return pageRef{}, false
			}
			dql := qv.current
			if dql == "" {
				dql = strings.TrimSpace(qv.editor.Value())
			}
			return pageRef{Crumb: qv.Crumb(), DQL: dql}, true
		},
		make: func(a *app, ref pageRef, tf catalog.Timeframe) (viewModel, error) {
			v := newQueryView(a.ds, ref.DQL, tf, a.qhist)
			if ref.DQL != "" {
				// Arrive on results, not the editor: Init re-runs a submitted query.
				v.current = ref.DQL
				v.editing = false
				v.editor.Blur()
			}
			return v, nil
		}},
	{kind: "detail",
		ref: func(v viewModel) (pageRef, bool) {
			dv, ok := v.(*detailView)
			if !ok {
				return pageRef{}, false
			}
			e := dv.entity
			return pageRef{Crumb: dv.Crumb(), Entity: &e}, true
		},
		make: func(a *app, ref pageRef, tf catalog.Timeframe) (viewModel, error) {
			if ref.Entity == nil {
				return nil, fmt.Errorf("detail page without entity")
			}
			return newDetailView(a.ds, *ref.Entity, nil, tf), nil
		}},
	{kind: "metrics",
		ref: func(v viewModel) (pageRef, bool) {
			mv, ok := v.(*metricsView)
			if !ok {
				return pageRef{}, false
			}
			e := mv.entity
			return pageRef{Crumb: mv.Crumb(), Entity: &e}, true
		},
		make: func(a *app, ref pageRef, tf catalog.Timeframe) (viewModel, error) {
			if ref.Entity == nil {
				return nil, fmt.Errorf("metrics page without entity")
			}
			return newMetricsView(a.ds, *ref.Entity, tf), nil
		}},
	{kind: "relations",
		ref: func(v viewModel) (pageRef, bool) {
			rv, ok := v.(*relationsView)
			if !ok {
				return pageRef{}, false
			}
			e := rv.entity
			return pageRef{Crumb: rv.Crumb(), Entity: &e}, true
		},
		make: func(a *app, ref pageRef, tf catalog.Timeframe) (viewModel, error) {
			if ref.Entity == nil {
				return nil, fmt.Errorf("relations page without entity")
			}
			return newRelationsView(a.ds, *ref.Entity, tf), nil
		}},
	{kind: "nav",
		ref: func(v viewModel) (pageRef, bool) {
			nv, ok := v.(*navView)
			if !ok {
				return pageRef{}, false
			}
			ref := pageRef{Crumb: nv.Crumb(), View: navModeName(nv.mode), Arg: nv.typ}
			if nv.mode == navBrowser && nv.search != "" {
				ref.View, ref.Arg = "search", nv.search
			}
			if nv.mode == navWalk {
				e := nv.root
				ref.Entity = &e
				ref.Trail = append([]catalog.Entity{}, nv.trail...)
			}
			return ref, true
		},
		make: func(a *app, ref pageRef, tf catalog.Timeframe) (viewModel, error) {
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
			case "search":
				if ref.Arg == "" {
					return nil, fmt.Errorf("name search without a term")
				}
				return newNavSearchView(a.ds, ref.Arg, tf), nil
			default:
				return newNavView(a.ds, tf), nil
			}
		}},
	{kind: "waterfall",
		ref: func(v viewModel) (pageRef, bool) {
			wv, ok := v.(*waterfallView)
			if !ok {
				return pageRef{}, false
			}
			return pageRef{Crumb: wv.Crumb(), TraceID: wv.traceID}, true
		},
		make: func(a *app, ref pageRef, tf catalog.Timeframe) (viewModel, error) {
			return newWaterfallView(a.ds, ref.TraceID, "", tf), nil
		}},
	{kind: "timeline",
		ref: func(v viewModel) (pageRef, bool) {
			tv, ok := v.(*timelineView)
			if !ok {
				return pageRef{}, false
			}
			return pageRef{Crumb: tv.Crumb(), Arg: tv.sessionID, Lens: tv.lens}, true
		},
		make: func(a *app, ref pageRef, tf catalog.Timeframe) (viewModel, error) {
			v := newTimelineView(a.ds, ref.Arg, nil, tf)
			if ref.Lens > 0 && ref.Lens < len(catalog.SessionTimelineLenses) {
				v.lens = ref.Lens
			}
			return v, nil
		}},
	{kind: "problem",
		ref: func(v viewModel) (pageRef, bool) {
			pv, ok := v.(*problemView)
			if !ok {
				return pageRef{}, false
			}
			return pageRef{Crumb: pv.Crumb(), Rec: pv.rec}, true
		},
		make: func(a *app, ref pageRef, _ catalog.Timeframe) (viewModel, error) {
			if ref.Rec == nil {
				return nil, fmt.Errorf("problem page without record")
			}
			return newProblemView(a.ds, ref.Rec, time.Now()), nil
		}},
	{kind: "vulnerability",
		ref: func(v viewModel) (pageRef, bool) {
			vv, ok := v.(*vulnerabilityView)
			if !ok {
				return pageRef{}, false
			}
			return pageRef{Crumb: vv.Crumb(), Rec: vv.rec}, true
		},
		make: func(a *app, ref pageRef, tf catalog.Timeframe) (viewModel, error) {
			if ref.Rec == nil {
				return nil, fmt.Errorf("vulnerability page without record")
			}
			return newVulnerabilityView(a.ds, ref.Rec, tf), nil
		}},
	{kind: "inspector",
		ref: func(v viewModel) (pageRef, bool) {
			iv, ok := v.(*inspectorView)
			if !ok {
				return pageRef{}, false
			}
			return pageRef{Crumb: iv.Crumb(), Title: iv.title, Rec: iv.rec}, true
		},
		make: func(a *app, ref pageRef, _ catalog.Timeframe) (viewModel, error) {
			return newInspectorView(a.ds, ref.Title, ref.Rec), nil
		}},
}

// pageRefOf snapshots a live view through the codec registry ("" ok for
// view types that don't persist, e.g. page tabs).
func pageRefOf(v viewModel) (pageRef, bool) {
	for _, c := range historyCodecs {
		if ref, ok := c.ref(v); ok {
			ref.Kind = c.kind
			return ref, true
		}
	}
	return pageRef{}, false
}

// viewFromRef rebuilds a fresh view from a persisted page descriptor.
func (a *app) viewFromRef(ref pageRef, tf catalog.Timeframe) (viewModel, error) {
	for _, c := range historyCodecs {
		if c.kind == ref.Kind {
			return c.make(a, ref, tf)
		}
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
// historyPicker is the 'H' overlay over the persistent trail history; list
// is a snapshot taken when it opened.
type historyPicker struct {
	app  *app
	list []historyEntry
	sel  int
}

func (a *app) openHistory() tea.Cmd {
	a.hist.refresh() // pick up trails from other open sessions
	list := a.hist.forContext(a.currentSignature())
	if len(list) == 0 {
		return status("no history yet — it fills up as you navigate")
	}
	a.overlay = &historyPicker{app: a, list: list}
	return nil
}

func (p *historyPicker) Hints() []keyHint {
	return []keyHint{{"enter", "restore"}, {"j/k", "move"}, {"esc", "close"}}
}

func (p *historyPicker) HandleKey(msg tea.KeyMsg) tea.Cmd {
	a := p.app
	key := msg.String()
	switch key {
	case "esc", "H":
		a.overlay = nil
		return nil
	case "home", "g":
		p.sel = 0
		return nil
	case "end", "G":
		p.sel = len(p.list) - 1
		return nil
	case "enter":
		a.overlay = nil
		if p.sel >= 0 && p.sel < len(p.list) {
			return a.restoreEntry(p.list[p.sel])
		}
		return nil
	}
	moveSel(key, &p.sel, len(p.list))
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
		a.tf = tf
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

func (p *historyPicker) View(width, height int) string {
	var b strings.Builder
	b.WriteString(theme.OverlayTitle.Render("history") + "\n\n")
	ageW, trailW, tfW := 5, 58, 8
	rowW := ageW + trailW + tfW + 3
	limit := max(height-8, 4)
	overlayRows(&b, len(p.list), p.sel, limit, func(i int) string {
		e := p.list[i]
		age := catalog.FormatDuration(time.Since(e.Visited))
		if i == p.sel {
			row := pad(age, ageW) + " " + pad(e.trail(), trailW) + " " + pad("last "+e.Timeframe, tfW)
			return theme.Selected.Render(pad(" "+row, rowW))
		}
		return " " + theme.Dim.Render(pad(age, ageW)) + " " +
			theme.HeaderVal.Render(pad(e.trail(), trailW)) + " " +
			theme.CrumbDim.Render(pad("last "+e.Timeframe, tfW))
	})
	b.WriteString("\n" + theme.Dim.Render("enter restore · j/k move · esc close — survives restarts"))
	return centerOverlay(width, height, b.String())
}
