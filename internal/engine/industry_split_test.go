package engine

import (
	"testing"

	"eve-flipper/internal/esi"
	"eve-flipper/internal/sde"
)

// The split-the-stack pass added a mixed buy+build strategy per material
// node. Old behavior was binary: either all-buy at the walked market cost
// or all-build via the BOM. When the sell-order book has a cheap head then
// jumps expensive, neither extreme is optimal — the analyzer over-estimated
// cost and under-stated ROI, and the shopping list asked for raw materials
// covering units that should have been sourced from the cheap head. These
// tests pin down the new behavior end-to-end.

func newSplitSDE() *sde.Data {
	ind := sde.NewIndustryData()
	// Root product 7000 built from 100 of typeID 7001 per run.
	ind.Blueprints[8000] = &sde.Blueprint{
		BlueprintTypeID: 8000,
		ProductTypeID:   7000,
		ProductQuantity: 1,
		Time:            3600,
		Materials:       []sde.BlueprintMaterial{{TypeID: 7001, Quantity: 100}},
		Activities: map[string]*sde.ActivityData{
			"manufacturing": {
				Materials: []sde.BlueprintMaterial{{TypeID: 7001, Quantity: 100}},
				Products:  []sde.BlueprintProduct{{TypeID: 7000, Quantity: 1}},
				Time:      3600,
			},
		},
	}
	ind.ProductToBlueprint[7000] = 8000

	// Sub-component 7001 is buildable from typeID 34 (Tritanium).
	// 5 tritanium per run → each 7001 costs ~5 ISK in materials at 1
	// ISK/trit → per-unit build cost ~5 ISK plus tiny job cost.
	ind.Blueprints[8001] = &sde.Blueprint{
		BlueprintTypeID: 8001,
		ProductTypeID:   7001,
		ProductQuantity: 1,
		Time:            60,
		Materials:       []sde.BlueprintMaterial{{TypeID: 34, Quantity: 5}},
		Activities: map[string]*sde.ActivityData{
			"manufacturing": {
				Materials: []sde.BlueprintMaterial{{TypeID: 34, Quantity: 5}},
				Products:  []sde.BlueprintProduct{{TypeID: 7001, Quantity: 1}},
				Time:      60,
			},
		},
	}
	ind.ProductToBlueprint[7001] = 8001

	return &sde.Data{
		Types: map[int32]*sde.ItemType{
			34:   {ID: 34, Name: "Tritanium", Volume: 0.01},
			7000: {ID: 7000, Name: "Root Product", Volume: 5},
			7001: {ID: 7001, Name: "Sub Component", Volume: 1},
		},
		Systems: map[int32]*sde.SolarSystem{
			30000142: {ID: 30000142, Name: "Jita", RegionID: 10000002},
		},
		Regions:  map[int32]*sde.Region{10000002: {ID: 10000002, Name: "The Forge"}},
		Stations: map[int64]*sde.Station{},
		Industry: ind,
	}
}

func TestComputeMixedMaterialCost_CheapHeadThenExpensive(t *testing.T) {
	// The canonical scenario: need 100 units, 30 units cheap on-market
	// (below build cost) and 70 units expensive (above). Walk should
	// buy 30 and stop at the price jump.
	orders := []esi.MarketOrder{
		{Price: 5, VolumeRemain: 30},
		{Price: 20, VolumeRemain: 70},
	}
	buyUnits, buyCost := computeMixedMaterialCost(orders, 100, 7.0)
	if buyUnits != 30 {
		t.Errorf("buyUnits = %d, want 30 (only cheap head under 7 ISK/unit)", buyUnits)
	}
	if buyCost != 150 {
		t.Errorf("buyCost = %v, want 150 (30 × 5)", buyCost)
	}
}

func TestComputeMixedMaterialCost_AllCheap(t *testing.T) {
	// When the entire book beats per-unit build cost, the walker fills
	// the batch at market. Caller (calculateCosts) then falls through
	// to the legacy binary compare — the mixed path isn't the winner
	// per se, all-buy is.
	orders := []esi.MarketOrder{
		{Price: 3, VolumeRemain: 50},
		{Price: 4, VolumeRemain: 100},
	}
	buyUnits, buyCost := computeMixedMaterialCost(orders, 80, 10.0)
	if buyUnits != 80 {
		t.Errorf("buyUnits = %d, want 80 (all under build cost)", buyUnits)
	}
	// 50 × 3 + 30 × 4 = 150 + 120 = 270
	if buyCost != 270 {
		t.Errorf("buyCost = %v, want 270", buyCost)
	}
}

func TestComputeMixedMaterialCost_NothingCheap(t *testing.T) {
	// When even the cheapest sell order is above per-unit build cost,
	// the mixed strategy yields nothing — build wins the binary compare.
	orders := []esi.MarketOrder{
		{Price: 10, VolumeRemain: 50},
		{Price: 20, VolumeRemain: 50},
	}
	buyUnits, buyCost := computeMixedMaterialCost(orders, 100, 5.0)
	if buyUnits != 0 || buyCost != 0 {
		t.Errorf("expected zero split; got buyUnits=%d buyCost=%v", buyUnits, buyCost)
	}
}

