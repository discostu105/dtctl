package tui

// scroller is the shared cursor-plus-viewport state of the record-list views
// (tables, waterfall, timeline, relations, navigator). The embedding view
// supplies the row count and its visible-row budget; the scroller clamps the
// cursor into [0, rows) and scrolls the offset window to keep it on screen.
type scroller struct {
	cursor, offset int
}

// move shifts the cursor by delta over n rows, scrolling to keep it inside
// the vis-row window.
func (s *scroller) move(delta, n, vis int) {
	s.cursor += delta
	if s.cursor >= n {
		s.cursor = n - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
	s.clamp(vis)
}

// clamp scrolls the offset so the cursor stays inside the vis-row window —
// also the fix-up after the row set or the window size changed under a
// parked cursor.
func (s *scroller) clamp(vis int) {
	if s.cursor < s.offset {
		s.offset = s.cursor
	}
	if vis < 1 {
		vis = 1
	}
	if s.cursor >= s.offset+vis {
		s.offset = s.cursor - vis + 1
	}
	if s.offset < 0 {
		s.offset = 0
	}
}

// top rewinds to the first row.
func (s *scroller) top() { s.cursor, s.offset = 0, 0 }

// handleKey applies the shared movement keys — j/k and arrows, pgup/pgdn
// (with ctrl+b/f and space), g/G and home/end — over n rows with a vis-row
// page; reports whether the key was one of them.
func (s *scroller) handleKey(key string, n, vis int) bool {
	switch key {
	case "up", "k":
		s.move(-1, n, vis)
	case "down", "j":
		s.move(1, n, vis)
	case "pgup", "ctrl+b":
		s.move(-vis, n, vis)
	case "pgdown", "ctrl+f", " ":
		s.move(vis, n, vis)
	case "home", "g":
		s.top()
	case "end", "G":
		s.move(n, n, vis)
	default:
		return false
	}
	return true
}
