package zkillboard

import (
	"math"
	"strings"
	"testing"
	"time"
)

/* The long window's denominator.
 *
 * This is the file that matters most in the long-window change, because the bug it
 * pins is silent and it points the wrong way. dailyScale's rule -- scale by the
 * span back to the oldest killmail seen -- is correct for the pastSeconds path,
 * where the sample is one contiguous run cut off at its far end. Once months are
 * paged separately it is wrong: a month that hit its 100-page limit leaves a hole
 * with covered time on both sides of it, and treating the whole span as covered
 * counts those missing days as days when nothing died. That under-reports the
 * rate, and an under-reported rate reads on screen as a covered item -- so the
 * failure mode is not an obviously wrong number, it is a row that quietly stops
 * asking to be restocked.
 */

// monthCov builds a coverage record for a whole, untruncated month.
func monthCov(year, month int) MonthCoverage {
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	return MonthCoverage{
		Year: year, Month: month, Pages: 40, Killmails: 8000,
		NewestSeen: start.AddDate(0, 1, 0).Add(-time.Hour),
		OldestSeen: start.Add(time.Hour),
	}
}

// truncatedMonthCov is a month whose sample stops partway in: the page cap was
// hit, so everything before oldestSeen is missing.
func truncatedMonthCov(year, month int, oldestSeen time.Time) MonthCoverage {
	c := monthCov(year, month)
	c.Pages = zkillMaxPages
	c.OldestSeen = oldestSeen
	c.Truncated = true
	c.Reason = "zkillboard's 100-page-per-month limit"
	return c
}

// naiveSpanScale is what dailyScale does: one span, from the oldest killmail
// anywhere in the walk to now. It is here as the wrong answer, written out, so the
// test below can assert the right one is bigger.
func naiveSpanScale(months []MonthCoverage, now time.Time) float64 {
	var oldest time.Time
	for _, m := range months {
		if m.OldestSeen.IsZero() {
			continue
		}
		if oldest.IsZero() || m.OldestSeen.Before(oldest) {
			oldest = m.OldestSeen
		}
	}
	if oldest.IsZero() {
		return 0
	}
	return now.Sub(oldest).Seconds()
}

// TestCoveredSeconds_TruncatedMiddleMonthIsAHoleNotAShorterSpan is the
// load-bearing assertion. A truncated month in the middle of a 90-day window must
// make the rate HIGHER than span scaling would, because the same destruction is
// divided by less covered time. Asserted as a strict inequality, since that is the
// direction the bug goes.
func TestCoveredSeconds_TruncatedMiddleMonthIsAHoleNotAShorterSpan(t *testing.T) {
	// A fixed clock, so the month arithmetic is readable: three whole months back
	// from the middle of December.
	now := time.Date(2026, 12, 15, 12, 0, 0, 0, time.UTC)
	since := now.AddDate(0, 0, -90) // 2026-09-16

	// December and October came back whole. November hit the page cap ten days in,
	// so 2026-11-01 through 2026-11-20 was never sampled.
	novTruncated := truncatedMonthCov(2026, 11, time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC))
	months := []MonthCoverage{
		monthCov(2026, 12),
		novTruncated,
		monthCov(2026, 10),
		monthCov(2026, 9),
	}

	covered := coveredSeconds(months, since, now)
	naive := naiveSpanScale(months, now)

	if covered <= 0 {
		t.Fatalf("covered = %v, want a positive denominator", covered)
	}

	// The whole point: less covered time, so a bigger per-day rate.
	if !(covered < naive) {
		t.Fatalf("covered %.1f days is not less than the naive span of %.1f days -- the hole was counted as covered",
			covered/86400, naive/86400)
		return
	}
	coveredRate := 86400 / covered
	naiveRate := 86400 / naive
	if !(coveredRate > naiveRate) {
		t.Errorf("per-month scaling gave rate %.4f, not more than span scaling's %.4f", coveredRate, naiveRate)
	}

	// And the size of the difference is the twenty missing days, within a day of
	// rounding: the window is 90 days, three whole ones plus the part-months at
	// each end, minus November's missing twenty.
	wantDays := now.Sub(since).Hours()/24 - 20
	if gotDays := covered / 86400; math.Abs(gotDays-wantDays) > 1 {
		t.Errorf("covered %.2f days, want about %.2f (the window less November's missing twenty)", gotDays, wantDays)
	}
}

// TestCoveredSeconds_WholeMonthsSumToTheWindow: with nothing truncated, the
// per-month sum must equal the window itself. Otherwise the careful path would
// report a different rate from the simple one on identical data, and there would
// be no way to tell which was right.
func TestCoveredSeconds_WholeMonthsSumToTheWindow(t *testing.T) {
	now := time.Date(2026, 12, 15, 12, 0, 0, 0, time.UTC)
	since := now.AddDate(0, 0, -90)

	months := []MonthCoverage{monthCov(2026, 12), monthCov(2026, 11), monthCov(2026, 10), monthCov(2026, 9)}

	covered := coveredSeconds(months, since, now)
	if want := now.Sub(since).Seconds(); math.Abs(covered-want) > 1 {
		t.Errorf("covered %.0f s, want the whole %.0f s window", covered, want)
	}
}

// TestCoveredSeconds_EdgeMonthsAreClippedToTheWindow: the first and last months of
// a window are only partly inside it, and claiming the whole of either would scale
// by more time than was asked for and under-report the rate.
func TestCoveredSeconds_EdgeMonthsAreClippedToTheWindow(t *testing.T) {
	now := time.Date(2026, 12, 15, 12, 0, 0, 0, time.UTC)
	since := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	months := []MonthCoverage{monthCov(2026, 12), monthCov(2026, 11)}
	covered := coveredSeconds(months, since, now)

	if want := now.Sub(since).Seconds(); math.Abs(covered-want) > 1 {
		t.Errorf("covered %.2f days, want the %.2f days between since and now",
			covered/86400, want/86400)
	}
}

