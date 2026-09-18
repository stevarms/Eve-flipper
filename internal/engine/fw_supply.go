package engine

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"eve-flipper/internal/esi"
)

// MinKillsWithItem is how many separate losses must have carried an item before
// it is worth shipping.
//
// Three, because a rate estimated from one killmail is not a rate. The militia
// analyzer deliberately does not apply this -- it measures, and an item on one
// loss is a real observation with a bad rate -- so the gate lives here, and the
// count stays on the row so thin evidence reads as thin rather than vanishing.
const MinKillsWithItem = 3

// Cover verdicts. Cover is stocked units over units destroyed per day, which is
// the quantitative form of "there are already 500 Tristans there": 500 hulls
// against 3 a day is 166 days of cover and nothing to add, while 2 hulls against
// the same 3 a day is 0.7 days and the gap the tool exists to find.
const (
	// VerdictGap is under the cover target: ship this.
	VerdictGap = "gap"
	// VerdictThin is between the target and CoveredMultiple times it: top up,
	// but do not lead the shipping list with it.
	VerdictThin = "thin"
	// VerdictCovered is at or past CoveredMultiple times the target. Do not ship.
	VerdictCovered = "covered"
	// VerdictUnpriceable is an item that cannot be sold profitably at all: no
	// Jita sell order to buy from, a non-gouging ceiling below what freight
	// costs, or a competitor already selling under our landed cost.
	VerdictUnpriceable = "unpriceable"
)

// Which rule in the ladder chose the price. Every row names one, because "1.09x"
// on its own does not say whether it was a reference price into an empty book or
// an undercut of someone resting below it -- and the gap between the two is the
// honest measure of how contested the item is.
const (
	// PriceRuleReference is the band-derived reference: an empty book, or a
	// competitor resting above it who must not drag the price up.
	PriceRuleReference = "reference"
	// PriceRuleUndercut goes under a competitor whose depth is material.
	PriceRuleUndercut = "undercut"
	// PriceRuleStepOver holds the reference over a competitor too shallow to
	// matter, and waits for their few units to clear.
	PriceRuleStepOver = "step_over"
	// PriceRuleNone is set when no price could be reached at all.
	PriceRuleNone = "none"
)

// Defaults. Cover target and the covered multiple are the plan's; the step-over
// threshold is a campaign setting exposed here so the boundary is pinned by a
// test rather than by whatever a caller happens to pass.
const (
	DefaultTargetCoverDays   = 7.0
	DefaultCoveredMultiple   = 2.0
	DefaultMinMarginPct      = 10.0
	DefaultStepOverDaysCover = 0.5
	// DefaultSellFeePct is the combined broker-plus-tax fallback, the same 8%
	// WarTracker has always assumed. It is deliberately pessimistic: the Order
	// Desk resolves real per-character rates, and this only stands in when they
	// are not available.
	DefaultSellFeePct = 8.0
)

// Category markup ceilings: the most a category may be marked up however
// generous the station's own book looks.
//
// Measured. Observed local/Jita ratios at Villasen and Rakapas collapse from
// 2.7-6x on sub-10k consumables to 1.70x above 10M, so the tolerance is a tail
// effect and the ceiling has to be per category rather than one number. 1.30 for
// hulls is where the distribution flattens and is also the number the trader
// gave me independently.
func DefaultCategoryCeilings() map[string]float64 {
	return map[string]float64{
		"ship":       1.30,
		"module":     1.60,
		"ammo":       2.00,
		"drone":      2.00,
		"consumable": 2.00,
	}
}

// minBandSamples is how many types a price band needs before its percentiles
// mean anything.
//
// Five. The calibration bands held 30-127 types each, so this only binds where a
// station genuinely has almost no overlapping book -- and there the category
// ceiling is the honest answer, not a median of three ratios.
const minBandSamples = 5

// markupBandCeilings are the upper bounds of the Jita-price bands the ladder is
// calibrated in, matching the buckets the ratios were measured in.
//
// The top band is MaxFloat64 rather than +Inf, which is what it means. The
// ladder is serialised into the plan cache, and encoding/json refuses to write
// an infinity -- refuses by failing the entire document, so one unbounded field
// took the whole plan with it. Every real ISK price is below MaxFloat64, so
// bandIndex is unchanged.
var markupBandCeilings = []float64{1e3, 1e4, 1e5, 1e6, 1e7, math.MaxFloat64}

// CoverDays is days of stock at the destination, measured against destruction.
//
// It is +Inf when nothing measurable is being destroyed, which is a real answer
// and the reason the type exists: encoding/json cannot write an infinity, and it
// fails the whole document rather than the one field. So the wire form of
// unbounded cover is null -- the absence of a rate is an absence, not a very
// large number -- and null reads back as +Inf, because a cached plan that said
// zero would turn "already covered forever" into what looks like a gap.
type CoverDays float64

