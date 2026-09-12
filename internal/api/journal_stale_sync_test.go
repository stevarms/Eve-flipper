package api

import (
	"net/http/httptest"
	"testing"
	"time"

	"eve-flipper/internal/db"
	"eve-flipper/internal/esi"
)

// The Trade Journal auto-syncs only when this function reports something
// stale, and for the entire life of the feature it never did: it looked only
// at transaction_synced_at, which seven unrelated endpoints keep fresh without
// doing any industry or corp work, and it skipped a blank timestamp entirely.
// Manufacturing cost basis and every corp wallet were therefore permanently
// empty. These tests pin both halves of the fix.
func TestWalletMetaFlagsNeverSyncedWallets(t *testing.T) {
	database := openAPITestDB(t)
	s := &Server{db: database}

	const userID = "stale-user"
	const charID = int64(5001)

	// UpsertWalletTransactionsForUser stamps transaction_synced_at but not
	// industry_synced_at — exactly the shape every real wallet is in today.
	if _, err := database.UpsertWalletTransactionsForUser(userID, charID, []esi.WalletTransaction{{
		TransactionID: 1, Date: "2026-05-01T10:00:00Z", TypeID: 34,
		LocationID: 60003760, UnitPrice: 5, Quantity: 10, IsBuy: true,
	}}); err != nil {
		t.Fatalf("seed transactions: %v", err)
	}

	filter := db.WalletScopeFilter{IncludeCharacters: []int64{charID}}
	_, stale := s.walletMetaForFilter(userID, &filter)
	if len(stale) != 1 {
		t.Fatalf("got %d stale entries, want 1 — a wallet with no industry sync is stale: %+v", len(stale), stale)
	}
	if stale[0]["kind"] != "never" {
		t.Errorf("kind = %v, want \"never\" (there is no industry timestamp to age)", stale[0]["kind"])
	}
	if stale[0]["wallet_key"] != "char:5001" {
		t.Errorf("wallet_key = %v, want char:5001", stale[0]["wallet_key"])
	}

	// Once industry has been synced too, nothing is stale.
	if _, err := database.UpsertIndustryJobsForUser(userID, charID, nil); err != nil {
		t.Fatalf("seed industry sync: %v", err)
	}
	if _, stale = s.walletMetaForFilter(userID, &filter); len(stale) != 0 {
		t.Fatalf("both kinds fresh but got %d stale entries: %+v", len(stale), stale)
	}

	// Age only the industry side: the wallet side stays fresh, and the old
	// single-field check would have reported nothing.
	old := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	if _, err := database.SqlDB().Exec(
		`UPDATE wallet_archive_sync SET industry_synced_at = ? WHERE user_id = ? AND character_id = ?`,
		old, userID, charID); err != nil {
		t.Fatalf("age industry sync: %v", err)
	}
	_, stale = s.walletMetaForFilter(userID, &filter)
	if len(stale) != 1 {
		t.Fatalf("got %d stale entries, want 1 for a 30-day-old industry sync: %+v", len(stale), stale)
	}
	if stale[0]["kind"] != "industry" {
		t.Errorf("kind = %v, want \"industry\"", stale[0]["kind"])
	}
	if days, _ := stale[0]["days_ago"].(int); days < 29 {
		t.Errorf("days_ago = %v, want ~30", stale[0]["days_ago"])
	}
}

// A corp division that has never been synced has no sidecar row, so it cannot
// report itself stale — without a synthetic entry the corp archives could
// never bootstrap.
func TestWalletMetaSynthesisesNeverSeenCorp(t *testing.T) {
	database := openAPITestDB(t)
	s := &Server{db: database}

	filter := db.WalletScopeFilter{IncludeAll: true}
	_, stale := s.walletMetaForFilter("corp-bootstrap-user", &filter)
	if len(stale) != 1 || stale[0]["wallet_key"] != "corp:*" {
		t.Fatalf("got %+v, want one synthetic corp:* entry", stale)
	}
	if stale[0]["kind"] != "never" {
		t.Errorf("kind = %v, want \"never\"", stale[0]["kind"])
	}
}

func TestParseOwnerScope(t *testing.T) {
	cases := []struct {
		owner string
		want  ownerScope
	}{
		{"char:42", ownerScope{Characters: []int64{42}}},
		{"characters", ownerScope{AllCharacters: true}},
		{"corp:98000001", ownerScope{Corporations: []int64{98000001}}},
		{"corps", ownerScope{AllCorporations: true}},
		{"all", ownerScope{AllCharacters: true, AllCorporations: true}},
	}
	for _, tc := range cases {
		got, ok, err := parseOwnerScope(httptest.NewRequest("GET", "/api/auth/character?owner="+tc.owner, nil))
		if err != nil || !ok {
			t.Fatalf("owner=%q: ok=%v err=%v", tc.owner, ok, err)
		}
		if got.AllCharacters != tc.want.AllCharacters || got.AllCorporations != tc.want.AllCorporations ||
			len(got.Characters) != len(tc.want.Characters) || len(got.Corporations) != len(tc.want.Corporations) {
			t.Errorf("owner=%q parsed to %+v, want %+v", tc.owner, got, tc.want)
		}
	}

	// Absent means "fall back to parseAuthScope", not "empty selection" —
	// getting that wrong would blank every existing caller's data.
	if _, ok, err := parseOwnerScope(httptest.NewRequest("GET", "/api/auth/character", nil)); ok || err != nil {
		t.Errorf("missing owner: ok=%v err=%v, want false/nil", ok, err)
	}
	for _, bad := range []string{"char:0", "char:abc", "corp:-1", "nonsense"} {
		if _, _, err := parseOwnerScope(httptest.NewRequest("GET", "/api/auth/character?owner="+bad, nil)); err == nil {
			t.Errorf("owner=%q was accepted, want an error", bad)
		}
	}
}
