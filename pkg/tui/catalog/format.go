package catalog

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
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
