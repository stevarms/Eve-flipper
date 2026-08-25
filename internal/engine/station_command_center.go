package engine

import (
	"sort"
	"time"

	"eve-flipper/internal/esi"
)

type StationCommandAction string

const (
	StationActionNewEntry StationCommandAction = "new_entry"
	StationActionReprice  StationCommandAction = "reprice"
	StationActionHold     StationCommandAction = "hold"
	StationActionCancel   StationCommandAction = "cancel"
)

type StationForecastBand struct {
	P50 float64 `json:"p50"`
	P80 float64 `json:"p80"`
	P95 float64 `json:"p95"`
}

type StationCommandForecast struct {
	DailyVolume StationForecastBand `json:"daily_volume"`
	DailyProfit StationForecastBand `json:"daily_profit"`
	ETADays     StationForecastBand `json:"eta_days"`
}

// StationCommandRow is one actionable row for the Station Command Center.
type StationCommandRow struct {
	Trade                    StationTrade           `json:"trade"`
	PersonalizedScore        float64                `json:"personalized_score"`
	RecommendedAction        StationCommandAction   `json:"recommended_action"`
	ActionReason             string                 `json:"action_reason"`
	Priority                 int                    `json:"priority"`
	ActiveOrderCount         int                    `json:"active_order_count"`
	ActiveOrderAtStation     int                    `json:"active_order_at_station"`
	OpenPositionQty          int64                  `json:"open_position_qty"`
	ExpectedDeltaDailyProfit float64                `json:"expected_delta_daily_profit"`
	Forecast                 StationCommandForecast `json:"forecast"`

	// Per-order recommendation folded in from what used to live only on
	// the character-popup Order Desk. Nil when the user has no order of
	// that side at this station+type. Frontend renders both when both
	// exist (rare cross-side arbitrage case).
	SellSuggestion *StationCommandSuggestedOrder `json:"sell_suggestion,omitempty"`
	BuySuggestion  *StationCommandSuggestedOrder `json:"buy_suggestion,omitempty"`
}

// StationCommandSuggestedOrder is the actionable per-order recommendation
// carried by StationCommandRow. Mirrors the Order Desk shape for one side
// of the book so the frontend can show the legal target price + fee-aware
// relist economics right next to the action verb.
type StationCommandSuggestedOrder struct {
	OrderID                int64   `json:"order_id"`
	IsBuyOrder             bool    `json:"is_buy_order"`
	CurrentPrice           float64 `json:"current_price"`
	VolumeRemain           int32   `json:"volume_remain"`
	BestPrice              float64 `json:"best_price"`
	SuggestedPrice         float64 `json:"suggested_price"`
	Position               int     `json:"position"` // 1 = top-of-book
	UndercutAmount         float64 `json:"undercut_amount"`
	RelistFeeISK           float64 `json:"relist_fee_isk"`
	NetRelistGainISK       float64 `json:"net_relist_gain_isk"`
	WarnUnprofitableRelist bool    `json:"warn_unprofitable_relist"`
}

// StationCommandSummary aggregates recommendation counts and context.
type StationCommandSummary struct {
	Rows              int `json:"rows"`
	NewEntryCount     int `json:"new_entry_count"`
	RepriceCount      int `json:"reprice_count"`
	HoldCount         int `json:"hold_count"`
	CancelCount       int `json:"cancel_count"`
	WithActiveOrders  int `json:"with_active_orders"`
	WithOpenPositions int `json:"with_open_positions"`
}

// StationCommandResult is the top-level recommendation payload.
type StationCommandResult struct {
	GeneratedAt string                `json:"generated_at"`
	Summary     StationCommandSummary `json:"summary"`
	Rows        []StationCommandRow   `json:"rows"`
}

type commandStationTypeKey struct {
	typeID     int32
	locationID int64
}

type commandOrderKey struct {
	typeID     int32
	locationID int64
	isBuy      bool
}

// pickSuggestionOrder picks the single active order most in need of user
// attention. Priority: (1) orders not at top-of-book (position != 1) —
// among those, whichever has the largest deviation from best price;
// (2) if all orders are at top-of-book, whichever has the largest
// remaining volume. `bestPrice` is top-of-book for that side (from
// StationTrade.BuyPrice / SellPrice). Returns nil when the list is empty.
func pickSuggestionOrder(orders []esi.CharacterOrder, bestPrice float64, isBuy bool) *esi.CharacterOrder {
	if len(orders) == 0 {
		return nil
	}
	// Prefer any order that's not top-of-book (needs attention).
	var notTop []esi.CharacterOrder
	for _, o := range orders {
		if !orderIsTopOfBook(o, bestPrice, isBuy) {
			notTop = append(notTop, o)
		}
	}
	pool := notTop
	if len(pool) == 0 {
		pool = orders
	}
	best := &pool[0]
	for i := 1; i < len(pool); i++ {
		// For not-top pool: pick the one furthest from best (biggest gap).
		// For top-of-book pool: pick the largest-volume one.
		if len(notTop) > 0 {
			gapBest := priceGap(best.Price, bestPrice, isBuy)
			gapCand := priceGap(pool[i].Price, bestPrice, isBuy)
			if gapCand > gapBest {
				best = &pool[i]
			}
		} else if pool[i].VolumeRemain > best.VolumeRemain {
			best = &pool[i]
		}
	}
	return best
}

