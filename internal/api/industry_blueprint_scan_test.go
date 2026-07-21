package api

import (
	"reflect"
	"testing"

	"eve-flipper/internal/db"
	"eve-flipper/internal/sde"
)

func TestGroupBlueprintsByType_MergesAcrossLocations(t *testing.T) {
	rows := []db.IndustryBlueprintPoolInput{
		{BlueprintTypeID: 641, BlueprintName: "Stabber Blueprint", LocationID: 60003760, Quantity: 1, ME: 8, TE: 14, IsBPO: true, AvailableRuns: 0},
		{BlueprintTypeID: 641, BlueprintName: "Stabber Blueprint", LocationID: 60008494, Quantity: 1, ME: 10, TE: 20, IsBPO: true, AvailableRuns: 0},
		{BlueprintTypeID: 641, BlueprintName: "Stabber Blueprint", LocationID: 60003760, Quantity: 5, ME: 4, TE: 8, IsBPO: false, AvailableRuns: 50},
		{BlueprintTypeID: 690, BlueprintName: "Rifter Blueprint", LocationID: 60003760, Quantity: 1, ME: 10, TE: 20, IsBPO: true},
	}

	got := groupBlueprintsByType(rows)
	if len(got) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(got))
	}

	// groupBlueprintsByType sorts by name asc, then BPO before BPC within the same name.
	// Expected order: Rifter BPO, Stabber BPO, Stabber BPC.
	rifter := got[0]
	if rifter.BlueprintTypeID != 690 || !rifter.IsBPO || rifter.OwnedQuantity != 1 {
		t.Fatalf("rifter group wrong: %+v", rifter)
	}

	stabberBPO := got[1]
	if !stabberBPO.IsBPO {
		t.Fatalf("expected first stabber group to be BPO: %+v", stabberBPO)
	}

	stabberBPC := got[2]
	if stabberBPC.IsBPO {
		t.Fatalf("expected second stabber group to be BPC: %+v", stabberBPC)
	}
	if stabberBPC.OwnedQuantity != 5 || stabberBPC.AvailableRuns != 50 {
		t.Fatalf("stabber BPC qty/runs wrong: %+v", stabberBPC)
	}
	if stabberBPO.OwnedQuantity != 2 {
		t.Fatalf("stabber BPO qty merge wrong: %+v", stabberBPO)
	}
	if stabberBPO.ME != 10 || stabberBPO.TE != 20 {
		t.Fatalf("stabber BPO ME/TE should pick best across copies, got ME=%d TE=%d", stabberBPO.ME, stabberBPO.TE)
	}
	if stabberBPO.AvailableRuns != 0 {
		t.Fatalf("BPO available runs must stay 0, got %d", stabberBPO.AvailableRuns)
	}
	wantLocations := []int64{60003760, 60008494}
	if !reflect.DeepEqual(stabberBPO.LocationIDs, wantLocations) {
		t.Fatalf("stabber BPO locations: got %v, want %v", stabberBPO.LocationIDs, wantLocations)
	}
}

func TestGroupBlueprintsByType_EmptyInput(t *testing.T) {
	got := groupBlueprintsByType(nil)
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %d entries", len(got))
	}
}

