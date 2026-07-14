package tui

import (
	"fmt"
	"math"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtctl/pkg/exec"
	"github.com/dynatrace-oss/dtctl/pkg/output"
	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
	"github.com/dynatrace-oss/dynatui/internal/tui/theme"
)

// metricsView renders timeseries charts with the braille renderer from
// pkg/output, in two modes: the canned per-entity-type charts behind the 'm'
// drill (host CPU/memory/disk, service RED), and the explorer chart behind
// enter on a metric-explorer row (one arbitrary key, cycling aggregations,
// splittable by dimension).
//
// Canned charts fetch in two phases: an availability probe (`metrics`
// summarized by key) picks which of the spec's series exist for the scope,
// then one timeseries query charts that subset — a timeseries query returns
// zero records when ANY requested metric is entirely absent (validated live),
// so charting blindly blanks the page for entities missing one metric.
type metricsView struct {
	ds     *dataSource
	entity catalog.Entity // zero ID = unscoped explorer chart
	tf     catalog.Timeframe
	mspec  *catalog.MetricsSpec

	// Explorer-chart mode ('a' cycles the aggregation; canned mode ignores).
	key    string
	aggIdx int
	// meta is the explored key's catalogue identity from the enriched chart
	// response (canned charts carry hand-curated units on their spec instead).
	meta metricMeta

	// Split mode ('b'): chart the metric per value of one dimension.
	splitDim string
	dims     []dimInfo // discovered dimensions (split picker)
	dimPick  bool      // picker overlay open
	dimSel   int

	// unavailable lists canned series hidden by the availability probe.
	unavailable []string

	rec     map[string]any   // aggregate result (single record)
	records []map[string]any // split results (one record per dim value)
	loading bool
	err     error
	seq     int
	dql     string
}

// dimInfo is one dimension of the charted metric, with the distinct values
// seen in the sampled series records.
type dimInfo struct {
	name   string
	values int
}

// metricMeta is a metric's catalogue identity — displayName, description, and
// unit (normalized to a FormatUnit token) — extracted from the chart query's
// metric-metadata enrichment (metadata.metrics[]).
type metricMeta struct {
	unit string
	name string
	desc string
}

// availOwner tags the canned view's availability probe; dimOwner tags the
// split picker's dimension discovery (both share the view's seq generation).
type availOwner struct{ v *metricsView }
type dimOwner struct{ v *metricsView }

func newMetricsView(ds *dataSource, entity catalog.Entity, tf catalog.Timeframe) *metricsView {
	return &metricsView{ds: ds, entity: entity, tf: tf, mspec: catalog.MetricsFor(entity.Type)}
}

// newMetricChartView opens the explorer chart for one metric key, scoped to
// the entity the explorer itself was scoped to (zero entity = whole tenant).
func newMetricChartView(ds *dataSource, key string, entity catalog.Entity, tf catalog.Timeframe) *metricsView {
	return &metricsView{ds: ds, entity: entity, tf: tf, key: key,
		mspec: catalog.ExploreMetricsSpec(key, catalog.ChartAggs[0])}
}

// explore reports whether the view charts one explorer-picked key.
func (v *metricsView) explore() bool { return v.key != "" }

func (v *metricsView) agg() string { return catalog.ChartAggs[v.aggIdx] }

func (v *metricsView) Init() tea.Cmd { return v.Refresh() }

func (v *metricsView) Refresh() tea.Cmd {
	if v.mspec == nil {
		return nil
	}
	v.seq++
	v.loading = true
	v.err = nil
	v.rec = nil
	v.records = nil
	if v.explore() {
		if v.splitDim != "" {
			v.dql = catalog.MetricSplitQuery(v.key, v.agg(), v.splitDim, v.entity, v.tf)
		} else {
			v.dql = v.mspec.Query(v.entity, v.tf, nil)
		}
		// Enriched: the response's metadata.metrics[] carries the key's
		// catalogue unit/displayName/description, which no client-side table
		// could know for arbitrary (custom) metrics.
		return v.ds.queryEnriched(v, v.seq, v.dql)
	}
	// Canned charts: probe which of the spec's metrics exist first. The probe
	// is the view's query until the chart query supersedes it (Echo, ctrl+q).
	v.unavailable = nil
	v.dql = v.mspec.AvailabilityQuery(v.entity, v.tf)
	return v.ds.query(availOwner{v}, v.seq, v.dql)
}

