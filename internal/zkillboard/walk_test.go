package zkillboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

/* The long demand window's fetch.
 *
 * Everything here runs against a fake transport rather than zkillboard, because
 * the facts being asserted are about the walk's arithmetic and its memory
 * behaviour, not about the API -- those were established live: pastSeconds is
 * capped at 604800, startTime and endTime are deprecated, and the month path is
 * the only way further back, with its own 100-page limit and 200 killmails a page.
 */

// monthPageRE pulls (year, month, page) out of a month-path URL.
var monthPageRE = regexp.MustCompile(`/losses/factionID/(\d+)/year/(\d+)/month/(\d+)/page/(\d+)/`)

// fakeZkill serves month pages from a function, so a test states the shape of the
// data rather than a table of fixtures.
type fakeZkill struct {
	// page returns the killmails for one (year, month, page). A short slice ends
	// the month, exactly as zkillboard's own paging does.
	page func(year, month, page int) []ESIKillmail

	// fail, when set and true for a request, makes it a 500.
	fail func(year, month, page int) bool

	requests   atomic.Int64
	livePages  atomic.Int64
	maxLive    atomic.Int64
	lastURL    atomic.Value
	monthPages map[string]int
}

func (f *fakeZkill) RoundTrip(req *http.Request) (*http.Response, error) {
	f.requests.Add(1)
	f.lastURL.Store(req.URL.String())

	m := monthPageRE.FindStringSubmatch(req.URL.Path)
	if m == nil {
		return &http.Response{StatusCode: 404, Body: http.NoBody, Header: http.Header{}}, nil
	}
	year, _ := strconv.Atoi(m[2])
	month, _ := strconv.Atoi(m[3])
	page, _ := strconv.Atoi(m[4])

	if f.monthPages == nil {
		f.monthPages = map[string]int{}
	}
	f.monthPages[fmt.Sprintf("%d-%02d", year, month)]++

	if f.fail != nil && f.fail(year, month, page) {
		return &http.Response{
			StatusCode: 500,
			Body:       http.NoBody,
			Header:     http.Header{},
		}, nil
	}

	kills := f.page(year, month, page)
	body, err := json.Marshal(kills)
	if err != nil {
		return nil, err
	}

	// Count this page as in flight until the walk drops it. The response body is
	// closed by getJSON as soon as it is decoded, so this measures how many pages
	// the client itself is holding, which is the memory claim under test.
	live := f.livePages.Add(1)
	for {
		max := f.maxLive.Load()
		if live <= max || f.maxLive.CompareAndSwap(max, live) {
			break
		}
	}

	return &http.Response{
		StatusCode: 200,
		Body:       &countingBody{r: strings.NewReader(string(body)), onClose: func() { f.livePages.Add(-1) }},
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

type countingBody struct {
	r       *strings.Reader
	onClose func()
	closed  bool
}

func (b *countingBody) Read(p []byte) (int, error) { return b.r.Read(p) }
func (b *countingBody) Close() error {
	if !b.closed {
		b.closed = true
		b.onClose()
	}
	return nil
}

// walkTestClient is a Client that talks to f and does not sleep between pages.
func walkTestClient(f *fakeZkill) *Client {
	c := NewClient()
	c.http = &http.Client{Transport: f}
	c.minInterval = 0
	return c
}

// fullPage is a page of exactly zkillPageSize losses, which is what tells the
// walk there is another page behind it.
func fullPage(systemID int32, base int64, newest time.Time, step time.Duration) []ESIKillmail {
	kills := make([]ESIKillmail, zkillPageSize)
	for i := range kills {
		kills[i] = lossIn(systemID, base+int64(i), newest.Add(-time.Duration(i)*step), 2679, 100)
	}
	return kills
}

func TestGetFactionLossesMonthPage_URLShape(t *testing.T) {
	f := &fakeZkill{page: func(year, month, page int) []ESIKillmail {
		return []ESIKillmail{lossIn(30045334, 1, time.Now(), 2679, 100)}
	}}
	c := walkTestClient(f)

	kills, err := c.GetFactionLossesMonthPage(500001, 2026, 9, 3)
	if err != nil {
		t.Fatalf("GetFactionLossesMonthPage: %v", err)
	}
	if len(kills) != 1 {
		t.Fatalf("got %d killmails, want 1", len(kills))
	}

	got, _ := f.lastURL.Load().(string)
	// The trailing slash is mandatory and the month is required alongside the
	// year -- a year on its own is ignored, which returns the current month's
	// data under a URL that looks like it asked for the whole year.
	want := "/api/losses/factionID/500001/year/2026/month/9/page/3/"
	if u, _ := url.Parse(got); u == nil || u.Path != want {
		t.Errorf("URL path = %q, want %q", got, want)
	}
}

// TestWalkFactionLossesSince_StopsAtTheWindowEdge: the walk must stop when a page
// reaches past since, and must not hand the caller killmails from outside the
// window it asked for -- those would be counted against a denominator that stops
// at since, which inflates the rate.
func TestWalkFactionLossesSince_StopsAtTheWindowEdge(t *testing.T) {
	now := time.Now().UTC()
	since := now.Add(-10 * 24 * time.Hour)

	// Every page is a full page an hour apart, so page 5 of the current month
	// reaches about 40 days back and crosses since.
	f := &fakeZkill{page: func(year, month, page int) []ESIKillmail {
		start := now.Add(-time.Duration(page-1) * zkillPageSize * time.Hour)
		return fullPage(30045334, int64(page)*1000, start, time.Hour)
	}}
	c := walkTestClient(f)

	var folded []ESIKillmail
	walk, err := c.WalkFactionLossesSince(context.Background(), 500001, since, func(page []ESIKillmail) error {
		folded = append(folded, page...)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkFactionLossesSince: %v", err)
	}

	if len(folded) == 0 {
		t.Fatal("the walk folded nothing")
	}
	for _, km := range folded {
		when, err := time.Parse(time.RFC3339, km.KillmailTime)
		if err != nil {
			t.Fatalf("unparseable time %q", km.KillmailTime)
		}
		if when.Before(since) {
			t.Errorf("killmail %d at %s is older than since %s", km.KillmailID, when, since)
		}
	}
	if walk.Killmails != len(folded) {
		t.Errorf("walk counted %d killmails, folded %d", walk.Killmails, len(folded))
	}

	// ~240 hours of window at one loss an hour is a little over one page, so the
	// walk should stop after two, not run to the page cap.
	if walk.Pages > 4 {
		t.Errorf("walked %d pages for a 10-day window; the crossing was not detected", walk.Pages)
	}
	for _, m := range walk.Months {
		if m.Truncated {
			t.Errorf("month %d-%02d reported truncated, but the walk reached the window edge: %q",
				m.Year, m.Month, m.Reason)
		}
	}
}

// TestWalkFactionLossesSince_ShortPageEndsTheMonth: a month with less than a full
// page of data left is finished, not truncated, and the walk moves to the month
// before it.
func TestWalkFactionLossesSince_ShortPageEndsTheMonth(t *testing.T) {
	now := time.Now().UTC()
	since := now.AddDate(0, -2, 0)

	f := &fakeZkill{page: func(year, month, page int) []ESIKillmail {
		if page > 1 {
			return nil
		}
		// One loss per month, dated inside that month.
		when := time.Date(year, time.Month(month), 2, 12, 0, 0, 0, time.UTC)
		if when.After(now) {
			when = now.Add(-time.Hour)
		}
		return []ESIKillmail{lossIn(30045334, int64(year*100+month), when, 2679, 100)}
	}}
	c := walkTestClient(f)

	walk, err := c.WalkFactionLossesSince(context.Background(), 500001, since, func([]ESIKillmail) error { return nil })
	if err != nil {
		t.Fatalf("WalkFactionLossesSince: %v", err)
	}

	// Three calendar months are touched by a two-month window in every case: the
	// current one, and the two before it.
	if len(walk.Months) != 3 {
		t.Errorf("walked %d months, want 3 for a 2-month window: %+v", len(walk.Months), walk.Months)
	}
	for _, m := range walk.Months {
		if m.Truncated {
			t.Errorf("month %d-%02d truncated on a short page: %q", m.Year, m.Month, m.Reason)
		}
		if m.Pages != 1 {
			t.Errorf("month %d-%02d took %d pages, want 1", m.Year, m.Month, m.Pages)
		}
	}
	if walk.BudgetReached {
		t.Error("a three-page walk claimed to have exhausted a 320-page budget")
	}
}

// TestWalkFactionLossesSince_MonthPageCap: a month that never runs out of full
// pages stops at zkillboard's per-month limit and says so, and the walk carries on
// into the month before it rather than giving up.
func TestWalkFactionLossesSince_MonthPageCap(t *testing.T) {
	now := time.Now().UTC()
	// Two months, so the newest month can be dense enough to hit its cap while
	// the walk still has budget left to continue.
	since := now.AddDate(0, -1, 0)

	f := &fakeZkill{page: func(year, month, page int) []ESIKillmail {
		// Dense: every page full, minutes apart, so 100 pages covers days rather
		// than the whole month and the cap is what stops it.
		start := time.Date(year, time.Month(month), 28, 0, 0, 0, 0, time.UTC)
		if start.After(now) {
			start = now.Add(-time.Minute)
		}
		return fullPage(30045334, int64(year*1_000_000+month*1000+page), start.Add(-time.Duration(page-1)*zkillPageSize*time.Minute), time.Minute)
	}}
	c := walkTestClient(f)

	walk, err := c.WalkFactionLossesSince(context.Background(), 500001, since, func([]ESIKillmail) error { return nil })
	if err != nil {
		t.Fatalf("WalkFactionLossesSince: %v", err)
	}

	first := walk.Months[0]
	if first.Pages != zkillMaxPages {
		t.Errorf("the newest month took %d pages, want the %d-page cap", first.Pages, zkillMaxPages)
	}
	if !first.Truncated {
		t.Fatal("a month stopped by the page cap did not report Truncated")
	}
	if !strings.Contains(first.Reason, "100-page") {
		t.Errorf("Reason = %q, want it to name zkillboard's per-month limit", first.Reason)
	}
	if len(walk.Months) < 2 {
		t.Error("the walk gave up after one truncated month instead of continuing back")
	}
}

// TestWalkFactionLossesSince_WholeWalkBudget: the per-month cap is not a bound on
// the walk, so there is a second one. Exceeding it stops the walk and is reported,
// because the oldest part of the window was then never fetched at all -- which is a
// different fact from a month coming back short.
func TestWalkFactionLossesSince_WholeWalkBudget(t *testing.T) {
	now := time.Now().UTC()
	since := now.AddDate(0, -6, 0) // six months: ~700 pages of dense data

	f := &fakeZkill{page: func(year, month, page int) []ESIKillmail {
		start := time.Date(year, time.Month(month), 28, 0, 0, 0, 0, time.UTC)
		if start.After(now) {
			start = now.Add(-time.Minute)
		}
		return fullPage(30045334, int64(year*1_000_000+month*1000+page), start.Add(-time.Duration(page-1)*zkillPageSize*time.Minute), time.Minute)
	}}
	c := walkTestClient(f)

	walk, err := c.WalkFactionLossesSince(context.Background(), 500001, since, func([]ESIKillmail) error { return nil })
	if err != nil {
		t.Fatalf("WalkFactionLossesSince: %v", err)
	}

	if !walk.BudgetReached {
		t.Error("a six-month dense walk did not report reaching the page budget")
	}
	if walk.Pages > fwMaxLossPages {
		t.Errorf("walked %d pages, over the %d-page budget", walk.Pages, fwMaxLossPages)
	}
	if int64(walk.Pages) != f.requests.Load() {
		t.Errorf("walk counted %d pages but made %d requests", walk.Pages, f.requests.Load())
	}
}

// TestWalkFactionLossesSince_HoldsOnePageAtATime is the memory claim, asserted
// rather than argued: the whole reason the signature is a fold is that a 90-day
// walk is ~104 MB of killmails if it is held and ~0.3 MB if it is not.
func TestWalkFactionLossesSince_HoldsOnePageAtATime(t *testing.T) {
	now := time.Now().UTC()
	since := now.AddDate(0, -1, 0)

	f := &fakeZkill{page: func(year, month, page int) []ESIKillmail {
		if page > 20 {
			return nil
		}
		start := time.Date(year, time.Month(month), 28, 0, 0, 0, 0, time.UTC)
		if start.After(now) {
			start = now.Add(-time.Minute)
		}
		return fullPage(30045334, int64(year*1_000_000+month*1000+page), start.Add(-time.Duration(page-1)*zkillPageSize*time.Minute), time.Minute)
	}}
	c := walkTestClient(f)

	folded := 0
	if _, err := c.WalkFactionLossesSince(context.Background(), 500001, since, func(page []ESIKillmail) error {
		folded += len(page)
		return nil
	}); err != nil {
		t.Fatalf("WalkFactionLossesSince: %v", err)
	}

	if folded == 0 {
		t.Fatal("nothing was folded, so nothing is being asserted")
	}
	if got := f.maxLive.Load(); got != 1 {
		t.Errorf("%d pages were in flight at once, want 1 (the walk is accumulating)", got)
	}
	if f.livePages.Load() != 0 {
		t.Errorf("%d pages still held after the walk", f.livePages.Load())
	}
}

// TestWalkFactionLossesSince_PageFailureIsPartialData follows GetFactionLosses'
// existing rule -- a page failing at 20 of 25 still describes the warzone -- with
// the addition the long path needs: the shortfall has to be recorded, because the
// rate is scaled by what was covered.
func TestWalkFactionLossesSince_PageFailureIsPartialData(t *testing.T) {
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -20)

	f := &fakeZkill{
		page: func(year, month, page int) []ESIKillmail {
			start := time.Date(year, time.Month(month), 28, 0, 0, 0, 0, time.UTC)
			if start.After(now) {
				start = now.Add(-time.Minute)
			}
			return fullPage(30045334, int64(year*1_000_000+month*1000+page), start.Add(-time.Duration(page-1)*zkillPageSize*time.Minute), time.Minute)
		},
		fail: func(year, month, page int) bool { return page == 3 },
	}
	c := walkTestClient(f)

	folded := 0
	walk, err := c.WalkFactionLossesSince(context.Background(), 500001, since, func(page []ESIKillmail) error {
		folded += len(page)
		return nil
	})
	if err != nil {
		t.Fatalf("a failed page must not fail the walk: %v", err)
	}
	if folded != 2*zkillPageSize {
		t.Errorf("folded %d killmails, want the %d from the two pages that worked", folded, 2*zkillPageSize)
	}

	first := walk.Months[0]
	if !first.Truncated || !strings.Contains(first.Reason, "failed") {
		t.Errorf("month coverage = %+v, want Truncated with a Reason naming the failure", first)
	}
}

func TestWalkFactionLossesSince_RefusesNonsense(t *testing.T) {
	c := walkTestClient(&fakeZkill{page: func(int, int, int) []ESIKillmail { return nil }})

	if _, err := c.WalkFactionLossesSince(context.Background(), 500001, time.Now().Add(-time.Hour), nil); err == nil {
		t.Error("a nil fold was accepted, which would walk 300 pages and discard them")
	}
	if _, err := c.WalkFactionLossesSince(context.Background(), 500001, time.Now().Add(time.Hour), func([]ESIKillmail) error { return nil }); err == nil {
		t.Error("a since in the future was accepted")
	}
}

// TestTrimToSince_KeepsUnparseableTimes: an unreadable timestamp is a reason to
// include a real loss, not to discard it.
func TestTrimToSince_KeepsUnparseableTimes(t *testing.T) {
	since := time.Now().UTC().Add(-24 * time.Hour)
	kills := []ESIKillmail{
		lossIn(30045334, 1, since.Add(time.Hour), 2679, 100),
		{KillmailID: 2, KillmailTime: "not a time", SolarSystemID: 30045334},
		lossIn(30045334, 3, since.Add(-time.Hour), 2679, 100),
	}

	kept, oldest, newest, crossed := trimToSince(kills, since)
	if len(kept) != 2 {
		t.Fatalf("kept %d, want 2 (the in-window loss and the unreadable one)", len(kept))
	}
	if !crossed {
		t.Error("crossed = false, but a killmail older than since was present")
	}
	if oldest.IsZero() || newest.IsZero() {
		t.Error("the span was not measured")
	}
	// Trimming must not write through to the caller's slice: the walk reuses
	// nothing, but a page whose head was overwritten would fold garbage.
	if kills[1].KillmailID != 2 {
		t.Errorf("the input page was rewritten: %+v", kills[:3])
	}
}
