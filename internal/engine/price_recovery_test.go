package engine

import (
	"fmt"
	"math"
	"testing"
	"time"

	"eve-flipper/internal/esi"
)

// recoveryTestHistory builds a daily series ending on a fixed date, so the
// gates are exercised against a known shape rather than whatever the clock
// says. priceAt is given the day index, oldest first.
func recoveryTestHistory(days int, priceAt func(day int) float64) []esi.HistoryEntry {
	end := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	out := make([]esi.HistoryEntry, 0, days)
	for i := 0; i < days; i++ {
		p := priceAt(i)
		out = append(out, esi.HistoryEntry{
			Date:    end.AddDate(0, 0, -(days - 1 - i)).Format("2006-01-02"),
			Average: p,
			Highest: p * 1.02,
			Lowest:  p * 0.98,
			Volume:  500,
		})
	}
	return out
}

// recoverySine is a clean oscillation about a flat level: the textbook dip
// that comes back, and the only shape the outlook is allowed to endorse.
func recoverySine(day int, amplitude, period float64) float64 {
	return 100 * math.Exp(amplitude*math.Sin(2*math.Pi*float64(day)/period))
}

func TestCalcRecoveryOutlookReadsAnOscillationAsADip(t *testing.T) {
	// 173 days of a 30-day cycle lands the final sample near a trough,
	// which is the state a trader would actually be looking at.
	h := recoveryTestHistory(173, func(day int) float64 {
		return recoverySine(day, 0.10, 30)
	})

	got := CalcRecoveryOutlook(h, 0)

	if got.Basis != RecoveryBasisHistory {
		t.Fatalf("basis = %q (%s), want %q", got.Basis, got.Reason, RecoveryBasisHistory)
	}
	if got.ZScore > recoveryDipZ {
		t.Fatalf("z = %.2f, want at or below %.2f", got.ZScore, recoveryDipZ)
	}
	if got.Episodes < recoveryMinEpisodes {
		t.Fatalf("episodes = %d, want at least %d", got.Episodes, recoveryMinEpisodes)
	}
	// Trough to trend is roughly three quarters of a cycle from the point
	// the price first crosses one sigma down.
	if got.MedianDays < 5 || got.MedianDays > 20 {
		t.Fatalf("median days = %.1f, want a plausible fraction of the 30d cycle", got.MedianDays)
	}
	last := h[len(h)-1].Average
	if got.TargetPrice <= last {
		t.Fatalf("target %.2f is not above today's %.2f", got.TargetPrice, last)
	}
	if got.TargetPrice > 115 {
		t.Fatalf("target %.2f promises more than this item has traded at", got.TargetPrice)
	}
}

func TestCalcRecoveryOutlookRefusesASteadyDecline(t *testing.T) {
	// The case that matters most: every day is below the last week, so a
	// naive "price is low, wait for it" reading is available and wrong.
	h := recoveryTestHistory(180, func(day int) float64 {
		return 100 * math.Exp(-0.005*float64(day)+0.01*math.Sin(2*math.Pi*float64(day)/17))
	})

	got := CalcRecoveryOutlook(h, 0)

	if got.Basis != RecoveryBasisNone {
		t.Fatalf("basis = %q, want %q on a declining series", got.Basis, RecoveryBasisNone)
	}
	if got.Reason != "trending down, not dipping" {
		t.Fatalf("reason = %q, want the decline reason", got.Reason)
	}
	if got.TrendPctDay >= 0 {
		t.Fatalf("trend = %.3f%%/day, want negative", got.TrendPctDay)
	}
	if got.TargetPrice != 0 {
		t.Fatalf("target = %v, want no target when the outlook is refused", got.TargetPrice)
	}
}

func TestCalcRecoveryOutlookRefusesThinHistory(t *testing.T) {
	cases := []struct {
		name string
		hist []esi.HistoryEntry
	}{
		{"nil", nil},
		{"too few days", recoveryTestHistory(40, func(day int) float64 {
			return recoverySine(day, 0.10, 30)
		})},
		{"long but barely traded", func() []esi.HistoryEntry {
			h := recoveryTestHistory(170, func(day int) float64 {
				return recoverySine(day, 0.10, 30)
			})
			// Plenty of quoted days, almost none with a trade behind them:
			// the price series is then an artefact, not a market.
			for i := range h {
				if i%4 != 0 {
					h[i].Volume = 0
				}
			}
			return h
		}()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CalcRecoveryOutlook(tc.hist, 0)
			if got.Basis != RecoveryBasisNone {
				t.Fatalf("basis = %q, want %q", got.Basis, RecoveryBasisNone)
			}
			if got.Reason != "not enough traded history" {
				t.Fatalf("reason = %q, want the evidence-gate reason", got.Reason)
			}
		})
	}
}

func TestCalcRecoveryOutlookRefusesAPriceThatIsNotLow(t *testing.T) {
	// Same oscillation, sampled where it crosses its own mean. Nothing is
	// wrong with the item; there is simply no dip to wait out, and holding
	// would just be waiting at a fair price.
	h := recoveryTestHistory(181, func(day int) float64 {
		return recoverySine(day, 0.10, 30)
	})

	got := CalcRecoveryOutlook(h, 0)

	if got.Basis != RecoveryBasisNone {
		t.Fatalf("basis = %q, want %q", got.Basis, RecoveryBasisNone)
	}
	if got.Reason != "price is not unusually low" {
		t.Fatalf("reason = %q, want the not-low reason (z was %.2f)", got.Reason, got.ZScore)
	}
}

