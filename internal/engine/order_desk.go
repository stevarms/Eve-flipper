package engine

import (
	"fmt"
	"math"
	"sort"
	"time"

	"eve-flipper/internal/esi"
)

// Fill-model tuning. These describe how much of a region's reported trade
// volume we believe actually drains one side of one station's book, and
// over what horizon that belief is worth holding.
const (
	// Level comes from the trailing week so the estimate tracks current
	// activity; shape comes from a longer window so a single odd weekend
	// cannot define the profile.
	orderDeskFlowBaseDays  = 7
	orderDeskDowWeeks      = 8
	orderDeskDowMinSamples = 4

	// Depth priced further than this from the region best is not competing
	// for today's flow, so it does not count toward a station's share of it.
	orderDeskCompetitiveBand = 0.05

	// A one-number estimator should never claim a side takes all the flow.
	orderDeskMinSideShare = 0.10
	orderDeskMaxSideShare = 0.90

	// Past this the answer is "not at this price", and a precise figure
	// would be false precision.
	orderDeskETACapDays = 90

	// Default floor under which a margin is reported as thin. Thin is a
	// warning, not a verdict — see orderDeskApplyMargin.
	orderDeskDefaultMinMarginPercent = 3.0
)

// What a row's margin was measured against. A row that could not be
// measured says so rather than reporting a zero margin, because "no data"
// and "no profit" call for opposite responses.
const (
	orderDeskMarginNone      = "none"
	orderDeskMarginBook      = "book"
	orderDeskMarginCostBasis = "cost_basis"
)

// OrderDeskHistoryKey identifies (region, type) history buckets.
type OrderDeskHistoryKey [2]int32

// NewOrderDeskHistoryKey creates a stable key for history lookup.
func NewOrderDeskHistoryKey(regionID, typeID int32) OrderDeskHistoryKey {
	return OrderDeskHistoryKey{regionID, typeID}
}

// OrderDeskOptions controls recommendation and economics assumptions.
type OrderDeskOptions struct {
	SalesTaxPercent  float64
	BrokerFeePercent float64
	TargetETADays    float64
	WarnExpiryDays   int

	// Margin below this (but still positive) is flagged thin without
	// changing the recommendation. Zero takes the default.
	MinMarginPercent float64

	// Average unit cost of stock currently held, keyed by type, used to
	// judge whether a *sell* order is still above water. Sourced from the
	// FIFO trade journal so it covers manufactured stock too. Nil is fine
	// and simply leaves sell rows unmeasured.
	CostBasisByType map[int32]float64
}

// OrderDeskSettings are echoed in the response.
type OrderDeskSettings struct {
	SalesTaxPercent  float64 `json:"sales_tax_percent"`
	BrokerFeePercent float64 `json:"broker_fee_percent"`
	TargetETADays    float64 `json:"target_eta_days"`
	WarnExpiryDays   int     `json:"warn_expiry_days"`
	MinMarginPercent float64 `json:"min_margin_percent"`
}

// OrderDeskSummary aggregates order health for quick triage.
type OrderDeskSummary struct {
	TotalOrders     int     `json:"total_orders"`
	BuyOrders       int     `json:"buy_orders"`
	SellOrders      int     `json:"sell_orders"`
	NeedsReprice    int     `json:"needs_reprice"`
	NeedsCancel     int     `json:"needs_cancel"`
	NeedsReview     int     `json:"needs_review"`
	TotalNotional   float64 `json:"total_notional"`
	MedianETADays   float64 `json:"median_eta_days"`
	AvgETADays      float64 `json:"avg_eta_days"`
	WorstETADays    float64 `json:"worst_eta_days"`
	UnknownETACount int     `json:"unknown_eta_count"`
}

