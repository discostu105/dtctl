package tui

import "testing"

func TestScrollerMoveClampsAndScrolls(t *testing.T) {
	var s scroller
	s.move(1, 0, 5) // empty list: cursor pinned to 0
	if s.cursor != 0 || s.offset != 0 {
		t.Fatalf("empty list moved: %+v", s)
	}
	s.move(-3, 10, 5) // below start
	if s.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", s.cursor)
	}
	s.move(99, 10, 5) // past end: clamp to last row, window follows
	if s.cursor != 9 || s.offset != 5 {
		t.Fatalf("end clamp: %+v, want cursor 9 offset 5", s)
	}
	s.move(-7, 10, 5) // back up: window follows the cursor upward
	if s.cursor != 2 || s.offset != 2 {
		t.Fatalf("up scroll: %+v, want cursor 2 offset 2", s)
	}
}

func TestScrollerClampAfterRowsShrink(t *testing.T) {
	s := scroller{cursor: 8, offset: 4}
	// The row set narrowed under a parked cursor (filter typed): a zero move
	// re-clamps the cursor, and the window follows it down.
	s.move(0, 3, 5)
	if s.cursor != 2 || s.offset != 2 {
		t.Fatalf("shrink clamp: %+v, want cursor 2 offset 2", s)
	}
}

func TestScrollerHandleKey(t *testing.T) {
	var s scroller
	cases := []struct {
		key    string
		cursor int
	}{
		{"j", 1}, {"down", 2}, {"k", 1}, {"up", 0},
		{" ", 5}, {"pgdown", 9}, {"ctrl+f", 9}, // page = 5 over 10 rows
		{"pgup", 4}, {"ctrl+b", 0},
		{"G", 9}, {"g", 0}, {"end", 9}, {"home", 0},
	}
	for _, c := range cases {
		if !s.handleKey(c.key, 10, 5) {
			t.Fatalf("handleKey(%q) not claimed", c.key)
		}
		if s.cursor != c.cursor {
			t.Fatalf("after %q cursor = %d, want %d", c.key, s.cursor, c.cursor)
		}
	}
	if s.handleKey("enter", 10, 5) || s.handleKey("x", 10, 5) {
		t.Fatal("non-movement keys must not be claimed")
	}
}
