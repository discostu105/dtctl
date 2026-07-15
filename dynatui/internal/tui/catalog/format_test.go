package catalog

import (
	"strings"
	"testing"
)

func TestNormalizeUnit(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Percent", "%"},
		{"Byte", "B"},
		{"BytePerSecond", "B/s"},
		{"MicroSecond", "µs"},
		{"MilliSecond", "ms"},
		{"NanoSecond", "ns"},
		{"MilliCores", "mCores"},
		{"PerSecond", "/s"},
		{"Count", ""},
		{"Ratio", ""},
		{"Unspecified", ""},
		{"NotApplicable", ""},
		{"", ""},
		// Case-insensitive: some API surfaces lowercase the catalogue names.
		{"percent", "%"},
		{"bytePerSecond", "B/s"},
		// Unknown names pass through as literal suffixes.
		{"FunkyUnit", "FunkyUnit"},
	}
	for _, c := range cases {
		if got := NormalizeUnit(c.in); got != c.want {
			t.Errorf("NormalizeUnit(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatUnitCatalogueTokens(t *testing.T) {
	cases := []struct {
		f    float64
		unit string
		want string
	}{
		{250, "ns", "250 ns"},                 // sub-µs durations stay in ns
		{2500000, "ns", "2.50 ms"},            // and scale up adaptively
		{12.5, "/s", "12.50/s"},               // rate suffixes attach without a space
		{3, "min", "3 min"},                   // unmapped-by-FormatUnit tokens keep the label
		{42.1234, "Percent", "42.12 Percent"}, // raw catalogue names must be normalized first
	}
	for _, c := range cases {
		if got := FormatUnit(c.f, c.unit); got != c.want {
			t.Errorf("FormatUnit(%v, %q) = %q, want %q", c.f, c.unit, got, c.want)
		}
	}
}

func TestSparkColumnSortsByLatestValue(t *testing.T) {
	col := SparkColumn("CPU", "cpu", 14, "%")
	rec := map[string]any{EnrichKey("cpu"): []any{1.0, nil, 3.5}}
	if got := col.Sort(rec); got != 3.5 {
		t.Errorf("Sort = %v, want 3.5 (latest non-null)", got)
	}
	if col.Sort(map[string]any{}) != nil {
		t.Error("unenriched rows must sort as empty")
	}
	// The cell carries the sparkline AND the latest value — a normalized
	// mini-graph alone can't distinguish a flat 3% from a flat 90%.
	if cell := col.Value(rec); !strings.HasSuffix(cell, "3.5%") {
		t.Errorf("sparkline cell should end with the latest value, got %q", cell)
	}
	if col.Value(map[string]any{}) != "" {
		t.Error("unenriched rows must render an empty cell")
	}
}

func TestFormatUnitShort(t *testing.T) {
	cases := []struct {
		f    float64
		unit string
		want string
	}{
		{78.2, "%", "78.2%"},
		{3.5, "mCores", "3.50m"},
		{73315123, "B", "69.9MiB"},
		{44134, "B/s", "43.1KiB/s"},
		{1234, "", "1234"},
	}
	for _, c := range cases {
		if got := FormatUnitShort(c.f, c.unit); got != c.want {
			t.Errorf("FormatUnitShort(%v, %q) = %q, want %q", c.f, c.unit, got, c.want)
		}
	}
	// The long form keeps the space (chart headers, vitals rows).
	if got := FormatUnit(73315123, "B"); got != "69.9 MiB" {
		t.Errorf("FormatUnit bytes = %q", got)
	}
}

func TestFormatNs(t *testing.T) {
	cases := map[string]string{
		"4845165":      "4.8ms",
		"163000":       "163µs",
		"20253838":     "20.3ms",
		"5800000000":   "5.80s",
		"126000000000": "2m06s",
	}
	for in, want := range cases {
		if got := FormatNs(in); got != want {
			t.Errorf("FormatNs(%s) = %q, want %q", in, got, want)
		}
	}
}
