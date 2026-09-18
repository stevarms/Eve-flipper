package zkillboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"eve-flipper/internal/logger"
)

const baseURL = "https://zkillboard.com/api"

// Client is a rate-limited Zkillboard API client.
// Zkillboard has strict rate limits: 10 requests per second max.
type Client struct {
	http    *http.Client
	sem     chan struct{}
	mu      sync.Mutex
	lastReq time.Time

	// minInterval is the spacing between requests. It is a field rather than a
	// constant only so a test walking three hundred pages against a fake
	// transport does not spend a minute asleep; nothing in production sets it.
	minInterval time.Duration
}

// NewClient creates a Zkillboard client with rate limiting.
func NewClient() *Client {
	return &Client{
		http:        &http.Client{Timeout: 60 * time.Second}, // Zkillboard can be slow
		sem:         make(chan struct{}, 5),                  // Max 5 concurrent requests
		minInterval: 200 * time.Millisecond,
	}
}

// RegionStats contains kill statistics for a region.
type RegionStats struct {
	ID             int32                  `json:"id"`
	Type           string                 `json:"type"`
	ShipsDestroyed int64                  `json:"shipsDestroyed"`
	ISKDestroyed   float64                `json:"iskDestroyed"`
	Months         map[string]*MonthStats `json:"months"`
	ActivePVP      *ActivePVP             `json:"activepvp"`
	TopLists       []TopList              `json:"topLists"`
	Groups         map[string]*GroupStats `json:"groups"`
	Info           *RegionInfo            `json:"info"`
}

// MonthStats contains monthly kill statistics.
type MonthStats struct {
	Year           int     `json:"year"`
	Month          int     `json:"month"`
	ShipsDestroyed int64   `json:"shipsDestroyed"`
	ISKDestroyed   float64 `json:"iskDestroyed"`
}

// ActivePVP contains active PVP statistics.
type ActivePVP struct {
	Characters   *CountStat `json:"characters"`
	Corporations *CountStat `json:"corporations"`
	Alliances    *CountStat `json:"alliances"`
	Ships        *CountStat `json:"ships"`
	Systems      *CountStat `json:"systems"`
	Kills        *CountStat `json:"kills"`
}

// CountStat is a simple count statistic.
type CountStat struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

// TopList contains top items of a specific type.
type TopList struct {
	Type   string     `json:"type"`
	Title  string     `json:"title"`
	Values []TopValue `json:"values"`
}

// TopValue is a single entry in a top list.
type TopValue struct {
	Kills      int    `json:"kills"`
	ID         int32  `json:"id"`
	Name       string `json:"name"`
	ShipTypeID int32  `json:"shipTypeID,omitempty"`
	ShipName   string `json:"shipName,omitempty"`
}

// GroupStats contains statistics for a ship group.
type GroupStats struct {
	GroupID        int32   `json:"groupID"`
	ShipsDestroyed int64   `json:"shipsDestroyed"`
	ISKDestroyed   float64 `json:"iskDestroyed"`
}

// RegionInfo contains basic region info.
type RegionInfo struct {
	ID       int32  `json:"id"`
	Name     string `json:"name"`
	RegionID int32  `json:"region_id"`
}

// Killmail represents a single killmail from Zkillboard.
type Killmail struct {
	KillmailID   int64    `json:"killmail_id"`
	KillmailHash string   `json:"zkb.hash"`
	ZKB          *ZKBInfo `json:"zkb"`
}

// ZKBInfo contains Zkillboard-specific killmail info.
type ZKBInfo struct {
	LocationID     int64   `json:"locationID"`
	Hash           string  `json:"hash"`
	FittedValue    float64 `json:"fittedValue"`
	DroppedValue   float64 `json:"droppedValue"`
	DestroyedValue float64 `json:"destroyedValue"`
	TotalValue     float64 `json:"totalValue"`
	Points         int     `json:"points"`
	NPC            bool    `json:"npc"`
	Solo           bool    `json:"solo"`
	Awox           bool    `json:"awox"`
}

