package api

import (
	"encoding/json"
	"testing"

	"eve-flipper/internal/db"
	"eve-flipper/internal/engine"
	"eve-flipper/internal/esi"
)

// fw_campaign_test.go -- the two rules in the campaign HTTP layer that are wrong
// in a way a screenshot cannot show.
//
// A PATCH that silently zeroes a field the body never mentioned still returns
// 200, still renders a plausible campaign, and only shows up later as headroom
// that does not match what was bought. Same for a bulwark seed that overrides an
// exclusion, and for a cached plan that survives the change of destination it was
// measured against. All three are asserted here rather than left to be noticed.

// patchRaw is the shape handleFWCampaignUpdate decodes before merging, so a test
// can exercise the merge without a request or a store.
func patchRaw(t *testing.T, body string) map[string]json.RawMessage {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("bad test fixture %q: %v", body, err)
	}
	return raw
}

// storedCampaign is a campaign as it comes back from the store: every parameter
// set, both lists populated, both owners named.
func storedCampaign() *db.FWCampaign {
	return &db.FWCampaign{
		CampaignID:        7,
		UserID:            "u1",
		Name:              "Caldari staging",
		MilitiaFactionID:  esi.MilitiaCaldari,
		SourceStationID:   60003760,
		DestStationID:     60015070,
		BudgetISK:         1_500_000_000,
		TargetCoverDays:   7,
		CoveredMultiple:   2,
		MinMarginPct:      10,
		FreightISKPerM3:   400,
		StepOverDaysCover: 0.5,
		CategoryCeilings:  map[string]float64{"ship": 1.30, "ammo": 2.00},
		ShipProfile:       "deep_space_transport",
		MaxJumpsFromFront: 2,
		PinnedSystems:     []int32{30045324},
		ExcludedSystems:   []int32{30045352},
		BuyerCharacterID:  91_000_001,
		SellerOwnerKind:   orderOwnerKindCorporation,
		SellerOwnerID:     98_000_001,
	}
}

// TestMergeFWCampaignPatchLeavesUnstatedFieldsAlone is the settings-panel guard.
// A panel that PATCHes the one field the user touched must not reset the rest,
// and a struct decoded into rather than over would zero all of them.
func TestMergeFWCampaignPatchLeavesUnstatedFieldsAlone(t *testing.T) {
	c := storedCampaign()
	before := *c

	if err := mergeFWCampaignPatch(c, patchRaw(t, `{"budget_isk": 2000000000}`)); err != nil {
		t.Fatalf("merge: %v", err)
	}

	if c.BudgetISK != 2_000_000_000 {
		t.Errorf("budget not applied: got %v", c.BudgetISK)
	}
	if c.TargetCoverDays != before.TargetCoverDays {
		t.Errorf("cover target zeroed by an unrelated patch: got %v want %v",
			c.TargetCoverDays, before.TargetCoverDays)
	}
	if c.MinMarginPct != before.MinMarginPct || c.StepOverDaysCover != before.StepOverDaysCover {
		t.Errorf("pricing parameters lost: margin %v step %v", c.MinMarginPct, c.StepOverDaysCover)
	}
	if c.DestStationID != before.DestStationID || c.SourceStationID != before.SourceStationID {
		t.Errorf("stations lost: dest %d source %d", c.DestStationID, c.SourceStationID)
	}
	if len(c.PinnedSystems) != 1 || c.PinnedSystems[0] != 30045324 {
		t.Errorf("pins lost: %v", c.PinnedSystems)
	}
	if len(c.ExcludedSystems) != 1 {
		t.Errorf("exclusions lost: %v", c.ExcludedSystems)
	}
	// The ownership pair is the one a settings panel is least likely to resend and
	// the one whose loss is hardest to spot: the campaign would keep planning, just
	// for nobody.
	if c.BuyerCharacterID != before.BuyerCharacterID ||
		c.SellerOwnerKind != before.SellerOwnerKind ||
		c.SellerOwnerID != before.SellerOwnerID {
		t.Errorf("ownership lost: buyer %d seller %s/%d",
			c.BuyerCharacterID, c.SellerOwnerKind, c.SellerOwnerID)
	}
	if c.Name != before.Name || c.ShipProfile != before.ShipProfile {
		t.Errorf("strings lost: name %q profile %q", c.Name, c.ShipProfile)
	}
}

