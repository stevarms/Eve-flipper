package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"eve-flipper/internal/esi"
	"eve-flipper/internal/fuzzwork"
)

// fuzzwork_import.go — seed the orderbook backtester with real historical depth.
//
// The backtester replays stored order books. ESI cannot backfill them — it only
// ever shows the book as it is now — so until this existed the replay could only
// reach back to whenever recording was switched on, and a strategy could not be
// tested against a market the user had not already sat through.
//
// market.fuzzwork.co.uk has archived the whole game's book roughly every half
// hour since mid-2023. That is precisely the missing input, and it is the one
// question that archive answers well: a backtest wants to know what was *on
// offer*, which is what an order book snapshot is. (It is the wrong source for
// price history, which needs executed trades — see the package comment on
// internal/fuzzwork.)
//
// Everything after parsing is the existing recorder. db.RecordMarketOrderSnapshot
// already does interest filtering, level aggregation, content-hash dedupe and
// the counts, so imported snapshots are shaped identically to recorded ones and
// the replay query finds them without knowing they came from somewhere else.
// Source is "fuzzwork" so they are still distinguishable, and so the recorder's
// 30-minute cooldown cannot make a live capture suppress an import or vice versa.

const (
	// Defaults chosen from what the backtester actually does with these.
	//
	// It pairs a buy-side book with a sell-side book captured within minutes of
	// each other, and every archived snapshot contains both sides at one
	// instant — so cadence does not degrade any individual simulated trade, it
	// only changes how many independent moments there are to observe.
	//
	// Recent history is where fill simulation and strategy validation happen and
	// where books move fastest, so it gets one per day. Further back the
	// question is seasonal shape rather than intraday fills, so weekly is
	// enough: 52 observations describing a slow-moving quantity. The cost of
	// weekly is that a short-lived event can fall between samples entirely.
	fuzzworkDefaultDailyDays  = 90
	fuzzworkDefaultWeeklyDays = 365

	// A hard ceiling regardless of the window asked for. Each file is ~27 MB
	// from a service run for free; this is the difference between a courtesy
	// and an abuse.
	fuzzworkMaxFiles = 200

	// Used to calibrate ordersets-per-day from two real timestamps rather than
	// assuming the ~31-minute cadence holds forever.
	fuzzworkCalibrationSpan = 2000
)

type fuzzworkImportRequest struct {
	RegionID   int32 `json:"region_id"`
	DailyDays  int   `json:"daily_days"`
	WeeklyDays int   `json:"weekly_days"`
	MaxFiles   int   `json:"max_files"`
	// DryRun resolves the file list and reports what would be fetched without
	// downloading anything. The plan is worth seeing before committing a few
	// gigabytes of transfer.
	DryRun bool `json:"dry_run"`
}

type fuzzworkImportResult struct {
	RegionID      int32  `json:"region_id"`
	InterestTypes int    `json:"interest_types"`
	Planned       int    `json:"planned"`
	Fetched       int    `json:"fetched"`
	Stored        int    `json:"stored"`
	Skipped       int    `json:"skipped"`
	Failed        int    `json:"failed"`
	OrdersKept    int64  `json:"orders_kept"`
	OldestCapture string `json:"oldest_capture,omitempty"`
	NewestCapture string `json:"newest_capture,omitempty"`
	// EstimatedBytes is the transfer a non-dry run would cost, so the number is
	// visible before it is spent.
	EstimatedBytes int64    `json:"estimated_bytes"`
	Warnings       []string `json:"warnings,omitempty"`
}

func (s *Server) handleFuzzworkImport(w http.ResponseWriter, r *http.Request) {
	var req fuzzworkImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.RegionID <= 0 {
		req.RegionID = holdingRuleRegionID
	}
	if req.DailyDays <= 0 {
		req.DailyDays = fuzzworkDefaultDailyDays
	}
	if req.WeeklyDays <= 0 {
		req.WeeklyDays = fuzzworkDefaultWeeklyDays
	}
	if req.MaxFiles <= 0 || req.MaxFiles > fuzzworkMaxFiles {
		req.MaxFiles = fuzzworkMaxFiles
	}
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "no database")
		return
	}

	// The archive is filtered to types the user deals in, and an empty set
	// means we cannot yet tell what those are. Importing a whole region on that
	// basis is exactly what the recorder's own guard exists to prevent, so fail
	// loudly rather than quietly storing nothing.
	interest := s.db.OrderBookInterestTypes()
	if len(interest) == 0 {
		writeError(w, http.StatusBadRequest,
			"nothing to seed yet: the order book archive only keeps types you trade, hold, watch or build with, and none are known yet")
		return
	}

	em, ok := beginNdjson(w, r)
	if !ok {
		return
	}

	result, err := s.runFuzzworkImport(r, req, interest, em.Progress)
	if err != nil {
		em.Error(err.Error())
		return
	}
	em.Result(result)
}