// GetRegionStats fetches kill statistics for a region.
func (c *Client) GetRegionStats(regionID int32) (*RegionStats, error) {
	url := fmt.Sprintf("%s/stats/regionID/%d/", baseURL, regionID)

	var stats RegionStats
	if err := c.getJSON(url, &stats); err != nil {
		return nil, fmt.Errorf("get region stats %d: %w", regionID, err)
	}

	return &stats, nil
}

// GetRecentKills fetches recent killmails for a region.
func (c *Client) GetRecentKills(regionID int32, pastSeconds int) ([]map[string]interface{}, error) {
	url := fmt.Sprintf("%s/regionID/%d/pastSeconds/%d/", baseURL, regionID, pastSeconds)

	var kills []map[string]interface{}
	if err := c.getJSON(url, &kills); err != nil {
		return nil, fmt.Errorf("get recent kills for region %d: %w", regionID, err)
	}

	return kills, nil
}

// zkillMaxPastSeconds is zkillboard's ceiling on the pastSeconds modifier, and
// zkillPageSize is how many killmails a page holds. Both are API facts.
const (
	zkillMaxPastSeconds = 604800 // 7 days
	zkillPageSize       = 200
	zkillMaxPages       = 100
)

// normalizePastSeconds clamps to zkillboard's 7-day ceiling and rounds up to a
// whole hour: the API rejects anything else, and rounding down would silently
// shorten the window a caller asked for.
func normalizePastSeconds(pastSeconds int) int {
	if pastSeconds <= 0 {
		return 3600
	}
	if pastSeconds%3600 != 0 {
		pastSeconds += 3600 - pastSeconds%3600
	}
	if pastSeconds > zkillMaxPastSeconds {
		return zkillMaxPastSeconds
	}
	return pastSeconds
}

// GetRecentKillmails fetches a region's recent killmails as whole killmails.
//
// zkillboard's list responses embed the full killmail -- victim, items and all
// -- so the items needed for demand analysis are already here and there is no
// reason to re-fetch each one from ESI by id and hash.
func (c *Client) GetRecentKillmails(regionID int32, pastSeconds int) ([]ESIKillmail, error) {
	url := fmt.Sprintf("%s/regionID/%d/pastSeconds/%d/", baseURL, regionID, normalizePastSeconds(pastSeconds))

	var kills []ESIKillmail
	if err := c.getJSON(url, &kills); err != nil {
		return nil, fmt.Errorf("get recent killmails for region %d: %w", regionID, err)
	}

	return kills, nil
}

// GetFactionLossesPage fetches one page of losses suffered by a militia's
// members. page is 1-based.
//
// This is membership, not location: only about a third of a militia's losses
// happen inside the warzone, so callers wanting warzone demand must filter on
// SolarSystemID themselves. See AnalyzeMilitiaDemand.
func (c *Client) GetFactionLossesPage(factionID int32, pastSeconds, page int) ([]ESIKillmail, error) {
	if page < 1 {
		page = 1
	}
	url := fmt.Sprintf("%s/losses/factionID/%d/pastSeconds/%d/page/%d/",
		baseURL, factionID, normalizePastSeconds(pastSeconds), page)

	var kills []ESIKillmail
	if err := c.getJSON(url, &kills); err != nil {
		return nil, fmt.Errorf("get faction %d losses page %d: %w", factionID, page, err)
	}

	return kills, nil
}

// GetFactionLosses pages through a militia's losses until a short page ends the
// run. A busy militia loses ~700 ships a day, so a 7-day window is ~25 pages;
// maxPages bounds that, and zkillboard refuses beyond 100 regardless.
//
// ctx is checked between pages. The underlying fetch is not cancellable, so the
// worst case is one outstanding request after cancellation, not an unbounded run.
func (c *Client) GetFactionLosses(ctx context.Context, factionID int32, pastSeconds, maxPages int) ([]ESIKillmail, error) {
	if maxPages <= 0 || maxPages > zkillMaxPages {
		maxPages = zkillMaxPages
	}

	var all []ESIKillmail
	for page := 1; page <= maxPages; page++ {
		if err := ctx.Err(); err != nil {
			return all, err
		}

		kills, err := c.GetFactionLossesPage(factionID, pastSeconds, page)
		if err != nil {
			// Partial data beats none: a page failing at 20 of 25 still
			// describes the warzone. Report it and stop, do not discard.
			if len(all) > 0 {
				logger.Warn("Demand", fmt.Sprintf("faction %d losses: stopping at page %d of %d (%d killmails so far): %v",
					factionID, page, maxPages, len(all), err))
				return all, nil
			}
			return nil, err
		}

		all = append(all, kills...)
		if len(kills) < zkillPageSize {
			break // short page: end of the window
		}
	}

	return all, nil
}

