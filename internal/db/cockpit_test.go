package db

import "testing"

func TestCockpitPreferencesRoundTrip(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	if _, _, ok, err := d.LoadCockpitPreferencesForUser("pilot-a"); err != nil || ok {
		t.Fatalf("LoadCockpitPreferencesForUser(empty) ok=%v err=%v, want ok=false err=nil", ok, err)
	}

	updatedAt, err := d.SaveCockpitPreferencesForUser("pilot-a", `{"version":1,"name":"Station desk"}`)
	if err != nil {
		t.Fatalf("SaveCockpitPreferencesForUser: %v", err)
	}
	if updatedAt == "" {
		t.Fatal("SaveCockpitPreferencesForUser updatedAt is empty")
	}

	payload, loadedAt, ok, err := d.LoadCockpitPreferencesForUser("pilot-a")
	if err != nil {
		t.Fatalf("LoadCockpitPreferencesForUser: %v", err)
	}
	if !ok {
		t.Fatal("LoadCockpitPreferencesForUser ok=false, want true")
	}
	if payload != `{"version":1,"name":"Station desk"}` {
		t.Fatalf("payload = %q", payload)
	}
	if loadedAt != updatedAt {
		t.Fatalf("loadedAt = %q, want %q", loadedAt, updatedAt)
	}
}

func TestCockpitLoadoutsActivateDuplicateDelete(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	a, err := d.UpsertCockpitLoadoutForUser("pilot-a", "station", "Station", `{"version":1,"name":"Station"}`, true)
	if err != nil {
		t.Fatalf("create active loadout: %v", err)
	}
	if !a.Active {
		t.Fatal("created loadout is not active")
	}

	b, err := d.UpsertCockpitLoadoutForUser("pilot-a", "", "Regional", `{"version":1,"name":"Regional"}`, true)
	if err != nil {
		t.Fatalf("create second active loadout: %v", err)
	}
	if b.LoadoutID == "" || !b.Active {
		t.Fatalf("second loadout = %+v, want generated active id", b)
	}

	loadouts, err := d.ListCockpitLoadoutsForUser("pilot-a")
	if err != nil {
		t.Fatalf("list loadouts: %v", err)
	}
	if len(loadouts) != 2 {
		t.Fatalf("len(loadouts) = %d, want 2", len(loadouts))
	}
	activeCount := 0
	for _, row := range loadouts {
		if row.Active {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Fatalf("active loadout count = %d, want 1", activeCount)
	}

	if _, err := d.ActivateCockpitLoadoutForUser("pilot-a", "station"); err != nil {
		t.Fatalf("activate station: %v", err)
	}
	active, ok, err := d.ActiveCockpitLoadoutForUser("pilot-a")
	if err != nil || !ok {
		t.Fatalf("active loadout ok=%v err=%v", ok, err)
	}
	if active.LoadoutID != "station" {
		t.Fatalf("active loadout = %q, want station", active.LoadoutID)
	}

	remaining, err := d.DeleteCockpitLoadoutForUser("pilot-a", "station")
	if err != nil {
		t.Fatalf("delete station: %v", err)
	}
	if len(remaining) != 1 || !remaining[0].Active || remaining[0].LoadoutID != b.LoadoutID {
		t.Fatalf("remaining loadouts = %+v", remaining)
	}
	if _, err := d.DeleteCockpitLoadoutForUser("pilot-a", b.LoadoutID); err == nil {
		t.Fatal("delete last loadout succeeded, want error")
	}
}
