package engine

import (
	"math"
	"testing"

	"eve-flipper/internal/esi"
	"eve-flipper/internal/sde"
)

// The scanner (industry_blueprint_scan.go) and the Analysis tab
// (server.go's /api/industry/analyze) both funnel every calculation through
// analyzer.Analyze. A user reported the two paths returning wildly different
// profit numbers for the same T2 row (Scanner: ~+1B; Analysis: minor loss).
// Every source of that divergence has to be one of:
//   1. Different params sent to Analyze (frontend layer).
//   2. Different fixture/market data seen by Analyze (backend layer).
// The tests here pin down (1) by asserting that when the two callers
// construct their params from equivalent user intent (same decryptor
// choice, same fees, same structure config), the analyzer returns the
// same numbers. If a future refactor introduces a silent divergence in
// one caller's param wiring, one of these tests fails.

// newInventionAlignmentSDE is a self-contained invention chain:
//   T1 source BP (bpTypeID 4000) — invention activity → T2 BPC (typeID 4010)
//   T2 BPC (bpTypeID 4010) — manufacturing activity → T2 product (typeID 4001)
//   T2 module material chain: needs 5 of typeID 1002 (base material) per run.
// A "10-run per success" invention output matches T2 module norms so a full
// BPC is 10 units of the T2 product.
func newInventionAlignmentSDE() *sde.Data {
	ind := sde.NewIndustryData()

	// T1 source blueprint — has an invention activity that outputs the T2
	// BPC on success. baseProbability 0.34, base 10-run BPC.
	t1BP := &sde.Blueprint{
		BlueprintTypeID: 4000,
		ProductTypeID:   4000, // T1 product (immaterial to this test)
		ProductQuantity: 1,
		Time:            600,
		Activities: map[string]*sde.ActivityData{
			"invention": {
				Materials: []sde.BlueprintMaterial{
					{TypeID: 20172, Quantity: 2}, // datacore stand-in
				},
				Products: []sde.BlueprintProduct{
					{TypeID: 4010, Quantity: 10, Probability: 0.34},
				},
				Time: 600,
			},
		},
	}
	ind.Blueprints[4000] = t1BP

	// T2 BPC — invention output. Its manufacturing activity produces the
	// T2 module (product 4001). Materials use a single base type (1002)
	// to keep the tree geometry trivial; both callers get the same
	// bill of materials regardless of ME cascade.
	t2BPC := &sde.Blueprint{
		BlueprintTypeID: 4010,
		ProductTypeID:   4001,
		ProductQuantity: 1,
		Time:            1200,
		Materials: []sde.BlueprintMaterial{
			{TypeID: 1002, Quantity: 5},
		},
		Activities: map[string]*sde.ActivityData{
			"manufacturing": {
				Materials: []sde.BlueprintMaterial{
					{TypeID: 1002, Quantity: 5},
				},
				Products: []sde.BlueprintProduct{
					{TypeID: 4001, Quantity: 1},
				},
				Time: 1200,
			},
		},
	}
	ind.Blueprints[4010] = t2BPC
	ind.ProductToBlueprint[4001] = 4010
	ind.InventionProducts = map[int32]bool{4010: true}
	ind.InventionOutputRunsByBPC = map[int32]int32{4010: 10}

	return &sde.Data{
		Types: map[int32]*sde.ItemType{
			1002:  {ID: 1002, Name: "Base Component", Volume: 0.5},
			4000:  {ID: 4000, Name: "T1 Source Product", Volume: 5},
			4001:  {ID: 4001, Name: "T2 Module", Volume: 5, MetaGroupID: 2},
			4010:  {ID: 4010, Name: "T2 Module Blueprint", Volume: 0.01},
			20172: {ID: 20172, Name: "Datacore - Test Physics", Volume: 0.1},
		},
		Systems: map[int32]*sde.SolarSystem{
			30000142: {ID: 30000142, Name: "Jita", RegionID: 10000002},
		},
		Regions: map[int32]*sde.Region{
			10000002: {ID: 10000002, Name: "The Forge"},
		},
		Stations: map[int64]*sde.Station{},
		Industry: ind,
	}
}

