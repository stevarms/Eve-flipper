package engine

import (
	"testing"

	"eve-flipper/internal/esi"
	"eve-flipper/internal/sde"
)

// ownedBPCAnalyzer builds the same T2-module-from-invention fixture as
// TestAnalyze_InventionAddsExpectedBPCCost, so the numbers below can be read
// against it: 20 runs / 10 runs-per-BPC = 2 successes, / 0.4 probability = 5
// expected attempts, 1070 ISK of invention cost, 2078 ISK all in.
func ownedBPCAnalyzer() *IndustryAnalyzer {
	ind := sde.NewIndustryData()
	ind.Blueprints[5001] = &sde.Blueprint{
		BlueprintTypeID: 5001,
		ProductTypeID:   5000,
		ProductQuantity: 1,
		Time:            1000,
		Materials:       []sde.BlueprintMaterial{{TypeID: 34, Quantity: 10}},
		Activities: map[string]*sde.ActivityData{
			"manufacturing": {
				Time:      1000,
				Materials: []sde.BlueprintMaterial{{TypeID: 34, Quantity: 10}},
				Products:  []sde.BlueprintProduct{{TypeID: 5000, Quantity: 1}},
			},
		},
	}
	ind.ProductToBlueprint[5000] = 5001
	ind.Blueprints[5100] = &sde.Blueprint{
		BlueprintTypeID: 5100,
		Activities: map[string]*sde.ActivityData{
			"invention": {
				Time:      100,
				Materials: []sde.BlueprintMaterial{{TypeID: 6001, Quantity: 2}},
				Products:  []sde.BlueprintProduct{{TypeID: 5001, Quantity: 10, Probability: 0.4}},
			},
		},
	}
	return &IndustryAnalyzer{
		SDE: &sde.Data{
			Types: map[int32]*sde.ItemType{
				34:   {ID: 34, Name: "Tritanium"},
				5000: {ID: 5000, Name: "T2 Module"},
				5001: {ID: 5001, Name: "T2 Module Blueprint"},
				5100: {ID: 5100, Name: "T1 Module Blueprint"},
				6001: {ID: 6001, Name: "Datacore"},
			},
			Systems:  map[int32]*sde.SolarSystem{30000142: {ID: 30000142, Name: "Jita", RegionID: 10000002}},
			Regions:  map[int32]*sde.Region{10000002: {ID: 10000002, Name: "The Forge"}},
			Industry: ind,
		},
		IndustryCache: esi.NewIndustryCache(),
		getAllAdjustedPrices: func(_ *esi.IndustryCache) (map[int32]float64, error) {
			return map[int32]float64{34: 1, 6001: 50}, nil
		},
		getSystemCostIndex: func(_ *esi.IndustryCache, _ int32) (*esi.SystemCostIndices, error) {
			return &esi.SystemCostIndices{Manufacturing: 0, Invention: 0.1}, nil
		},
		fetchMarketPricesFn: func(_ IndustryParams) (map[int32]float64, error) {
			return map[int32]float64{34: 5, 5000: 1000, 6001: 100}, nil
		},
		fetchMarketBooksFn: func(_ IndustryParams) (map[int32][]esi.MarketOrder, map[int32][]esi.MarketOrder, error) {
			return nil, nil, nil
		},
	}
}

func hasInventionStep(plan []IndustryActivityStep) bool {
	for _, step := range plan {
		if step.Activity == "invention" {
			return true
		}
	}
	return false
}

func inventionStepIn(t *testing.T, plan []IndustryActivityStep) IndustryActivityStep {
	t.Helper()
	for _, step := range plan {
		if step.Activity == "invention" {
			return step
		}
	}
	t.Fatalf("activity plan has no invention step: %+v", plan)
	return IndustryActivityStep{}
}

func stepMaterialQty(step IndustryActivityStep, typeID int32) int32 {
	for _, m := range step.Materials {
		if m.TypeID == typeID {
			return m.Quantity
		}
	}
	return 0
}

