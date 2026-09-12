package engine

import (
	"fmt"
	"math"
	"sort"
	"time"

	"eve-flipper/internal/esi"
)

// The three things you can do with stock whose sell order has gone
// underwater. They are not competing rules — they are three prices for the
// same ISK, quoted at the same future date, and the desk ranks them.
const (
	DispositionCut  = "cut"
	DispositionHold = "hold"
	DispositionMove = "move"
)

const (
	// Haul timing, from the freighter RouteExecutionProfile in
	// route_execution.go. Precision hardly matters — travel is minutes
	// against fill times measured in days — but the two should not
	// disagree about what a jump costs.
	dispositionDockMinutes    = 7.0
	dispositionMinutesPerJump = 3.6

	// Two plans this close apart are one plan with rounding error, and
	// picking a side would be false confidence.
	dispositionTooClosePct = 1.0

	// Cap on how many same-region alternatives are worth pricing. Beyond
	// the deepest few the rest are backwaters with a single hopeful order.
	DispositionMaxLocalVenues = 3
)

// DispositionVenue is one place the stock could end up. RegionOrders is
// that venue's whole region book for the type — the same shape
// ComputeOrderDesk already has in hand — because a station's share of
// regional flow is what makes its fill estimate meaningful.
type DispositionVenue struct {
	LocationID   int64
	LocationName string
	RegionID     int32

	// Jumps from the order's own station. Zero for home. A negative value
	// means the route could not be resolved, and the venue is dropped
	// rather than priced with a guessed haul cost.
	Jumps int

	RegionOrders []esi.MarketOrder
	History      []esi.HistoryEntry
}

// DispositionInput is everything needed to price the three plans. It is
// deliberately free of SDE and HTTP: the caller resolves jumps, unit
// volume and cost basis, so this stays testable against fixed numbers.
type DispositionInput struct {
	TypeID   int32
	TypeName string
	Qty      int64

	// CostBasisISK is the FIFO average unit cost of the held stock. Without
	// it there is no such thing as underwater and no plans are produced.
	CostBasisISK float64
	HeldSince    string

	Home      DispositionVenue
	Elsewhere []DispositionVenue

	// RecoveryOverride is a fit computed elsewhere from the full ~390-day
	// series, which is what this needs and what Home.History does not carry:
	// the raw history cache holds 90 days, and CalcRecoveryOutlook asks for a
	// 180-day window. Without this the verdict silently depended on whether
	// something else had warmed that cache -- 90 days on a hit, the full
	// series on a miss, so the same order could be told to hold or to cut
	// depending on scan order. Nil falls back to Home.History, which keeps
	// the engine usable standalone and keeps existing tests honest.
	RecoveryOverride *RecoveryOutlook

	UnitVolumeM3         float64
	ShipRateISKPerM3Jump float64

	SalesTaxPercent  float64
	BrokerFeePercent float64

	// The hurdle rate is derived from these two rather than being its own
	// setting: one normal trade cycle on this tab is MinMarginPercent over
	// TargetETADays, which is exactly what freed ISK could go and earn.
	MinMarginPercent float64
	TargetETADays    float64

	// Now is the calendar the fill walk starts from. Zero means now.
	Now time.Time
}

// DispositionPlan is one priced option. Every ISK figure is for the whole
// remaining quantity, not per unit, because the decision is about the
// position rather than about a unit of it.
type DispositionPlan struct {
	Kind        string `json:"kind"`
	Recommended bool   `json:"recommended"`

	Venue string `json:"venue,omitempty"`
	Jumps int    `json:"jumps,omitempty"`

	ExitPrice float64 `json:"exit_price"`
	GrossISK  float64 `json:"gross_isk"`
	HaulISK   float64 `json:"haul_isk,omitempty"`
	NetISK    float64 `json:"net_isk"`
	ProfitISK float64 `json:"profit_isk"`

	DaysToRealise float64 `json:"days_to_realise"`
	TerminalISK   float64 `json:"terminal_isk"`

	Notes []string `json:"notes,omitempty"`
}