// newAlignmentAnalyzer wires up an analyzer with a fixture SDE and stable
// market/price mocks so runs are deterministic and reproducible.
func newAlignmentAnalyzer(t *testing.T) *IndustryAnalyzer {
	t.Helper()
	sdeData := newInventionAlignmentSDE()
	return &IndustryAnalyzer{
		SDE:           sdeData,
		IndustryCache: esi.NewIndustryCache(),
		getAllAdjustedPrices: func(_ *esi.IndustryCache) (map[int32]float64, error) {
			return map[int32]float64{
				1002:  100.0,
				4001:  4_000_000.0, // T2 module CCP adjusted price
				20172: 25_000.0,
			}, nil
		},
		getSystemCostIndex: func(_ *esi.IndustryCache, systemID int32) (*esi.SystemCostIndices, error) {
			return &esi.SystemCostIndices{Manufacturing: 0.05, Invention: 0.02}, nil
		},
		fetchMarketPricesFn: func(_ IndustryParams) (map[int32]float64, error) {
			return map[int32]float64{
				1002:  150.0,       // Base component sell (raw ask)
				4001:  5_500_000.0, // T2 module sell (raw ask)
				20172: 30_000.0,    // Datacore
			}, nil
		},
	}
}

// scannerPathParams reproduces the param construction the scanner performs
// in industry_blueprint_scan.go's T2 invention branch for a "None" decryptor
// on a product whose SDE base-BPC-runs is 10.
func scannerPathParams(analyzer *IndustryAnalyzer, productTypeID int32, systemID int32, req scannerLikeReq) IndustryParams {
	// Mirror industry_blueprint_scan.go's inner loop for the None decryptor:
	//   meBase, teBase, outputRuns, chanceMult, cost := dec.EffectiveInventionParamsForBase(item.outputBPCRuns)
	//   params.ActivityMode = "invention"
	//   params.MaterialEfficiency = meBase
	//   params.TimeEfficiency = teBase
	//   chance := item.baseProbability * chanceMult * 100
	//   params.InventionChance = chance
	//   params.InventionOutputRuns = outputRuns
	//   bpcs := ceil(req.RunsPerJob / outputRuns)
	//   params.Runs = bpcs * outputRuns
	none := decryptorNoneForTest()
	meBase, teBase, outputRuns, chanceMult, cost := none.EffectiveInventionParamsForBase(req.outputBPCRuns)
	chance := req.baseProbability * chanceMult * 100
	if chance > 100 {
		chance = 100
	}
	bpcs := (req.runsPerJob + outputRuns - 1) / outputRuns
	if bpcs < 1 {
		bpcs = 1
	}
	return IndustryParams{
		TypeID:              productTypeID,
		Runs:                bpcs * outputRuns,
		ActivityMode:        "invention",
		MaterialEfficiency:  meBase,
		TimeEfficiency:      teBase,
		SystemID:            systemID,
		FacilityTax:         req.facilityTax,
		StructureBonus:      req.structureBonus,
		BrokerFee:           req.brokerFee,
		SalesTaxPercent:     req.salesTaxPercent,
		MaxDepth:            10,
		OwnBlueprint:        true,
		InventionChance:     chance,
		InventionOutputRuns: outputRuns,
		DecryptorCost:       cost,
		RevenueModel:        req.revenueModel,
		CostModel:           req.costModel,
	}
}