// OrderDeskOrder is one actionable row in the execution desk.
type OrderDeskOrder struct {
	OrderID             int64   `json:"order_id"`
	TypeID              int32   `json:"type_id"`
	TypeName            string  `json:"type_name"`
	LocationID          int64   `json:"location_id"`
	LocationName        string  `json:"location_name"`
	RegionID            int32   `json:"region_id"`
	IsBuyOrder          bool    `json:"is_buy_order"`
	Price               float64 `json:"price"`
	VolumeRemain        int32   `json:"volume_remain"`
	VolumeTotal         int32   `json:"volume_total"`
	Notional            float64 `json:"notional"`
	NetUnitISK          float64 `json:"net_unit_isk"`
	NetNotional         float64 `json:"net_notional"`
	Position            int     `json:"position"`
	TotalOrders         int     `json:"total_orders"`
	BookAvailable       bool    `json:"book_available"`
	BestPrice           float64 `json:"best_price"`
	SuggestedPrice      float64 `json:"suggested_price"`
	UndercutAmount      float64 `json:"undercut_amount"`
	UndercutPct         float64 `json:"undercut_pct"`
	QueueAheadQty       int64   `json:"queue_ahead_qty"`
	TopPriceQty         int64   `json:"top_price_qty"`
	AvgDailyVolume      float64 `json:"avg_daily_volume"`
	EstimatedFillPerDay float64 `json:"estimated_fill_per_day"`
	ETADays             float64 `json:"eta_days"` // -1 = unknown

	// Fill-model transparency. AvgDailyVolume above is the raw blended
	// region-wide volume ESI reports — every trade, both sides, every
	// station. EstimatedFillPerDay is what we actually believe flows
	// through *this* side of the book at *this* station, and is the number
	// the ETA is built from. The two shares are the factors between them,
	// surfaced so the Orders tab can show its working instead of asserting
	// a figure the user has no way to check.
	SellSideShare    float64 `json:"sell_side_share"`
	StationFlowShare float64 `json:"station_flow_share"`
	// Days before this order is even at the front of the queue. The
	// static-queue assumption behind ETADays is only credible over a short
	// horizon, so this is what the "buried" recommendation keys on.
	DaysToClearQueue float64 `json:"days_to_clear_queue"`
	FlowBasis        string  `json:"flow_basis"` // weekday | flat | none
	ETACapped        bool    `json:"eta_capped,omitempty"`
	IssuedAt         string  `json:"issued_at"`
	ExpiresAt        string  `json:"expires_at"`
	DaysToExpire     int     `json:"days_to_expire"` // -1 if unknown
	Recommendation   string  `json:"recommendation"` // hold | reprice | review | cancel
	Reason           string  `json:"reason"`

	// Owner tags stamped by the api-layer aggregator when scope=all so the
	// multi-character Orders tab can group / filter by owning character.
	// Empty when the row came through a single-character request.
	CharacterID   int64  `json:"character_id,omitempty"`
	CharacterName string `json:"character_name,omitempty"`

	// Broker-fee-aware relist economics — matches
	// AnalyzeUndercutsWithRelistFee at undercut.go. Populated inside
	// ComputeOrderDesk when a broker rate is set. Surfaced here (they used
	// to only exist internally) so the Orders tab can render the same
	// ⚠ "fee eats the gain" warning the character-popup desk was silently
	// computing but never showing.
	RelistFeeISK           float64 `json:"relist_fee_isk,omitempty"`
	NetRelistGainISK       float64 `json:"net_relist_gain_isk,omitempty"`
	WarnUnprofitableRelist bool    `json:"warn_unprofitable_relist,omitempty"`

	// Profitability. Until this existed the desk judged orders purely on
	// how fast they would fill, so an order could be deeply underwater and
	// still report "on track" — it was on track, nobody had asked whether
	// filling was a good idea.
	//
	// MarginBasis says which question was answerable for this row:
	// "book" (a buy order priced against the sell side of its own
	// station), "cost_basis" (a sell order priced against what the stock
	// actually cost), or "none" when neither input was available — in
	// which case the margin numbers are meaningless and every
	// margin-driven branch stays out of the way.
	ExitPrice     float64 `json:"exit_price,omitempty"`     // buy rows: assumed resale price
	CostBasisISK  float64 `json:"cost_basis_isk,omitempty"` // sell rows: avg unit cost held
	MarginUnitISK float64 `json:"margin_unit_isk"`
	MarginPercent float64 `json:"margin_percent"`
	MarginBasis   string  `json:"margin_basis"`
	// Positive but under the configured floor. A warning the UI renders,
	// deliberately not a change of recommendation.
	WarnThinMargin bool `json:"warn_thin_margin,omitempty"`
	// What the margin would become after repricing to SuggestedPrice.
	// Internal: it exists to stop the desk advising a move that crosses
	// break-even, and the reason string already says so.
	SuggestedMarginUnitISK float64 `json:"-"`
}

// OrderDeskResponse is the full API payload for the order desk tab.
type OrderDeskResponse struct {
	Summary  OrderDeskSummary  `json:"summary"`
	Orders   []OrderDeskOrder  `json:"orders"`
	Settings OrderDeskSettings `json:"settings"`
}

func normalizeOrderDeskOptions(opt OrderDeskOptions) OrderDeskOptions {
	if opt.SalesTaxPercent < 0 {
		opt.SalesTaxPercent = 0
	}
	if opt.SalesTaxPercent > 100 {
		opt.SalesTaxPercent = 100
	}
	if opt.BrokerFeePercent < 0 {
		opt.BrokerFeePercent = 0
	}
	if opt.BrokerFeePercent > 100 {
		opt.BrokerFeePercent = 100
	}
	if opt.TargetETADays <= 0 {
		opt.TargetETADays = 3
	}
	if opt.WarnExpiryDays <= 0 {
		opt.WarnExpiryDays = 2
	}
	if opt.MinMarginPercent <= 0 {
		opt.MinMarginPercent = orderDeskDefaultMinMarginPercent
	}
	if opt.MinMarginPercent > 100 {
		opt.MinMarginPercent = 100
	}
	return opt
}

