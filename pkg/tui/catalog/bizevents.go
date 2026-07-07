package catalog

import (
	"fmt"
	"strings"
	"time"
)

// Business events. Validated live: payload fields vary per producer (no
// uniform content field — one producer uses flat slo_name, another dotted
// slo.name), event.category can be absent entirely, and the default 2h window
// hides all but the chattiest producer — the window floors at 24h. The
// event.type / event.provider facet pair is the primary navigation.

var bizeventsSpec = &Spec{
	Name:    "bizevents",
	Aliases: []string{"biz", "be", "bizevent"},
	Kind:    KindSignal,
	Desc:    "Business events — facet by type and provider",
	Query: func(s Scope) string {
		var b strings.Builder
		fmt.Fprintf(&b, "fetch bizevents, from:%s", floorTimeframe(s.Timeframe, 24*time.Hour, "24h"))
		b.WriteString("\n| sort timestamp desc\n| limit 300")
		return b.String()
	},
	Columns: []Column{
		{Title: "TIME", Width: 12, Value: func(rec map[string]any) string { return FormatTime(Str(rec, "timestamp")) },
			Sort: func(rec map[string]any) any { return Str(rec, "timestamp") }},
		{Title: "TYPE", Field: "event.type", Width: 26},
		{Title: "PROVIDER", Field: "event.provider", Width: 26},
		{Title: "CONTENT", Value: bizeventContent},
	},
	Drills: map[string]string{},
}

// bizeventContent picks the most telling payload field — producers name their
// payloads inconsistently (validated live), so the chain is best-effort and
// the inspector shows the full record.
func bizeventContent(rec map[string]any) string {
	for _, key := range []string{"event.name", "slo_name", "slo.name", "event.category", "event.id"} {
		if v := Str(rec, key); v != "" {
			return v
		}
	}
	return ""
}