// buildTestSDE constructs a small SDE fixture with:
//   - typeID 641 "Stabber Blueprint" (T1 BP): manufacturing → 622 "Stabber",
//     invention → two T2 BPCs: 1032 "Vagabond Blueprint" (chance 0.30) and
//     1033 "Muninn Blueprint" (chance 0.25).
//   - typeID 1032 "Vagabond Blueprint" (T2 BP): manufacturing → 11999 "Vagabond".
//   - typeID 1033 "Muninn Blueprint" (T2 BP): manufacturing → 12005 "Muninn".
//   - typeID 690 "Rifter Blueprint" (T1 BP, no invention): mfg → 587 "Rifter".
//   - typeID 9999 "Broken BP" (no activities at all).
func buildTestSDE() *sde.Data {
	industry := sde.NewIndustryData()
	industry.Blueprints[641] = &sde.Blueprint{
		BlueprintTypeID: 641,
		Activities: map[string]*sde.ActivityData{
			"manufacturing": {Products: []sde.BlueprintProduct{{TypeID: 622, Quantity: 1}}},
			"invention": {Products: []sde.BlueprintProduct{
				{TypeID: 1032, Quantity: 10, Probability: 0.30},
				{TypeID: 1033, Quantity: 10, Probability: 0.25},
			}},
		},
	}
	industry.Blueprints[1032] = &sde.Blueprint{
		BlueprintTypeID: 1032,
		Activities: map[string]*sde.ActivityData{
			"manufacturing": {Products: []sde.BlueprintProduct{{TypeID: 11999, Quantity: 1}}},
		},
	}
	industry.Blueprints[1033] = &sde.Blueprint{
		BlueprintTypeID: 1033,
		Activities: map[string]*sde.ActivityData{
			"manufacturing": {Products: []sde.BlueprintProduct{{TypeID: 12005, Quantity: 1}}},
		},
	}
	industry.Blueprints[690] = &sde.Blueprint{
		BlueprintTypeID: 690,
		Activities: map[string]*sde.ActivityData{
			"manufacturing": {Products: []sde.BlueprintProduct{{TypeID: 587, Quantity: 1}}},
		},
	}
	industry.Blueprints[9999] = &sde.Blueprint{
		BlueprintTypeID: 9999,
		Activities:      map[string]*sde.ActivityData{},
	}

	return &sde.Data{
		Types: map[int32]*sde.ItemType{
			622: {ID: 622, Name: "Stabber", CategoryID: 6},
			// Vagabond marked T2, Muninn marked T3 so the tier-classification
			// tests can assert the split path per output within a single BP.
			11999: {ID: 11999, Name: "Vagabond", CategoryID: 6, MetaGroupID: sde.MetaGroupT2},
			12005: {ID: 12005, Name: "Muninn", CategoryID: 6, MetaGroupID: sde.MetaGroupT3},
			587:   {ID: 587, Name: "Rifter", CategoryID: 6},
		},
		Industry: industry,
	}
}

func TestBuildScanWork_ClassifiesT2VsT3ByMetaGroup(t *testing.T) {
	sdeData := buildTestSDE()
	groups := []blueprintGroup{
		{BlueprintTypeID: 641, BlueprintName: "Stabber Blueprint", IsBPO: true, OwnedQuantity: 1},
	}

	// T2 only: should include Vagabond (metaGroup 2), exclude Muninn (14).
	t2Only, _ := buildScanWork(groups, sdeData, true, false)
	var seenT2Product, seenT3Product bool
	for _, w := range t2Only {
		if w.scanMode == "t2_invention" && w.productTypeID == 11999 {
			seenT2Product = true
		}
		if w.scanMode == "t3_invention" {
			seenT3Product = true
		}
	}
	if !seenT2Product {
		t.Fatalf("T2-only scan missing Vagabond invention row: %+v", t2Only)
	}
	if seenT3Product {
		t.Fatalf("T2-only scan should NOT emit T3 rows: %+v", t2Only)
	}

	// T3 only: should include Muninn, exclude Vagabond.
	t3Only, _ := buildScanWork(groups, sdeData, false, true)
	seenT2Product, seenT3Product = false, false
	for _, w := range t3Only {
		if w.scanMode == "t2_invention" {
			seenT2Product = true
		}
		if w.scanMode == "t3_invention" && w.productTypeID == 12005 {
			seenT3Product = true
		}
	}
	if seenT2Product {
		t.Fatalf("T3-only scan should NOT emit T2 rows: %+v", t3Only)
	}
	if !seenT3Product {
		t.Fatalf("T3-only scan missing Muninn invention row: %+v", t3Only)
	}
}

