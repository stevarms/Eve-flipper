package api

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"eve-flipper/internal/esi"
	"eve-flipper/internal/sde"
)

// hubAllocateRequest — pick which hubs to consider and how many days of
// expected sales each hub may absorb before we spill over to the next one.
//
// Strategy values (defaults to "balanced" when empty):
//   profit   — sort hubs by suggested price DESC. Highest-paying hub gets
//              first crack at the qty (capped by days_of_stock × daily_flow).
//   balanced — sort by price × sqrt(daily_flow). Rewards both price and
//              liquidity so we don't over-send to thin hubs.
//   volume   — sort by daily_flow DESC. Highest-throughput hub gets the qty
//              first; low-volume hubs only get overflow.
//   percent  — ignore days_of_stock; split qty per HubPercents. Sum of
//              percentages is normalized to 100 across the given hubs.
//
// Profit/balanced/volume all drop hubs where daily_flow < MinDailyFlow (0.5
// per day by default) to stop rows like "10 Scarab I → Dodixie" that
// technically win on price but won't actually sell.
type hubAllocateRequest struct {
	StationIDs   []int64            `json:"station_ids"`
	DaysOfStock  int                `json:"days_of_stock"`
	Items        []priceAuditItem   `json:"items"`
	HistoryDays  int                `json:"history_days"` // flow window in days; 7 if 0
	FlowMetric   string             `json:"flow_metric"`  // "median" (default) | "mean"
	Strategy     string             `json:"strategy"`
	HubPercents  map[string]float64 `json:"hub_percents"` // station_id (as string) -> % share for strategy=percent
	MinDailyFlow float64            `json:"min_daily_flow"`
	// NoUnallocated forces any leftover qty into the deepest-market priced
	// hub instead of leaving it unallocated. Ignores the days-of-stock cap
	// for the overflow dump — the whole point of the option is "just get it
	// out the door, don't leave anything behind".
	NoUnallocated bool `json:"no_unallocated"`
	// HubCaps caps the total m³ we can ship to a given station. Keyed by
	// station_id (as string). Zero or missing = uncapped. Enforced AFTER
	// the primary strategy: excess volume gets trimmed by lowest ISK/m³
	// first so the cargo you do send is your densest, best-paying cargo.
	HubCaps map[string]float64 `json:"hub_caps"`
}

// hubAllocation is one shipment: sell `qty` at `station_id` for `price` ISK
// per unit. `daily_flow` is the region's average daily volume over the last
// N days (proxy for hub-level throughput; canonical trade hubs dominate their
// region's book so this is a good approximation).
type hubAllocation struct {
	StationID   int64            `json:"station_id"`
	StationName string           `json:"station_name"`
	SystemName  string           `json:"system_name"`
	Qty         int64            `json:"qty"`
	Price       *float64         `json:"price,omitempty"`
	DailyFlow   float64          `json:"daily_flow"`
	// DailyVolumes carries the raw per-day volume samples the flow metric was
	// derived from, oldest → newest. Frontend renders these in a hover tooltip
	// so the user can sanity-check "why does this row show 10/day".
	DailyVolumes []int64          `json:"daily_volumes,omitempty"`
	Source       priceAuditSource `json:"source"`
	// UnitVolume is the per-unit packaged m³ from the SDE. Frontend uses this
	// with Qty to compute the row's contribution to a capped hub's cargo.
	UnitVolume float64 `json:"unit_volume,omitempty"`
}

// hubAllocateResult is one input item's fanned-out allocation across hubs.
// `allocations` is sorted by price descending (best-paying hub first).
// `unallocated` is what's left over when every hub hits its days-of-stock cap.
type hubAllocateResult struct {
	Name        string          `json:"name"`
	Qty         int64           `json:"qty"`
	TypeID      int32           `json:"type_id,omitempty"`
	TypeName    string          `json:"type_name,omitempty"`
	Allocations []hubAllocation `json:"allocations"`
	Unallocated int64           `json:"unallocated"`
	Unresolved  bool            `json:"unresolved,omitempty"`
}