func TestCalcRecoveryOutlookRefusesAnUnprecedentedDip(t *testing.T) {
	// A flat item with exactly one drop like today's in its whole history.
	// The price is genuinely unusual and the trend is genuinely flat, so
	// neither earlier gate fires — but one precedent is an anecdote, and
	// quoting a median recovery time off it would be the confident-sounding
	// guess this whole model exists to avoid. The matching dip near the
	// start is what keeps the slope insignificant, so the refusal has to
	// come from the episode count rather than from the trend test.
	h := recoveryTestHistory(176, func(day int) float64 {
		p := 100 * math.Exp(0.01*math.Sin(2*math.Pi*float64(day)/13))
		if day >= 3 && day <= 7 {
			p *= 0.88
		}
		if day >= 171 {
			p *= 0.88
		}
		return p
	})

	got := CalcRecoveryOutlook(h, 0)

	if got.Basis != RecoveryBasisNone {
		t.Fatalf("basis = %q, want %q", got.Basis, RecoveryBasisNone)
	}
	if got.Reason != "no comparable dip in this item's history recovered" {
		t.Fatalf("reason = %q (episodes %d, z %.2f), want the no-precedent reason",
			got.Reason, got.Episodes, got.ZScore)
	}
	if got.Episodes >= recoveryMinEpisodes {
		t.Fatalf("episodes = %d, want fewer than %d", got.Episodes, recoveryMinEpisodes)
	}
}

func TestCalcRecoveryOutlookIgnoresTheClock(t *testing.T) {
	// The window is anchored to the newest sample, not to now, so the same
	// series read today and read in a year gives the same answer instead of
	// quietly being fitted over a shrinking tail.
	h := recoveryTestHistory(173, func(day int) float64 {
		return recoverySine(day, 0.10, 30)
	})
	shifted := make([]esi.HistoryEntry, len(h))
	copy(shifted, h)
	for i := range shifted {
		d, err := time.Parse("2006-01-02", shifted[i].Date)
		if err != nil {
			t.Fatalf("parse %q: %v", shifted[i].Date, err)
		}
		shifted[i].Date = d.AddDate(-3, 0, 0).Format("2006-01-02")
	}

	fresh := CalcRecoveryOutlook(h, 0)
	stale := CalcRecoveryOutlook(shifted, 0)

	if fresh.Basis != stale.Basis || fresh.Episodes != stale.Episodes {
		t.Fatalf("three-year-old copy read differently: %+v vs %+v", fresh, stale)
	}
	if math.Abs(fresh.MedianDays-stale.MedianDays) > 1e-9 {
		t.Fatalf("median days drifted with the calendar: %v vs %v", fresh.MedianDays, stale.MedianDays)
	}
}

func TestCalcRecoveryOutlookSkipsUnpricedDays(t *testing.T) {
	// ESI omits or zeroes days for illiquid items. A zero average is a hole,
	// not a free item, and log(0) would poison the whole fit.
	h := recoveryTestHistory(173, func(day int) float64 {
		return recoverySine(day, 0.10, 30)
	})
	h[10].Average = 0
	h[11].Average = -1
	h[12].Date = "not-a-date"

	got := CalcRecoveryOutlook(h, 0)

	if got.Basis != RecoveryBasisHistory {
		t.Fatalf("basis = %q (%s), want the three holes skipped, not fatal", got.Basis, got.Reason)
	}
	if got.Samples != 170 {
		t.Fatalf("samples = %d, want 170", got.Samples)
	}
	if math.IsNaN(got.MedianDays) || math.IsInf(got.TargetPrice, 0) {
		t.Fatalf("unusable numbers survived the holes: %+v", got)
	}
}

// Guards the shape of the refusal contract every caller depends on: when
// the outlook is refused it must carry a reason and no usable figures, so
// a caller cannot accidentally read a zero as a real recovery target.
func TestCalcRecoveryOutlookRefusalsAreAlwaysExplained(t *testing.T) {
	inputs := [][]esi.HistoryEntry{
		nil,
		recoveryTestHistory(10, func(int) float64 { return 100 }),
		recoveryTestHistory(180, func(int) float64 { return 100 }),
		recoveryTestHistory(180, func(day int) float64 { return 100 * math.Exp(-0.01*float64(day)) }),
	}
	for i, in := range inputs {
		t.Run(fmt.Sprintf("case%d", i), func(t *testing.T) {
			got := CalcRecoveryOutlook(in, 0)
			if got.Basis == RecoveryBasisHistory {
				t.Skip("this input is legitimately measurable")
			}
			if got.Reason == "" {
				t.Fatalf("refused with no reason: %+v", got)
			}
			if got.TargetPrice != 0 || got.MedianDays != 0 {
				t.Fatalf("refusal carried usable figures: %+v", got)
			}
		})
	}
}
