package api

import (
	"strings"
	"testing"
	"time"

	"eve-flipper/internal/db"
	"eve-flipper/internal/zkillboard"
)

// fw_demand_test.go -- the two windows, and the one way caching them can lie.
//
// A long walk can come back with a hole in the middle of it. The rates are scaled
// to the time actually covered, so they are correct AND they look complete; the
// only thing that says otherwise is a sentence stored beside them. Every test
// here is ultimately about that sentence surviving the round trip, because the
// read that comes back from cache is the one that looks authoritative.

// TestFWNormalizeLongWindow pins the three answers that are not the identity.
//
// The interesting one is the middle: a "long" window no longer than the short one
// is off rather than an error, because fetching the same seven days twice and
// showing a reader two identical columns is a worse answer than showing one.
func TestFWNormalizeLongWindow(t *testing.T) {
	cases := []struct {
		in, want int
		why      string
	}{
		{0, 0, "unset is off"},
		{-1, 0, "nonsense is off, not negative"},
		{3600, 0, "an hour is shorter than the short window, so it measures nothing new"},
		{fwDemandWindowSeconds, 0, "exactly seven days would be the same column twice"},
		{fwDemandWindowSeconds + 3600, fwDemandWindowSeconds + 3600, "anything genuinely longer is kept"},
		{fwLongWindow30d, fwLongWindow30d, "thirty days"},
		{fwLongWindow90d, fwLongWindow90d, "ninety days"},
		{fwLongWindow90d + 1, fwLongWindow90d, "past the walk budget, clamped rather than walked"},
		{365 * 86400, fwLongWindow90d, "a year clamps too, instead of becoming an unbounded fetch"},
	}
	for _, c := range cases {
		if got := fwNormalizeLongWindow(c.in); got != c.want {
			t.Errorf("fwNormalizeLongWindow(%d) = %d, want %d -- %s", c.in, got, c.want, c.why)
		}
	}
}

// TestFWDemandFreshnessScalesWithWindow: the expensive sample is the one allowed
// to be old.
//
// A ninety-day rate does not move hour to hour, and refetching it is a three
// hundred page walk; a seven-day rate moves faster and costs twenty-five pages.
// Scaling freshness to the window is what makes the long window affordable enough
// to leave switched on -- without it, every plan regeneration pays the full walk.
func TestFWDemandFreshnessScalesWithWindow(t *testing.T) {
	short := fwDemandFreshness(fwDemandWindowSeconds)
	thirty := fwDemandFreshness(fwLongWindow30d)
	ninety := fwDemandFreshness(fwLongWindow90d)

	if !(short < thirty && thirty < ninety) {
		t.Fatalf("freshness did not scale with the window: 7d=%v 30d=%v 90d=%v", short, thirty, ninety)
	}
	if short < 5*time.Hour || short > 7*time.Hour {
		t.Errorf("the seven-day sample should stay servable about six hours, got %v", short)
	}
	if ninety < 72*time.Hour {
		t.Errorf("the ninety-day sample should stay servable about three days, got %v", ninety)
	}
	// The floor, so nothing can ever turn into a refetch per request.
	if got := fwDemandFreshness(60); got != time.Hour {
		t.Errorf("tiny window freshness: got %v want the one-hour floor", got)
	}
	if got := fwDemandFreshness(0); got != short {
		t.Errorf("an unset window should be read as the short one: got %v want %v", got, short)
	}
}