// TestMergeFWCampaignPatchReplacesCeilings pins the map trap. json.Unmarshal
// merges into an existing map, so without the explicit clear a ceiling could be
// raised and never lowered again -- "set my ceilings to this" would mean "add
// these to whatever is already there".
func TestMergeFWCampaignPatchReplacesCeilings(t *testing.T) {
	c := storedCampaign()

	if err := mergeFWCampaignPatch(c, patchRaw(t, `{"category_ceilings": {"module": 1.45}}`)); err != nil {
		t.Fatalf("merge: %v", err)
	}

	if len(c.CategoryCeilings) != 1 {
		t.Fatalf("ceilings merged instead of replaced: %v", c.CategoryCeilings)
	}
	if got := c.CategoryCeilings["module"]; got != 1.45 {
		t.Errorf("module ceiling: got %v want 1.45", got)
	}
	if _, still := c.CategoryCeilings["ship"]; still {
		t.Error("ship ceiling survived a stated replacement, so it can never be removed")
	}
}

// TestMergeFWCampaignPatchKeepsCeilingsWhenUnstated is the other half: clearing
// the map must be triggered by the key being present, not by the merge running.
func TestMergeFWCampaignPatchKeepsCeilingsWhenUnstated(t *testing.T) {
	c := storedCampaign()
	if err := mergeFWCampaignPatch(c, patchRaw(t, `{"budget_isk": 1}`)); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if len(c.CategoryCeilings) != 2 {
		t.Errorf("ceilings dropped by an unrelated patch: %v", c.CategoryCeilings)
	}
}

// TestMergeFWCampaignPatchEmptyListClearsIt asserts a stated empty list means
// empty. Slices are replaced by encoding/json, which is the right reading: a user
// removing their last pin must be able to.
func TestMergeFWCampaignPatchEmptyListClearsIt(t *testing.T) {
	c := storedCampaign()
	if err := mergeFWCampaignPatch(c, patchRaw(t, `{"pinned_systems": []}`)); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if len(c.PinnedSystems) != 0 {
		t.Errorf("stated empty pin list ignored: %v", c.PinnedSystems)
	}
}

// TestMergeFWCampaignPatchRejectsGarbage: a bad body must fail loudly rather than
// half-apply. The merge is the only place that can tell, because by the time the
// handler writes, the campaign has already been mutated.
func TestMergeFWCampaignPatchRejectsGarbage(t *testing.T) {
	c := storedCampaign()
	err := mergeFWCampaignPatch(c, patchRaw(t, `{"budget_isk": "lots"}`))
	if err == nil {
		t.Fatal("a string budget was accepted")
	}
	if c.BudgetISK != 1_500_000_000 {
		t.Errorf("budget mutated by a rejected patch: %v", c.BudgetISK)
	}
}

// TestApplyFWCampaignDefaultsSeedsBulwark is verification step 6's precondition:
// a new Caldari campaign opens with Onnamon pinned, without the user knowing the
// system id.
func TestApplyFWCampaignDefaultsSeedsBulwark(t *testing.T) {
	c := &db.FWCampaign{MilitiaFactionID: esi.MilitiaCaldari}
	applyFWCampaignDefaults(c)

	if !containsInt32(c.PinnedSystems, 30045324) {
		t.Errorf("Onnamon not seeded for Caldari: %v", c.PinnedSystems)
	}
	if c.TargetCoverDays != engine.DefaultTargetCoverDays {
		t.Errorf("cover target: got %v want %v", c.TargetCoverDays, engine.DefaultTargetCoverDays)
	}
	if c.MaxJumpsFromFront != engine.DefaultMaxJumpsFromFront {
		t.Errorf("radius: got %d want %d", c.MaxJumpsFromFront, engine.DefaultMaxJumpsFromFront)
	}
	if len(c.CategoryCeilings) == 0 {
		t.Error("ceilings left empty, so the settings panel would show nothing to disagree with")
	}
	// Every militia has one, and a campaign that silently got no pin would just
	// look like a thin ring.
	for militia, system := range esi.BulwarkSystems {
		m := &db.FWCampaign{MilitiaFactionID: militia}
		applyFWCampaignDefaults(m)
		if !containsInt32(m.PinnedSystems, system) {
			t.Errorf("militia %d: bulwark %d not seeded (%v)", militia, system, m.PinnedSystems)
		}
	}
}

