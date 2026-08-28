package engine

import (
	"fmt"
	"log"
	"math"
	"sort"
	"strings"

	"eve-flipper/internal/esi"
	"eve-flipper/internal/sde"
)

// IndustryParams holds parameters for industry analysis.
type IndustryParams struct {
	TypeID              int32   // Target item to analyze
	Runs                int32   // Number of runs (default 1)
	ActivityMode        string  // auto/manufacturing/reaction/invention
	MaterialEfficiency  int32   // Blueprint ME (0-10)
	TimeEfficiency      int32   // Blueprint TE (0-20)
	SystemID            int32   // Manufacturing system (drives system cost index)
	// PricingSystemID, when non-zero, overrides which region market prices are
	// fetched from. Lets the scanner build in one region (cost index, structure
	// bonuses) while quoting prices from another (e.g. Jita). When zero, the
	// pricing region is derived from StationID, then SystemID — preserving the
	// pre-existing single-location analyzer behavior.
	PricingSystemID     int32
	StationID           int64   // Optional: specific station/structure for price lookup (0 = region-wide)
	FacilityTax         float64 // Facility tax % (default 0)
	StructureBonus      float64 // Structure material bonus % (e.g., 1% for Raitaru)
	BrokerFee           float64 // Legacy: broker fee % applied to both sides when SplitTradeFees is false
	SalesTaxPercent     float64 // Legacy: sales tax % applied to the sell side when SplitTradeFees is false
	// Split-fee model — same shape as backtest / scanner / station_trading.
	// When SplitTradeFees is true, the four side-specific rates below are
	// consumed instead of the legacy BrokerFee/SalesTaxPercent pair. When
	// false, BrokerFee is copied to both sides, sell tax is SalesTaxPercent,
	// buy tax is zero (matches EVE — buy orders pay broker, sellers pay
	// broker + sales tax). See internal/engine/fees.go for the canonical
	// normalization.
	SplitTradeFees       bool
	BuyBrokerFeePercent  float64
	SellBrokerFeePercent float64
	BuySalesTaxPercent   float64
	SellSalesTaxPercent  float64
	// Reprocessing sourcing — when IncludeReprocessing is true, the
	// analyzer considers "buy ore, reprocess, use as material" as an
	// alternative to buying leaf materials (minerals, ice products, moon
	// composites) directly. Uses SDE typeMaterials via
	// SDE.Industry.Reprocessing and picks the cheapest source at query time.
	IncludeReprocessing bool
	// Fallback / manual override: when non-zero AND all skill/tax fields
	// below are zero, this net yield is used directly (0-1, e.g. 0.85).
	// Callers who don't know their exact skill levels can just pass a
	// realistic net-yield estimate here.
	ReprocessingYield float64
	// Character skill levels (0-5). See ReprocessingYieldMultiplier for
	// the composed formula. All-zero preserves the "no skill bonus" case.
	// Structure yield (Athanor/Tatara/rigs) via ReprocessingBaseStationYield.
	ReprocessingSkillLevel           int32
	ReprocessingEfficiencySkillLevel int32
	SpecificProcessingSkillLevel     int32
	// Optional Reprocessing Efficiency implant bonus % (e.g. 4.0 for
	// Zainou 'Beancounter' Reprocessing Efficiency RX-604). Applied
	// multiplicatively on top of skill yield.
	ReprocessingImplantYieldBonus float64
	// Station tax on reprocessed output % (0-100). NPC stations up to 5%.
	ReprocessingStationTaxPercent float64
	// Base station yield (0-1). 0.50 NPC; Athanor = 0.52 + rig bonuses;
	// Tatara = 0.54 + rig bonuses. Defaults to 0.50 when 0.
	ReprocessingBaseStationYield float64
	// SCCSurchargePercent overrides DefaultSCCSurchargePercent for this
	// one call. Zero = use the package default (currently 4.0).
	SCCSurchargePercent float64
	MaxDepth            int     // Max recursion depth (default 10)
	OwnBlueprint        bool    // true = user owns BP (default), false = must buy
	BlueprintCost       float64 // ISK cost of blueprint (BPO or BPC)
	BlueprintIsBPO      bool    // true = BPO (amortize over runs), false = BPC (one-time)
	InventionChance     float64 // Optional invention chance override in percent (0 = SDE probability)
	// InventionChanceMult multiplies the per-product SDE base probability
	// (only applied when InventionChance is 0). Lets the frontend send a
	// decryptor's chance multiplier without needing to know the per-product
	// SDE base. 0 or 1 = no adjustment.
	InventionChanceMult float64
	// Invention skill levels (0-5). When any is > 0 and InventionChance
	// (absolute override) is 0, the engine applies EVE's canonical
	// invention-chance skill formula:
	//   chance = base × (1 + Encryption/40) × (1 + (Datacore1 + Datacore2)/30) × decryptorMult
	// All-L5 = 1.5× base. Callers that already fold skills into an absolute
	// InventionChance (e.g. legacy scanner rows) should leave these at 0.
	InventionEncryptionLevel int32
	InventionDatacoreLevel1  int32
	InventionDatacoreLevel2  int32
	DecryptorCost       float64 // Optional per-attempt decryptor cost
	// DecryptorTypeID identifies the decryptor the user picked. Included on
	// the invention step's per-step material bill and on IndustryAnalysis so
	// the client can label the invention task without a separate SDE lookup.
	// Zero = no decryptor (base invention). Independent of DecryptorCost so
	// the caller can still supply a market-derived cost even when we don't
	// know the exact typeID, though for the common case both are set.
	DecryptorTypeID     int32
	InventionOutputRuns int32   // Optional successful BPC runs override
	// BuildMode governs the per-node build-vs-buy decision made in
	// calculateCosts. "" or "auto" (default) picks whichever is cheaper at
	// runtime. "buy_all" forces buy on every non-root sub-product (falls
	// back to build if the item has no buy price). "build_all" forces build
	// on every buildable sub-product (falls back to buy if no blueprint).
	BuildMode string
	// SkipReactions, when true, treats any material that would be produced
	// via a reaction activity as a base (buy-from-market) node instead of
	// expanding it into a reaction step. Reflects the workflow of a builder
	// who never runs reactions themselves and always buys reaction outputs
	// (fuel blocks, moon composites, etc.). Ignored at the root — if the
	// user asks to analyze a reaction product directly, that request wins.
	SkipReactions bool
	// StructureRigs describes the Standup rig loadout on the build structure.
	// Empty RigTypeIDs → no rig math applied (backward-compatible with
	// scans that predate rig support).
	StructureRigs StructureRigConfig
	// StructureJobCostReduction is the hull-inherent job-cost bonus % for
	// the structure (Raitaru=3, Azbel=4, Sotiyo=5, refineries=0). Distinct
	// from rig job-cost reductions, which stack on top via StructureRigs.
	StructureJobCostReduction float64
	// RevenueModel picks between two ways of quoting the sell price:
	// "sell_to_sell" (default, aka maker) uses the visible best ask — models
	// listing your own sell order and waiting. "sell_to_buy" (aka instant)
	// walks the buy order book and models dumping into buy orders now for a
	// faster turnover at a worse fill price. Empty string preserves the
	// pre-toggle behavior (prefer instant when buy book has liquidity) so
	// older callers see no change.
	RevenueModel string
	// CostModel is the buy-side mirror of RevenueModel — how the analyzer
	// prices materials off the market. "buy_to_sell" (default) walks the
	// sell order book for the fill cost of an instant purchase, matching
	// the historical behavior. "buy_to_buy" uses the visible best bid — the
	// price at which you'd list a buy order and wait for someone to hit it,
	// modelling patient procurement. Empty string keeps the default so
	// older callers see no change.
	CostModel string
	// OwnedBlueprints, when non-nil, gives the analyzer per-product ME/TE
	// for sub-node builds. Keys are PRODUCT typeIDs so the recursion
	// (which walks products, not blueprints) can look up each sub-node's
	// own blueprint ME instead of cascading params.MaterialEfficiency /
	// params.TimeEfficiency to every level of the tree. When set:
	//   - Sub-node with an entry: use that entry's ME/TE.
	//   - Sub-node without an entry AND the node is otherwise buildable:
	//     marked as base (buy-only). The user can't build a T2 component
	//     they don't own a blueprint for, so the analyzer shouldn't ever
	//     recommend it — that's the "buy plasma thrusters because BuildCost
	//     was computed against ME=0 cascade" bug this field prevents.
	// The ROOT node always uses params.MaterialEfficiency / TimeEfficiency:
	// the user is running THAT job at the ME/TE they picked in the UI.
	// When nil (or empty), the analyzer falls back to the legacy cascade
	// so pre-owned-BP callers see identical output.
	OwnedBlueprints map[int32]OwnedBlueprint
}

// OwnedBlueprint captures a user-owned blueprint's ME/TE plus originality
// info for use as a per-product override during tree recursion. Held by
// IndustryParams; keyed by the PRODUCT typeID the blueprint manufactures.
//
// IsBPO / AvailableRuns feed the analyzer's copy-step emitter: if a T2
// invention step wants to consume a T1 BPC and the user owns only a BPO
// (no BPCs, IsBPO=true and no copies in the pool), the analyzer prepends
// a `copy` activity step to materialize BPCs first. Similarly a T1 mfg
// task consuming an owned BPC decrements AvailableRuns for scheduling.
//
// TargetME / TargetTE describe the assumed research level for the analysis.
// When the actual ME/TE (stored on this struct) is below the target, the
// analyzer emits research_material / research_time steps to close the gap.
// Zero targets mean "use whatever ME/TE the blueprint has" (no research).
type OwnedBlueprint struct {
	ME             int32 `json:"me"`
	TE             int32 `json:"te"`
	IsBPO          bool  `json:"is_bpo,omitempty"`
	AvailableRuns  int32 `json:"available_runs,omitempty"`
	TargetME       int32 `json:"target_me,omitempty"`
	TargetTE       int32 `json:"target_te,omitempty"`
}

// StructureRigConfig describes the rig loadout for the analyzer's build
// structure. All fields optional; zero-value = no rig contribution.
type StructureRigConfig struct {
	// Up to 3 rig typeIDs. Unknown IDs and rigs that don't fit
	// StructureTypeID are silently dropped.
	RigTypeIDs []int32
	// Structure hull typeID (Raitaru=35825, Azbel=35826, Sotiyo=35827,
	// Athanor/Tatara etc.). Zero → engine skips rig math entirely.
	StructureTypeID int32
	// SystemSecurity in [0.0, 1.0] range. When zero, engine looks it up
	// from SDE.Systems[SystemID].Security. Set explicitly by callers who
	// want to override (e.g. scanning for a "what if this were nullsec"
	// scenario).
	SystemSecurity float64
}

// MaterialNode represents a node in the production tree.
type MaterialNode struct {
	TypeID       int32           `json:"type_id"`
	TypeName     string          `json:"type_name"`
	Quantity     int32           `json:"quantity"`      // Required quantity
	Activity     string          `json:"activity"`      // manufacturing/reaction/base
	Runs         int32           `json:"runs"`          // Blueprint runs needed for this node
	IsBase       bool            `json:"is_base"`       // True if cannot be further produced
	BuyPrice     float64         `json:"buy_price"`     // Market buy price (sell orders) — walked for full Quantity
	MaterialCost float64         `json:"material_cost"` // Sum of chosen child material costs
	BuildCost    float64         `json:"build_cost"`    // Total cost to build the full Quantity (materials + job cost)
	ShouldBuild  bool            `json:"should_build"`  // True when at least some units are built (includes split nodes)
	JobCost      float64         `json:"job_cost"`      // Manufacturing job installation cost
	Children     []*MaterialNode `json:"children"`      // Required sub-materials
	Blueprint    *BlueprintInfo  `json:"blueprint"`     // Blueprint info if buildable
	Depth        int             `json:"depth"`         // Depth in tree
	// Split-strategy fields. Populated when the analyzer picks a mixed
	// buy+build strategy for THIS material — buy the cheap head of the
	// sell-order book that beats per-unit build cost, build the rest.
	// Example: need 100 units; 30 units on-market at 5M (below the 7M
	// per-unit build cost) + 70 built at 490M = 640M vs. all-build 700M
	// or all-buy 1,550M. ShouldSplit=false is the legacy binary path;
	// BuyUnits == Quantity if !ShouldBuild, or BuildUnits == Quantity
	// otherwise. Parent aggregation uses BuyPortionCost+BuildPortionCost
	// when ShouldSplit is true, otherwise BuyPrice or BuildCost per the
	// binary decision.
	ShouldSplit      bool    `json:"should_split"`
	BuyUnits         int32   `json:"buy_units"`
	BuildUnits       int32   `json:"build_units"`
	BuyPortionCost   float64 `json:"buy_portion_cost"`   // walked cost of BuyUnits from market
	BuildPortionCost float64 `json:"build_portion_cost"` // pro-rated BuildCost for BuildUnits
}

// BlueprintInfo contains blueprint information for display.
type BlueprintInfo struct {
	BlueprintTypeID int32   `json:"blueprint_type_id"`
	ProductQuantity int32   `json:"product_quantity"`
	ME              int32   `json:"me"`
	TE              int32   `json:"te"`
	Time            int32   `json:"time"` // Manufacturing time in seconds
	Activity        string  `json:"activity"`
	Probability     float64 `json:"probability,omitempty"`
}

// IndustryActivityStepMaterial is one material consumed by a single activity
// step (datacores for invention, none for copying/research, ME-adjusted BOM
// for manufacturing/reaction). Emitted only for step types where the material
// bill is step-specific and NOT captured by the recursive flat-materials
// shopping list (invention/copy/research). Manufacturing/reaction steps leave
// this empty — their materials live in the material tree.
type IndustryActivityStepMaterial struct {
	TypeID   int32  `json:"type_id"`
	TypeName string `json:"type_name,omitempty"`
	Quantity int32  `json:"quantity"`
}

