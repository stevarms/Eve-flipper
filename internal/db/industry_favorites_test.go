package db

import "testing"

func favorite(bpID, productID int32, mode string) IndustryBlueprintFavorite {
	return IndustryBlueprintFavorite{
		BlueprintTypeID: bpID,
		ProductTypeID:   productID,
		ScanMode:        mode,
		BlueprintName:   "Warrior Blueprint",
		ProductName:     "Warrior II",
	}
}

func TestIndustryFavoritesAddListDelete(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	if got := d.GetIndustryFavoritesForUser("user-a"); len(got) != 0 {
		t.Fatalf("fresh user favorites len = %d, want 0", len(got))
	}

	if err := d.AddIndustryFavoriteForUser("user-a", favorite(1000, 2000, "t2_invention")); err != nil {
		t.Fatalf("AddIndustryFavoriteForUser: %v", err)
	}

	got := d.GetIndustryFavoritesForUser("user-a")
	if len(got) != 1 {
		t.Fatalf("favorites len = %d, want 1", len(got))
	}
	if got[0].BlueprintTypeID != 1000 || got[0].ProductTypeID != 2000 || got[0].ScanMode != "t2_invention" {
		t.Fatalf("favorite = %+v, want the row we added", got[0])
	}
	if got[0].AddedAt == "" {
		t.Fatalf("AddedAt not defaulted")
	}

	// Re-starring is idempotent and refreshes display names rather than
	// duplicating the row.
	dup := favorite(1000, 2000, "t2_invention")
	dup.ProductName = "Warrior II (renamed)"
	if err := d.AddIndustryFavoriteForUser("user-a", dup); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	got = d.GetIndustryFavoritesForUser("user-a")
	if len(got) != 1 {
		t.Fatalf("after re-add len = %d, want 1", len(got))
	}
	if got[0].ProductName != "Warrior II (renamed)" {
		t.Fatalf("product name = %q, want refreshed", got[0].ProductName)
	}

	if err := d.DeleteIndustryFavoriteForUser("user-a", 1000, 2000, "t2_invention"); err != nil {
		t.Fatalf("DeleteIndustryFavoriteForUser: %v", err)
	}
	if got := d.GetIndustryFavoritesForUser("user-a"); len(got) != 0 {
		t.Fatalf("after delete len = %d, want 0", len(got))
	}

	// Unstarring something that was never starred is a no-op, not an error:
	// the star is a toggle and the client may hold a stale view.
	if err := d.DeleteIndustryFavoriteForUser("user-a", 1000, 2000, "t2_invention"); err != nil {
		t.Fatalf("delete of missing favorite: %v", err)
	}
}

func TestIndustryFavoritesScopedPerUser(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	if err := d.AddIndustryFavoriteForUser("user-a", favorite(1000, 2000, "t2_invention")); err != nil {
		t.Fatalf("add user-a: %v", err)
	}
	if err := d.AddIndustryFavoriteForUser("user-b", favorite(3000, 4000, "t1_mfg")); err != nil {
		t.Fatalf("add user-b: %v", err)
	}

	a := d.GetIndustryFavoritesForUser("user-a")
	if len(a) != 1 || a[0].BlueprintTypeID != 1000 {
		t.Fatalf("user-a favorites = %+v, want only its own", a)
	}
	b := d.GetIndustryFavoritesForUser("user-b")
	if len(b) != 1 || b[0].BlueprintTypeID != 3000 {
		t.Fatalf("user-b favorites = %+v, want only its own", b)
	}

	// Deleting user-b's row must not touch user-a's.
	if err := d.DeleteIndustryFavoriteForUser("user-b", 3000, 4000, "t1_mfg"); err != nil {
		t.Fatalf("delete user-b: %v", err)
	}
	if len(d.GetIndustryFavoritesForUser("user-a")) != 1 {
		t.Fatalf("user-a favorite collaterally deleted")
	}
}

// One BPO fans out to several invention products in the scanner. Each
// product row must star and unstar on its own.
func TestIndustryFavoritesSameBlueprintDifferentProducts(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	warrior := favorite(1000, 2000, "t2_invention")
	hobgoblin := favorite(1000, 2001, "t2_invention")
	hobgoblin.ProductName = "Hobgoblin II"

	if err := d.AddIndustryFavoriteForUser("user-a", warrior); err != nil {
		t.Fatalf("add warrior: %v", err)
	}
	if err := d.AddIndustryFavoriteForUser("user-a", hobgoblin); err != nil {
		t.Fatalf("add hobgoblin: %v", err)
	}
	if got := d.GetIndustryFavoritesForUser("user-a"); len(got) != 2 {
		t.Fatalf("favorites len = %d, want 2", len(got))
	}

	if err := d.DeleteIndustryFavoriteForUser("user-a", 1000, 2000, "t2_invention"); err != nil {
		t.Fatalf("delete warrior: %v", err)
	}
	got := d.GetIndustryFavoritesForUser("user-a")
	if len(got) != 1 || got[0].ProductTypeID != 2001 {
		t.Fatalf("after unstarring warrior, favorites = %+v, want only hobgoblin", got)
	}
}

// A row stored with an empty scan mode has to match the key the scanner
// builds, which falls back to t1_mfg.
func TestIndustryFavoritesEmptyScanModeNormalizes(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	if err := d.AddIndustryFavoriteForUser("user-a", favorite(1000, 2000, "")); err != nil {
		t.Fatalf("add: %v", err)
	}
	got := d.GetIndustryFavoritesForUser("user-a")
	if len(got) != 1 || got[0].ScanMode != "t1_mfg" {
		t.Fatalf("favorites = %+v, want scan_mode t1_mfg", got)
	}
	if err := d.DeleteIndustryFavoriteForUser("user-a", 1000, 2000, ""); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := d.GetIndustryFavoritesForUser("user-a"); len(got) != 0 {
		t.Fatalf("after delete len = %d, want 0", len(got))
	}
}
