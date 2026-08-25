package engine

import (
	"testing"

	"eve-flipper/internal/esi"
)

func TestBuildStationCommand_ActionSelection(t *testing.T) {
	trades := []StationTrade{
		{
			TypeID:          34,
			StationID:       60003760,
			CTS:             72,
			DailyProfit:     1_000_000,
			MarginPercent:   12,
			ConfidenceLabel: "high",
		},
		{
			TypeID:          35,
			StationID:       60003760,
			CTS:             65,
			DailyProfit:     700_000,
			MarginPercent:   9,
			ConfidenceLabel: "low",
			CI:              40,
		},
		{
			TypeID:            36,
			StationID:         60008494,
			CTS:               55,
			DailyProfit:       -100_000,
			MarginPercent:     3,
			RealMarginPercent: -2,
		},
	}
	activeOrders := []esi.CharacterOrder{
		{TypeID: 35, LocationID: 60003760},
		{TypeID: 36, LocationID: 60008494},
	}
	openPositions := []OpenPosition{
		{TypeID: 34, Quantity: 120},
	}

	got := BuildStationCommand(trades, activeOrders, openPositions, 0)

	if got.Summary.Rows != 3 {
		t.Fatalf("summary rows = %d, want 3", got.Summary.Rows)
	}
	if got.Summary.RepriceCount == 0 {
		t.Fatalf("expected at least one reprice recommendation")
	}
	if got.Summary.CancelCount != 1 {
		t.Fatalf("cancel count = %d, want 1", got.Summary.CancelCount)
	}

	byType := make(map[int32]StationCommandRow)
	for _, row := range got.Rows {
		byType[row.Trade.TypeID] = row
	}

	if row := byType[34]; row.RecommendedAction != StationActionReprice {
		t.Fatalf("type 34 action = %q, want reprice (inventory-aware)", row.RecommendedAction)
	}
	if row := byType[35]; row.RecommendedAction != StationActionReprice {
		t.Fatalf("type 35 action = %q, want reprice", row.RecommendedAction)
	}
	if row := byType[35]; row.ActionReason == "" || row.Priority <= 0 {
		t.Fatalf("type 35 should have non-empty reason and priority, got reason=%q priority=%d", row.ActionReason, row.Priority)
	}
	if row := byType[36]; row.RecommendedAction != StationActionCancel {
		t.Fatalf("type 36 action = %q, want cancel", row.RecommendedAction)
	}
	if row := byType[34]; row.Forecast.DailyVolume.P50 <= 0 {
		t.Fatalf("type 34 forecast daily volume p50 = %v, want > 0", row.Forecast.DailyVolume.P50)
	}
	if row := byType[34]; !(row.Forecast.DailyProfit.P50 >= row.Forecast.DailyProfit.P80 && row.Forecast.DailyProfit.P80 >= row.Forecast.DailyProfit.P95) {
		t.Fatalf("type 34 positive profit forecast should be p50>=p80>=p95, got %v/%v/%v",
			row.Forecast.DailyProfit.P50, row.Forecast.DailyProfit.P80, row.Forecast.DailyProfit.P95)
	}
	if row := byType[36]; !(row.Forecast.DailyProfit.P50 >= row.Forecast.DailyProfit.P80 && row.Forecast.DailyProfit.P80 >= row.Forecast.DailyProfit.P95) {
		t.Fatalf("type 36 negative profit forecast should be p50>=p80>=p95 (more conservative), got %v/%v/%v",
			row.Forecast.DailyProfit.P50, row.Forecast.DailyProfit.P80, row.Forecast.DailyProfit.P95)
	}
	if row := byType[35]; !(row.Forecast.ETADays.P50 <= row.Forecast.ETADays.P80 && row.Forecast.ETADays.P80 <= row.Forecast.ETADays.P95) {
		t.Fatalf("type 35 eta forecast should be p50<=p80<=p95, got %v/%v/%v",
			row.Forecast.ETADays.P50, row.Forecast.ETADays.P80, row.Forecast.ETADays.P95)
	}
}

func TestBuildStationCommand_SortingByPriorityThenScore(t *testing.T) {
	trades := []StationTrade{
		{
			TypeID:            1001,
			StationID:         60000001,
			CTS:               80,
			DailyProfit:       -10,
			RealMarginPercent: -1,
		},
		{
			TypeID:        1002,
			StationID:     60000002,
			CTS:           90,
			DailyProfit:   200,
			MarginPercent: 5,
		},
	}
	activeOrders := []esi.CharacterOrder{
		{TypeID: 1001, LocationID: 60000001},
	}

	got := BuildStationCommand(trades, activeOrders, nil, 0)
	if len(got.Rows) != 2 {
		t.Fatalf("rows len = %d, want 2", len(got.Rows))
	}
	if got.Rows[0].RecommendedAction != StationActionCancel {
		t.Fatalf("first action = %q, want cancel (highest priority)", got.Rows[0].RecommendedAction)
	}
}