type hubStationMeta struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	SystemName string  `json:"system_name"`
	RegionID   int32   `json:"region_id"`
	VolumeCap  float64 `json:"volume_cap,omitempty"` // m³; 0 means uncapped
}

type hubAllocateResponse struct {
	Results  []hubAllocateResult `json:"results"`
	Stations []hubStationMeta    `json:"stations"`
}

// priceAuditSource is exported here for reuse; the string tags match the
// single-station price-audit response so the frontend can share badge logic.
type priceAuditSource = string

// hubContext bundles the resolved metadata for one station in a hub-allocate
// request so we don't re-derive it inside the fan-out loop.
type hubContext struct {
	stationID   int64
	stationName string
	systemName  string
	regionID    int32
	volumeCap   float64 // m³; 0 = uncapped
}

// hubPriceQuote captures one (item, hub) pricing outcome from the fan-out
// stage: the reference low-sell price the suggestion was derived from, the
// suggested undercut, and which source in the fallback ladder it came from.
type hubPriceQuote struct {
	lowSell   float64
	suggested float64
	source    priceAuditSource
}

// hubQuote is one (item, hub) price+flow pairing produced by the fan-out
// stage and consumed by the allocation stage.
type hubQuote struct {
	itemIdx    int // index into the caller's `resolved` slice
	hubIdx     int // index into the caller's `hubs` slice
	q          hubPriceQuote
	flow       float64
	rawVolumes []int64 // raw per-day samples the flow metric was derived from
}

// resolvedItem is one input line that successfully mapped to an SDE type,
// plus everything the allocator needs to reason about it.
type resolvedItem struct {
	inputIdx int
	typeID   int32
	typeName string
	qty      int64
	volume   float64 // per-unit m³ from SDE
}

