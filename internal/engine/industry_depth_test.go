package engine

import (
	"testing"

	"eve-flipper/internal/esi"
	"eve-flipper/internal/sde"
)

// The scanner previously reported "+1B profit" on a T2 small rig because
// exactly one seller had listed a moon-price ask. With no other asks
// visible, bestAsk × totalQuantity in the analyzer's revenue math treats
// that lone bait ask as the batch's realizable price — which nobody will
// pay. Book depth is what actually distinguishes "real market at $Y" from
// "one seller at $Y." AskDepthUnits / BidDepthUnits surface that so the
// scanner UI can flag the row and the user can audit instead of chasing
// a phantom profit.

func TestMarketBookDepth_SumsVolumeRemainAcrossOrders(t *testing.T) {
	// Deep ask book (300 units across 3 orders), shallow bid (1 unit).
	// A batch of 100 units would sell fine on the ask side but the bid
	// side can only absorb 1 unit — the mirror of the moon-price case.
	a := &IndustryAnalyzer{
		SDE: &sde.Data{},
		marketSellOrders: map[int32][]esi.MarketOrder{
			1000: {
				{TypeID: 1000, Price: 100, VolumeRemain: 100},
				{TypeID: 1000, Price: 110, VolumeRemain: 150},
				{TypeID: 1000, Price: 120, VolumeRemain: 50},
			},
		},
		marketBuyOrders: map[int32][]esi.MarketOrder{
			1000: {
				{TypeID: 1000, Price: 90, VolumeRemain: 1, IsBuyOrder: true},
			},
		},
	}
	ask, bid := a.marketBookDepth(1000)
	if ask != 300 {
		t.Errorf("ask depth = %d, want 300 (100+150+50)", ask)
	}
	if bid != 1 {
		t.Errorf("bid depth = %d, want 1", bid)
	}
}

func TestMarketBookDepth_IgnoresZeroPriceAndZeroVolume(t *testing.T) {
	// Live ESI occasionally returns stubs with 0 price or 0 volume
	// (partial-fill echoes, hydration glitches). marketBookDepth must
	// not count them; otherwise the "shallow book" flag misfires.
	a := &IndustryAnalyzer{
		SDE: &sde.Data{},
		marketSellOrders: map[int32][]esi.MarketOrder{
			1000: {
				{TypeID: 1000, Price: 100, VolumeRemain: 5},
				{TypeID: 1000, Price: 0, VolumeRemain: 100},  // stub
				{TypeID: 1000, Price: 200, VolumeRemain: 0}, // fully drained
			},
		},
	}
	ask, _ := a.marketBookDepth(1000)
	if ask != 5 {
		t.Errorf("ask depth = %d, want 5 (only the valid order)", ask)
	}
}

func TestMarketBookOrderCounts_CountsDistinctValidOrders(t *testing.T) {
	// Order count is distinct from unit depth: 300 units across 3 orders
	// vs 300 units in 1 order is the difference between "real market"
	// and "one seller who could pull anytime." Both signals show up
	// side-by-side in the scanner tooltip ("300 units across 3 sell
	// orders") so the user can distinguish them without opening the
	// in-game market window.
	a := &IndustryAnalyzer{
		SDE: &sde.Data{},
		marketSellOrders: map[int32][]esi.MarketOrder{
			1000: {
				{TypeID: 1000, Price: 100, VolumeRemain: 100},
				{TypeID: 1000, Price: 110, VolumeRemain: 150},
				{TypeID: 1000, Price: 120, VolumeRemain: 50},
				// Same-price stubs get counted separately if they came
				// from different sellers — the ESI feed already returns
				// them as distinct orders.
				{TypeID: 1000, Price: 0, VolumeRemain: 100},  // filtered
				{TypeID: 1000, Price: 200, VolumeRemain: 0}, // filtered
			},
		},
		marketBuyOrders: map[int32][]esi.MarketOrder{
			1000: {
				{TypeID: 1000, Price: 90, VolumeRemain: 5, IsBuyOrder: true},
				{TypeID: 1000, Price: 85, VolumeRemain: 10, IsBuyOrder: true},
			},
		},
	}
	askOrders, bidOrders := a.marketBookOrderCounts(1000)
	if askOrders != 3 {
		t.Errorf("askOrders = %d, want 3 (invalid stubs filtered)", askOrders)
	}
	if bidOrders != 2 {
		t.Errorf("bidOrders = %d, want 2", bidOrders)
	}
}

