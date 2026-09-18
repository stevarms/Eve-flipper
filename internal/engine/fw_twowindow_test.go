package engine

import (
	"reflect"
	"testing"
)

// fw_twowindow_test.go -- which of the two destruction rates sizes a lot.
//
// Both rates are always shown; only one drives cover, the verdict and the
// quantity. The failure this file guards is specific and quiet: a rate of zero is
// unbounded cover, unbounded cover is "covered", and a covered row stops asking to
// be restocked. So every path that could hand the sizing arithmetic a zero has to
// be shown either not to, or to say clearly which window it fell back to.

// twoWindowItem is a module dear enough to sit above every minimum-lot band, so
// the quantities here are the cover model's own and nothing else's.
func twoWindowItem() FWSupplyItem {
	return FWSupplyItem{
		TypeID: 1001, TypeName: "Two-Window Module", Category: "module",
		VolumeM3: 10, JitaBestSell: 6_000_000,
		DailyDestroyed: 40, KillsWithItem: 10,
		DailyDestroyedLong: 4, KillsWithItemLong: 100,
	}
}

// TestFWSizeAgainstLongUsesTheLongRate: the setting has to actually move the
// number, and it has to say that it did.
//
// A week that saw forty a day against a quarter that saw four is a spike, and
// which of those two figures a lot is sized against is a ten-fold difference in
// capital committed. The row records which one spoke, because a reader looking at
// two rates and one quantity cannot otherwise tell.
func TestFWSizeAgainstLongUsesTheLongRate(t *testing.T) {
	cfg := coverConfig()
	shortRow := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{twoWindowItem()}, cfg), 1001)

	cfg.SizeAgainstLong = true
	longRow := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{twoWindowItem()}, cfg), 1001)

	if shortRow.SizedBy != FWSizedByShort || longRow.SizedBy != FWSizedByLong {
		t.Fatalf("sized_by wrong: short pass says %q, long pass says %q", shortRow.SizedBy, longRow.SizedBy)
	}
	// Seven days of cover at 40/day against seven at 4/day.
	if shortRow.SuggestedQty != 280 {
		t.Errorf("short sizing: got %d want 280 (7 days at 40/day)", shortRow.SuggestedQty)
	}
	if longRow.SuggestedQty != 28 {
		t.Errorf("long sizing: got %d want 28 (7 days at 4/day)", longRow.SuggestedQty)
	}
	// Both rates ride along on both rows either way. The pair is the point; the
	// setting only chooses which one does the arithmetic.
	for _, row := range []FWSupplyRow{shortRow, longRow} {
		if row.DailyDestroyed != 40 || row.DailyDestroyedLong != 4 {
			t.Errorf("a row lost one of its two rates: %+v", row)
		}
		if row.KillsWithItem != 10 || row.KillsWithItemLong != 100 {
			t.Errorf("a row lost one of its two evidence counts: %+v", row)
		}
	}
}

// TestFWSizingFallsBackSymmetrically is the union rule made safe.
//
// The candidate set is the union of the two windows, so a row can exist with a
// rate in one window and nothing in the other. Sizing it against the empty side
// would give it unbounded cover and file it as already stocked -- the row would
// still be in the table, reading as nothing to do. That is the one direction that
// quietly costs a sale, so the fallback runs both ways, and the row says which way
// it went rather than leaving a reader to infer it from two numbers.
func TestFWSizingFallsBackSymmetrically(t *testing.T) {
	// A staple that did not die this week: nothing in the short window, a habit in
	// the long one. The default configuration sizes short, and must not.
	staple := FWSupplyItem{
		TypeID: 1877, TypeName: "Scourge Fury Light Missile", Category: "ammo",
		VolumeM3: 0.0025, JitaBestSell: 6_000_000,
		DailyDestroyedLong: 900, KillsWithItemLong: 640,
	}
	row := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{staple}, coverConfig()), 1877)
	if row.SizedBy != FWSizedByLong {
		t.Errorf("a long-only staple was sized by %q, so it was sized against zero", row.SizedBy)
	}
	if row.DaysOfCover.Unbounded() {
		t.Error("a long-only staple got unbounded cover, which reads as already stocked -- the silent failure")
	}
	if row.Verdict == VerdictCovered {
		t.Errorf("a long-only staple with no stock was filed as covered: %+v", row)
	}
	if row.SuggestedQty != 6300 {
		t.Errorf("long fallback quantity: got %d want 6300 (7 days at 900/day)", row.SuggestedQty)
	}

	// And the other direction: a spike only this week, on a campaign that asked to
	// size against the quarter. Same reasoning, opposite window.
	spike := FWSupplyItem{
		TypeID: 593, TypeName: "Tristan", Category: "ship",
		VolumeM3: 27_289, JitaBestSell: 6_000_000,
		DailyDestroyed: 3, KillsWithItem: 12,
	}
	cfg := coverConfig()
	cfg.SizeAgainstLong = true
	spikeRow := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{spike}, cfg), 593)
	if spikeRow.SizedBy != FWSizedByShort {
		t.Errorf("a short-only spike was sized by %q, so it was sized against zero", spikeRow.SizedBy)
	}
	if spikeRow.DaysOfCover.Unbounded() || spikeRow.SuggestedQty != 21 {
		t.Errorf("short fallback: cover %v qty %d, want 7 days at 3/day = 21",
			spikeRow.DaysOfCover, spikeRow.SuggestedQty)
	}
}

