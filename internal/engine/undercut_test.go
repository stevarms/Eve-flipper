package engine

import (
	"math"
	"testing"

	"eve-flipper/internal/esi"
)

// TestAnalyzeUndercutsWithRelistFee verifies the reprice recommendation
// weighs the CCP modify-order fee against the theoretical price
// improvement. Audit P2.7.
func TestAnalyzeUndercutsWithRelistFee(t *testing.T) {
	// Player sell 100 ISK × 1000; best competitor at 90; suggested 89.99.
	// delta = 10.01, orderValue = 10,010. At 3% broker: fee = 300.3.
	player := []esi.CharacterOrder{
		{OrderID: 100, TypeID: 200, LocationID: 5000, RegionID: 10, Price: 100.0, VolumeRemain: 1000, IsBuyOrder: false},
	}
	regional := []esi.MarketOrder{
		{OrderID: 100, TypeID: 200, LocationID: 5000, Price: 100.0, VolumeRemain: 1000, IsBuyOrder: false},
		{OrderID: 200, TypeID: 200, LocationID: 5000, Price: 90.0, VolumeRemain: 500, IsBuyOrder: false},
	}
	// brokerFeePct=0 → fee fields stay zero (backward-compat).
	base := AnalyzeUndercutsWithRelistFee(player, regional, 0)
	if len(base) != 1 || base[0].ModifyOrderFee != 0 || base[0].NetRelistGainISK != 0 {
		t.Errorf("brokerFeePct=0 must leave fee fields at zero: %+v", base[0])
	}
	withFee := AnalyzeUndercutsWithRelistFee(player, regional, 3.0)
	us := withFee[0]
	wantDelta := 10.01 * 1000
	if math.Abs(us.GrossRelistGainISK-wantDelta) > 1e-6 {
		t.Errorf("GrossRelistGainISK = %v, want %v", us.GrossRelistGainISK, wantDelta)
	}
	wantFee := wantDelta * 0.03
	if math.Abs(us.ModifyOrderFee-wantFee) > 1e-6 {
		t.Errorf("ModifyOrderFee = %v, want %v", us.ModifyOrderFee, wantFee)
	}
	wantNet := wantDelta - wantFee
	if math.Abs(us.NetRelistGainISK-wantNet) > 1e-6 {
		t.Errorf("NetRelistGainISK = %v, want %v", us.NetRelistGainISK, wantNet)
	}
}

// TestAnalyzeUndercutsWithRelistFee_ThinBookHits100ISKFloor is the exact
// scenario the audit called out: 0.01 ISK undercut on a thin book where
// the fee floor eats the gain.
func TestAnalyzeUndercutsWithRelistFee_ThinBookHits100ISKFloor(t *testing.T) {
	// Sell order 100.00 × 10, competitor 99.99. Suggested 99.98. delta 0.02.
	// orderValue = 0.20. Raw broker 0.003 → floor to 100. Net = 0.20 - 100 < 0.
	player := []esi.CharacterOrder{
		{OrderID: 1, TypeID: 300, LocationID: 5000, RegionID: 10, Price: 100.0, VolumeRemain: 10, IsBuyOrder: false},
	}
	regional := []esi.MarketOrder{
		{OrderID: 1, TypeID: 300, LocationID: 5000, Price: 100.0, VolumeRemain: 10, IsBuyOrder: false},
		{OrderID: 2, TypeID: 300, LocationID: 5000, Price: 99.99, VolumeRemain: 5, IsBuyOrder: false},
	}
	res := AnalyzeUndercutsWithRelistFee(player, regional, 1.5)
	us := res[0]
	if us.ModifyOrderFee != MinBrokerFeeISK {
		t.Errorf("ModifyOrderFee = %v, want %v (100 ISK floor)", us.ModifyOrderFee, MinBrokerFeeISK)
	}
	if us.NetRelistGainISK >= 0 {
		t.Errorf("NetRelistGainISK = %v, want negative (relist would lose money)", us.NetRelistGainISK)
	}
}

func TestAnalyzeUndercuts_EmptyOrders(t *testing.T) {
	result := AnalyzeUndercuts(nil, nil)
	if len(result) != 0 {
		t.Errorf("expected empty result, got %d", len(result))
	}
}