// Unbounded reports cover with no destruction to measure it against.
func (c CoverDays) Unbounded() bool {
	return math.IsInf(float64(c), 1) || math.IsNaN(float64(c))
}

func (c CoverDays) MarshalJSON() ([]byte, error) {
	if c.Unbounded() {
		return []byte("null"), nil
	}
	return json.Marshal(float64(c))
}

func (c *CoverDays) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*c = CoverDays(math.Inf(1))
		return nil
	}
	var v float64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*c = CoverDays(v)
	return nil
}

// MarkupBand is one price band's observed behaviour at one station.
type MarkupBand struct {
	MaxJitaPrice float64 `json:"max_jita_price"`
	Samples      int     `json:"samples"`
	Median       float64 `json:"median"`
	P75          float64 `json:"p75"`
}

// Calibrated reports whether the band has enough samples to price from.
func (b MarkupBand) Calibrated() bool { return b.Samples >= minBandSamples }

// MarkupLadder is what a station's own customers already pay, by price band.
//
// Derived live from the station's book rather than stored, so it tracks the
// market instead of ageing. It is why "what the neighbours charge" is both the
// non-gouging answer and the empirical one -- and why Onnamon's median markup
// (1.33x) and Villasen's (1.19x) are allowed to differ.
type MarkupLadder struct {
	StationID int64        `json:"station_id"`
	Bands     []MarkupBand `json:"bands"`
}

// DeriveMarkupLadder measures local-over-Jita sell ratios at one station, in
// price bands.
//
// Only types where both books exist can produce a ratio, and only types the
// warzone is destroying are relevant -- a station's ratio on mining crystals says
// nothing about what militia pay for missiles. NPC seeds are excluded, because a
// 365-day order at three billion is not a price anyone paid.
func DeriveMarkupLadder(stationID int64, localOrders []esi.MarketOrder, jitaBestSell map[int32]float64, destroyedTypes map[int32]bool) MarkupLadder {
	best := make(map[int32]float64)
	for _, order := range localOrders {
		if order.LocationID != stationID || order.IsNPCSeeded() || !(order.Price > 0) {
			continue
		}
		if len(destroyedTypes) > 0 && !destroyedTypes[order.TypeID] {
			continue
		}
		if current, ok := best[order.TypeID]; !ok || order.Price < current {
			best[order.TypeID] = order.Price
		}
	}

	ratios := make([][]float64, len(markupBandCeilings))
	for typeID, localPrice := range best {
		jita := jitaBestSell[typeID]
		if !(jita > 0) {
			continue
		}
		band := bandIndex(jita)
		ratios[band] = append(ratios[band], localPrice/jita)
	}

	ladder := MarkupLadder{StationID: stationID, Bands: make([]MarkupBand, len(markupBandCeilings))}
	for i, ceiling := range markupBandCeilings {
		values := ratios[i]
		sort.Float64s(values)
		ladder.Bands[i] = MarkupBand{
			MaxJitaPrice: ceiling,
			Samples:      len(values),
			Median:       percentileFloat(values, 50),
			P75:          percentileFloat(values, 75),
		}
	}
	return ladder
}

// Band returns the band a Jita price falls in.
func (l MarkupLadder) Band(jitaPrice float64) MarkupBand {
	if len(l.Bands) == 0 {
		return MarkupBand{}
	}
	i := bandIndex(jitaPrice)
	if i >= len(l.Bands) {
		i = len(l.Bands) - 1
	}
	return l.Bands[i]
}

func bandIndex(price float64) int {
	for i, ceiling := range markupBandCeilings {
		if price < ceiling {
			return i
		}
	}
	return len(markupBandCeilings) - 1
}

