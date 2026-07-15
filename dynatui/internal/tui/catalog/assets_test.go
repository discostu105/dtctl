package catalog

import (
	"testing"
)

func TestAPIViewsHaveNoQueryButEcho(t *testing.T) {
	for _, spec := range []*Spec{slosSpec, detectorsSpec} {
		if spec.Query != nil {
			t.Errorf("%s: API-only views carry no DQL", spec.Name)
		}
		if spec.API == "" || spec.Echo == nil {
			t.Errorf("%s: API views need a source name and a CLI echo", spec.Name)
		}
		if spec.CanScope(DefaultTimeframe, Entity{ID: "HOST-1", Type: "HOST"}) {
			t.Errorf("%s: a pin can never scope an API view without a query", spec.Name)
		}
	}
}
