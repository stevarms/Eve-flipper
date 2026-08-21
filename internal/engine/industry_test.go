package engine

import (
	"math"
	"testing"

	"eve-flipper/internal/esi"
	"eve-flipper/internal/sde"
)

func TestGetBlueprintInfo_DelegatesToSDE(t *testing.T) {
	// Minimal SDE: IndustryData with one product -> blueprint
	ind := sde.NewIndustryData()
	bp := &sde.Blueprint{ProductTypeID: 999, ProductQuantity: 2}
	ind.Blueprints[100] = bp
	ind.ProductToBlueprint[999] = 100

	a := &IndustryAnalyzer{SDE: &sde.Data{Industry: ind}}

	got, ok := a.GetBlueprintInfo(999)
	if !ok || got != bp {
		t.Errorf("GetBlueprintInfo(999) = %v, %v; want bp, true", got, ok)
	}
	_, ok = a.GetBlueprintInfo(888)
	if ok {
		t.Error("GetBlueprintInfo(888) should be false")
	}
}

func TestResolveMarketRegion_PrefersSystemOverStation(t *testing.T) {
	a := &IndustryAnalyzer{
		SDE: &sde.Data{
			Systems: map[int32]*sde.SolarSystem{
				30000142: {ID: 30000142, RegionID: 10000002},
				30002187: {ID: 30002187, RegionID: 10000043},
			},
			Stations: map[int64]*sde.Station{
				60008494: {ID: 60008494, SystemID: 30002187},
			},
			Regions: map[int32]*sde.Region{
				10000002: {ID: 10000002, Name: "The Forge"},
				10000043: {ID: 10000043, Name: "Domain"},
			},
		},
	}

	regionID, regionName := a.resolveMarketRegion(IndustryParams{
		SystemID:  30000142,
		StationID: 60008494,
	})

	if regionID != 10000002 {
		t.Fatalf("regionID = %d, want 10000002", regionID)
	}
	if regionName != "The Forge" {
		t.Fatalf("regionName = %q, want The Forge", regionName)
	}
}

func TestResolveMarketRegion_UsesStationWhenSystemMissing(t *testing.T) {
	a := &IndustryAnalyzer{
		SDE: &sde.Data{
			Systems: map[int32]*sde.SolarSystem{
				30000142: {ID: 30000142, RegionID: 10000002},
			},
			Stations: map[int64]*sde.Station{
				60003760: {ID: 60003760, SystemID: 30000142},
			},
			Regions: map[int32]*sde.Region{
				10000002: {ID: 10000002, Name: "The Forge"},
			},
		},
	}

	regionID, regionName := a.resolveMarketRegion(IndustryParams{
		SystemID:  0,
		StationID: 60003760,
	})

	if regionID != 10000002 {
		t.Fatalf("regionID = %d, want 10000002", regionID)
	}
	if regionName != "The Forge" {
		t.Fatalf("regionName = %q, want The Forge", regionName)
	}
}

func TestMergeMarketPrices_StationOverridesRegionWithFallback(t *testing.T) {
	region := map[int32]float64{
		34:    5.0,  // fallback only
		35:    12.0, // overridden by station
		11399: 1.5,  // fallback only
	}
	station := map[int32]float64{
		35: 9.5,  // station override
		36: 20.0, // station-only type
	}

	got := mergeMarketPrices(region, station)

	if got[34] != 5.0 {
		t.Fatalf("type 34 = %v, want 5.0", got[34])
	}
	if got[35] != 9.5 {
		t.Fatalf("type 35 = %v, want 9.5", got[35])
	}
	if got[36] != 20.0 {
		t.Fatalf("type 36 = %v, want 20.0", got[36])
	}
	if got[11399] != 1.5 {
		t.Fatalf("type 11399 = %v, want 1.5", got[11399])
	}
}

