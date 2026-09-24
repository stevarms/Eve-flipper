package esi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A trimmed capture of GET /loyalty/stores/1000180/offers/: the 10-run Raven
// Navy Issue offer and the ISK-only single-run one. The second has an empty
// required_items array, which must decode to an empty slice, not trip anything.
const loyaltyOffersFixture = `[
 {"ak_cost":0,"isk_cost":200000000,"lp_cost":200000,"offer_id":19596,"quantity":10,
  "required_items":[{"quantity":8,"type_id":93611}],"type_id":17637},
 {"ak_cost":0,"isk_cost":20000000,"lp_cost":100000,"offer_id":14798,"quantity":1,
  "required_items":[],"type_id":17637}
]`

func TestFetchLoyaltyOffersDecodesAndCaches(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, loyaltyOffersFixture)
	}))
	defer srv.Close()

	c := NewClient(nil)
	url := srv.URL + "/loyalty/stores/1000180/offers/"
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

	offers, err := c.fetchLoyaltyOffersFrom(url, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) != 2 {
		t.Fatalf("got %d offers", len(offers))
	}
	raven := offers[0]
	if raven.OfferID != 19596 || raven.Quantity != 10 || raven.LPCost != 200_000 || raven.ISKCost != 200_000_000 {
		t.Fatalf("raven offer decoded wrong: %+v", raven)
	}
	if len(raven.RequiredItems) != 1 || raven.RequiredItems[0].TypeID != 93611 || raven.RequiredItems[0].Quantity != 8 {
		t.Fatalf("required items decoded wrong: %+v", raven.RequiredItems)
	}

	if _, err := c.fetchLoyaltyOffersFrom(url, now.Add(59*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("a fetch inside the hour must come from cache; server hit %d times", hits.Load())
	}
	if _, err := c.fetchLoyaltyOffersFrom(url, now.Add(61*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Fatalf("a fetch after the hour must refetch; server hit %d times", hits.Load())
	}
}

func TestResolveNamesCachesAndSkipsKnownIDs(t *testing.T) {
	var asked [][]int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ids []int64
		_ = json.NewDecoder(r.Body).Decode(&ids)
		asked = append(asked, ids)
		out := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			out = append(out, map[string]any{"id": id, "name": map[int64]string{7_000_001: "State Protectorate", 7_000_002: "Federal Defense Union"}[id], "category": "corporation"})
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	c := NewClient(nil)
	names, err := c.resolveNamesAt(srv.URL, []int64{7_000_001, 7_000_002, 7_000_001, 0})
	if err != nil {
		t.Fatal(err)
	}
	if names[7_000_001] != "State Protectorate" || names[7_000_002] != "Federal Defense Union" {
		t.Fatalf("names = %v", names)
	}
	if len(asked) != 1 || len(asked[0]) != 2 {
		t.Fatalf("first call should ask once for the two distinct IDs, asked %v", asked)
	}

	names, err = c.resolveNamesAt(srv.URL, []int64{7_000_002})
	if err != nil {
		t.Fatal(err)
	}
	if names[7_000_002] != "Federal Defense Union" || len(asked) != 1 {
		t.Fatalf("a known name must come from cache; asked %v", asked)
	}
}