// fwMaxLossPages bounds a whole long walk, on top of the 100-page cap each
// month path carries.
//
// A quarter of a busy militia is ~300 pages, so this is roughly one full 90-day
// walk and nothing more: a mis-set window or an unusually violent quarter cannot
// turn into an unbounded run against a rate-limited API. Same reasoning as
// fwMaxDepthRegions in the plan layer -- exceeding it is a named warning, not a
// silent stop, which is why the walk reports it rather than logging it.
const fwMaxLossPages = 320

// MonthCoverage is what one calendar month of a walk actually covered.
//
// It exists because "how far back did we get" cannot be read off the killmails
// alone once months are paged separately. A full month of a busy militia is ~119
// pages against zkillboard's 100-page cap, so a month in the middle of a 90-day
// window loses its oldest few days -- a hole inside the span, not a shorter
// span. Scaling by the oldest killmail seen would count those missing days as
// days when nothing died and under-report the rate, which reads on screen as a
// covered item. So coverage is summed per month instead, and this is the record
// that makes that possible.
type MonthCoverage struct {
	Year       int       `json:"year"`
	Month      int       `json:"month"`
	Pages      int       `json:"pages"`
	Killmails  int       `json:"killmails"`
	NewestSeen time.Time `json:"newest_seen"`
	OldestSeen time.Time `json:"oldest_seen"`

	// Truncated says this month's sample stops at OldestSeen rather than at the
	// month's own beginning, so its coverage is OldestSeen forward and not the
	// whole month. Reason names which limit did it, because a page cap and a
	// failed request deserve different words in a warning.
	Truncated bool   `json:"truncated"`
	Reason    string `json:"reason,omitempty"`
}

// LossWalk is what a walk covered, in the aggregate and per month.
//
// BudgetReached is separate from any month's Truncated because it is a different
// fact: the walk stopped before reaching the requested window at all, so months
// older than the last one listed were never asked for. A caller turning this into
// a warning needs to say that, not "September was short".
type LossWalk struct {
	Months        []MonthCoverage `json:"months"`
	Pages         int             `json:"pages"`
	Killmails     int             `json:"killmails"`
	BudgetReached bool            `json:"budget_reached"`
}

// GetFactionLossesMonthPage fetches one page of a militia's losses from one
// calendar month. page is 1-based.
//
// The month path is the only way past pastSeconds' 7-day ceiling: startTime and
// endTime are deprecated, and a year without a month is ignored. Each month path
// has its own 100-page budget, which at ~770 losses a day is about 4-5 days short
// of a full month -- see MonthCoverage.
//
// Like GetFactionLossesPage this is membership, not location.
func (c *Client) GetFactionLossesMonthPage(factionID int32, year, month, page int) ([]ESIKillmail, error) {
	if page < 1 {
		page = 1
	}
	url := fmt.Sprintf("%s/losses/factionID/%d/year/%d/month/%d/page/%d/",
		baseURL, factionID, year, month, page)

	var kills []ESIKillmail
	if err := c.getJSON(url, &kills); err != nil {
		return nil, fmt.Errorf("get faction %d losses %d-%02d page %d: %w", factionID, year, month, page, err)
	}

	return kills, nil
}