func (s *Server) handleHubAllocate(w http.ResponseWriter, r *http.Request) {
	var req hubAllocateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !s.isReady() {
		writeError(w, http.StatusServiceUnavailable, "SDE not loaded yet")
		return
	}
	if len(req.StationIDs) == 0 {
		writeError(w, http.StatusBadRequest, "station_ids required")
		return
	}
	if len(req.Items) == 0 {
		writeError(w, http.StatusBadRequest, "items array required")
		return
	}
	if req.DaysOfStock <= 0 {
		req.DaysOfStock = 7
	}
	if req.HistoryDays <= 0 {
		req.HistoryDays = 7
	}
	strategy := strings.ToLower(strings.TrimSpace(req.Strategy))
	switch strategy {
	case "profit", "balanced", "volume", "percent":
		// ok
	default:
		strategy = "balanced"
	}
	minFlow := req.MinDailyFlow
	if minFlow <= 0 {
		minFlow = 0.5
	}
	// Percent strategy needs normalized weights per hub. Zero-sum falls back
	// to equal split across the selected hubs.
	percentWeights := map[int64]float64{}
	if strategy == "percent" {
		total := 0.0
		for k, v := range req.HubPercents {
			// station_id came in as a JSON object key (string).
			id, err := strconv.ParseInt(k, 10, 64)
			if err != nil || v < 0 {
				continue
			}
			percentWeights[id] = v
			total += v
		}
		if total <= 0 {
			// Equal split fallback so the request still produces output.
			for _, id := range req.StationIDs {
				percentWeights[id] = 1
			}
			total = float64(len(req.StationIDs))
		}
		for id, v := range percentWeights {
			percentWeights[id] = v / total // now sums to 1
		}
	}

	s.mu.RLock()
	sdeData := s.sdeData
	s.mu.RUnlock()

	// Resolve each requested station to a hub context. Skip unknowns instead of
	// failing — the user may have a stale localStorage entry after a station
	// rename in the SDE, and dropping bad hubs is friendlier than 400ing.
	hubs := make([]hubContext, 0, len(req.StationIDs))
	stationsMeta := make([]hubStationMeta, 0, len(req.StationIDs))
	for _, id := range req.StationIDs {
		st, ok := sdeData.Stations[id]
		if !ok {
			continue
		}
		sys, ok := sdeData.Systems[st.SystemID]
		if !ok {
			continue
		}
		var cap float64
		if req.HubCaps != nil {
			if v, ok := req.HubCaps[strconv.FormatInt(id, 10)]; ok && v > 0 {
				cap = v
			}
		}
		hubs = append(hubs, hubContext{
			stationID:   id,
			stationName: st.Name,
			systemName:  sys.Name,
			regionID:    sys.RegionID,
			volumeCap:   cap,
		})
		stationsMeta = append(stationsMeta, hubStationMeta{
			ID:         id,
			Name:       st.Name,
			SystemName: sys.Name,
			RegionID:   sys.RegionID,
			VolumeCap:  cap,
		})
	}
	if len(hubs) == 0 {
		writeError(w, http.StatusBadRequest, "no valid stations")
		return
	}

	// Global fallback prices, fetched once (10-min cached upstream).
	avgByType := map[int32]float64{}
	if prices, err := s.esi.FetchMarketPrices(); err == nil {
		for _, p := range prices {
			if p.AveragePrice > 0 {
				avgByType[p.TypeID] = p.AveragePrice
			}
		}
	}

	// Resolve item names → typeIDs once so we don't do it per hub.
	results := make([]hubAllocateResult, len(req.Items))
	resolved := make([]resolvedItem, 0, len(req.Items))
	for i, in := range req.Items {
		results[i] = hubAllocateResult{
			Name:        in.Name,
			Qty:         in.Qty,
			Allocations: []hubAllocation{},
		}
		key := strings.ToLower(strings.TrimSpace(in.Name))
		if key == "" {
			results[i].Unresolved = true
			continue
		}
		typeID, ok := sdeData.TypeByName[key]
		if !ok {
			results[i].Unresolved = true
			continue
		}
		itemType, ok := sdeData.Types[typeID]
		if !ok {
			results[i].Unresolved = true
			continue
		}
		results[i].TypeID = typeID
		results[i].TypeName = itemType.Name
		resolved = append(resolved, resolvedItem{
			inputIdx: i,
			typeID:   typeID,
			typeName: itemType.Name,
			qty:      in.Qty,
			volume:   itemType.Volume,
		})
	}

	// Fan out per (item, hub) fetching orders + history in parallel. Each unit
	// of work touches a specific (regionID, typeID) pair. hubQuote is defined
	// at package scope so it's usable from the allocation helpers below.
	quoteCh := make(chan hubQuote, len(resolved)*len(hubs))

	type job struct {
		itemIdx int
		hubIdx  int
	}
	jobs := make([]job, 0, len(resolved)*len(hubs))
	for i := range resolved {
		for h := range hubs {
			jobs = append(jobs, job{itemIdx: i, hubIdx: h})
		}
	}

	const workers = 8
	jobCh := make(chan job)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobCh {
				item := resolved[j.itemIdx]
				hub := hubs[j.hubIdx]

				var q hubPriceQuote
				orders, err := s.esi.FetchRegionOrdersByType(hub.regionID, item.typeID)
				if err == nil {
					_, stationLowSell := aggregateStationBook(orders, hub.stationID)
					regionLowSell := aggregateRegionLowSell(orders)
					switch {
					case stationLowSell > 0:
						q.lowSell = stationLowSell
						q.suggested = nextSellUndercut(stationLowSell)
						q.source = "station"
					case regionLowSell > 0:
						q.lowSell = regionLowSell
						q.suggested = nextSellUndercut(regionLowSell)
						q.source = "region"
					default:
						if avg, ok := avgByType[item.typeID]; ok && avg > 0 {
							q.lowSell = avg
							q.suggested = avg
							q.source = "avg"
						}
					}
				}

				flow, rawVols := regionalDailyFlow(
					s.esi, hub.regionID, item.typeID, req.HistoryDays, req.FlowMetric,
				)

				quoteCh <- hubQuote{
					itemIdx:    j.itemIdx,
					hubIdx:     j.hubIdx,
					q:          q,
					flow:       flow,
					rawVolumes: rawVols,
				}
			}
		}()
	}
	go func() {
		for _, j := range jobs {
			jobCh <- j
		}
		close(jobCh)
	}()
	go func() {
		wg.Wait()
		close(quoteCh)
	}()

	// Bucket quotes per input item so we can run the allocator once per item
	// with a complete view of all hubs.
	perItem := make(map[int][]hubQuote, len(resolved))
	for hq := range quoteCh {
		perItem[hq.itemIdx] = append(perItem[hq.itemIdx], hq)
	}

	daysCap := float64(req.DaysOfStock)
	for i, item := range resolved {
		quotes := perItem[i]

		if strategy == "percent" {
			allocs, unallocated := allocateByPercent(item.qty, quotes, hubs, percentWeights, item.volume)
			if req.NoUnallocated && unallocated > 0 {
				allocs, unallocated = overflowRemainder(allocs, unallocated, quotes, hubs, item.volume)
			}
			results[item.inputIdx].Allocations = allocs
			results[item.inputIdx].Unallocated = unallocated
			continue
		}

		// Drop hubs that don't clear the min-flow bar OR failed to price.
		// This is the "don't send 10 Scarab I to Dodixie because avg-price
		// looked best" filter — regional avg with no real turnover isn't a
		// real opportunity.
		liveQuotes := quotes[:0]
		for _, hq := range quotes {
			if hq.q.suggested <= 0 {
				continue
			}
			if hq.flow < minFlow {
				continue
			}
			liveQuotes = append(liveQuotes, hq)
		}

		// Sort per strategy. Higher-scoring hubs are consumed first.
		switch strategy {
		case "profit":
			sort.SliceStable(liveQuotes, func(a, b int) bool {
				return liveQuotes[a].q.suggested > liveQuotes[b].q.suggested
			})
		case "volume":
			sort.SliceStable(liveQuotes, func(a, b int) bool {
				return liveQuotes[a].flow > liveQuotes[b].flow
			})
		default: // "balanced"
			score := func(q hubQuote) float64 {
				return q.q.suggested * math.Sqrt(q.flow+1)
			}
			sort.SliceStable(liveQuotes, func(a, b int) bool {
				return score(liveQuotes[a]) > score(liveQuotes[b])
			})
		}

		remaining := item.qty
		allocs := make([]hubAllocation, 0, len(liveQuotes))
		for _, hq := range liveQuotes {
			if remaining <= 0 {
				break
			}
			// Days-of-stock cap. Round up so a hub with even a trickle of
			// demand still gets a shot at absorbing something.
			capUnits := int64(math.Ceil(hq.flow * daysCap))
			if capUnits <= 0 {
				continue
			}
			take := capUnits
			if take > remaining {
				take = remaining
			}
			hub := hubs[hq.hubIdx]
			price := hq.q.suggested
			allocs = append(allocs, hubAllocation{
				StationID:    hub.stationID,
				StationName:  hub.stationName,
				SystemName:   hub.systemName,
				Qty:          take,
				Price:        &price,
				DailyFlow:    hq.flow,
				DailyVolumes: hq.rawVolumes,
				Source:       hq.q.source,
				UnitVolume:   item.volume,
			})
			remaining -= take
		}
		if req.NoUnallocated && remaining > 0 {
			allocs, remaining = overflowRemainder(allocs, remaining, quotes, hubs, item.volume)
		}
		results[item.inputIdx].Allocations = allocs
		results[item.inputIdx].Unallocated = remaining
	}

	// Enforce per-hub volume caps AFTER the primary strategy — this lets the
	// cap trim by density (ISK/m³) so the cargo you do send is your best.
	// Trimmed qty is added back to the item's Unallocated; if no_unallocated
	// is on we then run overflow again to try to place the trimmed qty in an
	// uncapped hub.
	if hasAnyCap(hubs) {
		applyVolumeCaps(results, hubs)
		if req.NoUnallocated {
			retryOverflowAfterCaps(results, perItem, hubs, resolved)
		}
	}

	writeJSON(w, hubAllocateResponse{
		Results:  results,
		Stations: stationsMeta,
	})
}

