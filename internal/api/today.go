package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"eve-flipper/internal/auth"
	"eve-flipper/internal/db"
	"eve-flipper/internal/engine"
	"eve-flipper/internal/esi"
	"eve-flipper/internal/sde"
)

// today.go — assembling the Today work order.
//
// Two endpoints with deliberately different costs:
//
//   - GET /api/auth/today reads the persisted plan. One SQLite row, so the
//     landing screen paints before anything touches the network.
//   - POST /api/auth/today/refresh rebuilds it, streaming progress over
//     NDJSON because it fans out to half a dozen sources.
//
// Everything the engine needs is fetched concurrently and mapped into
// engine.TodayInputs. The engine imports nothing from here, so the ranking
// and grading stay testable from plain structs.

// How old a plan gets before the frontend is told to refresh it. The
// frontend decides whether to act on that automatically; the server only
// reports the age.
const todayStaleAfter = 8 * time.Hour

// A plan older than this is not shown at all. Prices from three days ago are
// not a work order, they are a trap with a timestamp on it.
const todayMaxPlanAge = 72 * time.Hour

type todayPlanEnvelope struct {
	Plan        *engine.TodayPlan `json:"plan"`
	GeneratedAt string            `json:"generated_at,omitempty"`
	AgeSeconds  float64           `json:"age_seconds"`
	Stale       bool              `json:"stale"`
	// StaleAfterSeconds lets the frontend apply the same threshold the
	// server used, rather than hard-coding a second copy of it.
	StaleAfterSeconds float64 `json:"stale_after_seconds"`
}

func (s *Server) handleAuthToday(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)

	env := todayPlanEnvelope{StaleAfterSeconds: todayStaleAfter.Seconds(), Stale: true}
	if s.db == nil {
		writeJSON(w, env)
		return
	}

	payload, generatedAt, ok := s.db.GetTodayPlan(userID)
	if !ok {
		writeJSON(w, env)
		return
	}

	var plan engine.TodayPlan
	if err := json.Unmarshal([]byte(payload), &plan); err != nil {
		log.Printf("[TODAY] stored plan for %s will not parse: %v", userID, err)
		writeJSON(w, env)
		return
	}

	env.GeneratedAt = generatedAt
	if built, err := time.Parse(time.RFC3339, generatedAt); err == nil {
		age := time.Since(built)
		env.AgeSeconds = age.Seconds()
		env.Stale = age > todayStaleAfter
		if age > todayMaxPlanAge {
			// Report the age but withhold the plan: a three-day-old paste
			// price is worse than no paste price.
			writeJSON(w, env)
			return
		}
	}

	s.stampTodayActionState(userID, &plan, generatedAt)
	env.Plan = &plan
	writeJSON(w, env)
}

// stampTodayActionState marks the actions the user has already dealt with.
//
// Marks are filtered to those made at or after the plan was built. Action ids
// are deterministic, so without that filter an order repriced yesterday would
// come back already crossed out in today's plan.
func (s *Server) stampTodayActionState(userID string, plan *engine.TodayPlan, generatedAt string) {
	if s.db == nil || plan == nil {
		return
	}
	states := s.db.GetTodayActionStates(userID, generatedAt)
	if len(states) == 0 {
		return
	}
	apply := func(actions []engine.TodayAction) {
		for i := range actions {
			st, ok := states[actions[i].ID]
			if !ok {
				continue
			}
			switch strings.ToLower(st.Mode) {
			case "done":
				actions[i].Done = true
			case "skip":
				actions[i].Skipped = true
			}
		}
	}
	apply(plan.Actions)
	apply(plan.NotAdvised)
}

type todayStateRequest struct {
	ActionID string `json:"action_id"`
	Mode     string `json:"mode"` // done | skip | clear
	// What the plan promised at the moment of acting. Recorded here because
	// the plan that produced it is replaced on the next refresh, and this is
	// the only chance to write it down.
	Kind         string  `json:"kind"`
	TypeID       int32   `json:"type_id"`
	Grade        string  `json:"grade"`
	ProjectedISK float64 `json:"projected_isk"`
	Quantity     int64   `json:"quantity"`
	Price        float64 `json:"price"`
}

