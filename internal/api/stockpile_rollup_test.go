package api

import (
	"testing"

	"eve-flipper/internal/esi"
)

func TestRollUpAtStation_FlatItemsAtStation(t *testing.T) {
	assets := []stockpileAsset{
		{ItemID: 1, TypeID: 34, LocationID: 60003760, Quantity: 100_000},
		{ItemID: 2, TypeID: 35, LocationID: 60003760, Quantity: 50_000},
		{ItemID: 3, TypeID: 34, LocationID: 60003760, Quantity: 25_000}, // same type
	}
	got := rollUpAtStation(assets, 60003760)
	if got[34] != 125_000 {
		t.Errorf("type 34: want 125000, got %d", got[34])
	}
	if got[35] != 50_000 {
		t.Errorf("type 35: want 50000, got %d", got[35])
	}
}

func TestRollUpAtStation_ItemsInContainerChild(t *testing.T) {
	assets := []stockpileAsset{
		{ItemID: 100, TypeID: 17366, LocationID: 60003760, Quantity: 1}, // Small Standard Container
		{ItemID: 1, TypeID: 34, LocationID: 100, Quantity: 500_000},     // Tritanium inside container
		{ItemID: 2, TypeID: 35, LocationID: 100, Quantity: 200_000},     // Pyerite inside container
	}
	got := rollUpAtStation(assets, 60003760)
	if got[17366] != 1 {
		t.Errorf("container itself should count: want 1, got %d", got[17366])
	}
	if got[34] != 500_000 {
		t.Errorf("tritanium in container: want 500000, got %d", got[34])
	}
	if got[35] != 200_000 {
		t.Errorf("pyerite in container: want 200000, got %d", got[35])
	}
}

func TestRollUpAtStation_ThreeDeepShipCargoCan(t *testing.T) {
	assets := []stockpileAsset{
		{ItemID: 500, TypeID: 648, LocationID: 60003760, Quantity: 1}, // Badger docked at station
		{ItemID: 501, TypeID: 17366, LocationID: 500, Quantity: 1},    // Container inside Badger's cargo hold
		{ItemID: 1, TypeID: 34, LocationID: 501, Quantity: 1_000_000}, // Tritanium inside that container
	}
	got := rollUpAtStation(assets, 60003760)
	if got[34] != 1_000_000 {
		t.Errorf("3-deep tritanium: want 1000000, got %d", got[34])
	}
	if got[648] != 1 || got[17366] != 1 {
		t.Errorf("intermediate ship/container should also count: %+v", got)
	}
}

func TestRollUpAtStation_ExcludesOtherStations(t *testing.T) {
	assets := []stockpileAsset{
		{ItemID: 1, TypeID: 34, LocationID: 60003760, Quantity: 100_000}, // Jita
		{ItemID: 2, TypeID: 34, LocationID: 60008494, Quantity: 999_999}, // Amarr
	}
	got := rollUpAtStation(assets, 60003760)
	if got[34] != 100_000 {
		t.Errorf("expected only Jita 100000, got %d", got[34])
	}
	got2 := rollUpAtStation(assets, 60008494)
	if got2[34] != 999_999 {
		t.Errorf("expected only Amarr 999999, got %d", got2[34])
	}
}

func TestRollUpAtStation_EmptyInputs(t *testing.T) {
	if got := rollUpAtStation(nil, 60003760); len(got) != 0 {
		t.Errorf("nil assets: want empty, got %+v", got)
	}
	if got := rollUpAtStation([]stockpileAsset{{ItemID: 1, TypeID: 34, LocationID: 60003760, Quantity: 1}}, 0); len(got) != 0 {
		t.Errorf("zero stationID: want empty, got %+v", got)
	}
}

func TestRollUpAtStation_PlayerStructure(t *testing.T) {
	// Structures have IDs > 1e13 (64-bit). Same algorithm.
	const structureID int64 = 1_022_734_985_384
	assets := []stockpileAsset{
		{ItemID: 1, TypeID: 34, LocationID: structureID, Quantity: 42_000},
		{ItemID: 2, TypeID: 34, LocationID: 60003760, Quantity: 111}, // Jita — should NOT count
	}
	got := rollUpAtStation(assets, structureID)
	if got[34] != 42_000 {
		t.Errorf("structure rollup: want 42000, got %d", got[34])
	}
}

func TestRollUpAtStation_SkipsCyclesGracefully(t *testing.T) {
	// If ESI ever returns a cycle (it shouldn't), the visited map keeps us
	// finite instead of hanging.
	assets := []stockpileAsset{
		{ItemID: 10, TypeID: 34, LocationID: 60003760, Quantity: 1},
		{ItemID: 11, TypeID: 35, LocationID: 10, Quantity: 2},
		{ItemID: 10, TypeID: 34, LocationID: 11, Quantity: 999}, // self-cycle via duplicate
	}
	got := rollUpAtStation(assets, 60003760)
	// Should complete and count only once per visited itemID
	if got[34] != 1 {
		t.Errorf("cycle: expected typeID 34 counted once (1), got %d", got[34])
	}
	if got[35] != 2 {
		t.Errorf("cycle: expected typeID 35 counted once (2), got %d", got[35])
	}
}

func TestRollUpAtStation_ConvertersPreserveFields(t *testing.T) {
	char := []esi.CharacterAsset{
		{ItemID: 1, TypeID: 34, LocationID: 60003760, Quantity: 100},
	}
	corp := []esi.CorporationAsset{
		{ItemID: 2, TypeID: 35, LocationID: 60003760, Quantity: 200},
	}
	c := stockpileAssetsFromCharacter(char)
	if len(c) != 1 || c[0].TypeID != 34 || c[0].Quantity != 100 {
		t.Errorf("char converter: %+v", c)
	}
	co := stockpileAssetsFromCorporation(corp)
	if len(co) != 1 || co[0].TypeID != 35 || co[0].Quantity != 200 {
		t.Errorf("corp converter: %+v", co)
	}
}
