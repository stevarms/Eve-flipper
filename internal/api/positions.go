package api

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"eve-flipper/internal/auth"
	"eve-flipper/internal/db"
	"eve-flipper/internal/engine"
)

// Positions answers one question per row: should I sell this today?
//
// Two sources feed it, and they are never merged:
//
//   - FIFO open positions derived from real ESI transactions by the trade
//     journal engine — everything bought (or manufactured) and not yet sold.
//   - Manual holdings the user typed in, for stock the transaction history
//     cannot see: loot, contract buys, corp hangar transfers, anything
//     acquired before the wallet history window.
//
// A type present in both appears as two rows. Averaging a guessed cost basis
// into a measured one would quietly corrupt the number the whole tab exists
// to show.

// PositionRow is one holding, priced.
type PositionRow struct {
	TypeID      int32   `json:"type_id"`
	TypeName    string  `json:"type_name"`
	Source      string  `json:"source"` // trade | manufacture | orphan | manual
	Qty         int64   `json:"qty"`
	AvgUnitCost float64 `json:"avg_unit_cost"`
	CostBasis   float64 `json:"cost_basis"`
	OldestDate  string  `json:"oldest_date,omitempty"`
	DaysHeld    int     `json:"days_held"`

	// Live pricing. MarketPrice is zero when the hub has no sell order for
	// the type, in which case the value/unrealized fields stay zero and the
	// UI shows "no price" rather than a fabricated loss.
	// Locations is where the holding physically sits, biggest pile first.
	// Empty means no asset coverage for this type -- a manual row, or a
	// character whose assets could not be read -- not an empty hangar.
	Locations []PositionLocation `json:"locations,omitempty"`

	MarketPrice   float64 `json:"market_price"`
	MarketValue   float64 `json:"market_value"`
	NetProceeds   float64 `json:"net_proceeds"`
	UnrealizedISK float64 `json:"unrealized_isk"`
	UnrealizedPct float64 `json:"unrealized_pct"`

	// Already on the book?
	ListedQty   int64   `json:"listed_qty"`
	ListedPrice float64 `json:"listed_price"`

	// Manual-entry fields, zero/empty for derived rows.
	ManualID int64  `json:"manual_id,omitempty"`
	Note     string `json:"note,omitempty"`

	// --- Holding rule (holding_rules, keyed by type) ---
	//
	// The two reasons a thing you own is not for sale today. Both apply to
	// derived and manual rows alike, which is why they are keyed by type
	// rather than carried on the position.

	// TargetPrice is the unit price at or above which you want to sell.
	// Zero means the holding trades normally.
	TargetPrice float64 `json:"target_price,omitempty"`
	// TargetPercentile is the trailing-year percentile the target came from,
	// zero when it was typed in. TargetBasis is "percentile" or "manual".
	TargetPercentile float64 `json:"target_percentile,omitempty"`
	TargetBasis      string  `json:"target_basis,omitempty"`
	// TargetMet is whether the market has reached the target. False with a
	// target set is the "waiting" state; there is no third option, so the UI
	// never has to infer it from a price comparison of its own.
	TargetMet bool `json:"target_met,omitempty"`
	// TargetProgressPct is how far the current price has come toward the
	// target, 0-100, for the progress readout. Zero when there is no target
	// or no price to compare against.
	TargetProgressPct float64 `json:"target_progress_pct,omitempty"`

	// ReservedQty is units held back from trading entirely -- the ships you
	// actually fly. TradeableQty is Qty less that, and is what every
	// sell-side figure on this row is computed from.
	ReservedQty  int64 `json:"reserved_qty,omitempty"`
	TradeableQty int64 `json:"tradeable_qty"`

	// RuleNote is the holding rule's note, distinct from Note above, which
	// belongs to a manual entry.
	RuleNote string `json:"rule_note,omitempty"`
}