func TestComputeMixedMaterialCost_UnsortedOrders(t *testing.T) {
	// ESI doesn't guarantee sell-order order; the helper must sort so
	// the walk consumes cheapest-first regardless of input order.
	orders := []esi.MarketOrder{
		{Price: 20, VolumeRemain: 70},
		{Price: 5, VolumeRemain: 30},
	}
	buyUnits, buyCost := computeMixedMaterialCost(orders, 100, 10.0)
	if buyUnits != 30 || buyCost != 150 {
		t.Errorf("sort broken: buyUnits=%d buyCost=%v", buyUnits, buyCost)
	}
}

func TestComputeMixedMaterialCost_IgnoresInvalidOrders(t *testing.T) {
	// Zero-price / zero-volume stubs occasionally show up in the ESI
	// feed. Filter them so they don't contaminate the walk.
	orders := []esi.MarketOrder{
		{Price: 0, VolumeRemain: 50},
		{Price: 5, VolumeRemain: 0},
		{Price: 5, VolumeRemain: 30},
	}
	buyUnits, buyCost := computeMixedMaterialCost(orders, 50, 10.0)
	if buyUnits != 30 || buyCost != 150 {
		t.Errorf("stub filter broken: buyUnits=%d buyCost=%v", buyUnits, buyCost)
	}
}

func TestCalculateCosts_PicksSplitWhenItBeatsBothExtremes(t *testing.T) {
	// End-to-end via the analyzer. Root needs 100 of sub-component 7001.
	// Book: 30 units at 2 ISK each (well under per-unit build cost of ~5)
	// then 70 units at 20 ISK each (well above).
	//   All-buy walked cost:    30×2 + 70×20 = 60 + 1400 = 1460
	//   All-build cost (100):   ~500 + tiny job cost ≈ 500-something
	//   Split cost:             60 (buy 30) + 350 (build 70) = ~410
	// Split must win.
	sdeData := newSplitSDE()
	a := &IndustryAnalyzer{
		SDE:           sdeData,
		IndustryCache: esi.NewIndustryCache(),
		getAllAdjustedPrices: func(_ *esi.IndustryCache) (map[int32]float64, error) {
			return map[int32]float64{34: 1.0, 7000: 100.0, 7001: 10.0}, nil
		},
		getSystemCostIndex: func(_ *esi.IndustryCache, _ int32) (*esi.SystemCostIndices, error) {
			return &esi.SystemCostIndices{Manufacturing: 0.0}, nil
		},
		fetchMarketPricesFn: func(_ IndustryParams) (map[int32]float64, error) {
			// Best sell order = cheapest ask. For 7001 that's 2 ISK/unit.
			return map[int32]float64{34: 1.0, 7000: 5000.0, 7001: 2.0}, nil
		},
		fetchMarketBooksFn: func(_ IndustryParams) (map[int32][]esi.MarketOrder, map[int32][]esi.MarketOrder, error) {
			return map[int32][]esi.MarketOrder{
					34:   {{TypeID: 34, Price: 1, VolumeRemain: 10_000}},
					7001: {
						{TypeID: 7001, Price: 2, VolumeRemain: 30},
						{TypeID: 7001, Price: 20, VolumeRemain: 70},
					},
					7000: {{TypeID: 7000, Price: 5000, VolumeRemain: 100}},
				},
				map[int32][]esi.MarketOrder{}, nil
		},
	}

	result, err := a.Analyze(IndustryParams{
		TypeID:             7000,
		Runs:               1,
		SystemID:           30000142,
		MaterialEfficiency: 0,
	}, func(string) {})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	// The sub-component node should be split.
	var sub *MaterialNode
	for _, child := range result.MaterialTree.Children {
		if child.TypeID == 7001 {
			sub = child
		}
	}
	if sub == nil {
		t.Fatal("sub-component node missing")
	}
	if !sub.ShouldSplit {
		t.Fatalf("expected sub-component to use mixed strategy; ShouldSplit=%v ShouldBuild=%v BuyPrice=%v BuildCost=%v",
			sub.ShouldSplit, sub.ShouldBuild, sub.BuyPrice, sub.BuildCost)
	}
	if sub.BuyUnits != 30 {
		t.Errorf("BuyUnits = %d, want 30 (cheap-head units)", sub.BuyUnits)
	}
	if sub.BuildUnits != 70 {
		t.Errorf("BuildUnits = %d, want 70 (remaining)", sub.BuildUnits)
	}
	if sub.BuyPortionCost != 60 {
		t.Errorf("BuyPortionCost = %v, want 60 (30 × 2)", sub.BuyPortionCost)
	}
	// Build portion is pro-rated from the full-quantity BuildCost.
	// BuildCost for 100 units at 5 ISK material each + zero job cost = ~500.
	// 70/100 fraction of that = ~350.
	if sub.BuildPortionCost <= 300 || sub.BuildPortionCost >= 400 {
		t.Errorf("BuildPortionCost = %v, want ~350 (70%% of full build cost)", sub.BuildPortionCost)
	}
}

