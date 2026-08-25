package engine

import (
	"sort"

	"eve-flipper/internal/esi"
)

// UndercutStatus describes how a player's order compares to the market.
type UndercutStatus struct {
	OrderID        int64   `json:"order_id"`
	Position       int     `json:"position"`        // 1 = best, 2+ = undercut
	TotalOrders    int     `json:"total_orders"`    // total orders on this side at this location
	BestPrice      float64 `json:"best_price"`      // best competing price
	UndercutAmount float64 `json:"undercut_amount"` // absolute ISK difference (always >= 0)
	UndercutPct    float64 `json:"undercut_pct"`    // % difference (always >= 0)
	SuggestedPrice float64 `json:"suggested_price"` // price to beat best by 0.01 ISK
	// Relist economics — populated only when the caller passes a broker
	// fee via AnalyzeUndercutsWithRelistFee. All zero for the pre-fee
	// (backward-compat) shape. NetRelistGainISK ≤ 0 means "hold, don't
	// reprice" — the CCP modify-order broker fee eats the theoretical
	// improvement. Fixes the audit-called-out case of 0.01 ISK undercuts
	// on thin books where the relist recommendation loses money. Audit P2.7.
	ModifyOrderFee     float64 `json:"modify_order_fee"`
	GrossRelistGainISK float64 `json:"gross_relist_gain_isk"`
	NetRelistGainISK   float64 `json:"net_relist_gain_isk"`
	// Top of the order book (up to 5 levels)
	BookLevels []BookLevel `json:"book_levels"`
}

// BookLevel is a single price level in the order book snippet.
type BookLevel struct {
	Price    float64 `json:"price"`
	Volume   int32   `json:"volume"`
	IsPlayer bool    `json:"is_player"` // true if this level contains the player's order
}

// AnalyzeUndercuts compares a player's active orders against the regional order book
// and returns undercut status for each order.
func AnalyzeUndercuts(playerOrders []esi.CharacterOrder, regionOrders []esi.MarketOrder) []UndercutStatus {
	// Index regional orders by (location_id, type_id, side).
	type key struct {
		locationID int64
		typeID     int32
		isBuy      bool
	}
	book := make(map[key][]esi.MarketOrder)
	for _, o := range regionOrders {
		k := key{o.LocationID, o.TypeID, o.IsBuyOrder}
		book[k] = append(book[k], o)
	}

	results := make([]UndercutStatus, 0, len(playerOrders))

	for _, po := range playerOrders {
		k := key{po.LocationID, po.TypeID, po.IsBuyOrder}
		orders := book[k]

		us := UndercutStatus{OrderID: po.OrderID}

		if len(orders) == 0 {
			// No competing orders at all — player is best by default.
			us.Position = 1
			us.TotalOrders = 1
			us.BestPrice = po.Price
			us.SuggestedPrice = po.Price
			results = append(results, us)
			continue
		}

		// Sort: for sell, ascending (cheapest first); for buy, descending (highest first).
		sorted := make([]esi.MarketOrder, len(orders))
		copy(sorted, orders)
		if po.IsBuyOrder {
			sort.Slice(sorted, func(i, j int) bool { return sorted[i].Price > sorted[j].Price })
		} else {
			sort.Slice(sorted, func(i, j int) bool { return sorted[i].Price < sorted[j].Price })
		}

		us.BestPrice = sorted[0].Price
		us.TotalOrders = len(sorted)

		// Find player position (1-based).
		pos := 1
		for _, o := range sorted {
			if o.OrderID == po.OrderID {
				break
			}
			pos++
		}
		if pos > len(sorted) {
			// Player's order not in regional book (may happen for structure orders).
			// Still compute vs best.
			pos = len(sorted) + 1
		}
		us.Position = pos

		// Undercut amount.
		if po.IsBuyOrder {
			// Buy: undercut if someone bids higher.
			if us.BestPrice > po.Price {
				us.UndercutAmount = us.BestPrice - po.Price
			}
		} else {
			// Sell: undercut if someone asks lower.
			if us.BestPrice < po.Price {
				us.UndercutAmount = po.Price - us.BestPrice
			}
		}
		if po.Price > 0 {
			us.UndercutPct = us.UndercutAmount / po.Price * 100
		}

		// Suggested price: smallest legal step beating best. EVE enforces a
		// 4-significant-digit price rule; ±0.01 is only legal below ~100 ISK.
		// NextBuyOverbid / NextSellUndercut snap to the correct grid at any
		// magnitude — see internal/engine/pricing.go.
		if po.IsBuyOrder {
			us.SuggestedPrice = NextBuyOverbid(us.BestPrice)
		} else {
			us.SuggestedPrice = NextSellUndercut(us.BestPrice)
		}
		if us.SuggestedPrice <= 0 {
			// Degenerate best (zero / NaN / infinite input); leave the
			// user's own price so the recommendation isn't garbage.
			us.SuggestedPrice = po.Price
		}
		// If already best, keep current price (guaranteed legal — it was
		// accepted last time it was placed).
		if us.Position == 1 {
			us.SuggestedPrice = po.Price
		}

		// Build book levels (aggregate by price, top 5).
		us.BookLevels = buildBookLevels(sorted, po.OrderID, po.Price, 5)

		results = append(results, us)
	}

	return results
}