// PositionsResponse wraps the rows with the caveats the header has to state.
type PositionsResponse struct {
	Rows             []PositionRow `json:"rows"`
	PricingFailed    bool          `json:"pricing_failed"`
	OrdersFailed     bool          `json:"orders_failed"`
	// AssetsFailed means at least one hangar could not be read, so a row with
	// no locations proves nothing. Without this flag a missing scope and
	// genuinely missing stock look identical.
	AssetsFailed bool `json:"assets_failed"`
	SalesTaxPercent  float64       `json:"sales_tax_percent"`
	BrokerFeePercent float64       `json:"broker_fee_percent"`
	TotalCostBasis   float64       `json:"total_cost_basis"`
	TotalMarketValue float64       `json:"total_market_value"`
	TotalUnrealized  float64       `json:"total_unrealized"`
	GeneratedAt      string        `json:"generated_at"`
}

// positionFees resolves the sell-side fee pair — the shared profile
// (fee_profile.go) overridden by query params, the same convention
// handleAuthOrderDesk uses.
//
// It resolves against a character rather than reading config directly so that
// unrealized P&L here is charged the same rates the trade journal charges the
// realized side. Reading config alone left this tab quoting a fee snapshot the
// character had long since trained out of.
func (s *Server) positionFees(userID string, characterID int64, r *http.Request) (salesTax, brokerFee float64) {
	return s.positionFeesWith(userID, characterID, positionFeeOverrideFromQuery(r))
}

// positionFeeOverride is the query-parameter override expressed without an
// http.Request, so the Today builder can reuse fee resolution. A nil field
// means "no override" — distinct from an override of zero, which is a legal
// rate at a citadel with no broker fee.
type positionFeeOverride struct {
	SalesTax  *float64
	BrokerFee *float64
}

func positionFeeOverrideFromQuery(r *http.Request) positionFeeOverride {
	var out positionFeeOverride
	if r == nil {
		return out
	}
	if v := r.URL.Query().Get("sales_tax"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 100 {
			out.SalesTax = &f
		}
	}
	if v := r.URL.Query().Get("broker_fee"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 100 {
			out.BrokerFee = &f
		}
	}
	return out
}

func (s *Server) positionFeesWith(userID string, characterID int64, over positionFeeOverride) (salesTax, brokerFee float64) {
	profile := s.resolveFeeProfile(userID, characterID)
	salesTax, brokerFee = profile.SalesTaxPercent, profile.BrokerFeePercent
	if over.SalesTax != nil {
		salesTax = *over.SalesTax
	}
	if over.BrokerFee != nil {
		brokerFee = *over.BrokerFee
	}
	return salesTax, brokerFee
}

// positionScopeFilter translates the character-scope convention used by the
// rail's scope picker (character_id=<id> / scope=all) into the wallet filter
// the journal archive speaks.
func positionScopeFilter(characterID int64, all bool, sessionCharID int64) *db.WalletScopeFilter {
	if all || (characterID <= 0 && sessionCharID <= 0) {
		return &db.WalletScopeFilter{IncludeAll: true}
	}
	id := characterID
	if id <= 0 {
		id = sessionCharID
	}
	return &db.WalletScopeFilter{IncludeCharacters: []int64{id}}
}

