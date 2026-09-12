package api

import (
	"context"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"eve-flipper/internal/engine"
	"eve-flipper/internal/esi"
)

// order_desk_build.go — the order desk as a function rather than a handler.
//
// Building the desk is the most expensive authenticated thing the app does:
// every character's orders, then an ESI order book and price history per
// (region, type) pair, plus a full FIFO journal pass for cost basis. Two
// callers now want that payload — the Orders tab via GET /api/auth/orders/desk
// and the Today work order — and neither should own the fan-out.
//
// The handler keeps only what a handler is for: reading query parameters and
// turning an error into a status code.

// statusError carries the HTTP status a failure should surface as, so a
// builder can report "not logged in" without importing the handler's
// judgement about what that means.
type statusError struct {
	status int
	msg    string
}

func (e statusError) Error() string { return e.msg }

// Status returns the HTTP status to respond with, defaulting to 500.
func (e statusError) Status() int {
	if e.status == 0 {
		return http.StatusInternalServerError
	}
	return e.status
}

// writeStatusError responds with err's own status when it carries one, and
// 500 otherwise.
func writeStatusError(w http.ResponseWriter, err error) {
	if se, ok := err.(statusError); ok {
		writeError(w, se.Status(), se.msg)
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

type orderDeskBuildOptions struct {
	SalesTaxPercent  float64
	BrokerFeePercent float64
	TargetETADays    float64
	MinMarginPercent float64
}

// buildOrderDesk fetches every input the desk needs and computes it.
//
// allScope tolerates a per-character failure and carries on with the rest;
// single-character mode surfaces it, because there is nothing left to show.
func (s *Server) buildOrderDesk(
	ctx context.Context,
	userID string,
	characterID int64,
	allScope bool,
	opt orderDeskBuildOptions,
) (engine.OrderDeskResponse, error) {
	selectedSessions, err := s.authSessionsForScope(userID, characterID, allScope, true)
	if err != nil {
		return engine.OrderDeskResponse{}, authScopeStatusError(err)
	}

	engineOpts := engine.OrderDeskOptions{
		SalesTaxPercent:  opt.SalesTaxPercent,
		BrokerFeePercent: opt.BrokerFeePercent,
		TargetETADays:    opt.TargetETADays,
		WarnExpiryDays:   2,
		MinMarginPercent: opt.MinMarginPercent,
	}

	var orders []esi.CharacterOrder
	// Remember which order belongs to which session so the response can
	// carry owner tags on each row.
	type orderOwner struct {
		characterID   int64
		characterName string
	}
	ownerByOrderID := make(map[int64]orderOwner)
	for _, sess := range selectedSessions {
		token, tokenErr := s.sessions.EnsureValidTokenForUserCharacter(s.sso, userID, sess.CharacterID)
		if tokenErr != nil {
			log.Printf("[AUTH] OrderDesk token error (%s): %v", sess.CharacterName, tokenErr)
			if !allScope {
				return engine.OrderDeskResponse{}, statusError{http.StatusUnauthorized, tokenErr.Error()}
			}
			continue
		}
		charOrders, fetchErr := s.esi.GetCharacterOrders(sess.CharacterID, token)
		if fetchErr != nil {
			log.Printf("[AUTH] OrderDesk orders error (%s): %v", sess.CharacterName, fetchErr)
			if !allScope {
				return engine.OrderDeskResponse{}, statusError{
					http.StatusInternalServerError, "failed to fetch orders: " + fetchErr.Error()}
			}
			continue
		}
		for _, o := range charOrders {
			ownerByOrderID[o.OrderID] = orderOwner{characterID: sess.CharacterID, characterName: sess.CharacterName}
		}
		orders = append(orders, charOrders...)
	}

	if len(orders) == 0 {
		return engine.ComputeOrderDesk(nil, nil, nil, nil, engineOpts), nil
	}

	// Enrich names for UI readability.
	s.mu.RLock()
	sdeData := s.sdeData
	s.mu.RUnlock()
	if sdeData != nil {
		locationIDs := make(map[int64]bool, len(orders))
		for _, o := range orders {
			locationIDs[o.LocationID] = true
		}
		s.esi.PrefetchStationNames(locationIDs)
		for i := range orders {
			if t, ok := sdeData.Types[orders[i].TypeID]; ok {
				orders[i].TypeName = t.Name
			}
			orders[i].LocationName = s.esi.StationName(orders[i].LocationID)
		}
	}

	type regionType struct {
		regionID int32
		typeID   int32
	}
	pairs := make(map[regionType]bool)
	for _, o := range orders {
		pairs[regionType{regionID: o.RegionID, typeID: o.TypeID}] = true
	}

	type fetchResult struct {
		orders []esi.MarketOrder
		err    error
	}
	books := make(map[regionType]fetchResult)
	history := make(map[engine.OrderDeskHistoryKey][]esi.HistoryEntry)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)

	// The FIFO trade journal is the only source that knows what held stock
	// actually cost, which is the one thing the book cannot tell us about a
	// sell order. Run it against the book fan-out rather than after it, so
	// a cold journal cache costs no extra wall-clock.
	var costBasisByType map[int32]float64
	wg.Add(1)
	go func() {
		defer wg.Done()
		costBasisByType = s.orderDeskCostBasisByType(userID)
	}()

	for pair := range pairs {
		wg.Add(1)
		go func(rt regionType) {
			defer wg.Done()

			sem <- struct{}{}
			ro, fetchErr := s.esi.FetchRegionOrdersByTypeContext(ctx, rt.regionID, rt.typeID)
			<-sem

			var entries []esi.HistoryEntry
			var ok bool
			if s.db != nil {
				entries, ok = s.db.GetMarketHistory(rt.regionID, rt.typeID)
			}
			if !ok {
				fresh, histErr := s.esi.FetchMarketHistory(rt.regionID, rt.typeID)
				if histErr == nil {
					entries = fresh
					if s.db != nil && len(entries) > 0 {
						s.db.SetMarketHistory(rt.regionID, rt.typeID, entries)
					}
				}
			}

			mu.Lock()
			books[rt] = fetchResult{orders: ro, err: fetchErr}
			if len(entries) > 0 {
				history[engine.NewOrderDeskHistoryKey(rt.regionID, rt.typeID)] = entries
			}
			mu.Unlock()
		}(pair)
	}
	wg.Wait()

	var allRegional []esi.MarketOrder
	unavailableBooks := make(map[engine.OrderDeskHistoryKey]bool)
	for rt, fr := range books {
		if fr.err == nil {
			allRegional = append(allRegional, fr.orders...)
			continue
		}
		unavailableBooks[engine.NewOrderDeskHistoryKey(rt.regionID, rt.typeID)] = true
	}

	engineOpts.CostBasisByType = costBasisByType
	result := engine.ComputeOrderDesk(orders, allRegional, history, unavailableBooks, engineOpts)

	// Stamp owner tags for multi-character views (Orders tab). Always
	// populate — single-character requests just repeat the same identity
	// per row, and the frontend can still use it.
	for i := range result.Orders {
		if owner, ok := ownerByOrderID[result.Orders[i].OrderID]; ok {
			result.Orders[i].CharacterID = owner.characterID
			result.Orders[i].CharacterName = owner.characterName
		}
	}
	return result, nil
}

// authScopeStatusError maps authSessionsForScope's error to the status the
// handlers have always used for it.
func authScopeStatusError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "not logged in") {
		return statusError{http.StatusUnauthorized, err.Error()}
	}
	return statusError{http.StatusBadRequest, err.Error()}
}

