package engine

import (
	"math"
	"sort"
)

// lp_store.go -- what an LP store offer is worth, per loyalty point.
//
// Every value here is ISK per LP: (revenue - cost) / lp_cost. An offer can be
// realised several ways -- sold instantly into buy orders, listed as a sell
// order, and for a blueprint copy, sold on contract or built and the product
// sold -- and the whole point of the tool is to put those side by side, so
// each is its own nullable field rather than one blended number. Nil always
// means "does not apply or cannot be known", never zero: a zero reads as
// "break even", which is a claim.

// LPRequiredItem is an item the store takes alongside LP and ISK (a tag, a
// supply package). UnitPrice is what buying one costs in the pricing region;
// Priced is false when nothing is for sale there.
type LPRequiredItem struct {
	TypeID    int32   `json:"type_id"`
	TypeName  string  `json:"type_name"`
	Quantity  int64   `json:"quantity"`
	UnitPrice float64 `json:"unit_price"`
	Priced    bool    `json:"priced"`
}

// LPOffer is one store offer, with its required items already priced.
// For a blueprint offer, Quantity is the number of runs on the single copy the
// store hands over -- confirmed against the in-game store, where the
// quantity-10 Raven Navy Issue offer is one 10-run copy.
type LPOffer struct {
	OfferID       int32
	TypeID        int32
	Quantity      int64
	LPCost        int64
	ISKCost       float64
	RequiredItems []LPRequiredItem
}

// LPOfferMeta is what the SDE says about the offer's item.
type LPOfferMeta struct {
	TypeName      string
	IsBlueprint   bool
	ProductTypeID int32
	ProductName   string
	// ProductPerRun is how many product units one manufacturing run makes.
	ProductPerRun int64
	// Category, Group and MarketPath describe what gets sold -- the item, or
	// a blueprint's product -- so implants sort together and a search for
	// "ammunition" finds ammo, whatever the SDE category happens to be called.
	Category   string
	Group      string
	MarketPath []string
}

// LPMarketQuote is the pricing region's book for one type. For a blueprint
// offer it is the product's book, used only for liquidity.
type LPMarketQuote struct {
	BestBid        float64
	BestAsk        float64
	BidDepth       int64
	AskDepth       int64
	AvgDailyVolume float64
}

// LPFees are percentages, as the rest of the app stores them (4 = 4%).
type LPFees struct {
	SalesTaxPercent  float64
	BrokerFeePercent float64
}

// LPBuildResult is the industry analyzer's answer for building the offer's
// product at runs = quantity, with no blueprint cost counted. The profits are
// revenue after fees minus the build cost; the offer's own cost is not in them.
type LPBuildResult struct {
	InstantProfit    float64
	InstantAvailable bool
	ListedProfit     float64
	Error            string
	// Materials is the build's flattened shopping list for all runs, with the
	// structure's material bonus applied -- what the basket's multibuy adds.
	Materials []LPMaterial
	// BuildCost is materials plus job install for all runs; JobCost is the
	// install part of it. Shown, never used in a value: the profits above
	// already have them taken out.
	BuildCost float64
	JobCost   float64
}

// LPMaterial is one line of a build's shopping list.
type LPMaterial struct {
	TypeID   int32  `json:"type_id"`
	TypeName string `json:"type_name"`
	Quantity int64  `json:"quantity"`
}

// LPBPCPrice is what a blueprint copy of this type sells for on contract, per
// run -- the median of current asking prices, or the user's own figure.
type LPBPCPrice struct {
	PerRun   float64 `json:"per_run"`
	Samples  int     `json:"samples"`
	Override bool    `json:"override"`
}

// Best-method labels.
const (
	LPMethodSell      = "sell"
	LPMethodList      = "list"
	LPMethodSellBPC   = "sell_bpc"
	LPMethodBuildSell = "build_sell"
	LPMethodBuildList = "build_list"
)

// LPOfferRow is one offer as the tab shows it.
type LPOfferRow struct {
	OfferID       int32            `json:"offer_id"`
	TypeID        int32            `json:"type_id"`
	TypeName      string           `json:"type_name"`
	ProductTypeID int32            `json:"product_type_id"`
	ProductName   string           `json:"product_name"`
	IsBlueprint   bool             `json:"is_blueprint"`
	Category      string           `json:"category"`
	Group         string           `json:"group"`
	MarketPath    []string         `json:"market_path"`
	Runs          int64            `json:"runs"`
	Quantity      int64            `json:"quantity"`
	LPCost        int64            `json:"lp_cost"`
	ISKCost       float64          `json:"isk_cost"`
	RequiredItems []LPRequiredItem `json:"required_items"`

	// Cost is ISK plus required items at their buy price. Meaningless when
	// Unpriced, which is why every value is nil then.
	Cost     float64 `json:"cost"`
	Unpriced bool    `json:"unpriced"`

	Instant      *float64 `json:"instant"`
	Listed       *float64 `json:"listed"`
	BPCSale      *float64 `json:"bpc_sale"`
	BuildInstant *float64 `json:"build_instant"`
	BuildListed  *float64 `json:"build_listed"`
	Best         *float64 `json:"best"`
	BestMethod   string   `json:"best_method"`

	UnitsPerRedemption int64   `json:"units_per_redemption"`
	AvgDailyVolume     float64 `json:"avg_daily_volume"`
	// The pricing region's best bid and ask for what gets sold: the item, or
	// for a blueprint, its product. Shown so every value can be checked.
	UnitBid float64 `json:"unit_bid"`
	UnitAsk float64 `json:"unit_ask"`

	BPCPerRun   float64 `json:"bpc_per_run"`
	BPCSamples  int     `json:"bpc_samples"`
	BPCOverride bool    `json:"bpc_override"`
	BuildError  string  `json:"build_error,omitempty"`

	BuildMaterials []LPMaterial `json:"build_materials,omitempty"`
	BuildCost      float64      `json:"build_cost"`
	BuildJobCost   float64      `json:"build_job_cost"`
}

