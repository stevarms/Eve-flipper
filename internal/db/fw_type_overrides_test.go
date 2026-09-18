package db

import "testing"

// fw_type_overrides_test.go -- migration v54's two columns: per-item overrides
// of the model's own caution, named the way the row that produced them did.
//
// The name is the load-bearing part of the round trip. An excluded type may
// never appear in a plan again to look its name up from, so the chip that lets
// a user undo the exclusion has nothing else to read it from.

// TestFWCampaignTypeOverridesDefault: a campaign written without either list --
// every campaign that existed before v54 -- reads back with both empty rather
// than nil-vs-empty ambiguity biting a caller that ranges over them.
func TestFWCampaignTypeOverridesDefault(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	id, err := d.SaveFWCampaign(fwTestCampaign(fwUserA))
	if err != nil {
		t.Fatalf("save campaign: %v", err)
	}
	got, err := d.GetFWCampaign(fwUserA, id)
	if err != nil || got == nil {
		t.Fatalf("get campaign: %v (%v)", err, got)
	}
	if len(got.IncludedTypes) != 0 {
		t.Errorf("included types defaulted to %v, want empty", got.IncludedTypes)
	}
	if len(got.ExcludedTypes) != 0 {
		t.Errorf("excluded types defaulted to %v, want empty", got.ExcludedTypes)
	}
}

// TestFWCampaignTypeOverridesRoundTrip: both lists survive a write, an update
// and a read, with the type name intact -- not just the ID, since a settings
// chip has nowhere else to get the name from once a type stops appearing as a
// row.
func TestFWCampaignTypeOverridesRoundTrip(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	c := fwTestCampaign(fwUserA)
	c.IncludedTypes = []FWTypeOverride{{TypeID: 12345, TypeName: "5MN Y-T8 Compact Microwarpdrive"}}
	c.ExcludedTypes = []FWTypeOverride{{TypeID: 32880, TypeName: "Civilian Miner"}}
	id, err := d.SaveFWCampaign(c)
	if err != nil {
		t.Fatalf("save campaign: %v", err)
	}

	got, err := d.GetFWCampaign(fwUserA, id)
	if err != nil || got == nil {
		t.Fatalf("get campaign: %v (%v)", err, got)
	}
	if len(got.IncludedTypes) != 1 || got.IncludedTypes[0].TypeID != 12345 ||
		got.IncludedTypes[0].TypeName != "5MN Y-T8 Compact Microwarpdrive" {
		t.Fatalf("included types round-tripped as %+v", got.IncludedTypes)
	}
	if len(got.ExcludedTypes) != 1 || got.ExcludedTypes[0].TypeID != 32880 ||
		got.ExcludedTypes[0].TypeName != "Civilian Miner" {
		t.Fatalf("excluded types round-tripped as %+v", got.ExcludedTypes)
	}

	// Un-excluding is removing it from the list and saving again -- there is no
	// row to click "include" on for a type that stopped appearing entirely.
	got.ExcludedTypes = nil
	if _, err := d.SaveFWCampaign(got); err != nil {
		t.Fatalf("update campaign: %v", err)
	}
	again, err := d.GetFWCampaign(fwUserA, id)
	if err != nil || again == nil {
		t.Fatalf("re-read campaign: %v (%v)", err, again)
	}
	if len(again.ExcludedTypes) != 0 {
		t.Errorf("clearing the excluded list did not take: %+v", again.ExcludedTypes)
	}
	if len(again.IncludedTypes) != 1 || again.IncludedTypes[0].TypeID != 12345 {
		t.Errorf("clearing one list disturbed the other: %+v", again.IncludedTypes)
	}
}

// TestFWCampaignTypeOverridesScopedPerUser: the same scoping every other
// campaign field gets, pinned here because a leaked override list would either
// ship someone else's junk item or omit someone else's staple.
func TestFWCampaignTypeOverridesScopedPerUser(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	a := fwTestCampaign(fwUserA)
	a.ExcludedTypes = []FWTypeOverride{{TypeID: 32880, TypeName: "Civilian Miner"}}
	if _, err := d.SaveFWCampaign(a); err != nil {
		t.Fatalf("save campaign a: %v", err)
	}

	b := fwTestCampaign(fwUserB)
	if _, err := d.SaveFWCampaign(b); err != nil {
		t.Fatalf("save campaign b: %v", err)
	}

	gotB, err := d.GetFWCampaign(fwUserB, b.CampaignID)
	if err != nil || gotB == nil {
		t.Fatalf("get campaign b: %v (%v)", err, gotB)
	}
	if len(gotB.ExcludedTypes) != 0 {
		t.Errorf("user B's campaign inherited user A's exclusions: %+v", gotB.ExcludedTypes)
	}
}