// ComputeOrderDesk builds actionable order management analytics:
// position in book, queue ahead, ETA and repricing/cancel recommendations.
func ComputeOrderDesk(
	playerOrders []esi.CharacterOrder,
	regionOrders []esi.MarketOrder,
	historyByKey map[OrderDeskHistoryKey][]esi.HistoryEntry,
	unavailableBooks map[OrderDeskHistoryKey]bool,
	opt OrderDeskOptions,
) OrderDeskResponse {
	opt = normalizeOrderDeskOptions(opt)

	out := OrderDeskResponse{
		Orders: []OrderDeskOrder{},
		Settings: OrderDeskSettings{
			SalesTaxPercent:  opt.SalesTaxPercent,
			BrokerFeePercent: opt.BrokerFeePercent,
			TargetETADays:    opt.TargetETADays,
			WarnExpiryDays:   opt.WarnExpiryDays,
			MinMarginPercent: opt.MinMarginPercent,
		},
	}
	if len(playerOrders) == 0 {
		return out
	}

	type bookKey struct {
		locationID int64
		typeID     int32
		isBuy      bool
	}
	book := make(map[bookKey][]esi.MarketOrder)
	// Same orders indexed region-wide, so a row can weigh its own station
	// against the whole region without rescanning the book per row.
	type regionSideKey struct {
		regionID int32
		typeID   int32
		isBuy    bool
	}
	regionSide := make(map[regionSideKey][]esi.MarketOrder)
	for _, o := range regionOrders {
		k := bookKey{locationID: o.LocationID, typeID: o.TypeID, isBuy: o.IsBuyOrder}
		book[k] = append(book[k], o)
		rk := regionSideKey{regionID: o.RegionID, typeID: o.TypeID, isBuy: o.IsBuyOrder}
		regionSide[rk] = append(regionSide[rk], o)
	}

	etaKnown := make([]float64, 0, len(playerOrders))
	now := time.Now().UTC()
	out.Orders = make([]OrderDeskOrder, 0, len(playerOrders))

	for _, po := range playerOrders {
		row := OrderDeskOrder{
			OrderID:        po.OrderID,
			TypeID:         po.TypeID,
			TypeName:       po.TypeName,
			LocationID:     po.LocationID,
			LocationName:   po.LocationName,
			RegionID:       po.RegionID,
			IsBuyOrder:     po.IsBuyOrder,
			Price:          po.Price,
			VolumeRemain:   po.VolumeRemain,
			VolumeTotal:    po.VolumeTotal,
			Notional:       po.Price * float64(po.VolumeRemain),
			IssuedAt:       po.Issued,
			DaysToExpire:   -1,
			ETADays:        -1,
			BookAvailable:  true,
			Recommendation: "hold",
			Reason:         "on track",
		}

		if po.IsBuyOrder {
			row.NetUnitISK = po.Price * (1 + opt.BrokerFeePercent/100.0)
		} else {
			row.NetUnitISK = po.Price * (1 - (opt.BrokerFeePercent+opt.SalesTaxPercent)/100.0)
			if row.NetUnitISK < 0 {
				row.NetUnitISK = 0
			}
		}
		row.NetNotional = row.NetUnitISK * float64(po.VolumeRemain)

		if issuedAt, err := time.Parse(time.RFC3339, po.Issued); err == nil {
			expAt := issuedAt.AddDate(0, 0, po.Duration)
			row.ExpiresAt = expAt.Format(time.RFC3339)
			row.DaysToExpire = int(math.Ceil(expAt.Sub(now).Hours() / 24.0))
			if row.DaysToExpire < 0 {
				row.DaysToExpire = 0
			}
		}

		hk := NewOrderDeskHistoryKey(po.RegionID, po.TypeID)
		if unavailableBooks != nil && unavailableBooks[hk] {
			row.BookAvailable = false
			row.Position = 0
			row.TotalOrders = 0
			row.BestPrice = 0
			row.SuggestedPrice = po.Price
		} else {
			k := bookKey{locationID: po.LocationID, typeID: po.TypeID, isBuy: po.IsBuyOrder}
			orders := book[k]
			if len(orders) > 0 {
				sorted := make([]esi.MarketOrder, len(orders))
				copy(sorted, orders)
				if po.IsBuyOrder {
					sort.Slice(sorted, func(i, j int) bool {
						if sorted[i].Price == sorted[j].Price {
							return sorted[i].OrderID < sorted[j].OrderID
						}
						return sorted[i].Price > sorted[j].Price
					})
				} else {
					sort.Slice(sorted, func(i, j int) bool {
						if sorted[i].Price == sorted[j].Price {
							return sorted[i].OrderID < sorted[j].OrderID
						}
						return sorted[i].Price < sorted[j].Price
					})
				}

				row.BestPrice = sorted[0].Price
				for _, o := range sorted {
					if o.Price != row.BestPrice {
						break
					}
					row.TopPriceQty += int64(o.VolumeRemain)
				}

				pos := 1
				var queueAhead int64
				playerFound := false
				for _, o := range sorted {
					if o.OrderID == po.OrderID {
						playerFound = true
						break
					}
					queueAhead += int64(o.VolumeRemain)
					pos++
				}
				if !playerFound {
					pos = 1
					queueAhead = 0
					for _, o := range sorted {
						if orderDeskBetterPrice(po.IsBuyOrder, o.Price, po.Price) {
							queueAhead += int64(o.VolumeRemain)
							pos++
						}
					}
				}
				row.Position = pos
				row.QueueAheadQty = queueAhead
				row.TotalOrders = len(sorted)
				if row.TotalOrders < row.Position {
					row.TotalOrders = row.Position
				}
				if row.TotalOrders == 0 {
					row.TotalOrders = 1
				}

				// EVE 4-sig-fig price rule: use the shared pricing helpers
				// so the suggestion is legal at any magnitude (10k step in
				// millions, 1M in billions, etc.) instead of the pre-fix
				// ±0.01 which broke silently on high-value items.
				if po.IsBuyOrder {
					if row.BestPrice > po.Price {
						row.UndercutAmount = row.BestPrice - po.Price
					}
					row.SuggestedPrice = NextBuyOverbid(row.BestPrice)
				} else {
					if row.BestPrice < po.Price {
						row.UndercutAmount = po.Price - row.BestPrice
					}
					row.SuggestedPrice = NextSellUndercut(row.BestPrice)
				}
				if row.SuggestedPrice <= 0 {
					// Degenerate best price (zero / NaN); leave the user's
					// own price so the recommendation isn't garbage.
					row.SuggestedPrice = po.Price
				}
				if row.Position == 1 {
					// Already best — current price is by definition legal.
					row.SuggestedPrice = po.Price
				}
				if po.Price > 0 {
					row.UndercutPct = row.UndercutAmount / po.Price * 100.0
				}
				// Fee-aware relist economics — matches
				// AnalyzeUndercutsWithRelistFee at undercut.go and
				// buildSuggestedOrder in station_command_center.go. Only
				// meaningful when we'd actually be repricing (not at
				// position 1) and a broker rate is supplied.
				if opt.BrokerFeePercent > 0 && row.Position != 1 && row.SuggestedPrice > 0 && po.VolumeRemain > 0 {
					delta := row.SuggestedPrice - po.Price
					if delta < 0 {
						delta = -delta
					}
					fee := opt.BrokerFeePercent / 100.0 * delta * float64(po.VolumeRemain)
					if fee < 100 {
						fee = 100
					}
					row.RelistFeeISK = fee
					// GrossGain is always negative for a reprice toward
					// best (moving away from current position costs ISK
					// per unit either way). Users still want to see the
					// fee — the warning fires whenever net is negative,
					// which is the common case for high-fee low-volume
					// relists.
					grossGain := -delta * float64(po.VolumeRemain)
					row.NetRelistGainISK = grossGain - fee
					if row.NetRelistGainISK < 0 {
						row.WarnUnprofitableRelist = true
					}
				}
			} else {
				row.Position = 1
				row.TotalOrders = 1
				row.BestPrice = po.Price
				row.SuggestedPrice = po.Price
			}
		}

		// Turn the one blended number ESI gives us into a flow that could
		// plausibly reach this order: the right side of the book, at the
		// right station, shaped by the day of the week.
		entries := historyByKey[hk]
		row.AvgDailyVolume = orderDeskAvgDailyVolume(entries, orderDeskFlowBaseDays)
		row.SellSideShare = 0.5
		row.StationFlowShare = 1
		row.FlowBasis = "none"

		if row.BookAvailable {
			stationAsk := orderDeskBestPrice(book[bookKey{locationID: po.LocationID, typeID: po.TypeID, isBuy: false}], false)
			stationBid := orderDeskBestPrice(book[bookKey{locationID: po.LocationID, typeID: po.TypeID, isBuy: true}], true)
			row.SellSideShare = orderDeskSellSideShare(
				stationBid, stationAsk, orderDeskRecentAvgPrice(entries, orderDeskFlowBaseDays))
			row.StationFlowShare = orderDeskStationShare(
				regionSide[regionSideKey{regionID: po.RegionID, typeID: po.TypeID, isBuy: po.IsBuyOrder}],
				po.LocationID, po.IsBuyOrder)
		}

		// A sell order only drains on the volume that lifted sell orders;
		// a buy order only on the rest.
		sideShare := row.SellSideShare
		if po.IsBuyOrder {
			sideShare = 1 - row.SellSideShare
		}
		baseFlow := row.AvgDailyVolume * sideShare * row.StationFlowShare

		if baseFlow > 0 && row.VolumeRemain > 0 {
			dow, weekdayShape := orderDeskDowProfile(entries, orderDeskDowWeeks)
			if weekdayShape {
				row.FlowBasis = "weekday"
			} else {
				row.FlowBasis = "flat"
			}

			// One cumulative walk, not two additions: a queue that clears on
			// Friday puts your own units into Saturday's volume rather than
			// into an averaged day that exists nowhere on the calendar.
			row.DaysToClearQueue, _ = orderDeskWalkDays(float64(row.QueueAheadQty), baseFlow, dow, now)
			units := float64(row.QueueAheadQty) + float64(row.VolumeRemain)
			eta, capped := orderDeskWalkDays(units, baseFlow, dow, now)
			row.ETADays = eta
			row.ETACapped = capped

			// Report the flow the ETA actually implies, averaged across the
			// horizon it covers, so the figure shown and the figure used
			// cannot drift apart.
			if eta > 0 {
				row.EstimatedFillPerDay = units / eta
			} else {
				row.EstimatedFillPerDay = baseFlow
			}
			etaKnown = append(etaKnown, row.ETADays)
		}

		orderDeskApplyMargin(&row,
			orderDeskBestPrice(book[bookKey{locationID: po.LocationID, typeID: po.TypeID, isBuy: false}], false),
			opt)

		row.Recommendation, row.Reason = orderDeskRecommendation(row, opt)
		out.Orders = append(out.Orders, row)
	}

	for _, row := range out.Orders {
		out.Summary.TotalOrders++
		out.Summary.TotalNotional += row.Notional
		if row.IsBuyOrder {
			out.Summary.BuyOrders++
		} else {
			out.Summary.SellOrders++
		}
		switch row.Recommendation {
		case "reprice":
			out.Summary.NeedsReprice++
		case "cancel":
			out.Summary.NeedsCancel++
		case "review":
			out.Summary.NeedsReview++
		}
		if row.ETADays < 0 {
			out.Summary.UnknownETACount++
		}
	}
	if len(etaKnown) > 0 {
		var total float64
		for _, v := range etaKnown {
			total += v
			if v > out.Summary.WorstETADays {
				out.Summary.WorstETADays = v
			}
		}
		out.Summary.AvgETADays = total / float64(len(etaKnown))
		out.Summary.MedianETADays = orderDeskMedian(etaKnown)
	}

	sort.Slice(out.Orders, func(i, j int) bool {
		pi := orderDeskActionPriority(out.Orders[i].Recommendation)
		pj := orderDeskActionPriority(out.Orders[j].Recommendation)
		if pi != pj {
			return pi < pj
		}
		if out.Orders[i].ETADays == out.Orders[j].ETADays {
			return out.Orders[i].Notional > out.Orders[j].Notional
		}
		// Unknown ETA goes last.
		if out.Orders[i].ETADays < 0 {
			return false
		}
		if out.Orders[j].ETADays < 0 {
			return true
		}
		return out.Orders[i].ETADays > out.Orders[j].ETADays
	})

	return out
}

