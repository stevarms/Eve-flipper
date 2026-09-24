package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"eve-flipper/internal/engine"
	"eve-flipper/internal/esi"
	"eve-flipper/internal/sde"
)

// lp_store.go -- the LP Store tab: what every offer in a loyalty-point store
// is worth per LP, sold as-is, sold as a blueprint copy, or built and sold.
//
// The analysis streams in phases so the table is usable before the slow parts
// finish: offers with their market values first, then trade volume, then one
// build result per blueprint, then contract prices for blueprint copies.

// lpMilitiaCorporations are the four faction-warfare militias, listed first in
// the store picker because FW is where most players' LP comes from.
var lpMilitiaCorporations = []struct {
	ID   int32
	Name string
}{
	{1000180, "State Protectorate"},
	{1000181, "Federal Defense Union"},
	{1000179, "24th Imperial Crusade"},
	{1000182, "Tribal Liberation Force"},
}

const (
	// lpVolumeWindowDays is the window average daily volume is taken over.
	lpVolumeWindowDays = 30
	lpBuildWorkers     = 4
	lpHistoryWorkers   = 8
	// A single blueprint copy is 0.01 m3, so a contract holding exactly one is
	// 0.01 m3. The small margin absorbs float noise in ESI's volume.
	lpMaxBPCContractVolume = 0.0105
)

type lpAnalyzeRequest struct {
	CorporationID int32 `json:"corporation_id"`

	// The Profitable Blueprints scanner's saved settings, sent by the tab so
	// both tools build and price the same way.
	BuildSystemName           string  `json:"build_system_name"`
	PricingSystemName         string  `json:"pricing_system_name"`
	PricingStationID          int64   `json:"pricing_station_id"`
	FacilityTax               float64 `json:"facility_tax"`
	StructureBonus            float64 `json:"structure_bonus"`
	BrokerFee                 float64 `json:"broker_fee"`
	SalesTaxPercent           float64 `json:"sales_tax_percent"`
	StructureRigTypeIDs       []int32 `json:"structure_rig_type_ids"`
	StructureTypeID           int32   `json:"structure_type_id"`
	StructureJobCostReduction float64 `json:"structure_job_cost_reduction"`
	SkipReactions             bool    `json:"skip_reactions"`
	CostModel                 string  `json:"cost_model"`
	// BuildMode is the Industry tab's build-vs-buy choice for components:
	// "auto" builds a component when that is cheaper, "buy_all" buys every
	// component, "build_all" builds them all. It decides whether the build
	// value (and the build-materials multibuy) assumes you also run the
	// component jobs.
	BuildMode string `json:"build_mode"`
}

type lpCorporation struct {
	CorporationID int32  `json:"corporation_id"`
	Name          string `json:"name"`
	Militia       bool   `json:"militia"`
}

// handleLPCorporations lists the stores the picker offers without a login:
// the four militias. Stores the character holds LP with come from the
// balances route and are merged in by the tab.
// GET /api/lp/corporations
func (s *Server) handleLPCorporations(w http.ResponseWriter, r *http.Request) {
	out := make([]lpCorporation, 0, len(lpMilitiaCorporations))
	for _, c := range lpMilitiaCorporations {
		out = append(out, lpCorporation{CorporationID: c.ID, Name: c.Name, Militia: true})
	}
	writeJSON(w, out)
}

type lpBalance struct {
	CorporationID int32  `json:"corporation_id"`
	Name          string `json:"name"`
	LoyaltyPoints int64  `json:"loyalty_points"`
}

