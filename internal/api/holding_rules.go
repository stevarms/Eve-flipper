package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"eve-flipper/internal/db"
	"eve-flipper/internal/engine"
	"eve-flipper/internal/esi"
)

// holding_rules.go — "hold this until it is worth X" and "these ones are not
// stock".
//
// The primary surface is Assets -> Positions, which is the tab that already
// asks "should I sell this today?". Today only reflects the answer: it drops
// the list action for a holding below its target and nets the reserved units
// out of every sell-side figure. Nothing here is editable from Today, which is
// a summary of the other tabs rather than a place capability lives.

// The hub these targets are quoted against, matching how Positions prices a
// holding in the first place. A target compared against a different market
// than the one the row is priced in would be a silent unit mismatch.
const holdingRuleRegionID = stockpilePriceRegionID

func (s *Server) handleAuthHoldingRules(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	rules := s.holdingRulesFor(userID)

	// A map keyed by a number is awkward to consume in TypeScript, and the
	// order of a Go map is deliberately unstable. Emit a sorted list.
	out := make([]db.HoldingRule, 0, len(rules))
	for _, rule := range rules {
		out = append(out, rule)
	}
	sortHoldingRules(out)
	writeJSON(w, map[string]any{"rules": out})
}

func (s *Server) handleAuthHoldingRuleSave(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	typeID, ok := holdingRuleTypeIDFromPath(w, r)
	if !ok {
		return
	}

	var body struct {
		TargetPrice      float64 `json:"target_price"`
		TargetPercentile float64 `json:"target_percentile"`
		TargetBasis      string  `json:"target_basis"`
		ReservedQty      int64   `json:"reserved_qty"`
		Note             string  `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	rule := db.HoldingRule{
		TypeID:           typeID,
		TargetPrice:      body.TargetPrice,
		TargetPercentile: body.TargetPercentile,
		TargetBasis:      body.TargetBasis,
		ReservedQty:      body.ReservedQty,
		Note:             body.Note,
		UpdatedAt:        time.Now().UTC().Format(time.RFC3339),
	}
	if s.db == nil {
		writeJSON(w, rule)
		return
	}
	if err := s.db.SetHoldingRule(userID, rule); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Read back rather than echo: SetHoldingRule sanitises, and deletes a rule
	// that constrains nothing. Echoing the request would tell the UI a target
	// was stored when it was cleared.
	writeJSON(w, s.holdingRulesFor(userID)[typeID])
}

func (s *Server) handleAuthHoldingRuleDelete(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	typeID, ok := holdingRuleTypeIDFromPath(w, r)
	if !ok {
		return
	}
	if s.db != nil {
		if err := s.db.DeleteHoldingRule(userID, typeID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// handleAuthHoldingRulePercentiles suggests targets from the item's own
// trailing year.
//
// The offer is a set of named percentiles rather than a continuous slider,
// because the distribution they are read from does not survive the response
// (see engine.PricePercentiles) and shipping a year of daily prices per item
// to make a slider smooth is not a trade worth making. Five choices spanning
// typical to near-peak is enough to express an intent.
func (s *Server) handleAuthHoldingRulePercentiles(w http.ResponseWriter, r *http.Request) {
	typeID, ok := holdingRuleTypeIDFromPath(w, r)
	if !ok {
		return
	}

	// A year is the question being asked; accept a little less rather than
	// refetch forever for an item ESI simply has less history for.
	history := s.marketHistoryFor(holdingRuleRegionID, typeID, 300)
	pct := engine.CalcPricePercentiles(history, 0, time.Now().UTC())

	writeJSON(w, map[string]any{
		"type_id":     typeID,
		"region_id":   holdingRuleRegionID,
		"percentiles": pct,
		// The choices the editor renders, in the order it renders them.
		"choices": []map[string]any{
			{"percentile": 25, "price": pct.P25, "label": "cheap"},
			{"percentile": 50, "price": pct.P50, "label": "typical"},
			{"percentile": 75, "price": pct.P75, "label": "strong"},
			{"percentile": 90, "price": pct.P90, "label": "rare"},
			{"percentile": 95, "price": pct.P95, "label": "peak"},
		},
	})
}

// marketHistoryFor reads a type's price history, preferring the cache.
//
// minSpanDays is what the caller needs to answer its question, and a cached
// series shorter than that is treated as a miss even when it is inside the
// freshness window. That is not belt-and-braces: the cache spent a long time
// capped at 90 days on write, so entries stored before that cap was lifted are
// fresh and useless for a question about a year. Refetching repairs them in
// place, one type at a time, as each is actually asked about.
//
// A refetch that fails falls back to whatever was cached. A short answer beats
// no answer, and the percentile gates refuse it downstream anyway.
func (s *Server) marketHistoryFor(regionID, typeID int32, minSpanDays int) []esi.HistoryEntry {
	var cached []esi.HistoryEntry
	if s.db != nil {
		if entries, ok := s.db.GetMarketHistory(regionID, typeID); ok {
			cached = entries
			if historySpanDays(entries) >= minSpanDays {
				return entries
			}
		}
	}
	if s.esi == nil {
		return cached
	}
	fresh, err := s.esi.FetchMarketHistory(regionID, typeID)
	if err != nil || len(fresh) == 0 {
		return cached
	}
	if s.db != nil {
		s.db.SetMarketHistory(regionID, typeID, fresh)
	}
	return fresh
}

// historySpanDays is the number of days between the oldest and newest entry.
// Span rather than count, because an illiquid item legitimately has gaps and
// counting rows would send us back to ESI for a series that is already as
// complete as it will ever be.
func historySpanDays(entries []esi.HistoryEntry) int {
	if len(entries) == 0 {
		return 0
	}
	oldest, newest := "", ""
	for _, e := range entries {
		if oldest == "" || e.Date < oldest {
			oldest = e.Date
		}
		if e.Date > newest {
			newest = e.Date
		}
	}
	from, err1 := time.Parse("2006-01-02", oldest)
	to, err2 := time.Parse("2006-01-02", newest)
	if err1 != nil || err2 != nil {
		return 0
	}
	return int(to.Sub(from).Hours() / 24)
}

func holdingRuleTypeIDFromPath(w http.ResponseWriter, r *http.Request) (int32, bool) {
	raw := r.PathValue("typeID")
	parsed, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || parsed <= 0 {
		writeError(w, http.StatusBadRequest, "invalid type id")
		return 0, false
	}
	return int32(parsed), true
}

func sortHoldingRules(rules []db.HoldingRule) {
	for i := 1; i < len(rules); i++ {
		for j := i; j > 0 && rules[j].TypeID < rules[j-1].TypeID; j-- {
			rules[j], rules[j-1] = rules[j-1], rules[j]
		}
	}
}