// TestCoveredSeconds_TruncationBeyondTheWindowEdgeCostsNothing: a month truncated
// before the point the window starts is not missing anything the window wanted.
func TestCoveredSeconds_TruncationBeyondTheWindowEdgeCostsNothing(t *testing.T) {
	now := time.Date(2026, 12, 15, 12, 0, 0, 0, time.UTC)
	since := time.Date(2026, 12, 10, 0, 0, 0, 0, time.UTC)

	// The month's sample stops on the 5th, which is five days before the window
	// even begins.
	months := []MonthCoverage{truncatedMonthCov(2026, 12, time.Date(2026, 12, 5, 0, 0, 0, 0, time.UTC))}

	covered := coveredSeconds(months, since, now)
	if want := now.Sub(since).Seconds(); math.Abs(covered-want) > 1 {
		t.Errorf("covered %.2f days, want the full %.2f-day window", covered/86400, want/86400)
	}
}

// TestCoveredSeconds_MonthWithNothingInItClaimsNothing: a month that was cut off
// before returning a single killmail covered no time, and must not be credited
// with any -- crediting it is the same arithmetic error as the hole, one month wide.
func TestCoveredSeconds_MonthWithNothingInItClaimsNothing(t *testing.T) {
	now := time.Date(2026, 12, 15, 12, 0, 0, 0, time.UTC)
	since := now.AddDate(0, -2, 0)

	empty := MonthCoverage{Year: 2026, Month: 10, Truncated: true, Reason: "a page failed to fetch"}
	months := []MonthCoverage{monthCov(2026, 12), monthCov(2026, 11), empty}

	covered := coveredSeconds(months, since, now)
	withoutOctober := coveredSeconds(months[:2], since, now)
	if math.Abs(covered-withoutOctober) > 1 {
		t.Errorf("an empty month added %.2f days of coverage", (covered-withoutOctober)/86400)
	}
	if covered <= 0 {
		t.Fatal("the two real months covered nothing")
	}
}

func TestCoveredSeconds_NoMonthsIsZero(t *testing.T) {
	now := time.Now().UTC()
	if got := coveredSeconds(nil, now.AddDate(0, -3, 0), now); got != 0 {
		t.Errorf("coveredSeconds(nil) = %v, want 0", got)
	}
}

// TestCoverageWarnings_NameTheMonthAndTheScaling: both halves of the sentence are
// load-bearing. Without the shortfall the numbers look complete; without the
// scaling they look wrong.
func TestCoverageWarnings_NameTheMonthAndTheScaling(t *testing.T) {
	now := time.Date(2026, 12, 15, 12, 0, 0, 0, time.UTC)
	since := now.AddDate(0, 0, -90)

	walk := &LossWalk{
		Pages: 300,
		Months: []MonthCoverage{
			monthCov(2026, 12),
			truncatedMonthCov(2026, 11, time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)),
			monthCov(2026, 10),
			monthCov(2026, 9),
		},
	}

	warnings := coverageWarnings(walk, since, now, 90*86400)
	if len(warnings) == 0 {
		t.Fatal("a truncated month produced no warning at all")
	}
	joined := strings.Join(warnings, " | ")
	for _, want := range []string{"November 2026", "100-page", "4 days", "scaled to what was actually covered"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings missing %q: %s", want, joined)
		}
	}
}

// TestCoverageWarnings_BudgetIsItsOwnSentence: the walk's page budget running out
// means months older than the last one listed were never asked for, which is not
// the same fact as a month coming back short.
func TestCoverageWarnings_BudgetIsItsOwnSentence(t *testing.T) {
	now := time.Date(2026, 12, 15, 12, 0, 0, 0, time.UTC)
	since := now.AddDate(0, 0, -90)

	walk := &LossWalk{
		Pages:         fwMaxLossPages,
		BudgetReached: true,
		Months:        []MonthCoverage{monthCov(2026, 12), monthCov(2026, 11)},
	}

	joined := strings.Join(coverageWarnings(walk, since, now, 90*86400), " | ")
	if !strings.Contains(joined, "walk budget ran out") {
		t.Errorf("no budget warning: %s", joined)
	}
	// Two months of a ninety-day window is well short, and that has to be said
	// too: the rates are per-day and correct, but a reader deciding what to ship
	// needs to know they rest on a third of the sample asked for.
	if !strings.Contains(joined, "of the 90 days asked for") {
		t.Errorf("no shortfall warning for a 90-day window covered by two months: %s", joined)
	}
}

// TestCoverageWarnings_WholeWindowIsSilent: warnings are for shortfalls. A walk
// that covered what it asked for must not produce a sentence, or every plan
// carries one and none of them mean anything.
func TestCoverageWarnings_WholeWindowIsSilent(t *testing.T) {
	now := time.Date(2026, 12, 15, 12, 0, 0, 0, time.UTC)
	since := now.AddDate(0, 0, -90)

	walk := &LossWalk{
		Pages:  280,
		Months: []MonthCoverage{monthCov(2026, 12), monthCov(2026, 11), monthCov(2026, 10), monthCov(2026, 9)},
	}

	if got := coverageWarnings(walk, since, now, 90*86400); len(got) != 0 {
		t.Errorf("a complete walk warned anyway: %v", got)
	}
}
