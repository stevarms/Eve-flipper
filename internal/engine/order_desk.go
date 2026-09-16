package engine

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
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

	// How far under the best reaching bid a buy order has to sit before the
	// desk stops treating it as an order that is trying to fill today.
	//
	// A lowball is, by construction, last in the queue: everything at the
	// station is ahead of it and its ETA is enormous. Without this the desk
	// reports the defining property of a parked bid as a fault, every day,
	// on every parked bid.
	orderDeskDefaultLowballPct = 20.0

	// A suggested buy price this much above your own is not a reprice, it
	// is a different trade. Repricing is maintenance; committing another
	// order of magnitude of ISK is a decision.
	orderDeskDefaultRepriceJumpPct = 25.0

	// And bidding up here in the item's own year is worth a second look
	// whatever the spread says, because a spread is momentary and a price
	// level is not.
	orderDeskRiskPercentile = 85.0
)

// What a row's margin was measured against. A row that could not be
// measured says so rather than reporting a zero margin, because "no data"
// and "no profit" call for opposite responses.
const (
	orderDeskMarginNone      = "none"
	orderDeskMarginBook      = "book"
	orderDeskMarginCostBasis = "cost_basis"
	// A sell order with no cost basis but a target price you set by hand.
	// Measured as distance from the target rather than profit over cost —
	// see orderDeskApplyMargin — which is what gives the after-reprice
	// checks something to work with on rows that were previously unguarded
	// entirely.
	orderDeskMarginTarget = "target"
)

// orderDeskMarginIsLoss reports whether a basis can establish that money is
// being lost. A target shortfall is a decision not going your way, which is a
// different thing from a position underwater, and must not be reported with
// the same words.
func orderDeskMarginIsLoss(basis string) bool {
	return basis == orderDeskMarginBook || basis == orderDeskMarginCostBasis
}

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

	// Solar system for each of your own orders' locations, resolved
	// api-side because the engine holds no SDE.
	StationSystemID map[int64]int32

	// JumpsBetween returns gate jumps between two systems, or -1 when the
	// route is unknown. Nil switches range awareness off entirely and the
	// desk measures buy rows against their own station's book, which is
	// what it did before ranges were parsed at all.
	JumpsBetween func(fromSystem, toSystem int32) int

	// A buy order at or below this much under the best reaching bid is read
	// as parked rather than competing. Zero takes the default.
	LowballDiscountPct float64

	// PatientBidTypes forces that reading on a type whose bid sits closer
	// in than the threshold — the manual override, sourced from the
	// holding rule's patient-bid flag. Nil is fine.
	PatientBidTypes map[int32]bool

	// A suggested buy price more than this percent above your own is
	// reported as a new trade rather than a reprice. Zero takes the default.
	RepriceJumpPct float64

	// The item's own price distribution, keyed the same way history is.
	// Sourced api-side from the derived cache, because the raw history
	// cache is capped at 90 days and percentiles need a year. Absent
	// entries simply leave a row's percentile figures unset.
	PercentilesByKey map[OrderDeskHistoryKey]PricePercentiles

	// Prices you set by hand, from the same per-type holding rules Assets
	// and Today already read. TargetPriceByType is a floor under selling,
	// MaxBidByType a ceiling over bidding. Both nil is fine.
	//
	// They outrank every verdict derived from the book, because the book is
	// the one thing the desk can see and a decision about what an item is
	// worth is the one thing it cannot.
	TargetPriceByType map[int32]float64
	MaxBidByType      map[int32]float64

	// ExitStationByRegion names the station a buy row's stock is assumed to
	// be sold at, keyed by the region the order sits in — the configured
	// trade station, with the region's canonical hub as the fallback.
	//
	// Bidding from a quiet station a jump or two out of the hub, at a range
	// that reaches it, is ordinary practice: the hub's broker fee is a
	// percentage of a very large number and its neighbours charge nothing
	// like it. Those stations have no sell side of their own, so measuring
	// such an order's exit against its own station's book reported no
	// margin at all on rows that have a perfectly good one.
	//
	// The trade station wins over a local sell book rather than merely
	// filling in for a missing one: where the stock gets sold is a decision
	// about the trade, not a consequence of which backwater happens to have
	// an order standing in it today.
	//
	// Nil, or a region with no entry, leaves rows measured at their own
	// station — which is what the desk did before.
	ExitStationByRegion map[int32]int64
}

