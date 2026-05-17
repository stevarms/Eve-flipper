package api

import (
	"testing"
	"time"

	"eve-flipper/internal/config"
	"eve-flipper/internal/db"
	"eve-flipper/internal/engine"
)

func TestCheckWatchlistAlertsUsesBestMatchingRow(t *testing.T) {
	database := openAPITestDB(t)
	defer database.Close()

	userID := "watch-user"
	if !database.AddWatchlistItemForUser(userID, config.WatchlistItem{
		TypeID:         34,
		TypeName:       "Tritanium",
		AlertEnabled:   true,
		AlertMetric:    "total_profit",
		AlertThreshold: 1_000,
	}) {
		t.Fatal("AddWatchlistItemForUser returned false")
	}

	srv := NewServer(config.Default(), nil, database, nil, nil)
	alerts := srv.CheckWatchlistAlerts(userID, []engine.FlipResult{
		{TypeID: 34, TypeName: "Tritanium", TotalProfit: 250},
		{TypeID: 34, TypeName: "Tritanium", TotalProfit: 2_500},
	})
	if len(alerts) != 1 {
		t.Fatalf("alerts len = %d, want 1", len(alerts))
	}
	if alerts[0].CurrentValue != 2_500 {
		t.Fatalf("current value = %v, want best row 2500", alerts[0].CurrentValue)
	}
}

func TestCheckWatchlistAlertsCooldownSuppressesRepeat(t *testing.T) {
	database := openAPITestDB(t)
	defer database.Close()

	userID := "watch-user"
	if !database.AddWatchlistItemForUser(userID, config.WatchlistItem{
		TypeID:         35,
		TypeName:       "Pyerite",
		AlertEnabled:   true,
		AlertMetric:    "margin_percent",
		AlertThreshold: 5,
	}) {
		t.Fatal("AddWatchlistItemForUser returned false")
	}
	if err := database.SaveAlertHistoryForUser(userID, db.AlertHistoryEntry{
		WatchlistTypeID: 35,
		TypeName:        "Pyerite",
		AlertMetric:     "margin_percent",
		AlertThreshold:  5,
		CurrentValue:    7,
		Message:         "old",
		SentAt:          time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("SaveAlertHistoryForUser: %v", err)
	}

	srv := NewServer(config.Default(), nil, database, nil, nil)
	alerts := srv.CheckWatchlistAlerts(userID, []engine.FlipResult{
		{TypeID: 35, TypeName: "Pyerite", MarginPercent: 9},
	})
	if len(alerts) != 0 {
		t.Fatalf("alerts len = %d, want cooldown suppression", len(alerts))
	}
}
