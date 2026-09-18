package esi

import (
	"encoding/json"
	"testing"
)

// fwSystemsFixture is eight verbatim rows from /fw/systems/, captured
// 2026-09-16. Nothing here is constructed: every id, owner, occupier and
// contested value is what the live endpoint returned, and the six keys are the
// only keys it returns.
//
// The rows are chosen to cover all eight owner/occupier combinations that exist
// in the live data, including the four flips -- Caldari-owned space held by
// Gallente and the reverse, Minmatar-owned held by Amarr and the reverse. Those
// flips are what the theatre scope has to survive.
const fwSystemsFixture = `[
  {"contested":"contested","occupier_faction_id":500001,"owner_faction_id":500001,"solar_system_id":30002756,"victory_points":26,"victory_points_threshold":75000},
  {"contested":"contested","occupier_faction_id":500004,"owner_faction_id":500001,"solar_system_id":30002807,"victory_points":37777,"victory_points_threshold":75000},
  {"contested":"contested","occupier_faction_id":500001,"owner_faction_id":500004,"solar_system_id":30003828,"victory_points":8845,"victory_points_threshold":75000},
  {"contested":"uncontested","occupier_faction_id":500004,"owner_faction_id":500004,"solar_system_id":30004999,"victory_points":0,"victory_points_threshold":75000},
  {"contested":"contested","occupier_faction_id":500002,"owner_faction_id":500002,"solar_system_id":30002062,"victory_points":43666,"victory_points_threshold":75000},
  {"contested":"contested","occupier_faction_id":500003,"owner_faction_id":500002,"solar_system_id":30002097,"victory_points":4200,"victory_points_threshold":75000},
  {"contested":"contested","occupier_faction_id":500002,"owner_faction_id":500003,"solar_system_id":30003068,"victory_points":31696,"victory_points_threshold":75000},
  {"contested":"uncontested","occupier_faction_id":500003,"owner_faction_id":500003,"solar_system_id":30002957,"victory_points":0,"victory_points_threshold":75000}
]`

// The Caldari/Gallente theatre rows in the fixture, and the Amarr/Minmatar ones.
var (
	fixtureCalGalSystems   = []int32{30002756, 30002807, 30003828, 30004999}
	fixtureAmarrMinSystems = []int32{30002062, 30002097, 30003068, 30002957}
)

func fwFixture(t *testing.T) []FWSystem {
	t.Helper()
	var systems []FWSystem
	if err := json.Unmarshal([]byte(fwSystemsFixture), &systems); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return systems
}

// TestFWSystem_Unmarshal is the field-name guard. Every one of these is a
// snake_case ESI name that a Go struct would not produce by default.
func TestFWSystem_Unmarshal(t *testing.T) {
	systems := fwFixture(t)
	if len(systems) != 8 {
		t.Fatalf("got %d systems, want 8", len(systems))
	}

	// 30002807: Caldari sovereignty, Gallente occupation, three quarters flipped.
	s := systems[1]
	if s.SolarSystemID != 30002807 {
		t.Errorf("SolarSystemID = %d, want 30002807", s.SolarSystemID)
	}
	if s.OwnerFactionID != MilitiaCaldari {
		t.Errorf("OwnerFactionID = %d, want %d", s.OwnerFactionID, MilitiaCaldari)
	}
	if s.OccupierFactionID != MilitiaGallente {
		t.Errorf("OccupierFactionID = %d, want %d -- owner and occupier must not be conflated", s.OccupierFactionID, MilitiaGallente)
	}
	if s.Contested != "contested" {
		t.Errorf("Contested = %q, want \"contested\"", s.Contested)
	}
	if s.VictoryPoints != 37777 || s.VictoryPointsThreshold != 75000 {
		t.Errorf("victory points = %d/%d, want 37777/75000", s.VictoryPoints, s.VictoryPointsThreshold)
	}
}

// TestOccupiedBy uses occupation, which is who is there now -- so a system a
// militia owns but has lost is not theirs.
func TestOccupiedBy(t *testing.T) {
	systems := fwFixture(t)

	cal := OccupiedBy(systems, MilitiaCaldari)
	if !cal[30002756] {
		t.Error("30002756 is Caldari-held and must be listed")
	}
	if !cal[30003828] {
		t.Error("30003828 is Gallente-owned but Caldari-held, so it is Caldari-occupied")
	}
	if cal[30002807] {
		t.Error("30002807 is Caldari-owned but Gallente-held -- occupation is not ownership")
	}
	if len(cal) != 2 {
		t.Errorf("Caldari-occupied = %v, want 2 systems", cal)
	}

	amarr := OccupiedBy(systems, MilitiaAmarr)
	if !amarr[30002097] || !amarr[30002957] || len(amarr) != 2 {
		t.Errorf("Amarr-occupied = %v, want 30002097 and 30002957", amarr)
	}
}

