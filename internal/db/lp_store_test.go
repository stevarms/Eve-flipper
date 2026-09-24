package db

import "testing"

func TestLPBPCPriceOverridesRoundTrip(t *testing.T) {
	d := openTestDB(t)

	got, err := d.GetLPBPCPriceOverrides("alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("fresh user has overrides: %v", got)
	}

	if err := d.SetLPBPCPriceOverride("alice", 17637, 38_500_000); err != nil {
		t.Fatal(err)
	}
	if err := d.SetLPBPCPriceOverride("alice", 17637, 41_000_000); err != nil {
		t.Fatal(err)
	}
	if err := d.SetLPBPCPriceOverride("bob", 17637, 1); err != nil {
		t.Fatal(err)
	}

	got, err = d.GetLPBPCPriceOverrides("alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[17637] != 41_000_000 {
		t.Fatalf("setting twice must replace, and users must not see each other: %v", got)
	}

	if err := d.DeleteLPBPCPriceOverride("alice", 17637); err != nil {
		t.Fatal(err)
	}
	got, _ = d.GetLPBPCPriceOverrides("alice")
	if len(got) != 0 {
		t.Fatalf("delete left %v", got)
	}
	bob, _ := d.GetLPBPCPriceOverrides("bob")
	if bob[17637] != 1 {
		t.Fatalf("deleting alice's override touched bob's: %v", bob)
	}
}

func TestLPBPCPriceOverrideRejectsNonPositive(t *testing.T) {
	d := openTestDB(t)
	if err := d.SetLPBPCPriceOverride("alice", 17637, 0); err == nil {
		t.Fatal("a zero price is a delete, not an override; it must be refused")
	}
	if err := d.SetLPBPCPriceOverride("alice", 0, 5); err == nil {
		t.Fatal("type_id 0 must be refused")
	}
}