// DispositionResult is the whole answer, including the parts that were
// refused. Plans may be empty; Reason then says why, and an empty list is
// never to be read as "there is nothing to do".
type DispositionResult struct {
	TypeID   int32  `json:"type_id"`
	TypeName string `json:"type_name,omitempty"`
	Qty      int64  `json:"qty"`

	CostBasisISK  float64 `json:"cost_basis_isk"`
	PositionISK   float64 `json:"position_isk"`
	HeldSince     string  `json:"held_since,omitempty"`
	UnitVolumeM3  float64 `json:"unit_volume_m3,omitempty"`
	HorizonDays   float64 `json:"horizon_days"`
	HurdlePctDay  float64 `json:"hurdle_pct_day"`
	VenuesPriced  int     `json:"venues_priced"`
	VenuesSkipped int     `json:"venues_skipped"`

	Recovery RecoveryOutlook `json:"recovery"`

	Plans    []DispositionPlan `json:"plans"`
	TooClose bool              `json:"too_close"`
	Reason   string            `json:"reason,omitempty"`
}

// ComputeOrderDisposition prices cutting, holding and moving a held
// position, and ranks them on what the ISK is worth at one common date.
//
// The common date is the slowest plan's own completion, so a plan that
// frees ISK early is credited with what that ISK earns in the meantime and
// a plan that ties it up is not. That is the whole reason the three
// questions collapse into one: "sell anyway to free up the ISK" only ever
// wins if freed ISK is actually worth something, and the hurdle rate is
// where that belief is written down.
//
// Holding is offered only when CalcRecoveryOutlook produces evidence. When
// it refuses, the hold plan is absent and Recovery.Reason says why — it is
// never replaced by an optimistic default.
func ComputeOrderDisposition(in DispositionInput) DispositionResult {
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	out := DispositionResult{
		TypeID:       in.TypeID,
		TypeName:     in.TypeName,
		Qty:          in.Qty,
		CostBasisISK: in.CostBasisISK,
		PositionISK:  in.CostBasisISK * float64(in.Qty),
		HeldSince:    in.HeldSince,
		UnitVolumeM3: in.UnitVolumeM3,
		Recovery:     RecoveryOutlook{Basis: RecoveryBasisNone},
	}

	if in.Qty <= 0 {
		out.Reason = "nothing left on this order to dispose of"
		return out
	}
	if in.CostBasisISK <= 0 {
		// Same contract as the desk's margin column: no cost basis is not a
		// free position, it is an unmeasured one, and every plan here is
		// quoted against cost.
		out.Reason = "no cost basis for this item — the wallet archive has no lots to price it against"
		return out
	}

	qty := float64(in.Qty)
	cost := in.CostBasisISK * qty
	taxOnly := feeMultiplier(in.SalesTaxPercent)
	listed := feeMultiplier(in.SalesTaxPercent + in.BrokerFeePercent)

	hurdleDaily := 0.0
	if in.TargetETADays > 0 && in.MinMarginPercent > 0 {
		hurdleDaily = in.MinMarginPercent / in.TargetETADays / 100
	}
	out.HurdlePctDay = hurdleDaily * 100

	var plans []DispositionPlan

	// --- CUT ---------------------------------------------------------
	// Hitting a standing buy order pays sales tax but no broker fee: you
	// are taking the order, not placing one. That fee asymmetry is real
	// and is often most of the gap between cutting and relisting.
	if bid := orderDeskBestPrice(venueStationSide(in.Home, true), true); bid > 0 {
		gross := bid * taxOnly * qty
		plans = append(plans, DispositionPlan{
			Kind:          DispositionCut,
			Venue:         in.Home.LocationName,
			ExitPrice:     bid,
			GrossISK:      gross,
			NetISK:        gross,
			ProfitISK:     gross - cost,
			DaysToRealise: 0,
			Notes: []string{fmt.Sprintf("hits the standing bid — %.2f%% sales tax, no broker fee",
				in.SalesTaxPercent)},
		})
	}

	// --- HOLD --------------------------------------------------------
	if in.RecoveryOverride != nil {
		out.Recovery = *in.RecoveryOverride
	} else {
		out.Recovery = CalcRecoveryOutlook(in.Home.History, 0)
	}
	if out.Recovery.Basis == RecoveryBasisHistory {
		// Once the price is back at trend we would be relisting near the
		// top of the book, so the fill walk is the queue-free one. Both
		// fees apply: recovering means cancelling this order and placing a
		// higher one.
		fill := dispositionFillDays(in.Home, in.Qty, now)
		gross := out.Recovery.TargetPrice * listed * qty
		plans = append(plans, DispositionPlan{
			Kind:          DispositionHold,
			Venue:         in.Home.LocationName,
			ExitPrice:     out.Recovery.TargetPrice,
			GrossISK:      gross,
			NetISK:        gross,
			ProfitISK:     gross - cost,
			DaysToRealise: out.Recovery.MedianDays + fill,
			Notes: []string{fmt.Sprintf("%d past dips this deep took a median %.0fd to return to trend",
				out.Recovery.Episodes, out.Recovery.MedianDays)},
		})
	}

	// --- MOVE --------------------------------------------------------
	best, priced, skipped := bestRelocation(in, now, qty, cost, taxOnly, listed)
	out.VenuesPriced = priced
	out.VenuesSkipped = skipped
	if best != nil {
		plans = append(plans, *best)
	}

	if len(plans) == 0 {
		out.Reason = "nothing to compare: no bid here, no evidence of a bounce, and nowhere better to take it"
		return out
	}

	// One horizon for all three, set by the slowest, so the comparison is
	// of the same ISK on the same date rather than of three different bets.
	horizon := 0.0
	for _, p := range plans {
		if p.DaysToRealise > horizon {
			horizon = p.DaysToRealise
		}
	}
	out.HorizonDays = horizon
	for i := range plans {
		idle := horizon - plans[i].DaysToRealise
		plans[i].TerminalISK = plans[i].NetISK * (1 + hurdleDaily*idle)
	}

	sort.SliceStable(plans, func(i, j int) bool {
		if plans[i].TerminalISK != plans[j].TerminalISK {
			return plans[i].TerminalISK > plans[j].TerminalISK
		}
		// Deterministic tiebreak, and the faster plan is the safer one when
		// the ISK is identical.
		return plans[i].DaysToRealise < plans[j].DaysToRealise
	})
	plans[0].Recommended = true

	if len(plans) > 1 && plans[0].TerminalISK > 0 {
		gap := (plans[0].TerminalISK - plans[1].TerminalISK) / plans[0].TerminalISK * 100
		out.TooClose = gap < dispositionTooClosePct
	}

	out.Plans = plans
	return out
}

