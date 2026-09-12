// Package fuzzwork reads Steve Ronuken's archived EVE order book snapshots.
//
// ESI does not serve historical order books — it only ever shows you the book
// as it is right now — so the orderbook backtester can only replay depth this
// app happened to record itself, starting whenever recording was switched on.
// market.fuzzwork.co.uk has been capturing the whole game's book roughly every
// half hour since mid-2023 and keeps the files, which is exactly the data the
// backtester lacks and cannot obtain any other way.
//
// What this is NOT useful for: price history. These files are *quotes* — what
// was on offer — with no record of what executed, so no traded volume and no
// daily average. ESI's /markets/{region}/history endpoint answers that in one
// call per type, which is why the percentile and mean-reversion work uses it
// instead. The distinction matters because "we have years of market data" is
// true here and still the wrong source for half the questions.
//
// Politeness: this is a volunteer-run service and the files are ~27 MB each.
// The client identifies itself, downloads one at a time, and pauses between
// files. Callers should default to a modest window rather than pulling years.
package fuzzwork

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"eve-flipper/internal/esi"
)

const (
	baseURL = "https://market.fuzzwork.co.uk"

	// Identifies the caller, as the site asks. A bulk reader that cannot be
	// contacted is the kind that gets blocked.
	userAgent = "eve-flipper/1.0 (orderbook backtest seeding; github.com/fuzzysteve/FuzzMarket consumer)"

	// Between downloads. Not a rate limit imposed on us — a courtesy, because
	// a few hundred 27 MB requests from one client is a real cost to a service
	// run for free.
	politeDelay = 750 * time.Millisecond

	// A single file is ~27 MB gzipped and expands to several hundred MB of
	// text, so it is streamed, never buffered. This bounds a stuck transfer.
	downloadTimeout = 4 * time.Minute
)

// Client reads the archive. The zero value is not usable; use New.
type Client struct {
	http *http.Client
}

func New() *Client {
	return &Client{http: &http.Client{Timeout: downloadTimeout}}
}