// TestApplyFWCampaignDefaultsRespectsExclusion is the rule that makes the
// constant advisory. A bulwark the user has written off must stay written off,
// including across every later save -- which is why the seed checks the exclude
// list and why the pin lives on the campaign rather than being read from the
// constant at plan time.
func TestApplyFWCampaignDefaultsRespectsExclusion(t *testing.T) {
	c := &db.FWCampaign{
		MilitiaFactionID: esi.MilitiaCaldari,
		ExcludedSystems:  []int32{30045324},
	}
	applyFWCampaignDefaults(c)

	if containsInt32(c.PinnedSystems, 30045324) {
		t.Error("an excluded bulwark was re-pinned, so excluding it can never stick")
	}
}

// TestApplyFWCampaignDefaultsDoesNotDuplicateOrOverride: the defaults run on
// every save, so they must be idempotent and must never overwrite a number the
// user chose.
func TestApplyFWCampaignDefaultsDoesNotDuplicateOrOverride(t *testing.T) {
	c := &db.FWCampaign{
		MilitiaFactionID: esi.MilitiaCaldari,
		TargetCoverDays:  14,
		MinMarginPct:     25,
		CategoryCeilings: map[string]float64{"ship": 1.10},
	}
	applyFWCampaignDefaults(c)
	applyFWCampaignDefaults(c)
	applyFWCampaignDefaults(c)

	pins := 0
	for _, s := range c.PinnedSystems {
		if s == 30045324 {
			pins++
		}
	}
	if pins != 1 {
		t.Errorf("bulwark pinned %d times", pins)
	}
	if c.TargetCoverDays != 14 || c.MinMarginPct != 25 {
		t.Errorf("user parameters overwritten: cover %v margin %v", c.TargetCoverDays, c.MinMarginPct)
	}
	if len(c.CategoryCeilings) != 1 || c.CategoryCeilings["ship"] != 1.10 {
		t.Errorf("user ceilings replaced by defaults: %v", c.CategoryCeilings)
	}
}

// TestFWPlanPremiseChanged pins which edits invalidate the cached gap table.
//
// The cost of being wrong runs both ways, which is why this is a list and not a
// blanket drop: too narrow and the table describes a station the campaign has
// left, too wide and every budget keystroke throws away a full re-plan.
func TestFWPlanPremiseChanged(t *testing.T) {
	cases := []struct {
		body string
		want bool
		why  string
	}{
		{`{"dest_station_id": 60015108}`, true,
			"every stocked quantity and price in the table was measured at the old station"},
		{`{"militia_faction_id": 500004}`, true, "a different warzone is being destroyed"},
		{`{"source_station_id": 60008494}`, true, "landed cost changes, so every floor does"},
		{`{"target_cover_days": 14}`, true, "the verdicts are computed against it"},
		{`{"min_margin_pct": 20}`, true, "the floor moves, so unpriceable rows change"},
		{`{"category_ceilings": {"ship": 1.2}}`, true, "the reference prices are clamped by it"},
		{`{"covered_multiple": 3}`, true, "the covered/thin boundary moves"},
		{`{"step_over_days_cover": 1}`, true, "which competitors get stepped over changes"},
		{`{"freight_isk_per_m3": 500}`, true, "freight is part of landed cost"},

		{`{"budget_isk": 3000000000}`, false,
			"the shipping list re-trims from the same gap table, so nothing is stale"},
		{`{"name": "renamed"}`, false, "a label"},
		{`{"pinned_systems": [30045324, 30045352]}`, false,
			"the ring is re-ranked on demand; the gap table is about the chosen station"},
		{`{"buyer_character_id": 91000002}`, false, "who buys does not change what is missing"},
		{`{"ship_profile": "blockade_runner"}`, false,
			"trips and m3 are presentation over the same table"},
		{`{}`, false, "nothing stated"},
	}
	for _, c := range cases {
		if got := fwPlanPremiseChanged(patchRaw(t, c.body)); got != c.want {
			t.Errorf("%s -> %v, want %v (%s)", c.body, got, c.want, c.why)
		}
	}
}

// TestFWDefaultCampaignNameCoversEveryMilitia: an unnamed campaign must still be
// distinguishable in a list, for every militia and for an unknown one.
func TestFWDefaultCampaignNameCoversEveryMilitia(t *testing.T) {
	seen := map[string]bool{}
	for militia := range esi.BulwarkSystems {
		name := fwDefaultCampaignName(militia)
		if name == "" {
			t.Errorf("militia %d has no default name", militia)
		}
		if seen[name] {
			t.Errorf("militia %d reuses the name %q", militia, name)
		}
		seen[name] = true
	}
	if fwDefaultCampaignName(0) == "" {
		t.Error("an unknown militia must still get a name rather than an empty row")
	}
}
