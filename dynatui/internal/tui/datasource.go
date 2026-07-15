package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dtctl/pkg/exec"
	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
)

// Source fetches records for an API-backed view (catalog.Spec.API): REST
// lists and analyzer executions. dql is the spec's composed Query output
// ("" when the spec has none) — the log-patterns source analyzes it. Sources
// are constructed in cmd/tui.go; no HTTP lives in pkg/tui.
type Source func(ctx context.Context, scope catalog.Scope, dql string) ([]map[string]any, error)

// dataSource adapts the existing DQL executor to bubbletea: every query runs
// as a tea.Cmd goroutine and delivers a dataMsg. No HTTP lives in pkg/tui.
type dataSource struct {
	exec *exec.DQLExecutor
	// sources are the named non-DQL backends for API-backed views.
	sources map[string]Source
	// runFn replaces the executor in tests; nil in production.
	runFn func(dql string) ([]map[string]any, error)

	// dict caches the semantic dictionary's field definitions for the whole
	// session (fetched once, on the first inspector). Reads and writes both
	// happen on bubbletea's single update goroutine — no locking needed.
	dict          map[string]catalog.FieldDoc
	dictRequested bool

	// segments is the global segment scope, applied to every DQL query here —
	// the one place all queries are built — so views need no per-view
	// plumbing. Sent out-of-band as filterSegments on query:execute; the
	// API-backed sources above deliberately don't see them (REST endpoints
	// have no segment parameter).
	segments []exec.FilterSegmentRef

	// previewOff inverts the app-wide peek-pane preference (the P toggle).
	// It rides on the shared dataSource — the one object every view already
	// holds — so panes nested inside detail tabs and views pushed later
	// honor the one preference without plumbing. Stored inverted so the
	// zero value keeps the default of on: the pane costs nothing (it
	// renders the row already fetched), and the size gates in table.go hide
	// it where it cannot fit.
	previewOff bool
}

// previewOn reports the app-wide peek-pane preference (toggled with P).
func (d *dataSource) previewOn() bool { return !d.previewOff }

// execOpts builds one query's execution options — extracted so tests can
// assert the segment injection without HTTP (the runFn seam skips it).
// enrichMetrics requests metric-catalogue enrichment (metadata.metrics[]
// with displayName/description/unit) — set for explorer chart queries only,
// as the extra catalogue lookup is wasted on non-timeseries queries.
func (d *dataSource) execOpts(maxRecords int64, enrichMetrics bool) exec.DQLExecuteOptions {
	opts := exec.DQLExecuteOptions{
		MaxResultRecords:    maxRecords,
		FetchTimeoutSeconds: 60,
		Segments:            d.segments,
		// ShowProgress stays false: the progress bar draws on stderr and
		// would tear the TUI's alternate screen.
	}
	if enrichMetrics {
		opts.MetadataFields = []string{"metrics"}
	}
	return opts
}

// echoQuery renders a view's copyable CLI equivalent of a DQL fetch,
// carrying the active segments — without the -S flags the command would
// return different rows than the screen shows.
func (d *dataSource) echoQuery(dql string) string {
	if dql == "" {
		return ""
	}
	out := fmt.Sprintf("dtctl query '%s'", strings.Join(strings.Fields(strings.ReplaceAll(dql, "\n", " ")), " "))
	for _, ref := range d.segments {
		out += " -S " + ref.ID
	}
	return out
}

// dictOwner tags the one-shot dictionary fetch; the app routes its result
// into the shared cache instead of a view.
type dictOwner struct{}

// ensureDict fires the session's dictionary fetch on first use.
func (d *dataSource) ensureDict() tea.Cmd {
	if d.dictRequested {
		return nil
	}
	d.dictRequested = true
	return d.queryCapped(dictOwner{}, 0, catalog.FieldDocsQuery(), catalog.FieldDocLimit)
}

// fieldDoc explains a record key from the semantic dictionary ("" zero value
// when unknown or not yet loaded).
func (d *dataSource) fieldDoc(key string) (catalog.FieldDoc, bool) {
	doc, ok := d.dict[key]
	return doc, ok
}

// dataMsg is the result of an async query. owner identifies the query slot
// that issued it: the view itself for its main query, or a per-slot tag type
// (enrichOwner, pulseOwner, …) for secondary queries. The tag TYPE is the
// slot's compile-checked routing key — tags may carry routing payload
// (panelOwner.idx, facetOwner.field) — while seq guards staleness; most
// slots deliberately share their view's seq so one Refresh invalidates every
// in-flight secondary at once. metrics is the response's metric-catalogue
// metadata (metadata.metrics[]); it is populated only for queries issued via
// queryEnriched.
type dataMsg struct {
	owner   any
	seq     int
	dql     string
	records []map[string]any
	metrics []exec.MetricInfo
	elapsed time.Duration
	err     error
}

// call runs a named API source asynchronously, delivering the same dataMsg
// shape as query so owner/seq staleness handling applies unchanged. Like
// queries, calls are never cancelled mid-flight; stale results are dropped
// via seq.
func (d *dataSource) call(owner any, seq int, name string, scope catalog.Scope, dql string) tea.Cmd {
	return func() tea.Msg {
		start := time.Now()
		src := d.sources[name]
		if src == nil {
			return dataMsg{owner: owner, seq: seq, dql: dql, elapsed: time.Since(start),
				err: fmt.Errorf("data source %q is not wired", name)}
		}
		records, err := src(context.Background(), scope, dql)
		return dataMsg{owner: owner, seq: seq, dql: dql, elapsed: time.Since(start), records: records, err: err}
	}
}

// query runs a DQL query asynchronously. In-flight queries are never
// cancelled mid-poll (the executor prints a cancellation notice to stderr,
// which would corrupt the alternate screen); stale results are dropped via
// seq instead.
func (d *dataSource) query(owner any, seq int, dql string) tea.Cmd {
	return d.run(owner, seq, dql, 1000, false)
}

// queryEnriched is query with metric-catalogue enrichment: the response's
// metadata.metrics[] (displayName/description/unit) is delivered on the
// dataMsg. Used by explorer chart queries, where the unit is unknowable
// client-side.
func (d *dataSource) queryEnriched(owner any, seq int, dql string) tea.Cmd {
	return d.run(owner, seq, dql, 1000, true)
}

// queryCapped is query with an explicit result-record cap (the dictionary
// fetch needs more than the default view page).
func (d *dataSource) queryCapped(owner any, seq int, dql string, maxRecords int64) tea.Cmd {
	return d.run(owner, seq, dql, maxRecords, false)
}

func (d *dataSource) run(owner any, seq int, dql string, maxRecords int64, enrichMetrics bool) tea.Cmd {
	return func() tea.Msg {
		start := time.Now()
		if d.runFn != nil {
			records, err := d.runFn(dql)
			return dataMsg{owner: owner, seq: seq, dql: dql, elapsed: time.Since(start), records: records, err: err}
		}
		resp, err := d.exec.ExecuteQueryWithContext(context.Background(), dql, d.execOpts(maxRecords, enrichMetrics))
		msg := dataMsg{owner: owner, seq: seq, dql: dql, elapsed: time.Since(start), err: rewriteSegmentVarError(err)}
		if err == nil {
			if resp == nil {
				msg.err = errors.New("query cancelled")
			} else {
				msg.records = resp.GetRecords()
				msg.metrics = resp.GetMetrics()
			}
		}
		return msg
	}
}
