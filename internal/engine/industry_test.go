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