// collapsePositionsByType folds the engine's per-station open positions back
// into one row per type + source.
//
// The engine splits trade lots by station because the P&L projection needs a
// per-station cost basis. This tab has no station column, so two rows for the
// same item would read as a duplicate rather than as two locations. Adding that
// column is the better answer; until then, collapse.
func collapsePositionsByType(in []engine.JournalOpenPosition) []engine.JournalOpenPosition {
	type key struct {
		typeID int32
		source engine.LotSource
	}
	order := make([]key, 0, len(in))
	agg := make(map[key]*engine.JournalOpenPosition, len(in))
	for _, p := range in {
		if p.Qty <= 0 {
			continue
		}
		k := key{typeID: p.TypeID, source: p.Source}
		cur, ok := agg[k]
		if !ok {
			merged := p
			// Location is meaningless once stations are merged; leaving a
			// single station's id here would mislabel the whole row.
			merged.LocationID = 0
			merged.LocationName = ""
			agg[k] = &merged
			order = append(order, k)
			continue
		}
		cur.Qty += p.Qty
		cur.CostBasis += p.CostBasis
		if p.OldestDate != "" && (cur.OldestDate == "" || p.OldestDate < cur.OldestDate) {
			cur.OldestDate = p.OldestDate
		}
	}
	out := make([]engine.JournalOpenPosition, 0, len(order))
	for _, k := range order {
		p := agg[k]
		if p.Qty > 0 {
			p.AvgUnitCost = p.CostBasis / float64(p.Qty)
		}
		out = append(out, *p)
	}
	return out
}

func (s *Server) handleAuthPositions(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "not logged in")
		return
	}

	characterID, allScope, err := parseAuthScope(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := s.buildPositions(userID, characterID, allScope, positionFeeOverrideFromQuery(r))
	if err != nil {
		writeStatusError(w, err)
		return
	}
	writeJSON(w, resp)
}

