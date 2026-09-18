package engine

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"eve-flipper/internal/esi"
	"eve-flipper/internal/graph"
	"eve-flipper/internal/sde"
)

// --- synthetic topology -----------------------------------------------------
//
// A line of systems, so distance is arithmetic and every assertion about the
// radius is exact:
//
//	front(1) - 2 - 3 - 4 - 5 - 6(source hub)

func lineUniverse() *graph.Universe {
	u := graph.NewUniverse()
	for id := int32(1); id <= 6; id++ {
		u.SetSecurity(id, 0.6)
		u.SetRegion(id, 10000001)
		if id > 1 {
			u.AddGate(id-1, id)
			u.AddGate(id, id-1)
		}
	}
	return u
}

func lineSDE() *sde.Data {
	d := &sde.Data{
		Systems:  map[int32]*sde.SolarSystem{},
		Stations: map[int64]*sde.Station{},
	}
	for id := int32(1); id <= 6; id++ {
		d.Systems[id] = &sde.SolarSystem{ID: id, Name: string(rune('A' + id - 1)), RegionID: 10000001, Security: 0.6}
		// System 5 has no station, so a system the ring reaches but where nothing
		// can be listed is excluded. System 3 has two, so a system is not a
		// candidate -- a station is.
		if id == 5 {
			continue
		}
		d.Stations[int64(60000000+id)] = &sde.Station{ID: int64(60000000 + id), Name: "Station " + string(rune('A'+id-1)), SystemID: id}
		if id == 3 {
			d.Stations[60000103] = &sde.Station{ID: 60000103, Name: "Station C II", SystemID: 3}
		}
	}
	return d
}

func bySystem(candidates []FWStagingCandidate) map[int32]FWStagingCandidate {
	index := make(map[int32]FWStagingCandidate, len(candidates))
	for _, c := range candidates {
		if _, seen := index[c.SystemID]; !seen {
			index[c.SystemID] = c
		}
	}
	return index
}

func TestDeriveStagingRing_RadiusAndStations(t *testing.T) {
	got := DeriveStagingRing(lineUniverse(), lineSDE(), map[int32]bool{1: true}, RingOptions{
		MaxJumpsFromFront: 2,
		SourceSystemID:    6,
	})

	index := bySystem(got)
	for _, want := range []int32{1, 2, 3} {
		if _, ok := index[want]; !ok {
			t.Errorf("system %d is within radius 2 and has a station: must be a candidate", want)
		}
	}
	if _, ok := index[4]; ok {
		t.Error("system 4 is 3 jumps out and must not be in a radius-2 ring")
	}
	if _, ok := index[5]; ok {
		t.Error("system 5 has no station: nothing can be listed there")
	}

	stationsInThree := 0
	for _, c := range got {
		if c.SystemID == 3 {
			stationsInThree++
		}
	}
	if stationsInThree != 2 {
		t.Errorf("system 3 produced %d candidates, want 2 -- one per station", stationsInThree)
	}

	if d := index[1].JumpsToFront; d != 0 {
		t.Errorf("frontline system JumpsToFront = %d, want 0", d)
	}
	if d := index[3].JumpsToFront; d != 2 {
		t.Errorf("system 3 JumpsToFront = %d, want 2", d)
	}
	if j := index[3].JumpsFromSource; j != 3 {
		t.Errorf("system 3 JumpsFromSource = %d, want 3 (from system 6)", j)
	}
}