// IndustryActivityStep is one executable activity in the industry plan.
type IndustryActivityStep struct {
	Activity         string  `json:"activity"`
	BlueprintTypeID  int32   `json:"blueprint_type_id"`
	BlueprintName    string  `json:"blueprint_name"`
	ProductTypeID    int32   `json:"product_type_id"`
	ProductName      string  `json:"product_name"`
	Runs             float64 `json:"runs"`
	OutputQuantity   int32   `json:"output_quantity"`
	MaterialCost     float64 `json:"material_cost"`
	JobCost          float64 `json:"job_cost"`
	TotalCost        float64 `json:"total_cost"`
	TimeSeconds      int32   `json:"time_seconds"`
	Probability      float64 `json:"probability,omitempty"`
	ExpectedAttempts float64 `json:"expected_attempts,omitempty"`
	// BlueprintIsBPC signals that BlueprintTypeID is a T2 BPC (produced via
	// invention), so the plan-patch builder can default IsBPO=false on
	// blueprint-pool rows for this step. Without this hint, sub-BPs default
	// to BPO which is wrong for every T2 component in a build chain.
	BlueprintIsBPC bool   `json:"blueprint_is_bpc,omitempty"`
	Reason         string `json:"reason,omitempty"`
	// Materials is the step-specific material bill. Populated for invention
	// (datacores + optional decryptor), copy, and research steps — where the
	// bill can't be recovered from flat_materials via cost aggregation.
	// Empty for manufacturing/reaction (their materials live in the material
	// tree and are already reflected in flat_materials). The plan-patch
	// builder attributes these to the step's task_id AND subtracts them
	// from flat_materials before attributing the remainder to the output
	// (mfg) task, so datacores don't double-count in material_diff.
	Materials []IndustryActivityStepMaterial `json:"materials,omitempty"`
	// DecryptorTypeID / DecryptorName mirror params.DecryptorTypeID onto
	// invention steps so the plan-patch task label can include the decryptor
	// name ("invent Zealot Blueprint · Symmetry Decryptor") without the
	// client re-doing an SDE lookup. Zero when no decryptor.
	DecryptorTypeID int32  `json:"decryptor_type_id,omitempty"`
	DecryptorName   string `json:"decryptor_name,omitempty"`
}

// IndustryAnalysis is the result of analyzing a production chain.
type IndustryAnalysis struct {
	TargetTypeID          int32                  `json:"target_type_id"`
	TargetTypeName        string                 `json:"target_type_name"`
	Runs                  int32                  `json:"runs"`
	TotalQuantity         int32                  `json:"total_quantity"`
	MarketBuyPrice        float64                `json:"market_buy_price"`   // Cost to buy ready product (from sell orders, no broker fee)
	TotalBuildCost        float64                `json:"total_build_cost"`   // Cost to build from scratch
	OptimalBuildCost      float64                `json:"optimal_build_cost"` // Cost with optimal buy/build decisions
	Savings               float64                `json:"savings"`            // MarketBuyPrice - OptimalBuildCost
	SavingsPercent        float64                `json:"savings_percent"`
	SellRevenue           float64                `json:"sell_revenue"`       // Revenue after sales tax + broker fee
	Profit                float64                `json:"profit"`             // SellRevenue - OptimalBuildCost
	ProfitPercent         float64                `json:"profit_percent"`     // Profit / OptimalBuildCost * 100
	MakerSellRevenue      float64                `json:"maker_sell_revenue"` // Listing at visible ask after tax + broker fee
	MakerSellProfit       float64                `json:"maker_sell_profit"`
	InstantSellRevenue    float64                `json:"instant_sell_revenue"` // Selling into visible buy orders after sales tax
	InstantSellProfit     float64                `json:"instant_sell_profit"`
	InstantSellAvailable  bool                   `json:"instant_sell_available"`
	// UnitAskPrice is the raw per-unit best ask (before sales tax + broker
	// fee) in the pricing region the analyzer used for this run. This is
	// the "list price" a user sees in the in-game market window when they
	// open the item's sell orders — surfacing it lets a caller cross-check
	// against reality without doing the tax/broker inverse math from
	// SellRevenue. Zero when the pricing region has no visible sell orders
	// for the type, which is often the root cause of a "why does this row
	// think profit is huge/tiny?" investigation.
	UnitAskPrice          float64                `json:"unit_ask_price"`
	// UnitBidPrice is the raw per-unit best bid (before sales tax), for
	// the same auditing purpose on the instant-sell path.
	UnitBidPrice          float64                `json:"unit_bid_price"`
	// AskDepthUnits and BidDepthUnits are the total unit volume visible
	// on the sell / buy side of the pricing-region order book. When
	// AskDepthUnits is small (e.g. 1 unit) but the naive revenue math is
	// `bestAsk × TotalQuantity`, the row's profit is fantasy — nobody will
	// pay the lone bait seller's price for the batch. Surfacing depth is
	// the practical fix for "why does this T2 rig row claim +1B?" — you
	// see "only 3 units listed" in the tooltip and immediately know the
	// listing price isn't a bulk-market signal. Bid depth serves the
	// mirror-image question on the instant-sell path.
	AskDepthUnits         int64                  `json:"ask_depth_units"`
	BidDepthUnits         int64                  `json:"bid_depth_units"`
	// AskOrdersCount / BidOrdersCount pair with the depth fields: 100
	// units across 20 sellers = real market; 100 units in 1 order = one
	// seller who could pull it any moment. Distinct signal from depth.
	AskOrdersCount        int32                  `json:"ask_orders_count"`
	BidOrdersCount        int32                  `json:"bid_orders_count"`
	ISKPerHour            float64                `json:"isk_per_hour"`       // Profit / manufacturing hours (root activity time)
	ManufacturingTime     int32                  `json:"manufacturing_time"` // Root activity's own time in seconds (matches in-game display)
	TotalActivityTime     int32                  `json:"total_activity_time"` // Sum of every step's time across the plan (for planners that serialize all sub-builds)
	TotalJobCost          float64                `json:"total_job_cost"`      // Root install cost (+ invention install if any) — matches in-game single-job display
	TotalMaterialCost     float64                `json:"total_material_cost"` // All non-install spending: mfg materials + (for invention rows) datacores/decryptor. Reconciles: material + job + bp = optimal.
	InventionCost         float64                `json:"invention_cost"`
	InventionJobCost      float64                `json:"invention_job_cost"`
	InventionAttempts     float64                `json:"invention_attempts"`
	InventionProbability  float64                `json:"invention_probability"`
	ActivityMode          string                 `json:"activity_mode"`
	ActivityPlan          []IndustryActivityStep `json:"activity_plan"`
	MaterialTree          *MaterialNode          `json:"material_tree"`
	FlatMaterials         []*FlatMaterial        `json:"flat_materials"` // Flattened list of base materials
	SystemCostIndex       float64                `json:"system_cost_index"`
	RegionID              int32                  `json:"region_id"`               // Market region for execution plan
	RegionName            string                 `json:"region_name"`             // Optional display name
	BlueprintCostIncluded float64                `json:"blueprint_cost_included"` // BP cost added to build cost
	// JobCostBreakdown carries the EVE-canonical Job Installation Cost line
	// items (EIV, System Cost, Structure Bonus, Rig Bonus, Gross Install,
	// Facility Tax, SCC Surcharge, Net Install) summed across the whole
	// activity tree — invention step included. NetInstall matches
	// TotalJobCost within rounding.
	JobCostBreakdown JobCostBreakdown `json:"job_cost_breakdown"`
}

// FlatMaterial is a simplified material for the shopping list.
type FlatMaterial struct {
	TypeID     int32   `json:"type_id"`
	TypeName   string  `json:"type_name"`
	Quantity   int32   `json:"quantity"`
	UnitPrice  float64 `json:"unit_price"`
	TotalPrice float64 `json:"total_price"`
	Volume     float64 `json:"volume"`
}

// DefaultSCCSurchargePercent is CCP's flat "Secure Commerce Commission"
// surcharge, added to every job's install cost as a fixed % of EIV
// regardless of structure or location. Currently 4% (post-Uprising 2022;
// stepped from 1.5% during the industry cost rework).
//
// This value has changed twice since the 2022 rework and CCP has publicly
// discussed further adjustments. Callers can override per-call via
// IndustryParams.SCCSurchargePercent; the analyzer honors the override
// only when > 0 so a future patch is a one-line change in one place
// (this const), and per-user experimentation is a request-field change
// (no rebuild). Audit P5.26.
var DefaultSCCSurchargePercent = 4.0

// sccSurchargePercent resolves the effective SCC rate for one Analyze
// call: the per-call override wins when > 0; otherwise the package
// default.
func (p IndustryParams) sccSurchargePercent() float64 {
	if p.SCCSurchargePercent > 0 {
		return p.SCCSurchargePercent
	}
	return DefaultSCCSurchargePercent
}

// industryFeeInputs unpacks IndustryParams into the canonical tradeFeeInputs
// shape used by tradeFeeMultipliers. This is the ONLY place in industry.go
// that reads fee fields — so any future fee-model change is a one-file
// edit. Callers that leave the split-fee fields at zero see the legacy
// behavior: BrokerFee applied to both sides, no buy-side sales tax, sell
// tax = SalesTaxPercent. Matches EVE — buyers pay broker only, sellers
// pay broker + sales tax.
func industryFeeInputs(params IndustryParams) tradeFeeInputs {
	return tradeFeeInputs{
		SplitTradeFees:       params.SplitTradeFees,
		BrokerFeePercent:     params.BrokerFee,
		SalesTaxPercent:      params.SalesTaxPercent,
		BuyBrokerFeePercent:  params.BuyBrokerFeePercent,
		SellBrokerFeePercent: params.SellBrokerFeePercent,
		BuySalesTaxPercent:   params.BuySalesTaxPercent,
		SellSalesTaxPercent:  params.SellSalesTaxPercent,
	}
}

// SellRevenueMultiplier returns the per-unit net-of-fees revenue multiplier
// for the sell side (maker path — listing your own sell order and paying
// broker + sales tax). Uses the canonical additive formula from fees.go.
func (p IndustryParams) SellRevenueMultiplier() float64 {
	_, m := tradeFeeMultipliers(industryFeeInputs(p))
	return m
}

// InstantSellTaxOnlyMultiplier returns the sell-side multiplier for the
// instant-sell (taker) path — dumping into buys as market taker. Takers
// pay sales tax but NOT broker fee.
func (p IndustryParams) InstantSellTaxOnlyMultiplier() float64 {
	_, _, _, sellTax := tradeFeePercents(industryFeeInputs(p))
	m := 1.0 - sellTax/100.0
	if m < 0 {
		m = 0
	}
	return m
}

// InventionSkillMultiplier returns the multiplier applied to the SDE base
// invention probability from encryption and datacore skill levels.
//
//	mult = (1 + encryption/40) × (1 + (datacore1 + datacore2)/30)
//
// All-L5: 1.125 × 1.333... ≈ 1.5. All-0: 1.0. Levels are clamped 0..5.
// Independent of decryptor multiplier (which composes on top).
func InventionSkillMultiplier(encryption, datacore1, datacore2 int32) float64 {
	clamp := func(v int32) int32 {
		if v < 0 {
			return 0
		}
		if v > 5 {
			return 5
		}
		return v
	}
	e := clamp(encryption)
	d1 := clamp(datacore1)
	d2 := clamp(datacore2)
	return (1.0 + float64(e)/40.0) * (1.0 + float64(d1+d2)/30.0)
}

// batchUtilizationThreshold is the minimum fraction of one blueprint run's
// product output the parent must actually consume before the analyzer will
// recurse into building that sub-node. Motivated by reactions (10,000
// Fernite Carbide per run) and ammo BPs (5,000 charges per run): when a
// Plasma Thruster only needs 12 Fernite Carbide, firing a full 10,000-unit
// reaction would charge the entire reaction cost (~200k ISK) against a
// 12-unit demand. The buy-vs-build compare recovers correctly IF the
// sub-node has a positive BuyPrice — but a market-cache miss anywhere
// deep in the tree flips ShouldBuild=true and cascades the full batch
// cost up the ancestry, ~doubling top-level BuildCost.
//
// 0.5 = must be able to consume at least half a batch to consider build.
// Root nodes are exempt (user asked to analyze THAT specific quantity)
// and BuildMode "build_all" bypasses this guard (explicit user override).
const batchUtilizationThreshold = 0.5

// JobCostBreakdown is the aggregate job-install-cost math for a single
// Analyze() call. Mirrors CCP's canonical line items so the UI can render
// the breakdown without recomputing from scalars.
type JobCostBreakdown struct {
	EIV            float64 `json:"eiv"`
	SystemCost     float64 `json:"system_cost"`
	StructureBonus float64 `json:"structure_bonus"` // reduction, positive ISK
	RigBonus       float64 `json:"rig_bonus"`       // reduction, positive ISK
	GrossInstall   float64 `json:"gross_install"`
	FacilityTax    float64 `json:"facility_tax"`
	SCCSurcharge   float64 `json:"scc_surcharge"`
	NetInstall     float64 `json:"net_install"`
}

// IndustryAnalyzer performs industry calculations.
type IndustryAnalyzer struct {
	SDE                  *sde.Data
	ESI                  *esi.Client
	IndustryCache        *esi.IndustryCache
	adjustedPrices       map[int32]float64
	marketPrices         map[int32]float64 // Best sell order prices
	marketSellOrders     map[int32][]esi.MarketOrder
	marketBuyOrders      map[int32][]esi.MarketOrder
	systemCostIndices    *esi.SystemCostIndices
	jobCostBreakdown     JobCostBreakdown // reset at Analyze() start
	// reprocessingSources indexes materialID → ores that reprocess into
	// that material. Rebuilt once per Analyze() call when IncludeReprocessing
	// is on. Nil when reprocessing is disabled or SDE isn't loaded.
	reprocessingSources         ReprocessingSources
	currentReprocessingNetYield float64
	getAllAdjustedPrices func(cache *esi.IndustryCache) (map[int32]float64, error)
	getSystemCostIndex   func(cache *esi.IndustryCache, systemID int32) (*esi.SystemCostIndices, error)
	fetchMarketPricesFn  func(params IndustryParams) (map[int32]float64, error)
	fetchMarketBooksFn   func(params IndustryParams) (map[int32][]esi.MarketOrder, map[int32][]esi.MarketOrder, error)
}

// NewIndustryAnalyzer creates a new analyzer.
func NewIndustryAnalyzer(sdeData *sde.Data, esiClient *esi.Client) *IndustryAnalyzer {
	return &IndustryAnalyzer{
		SDE:           sdeData,
		ESI:           esiClient,
		IndustryCache: esi.NewIndustryCache(),
	}
}

// SetMarketBooksOverride injects a custom market-book fetcher. Used by the
// profitable-blueprints scanner to memoize book fetches once per scan
// instead of re-fetching + re-grouping the entire region's order book
// (~500k orders per side in The Forge) for every row. Pass nil to clear
// the override and restore default ESI-backed fetching.
func (a *IndustryAnalyzer) SetMarketBooksOverride(fn func(IndustryParams) (map[int32][]esi.MarketOrder, map[int32][]esi.MarketOrder, error)) {
	a.fetchMarketBooksFn = fn
}

