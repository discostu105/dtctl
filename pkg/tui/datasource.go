package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dtctl/pkg/exec"
	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
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
}

// dataMsg is the result of an async query. owner identifies the view that
// issued it (results for popped or superseded views are discarded by seq).
type dataMsg struct {
	owner   any
	seq     int
	dql     string
	records []map[string]any
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
	return func() tea.Msg {
		start := time.Now()
		if d.runFn != nil {
			records, err := d.runFn(dql)
			return dataMsg{owner: owner, seq: seq, dql: dql, elapsed: time.Since(start), records: records, err: err}
		}
		resp, err := d.exec.ExecuteQueryWithContext(context.Background(), dql, exec.DQLExecuteOptions{
			MaxResultRecords:    1000,
			FetchTimeoutSeconds: 60,
			// ShowProgress stays false: the progress bar draws on stderr and
			// would tear the TUI's alternate screen.
		})
		msg := dataMsg{owner: owner, seq: seq, dql: dql, elapsed: time.Since(start), err: err}
		if err == nil {
			if resp == nil {
				msg.err = errors.New("query cancelled")
			} else {
				msg.records = resp.GetRecords()
			}
		}
		return msg
	}
}
