package api

import (
	"log"
	"net/http"
	"sort"
	"strings"

	"eve-flipper/internal/esi"
)

// GET /api/auth/orders/history — closed orders (filled, cancelled, expired)
// across the requested character scope.
//
// This history was previously reachable only as one field of the very
// expensive /api/auth/character payload, which also fetches assets,
// transactions, skills and a risk rollup. The Orders tab needs the history and
// nothing else, so it gets its own endpoint rather than paying for all of that
// to render one sub-tab.
func (s *Server) handleAuthOrderHistory(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)

	characterID, allScope, err := parseAuthScope(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sessions, err := s.authSessionsForScope(userID, characterID, allScope, true)
	if err != nil {
		if strings.Contains(err.Error(), "not logged in") {
			writeError(w, http.StatusUnauthorized, err.Error())
		} else {
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}

	orders := []esi.HistoricalOrder{}
	for _, sess := range sessions {
		token, tokenErr := s.sessions.EnsureValidTokenForUserCharacter(s.sso, userID, sess.CharacterID)
		if tokenErr != nil {
			log.Printf("[AUTH] OrderHistory token error (%s): %v", sess.CharacterName, tokenErr)
			if !allScope {
				writeError(w, http.StatusUnauthorized, tokenErr.Error())
				return
			}
			continue
		}
		part, fetchErr := s.esi.GetOrderHistory(sess.CharacterID, token)
		if fetchErr != nil {
			log.Printf("[AUTH] OrderHistory fetch error (%s): %v", sess.CharacterName, fetchErr)
			if !allScope {
				writeError(w, http.StatusInternalServerError, "failed to fetch order history: "+fetchErr.Error())
				return
			}
			continue
		}
		orders = append(orders, part...)
	}

	// Names, so the grid reads like EVE rather than like a database.
	s.mu.RLock()
	sdeData := s.sdeData
	s.mu.RUnlock()
	if sdeData != nil && len(orders) > 0 {
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

	// Newest first — the only useful default for a history list.
	sort.SliceStable(orders, func(i, j int) bool { return orders[i].Issued > orders[j].Issued })

	writeJSON(w, map[string]any{"orders": orders})
}
