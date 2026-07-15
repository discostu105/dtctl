// Enter-key drill dispatch: what opening a table row means — inspector,
// detail page, spec drills, trace waterfall, session timeline.
package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
)

// inspect opens the raw record inspector for a row, titled by the most
// specific identity the record offers (a "detectors › detectors" crumb says
// nothing).
func (v *tableView) inspect(rec map[string]any) tea.Cmd {
	title := v.spec.Name
	if e := v.entityOf(rec); e != nil && e.Name != "" {
		title = e.Name
	} else {
		for _, key := range []string{"title", "name", "display_id"} {
			if t := catalog.Str(rec, key); t != "" {
				title = t
				break
			}
		}
	}
	return func() tea.Msg { return inspectMsg{title: title, rec: rec} }
}

// drill opens the target view scoped to the selected row's entity. The
// special targets: "metrics" (canned charts), "trace" (waterfall jump via
// the record's trace id), "patterns" (analyze the whole current list).
func (v *tableView) drill(target string) tea.Cmd {
	if target == "patterns" {
		// Pattern extraction describes the list being looked at, not one
		// row: it inherits the view's FULL scope (entity, trace, pattern)
		// and its server-side narrowing, and needs no selection.
		spec := catalog.Lookup("patterns")
		scope := catalog.Scope{Entity: v.scope.Entity, Timeframe: v.scope.Timeframe,
			TraceID: v.scope.TraceID, Pattern: v.scope.Pattern}
		return func() tea.Msg {
			return pushViewMsg{spec: spec, scope: scope, searches: v.searches, facets: v.facets}
		}
	}
	rec := v.selected()
	if rec == nil {
		return nil
	}
	if target == "trace" {
		return v.openTrace(rec)
	}
	if target == "session" {
		return v.openSession(rec)
	}
	if target == "trace-logs" {
		if v.spec.Trace == nil {
			return nil
		}
		id := v.spec.Trace(rec)
		if id == "" {
			return statusErr("record carries no trace id")
		}
		spec := catalog.Lookup("logs")
		scope := catalog.Scope{Timeframe: v.scope.Timeframe, TraceID: id}
		return func() tea.Msg { return pushViewMsg{spec: spec, scope: scope} }
	}
	entity := v.entityOf(rec)
	if entity == nil {
		return statusErr("selection carries no entity to scope by")
	}
	if target == "metrics" {
		// Types without canned charts get the metric explorer scoped to the
		// entity — every type has discoverable metrics, curated or not.
		if catalog.MetricsFor(entity.Type) == nil {
			spec := catalog.Lookup("metrics")
			scope := catalog.Scope{Entity: entity, Timeframe: v.scope.Timeframe}
			return func() tea.Msg { return pushViewMsg{spec: spec, scope: scope} }
		}
		return func() tea.Msg { return metricsMsg{entity: *entity} }
	}
	if target == "traces" && !catalog.SpanScopable(entity.Type) {
		return statusErr(fmt.Sprintf("spans carry no %s scope field", entity.Type))
	}
	spec := catalog.Lookup(target)
	if spec == nil {
		return statusErr(fmt.Sprintf("unknown view %q", target))
	}
	scope := catalog.Scope{Entity: entity, Timeframe: v.scope.Timeframe}
	if target == "traces" {
		// Scoped drills skip the roots lens — see DefaultSpanLens.
		scope.Lens = catalog.DefaultSpanLens(entity.Type)
	}
	return func() tea.Msg { return pushViewMsg{spec: spec, scope: scope} }
}

// openTrace jumps to the waterfall of the selected row's trace, anchored on
// the span the jump came from (a 500-span trace must not open at the root
// and leave the user hunting for the row they were standing on).
func (v *tableView) openTrace(rec map[string]any) tea.Cmd {
	if v.spec.Trace == nil {
		return nil
	}
	id := v.spec.Trace(rec)
	if id == "" {
		return statusErr("record carries no trace id")
	}
	span := catalog.Str(rec, "span.id")
	return func() tea.Msg { return waterfallMsg{traceID: id, focusSpanID: span} }
}

// openSession jumps to the session timeline of the selected row (a sessions
// row via enter, or any RUM event row via the 'u' drill).
func (v *tableView) openSession(rec map[string]any) tea.Cmd {
	id := catalog.Str(rec, "dt.rum.session.id")
	if id == "" {
		return statusErr("record carries no session id")
	}
	// Only a sessions-list row IS the session's record; a RUM event row (the
	// 'u' drill — it carries a classifier) merely names the session, and the
	// timeline fetches the record itself.
	session := rec
	if catalog.Str(rec, "characteristics.classifier") != "" {
		session = nil
	}
	return func() tea.Msg { return timelineMsg{sessionID: id, rec: session} }
}

func (v *tableView) entityOf(rec map[string]any) *catalog.Entity {
	if v.spec.Entity == nil {
		return nil
	}
	return v.spec.Entity(rec)
}