// hasAnyCap reports whether any hub in the request specified a volume cap.
// Skips the cap-enforcement pass entirely when no hub is capped.
func hasAnyCap(hubs []hubContext) bool {
	for _, h := range hubs {
		if h.volumeCap > 0 {
			return true
		}
	}
	return false
}

// applyVolumeCaps trims each capped hub's allocations down to its cap by
// dropping the LEAST dense (lowest ISK/m³) qty first. This maximises the
// value carried in the cargo we do send. Trimmed qty is added back to the
// row's Unallocated counter so the caller can decide what to do with it.
//
// Rows with UnitVolume <= 0 are treated as "unknown volume" and left alone —
// we don't want to accidentally trim a 0.01 m³ item claiming 4000 m³ worth
// of cargo because of a stale SDE row.
func applyVolumeCaps(results []hubAllocateResult, hubs []hubContext) {
	capByStation := map[int64]float64{}
	for _, h := range hubs {
		if h.volumeCap > 0 {
			capByStation[h.stationID] = h.volumeCap
		}
	}
	if len(capByStation) == 0 {
		return
	}

	// Bucket allocations per station across ALL items so we can trim by
	// global density. Each ref points back to the results slice for in-place
	// mutation.
	type allocRef struct {
		resultIdx int
		allocIdx  int
	}
	perStation := map[int64][]allocRef{}
	for ri := range results {
		for ai := range results[ri].Allocations {
			a := &results[ri].Allocations[ai]
			if _, capped := capByStation[a.StationID]; !capped {
				continue
			}
			perStation[a.StationID] = append(perStation[a.StationID], allocRef{ri, ai})
		}
	}

	for stationID, refs := range perStation {
		cap := capByStation[stationID]
		// Sort ascending by ISK/m³ — worst density first, drop those first.
		sort.SliceStable(refs, func(a, b int) bool {
			ai := &results[refs[a].resultIdx].Allocations[refs[a].allocIdx]
			bi := &results[refs[b].resultIdx].Allocations[refs[b].allocIdx]
			da := iskPerM3(ai)
			db := iskPerM3(bi)
			return da < db
		})
		used := 0.0
		for _, r := range refs {
			a := &results[r.resultIdx].Allocations[r.allocIdx]
			if a.UnitVolume <= 0 || a.Qty <= 0 {
				used += a.UnitVolume * float64(a.Qty)
				continue
			}
			used += a.UnitVolume * float64(a.Qty)
		}
		if used <= cap {
			continue
		}
		// Walk refs low-density → high-density, trim qty until under cap.
		// The loop iterates a copy of refs (asc) so we mutate lowest-value
		// allocations first, preserving the densest cargo.
		overflow := used - cap
		for _, r := range refs {
			if overflow <= 0 {
				break
			}
			a := &results[r.resultIdx].Allocations[r.allocIdx]
			if a.UnitVolume <= 0 || a.Qty <= 0 {
				continue
			}
			rowM3 := a.UnitVolume * float64(a.Qty)
			if rowM3 <= overflow {
				// Drop the entire row.
				overflow -= rowM3
				results[r.resultIdx].Unallocated += a.Qty
				a.Qty = 0
				continue
			}
			// Partial trim: drop units until we're under cap. Round up so
			// we don't accidentally leave the hub 0.001 m³ over.
			unitsToDrop := int64(math.Ceil(overflow / a.UnitVolume))
			if unitsToDrop > a.Qty {
				unitsToDrop = a.Qty
			}
			overflow -= float64(unitsToDrop) * a.UnitVolume
			results[r.resultIdx].Unallocated += unitsToDrop
			a.Qty -= unitsToDrop
		}
	}

	// Purge fully-trimmed (Qty == 0) allocations so the response is clean.
	for ri := range results {
		filtered := results[ri].Allocations[:0]
		for _, a := range results[ri].Allocations {
			if a.Qty > 0 {
				filtered = append(filtered, a)
			}
		}
		results[ri].Allocations = filtered
	}
}