// TestFWDemandCacheKeepsTheWindowsApart is the collision test.
//
// Both windows measure the same militia in the same warzone, and the only thing
// separating a seven-day rate from a ninety-day one in the cache is the window in
// the key. If they collided, the second fetch would overwrite the first and the
// gap table would show one number twice -- which is exactly the blended figure
// the two-column design exists to avoid, arrived at by accident.
func TestFWDemandCacheKeepsTheWindowsApart(t *testing.T) {
	database := openAPITestDB(t)
	s := &Server{db: database}

	shortScope := db.MilitiaDemandScope(500001, int64(fwDemandWindowSeconds))
	longScope := db.MilitiaDemandScope(500001, int64(fwLongWindow90d))

	s.fwStoreMilitiaDemand(shortScope, &zkillboard.MilitiaDemandProfile{
		MilitiaFactionID: 500001,
		WindowSeconds:    fwDemandWindowSeconds,
		FetchedKills:     5000,
		InWarzoneKills:   1400,
		Items: map[int32]*zkillboard.ItemDemandProfile{
			24492: {TypeID: 24492, TypeName: "Inferno Light Missile", KillmailCount: 180, EstDailyDemand: 4553},
		},
	})
	s.fwStoreMilitiaDemand(longScope, &zkillboard.MilitiaDemandProfile{
		MilitiaFactionID: 500001,
		WindowSeconds:    fwLongWindow90d,
		FetchedKills:     69000,
		InWarzoneKills:   20000,
		CoveredSeconds:   7_000_000,
		Truncated:        true,
		Warnings:         []string{"September 2026 hit zkillboard's 100-page limit"},
		Items: map[int32]*zkillboard.ItemDemandProfile{
			24492: {TypeID: 24492, TypeName: "Inferno Light Missile", KillmailCount: 1900, EstDailyDemand: 1200},
			1877:  {TypeID: 1877, TypeName: "Scourge Fury Light Missile", KillmailCount: 640, EstDailyDemand: 900},
		},
	})

	gotShort, err := s.fwCachedMilitiaDemand(shortScope, fwDemandWindowSeconds)
	if err != nil {
		t.Fatalf("read back the short window: %v", err)
	}
	gotLong, err := s.fwCachedMilitiaDemand(longScope, fwLongWindow90d)
	if err != nil {
		t.Fatalf("read back the long window: %v", err)
	}
	if gotShort == nil || gotLong == nil {
		t.Fatal("one of the two windows was not stored at all")
	}

	if len(gotShort.Items) != 1 {
		t.Errorf("the short window gained rows from the long one: %d items", len(gotShort.Items))
	}
	if len(gotLong.Items) != 2 {
		t.Errorf("the long window lost rows to the short one: %d items", len(gotLong.Items))
	}
	if r := gotShort.Items[24492].EstDailyDemand; r != 4553 {
		t.Errorf("the short rate was overwritten by the long one: got %v want 4553", r)
	}
	if r := gotLong.Items[24492].EstDailyDemand; r != 1200 {
		t.Errorf("the long rate was overwritten by the short one: got %v want 1200", r)
	}

	// Freshness is per window too, so a stored short sample cannot make a long one
	// look fresh -- that would serve nothing where the reader expects a quarter.
	if !database.IsFittingProfileFresh(longScope, fwDemandFreshness(fwLongWindow90d)) {
		t.Error("a just-written long sample did not read as fresh")
	}
	if database.IsFittingProfileFresh(db.MilitiaDemandScope(500001, int64(fwLongWindow30d)), time.Hour) {
		t.Error("a window that was never fetched read as fresh")
	}
}

// TestFWDemandCacheCarriesItsWarnings is the load-bearing one.
//
// The rates in a truncated sample are already scaled to what was covered, so they
// are correct and they look complete. Reading them back without the sentence that
// says a month is missing is worse than not caching at all, because the cached
// read is the one a reader trusts. Coverage, truncation and every warning have to
// come back with the numbers.
func TestFWDemandCacheCarriesItsWarnings(t *testing.T) {
	database := openAPITestDB(t)
	s := &Server{db: database}
	scope := db.MilitiaDemandScope(500004, int64(fwLongWindow90d))

	const holeWarning = "September 2026 hit zkillboard's 100-page limit, so its oldest ~4 days are not in the sample"
	s.fwStoreMilitiaDemand(scope, &zkillboard.MilitiaDemandProfile{
		MilitiaFactionID: 500004,
		WindowSeconds:    fwLongWindow90d,
		FetchedKills:     61000,
		InWarzoneKills:   17400,
		CoveredSeconds:   7_000_000,
		Truncated:        true,
		Warnings:         []string{holeWarning},
		Months:           []zkillboard.MonthCoverage{{Year: 2026, Month: 9, Truncated: true}},
		Items: map[int32]*zkillboard.ItemDemandProfile{
			24492: {TypeID: 24492, TypeName: "Inferno Light Missile", KillmailCount: 1900, EstDailyDemand: 1200},
		},
	})

	got, err := s.fwCachedMilitiaDemand(scope, fwLongWindow90d)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got == nil {
		t.Fatal("nothing came back from the cache")
	}
	if !got.Truncated {
		t.Error("a truncated sample read back as complete -- this is the failure the coverage table exists to prevent")
	}
	if got.CoveredSeconds != 7_000_000 {
		t.Errorf("covered seconds lost: got %v want 7000000", got.CoveredSeconds)
	}
	if got.FetchedKills != 61000 || got.InWarzoneKills != 17400 {
		t.Errorf("the sample's own counts were lost: %d fetched, %d in warzone", got.FetchedKills, got.InWarzoneKills)
	}
	if len(got.Months) != 1 || !got.Months[0].Truncated || got.Months[0].Month != 9 {
		t.Errorf("per-month coverage lost: %+v", got.Months)
	}
	found := false
	for _, w := range got.Warnings {
		if strings.Contains(w, "100-page limit") {
			found = true
		}
	}
	if !found {
		t.Errorf("the truncation warning did not survive the cache: %v", got.Warnings)
	}
}

