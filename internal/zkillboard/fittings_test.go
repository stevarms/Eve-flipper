package zkillboard

import (
	"encoding/json"
	"math"
	"sort"
	"testing"

	"eve-flipper/internal/sde"
)

// realKillmailFragment is a slice of a zkillboard losses response for Caldari
// militia (factionID 500001), trimmed to one victim. The field names and the
// type ids are as the API sends them -- in particular the type id field is
// item_type_id, which is the whole reason this file exists.
//
// The container at the end, and only that, is constructed: nesting appears
// nowhere in 1,000 sampled Caldari losses, so there was no real one to copy.
// Its shape follows the ESI killmail schema, where a nested item requires a flag
// and nests exactly one level. flag 0 is what a plain container's contents
// carry, since a container has no slots.
const realKillmailFragment = `{
  "killmail_id": 133042451,
  "killmail_time": "2026-09-16T20:11:47Z",
  "solar_system_id": 30045324,
  "victim": {
    "ship_type_id": 32788,
    "character_id": 96139539,
    "damage_taken": 4177,
    "items": [
      {"flag": 94, "item_type_id": 31716, "quantity_destroyed": 1, "singleton": 0},
      {"flag": 27, "item_type_id": 5975,  "quantity_destroyed": 1, "singleton": 0},
      {"flag": 19, "item_type_id": 5443,  "quantity_destroyed": 1, "singleton": 0},
      {"flag": 5,  "item_type_id": 2679,  "quantity_destroyed": 640, "singleton": 0},
      {"flag": 5,  "item_type_id": 31716, "quantity_destroyed": 3, "singleton": 0},
      {"flag": 87, "item_type_id": 2456,  "quantity_destroyed": 2, "singleton": 0},
      {
        "flag": 5, "item_type_id": 3467, "quantity_destroyed": 1, "singleton": 0,
        "items": [
          {"flag": 0, "item_type_id": 12608, "quantity_destroyed": 100, "singleton": 0}
        ]
      }
    ]
  }
}`

// TestESIItem_UsesItemTypeID is the regression guard for the bug the whole FW
// Supply feature sits on: ESIItem carried a type_id tag, killmails send
// item_type_id, so every fitted module, charge and drone unmarshalled to
// TypeID 0 and was silently dropped. Hulls survived because they arrive as
// victim.ship_type_id, which is why the demand tab looked like it worked while
// listing hulls only.
func TestESIItem_UsesItemTypeID(t *testing.T) {
	var km ESIKillmail
	if err := json.Unmarshal([]byte(realKillmailFragment), &km); err != nil {
		t.Fatalf("unmarshal killmail: %v", err)
	}

	if km.Victim.ShipTypeID != 32788 {
		t.Errorf("ShipTypeID = %d, want 32788", km.Victim.ShipTypeID)
	}
	if got := len(km.Victim.Items); got != 7 {
		t.Fatalf("len(Items) = %d, want 7", got)
	}

	for i, item := range km.Victim.Items {
		if item.TypeID == 0 {
			t.Errorf("Items[%d].TypeID = 0: the json tag is not reading item_type_id", i)
		}
	}
	if got := km.Victim.Items[0].TypeID; got != 31716 {
		t.Errorf("Items[0].TypeID = %d, want 31716", got)
	}
	if got := km.Victim.Items[0].Flag; got != 94 {
		t.Errorf("Items[0].Flag = %d, want 94", got)
	}
	if got := km.Victim.Items[3].QuantityDestroyed; got != 640 {
		t.Errorf("Items[3].QuantityDestroyed = %d, want 640", got)
	}
}