// iskPerM3 returns the density of one hubAllocation row. Rows with unknown
// or zero unit volume return +Inf so they sort as "always keep" — better to
// preserve them than accidentally trim what we can't measure.
func iskPerM3(a *hubAllocation) float64 {
	if a.UnitVolume <= 0 || a.Price == nil || *a.Price <= 0 {
		return math.Inf(1)
	}
	return *a.Price / a.UnitVolume
}

// retryOverflowAfterCaps places any qty that applyVolumeCaps kicked back to
// Unallocated. Walks hubs by deepest-market first and only places what will
// fit inside each hub's remaining cap — spilling to the next hub until the
// item is exhausted or every hub is full. This is where the "no unallocated"
// promise meets the cap constraint: cap wins, and if genuinely nothing fits
// the qty stays Unallocated (surfaced in the UI's red strip).
func retryOverflowAfterCaps(
	results []hubAllocateResult,
	perItem map[int][]hubQuote,
	hubs []hubContext,
	resolved []resolvedItem,
) {
	// Track live per-station usage so within-item placements respect the cap
	// AND across-item placements do too. Seeded from current allocations,
	// then incremented as we place more.
	usedByStation := map[int64]float64{}
	for _, r := range results {
		for _, a := range r.Allocations {
			usedByStation[a.StationID] += a.UnitVolume * float64(a.Qty)
		}
	}

	for i, item := range resolved {
		if results[item.inputIdx].Unallocated <= 0 {
			continue
		}
		quotes := perItem[i]
		// Keep only priced quotes; sort deepest-market first, price as tiebreak.
		priced := make([]hubQuote, 0, len(quotes))
		for _, q := range quotes {
			if q.q.suggested > 0 {
				priced = append(priced, q)
			}
		}
		sort.SliceStable(priced, func(a, b int) bool {
			if priced[a].flow != priced[b].flow {
				return priced[a].flow > priced[b].flow
			}
			return priced[a].q.suggested > priced[b].q.suggested
		})

		remaining := results[item.inputIdx].Unallocated
		allocs := results[item.inputIdx].Allocations

		for _, q := range priced {
			if remaining <= 0 {
				break
			}
			hub := hubs[q.hubIdx]

			// How many units fit at this hub? Uncapped hubs take everything.
			// Capped hubs take min(remaining, floor((cap - used) / unitVolume)).
			var maxUnits int64
			if hub.volumeCap > 0 {
				free := hub.volumeCap - usedByStation[hub.stationID]
				if free <= 0 {
					continue
				}
				if item.volume > 0 {
					maxUnits = int64(math.Floor(free / item.volume))
				} else {
					// Unknown volume: allow everything and hope for the best,
					// otherwise a broken SDE row could strand qty forever.
					maxUnits = remaining
				}
			} else {
				maxUnits = remaining
			}
			if maxUnits <= 0 {
				continue
			}
			take := remaining
			if take > maxUnits {
				take = maxUnits
			}

			// Fold into an existing allocation to the same hub, else append.
			price := q.q.suggested
			folded := false
			for j := range allocs {
				if allocs[j].StationID == hub.stationID {
					allocs[j].Qty += take
					folded = true
					break
				}
			}
			if !folded {
				allocs = append(allocs, hubAllocation{
					StationID:    hub.stationID,
					StationName:  hub.stationName,
					SystemName:   hub.systemName,
					Qty:          take,
					Price:        &price,
					DailyFlow:    q.flow,
					DailyVolumes: q.rawVolumes,
					Source:       q.q.source,
					UnitVolume:   item.volume,
				})
			}
			usedByStation[hub.stationID] += float64(take) * item.volume
			remaining -= take
		}

		results[item.inputIdx].Allocations = allocs
		results[item.inputIdx].Unallocated = remaining
	}
}

