package catalog

import (
	"math"
	"testing"

	"github.com/dynatrace-oss/dtctl/pkg/output"
)

// Spark must reproduce pkg/output's MiniGraph glyph-for-glyph — it exists
// only so the catalog's production code stays stdlib-only.
func TestSparkMatchesOutputMiniGraph(t *testing.T) {
	cases := [][]float64{
		nil,
		{},
		{5},
		{0, 0, 0, 0},
		{1, 2, 3, 4, 5, 6, 7, 8},
		{8, 1, 6, 2, 9, 0, 4, 4, 4, 7},
		{math.NaN(), 2, math.NaN(), 8, 3},
		{math.NaN(), math.NaN()},
		{-5, 10, -3, 0.5},
		{0.001, 0.002, 0.0015, 0.004, 0.001, 0.003},
	}
	for _, vals := range cases {
		for _, w := range []int{1, 4, 8, 16} {
			if got, want := Spark(vals, w), output.MiniGraph(vals, w); got != want {
				t.Errorf("Spark(%v, %d) = %q, want %q", vals, w, got, want)
			}
		}
	}
}