// TestFlattenItems_NestedContainer asserts the contents of a container reach the
// aggregator. A jettison can or a fitted secure container hides real ammo one
// level down, and the flat loop never looked.
func TestFlattenItems_NestedContainer(t *testing.T) {
	var km ESIKillmail
	if err := json.Unmarshal([]byte(realKillmailFragment), &km); err != nil {
		t.Fatalf("unmarshal killmail: %v", err)
	}

	if got := len(km.Victim.Items[6].Items); got != 1 {
		t.Fatalf("nested Items = %d, want 1", got)
	}

	flat := flattenItems(km.Victim.Items, nil)
	if got := len(flat); got != 8 {
		t.Fatalf("len(flattenItems) = %d, want 8 (7 top level + 1 nested)", got)
	}

	found := false
	for _, item := range flat {
		if item.TypeID == 12608 && item.QuantityDestroyed == 100 {
			found = true
		}
	}
	if !found {
		t.Error("nested type 12608 x100 did not survive flattening")
	}
}

// TestFlattenItems_Empty covers the nil and empty cases, since every killmail
// with a fully-dropped fit hits them.
func TestFlattenItems_Empty(t *testing.T) {
	if got := flattenItems(nil, nil); len(got) != 0 {
		t.Errorf("flattenItems(nil) = %d items, want 0", len(got))
	}
	if got := flattenItems([]ESIItem{}, nil); len(got) != 0 {
		t.Errorf("flattenItems(empty) = %d items, want 0", len(got))
	}
}

func TestCategorizeItem(t *testing.T) {
	tests := []struct {
		name       string
		categoryID int32
		volume     float64
		flag       int32
		quantity   int64
		want       string
	}{
		// The SDE category decides it. Volumes and flags below are real: a
		// 50mm Steel Plate is 5 m3 and a Module, Cap Booster 800 is 32 m3 and
		// a Charge. Nothing that reads volume alone can tell those apart.
		{"frigate hull", sdeCategoryShip, 16500, 0, 1, "ship"},
		{"shield reinforcer rig", sdeCategoryModule, 5, 94, 1, "module"},
		{"50mm steel plate", sdeCategoryModule, 5, 27, 1, "module"},
		{"800mm steel plate", sdeCategoryModule, 20, 27, 1, "module"},
		{"warp scrambler", sdeCategoryModule, 5, 19, 1, "module"},
		{"hobgoblin", sdeCategoryDrone, 5, 87, 4, "drone"},
		{"fighter", sdeCategoryFighter, 100, 158, 1, "drone"},

		// Charges split by volume, and only inside their own category.
		{"hail S loaded in a turret", sdeCategoryCharge, 0.0025, 19, 92, "ammo"},
		{"hail S in cargo", sdeCategoryCharge, 0.0025, 5, 640, "ammo"},
		{"nanite repair paste", sdeCategoryCharge, 0.01, 5, 50, "ammo"},
		{"core scanner probe at the boundary", sdeCategoryCharge, chargeMaxVolumeM3, 5, 8, "ammo"},
		{"combat scanner probe", sdeCategoryCharge, 1, 5, 8, "consumable"},
		{"cap booster 800 loaded", sdeCategoryCharge, 32, 22, 8, "consumable"},
		{"electron bomb", sdeCategoryCharge, 75, 19, 4, "consumable"},

		{"implant is a pod loss", sdeCategoryImplant, 1, 89, 1, ""},
		{"secure container is packaging", sdeCategoryCelestial, 100, 5, 1, ""},
		{"unhandled category", 43, 1, 5, 1, ""},

		// categoryID 0 is "SDE has no idea", which falls back to the flag.
		{"fallback: single unit in a slot", 0, 0, 19, 1, "module"},
		{"fallback: stack in a slot is a charge", 0, 0, 19, 40, "ammo"},
		{"fallback: last round in a launcher", 0, 0.0025, 19, 1, "ammo"},
		{"fallback: drone bay", 0, 0, 87, 2, "drone"},
		{"fallback: cargo is unguessable", 0, 0, 5, 640, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := categorizeItem(tt.categoryID, tt.volume, tt.flag, tt.quantity)
			if got != tt.want {
				t.Errorf("categorizeItem(cat=%d, vol=%v, flag=%d, qty=%d) = %q, want %q",
					tt.categoryID, tt.volume, tt.flag, tt.quantity, got, tt.want)
			}
		})
	}
}