func orderDeskBetterPrice(isBuy bool, a, b float64) bool {
	if isBuy {
		return a > b
	}
	return a < b
}

// orderDeskVolumeByDate collapses history into date -> volume, plus the
// earliest and latest dates present. Days with no trades are simply absent
// from ESI history, so callers index this map by calendar date and let the
// zero value carry the quiet days — but only inside [earliest, latest],
// outside which an absent date means "no data" rather than "no trades".
func orderDeskVolumeByDate(entries []esi.HistoryEntry) (map[string]float64, string, string) {
	volByDate := make(map[string]float64, len(entries))
	earliestDate, latestDate := "", ""
	for _, e := range entries {
		if e.Date == "" {
			continue
		}
		if e.Date > latestDate {
			latestDate = e.Date
		}
		if earliestDate == "" || e.Date < earliestDate {
			earliestDate = e.Date
		}
		if e.Volume > 0 {
			volByDate[e.Date] += float64(e.Volume)
		}
	}
	return volByDate, earliestDate, latestDate
}

func orderDeskAvgDailyVolume(entries []esi.HistoryEntry, days int) float64 {
	if len(entries) == 0 || days <= 0 {
		return 0
	}
	volByDate, _, latestDate := orderDeskVolumeByDate(entries)
	if latestDate == "" {
		return 0
	}
	end, err := time.Parse("2006-01-02", latestDate)
	if err != nil {
		return 0
	}
	total := 0.0
	for i := 0; i < days; i++ {
		d := end.AddDate(0, 0, -i).Format("2006-01-02")
		total += volByDate[d]
	}
	return total / float64(days)
}

