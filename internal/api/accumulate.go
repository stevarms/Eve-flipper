package api

import (
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"eve-flipper/internal/engine"
	"eve-flipper/internal/esi"
	"eve-flipper/internal/sde"
)

// accumulate.go — the market-wide sweep for items near the bottom of their own
// year.
//
// Two phases with very different costs, and the split is the whole design:
//
//  1. One region-orders fetch gives every type currently on the market with its
//     best bid, best ask and sell-side depth. Cheap, and it is what makes a
//     "broad sweep" affordable at all.
//  2. The yearly distribution needs price history per type, one ESI call each.
//     That is the expensive half, so candidates are ranked by sell-side depth
//     and capped -- and the cap biases toward liquid items, which is the same
//     direction the liquidity gate pushes anyway.
//
// After the first run most of phase 2 is served from market_derived_cache for a
// day, so a daily sweep is fast and only the first is slow. The handler streams
// progress because that first run is minutes, not seconds.

const (
	// How many types get a history call. The Forge lists on the order of ten
	// thousand; sweeping all of them cold would be ~10k ESI round trips. This
	// covers the liquid end, which is the only end that can pass the gates.
	accumulateDefaultMaxTypes = 1500

	// Below this there is not enough on offer to be worth a history call. It is
	// a coarse pre-filter, not the real liquidity gate -- that one runs on
	// traded volume in the engine, which is the honest measure.
	accumulateMinSellDepthISK = 20_000_000

	// And this many standing orders, on either side, before an item counts as a
	// market rather than someone's parked stock. A handful of orders is how a
	// 20B officer module looks, and those have no traded history to judge.
	accumulateMinOrderCount = 8

	// Concurrency on the history fan-out, matching the other scanners.
	accumulateHistoryWorkers = 10

	// Books read per side when measuring an item's spread. A year of daily
	// captures plus the weekly tail is well under this; the cap is there so a
	// densely recorded item cannot pull a huge result set into memory for a
	// figure that only needs a median.
	accumulateSpreadMaxBooks = 800

	// How far apart a bid and an ask may be captured and still count as one
	// spread. Archived snapshots carry both sides at the same instant, so they
	// pair at zero; this is the allowance for live-recorded books, whose two
	// sides are written by separate passes minutes apart.
	accumulateSpreadPairWindow = 15 * time.Minute
)

type accumulateRequest struct {
	RegionID   int32   `json:"region_id"`
	MaxTypes   int     `json:"max_types"`
	MaxPct     float64 `json:"max_percentile"`
	MinUnits   float64 `json:"min_units_per_day"`
	MinISK     float64 `json:"min_isk_per_day"`
	MinUpside  float64 `json:"min_upside_pct"`
	MaxPerItem float64 `json:"max_capital_per_item"`
}

type accumulateEnvelope struct {
	Result      *engine.AccumulateResult `json:"result"`
	GeneratedAt string                   `json:"generated_at,omitempty"`
	RegionID    int32                    `json:"region_id"`
	AgeSeconds  float64                  `json:"age_seconds"`
	Stale       bool                     `json:"stale"`
}

// A sweep quotes entry prices, so it goes stale in a way a dashboard does not.
const accumulateStaleAfter = 12 * time.Hour

func (s *Server) handleAccumulateResult(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	regionID := int32(0)
	parseInt32Param(r.URL.Query().Get("region_id"), &regionID)

	env := accumulateEnvelope{Stale: true, RegionID: regionID}
	if s.db == nil {
		writeJSON(w, env)
		return
	}

	var payload, generatedAt string
	var ok bool
	if regionID > 0 {
		payload, generatedAt, ok = s.db.GetAccumulateScan(userID, regionID)
	} else {
		// Today has no region of its own, so it asks for the newest sweep.
		payload, generatedAt, regionID, ok = s.db.LatestAccumulateScan(userID)
		env.RegionID = regionID
	}
	if !ok {
		writeJSON(w, env)
		return
	}

	var result engine.AccumulateResult
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		log.Printf("[ACCUMULATE] stored sweep will not parse: %v", err)
		writeJSON(w, env)
		return
	}
	env.GeneratedAt = generatedAt
	if built, err := time.Parse(time.RFC3339, generatedAt); err == nil {
		env.AgeSeconds = time.Since(built).Seconds()
		env.Stale = time.Since(built) > accumulateStaleAfter
	}
	env.Result = &result
	writeJSON(w, env)
}

