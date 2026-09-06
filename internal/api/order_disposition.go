package api

import (
	"context"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"eve-flipper/internal/auth"
	"eve-flipper/internal/config"
	"eve-flipper/internal/db"
	"eve-flipper/internal/engine"
	"eve-flipper/internal/esi"
	"eve-flipper/internal/sde"
)

// The four canonical hub stations. A hub region is only worth pricing at
// the station everybody actually trades at — the rest of Domain is not an
// alternative to Jita, Amarr is.
var dispositionHubs = []struct {
	StationID int64
	RegionID  int32
}{
	{60003760, 10000002}, // Jita IV-4, The Forge
	{60008494, 10000043}, // Amarr VIII, Domain
	{60011866, 10000032}, // Dodixie IX-20, Sinq Laison
	{60004588, 10000030}, // Rens VI-8, Heimatar
}

// How many of the home region's other stations to look at before giving
// up. Ranked by depth, so the ones past this are backwaters with a single
// hopeful order; the engine still only keeps the best venue overall.
const dispositionLocalCandidates = 6

// handleAuthOrderDisposition prices cutting, holding and moving one
// underwater sell order.
//
// It is a separate on-demand endpoint rather than part of the desk load
// because it costs up to four extra region fetches plus a full FIFO
// journal pass, and only a handful of rows are ever underwater. The desk
// keeps its latency; you pay for the rows you actually ask about.
func (s *Server) handleAuthOrderDisposition(w http.ResponseWriter, r *http.Request) {
	if !s.isReady() {
		writeError(w, 503, "SDE still loading")
		return
	}
	userID := userIDFromRequest(r)

	orderID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("order_id")), 10, 64)
	if err != nil || orderID <= 0 {
		writeError(w, 400, "order_id is required")
		return
	}

	characterID, allScope, err := parseAuthScope(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	sessions, err := s.authSessionsForScope(userID, characterID, allScope, true)
	if err != nil {
		if strings.Contains(err.Error(), "not logged in") {
			writeError(w, 401, err.Error())
		} else {
			writeError(w, 400, err.Error())
		}
		return
	}

	order, found := s.findCharacterOrder(userID, sessions, orderID)
	if !found {
		writeError(w, 404, "order not found — it may have filled or expired since the desk loaded")
		return
	}
	if order.IsBuyOrder {
		// A buy order's ISK is not committed, so there is nothing to
		// dispose of and `cancel` was already the whole answer.
		writeError(w, 404, "disposition applies to sell orders only")
		return
	}

	cfg := s.loadConfigForUser(userID)
	in := engine.DispositionInput{
		TypeID:           order.TypeID,
		Qty:              int64(order.VolumeRemain),
		SalesTaxPercent:  dispositionQueryFloat(r, "sales_tax", dispositionConfigSalesTax(cfg), 0, 100),
		BrokerFeePercent: dispositionQueryFloat(r, "broker_fee", 1, 0, 100),
		TargetETADays:    dispositionQueryFloat(r, "target_eta_days", 3, 0, 60),
		MinMarginPercent: dispositionQueryFloat(r, "min_margin_pct", 3, 0, 100),
	}
	if cfg != nil {
		in.ShipRateISKPerM3Jump = cfg.ShippingCostPerM3Jump
	}

	s.mu.RLock()
	sdeData := s.sdeData
	s.mu.RUnlock()
	if sdeData == nil {
		writeError(w, 503, "SDE still loading")
		return
	}
	if t, ok := sdeData.Types[order.TypeID]; ok {
		in.TypeName = t.Name
		in.UnitVolumeM3 = t.Volume
	}

	// The FIFO journal is the only thing that knows what this stock cost,
	// and every plan below is quoted against that. Run it alongside the
	// market fetches rather than before them.
	var basisWG sync.WaitGroup
	basisWG.Add(1)
	go func() {
		defer basisWG.Done()
		in.CostBasisISK, in.HeldSince = s.dispositionCostBasis(userID, order.TypeID)
	}()

	home, elsewhere := s.dispositionVenues(r.Context(), sdeData, cfg, order)
	in.Home = home
	in.Elsewhere = elsewhere

	basisWG.Wait()
	writeJSON(w, engine.ComputeOrderDisposition(in))
}

