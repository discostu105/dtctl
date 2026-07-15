package catalog

import (
	"strings"
	"testing"
)

func TestSyntheticLensSwitchesSource(t *testing.T) {
	all := syntheticSpec.Query(Scope{Timeframe: DefaultTimeframe})
	if !strings.Contains(all, "append [fetch dt.entity.http_check") {
		t.Errorf("all lens must union both monitor tables:\n%s", all)
	}
	http := syntheticSpec.Query(Scope{Timeframe: DefaultTimeframe, Lens: 2})
	if strings.Contains(http, "synthetic_test") || !strings.Contains(http, "dt.entity.http_check") {
		t.Errorf("http lens must fetch only http checks:\n%s", http)
	}
}
func TestSyntheticEnrichUnionsBothFamilies(t *testing.T) {
	dql := syntheticSpec.Enrich.Query(DefaultTimeframe, []string{"SYNTHETIC_TEST-1", "HTTP_CHECK-1"})
	for _, want := range []string{
		"dt.synthetic.browser.availability",
		"dt.synthetic.http.availability",
		`in(dt.entity.synthetic_test, {"SYNTHETIC_TEST-1", "HTTP_CHECK-1"})`,
		"| fieldsAdd key = dt.entity.http_check",
	} {
		if !strings.Contains(dql, want) {
			t.Errorf("synthetic enrich must contain %q:\n%s", want, dql)
		}
	}
}
func TestExecutionsScopedByMonitor(t *testing.T) {
	dql := executionsSpec.Query(Scope{Timeframe: DefaultTimeframe, Arg: "HTTP_CHECK-1", Lens: 3})
	if !strings.Contains(dql, `| filter dt.synthetic.monitor.id == "HTTP_CHECK-1"`) {
		t.Errorf("executions must scope by monitor id:\n%s", dql)
	}
	if !strings.Contains(dql, `result.state != "SUCCESS"`) {
		t.Errorf("failed lens must filter:\n%s", dql)
	}
}