// SetMarketPricesOverride is the sibling injection for best-ask price maps.
// Same rationale as SetMarketBooksOverride — batch scans call Analyze once
// per row and each call re-runs the price aggregation over cached ESI
// data, so memoizing the aggregated map is a straight win.
func (a *IndustryAnalyzer) SetMarketPricesOverride(fn func(IndustryParams) (map[int32]float64, error)) {
	a.fetchMarketPricesFn = fn
}

// SetAdjustedPricesOverride is the sibling injection for adjusted prices.
// The ESI cache already dedups the network round-trip; this lets a scan
// skip even the sync.Map lookup + map materialization on every row.
func (a *IndustryAnalyzer) SetAdjustedPricesOverride(fn func(*esi.IndustryCache) (map[int32]float64, error)) {
	a.getAllAdjustedPrices = fn
}

// LoadMarketBooksForParams exposes the internal market-book loader so the
// scanner's memoization closure can invoke the real (non-overridden) fetch
// path via a temporary analyzer copy. Not intended for general use — the
// standard entry point is Analyze().
func (a *IndustryAnalyzer) LoadMarketBooksForParams(p IndustryParams) (map[int32][]esi.MarketOrder, map[int32][]esi.MarketOrder, error) {
	return a.loadMarketBooks(p)
}

// LoadMarketPricesForParams — sibling to LoadMarketBooksForParams for the
// best-ask price map.
func (a *IndustryAnalyzer) LoadMarketPricesForParams(p IndustryParams) (map[int32]float64, error) {
	return a.loadMarketPrices(p)
}

// LoadAdjustedPrices — sibling to LoadMarketBooksForParams for adjusted prices.
func (a *IndustryAnalyzer) LoadAdjustedPrices() (map[int32]float64, error) {
	return a.loadAdjustedPrices()
}

func (a *IndustryAnalyzer) ensureIndustryCache() {
	if a.IndustryCache == nil {
		a.IndustryCache = esi.NewIndustryCache()
	}
}

func (a *IndustryAnalyzer) loadAdjustedPrices() (map[int32]float64, error) {
	a.ensureIndustryCache()
	if a.getAllAdjustedPrices != nil {
		return a.getAllAdjustedPrices(a.IndustryCache)
	}
	if a.ESI == nil {
		return nil, fmt.Errorf("esi client unavailable")
	}
	return a.ESI.GetAllAdjustedPrices(a.IndustryCache)
}

func (a *IndustryAnalyzer) loadSystemCostIndex(systemID int32) (*esi.SystemCostIndices, error) {
	a.ensureIndustryCache()
	if a.getSystemCostIndex != nil {
		return a.getSystemCostIndex(a.IndustryCache, systemID)
	}
	if a.ESI == nil {
		return nil, fmt.Errorf("esi client unavailable")
	}
	return a.ESI.GetSystemCostIndex(a.IndustryCache, systemID)
}

func (a *IndustryAnalyzer) loadMarketPrices(params IndustryParams) (map[int32]float64, error) {
	if a.fetchMarketPricesFn != nil {
		return a.fetchMarketPricesFn(params)
	}
	if a.ESI == nil {
		return nil, fmt.Errorf("esi client unavailable")
	}
	return a.fetchMarketPrices(params)
}

func (a *IndustryAnalyzer) loadMarketBooks(params IndustryParams) (map[int32][]esi.MarketOrder, map[int32][]esi.MarketOrder, error) {
	if a.fetchMarketBooksFn != nil {
		return a.fetchMarketBooksFn(params)
	}
	if a.ESI == nil {
		return nil, nil, fmt.Errorf("esi client unavailable")
	}
	return a.fetchMarketBooks(params)
}

// Analyze performs full industry analysis for a given item.
func (a *IndustryAnalyzer) Analyze(params IndustryParams, progress func(string)) (*IndustryAnalysis, error) {
	if params.Runs <= 0 {
		params.Runs = 1
	}
	if params.MaxDepth <= 0 {
		params.MaxDepth = 10
	}
	if params.ReprocessingYield <= 0 {
		params.ReprocessingYield = 0.50 // Default 50%
	}
	params.ActivityMode = normalizeIndustryActivityMode(params.ActivityMode)
	if params.InventionOutputRuns < 0 {
		params.InventionOutputRuns = 0
	}
	if params.DecryptorCost < 0 {
		params.DecryptorCost = 0
	}

	// Get type info
	typeInfo, ok := a.SDE.Types[params.TypeID]
	if !ok {
		return nil, fmt.Errorf("type %d not found", params.TypeID)
	}

	progress("Fetching market prices...")

	// Fetch adjusted prices for job cost calculation
	adjustedPrices, err := a.loadAdjustedPrices()
	if err != nil {
		log.Printf("Warning: failed to fetch adjusted prices: %v", err)
		adjustedPrices = make(map[int32]float64)
	}
	a.adjustedPrices = adjustedPrices

	// Fetch market prices (best sell orders) for buy/build comparison
	progress("Fetching sell order prices...")
	marketPrices, err := a.loadMarketPrices(params)
	if err != nil {
		log.Printf("Warning: failed to fetch market prices: %v", err)
		marketPrices = make(map[int32]float64)
	}
	a.marketPrices = marketPrices
	a.marketSellOrders = nil
	a.marketBuyOrders = nil

	progress("Fetching order book depth...")
	marketSellOrders, marketBuyOrders, err := a.loadMarketBooks(params)
	if err != nil {
		log.Printf("Warning: failed to fetch market order books: %v", err)
	} else {
		a.marketSellOrders = marketSellOrders
		a.marketBuyOrders = marketBuyOrders
	}

	// Reset per-call accumulators so a reused analyzer instance doesn't
	// mix breakdown terms across calls. (calculateCosts walks the tree and
	// adds to jobCostBreakdown; must start from zero every Analyze.)
	a.jobCostBreakdown = JobCostBreakdown{}

	// Reprocessing setup (per-call): reset by default; build the reverse
	// ore-material index and cache the net yield only when the caller
	// asked for the ore-vs-buy sourcing comparison. Rebuilding per call
	// keeps the SDE-owning analyzer thread-safe (each shallow-copy in the
	// scanner gets its own map). Audit P2.5.
	a.reprocessingSources = nil
	a.currentReprocessingNetYield = 0
	if params.IncludeReprocessing {
		a.reprocessingSources = BuildReprocessingSources(a.SDE)
		a.currentReprocessingNetYield = ReprocessingNetYield(params)
	}

	// Get system cost index
	var costIndex float64
	a.systemCostIndices = nil
	if params.SystemID != 0 {
		progress("Fetching system cost index...")
		idx, err := a.loadSystemCostIndex(params.SystemID)
		if err != nil {
			log.Printf("Warning: failed to fetch cost index: %v", err)
		} else {
			a.systemCostIndices = idx
			costIndex = idx.Manufacturing
		}
	}

	progress("Building production tree...")

	// FIX #1: Treat params.Runs as actual blueprint runs.
	// Calculate total items produced: runs × productQuantity.
	totalQuantity := params.Runs
	if bp, ok := a.SDE.Industry.GetBlueprintForProduct(params.TypeID); ok {
		// Root call: skipReactions=false so an explicit reaction-product
		// analysis still works when the user has SkipReactions on globally.
		activity := a.activityForProduct(bp, params.TypeID, params.ActivityMode, false)
		productQty, _ := blueprintProductForActivity(bp, params.TypeID, activity)
		if productQty <= 0 {
			productQty = bp.ProductQuantity
		}
		if productQty <= 0 {
			productQty = 1
		}
		totalQuantity = params.Runs * productQty
	}

	// Build material tree recursively using totalQuantity as desired items
	tree := a.buildMaterialTree(params.TypeID, totalQuantity, params, 0)

	// Calculate costs
	progress("Calculating optimal costs...")
	a.calculateCosts(tree, costIndex, params)
	if params.ActivityMode != "auto" && !tree.IsBase {
		tree.ShouldBuild = true
	}

	// Flatten materials for shopping list
	flatMaterials := a.flattenMaterials(tree)

	// MarketBuyPrice is cost to buy from visible sell-order depth (no broker fee).
	marketBuyPrice := a.materialCost(params.TypeID, totalQuantity, params.CostModel)

	optimalCost := tree.BuildCost
	if params.ActivityMode == "auto" && tree.BuyPrice < tree.BuildCost && tree.BuyPrice > 0 {
		optimalCost = tree.BuyPrice
	}

	// Blueprint acquisition cost (user doesn't own it)
	var bpCostIncluded float64
	if !params.OwnBlueprint && params.BlueprintCost > 0 {
		if params.BlueprintIsBPO {
			bpCostIncluded = params.BlueprintCost / float64(params.Runs)
		} else {
			bpCostIncluded = params.BlueprintCost
		}
		optimalCost += bpCostIncluded
	}

	inventionStep, hasInvention := a.calculateInventionStep(params, tree, costIndex)
	var inventionCost, inventionJobCost, inventionAttempts, inventionProbability float64
	if hasInvention {
		inventionCost = inventionStep.TotalCost
		inventionJobCost = inventionStep.JobCost
		inventionAttempts = inventionStep.ExpectedAttempts
		inventionProbability = inventionStep.Probability
		optimalCost += inventionCost
	}
	// Copy step — only when the invention source is BPO-only in the user's
	// pool (no BPCs available). Detects via OwnedBlueprints keyed on the
	// source BP's manufacturing product (the T1 item the source BP builds).
	copyStep, hasCopy := a.calculateCopyStep(params, inventionStep, hasInvention, costIndex)
	if hasCopy {
		optimalCost += copyStep.TotalCost
	}
	// Research steps (ME/TE) — root-BP-only for now: when the user owns a
	// BPO of the root but its actual ME/TE is below the analysis target,
	// emit a research step to close the gap. Sub-BP research is followup.
	researchSteps := a.calculateResearchSteps(params, costIndex)
	for _, rs := range researchSteps {
		optimalCost += rs.TotalCost
	}

	savings := marketBuyPrice - optimalCost
	savingsPercent := 0.0
	if marketBuyPrice > 0 {
		savingsPercent = savings / marketBuyPrice * 100
	}

	// FIX #6: Calculate profit if you sell the built product.
	// Fees flow through the canonical tradeFeeMultipliers path (fees.go)
	// so this file matches scanner.go / station_trading.go / route.go /
	// backtest.go / contracts.go — one file owns the additive
	// `1 - (broker + tax)/100` formula. Pre-2026 code multiplied
	// `(1-tax) × (1-broker)` which drifted ~15 bp of revenue vs every
	// other engine surface. Audit P1.3.
	unitAsk := a.marketBestAsk(params.TypeID)
	unitBid := a.marketBestBid(params.TypeID)
	askDepth, bidDepth := a.marketBookDepth(params.TypeID)
	askOrders, bidOrders := a.marketBookOrderCounts(params.TypeID)
	makerSellRevenue := unitAsk * float64(totalQuantity) * params.SellRevenueMultiplier()
	instantSellRevenue, instantSellAvailable := a.marketInstantSellRevenue(
		params.TypeID,
		totalQuantity,
		params.InstantSellTaxOnlyMultiplier(),
	)
	// Pick the revenue quote per the caller's chosen model. "sell_to_sell"
	// (list at the best ask) is the natural default a builder uses when
	// pricing "if I list these, what do I get?" — matches every other
	// industry planner's headline number. "sell_to_buy" (dump into buys)
	// is the fast-turnover alternative. Empty RevenueModel keeps the old
	// prefer-instant-when-available behavior so pre-toggle scans don't
	// silently change results.
	sellRevenue := makerSellRevenue
	switch params.RevenueModel {
	case "sell_to_sell":
		// keep makerSellRevenue
	case "sell_to_buy":
		if instantSellAvailable {
			sellRevenue = instantSellRevenue
		}
	default:
		if instantSellAvailable {
			sellRevenue = instantSellRevenue
		}
	}
	profit := sellRevenue - optimalCost
	profitPercent := 0.0
	if optimalCost > 0 {
		profitPercent = profit / optimalCost * 100
	}

	// Root activity time (blueprint base × TE modifiers already baked into
	// the tree's node.Blueprint.Time is CCP's static base; the per-run ME/TE
	// reduction is stored on the tree via node materials, not here — root
	// time is just the blueprint's own activity time for the queued runs).
	// This matches what the EVE Industry window shows for the top-level job.
	var rootTime int32
	if tree.Blueprint != nil {
		rootTime = tree.Blueprint.Time
	}

	// TotalJobCost is the install cost the player directly pays to produce
	// the target: root activity's install + invention step install (if any).
	// Sub-material install costs live inside their own build costs, which
	// are already inside tree.BuildCost via the recursive material tree —
	// summing every buildable node's JobCost here would double-count them
	// against tree.BuildCost and inflate the "Materials = build - job"
	// derivation on the frontend tooltip. Matches CCP's in-game Industry
	// window which shows exactly the one install cost for the queued job.
	totalJobCost := tree.JobCost
	// totalMaterialCost is what the user actually buys off the market —
	// mfg-tree materials plus, for invention rows, the datacores/decryptor
	// (invention step's total minus its install fees). Reconciles cleanly:
	// totalMaterialCost + totalJobCost + bpCost = optimalBuildCost.
	totalMaterialCost := tree.MaterialCost
	if hasInvention {
		totalJobCost += inventionJobCost
		totalMaterialCost += inventionCost - inventionJobCost
	}
	activityPlan := a.buildActivityPlan(tree)
	if hasInvention {
		activityPlan = append([]IndustryActivityStep{inventionStep}, activityPlan...)
	}
	// Copy prepends before invention so the ordering reads:
	// copy → invention → sub-mfg → root mfg.
	if hasCopy {
		activityPlan = append([]IndustryActivityStep{copyStep}, activityPlan...)
	}
	// Research steps prepend before everything else — you research the
	// blueprint first, then copy, then invent, then build. Both
	// research_material and research_time can appear in the same plan.
	if len(researchSteps) > 0 {
		activityPlan = append(researchSteps, activityPlan...)
	}
	// TotalActivityTime is the sum across the plan (serial worst-case).
	// Root time drives ISK/h since it's the throughput of the queued job:
	// sub-material builds run in separate slots and don't gate this job.
	totalActivityTime := sumActivityPlanTime(activityPlan)
	if totalActivityTime == 0 {
		totalActivityTime = rootTime
	}
	iskPerHour := 0.0
	if rootTime > 0 {
		iskPerHour = profit / (float64(rootTime) / 3600.0)
	}

	regionID, regionName := a.resolveMarketRegion(params)

	return &IndustryAnalysis{
		TargetTypeID:          params.TypeID,
		TargetTypeName:        typeInfo.Name,
		Runs:                  params.Runs,
		TotalQuantity:         totalQuantity,
		MarketBuyPrice:        marketBuyPrice,
		TotalBuildCost:        tree.BuildCost,
		OptimalBuildCost:      optimalCost,
		Savings:               savings,
		SavingsPercent:        savingsPercent,
		SellRevenue:           sellRevenue,
		Profit:                profit,
		ProfitPercent:         profitPercent,
		MakerSellRevenue:      makerSellRevenue,
		MakerSellProfit:       makerSellRevenue - optimalCost,
		InstantSellRevenue:    instantSellRevenue,
		InstantSellProfit:     instantSellRevenue - optimalCost,
		InstantSellAvailable:  instantSellAvailable,
		UnitAskPrice:          unitAsk,
		UnitBidPrice:          unitBid,
		AskDepthUnits:         askDepth,
		BidDepthUnits:         bidDepth,
		AskOrdersCount:        askOrders,
		BidOrdersCount:        bidOrders,
		ISKPerHour:            iskPerHour,
		ManufacturingTime:     rootTime,
		TotalActivityTime:     totalActivityTime,
		TotalJobCost:          totalJobCost,
		TotalMaterialCost:     totalMaterialCost,
		InventionCost:         inventionCost,
		InventionJobCost:      inventionJobCost,
		InventionAttempts:     inventionAttempts,
		InventionProbability:  inventionProbability,
		ActivityMode:          params.ActivityMode,
		ActivityPlan:          activityPlan,
		MaterialTree:          tree,
		FlatMaterials:         flatMaterials,
		SystemCostIndex:       costIndex,
		RegionID:              regionID,
		RegionName:            regionName,
		BlueprintCostIncluded: bpCostIncluded,
		JobCostBreakdown:      a.jobCostBreakdown,
	}, nil
}