// analyzePathParams reproduces the param construction the Analysis tab
// performs in IndustryTab.tsx's handleAnalyze — where the frontend has
// already resolved the "None" decryptor's outputRuns via
// effectiveInventionParams(baseRuns) and sent invention_chance_mult
// (not a pre-multiplied absolute chance).
func analyzePathParams(_ *IndustryAnalyzer, productTypeID int32, systemID int32, req scannerLikeReq) IndustryParams {
	// effectiveInventionParams("none", 10) → meBase=2, teBase=4, outputRuns=10, chanceMult=1.0
	none := decryptorNoneForTest()
	meBase, teBase, outputRuns, chanceMult, cost := none.EffectiveInventionParamsForBase(req.outputBPCRuns)
	// The Analysis tab sends `runs` directly from the UI — the user typically
	// picks a full BPC's worth (base + decryptor bonus), which matches the
	// scanner's bpcs*outputRuns math for the default 1-BPC case.
	runsPerBPC := outputRuns
	return IndustryParams{
		TypeID:              productTypeID,
		Runs:                runsPerBPC, // one full BPC, same as scanner default
		ActivityMode:        "invention",
		MaterialEfficiency:  meBase,
		TimeEfficiency:      teBase,
		SystemID:            systemID,
		FacilityTax:         req.facilityTax,
		StructureBonus:      req.structureBonus,
		BrokerFee:           req.brokerFee,
		SalesTaxPercent:     req.salesTaxPercent,
		MaxDepth:            10,
		OwnBlueprint:        true,
		InventionChanceMult: chanceMult, // frontend sends mult, backend applies to SDE base
		InventionOutputRuns: outputRuns,
		DecryptorCost:       cost,
		RevenueModel:        req.revenueModel,
		CostModel:           req.costModel,
	}
}

type scannerLikeReq struct {
	runsPerJob      int32
	outputBPCRuns   int32
	baseProbability float64
	facilityTax     float64
	structureBonus  float64
	brokerFee       float64
	salesTaxPercent float64
	revenueModel    string
	costModel       string
}

func decryptorNoneForTest() Decryptor {
	for _, d := range Decryptors {
		if d.Key == "none" {
			return d
		}
	}
	panic("Decryptors table missing 'none' entry")
}