// TestCategorizeItem_VolumeNeverOutranksCategory is the regression guard for the
// trap this classifier walked into twice: a 5 m3 consumable band looks
// reasonable until you notice it is exactly a small module, and a 32 m3 cap
// booster looks like freight until you notice it is a Charge.
func TestCategorizeItem_VolumeNeverOutranksCategory(t *testing.T) {
	// Same volume, opposite answers, decided entirely by category.
	if got := categorizeItem(sdeCategoryModule, 5, 5, 3); got != "module" {
		t.Errorf("3 spare plates in cargo = %q, want module", got)
	}
	if got := categorizeItem(sdeCategoryDrone, 5, 5, 3); got != "drone" {
		t.Errorf("3 spare drones in cargo = %q, want drone", got)
	}

	// A bulky charge is still demand, not freight.
	if got := categorizeItem(sdeCategoryCharge, 32, 5, 12); got != "consumable" {
		t.Errorf("cap booster 800 in cargo = %q, want consumable", got)
	}
}

// fittingsTestSDE builds the minimum SDE the accumulator reads. Every name,
// volume and category below is the real value for that type id, read out of
// data/sde — so the classification these tests assert is the classification the
// live pipeline will produce.
func fittingsTestSDE() *sde.Data {
	d := &sde.Data{Types: map[int32]*sde.ItemType{}}
	for _, row := range []struct {
		id       int32
		name     string
		volume   float64
		category int32
	}{
		{32788, "Cambion", 16500, sdeCategoryShip},
		{31716, "Small EM Shield Reinforcer I", 5, sdeCategoryModule},
		{5975, "50MN Cold-Gas Enduring Microwarpdrive", 10, sdeCategoryModule},
		{5443, "Faint Epsilon Scoped Warp Scrambler", 5, sdeCategoryModule},
		{2679, "Scourge Rage Heavy Assault Missile", 0.015, sdeCategoryCharge},
		{2456, "Hobgoblin II", 5, sdeCategoryDrone},
		{3467, "Small Secure Container", 100, sdeCategoryCelestial},
		{12608, "Hail S", 0.0025, sdeCategoryCharge},
	} {
		d.Types[row.id] = &sde.ItemType{
			ID: row.id, Name: row.name, Volume: row.volume, CategoryID: row.category,
		}
	}
	return d
}

// TestAccumulateKillmail_RealFragment is the end-to-end statement of the fix:
// the same killmail that used to yield one hull now yields the hull plus its
// modules, charges, drone and the ammo inside its container.
func TestAccumulateKillmail_RealFragment(t *testing.T) {
	var km ESIKillmail
	if err := json.Unmarshal([]byte(realKillmailFragment), &km); err != nil {
		t.Fatalf("unmarshal killmail: %v", err)
	}

	acc := make(map[int32]*killmailAccum)
	accumulateKillmail(acc, &km, fittingsTestSDE())

	// Hull, 3 modules, 1 charge type, 1 drone, and the nested Hail S. The
	// container itself is Celestial, not demand, and correctly absent.
	want := map[int32]struct {
		category  string
		destroyed int64
	}{
		32788: {"ship", 1},
		31716: {"module", 4}, // 1 fitted + 3 spares in cargo, one killmail
		5975:  {"module", 1},
		5443:  {"module", 1},
		2679:  {"ammo", 640},
		2456:  {"drone", 2},
		12608: {"ammo", 100}, // nested inside the container, flag inherited
	}

	for typeID, exp := range want {
		a, ok := acc[typeID]
		if !ok {
			t.Errorf("type %d missing from accumulator", typeID)
			continue
		}
		if a.category != exp.category {
			t.Errorf("type %d category = %q, want %q", typeID, a.category, exp.category)
		}
		p := a.profile(typeID, false)
		if p.TotalDestroyed != exp.destroyed {
			t.Errorf("type %d TotalDestroyed = %d, want %d", typeID, p.TotalDestroyed, exp.destroyed)
		}
		if p.KillmailCount != 1 {
			t.Errorf("type %d KillmailCount = %d, want 1", typeID, p.KillmailCount)
		}
	}

	if _, ok := acc[3467]; ok {
		t.Error("the 100 m3 container itself was counted as demand")
	}
	if len(acc) != len(want) {
		t.Errorf("accumulator holds %d types, want %d", len(acc), len(want))
	}
}