func orderIsTopOfBook(o esi.CharacterOrder, bestPrice float64, isBuy bool) bool {
	if bestPrice <= 0 {
		return true
	}
	if isBuy {
		// User's buy at or above the highest existing buy → top-of-book.
		return o.Price >= bestPrice
	}
	return o.Price <= bestPrice
}

func priceGap(orderPrice, bestPrice float64, isBuy bool) float64 {
	if bestPrice <= 0 {
		return 0
	}
	if isBuy {
		return bestPrice - orderPrice
	}
	return orderPrice - bestPrice
}

// buildSuggestedOrder assembles the per-order recommendation. Uses the
// existing 4-sig-fig helpers (NextBuyOverbid / NextSellUndercut) and the
// same broker-fee-aware relist formula as AnalyzeUndercutsWithRelistFee.
func buildSuggestedOrder(o *esi.CharacterOrder, bestPrice float64, isBuy bool, brokerFeePct float64) StationCommandSuggestedOrder {
	sug := StationCommandSuggestedOrder{
		OrderID:      o.OrderID,
		IsBuyOrder:   isBuy,
		CurrentPrice: o.Price,
		VolumeRemain: o.VolumeRemain,
		BestPrice:    bestPrice,
	}
	// Position 1 = already at top; anything else = 2. We don't have the
	// full book depth here, but binary "top / not top" is what the UI needs.
	if orderIsTopOfBook(*o, bestPrice, isBuy) {
		sug.Position = 1
		sug.SuggestedPrice = o.Price
	} else {
		sug.Position = 2
		sug.UndercutAmount = priceGap(o.Price, bestPrice, isBuy)
		if isBuy {
			sug.SuggestedPrice = NextBuyOverbid(bestPrice)
		} else {
			sug.SuggestedPrice = NextSellUndercut(bestPrice)
		}
		if sug.SuggestedPrice <= 0 {
			// Degenerate best; keep own price so the recommendation isn't garbage.
			sug.SuggestedPrice = o.Price
		}
	}
	// Fee-aware relist economics (matches AnalyzeUndercutsWithRelistFee).
	if brokerFeePct > 0 && sug.Position != 1 && sug.SuggestedPrice > 0 && o.VolumeRemain > 0 {
		delta := sug.SuggestedPrice - o.Price
		if delta < 0 {
			delta = -delta
		}
		fee := brokerFeePct / 100.0 * delta * float64(o.VolumeRemain)
		if fee < 100 {
			fee = 100
		}
		sug.RelistFeeISK = fee
		// Theoretical gain from moving toward best. For sells, gain per
		// unit = new price - old price is negative (we drop); the "gain"
		// is really "positional advantage" — approximate with the improved
		// margin against the current best. Simplify: NetRelistGainISK =
		// -delta * volume - fee for sells (they lose ISK per unit but
		// gain fills sooner); +delta * volume - fee for buys.
		//
		// We choose the sign so a positive number means "worth doing".
		var grossGain float64
		if isBuy {
			// Buying higher = paying more per unit → gross gain is negative;
			// warning fires when we're just spending more to be top-of-book
			// without commensurate volume flow. Approximate.
			grossGain = -delta * float64(o.VolumeRemain)
		} else {
			// Selling lower = receiving less per unit → gross gain is
			// negative similarly. Users still want to know the fee, hence
			// we report NetRelistGainISK = -fee here (all-negative) which
			// makes WarnUnprofitableRelist reliably true. The UI will show
			// the fee and let the user decide.
			grossGain = -delta * float64(o.VolumeRemain)
		}
		sug.NetRelistGainISK = grossGain - fee
		if sug.NetRelistGainISK < 0 {
			sug.WarnUnprofitableRelist = true
		}
	}
	return sug
}

