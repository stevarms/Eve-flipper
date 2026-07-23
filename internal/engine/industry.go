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
	BrokerFee           float64 // Broker fee % when buying materials / product (default 0)
	SalesTaxPercent     float64 // Sales tax % when selling product (for display / future use)
	ReprocessingYield   float64 // Reprocessing efficiency (0-1, e.g., 0.50 for 50%)
	IncludeReprocessing bool    // Whether to consider reprocessing ore as alternative
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
	DecryptorCost       float64 // Optional per-attempt decryptor cost
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
	BuyPrice     float64         `json:"buy_price"`     // Market buy price (sell orders)
	MaterialCost float64         `json:"material_cost"` // Sum of chosen child material costs
	BuildCost    float64         `json:"build_cost"`    // Total cost to build (materials + job cost)
	ShouldBuild  bool            `json:"should_build"`  // True if building is cheaper than buying
	JobCost      float64         `json:"job_cost"`      // Manufacturing job installation cost
	Children     []*MaterialNode `json:"children"`      // Required sub-materials
	Blueprint    *BlueprintInfo  `json:"blueprint"`     // Blueprint info if buildable
	Depth        int             `json:"depth"`         // Depth in tree
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

// jobCostSCCSurchargePercent is CCP's flat "Secure Commerce Commission"
// surcharge, added to every job's install cost as a fixed % of EIV
// regardless of structure or location. Currently 4% (post-Uprising).
const jobCostSCCSurchargePercent = 4.0

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

	savings := marketBuyPrice - optimalCost
	savingsPercent := 0.0
	if marketBuyPrice > 0 {
		savingsPercent = savings / marketBuyPrice * 100
	}

	// FIX #6: Calculate profit if you sell the built product.
	// Revenue = sell price × quantity × (1 - salesTax%) × (1 - brokerFee%)
	makerSellRevenue := a.marketBestAsk(params.TypeID) * float64(totalQuantity) *
		(1.0 - params.SalesTaxPercent/100) *
		(1.0 - params.BrokerFee/100)
	instantSellRevenue, instantSellAvailable := a.marketInstantSellRevenue(
		params.TypeID,
		totalQuantity,
		1.0-params.SalesTaxPercent/100,
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

	node.Blueprint = &BlueprintInfo{
		BlueprintTypeID: bp.BlueprintTypeID,
		ProductQuantity: productQuantity,
		ME:              params.MaterialEfficiency,
		TE:              params.TimeEfficiency,
		Time:            calculateActivityTime(bp, activity, runsNeeded, params.TimeEfficiency, rigTE),
		Activity:        activity,
		Probability:     probability,
	}

	// EVE formula: max(runs, ceil(base × runs × (1-ME/100) × (1-structureBonus/100) × (1-rigMEReduction/100)))
	materials := calculateActivityMaterials(bp, activity, runsNeeded, params.MaterialEfficiency, params.StructureBonus, rigME)

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

	// Calculate material cost (sum of optimal costs for children)
	var materialCost float64
	for _, child := range node.Children {
		if child.ShouldBuild {
			materialCost += child.BuildCost
		} else {
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
	sccSurcharge := eiv * jobCostSCCSurchargePercent / 100.0
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

	// Decide: buy or build. BuildMode overrides the cost-based choice:
	//   "buy_all"   → prefer buy when a buy price exists.
	//   "build_all" → prefer build when the node is buildable (has children).
	//   "auto"/""   → pick the cheaper of the two.
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
	switch activity {
	case "reaction":
		if a.systemCostIndices.Reaction > 0 {
			return a.systemCostIndices.Reaction
		}
	case "invention":
		if a.systemCostIndices.Invention > 0 {
			return a.systemCostIndices.Invention
		}
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
		chance = normalizeProbability(params.InventionChance)
	} else if params.InventionChanceMult > 0 && params.InventionChanceMult != 1.0 {
		// Frontend picked a decryptor without knowing this product's SDE
		// base probability — apply the picker's multiplier server-side.
		chance = chance * params.InventionChanceMult
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
	invSCC := eivPerAttempt * jobCostSCCSurchargePercent / 100.0
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
	}
	return step, true
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

func (a *IndustryAnalyzer) buildActivityPlan(root *MaterialNode) []IndustryActivityStep {
	var out []IndustryActivityStep
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
		isBPC := false
		if a.SDE != nil && a.SDE.Industry != nil {
			isBPC = a.SDE.Industry.InventionProducts[node.Blueprint.BlueprintTypeID]
		}
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
	a.collectBaseMaterials(root, materialMap)

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
func (a *IndustryAnalyzer) collectBaseMaterials(node *MaterialNode, materials map[int32]*FlatMaterial) {
	// If we should buy this node (not build), add it to the list (node.BuyPrice already includes broker)
	if !node.ShouldBuild || node.IsBase {
		if existing, ok := materials[node.TypeID]; ok {
			existing.Quantity += node.Quantity
			existing.TotalPrice += node.BuyPrice
			existing.UnitPrice = existing.TotalPrice / float64(existing.Quantity)
		} else {
			volume := 0.0
			if t, ok := a.SDE.Types[node.TypeID]; ok {
				volume = t.Volume
			}
			materials[node.TypeID] = &FlatMaterial{
				TypeID:     node.TypeID,
				TypeName:   node.TypeName,
				Quantity:   node.Quantity,
				UnitPrice:  node.BuyPrice / float64(node.Quantity),
				TotalPrice: node.BuyPrice,
				Volume:     volume * float64(node.Quantity),
			}
		}
		return
	}

	// Otherwise, recurse into children
	for _, child := range node.Children {
		a.collectBaseMaterials(child, materials)
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