func TestAnalyzePath_MatchesScannerPath_ForT2Invention(t *testing.T) {
	// The regression this locks down: given the SAME user intent — a T2
	// module invented with the None decryptor, sold at the Jita ask, base
	// components bought from Jita sell orders, standard fees — the scanner
	// and Analysis tab must produce IDENTICAL profit / revenue / cost
	// numbers. A silent divergence in either caller's param wiring gets
	// caught here. If a future change makes one caller pass a subtly
	// different value (invention chance absolute vs. mult, decryptor's ME
	// baseline, runs granularity, etc.), we surface it before it ships.
	req := scannerLikeReq{
		runsPerJob:      1,   // scanner default; rounded up to a full BPC below
		outputBPCRuns:   10,  // T2 module baseline
		baseProbability: 0.34,
		facilityTax:     0.25,
		structureBonus:  1.0,
		brokerFee:       1.0,
		salesTaxPercent: 3.6,
		revenueModel:    "sell_to_sell",
		costModel:       "buy_to_sell",
	}

	scanAnalyzer := newAlignmentAnalyzer(t)
	analyzeAnalyzer := newAlignmentAnalyzer(t)

	scanParams := scannerPathParams(scanAnalyzer, 4001, 30000142, req)
	analyzeParams := analyzePathParams(analyzeAnalyzer, 4001, 30000142, req)

	scanResult, err := scanAnalyzer.Analyze(scanParams, func(string) {})
	if err != nil {
		t.Fatalf("scanner-path Analyze: %v", err)
	}
	analyzeResult, err := analyzeAnalyzer.Analyze(analyzeParams, func(string) {})
	if err != nil {
		t.Fatalf("analyze-path Analyze: %v", err)
	}

	// TotalQuantity — same effective batch size (1 full BPC = 10 units)
	if scanResult.TotalQuantity != analyzeResult.TotalQuantity {
		t.Errorf("TotalQuantity diverged: scanner=%d analyze=%d", scanResult.TotalQuantity, analyzeResult.TotalQuantity)
	}

	// UnitAskPrice — both should quote the same per-unit ask from the
	// mocked market prices. If this diverges, one path is looking at a
	// different pricing region / station.
	if !closeEnough(scanResult.UnitAskPrice, analyzeResult.UnitAskPrice, 0.01) {
		t.Errorf("UnitAskPrice diverged: scanner=%v analyze=%v", scanResult.UnitAskPrice, analyzeResult.UnitAskPrice)
	}
	// Sanity: it should equal the mocked ask (5.5M).
	if !closeEnough(scanResult.UnitAskPrice, 5_500_000.0, 0.01) {
		t.Errorf("UnitAskPrice = %v, want 5_500_000", scanResult.UnitAskPrice)
	}

	// SellRevenue — same tax/broker applied to same ask over same
	// TotalQuantity. Any divergence is a fees/tax cascade bug.
	if !closeEnough(scanResult.SellRevenue, analyzeResult.SellRevenue, 0.01) {
		t.Errorf("SellRevenue diverged: scanner=%v analyze=%v", scanResult.SellRevenue, analyzeResult.SellRevenue)
	}

	// OptimalBuildCost — same materials, same job math. Divergence here
	// points at ME cascade, invention amortization, or blueprint-cost
	// wiring differences between the two callers.
	if !closeEnough(scanResult.OptimalBuildCost, analyzeResult.OptimalBuildCost, 0.01) {
		t.Errorf("OptimalBuildCost diverged: scanner=%v analyze=%v", scanResult.OptimalBuildCost, analyzeResult.OptimalBuildCost)
	}

	// InventionCost — the scanner sends InventionChance (pre-multiplied
	// absolute), the Analysis tab sends InventionChanceMult (multiplier
	// against SDE base). Both should collapse to the same effective
	// probability and therefore the same expected-attempt cost.
	if !closeEnough(scanResult.InventionCost, analyzeResult.InventionCost, 0.01) {
		t.Errorf("InventionCost diverged: scanner=%v analyze=%v (chance handling mismatch)", scanResult.InventionCost, analyzeResult.InventionCost)
	}

	// Profit — top-line number the user sees on both screens.
	// This is the alignment test's headline assertion: if this passes,
	// the two panels report the same profit for the same intent.
	if !closeEnough(scanResult.Profit, analyzeResult.Profit, 0.01) {
		t.Errorf("Profit diverged: scanner=%v analyze=%v — this is the bug the user reported when the two panels disagree by ~100M+ on the same row", scanResult.Profit, analyzeResult.Profit)
	}
}

func TestAnalyzePath_MatchesScannerPath_ForT2Ship(t *testing.T) {
	// T2 ships invent to base-1-run BPCs (not base-10 like modules). Both
	// paths must respect the SDE per-target base runs through the
	// EffectiveInventionParamsForBase plumbing. If either regresses to
	// hardcoded 10, this test fails — the scanner would over-estimate
	// output by 10x (the pre-v1.8.5 ship-invention profit-inflation bug).
	analyzer := newAlignmentAnalyzer(t)
	// Point 4010's BPC-runs to 1 (T2 ship style) and re-run both paths.
	analyzer.SDE.Industry.InventionOutputRunsByBPC[4010] = 1
	scanAnalyzer := analyzer
	analyzeAnalyzer := newAlignmentAnalyzer(t)
	analyzeAnalyzer.SDE.Industry.InventionOutputRunsByBPC[4010] = 1

	req := scannerLikeReq{
		runsPerJob:      1,
		outputBPCRuns:   1, // ship baseline — 1 successful invention = 1-run BPC
		baseProbability: 0.30,
		facilityTax:     0.10,
		structureBonus:  1.0,
		brokerFee:       1.0,
		salesTaxPercent: 3.6,
		revenueModel:    "sell_to_sell",
		costModel:       "buy_to_sell",
	}

	scanParams := scannerPathParams(scanAnalyzer, 4001, 30000142, req)
	analyzeParams := analyzePathParams(analyzeAnalyzer, 4001, 30000142, req)

	scanResult, err := scanAnalyzer.Analyze(scanParams, func(string) {})
	if err != nil {
		t.Fatalf("scanner-path Analyze: %v", err)
	}
	analyzeResult, err := analyzeAnalyzer.Analyze(analyzeParams, func(string) {})
	if err != nil {
		t.Fatalf("analyze-path Analyze: %v", err)
	}

	// Both must observe TotalQuantity = 1 (a ship-style single-run BPC).
	// If either goes to 10, that's the ship-invention-inflation regression.
	if scanResult.TotalQuantity != 1 {
		t.Errorf("scanner TotalQuantity = %d, want 1 (T2 ship base-1-run)", scanResult.TotalQuantity)
	}
	if analyzeResult.TotalQuantity != 1 {
		t.Errorf("analyze TotalQuantity = %d, want 1 (T2 ship base-1-run)", analyzeResult.TotalQuantity)
	}

	if !closeEnough(scanResult.Profit, analyzeResult.Profit, 0.01) {
		t.Errorf("Profit diverged (T2 ship path): scanner=%v analyze=%v", scanResult.Profit, analyzeResult.Profit)
	}
}

