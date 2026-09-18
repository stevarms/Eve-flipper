package zkillboard

import (
	"math"
	"testing"
	"time"

	"eve-flipper/internal/sde"
)

// militiaTestSDE is fittingsTestSDE plus the metadata the restockable filter
// reads, and two types that exist only to be rejected by it.
func militiaTestSDE() *sde.Data {
	d := fittingsTestSDE()
	for _, t := range d.Types {
		t.MetaGroupID = 1
		t.MarketGroupID = 100
	}
	// An officer module and something with no market group at all: both are real
	// killmail contents and neither can be restocked from Jita.
	d.Types[14268] = &sde.ItemType{
		ID: 14268, Name: "Draclira's Modified Sensor Booster", Volume: 5,
		CategoryID: sdeCategoryModule, MetaGroupID: 5, MarketGroupID: 100,
	}
	d.Types[3468] = &sde.ItemType{
		ID: 3468, Name: "Unpublished Thing", Volume: 5,
		CategoryID: sdeCategoryModule, MetaGroupID: 1, MarketGroupID: 0,
	}
	return d
}

// lossIn builds a killmail loss in one system, carrying one hull and one item.
func lossIn(systemID int32, killID int64, when time.Time, itemTypeID int32, qty int32) ESIKillmail {
	return ESIKillmail{
		KillmailID:    killID,
		KillmailTime:  when.UTC().Format(time.RFC3339),
		SolarSystemID: systemID,
		Victim: ESIVictim{
			ShipTypeID: 32788, // Cambion
			Items: []ESIItem{
				{TypeID: itemTypeID, Flag: 19, QuantityDestroyed: qty},
			},
		},
	}
}

// TestAggregateMilitiaLosses_ScopeIsLocationNotMembership is finding 5 asserted:
// only 29% of Caldari militia losses happened inside the warzone, so membership
// alone would count Jita and nullsec deaths as FW hub demand.
func TestAggregateMilitiaLosses_ScopeIsLocationNotMembership(t *testing.T) {
	const villasen, jita = 30045334, 30000142
	warzone := map[int32]bool{villasen: true}
	now := time.Now()

	losses := []ESIKillmail{
		lossIn(villasen, 1, now.Add(-1*time.Hour), 2679, 100), // counted
		lossIn(jita, 2, now.Add(-2*time.Hour), 2679, 100000),  // excluded
		lossIn(villasen, 3, now.Add(-3*time.Hour), 2679, 100), // counted
		lossIn(jita, 4, now.Add(-4*time.Hour), 12608, 999),    // excluded
	}

	got := aggregateMilitiaLosses(losses, warzone, militiaTestSDE(), 86400)

	if got.FetchedKills != 4 {
		t.Errorf("FetchedKills = %d, want 4 (everything fetched is reported)", got.FetchedKills)
	}
	if got.InWarzoneKills != 2 {
		t.Errorf("InWarzoneKills = %d, want 2", got.InWarzoneKills)
	}
	if _, ok := got.Items[12608]; ok {
		t.Error("type 12608 was only ever lost in Jita and must not appear")
	}
	scourge := got.Items[2679]
	if scourge == nil {
		t.Fatal("type 2679 was lost twice in the warzone and must appear")
	}
	if scourge.TotalDestroyed != 200 {
		t.Errorf("TotalDestroyed = %d, want 200 -- the 100000 in Jita must not be in it", scourge.TotalDestroyed)
	}
	if scourge.KillmailCount != 2 {
		t.Errorf("KillmailCount = %d, want 2", scourge.KillmailCount)
	}
}