// findCharacterOrder locates one of the user's live orders across whichever
// characters the request is scoped to. A token or fetch failure on one
// character is logged and skipped rather than failing the lookup: the order
// may well belong to a character that answered.
func (s *Server) findCharacterOrder(userID string, sessions []*auth.Session, orderID int64) (esi.CharacterOrder, bool) {
	for _, sess := range sessions {
		token, err := s.sessions.EnsureValidTokenForUserCharacter(s.sso, userID, sess.CharacterID)
		if err != nil {
			log.Printf("[AUTH] Disposition token error (%s): %v", sess.CharacterName, err)
			continue
		}
		orders, err := s.esi.GetCharacterOrders(sess.CharacterID, token)
		if err != nil {
			log.Printf("[AUTH] Disposition orders error (%s): %v", sess.CharacterName, err)
			continue
		}
		for _, o := range orders {
			if o.OrderID == orderID {
				return o, true
			}
		}
	}
	return esi.CharacterOrder{}, false
}

// dispositionCostBasis returns the quantity-weighted average unit cost of
// the stock held in this type and the date of the oldest lot still open.
// Zero cost means the archive has no lots to price it against, which the
// engine reports as a refusal rather than as free stock.
func (s *Server) dispositionCostBasis(userID string, typeID int32) (float64, string) {
	if s == nil || s.db == nil || strings.TrimSpace(userID) == "" {
		return 0, ""
	}
	result, err := s.tradeJournalResultFor(
		userID,
		&db.WalletScopeFilter{IncludeAll: true},
		time.Time{},
		engine.FIFOModeStrictDate,
	)
	if err != nil || result == nil {
		if err != nil {
			log.Printf("[AUTH] Disposition cost basis unavailable: %v", err)
		}
		return 0, ""
	}

	return dispositionPositionBasis(result.OpenPositions, typeID)
}

// dispositionPositionBasis blends one type's open positions down to an
// average unit cost and the date of its oldest surviving lot.
//
// A type shows up once per pool — bought stock and built stock are tracked
// separately — so the pools are blended by quantity rather than averaged,
// exactly as the desk's margin column does. A zero or negative cost is a
// gap in the archive, not free stock, and is left out entirely.
func dispositionPositionBasis(positions []engine.JournalOpenPosition, typeID int32) (float64, string) {
	var qty int64
	var cost float64
	oldest := ""
	for _, p := range positions {
		if p.TypeID != typeID || p.Qty <= 0 || p.AvgUnitCost <= 0 {
			continue
		}
		qty += p.Qty
		cost += p.AvgUnitCost * float64(p.Qty)
		// ISO dates, so lexical order is chronological order.
		if p.OldestDate != "" && (oldest == "" || p.OldestDate < oldest) {
			oldest = p.OldestDate
		}
	}
	if qty <= 0 {
		return 0, ""
	}
	return cost / float64(qty), oldest
}