// TestAccumulateKillmail_SameTypeTwiceIsOneKill pins the counter semantics.
// Type 31716 appears twice on the fragment -- fitted and spare in cargo -- and
// KillmailCount must stay 1, because it is the "how many losses carried this"
// gate, not an entry count. Asserted separately because this is the property a
// naive rewrite breaks.
func TestAccumulateKillmail_SameTypeTwiceIsOneKill(t *testing.T) {
	var km ESIKillmail
	if err := json.Unmarshal([]byte(realKillmailFragment), &km); err != nil {
		t.Fatalf("unmarshal killmail: %v", err)
	}

	acc := make(map[int32]*killmailAccum)
	accumulateKillmail(acc, &km, fittingsTestSDE())

	a := acc[31716]
	if a == nil {
		t.Fatal("type 31716 missing")
	}
	if a.kills != 1 {
		t.Fatalf("kills = %d, want 1 (both entries are the same loss)", a.kills)
	}
	if got := a.counts[4]; got != 1 {
		t.Errorf("contribution of 4 recorded %d times, want 1 (1 fitted + 3 in cargo, summed)", got)
	}
}

// TestAccumulateKillmail_NoSDE asserts analysis still runs on ids alone. Volume
// is then unknown, so cargo cannot be classified -- but module and drone slots
// are flag-based and must still land.
func TestAccumulateKillmail_NoSDE(t *testing.T) {
	var km ESIKillmail
	if err := json.Unmarshal([]byte(realKillmailFragment), &km); err != nil {
		t.Fatalf("unmarshal killmail: %v", err)
	}

	acc := make(map[int32]*killmailAccum)
	accumulateKillmail(acc, &km, nil)

	if _, ok := acc[31716]; !ok {
		t.Error("flag-based module classification needs no SDE, but type 31716 is missing")
	}
	if _, ok := acc[2456]; !ok {
		t.Error("flag-based drone classification needs no SDE, but type 2456 is missing")
	}
	// 640 rounds in cargo have no volume to judge, and flag 5 is not a module
	// slot, so the quantity fallback does not apply either.
	if _, ok := acc[2679]; ok {
		t.Error("cargo with unknown volume was classified anyway")
	}
}

// TestAccumulateKillmail_LoadedChargeFallback covers the inference path: a big
// stack in a module slot is a loaded charge, even with no volume to check.
func TestAccumulateKillmail_LoadedChargeFallback(t *testing.T) {
	km := ESIKillmail{Victim: ESIVictim{
		ShipTypeID: 32788,
		Items: []ESIItem{
			{Flag: 19, TypeID: 999001, QuantityDestroyed: 40}, // loaded charge
			{Flag: 19, TypeID: 999002, QuantityDestroyed: 1},  // the launcher
		},
	}}

	acc := make(map[int32]*killmailAccum)
	accumulateKillmail(acc, &km, nil)

	if a := acc[999001]; a == nil || a.category != "ammo" {
		t.Errorf("40 units in a high slot with no SDE: category = %v, want ammo", a)
	}
	if a := acc[999002]; a == nil || a.category != "module" {
		t.Errorf("1 unit in a high slot: category = %v, want module", a)
	}
}

// TestKillmailAccum_Winsorize is the guard against one loss manufacturing
// demand. Nine losses of ~2,000 rounds and one of 10,000: the raw total is
// dominated by the outlier, and winsorizing at p90 caps it to the ninth value.
func TestKillmailAccum_Winsorize(t *testing.T) {
	a := &killmailAccum{name: "Scourge Light Missile", category: "ammo"}
	for i := 0; i < 9; i++ {
		a.add(2000)
	}
	a.add(10000)

	raw := a.profile(2679, false)
	if raw.TotalDestroyed != 28000 {
		t.Fatalf("raw TotalDestroyed = %d, want 28000", raw.TotalDestroyed)
	}

	// p90 of ten values is the 9th by nearest rank, which is 2000. The outlier
	// is pulled down to 2000 and the total becomes 10 x 2000.
	w := a.profile(2679, true)
	if w.TotalDestroyed != 20000 {
		t.Errorf("winsorized TotalDestroyed = %d, want 20000", w.TotalDestroyed)
	}
	if w.KillmailCount != 10 {
		t.Errorf("winsorizing must not change KillmailCount: got %d, want 10", w.KillmailCount)
	}
	if w.TotalDestroyed >= raw.TotalDestroyed {
		t.Error("winsorizing did not reduce the total")
	}
}