// overflowRemainder dumps leftover qty into the "best" hub that had a valid
// price, ignoring the days-of-stock cap. "Best" = highest daily flow (deepest
// market — best real chance of clearing the stock), price used as tiebreak.
// If there's already an allocation to that hub the qty is folded in;
// otherwise a new hubAllocation is appended. Returns (allocations, leftover)
// — leftover stays > 0 only when no hub had a valid price to begin with.
func overflowRemainder(
	allocs []hubAllocation,
	remaining int64,
	quotes []hubQuote,
	hubs []hubContext,
	unitVolume float64,
) ([]hubAllocation, int64) {
	if remaining <= 0 {
		return allocs, 0
	}
	// Pick the best overflow target from ALL priced quotes (ignoring the
	// min-flow filter we applied earlier — we're explicitly saying "just
	// place it somewhere" here).
	bestIdx := -1
	for i, q := range quotes {
		if q.q.suggested <= 0 {
			continue
		}
		if bestIdx < 0 {
			bestIdx = i
			continue
		}
		b := quotes[bestIdx]
		if q.flow > b.flow || (q.flow == b.flow && q.q.suggested > b.q.suggested) {
			bestIdx = i
		}
	}
	if bestIdx < 0 {
		// Nothing was priced anywhere — genuinely unallocatable.
		return allocs, remaining
	}
	target := quotes[bestIdx]
	targetHub := hubs[target.hubIdx]

	// If we already allocated something to this hub, fold in the extra qty
	// rather than emit a duplicate row.
	for i := range allocs {
		if allocs[i].StationID == targetHub.stationID {
			allocs[i].Qty += remaining
			return allocs, 0
		}
	}
	price := target.q.suggested
	allocs = append(allocs, hubAllocation{
		StationID:    targetHub.stationID,
		StationName:  targetHub.stationName,
		SystemName:   targetHub.systemName,
		Qty:          remaining,
		Price:        &price,
		DailyFlow:    target.flow,
		DailyVolumes: target.rawVolumes,
		Source:       target.q.source,
		UnitVolume:   unitVolume,
	})
	return allocs, 0
}