func (s *Server) handleAuthTodayState(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)

	var req todayStateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if strings.TrimSpace(req.ActionID) == "" {
		writeError(w, http.StatusBadRequest, "action_id is required")
		return
	}
	switch strings.ToLower(req.Mode) {
	case "done", "skip", "clear":
	default:
		writeError(w, http.StatusBadRequest, `mode must be "done", "skip" or "clear"`)
		return
	}
	if s.db == nil {
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	err := s.db.SetTodayActionState(userID, db.TodayActionState{
		ActionID:     req.ActionID,
		Mode:         strings.ToLower(req.Mode),
		Kind:         req.Kind,
		TypeID:       req.TypeID,
		Grade:        req.Grade,
		ProjectedISK: req.ProjectedISK,
		Quantity:     req.Quantity,
		Price:        req.Price,
		ActedAt:      time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleAuthTodayRefresh(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	characterID, allScope, err := parseAuthScope(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Resolve the scope before the stream opens. Once NDJSON headers are
	// written the status is 200 whatever happens next, and "log in" and "the
	// plan failed" need different responses from the client.
	if _, scopeErr := s.authSessionsForScope(userID, characterID, allScope, true); scopeErr != nil {
		writeStatusError(w, authScopeStatusError(scopeErr))
		return
	}

	em, ok := beginNdjson(w, r)
	if !ok {
		return
	}

	plan, err := s.buildTodayPlan(r.Context(), userID, characterID, allScope, em.Progress)
	if err != nil {
		em.Error(err.Error())
		return
	}

	if s.db != nil {
		if payload, marshalErr := json.Marshal(plan); marshalErr == nil {
			if saveErr := s.db.SaveTodayPlan(userID, plan.GeneratedAt, string(payload)); saveErr != nil {
				log.Printf("[TODAY] could not persist plan for %s: %v", userID, saveErr)
			}
		} else {
			log.Printf("[TODAY] plan for %s will not marshal: %v", userID, marshalErr)
		}
	}

	em.Result(plan)
}

// buildTodayPlan gathers every input concurrently and hands them to the
// engine.
//
// A source that fails is a missing input, not a failed request: the engine
// grades an action it cannot verify as `unproven` and says why, so a plan
// built with the order desk down is smaller and honest rather than absent.
// The only hard failure is having no session at all.
func (s *Server) buildTodayPlan(
	ctx context.Context,
	userID string,
	characterID int64,
	allScope bool,
	progress func(string),
) (engine.TodayPlan, error) {
	sessions, err := s.authSessionsForScope(userID, characterID, allScope, true)
	if err != nil {
		return engine.TodayPlan{}, authScopeStatusError(err)
	}

	in := engine.TodayInputs{Now: time.Now().UTC()}
	if cfg := s.loadConfigForUser(userID); cfg != nil {
		in.MaxInvestmentISK = cfg.MaxInvestment
	}

	var mu sync.Mutex
	var wg sync.WaitGroup

	run := func(label string, fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			progress(label)
			fn()
		}()
	}

	run("Reading your open orders", func() {
		opt := orderDeskBuildOptions{
			SalesTaxPercent:  8.0,
			BrokerFeePercent: 1.0,
			TargetETADays:    3.0,
			MinMarginPercent: 3.0,
		}
		if cfg := s.loadConfigForUser(userID); cfg != nil {
			if cfg.SalesTaxPercent > 0 {
				opt.SalesTaxPercent = cfg.SalesTaxPercent
			}
			if cfg.BrokerFeePercent > 0 {
				opt.BrokerFeePercent = cfg.BrokerFeePercent
			}
		}
		desk, deskErr := s.buildOrderDesk(ctx, userID, characterID, allScope, opt)
		if deskErr != nil {
			log.Printf("[TODAY] order desk: %v", deskErr)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		in.Desk = &desk
		for _, o := range desk.Orders {
			if o.IsBuyOrder {
				in.Capital.BuyOrderISK += o.Notional
			} else {
				in.Capital.SellOrderISK += o.Notional
			}
		}
	})

	run("Pricing what you are holding", func() {
		pos, posErr := s.buildPositions(userID, characterID, allScope, positionFeeOverride{})
		if posErr != nil {
			log.Printf("[TODAY] positions: %v", posErr)
			return
		}
		rows := make([]engine.TodayPosition, 0, len(pos.Rows))
		for _, p := range pos.Rows {
			rows = append(rows, engine.TodayPosition{
				TypeID: p.TypeID, TypeName: p.TypeName, Qty: p.Qty,
				AvgUnitCost: p.AvgUnitCost, CostBasis: p.CostBasis,
				MarketPrice: p.MarketPrice, NetProceeds: p.NetProceeds,
				UnrealizedISK: p.UnrealizedISK, UnrealizedPct: p.UnrealizedPct,
				ListedQty: p.ListedQty, DaysHeld: p.DaysHeld,
				// The holding rule, already resolved by buildPositions, so
				// Today and the Positions tab cannot disagree about what is
				// for sale.
				TradeableQty:      p.TradeableQty,
				ReservedQty:       p.ReservedQty,
				TargetPrice:       p.TargetPrice,
				TargetMet:         p.TargetMet,
				TargetProgressPct: p.TargetProgressPct,
				TargetPercentile:  p.TargetPercentile,
			})
		}
		mu.Lock()
		defer mu.Unlock()
		in.Positions = rows
		in.PositionsPricingFailed = pos.PricingFailed
		in.Capital.InventoryCostISK = pos.TotalCostBasis
	})

	run("Checking your colonies", func() {
		pi, piErr := s.buildPIPlanets(userID, characterID, allScope)
		if piErr != nil {
			log.Printf("[TODAY] pi planets: %v", piErr)
			return
		}
		rows := make([]engine.TodayPlanet, 0, len(pi.Planets))
		for _, p := range pi.Planets {
			row := engine.TodayPlanet{
				CharacterID: p.CharacterID, CharacterName: p.CharacterName,
				PlanetID: int64(p.PlanetID), SolarSystemID: p.SolarSystemID,
				SolarSystemName:      p.SolarSystemName,
				ExpiredExtractorPins: p.ExpiredExtractorPins,
				IdleFactoryPins:      p.IdleFactoryPins,
				NetISKPerDay:         p.NetISKPerDay,
				Status:               p.Status,
			}
			if t, parseErr := time.Parse(time.RFC3339, p.NextExpiry); parseErr == nil {
				row.NextExpiry = t.UTC()
			}
			rows = append(rows, row)
		}
		mu.Lock()
		defer mu.Unlock()
		in.Planets = rows
	})

	run("Looking for finished jobs", func() {
		jobs, locations, wallet := s.todayCharacterState(userID, sessions)
		mu.Lock()
		defer mu.Unlock()
		in.IndustryJobs = jobs
		in.Locations = locations
		in.Capital.WalletISK = wallet
	})

	run("Reading what these items have actually made you", func() {
		history, daily := s.todayJournalEvidence(userID, characterID, allScope, sessions)
		mu.Lock()
		defer mu.Unlock()
		in.ItemHistory = history
		in.DailyPnL = daily
	})

	run("Checking your trading record", func() {
		edges := s.todayEdgeByType(userID)
		mu.Lock()
		defer mu.Unlock()
		in.EdgeByType = edges
	})

	run("Loading your last station scan", func() {
		trades := s.todayLastStationScan()
		mu.Lock()
		defer mu.Unlock()
		in.ScanTrades = trades
	})

	wg.Wait()
	progress("Ranking the work")

	return engine.BuildTodayPlan(in, engine.TodayOpts{}), nil
}

// todayCharacterState collects the three per-character facts Today needs that
// nothing else already fetches: finished industry jobs, where each character
// is docked, and how much ISK is actually spendable.
func (s *Server) todayCharacterState(
	userID string,
	sessions []*auth.Session,
) ([]engine.TodayIndustryJob, []engine.TodayLocation, float64) {
	var jobs []engine.TodayIndustryJob
	var locations []engine.TodayLocation
	var wallet float64

	s.mu.RLock()
	sdeData := s.sdeData
	s.mu.RUnlock()

	// Adjusted prices value the finished product. Same source the PI
	// summariser uses, so a built item and an extracted one are priced by
	// one method.
	priceByType := map[int32]float64{}
	if s.esi != nil && s.industryAnalyzer != nil && s.industryAnalyzer.IndustryCache != nil {
		if prices, err := s.esi.GetAllAdjustedPrices(s.industryAnalyzer.IndustryCache); err == nil {
			priceByType = prices
		}
	}

	for _, sess := range sessions {
		token, err := s.sessions.EnsureValidTokenForUserCharacter(s.sso, userID, sess.CharacterID)
		if err != nil {
			log.Printf("[TODAY] token (%s): %v", sess.CharacterName, err)
			continue
		}

		if balance, balErr := s.esi.GetWalletBalance(sess.CharacterID, token); balErr == nil {
			wallet += balance
		} else {
			log.Printf("[TODAY] wallet (%s): %v", sess.CharacterName, balErr)
		}

		if loc, locErr := s.esi.GetCharacterLocation(sess.CharacterID, token); locErr == nil && loc != nil {
			stationID := loc.StationID
			if stationID == 0 {
				stationID = loc.StructureID
			}
			locations = append(locations, engine.TodayLocation{
				CharacterID:   sess.CharacterID,
				CharacterName: sess.CharacterName,
				StationID:     stationID,
				StationName:   s.esi.StationName(stationID),
				SolarSystemID: loc.SolarSystemID,
			})
		}

		esiJobs, jobErr := s.esi.GetCharacterIndustryJobs(sess.CharacterID, token, false)
		if jobErr != nil {
			log.Printf("[TODAY] industry jobs (%s): %v", sess.CharacterName, jobErr)
			continue
		}
		for _, j := range esiJobs {
			// Manufacturing (1) and reactions (11) produce something to
			// collect. Invention and copying do too, but their output is a
			// blueprint with no adjusted price, so valuing them here would
			// invent a number.
			if j.ActivityID != 1 && j.ActivityID != 11 {
				continue
			}
			end, parseErr := time.Parse(time.RFC3339, j.EndDate)
			if parseErr != nil {
				continue
			}
			jobs = append(jobs, engine.TodayIndustryJob{
				JobID:            j.JobID,
				CharacterID:      sess.CharacterID,
				CharacterName:    sess.CharacterName,
				ProductTypeID:    j.ProductTypeID,
				ProductTypeName:  todayTypeName(sdeData, j.ProductTypeID),
				ProductQuantity:  todayJobProductQuantity(sdeData, j),
				UnitValueISK:     priceByType[j.ProductTypeID],
				EndDate:          end.UTC(),
				Status:           j.Status,
				OutputLocationID: j.OutputLocationID,
				FacilityName:     s.esi.StationName(j.OutputLocationID),
			})
		}
	}
	return jobs, locations, wallet
}

// todayJobProductQuantity is runs × products-per-run, resolved the same way
// the trade journal resolves a manufacturing lot's produced quantity. A
// blueprint missing from the SDE falls back to one per run rather than to
// zero, which would silently drop the job out of the plan.
func todayJobProductQuantity(sdeData *sde.Data, j esi.CharacterIndustryJob) int64 {
	runs := int64(j.Runs)
	if j.SuccessfulRuns > 0 {
		runs = int64(j.SuccessfulRuns)
	}
	if runs <= 0 {
		return 0
	}
	perRun := int64(1)
	if sdeData != nil && sdeData.Industry != nil {
		if bp := sdeData.Industry.Blueprints[j.BlueprintTypeID]; bp != nil {
			activity := "manufacturing"
			if j.ActivityID == 11 {
				activity = "reaction"
			}
			if act := bp.Activities[activity]; act != nil && len(act.Products) > 0 && act.Products[0].Quantity > 0 {
				perRun = int64(act.Products[0].Quantity)
			}
		}
	}
	return runs * perRun
}

func todayTypeName(sdeData *sde.Data, typeID int32) string {
	if sdeData == nil || typeID == 0 {
		return ""
	}
	if t, ok := sdeData.Types[typeID]; ok {
		return t.Name
	}
	return ""
}

// todayJournalEvidence turns the FIFO trade journal into the two things
// grading needs: what each item has actually earned, and the daily P&L series
// behind the return rate.
//
// This runs the journal a second time — buildPositions already ran it for its
// own rows. Sharing one pass would mean threading the result through
// buildPositions' signature for the benefit of one caller, and the compute is
// cached per (user, scope, mode, fees) inside loadTradeJournalResultFor, so
// the second call is usually a map lookup.
func (s *Server) todayJournalEvidence(
	userID string,
	characterID int64,
	allScope bool,
	sessions []*auth.Session,
) (map[int32]engine.TodayItemHistory, []engine.TodayDailyPnL) {
	history := map[int32]engine.TodayItemHistory{}
	var daily []engine.TodayDailyPnL

	var sessionCharID int64
	if len(sessions) > 0 {
		sessionCharID = sessions[0].CharacterID
	}
	filter := positionScopeFilter(characterID, allScope, sessionCharID)

	result, err := s.loadTradeJournalResultFor(userID, filter, time.Time{}, engine.FIFOModeStrictDate, journalFeeRates{})
	if err != nil || result == nil {
		if err != nil {
			log.Printf("[TODAY] journal: %v", err)
		}
		return history, daily
	}

	for _, lot := range result.Lots {
		h := history[lot.TypeID]
		h.TypeID = lot.TypeID
		h.SellsQty++
		h.RealizedISK += lot.NetProfit
		history[lot.TypeID] = h
	}
	for _, d := range result.DailyPnL {
		daily = append(daily, engine.TodayDailyPnL{Date: d.Date, CombinedISK: d.CombinedPnL})
	}
	return history, daily
}

// todayEdgeByType reduces the Trading Edge item rows to the per-type verdict
// grading consults. Paper trades are the only input, so an empty map is the
// normal state for a user who has never logged one — which is why the grader
// treats it as absent rather than as a negative.
func (s *Server) todayEdgeByType(userID string) map[int32]engine.TodayEdge {
	out := map[int32]engine.TodayEdge{}
	if s.db == nil {
		return out
	}
	trades, err := s.db.ListPaperTradesForUser(userID, "all", 1000)
	if err != nil || len(trades) == 0 {
		return out
	}
	summary := s.buildTradingEdgeSummary(trades, 0, "", "")
	for _, row := range summary.Items {
		// Item-scope rows are keyed by the decimal type id (see
		// buildTradingEdgeSummary). Anything that will not parse is a
		// category or station row that reached the wrong slice.
		typeID, parseErr := strconv.ParseInt(row.Key, 10, 32)
		if parseErr != nil || typeID <= 0 {
			continue
		}
		out[int32(typeID)] = engine.TodayEdge{
			LabelCode:         row.LabelCode,
			RealityRatio:      row.RealityRatio,
			WinRatePct:        row.WinRate,
			SampleTrades:      row.ClosedTrades,
			MaxRecommendedQty: row.MaxRecommendedQty,
			MaxExposureISK:    row.MaxExposureISK,
			Advice:            row.Advice,
		}
	}
	return out
}

// todayLastStationScan reads the newest saved station scan for buy
// candidates. Phase 1 deliberately does not run a scan of its own: a refresh
// that took a minute would not get run, and a stale candidate is graded on
// its own conservative figure either way.
func (s *Server) todayLastStationScan() []engine.StationTrade {
	if s.db == nil {
		return nil
	}
	for _, rec := range s.db.GetHistory(25) {
		if rec.Tab != "station" {
			continue
		}
		return s.db.GetStationResults(rec.ID)
	}
	return nil
}