// orderDeskRecentAvgPrice is the volume-weighted mean trade price over the
// last `days` of history. Volume-weighted rather than a plain mean so one
// quiet day at an odd price cannot drag it.
func orderDeskRecentAvgPrice(entries []esi.HistoryEntry, days int) float64 {
	if len(entries) == 0 || days <= 0 {
		return 0
	}
	_, _, latestDate := orderDeskVolumeByDate(entries)
	if latestDate == "" {
		return 0
	}
	end, err := time.Parse("2006-01-02", latestDate)
	if err != nil {
		return 0
	}
	cutoff := end.AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	var weighted, weight float64
	for _, e := range entries {
		if e.Date < cutoff || e.Date > latestDate || e.Volume <= 0 || e.Average <= 0 {
			continue
		}
		weighted += e.Average * float64(e.Volume)
		weight += float64(e.Volume)
	}
	if weight <= 0 {
		return 0
	}
	return weighted / weight
}

// orderDeskBestPrice returns the best price on one side of a book — lowest
// for sells, highest for buys — ignoring empty and non-positive orders.
func orderDeskBestPrice(orders []esi.MarketOrder, isBuy bool) float64 {
	best := 0.0
	for _, o := range orders {
		if o.Price <= 0 || o.VolumeRemain <= 0 {
			continue
		}
		if best == 0 || orderDeskBetterPrice(isBuy, o.Price, best) {
			best = o.Price
		}
	}
	return best
}