// handleLPBalances returns the logged-in character's LP per corporation.
// Anything that stops that -- no login, a token issued before the loyalty
// scope was added, ESI down -- comes back as available: false with a reason,
// never an error status, because the tab has a perfectly good fallback: the
// user types their LP.
// GET /api/auth/lp/balances
func (s *Server) handleLPBalances(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	unavailable := func(reason string) {
		writeJSON(w, map[string]interface{}{"available": false, "reason": reason})
	}
	if s.sessions == nil || s.sso == nil {
		unavailable("not_logged_in")
		return
	}
	sess := s.sessions.GetForUser(userID)
	if sess == nil {
		unavailable("not_logged_in")
		return
	}
	token, err := s.sessions.EnsureValidTokenForUserCharacter(s.sso, userID, sess.CharacterID)
	if err != nil {
		unavailable("token")
		return
	}
	points, err := s.esi.GetCharacterLoyaltyPoints(sess.CharacterID, token)
	if err != nil {
		if strings.Contains(err.Error(), "ESI 403") {
			unavailable("missing_scope")
			return
		}
		unavailable("esi")
		return
	}

	ids := make([]int64, 0, len(points))
	for _, p := range points {
		ids = append(ids, int64(p.CorporationID))
	}
	// A failed name lookup costs the names, not the balances.
	names, _ := s.esi.ResolveNames(ids)
	balances := make([]lpBalance, 0, len(points))
	for _, p := range points {
		name := names[int64(p.CorporationID)]
		if name == "" {
			name = fmt.Sprintf("Corporation %d", p.CorporationID)
		}
		balances = append(balances, lpBalance{CorporationID: p.CorporationID, Name: name, LoyaltyPoints: p.LoyaltyPoints})
	}
	sort.Slice(balances, func(i, j int) bool { return balances[i].LoyaltyPoints > balances[j].LoyaltyPoints })
	writeJSON(w, map[string]interface{}{
		"available":      true,
		"character_name": sess.CharacterName,
		"balances":       balances,
	})
}

// handleLPBPCPricesGet returns the user's price-per-run overrides.
// GET /api/auth/lp/bpc-prices
func (s *Server) handleLPBPCPricesGet(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	prices, err := s.db.GetLPBPCPriceOverrides(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "lp bpc prices: "+err.Error())
		return
	}
	writeJSON(w, map[string]interface{}{"prices": prices})
}

