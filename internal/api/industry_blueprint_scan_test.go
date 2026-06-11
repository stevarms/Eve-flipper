package api

import (
	"reflect"
	"testing"

	"eve-flipper/internal/db"
)

func TestGroupBlueprintsByType_MergesAcrossLocations(t *testing.T) {
	rows := []db.IndustryBlueprintPoolInput{
		{BlueprintTypeID: 641, BlueprintName: "Stabber Blueprint", LocationID: 60003760, Quantity: 1, ME: 8, TE: 14, IsBPO: true, AvailableRuns: 0},
		{BlueprintTypeID: 641, BlueprintName: "Stabber Blueprint", LocationID: 60008494, Quantity: 1, ME: 10, TE: 20, IsBPO: true, AvailableRuns: 0},
		{BlueprintTypeID: 641, BlueprintName: "Stabber Blueprint", LocationID: 60003760, Quantity: 5, ME: 4, TE: 8, IsBPO: false, AvailableRuns: 50},
		{BlueprintTypeID: 690, BlueprintName: "Rifter Blueprint", LocationID: 60003760, Quantity: 1, ME: 10, TE: 20, IsBPO: true},
	}

	got := groupBlueprintsByType(rows)
	if len(got) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(got))
	}

	// groupBlueprintsByType sorts by name asc, then BPO before BPC within the same name.
	// Expected order: Rifter BPO, Stabber BPO, Stabber BPC.
	rifter := got[0]
	if rifter.BlueprintTypeID != 690 || !rifter.IsBPO || rifter.OwnedQuantity != 1 {
		t.Fatalf("rifter group wrong: %+v", rifter)
	}

	stabberBPO := got[1]
	if !stabberBPO.IsBPO {
		t.Fatalf("expected first stabber group to be BPO: %+v", stabberBPO)
	}

	stabberBPC := got[2]
	if stabberBPC.IsBPO {
		t.Fatalf("expected second stabber group to be BPC: %+v", stabberBPC)
	}
	if stabberBPC.OwnedQuantity != 5 || stabberBPC.AvailableRuns != 50 {
		t.Fatalf("stabber BPC qty/runs wrong: %+v", stabberBPC)
	}
	if stabberBPO.OwnedQuantity != 2 {
		t.Fatalf("stabber BPO qty merge wrong: %+v", stabberBPO)
	}
	if stabberBPO.ME != 10 || stabberBPO.TE != 20 {
		t.Fatalf("stabber BPO ME/TE should pick best across copies, got ME=%d TE=%d", stabberBPO.ME, stabberBPO.TE)
	}
	if stabberBPO.AvailableRuns != 0 {
		t.Fatalf("BPO available runs must stay 0, got %d", stabberBPO.AvailableRuns)
	}
	wantLocations := []int64{60003760, 60008494}
	if !reflect.DeepEqual(stabberBPO.LocationIDs, wantLocations) {
		t.Fatalf("stabber BPO locations: got %v, want %v", stabberBPO.LocationIDs, wantLocations)
	}
}

func TestGroupBlueprintsByType_EmptyInput(t *testing.T) {
	got := groupBlueprintsByType(nil)
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %d entries", len(got))
	}
}

func TestProfitableScanRowPassesFilters(t *testing.T) {
	row := profitableScanRow{
		Profit:        500_000,
		ProfitPercent: 12,
		ISKPerHour:    25_000_000,
	}

	cases := []struct {
		name string
		req  profitableScanRequest
		want bool
	}{
		{"no filters", profitableScanRequest{}, true},
		{"isk/h ok", profitableScanRequest{MinISKPerHour: 10_000_000}, true},
		{"isk/h fail", profitableScanRequest{MinISKPerHour: 50_000_000}, false},
		{"profit fail", profitableScanRequest{MinProfit: 1_000_000}, false},
		{"margin fail", profitableScanRequest{MinMarginPct: 25}, false},
		{"all pass", profitableScanRequest{MinISKPerHour: 1, MinProfit: 1, MinMarginPct: 1}, true},
		{"only one fails", profitableScanRequest{MinISKPerHour: 1, MinProfit: 1, MinMarginPct: 50}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := profitableScanRowPassesFilters(row, tc.req); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