// orderDeskSellSideShare estimates what fraction of traded volume executed
// against sell orders. ESI reports one blended figure per day, but only the
// trades that lifted a sell order drain a sell queue — counting the rest is
// why the desk used to promise fills that never came.
//
// Where the volume-weighted average trade price sits inside the current
// spread is the tell: near the ask means buyers were lifting sell orders,
// near the bid means sellers were hitting buy orders. Falls back to an even
// split whenever the spread is degenerate or the average sits outside it —
// a stale book, or a price that has moved since the history was written.
func orderDeskSellSideShare(bestBid, bestAsk, avgPrice float64) float64 {
	if bestBid <= 0 || bestAsk <= 0 || avgPrice <= 0 || bestAsk <= bestBid {
		return 0.5
	}
	if avgPrice < bestBid || avgPrice > bestAsk {
		return 0.5
	}
	share := (avgPrice - bestBid) / (bestAsk - bestBid)
	return math.Min(orderDeskMaxSideShare, math.Max(orderDeskMinSideShare, share))
}

// orderDeskStationShare approximates how much of a region's flow for one
// side of one item passes through a single station, using that station's
// share of competitively-priced depth as the proxy.
//
// Depth further than orderDeskCompetitiveBand from the region best is not
// competing for today's trades, so a hopeful dumper parked in a backwater
// cannot claim a share of flow it will never see. If that band turns out to
// be empty on either side of the ratio the unbanded share is used instead,
// which is imprecise but never zero — and a zero here would silently read
// as "no liquidity data" downstream.
func orderDeskStationShare(orders []esi.MarketOrder, locationID int64, isBuy bool) float64 {
	if len(orders) == 0 {
		return 1
	}
	best := orderDeskBestPrice(orders, isBuy)

	var bandRegion, bandStation, allRegion, allStation float64
	for _, o := range orders {
		if o.Price <= 0 || o.VolumeRemain <= 0 {
			continue
		}
		qty := float64(o.VolumeRemain)
		allRegion += qty
		if o.LocationID == locationID {
			allStation += qty
		}
		if best <= 0 {
			continue
		}
		offset := (o.Price - best) / best
		if isBuy {
			offset = (best - o.Price) / best
		}
		if offset > orderDeskCompetitiveBand {
			continue
		}
		bandRegion += qty
		if o.LocationID == locationID {
			bandStation += qty
		}
	}

	if bandRegion > 0 && bandStation > 0 {
		return bandStation / bandRegion
	}
	if allRegion > 0 && allStation > 0 {
		return allStation / allRegion
	}
	return 1
}

