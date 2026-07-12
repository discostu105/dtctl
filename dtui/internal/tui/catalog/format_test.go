package catalog

import "testing"

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
