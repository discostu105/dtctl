package tui

import (
	"fmt"
	"math"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dtctl/pkg/output"
	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
	"github.com/dynatrace-oss/dtctl/pkg/tui/theme"
)

// metricsView renders the canned per-entity-type charts behind the 'm' drill
// (host CPU/memory/disk, service RED). It reuses the existing braille
// renderer from pkg/output.
type metricsView struct {
	ds     *dataSource
	entity catalog.Entity
	tf     catalog.Timeframe
	mspec  *catalog.MetricsSpec

	rec     map[string]any
	loading bool
	err     error
	seq     int
	dql     string
}

func newMetricsView(ds *dataSource, entity catalog.Entity, tf catalog.Timeframe) *metricsView {
	return &metricsView{ds: ds, entity: entity, tf: tf, mspec: catalog.MetricsFor(entity.Type)}
}

func (v *metricsView) Init() tea.Cmd { return v.Refresh() }

func (v *metricsView) Refresh() tea.Cmd {
	if v.mspec == nil {
		return nil
	}
	v.seq++
	v.loading = true
	v.err = nil
	v.dql = v.mspec.Query(v.entity, v.tf)
	return v.ds.query(v, v.seq, v.dql)
}

func (v *metricsView) SetTimeframe(tf catalog.Timeframe) tea.Cmd {
	v.tf = tf
	return v.Refresh()
}

func (v *metricsView) InputActive() bool { return false }

func (v *metricsView) Crumb() string {
	return fmt.Sprintf("metrics (%s)", entityName(v.entity))
}

func (v *metricsView) Echo() string {
	if v.dql == "" {
		return ""
	}
	return fmt.Sprintf("dtctl query '%s'", strings.ReplaceAll(v.dql, "\n", " "))
}

func (v *metricsView) Hints() []keyHint { return nil }

// DQL reveals the charts' timeseries query (ctrl+q).
func (v *metricsView) DQL() string { return v.dql }

// Selection exposes the charted entity (pin, relations, open in browser).
func (v *metricsView) Selection() (map[string]any, *catalog.Entity) {
	entity := v.entity
	return nil, &entity
}

func (v *metricsView) Update(msg tea.Msg) tea.Cmd {
	if msg, ok := msg.(dataMsg); ok {
		if msg.owner != any(v) || msg.seq != v.seq {
			return nil
		}
		v.loading = false
		v.err = msg.err
		v.rec = nil
		if msg.err == nil && len(msg.records) > 0 {
			v.rec = msg.records[0]
		}
	}
	return nil
}

func (v *metricsView) View(width, height int) string {
	var b strings.Builder
	title := fmt.Sprintf("%s · %s · last %s", v.entity.Name, v.entity.Type, v.tf.Label)
	b.WriteString(theme.GroupTitle.Render(title) + "\n")

	switch {
	case v.loading:
		b.WriteString(theme.Spinner.Render("⟳ loading…"))
		return b.String()
	case v.err != nil:
		b.WriteString(theme.Error.Render(wrap(v.err.Error(), width)))
		return b.String()
	case v.rec == nil:
		b.WriteString(theme.Dim.Render("no data in timeframe / not monitored"))
		return b.String()
	}

	chartWidth := width - 2
	if chartWidth < 10 {
		chartWidth = 10
	}
	for _, series := range v.mspec.Series {
		values := floatSeries(v.rec[series.Alias])
		b.WriteString("\n" + theme.Label.Render(series.Title))
		if len(values) == 0 {
			b.WriteString("  " + theme.Dim.Render("no data") + "\n")
			continue
		}
		minV, maxV, avg, last := seriesStats(values)
		b.WriteString(theme.Dim.Render(fmt.Sprintf("  min %s  avg %s  max %s  last %s%s",
			formatMetric(minV), formatMetric(avg), formatMetric(maxV), formatMetric(last), series.Unit)))
		b.WriteString("\n")

		graph := output.NewBrailleGraph(chartWidth, 3)
		graph.PlotLine(values, minV, maxV)
		b.WriteString(theme.Chart.Render(indent(graph.Render(), 1)) + "\n")
	}
	return b.String()
}

// floatSeries extracts the numeric points of a timeseries array field,
// skipping nulls (gaps compress visually, which is fine for a sparkchart).
func floatSeries(v any) []float64 {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]float64, 0, len(arr))
	for _, e := range arr {
		if f, ok := e.(float64); ok && !math.IsNaN(f) {
			out = append(out, f)
		}
	}
	return out
}

func seriesStats(values []float64) (minV, maxV, avg, last float64) {
	minV, maxV = values[0], values[0]
	var sum float64
	for _, f := range values {
		if f < minV {
			minV = f
		}
		if f > maxV {
			maxV = f
		}
		sum += f
	}
	return minV, maxV, sum / float64(len(values)), values[len(values)-1]
}

func formatMetric(f float64) string {
	switch {
	case math.Abs(f) >= 1_000_000:
		return fmt.Sprintf("%.1fM", f/1_000_000)
	case math.Abs(f) >= 10_000:
		return fmt.Sprintf("%.1fk", f/1_000)
	case f == math.Trunc(f):
		return fmt.Sprintf("%.0f", f)
	default:
		return fmt.Sprintf("%.2f", f)
	}
}