// buildPositions is the positions payload without the HTTP wrapper, so the
// Today work order can ask "what am I holding, and is it in profit" without
// going back out through the network.
func (s *Server) buildPositions(
	userID string,
	characterID int64,
	allScope bool,
	feeOverride positionFeeOverride,
) (PositionsResponse, error) {
	// Manual rows are readable with no EVE session at all — that is the
	// whole point of them, and it keeps the tab useful before SSO.
	manual := []db.ManualPosition{}
	if s.db != nil {
		manual = s.db.GetManualPositionsForUser(userID)
	}

	// Derived rows need a session. Absent one, the manual rows still render.
	var derived []engine.JournalOpenPosition
	var sessionCharID int64
	sessions, sessErr := s.authSessionsForScope(userID, characterID, allScope, true)
	if sessErr == nil && len(sessions) > 0 {
		sessionCharID = sessions[0].CharacterID
		filter := positionScopeFilter(characterID, allScope, sessionCharID)
		result, jErr := s.loadTradeJournalResultFor(userID, filter, time.Time{}, engine.FIFOModeStrictDate, journalFeeRates{})
		if jErr != nil {
			log.Printf("[POSITIONS] journal compute: %v", jErr)
		} else if result != nil {
			derived = collapsePositionsByType(result.OpenPositions)
		}
	}

	// After the session lookup: the fee profile needs a character to read
	// skills from, and sessionCharID is the one whose positions these are.
	feeCharID := characterID
	if feeCharID <= 0 {
		feeCharID = sessionCharID
	}
	salesTax, brokerFee := s.positionFeesWith(userID, feeCharID, feeOverride)

	rows := make([]PositionRow, 0, len(derived)+len(manual))
	for _, p := range derived {
		if p.Qty <= 0 {
			continue
		}
		rows = append(rows, PositionRow{
			TypeID:      p.TypeID,
			TypeName:    p.TypeName,
			Source:      string(p.Source),
			Qty:         p.Qty,
			AvgUnitCost: p.AvgUnitCost,
			CostBasis:   p.CostBasis,
			OldestDate:  p.OldestDate,
		})
	}
	for _, m := range manual {
		rows = append(rows, PositionRow{
			TypeID:      m.TypeID,
			TypeName:    m.TypeName,
			Source:      "manual",
			Qty:         m.Quantity,
			AvgUnitCost: m.UnitCost,
			CostBasis:   m.UnitCost * float64(m.Quantity),
			OldestDate:  m.AcquiredAt,
			ManualID:    m.ID,
			TargetPrice: m.TargetPrice,
			Note:        m.Note,
		})
	}

	if len(rows) == 0 {
		return PositionsResponse{
			Rows:             []PositionRow{},
			SalesTaxPercent:  salesTax,
			BrokerFeePercent: brokerFee,
			GeneratedAt:      time.Now().UTC().Format(time.RFC3339),
		}, nil
	}

	s.fillPositionNames(rows)

	typeIDs := make([]int32, 0, len(rows))
	seen := make(map[int32]bool, len(rows))
	for _, row := range rows {
		if row.TypeID > 0 && !seen[row.TypeID] {
			seen[row.TypeID] = true
			typeIDs = append(typeIDs, row.TypeID)
		}
	}
	prices, pricingFailed := s.fetchHubSellPrices(typeIDs)
	listed, listedPrice, ordersFailed := s.fetchListedSellQuantities(userID, sessions, seen)
	s.mu.RLock()
	sdeForAssets := s.sdeData
	s.mu.RUnlock()
	locations, assetsComplete := s.buildPositionLocations(userID, sessions, sdeForAssets, seen)

	now := time.Now().UTC()
	resp := PositionsResponse{
		PricingFailed:    pricingFailed,
		OrdersFailed:     ordersFailed,
		// No sessions at all is not an asset failure -- manual-only rows are a
		// supported state, and flagging it would put a warning on a page that
		// is working exactly as intended.
		AssetsFailed: len(sessions) > 0 && !assetsComplete,
		SalesTaxPercent:  salesTax,
		BrokerFeePercent: brokerFee,
		GeneratedAt:      now.Format(time.RFC3339),
	}

	keepRate := 1.0 - (salesTax+brokerFee)/100.0
	if keepRate < 0 {
		keepRate = 0
	}
	for i := range rows {
		row := &rows[i]
		row.DaysHeld = daysSince(row.OldestDate, now)
		row.ListedQty = listed[row.TypeID]
		row.Locations = locations[row.TypeID]
		row.ListedPrice = listedPrice[row.TypeID]
		if px, ok := prices[row.TypeID]; ok && px > 0 {
			row.MarketPrice = px
			row.MarketValue = px * float64(row.Qty)
			// A position is not in profit until it clears the sell-side
			// broker fee and sales tax, so unrealized is stated net.
			row.NetProceeds = row.MarketValue * keepRate
			row.UnrealizedISK = row.NetProceeds - row.CostBasis
			if row.CostBasis > 0 {
				row.UnrealizedPct = row.UnrealizedISK / row.CostBasis * 100
			}
		}
		resp.TotalCostBasis += row.CostBasis
		resp.TotalMarketValue += row.MarketValue
		resp.TotalUnrealized += row.UnrealizedISK
	}

	applyHoldingRules(rows, s.holdingRulesFor(userID))

	// Biggest unrealized swing first — the rows worth a decision today.
	sort.SliceStable(rows, func(i, j int) bool {
		return math.Abs(rows[i].UnrealizedISK) > math.Abs(rows[j].UnrealizedISK)
	})
	resp.Rows = rows
	return resp, nil
}

// fillPositionNames backfills type names from the SDE. FIFO rows usually
// carry one already; manual rows only do if the client resolved the name.
func (s *Server) fillPositionNames(rows []PositionRow) {
	s.mu.RLock()
	sdeData := s.sdeData
	s.mu.RUnlock()
	if sdeData == nil {
		return
	}
	for i := range rows {
		if rows[i].TypeName != "" {
			continue
		}
		if t, ok := sdeData.Types[rows[i].TypeID]; ok {
			rows[i].TypeName = t.Name
		}
	}
}

