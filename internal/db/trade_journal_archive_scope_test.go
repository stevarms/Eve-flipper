package db

import (
	"testing"
	"time"

	"eve-flipper/internal/esi"
)

// TestListArchivedWalletActivityScopedToCharacters is a regression test for a
// scope filter that silently returned nothing.
//
// buildWalletScopeSQL emitted "character_id IN (?)" but the ids were never
// bound, so every character-scoped read failed with "missing argument with
// index 3" — and each caller (Positions, the Trade Journal, the portfolio
// endpoint) logged that and rendered an empty tab. Only scope=all worked,
// because "1=1" has no placeholders to bind.
func TestListArchivedWalletActivityScopedToCharacters(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	const userID = "scope-user"
	const mine, theirs = int64(9101), int64(9102)

	if _, err := d.UpsertWalletTransactionsForUser(userID, mine, []esi.WalletTransaction{{
		TransactionID: 1, Date: "2026-05-01T10:00:00Z", TypeID: 34,
		LocationID: 60003760, UnitPrice: 5, Quantity: 100, IsBuy: true,
	}}); err != nil {
		t.Fatalf("upsert mine: %v", err)
	}
	if _, err := d.UpsertWalletTransactionsForUser(userID, theirs, []esi.WalletTransaction{{
		TransactionID: 2, Date: "2026-05-02T10:00:00Z", TypeID: 35,
		LocationID: 60008494, UnitPrice: 9, Quantity: 50, IsBuy: true,
	}}); err != nil {
		t.Fatalf("upsert theirs: %v", err)
	}

	scoped, _, err := d.ListArchivedWalletActivityForUser(userID, WalletScopeFilter{
		IncludeCharacters: []int64{mine},
	}, time.Time{})
	if err != nil {
		t.Fatalf("scoped read: %v", err)
	}
	if len(scoped) != 1 {
		t.Fatalf("scoped read returned %d rows, want 1 (the scoped character's): %+v", len(scoped), scoped)
	}
	if scoped[0].CharacterID != mine || scoped[0].WalletKey != "char:9101" {
		t.Errorf("scoped row = %+v, want character %d", scoped[0], mine)
	}

	// A since filter adds a second placeholder after the scope's — the exact
	// ordering the old code got wrong.
	since := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)
	windowed, _, err := d.ListArchivedWalletActivityForUser(userID, WalletScopeFilter{
		IncludeCharacters: []int64{mine, theirs},
	}, since)
	if err != nil {
		t.Fatalf("scoped read with since: %v", err)
	}
	if len(windowed) != 1 || windowed[0].TransactionID != 2 {
		t.Fatalf("since-filtered read = %+v, want only transaction 2", windowed)
	}

	all, _, err := d.ListArchivedWalletActivityForUser(userID, WalletScopeFilter{IncludeAll: true}, time.Time{})
	if err != nil {
		t.Fatalf("all-scope read: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("all-scope read = %d rows, want 2", len(all))
	}
}
