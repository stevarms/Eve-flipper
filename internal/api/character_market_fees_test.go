package api

import (
	"math"
	"testing"
)

func floatEq(a, b, tol float64) bool { return math.Abs(a-b) < tol }

func TestSuggestedSalesTax(t *testing.T) {
	cases := []struct {
		level int
		want  float64
	}{
		{-1, 8.0},   // clamped to 0
		{0, 8.0},
		{1, 7.12},
		{3, 5.36},
		{5, 3.6},
		{9, 3.6}, // clamped to 5
	}
	for _, c := range cases {
		got := suggestedSalesTax(c.level)
		if !floatEq(got, c.want, 1e-9) {
			t.Errorf("suggestedSalesTax(%d) = %g, want %g", c.level, got, c.want)
		}
	}
}

func TestSuggestedBrokerFee(t *testing.T) {
	// Post-March-2020 rebalance: broker fee reduced by 0.3 pp per Broker
	// Relations level. L5 = 1.5%, not 1.0%.
	cases := []struct {
		level int
		want  float64
	}{
		{-1, 3.0}, // clamped to 0
		{0, 3.0},
		{1, 2.7},
		{3, 2.1},
		{5, 1.5},
		{9, 1.5}, // clamped to 5
	}
	for _, c := range cases {
		got := suggestedBrokerFee(c.level)
		if !floatEq(got, c.want, 1e-9) {
			t.Errorf("suggestedBrokerFee(%d) = %g, want %g", c.level, got, c.want)
		}
	}
}