// fetchListedSellQuantities sums the user's open sell orders per type so a row
// can say "already listed ×N" instead of prompting a duplicate listing.
// Returns (qty, best listed price, failed) and never errors the request —
// a missing orders fetch degrades the column, not the page.
func (s *Server) fetchListedSellQuantities(userID string, sessions []*auth.Session, wanted map[int32]bool) (map[int32]int64, map[int32]float64, bool) {
	qty := map[int32]int64{}
	px := map[int32]float64{}
	if len(sessions) == 0 || s.esi == nil || s.sessions == nil {
		return qty, px, false
	}
	failed := false
	for _, sess := range sessions {
		token, err := s.sessions.EnsureValidTokenForUserCharacter(s.sso, userID, sess.CharacterID)
		if err != nil {
			failed = true
			continue
		}
		orders, err := s.esi.GetCharacterOrders(sess.CharacterID, token)
		if err != nil {
			failed = true
			continue
		}
		for _, o := range orders {
			if o.IsBuyOrder || !wanted[o.TypeID] {
				continue
			}
			qty[o.TypeID] += int64(o.VolumeRemain)
			if cur, ok := px[o.TypeID]; !ok || o.Price < cur {
				px[o.TypeID] = o.Price
			}
		}
	}
	return qty, px, failed
}

// daysSince parses the loosely-typed date strings both sources produce
// (RFC3339 from ESI, plain dates from hand entry) and returns whole days held.
func daysSince(date string, now time.Time) int {
	date = strings.TrimSpace(date)
	if date == "" {
		return 0
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, date); err == nil {
			d := int(now.Sub(t).Hours() / 24)
			if d < 0 {
				return 0
			}
			return d
		}
	}
	return 0
}

func (s *Server) handleAuthPositionSave(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	if userID == "" || s.db == nil {
		writeError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	var body db.ManualPosition
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if body.TypeName == "" {
		s.mu.RLock()
		sdeData := s.sdeData
		s.mu.RUnlock()
		if sdeData != nil {
			if t, ok := sdeData.Types[body.TypeID]; ok {
				body.TypeName = t.Name
			}
		}
	}
	saved, err := s.db.SaveManualPositionForUser(userID, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, saved)
}

func (s *Server) handleAuthPositionDelete(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	if userID == "" || s.db == nil {
		writeError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid position id %q", r.PathValue("id")))
		return
	}
	if err := s.db.DeleteManualPositionForUser(userID, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// holdingRulesFor reads the user's per-type selling constraints.
func (s *Server) holdingRulesFor(userID string) map[int32]db.HoldingRule {
	if s.db == nil {
		return map[int32]db.HoldingRule{}
	}
	return s.db.GetHoldingRules(userID)
}

// applyHoldingRules stamps each row with its rule and derives the two figures
// that depend on it.
//
// TradeableQty is always set, rule or not, so every consumer can read one
// field instead of remembering to subtract. That matters because the
// subtraction is the whole point: a position of six Gnosis with two reserved
// is a position of four as far as selling is concerned, and a caller that
// forgets would quietly advise selling the ships being flown.
func applyHoldingRules(rows []PositionRow, rules map[int32]db.HoldingRule) {
	for i := range rows {
		row := &rows[i]
		row.TradeableQty = row.Qty

		rule, ok := rules[row.TypeID]
		if !ok {
			continue
		}

		if rule.ReservedQty > 0 {
			row.ReservedQty = rule.ReservedQty
			row.TradeableQty = row.Qty - rule.ReservedQty
			if row.TradeableQty < 0 {
				// Reserved more than is held: the position shrank since the
				// rule was set. Nothing is for sale, which is the safe read.
				row.TradeableQty = 0
			}
		}

		row.RuleNote = rule.Note
		if rule.TargetPrice <= 0 {
			continue
		}
		row.TargetPrice = rule.TargetPrice
		row.TargetPercentile = rule.TargetPercentile
		row.TargetBasis = rule.TargetBasis
		if row.MarketPrice > 0 {
			row.TargetMet = row.MarketPrice >= rule.TargetPrice
			row.TargetProgressPct = clampPercent(row.MarketPrice / rule.TargetPrice * 100)
		}
	}
}

func clampPercent(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