// dispositionVenues builds the order's own station plus every alternative
// worth pricing: the deepest other stations in its region, which the region
// book already covers for free, and the canonical hub stations, which cost
// one fetch each.
//
// Best-effort throughout. A hub that fails to fetch is simply not offered;
// a venue whose route cannot be resolved is passed through with Jumps = -1
// so the engine reports it as skipped instead of pricing a guessed haul.
func (s *Server) dispositionVenues(ctx context.Context, sdeData *sde.Data, cfg *config.Config, order esi.CharacterOrder) (engine.DispositionVenue, []engine.DispositionVenue) {
	minSec := 0.0
	if cfg != nil {
		minSec = cfg.MinRouteSecurity
	}
	originSystem, originOK := s.dispositionSystemID(sdeData, order.LocationID)

	regions := map[int32]bool{order.RegionID: true}
	for _, hub := range dispositionHubs {
		regions[hub.RegionID] = true
	}

	type regionBook struct {
		orders  []esi.MarketOrder
		history []esi.HistoryEntry
		ok      bool
	}
	books := make(map[int32]regionBook, len(regions))

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)
	for regionID := range regions {
		wg.Add(1)
		go func(rid int32) {
			defer wg.Done()

			sem <- struct{}{}
			orders, err := s.esi.FetchRegionOrdersByTypeContext(ctx, rid, order.TypeID)
			<-sem
			if err != nil {
				log.Printf("[AUTH] Disposition book unavailable (region %d type %d): %v", rid, order.TypeID, err)
				return
			}

			var entries []esi.HistoryEntry
			var cached bool
			if s.db != nil {
				entries, cached = s.db.GetMarketHistory(rid, order.TypeID)
			}
			if !cached {
				if fresh, histErr := s.esi.FetchMarketHistory(rid, order.TypeID); histErr == nil {
					entries = fresh
					if s.db != nil && len(entries) > 0 {
						s.db.SetMarketHistory(rid, order.TypeID, entries)
					}
				}
			}

			mu.Lock()
			books[rid] = regionBook{orders: orders, history: entries, ok: true}
			mu.Unlock()
		}(regionID)
	}
	wg.Wait()

	homeBook := books[order.RegionID]
	home := engine.DispositionVenue{
		LocationID:   order.LocationID,
		LocationName: s.esi.StationName(order.LocationID),
		RegionID:     order.RegionID,
		RegionOrders: homeBook.orders,
		History:      homeBook.history,
	}

	// Resolving a route needs both ends. Without the origin we can still
	// price cutting and holding, so the panel degrades to two options
	// rather than to an error.
	jumpsTo := func(locationID int64) int {
		if !originOK {
			return -1
		}
		dest, ok := s.dispositionSystemID(sdeData, locationID)
		if !ok {
			return -1
		}
		if dest == originSystem {
			return 0
		}
		return sdeData.Universe.ShortestPathMinSecurity(originSystem, dest, minSec)
	}

	var elsewhere []engine.DispositionVenue
	seen := map[int64]bool{order.LocationID: true}

	// Same region first: the book is already in hand, so these cost nothing
	// beyond a route lookup, and from a hub the nearby structures are often
	// the only realistic move.
	resolved := 0
	for _, locationID := range dispositionDeepestStations(homeBook.orders, order.LocationID) {
		if resolved >= engine.DispositionMaxLocalVenues {
			break
		}
		seen[locationID] = true
		jumps := jumpsTo(locationID)
		if jumps >= 0 {
			resolved++
		}
		elsewhere = append(elsewhere, engine.DispositionVenue{
			LocationID:   locationID,
			LocationName: s.esi.StationName(locationID),
			RegionID:     order.RegionID,
			Jumps:        jumps,
			RegionOrders: homeBook.orders,
			History:      homeBook.history,
		})
	}

	for _, hub := range dispositionHubs {
		if seen[hub.StationID] {
			continue
		}
		book, ok := books[hub.RegionID]
		if !ok || !book.ok {
			continue
		}
		seen[hub.StationID] = true
		elsewhere = append(elsewhere, engine.DispositionVenue{
			LocationID:   hub.StationID,
			LocationName: s.esi.StationName(hub.StationID),
			RegionID:     hub.RegionID,
			Jumps:        jumpsTo(hub.StationID),
			RegionOrders: book.orders,
			History:      book.history,
		})
	}

	return home, elsewhere
}

// dispositionSystemID resolves a market location to its solar system.
// Upwell structures are not in the SDE, so they fall back to the system ids
// ESI has already handed us for structures we can see. Unknown means
// unknown — the caller drops the venue rather than assuming a distance.
func (s *Server) dispositionSystemID(sdeData *sde.Data, locationID int64) (int32, bool) {
	if sdeData != nil {
		if st, ok := sdeData.Stations[locationID]; ok && st.SystemID != 0 {
			return st.SystemID, true
		}
	}
	if s.esi != nil {
		if sysID, ok := s.esi.StructureSystemID(locationID); ok && sysID != 0 {
			return sysID, true
		}
	}
	return 0, false
}

// dispositionDeepestStations ranks the other stations trading this type in
// a region by how much stock is standing on their books, deepest first.
// Depth is a proxy for "somebody actually trades here", which is what
// separates a real alternative from one optimistic order.
func dispositionDeepestStations(regionOrders []esi.MarketOrder, exclude int64) []int64 {
	depth := make(map[int64]int64)
	for _, o := range regionOrders {
		if o.LocationID == exclude || o.VolumeRemain <= 0 {
			continue
		}
		depth[o.LocationID] += int64(o.VolumeRemain)
	}
	out := make([]int64, 0, len(depth))
	for locationID := range depth {
		out = append(out, locationID)
	}
	sort.Slice(out, func(i, j int) bool {
		if depth[out[i]] != depth[out[j]] {
			return depth[out[i]] > depth[out[j]]
		}
		return out[i] < out[j] // stable across requests
	})
	if len(out) > dispositionLocalCandidates {
		out = out[:dispositionLocalCandidates]
	}
	return out
}

// dispositionQueryFloat reads a bounded numeric override, falling back to
// the default whenever the value is missing or out of range.
func dispositionQueryFloat(r *http.Request, key string, def, min, max float64) float64 {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < min || f > max {
		return def
	}
	return f
}

func dispositionConfigSalesTax(cfg *config.Config) float64 {
	if cfg != nil && cfg.SalesTaxPercent > 0 {
		return cfg.SalesTaxPercent
	}
	return 8
}