// WalkFactionLossesSince walks a militia's losses from now back to since,
// newest month first, handing each page to fold and then dropping it.
//
// fold rather than a returned slice, and that is the whole point of the
// signature: an ESIKillmail carries ~30 items at 48 bytes each, so a 90-day
// Caldari walk is ~69,000 losses at ~1.5 KB -- about 104 MB live, and Go will let
// the heap run to roughly twice that. Folding page by page holds 200 killmails at
// a time, about 0.3 MB. Most of what would be held is discarded by the caller's
// scope filter anyway: only about a third of a militia's losses happen inside its
// warzone.
//
// The walk is deliberately serial. The client's 5-way concurrency exists to make
// 25 pages quick; at 300 pages it would multiply peak memory by five to save time
// on a result that is then cached for days.
//
// Pages are trimmed to since before folding, so the caller never sees a killmail
// from outside the window it asked for -- the last page of the oldest month
// otherwise reaches past it and would inflate a rate that is scaled to the window.
//
// A page that fails ends that month at what was already seen, marked Truncated,
// rather than failing the walk: partial data beats none, and the coverage record
// is what keeps it honest.
func (c *Client) WalkFactionLossesSince(ctx context.Context, factionID int32, since time.Time,
	fold func(page []ESIKillmail) error) (*LossWalk, error) {
	if fold == nil {
		return nil, fmt.Errorf("walk faction losses: fold callback required")
	}

	now := time.Now().UTC()
	since = since.UTC()
	if !since.Before(now) {
		return nil, fmt.Errorf("walk faction losses: since %s is not in the past", since.Format(time.RFC3339))
	}

	walk := &LossWalk{}
	oldestMonth := time.Date(since.Year(), since.Month(), 1, 0, 0, 0, 0, time.UTC)

	for cursor := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC); !cursor.Before(oldestMonth); cursor = cursor.AddDate(0, -1, 0) {
		if err := ctx.Err(); err != nil {
			return walk, err
		}

		remaining := fwMaxLossPages - walk.Pages
		if remaining <= 0 {
			// Months older than this were never asked for, which is a different
			// thing from a month coming back short.
			walk.BudgetReached = true
			logger.Warn("Demand", fmt.Sprintf("faction %d losses: %d-page walk budget reached before %d-%02d",
				factionID, fwMaxLossPages, cursor.Year(), int(cursor.Month())))
			break
		}

		cov, reachedSince := c.walkMonthLosses(ctx, factionID, cursor.Year(), int(cursor.Month()), since, remaining, fold)
		if cov.Pages > 0 || cov.Truncated {
			walk.Months = append(walk.Months, cov)
		}
		walk.Pages += cov.Pages
		walk.Killmails += cov.Killmails

		if reachedSince {
			break
		}
	}

	return walk, nil
}

// walkMonthLosses pages one calendar month, newest first, until the month runs
// out, a killmail older than since appears, or maxPages is spent. It reports what
// it covered and whether the walk as a whole can stop.
func (c *Client) walkMonthLosses(ctx context.Context, factionID int32, year, month int, since time.Time,
	maxPages int, fold func(page []ESIKillmail) error) (MonthCoverage, bool) {
	cov := MonthCoverage{Year: year, Month: month}

	if maxPages > zkillMaxPages {
		maxPages = zkillMaxPages
	}

	for page := 1; page <= maxPages; page++ {
		if err := ctx.Err(); err != nil {
			cov.Truncated = true
			cov.Reason = "cancelled"
			return cov, true
		}

		kills, err := c.GetFactionLossesMonthPage(factionID, year, month, page)
		if err != nil {
			// Partial data beats none, exactly as GetFactionLosses decides it --
			// but here the shortfall has to be recorded, because the rate is
			// scaled by what was covered.
			logger.Warn("Demand", fmt.Sprintf("faction %d losses %d-%02d: stopping at page %d: %v",
				factionID, year, month, page, err))
			cov.Truncated = true
			cov.Reason = "a page failed to fetch"
			return cov, false
		}

		full := len(kills) >= zkillPageSize
		kept, oldest, newest, crossed := trimToSince(kills, since)

		cov.Pages++
		cov.Killmails += len(kept)
		if !newest.IsZero() && (cov.NewestSeen.IsZero() || newest.After(cov.NewestSeen)) {
			cov.NewestSeen = newest
		}
		if !oldest.IsZero() && (cov.OldestSeen.IsZero() || oldest.Before(cov.OldestSeen)) {
			cov.OldestSeen = oldest
		}

		if len(kept) > 0 {
			if err := fold(kept); err != nil {
				cov.Truncated = true
				cov.Reason = "the fold stopped the walk"
				return cov, true
			}
		}

		switch {
		case crossed:
			// The window's far edge is inside this page, so this month is fully
			// covered back to it and nothing older is wanted.
			return cov, true
		case !full:
			// A short page is the end of the month's data, not a limit.
			return cov, false
		case page == maxPages:
			cov.Truncated = true
			if maxPages < zkillMaxPages {
				cov.Reason = fmt.Sprintf("the walk's %d-page budget ran out", fwMaxLossPages)
			} else {
				cov.Reason = fmt.Sprintf("zkillboard's %d-page-per-month limit", zkillMaxPages)
			}
			return cov, false
		}
	}

	return cov, false
}