func (v *metricsView) SetTimeframe(tf catalog.Timeframe) tea.Cmd {
	v.tf = tf
	v.dims = nil // window changed; rediscover dimensions on next 'b'
	return v.Refresh()
}

// InputActive claims the keyboard while the split picker is open, so global
// single-letter keys (q, digits) don't fire mid-selection.
func (v *metricsView) InputActive() bool { return v.dimPick }

// Busy reports whether a query (probe or chart) is in flight.
func (v *metricsView) Busy() bool { return v.loading }

func (v *metricsView) Crumb() string {
	if v.explore() {
		crumb := v.key
		if v.splitDim != "" {
			crumb += " by " + v.splitDim
		}
		if v.entity.ID != "" {
			crumb += fmt.Sprintf(" (%s)", entityName(v.entity))
		}
		return crumb
	}
	return fmt.Sprintf("metrics (%s)", entityName(v.entity))
}

func (v *metricsView) Echo() string { return v.ds.echoQuery(v.dql) }

func (v *metricsView) Hints() []keyHint {
	if v.dimPick {
		return []keyHint{{"j/k", "move"}, {"enter", "split"}, {"esc", "close"}}
	}
	if v.explore() {
		return []keyHint{{"a", "aggregation"}, {"b", "split by dimension"}}
	}
	return []keyHint{{"enter", "all metrics"}}
}

// DQL reveals the charts' timeseries query (ctrl+q).
func (v *metricsView) DQL() string { return v.dql }

// Selection exposes the charted entity (pin, relations, open in browser);
// an unscoped explorer chart carries none.
func (v *metricsView) Selection() (map[string]any, *catalog.Entity) {
	if v.entity.ID == "" {
		return nil, nil
	}
	entity := v.entity
	return nil, &entity
}

func (v *metricsView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case dataMsg:
		return v.onData(msg)

	case tea.KeyMsg:
		if v.dimPick {
			return v.pickerKey(msg.String())
		}
		switch msg.String() {
		case "a":
			if !v.explore() {
				return nil
			}
			v.aggIdx = (v.aggIdx + 1) % len(catalog.ChartAggs)
			v.mspec = catalog.ExploreMetricsSpec(v.key, v.agg())
			return tea.Batch(v.Refresh(), status("aggregation: "+v.agg()))
		case "b":
			if !v.explore() {
				return nil
			}
			if v.dims != nil {
				v.dimPick = true
				return claimKey
			}
			// Discover the metric's dimensions from sampled series records.
			v.loading = true
			return v.ds.query(dimOwner{v}, v.seq, catalog.MetricDimsQuery(v.key, v.entity, v.tf))
		case "enter":
			// The canned charts are a curated slice — enter opens the full
			// per-environment metric explorer scoped to the same entity.
			if v.explore() {
				return nil
			}
			spec := catalog.Lookup("metrics")
			if spec == nil {
				return nil
			}
			entity := v.entity
			scope := catalog.Scope{Entity: &entity, Timeframe: v.tf}
			return func() tea.Msg { return pushViewMsg{spec: spec, scope: scope} }
		}
	}
	return nil
}

