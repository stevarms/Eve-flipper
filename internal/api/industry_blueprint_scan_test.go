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
			622:   {ID: 622, Name: "Stabber"},
			11999: {ID: 11999, Name: "Vagabond"},
			12005: {ID: 12005, Name: "Muninn"},
			587:   {ID: 587, Name: "Rifter"},
		},
		Industry: industry,
	}
}

func TestBuildScanWork_T1MfgOnlyWhenInventionDisabled(t *testing.T) {
	sdeData := buildTestSDE()
	groups := []blueprintGroup{
		{BlueprintTypeID: 641, BlueprintName: "Stabber Blueprint", IsBPO: true, OwnedQuantity: 1},
	}

	work, skipped := buildScanWork(groups, sdeData, false, 1.0)

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

	work, skipped := buildScanWork(groups, sdeData, true, 1.0)

	if skipped != 0 {
		t.Fatalf("expected 0 skipped, got %d", skipped)
	}
	// 1 T1 mfg + 2 T2 invention (Vagabond, Muninn) = 3 items
	if len(work) != 3 {
		t.Fatalf("expected 3 work items, got %d", len(work))
	}

	var t1Count, t2Count int
	inventionModules := map[int32]bool{}
	for _, w := range work {
		switch w.scanMode {
		case "t1_mfg":
			t1Count++
		case "t2_invention":
			t2Count++
			inventionModules[w.productTypeID] = true
			if w.sourceBlueprintID != 641 {
				t.Errorf("expected source BP=641, got %d", w.sourceBlueprintID)
			}
			if w.attemptsCap != -1 {
				t.Errorf("BPO source should be unlimited (cap=-1), got %d", w.attemptsCap)
			}
			// The T2 BPC name should fall back to "<T2 module name> Blueprint"
			// when the invented BPC typeID isn't in Types (which is the norm
			// for invented BPCs). Vagabond BPC (1032) → "Vagabond Blueprint".
			if w.outputBlueprintID == 0 || w.outputBlueprintName == "" {
				t.Errorf("expected output BPC name populated, got id=%d name=%q", w.outputBlueprintID, w.outputBlueprintName)
			}
		}
	}
	if t1Count != 1 || t2Count != 2 {
		t.Fatalf("expected 1 T1 + 2 T2, got %d T1 + %d T2", t1Count, t2Count)
	}
	if !inventionModules[11999] || !inventionModules[12005] {
		t.Fatalf("expected T2 modules 11999 and 12005, got %v", inventionModules)
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

	work, _ := buildScanWork(groups, sdeData, true, 1.0)

	for _, w := range work {
		if w.scanMode == "t2_invention" && w.attemptsCap != 30 {
			t.Fatalf("expected cap=30 for BPC source, got %d", w.attemptsCap)
		}
	}
}

func TestBuildScanWork_ChanceMultiplierApplied(t *testing.T) {
	sdeData := buildTestSDE()
	groups := []blueprintGroup{
		{BlueprintTypeID: 641, BlueprintName: "Stabber Blueprint", IsBPO: true},
	}

	work, _ := buildScanWork(groups, sdeData, true, 2.0)

	for _, w := range work {
		if w.scanMode != "t2_invention" {
			continue
		}
		// Vagabond base 0.30 × 2.0 = 0.60; Muninn base 0.25 × 2.0 = 0.50.
		var wantChance float64
		switch w.productTypeID {
		case 11999:
			wantChance = 0.60
		case 12005:
			wantChance = 0.50
		default:
			t.Errorf("unexpected T2 product %d", w.productTypeID)
			continue
		}
		if diff := w.effectiveProbability - wantChance; diff > 0.0001 || diff < -0.0001 {
			t.Errorf("product %d: expected chance ~%.4f, got %.4f", w.productTypeID, wantChance, w.effectiveProbability)
		}
	}
}

func TestBuildScanWork_ChanceClampedAtOne(t *testing.T) {
	sdeData := buildTestSDE()
	groups := []blueprintGroup{
		{BlueprintTypeID: 641, BlueprintName: "Stabber Blueprint", IsBPO: true},
	}

	work, _ := buildScanWork(groups, sdeData, true, 100.0)

	for _, w := range work {
		if w.scanMode == "t2_invention" && w.effectiveProbability > 1.0 {
			t.Fatalf("chance should be clamped at 1.0, got %.4f", w.effectiveProbability)
		}
	}
}

func TestBuildScanWork_SkipsGroupsWithNoActivities(t *testing.T) {
	sdeData := buildTestSDE()
	groups := []blueprintGroup{
		{BlueprintTypeID: 9999, BlueprintName: "Broken BP", IsBPO: true},
	}

	work, skipped := buildScanWork(groups, sdeData, true, 1.0)

	if len(work) != 0 {
		t.Fatalf("expected no work items, got %d", len(work))
	}
	if skipped != 1 {
		t.Fatalf("expected 1 skipped, got %d", skipped)
	}
}

func TestBuildScanWork_T1WithoutInventionActivityStillEmitsMfg(t *testing.T) {
	sdeData := buildTestSDE()
	groups := []blueprintGroup{
		{BlueprintTypeID: 690, BlueprintName: "Rifter Blueprint", IsBPO: true},
	}

	work, skipped := buildScanWork(groups, sdeData, true, 1.0)

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