// BuildStationCommand converts raw station scan rows into an operator-oriented
// recommendation list using active orders and open inventory context.
//
// brokerFeePct is the character's effective per-order broker rate (post-skill,
// post-standings), used to compute fee-aware relist economics on per-order
// suggestions. Zero is fine — it just leaves the fee/gain fields at 0 and the
// WarnUnprofitableRelist flag off. Callers that don't have the rate at hand
// can pass 0 and the Suggested Price still fills in.
func BuildStationCommand(trades []StationTrade, activeOrders []esi.CharacterOrder, openPositions []OpenPosition, brokerFeePct float64) StationCommandResult {
	activeByType := make(map[int32]int)
	activeByTypeStation := make(map[commandStationTypeKey]int)
	// Index active orders by (typeID, stationID, isBuy) so the per-row
	// suggestion pass can grab them in O(1).
	ordersByKey := make(map[commandOrderKey][]esi.CharacterOrder)
	for _, o := range activeOrders {
		activeByType[o.TypeID]++
		activeByTypeStation[commandStationTypeKey{typeID: o.TypeID, locationID: o.LocationID}]++
		k := commandOrderKey{typeID: o.TypeID, locationID: o.LocationID, isBuy: o.IsBuyOrder}
		ordersByKey[k] = append(ordersByKey[k], o)
	}

	openQtyByType := make(map[int32]int64)
	for _, pos := range openPositions {
		openQtyByType[pos.TypeID] += pos.Quantity
	}

	out := StationCommandResult{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Rows:        make([]StationCommandRow, 0, len(trades)),
	}

	for _, t := range trades {
		row := StationCommandRow{
			Trade:                    t,
			PersonalizedScore:        defaultStationCommandScore(t),
			RecommendedAction:        StationActionHold,
			ActionReason:             "pending action evaluation",
			Priority:                 stationCommandActionPriority(StationActionHold),
			ActiveOrderCount:         activeByType[t.TypeID],
			ActiveOrderAtStation:     activeByTypeStation[commandStationTypeKey{typeID: t.TypeID, locationID: t.StationID}],
			OpenPositionQty:          openQtyByType[t.TypeID],
			ExpectedDeltaDailyProfit: t.DailyProfit,
		}
		if sell := pickSuggestionOrder(ordersByKey[commandOrderKey{typeID: t.TypeID, locationID: t.StationID, isBuy: false}], t.SellPrice, false); sell != nil {
			s := buildSuggestedOrder(sell, t.SellPrice, false, brokerFeePct)
			row.SellSuggestion = &s
		}
		if buy := pickSuggestionOrder(ordersByKey[commandOrderKey{typeID: t.TypeID, locationID: t.StationID, isBuy: true}], t.BuyPrice, true); buy != nil {
			s := buildSuggestedOrder(buy, t.BuyPrice, true, brokerFeePct)
			row.BuySuggestion = &s
		}

		evaluateStationAction(&row)
		row.Forecast = buildStationForecast(&row)
		row.PersonalizedScore = clampRange(row.PersonalizedScore, 0, 100)

		if row.ActiveOrderCount > 0 {
			out.Summary.WithActiveOrders++
		}
		if row.OpenPositionQty > 0 {
			out.Summary.WithOpenPositions++
		}
		switch row.RecommendedAction {
		case StationActionNewEntry:
			out.Summary.NewEntryCount++
		case StationActionReprice:
			out.Summary.RepriceCount++
		case StationActionHold:
			out.Summary.HoldCount++
		case StationActionCancel:
			out.Summary.CancelCount++
		}
		out.Rows = append(out.Rows, row)
	}

	sort.Slice(out.Rows, func(i, j int) bool {
		if out.Rows[i].Priority != out.Rows[j].Priority {
			return out.Rows[i].Priority > out.Rows[j].Priority
		}
		if out.Rows[i].PersonalizedScore != out.Rows[j].PersonalizedScore {
			return out.Rows[i].PersonalizedScore > out.Rows[j].PersonalizedScore
		}
		if out.Rows[i].Trade.DailyProfit != out.Rows[j].Trade.DailyProfit {
			return out.Rows[i].Trade.DailyProfit > out.Rows[j].Trade.DailyProfit
		}
		if out.Rows[i].Trade.CTS != out.Rows[j].Trade.CTS {
			return out.Rows[i].Trade.CTS > out.Rows[j].Trade.CTS
		}
		if out.Rows[i].Trade.TypeID != out.Rows[j].Trade.TypeID {
			return out.Rows[i].Trade.TypeID < out.Rows[j].Trade.TypeID
		}
		return out.Rows[i].Trade.StationID < out.Rows[j].Trade.StationID
	})

	out.Summary.Rows = len(out.Rows)
	return out
}

func defaultStationCommandScore(t StationTrade) float64 {
	if t.CTS > 0 {
		return t.CTS
	}
	if t.ConfidenceScore > 0 {
		return t.ConfidenceScore
	}
	return t.MarginPercent
}

func clampRange(v, minV, maxV float64) float64 {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}