func TestBuildScanWork_T1MfgOnlyWhenInventionDisabled(t *testing.T) {
	sdeData := buildTestSDE()
	groups := []blueprintGroup{
		{BlueprintTypeID: 641, BlueprintName: "Stabber Blueprint", IsBPO: true, OwnedQuantity: 1},
	}

	work, skipped := buildScanWork(groups, sdeData, false, false)

	if skipped != 0 {
		t.Fatalf("expected 0 skipped, got %d", skipped)
	}
	if len(work) != 1 {
		t.Fatalf("expected 1 work item (T1 mfg only), got %d", len(work))
	}
	if work[0].scanMode != "t1_mfg" {
		t.Fatalf("expected scanMode=t1_mfg, got %q", work[0].scanMode)
	}
	if work[0].productTypeID != 622 {
		t.Fatalf("expected productTypeID=622 (Stabber), got %d", work[0].productTypeID)
	}
}

func TestBuildScanWork_T2InventionFanoutEmitsPerProduct(t *testing.T) {
	sdeData := buildTestSDE()
	groups := []blueprintGroup{
		{BlueprintTypeID: 641, BlueprintName: "Stabber Blueprint", IsBPO: true, OwnedQuantity: 1},
	}

	// Both T2 + T3 on so both Vagabond (metaGroup 2) and Muninn (metaGroup 14)
	// fan out — the fixture now marks these different tiers so tier-classification
	// tests can distinguish them.
	work, skipped := buildScanWork(groups, sdeData, true, true)

	if skipped != 0 {
		t.Fatalf("expected 0 skipped, got %d", skipped)
	}
	// 1 T1 mfg + 1 T2 invention (Vagabond) + 1 T3 invention (Muninn) = 3 items
	if len(work) != 3 {
		t.Fatalf("expected 3 work items, got %d", len(work))
	}

	var t1Count, invCount int
	inventionModules := map[int32]bool{}
	for _, w := range work {
		switch w.scanMode {
		case "t1_mfg":
			t1Count++
		case "t2_invention", "t3_invention":
			invCount++
			inventionModules[w.productTypeID] = true
			if w.sourceBlueprintID != 641 {
				t.Errorf("expected source BP=641, got %d", w.sourceBlueprintID)
			}
			if w.attemptsCap != -1 {
				t.Errorf("BPO source should be unlimited (cap=-1), got %d", w.attemptsCap)
			}
			// The invented BPC name should fall back to "<module name> Blueprint"
			// when the BPC typeID isn't in Types (the norm for invented BPCs).
			if w.outputBlueprintID == 0 || w.outputBlueprintName == "" {
				t.Errorf("expected output BPC name populated, got id=%d name=%q", w.outputBlueprintID, w.outputBlueprintName)
			}
		}
	}
	if t1Count != 1 || invCount != 2 {
		t.Fatalf("expected 1 T1 + 2 invention, got %d T1 + %d inv", t1Count, invCount)
	}
	if !inventionModules[11999] || !inventionModules[12005] {
		t.Fatalf("expected invention modules 11999 and 12005, got %v", inventionModules)
	}
}

func TestBlueprintDisplayName_FallbackFromProductName(t *testing.T) {
	sdeData := buildTestSDE()
	// T2 BPC 1032 is NOT in sdeData.Types (mirrors real SDE — invented BPCs
	// have no market group). Its mfg product Vagabond (11999) IS in Types.
	// Expect the fallback to compose "Vagabond Blueprint".
	got := blueprintDisplayName(1032, sdeData)
	if got != "Vagabond Blueprint" {
		t.Fatalf("expected fallback \"Vagabond Blueprint\", got %q", got)
	}

	// A T1 BP with Types entry uses the Types name directly. Our fixture has
	// no Types entry for 690 (Rifter Blueprint), so it should fall back to
	// "Rifter Blueprint" via the same mfg-product path.
	if got := blueprintDisplayName(690, sdeData); got != "Rifter Blueprint" {
		t.Fatalf("expected \"Rifter Blueprint\", got %q", got)
	}

	// Unknown typeID → "Type N".
	if got := blueprintDisplayName(999999, sdeData); got != "Type 999999" {
		t.Fatalf("expected \"Type 999999\", got %q", got)
	}
}