func (s *Server) handleAccumulateScan(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	if !s.isReady() {
		writeError(w, http.StatusServiceUnavailable, "SDE not loaded yet")
		return
	}

	var req accumulateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.RegionID <= 0 {
		req.RegionID = holdingRuleRegionID // The Forge, the default trading hub
	}
	if req.MaxTypes <= 0 || req.MaxTypes > 5000 {
		req.MaxTypes = accumulateDefaultMaxTypes
	}

	em, ok := beginNdjson(w, r)
	if !ok {
		return
	}

	result, err := s.runAccumulateScan(r, userID, req, em.Progress)
	if err != nil {
		em.Error(err.Error())
		return
	}
	em.Result(result)
}

// runAccumulateScan does the two phases and persists the outcome.
func (s *Server) runAccumulateScan(
	r *http.Request,
	userID string,
	req accumulateRequest,
	progress func(string),
) (engine.AccumulateResult, error) {
	s.mu.RLock()
	sdeData := s.sdeData
	s.mu.RUnlock()

	// --- Phase 1: who is on the market at all ---
	progress("Reading every order in the region")
	orders, err := s.esi.FetchRegionOrders(req.RegionID, "all")
	if err != nil {
		return engine.AccumulateResult{}, err
	}

	candidates := accumulateCandidatesFromOrders(orders, sdeData)
	progress(accumulateProgressMsg(len(candidates), 0, 0))

	// Rank by order count, not by listed ISK. See the note on
	// AccumulateCandidate.OrderCount: depth measures what is parked, order count
	// measures how contested a market is, and only the second correlates with an
	// item having enough traded history to judge. Ranking a real sweep by depth
	// spent a third of its ESI budget on items that came back "not enough price
	// history".
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].OrderCount != candidates[j].OrderCount {
			return candidates[i].OrderCount > candidates[j].OrderCount
		}
		return candidates[i].SellDepthISK > candidates[j].SellDepthISK
	})
	if len(candidates) > req.MaxTypes {
		candidates = candidates[:req.MaxTypes]
	}

	// --- Phase 2: a year of prices each, mostly from cache ---
	cachedBefore := 0
	if s.db != nil {
		cachedBefore = s.db.CountFreshMarketDerived(req.RegionID)
	}
	progress(accumulateProgressStart(len(candidates), cachedBefore))

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		done      int
		sem       = make(chan struct{}, accumulateHistoryWorkers)
		cancelled bool
	)
	for i := range candidates {
		if r.Context().Err() != nil {
			cancelled = true
			break
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			derived := s.marketDerivedFor(req.RegionID, candidates[idx].TypeID)

			mu.Lock()
			candidates[idx].Derived = derived
			done++
			// Every 50 rather than every row: one NDJSON line per type would
			// be 1,500 messages and the client renders one progress string.
			if done%50 == 0 {
				progress(accumulateProgressMsg(len(candidates), done, 0))
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if cancelled {
		return engine.AccumulateResult{}, r.Context().Err()
	}

	progress("Judging what is cheap, liquid and has recovered before")

	cfg := s.loadConfigForUser(userID)
	opts := engine.AccumulateOpts{
		RegionID:             req.RegionID,
		MaxPercentile:        req.MaxPct,
		MinUnitsPerDay:       req.MinUnits,
		MinISKPerDay:         req.MinISK,
		MinUpsidePct:         req.MinUpside,
		MaxCapitalPerItemISK: req.MaxPerItem,
		Now:                  time.Now().UTC(),
	}
	// Fees come from the same resolver every other realized figure uses, so an
	// upside quoted here and a margin quoted on the Journal agree.
	profile := s.resolveFeeProfile(userID, 0)
	opts.SalesTaxPercent = profile.SalesTaxPercent
	opts.BrokerFeePercent = profile.BrokerFeePercent
	if opts.MaxCapitalPerItemISK <= 0 && cfg != nil && cfg.MaxInvestment > 0 {
		opts.MaxCapitalPerItemISK = cfg.MaxInvestment
	}

	result := engine.BuildAccumulate(candidates, opts)

	// Price the survivors' round trip the way it is actually executed. ESI
	// history has no bid and no ask, so this is the one part of the scan that
	// needs stored order books; it runs over the handful of rows that cleared
	// the gates rather than every candidate, which keeps it to tens of local
	// queries instead of thousands.
	engine.EnrichAccumulateSpreads(&result, opts, s.accumulateSpreadLookup(req.RegionID))

	if len(result.Rows) == 0 {
		result.Warnings = append(result.Warnings,
			"Nothing cleared the gates. That is the normal outcome most days — the filters refuse an item unless it is cheap against its own year, liquid enough to exit, and has a history of dips that recovered.")
	}

	if s.db != nil {
		if payload, marshalErr := json.Marshal(result); marshalErr == nil {
			if saveErr := s.db.SaveAccumulateScan(
				userID, req.RegionID, result.Summary.GeneratedAt, string(payload)); saveErr != nil {
				log.Printf("[ACCUMULATE] persist: %v", saveErr)
			}
		}
	}
	return result, nil
}

// accumulateCandidatesFromOrders reduces a region's whole order book to one row
// per type: the best price on each side, and how much sell-side value is
// sitting there.
func accumulateCandidatesFromOrders(orders []esi.MarketOrder, sdeData *sde.Data) []engine.AccumulateCandidate {
	type agg struct {
		bestSell  float64
		bestBuy   float64
		sellDepth float64
		orders    int
	}
	byType := make(map[int32]*agg, 4096)
	for _, o := range orders {
		if o.Price <= 0 || o.VolumeRemain <= 0 {
			continue
		}
		a := byType[o.TypeID]
		if a == nil {
			a = &agg{}
			byType[o.TypeID] = a
		}
		a.orders++
		if o.IsBuyOrder {
			if o.Price > a.bestBuy {
				a.bestBuy = o.Price
			}
			continue
		}
		if a.bestSell == 0 || o.Price < a.bestSell {
			a.bestSell = o.Price
		}
		a.sellDepth += o.Price * float64(o.VolumeRemain)
	}

	out := make([]engine.AccumulateCandidate, 0, len(byType))
	for typeID, a := range byType {
		if a.bestSell <= 0 || a.sellDepth < accumulateMinSellDepthISK {
			continue
		}
		if a.orders < accumulateMinOrderCount {
			continue
		}
		c := engine.AccumulateCandidate{
			TypeID:       typeID,
			BestSell:     a.bestSell,
			BestBuy:      a.bestBuy,
			SellDepthISK: a.sellDepth,
			OrderCount:   a.orders,
		}
		if sdeData != nil {
			if t, ok := sdeData.Types[typeID]; ok {
				c.TypeName = t.Name
				c.CategoryID = t.CategoryID
			}
		}
		// A type the SDE cannot name is one we cannot present, and it is
		// almost always something not really on the player market.
		if c.TypeName == "" {
			continue
		}
		out = append(out, c)
	}
	return out
}

func accumulateProgressStart(total, cached int) string {
	if cached >= total {
		return "Reading a year of prices for " + itoa(total) + " items (all cached)"
	}
	return "Reading a year of prices for " + itoa(total) + " items · " +
		itoa(cached) + " already cached, the rest from ESI — the first run takes a few minutes"
}

func accumulateProgressMsg(total, done, _ int) string {
	if done == 0 {
		return itoa(total) + " items on the market worth examining"
	}
	return "Checked " + itoa(done) + " of " + itoa(total)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func parseInt32Param(raw string, dst *int32) {
	if raw == "" {
		return
	}
	var v int64
	for _, c := range raw {
		if c < '0' || c > '9' {
			return
		}
		v = v*10 + int64(c-'0')
		if v > 2147483647 {
			return
		}
	}
	*dst = int32(v)
}

// accumulateSpreadLookup measures an item's typical bid-ask gap from stored
// order books.
//
// Returns an unmeasured profile for anything the archive does not cover, which
// is the common case until a Fuzzwork import has run for the region — absent
// evidence, reported as absent rather than as a zero spread.
func (s *Server) accumulateSpreadLookup(regionID int32) func(int32) engine.SpreadProfile {
	if s.db == nil {
		return nil
	}
	get := s.orderBookReplayGetter()
	now := time.Now().UTC()
	from := now.AddDate(-1, 0, 0)

	return func(typeID int32) engine.SpreadProfile {
		side := func(which string) []engine.OrderBookReplayBook {
			books, err := get(engine.OrderBookReplayFilter{
				RegionID: regionID,
				TypeID:   typeID,
				Side:     which,
				From:     from,
				To:       now,
				Limit:    accumulateSpreadMaxBooks,
			})
			if err != nil {
				return nil
			}
			return books
		}
		bids := side("buy")
		if len(bids) == 0 {
			return engine.SpreadProfile{Basis: "none", Reason: "no stored order books for this item"}
		}
		// An archived snapshot carries both sides at one instant, so they pair
		// at zero age. The tolerance is for books recorded live, where the two
		// sides were written by separate passes.
		return engine.CalcSpreadProfile(bids, side("sell"), accumulateSpreadPairWindow)
	}
}
