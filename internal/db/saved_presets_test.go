package db

import "testing"

func TestSavedPresetsCreateListUpdateActivateDelete(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	created, err := d.CreateSavedPresetForUser("pilot-a", "flipper", "Aggressive Jita", `{"min_margin":2}`, true)
	if err != nil {
		t.Fatalf("create preset: %v", err)
	}
	if created.PresetID == "" || !created.Active || created.Tab != "flipper" {
		t.Fatalf("created preset = %+v, want generated id, active, tab=flipper", created)
	}

	list, err := d.ListSavedPresetsForUser("pilot-a")
	if err != nil {
		t.Fatalf("list presets: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}

	// Rename without touching params.
	newName := "Aggressive Jita v2"
	renamed, err := d.UpdateSavedPresetForUser("pilot-a", created.PresetID, &newName, nil, nil)
	if err != nil {
		t.Fatalf("rename preset: %v", err)
	}
	if renamed.Name != newName {
		t.Fatalf("renamed.Name = %q, want %q", renamed.Name, newName)
	}
	if renamed.PayloadJSON != `{"min_margin":2}` {
		t.Fatalf("rename clobbered params: %q", renamed.PayloadJSON)
	}

	// Update params without touching the name.
	newParams := `{"min_margin":4}`
	reparam, err := d.UpdateSavedPresetForUser("pilot-a", created.PresetID, nil, &newParams, nil)
	if err != nil {
		t.Fatalf("update params: %v", err)
	}
	if reparam.Name != newName {
		t.Fatalf("param update clobbered name: %q", reparam.Name)
	}
	if reparam.PayloadJSON != newParams {
		t.Fatalf("reparam.PayloadJSON = %q, want %q", reparam.PayloadJSON, newParams)
	}

	deleted, err := d.DeleteSavedPresetForUser("pilot-a", created.PresetID)
	if err != nil {
		t.Fatalf("delete preset: %v", err)
	}
	if len(deleted) != 0 {
		t.Fatalf("len(deleted list) = %d, want 0", len(deleted))
	}
}

// TestSavedPresetsActivePerTabIndex pins the plan's requirement: one active
// preset per (user, tab), not per user -- flipper and station each remember
// their own active choice, so activating a second preset on the flipper tab
// must not disturb the active preset already set on station.
func TestSavedPresetsActivePerTabIndex(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	flipA, err := d.CreateSavedPresetForUser("pilot-a", "flipper", "Flip A", `{}`, true)
	if err != nil {
		t.Fatalf("create flipA: %v", err)
	}
	stationA, err := d.CreateSavedPresetForUser("pilot-a", "station", "Station A", `{}`, true)
	if err != nil {
		t.Fatalf("create stationA: %v", err)
	}
	flipB, err := d.CreateSavedPresetForUser("pilot-a", "flipper", "Flip B", `{}`, true)
	if err != nil {
		t.Fatalf("create flipB: %v", err)
	}

	list, err := d.ListSavedPresetsForUser("pilot-a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	active := map[string]bool{}
	for _, row := range list {
		active[row.PresetID] = row.Active
	}
	if active[flipB.PresetID] != true {
		t.Fatalf("flipB should be active after creation")
	}
	if active[flipA.PresetID] != false {
		t.Fatalf("flipA should have been deactivated when flipB activated on the same tab")
	}
	if active[stationA.PresetID] != true {
		t.Fatalf("stationA should still be active -- a different tab's activation must not touch it")
	}

	// Explicitly re-activate flipA; stationA (a different tab) must still be untouched.
	if _, err := d.ActivateSavedPresetForUser("pilot-a", flipA.PresetID); err != nil {
		t.Fatalf("activate flipA: %v", err)
	}
	list, err = d.ListSavedPresetsForUser("pilot-a")
	if err != nil {
		t.Fatalf("list after activate: %v", err)
	}
	active = map[string]bool{}
	for _, row := range list {
		active[row.PresetID] = row.Active
	}
	if !active[flipA.PresetID] || active[flipB.PresetID] {
		t.Fatalf("activating flipA should deactivate flipB on the same tab")
	}
	if !active[stationA.PresetID] {
		t.Fatalf("stationA should remain active across an unrelated tab's activation")
	}
}

func TestSavedPresetsUserIsolation(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	if _, err := d.CreateSavedPresetForUser("pilot-a", "flipper", "A's preset", `{}`, true); err != nil {
		t.Fatalf("create for pilot-a: %v", err)
	}
	if _, err := d.CreateSavedPresetForUser("pilot-b", "flipper", "B's preset", `{}`, true); err != nil {
		t.Fatalf("create for pilot-b: %v", err)
	}

	aList, err := d.ListSavedPresetsForUser("pilot-a")
	if err != nil {
		t.Fatalf("list pilot-a: %v", err)
	}
	if len(aList) != 1 || aList[0].Name != "A's preset" {
		t.Fatalf("pilot-a list = %+v, want just A's preset", aList)
	}

	bList, err := d.ListSavedPresetsForUser("pilot-b")
	if err != nil {
		t.Fatalf("list pilot-b: %v", err)
	}
	if len(bList) != 1 || bList[0].Name != "B's preset" {
		t.Fatalf("pilot-b list = %+v, want just B's preset", bList)
	}
}

func TestSavedPresetsDeleteMissingReturnsErrNoRows(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	if _, err := d.DeleteSavedPresetForUser("pilot-a", "does-not-exist"); err == nil {
		t.Fatal("delete of missing preset should error")
	}
}