// TestDeriveStagingRing_PinSurvivesRadius is the Intaki case in miniature, and
// the plan's proof that the seed and the geometry are independent paths: drop
// the radius and a pinned system leaves the ring but stays in the list.
func TestDeriveStagingRing_PinSurvivesRadius(t *testing.T) {
	u, d := lineUniverse(), lineSDE()
	frontline := map[int32]bool{1: true}

	wide := bySystem(DeriveStagingRing(u, d, frontline, RingOptions{
		MaxJumpsFromFront: 3, BulwarkSystemID: 4,
	}))
	c, ok := wide[4]
	if !ok {
		t.Fatal("system 4 is within radius 3 and must be a candidate")
	}
	if !c.InRing {
		t.Error("at radius 3 the geometry reaches the bulwark: InRing must be true")
	}
	if !c.IsBulwark || !c.Pinned {
		t.Errorf("bulwark badge/pin = %v/%v, want both true", c.IsBulwark, c.Pinned)
	}

	narrow := bySystem(DeriveStagingRing(u, d, frontline, RingOptions{
		MaxJumpsFromFront: 2, BulwarkSystemID: 4,
	}))
	c, ok = narrow[4]
	if !ok {
		t.Fatal("a pinned system must stay in the list even when the ring no longer reaches it")
	}
	if c.InRing {
		t.Error("at radius 2 system 4 is outside the ring: InRing must be false")
	}
	if c.JumpsToFront != 3 {
		t.Errorf("pinned-but-outside JumpsToFront = %d, want its true distance 3 -- the column stays honest", c.JumpsToFront)
	}
}

// TestDeriveStagingRing_ExcludeBeatsPin: exclusion is the stronger statement, so
// a bulwark written off stays written off.
func TestDeriveStagingRing_ExcludeBeatsPin(t *testing.T) {
	got := DeriveStagingRing(lineUniverse(), lineSDE(), map[int32]bool{1: true}, RingOptions{
		MaxJumpsFromFront: 2,
		BulwarkSystemID:   2,
		PinnedSystems:     map[int32]bool{2: true},
		ExcludedSystems:   map[int32]bool{2: true},
	})
	if _, ok := bySystem(got)[2]; ok {
		t.Error("an excluded system must never appear, badged and pinned or not")
	}
}

// TestSupplyRoute_LowsecIsCounted: the cheap half of haul risk. A route through
// one lowsec system is not a highsec route, and the count says how exposed it is.
func TestSupplyRoute_LowsecIsCounted(t *testing.T) {
	u := lineUniverse()
	u.SetSecurity(3, 0.2) // one lowsec system in the middle of the line

	jumps, lowsec, highsecRoute := supplyRoute(u, 6, 1)
	if jumps != 5 {
		t.Errorf("jumps = %d, want 5", jumps)
	}
	if lowsec != 1 {
		t.Errorf("lowsec jumps = %d, want 1", lowsec)
	}
	if highsecRoute {
		t.Error("the line is the only route and it passes through lowsec: HighsecRoute must be false")
	}

	if _, lowsec, highsecRoute := supplyRoute(lineUniverse(), 6, 1); lowsec != 0 || !highsecRoute {
		t.Errorf("all-highsec line gave lowsec=%d highsecRoute=%v, want 0/true", lowsec, highsecRoute)
	}

	// Source equal to destination is a valid no-op, not a -1.
	if jumps, lowsec, highsec := supplyRoute(lineUniverse(), 3, 3); jumps != 0 || lowsec != 0 || !highsec {
		t.Errorf("same-system route = %d/%d/%v, want 0/0/true", jumps, lowsec, highsec)
	}
}

// --- real universe ----------------------------------------------------------
//
// Everything below runs against the real gate graph and a frozen capture of
// /fw/systems/. The numbers asserted were measured, not remembered: each one is
// reproduced in the comment above its assertion, and a failure here means the
// measurement moved, not that the test is wrong.

const (
	sysOnnamon  int32 = 30045324 // Caldari bulwark,  0.56
	sysAmo      int32 = 30002055 // Minmatar bulwark, 0.47
	sysMehatoor int32 = 30002974 // Amarr bulwark,    0.66
	sysIntaki   int32 = 30003788 // Gallente bulwark, 0.60
	sysVillasen int32 = 30045309 // Caldari-held frontline, 0.14
	sysRakapas  int32 = 30045349 // Caldari-held frontline, 0.22
	sysHallanen int32 = 30045341 // the NPC-seeded book, 0.36
	sysJita     int32 = 30000142
)

// realUniverse is parsed once for the whole package: three files, ~13k rows, and
// no reason to redo it per test.
var (
	realOnce     sync.Once
	realUniverse *graph.Universe
	realSDE      *sde.Data
	realErr      string
)

