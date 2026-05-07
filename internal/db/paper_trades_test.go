package db

import "testing"

func TestPaperTradeCRUDAndPnL(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	trade, err := d.CreatePaperTradeForUser("user-paper", PaperTradeCreateInput{
		TypeID:            34,
		TypeName:          "Tritanium",
		PlannedQuantity:   100,
		PlannedBuyPrice:   5,
		PlannedSellPrice:  7,
		PlannedProfitISK:  190,
		PlannedROIPercent: 38,
		BuyStation:        "Buy Station",
		SellStation:       "Sell Station",
		BuySystemName:     "Jita",
		SellSystemName:    "Amarr",
		BuyRegionID:       10000002,
		SellRegionID:      10000043,
		Source:            "scanner",
	})
	if err != nil {
		t.Fatalf("create paper trade: %v", err)
	}
	if trade.ID <= 0 {
		t.Fatalf("expected id, got %d", trade.ID)
	}
	if trade.Status != PaperTradeStatusPlanned {
		t.Fatalf("status=%q, want planned", trade.Status)
	}
	if trade.ExpectedProfitISK != 190 || trade.ROIPercent != 38 {
		t.Fatalf("expected profit/roi = %.2f/%.2f", trade.ExpectedProfitISK, trade.ROIPercent)
	}

	list, err := d.ListPaperTradesForUser("user-paper", PaperTradeStatusActive, 20)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(list) != 1 || list[0].ID != trade.ID {
		t.Fatalf("active list mismatch: %#v", list)
	}

	listedStatus := PaperTradeStatusListed
	listed, err := d.UpdatePaperTradeForUser("user-paper", trade.ID, PaperTradeUpdateInput{
		Status: &listedStatus,
	})
	if err != nil {
		t.Fatalf("update listed: %v", err)
	}
	if listed.Status != PaperTradeStatusListed || listed.ClosedAt != "" {
		t.Fatalf("listed result = %q/%q, want active listed", listed.Status, listed.ClosedAt)
	}
	list, err = d.ListPaperTradesForUser("user-paper", PaperTradeStatusActive, 20)
	if err != nil {
		t.Fatalf("list active after listed: %v", err)
	}
	if len(list) != 1 || list[0].Status != PaperTradeStatusListed {
		t.Fatalf("listed trade should remain active, got %#v", list)
	}

	status := PaperTradeStatusSold
	actualQty := int64(90)
	actualBuy := 5.1
	actualSell := 6.8
	fees := 12.0
	hauling := 8.0
	updated, err := d.UpdatePaperTradeForUser("user-paper", trade.ID, PaperTradeUpdateInput{
		Status:          &status,
		ActualQuantity:  &actualQty,
		ActualBuyPrice:  &actualBuy,
		ActualSellPrice: &actualSell,
		FeesISK:         &fees,
		HaulingCostISK:  &hauling,
	})
	if err != nil {
		t.Fatalf("update sold: %v", err)
	}
	wantPnL := (actualSell-actualBuy)*float64(actualQty) - fees - hauling
	if updated.Status != PaperTradeStatusSold || updated.RealizedProfitISK != wantPnL {
		t.Fatalf("sold result status/pnl = %q/%.2f, want %.2f", updated.Status, updated.RealizedProfitISK, wantPnL)
	}
	if updated.ClosedAt == "" {
		t.Fatalf("closed_at not set for sold trade")
	}

	reconciledStatus := PaperTradeStatusReconciled
	reconciled, err := d.UpdatePaperTradeForUser("user-paper", trade.ID, PaperTradeUpdateInput{
		Status: &reconciledStatus,
	})
	if err != nil {
		t.Fatalf("update reconciled: %v", err)
	}
	if reconciled.Status != PaperTradeStatusReconciled || reconciled.ClosedAt == "" || reconciled.RealizedProfitISK != wantPnL {
		t.Fatalf("reconciled result status/closed/pnl = %q/%q/%.2f", reconciled.Status, reconciled.ClosedAt, reconciled.RealizedProfitISK)
	}

	active, err := d.ListPaperTradesForUser("user-paper", PaperTradeStatusActive, 20)
	if err != nil {
		t.Fatalf("list active after sold: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("sold trade should not be active, got %d active rows", len(active))
	}

	all, err := d.ListPaperTradesForUser("user-paper", "all", 20)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("all len=%d, want 1", len(all))
	}

	deleted, err := d.DeletePaperTradeForUser("user-paper", trade.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted=%d, want 1", deleted)
	}
}

func TestPaperTradesAreUserScoped(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	_, err := d.CreatePaperTradeForUser("user-a", PaperTradeCreateInput{
		TypeID:          1,
		TypeName:        "Scoped Item",
		PlannedQuantity: 1,
		PlannedBuyPrice: 10,
	})
	if err != nil {
		t.Fatalf("create user-a: %v", err)
	}
	rows, err := d.ListPaperTradesForUser("user-b", "all", 20)
	if err != nil {
		t.Fatalf("list user-b: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected user-b isolation, got %d rows", len(rows))
	}
}
