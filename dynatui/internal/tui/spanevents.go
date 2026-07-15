package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
	"github.com/dynatrace-oss/dynatui/internal/tui/theme"
)

// Span-event rendering: exceptions and the span's other recorded events
// (bizevents, feature flags, messaging markers) as first-class inspector rows
// instead of one collapsed JSON blob in the span group. Exceptions are the
// point: they mostly ride on spans whose status is ok/null (validated live —
// a caught exception or a 404 recorded as an event never fails the span), so
// the blob was the only place they showed.

// markSpanEventsConsumed flags the record key the span-events section renders
// itself; the namespace-group renderer skips it.
func markSpanEventsConsumed(_ map[string]any, rendered map[string]bool) {
	rendered["span.events"] = true
}

// addSpanEvents renders span.events as its own section and reports whether
// one was drawn. Events keep their recorded order (a span's timeline).
// Exceptions render loud — type and message always visible, the stack trace
// collapsed behind its top frame — and other events as compact rows that
// expand into per-field JSON rows.
func (v *inspectorView) addSpanEvents(needle string) bool {
	events := catalog.SpanEvents(v.rec)
	if len(events) == 0 {
		return false
	}
	excCount := 0
	for _, ev := range events {
		if ev.Name == "exception" {
			excCount++
		}
	}
	title := fmt.Sprintf("span events (%d)", len(events))
	switch {
	case excCount == len(events) && excCount == 1:
		title = "exception"
	case excCount == len(events):
		title = fmt.Sprintf("exceptions (%d)", excCount)
	}

	v.addLine(theme.Section(title))
	excIdx := 0
	for i, ev := range events {
		if ex, ok := ev.Exception(); ok {
			// Labels double as expand-state identity — index them when the
			// span carries a cause chain so rows toggle independently.
			suffix := ""
			if excCount > 1 {
				suffix = fmt.Sprintf("[%d]", excIdx)
			}
			excIdx++
			v.addExceptionRows("exception"+suffix, "stack trace"+suffix, ex, needle)
			continue
		}
		label := fmt.Sprintf("ev[%d] · %s", i, ansi.Truncate(ev.Name, 20, "…"))
		v.addEventRow(label, ev, needle)
	}
	v.addLine("")
	return true
}

// addExceptionRows lays out one exception event: the type+message row always
// expanded (it is what the reader came for), the stack trace behind its top
// frame — one keypress away instead of forty lines up front.
func (v *inspectorView) addExceptionRows(label, stackLabel string, ex catalog.SpanException, needle string) {
	head := ex.Type
	if head == "" {
		head = "exception"
	}
	text, raw := theme.Class("error", head), head
	if ex.Message != "" {
		text += theme.Dim.Render(" — ") + ex.Message
		raw += " — " + ex.Message
	}
	if ex.Location != "" {
		text += theme.Dim.Render("  @ " + ex.Location)
		raw += " @ " + ex.Location
	}
	if fieldMatches(needle, label, raw) {
		v.addRow("span.events", label, v.labelStyle(needle, label, theme.Error), valueView{
			lines:   wrapLines(text, v.vp.Width-6),
			compact: compactText(raw),
			raw:     raw,
			block:   true,
		})
	}
	if ex.StackTrace == "" || !fieldMatches(needle, stackLabel, ex.StackTrace) {
		return
	}
	v.addRow("span.events", stackLabel, v.labelStyle(needle, stackLabel, theme.Dim),
		stackValue(ex.StackTrace, v.vp.Width-6))
}

// addEventRow lays out one non-exception event: a compact "k=v · k=v" preview
// that expands into per-field JSON rows (enter), the discriminator name in
// the label.
func (v *inspectorView) addEventRow(label string, ev catalog.SpanEvent, needle string) {
	fields := map[string]any{}
	for k, val := range ev.Fields {
		if k == "span_event.name" || k == "name" {
			continue
		}
		fields[k] = val
	}
	raw := rawJSON(fields)
	if !fieldMatches(needle, label, raw) && !fieldMatches(needle, label, ev.Name) {
		return
	}
	if len(fields) == 0 {
		v.addRow("span.events", label, v.labelStyle(needle, label, theme.Label), valueView{
			lines: []string{theme.Dim.Render("(no attributes)")}, raw: ev.Name})
		return
	}
	val := renderValue("span.events", fields, v.vp.Width-6)
	val.block = false
	val.compact = compactEventFields(fields)
	v.addRow("span.events", label, v.labelStyle(needle, label, theme.Label), val)
}

// compactEventFields squashes an event's attributes into one "k=v · k=v"
// line for the collapsed preview.
func compactEventFields(fields map[string]any) string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+catalog.FormatValue(fields[k]))
	}
	return strings.Join(parts, " · ")
}
