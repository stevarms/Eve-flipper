package engine

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"eve-flipper/internal/esi"
)

// The seven Black Rise stations in the frozen sell book. Three are Onnamon's
// (the highsec bulwark), three are on the Caldari front, and Hallanen is the
// NPC-seeded book that reads as the deepest market in the warzone until the
// seeds come out.
const (
	staOnnamonIV    int64 = 60015070
	staOnnamonIII   int64 = 60015131
	staOnnamonVIII  int64 = 60015184
	staVillasenV    int64 = 60015108
	staRakapasIV    int64 = 60015077
	staRakapasV     int64 = 60015130
	staHallanenVIII int64 = 60015125
)

// blackRiseOrderCount is every sell order in the fixture. Asserted on load: a
// short parse would quietly turn every count below into a smaller true statement
// about a smaller book.
const blackRiseOrderCount = 6175

// stationBook is one line of the fixture: a station and its [type_id, duration]
// pairs. Only those two fields were kept, because the ring scores how much of a
// real market sits at a station, not what it charges.
type stationBook struct {
	LocationID int64      `json:"location_id"`
	Orders     [][2]int32 `json:"orders"`
}

// realBlackRiseBook is the live sell book at those seven stations, captured
// 2026-09-16, expanded back into orders.
func realBlackRiseBook(t *testing.T) []esi.MarketOrder {
	t.Helper()

	f, err := os.Open(filepath.Join("testdata", "black_rise_sell_book.jsonl"))
	if err != nil {
		t.Fatalf("open black rise sell book: %v", err)
	}
	defer f.Close()

	var out []esi.MarketOrder
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		var book stationBook
		if err := json.Unmarshal(line, &book); err != nil {
			t.Fatalf("unmarshal station book: %v", err)
		}
		for _, pair := range book.Orders {
			out = append(out, esi.MarketOrder{
				LocationID: book.LocationID,
				TypeID:     pair[0],
				Duration:   pair[1],
			})
		}
	}
	if len(out) != blackRiseOrderCount {
		t.Fatalf("parsed %d orders, want the captured %d", len(out), blackRiseOrderCount)
	}
	return out
}