func TestBuildScanWork_BPCSourceHasFiniteAttemptsCap(t *testing.T) {
	sdeData := buildTestSDE()
	groups := []blueprintGroup{
		{BlueprintTypeID: 641, BlueprintName: "Stabber Blueprint", IsBPO: false, OwnedQuantity: 3, AvailableRuns: 30},
	}

	work, _ := buildScanWork(groups, sdeData, true, false)

	for _, w := range work {
		if w.scanMode == "t2_invention" && w.attemptsCap != 30 {
			t.Fatalf("expected cap=30 for BPC source, got %d", w.attemptsCap)
		}
	}
}

func TestBuildScanWork_StoresBaseProbability(t *testing.T) {
	// The T2 worker loops through decryptors and applies each one's chance
	// multiplier itself, so the fanout just needs to record the per-product
	// SDE base probability verbatim.
	sdeData := buildTestSDE()
	groups := []blueprintGroup{
		{BlueprintTypeID: 641, BlueprintName: "Stabber Blueprint", IsBPO: true},
	}

	work, _ := buildScanWork(groups, sdeData, true, false)

	for _, w := range work {
		if w.scanMode != "t2_invention" {
			continue
		}
		var wantBase float64
		switch w.productTypeID {
		case 11999:
			wantBase = 0.30
		case 12005:
			wantBase = 0.25
		default:
			t.Errorf("unexpected T2 product %d", w.productTypeID)
			continue
		}
		if diff := w.baseProbability - wantBase; diff > 0.0001 || diff < -0.0001 {
			t.Errorf("product %d: expected base ~%.4f, got %.4f", w.productTypeID, wantBase, w.baseProbability)
		}
	}
}

func TestBuildScanWork_SkipsGroupsWithNoActivities(t *testing.T) {
	sdeData := buildTestSDE()
	groups := []blueprintGroup{
		{BlueprintTypeID: 9999, BlueprintName: "Broken BP", IsBPO: true},
	}

	work, skipped := buildScanWork(groups, sdeData, true, false)

	if len(work) != 0 {
		t.Fatalf("expected no work items, got %d", len(work))
	}
	if skipped != 1 {
		t.Fatalf("expected 1 skipped, got %d", skipped)
	}
}

func TestSynthesizeUnownedGroups_SkipsAlreadyOwned(t *testing.T) {
	sdeData := buildTestSDE()
	owned := []blueprintGroup{
		{BlueprintTypeID: 641, BlueprintName: "Stabber Blueprint", IsBPO: true, Owned: true},
	}
	unowned := synthesizeUnownedGroups(sdeData, owned, 10, 20, false)

	for _, g := range unowned {
		if g.BlueprintTypeID == 641 {
			t.Fatalf("unowned list contained already-owned 641")
		}
		if !g.IsBPO {
			t.Errorf("synth group %d not marked BPO", g.BlueprintTypeID)
		}
		if g.Owned {
			t.Errorf("synth group %d incorrectly marked Owned=true", g.BlueprintTypeID)
		}
		if g.ME != 10 || g.TE != 20 {
			t.Errorf("synth group %d ME/TE = %d/%d, want 10/20", g.BlueprintTypeID, g.ME, g.TE)
		}
	}
	// Rifter (690) has a manufacturing activity + product 587 with a name,
	// so it must appear.
	found := false
	for _, g := range unowned {
		if g.BlueprintTypeID == 690 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Rifter Blueprint 690 in synth output, got %+v", unowned)
	}
}

