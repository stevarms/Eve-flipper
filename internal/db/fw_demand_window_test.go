package db

import (
	"strings"
	"testing"
	"time"

	"eve-flipper/internal/engine"
)

// fw_demand_window_test.go -- migration v53's two columns and the coverage
// sidecar beside them.
//
// The columns are ordinary. The sidecar is not: it holds the sentences that say a
// cached demand sample is incomplete, and those rates are already scaled to what
// was covered, so they are correct and they look whole. If a warning does not
// survive this round trip then the second read of a truncated quarter is the one
// that looks authoritative, which is worse than not caching it at all.

// TestFWCampaignDemandWindowDefaults: a campaign written without either column --
// which is every campaign that existed before v53, and every one created by a
// client that does not know about the setting -- reads back with the long window
// off and sizing against the short one.
//
// "Off" is the only safe default in that direction. A campaign inheriting a long
// window it never asked for would pay a three hundred page walk on its next
// regeneration, and one inheriting size_against = long with nothing measured would
// size every row against zero, which is unbounded cover, which reads as already
// stocked.
func TestFWCampaignDemandWindowDefaults(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	// fwTestCampaign sets neither field, exactly as a pre-v53 row would have.
	id, err := d.SaveFWCampaign(fwTestCampaign(fwUserA))
	if err != nil {
		t.Fatalf("save campaign: %v", err)
	}
	got, err := d.GetFWCampaign(fwUserA, id)
	if err != nil || got == nil {
		t.Fatalf("get campaign: %v (%v)", err, got)
	}
	if got.LongDemandWindowSeconds != 0 {
		t.Errorf("long window defaulted to %d, want 0 (off)", got.LongDemandWindowSeconds)
	}
	if got.SizeAgainst != engine.FWSizedByShort {
		t.Errorf("size_against defaulted to %q, want %q", got.SizeAgainst, engine.FWSizedByShort)
	}
	if got.SizesAgainstLong() {
		t.Error("a campaign with no long window reported that it sizes against one")
	}
	if got.SupplyConfig().SizeAgainstLong {
		t.Error("the supply config asked the engine to size against a window nothing measured")
	}
}

// TestFWCampaignDemandWindowRoundTrip: both columns survive a write, an update and
// a read, and the pair resolves to a single answer the engine can act on.
//
// The last assertion is the load-bearing one. The two settings can disagree --
// turning the long window off does not rewrite size_against, and should not, since
// switching it back on ought to restore the choice. What must never happen is that
// disagreement reaching the engine, because sizing against an unmeasured window is
// sizing against zero.
func TestFWCampaignDemandWindowRoundTrip(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	c := fwTestCampaign(fwUserA)
	c.LongDemandWindowSeconds = 7_776_000
	c.SizeAgainst = engine.FWSizedByLong
	id, err := d.SaveFWCampaign(c)
	if err != nil {
		t.Fatalf("save campaign: %v", err)
	}

	got, err := d.GetFWCampaign(fwUserA, id)
	if err != nil || got == nil {
		t.Fatalf("get campaign: %v (%v)", err, got)
	}
	if got.LongDemandWindowSeconds != 7_776_000 || got.SizeAgainst != engine.FWSizedByLong {
		t.Fatalf("round-tripped as window %d, size_against %q", got.LongDemandWindowSeconds, got.SizeAgainst)
	}
	if !got.SizesAgainstLong() || !got.SupplyConfig().SizeAgainstLong {
		t.Error("ninety days configured and long sizing asked for, yet the engine was told to size short")
	}

	// Thirty days, still sizing long: an update must carry both columns, not just
	// the one that changed.
	got.LongDemandWindowSeconds = 2_592_000
	if _, err := d.SaveFWCampaign(got); err != nil {
		t.Fatalf("update campaign: %v", err)
	}
	again, err := d.GetFWCampaign(fwUserA, id)
	if err != nil || again == nil {
		t.Fatalf("re-read campaign: %v (%v)", err, again)
	}
	if again.LongDemandWindowSeconds != 2_592_000 || again.SizeAgainst != engine.FWSizedByLong {
		t.Errorf("update lost a column: window %d, size_against %q", again.LongDemandWindowSeconds, again.SizeAgainst)
	}

	// The window off, the stored preference kept: the settings pair may be
	// inconsistent, the plan may not.
	again.LongDemandWindowSeconds = 0
	if _, err := d.SaveFWCampaign(again); err != nil {
		t.Fatalf("update campaign: %v", err)
	}
	off, err := d.GetFWCampaign(fwUserA, id)
	if err != nil || off == nil {
		t.Fatalf("re-read campaign: %v (%v)", err, off)
	}
	if off.SizeAgainst != engine.FWSizedByLong {
		t.Errorf("switching the window off rewrote the preference to %q, so switching it back on would lose the choice", off.SizeAgainst)
	}
	if off.SizesAgainstLong() || off.SupplyConfig().SizeAgainstLong {
		t.Error("a campaign with the long window off still told the engine to size against it -- every row would read as covered")
	}
}