// allocateByPercent splits qty across hubs per user-supplied percentage
// weights (already normalized so values sum to 1). Uses largest-remainder
// rounding so sum(alloc) == qty exactly. Hubs where we couldn't price the
// item are skipped and any residual is returned as `unallocated`.
func allocateByPercent(
	qty int64,
	quotes []hubQuote,
	hubs []hubContext,
	weights map[int64]float64,
	unitVolume float64,
) ([]hubAllocation, int64) {
	if qty <= 0 || len(quotes) == 0 {
		return []hubAllocation{}, qty
	}
	// Only consider hubs that have both a valid price and a weight.
	type slot struct {
		hq     hubQuote
		weight float64
	}
	slots := make([]slot, 0, len(quotes))
	totalWeight := 0.0
	for _, hq := range quotes {
		if hq.q.suggested <= 0 {
			continue
		}
		w := weights[hubs[hq.hubIdx].stationID]
		if w <= 0 {
			continue
		}
		slots = append(slots, slot{hq: hq, weight: w})
		totalWeight += w
	}
	if len(slots) == 0 || totalWeight <= 0 {
		return []hubAllocation{}, qty
	}
	// Re-normalize (some weights may have been dropped for unpriced hubs).
	// Compute floor allocations and track remainders for largest-remainder.
	type portion struct {
		floor     int64
		remainder float64
		idx       int
	}
	portions := make([]portion, len(slots))
	assigned := int64(0)
	for i, s := range slots {
		raw := float64(qty) * (s.weight / totalWeight)
		fl := int64(math.Floor(raw))
		portions[i] = portion{floor: fl, remainder: raw - float64(fl), idx: i}
		assigned += fl
	}
	// Hand out leftover units to the largest fractional remainders.
	leftover := qty - assigned
	if leftover > 0 {
		sort.SliceStable(portions, func(a, b int) bool {
			return portions[a].remainder > portions[b].remainder
		})
		for k := int64(0); k < leftover && k < int64(len(portions)); k++ {
			portions[k].floor++
		}
	}
	// Build allocations, sorted back to original hub order for stable output.
	sort.SliceStable(portions, func(a, b int) bool {
		return portions[a].idx < portions[b].idx
	})
	allocs := make([]hubAllocation, 0, len(portions))
	for _, p := range portions {
		if p.floor <= 0 {
			continue
		}
		s := slots[p.idx]
		hub := hubs[s.hq.hubIdx]
		price := s.hq.q.suggested
		allocs = append(allocs, hubAllocation{
			StationID:    hub.stationID,
			StationName:  hub.stationName,
			SystemName:   hub.systemName,
			Qty:          p.floor,
			Price:        &price,
			DailyFlow:    s.hq.flow,
			DailyVolumes: s.hq.rawVolumes,
			Source:       s.hq.q.source,
			UnitVolume:   unitVolume,
		})
	}
	// Percent mode never leaves a remainder unless every hub failed to
	// price. In that case return the qty as unallocated so the UI can flag it.
	placed := int64(0)
	for _, a := range allocs {
		placed += a.Qty
	}
	return allocs, qty - placed
}