// buildMaterialTree recursively builds the material tree.
func (a *IndustryAnalyzer) buildMaterialTree(typeID int32, quantity int32, params IndustryParams, depth int) *MaterialNode {
	typeName := ""
	if t, ok := a.SDE.Types[typeID]; ok {
		typeName = t.Name
	}

	node := &MaterialNode{
		TypeID:   typeID,
		TypeName: typeName,
		Quantity: quantity,
		Depth:    depth,
		BuyPrice: a.materialCost(typeID, quantity, params.CostModel),
		Activity: "base",
	}

	// Check if we can build this item
	bp, hasBP := a.SDE.Industry.GetBlueprintForProduct(typeID)
	if !hasBP || depth >= params.MaxDepth {
		node.IsBase = true
		return node
	}
	// Apply SkipReactions only to children (depth > 0); at the root the
	// caller passed ActivityMode explicitly and we honor that choice.
	skipReactions := params.SkipReactions && depth > 0
	activity := a.activityForProduct(bp, typeID, params.ActivityMode, skipReactions)
	if activity == "" {
		node.IsBase = true
		return node
	}
	productQuantity, probability := blueprintProductForActivity(bp, typeID, activity)
	if productQuantity <= 0 {
		productQuantity = 1
	}

	// Batch-output overshoot guard. Reactions and ammo BPs emit hundreds
	// to tens of thousands of units per run; when the parent needs a
	// small fraction of one batch, firing the full run charges the entire
	// batch cost against a much smaller demand. Root is exempt (the user
	// explicitly asked to build THAT quantity of THIS item); BuildMode
	// "build_all" is also exempt (explicit user override). Otherwise, if
	// we'd consume less than batchUtilizationThreshold of one full run's
	// output, mark the node base so the parent uses its market BuyPrice
	// for the small quantity actually needed. See const definition for
	// full rationale.
	isRoot := depth == 0
	if !isRoot && params.BuildMode != "build_all" && productQuantity > 0 {
		if float64(quantity) < float64(productQuantity)*batchUtilizationThreshold {
			node.IsBase = true
			return node
		}
	}

	// Calculate how many runs we need
	runsNeeded := quantity / productQuantity
	if quantity%productQuantity != 0 {
		runsNeeded++
	}
	node.Activity = activity
	node.Runs = runsNeeded

	// Rig contribution for this specific product + activity. Cheap when
	// there are no rigs (early-returns zeros). Computed once per node.
	sec := a.resolveSystemSecurity(params.StructureRigs, params.SystemID)
	rigME, rigTE, _ := a.rigContribution(params.StructureRigs, activity, typeID, sec)

	// Per-node ME/TE selection. Root uses the user-supplied top-level ME/TE
	// (the BP they picked for this specific job). Sub-nodes prefer their
	// own blueprint's ME/TE from OwnedBlueprints; falling back to the
	// legacy cascade only when OwnedBlueprints is nil (older callers that
	// haven't opted into per-BP awareness). When OwnedBlueprints IS set
	// but a sub-node's product is missing, we mark it base so the analyzer
	// stops recommending "build" for T2 components the user can't build.
	me := params.MaterialEfficiency
	te := params.TimeEfficiency
	if !isRoot && params.OwnedBlueprints != nil {
		if owned, has := params.OwnedBlueprints[typeID]; has {
			me = owned.ME
			te = owned.TE
		} else if params.BuildMode == "build_all" {
			// User explicitly asked to build every buildable sub-node,
			// even those they don't own a BP for. Fall back to ME=0/TE=0
			// (the analyzer's baseline assumption when no research data
			// is known) — the resulting build cost is a slight over-
			// estimate but the whole point of build_all is to surface
			// the fan-out as tasks, not to optimize cost.
			me = 0
			te = 0
		} else {
			// Owned-BP mode + auto/buy_all: refuse to recommend building
			// a sub-material without a BP for it. Mark base so
			// calculateCosts sets BuildCost = BuyPrice and ShouldBuild
			// = false.
			node.IsBase = true
			return node
		}
	}

	node.Blueprint = &BlueprintInfo{
		BlueprintTypeID: bp.BlueprintTypeID,
		ProductQuantity: productQuantity,
		ME:              me,
		TE:              te,
		Time:            calculateActivityTime(bp, activity, runsNeeded, te, rigTE),
		Activity:        activity,
		Probability:     probability,
	}

	// EVE formula: max(runs, ceil(base × runs × (1-ME/100) × (1-structureBonus/100) × (1-rigMEReduction/100)))
	materials := calculateActivityMaterials(bp, activity, runsNeeded, me, params.StructureBonus, rigME)

	// Build children recursively
	for _, mat := range materials {
		child := a.buildMaterialTree(mat.TypeID, mat.Quantity, params, depth+1)
		node.Children = append(node.Children, child)
	}

	return node
}

// calculateCosts calculates build costs bottom-up and decides buy vs build.
func (a *IndustryAnalyzer) calculateCosts(node *MaterialNode, costIndex float64, params IndustryParams) {
	// First, calculate costs for all children
	for _, child := range node.Children {
		a.calculateCosts(child, costIndex, params)
	}

	if node.IsBase {
		// Base material - can only buy
		node.BuildCost = node.BuyPrice
		node.ShouldBuild = false
		return
	}

	// Calculate material cost (sum of optimal costs for children). Split
	// children contribute BuyPortionCost + BuildPortionCost; binary
	// children contribute the extreme they picked.
	var materialCost float64
	for _, child := range node.Children {
		switch {
		case child.ShouldSplit:
			materialCost += child.BuyPortionCost + child.BuildPortionCost
		case child.ShouldBuild:
			materialCost += child.BuildCost
		default:
			materialCost += child.BuyPrice
		}
	}
	node.MaterialCost = materialCost

	// Calculate job installation cost — matches CCP's real EVE formula.
	// Corrected on 2026-07-22 after cross-checking against the in-game
	// Industry window: BOTH FacilityTax and SCCSurcharge are % of EIV
	// (not % of Gross Install). Prior code multiplied FacilityTax against
	// Gross, which under-charged by ~30x for a typical 4% SCI hisec setup.
	//
	//   SystemCost     = EIV × SCI
	//   StructureBonus = SystemCost × structureJobCostReduction%   (reduction)
	//   RigBonus       = SystemCost × rigCost%                     (reduction)
	//   GrossInstall   = SystemCost − StructureBonus − RigBonus
	//   FacilityTax    = EIV × facilityTax%                         (of EIV, not Gross)
	//   SCCSurcharge   = EIV × 4%                                   (CCP flat fee)
	//   NetInstall     = GrossInstall + FacilityTax + SCCSurcharge
	eiv := a.calculateEIV(node)
	sci := a.costIndexForActivity(node.Activity, costIndex)
	sec := a.resolveSystemSecurity(params.StructureRigs, params.SystemID)
	_, _, rigCost := a.rigContribution(params.StructureRigs, node.Activity, node.TypeID, sec)

	systemCost := eiv * sci
	structureBonus := systemCost * (params.StructureJobCostReduction / 100.0)
	if structureBonus < 0 {
		structureBonus = 0
	}
	rigBonus := systemCost * (rigCost / 100.0)
	if rigBonus < 0 {
		rigBonus = 0
	}
	grossInstall := systemCost - structureBonus - rigBonus
	if grossInstall < 0 {
		grossInstall = 0
	}
	facilityTax := eiv * (params.FacilityTax / 100.0)
	sccSurcharge := eiv * params.sccSurchargePercent() / 100.0
	node.JobCost = grossInstall + facilityTax + sccSurcharge

	// Breakdown is populated ONLY for the root node — the specific job the
	// user is running. In-game EVE Industry window shows just that job's
	// install cost, not the aggregate of every built sub-material. Summing
	// every buildable child into one figure would double- or triple-count
	// jobs the user isn't actually about to install. Sub-material job
	// costs still add into total_build_cost via node.JobCost above.
	if node.TypeID == params.TypeID {
		a.jobCostBreakdown = JobCostBreakdown{
			EIV:            eiv,
			SystemCost:     systemCost,
			StructureBonus: structureBonus,
			RigBonus:       rigBonus,
			GrossInstall:   grossInstall,
			FacilityTax:    facilityTax,
			SCCSurcharge:   sccSurcharge,
			NetInstall:     node.JobCost,
		}
	}

	node.BuildCost = materialCost + node.JobCost

	// The tree root (params.TypeID) is what the user asked to analyze — always
	// build it, regardless of mode. BuildMode only governs sub-node decisions.
	isRoot := node.TypeID == params.TypeID
	buildable := node.Blueprint != nil && len(node.Children) > 0

	if isRoot {
		node.ShouldBuild = true
		return
	}

	// Decide: buy, build, or split. BuildMode overrides the cost-based
	// choice:
	//   "buy_all"   → prefer buy when a buy price exists.
	//   "build_all" → prefer build when the node is buildable (has children).
	//   "auto"/""   → pick the cheapest of buy / build / mixed split.
	//
	// The split path: for buildable nodes with sell-order depth, walk the
	// book for orders whose per-unit price beats per-unit build cost and
	// take those; build the rest. Mixed wins over both extremes when the
	// book has a cheap head that then jumps. Auto-mode only — explicit
	// buy_all / build_all overrides bypass the split.
	switch params.BuildMode {
	case "buy_all":
		if node.BuyPrice > 0 {
			node.ShouldBuild = false
		} else {
			// No buy price → fall back to build if we can, else buy at 0.
			node.ShouldBuild = buildable
		}
	case "build_all":
		if buildable {
			node.ShouldBuild = true
		} else {
			node.ShouldBuild = false
		}
	default:
		if !buildable || node.Quantity <= 0 || node.BuildCost <= 0 {
			// Not buildable, or degenerate quantity — fall through to
			// legacy binary compare.
			if node.BuyPrice > 0 && node.BuyPrice < node.BuildCost {
				node.ShouldBuild = false
			} else {
				node.ShouldBuild = true
			}
			break
		}
		perUnitBuild := node.BuildCost / float64(node.Quantity)
		buyUnits, buyCost := computeMixedMaterialCost(a.marketSellOrders[node.TypeID], node.Quantity, perUnitBuild)
		if buyUnits > 0 && buyUnits < node.Quantity {
			// Some cheap orders beat per-unit build cost, but not enough
			// to cover the batch. Mixed strategy is a candidate; compute
			// its total against the two extremes and pick the winner.
			buildUnits := node.Quantity - buyUnits
			buildPortion := node.BuildCost * float64(buildUnits) / float64(node.Quantity)
			mixedTotal := buyCost + buildPortion
			allBuy := node.BuyPrice
			allBuild := node.BuildCost
			if mixedTotal < allBuild && (allBuy <= 0 || mixedTotal < allBuy) {
				// Mixed beats both extremes — commit to the split.
				node.ShouldSplit = true
				node.ShouldBuild = true // some units are built; parent aggregation reads ShouldSplit first
				node.BuyUnits = buyUnits
				node.BuildUnits = buildUnits
				node.BuyPortionCost = buyCost
				node.BuildPortionCost = buildPortion
				break
			}
		} else if buyUnits >= node.Quantity {
			// Entire batch fits under the per-unit build threshold — the
			// mixed walker already found all-buy at the walked price is
			// cheaper than build. This is exactly what the legacy compare
			// would decide too (BuyPrice < BuildCost), but computeMixed's
			// walk gives a tighter number when cheap-head + expensive-tail
			// happens to also fit — the walked cost `buyCost` is what
			// we'd actually pay for the cheap head, which never exceeds
			// the full walked BuyPrice. Fall through to the legacy binary
			// compare here so BuyPrice remains the canonical all-buy cost.
		}
		if node.BuyPrice > 0 && node.BuyPrice < node.BuildCost {
			node.ShouldBuild = false
		} else {
			node.ShouldBuild = true
		}
	}
}