// onData routes the three result kinds: availability probe, dimension
// discovery, and the chart query itself.
func (v *metricsView) onData(msg dataMsg) tea.Cmd {
	if ao, ok := msg.owner.(availOwner); ok && ao.v == v {
		if msg.seq != v.seq {
			return nil
		}
		if msg.err != nil {
			// Probe failed — degrade to charting the full series list.
			v.dql = v.mspec.Query(v.entity, v.tf, nil)
			return v.ds.query(v, v.seq, v.dql)
		}
		available := map[string]bool{}
		for _, rec := range msg.records {
			if k := catalog.Str(rec, "metric.key"); k != "" {
				available[k] = true
			}
		}
		for _, s := range v.mspec.Series {
			if !available[s.Key] {
				v.unavailable = append(v.unavailable, s.Title)
			}
		}
		v.dql = v.mspec.Query(v.entity, v.tf, available)
		if v.dql == "" {
			v.loading = false // nothing reports; View shows ∅
			return nil
		}
		return v.ds.query(v, v.seq, v.dql)
	}
	if do, ok := msg.owner.(dimOwner); ok && do.v == v {
		if msg.seq != v.seq {
			return nil
		}
		v.loading = false
		if msg.err != nil {
			return statusErr("dimensions: " + msg.err.Error())
		}
		v.dims = discoverDims(msg.records)
		if len(v.dims) == 0 {
			return status("metric has no dimensions to split by")
		}
		v.dimPick = true
		v.dimSel = 0
		return nil
	}
	if msg.owner != any(v) || msg.seq != v.seq {
		return nil
	}
	v.loading = false
	v.err = msg.err
	v.rec = nil
	v.records = nil
	if msg.err == nil && len(msg.records) > 0 {
		v.rec = msg.records[0]
		v.records = msg.records
	}
	if v.explore() {
		v.applyMetricMeta(msg.metrics)
	}
	return nil
}

// applyMetricMeta picks the explored key's catalogue entry out of the
// enriched response metadata. A response without one (enrichment unavailable,
// query error) keeps the last known identity — the key doesn't change across
// aggregation cycles or splits, so stale is better than blank.
func (v *metricsView) applyMetricMeta(infos []exec.MetricInfo) {
	for _, mi := range infos {
		if mi.MetricKey != "" && mi.MetricKey != v.key {
			continue
		}
		v.meta = metricMeta{unit: catalog.NormalizeUnit(mi.Unit), name: mi.DisplayName, desc: mi.Description}
		return
	}
}

// pickerKey drives the split-dimension picker overlay.
func (v *metricsView) pickerKey(key string) tea.Cmd {
	options := len(v.dims) + 1 // "(aggregate)" + dims
	switch key {
	case "esc", "b":
		v.dimPick = false
		return claimKey
	case "up", "k":
		v.dimSel = (v.dimSel + options - 1) % options
	case "down", "j":
		v.dimSel = (v.dimSel + 1) % options
	case "enter":
		v.dimPick = false
		dim := ""
		if v.dimSel > 0 {
			dim = v.dims[v.dimSel-1].name
		}
		if dim == v.splitDim {
			return claimKey
		}
		v.splitDim = dim
		if dim == "" {
			return tea.Batch(v.Refresh(), status("aggregate view"))
		}
		return tea.Batch(v.Refresh(), status("split by "+dim))
	}
	return claimKey
}

// discoverDims collects the dimension fields of sampled series records with
// their distinct-value counts, low cardinality first (those chart best).
func discoverDims(records []map[string]any) []dimInfo {
	values := map[string]map[string]bool{}
	for _, rec := range records {
		for field, val := range rec {
			if field == "metric.key" {
				continue
			}
			s := catalog.FormatValue(val)
			if s == "" {
				continue
			}
			if values[field] == nil {
				values[field] = map[string]bool{}
			}
			values[field][s] = true
		}
	}
	dims := make([]dimInfo, 0, len(values))
	for field, vals := range values {
		dims = append(dims, dimInfo{name: field, values: len(vals)})
	}
	sort.Slice(dims, func(i, j int) bool {
		if dims[i].values != dims[j].values {
			return dims[i].values < dims[j].values
		}
		return dims[i].name < dims[j].name
	})
	return dims
}