// TestAnalyze_OwnedT2BPC covers the matching rules for skipping the invention
// job. They are deliberately strict: a blueprint at a different ME or TE
// builds at a different cost, so anything short of an exact match has to
// leave the invention step alone.
func TestAnalyze_OwnedT2BPC(t *testing.T) {
	const wantInventionCost = 1070.0
	const wantOptimalCost = 2078.0

	cases := []struct {
		name  string
		owned map[int32]OwnedBlueprint
		// wantStep: is there still an invention job to install?
		wantStep        bool
		wantCoveredRuns int32
		// wantReplacement: the slice of invention cost attributed to the
		// blueprint already sitting in the hangar.
		wantReplacement float64
		// wantAttempts / wantDatacores describe the surviving step.
		wantAttempts  float64
		wantDatacores int32
	}{
		{
			name:            "no owned blueprint invents everything",
			owned:           nil,
			wantStep:        true,
			wantCoveredRuns: 0,
			wantReplacement: 0,
			wantAttempts:    5,
			wantDatacores:   10,
		},
		{
			name:            "exact match with enough runs drops the job but keeps the cost",
			owned:           map[int32]OwnedBlueprint{5000: {ME: 0, TE: 0, AvailableRuns: 20}},
			wantStep:        false,
			wantCoveredRuns: 20,
			wantReplacement: wantInventionCost,
		},
		{
			name:            "surplus runs only ever cover what the build needs",
			owned:           map[int32]OwnedBlueprint{5000: {ME: 0, TE: 0, AvailableRuns: 500}},
			wantStep:        false,
			wantCoveredRuns: 20,
			wantReplacement: wantInventionCost,
		},
		{
			name:            "ME mismatch keeps the invention job",
			owned:           map[int32]OwnedBlueprint{5000: {ME: 2, TE: 0, AvailableRuns: 20}},
			wantStep:        true,
			wantCoveredRuns: 0,
			wantReplacement: 0,
			wantAttempts:    5,
			wantDatacores:   10,
		},
		{
			name:            "TE mismatch keeps the invention job",
			owned:           map[int32]OwnedBlueprint{5000: {ME: 0, TE: 4, AvailableRuns: 20}},
			wantStep:        true,
			wantCoveredRuns: 0,
			wantReplacement: 0,
			wantAttempts:    5,
			wantDatacores:   10,
		},
		{
			name:            "a BPO is not an invention output",
			owned:           map[int32]OwnedBlueprint{5000: {ME: 0, TE: 0, IsBPO: true, AvailableRuns: 20}},
			wantStep:        true,
			wantCoveredRuns: 0,
			wantReplacement: 0,
			wantAttempts:    5,
			wantDatacores:   10,
		},
		{
			name: "part-used BPC invents only the shortfall",
			// 10 of the 20 runs are covered, so one more 10-run BPC is
			// needed: 1 success / 0.4 = 2.5 attempts, half the full 5, and
			// half the datacores on the shopping list.
			owned:           map[int32]OwnedBlueprint{5000: {ME: 0, TE: 0, AvailableRuns: 10}},
			wantStep:        true,
			wantCoveredRuns: 10,
			wantReplacement: wantInventionCost / 2,
			wantAttempts:    2.5,
			wantDatacores:   5,
		},
		{
			name:            "a spent BPC covers nothing",
			owned:           map[int32]OwnedBlueprint{5000: {ME: 0, TE: 0, AvailableRuns: 0}},
			wantStep:        true,
			wantCoveredRuns: 0,
			wantReplacement: 0,
			wantAttempts:    5,
			wantDatacores:   10,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ownedBPCAnalyzer().Analyze(IndustryParams{
				TypeID:          5000,
				Runs:            20,
				ActivityMode:    "invention",
				SystemID:        30000142,
				OwnedBlueprints: tc.owned,
			}, func(string) {})
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}

			if got := hasInventionStep(result.ActivityPlan); got != tc.wantStep {
				t.Errorf("invention step present = %v, want %v (plan %+v)", got, tc.wantStep, result.ActivityPlan)
			}
			if result.InventionCoveredRuns != tc.wantCoveredRuns {
				t.Errorf("InventionCoveredRuns = %d, want %d", result.InventionCoveredRuns, tc.wantCoveredRuns)
			}
			if !industryAlmostEqual(result.InventionReplacementCost, tc.wantReplacement) {
				t.Errorf("InventionReplacementCost = %v, want %v", result.InventionReplacementCost, tc.wantReplacement)
			}
			if tc.wantStep {
				step := inventionStepIn(t, result.ActivityPlan)
				if !industryAlmostEqual(step.ExpectedAttempts, tc.wantAttempts) {
					t.Errorf("step.ExpectedAttempts = %v, want %v", step.ExpectedAttempts, tc.wantAttempts)
				}
				// Datacores for runs the hangar already covers are never
				// consumed, so they must not reach the shopping list even
				// though their replacement cost stays in the basis.
				if got := stepMaterialQty(step, 6001); got != tc.wantDatacores {
					t.Errorf("step datacores = %d, want %d", got, tc.wantDatacores)
				}
			}

			// The whole point of replacement-cost accounting: owning the
			// blueprint changes the work, never the economics. The user
			// prices these builds with the invention cost included on
			// purpose, and treating an owned BPC as free would reshuffle
			// every scanner ranking.
			if !industryAlmostEqual(result.InventionCost, wantInventionCost) {
				t.Errorf("InventionCost = %v, want %v — owning a BPC must not reprice the build", result.InventionCost, wantInventionCost)
			}
			if !industryAlmostEqual(result.OptimalBuildCost, wantOptimalCost) {
				t.Errorf("OptimalBuildCost = %v, want %v — owning a BPC must not reprice the build", result.OptimalBuildCost, wantOptimalCost)
			}
		})
	}
}

// Coverage is only consulted in invention mode. In buy-the-BPC mode there is
// no invention step to drop and nothing to report.
func TestAnalyze_OwnedT2BPCIgnoredOutsideInventionMode(t *testing.T) {
	result, err := ownedBPCAnalyzer().Analyze(IndustryParams{
		TypeID:          5000,
		Runs:            20,
		SystemID:        30000142,
		OwnedBlueprints: map[int32]OwnedBlueprint{5000: {AvailableRuns: 20}},
	}, func(string) {})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if hasInventionStep(result.ActivityPlan) {
		t.Errorf("activity plan = %+v, want no invention step outside invention mode", result.ActivityPlan)
	}
	if result.InventionCoveredRuns != 0 || result.InventionReplacementCost != 0 {
		t.Errorf("InventionCoveredRuns = %d / InventionReplacementCost = %v, want 0 / 0",
			result.InventionCoveredRuns, result.InventionReplacementCost)
	}
}