// NewLPOfferRow computes everything that needs only the market: the cost and,
// for anything that is not a blueprint, the instant and listed values. Build
// and contract values arrive later through ApplyBuild and ApplyBPCPrice.
func NewLPOfferRow(o LPOffer, meta LPOfferMeta, q *LPMarketQuote, fees LPFees) LPOfferRow {
	row := LPOfferRow{
		OfferID:       o.OfferID,
		TypeID:        o.TypeID,
		TypeName:      meta.TypeName,
		ProductTypeID: meta.ProductTypeID,
		ProductName:   meta.ProductName,
		IsBlueprint:   meta.IsBlueprint,
		Category:      meta.Category,
		Group:         meta.Group,
		MarketPath:    meta.MarketPath,
		Quantity:      o.Quantity,
		LPCost:        o.LPCost,
		ISKCost:       o.ISKCost,
		RequiredItems: o.RequiredItems,
	}
	if row.RequiredItems == nil {
		row.RequiredItems = []LPRequiredItem{}
	}
	if row.MarketPath == nil {
		row.MarketPath = []string{}
	}
	row.Cost, row.Unpriced = lpOfferCost(o)

	if meta.IsBlueprint {
		row.Runs = o.Quantity
		perRun := meta.ProductPerRun
		if perRun <= 0 {
			perRun = 1
		}
		row.UnitsPerRedemption = o.Quantity * perRun
	} else {
		row.UnitsPerRedemption = o.Quantity
	}
	if q != nil {
		row.AvgDailyVolume = q.AvgDailyVolume
		row.UnitBid, row.UnitAsk = q.BestBid, q.BestAsk
	}

	// A blueprint copy has no market; its product's book is only liquidity.
	if !meta.IsBlueprint && q != nil {
		qty := float64(o.Quantity)
		if q.BestBid > 0 {
			row.Instant = row.perLP(qty * q.BestBid * (1 - fees.SalesTaxPercent/100))
		}
		if q.BestAsk > 0 {
			row.Listed = row.perLP(qty * q.BestAsk * (1 - (fees.BrokerFeePercent+fees.SalesTaxPercent)/100))
		}
	}
	row.pickBest()
	return row
}

// ApplyBuild sets the two build values from the industry analysis.
func (r *LPOfferRow) ApplyBuild(b LPBuildResult) {
	r.BuildInstant, r.BuildListed = nil, nil
	r.BuildError = b.Error
	r.BuildMaterials = b.Materials
	r.BuildCost, r.BuildJobCost = b.BuildCost, b.JobCost
	if b.Error == "" {
		// The analyzer's profit is already net of the build; it is revenue
		// the offer's cost has not been taken from yet.
		r.BuildListed = r.perLP(b.ListedProfit)
		if b.InstantAvailable {
			r.BuildInstant = r.perLP(b.InstantProfit)
		}
	}
	r.pickBest()
}

// ApplyBPCPrice sets the contract value. Contracts pay no broker fee or sales
// tax, only a small flat creation fee, which is ignored. Nil clears it.
func (r *LPOfferRow) ApplyBPCPrice(p *LPBPCPrice) {
	r.BPCSale = nil
	r.BPCPerRun, r.BPCSamples, r.BPCOverride = 0, 0, false
	if p != nil {
		r.BPCPerRun, r.BPCSamples, r.BPCOverride = p.PerRun, p.Samples, p.Override
		if p.PerRun > 0 && r.Runs > 0 {
			r.BPCSale = r.perLP(float64(r.Runs) * p.PerRun)
		}
	}
	r.pickBest()
}

// perLP turns a revenue into ISK/LP after the offer's cost. Nil when the offer
// is unpriced or costs no LP -- a divide by zero here would be an Inf the JSON
// encoder refuses, and an ISK-only offer has no ISK/LP to rank by anyway.
func (r *LPOfferRow) perLP(revenue float64) *float64 {
	if r.Unpriced || r.LPCost <= 0 {
		return nil
	}
	v := (revenue - r.Cost) / float64(r.LPCost)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}

func (r *LPOfferRow) pickBest() {
	r.Best, r.BestMethod = nil, ""
	for _, c := range []struct {
		v      *float64
		method string
	}{
		{r.Instant, LPMethodSell},
		{r.Listed, LPMethodList},
		{r.BPCSale, LPMethodSellBPC},
		{r.BuildInstant, LPMethodBuildSell},
		{r.BuildListed, LPMethodBuildList},
	} {
		if c.v == nil {
			continue
		}
		if r.Best == nil || *c.v > *r.Best {
			v := *c.v
			r.Best, r.BestMethod = &v, c.method
		}
	}
}

// lpOfferCost is ISK plus required items at their buy price. unpriced is true
// when any required item has no price: costing it at zero is the flaw this
// tool exists to fix, so the offer gets no cost at all instead.
func lpOfferCost(o LPOffer) (cost float64, unpriced bool) {
	cost = o.ISKCost
	for _, ri := range o.RequiredItems {
		if !ri.Priced || ri.UnitPrice <= 0 {
			unpriced = true
			continue
		}
		cost += float64(ri.Quantity) * ri.UnitPrice
	}
	return cost, unpriced
}

// LPMedian is the median of the samples, 0 for none. The median, not the mean,
// because contract asking prices carry the occasional absurd listing.
func LPMedian(samples []float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	s := append([]float64(nil), samples...)
	sort.Float64s(s)
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}
	return (s[mid-1] + s[mid]) / 2
}
