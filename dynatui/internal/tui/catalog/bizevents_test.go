package catalog

import (
	"strings"
	"testing"
)

func TestBizeventsFloorsTimeframe(t *testing.T) {
	dql := bizeventsSpec.Query(Scope{Timeframe: DefaultTimeframe}) // 2h
	if !strings.Contains(dql, "from:now() - 24h") {
		t.Errorf("bizevents must floor the window at 24h:\n%s", dql)
	}
}
