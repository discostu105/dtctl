package catalog

import (
	"testing"
	"time"
)

func TestParseTimeframe(t *testing.T) {
	cases := []struct {
		label string
		ok    bool
		dur   time.Duration
	}{
		{"30m", true, 30 * time.Minute}, // picker preset
		{"2h", true, 2 * time.Hour},     // picker preset
		{"45m", true, 45 * time.Minute}, // custom window
		{"3d", true, 72 * time.Hour},    // custom window
		{"", false, 0},
		{"yesterday", false, 0},
		{"2w", false, 0},
		{"0h", false, 0},
		{"-2h", false, 0},
	}
	for _, tc := range cases {
		tf, ok := ParseTimeframe(tc.label)
		if ok != tc.ok {
			t.Errorf("ParseTimeframe(%q) ok = %v, want %v", tc.label, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if tf.Label != tc.label || tf.Dur != tc.dur {
			t.Errorf("ParseTimeframe(%q) = {%s %s}, want {%s %s}", tc.label, tf.Label, tf.Dur, tc.label, tc.dur)
		}
	}
	// Preset labels must return the preset itself so the picker highlight
	// stays in sync.
	if tf, _ := ParseTimeframe("24h"); tf != Timeframes[2] {
		t.Errorf("ParseTimeframe(24h) = %+v, want the picker preset", tf)
	}
}