// trimToSince drops killmails older than since and reports the page's time span.
//
// crossed says a killmail older than since was present, which is how the walk
// knows it has reached the far edge of the window rather than merely the end of a
// page. Killmails whose time will not parse are kept: an unreadable timestamp is
// a reason to include a real loss, not to discard it, and it simply does not move
// the span.
func trimToSince(kills []ESIKillmail, since time.Time) (kept []ESIKillmail, oldest, newest time.Time, crossed bool) {
	kept = kills[:0:0]
	for i := range kills {
		t, err := time.Parse(time.RFC3339, kills[i].KillmailTime)
		if err != nil {
			kept = append(kept, kills[i])
			continue
		}
		if t.Before(since) {
			crossed = true
			continue
		}
		kept = append(kept, kills[i])
		if oldest.IsZero() || t.Before(oldest) {
			oldest = t
		}
		if newest.IsZero() || t.After(newest) {
			newest = t
		}
	}
	return kept, oldest, newest, crossed
}

const maxRetries = 3

// getJSON fetches a URL and decodes JSON with rate limiting.
func (c *Client) getJSON(url string, dst interface{}) error {
	return c.getJSONWithRetry(url, dst, 0)
}

func (c *Client) getJSONWithRetry(url string, dst interface{}, attempt int) error {
	c.sem <- struct{}{}
	defer func() { <-c.sem }()

	// FIX #7: compute sleep duration under mutex, but sleep outside it
	// so other goroutines aren't blocked during the wait.
	c.mu.Lock()
	elapsed := time.Since(c.lastReq)
	var sleepDur time.Duration
	if elapsed < c.minInterval {
		sleepDur = c.minInterval - elapsed
	}
	c.lastReq = time.Now().Add(sleepDur)
	c.mu.Unlock()

	if sleepDur > 0 {
		time.Sleep(sleepDur)
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "eve-flipper/1.0 (https://github.com/user/eve-flipper)")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		// FIX #3: Rate limited - retry with limit to prevent infinite recursion.
		if attempt >= maxRetries {
			return fmt.Errorf("zkillboard rate limited after %d retries: %s", maxRetries, url)
		}
		wait := time.Duration(5*(attempt+1)) * time.Second
		logger.Warn("Zkillboard", fmt.Sprintf("Rate limited, waiting %v (attempt %d/%d)...", wait, attempt+1, maxRetries))
		time.Sleep(wait)
		return c.getJSONWithRetry(url, dst, attempt+1)
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("zkillboard %d: %s", resp.StatusCode, string(body))
	}

	return json.NewDecoder(resp.Body).Decode(dst)
}

// GetSystemKills fetches recent killmails for a specific system.
func (c *Client) GetSystemKills(systemID int32, pastSeconds int) ([]map[string]interface{}, error) {
	url := fmt.Sprintf("%s/kills/systemID/%d/pastSeconds/%d/", baseURL, systemID, pastSeconds)
	var kills []map[string]interface{}
	if err := c.getJSON(url, &kills); err != nil {
		return nil, fmt.Errorf("get system kills for system %d: %w", systemID, err)
	}
	return kills, nil
}

// HealthCheck pings Zkillboard to verify connectivity.
func (c *Client) HealthCheck() bool {
	url := baseURL + "/stats/regionID/10000002/" // The Forge - always has data

	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", "eve-flipper/1.0")

	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()

	return resp.StatusCode == 200
}