// calculateEIV calculates Estimated Item Value for job cost.
// FIX #2: EVE uses BASE material quantities (before ME) for EIV, not ME-reduced.
// Formula: EIV = sum(adjusted_price × base_quantity × runs)
func (a *IndustryAnalyzer) calculateEIV(node *MaterialNode) float64 {
	bp, ok := a.SDE.Industry.GetBlueprintForProduct(node.TypeID)
	if !ok || bp == nil {
		return 0
	}
	activity := node.Activity
	if activity == "" {
		// Post-tree bookkeeping (EIV computation for time/job-cost display).
		// Passing skipReactions=false is correct here: if the tree already
		// marked a node as base via SkipReactions, its ShouldBuild=false and
		// we don't consume the EIV in cost math — recomputing an activity
		// name here just gives labeling data without changing behavior.
		activity = a.activityForProduct(bp, node.TypeID, "", false)
	}
	productQuantity, _ := blueprintProductForActivity(bp, node.TypeID, activity)
	if productQuantity <= 0 {
		productQuantity = 1
	}

	// Calculate actual blueprint runs for this node
	runsNeeded := node.Quantity / productQuantity
	if node.Quantity%productQuantity != 0 {
		runsNeeded++
	}

	var eiv float64
	for _, mat := range activityMaterials(bp, activity) {
		price := a.adjustedPrices[mat.TypeID]
		// Use base_quantity × runs (NOT ME-adjusted quantities)
		eiv += price * float64(mat.Quantity) * float64(runsNeeded)
	}
	return eiv
}

func normalizeIndustryActivityMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "manufacturing", "reaction", "invention":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "auto"
	}
}

// activityForProduct picks the activity that will produce the given typeID
// for this blueprint. skipReactions, when true, refuses to fall back to
// "reaction" — reaction-only materials are then treated as base (buy)
// nodes. Applied to child materials; the caller passes false at the root
// so an explicit reaction analysis still works.
func (a *IndustryAnalyzer) activityForProduct(bp *sde.Blueprint, productTypeID int32, preferred string, skipReactions bool) string {
	if bp == nil {
		return ""
	}
	preferred = normalizeIndustryActivityMode(preferred)
	if (preferred == "auto" || preferred == "manufacturing") && bp.ProductTypeID == productTypeID && len(bp.Materials) > 0 {
		return "manufacturing"
	}
	if preferred != "auto" && preferred != "invention" {
		if activityProduces(bp, preferred, productTypeID) {
			// Explicit reaction pick at root wins even when skipReactions is on
			// — the flag is a preference about implicit fallback, not a veto.
			return preferred
		}
	}
	if activityProduces(bp, "manufacturing", productTypeID) {
		return "manufacturing"
	}
	if !skipReactions && activityProduces(bp, "reaction", productTypeID) {
		return "reaction"
	}
	if preferred != "auto" && preferred != "invention" {
		if _, ok := bp.Activities[preferred]; ok {
			return preferred
		}
	}
	return ""
}

func activityProduces(bp *sde.Blueprint, activity string, productTypeID int32) bool {
	if bp == nil {
		return false
	}
	act := bp.Activities[activity]
	if act == nil {
		return false
	}
	for _, product := range act.Products {
		if product.TypeID == productTypeID {
			return true
		}
	}
	return false
}

func blueprintProductForActivity(bp *sde.Blueprint, productTypeID int32, activity string) (int32, float64) {
	if bp == nil {
		return 0, 0
	}
	act := bp.Activities[activity]
	if act == nil {
		return bp.ProductQuantity, 0
	}
	for _, product := range act.Products {
		if product.TypeID == productTypeID {
			return product.Quantity, normalizeProbability(product.Probability)
		}
	}
	if len(act.Products) > 0 {
		return act.Products[0].Quantity, normalizeProbability(act.Products[0].Probability)
	}
	return bp.ProductQuantity, 0
}

func activityMaterials(bp *sde.Blueprint, activity string) []sde.BlueprintMaterial {
	if bp == nil {
		return nil
	}
	if act := bp.Activities[activity]; act != nil {
		return act.Materials
	}
	return bp.Materials
}

// calculateActivityMaterials returns the raw material list for `runs` of the
// given activity, applying blueprint ME, structure-inherent ME, and rig-derived
// ME reductions. rigMEReduction is a positive number in percent-form (e.g. 4.4
// for a 4.4% reduction). Applied multiplicatively so real EVE-style combined
// bonuses fall out naturally.
//
// Reactions apply structureBonus + rigMEReduction the same way manufacturing
// does (blueprint ME is always 0 for reactions). Historically reactions
// received no structure/rig ME; the fix in this pass makes reaction rigs
// actually reduce moon-composite material use.
// CalculateActivityMaterialsExported is a thin exported wrapper around the
// package-private calculateActivityMaterials so callers outside the engine
// package (currently the /materials/recalc-remaining handler) can compute
// per-BP material needs without wiring up a full IndustryAnalyzer, market
// fetch, or price cache. Named with the "Exported" suffix intentionally to
// signal that the canonical, richer entry point remains
// IndustryAnalyzer.Analyze — this is a narrow "just multiply BP mats by
// runs, apply ME" surface for callers that don't need the tree.
func CalculateActivityMaterialsExported(bp *sde.Blueprint, activity string, runs, me int32, structureBonus, rigMEReduction float64) []sde.BlueprintMaterial {
	return calculateActivityMaterials(bp, activity, runs, me, structureBonus, rigMEReduction)
}

func calculateActivityMaterials(bp *sde.Blueprint, activity string, runs, me int32, structureBonus, rigMEReduction float64) []sde.BlueprintMaterial {
	materials := activityMaterials(bp, activity)
	if len(materials) == 0 || runs <= 0 {
		return nil
	}
	result := make([]sde.BlueprintMaterial, 0, len(materials))
	switch activity {
	case "manufacturing", "reaction":
		if me < 0 {
			me = 0
		}
		if me > 10 {
			me = 10
		}
		if structureBonus < 0 {
			structureBonus = 0
		}
		if rigMEReduction < 0 {
			rigMEReduction = 0
		}
		meMultiplier := 1.0 - float64(me)/100.0
		structureMultiplier := 1.0 - structureBonus/100.0
		rigMultiplier := 1.0 - rigMEReduction/100.0
		if activity == "reaction" {
			// Reactions have no blueprint ME; only structure + rig apply.
			meMultiplier = 1.0
		}
		for _, mat := range materials {
			qty := int32(math.Ceil(float64(mat.Quantity) * float64(runs) * meMultiplier * structureMultiplier * rigMultiplier))
			if qty < runs {
				qty = runs
			}
			result = append(result, sde.BlueprintMaterial{TypeID: mat.TypeID, Quantity: qty})
		}
	default:
		for _, mat := range materials {
			result = append(result, sde.BlueprintMaterial{TypeID: mat.TypeID, Quantity: mat.Quantity * runs})
		}
	}
	return result
}

// calculateActivityTime returns total activity time in seconds. Applies
// blueprint TE (manufacturing only) and rig TE reduction (manufacturing,
// reaction, and invention). rigTEReduction is percent-form, positive
// (e.g. 20.0 for a 20% time reduction).
func calculateActivityTime(bp *sde.Blueprint, activity string, runs, te int32, rigTEReduction float64) int32 {
	if bp == nil || runs <= 0 {
		return 0
	}
	baseTime := bp.Time
	if act := bp.Activities[activity]; act != nil && act.Time > 0 {
		baseTime = act.Time
	}
	if rigTEReduction < 0 {
		rigTEReduction = 0
	}
	rigMultiplier := 1.0 - rigTEReduction/100.0
	if activity == "manufacturing" {
		if te < 0 {
			te = 0
		}
		if te > 20 {
			te = 20
		}
		return int32(float64(baseTime) * float64(runs) * (1.0 - float64(te)/100.0) * rigMultiplier)
	}
	// Reactions + invention: no blueprint TE, but rig TE still applies.
	return int32(float64(baseTime) * float64(runs) * rigMultiplier)
}

// resolveSystemSecurity returns the security status used for rig sec-scaling
// this analyze call. Prefers the explicit override in the config, else looks
// up SDE.Systems[SystemID].Security. Returns 0 (nullsec-equivalent) when
// neither is available.
func (a *IndustryAnalyzer) resolveSystemSecurity(cfg StructureRigConfig, systemID int32) float64 {
	if cfg.SystemSecurity > 0 {
		return cfg.SystemSecurity
	}
	if a == nil || a.SDE == nil || systemID == 0 {
		return 0
	}
	if sys, ok := a.SDE.Systems[systemID]; ok && sys != nil {
		return sys.Security
	}
	return 0
}

// rigContribution walks the fitted rigs, filters to those whose affinity
// matches (activity, product category/group/metaGroup), applies the sec-
// status multiplier, and returns aggregated ME/TE/cost reductions in
// percent-form (positive numbers). Additive across up to 3 rigs — EVE's
// structure rigs don't stacking-penalize.
//
// productTypeID is the item being produced by this activity. For invention
// rows, that's the T2/T3 module (not the source BPC). For manufacturing/
// reaction, it's the direct output.
//
// Returns zeros when rig math isn't applicable (empty loadout, no SDE,
// unknown structure).
func (a *IndustryAnalyzer) rigContribution(cfg StructureRigConfig, activity string, productTypeID int32, systemSec float64) (meReduction, teReduction, costReduction float64) {
	if a == nil || a.SDE == nil || len(cfg.RigTypeIDs) == 0 {
		return 0, 0, 0
	}
	if a.SDE.Rigs == nil || a.SDE.RigAffinities == nil {
		return 0, 0, 0
	}
	product := a.SDE.Types[productTypeID]
	for _, rigID := range cfg.RigTypeIDs {
		if rigID <= 0 {
			continue
		}
		rig := a.SDE.Rigs[rigID]
		if rig == nil {
			continue
		}
		aff, hasAff := a.SDE.RigAffinities[rig.GroupID]
		if !hasAff {
			continue
		}
		if !aff.Matches(activity, product) {
			continue
		}
		// Fit check: if structure hull known, silently drop rigs that
		// don't fit (guards stale UI state that survived a hull switch).
		if cfg.StructureTypeID != 0 {
			hullGroup := int32(0)
			if t := a.SDE.Types[cfg.StructureTypeID]; t != nil {
				hullGroup = t.GroupID
			}
			if hullGroup != 0 {
				fits := false
				for _, g := range rig.FitsStructureGroups {
					if g == hullGroup {
						fits = true
						break
					}
				}
				if !fits {
					continue
				}
			}
		}
		mult := rig.SecMultiplier(systemSec)
		if mult == 0 {
			continue // rig can't operate at this sec (e.g. advanced rig in hisec)
		}
		// Bonus values in SDE are negative for reductions; flip sign so
		// meReduction/teReduction/costReduction are positive percentages.
		meReduction += -rig.MEBonus * mult
		teReduction += -rig.TEBonus * mult
		costReduction += -rig.CostBonus * mult
	}
	return meReduction, teReduction, costReduction
}

func normalizeProbability(probability float64) float64 {
	if probability <= 0 {
		return 0
	}
	if probability > 1 {
		probability /= 100
	}
	if probability > 1 {
		return 1
	}
	return probability
}

func (a *IndustryAnalyzer) costIndexForActivity(activity string, fallback float64) float64 {
	if a.systemCostIndices == nil {
		return fallback
	}
	// Non-manufacturing activities MUST NOT fall back to the manufacturing SCI
	// — they're 5-10× lower than mfg in EVE, so borrowing the mfg SCI over-
	// estimates install cost dramatically. A missing SCI for an activity means
	// ESI didn't report one for this system (typical for high-throughput hubs
	// where the copy/invention/research indices are effectively negligible);
	// return 0 so install cost = 0 while facility_tax + SCC still apply — same
	// behavior as the in-game industry window in that case.
	switch activity {
	case "reaction":
		return a.systemCostIndices.Reaction
	case "invention":
		return a.systemCostIndices.Invention
	case "copy":
		return a.systemCostIndices.Copying
	case "research_material":
		return a.systemCostIndices.MEResearch
	case "research_time":
		return a.systemCostIndices.TEResearch
	default:
		if a.systemCostIndices.Manufacturing > 0 {
			return a.systemCostIndices.Manufacturing
		}
	}
	return fallback
}