// TestFWCachedDemandWithoutCoverageSaysSo: nil coverage is not "the fetch was
// complete".
//
// The two halves are written separately and on purpose in that order, so a
// failure between them leaves rates with no coverage record. What the reader must
// not get in that case is silence -- the honest answer is that how much of the
// window these rates span is unknown, and that is a different sentence from
// saying they span all of it.
func TestFWCachedDemandWithoutCoverageSaysSo(t *testing.T) {
	database := openAPITestDB(t)
	s := &Server{db: database}
	scope := db.MilitiaDemandScope(500002, int64(fwLongWindow30d))

	// Rates only: exactly the state a crash between the two writes leaves behind.
	if err := database.SaveFittingDemandProfile(scope, []db.FittingDemandItem{
		{TypeID: 24492, TypeName: "Inferno Light Missile", KillmailCount: 900, EstDailyDemand: 1300},
	}); err != nil {
		t.Fatalf("save rates: %v", err)
	}

	got, err := s.fwCachedMilitiaDemand(scope, fwLongWindow30d)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got == nil {
		t.Fatal("rates with no coverage record came back as nothing at all")
	}
	if len(got.Items) != 1 {
		t.Errorf("the rates themselves were lost: %d items", len(got.Items))
	}
	said := false
	for _, w := range got.Warnings {
		if strings.Contains(w, "unknown") {
			said = true
		}
	}
	if !said {
		t.Errorf("a sample of unknown completeness was served silently: %v", got.Warnings)
	}
}

// TestFWStoreDemandRecordsTheShortWindowAsWhole: the seven-day path has one
// request path and one contiguous sample, so it cannot have a hole and does not
// measure coverage. Recording the window it asked for is the true statement
// there, and it keeps a reader from having to know which path produced the row.
func TestFWStoreDemandRecordsTheShortWindowAsWhole(t *testing.T) {
	database := openAPITestDB(t)
	s := &Server{db: database}
	scope := db.MilitiaDemandScope(500003, int64(fwDemandWindowSeconds))

	s.fwStoreMilitiaDemand(scope, &zkillboard.MilitiaDemandProfile{
		MilitiaFactionID: 500003,
		WindowSeconds:    fwDemandWindowSeconds,
		Items: map[int32]*zkillboard.ItemDemandProfile{
			24492: {TypeID: 24492, KillmailCount: 180, EstDailyDemand: 4553},
		},
	})

	cov, err := s.db.GetDemandScopeCoverage(scope)
	if err != nil {
		t.Fatalf("read coverage: %v", err)
	}
	if cov == nil {
		t.Fatal("the short path stored no coverage record, so its rates will read as of unknown completeness")
	}
	if cov.CoveredSeconds != float64(fwDemandWindowSeconds) {
		t.Errorf("short window coverage: got %v want %d", cov.CoveredSeconds, fwDemandWindowSeconds)
	}
	if cov.Truncated {
		t.Error("the short path reported itself truncated")
	}

	// And no warning about unknown completeness, because it is known.
	got, err := s.fwCachedMilitiaDemand(scope, fwDemandWindowSeconds)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	for _, w := range got.Warnings {
		if strings.Contains(w, "unknown") {
			t.Errorf("the short path warned about its own completeness: %v", got.Warnings)
		}
	}
}
