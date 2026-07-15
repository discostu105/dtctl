package catalog

import (
	"strings"
	"testing"
)

func TestResourcesQueryUsesArg(t *testing.T) {
	spec := Lookup("resources")
	q := spec.Query(Scope{Timeframe: Timeframe{Label: "2h"}, Arg: "AWS_EC2_INSTANCE"})
	if !strings.Contains(q, `smartscapeNodes "AWS_EC2_INSTANCE"`) {
		t.Errorf("resources query ignores Arg:\n%s", q)
	}
	if !strings.Contains(q, "`tags:aws`[`Name`]") {
		t.Errorf("resources query missing Name-tag display fallback:\n%s", q)
	}
	if got := spec.Query(Scope{Timeframe: Timeframe{Label: "2h"}}); !strings.Contains(got, `smartscapeNodes "*"`) {
		t.Errorf("resources query without Arg should browse all types:\n%s", got)
	}
}
func TestPrettyType(t *testing.T) {
	if got := prettyType("AWS_EC2_INSTANCE"); got != "ec2 instance" {
		t.Errorf("prettyType = %q", got)
	}
	if got := prettyType("K8S_POD"); got != "pod" {
		t.Errorf("prettyType = %q", got)
	}
}
