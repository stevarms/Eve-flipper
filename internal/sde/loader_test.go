package sde

import "testing"

func TestIsRigGroupName(t *testing.T) {
	tests := []struct {
		name       string
		categoryID int32
		groupName  string
		want       bool
	}{
		{name: "rig armor", categoryID: 7, groupName: "Rig Armor", want: true},
		{name: "rig launcher", categoryID: 7, groupName: "Rig Launcher", want: true},
		{name: "rig navigation lowercase", categoryID: 7, groupName: "rig navigation", want: true},
		{name: "non-rig module", categoryID: 7, groupName: "Energy Weapon", want: false},
		{name: "rig blueprint", categoryID: 9, groupName: "Rig Blueprint", want: false},
		{name: "ship group", categoryID: 6, groupName: "Tactical Destroyer", want: false},
		{name: "empty", categoryID: 7, groupName: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRigGroupName(tt.categoryID, tt.groupName); got != tt.want {
				t.Fatalf("isRigGroupName(%d, %q) = %v, want %v", tt.categoryID, tt.groupName, got, tt.want)
			}
		})
	}
}

func TestApplyShipPackagedVolumes(t *testing.T) {
	data := &Data{
		Types: map[int32]*ItemType{
			608: {ID: 608, Name: "Atron", CategoryID: 6, Volume: 22500},
			34:  {ID: 34, Name: "Tritanium", CategoryID: 4, Volume: 0.01},
		},
		shipTypesMissingPackagedVolume: map[int32]bool{608: true},
	}

	if got := data.ApplyShipPackagedVolumes(map[int32]float64{608: 2500, 34: 999}); got != 1 {
		t.Fatalf("ApplyShipPackagedVolumes applied %d, want 1", got)
	}
	if data.Types[608].Volume != 2500 {
		t.Fatalf("ship volume = %v, want packaged volume 2500", data.Types[608].Volume)
	}
	if data.Types[34].Volume != 0.01 {
		t.Fatalf("non-ship volume changed to %v", data.Types[34].Volume)
	}
	if missing := data.MissingShipPackagedVolumeTypeIDs(); len(missing) != 0 {
		t.Fatalf("missing after apply = %v, want empty", missing)
	}
}