func TestMarketBookDepth_UnknownTypeReturnsZero(t *testing.T) {
	// Sub-materials that don't appear in the pricing region (some obscure
	// moon products) legitimately have zero depth. Returning 0 is the
	// signal the frontend consumes to render "—" in the depth line.
	a := &IndustryAnalyzer{
		SDE:              &sde.Data{},
		marketSellOrders: map[int32][]esi.MarketOrder{},
		marketBuyOrders:  map[int32][]esi.MarketOrder{},
	}
	ask, bid := a.marketBookDepth(9999)
	if ask != 0 || bid != 0 {
		t.Errorf("depths = (%d, %d), want (0, 0) for unknown type", ask, bid)
	}
}

func TestAnalyze_PopulatesAskAndBidDepthOnResult(t *testing.T) {
	// End-to-end: given a book with distinct ask + bid depth, the
	// AskDepthUnits / BidDepthUnits fields on IndustryAnalysis must
	// reflect the fixture. If a future refactor forgets to copy them,
	// the scanner UI silently loses the shallow-book warning and the
	// "+1B on a T2 rig" class of bug slips through again.
	sdeData := newTestIndustrySDE()
	a := &IndustryAnalyzer{
		SDE:           sdeData,
		IndustryCache: esi.NewIndustryCache(),
		getAllAdjustedPrices: func(_ *esi.IndustryCache) (map[int32]float64, error) {
			return map[int32]float64{34: 1.0, 1001: 2.0, 1002: 3.0}, nil
		},
		getSystemCostIndex: func(_ *esi.IndustryCache, _ int32) (*esi.SystemCostIndices, error) {
			return &esi.SystemCostIndices{Manufacturing: 0.1}, nil
		},
		fetchMarketPricesFn: func(_ IndustryParams) (map[int32]float64, error) {
			return map[int32]float64{34: 1.0, 1000: 300.0, 1001: 20.0, 1002: 15.0}, nil
		},
		fetchMarketBooksFn: func(_ IndustryParams) (map[int32][]esi.MarketOrder, map[int32][]esi.MarketOrder, error) {
			return map[int32][]esi.MarketOrder{
					// One lone bait seller at a garbage price — mimics the
					// T2-rig-at-moon-price scenario the user reported.
					1000: {
						{TypeID: 1000, Price: 100_000_000, VolumeRemain: 1},
					},
					34:   {{TypeID: 34, Price: 1, VolumeRemain: 1000}},
					1001: {{TypeID: 1001, Price: 20, VolumeRemain: 1000}},
					1002: {{TypeID: 1002, Price: 15, VolumeRemain: 1000}},
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
		TypeID:   1000,
		Runs:     10, // batch far exceeds the 1-unit ask depth: shallow-book case
		SystemID: 30000142,
		MaterialEfficiency: 0,
	}, func(string) {})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if result.AskDepthUnits != 1 {
		t.Errorf("AskDepthUnits = %d, want 1 (the one bait seller)", result.AskDepthUnits)
	}
	if result.BidDepthUnits != 2 {
		t.Errorf("BidDepthUnits = %d, want 2", result.BidDepthUnits)
	}
	// Order counts must also flow through the analyzer's result. The
	// scanner tooltip surfaces "N units across M sell orders" and
	// silently loses the second half if the count fields aren't
	// populated end-to-end.
	if result.AskOrdersCount != 1 {
		t.Errorf("AskOrdersCount = %d, want 1", result.AskOrdersCount)
	}
	if result.BidOrdersCount != 1 {
		t.Errorf("BidOrdersCount = %d, want 1", result.BidOrdersCount)
	}
	// UnitAskPrice must be the bait ask; the frontend consumes this alongside
	// AskDepthUnits to render the "⚠ shallow book" flag. If ask price is 0
	// but depth is 1, the flag logic will still fire and the tooltip is
	// still informative — but this test locks in that both fields come
	// through together.
	if result.UnitAskPrice <= 0 {
		t.Errorf("UnitAskPrice = %v, want > 0", result.UnitAskPrice)
	}
	// The naive Profit math (Profit = bestAsk × qty − cost) is high because
	// of the moon-price ask. That's exactly the number the user was seeing
	// in the wild. Depth is what tells the UI it's fantasy.
	if result.Profit <= 0 {
		t.Errorf("Profit should be positive under the naive bait-ask model; got %v", result.Profit)
	}
}