func (s *Server) handleAuthOrderDesk(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)

	characterID, allScope, err := parseAuthScope(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}

	opt := orderDeskBuildOptions{
		SalesTaxPercent:  8.0,
		BrokerFeePercent: 1.0,
		TargetETADays:    3.0,
		MinMarginPercent: 3.0,
	}
	if cfg := s.loadConfigForUser(userID); cfg != nil {
		opt.SalesTaxPercent = cfg.SalesTaxPercent
	}
	q := r.URL.Query()
	parseFloatParam(q.Get("sales_tax"), 0, 100, &opt.SalesTaxPercent)
	parseFloatParam(q.Get("broker_fee"), 0, 100, &opt.BrokerFeePercent)
	parseFloatParamExclusiveMin(q.Get("target_eta_days"), 0, 60, &opt.TargetETADays)
	parseFloatParamExclusiveMin(q.Get("min_margin_pct"), 0, 100, &opt.MinMarginPercent)

	result, err := s.buildOrderDesk(r.Context(), userID, characterID, allScope, opt)
	if err != nil {
		writeStatusError(w, err)
		return
	}
	writeJSON(w, result)
}

// parseFloatParam assigns *dst only when raw parses to a real number inside
// [min, max]. NaN fails every comparison, so the explicit check is what stops
// the literal "NaN" — which strconv accepts — from reaching the response.
func parseFloatParam(raw string, min, max float64, dst *float64) {
	v, ok := parseBoundedFloat(raw, min, max, false)
	if ok {
		*dst = v
	}
}

// parseFloatParamExclusiveMin is the same with a strict lower bound, for the
// parameters where zero is not a legal value.
func parseFloatParamExclusiveMin(raw string, min, max float64, dst *float64) {
	v, ok := parseBoundedFloat(raw, min, max, true)
	if ok {
		*dst = v
	}
}

func parseBoundedFloat(raw string, min, max float64, exclusiveMin bool) (float64, bool) {
	if raw == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}
	// strconv accepts the literal "NaN", and every comparison against NaN
	// is false, so a range check alone would let it through into the
	// response. This is the bug writeJSON's encoder failure was hiding.
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	if exclusiveMin && !(f > min) {
		return 0, false
	}
	if !exclusiveMin && f < min {
		return 0, false
	}
	if f > max {
		return 0, false
	}
	return f, true
}