// loadRealUniverse builds a graph.Universe from the SDE map files directly.
//
// sde.Load reads the whole SDE, which is far more than a question about 5,268
// stargates needs, so only the three files carrying the geometry are parsed. The
// topology is not a fixture of the universe -- it is the universe.
func loadRealUniverse(t *testing.T) (*graph.Universe, *sde.Data) {
	t.Helper()
	realOnce.Do(buildRealUniverse)
	if realErr != "" {
		t.Skip(realErr)
	}
	return realUniverse, realSDE
}

func buildRealUniverse() {
	dir := filepath.Join("..", "..", "data", "sde")
	u := graph.NewUniverse()
	d := &sde.Data{
		Systems:  map[int32]*sde.SolarSystem{},
		Stations: map[int64]*sde.Station{},
	}

	systems := scanJSONL(filepath.Join(dir, "mapSolarSystems.jsonl"), func(line []byte) bool {
		var row struct {
			Key            int32   `json:"_key"`
			RegionID       int32   `json:"regionID"`
			SecurityStatus float64 `json:"securityStatus"`
			Name           struct {
				EN string `json:"en"`
			} `json:"name"`
		}
		if json.Unmarshal(line, &row) != nil || row.Key == 0 {
			return false
		}
		d.Systems[row.Key] = &sde.SolarSystem{
			ID: row.Key, Name: row.Name.EN, RegionID: row.RegionID, Security: row.SecurityStatus,
		}
		u.SetSecurity(row.Key, row.SecurityStatus)
		u.SetRegion(row.Key, row.RegionID)
		return true
	})

	gates := scanJSONL(filepath.Join(dir, "mapStargates.jsonl"), func(line []byte) bool {
		var row struct {
			SolarSystemID int32 `json:"solarSystemID"`
			Destination   struct {
				SolarSystemID int32 `json:"solarSystemID"`
			} `json:"destination"`
		}
		if json.Unmarshal(line, &row) != nil {
			return false
		}
		if row.SolarSystemID == 0 || row.Destination.SolarSystemID == 0 {
			return false
		}
		u.AddGate(row.SolarSystemID, row.Destination.SolarSystemID)
		return true
	})

	stations := scanJSONL(filepath.Join(dir, "npcStations.jsonl"), func(line []byte) bool {
		var row struct {
			Key           int64 `json:"_key"`
			SolarSystemID int32 `json:"solarSystemID"`
		}
		if json.Unmarshal(line, &row) != nil || row.Key == 0 || row.SolarSystemID == 0 {
			return false
		}
		name := ""
		if s := d.Systems[row.SolarSystemID]; s != nil {
			name = s.Name
		}
		d.Stations[row.Key] = &sde.Station{ID: row.Key, Name: name, SystemID: row.SolarSystemID}
		return true
	})

	// A partial parse would silently weaken every assertion below into a tautology,
	// so refuse it outright rather than skip: the files are present but wrong.
	switch {
	case systems == 0 && gates == 0 && stations == 0:
		realErr = "SDE not present at data/sde"
	case systems < 5000 || gates < 10000 || stations < 3000:
		realErr = ""
		panic("SDE parsed short: " +
			itoa(systems) + " systems, " + itoa(gates) + " gates, " + itoa(stations) + " stations")
	}

	realUniverse, realSDE = u, d
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// scanJSONL returns how many lines fn accepted, or 0 when the file is absent.
func scanJSONL(path string, fn func([]byte) bool) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	n := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		if fn(scanner.Bytes()) {
			n++
		}
	}
	return n
}

// realFWSystems is the whole /fw/systems/ response, captured 2026-09-16 and
// frozen in testdata. All 160 rows, so the frontline sets below are the real
// ones rather than a hand-picked handful -- which is what makes the distances
// measurements instead of illustrations.
func realFWSystems(t *testing.T) []esi.FWSystem {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "fw_systems.json"))
	if err != nil {
		t.Fatalf("read fw systems testdata: %v", err)
	}
	var systems []esi.FWSystem
	if err := json.Unmarshal(raw, &systems); err != nil {
		t.Fatalf("unmarshal fw systems testdata: %v", err)
	}
	if len(systems) != 160 {
		t.Fatalf("testdata has %d systems, want the captured 160", len(systems))
	}
	return systems
}

