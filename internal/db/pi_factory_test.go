package db

import "testing"

func TestPIFactoryPortfolioRoundTrip(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	entries := []PIFactoryEntry{
		{ClientID: "f_1", Name: "Water", SchematicID: 100, FactoryCount: 2},
		{ClientID: "f_2", Name: "Oxygen", SchematicID: 200, FactoryCount: 1},
	}
	if err := d.ReplacePIFactoryPortfolioForUser("pilot-a", entries); err != nil {
		t.Fatalf("replace portfolio: %v", err)
	}

	got, err := d.GetPIFactoryPortfolioForUser("pilot-a")
	if err != nil {
		t.Fatalf("get portfolio: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].ClientID != "f_1" || got[0].SortOrder != 0 {
		t.Fatalf("got[0] = %+v, want f_1 at sort_order 0", got[0])
	}
	if got[1].ClientID != "f_2" || got[1].SortOrder != 1 {
		t.Fatalf("got[1] = %+v, want f_2 at sort_order 1", got[1])
	}
}

// TestPIFactoryPortfolioReorderPreservesSortOrder pins the plan's
// requirement: dragging entries into a new order and saving must have that
// order survive a reload, not just the same set of entries.
func TestPIFactoryPortfolioReorderPreservesSortOrder(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	if err := d.ReplacePIFactoryPortfolioForUser("pilot-a", []PIFactoryEntry{
		{ClientID: "f_1", Name: "Water", SchematicID: 100, FactoryCount: 2},
		{ClientID: "f_2", Name: "Oxygen", SchematicID: 200, FactoryCount: 1},
		{ClientID: "f_3", Name: "Biomass", SchematicID: 300, FactoryCount: 3},
	}); err != nil {
		t.Fatalf("initial save: %v", err)
	}

	// Reorder: f_3, f_1, f_2.
	if err := d.ReplacePIFactoryPortfolioForUser("pilot-a", []PIFactoryEntry{
		{ClientID: "f_3", Name: "Biomass", SchematicID: 300, FactoryCount: 3},
		{ClientID: "f_1", Name: "Water", SchematicID: 100, FactoryCount: 2},
		{ClientID: "f_2", Name: "Oxygen", SchematicID: 200, FactoryCount: 1},
	}); err != nil {
		t.Fatalf("reorder save: %v", err)
	}

	got, err := d.GetPIFactoryPortfolioForUser("pilot-a")
	if err != nil {
		t.Fatalf("get portfolio: %v", err)
	}
	wantOrder := []string{"f_3", "f_1", "f_2"}
	if len(got) != len(wantOrder) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(wantOrder))
	}
	for i, id := range wantOrder {
		if got[i].ClientID != id {
			t.Fatalf("got[%d].ClientID = %q, want %q", i, got[i].ClientID, id)
		}
		if got[i].SortOrder != i {
			t.Fatalf("got[%d].SortOrder = %d, want %d", i, got[i].SortOrder, i)
		}
	}
}

func TestPIFactoryPortfolioReplaceWithEmptyClears(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	if err := d.ReplacePIFactoryPortfolioForUser("pilot-a", []PIFactoryEntry{
		{ClientID: "f_1", Name: "Water", SchematicID: 100, FactoryCount: 2},
	}); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	if err := d.ReplacePIFactoryPortfolioForUser("pilot-a", nil); err != nil {
		t.Fatalf("clear save: %v", err)
	}
	got, err := d.GetPIFactoryPortfolioForUser("pilot-a")
	if err != nil {
		t.Fatalf("get portfolio: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len(got) = %d, want 0 after clearing", len(got))
	}
}

func TestPIFactoryPortfolioUserIsolation(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	if err := d.ReplacePIFactoryPortfolioForUser("pilot-a", []PIFactoryEntry{
		{ClientID: "f_1", Name: "A's factory", SchematicID: 100, FactoryCount: 1},
	}); err != nil {
		t.Fatalf("save for pilot-a: %v", err)
	}
	if err := d.ReplacePIFactoryPortfolioForUser("pilot-b", []PIFactoryEntry{
		{ClientID: "f_1", Name: "B's factory", SchematicID: 200, FactoryCount: 2},
	}); err != nil {
		t.Fatalf("save for pilot-b: %v", err)
	}

	aGot, err := d.GetPIFactoryPortfolioForUser("pilot-a")
	if err != nil {
		t.Fatalf("get pilot-a: %v", err)
	}
	if len(aGot) != 1 || aGot[0].Name != "A's factory" {
		t.Fatalf("pilot-a portfolio = %+v, want just A's factory", aGot)
	}

	bGot, err := d.GetPIFactoryPortfolioForUser("pilot-b")
	if err != nil {
		t.Fatalf("get pilot-b: %v", err)
	}
	if len(bGot) != 1 || bGot[0].Name != "B's factory" {
		t.Fatalf("pilot-b portfolio = %+v, want just B's factory", bGot)
	}
}