// handleLPBPCPricesPut sets one override, or clears it with a null price.
// PUT /api/auth/lp/bpc-prices  {"type_id": 17637, "price_per_run": 38500000 | null}
func (s *Server) handleLPBPCPricesPut(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	var body struct {
		TypeID      int32    `json:"type_id"`
		PricePerRun *float64 `json:"price_per_run"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, defaultAPIRequestBodyMaxBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.TypeID <= 0 {
		writeError(w, http.StatusBadRequest, "type_id is required")
		return
	}
	var err error
	if body.PricePerRun == nil || *body.PricePerRun <= 0 {
		err = s.db.DeleteLPBPCPriceOverride(userID, body.TypeID)
	} else {
		err = s.db.SetLPBPCPriceOverride(userID, body.TypeID, *body.PricePerRun)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "lp bpc price: "+err.Error())
		return
	}
	s.handleLPBPCPricesGet(w, r)
}

// handleLPAnalyze streams the valuation of one LP store.
// POST /api/lp/analyze
//
// Messages, one JSON object per line:
//
//	{"type":"progress","message":...}
//	{"type":"offers","rows":[...],"corporation_id":..,"region_id":..}  -- once
//	{"type":"row","row":{...}}       -- a row whose values changed
//	{"type":"warning","message":...} -- a phase that could not finish
//	{"type":"error","message":...}   -- fatal; nothing follows
//	{"type":"done"}
func (s *Server) handleLPAnalyze(w http.ResponseWriter, r *http.Request) {
	if !s.isReady() {
		writeError(w, http.StatusServiceUnavailable, "SDE not loaded yet")
		return
	}
	userID := userIDFromRequest(r)

	r.Body = http.MaxBytesReader(w, r.Body, defaultAPIRequestBodyMaxBytes)
	var req lpAnalyzeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.CorporationID <= 0 {
		writeError(w, http.StatusBadRequest, "corporation_id is required")
		return
	}
	req.FacilityTax = clampFloat64(req.FacilityTax, 0, 100)
	req.StructureBonus = clampFloat64(req.StructureBonus, -100, 100)
	req.StructureJobCostReduction = clampFloat64(req.StructureJobCostReduction, 0, 100)
	req.BrokerFee = clampFloat64(req.BrokerFee, 0, 100)
	req.SalesTaxPercent = clampFloat64(req.SalesTaxPercent, 0, 100)
	if req.PricingStationID < 0 {
		req.PricingStationID = 0
	}
	if len(req.StructureRigTypeIDs) > 3 {
		req.StructureRigTypeIDs = req.StructureRigTypeIDs[:3]
	}
	req.CostModel = strings.TrimSpace(strings.ToLower(req.CostModel))
	switch req.CostModel {
	case "", "buy_to_sell", "buy_to_buy":
	default:
		writeError(w, http.StatusBadRequest, "invalid cost_model")
		return
	}
	req.BuildMode = strings.TrimSpace(strings.ToLower(req.BuildMode))
	switch req.BuildMode {
	case "", "auto", "buy_all", "build_all":
	default:
		writeError(w, http.StatusBadRequest, "invalid build_mode")
		return
	}
	if strings.TrimSpace(req.PricingSystemName) == "" {
		req.PricingSystemName = "Jita"
	}

	s.mu.RLock()
	sdeData := s.sdeData
	analyzer := s.industryAnalyzer
	scanner := s.scanner
	s.mu.RUnlock()
	if sdeData == nil || analyzer == nil {
		writeError(w, http.StatusServiceUnavailable, "SDE not loaded yet")
		return
	}
	buildSystemID := sdeData.SystemByName[strings.ToLower(strings.TrimSpace(req.BuildSystemName))]
	pricingSystemID := sdeData.SystemByName[strings.ToLower(strings.TrimSpace(req.PricingSystemName))]
	if pricingSystemID == 0 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown pricing system %q", req.PricingSystemName))
		return
	}
	regionID := int32(0)
	if sys := sdeData.Systems[pricingSystemID]; sys != nil {
		regionID = sys.RegionID
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, flushOK := w.(http.Flusher)
	if !flushOK {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	// http.ResponseWriter is not safe for concurrent use and the build phase
	// writes from a worker pool, so every write goes through this mutex, and a
	// write after the client left is dropped rather than attempted.
	var writeMu sync.Mutex
	ctx := r.Context()
	writeLine := func(payload interface{}) {
		line, err := json.Marshal(payload)
		if err != nil {
			return
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		if ctx.Err() != nil {
			return
		}
		fmt.Fprintf(w, "%s\n", line)
		flusher.Flush()
	}
	progress := func(msg string) { writeLine(map[string]string{"type": "progress", "message": msg}) }
	warn := func(msg string) { writeLine(map[string]string{"type": "warning", "message": msg}) }

	// Phase 1: offers and the market.
	progress("Fetching LP store offers...")
	offers, err := s.esi.FetchLoyaltyOffers(req.CorporationID)
	if err != nil {
		writeLine(map[string]string{"type": "error", "message": err.Error()})
		return
	}

	baseParams := engine.IndustryParams{
		ActivityMode:              "manufacturing",
		MaterialEfficiency:        0,
		TimeEfficiency:            0,
		SystemID:                  buildSystemID,
		PricingSystemID:           pricingSystemID,
		StationID:                 req.PricingStationID,
		FacilityTax:               req.FacilityTax,
		StructureBonus:            req.StructureBonus,
		BrokerFee:                 req.BrokerFee,
		SalesTaxPercent:           req.SalesTaxPercent,
		MaxDepth:                  10,
		OwnBlueprint:              true,
		SkipReactions:             req.SkipReactions,
		StructureJobCostReduction: req.StructureJobCostReduction,
		StructureRigs: engine.StructureRigConfig{
			RigTypeIDs:      req.StructureRigTypeIDs,
			StructureTypeID: req.StructureTypeID,
		},
		CostModel: req.CostModel,
		BuildMode: req.BuildMode,
	}

	progress("Loading market orders...")
	sellBooks, buyBooks, err := analyzer.LoadMarketBooksForParams(baseParams)
	if err != nil {
		writeLine(map[string]string{"type": "error", "message": "market orders: " + err.Error()})
		return
	}
	fees := engine.LPFees{SalesTaxPercent: req.SalesTaxPercent, BrokerFeePercent: req.BrokerFee}

	rows := make([]engine.LPOfferRow, 0, len(offers))
	for _, o := range offers {
		// Analysis Kredits are a separate currency the tool does not value.
		if o.AKCost > 0 {
			continue
		}
		meta := lpOfferMeta(o.TypeID, sdeData)
		quoteType := o.TypeID
		if meta.IsBlueprint {
			quoteType = meta.ProductTypeID
		}
		q := lpQuoteFromBooks(sellBooks[quoteType], buyBooks[quoteType])
		offer := engine.LPOffer{
			OfferID:  o.OfferID,
			TypeID:   o.TypeID,
			Quantity: o.Quantity,
			LPCost:   o.LPCost,
			ISKCost:  o.ISKCost,
		}
		for _, ri := range o.RequiredItems {
			ask := lpQuoteFromBooks(sellBooks[ri.TypeID], nil).BestAsk
			offer.RequiredItems = append(offer.RequiredItems, engine.LPRequiredItem{
				TypeID:    ri.TypeID,
				TypeName:  lpTypeName(ri.TypeID, sdeData),
				Quantity:  ri.Quantity,
				UnitPrice: ask,
				Priced:    ask > 0,
			})
		}
		rows = append(rows, engine.NewLPOfferRow(offer, meta, &q, fees))
	}
	writeLine(map[string]interface{}{
		"type":           "offers",
		"corporation_id": req.CorporationID,
		"region_id":      regionID,
		"rows":           rows,
	})

	var rowsMu sync.Mutex
	emitRow := func(i int) {
		rowsMu.Lock()
		row := rows[i]
		rowsMu.Unlock()
		writeLine(map[string]interface{}{"type": "row", "row": row})
	}

	// Phase 2: trade volume, for liquidity. Cached in the DB for a day, so a
	// second analysis of the same store is nearly free.
	if ctx.Err() == nil && regionID > 0 {
		progress("Loading trade volume...")
		volumes := s.lpVolumes(ctx, regionID, rows)
		for i := range rows {
			vt := rows[i].TypeID
			if rows[i].IsBlueprint {
				vt = rows[i].ProductTypeID
			}
			if v, ok := volumes[vt]; ok && v != rows[i].AvgDailyVolume {
				rows[i].AvgDailyVolume = v
				emitRow(i)
			}
		}
	}

	// Phase 3: build every blueprint offer. Offers for the same blueprint at
	// the same run count are the same build, so each is analyzed once.
	type buildKey struct {
		product int32
		runs    int64
	}
	buildGroups := map[buildKey][]int{}
	for i, row := range rows {
		if row.IsBlueprint && row.ProductTypeID > 0 && row.Runs > 0 {
			k := buildKey{row.ProductTypeID, row.Runs}
			buildGroups[k] = append(buildGroups[k], i)
		}
	}
	if ctx.Err() == nil && len(buildGroups) > 0 {
		progress(fmt.Sprintf("Analyzing %d blueprint builds...", len(buildGroups)))
		scanAnalyzer := lpMemoizedAnalyzer(analyzer, sellBooks, buyBooks)
		sem := make(chan struct{}, lpBuildWorkers)
		var wg sync.WaitGroup
		var doneMu sync.Mutex
		done := 0
		for k, idxs := range buildGroups {
			if ctx.Err() != nil {
				break
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(k buildKey, idxs []int) {
				defer wg.Done()
				defer func() { <-sem }()
				if ctx.Err() != nil {
					return
				}
				// The analyzer keeps per-call state on its receiver; each
				// worker gets its own shallow copy (see CLAUDE.md).
				local := scanAnalyzer
				params := baseParams
				params.TypeID = k.product
				params.Runs = int32(k.runs)
				res, err := local.Analyze(params, func(string) {})
				result := lpBuildResultFrom(res, err)
				rowsMu.Lock()
				for _, i := range idxs {
					rows[i].ApplyBuild(result)
				}
				rowsMu.Unlock()
				for _, i := range idxs {
					emitRow(i)
				}
				doneMu.Lock()
				done++
				n := done
				doneMu.Unlock()
				progress(fmt.Sprintf("Analyzed %d/%d blueprint builds", n, len(buildGroups)))
			}(k, idxs)
		}
		wg.Wait()
	}

	// Phase 4: what blueprint copies sell for on contract, overridden by the
	// user's own figure where they have one.
	bpTypes := map[int32]bool{}
	for _, row := range rows {
		if row.IsBlueprint {
			bpTypes[row.TypeID] = true
		}
	}
	if ctx.Err() == nil && len(bpTypes) > 0 {
		overrides, _ := s.db.GetLPBPCPriceOverrides(userID)
		prices := map[int32]engine.LPBPCPrice{}
		if scanner != nil && regionID > 0 {
			progress("Loading blueprint copy contracts...")
			contracts, err := s.esi.FetchRegionContractsCached(scanner.ContractsCache, regionID)
			if err != nil {
				warn("blueprint copy contracts could not be loaded, so the blueprint-sale column is empty: " + err.Error())
			} else {
				ids := lpBPCCandidateContracts(contracts)
				shapes := s.lpRefreshContractShapes(regionID, ids, scanner.ContractItemsCache, func(done, total int) {
					if done == total || done%100 == 0 {
						progress(fmt.Sprintf("Reading new blueprint copy contracts %d/%d", done, total))
					}
				})
				prices = lpBPCPricesFromShapes(contracts, shapes, bpTypes)
			}
		}
		for i := range rows {
			if !rows[i].IsBlueprint {
				continue
			}
			var p *engine.LPBPCPrice
			if price, ok := overrides[rows[i].TypeID]; ok && price > 0 {
				p = &engine.LPBPCPrice{PerRun: price, Samples: prices[rows[i].TypeID].Samples, Override: true}
			} else if est, ok := prices[rows[i].TypeID]; ok {
				p = &est
			}
			if p == nil {
				continue
			}
			rowsMu.Lock()
			rows[i].ApplyBPCPrice(p)
			rowsMu.Unlock()
			emitRow(i)
		}
	}

	writeLine(map[string]string{"type": "done"})
}

// lpMemoizedAnalyzer returns an analyzer copy that reuses the books already
// loaded for phase 1 and memoizes the other region-wide fetches, so a hundred
// builds do not regroup The Forge's order book a hundred times.
func lpMemoizedAnalyzer(analyzer *engine.IndustryAnalyzer, sell, buy map[int32][]esi.MarketOrder) engine.IndustryAnalyzer {
	var (
		pricesOnce     sync.Once
		cachedPrices   map[int32]float64
		pricesErr      error
		adjustedOnce   sync.Once
		cachedAdjusted map[int32]float64
		adjustedErr    error
	)
	a := *analyzer
	a.SetMarketBooksOverride(func(engine.IndustryParams) (map[int32][]esi.MarketOrder, map[int32][]esi.MarketOrder, error) {
		return sell, buy, nil
	})
	a.SetMarketPricesOverride(func(p engine.IndustryParams) (map[int32]float64, error) {
		pricesOnce.Do(func() {
			tmp := *analyzer
			tmp.SetMarketPricesOverride(nil)
			cachedPrices, pricesErr = tmp.LoadMarketPricesForParams(p)
		})
		return cachedPrices, pricesErr
	})
	a.SetAdjustedPricesOverride(func(_ *esi.IndustryCache) (map[int32]float64, error) {
		adjustedOnce.Do(func() {
			tmp := *analyzer
			tmp.SetAdjustedPricesOverride(nil)
			cachedAdjusted, adjustedErr = tmp.LoadAdjustedPrices()
		})
		return cachedAdjusted, adjustedErr
	})
	return a
}

func lpBuildResultFrom(res *engine.IndustryAnalysis, err error) engine.LPBuildResult {
	if err != nil {
		return engine.LPBuildResult{Error: err.Error()}
	}
	if res == nil {
		return engine.LPBuildResult{Error: "no analysis"}
	}
	out := engine.LPBuildResult{
		InstantProfit:    res.InstantSellProfit,
		InstantAvailable: res.InstantSellAvailable,
		ListedProfit:     res.MakerSellProfit,
		BuildCost:        res.OptimalBuildCost,
		JobCost:          res.TotalJobCost,
	}
	for _, m := range res.FlatMaterials {
		if m == nil || m.Quantity <= 0 {
			continue
		}
		out.Materials = append(out.Materials, engine.LPMaterial{TypeID: m.TypeID, TypeName: m.TypeName, Quantity: int64(m.Quantity)})
	}
	return out
}

// lpVolumes returns average daily volume per product type over the window,
// from the DB's day-long history cache or ESI.
func (s *Server) lpVolumes(ctx context.Context, regionID int32, rows []engine.LPOfferRow) map[int32]float64 {
	types := map[int32]bool{}
	for _, row := range rows {
		t := row.TypeID
		if row.IsBlueprint {
			t = row.ProductTypeID
		}
		if t > 0 {
			types[t] = true
		}
	}
	out := make(map[int32]float64, len(types))
	var mu sync.Mutex
	sem := make(chan struct{}, lpHistoryWorkers)
	var wg sync.WaitGroup
	now := time.Now().UTC()
	for t := range types {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(typeID int32) {
			defer wg.Done()
			defer func() { <-sem }()
			entries, ok := s.db.GetMarketHistory(regionID, typeID)
			if !ok {
				fetched, err := s.esi.FetchMarketHistory(regionID, typeID)
				if err != nil {
					return
				}
				entries = fetched
				s.db.SetMarketHistory(regionID, typeID, fetched)
			}
			v := lpAvgDailyVolume(entries, lpVolumeWindowDays, now)
			mu.Lock()
			out[typeID] = v
			mu.Unlock()
		}(t)
	}
	wg.Wait()
	return out
}

// lpOfferMeta is what the SDE says about an offer's item: its name and, for a
// blueprint, what it builds and how many per run.
func lpOfferMeta(typeID int32, sdeData *sde.Data) engine.LPOfferMeta {
	meta := engine.LPOfferMeta{TypeName: lpTypeName(typeID, sdeData)}
	if sdeData == nil || sdeData.Industry == nil {
		return meta
	}
	bp, ok := sdeData.Industry.Blueprints[typeID]
	if !ok || bp == nil || bp.ProductTypeID <= 0 {
		return meta
	}
	meta.IsBlueprint = true
	meta.TypeName = blueprintDisplayName(typeID, sdeData)
	meta.ProductTypeID = bp.ProductTypeID
	meta.ProductName = lpTypeName(bp.ProductTypeID, sdeData)
	meta.ProductPerRun = int64(bp.ProductQuantity)
	if meta.ProductPerRun <= 0 {
		meta.ProductPerRun = 1
	}
	return meta
}

func lpTypeName(typeID int32, sdeData *sde.Data) string {
	if sdeData != nil {
		if t, ok := sdeData.Types[typeID]; ok && t != nil && strings.TrimSpace(t.Name) != "" {
			return strings.TrimSpace(t.Name)
		}
	}
	return fmt.Sprintf("Type %d", typeID)
}

// lpQuoteFromBooks reduces one type's two order books to best prices and
// depth. Orders with nothing left are ignored.
func lpQuoteFromBooks(sell, buy []esi.MarketOrder) engine.LPMarketQuote {
	var q engine.LPMarketQuote
	for _, o := range sell {
		if o.Price <= 0 || o.VolumeRemain <= 0 {
			continue
		}
		q.AskDepth += int64(o.VolumeRemain)
		if q.BestAsk == 0 || o.Price < q.BestAsk {
			q.BestAsk = o.Price
		}
	}
	for _, o := range buy {
		if o.Price <= 0 || o.VolumeRemain <= 0 {
			continue
		}
		q.BidDepth += int64(o.VolumeRemain)
		if o.Price > q.BestBid {
			q.BestBid = o.Price
		}
	}
	return q
}

// lpAvgDailyVolume is units traded per day over the last `days` days. Days
// with no trade are absent from ESI's history, so this divides by the window,
// not by the number of entries -- dividing by entries would make an item that
// traded once in a month look like it trades every day.
func lpAvgDailyVolume(entries []esi.HistoryEntry, days int, now time.Time) float64 {
	if len(entries) == 0 || days <= 0 {
		return 0
	}
	cutoff := now.AddDate(0, 0, -days).Format("2006-01-02")
	var total int64
	for _, e := range entries {
		if e.Date >= cutoff {
			total += e.Volume
		}
	}
	return float64(total) / float64(days)
}

// lpBPCCandidateContracts picks the public contracts that could be a single
// blueprint copy for sale, from the listing alone: a priced item exchange,
// still open, at one copy's volume. Only these get the per-contract item
// lookup, which is what makes this phase affordable in The Forge.
func lpBPCCandidateContracts(contracts []esi.PublicContract) []int32 {
	var ids []int32
	for _, c := range contracts {
		if c.Type != "item_exchange" || c.Price <= 0 || c.Volume <= 0 || c.Volume > lpMaxBPCContractVolume {
			continue
		}
		if c.IsExpired() {
			continue
		}
		ids = append(ids, c.ContractID)
	}
	return ids
}

// lpContractShape is all the LP tool needs to know about a contract's
// contents: whether it sells exactly one blueprint copy, and of what.
type lpContractShape struct {
	TypeID int32
	Runs   int
	OK     bool // exactly one included blueprint copy with runs
}

func lpContractShapeOf(items []esi.ContractItem) lpContractShape {
	if len(items) != 1 {
		return lpContractShape{}
	}
	it := items[0]
	if !it.IsIncluded || !it.IsBlueprintCopy || it.Runs <= 0 || it.Quantity != 1 {
		return lpContractShape{}
	}
	return lpContractShape{TypeID: it.TypeID, Runs: it.Runs, OK: true}
}

// lpContractShapes remembers every candidate contract's shape per region.
// Contract contents never change, and The Forge has more single-copy-sized
// contracts than the scanner's item cache holds, so without this every
// analysis refetched thousands of contracts. Each refresh keeps only the
// contracts still listed, so the map is bounded by the live market.
var (
	lpContractShapesMu sync.Mutex
	lpContractShapes   = map[int32]map[int32]lpContractShape{}
)

// lpRefreshContractShapes returns the shape of every candidate, fetching only
// the ones not seen before. A contract whose items could not be fetched is
// left out, and tried again next time.
func (s *Server) lpRefreshContractShapes(regionID int32, candidates []int32, cache *esi.ContractItemsCache, progress func(done, total int)) map[int32]lpContractShape {
	lpContractShapesMu.Lock()
	known := lpContractShapes[regionID]
	lpContractShapesMu.Unlock()

	live := make(map[int32]lpContractShape, len(candidates))
	var missing []int32
	for _, id := range candidates {
		if shape, ok := known[id]; ok {
			live[id] = shape
		} else {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		items := s.esi.FetchContractItemsBatch(missing, cache, progress)
		for _, id := range missing {
			if its, ok := items[id]; ok {
				live[id] = lpContractShapeOf(its)
			}
		}
	}

	lpContractShapesMu.Lock()
	lpContractShapes[regionID] = live
	lpContractShapesMu.Unlock()
	return live
}

// lpBPCPricesFromContracts is the median asking price per run for each wanted
// blueprint type, from contracts selling exactly one copy of it and nothing
// else. Per run, because the same blueprint is sold as 1-run and 10-run copies.
func lpBPCPricesFromContracts(contracts []esi.PublicContract, items map[int32][]esi.ContractItem, want map[int32]bool) map[int32]engine.LPBPCPrice {
	shapes := make(map[int32]lpContractShape, len(items))
	for id, its := range items {
		shapes[id] = lpContractShapeOf(its)
	}
	return lpBPCPricesFromShapes(contracts, shapes, want)
}

func lpBPCPricesFromShapes(contracts []esi.PublicContract, shapes map[int32]lpContractShape, want map[int32]bool) map[int32]engine.LPBPCPrice {
	samples := map[int32][]float64{}
	for _, c := range contracts {
		shape, ok := shapes[c.ContractID]
		if !ok || !shape.OK || c.Price <= 0 || !want[shape.TypeID] {
			continue
		}
		samples[shape.TypeID] = append(samples[shape.TypeID], c.Price/float64(shape.Runs))
	}
	out := make(map[int32]engine.LPBPCPrice, len(samples))
	for typeID, ss := range samples {
		out[typeID] = engine.LPBPCPrice{PerRun: engine.LPMedian(ss), Samples: len(ss)}
	}
	return out
}