// regionalDailyFlow returns the flow metric for typeID in regionID over the
// last `days` CALENDAR DAYS, plus the raw per-day volumes (oldest first) so
// the frontend can surface them in a hover tooltip. Days ESI didn't return
// are padded as zeros — critical, because ESI's market-history endpoint
// omits days with no trades, and treating those as "not in the window" would
// silently narrow the window to only-active days (making thin markets look
// far more liquid than they are).
//
// Median is the default so a one-time bulk buy weeks ago doesn't masquerade
// as ongoing liquidity; mean is available when the caller wants the classic
// average-over-window.
func regionalDailyFlow(
	client *esi.Client,
	regionID int32,
	typeID int32,
	days int,
	metric string,
) (float64, []int64) {
	if client == nil || days <= 0 {
		return 0, nil
	}
	entries, err := client.FetchMarketHistory(regionID, typeID)
	if err != nil {
		return 0, nil
	}
	// Index available data by date string. Some entries may have zero volume;
	// they still count as "we heard from ESI, use their number".
	byDate := make(map[string]int64, len(entries))
	var newestDate string
	for _, e := range entries {
		byDate[e.Date] = e.Volume
		if e.Date > newestDate {
			newestDate = e.Date
		}
	}

	// Anchor the window at whichever is more recent: today (UTC) or the
	// newest ESI entry we have. ESI market history typically lags by 1 day,
	// so today's slot usually pads to 0 — that's fine and truthful. If the
	// item is really thin and newest data is weeks old, we still start from
	// today so the tail correctly shows "hasn't sold in a while".
	const layout = "2006-01-02"
	end := time.Now().UTC()
	if newestDate != "" {
		if parsed, perr := time.Parse(layout, newestDate); perr == nil && parsed.After(end) {
			end = parsed
		}
	}

	raw := make([]int64, days)
	for i := 0; i < days; i++ {
		// oldest first — i=0 is (days-1) ago, i=days-1 is today.
		d := end.AddDate(0, 0, -(days - 1 - i))
		raw[i] = byDate[d.Format(layout)] // zero if missing (thin day)
	}

	switch strings.ToLower(metric) {
	case "mean":
		var sum int64
		for _, v := range raw {
			sum += v
		}
		return float64(sum) / float64(len(raw)), raw
	default: // "median"
		sorted := make([]int64, len(raw))
		copy(sorted, raw)
		sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })
		mid := len(sorted) / 2
		if len(sorted)%2 == 1 {
			return float64(sorted[mid]), raw
		}
		return float64(sorted[mid-1]+sorted[mid]) / 2, raw
	}
}

// Compile-time reminder that we depend on the same SDE shape as price_audit.
var _ = sde.Data{}
