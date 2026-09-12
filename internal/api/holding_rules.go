package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"eve-flipper/internal/db"
	"eve-flipper/internal/engine"
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

	pct := s.pricePercentilesFor(holdingRuleRegionID, typeID)

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

// pricePercentilesFor answers "where does today's price sit in this item's
// year", looking the year up rather than storing it.
//
// The raw history cache deliberately holds only 90 days, because the blueprint
// scanner keeps every scanned type's series in memory at once and series
// length multiplies by type count. So this does not read that cache at all.
// It reads a summary cache of about a dozen derived floats; on a miss it pulls
// the full ~390-day series straight from ESI, reduces it, stores the summary,
// and lets the series go out of scope. Roughly 250 bytes kept instead of
// roughly 32 KB, and nothing large is retained between calls.
//
// A refusal ("too little traded history") is cached like any other answer —
// it costs the same round trip to rediscover and is just as true tomorrow.
func (s *Server) pricePercentilesFor(regionID, typeID int32) engine.PricePercentiles {
	if s.db != nil {
		if payload, ok := s.db.GetPricePercentiles(regionID, typeID); ok {
			var cached engine.PricePercentiles
			if err := json.Unmarshal([]byte(payload), &cached); err == nil {
				return cached
			}
		}
	}
	if s.esi == nil {
		return engine.PricePercentiles{Basis: engine.PercentileBasisNone, Reason: "no market client"}
	}

	history, err := s.esi.FetchMarketHistory(regionID, typeID)
	if err != nil {
		// Not cached: a transient ESI failure is not a fact about the item,
		// and storing it would suppress the real answer for a day.
		return engine.PricePercentiles{Basis: engine.PercentileBasisNone, Reason: "price history unavailable"}
	}

	pct := engine.CalcPricePercentiles(history, 0, time.Now().UTC())
	if s.db != nil {
		if payload, marshalErr := json.Marshal(pct); marshalErr == nil {
			if setErr := s.db.SetPricePercentiles(regionID, typeID, string(payload)); setErr != nil {
				log.Printf("[PERCENTILE] cache write %d/%d: %v", regionID, typeID, setErr)
			}
		}
	}
	return pct
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