func (a *IndustryAnalyzer) calculateInventionStep(params IndustryParams, tree *MaterialNode, fallbackCostIndex float64) (IndustryActivityStep, bool) {
	if params.ActivityMode != "invention" || tree == nil || tree.Blueprint == nil {
		return IndustryActivityStep{}, false
	}
	sourceBP, product, ok := a.findInventionForBlueprint(tree.Blueprint.BlueprintTypeID)
	if !ok || sourceBP == nil || product.TypeID == 0 {
		return IndustryActivityStep{}, false
	}
	chance := normalizeProbability(product.Probability)
	if params.InventionChance > 0 {
		// Absolute override: caller has already folded any skill / decryptor
		// / standings adjustments into this value. Don't double-apply.
		chance = normalizeProbability(params.InventionChance)
	} else {
		// Apply EVE's canonical invention skill formula to the SDE base:
		//   base × (1 + Enc/40) × (1 + (DC1+DC2)/30)
		// All levels zero = 1.0 (identity), preserving pre-skill-aware
		// callers. Then compose the decryptor multiplier from the mult path.
		if params.InventionEncryptionLevel > 0 || params.InventionDatacoreLevel1 > 0 || params.InventionDatacoreLevel2 > 0 {
			chance = chance * InventionSkillMultiplier(
				params.InventionEncryptionLevel,
				params.InventionDatacoreLevel1,
				params.InventionDatacoreLevel2,
			)
		}
		if params.InventionChanceMult > 0 && params.InventionChanceMult != 1.0 {
			// Frontend picked a decryptor without knowing this product's SDE
			// base probability — apply the picker's multiplier server-side.
			chance = chance * params.InventionChanceMult
		}
		if chance > 1 {
			chance = 1
		}
	}
	if chance <= 0 {
		return IndustryActivityStep{}, false
	}
	outputRuns := product.Quantity
	if params.InventionOutputRuns > 0 {
		outputRuns = params.InventionOutputRuns
	}
	if outputRuns <= 0 {
		outputRuns = 1
	}
	successesNeeded := math.Ceil(float64(params.Runs) / float64(outputRuns))
	if successesNeeded < 1 {
		successesNeeded = 1
	}
	expectedAttempts := successesNeeded / chance
	// Invention rigs apply here: TE reduces invention time, cost bonus
	// reduces invention job cost. ME rigs don't affect invention (datacore
	// materials aren't ME-reduced in EVE).
	invSec := a.resolveSystemSecurity(params.StructureRigs, params.SystemID)
	_, invRigTE, invRigCost := a.rigContribution(params.StructureRigs, "invention", product.TypeID, invSec)
	attemptMaterials := calculateActivityMaterials(sourceBP, "invention", 1, 0, 0, 0)
	materialCostPerAttempt := 0.0
	eivPerAttempt := 0.0
	for _, mat := range attemptMaterials {
		materialCostPerAttempt += a.materialCost(mat.TypeID, mat.Quantity, params.CostModel)
		eivPerAttempt += a.adjustedPrices[mat.TypeID] * float64(mat.Quantity)
	}
	// Same CCP formula as the tree job cost: SystemCost − StructureBonus
	// − RigBonus + FacilityTax + SCCSurcharge, all summed per attempt.
	invSCI := a.costIndexForActivity("invention", fallbackCostIndex)
	invSystemCost := eivPerAttempt * invSCI
	invStructureBonus := invSystemCost * (params.StructureJobCostReduction / 100.0)
	if invStructureBonus < 0 {
		invStructureBonus = 0
	}
	invRigBonus := invSystemCost * (invRigCost / 100.0)
	if invRigBonus < 0 {
		invRigBonus = 0
	}
	invGrossInstall := invSystemCost - invStructureBonus - invRigBonus
	if invGrossInstall < 0 {
		invGrossInstall = 0
	}
	invFacilityTax := eivPerAttempt * (params.FacilityTax / 100.0)
	invSCC := eivPerAttempt * params.sccSurchargePercent() / 100.0
	jobCostPerAttempt := invGrossInstall + invFacilityTax + invSCC
	// Accumulate the invention step's breakdown onto the analyzer's
	// running totals, scaled by expected attempts.
	a.jobCostBreakdown.EIV += eivPerAttempt * expectedAttempts
	a.jobCostBreakdown.SystemCost += invSystemCost * expectedAttempts
	a.jobCostBreakdown.StructureBonus += invStructureBonus * expectedAttempts
	a.jobCostBreakdown.RigBonus += invRigBonus * expectedAttempts
	a.jobCostBreakdown.GrossInstall += invGrossInstall * expectedAttempts
	a.jobCostBreakdown.FacilityTax += invFacilityTax * expectedAttempts
	a.jobCostBreakdown.SCCSurcharge += invSCC * expectedAttempts
	a.jobCostBreakdown.NetInstall += jobCostPerAttempt * expectedAttempts
	totalPerAttempt := materialCostPerAttempt + jobCostPerAttempt + params.DecryptorCost
	// Both source BP and invention output (T2 BPC) are blueprint typeIDs — use
	// the BP-aware name resolver so we don't emit "Type 41370" when the
	// invented BPC has no market group entry. BlueprintIsBPC = false because
	// the SOURCE (input) of invention is a T1 BP the user owns as BPO/BPC —
	// this step spends that BP, not a T2 BPC.
	// Per-step material bill: datacores from the invention activity BOM
	// scaled by expectedAttempts (invention has no ME reduction), plus the
	// decryptor (one per attempt) when the caller supplied a DecryptorTypeID.
	// The plan-patch builder attributes these to the invention task's
	// material rows so the task expansion in Operations shows what the user
	// actually needs to buy. flat_materials doesn't include datacores or
	// decryptors — they're routed through step.materials only, and the
	// material_diff footer aggregates all rows by type_id so nothing is
	// double-counted.
	stepMats := make([]IndustryActivityStepMaterial, 0, len(attemptMaterials)+1)
	for _, mat := range attemptMaterials {
		if mat.TypeID <= 0 || mat.Quantity <= 0 {
			continue
		}
		scaled := int32(math.Ceil(float64(mat.Quantity) * expectedAttempts))
		if scaled <= 0 {
			continue
		}
		stepMats = append(stepMats, IndustryActivityStepMaterial{
			TypeID:   mat.TypeID,
			TypeName: a.typeName(mat.TypeID),
			Quantity: scaled,
		})
	}
	if params.DecryptorTypeID > 0 {
		decQty := int32(math.Ceil(expectedAttempts))
		if decQty > 0 {
			stepMats = append(stepMats, IndustryActivityStepMaterial{
				TypeID:   params.DecryptorTypeID,
				TypeName: a.typeName(params.DecryptorTypeID),
				Quantity: decQty,
			})
		}
	}
	step := IndustryActivityStep{
		Activity:         "invention",
		BlueprintTypeID:  sourceBP.BlueprintTypeID,
		BlueprintName:    a.SDE.BlueprintName(sourceBP.BlueprintTypeID),
		ProductTypeID:    product.TypeID,
		ProductName:      a.SDE.BlueprintName(product.TypeID),
		BlueprintIsBPC:   false,
		Runs:             expectedAttempts,
		OutputQuantity:   int32(math.Ceil(successesNeeded)) * outputRuns,
		MaterialCost:     materialCostPerAttempt * expectedAttempts,
		JobCost:          jobCostPerAttempt * expectedAttempts,
		TotalCost:        totalPerAttempt * expectedAttempts,
		TimeSeconds:      int32(math.Ceil(float64(calculateActivityTime(sourceBP, "invention", 1, 0, invRigTE)) * expectedAttempts)),
		Probability:      chance,
		ExpectedAttempts: expectedAttempts,
		Reason:           "expected_bpc_cost",
		Materials:        stepMats,
	}
	if params.DecryptorTypeID > 0 {
		step.DecryptorTypeID = params.DecryptorTypeID
		step.DecryptorName = a.typeName(params.DecryptorTypeID)
	}
	return step, true
}

// calculateCopyStep emits a `copy` activity step when the source blueprint
// for an invention pipeline is BPO-only (user owns a BPO but no BPCs). In
// EVE you can't invent from a BPO — invention consumes a T1 BPC. So the
// analyzer prepends a copy job to materialize the BPCs first.
//
// Detection is client-input-driven: OwnedBlueprints[T1 mfg product typeID]
// carries IsBPO + AvailableRuns after the seeding pass. AvailableRuns is
// the count of BPC runs available in the user's pool; zero means the user
// has to copy first. If they have some BPCs (AvailableRuns > 0), no copy
// step is emitted even when a BPO also exists — the plan can lean on the
// existing BPCs. Future refinement: emit copy for the SHORTFALL when
// expected_attempts > AvailableRuns.
//
// Cost model mirrors invention/manufacturing: system cost index * EIV,
// structure/rig discounts, facility tax, SCC surcharge. Copy jobs almost
// never have materials in EVE so material_cost stays ~0.
func (a *IndustryAnalyzer) calculateCopyStep(
	params IndustryParams,
	inventionStep IndustryActivityStep,
	hasInvention bool,
	fallbackCostIndex float64,
) (IndustryActivityStep, bool) {
	if !hasInvention {
		log.Printf("[COPY] skip: hasInvention=false")
		return IndustryActivityStep{}, false
	}
	// Look up the T1 source BP; its manufacturing product is the key into
	// OwnedBlueprints for BPO/BPC discovery.
	sourceBP, ok := a.SDE.Industry.Blueprints[inventionStep.BlueprintTypeID]
	if !ok || sourceBP == nil {
		log.Printf("[COPY] skip: source BP %d not in SDE", inventionStep.BlueprintTypeID)
		return IndustryActivityStep{}, false
	}
	mfg := sourceBP.Activities["manufacturing"]
	if mfg == nil || len(mfg.Products) == 0 {
		log.Printf("[COPY] skip: source BP %d has no mfg activity/products", inventionStep.BlueprintTypeID)
		return IndustryActivityStep{}, false
	}
	t1ProductID := mfg.Products[0].TypeID
	if params.OwnedBlueprints == nil {
		log.Printf("[COPY] skip: params.OwnedBlueprints is nil (client didn't send owned_blueprints or all entries filtered out)")
		return IndustryActivityStep{}, false
	}
	owned, has := params.OwnedBlueprints[t1ProductID]
	if !has {
		log.Printf("[COPY] skip: T1 product %d not in OwnedBlueprints map (map has %d entries) — user doesn't own the T1 source BP that produces this item, or it wasn't sent by the client", t1ProductID, len(params.OwnedBlueprints))
		return IndustryActivityStep{}, false
	}
	// Copying requires a BPO — you can't copy from a BPC.
	if !owned.IsBPO {
		log.Printf("[COPY] skip: T1 product %d owned entry has IsBPO=false (AvailableRuns=%d) — no BPO to copy from", t1ProductID, owned.AvailableRuns)
		return IndustryActivityStep{}, false
	}
	// SHORTFALL sizing: invention consumes one BPC run per attempt. The user's
	// existing BPC pool (owned.AvailableRuns) covers part of that; we only copy
	// the remainder. Zero BPCs → copy everything (the original BPO-only case);
	// enough BPCs already → no copy step at all.
	requiredRuns := int64(math.Ceil(inventionStep.ExpectedAttempts))
	if requiredRuns < 1 {
		requiredRuns = 1
	}
	shortfallRuns := requiredRuns - int64(owned.AvailableRuns)
	if shortfallRuns <= 0 {
		log.Printf("[COPY] skip: T1 product %d has %d BPC runs available, invention needs %d — existing copies cover it", t1ProductID, owned.AvailableRuns, requiredRuns)
		return IndustryActivityStep{}, false
	}
	// SDE copy activity (may be nil for BPs that don't support copying —
	// rare but guard anyway).
	copyAct := sourceBP.Activities["copying"]
	if copyAct == nil {
		log.Printf("[COPY] skip: source BP %d has no copying activity in SDE", inventionStep.BlueprintTypeID)
		return IndustryActivityStep{}, false
	}
	log.Printf("[COPY] emitting: T1 product %d — invention needs %d runs, pool has %d, shortfall %d (source BP %d, %.2f expected attempts)",
		t1ProductID, requiredRuns, owned.AvailableRuns, shortfallRuns, inventionStep.BlueprintTypeID, inventionStep.ExpectedAttempts)
	baseCopyTime := copyAct.Time
	if baseCopyTime <= 0 {
		baseCopyTime = 60
	}
	// Sizing the copy job:
	//   shortfallRuns   = BPC runs invention needs beyond what's already in
	//                     the pool (computed above).
	//   maxRunsPerBPC   = SDE top-level `maxProductionLimit` — the cap on
	//                     runs any single BPC can hold when copied.
	//   copiesNeeded    = ceil(shortfallRuns / maxRunsPerBPC) — number of
	//                     BPCs to make.
	//   totalRuns       = copiesNeeded × maxRunsPerBPC — total mfg-run-
	//                     equivalents baked into all BPCs. Convention here
	//                     is "always copy to max runs per BPC": if the
	//                     shortfall is 3 and maxRunsPerBPC=600, we make 1
	//                     BPC with 600 runs on it (extra runs stay for
	//                     future invention jobs). Rationale: in EVE users
	//                     always max out copy runs, and idle BPC runs don't
	//                     burn anything.
	attemptsNeeded := shortfallRuns
	maxRunsPerBPC := int64(1)
	if sourceBP.MaxProductionLimit > 0 {
		maxRunsPerBPC = int64(sourceBP.MaxProductionLimit)
	}
	copiesNeeded := (attemptsNeeded + maxRunsPerBPC - 1) / maxRunsPerBPC
	if copiesNeeded < 1 {
		copiesNeeded = 1
	}
	totalRuns := copiesNeeded * maxRunsPerBPC
	// TE / cost rigs for copying — mirrors what invention/manufacturing do.
	copySec := a.resolveSystemSecurity(params.StructureRigs, params.SystemID)
	_, copyRigTE, copyRigCost := a.rigContribution(params.StructureRigs, "copying", t1ProductID, copySec)
	// Copy materials are almost always empty in EVE, but iterate anyway so
	// exotic BPs with copy materials (rare) still cost through.
	copyMaterials := calculateActivityMaterials(sourceBP, "copying", int32(copiesNeeded), 0, 0, 0)
	materialCost := 0.0
	eiv := 0.0
	for _, mat := range copyMaterials {
		materialCost += a.materialCost(mat.TypeID, mat.Quantity, params.CostModel)
		eiv += a.adjustedPrices[mat.TypeID] * float64(mat.Quantity)
	}
	// CCP's cost formula: when the copying activity has no material bill
	// (the common case), EIV = adjusted-price sum of one manufacturing run's
	// materials × TOTAL RUNS BEING COPIED. Total runs = attemptsNeeded (what
	// invention will consume). Matches the in-game copy-job window's install
	// cost, which scales with runs_per_copy × copies, not with copies alone.
	if eiv == 0 {
		perRunMats := calculateActivityMaterials(sourceBP, "manufacturing", 1, 0, 0, 0)
		perRunEIV := 0.0
		for _, mat := range perRunMats {
			perRunEIV += a.adjustedPrices[mat.TypeID] * float64(mat.Quantity)
		}
		// CCP's copy-cost formula multiplies the manufacturing-EIV base by
		// 0.02 (a copy job is priced at ~2% of the equivalent mfg job for
		// the same runs). Without this factor the analyzer produces ~50×
		// the in-game install cost for the same copy job. Scales by
		// totalRuns (copies × maxRunsPerBPC) because in EVE we always
		// max-out the runs per copy — see totalRuns comment above.
		eiv = perRunEIV * float64(totalRuns) * 0.02
		log.Printf("[COPY] EIV breakdown for BP %d: perRunEIV=%.2f, totalRuns=%d (copies=%d × maxPerBPC=%d, attemptsNeeded=%d), eiv=%.2f (post 0.02× copy factor), mfg_mats=%d types",
			inventionStep.BlueprintTypeID, perRunEIV, totalRuns, copiesNeeded, maxRunsPerBPC, attemptsNeeded, eiv, len(perRunMats))
	}
	// Time scaling: per-run copy time × totalRuns × rig TE reduction. Time
	// scales with total mfg-run-equivalents baked into the BPCs (same
	// convention as EIV — always max out runs per copy). TE bonus is a
	// percent reduction; 0 = no bonus.
	perRunTime := float64(baseCopyTime)
	if copyRigTE > 0 {
		perRunTime = perRunTime * (1 - copyRigTE/100.0)
		if perRunTime < 1 {
			perRunTime = 1
		}
	}
	totalTime := int32(math.Ceil(perRunTime * float64(totalRuns)))
	copySCI := a.costIndexForActivity("copy", fallbackCostIndex)
	systemCost := eiv * copySCI
	structureBonus := systemCost * (params.StructureJobCostReduction / 100.0)
	if structureBonus < 0 {
		structureBonus = 0
	}
	rigBonus := systemCost * (copyRigCost / 100.0)
	if rigBonus < 0 {
		rigBonus = 0
	}
	grossInstall := systemCost - structureBonus - rigBonus
	if grossInstall < 0 {
		grossInstall = 0
	}
	facilityTax := eiv * (params.FacilityTax / 100.0)
	scc := eiv * params.sccSurchargePercent() / 100.0
	jobCost := grossInstall + facilityTax + scc
	a.jobCostBreakdown.EIV += eiv
	a.jobCostBreakdown.SystemCost += systemCost
	a.jobCostBreakdown.StructureBonus += structureBonus
	a.jobCostBreakdown.RigBonus += rigBonus
	a.jobCostBreakdown.GrossInstall += grossInstall
	a.jobCostBreakdown.FacilityTax += facilityTax
	a.jobCostBreakdown.SCCSurcharge += scc
	a.jobCostBreakdown.NetInstall += jobCost
	log.Printf("[COPY] cost: eiv=%.2f SCI=%.4f sys=%.2f str=-%.2f rig=-%.2f gross=%.2f tax=%.2f scc=%.2f total=%.2f (copies=%d × maxPerBPC=%d = totalRuns=%d, attemptsNeeded=%d, dur=%ds)",
		eiv, copySCI, systemCost, structureBonus, rigBonus, grossInstall, facilityTax, scc, jobCost, copiesNeeded, maxRunsPerBPC, totalRuns, attemptsNeeded, totalTime)
	return IndustryActivityStep{
		Activity:        "copy",
		BlueprintTypeID: sourceBP.BlueprintTypeID,
		BlueprintName:   a.SDE.BlueprintName(sourceBP.BlueprintTypeID),
		ProductTypeID:   sourceBP.BlueprintTypeID, // copies are of the same typeID
		ProductName:     a.SDE.BlueprintName(sourceBP.BlueprintTypeID),
		BlueprintIsBPC:  false, // source is BPO
		Runs:            float64(copiesNeeded),
		OutputQuantity:  int32(copiesNeeded),
		MaterialCost:    materialCost,
		JobCost:         jobCost,
		TotalCost:       materialCost + jobCost,
		TimeSeconds:     totalTime,
		Reason:          "materialize_bpcs_for_invention",
	}, true
}

