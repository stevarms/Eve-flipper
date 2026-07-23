package api

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"sync"

	"eve-flipper/internal/esi"
)

// priceAuditItem is one entry in the request payload. Name and TypeID are
// both accepted — TypeID wins when set (lets programmatic callers skip the
// SDE name-lookup roundtrip). Qty is preserved verbatim in the response.
type priceAuditItem struct {
	Name   string `json:"name"`
	TypeID int32  `json:"type_id,omitempty"`
	Qty    int64  `json:"qty"`
}

// priceAuditResult mirrors one input item after ESI lookup.
// Source values: "station" | "region" | "avg" | "none".
// Unresolved rows (name not in SDE) return with unresolved=true and no prices.
type priceAuditResult struct {
	Name           string   `json:"name"`
	Qty            int64    `json:"qty"`
	TypeID         int32    `json:"type_id,omitempty"`
	TypeName       string   `json:"type_name,omitempty"`
	TopBuy         *float64 `json:"top_buy,omitempty"`
	LowSell        *float64 `json:"low_sell,omitempty"`
	SuggestedPrice *float64 `json:"suggested_price,omitempty"`
	Source         string   `json:"source"`
	Unresolved     bool     `json:"unresolved,omitempty"`
}

type priceAuditResponse struct {
	Results     []priceAuditResult `json:"results"`
	RegionID    int32              `json:"region_id"`
	StationID   int64              `json:"station_id"`
	StationName string             `json:"station_name"`
}

// nextSellUndercut returns the largest EVE-legal (4-significant-digit) price
// strictly less than lowestSell. Mirrors the pricing rule in the multisell
// "Import Prices" workflow: minimum undercut is the place value of the 4th
// significant digit (10k on prices in the millions, 1M on prices in the
// billions, etc). Returns 0 when the input isn't a positive finite number.
func nextSellUndercut(lowestSell float64) float64 {
	if !(lowestSell > 0) || math.IsInf(lowestSell, 0) || math.IsNaN(lowestSell) {
		return 0
	}
	magnitude := math.Floor(math.Log10(lowestSell))
	place := math.Pow(10, magnitude-3)
	if place <= 0 {
		return 0
	}
	// Snap down to the nearest 4-sig-fig grid.
	floored := math.Floor(lowestSell/place) * place
	if floored < lowestSell {
		return snapToGrid(floored, place)
	}
	// Already on a valid boundary — step down one place.
	stepped := lowestSell - place
	if stepped <= 0 {
		return 0
	}
	return snapToGrid(stepped, place)
}

// snapToGrid rounds value to the nearest multiple of place, scrubbing IEEE-754
// noise from the subtraction (e.g. 19.17 - 0.01 producing 19.16999...998).
// After this the value is a clean multiple of place and JSON-encodes to the
// expected decimal string.
func snapToGrid(value, place float64) float64 {
	if place <= 0 {
		return value
	}
	return math.Round(value/place) * place
}