// AnalyzeUndercutsWithRelistFee decorates the AnalyzeUndercuts output with
// the CCP modify-order fee — the broker fee charged when repricing an
// active order — so the order-desk "reprice" recommendation can weigh
// the theoretical price improvement against its cost. Without this,
// thin books with a 0.01 ISK undercut trigger a relist that a broker
// fee eats entirely (and then some). Audit P2.7.
//
// Modify-order fee model (matches CCP in-client):
//
//	fee = max(brokerRate/100 × |priceChange| × remainingVolume, 100 ISK)
//
// (An over-approximation of the exact formula, which CCP has quietly
// tweaked; erring generous here means the tool holds a relist that
// might have been marginally profitable rather than recommending one
// that isn't.)
//
// brokerFeePct is the effective per-order rate (post-skill, post-
// standings) the character faces. When 0 (default, or no-fee caller),
// the three fee fields on UndercutStatus stay at 0.
func AnalyzeUndercutsWithRelistFee(playerOrders []esi.CharacterOrder, regionOrders []esi.MarketOrder, brokerFeePct float64) []UndercutStatus {
	results := AnalyzeUndercuts(playerOrders, regionOrders)
	if brokerFeePct <= 0 {
		return results
	}
	byID := make(map[int64]esi.CharacterOrder, len(playerOrders))
	for _, o := range playerOrders {
		byID[o.OrderID] = o
	}
	for i := range results {
		us := &results[i]
		if us.Position == 1 {
			continue
		}
		po, ok := byID[us.OrderID]
		if !ok || po.VolumeRemain <= 0 {
			continue
		}
		delta := us.SuggestedPrice - po.Price
		if delta < 0 {
			delta = -delta
		}
		if delta <= 0 {
			continue
		}
		orderValue := delta * float64(po.VolumeRemain)
		us.ModifyOrderFee = BrokerFeeForOrder(orderValue, brokerFeePct)
		us.GrossRelistGainISK = orderValue
		us.NetRelistGainISK = us.GrossRelistGainISK - us.ModifyOrderFee
	}
	return results
}

// buildBookLevels aggregates orders into price levels and marks which one is the player's.
func buildBookLevels(sorted []esi.MarketOrder, playerOrderID int64, playerPrice float64, maxLevels int) []BookLevel {
	type level struct {
		price    float64
		volume   int32
		isPlayer bool
	}
	var levels []level
	var current *level

	for _, o := range sorted {
		if current == nil || o.Price != current.price {
			if current != nil {
				levels = append(levels, *current)
			}
			lv := level{price: o.Price, volume: o.VolumeRemain}
			if o.OrderID == playerOrderID {
				lv.isPlayer = true
			}
			current = &lv
		} else {
			current.volume += o.VolumeRemain
			if o.OrderID == playerOrderID {
				current.isPlayer = true
			}
		}
	}
	if current != nil {
		levels = append(levels, *current)
	}

	// If player's level is beyond maxLevels, include it and trim.
	playerIdx := -1
	for i, lv := range levels {
		if lv.isPlayer {
			playerIdx = i
			break
		}
	}

	if playerIdx >= 0 && playerIdx >= maxLevels {
		// Show top (maxLevels-1) + player's level.
		top := levels[:maxLevels-1]
		top = append(top, levels[playerIdx])
		levels = top
	} else if len(levels) > maxLevels {
		levels = levels[:maxLevels]
	}

	result := make([]BookLevel, len(levels))
	for i, lv := range levels {
		result[i] = BookLevel{Price: lv.price, Volume: lv.volume, IsPlayer: lv.isPlayer}
	}
	return result
}