// militiaCases pairs each militia with its bulwark and the measured distance
// from that bulwark to the nearest system the militia occupies.
//
// Measured against the frozen payload and the real gate graph:
//
//	Onnamon  -> Kinakka            1 jump
//	Amo      -> Resbroko / Auner   1 jump
//	Mehatoor -> Raa                1 jump
//	Intaki   -> Frarie / Loes      2 jumps
//
// The 2 is the whole reason DefaultMaxJumpsFromFront is 2.
var militiaCases = []struct {
	name       string
	militia    int32
	bulwark    int32
	wantJumps  int
	wantInRing map[int]bool // radius -> reached
}{
	{"Caldari", esi.MilitiaCaldari, sysOnnamon, 1, map[int]bool{1: true, 2: true}},
	{"Minmatar", esi.MilitiaMinmatar, sysAmo, 1, map[int]bool{1: true, 2: true}},
	{"Amarr", esi.MilitiaAmarr, sysMehatoor, 1, map[int]bool{1: true, 2: true}},
	{"Gallente", esi.MilitiaGallente, sysIntaki, 2, map[int]bool{1: false, 2: true}},
}

// TestStagingRing_RadiusIsCalibratedNotGuessed is the load-bearing measurement:
// radius 2 reaches every bulwark and radius 1 reaches every one but Intaki.
//
// The bulwarks are highsec and absent from /fw/systems/, so nothing but the gate
// graph can find them -- and if radius 1 were enough, the default would be 1.
// A failure here means the warzone or the map moved and the constant needs
// rechecking against live data; it does not mean the assertion should be
// loosened.
func TestStagingRing_RadiusIsCalibratedNotGuessed(t *testing.T) {
	u, _ := loadRealUniverse(t)
	fwSystems := realFWSystems(t)

	if DefaultMaxJumpsFromFront != 2 {
		t.Fatalf("DefaultMaxJumpsFromFront = %d: this test calibrates 2", DefaultMaxJumpsFromFront)
	}

	for _, tc := range militiaCases {
		t.Run(tc.name, func(t *testing.T) {
			if len(u.Adj[tc.bulwark]) == 0 {
				t.Fatalf("bulwark %d has no stargates in the SDE: the id is wrong", tc.bulwark)
			}
			if got := esi.BulwarkSystems[tc.militia]; got != tc.bulwark {
				t.Fatalf("esi.BulwarkSystems[%d] = %d, want %d", tc.militia, got, tc.bulwark)
			}

			frontline := esi.OccupiedBy(fwSystems, tc.militia)
			if len(frontline) == 0 {
				t.Fatalf("no occupied systems for %s in the captured payload", tc.name)
			}

			if got := distanceToSet(u, tc.bulwark, frontline); got != tc.wantJumps {
				t.Errorf("%s bulwark is %d jumps from its nearest occupied system, want %d",
					tc.name, got, tc.wantJumps)
			}

			for radius, want := range tc.wantInRing {
				_, reached := jumpsToNearest(u, frontline, radius)[tc.bulwark]
				if reached != want {
					t.Errorf("radius %d reaches %s bulwark = %v, want %v", radius, tc.name, reached, want)
				}
			}
		})
	}
}