// realDestroyedTypes is what Caldari militia lost inside the warzone over the
// frozen 20-hour window: 441 of 609 losses, 801 distinct types.
func realDestroyedTypes(t *testing.T) map[int32]bool {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", "caldari_destroyed_types.json"))
	if err != nil {
		t.Fatalf("read destroyed types: %v", err)
	}
	var doc struct {
		MilitiaFactionID    int32   `json:"militia_faction_id"`
		LossesInsideWarzone int     `json:"losses_inside_warzone"`
		TypeIDs             []int32 `json:"type_ids"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal destroyed types: %v", err)
	}
	if doc.MilitiaFactionID != esi.MilitiaCaldari {
		t.Fatalf("fixture militia = %d, want Caldari %d", doc.MilitiaFactionID, esi.MilitiaCaldari)
	}
	if len(doc.TypeIDs) != 801 {
		t.Fatalf("fixture has %d destroyed types, want the captured 801", len(doc.TypeIDs))
	}
	if doc.LossesInsideWarzone != 441 {
		t.Fatalf("fixture covers %d in-warzone losses, want the captured 441", doc.LossesInsideWarzone)
	}

	set := make(map[int32]bool, len(doc.TypeIDs))
	for _, id := range doc.TypeIDs {
		set[id] = true
	}
	return set
}

// asPlayerOrders returns the same book with every duration rewritten to 90 days,
// which is the longest a player can list.
//
// It is the counterfactual for finding 6: this is the book as it would read if we
// could not tell a seed from a competitor. Running the real MeasureStagingDepth
// over it, rather than a second copy of the counting logic, is what makes the
// comparison mean something.
func asPlayerOrders(orders []esi.MarketOrder) []esi.MarketOrder {
	out := make([]esi.MarketOrder, len(orders))
	copy(out, orders)
	for i := range out {
		out[i].Duration = 90
	}
	return out
}

// TestMeasureStagingDepth_SeedsAreNotDepth measures each station's book twice --
// as it is, and as it would read with seeds indistinguishable -- against the live
// numbers.
//
// The wantAll column is not decoration. Hallanen lists 405 sell types and 55 of
// them are player orders; Onnamon III lists 562 and 152. Anything that reads the
// raw book takes a station nobody trades at for a deep market, which is the one
// mistake that would poison every number downstream of it.
func TestMeasureStagingDepth_SeedsAreNotDepth(t *testing.T) {
	book := realBlackRiseBook(t)
	destroyed := realDestroyedTypes(t)

	got := MeasureStagingDepth(book, destroyed)
	unfiltered := MeasureStagingDepth(asPlayerOrders(book), destroyed)

	for _, tc := range []struct {
		name    string
		station int64
		// Player orders only -- what the ring scores.
		wantOrders, wantTypes, wantOverlap int
		// The same station read without the seed filter.
		wantAllOrders, wantAllTypes int
	}{
		{"Onnamon IV", staOnnamonIV, 3140, 1999, 636, 3140, 1999},
		{"Onnamon III", staOnnamonIII, 176, 152, 50, 599, 562},
		{"Onnamon VIII", staOnnamonVIII, 509, 401, 207, 860, 752},
		{"Villasen V", staVillasenV, 569, 449, 157, 571, 451},
		{"Rakapas IV", staRakapasIV, 11, 11, 4, 11, 11},
		{"Rakapas V", staRakapasV, 580, 520, 208, 581, 521},
		{"Hallanen VIII", staHallanenVIII, 63, 55, 34, 413, 405},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := got[tc.station]
			if d.Orders != tc.wantOrders {
				t.Errorf("player orders = %d, want %d", d.Orders, tc.wantOrders)
			}
			if d.Types != tc.wantTypes {
				t.Errorf("player types = %d, want %d", d.Types, tc.wantTypes)
			}
			if d.Overlap != tc.wantOverlap {
				t.Errorf("FW-item overlap = %d, want %d", d.Overlap, tc.wantOverlap)
			}

			all := unfiltered[tc.station]
			if all.Orders != tc.wantAllOrders || all.Types != tc.wantAllTypes {
				t.Errorf("unfiltered = %d orders / %d types, want %d / %d",
					all.Orders, all.Types, tc.wantAllOrders, tc.wantAllTypes)
			}
		})
	}

	// Onnamon IV carries no seed at all, so the filter is not doing the work
	// there -- the 1,999 types are real, and that is why it is the destination.
	if got[staOnnamonIV] != unfiltered[staOnnamonIV] {
		t.Errorf("Onnamon IV depth changed with the filter (%+v vs %+v); the measured book has zero seeds",
			got[staOnnamonIV], unfiltered[staOnnamonIV])
	}

	// The reversal the filter buys: unfiltered, Onnamon III looks like the broader
	// market of the two; filtered, Villasen is three times broader.
	if !(unfiltered[staOnnamonIII].Types > unfiltered[staVillasenV].Types) {
		t.Errorf("unfiltered, Onnamon III (%d types) should out-read Villasen (%d) -- otherwise this pair proves nothing",
			unfiltered[staOnnamonIII].Types, unfiltered[staVillasenV].Types)
	}
	if !(got[staVillasenV].Types > 2*got[staOnnamonIII].Types) {
		t.Errorf("filtered, Villasen (%d types) must be far broader than Onnamon III (%d)",
			got[staVillasenV].Types, got[staOnnamonIII].Types)
	}
}

// TestMeasureStagingDepth_UnknownIsNotGood: a station with no orders in the book
// gets zeroes, and zeroes score badly. A missing market must never read as an
// empty one waiting to be filled.
func TestMeasureStagingDepth_UnknownIsNotGood(t *testing.T) {
	depth := MeasureStagingDepth(realBlackRiseBook(t), realDestroyedTypes(t))
	if d, ok := depth[60003760]; ok {
		t.Errorf("Jita 4-4 is not in the Black Rise fixture but has depth %+v", d)
	}

	scored := ScoreStagingCandidates([]FWStagingCandidate{{
		StationID: 60003760, Security: 0.95, JumpsToFront: 0, HighsecRoute: true,
	}}, depth, DefaultMaxJumpsFromFront)
	c := scored[0]
	if c.PlayerOrders != 0 || c.PlayerTypes != 0 || c.FWItemOverlap != 0 {
		t.Errorf("depth = %d/%d/%d, want zeroes for a station with no book",
			c.PlayerOrders, c.PlayerTypes, c.FWItemOverlap)
	}
	// Everything but the demand terms is perfect here, and those are 0.70 of the
	// score, so the ceiling for a station with no market is 30.
	if c.Score > 30.001 {
		t.Errorf("score = %.2f with no market at all; the demand terms must dominate", c.Score)
	}
}

// TestScoreStagingCandidates_OnnamonOutranksTheFrontline is the whole of §1 run
// end to end on real data: the real gate graph, the real occupied set, the real
// sell book and the real losses.
//
// It is the plan's central claim, and it is the one that surprised me when I
// measured it. Onnamon is highsec, uncontested, absent from /fw/systems/, and one
// jump off the front -- and it beats both frontline hubs decisively, because that
// is where the militia actually shops. A tool restricted to contested systems
// would never have offered it.
//
// The other candidates in the ring have no book in the fixture, so this asserts
// the ordering among the seven Black Rise stations. That is the comparison the
// picker has to get right.
func TestScoreStagingCandidates_OnnamonOutranksTheFrontline(t *testing.T) {
	u, d := loadRealUniverse(t)
	frontline := esi.OccupiedBy(realFWSystems(t), esi.MilitiaCaldari)

	ring := DeriveStagingRing(u, d, frontline, RingOptions{
		MaxJumpsFromFront: DefaultMaxJumpsFromFront,
		SourceSystemID:    sysJita,
		BulwarkSystemID:   sysOnnamon,
	})
	scored := ScoreStagingCandidates(ring, MeasureStagingDepth(realBlackRiseBook(t), realDestroyedTypes(t)), DefaultMaxJumpsFromFront)

	rank := make(map[int64]int, len(scored))
	byStation := make(map[int64]FWStagingCandidate, len(scored))
	for i, c := range scored {
		rank[c.StationID] = i
		byStation[c.StationID] = c
	}

	if rank[staOnnamonIV] != 0 {
		t.Errorf("Onnamon IV ranks %d, want first -- 1,999 player types and 636 of them being destroyed",
			rank[staOnnamonIV])
	}
	for _, station := range []int64{staOnnamonIII, staOnnamonVIII, staVillasenV, staRakapasIV, staRakapasV, staHallanenVIII} {
		if _, found := byStation[station]; !found {
			t.Fatalf("station %d must be in the Caldari ring", station)
		}
		if gap := byStation[staOnnamonIV].Score - byStation[station].Score; gap < 10 {
			t.Errorf("Onnamon IV leads %d by only %.2f points; the measured gap is not a hairline",
				station, gap)
		}
	}

	// Hallanen is the load-bearing one. Its raw book is comparable to Villasen's
	// and Rakapas', and its player book is a seventh of either -- so it must rank
	// below both, and the ring must not offer it as a frontline alternative.
	for _, better := range []int64{staVillasenV, staRakapasV} {
		if byStation[staHallanenVIII].Score >= byStation[better].Score {
			t.Errorf("Hallanen (%.2f) must rank below %d (%.2f): 55 player types against ~450",
				byStation[staHallanenVIII].Score, better, byStation[better].Score)
		}
	}

	// Eleven orders is not a market. Rakapas IV is in the same system as Rakapas V
	// and must not borrow its rank.
	for _, station := range []int64{staOnnamonIV, staOnnamonIII, staOnnamonVIII, staVillasenV, staRakapasV, staHallanenVIII} {
		if byStation[staRakapasIV].Score >= byStation[station].Score {
			t.Errorf("Rakapas IV (11 orders, %.2f) must rank below %d (%.2f)",
				byStation[staRakapasIV].Score, station, byStation[station].Score)
		}
	}

	for _, c := range scored[:len(scored)-1] {
		if c.Score < 0 || c.Score > 100.001 {
			t.Fatalf("station %d scored %.4f, outside 0..100", c.StationID, c.Score)
		}
	}

	// Measured: 96.7 / 85.4 / 75.8 / 71.7 / 71.5 / 57.4 / 35.8. Villasen and
	// Onnamon III land two tenths apart -- broad and lowsec against thin and safe --
	// and that is deliberately not asserted. The score puts the right station on
	// top and refuses to adjudicate the middle, which is the judgement the trader
	// makes from the columns beside it.
	t.Logf("Onnamon IV %.1f | Onnamon VIII %.1f | Rakapas V %.1f | Villasen V %.1f | Onnamon III %.1f | Hallanen %.1f | Rakapas IV %.1f",
		byStation[staOnnamonIV].Score, byStation[staOnnamonVIII].Score, byStation[staRakapasV].Score,
		byStation[staVillasenV].Score, byStation[staOnnamonIII].Score,
		byStation[staHallanenVIII].Score, byStation[staRakapasIV].Score)
}

// TestScoreStagingCandidates_SeedsWouldMisrankHallanen scores the same ring twice
// and shows what the filter is worth.
//
// Onnamon IV holds no seeds, so its score must be identical either way; Hallanen
// and Onnamon III are mostly seed, so theirs must fall. A test that only checked
// the filtered side could not tell the filter from a no-op.
func TestScoreStagingCandidates_SeedsWouldMisrankHallanen(t *testing.T) {
	u, d := loadRealUniverse(t)
	frontline := esi.OccupiedBy(realFWSystems(t), esi.MilitiaCaldari)
	book := realBlackRiseBook(t)
	destroyed := realDestroyedTypes(t)

	scoreOf := func(orders []esi.MarketOrder) map[int64]float64 {
		ring := DeriveStagingRing(u, d, frontline, RingOptions{
			MaxJumpsFromFront: DefaultMaxJumpsFromFront,
			SourceSystemID:    sysJita,
			BulwarkSystemID:   sysOnnamon,
		})
		out := make(map[int64]float64, len(ring))
		for _, c := range ScoreStagingCandidates(ring, MeasureStagingDepth(orders, destroyed), DefaultMaxJumpsFromFront) {
			out[c.StationID] = c.Score
		}
		return out
	}

	filtered := scoreOf(book)
	unfiltered := scoreOf(asPlayerOrders(book))

	if filtered[staOnnamonIV] != unfiltered[staOnnamonIV] {
		t.Errorf("Onnamon IV scored %.4f filtered and %.4f unfiltered; it has no seeds to filter",
			filtered[staOnnamonIV], unfiltered[staOnnamonIV])
	}
	for _, station := range []int64{staHallanenVIII, staOnnamonIII, staOnnamonVIII} {
		if !(unfiltered[station] > filtered[station]) {
			t.Errorf("station %d scored %.4f filtered and %.4f unfiltered; a seeded book must flatter it",
				station, filtered[station], unfiltered[station])
		}
	}
}

// TestScoreStagingCandidates_DemandOutweighsSafety pins the weighting as a
// decision rather than an accident.
//
// A station where nothing sells cannot be redeemed by being safe and close, and a
// dangerous one where the militia shops still works -- so the demand terms have
// to be able to overturn every cost term combined. The converse is asserted too:
// at equal depth the safer, closer station wins, or the cost terms are decoration.
func TestScoreStagingCandidates_DemandOutweighsSafety(t *testing.T) {
	deepAndDangerous := FWStagingCandidate{
		StationID: 1, Security: 0.14, JumpsToFront: 0,
		JumpsFromSource: 12, LowsecJumpsFromSource: 5, HighsecRoute: false,
	}
	safeAndEmpty := FWStagingCandidate{
		StationID: 2, Security: 0.95, JumpsToFront: 0,
		JumpsFromSource: 3, LowsecJumpsFromSource: 0, HighsecRoute: true,
	}
	depth := map[int64]StagingDepth{
		1: {Orders: 600, Types: 450, Overlap: 200},
		2: {Orders: 4, Types: 4, Overlap: 0},
	}

	scored := ScoreStagingCandidates([]FWStagingCandidate{deepAndDangerous, safeAndEmpty}, depth, DefaultMaxJumpsFromFront)
	if scored[0].StationID != 1 {
		t.Errorf("first = station %d, want the deep lowsec station: safety cannot substitute for a market",
			scored[0].StationID)
	}

	// Same two stations, same book. Now safety is the only thing separating them.
	equal := map[int64]StagingDepth{
		1: {Orders: 600, Types: 450, Overlap: 200},
		2: {Orders: 600, Types: 450, Overlap: 200},
	}
	scored = ScoreStagingCandidates([]FWStagingCandidate{deepAndDangerous, safeAndEmpty}, equal, DefaultMaxJumpsFromFront)
	if scored[0].StationID != 2 {
		t.Errorf("first = station %d, want the highsec station at equal depth", scored[0].StationID)
	}
}

// TestScoreStagingCandidates_BulwarkBadgeIsNotAPromotion: the maintained constant
// pins a system into the list and badges it. It must not rank it.
//
// The constant is four rows I was told, and the ring is the discovery path for the
// staging systems nobody has named. If the badge added score, the ring would
// always agree with the constant and would stop being evidence of anything --
// including when CCP moves a bulwark and the constant goes stale.
func TestScoreStagingCandidates_BulwarkBadgeIsNotAPromotion(t *testing.T) {
	quietBulwark := FWStagingCandidate{
		StationID: 1, IsBulwark: true, Pinned: true, InRing: true,
		Security: 0.60, JumpsToFront: 1, HighsecRoute: true,
	}
	busyUnknown := FWStagingCandidate{
		StationID: 2,
		Security:  0.60, JumpsToFront: 1, HighsecRoute: true,
	}
	depth := map[int64]StagingDepth{
		1: {Orders: 20, Types: 18, Overlap: 3},
		2: {Orders: 900, Types: 700, Overlap: 300},
	}

	scored := ScoreStagingCandidates([]FWStagingCandidate{quietBulwark, busyUnknown}, depth, DefaultMaxJumpsFromFront)
	if scored[0].StationID != 2 {
		t.Errorf("first = station %d, want the unbadged station with the real book", scored[0].StationID)
	}
	if !scored[1].IsBulwark {
		t.Error("the bulwark must still be in the list, badged -- it is pinned, only not promoted")
	}
}

// TestRouteScore_UnmeasuredIsNotSafe: with no source hub configured every
// candidate gets the same route score, so the term drops out of the ranking
// instead of quietly rewarding a route nobody checked. An unreachable
// destination scores zero.
func TestRouteScore_UnmeasuredIsNotSafe(t *testing.T) {
	if got := routeScore(0, 0, false); got != 1 {
		t.Errorf("unmeasured route = %.2f, want 1 so the term cancels", got)
	}
	if got := routeScore(-1, 0, false); got != 0 {
		t.Errorf("unreachable route = %.2f, want 0", got)
	}
	if routeScore(20, 0, true) <= routeScore(4, 3, false) {
		t.Error("a long all-highsec haul must beat a short one through three lowsec systems")
	}
	if routeScore(4, 1, false) <= routeScore(4, 4, false) {
		t.Error("more lowsec on the route must score worse")
	}
}

// TestSecurityScore_NullsecIsNotHighsec pins the destination-safety term: highsec
// is flat 1.0 because 0.5 and 0.9 are equally protected, and it falls away below
// 0.45 where a hauler can be stopped without a CONCORD response.
func TestSecurityScore_NullsecIsNotHighsec(t *testing.T) {
	if securityScore(0.45) != 1 || securityScore(0.95) != 1 {
		t.Error("everything at 0.45 and above is highsec and scores the same")
	}
	if securityScore(-0.1) != 0 || securityScore(0) != 0 {
		t.Error("nullsec scores zero")
	}
	if !(securityScore(0.36) > securityScore(0.14)) {
		t.Error("0.36 is a safer place to keep stock than 0.14")
	}
}