// orderDeskDowProfile derives seven multipliers, Sunday-indexed and
// averaging 1.0, describing how volume distributes across the week. EVE's
// market is markedly heavier Friday through Sunday, so a flat weekly mean
// answers "how much trades on an average day" when the question is "how
// much trades over the next three days, starting today".
//
// Only the shape comes from this window — the level still comes from the
// trailing week — and it reports false when any weekday is too sparse to
// characterise, rather than inventing a shape out of two data points.
func orderDeskDowProfile(entries []esi.HistoryEntry, weeks int) ([7]float64, bool) {
	flat := [7]float64{1, 1, 1, 1, 1, 1, 1}
	if len(entries) == 0 || weeks <= 0 {
		return flat, false
	}
	volByDate, earliestDate, latestDate := orderDeskVolumeByDate(entries)
	if latestDate == "" || earliestDate == "" {
		return flat, false
	}
	end, err := time.Parse("2006-01-02", latestDate)
	if err != nil {
		return flat, false
	}
	start, err := time.Parse("2006-01-02", earliestDate)
	if err != nil {
		return flat, false
	}

	// Count only days the history actually covers. Walking the full window
	// regardless would turn one observation per weekday into eight
	// "samples" of mostly-imaginary zeroes and sail straight past the
	// sparsity guard below.
	var sum [7]float64
	var count [7]int
	for i := 0; i < weeks*7; i++ {
		d := end.AddDate(0, 0, -i)
		if d.Before(start) {
			break
		}
		wd := int(d.Weekday())
		sum[wd] += volByDate[d.Format("2006-01-02")]
		count[wd]++
	}

	var mean [7]float64
	var total float64
	for i := 0; i < 7; i++ {
		if count[i] < orderDeskDowMinSamples {
			return flat, false
		}
		mean[i] = sum[i] / float64(count[i])
		total += mean[i]
	}
	if total <= 0 {
		return flat, false
	}

	overall := total / 7
	var out [7]float64
	for i := 0; i < 7; i++ {
		out[i] = mean[i] / overall
	}
	return out, true
}

// orderDeskWalkDays steps forward from `from`, consuming each day's expected
// flow until `units` are covered, and returns how many fractional days that
// took plus whether it hit the cap. Walking the calendar rather than
// dividing by an average is the whole point of the weekday profile: listing
// on a Thursday and listing on a Sunday night are different questions.
func orderDeskWalkDays(units, baseFlow float64, dow [7]float64, from time.Time) (float64, bool) {
	if units <= 0 {
		return 0, false
	}
	if baseFlow <= 0 {
		return float64(orderDeskETACapDays), true
	}
	elapsed := 0.0
	remaining := units
	for i := 0; i < orderDeskETACapDays; i++ {
		flow := baseFlow * dow[int(from.AddDate(0, 0, i).Weekday())]
		if flow <= 0 {
			elapsed++
			continue
		}
		if remaining <= flow {
			return elapsed + remaining/flow, false
		}
		remaining -= flow
		elapsed++
	}
	return float64(orderDeskETACapDays), true
}

// orderDeskApplyMargin works out what one more filled unit is actually
// worth, and what repricing to the suggested price would do to that.
//
// The two sides are different questions answered from different data:
//
//   - A buy order is forward-looking and needs only the book. The ISK is
//     not spent yet, so the question is whether filling would make money:
//     what you would net reselling here, less what you are bidding. The
//     broker fee already paid to place the order is sunk — cancelling does
//     not refund it — so it is deliberately excluded, which is why this
//     does not reuse NetUnitISK.
//
//   - A sell order is backward-looking and needs a cost basis, because the
//     ISK is already spent and the book cannot tell you what you paid.
//
// stationAsk is the best sell price at this row's own station. Either
// input can be missing — an empty sell side, no journal history for the
// type — and the row then reports basis "none" rather than a fabricated
// zero.
func orderDeskApplyMargin(row *OrderDeskOrder, stationAsk float64, opt OrderDeskOptions) {
	row.MarginBasis = orderDeskMarginNone
	if !row.BookAvailable || row.Price <= 0 {
		return
	}

	// Both fees land on the sale, whichever side of the book we came from.
	proceedsMult := 1 - (opt.SalesTaxPercent+opt.BrokerFeePercent)/100.0
	if proceedsMult < 0 {
		proceedsMult = 0
	}

	var basis string
	var reference float64              // what a percentage is taken against
	var marginAt func(float64) float64 // margin if our price were x

	if row.IsBuyOrder {
		if stationAsk <= 0 {
			return
		}
		// You cannot sell *at* the best ask, you have to beat it — the
		// same step the Suggested column already tells you to take.
		exit := NextSellUndercut(stationAsk)
		if exit <= 0 {
			return
		}
		row.ExitPrice = exit
		basis = orderDeskMarginBook
		reference = row.Price
		marginAt = func(bid float64) float64 { return exit*proceedsMult - bid }
	} else {
		cost := 0.0
		if opt.CostBasisByType != nil {
			cost = opt.CostBasisByType[row.TypeID]
		}
		if cost <= 0 {
			return
		}
		row.CostBasisISK = cost
		basis = orderDeskMarginCostBasis
		reference = cost
		marginAt = func(ask float64) float64 { return ask*proceedsMult - cost }
	}
	if reference <= 0 {
		return
	}

	row.MarginBasis = basis
	row.MarginUnitISK = marginAt(row.Price)
	row.MarginPercent = row.MarginUnitISK / reference * 100.0

	// At position 1 SuggestedPrice is the current price, so this collapses
	// to the same number and the break-even guard can never misfire.
	row.SuggestedMarginUnitISK = row.MarginUnitISK
	if row.SuggestedPrice > 0 {
		row.SuggestedMarginUnitISK = marginAt(row.SuggestedPrice)
	}

	if row.MarginUnitISK > 0 && row.MarginPercent < opt.MinMarginPercent {
		row.WarnThinMargin = true
	}
}