func TestAnalyzeUndercuts_NoCompetition(t *testing.T) {
	player := []esi.CharacterOrder{
		{OrderID: 1, TypeID: 100, LocationID: 1000, RegionID: 10, Price: 50.0, VolumeRemain: 10, IsBuyOrder: false},
	}
	// Empty regional book.
	result := AnalyzeUndercuts(player, nil)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	us := result[0]
	if us.OrderID != 1 {
		t.Errorf("OrderID = %d, want 1", us.OrderID)
	}
	if us.Position != 1 {
		t.Errorf("Position = %d, want 1", us.Position)
	}
	if us.TotalOrders != 1 {
		t.Errorf("TotalOrders = %d, want 1", us.TotalOrders)
	}
	if us.UndercutAmount != 0 {
		t.Errorf("UndercutAmount = %v, want 0", us.UndercutAmount)
	}
	if us.SuggestedPrice != 50.0 {
		t.Errorf("SuggestedPrice = %v, want 50", us.SuggestedPrice)
	}
}

func TestAnalyzeUndercuts_SellOrder_Undercut(t *testing.T) {
	player := []esi.CharacterOrder{
		{OrderID: 10, TypeID: 100, LocationID: 1000, RegionID: 10, Price: 100.0, VolumeRemain: 5, IsBuyOrder: false},
	}
	regional := []esi.MarketOrder{
		{OrderID: 10, TypeID: 100, LocationID: 1000, Price: 100.0, VolumeRemain: 5, IsBuyOrder: false},
		{OrderID: 20, TypeID: 100, LocationID: 1000, Price: 90.0, VolumeRemain: 10, IsBuyOrder: false},
		{OrderID: 30, TypeID: 100, LocationID: 1000, Price: 95.0, VolumeRemain: 8, IsBuyOrder: false},
	}
	result := AnalyzeUndercuts(player, regional)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	us := result[0]

	// Cheapest sell is 90.0, player is at 100.0, so position should be 3.
	if us.Position != 3 {
		t.Errorf("Position = %d, want 3", us.Position)
	}
	if us.BestPrice != 90.0 {
		t.Errorf("BestPrice = %v, want 90", us.BestPrice)
	}
	if math.Abs(us.UndercutAmount-10.0) > 1e-9 {
		t.Errorf("UndercutAmount = %v, want 10", us.UndercutAmount)
	}
	if math.Abs(us.UndercutPct-10.0) > 1e-9 {
		t.Errorf("UndercutPct = %v, want 10", us.UndercutPct)
	}
	// Suggested: 90 - 0.01 = 89.99
	if math.Abs(us.SuggestedPrice-89.99) > 1e-9 {
		t.Errorf("SuggestedPrice = %v, want 89.99", us.SuggestedPrice)
	}
	if us.TotalOrders != 3 {
		t.Errorf("TotalOrders = %d, want 3", us.TotalOrders)
	}
}

func TestAnalyzeUndercuts_BuyOrder_Undercut(t *testing.T) {
	player := []esi.CharacterOrder{
		{OrderID: 10, TypeID: 200, LocationID: 2000, RegionID: 20, Price: 50.0, VolumeRemain: 100, IsBuyOrder: true},
	}
	regional := []esi.MarketOrder{
		{OrderID: 10, TypeID: 200, LocationID: 2000, Price: 50.0, VolumeRemain: 100, IsBuyOrder: true},
		{OrderID: 20, TypeID: 200, LocationID: 2000, Price: 55.0, VolumeRemain: 80, IsBuyOrder: true},
	}
	result := AnalyzeUndercuts(player, regional)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	us := result[0]

	// Highest buy is 55.0, player is at 50.0, position = 2.
	if us.Position != 2 {
		t.Errorf("Position = %d, want 2", us.Position)
	}
	if us.BestPrice != 55.0 {
		t.Errorf("BestPrice = %v, want 55", us.BestPrice)
	}
	if math.Abs(us.UndercutAmount-5.0) > 1e-9 {
		t.Errorf("UndercutAmount = %v, want 5", us.UndercutAmount)
	}
	// Suggested: 55 + 0.01 = 55.01
	if math.Abs(us.SuggestedPrice-55.01) > 1e-9 {
		t.Errorf("SuggestedPrice = %v, want 55.01", us.SuggestedPrice)
	}
}

