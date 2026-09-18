package zkillboard

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestStaticListsMatchTheSDE is the guard for the failure these lists shipped
// with until 2026-09-17: every ID was a real published item, carrying another
// item's name.
//
// That is worse than an invalid ID. An invalid ID yields no price and the row
// vanishes; a valid ID under the wrong name yields a perfectly plausible price
// for something else entirely, and there is nothing on screen to notice. Names
// are what a person reads, so the name is what must be true.
//
// The check reads the SDE directly rather than through sde.Load, which parses far
// more than a question about 53 type names needs. data/ is not tracked, so the
// test skips where the SDE has not been downloaded -- it is a guard for the
// machine where the lists get edited.
func TestStaticListsMatchTheSDE(t *testing.T) {
	names := sdeTypeNames(t)

	for _, entry := range commonPvPModules {
		assertTypeName(t, names, entry.TypeID, entry.Name)
		if entry.Category != "module" {
			t.Errorf("%s (%d) is categorized %q in commonPvPModules", entry.Name, entry.TypeID, entry.Category)
		}
	}
	for _, entry := range commonAmmo {
		assertTypeName(t, names, entry.TypeID, entry.Name)
	}
}

func assertTypeName(t *testing.T, names map[int32]string, typeID int32, want string) {
	t.Helper()
	got, ok := names[typeID]
	if !ok {
		t.Errorf("type %d is listed as %q but is not a published type in the SDE", typeID, want)
		return
	}
	if got != want {
		t.Errorf("type %d is listed as %q but the SDE calls it %q -- a real item under the wrong "+
			"name prices plausibly and is invisible on screen", typeID, want, got)
	}
}

// TestStaticListsHaveNoDuplicates: the old modules list carried Damage Control II
// twice, under two different IDs, neither of which was Damage Control II. A
// duplicate name is the cheap half of that signal and needs no SDE to catch, so
// this one always runs.
func TestStaticListsHaveNoDuplicates(t *testing.T) {
	type entry struct {
		TypeID int32
		Name   string
	}
	var all []entry
	for _, m := range commonPvPModules {
		all = append(all, entry{m.TypeID, m.Name})
	}
	for _, a := range commonAmmo {
		all = append(all, entry{a.TypeID, a.Name})
	}

	byID := make(map[int32]string, len(all))
	byName := make(map[string]int32, len(all))
	for _, e := range all {
		if first, seen := byID[e.TypeID]; seen {
			t.Errorf("type %d appears twice, as %q and %q", e.TypeID, first, e.Name)
		}
		byID[e.TypeID] = e.Name
		if first, seen := byName[e.Name]; seen {
			t.Errorf("%q appears twice, as type %d and type %d -- at most one can be right", e.Name, first, e.TypeID)
		}
		byName[e.Name] = e.TypeID
	}
}

// sdeTypeNames reads published type names out of data/sde/types.jsonl, or skips.
func sdeTypeNames(t *testing.T) map[int32]string {
	t.Helper()

	path := filepath.Join("..", "..", "data", "sde", "types.jsonl")
	file, err := os.Open(path)
	if err != nil {
		t.Skipf("no local SDE to check against (%v); run the SDE download to enable this guard", err)
	}
	defer file.Close()

	names := make(map[int32]string, 60000)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var row struct {
			Key       int32 `json:"_key"`
			Published bool  `json:"published"`
			Name      struct {
				EN string `json:"en"`
			} `json:"name"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			continue
		}
		if row.Published && row.Name.EN != "" {
			names[row.Key] = row.Name.EN
		}
	}
	if err := scanner.Err(); err != nil {
		t.Skipf("could not read %s: %v", path, err)
	}
	// A short read would weaken every assertion below into a skip-by-absence, so
	// fail rather than pass on a truncated file.
	if len(names) < 20000 {
		t.Fatalf("parsed only %d published types from %s; the SDE has tens of thousands, "+
			"so this file is truncated and the check would be meaningless", len(names), path)
	}
	return names
}