func orderDeskRecommendation(row OrderDeskOrder, opt OrderDeskOptions) (string, string) {
	if !row.BookAvailable {
		return "hold", "market book unavailable"
	}

	// A losing position outranks every liquidity verdict below. Filling
	// sooner is not an improvement when the fill is the problem, and
	// "hold — on track" is exactly the answer that made this necessary.
	if row.MarginBasis != orderDeskMarginNone && row.MarginUnitISK <= 0 {
		if row.IsBuyOrder {
			return "cancel", fmt.Sprintf("margin gone: %+.1f%% at current book", row.MarginPercent)
		}
		// A sell order is already paid for, so cancelling does not undo the
		// loss — it swaps realising it for holding stock. Whether that is
		// right depends on the bounce, the other hubs and what the freed ISK
		// would earn, none of which this row can see. Say "review" and let
		// the disposition panel price the three options.
		return "review", fmt.Sprintf("below cost: %+.1f%% — weigh cut, move or hold", row.MarginPercent)
	}

	action, reason := orderDeskLiquidityRecommendation(row, opt)

	// Never advise chasing the price past break-even. The reprice branches
	// key on queue position alone, so on an undercut order they say "match
	// the leader" without ever checking whether the leader's price is one
	// you can afford to meet.
	if action == "reprice" &&
		row.MarginBasis != orderDeskMarginNone &&
		row.SuggestedPrice > 0 &&
		row.SuggestedMarginUnitISK <= 0 {
		if row.IsBuyOrder {
			return "cancel", "overbidding would erase the margin"
		}
		return "review", "reprice would sell below cost"
	}

	return action, reason
}

// orderDeskLiquidityRecommendation is the original will-it-fill verdict,
// unchanged. Kept separate so the profitability checks above can sit in
// front of it without being tangled through its branches.
func orderDeskLiquidityRecommendation(row OrderDeskOrder, opt OrderDeskOptions) (string, string) {
	if row.ETADays < 0 {
		if row.DaysToExpire >= 0 && row.DaysToExpire <= opt.WarnExpiryDays {
			return "cancel", "low liquidity near expiry"
		}
		return "hold", "insufficient liquidity history"
	}

	if row.DaysToExpire >= 0 && row.DaysToExpire <= 1 && row.ETADays > float64(row.DaysToExpire)+0.5 {
		return "cancel", "unlikely to fill before expiry"
	}

	if row.Position > 1 && row.DaysToExpire >= 0 && row.DaysToExpire <= opt.WarnExpiryDays {
		return "reprice", "undercut near expiry"
	}

	// Depth ahead is only worth waiting out over a short horizon. Past
	// that, the static-queue assumption behind the ETA stops being
	// credible — competitors relist under you faster than a deep queue
	// drains — so the desk stops claiming the order is on track.
	if row.Position > 1 && row.DaysToClearQueue > opt.TargetETADays/2 {
		return "reprice", fmt.Sprintf("buried: %.1fd of depth ahead", row.DaysToClearQueue)
	}

	if row.Position > 1 && row.ETADays > opt.TargetETADays {
		return "reprice", "eta above target"
	}

	if row.Position == 1 && row.ETADays > opt.TargetETADays*2 {
		return "hold", "top of book but slow market"
	}

	return "hold", "on track"
}

func orderDeskActionPriority(action string) int {
	switch action {
	case "cancel":
		return 0
	case "review":
		return 1
	case "reprice":
		return 2
	default:
		return 3
	}
}

func orderDeskMedian(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	s := make([]float64, len(values))
	copy(s, values)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}