func TestAnalyze_EndToEndInjectedPricing(t *testing.T) {
	sdeData := newTestIndustrySDE()
	a := &IndustryAnalyzer{
		SDE:           sdeData,
		IndustryCache: esi.NewIndustryCache(),
		getAllAdjustedPrices: func(_ *esi.IndustryCache) (map[int32]float64, error) {
			return map[int32]float64{
				34:   1.0,
				1001: 2.0,
				1002: 3.0,
			}, nil
		},
		getSystemCostIndex: func(_ *esi.IndustryCache, systemID int32) (*esi.SystemCostIndices, error) {
			if systemID != 30000142 {
				t.Fatalf("systemID = %d, want 30000142", systemID)
			}
			return &esi.SystemCostIndices{Manufacturing: 0.1}, nil
		},
		fetchMarketPricesFn: func(_ IndustryParams) (map[int32]float64, error) {
			return map[int32]float64{
				34:   1.0,
				1000: 300.0,
				1001: 20.0,
				1002: 15.0,
			}, nil
		},
	}

	progress := make([]string, 0, 5)
	result, err := a.Analyze(IndustryParams{
		TypeID:             1000,
		Runs:               2,
		SystemID:           30000142,
		BrokerFee:          5,
		SalesTaxPercent:    10,
		MaterialEfficiency: 0,
		TimeEfficiency:     0,
	}, func(msg string) {
		progress = append(progress, msg)
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(progress) != 6 {
		t.Fatalf("progress count = %d, want 6", len(progress))
	}

	if result.TotalQuantity != 2 {
		t.Fatalf("TotalQuantity = %d, want 2", result.TotalQuantity)
	}
	if result.RegionID != 10000002 || result.RegionName != "The Forge" {
		t.Fatalf("region = (%d, %q), want (10000002, The Forge)", result.RegionID, result.RegionName)
	}
	if !industryAlmostEqual(result.SystemCostIndex, 0.1) {
		t.Fatalf("SystemCostIndex = %v, want 0.1", result.SystemCostIndex)
	}
	if !industryAlmostEqual(result.MarketBuyPrice, 600.0) {
		t.Fatalf("MarketBuyPrice = %v, want 600", result.MarketBuyPrice)
	}
	// TotalJobCost is ROOT-ONLY (matches CCP's in-game display): the one
	// install cost the player pays for the queued job at the top level.
	// Sub-material install costs live inside their own build costs and
	// bubble up via tree.BuildCost's recursive material cost. Summing
	// every buildable node's JobCost here would double-count against
	// tree.BuildCost.
	//
	// Fixture geometry: two built layers (root 1000 + component 1001) each
	// with material 34. EIV × SCI = 4.9 per node ⇒ system cost 4.9,
	// SCC = 4% × 49 EIV = 1.96, gross = 4.9 (no rig/structure bonus),
	// facility tax = 0 (params.FacilityTax not set). Node install ≈ 4.9 + 1.96 ≈ 6.86.
	// Legacy sum across the two built nodes gave ~13.7; earlier tests had 18.2
	// after the SCC fix while it was still summed. Root-only ≈ 9.8 (only root).
	if !industryAlmostEqual(result.TotalBuildCost, 228.2) {
		t.Fatalf("TotalBuildCost = %v, want 228.2", result.TotalBuildCost)
	}
	if !industryAlmostEqual(result.OptimalBuildCost, 228.2) {
		t.Fatalf("OptimalBuildCost = %v, want 228.2", result.OptimalBuildCost)
	}
	if !industryAlmostEqual(result.TotalJobCost, 9.8) {
		t.Fatalf("TotalJobCost = %v, want 9.8", result.TotalJobCost)
	}
	if !industryAlmostEqual(result.SellRevenue, 513.0) {
		t.Fatalf("SellRevenue = %v, want 513", result.SellRevenue)
	}
	if !industryAlmostEqual(result.Profit, 284.8) {
		t.Fatalf("Profit = %v, want 284.8", result.Profit)
	}
	// ISK/h uses ROOT activity time only (matches CCP's "this job's slot
	// throughput" semantic; sub-material builds run in independent slots and
	// don't gate the queued job). Root blueprint time = 7200s = 2h, so
	// ISK/h = 284.8 / 2 = 142.4.
	if !industryAlmostEqual(result.ISKPerHour, 142.4) {
		t.Fatalf("ISKPerHour = %v, want 142.4", result.ISKPerHour)
	}
	if result.MaterialTree == nil {
		t.Fatalf("MaterialTree is nil")
	}
	if !result.MaterialTree.ShouldBuild {
		t.Fatalf("root should_build = false, want true")
	}

	byType := map[int32]*MaterialNode{}
	for _, child := range result.MaterialTree.Children {
		byType[child.TypeID] = child
	}
	componentNode := byType[1001]
	if componentNode == nil {
		t.Fatalf("component node (1001) missing")
	}
	if !componentNode.ShouldBuild {
		t.Fatalf("component node should_build = false, want true")
	}
	baseNode := byType[1002]
	if baseNode == nil {
		t.Fatalf("base material node (1002) missing")
	}
	if baseNode.ShouldBuild {
		t.Fatalf("base material node should_build = true, want false")
	}

	if len(result.FlatMaterials) != 2 {
		t.Fatalf("flat materials len = %d, want 2", len(result.FlatMaterials))
	}
	flatByType := map[int32]*FlatMaterial{}
	for _, m := range result.FlatMaterials {
		flatByType[m.TypeID] = m
	}
	if flatByType[1002] == nil || flatByType[1002].Quantity != 10 {
		t.Fatalf("flat material 1002 = %+v, want quantity 10", flatByType[1002])
	}
	if flatByType[34] == nil || flatByType[34].Quantity != 60 {
		t.Fatalf("flat material 34 = %+v, want quantity 60", flatByType[34])
	}
}

func TestAnalyze_UsesDepthAwareBuyCostAndInstantSellProfit(t *testing.T) {
	sdeData := newTestIndustrySDE()
	a := &IndustryAnalyzer{
		SDE:           sdeData,
		IndustryCache: esi.NewIndustryCache(),
		getAllAdjustedPrices: func(_ *esi.IndustryCache) (map[int32]float64, error) {
			return map[int32]float64{
				34:   1.0,
				1001: 2.0,
				1002: 3.0,
			}, nil
		},
		getSystemCostIndex: func(_ *esi.IndustryCache, systemID int32) (*esi.SystemCostIndices, error) {
			return &esi.SystemCostIndices{Manufacturing: 0.1}, nil
		},
		fetchMarketPricesFn: func(_ IndustryParams) (map[int32]float64, error) {
			return map[int32]float64{
				34:   1.0,
				1000: 300.0,
				1001: 20.0,
				1002: 15.0,
			}, nil
		},
		fetchMarketBooksFn: func(_ IndustryParams) (map[int32][]esi.MarketOrder, map[int32][]esi.MarketOrder, error) {
			return map[int32][]esi.MarketOrder{
					34: {
						{TypeID: 34, Price: 1, VolumeRemain: 60},
					},
					1000: {
						{TypeID: 1000, Price: 300, VolumeRemain: 1},
						{TypeID: 1000, Price: 400, VolumeRemain: 1},
					},
					1001: {
						{TypeID: 1001, Price: 20, VolumeRemain: 20},
					},
					1002: {
						{TypeID: 1002, Price: 15, VolumeRemain: 20},
					},
				},
				map[int32][]esi.MarketOrder{
					1000: {
						{TypeID: 1000, Price: 250, VolumeRemain: 2, IsBuyOrder: true},
					},
				},
				nil
		},
	}

	result, err := a.Analyze(IndustryParams{
		TypeID:             1000,
		Runs:               2,
		SystemID:           30000142,
		BrokerFee:          5,
		SalesTaxPercent:    10,
		MaterialEfficiency: 0,
		TimeEfficiency:     0,
	}, func(string) {})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if !industryAlmostEqual(result.MarketBuyPrice, 700.0) {
		t.Fatalf("MarketBuyPrice = %v, want depth-aware 700", result.MarketBuyPrice)
	}
	if !result.InstantSellAvailable {
		t.Fatalf("InstantSellAvailable = false, want true")
	}
	if !industryAlmostEqual(result.InstantSellRevenue, 450.0) {
		t.Fatalf("InstantSellRevenue = %v, want 450", result.InstantSellRevenue)
	}
	if !industryAlmostEqual(result.MakerSellRevenue, 513.0) {
		t.Fatalf("MakerSellRevenue = %v, want 513", result.MakerSellRevenue)
	}
	if !industryAlmostEqual(result.SellRevenue, result.InstantSellRevenue) {
		t.Fatalf("SellRevenue = %v, want conservative instant revenue %v", result.SellRevenue, result.InstantSellRevenue)
	}
	// Pre-SCC: 227. New: 227 − 5.2 (SCC bump to job cost same as prior test) = 221.8.
	if !industryAlmostEqual(result.Profit, 221.8) {
		t.Fatalf("Profit = %v, want instant liquidation profit 221.8", result.Profit)
	}
}

func TestBuildMaterialTree_AppliesMEEAndMaxDepth(t *testing.T) {
	a := &IndustryAnalyzer{
		SDE: newTestIndustrySDE(),
		marketPrices: map[int32]float64{
			1000: 300,
			1001: 20,
			1002: 15,
			34:   1,
		},
	}

	tree := a.buildMaterialTree(1000, 2, IndustryParams{
		MaxDepth:           1,
		MaterialEfficiency: 10,
		StructureBonus:     1,
	}, 0)
	if tree.IsBase {
		t.Fatalf("root IsBase = true, want false")
	}
	if len(tree.Children) != 2 {
		t.Fatalf("children len = %d, want 2", len(tree.Children))
	}

	byType := map[int32]*MaterialNode{}
	for _, child := range tree.Children {
		byType[child.TypeID] = child
	}
	component := byType[1001]
	if component == nil {
		t.Fatalf("component child missing")
	}
	if component.Quantity != 18 {
		t.Fatalf("component quantity = %d, want 18", component.Quantity)
	}
	if !component.IsBase {
		t.Fatalf("component IsBase = false, want true because max depth reached")
	}
}

func TestCalculateCosts_PrefersBuyingWhenCheaper(t *testing.T) {
	a := &IndustryAnalyzer{
		SDE: newTestIndustrySDE(),
		marketPrices: map[int32]float64{
			1001: 5,
			34:   10,
		},
		adjustedPrices: map[int32]float64{
			34: 1,
		},
	}

	// Analyze 1001 as the ROOT — root is always ShouldBuild=true, so to
	// exercise the buy-vs-build cost comparison we analyze 1000 (the parent)
	// and inspect the 1001 CHILD instead.
	tree := a.buildMaterialTree(1000, 1, IndustryParams{MaxDepth: 10, TypeID: 1000}, 0)
	a.calculateCosts(tree, 0.1, IndustryParams{TypeID: 1000})

	// Find the 1001 child.
	var child *MaterialNode
	for _, c := range tree.Children {
		if c.TypeID == 1001 {
			child = c
			break
		}
	}
	if child == nil {
		t.Fatalf("expected 1001 child under 1000")
	}
	if child.ShouldBuild {
		t.Fatalf("child.ShouldBuild = true, want false (buying is cheaper)")
	}
	// The child (1001) is required 10× by the root recipe. Prices reflect
	// 10-unit totals: BuyPrice = 10 × 5 = 50. Materials: Tritanium (base 3
	// per run × 10 runs = 30 units × price 10 = 300 ISK). Job cost breakdown:
	//   EIV       = 30 units × adjustedPrice 1 = 30
	//   SystemCost = 30 × 0.1 SCI = 3.0
	//   Gross     = 3.0 (no structure/rig/facility tax in this fixture)
	//   SCC       = 30 × 4% = 1.2 (CCP flat surcharge)
	//   JobCost   = 3.0 + 1.2 = 4.2
	// BuildCost = 300 materials + 4.2 job cost = 304.2.
	if !industryAlmostEqual(child.BuyPrice, 50.0) {
		t.Fatalf("child.BuyPrice = %v, want 50", child.BuyPrice)
	}
	if !industryAlmostEqual(child.BuildCost, 304.2) {
		t.Fatalf("child.BuildCost = %v, want 304.2", child.BuildCost)
	}
	if !industryAlmostEqual(child.JobCost, 4.2) {
		t.Fatalf("child.JobCost = %v, want 4.2", child.JobCost)
	}
}

// BuildMode variants override the per-node buy-vs-build decision. Uses the
// same fixture as PrefersBuyingWhenCheaper (child 1001 is cheaper to buy
// than build) so we can prove the mode flips the decision.
func TestCalculateCosts_BuildModeBuildAllForcesBuildOnChildren(t *testing.T) {
	a := &IndustryAnalyzer{
		SDE: newTestIndustrySDE(),
		marketPrices: map[int32]float64{
			1001: 5,
			34:   10,
		},
		adjustedPrices: map[int32]float64{34: 1},
	}
	params := IndustryParams{MaxDepth: 10, TypeID: 1000, BuildMode: "build_all"}
	tree := a.buildMaterialTree(1000, 1, params, 0)
	a.calculateCosts(tree, 0.1, params)

	var child *MaterialNode
	for _, c := range tree.Children {
		if c.TypeID == 1001 {
			child = c
			break
		}
	}
	if child == nil {
		t.Fatalf("expected 1001 child under 1000")
	}
	if !child.ShouldBuild {
		t.Fatalf("build_all: child.ShouldBuild = false, want true (mode forces build)")
	}
}

func TestCalculateCosts_BuildModeBuyAllForcesBuyOnChildren(t *testing.T) {
	a := &IndustryAnalyzer{
		SDE: newTestIndustrySDE(),
		marketPrices: map[int32]float64{
			1001: 5000, // Make buying WAY more expensive than building.
			34:   10,
		},
		adjustedPrices: map[int32]float64{34: 1},
	}
	// In auto mode this would ShouldBuild=true (build is cheaper), but with
	// buy_all we force buying regardless.
	params := IndustryParams{MaxDepth: 10, TypeID: 1000, BuildMode: "buy_all"}
	tree := a.buildMaterialTree(1000, 1, params, 0)
	a.calculateCosts(tree, 0.1, params)

	var child *MaterialNode
	for _, c := range tree.Children {
		if c.TypeID == 1001 {
			child = c
			break
		}
	}
	if child == nil {
		t.Fatalf("expected 1001 child under 1000")
	}
	if child.ShouldBuild {
		t.Fatalf("buy_all: child.ShouldBuild = true, want false (mode forces buy)")
	}
}

func TestCalculateCosts_BuildModeRootAlwaysBuilds(t *testing.T) {
	a := &IndustryAnalyzer{
		SDE: newTestIndustrySDE(),
		marketPrices: map[int32]float64{
			1000: 1, // Root is available on market for 1 ISK — buy would seem best.
			1001: 5,
			34:   10,
		},
		adjustedPrices: map[int32]float64{34: 1},
	}
	// Even with buy_all, the ROOT (typeID 1000) must ShouldBuild=true —
	// otherwise "analyze this thing" produces no plan.
	params := IndustryParams{MaxDepth: 10, TypeID: 1000, BuildMode: "buy_all"}
	tree := a.buildMaterialTree(1000, 1, params, 0)
	a.calculateCosts(tree, 0.1, params)
	if !tree.ShouldBuild {
		t.Fatalf("root ShouldBuild = false with buy_all, want true (root is exempt)")
	}
}

func TestAnalyze_ReactionActivityUsesReactionMaterialsAndCostIndex(t *testing.T) {
	ind := sde.NewIndustryData()
	ind.Blueprints[3000] = &sde.Blueprint{
		BlueprintTypeID: 3000,
		ProductTypeID:   4000,
		ProductQuantity: 2,
		Activities: map[string]*sde.ActivityData{
			"reaction": {
				Time: 600,
				Materials: []sde.BlueprintMaterial{
					{TypeID: 34, Quantity: 5},
				},
				Products: []sde.BlueprintProduct{
					{TypeID: 4000, Quantity: 2},
				},
			},
		},
	}
	ind.ProductToBlueprint[4000] = 3000
	a := &IndustryAnalyzer{
		SDE: &sde.Data{
			Types: map[int32]*sde.ItemType{
				34:   {ID: 34, Name: "Tritanium"},
				3000: {ID: 3000, Name: "Reaction Formula"},
				4000: {ID: 4000, Name: "Reacted Material"},
			},
			Systems: map[int32]*sde.SolarSystem{
				30000142: {ID: 30000142, Name: "Jita", RegionID: 10000002},
			},
			Regions:  map[int32]*sde.Region{10000002: {ID: 10000002, Name: "The Forge"}},
			Industry: ind,
		},
		IndustryCache: esi.NewIndustryCache(),
		getAllAdjustedPrices: func(_ *esi.IndustryCache) (map[int32]float64, error) {
			return map[int32]float64{34: 1}, nil
		},
		getSystemCostIndex: func(_ *esi.IndustryCache, _ int32) (*esi.SystemCostIndices, error) {
			return &esi.SystemCostIndices{Manufacturing: 0.01, Reaction: 0.2}, nil
		},
		fetchMarketPricesFn: func(_ IndustryParams) (map[int32]float64, error) {
			return map[int32]float64{34: 10, 4000: 100}, nil
		},
		fetchMarketBooksFn: func(_ IndustryParams) (map[int32][]esi.MarketOrder, map[int32][]esi.MarketOrder, error) {
			return nil, nil, nil
		},
	}

	result, err := a.Analyze(IndustryParams{
		TypeID:       4000,
		Runs:         2,
		ActivityMode: "reaction",
		SystemID:     30000142,
	}, func(string) {})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if result.TotalQuantity != 4 {
		t.Fatalf("TotalQuantity = %d, want 4", result.TotalQuantity)
	}
	if result.MaterialTree.Activity != "reaction" {
		t.Fatalf("root activity = %q, want reaction", result.MaterialTree.Activity)
	}
	// Pre-SCC: TotalJobCost 2, TotalBuildCost 102. This reaction uses EIV 10
	// (adjustedPrice-weighted material total), SCC = 10 × 4% = 0.4.
	if !industryAlmostEqual(result.TotalBuildCost, 102.4) {
		t.Fatalf("TotalBuildCost = %v, want 102.4", result.TotalBuildCost)
	}
	if !industryAlmostEqual(result.TotalJobCost, 2.4) {
		t.Fatalf("TotalJobCost = %v, want reaction-index job cost 2.4", result.TotalJobCost)
	}
	if len(result.FlatMaterials) != 1 || result.FlatMaterials[0].TypeID != 34 || result.FlatMaterials[0].Quantity != 10 {
		t.Fatalf("flat materials = %+v, want 10 Tritanium", result.FlatMaterials)
	}
	if len(result.ActivityPlan) != 1 || result.ActivityPlan[0].Activity != "reaction" {
		t.Fatalf("activity plan = %+v, want one reaction step", result.ActivityPlan)
	}
}

func TestAnalyze_InventionAddsExpectedBPCCost(t *testing.T) {
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
	a := &IndustryAnalyzer{
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

	result, err := a.Analyze(IndustryParams{
		TypeID:       5000,
		Runs:         20,
		ActivityMode: "invention",
		SystemID:     30000142,
	}, func(string) {})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !industryAlmostEqual(result.InventionAttempts, 5) {
		t.Fatalf("InventionAttempts = %v, want 5", result.InventionAttempts)
	}
	if !industryAlmostEqual(result.InventionProbability, 0.4) {
		t.Fatalf("InventionProbability = %v, want 0.4", result.InventionProbability)
	}
	// Pre-SCC: InventionCost 1050. New: +20 SCC (invention EIV 500 × 4% =
	// 20, × expected attempts). Build side may also gain SCC — verify via
	// OptimalBuildCost update.
	if !industryAlmostEqual(result.InventionCost, 1070) {
		t.Fatalf("InventionCost = %v, want 1070", result.InventionCost)
	}
	if !industryAlmostEqual(result.OptimalBuildCost, 2078) {
		t.Fatalf("OptimalBuildCost = %v, want build 1008 (+8 SCC) + invention 1070", result.OptimalBuildCost)
	}
	if len(result.ActivityPlan) < 2 || result.ActivityPlan[0].Activity != "invention" || result.ActivityPlan[1].Activity != "manufacturing" {
		t.Fatalf("activity plan = %+v, want invention then manufacturing", result.ActivityPlan)
	}
}

func TestAnalyze_TypeNotFound(t *testing.T) {
	a := &IndustryAnalyzer{
		SDE: &sde.Data{
			Types: map[int32]*sde.ItemType{},
		},
	}

	_, err := a.Analyze(IndustryParams{TypeID: 999999}, func(string) {})
	if err == nil {
		t.Fatalf("Analyze should fail for unknown type")
	}
}

func industryAlmostEqual(got, want float64) bool {
	return math.Abs(got-want) < 0.000001
}

// The three tests below pin down the per-node ME/TE lookup added to
// buildMaterialTree. Before this change, params.MaterialEfficiency for the
// top-level BP cascaded to every sub-node — an analysis with top-level
// ME=0 would compute the Build Component sub-node's material cost using
// ME=0 even if the user actually owned an ME=10 Build Component BPO. That
// inflated the sub-node's BuildCost and tipped the analyzer to "buy" for
// T2 components (Plasma Thrusters in the original bug report). With
// OwnedBlueprints populated, each sub-node uses ITS OWN blueprint's ME;
// sub-nodes with no owned BP get marked base so the analyzer doesn't
// recommend building something the user can't actually build.

func TestBuildMaterialTree_OwnedBlueprintOverridesCascadedME(t *testing.T) {
	sdeData := newTestIndustrySDE()
	a := &IndustryAnalyzer{SDE: sdeData}

	// Top-level ME=0 — with the OLD cascade this would demand
	// ceil(3 × 10 × 1.0) = 30 tritanium for the 10 Build Components
	// (10 runs of BP 2001, each needing 3 trit at ME=0). With the fix
	// and OwnedBlueprints saying Build Component's BPO is ME=10, we
	// should get ceil(3 × 10 × 0.90) = 27.
	params := IndustryParams{
		TypeID:             1000,
		Runs:               1,
		MaterialEfficiency: 0,
		MaxDepth:           10,
		OwnedBlueprints: map[int32]OwnedBlueprint{
			// Root's own ME/TE still comes from params — this entry
			// is deliberately not for typeID 1000. Sub-node 1001
			// (Build Component) gets ME=10 from here.
			1001: {ME: 10, TE: 20},
		},
	}
	tree := a.buildMaterialTree(1000, 1, params, 0)
	if tree.IsBase {
		t.Fatal("root should not be marked base")
	}
	// Find the Build Component sub-node.
	var comp *MaterialNode
	for _, child := range tree.Children {
		if child.TypeID == 1001 {
			comp = child
		}
	}
	if comp == nil {
		t.Fatal("Build Component sub-node not found")
	}
	if comp.Blueprint == nil {
		t.Fatal("Build Component should have Blueprint info (buildable)")
	}
	if comp.Blueprint.ME != 10 {
		t.Fatalf("Build Component blueprint ME = %d, want 10 (from OwnedBlueprints, not the cascade of root's ME=0)", comp.Blueprint.ME)
	}
	// The material demand under Build Component should reflect ME=10.
	var trit *MaterialNode
	for _, child := range comp.Children {
		if child.TypeID == 34 {
			trit = child
		}
	}
	if trit == nil {
		t.Fatal("Tritanium leaf under Build Component not found")
	}
	// 10 runs × 3 base × (1 - 0.10) = 27.
	if trit.Quantity != 27 {
		t.Fatalf("Tritanium demand = %d, want 27 (ME=10 applied to Build Component's own blueprint)", trit.Quantity)
	}
}

func TestBuildMaterialTree_SubNodeMissingFromOwnedBlueprintsIsBuyOnly(t *testing.T) {
	sdeData := newTestIndustrySDE()
	a := &IndustryAnalyzer{
		SDE: sdeData,
		// Give Build Component a market price so the "buy" fallback has
		// a positive number to record — mirrors ordinary market state.
		marketPrices: map[int32]float64{1001: 100.0},
	}

	// OwnedBlueprints is set (non-nil) but doesn't contain Build Component
	// (typeID 1001). That models "user opted into owned-BP awareness but
	// doesn't own a Plasma Thruster BPO." The analyzer must mark 1001 as
	// base and refuse to recommend building it.
	params := IndustryParams{
		TypeID:             1000,
		Runs:               1,
		MaterialEfficiency: 10,
		MaxDepth:           10,
		OwnedBlueprints: map[int32]OwnedBlueprint{
			1000: {ME: 10, TE: 20}, // root only
		},
	}
	tree := a.buildMaterialTree(1000, 1, params, 0)

	var comp *MaterialNode
	for _, child := range tree.Children {
		if child.TypeID == 1001 {
			comp = child
		}
	}
	if comp == nil {
		t.Fatal("Build Component sub-node missing")
	}
	if !comp.IsBase {
		t.Fatalf("Build Component should be marked base when user has no BP for it (OwnedBlueprints set, entry absent); IsBase=%v", comp.IsBase)
	}
	if len(comp.Children) != 0 {
		t.Fatalf("base sub-node should not have recursed into children; got %d", len(comp.Children))
	}
	if comp.Blueprint != nil {
		t.Fatalf("base sub-node should not carry Blueprint info; got %+v", comp.Blueprint)
	}
}

// The next block of tests pins down the batch-overshoot guard added to
// buildMaterialTree. Reactions like Ferrogel produce 400 units per run
// and Fernite Carbide 10,000 per run — recursing to build 1 Ferrogel or
// 12 Fernite Carbide against a full-batch cost was the doubling bug from
// the Plasma Thruster incident. The guard forces base (buy-only) on
// sub-nodes that would consume less than batchUtilizationThreshold of a
// batch; root and BuildMode=build_all are exempt.

func newBatchOvershootSDE(t *testing.T) *sde.Data {
	t.Helper()
	ind := sde.NewIndustryData()

	// Root product 5000 built from 1 of typeID 5001 per run.
	ind.Blueprints[6000] = &sde.Blueprint{
		BlueprintTypeID: 6000,
		ProductTypeID:   5000,
		ProductQuantity: 1,
		Time:            3600,
		Materials: []sde.BlueprintMaterial{
			{TypeID: 5001, Quantity: 1},
		},
	}
	ind.Blueprints[6000].Activities = map[string]*sde.ActivityData{
		"manufacturing": {
			Materials: []sde.BlueprintMaterial{{TypeID: 5001, Quantity: 1}},
			Products:  []sde.BlueprintProduct{{TypeID: 5000, Quantity: 1}},
			Time:      3600,
		},
	}
	ind.ProductToBlueprint[5000] = 6000

	// Sub-component 5001 is a "reaction" that emits 400 units per run
	// (mirrors Ferrogel — need 1, batch of 400). Base material 34 is
	// the raw input; 5 per reaction run.
	ind.Blueprints[6001] = &sde.Blueprint{
		BlueprintTypeID: 6001,
		ProductTypeID:   5001,
		ProductQuantity: 400,
		Time:            10800,
	}
	ind.Blueprints[6001].Activities = map[string]*sde.ActivityData{
		"reaction": {
			Materials: []sde.BlueprintMaterial{{TypeID: 34, Quantity: 5}},
			Products:  []sde.BlueprintProduct{{TypeID: 5001, Quantity: 400}},
			Time:      10800,
		},
	}
	ind.ProductToBlueprint[5001] = 6001

	return &sde.Data{
		Types: map[int32]*sde.ItemType{
			34:   {ID: 34, Name: "Tritanium", Volume: 0.01},
			5000: {ID: 5000, Name: "Root Product", Volume: 5},
			5001: {ID: 5001, Name: "Batch Reaction Product", Volume: 1},
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

func TestBuildMaterialTree_SubNodeBelowBatchThresholdIsBuyOnly(t *testing.T) {
	// The parent needs 1 of typeID 5001, whose reaction batch produces 400.
	// Utilization = 1/400 = 0.25% << 50% threshold → sub-node must be base.
	// Without this guard the analyzer would try to model firing a full
	// reaction (cost of 5 tritanium + job) against a 1-unit demand.
	sdeData := newBatchOvershootSDE(t)
	a := &IndustryAnalyzer{
		SDE:          sdeData,
		marketPrices: map[int32]float64{5001: 25.0, 34: 1.0},
	}

	tree := a.buildMaterialTree(5000, 1, IndustryParams{
		TypeID:   5000,
		Runs:     1,
		MaxDepth: 10,
	}, 0)

	if len(tree.Children) != 1 || tree.Children[0].TypeID != 5001 {
		t.Fatalf("expected one child typeID=5001, got %+v", tree.Children)
	}
	child := tree.Children[0]
	if !child.IsBase {
		t.Fatalf("sub-node should be marked base by batch guard (1 needed / 400 batch = 0.25%%); IsBase=%v", child.IsBase)
	}
	if len(child.Children) != 0 {
		t.Fatalf("base sub-node must not recurse; got %d children", len(child.Children))
	}
	if child.Blueprint != nil {
		t.Fatalf("base sub-node must not carry Blueprint info; got %+v", child.Blueprint)
	}
}

func TestBuildMaterialTree_SubNodeAtOrAboveBatchThresholdRecurses(t *testing.T) {
	// At exactly 50% utilization (200 needed / 400 batch), the guard must
	// let the sub-node recurse — otherwise legitimate half-batch builds
	// get force-bought too. Half a batch is the intentional lower edge.
	sdeData := newBatchOvershootSDE(t)
	a := &IndustryAnalyzer{
		SDE:          sdeData,
		marketPrices: map[int32]float64{5001: 25.0, 34: 1.0},
	}

	// To make the root need 200 sub-components we set the root recipe to
	// require 200 of typeID 5001 per run. Mutating the fixture BP in-place
	// keeps the sub-tree geometry identical elsewhere.
	sdeData.Industry.Blueprints[6000].Materials[0].Quantity = 200
	sdeData.Industry.Blueprints[6000].Activities["manufacturing"].Materials[0].Quantity = 200

	tree := a.buildMaterialTree(5000, 1, IndustryParams{
		TypeID:   5000,
		Runs:     1,
		MaxDepth: 10,
	}, 0)

	child := tree.Children[0]
	if child.IsBase {
		t.Fatalf("sub-node at 200/400 = 50%% utilization should recurse, not be forced base")
	}
	if child.Blueprint == nil {
		t.Fatalf("sub-node should carry Blueprint info after recursion")
	}
	// The reaction leaf (tritanium) must appear underneath.
	if len(child.Children) == 0 {
		t.Fatalf("expected reaction inputs recursed under sub-node; got 0 grandchildren")
	}
}

func TestBuildMaterialTree_RootExemptFromBatchThreshold(t *testing.T) {
	// Root is always analyzed — the user explicitly asked to model THAT
	// specific quantity of THIS specific item. Even if root's own recipe
	// would batch-overshoot at this quantity, we still recurse the root's
	// tree. The guard only fires at depth > 0.
	sdeData := newBatchOvershootSDE(t)
	a := &IndustryAnalyzer{
		SDE:          sdeData,
		marketPrices: map[int32]float64{5001: 25.0, 34: 1.0},
	}

	// Ask to analyze 1 unit of the reaction product directly at the root
	// (1/400 = 0.25% utilization). If the guard incorrectly fires at root,
	// there'd be no Blueprint info and no children.
	tree := a.buildMaterialTree(5001, 1, IndustryParams{
		TypeID:   5001,
		Runs:     1,
		MaxDepth: 10,
	}, 0)

	if tree.IsBase {
		t.Fatal("root must never be marked base by the batch guard, regardless of quantity")
	}
	if tree.Blueprint == nil {
		t.Fatal("root should carry Blueprint info even when batch-overshooting")
	}
}

func TestBuildMaterialTree_BuildAllModeBypassesBatchGuard(t *testing.T) {
	// BuildMode=build_all is the escape hatch for users who explicitly
	// want to model firing full reactions even at low utilization (they
	// plan to stockpile the excess, or they're pricing a long-horizon
	// production run). The guard must respect that override.
	sdeData := newBatchOvershootSDE(t)
	a := &IndustryAnalyzer{
		SDE:          sdeData,
		marketPrices: map[int32]float64{5001: 25.0, 34: 1.0},
	}

	tree := a.buildMaterialTree(5000, 1, IndustryParams{
		TypeID:    5000,
		Runs:      1,
		MaxDepth:  10,
		BuildMode: "build_all",
	}, 0)

	child := tree.Children[0]
	if child.IsBase {
		t.Fatalf("build_all must bypass the batch guard; sub-node should recurse")
	}
	if child.Blueprint == nil {
		t.Fatal("expected sub-node to carry Blueprint info under build_all")
	}
}

func TestBuildMaterialTree_LegacyCascadeWhenOwnedBlueprintsNil(t *testing.T) {
	// Backward-compat: when OwnedBlueprints is nil (analyzer callers that
	// haven't opted in — direct Analyze tab, historical scan replays) the
	// tree must behave exactly as before the fix: root ME cascades to every
	// sub-node, no sub-node is arbitrarily marked base for lacking an entry
	// in a map that isn't there.
	sdeData := newTestIndustrySDE()
	a := &IndustryAnalyzer{SDE: sdeData}

	params := IndustryParams{
		TypeID:             1000,
		Runs:               1,
		MaterialEfficiency: 10,
		MaxDepth:           10,
		// OwnedBlueprints deliberately nil.
	}
	tree := a.buildMaterialTree(1000, 1, params, 0)

	var comp *MaterialNode
	for _, child := range tree.Children {
		if child.TypeID == 1001 {
			comp = child
		}
	}
	if comp == nil {
		t.Fatal("Build Component sub-node missing")
	}
	if comp.IsBase {
		t.Fatal("Build Component must NOT be marked base under legacy cascade — OwnedBlueprints is nil")
	}
	if comp.Blueprint == nil || comp.Blueprint.ME != 10 {
		t.Fatalf("Build Component should inherit root ME=10 via legacy cascade; got %+v", comp.Blueprint)
	}
}

func newTestIndustrySDE() *sde.Data {
	ind := sde.NewIndustryData()

	ind.Blueprints[2000] = &sde.Blueprint{
		BlueprintTypeID: 2000,
		ProductTypeID:   1000,
		ProductQuantity: 1,
		Time:            3600,
		Materials: []sde.BlueprintMaterial{
			{TypeID: 1001, Quantity: 10},
			{TypeID: 1002, Quantity: 5},
		},
	}
	ind.ProductToBlueprint[1000] = 2000

	ind.Blueprints[2001] = &sde.Blueprint{
		BlueprintTypeID: 2001,
		ProductTypeID:   1001,
		ProductQuantity: 1,
		Time:            600,
		Materials: []sde.BlueprintMaterial{
			{TypeID: 34, Quantity: 3},
		},
	}
	ind.ProductToBlueprint[1001] = 2001

	return &sde.Data{
		Types: map[int32]*sde.ItemType{
			34:   {ID: 34, Name: "Tritanium", Volume: 0.01},
			1000: {ID: 1000, Name: "Final Item", Volume: 5},
			1001: {ID: 1001, Name: "Build Component", Volume: 1},
			1002: {ID: 1002, Name: "Base Component", Volume: 0.5},
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
