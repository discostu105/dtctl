package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestIsStackKey: every stack spelling matches (OneAgent code.call_stack,
// OTel code.stacktrace, both exception spellings, RUM/log error.stack_trace);
// vulnerability.stack is a tech-stack enum and must not.
func TestIsStackKey(t *testing.T) {
	for key, want := range map[string]bool{
		"code.call_stack":       true,
		"code.stacktrace":       true,
		"exception.stack_trace": true,
		"exception.stacktrace":  true,
		"error.stack_trace":     true,
		"vulnerability.stack":   false,
		"span.name":             false,
	} {
		if got := isStackKey(key); got != want {
			t.Errorf("isStackKey(%q) = %v, want %v", key, got, want)
		}
	}
}

// TestStackValueCollapsesBehindTopFrame: a multi-line callstack renders
// collapsed — the top frame plus the frame count as the preview — instead of
// the auto-expanded block a plain multi-line string gets.
func TestStackValueCollapsesBehindTopFrame(t *testing.T) {
	trace := "a.b.C.one (C.java:1)\na.b.C.two (C.java:2)\na.b.C.three (C.java:3)"
	val := renderValue("code.call_stack", trace, 120)
	if val.block {
		t.Error("a callstack must start collapsed")
	}
	compact := ansi.Strip(val.compact)
	if !strings.HasPrefix(compact, "a.b.C.one (C.java:1)") || !strings.Contains(compact, "⋯ 3 frames") {
		t.Errorf("compact = %q, want top frame + frame count", compact)
	}
	if val.raw != trace {
		t.Error("raw must stay the whole trace for yank")
	}
	// A plain multi-line string of the same shape still auto-expands.
	if plain := renderValue("some.field", trace, 120); !plain.block {
		t.Error("non-stack multi-line strings keep their expanded default")
	}
}

// TestInspectorScrollsThroughTallBlocks: a selected row taller than the
// viewport scrolls line-by-line under j/k (and page-wise under ctrl+d)
// before the cursor moves on — its middle must be readable, not leapt over.
func TestInspectorScrollsThroughTallBlocks(t *testing.T) {
	long := strings.TrimSpace(strings.Repeat("frame line\n", 100))
	v := newInspectorView(nil, "span", map[string]any{
		"content":  long, // blockString → expanded by default
		"zz.after": "next field",
	})
	v.resize(80, 20)
	idx := -1
	for i, r := range v.rows {
		if r.key == "content" {
			idx = i
			break
		}
	}
	if idx < 0 || v.rows[idx].span <= v.vp.Height {
		t.Fatalf("need an expanded content row taller than the viewport (idx=%d)", idx)
	}
	v.cursor = idx
	v.refreshVP()
	v.ensureVisible()

	off := v.vp.YOffset
	v.moveCursor(1)
	if v.cursor != idx || v.vp.YOffset != off+1 {
		t.Fatalf("j on a tall row must scroll one line, not leave: cursor %d→%d, offset %d→%d",
			idx, v.cursor, off, v.vp.YOffset)
	}
	v.pageCursor(10)
	if v.cursor != idx || v.vp.YOffset != off+11 {
		t.Fatalf("ctrl+d on a tall row must page within it: cursor %d, offset %d", v.cursor, v.vp.YOffset)
	}
	v.moveCursor(-1)
	if v.cursor != idx || v.vp.YOffset != off+10 {
		t.Fatalf("k must scroll back up within the block: cursor %d, offset %d", v.cursor, v.vp.YOffset)
	}

	// Scrolling to the block's bottom releases the cursor on the next j.
	for range 300 {
		if v.cursor != idx {
			break
		}
		v.moveCursor(1)
	}
	if v.cursor != idx+1 {
		t.Fatalf("cursor must eventually move past the block, stuck at %d", v.cursor)
	}
}