// TestKillmailAccum_WinsorizeIsNotDestructive: a well-behaved distribution must
// come through untouched, or the cap would be quietly shaving real demand.
func TestKillmailAccum_WinsorizeIsNotDestructive(t *testing.T) {
	a := accumOf("ammo", 100, 100, 100, 100)
	if got := a.profile(1, true).TotalDestroyed; got != 400 {
		t.Errorf("uniform contributions winsorized to %d, want 400", got)
	}

	single := accumOf("ship", 1)
	if got := single.profile(1, true).TotalDestroyed; got != 1 {
		t.Errorf("single contribution winsorized to %d, want 1", got)
	}
}

// accumOf builds an accumulator from one contribution per killmail, which is
// how every one of these tests thinks about the input even though the
// accumulator no longer stores it that way.
func accumOf(category string, contributions ...int64) *killmailAccum {
	a := &killmailAccum{category: category}
	for _, c := range contributions {
		a.add(c)
	}
	return a
}

// slicePercentileInt64 is the implementation killmailAccum used to carry: sort
// one entry per killmail, take the nearest rank. It lives here now as the oracle
// the histogram is measured against, because "the histogram gives the same
// answer" is only worth asserting if the thing it is compared to is the old
// answer, written out.
func slicePercentileInt64(values []int64, pct int) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	rank := (pct*len(sorted) + 99) / 100 // ceil(pct% of n), 1-based
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

// sliceWinsorizedTotal is the other half of the old implementation.
func sliceWinsorizedTotal(values []int64, ceiling int64) int64 {
	var total int64
	for _, v := range values {
		if v > ceiling {
			v = ceiling
		}
		total += v
	}
	return total
}

func TestSlicePercentileInt64(t *testing.T) {
	tests := []struct {
		name   string
		values []int64
		pct    int
		want   int64
	}{
		{"empty", nil, 90, 0},
		{"single", []int64{7}, 90, 7},
		{"p90 of ten", []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 100}, 90, 9},
		{"p100 is the max", []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 100}, 100, 100},
		{"p50 of four", []int64{1, 2, 3, 4}, 50, 2},
		{"unsorted input", []int64{100, 3, 1, 2}, 100, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := slicePercentileInt64(tt.values, tt.pct); got != tt.want {
				t.Errorf("slicePercentileInt64(%v, %d) = %d, want %d", tt.values, tt.pct, got, tt.want)
			}
		})
	}
}