// OrderDeskSettings are echoed in the response.
type OrderDeskSettings struct {
	SalesTaxPercent    float64 `json:"sales_tax_percent"`
	BrokerFeePercent   float64 `json:"broker_fee_percent"`
	TargetETADays      float64 `json:"target_eta_days"`
	WarnExpiryDays     int     `json:"warn_expiry_days"`
	MinMarginPercent   float64 `json:"min_margin_percent"`
	LowballDiscountPct float64 `json:"lowball_discount_pct"`
	RepriceJumpPct     float64 `json:"reprice_jump_pct"`
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
	// "book" (a buy order priced against a sell side — its own station's,
	// or the trade station's when the order is parked elsewhere; see
	// ExitLocationID), "cost_basis" (a sell order priced against what the
	// stock actually cost), "target" (a sell order with no cost basis but a
	// target you set), or "none" when nothing was available — in which case
	// the margin numbers are meaningless and every margin-driven branch
	// stays out of the way.
	ExitPrice float64 `json:"exit_price,omitempty"` // buy rows: assumed resale price
	// Where ExitPrice was read, when that is not the order's own station.
	// Zero means the exit is local and the margin is takeable on the spot.
	//
	// A bid parked one system out of the hub to dodge its broker fee is
	// standard practice, and such a station usually has no sell side at all
	// — so the honest exit for it is the hub, and the row has to say so.
	// Hauling is not modelled: this is the flag that stops a hub margin
	// reading as a local one.
	ExitLocationID   int64   `json:"exit_location_id,omitempty"`
	ExitLocationName string  `json:"exit_location_name,omitempty"`
	CostBasisISK     float64 `json:"cost_basis_isk,omitempty"` // sell rows: avg unit cost held
	MarginUnitISK    float64 `json:"margin_unit_isk"`
	MarginPercent    float64 `json:"margin_percent"`
	MarginBasis      string  `json:"margin_basis"`
	// Positive but under the configured floor. A warning the UI renders,
	// deliberately not a change of recommendation.
	WarnThinMargin bool `json:"warn_thin_margin,omitempty"`

	// Range awareness, buy rows only — sell orders in EVE are always
	// station-range. OrderRange is your own order's range as ESI reports
	// it, and CompetingRemoteBids counts how many of the orders ahead of
	// you are standing somewhere other than your station. That count is
	// the whole explanation for a position that looks wrong against the
	// station's own book.
	OrderRange          string `json:"order_range,omitempty"`
	CompetingRemoteBids int    `json:"competing_remote_bids,omitempty"`

	// IsLowball marks a buy order parked well under the best reaching bid.
	// It is not a fault: it is a bid waiting for someone in a hurry, and the
	// desk's queue and ETA verdicts stand down for it entirely.
	IsLowball bool `json:"is_lowball,omitempty"`
	// PatientBid is IsLowball asserted by hand rather than inferred from
	// distance. The two are not interchangeable: distance cannot tell a bid
	// parked on purpose from one the market ran away from, so an inferred
	// lowball still gets the price-level test below and a declared one does
	// not.
	PatientBid bool `json:"patient_bid,omitempty"`
	// A lowball worth placing, suggested from the item's own year, with the
	// share of days its daily average actually sat at or below that price —
	// the only honest fill-odds statement daily bars can support.
	LowballPrice       float64 `json:"lowball_price,omitempty"`
	LowballFillDaysPct float64 `json:"lowball_fill_days_pct,omitempty"`

	// What a reprice would commit, and where it would sit in the item's own
	// history. The desk used to price a reprice purely as a concession —
	// NetRelistGainISK, above — which says what the move costs per unit and
	// nothing at all about the ISK it puts on the table. A bid chasing a
	// book that has moved 10x passes every per-unit test ever written.
	SuggestedNotional float64 `json:"suggested_notional,omitempty"`
	AddedCapitalISK   float64 `json:"added_capital_isk,omitempty"`
	// PercentileBasis is "history" or "none"; when it is "none" the two
	// percentiles are unmeasured, not zero.
	PricePercentile          float64 `json:"price_percentile,omitempty"`
	SuggestedPricePercentile float64 `json:"suggested_price_percentile,omitempty"`
	PercentileBasis          string  `json:"percentile_basis,omitempty"`

	// The holding rule in force for this type, if any — the same rule
	// Assets → Positions edits, deliberately not a second one keyed by
	// order id, since relisting mints a new order id and would lose it.
	//
	// TargetPrice floors a sell, MaxBidPrice caps a bid, and
	// TargetProgressPct is how far the market has come toward the target,
	// mirroring the figure Positions shows for the same rule.
	TargetPrice       float64 `json:"target_price,omitempty"`
	TargetProgressPct float64 `json:"target_progress_pct,omitempty"`
	TargetMet         bool    `json:"target_met,omitempty"`
	MaxBidPrice       float64 `json:"max_bid_price,omitempty"`
	HasHoldingRule    bool    `json:"has_holding_rule,omitempty"`
	// What the margin would become after repricing to SuggestedPrice.
	//
	// This used to be internal, on the grounds that the break-even guard
	// already spoke for it in the reason string. That was wrong in the one
	// case that matters: a move from a healthy margin to a technically
	// positive one is advised silently, and the user has no way to see the
	// trade getting worse until it has.
	SuggestedMarginUnitISK float64 `json:"suggested_margin_unit_isk"`
	SuggestedMarginPercent float64 `json:"suggested_margin_percent"`
	// After-margin is positive but under the floor. Like WarnThinMargin
	// this changes no verdict; unlike it, taking the suggested price is
	// what would cause it.
	//
	// Set whenever a different price is on the table, which is not the same
	// as the desk advising it — SuggestedPrice is computed from the book for
	// every row, including one told to hold. Consumers gate on the
	// recommendation: the reason string below only mentions this under
	// "reprice", and the UI shows a second margin figure only there too.
	WarnThinAfterReprice bool `json:"warn_thin_after_reprice,omitempty"`
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
	if opt.LowballDiscountPct <= 0 {
		opt.LowballDiscountPct = orderDeskDefaultLowballPct
	}
	// A 100% discount is a bid of zero, so the whole range has to stay
	// under it or the test can never be true.
	if opt.LowballDiscountPct > 99 {
		opt.LowballDiscountPct = 99
	}
	if opt.RepriceJumpPct <= 0 {
		opt.RepriceJumpPct = orderDeskDefaultRepriceJumpPct
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
			SalesTaxPercent:    opt.SalesTaxPercent,
			BrokerFeePercent:   opt.BrokerFeePercent,
			TargetETADays:      opt.TargetETADays,
			WarnExpiryDays:     opt.WarnExpiryDays,
			MinMarginPercent:   opt.MinMarginPercent,
			LowballDiscountPct: opt.LowballDiscountPct,
			RepriceJumpPct:     opt.RepriceJumpPct,
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
			OrderRange:     po.Range,
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
			// A buy order competes with every bid that can reach your
			// station, not with the ones parked in the same building. Sell
			// orders are always station-range, so they keep the station
			// book untouched.
			if po.IsBuyOrder && opt.JumpsBetween != nil {
				mySystem := int32(0)
				if opt.StationSystemID != nil {
					mySystem = opt.StationSystemID[po.LocationID]
				}
				pool := regionSide[regionSideKey{regionID: po.RegionID, typeID: po.TypeID, isBuy: true}]
				reaching := make([]esi.MarketOrder, 0, len(pool))
				for _, o := range pool {
					if orderDeskBidReaches(o, po.LocationID, mySystem, opt.JumpsBetween) {
						reaching = append(reaching, o)
					}
				}
				// An empty result means the region book did not come back,
				// not that nobody is bidding; fall back rather than report
				// an empty queue as position 1.
				if len(reaching) > 0 {
					orders = reaching
				}
			}
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
				// How much of that queue is somewhere else. Only ever
				// non-zero on a buy row, since the sell book above is
				// station-scoped by construction.
				for _, o := range sorted[:min(pos-1, len(sorted))] {
					if o.LocationID != po.LocationID {
						row.CompetingRemoteBids++
					}
				}
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

		// The prices you set by hand, before anything derived from the book
		// gets a say.
		if v := opt.TargetPriceByType[po.TypeID]; v > 0 {
			row.TargetPrice = v
			row.HasHoldingRule = true
			if row.BestPrice > 0 {
				row.TargetProgressPct = clampRange(row.BestPrice/v*100.0, 0, 100)
				row.TargetMet = row.BestPrice >= v
			}
		}
		if v := opt.MaxBidByType[po.TypeID]; v > 0 {
			row.MaxBidPrice = v
			row.HasHoldingRule = true
		}
		if opt.PatientBidTypes[po.TypeID] {
			row.PatientBid = true
			row.HasHoldingRule = true
		}

		// Is this bid parked, or trying to fill? Judged against the
		// range-aware best bid from above, so the discount is measured
		// against the competition that actually exists.
		if po.IsBuyOrder && row.BookAvailable && po.Price > 0 {
			switch {
			case opt.PatientBidTypes[po.TypeID]:
				row.IsLowball = true
			case row.BestPrice > 0 && po.Price <= row.BestPrice*(1-opt.LowballDiscountPct/100.0):
				row.IsLowball = true
			}
		}

		// What the suggested move would commit, and where both prices sit in
		// the item's own year.
		row.SuggestedNotional = row.SuggestedPrice * float64(row.VolumeRemain)
		if po.IsBuyOrder {
			row.AddedCapitalISK = row.SuggestedNotional - row.Notional
		}
		row.PercentileBasis = PercentileBasisNone
		if pct, ok := opt.PercentilesByKey[hk]; ok && pct.Basis == PercentileBasisHistory {
			row.PercentileBasis = PercentileBasisHistory
			row.PricePercentile = pct.RankOfPrice(po.Price)
			if row.SuggestedPrice > 0 {
				row.SuggestedPricePercentile = pct.RankOfPrice(row.SuggestedPrice)
			}
			// A lowball worth placing is a decile price, not a round number
			// off the top of the book.
			if po.IsBuyOrder && pct.P10 > 0 {
				row.LowballPrice = pct.P10
				row.LowballFillDaysPct = pct.RankOfPrice(pct.P10)
			}
		}

		// Where this row's stock would be sold. A sell order is already
		// standing where its stock is, so only buy rows can exit somewhere
		// else, and only when the trade station has a book to exit into.
		exitStation := po.LocationID
		exitAsk := orderDeskBestPrice(book[bookKey{locationID: po.LocationID, typeID: po.TypeID, isBuy: false}], false)
		if po.IsBuyOrder {
			if hub := opt.ExitStationByRegion[po.RegionID]; hub > 0 && hub != po.LocationID {
				if ask := orderDeskBestPrice(book[bookKey{locationID: hub, typeID: po.TypeID, isBuy: false}], false); ask > 0 {
					exitStation, exitAsk = hub, ask
				}
			}
		}
		orderDeskApplyMargin(&row, exitAsk, exitStation, opt)

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
// exitAsk is the best sell price where this row's stock would be sold, and
// exitStation is where that was read — the row's own station normally, the
// configured trade station for a bid parked outside it. Either input can be
// missing — an empty sell side, no journal history for the type — and the row
// then reports basis "none" rather than a fabricated zero.
func orderDeskApplyMargin(row *OrderDeskOrder, exitAsk float64, exitStation int64, opt OrderDeskOptions) {
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
		if exitAsk <= 0 {
			return
		}
		// You cannot sell *at* the best ask, you have to beat it — the
		// same step the Suggested column already tells you to take.
		exit := NextSellUndercut(exitAsk)
		if exit <= 0 {
			return
		}
		row.ExitPrice = exit
		if exitStation != 0 && exitStation != row.LocationID {
			row.ExitLocationID = exitStation
		}
		basis = orderDeskMarginBook
		reference = row.Price
		marginAt = func(bid float64) float64 { return exit*proceedsMult - bid }
	} else {
		cost := 0.0
		if opt.CostBasisByType != nil {
			cost = opt.CostBasisByType[row.TypeID]
		}
		switch {
		case cost > 0:
			row.CostBasisISK = cost
			basis = orderDeskMarginCostBasis
			reference = cost
			marginAt = func(ask float64) float64 { return ask*proceedsMult - cost }
		case row.TargetPrice > 0:
			// No journal history for this type, but you have said what you
			// want for it. Measure distance from that instead of reporting
			// "none" — which is what used to switch off the break-even
			// guard and the after-reprice checks on exactly the rows the
			// desk was most confidently wrong about.
			//
			// Both sides carry the same fees, so this is the difference in
			// what actually reaches the wallet: zero at the target,
			// negative below it. It is not profit over cost and the basis
			// says so.
			target := row.TargetPrice
			basis = orderDeskMarginTarget
			reference = target
			marginAt = func(ask float64) float64 { return (ask - target) * proceedsMult }
		default:
			return
		}
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
	// Taken against the same reference as the before-margin, so the two are
	// the same measurement at two prices and can be shown side by side.
	//
	// On a buy row the reference is your *current* price rather than the
	// suggested one on purpose: "what this order is worth to me" is the
	// question, and re-basing the after figure on the new bid would flatter
	// exactly the move this change exists to question.
	row.SuggestedMarginPercent = row.SuggestedMarginUnitISK / reference * 100.0

	if row.MarginUnitISK > 0 && row.MarginPercent < opt.MinMarginPercent {
		row.WarnThinMargin = true
	}
	if row.SuggestedPrice > 0 && row.SuggestedPrice != row.Price &&
		row.SuggestedMarginUnitISK > 0 && row.SuggestedMarginPercent < opt.MinMarginPercent {
		row.WarnThinAfterReprice = true
	}
}

func orderDeskRecommendation(row OrderDeskOrder, opt OrderDeskOptions) (string, string) {
	if !row.BookAvailable {
		return "hold", "market book unavailable"
	}

	// A losing position outranks every liquidity verdict below. Filling
	// sooner is not an improvement when the fill is the problem, and
	// "hold — on track" is exactly the answer that made this necessary.
	if orderDeskMarginIsLoss(row.MarginBasis) && row.MarginUnitISK <= 0 {
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

	// A price you set by hand outranks anything read off the book, because
	// the book is what the desk can see and what an item is worth to you is
	// what it cannot.
	if action, reason, ok := orderDeskHoldingRuleVerdict(row); ok {
		return action, reason
	}

	// A parked bid is not waiting to fill, so none of the liquidity
	// verdicts apply to it: queue depth, ETA and undercut advice all
	// describe an order trying to be first, and this one is deliberately
	// last. Expiry is the exception — a parked bid still lapses, and
	// silently losing one is the failure mode this must not introduce.
	if row.IsLowball {
		if row.DaysToExpire >= 0 && row.DaysToExpire <= opt.WarnExpiryDays {
			return "review", fmt.Sprintf("parked bid expiring in %dd", row.DaysToExpire)
		}
		// A bid you declared patient is patient. One the desk inferred from
		// distance alone might instead be a bid the book ran away from, and
		// the price level is what tells them apart: a bid parked under a
		// stable market suggests a normal price, while a book that has run
		// away suggests one at the top of the item's year. The capital-jump
		// test cannot help here — a lowball's jump up to the book is large by
		// construction — so only the level test runs.
		if !row.PatientBid {
			if action, reason, ok := orderDeskPriceLevelVerdict(row); ok {
				return action, reason
			}
		}
		if row.BestPrice > 0 && row.Price > 0 {
			return "hold", fmt.Sprintf("parked bid: %.0f%% under best, not waiting to fill",
				(row.BestPrice-row.Price)/row.BestPrice*100)
		}
		return "hold", "parked bid, not waiting to fill"
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

	// Then: is this still a reprice at all? Everything above prices the
	// move per unit. These two ask what the move commits and where it
	// stands in the item's own history, which is how a bid can clear every
	// margin test and still be a bad buy.
	if action == "reprice" {
		if riskAction, riskReason, ok := orderDeskCapitalJumpVerdict(row, opt); ok {
			return riskAction, riskReason
		}
		if riskAction, riskReason, ok := orderDeskPriceLevelVerdict(row); ok {
			return riskAction, riskReason
		}
	}

	// Surviving reprices still say when the move makes the trade worse.
	if action == "reprice" && row.WarnThinAfterReprice {
		reason += fmt.Sprintf(" · margin drops to %+.1f%% after the move", row.SuggestedMarginPercent)
	}

	return action, reason
}

// orderDeskHoldingRuleVerdict applies the target price or bid ceiling you set
// by hand, from the same per-type holding rule Assets and Today read.
//
// The desk's advice is derived entirely from the book, so on a position you
// are deliberately holding — because you think the price rebounds, or because
// your cost was higher than the market's — it will tell you to cut, every day,
// with increasing confidence. Saying so once should be enough.
func orderDeskHoldingRuleVerdict(row OrderDeskOrder) (string, string, bool) {
	if row.SuggestedPrice <= 0 {
		return "", "", false
	}

	if !row.IsBuyOrder && row.TargetPrice > 0 && row.SuggestedPrice < row.TargetPrice {
		if row.BestPrice > 0 {
			return "hold", fmt.Sprintf("holding for %s — market %s, %.0f%% of the way",
				todayISK(row.TargetPrice), todayISK(row.BestPrice), row.TargetProgressPct), true
		}
		return "hold", fmt.Sprintf("holding for %s", todayISK(row.TargetPrice)), true
	}

	if row.IsBuyOrder && row.MaxBidPrice > 0 && row.SuggestedPrice > row.MaxBidPrice {
		return "hold", fmt.Sprintf("suggested %s is over your %s ceiling",
			todayISK(row.SuggestedPrice), todayISK(row.MaxBidPrice)), true
	}

	return "", "", false
}

// The two questions a reprice on a buy row has to answer beyond per-unit
// margin, kept apart because they are asked from different places.
//
// The desk's economics for a reprice was, until these existed, the price
// concession alone: delta times volume, less the broker fee. That is a
// complete account of what the move costs per unit and says nothing about the
// ISK it puts on the table. The case that prompted it: 100 units bid at 50k,
// the book now 500k, the best ask 600k. Margin at the new bid is
// 600k × 0.91 − 500k = 46k a unit, +9.2%, comfortably over any floor — while
// committing 45M ISK more than the order holds today, at a price near the top
// of everything the item has traded at all year. The spread is momentary; the
// price level is not.
//
// Both return "review" rather than "cancel" deliberately. The trade may still
// be right, and the row already has an expander that prices cut, hold and move.
//
// orderDeskCapitalJumpVerdict measures the move against your own price: how
// much further out of pocket following the book would put you.
//
// Note that at the default prefs this test and the lowball threshold are the
// same line — a bid needs raising by 25% exactly when it sits 20% under best —
// so at defaults the lowball path handles every move wide enough to trip this,
// and this one covers the window that opens when Lowball % is raised above it.
func orderDeskCapitalJumpVerdict(row OrderDeskOrder, opt OrderDeskOptions) (string, string, bool) {
	if !row.IsBuyOrder || row.Price <= 0 || row.SuggestedPrice <= 0 || row.AddedCapitalISK <= 0 {
		return "", "", false
	}
	jump := (row.SuggestedPrice - row.Price) / row.Price * 100.0
	if jump < opt.RepriceJumpPct {
		return "", "", false
	}
	return "review", fmt.Sprintf(
		"book moved +%.0f%%: this is a new buy at %s, not a reprice — %s more ISK committed",
		jump, todayISK(row.SuggestedPrice), todayISK(row.AddedCapitalISK)), true
}

// orderDeskPriceLevelVerdict measures the move against the item's own year
// instead: a price can be a small step from yours and still be near the most
// anyone has paid for the thing in twelve months.
func orderDeskPriceLevelVerdict(row OrderDeskOrder) (string, string, bool) {
	if !row.IsBuyOrder || row.SuggestedPrice <= 0 ||
		row.PercentileBasis != PercentileBasisHistory ||
		row.SuggestedPricePercentile < orderDeskRiskPercentile {
		return "", "", false
	}
	if row.AddedCapitalISK > 0 {
		return "review", fmt.Sprintf(
			"bidding %s would be the %.0fth percentile of its year — %s more ISK committed",
			todayISK(row.SuggestedPrice), row.SuggestedPricePercentile,
			todayISK(row.AddedCapitalISK)), true
	}
	return "review", fmt.Sprintf("would bid at the %.0fth percentile of its year",
		row.SuggestedPricePercentile), true
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

// orderDeskBidReaches reports whether a competing buy order can take units
// offered at myLocationID.
//
// EVE buy orders carry a reach. A region-range bid three systems away
// competes for everything a seller at your station wants to move; a
// station-range bid in the next building competes for none of it. The desk
// used to measure a buy row against its own station's book alone, which made
// every buy position and queue-ahead optimistic by however much of the region
// was bidding over it.
//
// Unrecognised and empty ranges read as station-only. That is the
// conservative direction: it reproduces the old behaviour rather than
// inventing competition out of a string nobody has seen before.
func orderDeskBidReaches(o esi.MarketOrder, myLocationID int64, mySystemID int32, jumps func(int32, int32) int) bool {
	if o.LocationID == myLocationID {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(o.Range)) {
	case "region":
		return true
	case "solarsystem":
		return mySystemID > 0 && o.SystemID == mySystemID
	case "station", "":
		return false
	}

	n, err := strconv.Atoi(strings.TrimSpace(o.Range))
	if err != nil || n < 0 {
		return false
	}
	if mySystemID <= 0 || o.SystemID <= 0 {
		return false
	}
	if o.SystemID == mySystemID {
		return true
	}
	if jumps == nil {
		return false
	}
	d := jumps(o.SystemID, mySystemID)
	return d >= 0 && d <= n
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