// calculateResearchSteps emits `research_material` and/or `research_time`
// activity steps when the root blueprint's actual ME/TE (from
// OwnedBlueprints) is below the analysis target (params.MaterialEfficiency
// / params.TimeEfficiency). Root-BP-only for now — sub-BP research is a
// followup, since the analyzer doesn't currently model per-sub-BP
// research targets.
//
// TIME MODEL: The SDE ships one "base time" per activity; EVE's actual
// per-level time is base × 105^level (ME) or 105^level (TE), a geometric
// series. For MVP simplicity this uses `base × levels` (linear) which
// under-estimates for high target levels. Callers who need level-accurate
// scheduling should treat these steps as advisory.
//
// COST MODEL: Research jobs have no materials in EVE, so EIV = 0 and the
// standard SCI × EIV formula yields 0 cost. Real EVE research charges a
// small install fee derived from the blueprint's own value; that's not
// modeled here yet.
func (a *IndustryAnalyzer) calculateResearchSteps(params IndustryParams, fallbackCostIndex float64) []IndustryActivityStep {
	if params.OwnedBlueprints == nil {
		return nil
	}
	// Look up the root blueprint from the product typeID being analyzed.
	if a.SDE == nil || a.SDE.Industry == nil {
		return nil
	}
	rootBPID, ok := a.SDE.Industry.ProductToBlueprint[params.TypeID]
	if !ok || rootBPID == 0 {
		return nil
	}
	bp, ok := a.SDE.Industry.Blueprints[rootBPID]
	if !ok || bp == nil {
		return nil
	}
	owned, has := params.OwnedBlueprints[params.TypeID]
	if !has || !owned.IsBPO {
		// Research only applies to BPOs. BPCs come pre-researched at
		// their invention output ME/TE (or the researched BPO's) and
		// can't be researched further.
		return nil
	}
	var out []IndustryActivityStep
	if act := bp.Activities["research_material"]; act != nil {
		if params.MaterialEfficiency > owned.ME {
			out = append(out, a.buildResearchStep("research_material", bp, act, params.MaterialEfficiency-owned.ME, params, fallbackCostIndex))
		}
	}
	if act := bp.Activities["research_time"]; act != nil {
		if params.TimeEfficiency > owned.TE {
			out = append(out, a.buildResearchStep("research_time", bp, act, params.TimeEfficiency-owned.TE, params, fallbackCostIndex))
		}
	}
	return out
}

func (a *IndustryAnalyzer) buildResearchStep(
	activity string,
	bp *sde.Blueprint,
	act *sde.ActivityData,
	levels int32,
	params IndustryParams,
	fallbackCostIndex float64,
) IndustryActivityStep {
	baseTime := act.Time
	if baseTime <= 0 {
		baseTime = 60
	}
	// Linear approximation — see calculateResearchSteps for the caveat.
	totalTime := int32(math.Max(1, float64(baseTime)*float64(levels)))
	sci := a.costIndexForActivity(activity, fallbackCostIndex)
	// EIV=0 because research has no materials; the formula collapses to 0
	// but we compute it anyway so future material-carrying research (rare
	// exotic BPs) is handled uniformly.
	systemCost := 0.0
	structureBonus := 0.0
	rigBonus := 0.0
	facilityTax := 0.0
	scc := 0.0
	jobCost := systemCost - structureBonus - rigBonus + facilityTax + scc
	_ = sci // reserved for future material-carrying research; keeps the
	// route wired if EIV becomes non-zero later.
	return IndustryActivityStep{
		Activity:        activity,
		BlueprintTypeID: bp.BlueprintTypeID,
		BlueprintName:   a.SDE.BlueprintName(bp.BlueprintTypeID),
		ProductTypeID:   bp.BlueprintTypeID, // research is done to the BP itself
		ProductName:     a.SDE.BlueprintName(bp.BlueprintTypeID),
		BlueprintIsBPC:  false,
		Runs:            float64(levels),
		OutputQuantity:  levels,
		MaterialCost:    0,
		JobCost:         jobCost,
		TotalCost:       jobCost,
		TimeSeconds:     totalTime,
		Reason:          "close_me_te_gap",
	}
}

func (a *IndustryAnalyzer) findInventionForBlueprint(blueprintTypeID int32) (*sde.Blueprint, sde.BlueprintProduct, bool) {
	if a == nil || a.SDE == nil || a.SDE.Industry == nil {
		return nil, sde.BlueprintProduct{}, false
	}
	for _, bp := range a.SDE.Industry.Blueprints {
		act := bp.Activities["invention"]
		if act == nil {
			continue
		}
		for _, product := range act.Products {
			if product.TypeID == blueprintTypeID {
				return bp, product, true
			}
		}
	}
	return nil, sde.BlueprintProduct{}, false
}

// buildActivityPlan walks the material tree post-order and emits one step per
// buildable node, then COLLAPSES duplicates.
//
// A shared component (Nitrogen Fuel Block feeding six different parents, say)
// appears once per tree position. Emitting all of them produces a task list
// with the same job repeated a dozen times, which is unusable in Operations
// and wrong as a work plan — in EVE you queue one job for the combined runs,
// not N jobs for the same product. Collapsing sums runs / output / costs /
// time across every occurrence.
//
// Ordering: post-order means every occurrence of a component precedes all of
// its consumers, so keeping the FIRST occurrence's position preserves the
// dependency ordering for every consumer that followed it.
func (a *IndustryAnalyzer) buildActivityPlan(root *MaterialNode) []IndustryActivityStep {
	var out []IndustryActivityStep
	// (activity, productTypeID) → index into out, for the collapse.
	type stepKey struct {
		Activity  string
		ProductID int32
	}
	indexByKey := make(map[stepKey]int)
	var walk func(*MaterialNode)
	walk = func(node *MaterialNode) {
		if node == nil {
			return
		}
		for _, child := range node.Children {
			walk(child)
		}
		if node.IsBase || !node.ShouldBuild || node.Blueprint == nil {
			return
		}
		key := stepKey{Activity: node.Activity, ProductID: node.TypeID}
		if idx, seen := indexByKey[key]; seen {
			// Merge into the existing step. Runs/quantity/cost/time are all
			// additive; the identity fields (blueprint, names, probability)
			// are identical by construction for the same product+activity.
			existing := &out[idx]
			existing.Runs += float64(node.Runs)
			existing.OutputQuantity += node.Quantity
			existing.MaterialCost += node.MaterialCost
			existing.JobCost += node.JobCost
			existing.TotalCost += node.BuildCost
			existing.TimeSeconds += node.Blueprint.Time
			return
		}
		isBPC := false
		if a.SDE != nil && a.SDE.Industry != nil {
			isBPC = a.SDE.Industry.InventionProducts[node.Blueprint.BlueprintTypeID]
		}
		indexByKey[key] = len(out)
		out = append(out, IndustryActivityStep{
			Activity:        node.Activity,
			BlueprintTypeID: node.Blueprint.BlueprintTypeID,
			BlueprintName:   a.SDE.BlueprintName(node.Blueprint.BlueprintTypeID),
			ProductTypeID:   node.TypeID,
			ProductName:     node.TypeName,
			Runs:            float64(node.Runs),
			OutputQuantity:  node.Quantity,
			MaterialCost:    node.MaterialCost,
			JobCost:         node.JobCost,
			TotalCost:       node.BuildCost,
			TimeSeconds:     node.Blueprint.Time,
			Probability:     node.Blueprint.Probability,
			BlueprintIsBPC:  isBPC,
		})
	}
	walk(root)
	return out
}

func sumActivityPlanTime(steps []IndustryActivityStep) int32 {
	var total int64
	for _, step := range steps {
		if step.TimeSeconds > 0 {
			total += int64(step.TimeSeconds)
		}
	}
	const maxInt32 = int64(1<<31 - 1)
	if total > maxInt32 {
		return int32(maxInt32)
	}
	return int32(total)
}

func (a *IndustryAnalyzer) typeName(typeID int32) string {
	if a != nil && a.SDE != nil {
		if t, ok := a.SDE.Types[typeID]; ok {
			return t.Name
		}
	}
	return fmt.Sprintf("Type %d", typeID)
}

// flattenMaterials creates a shopping list of base materials.
func (a *IndustryAnalyzer) flattenMaterials(root *MaterialNode) []*FlatMaterial {
	materialMap := make(map[int32]*FlatMaterial)
	a.collectBaseMaterials(root, 1.0, materialMap)

	// Convert to slice and sort by total price
	result := make([]*FlatMaterial, 0, len(materialMap))
	for _, m := range materialMap {
		result = append(result, m)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].TotalPrice > result[j].TotalPrice
	})

	return result
}

// collectBaseMaterials recursively collects materials that should be bought.
// The `scale` parameter propagates split-strategy fractions down the tree:
// when an ancestor node is a mixed buy+build split, only the build portion's
// share of its own quantity feeds down into descendant material requirements.
// A leaf's actual shopping-list quantity is `node.Quantity * scale`.
//
// Split nodes contribute BOTH sides: the buy portion of the node is added
// to the shopping list directly (scaled qty at market prices), and the
// build portion recurses into children with a further-multiplied scale.
// Without this, the shopping list would ask you to buy raw materials for
// the full parent quantity even though only a fraction is being built.
func (a *IndustryAnalyzer) collectBaseMaterials(node *MaterialNode, scale float64, materials map[int32]*FlatMaterial) {
	if scale <= 0 {
		return
	}
	// Split node: shopping list gets the buy portion at the split's walked
	// cost; the build portion pushes down into children at a compounded
	// scale so their raw materials reflect the fraction actually built.
	if node.ShouldSplit && !node.IsBase {
		if node.BuyUnits > 0 && node.Quantity > 0 {
			buyQty := int32(math.Round(float64(node.BuyUnits) * scale))
			if buyQty > 0 {
				buyCost := node.BuyPortionCost * scale
				a.addFlatMaterial(materials, node.TypeID, node.TypeName, buyQty, buyCost)
			}
		}
		if node.BuildUnits > 0 && node.Quantity > 0 {
			childScale := scale * float64(node.BuildUnits) / float64(node.Quantity)
			for _, child := range node.Children {
				a.collectBaseMaterials(child, childScale, materials)
			}
		}
		return
	}
	// Binary buy: node is bought whole. node.BuyPrice already includes any
	// broker fee the caller applied via CostModel.
	if !node.ShouldBuild || node.IsBase {
		qty := int32(math.Round(float64(node.Quantity) * scale))
		if qty <= 0 {
			return
		}
		cost := node.BuyPrice * scale
		a.addFlatMaterial(materials, node.TypeID, node.TypeName, qty, cost)
		return
	}
	// Binary build: recurse into children with the current scale.
	for _, child := range node.Children {
		a.collectBaseMaterials(child, scale, materials)
	}
}

// addFlatMaterial merges (typeID, qty, cost) into the shopping-list map,
// summing across duplicate contributions from different branches of the
// tree (a split-buy'd component may also appear as a raw material for
// another node's build portion). Volume is looked up from the SDE.
func (a *IndustryAnalyzer) addFlatMaterial(materials map[int32]*FlatMaterial, typeID int32, typeName string, qty int32, totalCost float64) {
	if qty <= 0 {
		return
	}
	volume := 0.0
	if t, ok := a.SDE.Types[typeID]; ok {
		volume = t.Volume
	}
	if existing, ok := materials[typeID]; ok {
		existing.Quantity += qty
		existing.TotalPrice += totalCost
		if existing.Quantity > 0 {
			existing.UnitPrice = existing.TotalPrice / float64(existing.Quantity)
		}
		existing.Volume += volume * float64(qty)
		return
	}
	unitPrice := 0.0
	if qty > 0 {
		unitPrice = totalCost / float64(qty)
	}
	materials[typeID] = &FlatMaterial{
		TypeID:     typeID,
		TypeName:   typeName,
		Quantity:   qty,
		UnitPrice:  unitPrice,
		TotalPrice: totalCost,
		Volume:     volume * float64(qty),
	}
}