func TestAnalyzePath_MatchesScannerPath_UsesInventionChanceHandlingConsistently(t *testing.T) {
	// The scanner path sets InventionChance (absolute percent, pre-computed
	// as SDE base × decryptor mult × 100). The Analysis tab path sets
	// InventionChanceMult (leaving the absolute-vs-mult resolution to the
	// backend, which applies the mult against the SDE base). If the
	// backend's precedence rule between the two fields ever gets flipped,
	// or one path leaves both fields at zero and silently defaults to a
	// wrong probability, this test catches it — the expected-attempts
	// number is highly sensitive to probability, so InventionCost is a
	// direct probe of the effective chance the analyzer used.
	req := scannerLikeReq{
		runsPerJob:      1,
		outputBPCRuns:   10,
		baseProbability: 0.34, // typical T2 module base
		facilityTax:     0.10,
		structureBonus:  1.0,
		brokerFee:       1.0,
		salesTaxPercent: 3.6,
		revenueModel:    "sell_to_sell",
		costModel:       "buy_to_sell",
	}
	scanAnalyzer := newAlignmentAnalyzer(t)
	analyzeAnalyzer := newAlignmentAnalyzer(t)

	scanResult, err := scanAnalyzer.Analyze(scannerPathParams(scanAnalyzer, 4001, 30000142, req), func(string) {})
	if err != nil {
		t.Fatalf("scanner-path Analyze: %v", err)
	}
	analyzeResult, err := analyzeAnalyzer.Analyze(analyzePathParams(analyzeAnalyzer, 4001, 30000142, req), func(string) {})
	if err != nil {
		t.Fatalf("analyze-path Analyze: %v", err)
	}

	// InventionProbability is what the analyzer USED, echoed back. Both
	// paths' effective probability should be base × mult (None decryptor
	// mult = 1.0), so 0.34 for both.
	if !closeEnough(scanResult.InventionProbability, 0.34, 0.001) {
		t.Errorf("scanner InventionProbability = %v, want 0.34", scanResult.InventionProbability)
	}
	if !closeEnough(analyzeResult.InventionProbability, 0.34, 0.001) {
		t.Errorf("analyze InventionProbability = %v, want 0.34", analyzeResult.InventionProbability)
	}
	// And they must match each other regardless of which field was used.
	if !closeEnough(scanResult.InventionProbability, analyzeResult.InventionProbability, 0.001) {
		t.Errorf("InventionProbability diverged between paths: scanner=%v analyze=%v", scanResult.InventionProbability, analyzeResult.InventionProbability)
	}
}

func closeEnough(a, b, epsilon float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return false
	}
	return math.Abs(a-b) <= epsilon || math.Abs(a-b) <= math.Abs(a)*epsilon
}