// TestFWNoLongWindowMakesTheSettingInert is the no-regression guard.
//
// Every campaign that predates the long window has size_against at its default,
// and a campaign can also ask to size long while measuring nothing long -- the db
// layer refuses that combination, but the engine must not depend on it. With no
// long rates present, both settings have to produce the identical plan, because
// the alternative is a setting that silently zeroes the arithmetic.
func TestFWNoLongWindowMakesTheSettingInert(t *testing.T) {
	items := []FWSupplyItem{
		{TypeID: 24492, TypeName: "Inferno Light Missile", Category: "ammo",
			VolumeM3: 0.0025, JitaBestSell: 105, DailyDestroyed: 4553, KillsWithItem: 180},
		{TypeID: 3831, TypeName: "Miner I", Category: "module",
			VolumeM3: 5, JitaBestSell: 9_300, DailyDestroyed: 0.3, KillsWithItem: 40},
		{TypeID: 2488, TypeName: "Warrior II", Category: "drone",
			VolumeM3: 5, JitaBestSell: 750_000, DailyDestroyed: 40, KillsWithItem: 2},
	}

	cfg := coverConfig()
	shortPlan := BuildFWSupplyPlan(items, cfg)
	cfg.SizeAgainstLong = true
	longPlan := BuildFWSupplyPlan(items, cfg)

	if !reflect.DeepEqual(shortPlan, longPlan) {
		t.Errorf("size_against changed a plan with no long window measured:\n short %+v\n long  %+v", shortPlan, longPlan)
	}
	for _, row := range shortPlan {
		if row.SizedBy != FWSizedByShort {
			t.Errorf("%s was sized by %q with no long rate to size against", row.TypeName, row.SizedBy)
		}
		if row.DailyDestroyedLong != 0 || row.KillsWithItemLong != 0 {
			t.Errorf("%s invented long-window figures: %+v", row.TypeName, row)
		}
	}
}

// TestFWThinEvidenceIsJudgedAgainstTheSizingWindow: three kills in ninety days is
// a thinner signal than three in seven, so the count MinKillsWithItem is measured
// against has to come from the same window as the rate that sized the row.
//
// Warrior II here is thin this week and a habit over the quarter. Sized short it
// is not shippable and the gap table shows it as thin, which is correct; sized
// long it is shippable, which is also correct. What would be wrong is either
// window's rate judged against the other's evidence.
func TestFWThinEvidenceIsJudgedAgainstTheSizingWindow(t *testing.T) {
	item := FWSupplyItem{
		TypeID: 2488, TypeName: "Warrior II", Category: "drone",
		VolumeM3: 5, JitaBestSell: 6_000_000,
		DailyDestroyed: 40, KillsWithItem: 2,
		DailyDestroyedLong: 6, KillsWithItemLong: 30,
	}

	cfg := coverConfig()
	shortRow := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{item}, cfg), 2488)
	if shortRow.Shippable {
		t.Error("two losses in seven days read as evidence enough to ship")
	}
	if shortRow.SuggestedQty == 0 {
		t.Error("a thin row lost its quantity -- the gap table shows thinness as thin, it does not hide the row")
	}

	cfg.SizeAgainstLong = true
	longRow := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{item}, cfg), 2488)
	if !longRow.Shippable {
		t.Error("thirty losses over the quarter still read as thin evidence")
	}
	if longRow.SuggestedQty != 42 {
		t.Errorf("long sizing: got %d want 42 (7 days at 6/day)", longRow.SuggestedQty)
	}
}