func TestAnalyzeUndercuts_SellOrder_AlreadyBest(t *testing.T) {
	player := []esi.CharacterOrder{
		{OrderID: 10, TypeID: 100, LocationID: 1000, RegionID: 10, Price: 80.0, VolumeRemain: 20, IsBuyOrder: false},
	}
	regional := []esi.MarketOrder{
		{OrderID: 10, TypeID: 100, LocationID: 1000, Price: 80.0, VolumeRemain: 20, IsBuyOrder: false},
		{OrderID: 20, TypeID: 100, LocationID: 1000, Price: 90.0, VolumeRemain: 30, IsBuyOrder: false},
	}
	result := AnalyzeUndercuts(player, regional)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	us := result[0]

	if us.Position != 1 {
		t.Errorf("Position = %d, want 1", us.Position)
	}
	if us.UndercutAmount != 0 {
		t.Errorf("UndercutAmount = %v, want 0", us.UndercutAmount)
	}
	// Suggested = own price when #1.
	if us.SuggestedPrice != 80.0 {
		t.Errorf("SuggestedPrice = %v, want 80", us.SuggestedPrice)
	}
}

func TestAnalyzeUndercuts_MultipleOrders(t *testing.T) {
	player := []esi.CharacterOrder{
		{OrderID: 1, TypeID: 100, LocationID: 1000, RegionID: 10, Price: 100.0, VolumeRemain: 5, IsBuyOrder: false},
		{OrderID: 2, TypeID: 200, LocationID: 1000, RegionID: 10, Price: 50.0, VolumeRemain: 10, IsBuyOrder: true},
	}
	regional := []esi.MarketOrder{
		// Type 100 sell orders
		{OrderID: 1, TypeID: 100, LocationID: 1000, Price: 100.0, VolumeRemain: 5, IsBuyOrder: false},
		{OrderID: 11, TypeID: 100, LocationID: 1000, Price: 95.0, VolumeRemain: 3, IsBuyOrder: false},
		// Type 200 buy orders
		{OrderID: 2, TypeID: 200, LocationID: 1000, Price: 50.0, VolumeRemain: 10, IsBuyOrder: true},
	}
	result := AnalyzeUndercuts(player, regional)
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}

	// Find each by order_id
	var sell, buy *UndercutStatus
	for i := range result {
		if result[i].OrderID == 1 {
			sell = &result[i]
		}
		if result[i].OrderID == 2 {
			buy = &result[i]
		}
	}

	if sell == nil || buy == nil {
		t.Fatal("expected both sell and buy undercut results")
	}

	// Sell order: undercut by 95.0 vs 100.0
	if sell.Position != 2 {
		t.Errorf("Sell Position = %d, want 2", sell.Position)
	}
	// Buy order: already best (only order)
	if buy.Position != 1 {
		t.Errorf("Buy Position = %d, want 1", buy.Position)
	}
}

func TestBuildBookLevels_AggregatesSamePrice(t *testing.T) {
	sorted := []esi.MarketOrder{
		{OrderID: 1, Price: 100.0, VolumeRemain: 50},
		{OrderID: 2, Price: 100.0, VolumeRemain: 30},
		{OrderID: 3, Price: 110.0, VolumeRemain: 20},
	}
	levels := buildBookLevels(sorted, 1, 100.0, 5)
	if len(levels) != 2 {
		t.Fatalf("expected 2 levels, got %d", len(levels))
	}
	if levels[0].Price != 100.0 || levels[0].Volume != 80 {
		t.Errorf("level 0: price=%v volume=%v, want 100/80", levels[0].Price, levels[0].Volume)
	}
	if !levels[0].IsPlayer {
		t.Error("level 0 should be player")
	}
	if levels[1].Price != 110.0 || levels[1].Volume != 20 {
		t.Errorf("level 1: price=%v volume=%v, want 110/20", levels[1].Price, levels[1].Volume)
	}
}

