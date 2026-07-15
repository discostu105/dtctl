package catalog

import (
	"strings"
	"testing"
	"time"
)

func TestSessionsQueryFloorsTimeframe(t *testing.T) {
	short := sessionsSpec.Query(Scope{Timeframe: Timeframes[0]}) // 30m
	if !strings.Contains(short, "from:now() - 24h") {
		t.Errorf("sessions must floor the window at 24h:\n%s", short)
	}
	week := Timeframe{Label: "7d", Dur: 7 * 24 * time.Hour}
	long := sessionsSpec.Query(Scope{Timeframe: week})
	if !strings.Contains(long, "from:now() - 7d") {
		t.Errorf("a wider window must pass through:\n%s", long)
	}
}
func TestSessionsScopeByFrontend(t *testing.T) {
	fe := Entity{ID: "FRONTEND-1", Name: "shop", Type: "FRONTEND"}
	dql := sessionsSpec.Query(Scope{Timeframe: DefaultTimeframe, Entity: &fe})
	// Sessions carry the frontend as an ARRAY — the filter must go through
	// the rendered-array match, not a scalar comparison.
	if !strings.Contains(dql, `matchesPhrase(arrayToString(dt.smartscape.frontend`) {
		t.Errorf("sessions frontend scope must match the array field:\n%s", dql)
	}
	if !sessionsSpec.CanScope(DefaultTimeframe, fe) {
		t.Error("sessions must accept a FRONTEND pin")
	}
	if sessionsSpec.CanScope(DefaultTimeframe, Entity{ID: "HOST-1", Type: "HOST"}) {
		t.Error("sessions must reject a HOST pin (query would not change)")
	}
}
func TestUserEventsSessionTimeline(t *testing.T) {
	dql := userEventsSpec.Query(Scope{Timeframe: DefaultTimeframe, Arg: "SESSION-1"})
	if !strings.Contains(dql, `| filter dt.rum.session.id == "SESSION-1"`) {
		t.Errorf("session arg must filter by dt.rum.session.id:\n%s", dql)
	}
	if !strings.Contains(dql, "sort start_time asc") {
		t.Errorf("a session timeline reads oldest-first:\n%s", dql)
	}
	// The timeline window floors at 24h like the sessions list — a session
	// picked there may predate the global 2h window, and a clipped timeline
	// silently loses its earliest events (found on a live drive).
	if !strings.Contains(dql, "from:now() - 24h") {
		t.Errorf("session timeline must floor the window at 24h:\n%s", dql)
	}
}
func TestUserEventsFrontendScopeAndLens(t *testing.T) {
	fe := Entity{ID: "FRONTEND-1", Type: "FRONTEND"}
	dql := userEventsSpec.Query(Scope{Timeframe: DefaultTimeframe, Entity: &fe, Lens: 1})
	if !strings.Contains(dql, `dt.smartscape.frontend == toSmartscapeId("FRONTEND-1")`) {
		t.Errorf("user.events frontend scope is a scalar smartscape id:\n%s", dql)
	}
	if !strings.Contains(dql, `characteristics.classifier == "error"`) {
		t.Errorf("lens 1 must slice errors:\n%s", dql)
	}
	// There is NO event.type on user.events — the classifier is the
	// discriminator (validated live); guard against regressions to it.
	if strings.Contains(dql, "event.type") {
		t.Errorf("user.events has no event.type field:\n%s", dql)
	}
}