// CurrentOrderset returns the newest orderset id.
func (c *Client) CurrentOrderset(ctx context.Context) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/orderset", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("orderset id: status %d", resp.StatusCode)
	}
	var body struct {
		Orderset int `json:"orderset"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body); err != nil {
		return 0, err
	}
	if body.Orderset <= 0 {
		return 0, fmt.Errorf("orderset id: got %d", body.Orderset)
	}
	return body.Orderset, nil
}

// CapturedAt returns when an orderset was written, without downloading it.
//
// The capture time is the file's Last-Modified, not anything inside it: the
// rows carry each order's own `issued` date, which is when a player placed it
// and can be months before the snapshot. Reading the timestamp from the wrong
// place would silently date every imported book to whenever its oldest order
// happened to be created.
func (c *Client) CapturedAt(ctx context.Context, orderset int) (time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, ordersetURL(orderset), nil)
	if err != nil {
		return time.Time{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return time.Time{}, ErrNotArchived
	}
	if resp.StatusCode != http.StatusOK {
		return time.Time{}, fmt.Errorf("orderset %d: status %d", orderset, resp.StatusCode)
	}
	return parseLastModified(resp.Header.Get("Last-Modified"))
}

// ErrNotArchived is returned for an orderset the archive no longer holds,
// which is the normal way to discover how far back it reaches.
var ErrNotArchived = fmt.Errorf("orderset is not in the archive")

// Fetch streams one orderset and returns the orders `keep` accepts.
//
// Streamed and filtered as it is read: the decompressed file is several
// hundred megabytes of text covering all of New Eden, and only the caller's
// region and type set survive. Peak memory is one bufio buffer plus whatever
// the filter admits, not the file.
func (c *Client) Fetch(
	ctx context.Context,
	orderset int,
	keep func(typeID, regionID int32) bool,
) ([]esi.MarketOrder, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ordersetURL(orderset), nil)
	if err != nil {
		return nil, time.Time{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	// The file is already gzip; asking for transfer encoding on top wastes CPU
	// on both ends.
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, time.Time{}, ErrNotArchived
	}
	if resp.StatusCode != http.StatusOK {
		return nil, time.Time{}, fmt.Errorf("orderset %d: status %d", orderset, resp.StatusCode)
	}
	capturedAt, tErr := parseLastModified(resp.Header.Get("Last-Modified"))
	if tErr != nil {
		return nil, time.Time{}, fmt.Errorf("orderset %d: %w", orderset, tErr)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("orderset %d: %w", orderset, err)
	}
	defer gz.Close()

	orders, err := ParseOrders(ctx, gz, keep)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("orderset %d: %w", orderset, err)
	}
	return orders, capturedAt, nil
}

// Column positions in the orderset TSV. There is no header row, so these are
// the contract and a silent reshuffle upstream would corrupt every import —
// which is why ParseOrders validates rather than trusting field count alone.
const (
	colOrderID = iota
	colTypeID
	colIssued
	colIsBuyOrder
	colVolumeRemain
	colVolumeTotal
	colMinVolume
	colPrice
	colLocationID
	colRange
	colDuration
	colRegionID
	colOrderset
	colCount
)

// ParseOrders reads the tab-separated order rows, keeping only what `keep`
// accepts. Exported so the column mapping can be tested against a fixture
// without touching the network.
func ParseOrders(
	ctx context.Context,
	r io.Reader,
	keep func(typeID, regionID int32) bool,
) ([]esi.MarketOrder, error) {
	sc := bufio.NewScanner(r)
	// Rows are short, but the default 64 KB ceiling turns one malformed line
	// into a truncated import rather than an error, so raise it and let a
	// genuinely long line fail loudly.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	out := make([]esi.MarketOrder, 0, 4096)
	line := 0
	for sc.Scan() {
		line++
		// Cancelling mid-file matters: these take minutes each.
		if line%10000 == 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
		}

		fields := strings.Split(sc.Text(), "\t")
		if len(fields) < colCount {
			continue
		}

		typeID, err1 := parseInt32(fields[colTypeID])
		regionID, err2 := parseInt32(fields[colRegionID])
		if err1 != nil || err2 != nil || typeID <= 0 || regionID <= 0 {
			continue
		}
		if keep != nil && !keep(typeID, regionID) {
			continue
		}

		price, err := strconv.ParseFloat(fields[colPrice], 64)
		if err != nil || price <= 0 {
			continue
		}
		volRemain, err := strconv.ParseInt(fields[colVolumeRemain], 10, 32)
		if err != nil || volRemain <= 0 {
			continue
		}
		locationID, err := strconv.ParseInt(fields[colLocationID], 10, 64)
		if err != nil {
			continue
		}
		orderID, _ := strconv.ParseInt(fields[colOrderID], 10, 64)
		volTotal, _ := strconv.ParseInt(fields[colVolumeTotal], 10, 32)
		minVolume, _ := strconv.ParseInt(fields[colMinVolume], 10, 32)

		out = append(out, esi.MarketOrder{
			OrderID:      orderID,
			TypeID:       typeID,
			LocationID:   locationID,
			Price:        price,
			VolumeRemain: int32(volRemain),
			MinVolume:    int32(minVolume),
			// Python's csv writer emits Python bools.
			IsBuyOrder: isTrue(fields[colIsBuyOrder]),
			RegionID:   regionID,
		})
		_ = volTotal // present in the file, not carried on esi.MarketOrder
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// PoliteDelay is how long a caller should wait between files.
func PoliteDelay() time.Duration { return politeDelay }

func ordersetURL(orderset int) string {
	return fmt.Sprintf("%s/orderbooks/orderset-%d.csv.gz", baseURL, orderset)
}

func parseLastModified(raw string) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, fmt.Errorf("no Last-Modified header to date the snapshot by")
	}
	t, err := http.ParseTime(raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("unreadable Last-Modified %q: %w", raw, err)
	}
	return t.UTC(), nil
}

func parseInt32(s string) (int32, error) {
	v, err := strconv.ParseInt(s, 10, 32)
	return int32(v), err
}

// isTrue accepts the Python and JSON spellings; anything else is false, which
// is the safe reading because mislabelling a sell as a buy would put it on the
// wrong side of the book.
func isTrue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "t", "1":
		return true
	default:
		return false
	}
}

// Plan lists the orderset ids to fetch, newest first: one per day
// across the daily window, then one per week out to the weekly window.
func Plan(current int, perDay float64, dailyDays, weeklyDays, maxFiles int) []int {
	if perDay <= 0 {
		return nil
	}
	step := int(perDay + 0.5)
	if step < 1 {
		step = 1
	}

	out := make([]int, 0, maxFiles)
	id := current
	for day := 0; day < dailyDays && len(out) < maxFiles && id > 0; day++ {
		out = append(out, id)
		id -= step
	}
	weekStep := step * 7
	for day := dailyDays; day < weeklyDays && len(out) < maxFiles && id > 0; day += 7 {
		out = append(out, id)
		id -= weekStep
	}
	return out
}

// NearTolerance is how far either side of a target orderset to look before
// giving up on that point in time.
//
// The archive is not contiguous: measured against the live service, 171601 is
// missing while 171600 and 171602 are both present. Nothing about a specific id
// matters to a caller -- it wants *a* snapshot near a moment, and the real
// capture time comes from the file's own header -- so a gap should cost a
// neighbour, not the slot.
const NearTolerance = 6

// nearby yields target, then target-1, target+1, target-2, ... outward.
// Older-first on each step, because when a slot is contested the older
// neighbour keeps the series evenly spaced rather than bunching toward now.
func nearby(target, tolerance int) []int {
	out := make([]int, 0, tolerance*2+1)
	out = append(out, target)
	for d := 1; d <= tolerance; d++ {
		if target-d > 0 {
			out = append(out, target-d)
		}
		out = append(out, target+d)
	}
	return out
}

// CapturedAtNear dates the first archived orderset at or near target, and
// reports which id that turned out to be.
func (c *Client) CapturedAtNear(ctx context.Context, target, tolerance int) (int, time.Time, error) {
	var lastErr error = ErrNotArchived
	for _, id := range nearby(target, tolerance) {
		at, err := c.CapturedAt(ctx, id)
		if err == nil {
			return id, at, nil
		}
		if !errors.Is(err, ErrNotArchived) {
			lastErr = err
		}
	}
	return 0, time.Time{}, lastErr
}

// FetchNear downloads the first archived orderset at or near target, returning
// the id actually used so a caller can report what a slot resolved to.
func (c *Client) FetchNear(
	ctx context.Context,
	target, tolerance int,
	keep func(typeID, regionID int32) bool,
) (int, []esi.MarketOrder, time.Time, error) {
	var lastErr error = ErrNotArchived
	for _, id := range nearby(target, tolerance) {
		if ctx.Err() != nil {
			return 0, nil, time.Time{}, ctx.Err()
		}
		orders, at, err := c.Fetch(ctx, id, keep)
		if err == nil {
			return id, orders, at, nil
		}
		if !errors.Is(err, ErrNotArchived) {
			lastErr = err
		}
	}
	return 0, nil, time.Time{}, lastErr
}