// TestStagingRing_BulwarkIsPinnedNotRanked: the seed puts the bulwark in the
// list and badges it, and does nothing else. It must not jump the order, because
// a bulwark that has gone quiet still has to earn its place.
func TestStagingRing_BulwarkIsPinnedNotRanked(t *testing.T) {
	u, d := loadRealUniverse(t)
	frontline := esi.OccupiedBy(realFWSystems(t), esi.MilitiaCaldari)

	got := DeriveStagingRing(u, d, frontline, RingOptions{
		MaxJumpsFromFront: DefaultMaxJumpsFromFront,
		SourceSystemID:    sysJita,
		BulwarkSystemID:   sysOnnamon,
	})

	var badged, first int
	for i, c := range got {
		if c.IsBulwark {
			badged++
			if badged == 1 {
				first = i
			}
		}
		if c.Score != 0 {
			t.Fatalf("%s carries a score before any market data: geometry must not rank", c.StationName)
		}
	}
	// Onnamon has three stations, all badged.
	if badged != 3 {
		t.Errorf("badged Onnamon stations = %d, want 3", badged)
	}
	// Onnamon is 1 jump from the front, so the sort puts it behind every station
	// in contested space -- which is the point: the badge is not a promotion.
	if first == 0 {
		t.Error("the bulwark sorted to the very top before scoring: the seed is forcing a rank")
	}

	// Excluding it overrides the seed entirely.
	excluded := DeriveStagingRing(u, d, frontline, RingOptions{
		MaxJumpsFromFront: DefaultMaxJumpsFromFront,
		SourceSystemID:    sysJita,
		BulwarkSystemID:   sysOnnamon,
		ExcludedSystems:   map[int32]bool{sysOnnamon: true},
	})
	if _, ok := bySystem(excluded)[sysOnnamon]; ok {
		t.Error("excluding the bulwark must override the seed")
	}
}

// TestStagingRing_CaldariShape is the Onnamon case from the plan, asserted end to
// end: the ring reaches past the warzone into highsec and finds the destination
// the militia actually buys at.
//
// Measured against the frozen payload: 55 occupied systems, a radius-2 closure of
// 120 systems, 82 of them with a station, 49 of those outside the occupied set
// and 17 of them highsec. The plan's table reports that 49 -- the systems the
// ring adds; both readings are recorded here because they answer different
// questions, and only the highsec count bounds what a human has to eyeball.
func TestStagingRing_CaldariShape(t *testing.T) {
	u, d := loadRealUniverse(t)
	fwSystems := realFWSystems(t)

	frontline := esi.OccupiedBy(fwSystems, esi.MilitiaCaldari)
	if len(frontline) != 55 {
		t.Fatalf("Caldari occupy %d systems in the captured payload, want 55", len(frontline))
	}

	got := DeriveStagingRing(u, d, frontline, RingOptions{
		MaxJumpsFromFront: DefaultMaxJumpsFromFront,
		SourceSystemID:    sysJita,
		BulwarkSystemID:   sysOnnamon,
	})

	index := bySystem(got)
	outside, highsec := 0, 0
	for systemID, c := range index {
		if !frontline[systemID] {
			outside++
		}
		if c.Security >= highsecMinimum {
			highsec++
		}
	}
	if len(index) != 82 {
		t.Errorf("candidate systems = %d, want the measured 82", len(index))
	}
	if outside != 49 {
		t.Errorf("systems the ring adds outside occupied space = %d, want the measured 49", outside)
	}
	if highsec != 17 {
		t.Errorf("highsec candidate systems = %d, want the measured 17", highsec)
	}

	// The frontline hubs are candidates: they are the thin, dangerous option the
	// picker has to weigh Onnamon against, so leaving them out would hide the
	// tradeoff rather than resolve it.
	for _, want := range []struct {
		systemID int32
		name     string
	}{
		{sysVillasen, "Villasen"},
		{sysRakapas, "Rakapas"},
		{sysHallanen, "Hallanen"}, // ranked out later by the NPC-seed filter, not here
	} {
		c, ok := index[want.systemID]
		if !ok {
			t.Errorf("%s is occupied Caldari space with a station and must be a candidate", want.name)
			continue
		}
		if c.JumpsToFront != 0 {
			t.Errorf("%s is in contested space: JumpsToFront = %d, want 0", want.name, c.JumpsToFront)
		}
	}
}

