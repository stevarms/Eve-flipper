package engine

import (
	"math"
	"testing"
)

func TestNextSellUndercut(t *testing.T) {
	cases := []struct {
		name string
		in   float64
		want float64
	}{
		// Millions — step 10k
		{"1234M off-grid", 12_345_678, 12_340_000},
		{"1234M on-grid steps down one place", 12_340_000, 12_330_000},
		// Billions — step 1M
		{"2.456B off-grid", 2_456_789_000, 2_456_000_000},
		{"2.456B on-grid", 2_456_000_000, 2_455_000_000},
		// Hundreds of thousands — step 100
		{"456k off-grid", 456_789, 456_700},
		{"456k on-grid", 456_700, 456_600},
		// Tens/ones — step 0.01 (small-price case matches legacy behavior)
		{"90 ISK small-price", 90, 89.99},
		{"12.34 ISK small-price", 12.34, 12.33},
		// Trillions — step 1B
		{"1.234T off-grid", 1_234_500_000_000, 1_234_000_000_000},
		// Guards
		{"zero", 0, 0},
		{"negative", -100, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NextSellUndercut(c.in)
			if math.Abs(got-c.want) > 1e-6 {
				t.Errorf("NextSellUndercut(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}

	if got := NextSellUndercut(math.Inf(1)); got != 0 {
		t.Errorf("NextSellUndercut(+Inf) = %v, want 0", got)
	}
	if got := NextSellUndercut(math.NaN()); got != 0 {
		t.Errorf("NextSellUndercut(NaN) = %v, want 0", got)
	}
}

func TestNextBuyOverbid(t *testing.T) {
	cases := []struct {
		name string
		in   float64
		want float64
	}{
		{"1234M off-grid", 12_345_678, 12_350_000},
		{"1234M on-grid steps up one place", 12_340_000, 12_350_000},
		{"2.456B off-grid", 2_456_789_000, 2_457_000_000},
		{"2.456B on-grid", 2_456_000_000, 2_457_000_000},
		{"456k off-grid", 456_789, 456_800},
		{"456k on-grid", 456_700, 456_800},
		{"90 ISK small-price", 90, 90.01},
		{"12.34 ISK small-price", 12.34, 12.35},
		{"zero", 0, 0},
		{"negative", -100, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NextBuyOverbid(c.in)
			if math.Abs(got-c.want) > 1e-6 {
				t.Errorf("NextBuyOverbid(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestSellUndercutBuyOverbidSymmetry — for any positive x that's not on the
// exact power-of-ten boundary between magnitudes, the sell-undercut must be
// strictly less than x and the buy-overbid must be strictly greater. This
// catches drift bugs where snapping accidentally lands on x itself.
func TestSellUndercutBuyOverbidSymmetry(t *testing.T) {
	inputs := []float64{
		12.34,
		89.99,
		456_789,
		1_234_567,
		12_345_678,
		987_654_321,
		2_456_789_000,
	}
	for _, x := range inputs {
		sell := NextSellUndercut(x)
		buy := NextBuyOverbid(x)
		if !(sell < x) {
			t.Errorf("NextSellUndercut(%v)=%v not strictly less than input", x, sell)
		}
		if !(buy > x) {
			t.Errorf("NextBuyOverbid(%v)=%v not strictly greater than input", x, buy)
		}
	}
}

func TestSnapToGrid(t *testing.T) {
	// Scrubs IEEE-754 noise from 89.99 - 0.01 which naïvely produces
	// 89.97999999999999...
	if got := SnapToGrid(89.99-0.01, 0.01); math.Abs(got-89.98) > 1e-9 {
		t.Errorf("SnapToGrid(89.99-0.01, 0.01) = %v, want 89.98", got)
	}
	// Zero place value → passthrough
	if got := SnapToGrid(42, 0); got != 42 {
		t.Errorf("SnapToGrid(42, 0) = %v, want 42 passthrough", got)
	}
}
