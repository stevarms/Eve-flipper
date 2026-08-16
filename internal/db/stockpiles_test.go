package db

import (
	"errors"
	"testing"

	"eve-flipper/internal/config"
)

func TestStockpiles_CreateGetListDelete(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	created, err := d.CreateStockpileForUser("alice", config.Stockpile{
		Name:              "Jita build hub",
		Source:            config.StockpileSourceCharacter,
		SourceCharacterID: 90000001,
		StationID:         60003760,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID <= 0 {
		t.Fatalf("expected id, got %d", created.ID)
	}
	if created.CreatedAt == "" || created.UpdatedAt == "" {
		t.Fatalf("expected timestamps, got %+v", created)
	}

	got, err := d.GetStockpileForUser("alice", created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Jita build hub" || got.StationID != 60003760 || got.Source != "character" {
		t.Fatalf("get mismatch: %+v", got)
	}
	if len(got.Items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(got.Items))
	}

	// Isolation: bob sees nothing
	list, err := d.ListStockpilesForUser("bob")
	if err != nil {
		t.Fatalf("list bob: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected bob empty, got %+v", list)
	}
	list, err = d.ListStockpilesForUser("alice")
	if err != nil {
		t.Fatalf("list alice: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("expected alice one stockpile, got %+v", list)
	}

	if err := d.DeleteStockpileForUser("alice", created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := d.GetStockpileForUser("alice", created.ID); !errors.Is(err, ErrStockpileNotFound) {
		t.Fatalf("expected not-found after delete, got %v", err)
	}
}

func TestStockpiles_NameConflict(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	_, err := d.CreateStockpileForUser("alice", config.Stockpile{
		Name: "hub", Source: "character", SourceCharacterID: 1, StationID: 60003760,
	})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err = d.CreateStockpileForUser("alice", config.Stockpile{
		Name: "hub", Source: "character", SourceCharacterID: 1, StationID: 60003760,
	})
	if !errors.Is(err, ErrStockpileNameConflict) {
		t.Fatalf("expected name-conflict, got %v", err)
	}

	// Different user with same name is fine
	if _, err := d.CreateStockpileForUser("bob", config.Stockpile{
		Name: "hub", Source: "character", SourceCharacterID: 2, StationID: 60003760,
	}); err != nil {
		t.Fatalf("bob create with same name should succeed: %v", err)
	}
}

func TestStockpiles_ValidationRejects(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	cases := []struct {
		name string
		in   config.Stockpile
	}{
		{"empty name", config.Stockpile{Source: "character", SourceCharacterID: 1, StationID: 60003760}},
		{"bad source", config.Stockpile{Name: "x", Source: "wallet", SourceCharacterID: 1, StationID: 60003760}},
		{"no station", config.Stockpile{Name: "x", Source: "character", SourceCharacterID: 1}},
		{"character source missing char id", config.Stockpile{Name: "x", Source: "character", StationID: 60003760}},
		{"corp source missing corp id", config.Stockpile{Name: "x", Source: "corporation", StationID: 60003760}},
	}
	for _, tc := range cases {
		if _, err := d.CreateStockpileForUser("alice", tc.in); err == nil {
			t.Errorf("case %q: expected error, got nil", tc.name)
		}
	}
}

func TestStockpiles_UpsertAndReplaceItems(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	sp, err := d.CreateStockpileForUser("alice", config.Stockpile{
		Name: "hub", Source: "character", SourceCharacterID: 1, StationID: 60003760,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Upsert two items
	err = d.UpsertStockpileItemsForUser("alice", sp.ID, []config.StockpileItem{
		{TypeID: 34, TypeName: "Tritanium", ThresholdQty: 1_000_000},
		{TypeID: 35, TypeName: "Pyerite", ThresholdQty: 500_000},
	})
	if err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	got, _ := d.GetStockpileForUser("alice", sp.ID)
	if len(got.Items) != 2 {
		t.Fatalf("want 2 items, got %d", len(got.Items))
	}

	// Upsert overwrites Tritanium and adds Mexallon
	err = d.UpsertStockpileItemsForUser("alice", sp.ID, []config.StockpileItem{
		{TypeID: 34, TypeName: "Tritanium", ThresholdQty: 2_000_000},
		{TypeID: 36, TypeName: "Mexallon", ThresholdQty: 100_000},
	})
	if err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	got, _ = d.GetStockpileForUser("alice", sp.ID)
	if len(got.Items) != 3 {
		t.Fatalf("want 3 items after merge, got %d", len(got.Items))
	}
	byID := map[int32]int64{}
	for _, it := range got.Items {
		byID[it.TypeID] = it.ThresholdQty
	}
	if byID[34] != 2_000_000 {
		t.Fatalf("tritanium threshold not updated: %d", byID[34])
	}
	if byID[35] != 500_000 {
		t.Fatalf("pyerite threshold changed unexpectedly: %d", byID[35])
	}
	if byID[36] != 100_000 {
		t.Fatalf("mexallon missing/wrong: %d", byID[36])
	}

	// Replace wipes 35, keeps only what we send
	err = d.ReplaceStockpileItemsForUser("alice", sp.ID, []config.StockpileItem{
		{TypeID: 34, TypeName: "Tritanium", ThresholdQty: 3_000_000},
	})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, _ = d.GetStockpileForUser("alice", sp.ID)
	if len(got.Items) != 1 || got.Items[0].TypeID != 34 || got.Items[0].ThresholdQty != 3_000_000 {
		t.Fatalf("replace did not wipe/replace: %+v", got.Items)
	}
}

func TestStockpiles_DeleteItem(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	sp, _ := d.CreateStockpileForUser("alice", config.Stockpile{
		Name: "hub", Source: "character", SourceCharacterID: 1, StationID: 60003760,
	})
	_ = d.UpsertStockpileItemsForUser("alice", sp.ID, []config.StockpileItem{
		{TypeID: 34, TypeName: "Tritanium", ThresholdQty: 1000},
		{TypeID: 35, TypeName: "Pyerite", ThresholdQty: 500},
	})
	if err := d.DeleteStockpileItemForUser("alice", sp.ID, 34); err != nil {
		t.Fatalf("delete item: %v", err)
	}
	got, _ := d.GetStockpileForUser("alice", sp.ID)
	if len(got.Items) != 1 || got.Items[0].TypeID != 35 {
		t.Fatalf("expected only pyerite: %+v", got.Items)
	}
}

func TestStockpiles_CascadeDeleteRemovesItems(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	sp, _ := d.CreateStockpileForUser("alice", config.Stockpile{
		Name: "hub", Source: "character", SourceCharacterID: 1, StationID: 60003760,
	})
	_ = d.UpsertStockpileItemsForUser("alice", sp.ID, []config.StockpileItem{
		{TypeID: 34, TypeName: "Tritanium", ThresholdQty: 1000},
	})
	if err := d.DeleteStockpileForUser("alice", sp.ID); err != nil {
		t.Fatalf("delete stockpile: %v", err)
	}
	var count int
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM stockpile_items WHERE stockpile_id = ?`, sp.ID).Scan(&count); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected items cascaded away, got %d", count)
	}
}

func TestStockpiles_UpdateRename(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	sp, _ := d.CreateStockpileForUser("alice", config.Stockpile{
		Name: "hub", Source: "character", SourceCharacterID: 1, StationID: 60003760,
	})
	updated, err := d.UpdateStockpileForUser("alice", sp.ID, config.Stockpile{Name: "renamed hub", StationID: 60008494})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "renamed hub" || updated.StationID != 60008494 {
		t.Fatalf("update did not apply: %+v", updated)
	}
	// Original character id preserved
	if updated.SourceCharacterID != 1 {
		t.Fatalf("expected char id 1 preserved, got %d", updated.SourceCharacterID)
	}
}