// TestAggregateMilitiaLosses_Winsorize is the ammo-barge guard: one enormous loss
// among ordinary ones must not manufacture a week of demand.
func TestAggregateMilitiaLosses_Winsorize(t *testing.T) {
	const villasen = 30045334
	warzone := map[int32]bool{villasen: true}
	now := time.Now()

	// Nine losses of ~2,000 rounds and one of 10,000. Unwinsorized that is
	// 28,000; at the p90 ceiling the outlier is pulled back to 2,000.
	var losses []ESIKillmail
	for i := 0; i < 9; i++ {
		losses = append(losses, lossIn(villasen, int64(i+1), now.Add(-time.Duration(i+1)*time.Hour), 2679, 2000))
	}
	losses = append(losses, lossIn(villasen, 10, now.Add(-10*time.Hour), 2679, 10000))

	got := aggregateMilitiaLosses(losses, warzone, militiaTestSDE(), 86400)

	p := got.Items[2679]
	if p == nil {
		t.Fatal("type 2679 missing")
	}
	if p.TotalDestroyed != 20000 {
		t.Errorf("TotalDestroyed = %d, want 20000 (10x2000 after winsorizing, not 28000)", p.TotalDestroyed)
	}
	if p.KillmailCount != 10 {
		t.Errorf("KillmailCount = %d, want 10 -- winsorizing caps the amount, not the count", p.KillmailCount)
	}
}

// TestAggregateMilitiaLosses_ThinSignalIsReportedNotDropped pins the decision
// that the analyzer measures and the engine gates. An item on two killmails is
// below minKillsWithItem, and must still be visible with a count that says so.
func TestAggregateMilitiaLosses_ThinSignalIsReportedNotDropped(t *testing.T) {
	const villasen = 30045334
	warzone := map[int32]bool{villasen: true}
	now := time.Now()

	losses := []ESIKillmail{
		lossIn(villasen, 1, now.Add(-1*time.Hour), 5443, 1),
		lossIn(villasen, 2, now.Add(-2*time.Hour), 5443, 1),
	}

	got := aggregateMilitiaLosses(losses, warzone, militiaTestSDE(), 86400)

	p := got.Items[5443]
	if p == nil {
		t.Fatal("a 2-killmail item must still be reported, so the gap table can show it as thin")
	}
	if p.KillmailCount >= minKillsWithItem {
		t.Errorf("KillmailCount = %d, want below the shippability threshold %d", p.KillmailCount, minKillsWithItem)
	}
}

// TestAggregateMilitiaLosses_RestockableFilter: officer drops and types with no
// market group are real killmail contents that cannot be bought in Jita.
func TestAggregateMilitiaLosses_RestockableFilter(t *testing.T) {
	const villasen = 30045334
	warzone := map[int32]bool{villasen: true}
	now := time.Now()

	losses := []ESIKillmail{
		lossIn(villasen, 1, now.Add(-1*time.Hour), 14268, 1), // officer
		lossIn(villasen, 2, now.Add(-2*time.Hour), 3468, 1),  // no market group
		lossIn(villasen, 3, now.Add(-3*time.Hour), 5443, 1),  // ordinary T1 module
	}

	got := aggregateMilitiaLosses(losses, warzone, militiaTestSDE(), 86400)

	if _, ok := got.Items[14268]; ok {
		t.Error("officer module is loot, not stock -- must be filtered")
	}
	if _, ok := got.Items[3468]; ok {
		t.Error("type with MarketGroupID 0 is not on the market -- must be filtered")
	}
	if _, ok := got.Items[5443]; !ok {
		t.Error("ordinary T1 module must survive the filter")
	}
	// The hull is on all three losses and is restockable, which is how we know
	// the filter rejected on metadata rather than dropping everything.
	if got.Items[32788] == nil {
		t.Error("hull missing: the filter rejected too much")
	}
}