// percentileFloat is a nearest-rank percentile over an already-sorted slice.
//
// One helper for both the median and p75 so the two cannot disagree about
// interpolation, which on a 30-sample band would move the reference price by
// more than the margin being argued over.
func percentileFloat(sorted []float64, pct float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := pct / 100 * float64(len(sorted)-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if hi >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	if lo == hi {
		return sorted[lo]
	}
	frac := rank - float64(lo)
	return sorted[lo] + (sorted[hi]-sorted[lo])*frac
}

// FWSupplyItem is one candidate type, as the demand and market layers deliver it.
type FWSupplyItem struct {
	TypeID   int32
	TypeName string
	// Category is one of categorizeItem's outputs: ship, module, ammo, drone,
	// consumable. It selects the markup ceiling.
	Category string
	VolumeM3 float64

	// DailyDestroyed is the winsorized destruction rate inside the warzone, and
	// KillsWithItem how many separate losses carried it. Both are the short
	// window -- seven days, which is zkillboard's pastSeconds ceiling and what
	// "this week" means.
	DailyDestroyed float64
	KillsWithItem  int

	// DailyDestroyedLong and KillsWithItemLong are the same two measurements over
	// the campaign's long window, and both are zero when no long window is set.
	// They exist to be read beside the short pair rather than instead of it: a
	// rate over a week and the same rate over a quarter is what separates a spike
	// from a staple, and one number cannot.
	DailyDestroyedLong float64
	KillsWithItemLong  int

	JitaBestSell float64

	// LocalOrders is the destination station's sell book for this type, as ESI
	// returned it. Filtering seeds out is this package's job, not the caller's,
	// so that stock, competition depth and calibration cannot end up applying
	// three different definitions of a competitor.
	LocalOrders []esi.MarketOrder

	// Included is the campaign's judgment overriding the model's for this one
	// type: it relaxes the thin-evidence Shippable gate and the one pricing
	// refusal driven by a competitor's depth rather than by economics. It does
	// not invent a rate -- a type with nothing measured in either window still
	// sizes to zero, Included or not, because that would be fabricating demand
	// with real ISK on the line.
	Included bool
}

// FWSupplyConfig is the campaign's settings. Zero fields take documented
// defaults, so a partially configured campaign behaves rather than dividing by
// zero.
type FWSupplyConfig struct {
	DestStationID int64

	TargetCoverDays float64
	CoveredMultiple float64

	MinMarginPct     float64
	FreightISKPerM3  float64
	SalesTaxPercent  float64
	BrokerFeePercent float64

	StepOverDaysCover float64
	CategoryCeilings  map[string]float64
	Ladder            MarkupLadder

	// SizeAgainstLong drives cover, quantities and the step-over test off the
	// long window's rate instead of the short one. It is a choice about which
	// measurement to trust, not about which to show -- both rates appear on every
	// row either way.
	SizeAgainstLong bool
}

func (c FWSupplyConfig) withDefaults() FWSupplyConfig {
	if c.TargetCoverDays <= 0 {
		c.TargetCoverDays = DefaultTargetCoverDays
	}
	if c.CoveredMultiple <= 1 {
		c.CoveredMultiple = DefaultCoveredMultiple
	}
	if c.MinMarginPct <= 0 {
		c.MinMarginPct = DefaultMinMarginPct
	}
	if c.StepOverDaysCover <= 0 {
		c.StepOverDaysCover = DefaultStepOverDaysCover
	}
	if c.SalesTaxPercent <= 0 && c.BrokerFeePercent <= 0 {
		c.BrokerFeePercent = DefaultSellFeePct
	}
	if len(c.CategoryCeilings) == 0 {
		c.CategoryCeilings = DefaultCategoryCeilings()
	}
	return c
}

// ceilingFor returns the markup ceiling for a category.
//
// An unrecognised category gets the strictest ceiling configured, not a generous
// default: guessing high is the gouging direction, and an item we cannot classify
// is not one to be adventurous about.
// keepRate is the share of a sale that survives broker fee and sales tax. Both
// land on the sale, so they scale with the price rather than adding to the cost
// -- which is why the floor divides by this and the margin multiplies by it.
func (c FWSupplyConfig) keepRate() float64 {
	return 1 - (c.SalesTaxPercent+c.BrokerFeePercent)/100
}

func (c FWSupplyConfig) ceilingFor(category string) float64 {
	if ceiling, ok := c.CategoryCeilings[category]; ok && ceiling > 0 {
		return ceiling
	}
	strictest := 0.0
	for _, ceiling := range c.CategoryCeilings {
		if ceiling > 0 && (strictest == 0 || ceiling < strictest) {
			strictest = ceiling
		}
	}
	return strictest
}

// FWSupplyRow is one line of the gap table: what is being destroyed, what is
// already there, what to charge, and how much to send.
type FWSupplyRow struct {
	TypeID   int32   `json:"type_id"`
	TypeName string  `json:"type_name"`
	Category string  `json:"category"`
	VolumeM3 float64 `json:"volume_m3"`

	// The short window's rate and evidence count, then the long window's -- both
	// on every row, both zero-valued when no long window is set. They are shown
	// side by side rather than blended: a weighted rate would hide the very
	// spike-versus-staple difference the second window was added to expose.
	DailyDestroyed     float64 `json:"daily_destroyed"`
	KillsWithItem      int     `json:"kills_with_item"`
	DailyDestroyedLong float64 `json:"daily_destroyed_long"`
	KillsWithItemLong  int     `json:"kills_with_item_long"`
	// SizedBy is "short" or "long": which of the two rates set the cover, the
	// verdict and the quantity on this row. Usually it is the campaign's
	// size_against setting, and it differs from it exactly when the chosen window
	// measured nothing for this item and the other one did.
	SizedBy string `json:"sized_by"`
	// Shippable is false for an item seen on fewer than MinKillsWithItem losses.
	// The row still appears, with its counts, so thin evidence is visible instead
	// of silently ranked or silently dropped.
	Shippable bool `json:"shippable"`
	// Included is the campaign's judgment overriding the model's for this type:
	// thin evidence and a competitor's depth stopped being reasons to withhold
	// it. See FWSupplyItem.Included for what it does and, as importantly, what
	// it deliberately does not do.
	Included bool `json:"included"`

	// StockedQty and DaysOfCover count player units only. A million NPC-seeded
	// rounds would otherwise read as infinite cover and hide a real gap.
	StockedQty      int64     `json:"stocked_qty"`
	DaysOfCover     CoverDays `json:"days_of_cover"`
	LocalBestSell   float64   `json:"local_best_sell"`
	LocalOrderCount int       `json:"local_order_count"`

	JitaBestSell float64 `json:"jita_best_sell"`
	// LandedCost is what a unit costs delivered, before any fee on the sale.
	LandedCost float64 `json:"landed_cost"`
	// FloorPrice is the lowest price that still clears LandedCost, the fees on
	// the sale and MinMarginPct. Nothing is ever priced below it.
	FloorPrice float64 `json:"floor_price"`

	// ReferencePrice is what may be charged into an empty book: the tightest of
	// the band median, the band p75 and the category ceiling. It is a ceiling and
	// a gouging guard -- never a price we are entitled to.
	ReferencePrice  float64 `json:"reference_price"`
	ReferenceMarkup float64 `json:"reference_markup"`
	// ReferenceSource names what set the reference: this station's own median in
	// this price band, or the category ceiling when the band is uncalibrated.
	ReferenceSource string  `json:"reference_source"`
	SuggestedPrice  float64 `json:"suggested_price"`
	SuggestedMarkup float64 `json:"suggested_markup"`
	PriceRule       string  `json:"price_rule"`
	PriceReason     string  `json:"price_reason"`

	// Competition resting below the reference: what the step-over test weighs.
	CompetingUnitsBelow  int64 `json:"competing_units_below"`
	CompetingOrdersBelow int   `json:"competing_orders_below"`

	Verdict       string `json:"verdict"`
	VerdictReason string `json:"verdict_reason"`

	SuggestedQty int64   `json:"suggested_qty"`
	CargoM3      float64 `json:"cargo_m3"`
	CostISK      float64 `json:"cost_isk"`

	// CoverSizedQty is what the cover model alone asked for, kept beside
	// SuggestedQty so a raised lot shows both numbers rather than replacing the
	// smaller one. QtyReason names the raise and the cover it implies; both are
	// empty when the floor did not bite.
	CoverSizedQty int64  `json:"cover_sized_qty"`
	QtyReason     string `json:"qty_reason"`

	// NetUnitISK is what a unit actually brings in: SuggestedPrice less the
	// broker fee and sales tax that come off the sale.
	NetUnitISK float64 `json:"net_unit_isk"`
	// UnitProfitISK is NetUnitISK less LandedCost -- freight in, fees out.
	UnitProfitISK float64 `json:"unit_profit_isk"`
	// MarginPct takes UnitProfitISK against LandedCost, the capital a unit ties
	// up. That is the same basis the Order Desk uses for a sell row, so a number
	// here and a number there mean the same thing, and it is the basis
	// MinMarginPct is defined in -- a row priced exactly at its floor reports
	// exactly MinMarginPct.
	MarginPct float64 `json:"margin_pct"`
	// ProfitISK is UnitProfitISK across SuggestedQty: what this row earns if it
	// all sells. Zero for a row with nothing to ship, which is not the same as a
	// row that earns nothing -- read it beside SuggestedQty.
	ProfitISK float64 `json:"profit_isk"`
}

// applyFWMargin fills the profit fields from whatever price the ladder chose.
//
// A row without a price leaves them at zero. An unpriceable item earns nothing
// because it is not being shipped, and reporting a margin against a price we
// refused to name would make a refusal look like an opportunity.
func applyFWMargin(row *FWSupplyRow, cfg FWSupplyConfig) {
	if !(row.SuggestedPrice > 0) || !(row.LandedCost > 0) {
		return
	}
	keepRate := cfg.keepRate()
	if keepRate <= 0 {
		return
	}
	row.NetUnitISK = row.SuggestedPrice * keepRate
	row.UnitProfitISK = row.NetUnitISK - row.LandedCost
	row.MarginPct = row.UnitProfitISK / row.LandedCost * 100
	row.ProfitISK = row.UnitProfitISK * float64(row.SuggestedQty)
}

// CoverDeficit is how many days short of the target this item is. It is the
// ranking key for trimming a shipping list to a budget: the biggest holes first.
func (r FWSupplyRow) CoverDeficit(targetCoverDays float64) float64 {
	return math.Max(0, targetCoverDays-float64(r.DaysOfCover))
}

// sellLevel is the destination book aggregated to one entry per price.
type sellLevel struct {
	price float64
	qty   int64
	count int
}

// aggregateLocalBook reduces one type's sell orders at one station to price
// levels, player orders only, cheapest first.
func aggregateLocalBook(orders []esi.MarketOrder, stationID int64) []sellLevel {
	byPrice := make(map[float64]*sellLevel)
	for _, order := range orders {
		if stationID != 0 && order.LocationID != stationID {
			continue
		}
		if order.IsNPCSeeded() || !(order.Price > 0) || order.VolumeRemain <= 0 {
			continue
		}
		level := byPrice[order.Price]
		if level == nil {
			level = &sellLevel{price: order.Price}
			byPrice[order.Price] = level
		}
		level.qty += int64(order.VolumeRemain)
		level.count++
	}

	levels := make([]sellLevel, 0, len(byPrice))
	for _, level := range byPrice {
		levels = append(levels, *level)
	}
	sort.Slice(levels, func(i, j int) bool { return levels[i].price < levels[j].price })
	return levels
}

// npcFloorPrice is the cheapest NPC-seeded order at the station, or zero.
//
// Seeds are excluded from depth and calibration because they never reprice, but
// one resting under our landed cost is still a wall we cannot sell through, and
// pretending it is not there would recommend a shipment that cannot clear.
func npcFloorPrice(orders []esi.MarketOrder, stationID int64) float64 {
	best := 0.0
	for _, order := range orders {
		if stationID != 0 && order.LocationID != stationID {
			continue
		}
		if !order.IsNPCSeeded() || !(order.Price > 0) || order.VolumeRemain <= 0 {
			continue
		}
		if best == 0 || order.Price < best {
			best = order.Price
		}
	}
	return best
}

// BuildFWSupplyPlan turns candidate types into the gap table: cover verdicts,
// prices and quantities, ranked so that trimming to a budget takes the biggest
// holes first.
//
// Quantities here are sized to the cover target only. Trimming against the
// campaign budget and against cargo is a separate pass, because both are
// properties of the shipment rather than of the item.
func BuildFWSupplyPlan(items []FWSupplyItem, cfg FWSupplyConfig) []FWSupplyRow {
	cfg = cfg.withDefaults()

	rows := make([]FWSupplyRow, 0, len(items))
	for _, item := range items {
		rows = append(rows, buildFWSupplyRow(item, cfg))
	}

	// gap before thin before everything else, then the biggest cover hole first.
	// This is the order §6 trims in, so the ranking and the budget agree.
	rank := map[string]int{VerdictGap: 0, VerdictThin: 1, VerdictUnpriceable: 2, VerdictCovered: 3}
	sort.Slice(rows, func(i, j int) bool {
		if rank[rows[i].Verdict] != rank[rows[j].Verdict] {
			return rank[rows[i].Verdict] < rank[rows[j].Verdict]
		}
		di, dj := rows[i].CoverDeficit(cfg.TargetCoverDays), rows[j].CoverDeficit(cfg.TargetCoverDays)
		if di != dj {
			return di > dj
		}
		return rows[i].TypeID < rows[j].TypeID
	})
	return rows
}

// FWSizedByShort and FWSizedByLong name which window drove a row, so the table
// can mark the column that decided the quantity rather than leaving a reader to
// infer it from two numbers and a setting.
const (
	FWSizedByShort = "short"
	FWSizedByLong  = "long"
)

// sizingDemand picks the destruction rate that drives cover, quantities and the
// step-over test, together with the evidence count MinKillsWithItem is judged
// against -- since 3 kills in 90 days is a thinner signal than 3 in 7, the count
// has to come from the same window as the rate.
//
// The configured window wins, with one exception in each direction: a window that
// measured nothing, while the other measured something, falls back to the other.
// That is what makes the union of the two windows mean anything. A staple that
// happened not to die this week is the whole reason the long window exists, and
// sizing it against a zero rate would give it unbounded cover and file it as
// covered -- reading a zero as "already stocked" is the silent failure here, and
// the only one of the two directions that quietly costs a sale.
func (i FWSupplyItem) sizingDemand(againstLong bool) (rate float64, kills int, sizedBy string) {
	if againstLong {
		if i.DailyDestroyedLong > 0 || !(i.DailyDestroyed > 0) {
			return i.DailyDestroyedLong, i.KillsWithItemLong, FWSizedByLong
		}
		return i.DailyDestroyed, i.KillsWithItem, FWSizedByShort
	}
	if !(i.DailyDestroyed > 0) && i.DailyDestroyedLong > 0 {
		return i.DailyDestroyedLong, i.KillsWithItemLong, FWSizedByLong
	}
	return i.DailyDestroyed, i.KillsWithItem, FWSizedByShort
}

func buildFWSupplyRow(item FWSupplyItem, cfg FWSupplyConfig) FWSupplyRow {
	rate, kills, sizedBy := item.sizingDemand(cfg.SizeAgainstLong)
	row := FWSupplyRow{
		TypeID:             item.TypeID,
		TypeName:           item.TypeName,
		Category:           item.Category,
		VolumeM3:           item.VolumeM3,
		DailyDestroyed:     item.DailyDestroyed,
		KillsWithItem:      item.KillsWithItem,
		DailyDestroyedLong: item.DailyDestroyedLong,
		KillsWithItemLong:  item.KillsWithItemLong,
		SizedBy:            sizedBy,
		// Included skips the thin-evidence gate; it does not invent a rate, so a
		// row with nothing measured is still not shippable -- kills stays 0 either
		// way, and 0 >= MinKillsWithItem is false regardless of the override.
		Shippable:    item.Included || kills >= MinKillsWithItem,
		Included:     item.Included,
		JitaBestSell: item.JitaBestSell,
		PriceRule:    PriceRuleNone,
	}

	levels := aggregateLocalBook(item.LocalOrders, cfg.DestStationID)
	for _, level := range levels {
		row.StockedQty += level.qty
		row.LocalOrderCount += level.count
	}
	if len(levels) > 0 {
		row.LocalBestSell = levels[0].price
	}

	// Cover first, because it decides whether the item is worth pricing at all.
	switch {
	case rate > 0:
		row.DaysOfCover = CoverDays(float64(row.StockedQty) / rate)
	default:
		row.DaysOfCover = CoverDays(math.Inf(1))
	}

	coveredAt := cfg.TargetCoverDays * cfg.CoveredMultiple
	switch {
	case float64(row.DaysOfCover) >= coveredAt:
		row.Verdict = VerdictCovered
		if row.DaysOfCover.Unbounded() {
			row.VerdictReason = "nothing measurable is being destroyed"
		} else {
			row.VerdictReason = fmt.Sprintf("%.0f days of cover against %.0f destroyed a day; already stocked",
				row.DaysOfCover, rate)
		}
	case float64(row.DaysOfCover) >= cfg.TargetCoverDays:
		row.Verdict = VerdictThin
		row.VerdictReason = fmt.Sprintf("%.1f days of cover against a %.0f-day target; top up, do not lead",
			row.DaysOfCover, cfg.TargetCoverDays)
	default:
		row.Verdict = VerdictGap
		row.VerdictReason = fmt.Sprintf("%.1f days of cover against %.0f destroyed a day",
			row.DaysOfCover, rate)
	}

	// A covered item is priced for information but never becomes unpriceable:
	// we are not shipping it either way, and "already stocked 86 days deep" is a
	// more useful answer than "we could not price it".
	priced := priceFWSupplyRow(&row, item, cfg, levels)
	if !priced && row.Verdict != VerdictCovered {
		row.Verdict = VerdictUnpriceable
		row.VerdictReason = row.PriceReason
	}

	if row.Verdict == VerdictGap || row.Verdict == VerdictThin {
		deficit := (cfg.TargetCoverDays - float64(row.DaysOfCover)) * rate
		if deficit > 0 && !math.IsInf(deficit, 0) && !math.IsNaN(deficit) {
			row.SuggestedQty = int64(math.Round(deficit))
		}
		applyFWMinLot(&row, item, rate)
		row.CargoM3 = float64(row.SuggestedQty) * item.VolumeM3
		row.CostISK = float64(row.SuggestedQty) * item.JitaBestSell
	}

	applyFWMargin(&row, cfg)
	return row
}

// FWMinLotBands is the minimum viable lot by unit price: below UnderISK, do not
// ship fewer than Floor. Chosen, not derived -- a measured version would need
// sales data at the destination, which is what this tool is being built to
// create. The bands normalize on capital rather than count: 50 units under 100k
// and 5 units under 5m are both roughly 5-25M of stock on the shelf.
//
// Boundaries are strict <, so 100,000 exactly falls in the 30-item band.
var FWMinLotBands = []struct {
	UnderISK float64
	Floor    int64
}{
	{100_000, 50},
	{1_000_000, 30},
	{1_500_000, 15},
	{5_000_000, 5},
}

// fwFloorStretch caps how far a floor may carry a line past what destruction
// justifies.
//
// The floor exists because destruction is a proxy for demand, not demand itself:
// two Miner I dying in the warzone does not mean only two would sell, since
// miners get bought at a staging hub by people who then do not die in them. But
// a floor only ever bites on the slowest movers, so an absolute one would stretch
// 2 units to 50 and park months of cover in exactly the stock the warzone
// consumes slowest. 10x is the compromise: the band is the minimum viable lot,
// this is the ceiling on believing it.
const fwFloorStretch = 10

// fwMinLotFloor is the band floor for a unit price, or 0 above every band.
func fwMinLotFloor(unitPrice float64) int64 {
	if !(unitPrice > 0) || math.IsInf(unitPrice, 0) || math.IsNaN(unitPrice) {
		return 0
	}
	for _, band := range FWMinLotBands {
		if unitPrice < band.UnderISK {
			return band.Floor
		}
	}
	return 0
}

// applyFWMinLot raises a lot to the minimum worth shipping, and says so.
//
// Two lines it will not cross. A quantity of zero stays zero -- the cover model
// declining to want an item is not something a floor may overrule, and the
// shipment gate reads SuggestedQty <= 0 -- so nothing new enters the buy list
// here. And the raise never exceeds fwFloorStretch times what cover asked for.
func applyFWMinLot(row *FWSupplyRow, item FWSupplyItem, rate float64) {
	sized := row.SuggestedQty
	row.CoverSizedQty = sized
	if sized <= 0 {
		return
	}
	floor := fwMinLotFloor(item.JitaBestSell)
	if floor <= sized {
		return
	}
	raised := floor
	if capped := sized * fwFloorStretch; capped < raised {
		raised = capped
	}
	if raised <= sized {
		return
	}
	row.SuggestedQty = raised

	// Both edges of the bet, in words: what destruction asked for, what the
	// floor asked for, which one won, and how long the raised lot would take to
	// sell at the rate that sized it. A stretched lot is a deliberate wager that
	// destruction understates demand, and it should read as one.
	cover := "cover beyond measurement"
	if rate > 0 {
		cover = fmt.Sprintf("%.0f days of cover at %.2f destroyed/day",
			float64(raised)/rate, rate)
	}
	capNote := ""
	if raised < floor {
		capNote = fmt.Sprintf(", capped at %dx demand", fwFloorStretch)
	}
	row.QtyReason = fmt.Sprintf("sized %d by cover; raised to %d -- the %d-item floor for items under %s ISK%s -- %s",
		sized, raised, floor, fwBandLabel(item.JitaBestSell), capNote, cover)
}

// fwBandLabel names the band a price falls in the way the bands are written --
// "100k", "1.5m" -- so the reason reads back as the setting it came from.
func fwBandLabel(unitPrice float64) string {
	ceiling := 0.0
	for _, band := range FWMinLotBands {
		if unitPrice < band.UnderISK {
			ceiling = band.UnderISK
			break
		}
	}
	switch {
	case ceiling >= 1_000_000:
		return strings.TrimSuffix(strconv.FormatFloat(ceiling/1_000_000, 'f', -1, 64), ".0") + "m"
	case ceiling >= 1_000:
		return strings.TrimSuffix(strconv.FormatFloat(ceiling/1_000, 'f', -1, 64), ".0") + "k"
	default:
		return strconv.FormatFloat(ceiling, 'f', -1, 64)
	}
}

// priceFWSupplyRow walks the ladder in a fixed order and reports whether a price
// was reached. It fills PriceReason with the refusal when one was not.
//
// The order is the whole point: the floor is absolute, the reference is a ceiling
// that may never hold a price up, and live competition wins whenever it sits
// below the reference with depth behind it.
func priceFWSupplyRow(row *FWSupplyRow, item FWSupplyItem, cfg FWSupplyConfig, levels []sellLevel) bool {
	if !(item.JitaBestSell > 0) {
		row.PriceReason = "no Jita sell order to buy from"
		return false
	}

	// 1. The hard floor. Freight is charged on the way in; broker and tax come
	// off the sale, so they scale with the price rather than adding to the cost.
	keepRate := cfg.keepRate()
	if keepRate <= 0 {
		row.PriceReason = fmt.Sprintf("fees of %.1f%% consume the whole sale", cfg.SalesTaxPercent+cfg.BrokerFeePercent)
		return false
	}
	row.LandedCost = item.JitaBestSell + cfg.FreightISKPerM3*item.VolumeM3
	row.FloorPrice = row.LandedCost / keepRate * (1 + cfg.MinMarginPct/100)

	// 2. The reference: the lower of what this station's own customers pay in this
	// price band and what the category tolerates. An uncalibrated band drops out
	// rather than contributing a median of three ratios.
	//
	// The plan reads min(band median, category ceiling, band p75). The p75 term is
	// dropped because it can never bind -- p75 >= median by construction, so a
	// three-way min is a two-way min wearing a hat. The band still reports its p75
	// as context: it is how much tail the station tolerates, which is worth seeing
	// next to the median even though it never sets a price.
	multiple := cfg.ceilingFor(item.Category)
	source := "category ceiling"
	if band := cfg.Ladder.Band(item.JitaBestSell); band.Calibrated() && band.Median > 0 && band.Median < multiple {
		multiple = band.Median
		source = fmt.Sprintf("band median over %d types", band.Samples)
	}
	if !(multiple > 0) {
		row.PriceReason = "no markup ceiling configured for this category"
		return false
	}
	row.ReferencePrice = item.JitaBestSell * multiple
	row.ReferenceMarkup = multiple
	row.ReferenceSource = source

	// The ladder is a ceiling, so a reference below the floor is not raised to
	// meet it -- it means this item cannot be sold here without gouging.
	if row.ReferencePrice < row.FloorPrice {
		row.PriceReason = fmt.Sprintf("%s tolerates %.2fx but freight and fees need %.2fx",
			source, multiple, row.FloorPrice/item.JitaBestSell)
		return false
	}

	// 3. Competition. Seeds are not in levels -- they never move or reprice -- but
	// one resting under our cost is still a wall, so it is checked separately.
	//
	// This refusal is unconditional, where a player dumping under our cost gets
	// the depth test below. That asymmetry is the point: a player's few cheap
	// units will clear and then we sell, while an NPC seed is a standing fact
	// about the item. There is no waiting it out.
	if seed := npcFloorPrice(item.LocalOrders, cfg.DestStationID); seed > 0 && seed < row.FloorPrice {
		row.PriceReason = fmt.Sprintf("an NPC seeds this at %.2f, under our landed cost of %.2f", seed, row.FloorPrice)
		return false
	}

	for _, level := range levels {
		if level.price >= row.ReferencePrice {
			break
		}
		row.CompetingUnitsBelow += level.qty
		row.CompetingOrdersBelow += level.count
	}

	if len(levels) == 0 {
		row.SuggestedPrice = row.ReferencePrice
		row.PriceRule = PriceRuleReference
		row.PriceReason = fmt.Sprintf("%.2fx reference (%s) -- no player order at the destination", multiple, source)
		row.SuggestedMarkup = multiple
		return true
	}

	best := levels[0].price
	if best >= row.ReferencePrice {
		row.SuggestedPrice = row.ReferencePrice
		row.PriceRule = PriceRuleReference
		row.PriceReason = fmt.Sprintf("%.2fx reference (%s); the cheapest local order is %.2f, above it -- not following it up",
			multiple, source, best)
		row.SuggestedMarkup = multiple
		return true
	}

	// Depth decides, and it decides both of the remaining questions. Units resting
	// below our price are weighed against a day's destruction: a couple of units
	// are worth stepping over and waiting out, while thousands are the market
	// price and the reference is fiction.
	//
	// The measure is units below the *reference*, not below the best price,
	// because everything under where we list has to clear before we sell -- that
	// is the same quantity whichever branch is asking.
	stepOverRate, _, _ := item.sizingDemand(cfg.SizeAgainstLong)
	stepOverUnits := cfg.StepOverDaysCover * stepOverRate
	trivialDepth := float64(row.CompetingUnitsBelow) < stepOverUnits

	// A competitor under our landed cost cannot be undercut without losing money
	// on every unit. But a shallow dump under our cost is the same situation as a
	// shallow dump over it: a few units that will clear on their own, and no
	// reason to abandon the market. So the depth test decides here too, and only
	// material below-cost depth makes the item unpriceable.
	if best < row.FloorPrice {
		// A dump under cost is refused outright by default -- undercutting it
		// loses money on every unit, and the only reason to buy in anyway is a
		// belief about market turnover this tool has no way to measure. That
		// belief is exactly what Included states, so it is the one caution here
		// a manual override can honestly relax: not by pricing below cost (still
		// never done), but by buying at the reference and waiting the dump out
		// regardless of how deep the depth test judges it to be.
		if !trivialDepth && !item.Included {
			row.PriceReason = fmt.Sprintf("%d units rest at %.2f, under our landed cost of %.2f -- undercutting them would lose money",
				row.CompetingUnitsBelow, best, row.FloorPrice)
			return false
		}
		row.SuggestedPrice = row.ReferencePrice
		row.PriceRule = PriceRuleStepOver
		row.SuggestedMarkup = multiple
		if trivialDepth {
			row.PriceReason = fmt.Sprintf("%.2fx reference (%s) -- stepping over %d units dumped at %.2f, below our %.2f landed cost but under %.1f days of sales",
				multiple, source, row.CompetingUnitsBelow, best, row.FloorPrice, cfg.StepOverDaysCover)
		} else {
			row.PriceReason = fmt.Sprintf("included override -- stepping over %d units dumped at %.2f, below our %.2f landed cost, because this item is marked to ship regardless of competition",
				row.CompetingUnitsBelow, best, row.FloorPrice)
		}
		return true
	}

	if trivialDepth {
		row.SuggestedPrice = row.ReferencePrice
		row.PriceRule = PriceRuleStepOver
		row.SuggestedMarkup = multiple
		row.PriceReason = fmt.Sprintf("%.2fx reference (%s) -- stepping over %d units at %.2f, under %.1f days of sales",
			multiple, source, row.CompetingUnitsBelow, best, cfg.StepOverDaysCover)
		return true
	}

	undercut := NextSellUndercut(best)
	if !(undercut > 0) {
		row.PriceReason = fmt.Sprintf("cannot price under %.2f", best)
		return false
	}
	if undercut < row.FloorPrice {
		row.PriceReason = fmt.Sprintf("undercutting %.2f means %.2f, under our landed cost of %.2f",
			best, undercut, row.FloorPrice)
		return false
	}
	row.SuggestedPrice = undercut
	row.PriceRule = PriceRuleUndercut
	row.SuggestedMarkup = undercut / item.JitaBestSell
	row.PriceReason = fmt.Sprintf("%.2fx -- undercutting %d order(s) holding %d units at %.2f; reference was %.2fx (%s)",
		row.SuggestedMarkup, row.CompetingOrdersBelow, row.CompetingUnitsBelow, best, multiple, source)
	return true
}