// chartData is one chart to render: canned/explore aggregates map one spec
// series each; split mode maps one dimension value each.
type chartData struct {
	title  string
	unit   string
	values []float64
}

// splitChartCap bounds how many split charts render (the rest are counted).
const splitChartCap = 6

// charts assembles the render list for the current mode.
func (v *metricsView) charts() (out []chartData, more int) {
	if v.splitDim != "" {
		for _, rec := range v.records {
			title := catalog.FormatValue(rec[v.splitDim])
			if title == "" {
				continue // by-splits emit one null-key record for series without the dim
			}
			out = append(out, chartData{title: title, unit: v.meta.unit, values: floatSeries(rec["value"])})
		}
		// Rank by average so the busiest series surface first.
		sort.SliceStable(out, func(i, j int) bool {
			return seriesAvg(out[i].values) > seriesAvg(out[j].values)
		})
		if len(out) > splitChartCap {
			more = len(out) - splitChartCap
			out = out[:splitChartCap]
		}
		return out, more
	}
	unavail := map[string]bool{}
	for _, title := range v.unavailable {
		unavail[title] = true
	}
	for _, s := range v.mspec.Series {
		if unavail[s.Title] {
			continue
		}
		unit := s.Unit
		if v.explore() {
			unit = v.meta.unit // ExploreMetricsSpec carries none; the enriched response does
		}
		out = append(out, chartData{title: s.Title, unit: unit, values: floatSeries(v.rec[s.Alias])})
	}
	return out, 0
}

func seriesAvg(values []float64) float64 {
	if len(values) == 0 {
		return math.Inf(-1)
	}
	var sum float64
	for _, f := range values {
		sum += f
	}
	return sum / float64(len(values))
}

// axisW is the y-axis label column: right-aligned max/min values ahead of
// the ┤ ticks.
const axisW = 8