// TestBuildStationCommand_PerOrderSuggestion — new in item 2 of the
// order-maintenance roadmap. Verifies that per-order suggestions carry a
// legal 4-sig-fig price and correct position/fee fields.
func TestBuildStationCommand_PerOrderSuggestion(t *testing.T) {
	trades := []StationTrade{
		{
			TypeID:    587,
			StationID: 60003760,
			BuyPrice:  0,
			SellPrice: 99_000_000, // 100M-magnitude sell top-of-book
			CTS:       70,
		},
	}
	// User has a sell listing 1M above the best (2 = not top).
	activeOrders := []esi.CharacterOrder{
		{
			OrderID:      777,
			TypeID:       587,
			LocationID:   60003760,
			Price:        100_000_000,
			VolumeRemain: 1,
			IsBuyOrder:   false,
		},
	}
	got := BuildStationCommand(trades, activeOrders, nil, 1.0 /* 1% broker */)
	if len(got.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(got.Rows))
	}
	row := got.Rows[0]
	if row.SellSuggestion == nil {
		t.Fatal("SellSuggestion is nil; wanted populated per-order recommendation")
	}
	s := row.SellSuggestion
	if s.OrderID != 777 {
		t.Errorf("OrderID = %d, want 777", s.OrderID)
	}
	if s.Position != 2 {
		t.Errorf("Position = %d, want 2 (not top-of-book)", s.Position)
	}
	// Best is 99M, magnitude 7, step 10k → NextSellUndercut → 98,990,000.
	if abs64(s.SuggestedPrice-98_990_000) > 1 {
		t.Errorf("SuggestedPrice = %v, want 98,990,000 (legal 4-sig-fig)", s.SuggestedPrice)
	}
	if s.RelistFeeISK <= 0 {
		t.Errorf("RelistFeeISK = %v, want > 0 with 1%% broker fee", s.RelistFeeISK)
	}
	if !s.WarnUnprofitableRelist {
		// Selling at 98.99M vs current 100M is a per-unit loss; gross gain
		// is negative → net should be negative → warn.
		t.Errorf("WarnUnprofitableRelist = false, want true when net gain is negative")
	}
}

// TestBuildStationCommand_ReconciliationDemotesRepriceToHold — item 3 in
// the order-maintenance track. When a "reprice" recommendation would
// fire but the user's active order is already top-of-book (Position 1),
// the action demotes to "hold" so the recommendation stops nagging
// after the user applies it. Pure function of the current per-order
// snapshot — no persistence needed.
func TestBuildStationCommand_ReconciliationDemotesRepriceToHold(t *testing.T) {
	// Trade profile that would normally trigger a "reprice" verb: user has
	// an active order at station AND the row has queue pressure signals
	// (low confidence / high CI).
	trades := []StationTrade{
		{
			TypeID:          587,
			StationID:       60003760,
			SellPrice:       100_000_000,
			DailyProfit:     500_000,
			ConfidenceLabel: "low",
			CI:              40,
			CTS:             65,
		},
	}
	// User's sell order is already top-of-book (equals best sell).
	activeOrders := []esi.CharacterOrder{
		{
			OrderID:      888,
			TypeID:       587,
			LocationID:   60003760,
			Price:        100_000_000,
			VolumeRemain: 1,
			IsBuyOrder:   false,
		},
	}
	got := BuildStationCommand(trades, activeOrders, nil, 0)
	if len(got.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(got.Rows))
	}
	row := got.Rows[0]
	if row.SellSuggestion == nil || row.SellSuggestion.Position != 1 {
		t.Fatalf("expected SellSuggestion at position 1, got %+v", row.SellSuggestion)
	}
	if row.RecommendedAction != StationActionHold {
		t.Errorf("RecommendedAction = %q, want hold (reprice demoted since already top-of-book)", row.RecommendedAction)
	}
}

// TestBuildStationCommand_PerOrderSuggestion_TopOfBook — when user is
// already best on their side, the suggestion keeps their own price and
// reports Position 1 without triggering the relist-fee warning.
func TestBuildStationCommand_PerOrderSuggestion_TopOfBook(t *testing.T) {
	trades := []StationTrade{
		{
			TypeID:    587,
			StationID: 60003760,
			SellPrice: 100_000_000,
		},
	}
	// User's sell equals the best (top-of-book).
	activeOrders := []esi.CharacterOrder{
		{
			OrderID:      777,
			TypeID:       587,
			LocationID:   60003760,
			Price:        100_000_000,
			VolumeRemain: 3,
			IsBuyOrder:   false,
		},
	}
	got := BuildStationCommand(trades, activeOrders, nil, 1.0)
	if len(got.Rows) != 1 || got.Rows[0].SellSuggestion == nil {
		t.Fatalf("want 1 row with SellSuggestion, got %+v", got)
	}
	s := got.Rows[0].SellSuggestion
	if s.Position != 1 {
		t.Errorf("Position = %d, want 1", s.Position)
	}
	if s.SuggestedPrice != 100_000_000 {
		t.Errorf("SuggestedPrice = %v, want 100M (keep own price)", s.SuggestedPrice)
	}
	if s.WarnUnprofitableRelist {
		t.Error("WarnUnprofitableRelist should be false when at top-of-book")
	}
}

func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
