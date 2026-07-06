package tui

import (
	"fmt"
	"math"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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

// Busy reports whether the timeseries query is in flight.
func (v *metricsView) Busy() bool { return v.loading }

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

// axisW is the y-axis label column: right-aligned max/min values ahead of
// the ┤ ticks.
const axisW = 8

func (v *metricsView) View(width, height int) string {
	var b strings.Builder
	title := " " + theme.OverlayTitle.Render(v.entity.Name) + "  " + theme.Badge.Render(v.entity.Type) +
		theme.Dim.Render("  last "+v.tf.Label)
	b.WriteString(title + "\n")

	switch {
	case v.loading:
		b.WriteString(" " + theme.Spinner.Render(theme.Spin()+" loading…"))
		return b.String()
	case v.err != nil:
		b.WriteString(theme.Error.Render("✗ " + wrap(v.err.Error(), width-2)))
		return b.String()
	case v.rec == nil:
		b.WriteString("\n" + lipgloss.PlaceHorizontal(width, lipgloss.Center,
			theme.Dim.Render("∅ no data in timeframe / not monitored")))
		return b.String()
	}

	n := len(v.mspec.Series)
	chartW := width - axisW - 2
	if chartW < 10 {
		chartW = 10
	}
	// Charts share the body height btop-style: title + per-series header
	// lines + one time-axis line are fixed, the rest divides into chart rows.
	chartRows := (height - 2 - 2*n) / n
	if chartRows < 2 {
		chartRows = 2
	}
	if chartRows > 9 {
		chartRows = 9
	}

	for i, series := range v.mspec.Series {
		style := theme.SeriesAt(i)
		values := floatSeries(v.rec[series.Alias])
		b.WriteString("\n")
		if len(values) == 0 {
			b.WriteString(" " + style.Bold(true).Render("● "+series.Title) +
				"  " + theme.Dim.Render("no data") + "\n")
			continue
		}
		minV, maxV, avg, last := seriesStats(values)
		// Filled area charts read from a zero baseline (min-based scaling
		// exaggerates noise); percent metrics scale to a true 0–100 gauge.
		plotMin := math.Min(0, minV)
		plotMax := maxV
		if series.Unit == "%" && maxV <= 100 {
			plotMax = 100
		}
		if plotMax <= plotMin {
			plotMax = plotMin + 1
		}

		header := " " + style.Bold(true).Render("● "+series.Title) +
			"  " + theme.OverlayTitle.Render(fmtUnit(last, series.Unit)) +
			theme.Dim.Render(fmt.Sprintf("   min %s · avg %s · max %s",
				fmtUnit(minV, series.Unit), fmtUnit(avg, series.Unit), fmtUnit(maxV, series.Unit)))
		b.WriteString(ansi.Truncate(header, width, "…") + "\n")

		graph := output.NewBrailleGraph(chartW, chartRows)
		graph.PlotFilled(values, plotMin, plotMax)
		rows := strings.Split(graph.Render(), "\n")
		grad := theme.Gradient(theme.SeriesColorAt(i), len(rows))
		for r, rowStr := range rows {
			switch {
			case r == 0:
				b.WriteString(theme.Dim.Render(cell(fmtUnit(plotMax, series.Unit), axisW, true)) + theme.Track.Render("┤"))
			case r == len(rows)-1:
				b.WriteString(theme.Dim.Render(cell(fmtUnit(plotMin, series.Unit), axisW, true)) + theme.Track.Render("┤"))
			default:
				b.WriteString(strings.Repeat(" ", axisW) + theme.Track.Render("│"))
			}
			b.WriteString(grad[r].Render(rowStr) + "\n")
		}
	}

	// One shared time axis: every chart spans the same window.
	leftLbl := " " + v.tf.Label + " ago"
	gap := chartW - len(leftLbl) - 3
	if gap < 1 {
		gap = 1
	}
	b.WriteString(strings.Repeat(" ", axisW) + theme.Track.Render("└") +
		theme.Dim.Render(leftLbl) + strings.Repeat(" ", gap) + theme.Dim.Render("now"))
	return b.String()
}

// fmtUnit renders a metric value in its series unit ("B" gets IEC bytes,
// "%" and "ms" attach their suffix, anything else appends the unit label).
func fmtUnit(f float64, unit string) string {
	switch unit {
	case "%":
		return formatMetric(f) + "%"
	case "B":
		if f >= 0 {
			return catalog.FormatBytes(int64(f))
		}
		return formatMetric(f) + " B"
	case "ms":
		return formatMetric(f) + " ms"
	case "":
		return formatMetric(f)
	default:
		return formatMetric(f) + " " + unit
	}
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
	case math.Abs(f) >= 1_000_000_000:
		return fmt.Sprintf("%.1fG", f/1_000_000_000)
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