// bestRelocation prices every reachable alternative and keeps the best one.
// Only one move plan is returned: the panel is a decision aid, and eight
// ranked hauls is a spreadsheet, not an answer.
func bestRelocation(in DispositionInput, now time.Time, qty, cost, taxOnly, listed float64) (best *DispositionPlan, priced, skipped int) {
	for _, v := range in.Elsewhere {
		if v.Jumps < 0 {
			// Unresolved route — an Upwell structure the SDE does not know,
			// or no highsec path at the configured minimum security.
			skipped++
			continue
		}
		plan, ok := relocationPlan(in, v, now, qty, cost, taxOnly, listed)
		if !ok {
			skipped++
			continue
		}
		priced++
		if best == nil || plan.TerminalISK > best.TerminalISK {
			// TerminalISK is not final yet — the horizon is not known until
			// every plan exists — so this ranks on NetISK, stashed there by
			// relocationPlan purely to pick a venue.
			p := plan
			best = &p
		}
	}
	if best != nil {
		best.TerminalISK = 0
	}
	return best, priced, skipped
}

// relocationPlan prices hauling to one venue and selling there, taking
// whichever of the two exits nets more: lifting the standing bid, or
// undercutting the ask and waiting.
func relocationPlan(in DispositionInput, v DispositionVenue, now time.Time, qty, cost, taxOnly, listed float64) (DispositionPlan, bool) {
	haul := in.ShipRateISKPerM3Jump * in.UnitVolumeM3 * qty * float64(v.Jumps)
	haulDays := (dispositionDockMinutes + dispositionMinutesPerJump*float64(v.Jumps)) / 1440

	bid := orderDeskBestPrice(venueStationSide(v, true), true)
	ask := orderDeskBestPrice(venueStationSide(v, false), false)

	var exit, gross, days float64
	var note string

	if ask > 0 {
		if listPrice := NextSellUndercut(ask); listPrice > 0 {
			exit = listPrice
			gross = listPrice * listed * qty
			days = haulDays + dispositionFillDays(v, in.Qty, now)
			note = fmt.Sprintf("one step under the %s ask", v.LocationName)
		}
	}
	if bid > 0 {
		if bidGross := bid * taxOnly * qty; bidGross > gross {
			exit = bid
			gross = bidGross
			days = haulDays
			note = fmt.Sprintf("the %s bid pays more than listing there does", v.LocationName)
		}
	}
	if gross <= 0 {
		return DispositionPlan{}, false
	}

	net := gross - haul
	return DispositionPlan{
		Kind:          DispositionMove,
		Venue:         v.LocationName,
		Jumps:         v.Jumps,
		ExitPrice:     exit,
		GrossISK:      gross,
		HaulISK:       haul,
		NetISK:        net,
		ProfitISK:     net - cost,
		DaysToRealise: days,
		// Ranking key while venues are compared; overwritten with the real
		// horizon-adjusted figure once every plan is known.
		TerminalISK: net,
		Notes: []string{
			note,
			fmt.Sprintf("%d jumps, %.0f m³ at %s ISK/m³/jump",
				v.Jumps, in.UnitVolumeM3*qty, trimISK(in.ShipRateISKPerM3Jump)),
		},
	}, true
}