func (v *metricsView) View(width, height int) string {
	var b strings.Builder
	var title string
	if v.explore() {
		title = " " + theme.OverlayTitle.Render(v.key) + "  " + theme.Badge.Render(v.agg())
		if v.splitDim != "" {
			title += "  " + theme.Badge.Render("by "+v.splitDim)
		}
		if v.entity.ID != "" {
			title += theme.Dim.Render("  " + entityName(v.entity))
		}
		title += theme.Dim.Render("  last " + v.tf.Label)
	} else {
		title = " " + theme.OverlayTitle.Render(v.entity.Name) + "  " + theme.Badge.Render(v.entity.Type) +
			theme.Dim.Render("  last "+v.tf.Label)
	}
	b.WriteString(title + "\n")

	if v.dimPick {
		return b.String() + "\n" + v.renderDimPicker(width, height-2)
	}

	head := 2 // title + shared time axis
	if line := v.metaLine(); line != "" {
		b.WriteString(theme.Dim.Render(ansi.Truncate(line, width-1, "…")) + "\n")
		head++
	}

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

	charts, more := v.charts()
	if len(charts) == 0 {
		b.WriteString("\n" + lipgloss.PlaceHorizontal(width, lipgloss.Center,
			theme.Dim.Render("∅ no data in timeframe / not monitored")))
		return b.String()
	}

	n := len(charts)
	chartW := width - axisW - 2
	if chartW < 10 {
		chartW = 10
	}
	// Charts share the body height btop-style: title (+ catalogue line) +
	// per-series header lines + one time-axis line are fixed, the rest
	// divides into chart rows.
	chartRows := (height - head - 2*n) / n
	if chartRows < 2 {
		chartRows = 2
	}
	if chartRows > 9 {
		chartRows = 9
	}

	for i, chart := range charts {
		style := theme.SeriesAt(i)
		values := chart.values
		b.WriteString("\n")
		if len(values) == 0 {
			b.WriteString(" " + style.Bold(true).Render("● "+chart.title) +
				"  " + theme.Dim.Render("no data") + "\n")
			continue
		}
		minV, maxV, avg, last := seriesStats(values)
		// Filled area charts read from a zero baseline (min-based scaling
		// exaggerates noise); percent metrics scale to a true 0–100 gauge.
		plotMin := math.Min(0, minV)
		plotMax := maxV
		if chart.unit == "%" && maxV <= 100 {
			plotMax = 100
		}
		if plotMax <= plotMin {
			plotMax = plotMin + 1
		}

		header := " " + style.Bold(true).Render("● "+chart.title) +
			"  " + theme.OverlayTitle.Render(fmtUnit(last, chart.unit)) +
			theme.Dim.Render(fmt.Sprintf("   min %s · avg %s · max %s",
				fmtUnit(minV, chart.unit), fmtUnit(avg, chart.unit), fmtUnit(maxV, chart.unit)))
		b.WriteString(ansi.Truncate(header, width, "…") + "\n")

		graph := output.NewBrailleGraph(chartW, chartRows)
		graph.PlotFilled(values, plotMin, plotMax)
		rows := strings.Split(graph.Render(), "\n")
		grad := theme.Gradient(theme.SeriesColorAt(i), len(rows))
		for r, rowStr := range rows {
			switch {
			case r == 0:
				b.WriteString(theme.Dim.Render(cell(fmtUnit(plotMax, chart.unit), axisW, true)) + theme.Track.Render("┤"))
			case r == len(rows)-1:
				b.WriteString(theme.Dim.Render(cell(fmtUnit(plotMin, chart.unit), axisW, true)) + theme.Track.Render("┤"))
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
	var notes []string
	if len(v.unavailable) > 0 {
		notes = append(notes, "not reported: "+strings.Join(v.unavailable, " · "))
	}
	if more > 0 {
		notes = append(notes, fmt.Sprintf("+%d more series", more))
	}
	if len(notes) > 0 {
		b.WriteString("\n" + theme.Dim.Render(ansi.Truncate(" "+strings.Join(notes, "   "), width, "…")))
	}
	return b.String()
}

// metaLine is the dim catalogue-identity line under the explorer title:
// "displayName — description" from metric-metadata enrichment. Empty for
// canned charts, for keys the catalogue doesn't know, and when the display
// name merely repeats the key.
func (v *metricsView) metaLine() string {
	if !v.explore() {
		return ""
	}
	var parts []string
	if v.meta.name != "" && v.meta.name != v.key {
		parts = append(parts, v.meta.name)
	}
	if v.meta.desc != "" {
		parts = append(parts, v.meta.desc)
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " — ")
}

// renderDimPicker draws the split-dimension overlay: the metric's dimensions
// with their distinct-value counts, "(aggregate)" on top to unsplit.
func (v *metricsView) renderDimPicker(width, height int) string {
	var b strings.Builder
	b.WriteString(" " + theme.OverlayTitle.Render("split by dimension") + "\n\n")
	labels := make([]string, 0, len(v.dims)+1)
	labels = append(labels, "(aggregate)")
	for _, d := range v.dims {
		plural := "values"
		if d.values == 1 {
			plural = "value"
		}
		labels = append(labels, fmt.Sprintf("%s  (%d %s)", d.name, d.values, plural))
	}
	limit := max(height-4, 3)
	offset := 0
	if v.dimSel >= limit {
		offset = v.dimSel - limit + 1
	}
	for i := offset; i < len(labels) && i < offset+limit; i++ {
		if i == v.dimSel {
			b.WriteString(theme.Selected.Render(" "+labels[i]+" ") + "\n")
		} else {
			b.WriteString("  " + theme.HeaderVal.Render(labels[i]) + "\n")
		}
	}
	if rest := len(labels) - offset - limit; rest > 0 {
		b.WriteString(theme.Dim.Render(fmt.Sprintf(" … %d more", rest)) + "\n")
	}
	return b.String()
}

// fmtUnit renders a metric value in its series unit (catalog.FormatUnit —
// shared with the table spark columns).
func fmtUnit(f float64, unit string) string { return catalog.FormatUnit(f, unit) }

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