func (s *Server) runFuzzworkImport(
	r *http.Request,
	req fuzzworkImportRequest,
	interest map[int32]bool,
	progress func(string),
) (fuzzworkImportResult, error) {
	ctx := r.Context()
	client := fuzzwork.New()
	out := fuzzworkImportResult{RegionID: req.RegionID, InterestTypes: len(interest)}

	progress("Asking the archive where it is up to")
	current, err := client.CurrentOrderset(ctx)
	if err != nil {
		return out, fmt.Errorf("could not read the archive index: %w", err)
	}

	perDay, err := s.fuzzworkCalibrate(ctx, client, current)
	if err != nil {
		return out, err
	}
	progress(fmt.Sprintf("Archive is at snapshot %d, about %.0f per day", current, perDay))

	targets := fuzzwork.Plan(current, perDay, req.DailyDays, req.WeeklyDays, req.MaxFiles)
	out.Planned = len(targets)
	// ~27 MB each, stated up front because a plan of 129 files is 3.5 GB of
	// somebody else's bandwidth.
	out.EstimatedBytes = int64(len(targets)) * 27 << 20

	if req.DryRun {
		progress(fmt.Sprintf("Dry run: %d snapshots would be fetched (~%.1f GB)",
			len(targets), float64(out.EstimatedBytes)/(1<<30)))
		return out, nil
	}

	keep := func(typeID, regionID int32) bool {
		return regionID == req.RegionID && interest[typeID]
	}

	// Oldest first, and this is load-bearing rather than tidiness.
	//
	// RecordMarketOrderSnapshot suppresses a write when one already exists
	// within 30 minutes of the incoming captured_at -- a cooldown that assumes
	// captured_at only ever moves forward, which is true of live recording and
	// false of a backfill. Walking newest-first, every older snapshot saw the
	// newer one already written as "recent" and was silently swallowed: three
	// files fetched, one row stored, and the recorder returns nil either way so
	// nothing complained. Going forwards in time makes each snapshot look at
	// genuinely older neighbours, which are a day or a week away.
	for l, r := 0, len(targets)-1; l < r; l, r = l+1, r-1 {
		targets[l], targets[r] = targets[r], targets[l]
	}

	// Counted from the table, not from calls that returned no error, for the
	// same reason.
	storedBefore := s.db.CountOrderBookSnapshotsBySource("fuzzwork")

	for i, orderset := range targets {
		if ctx.Err() != nil {
			out.Warnings = append(out.Warnings, "stopped early: request cancelled")
			return out, nil
		}
		if i > 0 {
			// Courtesy, not a rate limit imposed on us. See internal/fuzzwork.
			select {
			case <-ctx.Done():
				return out, nil
			case <-time.After(fuzzwork.PoliteDelay()):
			}
		}

		progress(fmt.Sprintf("Snapshot %d of %d", i+1, len(targets)))
		_, orders, capturedAt, err := client.FetchNear(ctx, orderset, fuzzwork.NearTolerance, keep)
		if err != nil {
			if errors.Is(err, fuzzwork.ErrNotArchived) {
				// Running oldest-first, an empty slot before the first hit simply
				// means the archive does not reach that far back -- keep walking
				// forward. After the first hit it is one of the archive's scattered
				// holes (171601 is absent between two present neighbours), and
				// FetchNear has already searched either side of it. Neither is a
				// reason to abandon the newer snapshots that certainly exist.
				out.Skipped++
				continue
			}
			out.Failed++
			out.Warnings = append(out.Warnings, err.Error())
			continue
		}
		out.Fetched++
		out.OrdersKept += int64(len(orders))

		if len(orders) == 0 {
			// Nothing of yours traded in that region at that moment. Not a
			// failure, and not worth a row.
			out.Skipped++
			continue
		}

		if err := s.db.RecordMarketOrderSnapshot(esi.MarketOrderSnapshot{
			RegionID: req.RegionID,
			// "all" because the file carries both sides at one instant, which
			// is what makes the backtester's buy/sell pairing exact regardless
			// of how far apart the snapshots are.
			OrderType:  "all",
			Source:     "fuzzwork",
			CapturedAt: capturedAt,
			Orders:     orders,
		}); err != nil {
			out.Failed++
			out.Warnings = append(out.Warnings, fmt.Sprintf("storing snapshot %d: %v", orderset, err))
			continue
		}

		stamp := capturedAt.UTC().Format(time.RFC3339)
		if out.OldestCapture == "" || stamp < out.OldestCapture {
			out.OldestCapture = stamp
		}
		if stamp > out.NewestCapture {
			out.NewestCapture = stamp
		}
	}

	// The authoritative figure. out.Stored counted successful calls, and the
	// recorder succeeds when it decides not to write.
	out.Stored = s.db.CountOrderBookSnapshotsBySource("fuzzwork") - storedBefore
	if out.Stored < 0 {
		out.Stored = 0
	}
	if out.Stored == 0 && out.Fetched > 0 {
		out.Warnings = append(out.Warnings,
			"nothing new was stored: every snapshot either held none of your types in this region, or matched one already saved")
	}
	return out, nil
}

// fuzzworkCalibrate measures ordersets per day from two real timestamps.
//
// The cadence has been about one every 31 minutes for years, but deriving the
// step from a hardcoded rate would drift silently if that ever changed — and
// the symptom would be an import that thinks it covered a year while actually
// covering four months.
func (s *Server) fuzzworkCalibrate(ctx context.Context, client *fuzzwork.Client, current int) (float64, error) {
	newestID, newest, err := client.CapturedAtNear(ctx, current, fuzzwork.NearTolerance)
	if err != nil {
		return 0, fmt.Errorf("could not date the newest snapshot: %w", err)
	}
	olderID, older, err := client.CapturedAtNear(ctx, current-fuzzworkCalibrationSpan, fuzzwork.NearTolerance)
	if err != nil {
		return 0, fmt.Errorf("could not date a snapshot near %d: %w", current-fuzzworkCalibrationSpan, err)
	}
	days := newest.Sub(older).Hours() / 24
	span := newestID - olderID
	if days <= 0 || span <= 0 {
		return 0, fmt.Errorf("archive timestamps are not ordered as expected")
	}
	// Measured against the ids that actually resolved, not the ones asked
	// for: a gap can shift either end by a few.
	return float64(span) / days, nil
}