func TestCalculateCosts_BinaryPathIntactWhenSplitDoesntWin(t *testing.T) {
	// Same setup but sell orders are all cheap → mixed collapses to
	// "everything below per-unit build" → binary all-buy wins.
	sdeData := newSplitSDE()
	a := &IndustryAnalyzer{
		SDE:           sdeData,
		IndustryCache: esi.NewIndustryCache(),
		getAllAdjustedPrices: func(_ *esi.IndustryCache) (map[int32]float64, error) {
			return map[int32]float64{34: 1.0, 7000: 100.0, 7001: 10.0}, nil
		},
		getSystemCostIndex: func(_ *esi.IndustryCache, _ int32) (*esi.SystemCostIndices, error) {
			return &esi.SystemCostIndices{Manufacturing: 0.0}, nil
		},
		fetchMarketPricesFn: func(_ IndustryParams) (map[int32]float64, error) {
			return map[int32]float64{34: 1.0, 7000: 5000.0, 7001: 2.0}, nil
		},
		fetchMarketBooksFn: func(_ IndustryParams) (map[int32][]esi.MarketOrder, map[int32][]esi.MarketOrder, error) {
			return map[int32][]esi.MarketOrder{
					34:   {{TypeID: 34, Price: 1, VolumeRemain: 10_000}},
					7001: {{TypeID: 7001, Price: 2, VolumeRemain: 200}}, // deep and cheap
					7000: {{TypeID: 7000, Price: 5000, VolumeRemain: 100}},
				},
				map[int32][]esi.MarketOrder{}, nil
		},
	}

	result, err := a.Analyze(IndustryParams{TypeID: 7000, Runs: 1, SystemID: 30000142}, func(string) {})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	var sub *MaterialNode
	for _, child := range result.MaterialTree.Children {
		if child.TypeID == 7001 {
			sub = child
		}
	}
	if sub == nil {
		t.Fatal("sub-component node missing")
	}
	if sub.ShouldSplit {
		t.Errorf("expected no split when whole book beats build; ShouldSplit=%v", sub.ShouldSplit)
	}
	if sub.ShouldBuild {
		t.Errorf("expected all-buy when market fully beats build; ShouldBuild=%v", sub.ShouldBuild)
	}
}

func TestFlattenMaterials_SplitAddsBuyPortionAndScalesChildren(t *testing.T) {
	// The shopping list is the pilot's actual multibuy — if the split
	// isn't reflected here, they'll over-buy raw materials. Verify:
	//   - The buy portion of a split node appears with the correct qty.
	//   - Sub-materials for the build portion are scaled by
	//     BuildUnits/Quantity so raw material demand matches what's
	//     actually being built.
	sdeData := newSplitSDE()
	a := &IndustryAnalyzer{
		SDE:           sdeData,
		IndustryCache: esi.NewIndustryCache(),
		getAllAdjustedPrices: func(_ *esi.IndustryCache) (map[int32]float64, error) {
			return map[int32]float64{34: 1.0, 7000: 100.0, 7001: 10.0}, nil
		},
		getSystemCostIndex: func(_ *esi.IndustryCache, _ int32) (*esi.SystemCostIndices, error) {
			return &esi.SystemCostIndices{Manufacturing: 0.0}, nil
		},
		fetchMarketPricesFn: func(_ IndustryParams) (map[int32]float64, error) {
			return map[int32]float64{34: 1.0, 7000: 5000.0, 7001: 2.0}, nil
		},
		fetchMarketBooksFn: func(_ IndustryParams) (map[int32][]esi.MarketOrder, map[int32][]esi.MarketOrder, error) {
			return map[int32][]esi.MarketOrder{
					34:   {{TypeID: 34, Price: 1, VolumeRemain: 10_000}},
					7001: {
						{TypeID: 7001, Price: 2, VolumeRemain: 30},
						{TypeID: 7001, Price: 20, VolumeRemain: 70},
					},
					7000: {{TypeID: 7000, Price: 5000, VolumeRemain: 100}},
				},
				map[int32][]esi.MarketOrder{}, nil
		},
	}
	result, err := a.Analyze(IndustryParams{TypeID: 7000, Runs: 1, SystemID: 30000142}, func(string) {})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	byType := map[int32]*FlatMaterial{}
	for _, m := range result.FlatMaterials {
		byType[m.TypeID] = m
	}

	// The split-node's buy portion (30 units of 7001) must be in the list.
	if byType[7001] == nil {
		t.Fatalf("split node's buy portion (7001) missing from shopping list")
	}
	if byType[7001].Quantity != 30 {
		t.Errorf("split-buy qty for 7001 = %d, want 30", byType[7001].Quantity)
	}
	// The build portion (70 units of 7001) drives 70 × 5 = 350 tritanium,
	// not 500 (the full 100-unit demand). Verify the scaling.
	if byType[34] == nil {
		t.Fatalf("tritanium missing from shopping list")
	}
	if byType[34].Quantity != 350 {
		t.Errorf("tritanium qty = %d, want 350 (70 built × 5 trit/unit — split-scaled)", byType[34].Quantity)
	}
}
