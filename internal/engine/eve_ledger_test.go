package engine

import (
	"testing"
	"time"

	"eve-flipper/internal/esi"
)

func TestComputeEveLedgerDashboard_CashflowCategoriesAndCapital(t *testing.T) {
	now := time.Now().UTC()
	day := func(offset int) string {
		return now.AddDate(0, 0, offset).Format(time.RFC3339)
	}

	result := ComputeEveLedgerDashboard(
		[]esi.WalletJournalEntry{
			{ID: 1, Date: day(-2), RefType: "market_transaction", Amount: 2000},
			{ID: 2, Date: day(-1), RefType: "market_escrow", Amount: -1200},
			{ID: 3, Date: day(-1), RefType: "agent_mission_reward", Amount: 500},
			{ID: 4, Date: day(0), RefType: "contract_price", Amount: -100},
		},
		[]esi.WalletTransaction{
			{TransactionID: 10, Date: day(-2), TypeID: 34, TypeName: "Tritanium", UnitPrice: 100, Quantity: 10, IsBuy: true},
			{TransactionID: 11, Date: day(-1), TypeID: 34, TypeName: "Tritanium", UnitPrice: 150, Quantity: 10, IsBuy: false},
		},
		[]esi.CharacterOrder{
			{OrderID: 20, TypeID: 34, Price: 90, VolumeRemain: 3, IsBuyOrder: true},
			{OrderID: 21, TypeID: 35, Price: 50, VolumeRemain: 4, IsBuyOrder: false},
		},
		[]esi.CharacterAsset{
			{ItemID: 30, TypeID: 34, TypeName: "Tritanium", Quantity: 5},
			{ItemID: 31, TypeID: 35, TypeName: "Pyerite", Quantity: 2},
		},
		map[int32]float64{34: 120, 35: 55},
		10_000,
		EveLedgerOptions{LookbackDays: 7, SalesTaxPercent: 0, BrokerFeePercent: 0},
	)

	if result == nil {
		t.Fatal("expected dashboard")
	}
	if result.Summary.JournalIncomeISK != 2500 {
		t.Fatalf("income = %v, want 2500", result.Summary.JournalIncomeISK)
	}
	if result.Summary.JournalOutgoingISK != 1300 {
		t.Fatalf("outgoing = %v, want 1300", result.Summary.JournalOutgoingISK)
	}
	if result.Summary.TradingCashflowISK != 800 {
		t.Fatalf("trading cashflow = %v, want 800", result.Summary.TradingCashflowISK)
	}
	if result.Summary.OtherNetISK != 400 {
		t.Fatalf("other net = %v, want 400", result.Summary.OtherNetISK)
	}
	if result.Summary.TradingPnLISK != 500 {
		t.Fatalf("trading pnl = %v, want 500", result.Summary.TradingPnLISK)
	}
	if result.Summary.InventoryMTMISK != 710 {
		t.Fatalf("inventory mtm = %v, want 710", result.Summary.InventoryMTMISK)
	}
	if result.Summary.BuyOrdersValueISK != 270 || result.Summary.SellOrdersValueISK != 200 {
		t.Fatalf("order values = buy %v sell %v, want 270/200", result.Summary.BuyOrdersValueISK, result.Summary.SellOrdersValueISK)
	}
	if len(result.Daily) != 7 {
		t.Fatalf("daily len = %d, want 7", len(result.Daily))
	}
	if len(result.Weekly) == 0 || len(result.Monthly) == 0 {
		t.Fatalf("expected weekly and monthly aggregates")
	}

	var market, pve *EveLedgerCategory
	for i := range result.Categories {
		switch result.Categories[i].Key {
		case "market":
			market = &result.Categories[i]
		case "pve":
			pve = &result.Categories[i]
		}
	}
	if market == nil || market.NetISK != 800 || !market.IsTrading {
		t.Fatalf("market category = %+v, want net 800 trading", market)
	}
	if pve == nil || pve.NetISK != 500 || pve.IsTrading {
		t.Fatalf("pve category = %+v, want net 500 non-trading", pve)
	}
}

func TestComputeEveLedgerDashboard_UnrealizedRequiresCostBasis(t *testing.T) {
	result := ComputeEveLedgerDashboard(
		nil,
		nil,
		nil,
		[]esi.CharacterAsset{
			{ItemID: 1, TypeID: 34, TypeName: "Tritanium", Quantity: 10},
		},
		map[int32]float64{34: 100},
		0,
		EveLedgerOptions{LookbackDays: 30},
	)

	if result.Summary.InventoryMTMISK != 1000 {
		t.Fatalf("inventory mtm = %v, want 1000", result.Summary.InventoryMTMISK)
	}
	if result.Summary.UnrealizedPnLISK != 0 {
		t.Fatalf("unrealized without cost basis = %v, want 0", result.Summary.UnrealizedPnLISK)
	}
	if len(result.Inventory) != 1 || result.Inventory[0].UnrealizedPnL != 0 {
		t.Fatalf("inventory unrealized = %+v, want zero", result.Inventory)
	}
}