func TestBuildBookLevels_TruncatesToMax(t *testing.T) {
	sorted := []esi.MarketOrder{
		{OrderID: 1, Price: 10.0, VolumeRemain: 1},
		{OrderID: 2, Price: 20.0, VolumeRemain: 2},
		{OrderID: 3, Price: 30.0, VolumeRemain: 3},
		{OrderID: 4, Price: 40.0, VolumeRemain: 4},
		{OrderID: 5, Price: 50.0, VolumeRemain: 5},
		{OrderID: 6, Price: 60.0, VolumeRemain: 6},
	}
	levels := buildBookLevels(sorted, 99, 99.0, 3)
	if len(levels) != 3 {
		t.Errorf("expected 3 levels, got %d", len(levels))
	}
}

func TestBuildBookLevels_IncludesPlayerBeyondMax(t *testing.T) {
	sorted := []esi.MarketOrder{
		{OrderID: 1, Price: 10.0, VolumeRemain: 1},
		{OrderID: 2, Price: 20.0, VolumeRemain: 2},
		{OrderID: 3, Price: 30.0, VolumeRemain: 3},
		{OrderID: 4, Price: 40.0, VolumeRemain: 4},
		{OrderID: 5, Price: 50.0, VolumeRemain: 5}, // player
	}
	levels := buildBookLevels(sorted, 5, 50.0, 3)
	// Should show top 2 + player's level (index 4).
	if len(levels) != 3 {
		t.Fatalf("expected 3 levels, got %d", len(levels))
	}
	foundPlayer := false
	for _, l := range levels {
		if l.IsPlayer {
			foundPlayer = true
			if l.Price != 50.0 {
				t.Errorf("player level price = %v, want 50", l.Price)
			}
		}
	}
	if !foundPlayer {
		t.Error("player level not found in truncated book")
	}
}

// Regression: the pre-fix code used BestPrice ± 0.01 for suggested reprice.
// At high magnitudes that's not a legal EVE price (fails the 4-sig-fig
// rule) and the recommendation couldn't be pasted into the client. Verify
// the new snap-to-grid helpers give a legal price on both sides.
func TestAnalyzeUndercuts_HighPriceSellSnapsToLegalGrid(t *testing.T) {
	player := esi.CharacterOrder{
		OrderID:      1,
		TypeID:       587,
		LocationID:   60003760,
		Price:        100_000_000, // 100M ISK sell — magnitude 8, step 100k
		VolumeRemain: 1,
		IsBuyOrder:   false,
	}
	region := []esi.MarketOrder{
		{OrderID: 2, TypeID: 587, LocationID: 60003760, Price: 99_000_000, VolumeRemain: 1, IsBuyOrder: false},
	}
	got := AnalyzeUndercuts([]esi.CharacterOrder{player}, region)
	if len(got) != 1 {
		t.Fatalf("want 1 result, got %d", len(got))
	}
	// Best sell 99M — magnitude 7, step 10k. On-grid, so step down one
	// place → 98,990,000. Pre-fix would have returned 98,999,999.99 (illegal).
	if math.Abs(got[0].SuggestedPrice-98_990_000) > 1 {
		t.Errorf("SuggestedPrice = %v, want 98,990,000 (legal 4-sig-fig)", got[0].SuggestedPrice)
	}
}

func TestAnalyzeUndercuts_HighPriceBuySnapsToLegalGrid(t *testing.T) {
	player := esi.CharacterOrder{
		OrderID:      1,
		TypeID:       587,
		LocationID:   60003760,
		Price:        100_000_000, // 100M ISK buy
		VolumeRemain: 1,
		IsBuyOrder:   true,
	}
	region := []esi.MarketOrder{
		{OrderID: 2, TypeID: 587, LocationID: 60003760, Price: 101_000_000, VolumeRemain: 1, IsBuyOrder: true},
	}
	got := AnalyzeUndercuts([]esi.CharacterOrder{player}, region)
	if len(got) != 1 {
		t.Fatalf("want 1 result, got %d", len(got))
	}
	// Best is 101M (on-grid at 100k step) → step up one place → 101,100,000
	// Pre-fix would have returned 101,000,000.01 (illegal).
	if math.Abs(got[0].SuggestedPrice-101_100_000) > 1 {
		t.Errorf("SuggestedPrice = %v, want 101,100,000 (legal 4-sig-fig)", got[0].SuggestedPrice)
	}
}