// TestStagingRing_OnnamonIsTheHighsecDestination: the non-FW highsec system one
// jump from the front, with a clean supply route -- which is why the ring is not
// built from /fw/systems/ alone.
//
// Measured: Onnamon is 0.56, holds stations 60015070 / 60015131 / 60015184, and
// sits 7 jumps from Jita with no lowsec hop on the route.
func TestStagingRing_OnnamonIsTheHighsecDestination(t *testing.T) {
	u, d := loadRealUniverse(t)
	frontline := esi.OccupiedBy(realFWSystems(t), esi.MilitiaCaldari)

	if frontline[sysOnnamon] {
		t.Fatal("Onnamon is uncontested highsec: it must not be in the occupied set, or this test proves nothing")
	}

	got := DeriveStagingRing(u, d, frontline, RingOptions{
		MaxJumpsFromFront: DefaultMaxJumpsFromFront,
		SourceSystemID:    sysJita,
		BulwarkSystemID:   sysOnnamon,
	})

	var stations []int64
	var c FWStagingCandidate
	for _, cand := range got {
		if cand.SystemID == sysOnnamon {
			stations = append(stations, cand.StationID)
			c = cand
		}
	}
	if len(stations) == 0 {
		t.Fatal("Onnamon has stations and is 1 jump from the front: it must be a candidate")
	}
	wantStations := map[int64]bool{60015070: true, 60015131: true, 60015184: true}
	if len(stations) != len(wantStations) {
		t.Errorf("Onnamon stations = %v, want the three in the SDE", stations)
	}
	for _, id := range stations {
		if !wantStations[id] {
			t.Errorf("unexpected Onnamon station %d", id)
		}
	}

	if c.Security < highsecMinimum {
		t.Errorf("Onnamon security = %.2f, want highsec -- being safe is the whole point", c.Security)
	}
	if c.JumpsToFront != 1 {
		t.Errorf("Onnamon JumpsToFront = %d, want 1 (Kinakka)", c.JumpsToFront)
	}
	if !c.IsBulwark || !c.Pinned || !c.InRing {
		t.Errorf("Onnamon bulwark/pinned/inRing = %v/%v/%v, want all true -- the constant and the geometry agree",
			c.IsBulwark, c.Pinned, c.InRing)
	}
	if c.JumpsFromSource != 7 {
		t.Errorf("Jita to Onnamon = %d jumps, want the measured 7", c.JumpsFromSource)
	}
	if c.LowsecJumpsFromSource != 0 || !c.HighsecRoute {
		t.Errorf("Jita to Onnamon: lowsec hops = %d, highsecRoute = %v, want 0/true",
			c.LowsecJumpsFromSource, c.HighsecRoute)
	}

	// Villasen, the frontline alternative, is the opposite trade on every count.
	// The ring must show both rather than pick for the user.
	villasen, ok := bySystem(got)[sysVillasen]
	if !ok {
		t.Fatal("Villasen must be a candidate")
	}
	if villasen.Security >= highsecMinimum {
		t.Errorf("Villasen security = %.2f, want lowsec", villasen.Security)
	}
	if villasen.HighsecRoute {
		t.Error("Villasen is 0.14: there is no all-highsec route to it")
	}
	if villasen.LowsecJumpsFromSource == 0 {
		t.Error("the Jita run to Villasen leaves highsec and the count must say so")
	}
}

// TestStagingRing_IntakiPinSurvivesARadiusDrop is plan verification step 6 on the
// real map: at radius 2 Intaki is in the ring, at radius 1 it drops out of the
// ring but stays in the list as a pin. That pair is the proof the seed and the
// geometry are independent paths.
func TestStagingRing_IntakiPinSurvivesARadiusDrop(t *testing.T) {
	u, d := loadRealUniverse(t)
	frontline := esi.OccupiedBy(realFWSystems(t), esi.MilitiaGallente)

	for _, tc := range []struct {
		radius     int
		wantInRing bool
	}{
		{2, true},
		{1, false},
	} {
		got := bySystem(DeriveStagingRing(u, d, frontline, RingOptions{
			MaxJumpsFromFront: tc.radius,
			SourceSystemID:    sysJita,
			BulwarkSystemID:   sysIntaki,
		}))
		c, ok := got[sysIntaki]
		if !ok {
			t.Fatalf("radius %d: Intaki is pinned and must be in the list either way", tc.radius)
		}
		if c.InRing != tc.wantInRing {
			t.Errorf("radius %d: Intaki InRing = %v, want %v", tc.radius, c.InRing, tc.wantInRing)
		}
		if c.JumpsToFront != 2 {
			t.Errorf("radius %d: Intaki JumpsToFront = %d, want its true distance 2", tc.radius, c.JumpsToFront)
		}
	}
}