// A relic-style BP (invention-only, no manufacturing activity) whose
// invention product's mfg product IS marketable should be included in
// the unowned synthesis when includeInvention=true, and excluded when
// includeInvention=false. This mirrors the T3 destroyer "Hull Section"
// path: the user rarely owns hull-section BPs but the scanner should
// still surface Jackdaw etc. via the discovery flow.
func TestSynthesizeUnownedGroups_InventionOnlyBPIncludedWhenFlagged(t *testing.T) {
	sdeData := buildTestSDE()
	// Fixture "relic" BP 12345: no manufacturing activity, invention
	// activity produces T2 BPC 1032 (Vagabond) — whose mfg product
	// Vagabond (11999) IS in Types.
	sdeData.Industry.Blueprints[12345] = &sde.Blueprint{
		BlueprintTypeID: 12345,
		Activities: map[string]*sde.ActivityData{
			"invention": {Products: []sde.BlueprintProduct{
				{TypeID: 1032, Quantity: 10, Probability: 0.30},
			}},
		},
	}

	// Without the flag: silently dropped (baseline behavior).
	off := synthesizeUnownedGroups(sdeData, nil, 10, 20, false)
	for _, g := range off {
		if g.BlueprintTypeID == 12345 {
			t.Fatalf("relic BP 12345 must NOT be in unowned synth when includeInvention=false")
		}
	}

	// With the flag: included.
	on := synthesizeUnownedGroups(sdeData, nil, 10, 20, true)
	found := false
	for _, g := range on {
		if g.BlueprintTypeID == 12345 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("relic BP 12345 must be in unowned synth when includeInvention=true; got %+v", on)
	}
}

func TestSynthesizeUnownedGroups_SkipsUnmarketableProducts(t *testing.T) {
	// Product 622 (Stabber) is in Types; product for BP 9999 is not. Only
	// BPs whose product has a Types entry should be included.
	sdeData := buildTestSDE()
	// Add a BP whose product is NOT in sdeData.Types.
	sdeData.Industry.Blueprints[7777] = &sde.Blueprint{
		BlueprintTypeID: 7777,
		Activities: map[string]*sde.ActivityData{
			"manufacturing": {Products: []sde.BlueprintProduct{{TypeID: 88888, Quantity: 1}}},
		},
	}

	unowned := synthesizeUnownedGroups(sdeData, nil, 10, 20, false)
	for _, g := range unowned {
		if g.BlueprintTypeID == 7777 {
			t.Fatalf("synth list contained BP 7777 whose product has no Types entry")
		}
	}
}

func TestBuildScanWork_T1WithoutInventionActivityStillEmitsMfg(t *testing.T) {
	sdeData := buildTestSDE()
	groups := []blueprintGroup{
		{BlueprintTypeID: 690, BlueprintName: "Rifter Blueprint", IsBPO: true},
	}

	work, skipped := buildScanWork(groups, sdeData, true, false)

	if skipped != 0 {
		t.Fatalf("expected 0 skipped, got %d", skipped)
	}
	if len(work) != 1 || work[0].scanMode != "t1_mfg" {
		t.Fatalf("expected 1 t1_mfg item, got %+v", work)
	}
}

func TestProfitableScanRowPassesFilters(t *testing.T) {
	row := profitableScanRow{
		Profit:        500_000,
		ProfitPercent: 12,
		ISKPerHour:    25_000_000,
	}

	cases := []struct {
		name string
		req  profitableScanRequest
		want bool
	}{
		{"no filters", profitableScanRequest{}, true},
		{"isk/h ok", profitableScanRequest{MinISKPerHour: 10_000_000}, true},
		{"isk/h fail", profitableScanRequest{MinISKPerHour: 50_000_000}, false},
		{"profit fail", profitableScanRequest{MinProfit: 1_000_000}, false},
		{"margin fail", profitableScanRequest{MinMarginPct: 25}, false},
		{"all pass", profitableScanRequest{MinISKPerHour: 1, MinProfit: 1, MinMarginPct: 1}, true},
		{"only one fails", profitableScanRequest{MinISKPerHour: 1, MinProfit: 1, MinMarginPct: 50}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := profitableScanRowPassesFilters(row, tc.req); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