// TestDemandScopeCoverageRoundTrip: everything the sidecar exists to carry comes
// back, including the warning text verbatim.
//
// Verbatim matters. The warning names a month and says roughly how many days of it
// are missing; paraphrasing it on the way through, or storing a flag and
// regenerating a sentence on read, would leave the sentence describing the code's
// idea of the gap rather than the walk's measurement of it.
func TestDemandScopeCoverageRoundTrip(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	scope := MilitiaDemandScope(500001, 7_776_000)
	const warning = "September 2026 hit zkillboard's 100-page limit, so its oldest ~4 days are not in the sample; the rate is scaled to what was actually covered"
	want := DemandScopeCoverage{
		Scope:          scope,
		FetchedKills:   61_000,
		InWarzoneKills: 17_400,
		CoveredSeconds: 7_000_000,
		Truncated:      true,
		Warnings:       []string{warning},
		MonthsJSON:     `[{"year":2026,"month":9,"truncated":true}]`,
	}
	if err := d.SaveDemandScopeCoverage(want); err != nil {
		t.Fatalf("save coverage: %v", err)
	}

	got, err := d.GetDemandScopeCoverage(scope)
	if err != nil {
		t.Fatalf("get coverage: %v", err)
	}
	if got == nil {
		t.Fatal("nothing came back, so a truncated sample would read as complete")
	}
	if got.FetchedKills != 61_000 || got.InWarzoneKills != 17_400 {
		t.Errorf("counts round-tripped as %d/%d", got.FetchedKills, got.InWarzoneKills)
	}
	if got.CoveredSeconds != 7_000_000 {
		t.Errorf("covered seconds round-tripped as %v", got.CoveredSeconds)
	}
	if !got.Truncated {
		t.Error("truncation round-tripped as false -- the one bit that must not be lost")
	}
	if len(got.Warnings) != 1 || got.Warnings[0] != warning {
		t.Errorf("warning did not survive verbatim: %v", got.Warnings)
	}
	if !strings.Contains(got.MonthsJSON, `"month":9`) {
		t.Errorf("per-month coverage round-tripped as %q", got.MonthsJSON)
	}
	if got.UpdatedAt.IsZero() || time.Since(got.UpdatedAt) > time.Hour {
		t.Errorf("updated_at round-tripped as %v", got.UpdatedAt)
	}

	// Re-saving replaces rather than accumulates, so the record always describes
	// the last fetch and not the union of every fetch.
	want.Truncated = false
	want.Warnings = nil
	want.CoveredSeconds = 7_776_000
	if err := d.SaveDemandScopeCoverage(want); err != nil {
		t.Fatalf("re-save coverage: %v", err)
	}
	clean, err := d.GetDemandScopeCoverage(scope)
	if err != nil || clean == nil {
		t.Fatalf("re-read coverage: %v (%v)", err, clean)
	}
	if clean.Truncated || len(clean.Warnings) != 0 || clean.CoveredSeconds != 7_776_000 {
		t.Errorf("a complete refetch did not clear the earlier gap: %+v", clean)
	}
}

// TestDemandScopeCoverageKeepsTheWindowsApart: the same militia at seven days and
// at ninety are two samples with two different completeness stories, and the
// window is the only thing in the key separating them.
//
// A collision here would attach the quarter's truncation warning to the week's
// rates, or worse, the week's clean record to the quarter's holed ones.
func TestDemandScopeCoverageKeepsTheWindowsApart(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	short := MilitiaDemandScope(500001, 604_800)
	long := MilitiaDemandScope(500001, 7_776_000)

	if err := d.SaveDemandScopeCoverage(DemandScopeCoverage{
		Scope: short, FetchedKills: 5_000, CoveredSeconds: 604_800,
	}); err != nil {
		t.Fatalf("save short: %v", err)
	}
	if err := d.SaveDemandScopeCoverage(DemandScopeCoverage{
		Scope: long, FetchedKills: 61_000, CoveredSeconds: 7_000_000,
		Truncated: true, Warnings: []string{"September 2026 hit zkillboard's 100-page limit"},
	}); err != nil {
		t.Fatalf("save long: %v", err)
	}

	gotShort, err := d.GetDemandScopeCoverage(short)
	if err != nil || gotShort == nil {
		t.Fatalf("get short: %v (%v)", err, gotShort)
	}
	gotLong, err := d.GetDemandScopeCoverage(long)
	if err != nil || gotLong == nil {
		t.Fatalf("get long: %v (%v)", err, gotLong)
	}
	if gotShort.Truncated || len(gotShort.Warnings) != 0 {
		t.Errorf("the quarter's truncation leaked onto the week: %+v", gotShort)
	}
	if !gotLong.Truncated || len(gotLong.Warnings) != 1 {
		t.Errorf("the week's clean record overwrote the quarter's gap: %+v", gotLong)
	}
	if gotShort.FetchedKills != 5_000 || gotLong.FetchedKills != 61_000 {
		t.Errorf("counts crossed: short %d, long %d", gotShort.FetchedKills, gotLong.FetchedKills)
	}
}

// TestDemandScopeCoverageAbsentIsNotComplete: a scope that was never recorded
// returns nil and no error, and the distinction is the whole reason this table
// exists. Nil means "cannot say how much of the window these rates span", which
// the caller turns into a sentence -- it does not mean they span all of it.
func TestDemandScopeCoverageAbsentIsNotComplete(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	got, err := d.GetDemandScopeCoverage(MilitiaDemandScope(500002, 7_776_000))
	if err != nil {
		t.Fatalf("an unrecorded scope errored instead of returning nothing: %v", err)
	}
	if got != nil {
		t.Errorf("an unrecorded scope returned a record: %+v", got)
	}

	// And a scope that addresses nothing is refused rather than stored under an
	// empty key, where nothing would ever read it back.
	if err := d.SaveDemandScopeCoverage(DemandScopeCoverage{Scope: DemandScope{WindowSeconds: 604_800}}); err == nil {
		t.Error("coverage was stored under a scope with no kind and no id")
	}
}