func (s *Server) handlePriceAudit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StationID int64            `json:"station_id"`
		Items     []priceAuditItem `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !s.isReady() {
		writeError(w, http.StatusServiceUnavailable, "SDE not loaded yet")
		return
	}
	if req.StationID <= 0 {
		writeError(w, http.StatusBadRequest, "station_id required")
		return
	}
	if len(req.Items) == 0 {
		writeError(w, http.StatusBadRequest, "items array required")
		return
	}

	s.mu.RLock()
	sdeData := s.sdeData
	scanner := s.scanner
	s.mu.RUnlock()
	_ = scanner // reserved for future depth-aware pricing

	station, ok := sdeData.Stations[req.StationID]
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown station")
		return
	}
	system, ok := sdeData.Systems[station.SystemID]
	if !ok {
		writeError(w, http.StatusInternalServerError, "station has no system")
		return
	}
	regionID := system.RegionID

	// Fetch global market prices once (cached, 10-min TTL) for the last-resort
	// "avg" fallback. Failure here is non-fatal — we just won't have averages.
	avgByType := map[int32]float64{}
	if prices, err := s.esi.FetchMarketPrices(); err == nil {
		for _, p := range prices {
			if p.AveragePrice > 0 {
				avgByType[p.TypeID] = p.AveragePrice
			}
		}
	}

	// Build the initial results skeleton in input order.
	results := make([]priceAuditResult, len(req.Items))
	type resolvedJob struct {
		idx    int
		typeID int32
	}
	jobs := make([]resolvedJob, 0, len(req.Items))
	for i, in := range req.Items {
		results[i] = priceAuditResult{
			Name:   in.Name,
			Qty:    in.Qty,
			Source: "none",
		}
		var typeID int32
		if in.TypeID > 0 {
			typeID = in.TypeID
		} else {
			key := strings.ToLower(strings.TrimSpace(in.Name))
			if key == "" {
				results[i].Unresolved = true
				continue
			}
			var ok bool
			typeID, ok = sdeData.TypeByName[key]
			if !ok {
				results[i].Unresolved = true
				continue
			}
		}
		itemType, ok := sdeData.Types[typeID]
		if !ok {
			results[i].Unresolved = true
			continue
		}
		results[i].TypeID = typeID
		results[i].TypeName = itemType.Name
		jobs = append(jobs, resolvedJob{idx: i, typeID: typeID})
	}

	// Fan out per-type order fetches. ESI's per-type endpoint is cheaper than
	// pulling the whole region's book, and the client-level rate-limit
	// semaphore inside FetchRegionOrdersByType keeps us safe under burst.
	const workers = 8
	jobCh := make(chan resolvedJob)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobCh {
				orders, err := s.esi.FetchRegionOrdersByType(regionID, job.typeID)
				if err != nil {
					mu.Lock()
					results[job.idx].Source = "none"
					mu.Unlock()
					continue
				}
				stationTopBuy, stationLowSell := aggregateStationBook(orders, req.StationID)
				regionLowSell := aggregateRegionLowSell(orders)

				mu.Lock()
				if stationTopBuy > 0 {
					v := stationTopBuy
					results[job.idx].TopBuy = &v
				}
				switch {
				case stationLowSell > 0:
					v := stationLowSell
					results[job.idx].LowSell = &v
					suggested := nextSellUndercut(stationLowSell)
					if suggested > 0 {
						results[job.idx].SuggestedPrice = &suggested
					}
					results[job.idx].Source = "station"
				case regionLowSell > 0:
					v := regionLowSell
					results[job.idx].LowSell = &v
					suggested := nextSellUndercut(regionLowSell)
					if suggested > 0 {
						results[job.idx].SuggestedPrice = &suggested
					}
					results[job.idx].Source = "region"
				default:
					if avg, ok := avgByType[job.typeID]; ok && avg > 0 {
						v := avg
						results[job.idx].LowSell = &v
						results[job.idx].SuggestedPrice = &v
						results[job.idx].Source = "avg"
					}
				}
				mu.Unlock()
			}
		}()
	}
	for _, job := range jobs {
		jobCh <- job
	}
	close(jobCh)
	wg.Wait()

	writeJSON(w, priceAuditResponse{
		Results:     results,
		RegionID:    regionID,
		StationID:   req.StationID,
		StationName: station.Name,
	})
}

// aggregateStationBook scans a per-type region order slice and returns the
// station-scoped top buy and lowest sell (0 when absent).
func aggregateStationBook(orders []esi.MarketOrder, stationID int64) (topBuy, lowSell float64) {
	lowSell = math.MaxFloat64
	for _, o := range orders {
		if o.LocationID != stationID || o.VolumeRemain <= 0 {
			continue
		}
		if o.IsBuyOrder {
			if o.Price > topBuy {
				topBuy = o.Price
			}
		} else {
			if o.Price < lowSell {
				lowSell = o.Price
			}
		}
	}
	if lowSell == math.MaxFloat64 {
		lowSell = 0
	}
	return topBuy, lowSell
}

// aggregateRegionLowSell returns the lowest sell price across the whole
// per-type slice (i.e. the region's cheapest ask for this type).
func aggregateRegionLowSell(orders []esi.MarketOrder) float64 {
	lowSell := math.MaxFloat64
	for _, o := range orders {
		if o.IsBuyOrder || o.VolumeRemain <= 0 {
			continue
		}
		if o.Price < lowSell {
			lowSell = o.Price
		}
	}
	if lowSell == math.MaxFloat64 {
		return 0
	}
	return lowSell
}