// TestWarzoneSystems_IncludesTheEnemyHalf is the measured scope decision: 72.3%
// of Caldari losses fall somewhere in the theatre against 32.3% in Caldari-held
// systems, so the enemy's half carries more than half the demand signal.
func TestWarzoneSystems_IncludesTheEnemyHalf(t *testing.T) {
	systems := fwFixture(t)

	calWarzone := WarzoneSystems(systems, MilitiaCaldari)
	for _, want := range fixtureCalGalSystems {
		if !calWarzone[want] {
			t.Errorf("system %d is in the Caldari/Gallente theatre and must be in scope", want)
		}
	}
	// The other theatre is someone else's demand: a Minmatar loss in Amarr space
	// restocks at a Minmatar hub, not a Caldari one.
	for _, unwanted := range fixtureAmarrMinSystems {
		if calWarzone[unwanted] {
			t.Errorf("system %d is Amarr/Minmatar and must not be in a Caldari scope", unwanted)
		}
	}
	if len(calWarzone) != len(fixtureCalGalSystems) {
		t.Errorf("Caldari warzone = %d systems, want %d", len(calWarzone), len(fixtureCalGalSystems))
	}

	// Specifically: the Gallente-held system is in scope even though no Caldari
	// pilot holds it. That is the row that doubles the signal.
	if !calWarzone[30002807] {
		t.Error("30002807 is Gallente-held Caldari space -- Caldari die there and restock at their own hub")
	}
}

// TestWarzoneSystems_TheatreFollowsSovereigntyNotOccupation: a flipped system
// stays in its own theatre, so the scope does not shrink when the front moves.
func TestWarzoneSystems_TheatreFollowsSovereigntyNotOccupation(t *testing.T) {
	systems := fwFixture(t)

	// 30002097 is Minmatar-owned and Amarr-occupied. It is in both those
	// militias' warzone and in neither Caldari's nor Gallente's.
	for _, militia := range []int32{MilitiaMinmatar, MilitiaAmarr} {
		if !WarzoneSystems(systems, militia)[30002097] {
			t.Errorf("militia %d: flipped system 30002097 must stay in the theatre", militia)
		}
	}
	for _, militia := range []int32{MilitiaCaldari, MilitiaGallente} {
		if WarzoneSystems(systems, militia)[30002097] {
			t.Errorf("militia %d: 30002097 is in the other theatre", militia)
		}
	}

	// Symmetry: both sides of a theatre see the same warzone, whoever is winning.
	cal := WarzoneSystems(systems, MilitiaCaldari)
	gal := WarzoneSystems(systems, MilitiaGallente)
	if len(cal) != len(gal) {
		t.Fatalf("Caldari scope %d systems, Gallente %d -- a theatre is one warzone", len(cal), len(gal))
	}
	for id := range cal {
		if !gal[id] {
			t.Errorf("system %d in the Caldari scope but not the Gallente one", id)
		}
	}
}

// TestWarzoneSystems_UnknownMilitia falls back to occupation rather than
// returning the whole warzone, because a scope that quietly widens to everything
// is the failure mode AnalyzeMilitiaDemand refuses to accept.
func TestWarzoneSystems_UnknownMilitia(t *testing.T) {
	got := WarzoneSystems(fwFixture(t), 500099)
	if len(got) != 0 {
		t.Errorf("unknown militia scope = %v, want empty -- never a silent widening", got)
	}
}

// TestBulwarkSystems pins the hand-maintained constant. ESI has no bulwark field,
// so a silent edit here would be invisible; these are the four verified ids.
func TestBulwarkSystems(t *testing.T) {
	want := map[int32]int32{
		MilitiaCaldari:  30045324, // Onnamon
		MilitiaMinmatar: 30002055, // Amo
		MilitiaAmarr:    30002974, // Mehatoor
		MilitiaGallente: 30003788, // Intaki
	}
	if len(BulwarkSystems) != len(want) {
		t.Fatalf("BulwarkSystems has %d entries, want %d", len(BulwarkSystems), len(want))
	}
	for militia, system := range want {
		if got := BulwarkSystems[militia]; got != system {
			t.Errorf("militia %d bulwark = %d, want %d", militia, got, system)
		}
	}
	for militia := range BulwarkSystems {
		if _, ok := opposingMilitia[militia]; !ok {
			t.Errorf("bulwark keyed on %d, which is not a militia", militia)
		}
	}
}

// TestBulwarkSystems_AreNeverInFWSystems is the reason the constant exists at
// all, and the reason the staging ring cannot be built from this endpoint.
//
// All four bulwarks are uncontested highsec, so /fw/systems/ never lists them --
// verified against the live endpoint, where all 160 rows exclude every one of
// them. Anything that only walks this list will never find the destination the
// militia actually buys at, which is why the ring walks the gate graph outward
// from the front instead.
func TestBulwarkSystems_AreNeverInFWSystems(t *testing.T) {
	listed := make(map[int32]bool)
	for _, s := range fwFixture(t) {
		listed[s.SolarSystemID] = true
	}

	for militia, bulwark := range BulwarkSystems {
		if listed[bulwark] {
			t.Errorf("militia %d bulwark %d appears in /fw/systems/: it is contested space, so either the constant or this assumption is wrong", militia, bulwark)
		}
		if WarzoneSystems(fwFixture(t), militia)[bulwark] {
			t.Errorf("militia %d bulwark %d is inside the warzone scope, so demand there would be double counted", militia, bulwark)
		}
	}
}