// dispositionFillDays estimates how long it takes to sell qty at a venue
// when listed at the top of its book, reusing the desk's own fill model
// rather than a second opinion: regional daily volume, narrowed to the
// share that lifts sell orders, narrowed again to this station's share of
// competitive depth, then walked across the calendar so a weekend counts
// for what it is worth.
//
// Listing one step under the best ask puts us at position one, so there is
// no queue ahead to clear — the whole quantity is our own.
func dispositionFillDays(v DispositionVenue, qty int64, now time.Time) float64 {
	if qty <= 0 {
		return 0
	}
	avgDaily := orderDeskAvgDailyVolume(v.History, orderDeskFlowBaseDays)
	if avgDaily <= 0 {
		return float64(orderDeskETACapDays)
	}
	bid := orderDeskBestPrice(venueStationSide(v, true), true)
	ask := orderDeskBestPrice(venueStationSide(v, false), false)
	sellShare := orderDeskSellSideShare(bid, ask, orderDeskRecentAvgPrice(v.History, orderDeskFlowBaseDays))
	stationShare := orderDeskStationShare(venueRegionSide(v, false), v.LocationID, false)

	dow, _ := orderDeskDowProfile(v.History, orderDeskDowWeeks)
	days, _ := orderDeskWalkDays(float64(qty), avgDaily*sellShare*stationShare, dow, now)
	return days
}

// stationSide picks one side of one station's book out of the region book.
func venueStationSide(v DispositionVenue, isBuy bool) []esi.MarketOrder {
	out := make([]esi.MarketOrder, 0, 8)
	for _, o := range v.RegionOrders {
		if o.LocationID == v.LocationID && o.IsBuyOrder == isBuy {
			out = append(out, o)
		}
	}
	return out
}

// regionSide picks one side of the whole region's book, which is the
// denominator orderDeskStationShare measures a station against.
func venueRegionSide(v DispositionVenue, isBuy bool) []esi.MarketOrder {
	out := make([]esi.MarketOrder, 0, len(v.RegionOrders))
	for _, o := range v.RegionOrders {
		if o.IsBuyOrder == isBuy {
			out = append(out, o)
		}
	}
	return out
}

// feeMultiplier turns a percentage of fees into what is left of a sale,
// floored at zero so an absurd fee setting cannot produce negative
// proceeds and invert every comparison downstream.
func feeMultiplier(pct float64) float64 {
	m := 1 - pct/100
	if m < 0 {
		return 0
	}
	return m
}

// trimISK renders a rate compactly for a note without dragging in the
// frontend's formatting rules.
func trimISK(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.2f", v)
}