// TestAggregateMilitiaLosses_DailyRateUsesRequestedWindow: a quiet week is less
// demand, not a shorter sample, so the requested window is the denominator.
func TestAggregateMilitiaLosses_DailyRateUsesRequestedWindow(t *testing.T) {
	const villasen = 30045334
	warzone := map[int32]bool{villasen: true}
	now := time.Now()

	// 700 rounds over a 7-day window is 100/day before the combat multiplier.
	losses := []ESIKillmail{
		lossIn(villasen, 1, now.Add(-1*time.Hour), 2679, 350),
		lossIn(villasen, 2, now.Add(-2*time.Hour), 2679, 350),
	}

	got := aggregateMilitiaLosses(losses, warzone, militiaTestSDE(), 7*86400)

	p := got.Items[2679]
	if p == nil {
		t.Fatal("type 2679 missing")
	}
	want := 100.0 * combatAmmoMultiplier
	if math.Abs(p.EstDailyDemand-want) > 0.01 {
		t.Errorf("EstDailyDemand = %.2f, want %.2f (700 over 7 days, x%.0f for ammo burned in the fight)",
			p.EstDailyDemand, want, combatAmmoMultiplier)
	}
	// Hulls are not fired, so they get no multiplier: 2 over 7 days.
	if hull := got.Items[32788]; hull == nil {
		t.Error("hull missing")
	} else if math.Abs(hull.EstDailyDemand-2.0/7.0) > 0.01 {
		t.Errorf("hull EstDailyDemand = %.4f, want %.4f -- the ammo multiplier must not reach ships",
			hull.EstDailyDemand, 2.0/7.0)
	}
}

// TestAggregateMilitiaLosses_NoWarzoneKills: a militia that lost nothing inside
// its own warzone yields an empty profile, not an error and not region data.
func TestAggregateMilitiaLosses_NoWarzoneKills(t *testing.T) {
	now := time.Now()
	losses := []ESIKillmail{lossIn(30000142, 1, now, 2679, 5000)}

	got := aggregateMilitiaLosses(losses, map[int32]bool{30045334: true}, militiaTestSDE(), 86400)

	if got.FetchedKills != 1 {
		t.Errorf("FetchedKills = %d, want 1", got.FetchedKills)
	}
	if got.InWarzoneKills != 0 {
		t.Errorf("InWarzoneKills = %d, want 0", got.InWarzoneKills)
	}
	if len(got.Items) != 0 {
		t.Errorf("Items = %d, want 0", len(got.Items))
	}
}

func TestDailyScale(t *testing.T) {
	if got := dailyScale(86400, false, time.Time{}); math.Abs(got-1.0) > 1e-9 {
		t.Errorf("24h window scale = %v, want 1", got)
	}
	if got := dailyScale(7*86400, false, time.Time{}); math.Abs(got-1.0/7.0) > 1e-9 {
		t.Errorf("7d window scale = %v, want 1/7", got)
	}

	// Truncated: the page cap was hit before the window was reached, so the
	// oldest killmail is the real edge of coverage. Dividing by the requested
	// 7 days would under-report a rate measured over 2.
	oldest := time.Now().Add(-48 * time.Hour)
	got := dailyScale(7*86400, true, oldest)
	if math.Abs(got-0.5) > 0.01 {
		t.Errorf("truncated scale = %v, want ~0.5 (2 days covered)", got)
	}

	// A truncated run whose oldest kill is older than the window cannot narrow
	// it -- the window still bounds what was asked for.
	if got := dailyScale(86400, true, time.Now().Add(-30*24*time.Hour)); math.Abs(got-1.0) > 1e-9 {
		t.Errorf("truncated but window-bounded scale = %v, want 1", got)
	}
	if got := dailyScale(0, false, time.Time{}); got != 0 {
		t.Errorf("zero window scale = %v, want 0", got)
	}
}

func TestRestockable(t *testing.T) {
	d := militiaTestSDE()

	if !restockable(nil, 12345) {
		t.Error("with no SDE nothing can be judged, so everything must pass")
	}
	if restockable(d, 999999) {
		t.Error("a type absent from a loaded SDE is unknown and must be rejected")
	}
	for _, meta := range []int32{5, 6, 15} {
		d.Types[5443].MetaGroupID = meta
		if restockable(d, 5443) {
			t.Errorf("MetaGroupID %d must be rejected", meta)
		}
	}
	for _, meta := range []int32{0, 1, 2, 3, 4, 14} {
		d.Types[5443].MetaGroupID = meta
		if !restockable(d, 5443) {
			t.Errorf("MetaGroupID %d is buyable and must pass", meta)
		}
	}
}