// resolveMarketRegion chooses the market region for pricing.
// Priority for the pricing region:
//   1. PricingSystemID — explicit override used by the scanner so the user
//      can build in one region and read prices from another. When set, it
//      wins over both SystemID and StationID.
//   2. SystemID — the legacy "build system also drives pricing" behavior used
//      by the single-location analysis flow.
//   3. StationID — fallback when neither system is provided.
//   4. The Forge (Jita) as the absolute default.
func (a *IndustryAnalyzer) resolveMarketRegion(params IndustryParams) (int32, string) {
	// Default: The Forge (Jita)
	regionID := int32(10000002)
	regionName := ""

	if params.PricingSystemID != 0 {
		if sys, ok := a.SDE.Systems[params.PricingSystemID]; ok && sys.RegionID != 0 {
			regionID = sys.RegionID
		}
	} else if params.SystemID != 0 {
		if sys, ok := a.SDE.Systems[params.SystemID]; ok && sys.RegionID != 0 {
			regionID = sys.RegionID
		}
	} else if params.StationID != 0 {
		if st, ok := a.SDE.Stations[params.StationID]; ok {
			if sys, ok := a.SDE.Systems[st.SystemID]; ok && sys.RegionID != 0 {
				regionID = sys.RegionID
			}
		}
	}

	if r, ok := a.SDE.Regions[regionID]; ok {
		regionName = r.Name
	}
	return regionID, regionName
}

func mergeMarketPrices(regionPrices, stationPrices map[int32]float64) map[int32]float64 {
	out := make(map[int32]float64, len(regionPrices)+len(stationPrices))
	for typeID, price := range regionPrices {
		out[typeID] = price
	}
	for typeID, price := range stationPrices {
		// Station-specific price wins when available.
		out[typeID] = price
	}
	return out
}

func groupIndustryOrdersByType(orders []esi.MarketOrder, locationID int64, isBuy bool) map[int32][]esi.MarketOrder {
	out := make(map[int32][]esi.MarketOrder)
	for _, o := range orders {
		if locationID != 0 && o.LocationID != locationID {
			continue
		}
		if o.VolumeRemain <= 0 || o.Price <= 0 {
			continue
		}
		o.IsBuyOrder = isBuy
		out[o.TypeID] = append(out[o.TypeID], o)
	}
	return out
}

func (a *IndustryAnalyzer) fetchMarketBooks(params IndustryParams) (map[int32][]esi.MarketOrder, map[int32][]esi.MarketOrder, error) {
	regionID, _ := a.resolveMarketRegion(params)

	sellOrders, err := a.ESI.FetchRegionOrders(regionID, "sell")
	if err != nil {
		return nil, nil, err
	}
	buyOrders, err := a.ESI.FetchRegionOrders(regionID, "buy")
	if err != nil {
		return nil, nil, err
	}

	return groupIndustryOrdersByType(sellOrders, params.StationID, false),
		groupIndustryOrdersByType(buyOrders, params.StationID, true),
		nil
}

func (a *IndustryAnalyzer) marketBestAsk(typeID int32) float64 {
	if price := a.marketPrices[typeID]; price > 0 {
		return price
	}
	orders := a.marketSellOrders[typeID]
	best := 0.0
	for _, o := range orders {
		if o.Price <= 0 || o.VolumeRemain <= 0 {
			continue
		}
		if best == 0 || o.Price < best {
			best = o.Price
		}
	}
	return best
}

// marketBookDepth returns total unit volume visible on the sell / buy side
// of the pricing-region order book for the type. Used to flag rows whose
// revenue calc is a fantasy because the ask side has ~1 unit and the batch
// is 100. Zero when the order book isn't loaded (older code paths fall
// back to `a.marketPrices` scalars which have no depth info attached).
func (a *IndustryAnalyzer) marketBookDepth(typeID int32) (askUnits, bidUnits int64) {
	for _, o := range a.marketSellOrders[typeID] {
		if o.Price <= 0 || o.VolumeRemain <= 0 {
			continue
		}
		askUnits += int64(o.VolumeRemain)
	}
	for _, o := range a.marketBuyOrders[typeID] {
		if o.Price <= 0 || o.VolumeRemain <= 0 {
			continue
		}
		bidUnits += int64(o.VolumeRemain)
	}
	return
}

// marketBookOrderCounts returns the number of distinct active sell / buy
// orders in the pricing region. Complements marketBookDepth: 100 units in
// 20 orders means real competition, 100 units in 1 order means a single
// seller. The pair (depth, count) is what the scanner surfaces in the
// per-row tooltip so the user can distinguish a busy market from one bait
// listing that happens to have volume behind it.
func (a *IndustryAnalyzer) marketBookOrderCounts(typeID int32) (askOrders, bidOrders int32) {
	for _, o := range a.marketSellOrders[typeID] {
		if o.Price <= 0 || o.VolumeRemain <= 0 {
			continue
		}
		askOrders++
	}
	for _, o := range a.marketBuyOrders[typeID] {
		if o.Price <= 0 || o.VolumeRemain <= 0 {
			continue
		}
		bidOrders++
	}
	return
}

// computeMixedMaterialCost walks the sell-order book for a type and decides,
// per unit, whether to buy at market or build. Returns the buy portion of
// the split (units + cost). Callers combine it with a per-unit build cost
// applied to the remainder.
//
// Motivation: the analyzer used to make a binary all-buy or all-build
// decision per material. When the book has a cheap head then jumps
// expensive, the optimal is a mixed strategy — buy the cheap head at
// market prices, build the rest. Example: need 100 units, book is
// [30 @ 5M, 70 @ 20M], build cost 700M. All-buy walks to 1550M,
// all-build is 700M, mixed = 150M + 490M = 640M — beats both.
//
// Orders are sorted cheapest-first before walking so the caller doesn't
// have to. Orders with zero/negative price or volume are filtered.
// perUnitBuildCost is the linear per-unit build cost (BuildCost /
// Quantity); the walk stops as soon as an order's price ≥ that cost.
// Assumes per-unit build cost is linear across the split, which holds
// past the batch-overshoot guard threshold — reactions/ammo BPs with
// step-function costs are marked base below 50% batch utilization and
// never reach this function.
func computeMixedMaterialCost(orders []esi.MarketOrder, quantity int32, perUnitBuildCost float64) (buyUnits int32, buyCost float64) {
	if quantity <= 0 || perUnitBuildCost <= 0 || len(orders) == 0 {
		return 0, 0
	}
	sorted := make([]esi.MarketOrder, 0, len(orders))
	for _, o := range orders {
		if o.Price <= 0 || o.VolumeRemain <= 0 {
			continue
		}
		sorted = append(sorted, o)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Price < sorted[j].Price })

	remaining := quantity
	for _, o := range sorted {
		if remaining <= 0 {
			break
		}
		if o.Price >= perUnitBuildCost {
			// From here on, every order costs at least as much per unit
			// as building. Building the remainder wins.
			break
		}
		take := int32(o.VolumeRemain)
		if take > remaining {
			take = remaining
		}
		buyCost += float64(take) * o.Price
		buyUnits += take
		remaining -= take
	}
	return
}

func (a *IndustryAnalyzer) marketBuyCost(typeID int32, quantity int32) float64 {
	if quantity <= 0 {
		return 0
	}
	if orders := a.marketSellOrders[typeID]; len(orders) > 0 {
		plan := ComputeExecutionPlan(orders, quantity, true)
		if plan.CanFill && plan.TotalCost > 0 && !math.IsNaN(plan.TotalCost) && !math.IsInf(plan.TotalCost, 0) {
			return plan.TotalCost
		}
	}
	return a.marketBestAsk(typeID) * float64(quantity)
}

// marketBestBid returns the highest live buy-order price for the type, or 0
// when the buy side of the book is empty. Used by the "buy_to_buy" cost model
// which prices materials at the price a patient buyer would list at.
func (a *IndustryAnalyzer) marketBestBid(typeID int32) float64 {
	orders := a.marketBuyOrders[typeID]
	best := 0.0
	for _, o := range orders {
		if o.Price <= 0 || o.VolumeRemain <= 0 {
			continue
		}
		if o.Price > best {
			best = o.Price
		}
	}
	return best
}

// materialCost dispatches the per-material buy-side quote based on the
// caller's CostModel. Falls back to the ask-side walk (buy_to_sell) when the
// buy book is empty for the type — a patient buy order on a type nobody's
// currently bidding on has no meaningful reference price, so we degrade to the
// instant-cost quote rather than emit 0 and pretend materials are free.
func (a *IndustryAnalyzer) materialCost(typeID int32, quantity int32, model string) float64 {
	direct := a.materialCostRaw(typeID, quantity, model)
	if a.reprocessingSources == nil || quantity <= 0 {
		return direct
	}
	// See if any ore reprocesses into this material more cheaply than
	// the direct market price. Net yield reflects the current params
	// (skills, tax, implants, base station yield) captured at Analyze()
	// entry. Audit P2.5.
	perUnit, ok := a.cheapestReprocessedCostPerUnit(typeID, a.currentReprocessingNetYield, model)
	if !ok {
		return direct
	}
	reproCost := perUnit * float64(quantity)
	if direct > 0 && reproCost >= direct {
		return direct
	}
	if direct <= 0 {
		return reproCost
	}
	return reproCost
}

// materialCostRaw is the no-reprocessing-alternative price path. The
// public materialCost wraps this with the ore-reprocess-vs-direct
// comparison so the analyzer picks the cheaper sourcing per material.
func (a *IndustryAnalyzer) materialCostRaw(typeID int32, quantity int32, model string) float64 {
	if quantity <= 0 {
		return 0
	}
	if model == "buy_to_buy" {
		if bid := a.marketBestBid(typeID); bid > 0 {
			return bid * float64(quantity)
		}
	}
	return a.marketBuyCost(typeID, quantity)
}

func (a *IndustryAnalyzer) marketInstantSellRevenue(typeID int32, quantity int32, revenueMult float64) (float64, bool) {
	if quantity <= 0 || revenueMult <= 0 {
		return 0, false
	}
	orders := a.marketBuyOrders[typeID]
	if len(orders) == 0 {
		return 0, false
	}
	plan := ComputeExecutionPlan(orders, quantity, false)
	if !plan.CanFill || plan.TotalCost <= 0 || math.IsNaN(plan.TotalCost) || math.IsInf(plan.TotalCost, 0) {
		return 0, false
	}
	return plan.TotalCost * revenueMult, true
}

// fetchMarketPrices fetches best sell order prices for materials.
// If StationID is provided, station-specific prices are used with per-item fallback
// to regional prices so missing station liquidity doesn't zero out pricing.
func (a *IndustryAnalyzer) fetchMarketPrices(params IndustryParams) (map[int32]float64, error) {
	regionID, _ := a.resolveMarketRegion(params)

	regionPrices, err := a.ESI.GetCachedMarketPrices(a.IndustryCache, regionID)
	if err != nil {
		return nil, err
	}

	if params.StationID == 0 {
		return regionPrices, nil
	}

	stationPrices, err := a.ESI.GetCachedMarketPricesByLocation(a.IndustryCache, regionID, params.StationID)
	if err != nil {
		// Graceful fallback: station-level fetch failed, keep regional pricing.
		log.Printf("Warning: failed to fetch station prices for location %d in region %d: %v",
			params.StationID, regionID, err)
		return regionPrices, nil
	}
	if len(stationPrices) == 0 {
		// No visible liquidity on selected station/structure; use region fallback.
		return regionPrices, nil
	}

	return mergeMarketPrices(regionPrices, stationPrices), nil
}

// GetBlueprintInfo returns blueprint information for a type.
func (a *IndustryAnalyzer) GetBlueprintInfo(typeID int32) (*sde.Blueprint, bool) {
	return a.SDE.Industry.GetBlueprintForProduct(typeID)
}

// SearchResult holds a search result with relevance score.
type SearchResult struct {
	TypeID       int32  `json:"type_id"`
	TypeName     string `json:"type_name"`
	HasBlueprint bool   `json:"has_blueprint"`
	// IsT2BP is true when this item's blueprint is produced via invention
	// (its blueprintTypeID appears in some other blueprint's invention
	// products). The Analyze tab uses this to default ME/TE to 2/4 for T2
	// items instead of the T1 BPO-researched 10/20.
	IsT2BP bool `json:"is_t2_bp"`
	// BaseInventionRuns is the base runs of one invented BPC that produces
	// this product (before decryptor bonuses). Zero for non-invented items.
	// The Analyze tab uses it to auto-scale the "runs" field to a full BPC
	// so invention amortization spreads over the whole 10/100/1-run BPC.
	BaseInventionRuns int32 `json:"base_invention_runs"`
	relevance         int   // 0 = exact, 1 = starts with, 2 = contains
}

// SearchBuildableItems returns items matching the query.
// Searches all market items and indicates if they have a blueprint.
// Results are sorted by relevance: exact match > starts with > contains.
func (a *IndustryAnalyzer) SearchBuildableItems(query string, limit int) []SearchResult {
	if limit <= 0 {
		limit = 20
	}

	queryLower := strings.ToLower(strings.TrimSpace(query))
	if queryLower == "" {
		return []SearchResult{}
	}

	var results []SearchResult

	// Search ALL types (not just those with blueprints)
	for typeID, t := range a.SDE.Types {
		nameLower := strings.ToLower(t.Name)

		// Check for match and determine relevance
		var relevance int
		if nameLower == queryLower {
			relevance = 0 // Exact match - highest priority
		} else if strings.HasPrefix(nameLower, queryLower) {
			relevance = 1 // Starts with - high priority
		} else if strings.Contains(nameLower, queryLower) {
			relevance = 2 // Contains - normal priority
		} else {
			continue // No match
		}

		// Check if this item has a blueprint (safely)
		hasBlueprint := false
		isT2 := false
		var baseInvRuns int32
		if a.SDE.Industry != nil {
			bpID, ok := a.SDE.Industry.ProductToBlueprint[typeID]
			hasBlueprint = ok
			if ok && a.SDE.Industry.InventionProducts[bpID] {
				isT2 = true
				baseInvRuns = a.SDE.Industry.InventionOutputRunsByBPC[bpID]
			}
		}

		results = append(results, SearchResult{
			TypeID:            typeID,
			TypeName:          t.Name,
			HasBlueprint:      hasBlueprint,
			IsT2BP:            isT2,
			BaseInventionRuns: baseInvRuns,
			relevance:         relevance,
		})
	}

	// Sort: items with blueprints first, then by relevance, then alphabetically
	sort.Slice(results, func(i, j int) bool {
		// Prioritize items with blueprints
		if results[i].HasBlueprint != results[j].HasBlueprint {
			return results[i].HasBlueprint
		}
		if results[i].relevance != results[j].relevance {
			return results[i].relevance < results[j].relevance
		}
		return results[i].TypeName < results[j].TypeName
	})

	// Limit results
	if len(results) > limit {
		results = results[:limit]
	}

	return results
}