// TestKillmailAccum_HistogramMatchesTheSlice is the no-regression guard for the
// memory change. AnalyzeRegionFittings shares killmailAccum with the militia
// path, so "the histogram is exact" cannot be an argument -- it has to be a
// comparison, against the implementation that was there before, on the shapes
// that break percentile code: one value, two values, all equal, a long tail, and
// an outlier heavy enough to matter.
func TestKillmailAccum_HistogramMatchesTheSlice(t *testing.T) {
	cases := map[string][]int64{
		"single":            {5},
		"two":               {1, 9},
		"all equal":         {3, 3, 3, 3, 3, 3, 3},
		"the ammo outlier":  {2000, 2000, 2000, 2000, 2000, 2000, 2000, 2000, 2000, 10000},
		"modules are ones":  {1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
		"long tail":         {1, 1, 1, 2, 2, 3, 5, 8, 13, 21, 34, 55, 89},
		"descending":        {100, 90, 80, 70, 60, 50, 40, 30, 20, 10},
		"repeats and gaps":  {7, 7, 7, 40, 40, 1, 1, 1, 1, 900},
		"nine of a hundred": {100, 100, 100, 100, 100, 100, 100, 100, 100, 1},
	}

	for name, values := range cases {
		t.Run(name, func(t *testing.T) {
			a := accumOf("ammo", values...)

			wantCeiling := slicePercentileInt64(values, 90)
			if got := a.percentile(90); got != wantCeiling {
				t.Errorf("p90 = %d, want %d (the slice's answer on %v)", got, wantCeiling, values)
			}

			w := a.profile(1, true)
			if want := sliceWinsorizedTotal(values, wantCeiling); w.TotalDestroyed != want {
				t.Errorf("winsorized total = %d, want %d", w.TotalDestroyed, want)
			}
			if w.KillmailCount != len(values) {
				t.Errorf("KillmailCount = %d, want %d", w.KillmailCount, len(values))
			}

			raw := a.profile(1, false)
			if want := sliceWinsorizedTotal(values, math.MaxInt64); raw.TotalDestroyed != want {
				t.Errorf("raw total = %d, want %d", raw.TotalDestroyed, want)
			}
		})
	}
}

// TestKillmailAccum_EmptyIsZero: an accumulator that never saw a killmail must
// not divide by anything or index anything.
func TestKillmailAccum_EmptyIsZero(t *testing.T) {
	a := &killmailAccum{category: "ship"}
	if got := a.percentile(90); got != 0 {
		t.Errorf("p90 of nothing = %d, want 0", got)
	}
	p := a.profile(1, true)
	if p.TotalDestroyed != 0 || p.KillmailCount != 0 {
		t.Errorf("empty profile = %+v, want zeroes", p)
	}
}

// TestKillmailAccum_MemoryIsFlatInWindowLength is the property that makes the
// long demand window safe: the accumulator's retained size must be a function of
// how many distinct contributions a type has, not of how many killmails carried
// it. Ten times the killmails, over the same mix of values, must not retain ten
// times the memory -- which is exactly what the old one-entry-per-killmail slice
// did.
func TestKillmailAccum_MemoryIsFlatInWindowLength(t *testing.T) {
	// The mix a real type shows: mostly the same handful of values.
	mix := []int64{1, 1, 1, 2, 2, 3, 1, 1, 8, 1}

	fold := func(pages int) *killmailAccum {
		a := &killmailAccum{category: "ammo"}
		for p := 0; p < pages; p++ {
			for _, v := range mix {
				a.add(v)
			}
		}
		return a
	}

	small := fold(30)
	large := fold(300)

	if len(large.counts) != len(small.counts) {
		t.Errorf("distinct values grew with the window: %d at 300 pages vs %d at 30",
			len(large.counts), len(small.counts))
	}
	if large.kills != small.kills*10 {
		t.Fatalf("the fixture did not actually fold ten times as much: %d vs %d", large.kills, small.kills)
	}

	// And the arithmetic still tracks the larger input, so flat memory is not
	// flat because nothing was recorded.
	if large.profile(1, false).TotalDestroyed != small.profile(1, false).TotalDestroyed*10 {
		t.Error("ten times the killmails did not produce ten times the destruction")
	}
}

func TestNormalizePastSeconds(t *testing.T) {
	tests := []struct {
		in, want int
	}{
		{0, 3600},
		{-1, 3600},
		{3600, 3600},
		{86400, 86400},
		{604800, 604800},
		{700000, 604800}, // clamped to zkillboard's 7-day ceiling
		{5000, 7200},     // rounded up, never down
		{3601, 7200},
		{604801, 604800}, // rounds to 608400, then clamps
	}

	for _, tt := range tests {
		got := normalizePastSeconds(tt.in)
		if got != tt.want {
			t.Errorf("normalizePastSeconds(%d) = %d, want %d", tt.in, got, tt.want)
		}
		if got%3600 != 0 {
			t.Errorf("normalizePastSeconds(%d) = %d, not a whole hour", tt.in, got)
		}
	}
}
