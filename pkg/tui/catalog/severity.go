package catalog

import "strings"

// Shared severity rendering. Severity is a first-class render concept, not a
// logs feature: a CRITICAL Davis event colors exactly like an ERROR log line,
// everywhere it appears (tables, evidence, the record inspector, the sampler).
//
// Two shapes exist in Grail (validated live via the semantic dictionary):
//   - textual levels: loglevel/status on logs and dt.system.events records
//     (ERROR, WARN, SUCCEEDED, …) — ClassLevel maps the words;
//   - the numeric event.severity ordinal on events/problems, ITIL-aligned:
//     1 = most severe … 5 = least severe — rendered as a SEVn badge. (The
//     dictionary semantics invert the old HIGH/CRITICAL word guesses: 4 is
//     LESS severe than 3, so ordinal words were lying and the badge is not.)

// ClassLevel maps a textual severity/level/status word to a render class.
// Deliberately conservative for generic use (the record sampler applies it to
// any severity-ish column): only unambiguous severity words are mapped.
func ClassLevel(val string) string {
	switch strings.ToUpper(val) {
	case "ERROR", "SEVERE", "CRITICAL", "FATAL", "EMERGENCY", "ALERT", "FAILED", "FAILURE":
		return "error"
	case "WARN", "WARNING":
		return "warn"
	case "INFO", "NOTICE", "OK", "SUCCESS", "SUCCEEDED":
		return "ok"
	case "NONE", "DEBUG", "TRACE":
		return "dim"
	}
	return ""
}

// SeverityBadge renders a numeric event.severity value as SEVn ("" when
// absent, raw value when it is not a known ordinal).
func SeverityBadge(severity string) string {
	switch severity {
	case "":
		return ""
	case "1", "2", "3", "4", "5":
		return "SEV" + severity
	}
	return severity
}

// ClassSeverityBadge maps a SEVn badge to a render class: SEV1/2 error,
// SEV3/4 warn, SEV5 dim — monotonic with the ITIL ordinal.
func ClassSeverityBadge(val string) string {
	switch val {
	case "SEV1", "SEV2":
		return "error"
	case "SEV3", "SEV4":
		return "warn"
	case "SEV5":
		return "dim"
	}
	return ""
}

// severityColumn is the shared SEV column for event-shaped tables.
var severityColumn = Column{
	Title: "SEV", Width: 4,
	Value: func(rec map[string]any) string { return SeverityBadge(Str(rec, "event.severity")) },
	Class: ClassSeverityBadge,
	Sort:  func(rec map[string]any) any { return Str(rec, "event.severity") },
}

// SeverityField reports whether a field name carries severity semantics —
// the guard generic renderers (inspector, record sampler) use before wiring a
// severity class onto a column or value.
func SeverityField(field string) bool {
	switch field {
	case "loglevel", "status", "level", "event.severity", "event.status":
		return true
	}
	return false
}

// SeverityFieldClass returns the render class for one record field the
// inspector shows, "" for fields that carry no severity semantics. It keys on
// the field NAME so only genuinely severity-shaped values are colored.
func SeverityFieldClass(field, val string) string {
	switch field {
	case "loglevel", "status", "level":
		return ClassLevel(val)
	case "event.severity":
		if val == "" {
			return ""
		}
		if len(val) == 1 {
			return ClassSeverityBadge("SEV" + val)
		}
		return ClassSeverityBadge(val)
	case "event.status":
		return classProblemStatus(val)
	}
	return ""
}
