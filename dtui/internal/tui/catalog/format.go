package catalog

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/dynatrace-oss/dtctl/pkg/output"
)

// FormatValue renders an arbitrary record value as table-cell text.
func FormatValue(v any) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return val
	case bool:
		return strconv.FormatBool(val)
	case float64:
		if val == float64(int64(val)) {
			return strconv.FormatInt(int64(val), 10)
		}
		return strconv.FormatFloat(val, 'f', 2, 64)
	case []any:
		parts := make([]string, 0, len(val))
		for i, e := range val {
			if i == 3 {
				parts = append(parts, fmt.Sprintf("+%d", len(val)-3))
				break
			}
			parts = append(parts, FormatValue(e))
		}
		return strings.Join(parts, ",")
	case map[string]any:
		b, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprintf("%v", val)
		}
		return string(b)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// Str returns a record field as a string ("" when absent or non-scalar).
func Str(rec map[string]any, key string) string {
	if s, ok := rec[key].(string); ok {
		return s
	}
	return ""
}

// StrFirst returns the first element of an array field as a string, or the
// field itself when it is a plain string.
func StrFirst(rec map[string]any, key string) string {
	switch v := rec[key].(type) {
	case string:
		return v
	case []any:
		if len(v) > 0 {
			return FormatValue(v[0])
		}
	}
	return ""
}

// FormatTime renders an ISO timestamp as a short clock time (with the date
// when it is not from today).
func FormatTime(iso string) string {
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		return iso
	}
	t = t.Local()
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04:05")
	}
	return t.Format("Jan 02 15:04")
}

// Age renders how long ago an ISO timestamp was, k9s-style ("34m", "2d").
func Age(iso string) string {
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		return ""
	}
	return FormatDuration(time.Since(t))
}

// FormatDuration renders a duration compactly ("45s", "34m", "5h", "2d").
func FormatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// FormatBytes renders a byte count in IEC units ("7.6 GiB").
func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// FormatBytesStr renders a byte count held as a string field.
func FormatBytesStr(s string) string {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return s
	}
	return FormatBytes(n)
}

// FormatNs renders a nanosecond count (Grail serializes durations as strings
// of nanoseconds) as a compact human duration ("4.8ms", "1.2s", "2m03s").
func FormatNs(v any) string {
	var ns int64
	switch val := v.(type) {
	case string:
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return val
		}
		ns = n
	case float64:
		ns = int64(val)
	default:
		return ""
	}
	d := time.Duration(ns)
	switch {
	case d < time.Millisecond:
		return fmt.Sprintf("%dµs", d.Microseconds())
	case d < time.Second:
		return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
	case d < time.Minute:
		return fmt.Sprintf("%.2fs", d.Seconds())
	default:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
}

// FloatValue coerces a record value to a float64. Grail serializes longs as
// JSON strings and doubles as numbers — the same logical field arrives as
// either depending on its type (validated live).
func FloatValue(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case string:
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// nsToMs converts a nanosecond field (string or number) to milliseconds.
func nsToMs(v any) (float64, bool) {
	f, ok := FloatValue(v)
	if !ok {
		return 0, false
	}
	return f / 1e6, true
}

// parseMsSuffix parses a rendered "1234ms" cell back to its number (column
// Class funcs receive the formatted text).
func parseMsSuffix(val string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSuffix(val, "ms"), 64)
}

// FloatSeries extracts the numeric points of a timeseries array, skipping
// nulls (gaps compress, which is fine for a sparkline).
func FloatSeries(v any) []float64 {
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

// SeriesLast returns the most recent non-null point of a series field, or
// NaN. Sort keys built on it order unenriched rows last.
func SeriesLast(rec map[string]any, key string) float64 {
	vals := FloatSeries(rec[key])
	if len(vals) == 0 {
		return math.NaN()
	}
	return vals[len(vals)-1]
}

// SparkColumn builds the standard enrichment sparkline column: a braille
// mini-graph of the batched series with the latest value right-aligned
// beside it, sortable by that value. Both belong in the cell — the mini
// graph is normalized to its own range, so the shape alone cannot tell a
// flat 3% from a flat 90%. unit renders the value via FormatUnitShort
// ("" = bare count).
func SparkColumn(title, alias string, width int, unit string) Column {
	key := EnrichKey(alias)
	const graphW = 8
	return Column{
		Title: title,
		Width: width,
		Class: func(string) string { return "spark" },
		Value: func(rec map[string]any) string {
			series := FloatSeries(rec[key])
			if len(series) == 0 {
				return ""
			}
			value := FormatUnitShort(series[len(series)-1], unit)
			return fmt.Sprintf("%s %*s", output.MiniGraph(series, graphW), width-graphW-1, value)
		},
		Sort: func(rec map[string]any) any {
			if last := SeriesLast(rec, key); !math.IsNaN(last) {
				return last
			}
			return nil
		},
	}
}

// FormatUnit renders a metric value in its series unit ("B" gets IEC bytes,
// "B/s" a rate, durations ("µs", "ms", "s") scale adaptively, "%" attaches
// its suffix, anything else appends the unit label).
func FormatUnit(f float64, unit string) string {
	switch unit {
	case "%":
		return formatMetric(f) + "%"
	case "B":
		if f >= 0 {
			return FormatBytes(int64(f))
		}
		return formatMetric(f) + " B"
	case "B/s":
		if f >= 0 {
			return FormatBytes(int64(f)) + "/s"
		}
		return formatMetric(f) + " B/s"
	case "µs":
		return fmtSeconds(f / 1e6)
	case "ms":
		return fmtSeconds(f / 1e3)
	case "s":
		return fmtSeconds(f)
	case "":
		return formatMetric(f)
	default:
		return formatMetric(f) + " " + unit
	}
}

// FormatUnitShort is FormatUnit for dense table cells: percent keeps one
// decimal ("26.8%"), bytes drop the space ("69.9MiB"), millicores use the
// k8s suffix ("500m"), everything else matches FormatUnit.
func FormatUnitShort(f float64, unit string) string {
	switch unit {
	case "%":
		if f == math.Trunc(f) {
			return fmt.Sprintf("%.0f%%", f)
		}
		return fmt.Sprintf("%.1f%%", f)
	case "B":
		if f >= 0 {
			return strings.ReplaceAll(FormatBytes(int64(f)), " ", "")
		}
	case "B/s":
		if f >= 0 {
			return strings.ReplaceAll(FormatBytes(int64(f)), " ", "") + "/s"
		}
	case "mCores":
		return formatMetric(f) + "m"
	}
	return FormatUnit(f, unit)
}

// fmtSeconds renders a duration given in seconds at a readable magnitude
// (Grail serves response times in µs, OTel histograms in s — both land here).
func fmtSeconds(f float64) string {
	abs := math.Abs(f)
	switch {
	case abs >= 1 || abs == 0:
		return formatMetric(f) + " s"
	case abs >= 1e-3:
		return formatMetric(f*1e3) + " ms"
	default:
		return formatMetric(f*1e6) + " µs"
	}
}

// formatMetric renders a bare metric value at a readable magnitude.
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
