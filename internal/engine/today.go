package engine

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// today.go — the work order for one session.
//
// Everything the app can recommend, reduced to one ranked list you can walk
// top to bottom in twenty minutes. Trading, inventory, industry and PI all
// produce ISK in different shapes — a relist is one-off, a new buy order is
// a rate, a stalled extractor is a loss you are already taking — so each is
// normalized to the same figure: **ISK over the next seven days**, computed
// twice.
//
//   - ExpectedISK7d is the median view. It is displayed and never ranked on.
//   - DownsideISK7d is the realistic-bad view. It is what the queue sorts by.
//
// Ranking on the median is how a tool ends up recommending lottery tickets:
// a 10M "opportunity" in an illiquid item outranks a reliable 2M reprice
// every time, and the 10M arrives about a third as often as advertised.
// Ranking on the downside inverts that, and it costs nothing, because the
// conservative figure already exists everywhere we look —
// StationTrade.RealizableDailyProfit, StationCommandForecast's P80 band, and
// OrderDeskOrder.FlowBasis all say how much the optimistic number should be
// believed.
//
// On top of the downside, every action is graded against evidence
// (today_risk.go). Only `proven` and `likely` reach Actions; the rest go to
// NotAdvised with their blockers named, because a row filtered out silently
// is indistinguishable from a bug.

const (
	// The horizon every value is expressed over. Seven days is long enough
	// for a market order to fill and short enough that a rate and a one-off
	// are comparable without pretending to forecast a month.
	todayHorizonDays = 7.0

	// Twenty minutes, the session this screen is designed around.
	todayDefaultBudgetSeconds = 1200

	todayDefaultMaxActions = 40
)

// How long each kind of action takes in the client. These set the
// denominator of the ranking, so they decide what is worth doing in a short
// session — a 2M action that takes ten seconds beats a 6M action that takes
// two minutes. Rough by nature; kept together so they can be tuned in one
// place rather than rediscovered per call site.
const (
	todaySecondsReprice   = 20
	todaySecondsCancel    = 10
	todaySecondsBuy       = 45
	todaySecondsList      = 45
	todaySecondsDeliver   = 120
	todaySecondsPIRestart = 180
)

// Bounds on the measured daily return, used to price capital that is stuck
// or freed. The floor is not optimism: capital in a dead buy order earns
// exactly nothing, and pricing that at zero would rank cancelling a dead
// order as literally worthless. The ceiling stops one extraordinary month
// from inflating every cancel in the queue.
const (
	todayMinDailyReturn = 0.002
	todayMaxDailyReturn = 0.05
)

// The order desk models a fill rate but publishes no confidence band, so the
// downside view discounts it by how the rate was derived. A weekday profile
// built from ten weeks of history has earned more trust than a flat average
// over the trailing week; no flow estimate at all is not a discount, it is a
// missing input, and the row is graded `unproven` instead.
func todayFlowConfidence(flowBasis string) float64 {
	switch flowBasis {
	case "weekday":
		return 0.75
	case "flat":
		return 0.50
	default:
		return 0
	}
}

// Listing held stock is the surest action on the board — the stock exists
// and the price is live — but it is not instant, and the book can move
// between the click and the fill. The downside view keeps this much of the
// unrealized gain.
const todayListDownsideFactor = 0.80

// TodayActionKind is the verb. The UI badges it and the reconciler groups by
// it, so the set is closed.
type TodayActionKind string

const (
	TodayActionReprice   TodayActionKind = "reprice"
	TodayActionCancel    TodayActionKind = "cancel"
	TodayActionBuy       TodayActionKind = "buy"
	TodayActionList      TodayActionKind = "list"
	TodayActionDeliver   TodayActionKind = "deliver"
	TodayActionPIRestart TodayActionKind = "pi_restart"
)

// TodayDeepLink points at the tab that owns the underlying row, so no action
// is a dead end.
type TodayDeepLink struct {
	Tab       string `json:"tab"`
	TypeID    int32  `json:"type_id,omitempty"`
	StationID int64  `json:"station_id,omitempty"`
	OrderID   int64  `json:"order_id,omitempty"`
}

// TodayAction is one thing to do, with everything needed to do it and
// everything needed to judge whether to.
type TodayAction struct {
	ID      string          `json:"id"`
	Kind    TodayActionKind `json:"kind"`
	Urgency string          `json:"urgency"` // now | today | soon

	TypeID       int32  `json:"type_id,omitempty"`
	TypeName     string `json:"type_name,omitempty"`
	CategoryID   int32  `json:"category_id,omitempty"`
	LocationID   int64  `json:"location_id,omitempty"`
	LocationName string `json:"location_name,omitempty"`

	// Who does it, and whether they are already standing there.
	CharacterID   int64  `json:"character_id,omitempty"`
	CharacterName string `json:"character_name,omitempty"`
	Here          bool   `json:"here"`

	Headline string `json:"headline"`
	Why      string `json:"why"`

	// What to paste. PastePrice is already on EVE's 4-significant-digit
	// grid, so it is a price the client will accept rather than one the
	// user has to round.
	CurrentPrice float64 `json:"current_price,omitempty"`
	PastePrice   float64 `json:"paste_price,omitempty"`
	PriceStep    float64 `json:"price_step,omitempty"`
	Quantity     int64   `json:"quantity,omitempty"`
	CapitalISK   float64 `json:"capital_isk,omitempty"`

	// Reward and risk, always both.
	ExpectedISK7d float64 `json:"expected_isk_7d"`
	DownsideISK7d float64 `json:"downside_isk_7d"`
	// AtRiskISK is the capital that is exposed if this goes wrong — the
	// number that makes a downside legible. Zero for actions that spend
	// nothing.
	AtRiskISK float64 `json:"at_risk_isk"`

	Grade       TodayGrade       `json:"grade"`
	Reliability TodayReliability `json:"reliability"`

	EstSeconds int `json:"est_seconds"`
	// The sort key: risk-adjusted seven-day ISK per minute of attention.
	RiskAdjustedISKPerMinute float64 `json:"risk_adjusted_isk_per_minute"`

	// Budget walk, filled after sorting.
	CumulativeSeconds int  `json:"cumulative_seconds"`
	InBudget          bool `json:"in_budget"`

	Deadline   string `json:"deadline,omitempty"`
	TimingNote string `json:"timing_note,omitempty"`

	DeepLink TodayDeepLink `json:"deep_link"`

	Done    bool `json:"done"`
	Skipped bool `json:"skipped"`
}

// TodayCapitalInput is where the ISK currently sits, as measured.
type TodayCapitalInput struct {
	WalletISK        float64
	BuyOrderISK      float64
	InventoryCostISK float64
	SellOrderISK     float64
}

// TodayCapital is the same, with the verdict the page leads on.
type TodayCapital struct {
	WalletISK         float64 `json:"wallet_isk"`
	BuyOrderISK       float64 `json:"buy_order_isk"`
	InventoryISK      float64 `json:"inventory_isk"`
	SellOrderISK      float64 `json:"sell_order_isk"`
	TotalISK          float64 `json:"total_isk"`
	IdlePct           float64 `json:"idle_pct"`
	IdleCostISKPerDay float64 `json:"idle_cost_isk_per_day"`
	Verdict           string  `json:"verdict"`
}

// TodayDailyPnL is one day of realized profit out of the trade journal.
type TodayDailyPnL struct {
	Date        string // YYYY-MM-DD, UTC
	CombinedISK float64
}

// TodayPerformance is how the capital is actually doing. ReturnPctPerDay is
// the compounding rate — the one number worth maximizing, and the basis for
// pricing idle ISK.
type TodayPerformance struct {
	RealizedTodayISK float64 `json:"realized_today_isk"`
	Avg7dISKPerDay   float64 `json:"avg_7d_isk_per_day"`
	Avg30dISKPerDay  float64 `json:"avg_30d_isk_per_day"`
	ReturnPctPerDay  float64 `json:"return_pct_per_day"`
	// Measured says the rate came from real journal days rather than the
	// floor. The UI must not present a defaulted rate as an observation.
	Measured bool `json:"measured"`
}

// TodayOption is a place to put idle ISK, quoted so that options across
// trading, industry and PI compare directly on return per day.
type TodayOption struct {
	ID                string        `json:"id"`
	Kind              string        `json:"kind"` // station_flips | build | pi | cash
	Label             string        `json:"label"`
	Detail            string        `json:"detail"`
	CapitalISK        float64       `json:"capital_isk"`
	ExpectedISKPerDay float64       `json:"expected_isk_per_day"`
	DownsideISKPerDay float64       `json:"downside_isk_per_day"`
	ReturnPctPerDay   float64       `json:"return_pct_per_day"`
	SetupSeconds      int           `json:"setup_seconds"`
	Grade             TodayGrade    `json:"grade"`
	ActionCount       int           `json:"action_count"`
	DeepLink          TodayDeepLink `json:"deep_link"`
}

// TodayBatchKind is how a batch is consumed. The clipboard formatting lives
// in the frontend, next to the existing multibuy convention.
type TodayBatchKind string

const (
	TodayBatchMultibuy    TodayBatchKind = "multibuy"
	TodayBatchRepriceList TodayBatchKind = "reprice_list"
	TodayBatchWaypoint    TodayBatchKind = "waypoint"
)

type TodayBatchItem struct {
	TypeID   int32   `json:"type_id"`
	TypeName string  `json:"type_name"`
	Quantity int64   `json:"quantity"`
	Price    float64 `json:"price,omitempty"`
}

// TodayBatch is work that is faster in bulk than one action at a time.
type TodayBatch struct {
	ID                  string           `json:"id"`
	Kind                TodayBatchKind   `json:"kind"`
	Label               string           `json:"label"`
	Items               []TodayBatchItem `json:"items,omitempty"`
	DestinationID       int64            `json:"destination_id,omitempty"`
	DestinationSystemID int32            `json:"destination_system_id,omitempty"`
	DestinationName     string           `json:"destination_name,omitempty"`
	TotalCapitalISK     float64          `json:"total_capital_isk"`
	ExpectedISK7d       float64          `json:"expected_isk_7d"`
	DownsideISK7d       float64          `json:"downside_isk_7d"`
}

// TodayWaitingRow is a holding parked behind a target price.
//
// Not an action, on purpose. There is nothing to do about it today, and
// putting it in the queue as a zero-value row would be noise. It exists so
// that stock deliberately held back is visible rather than silently missing --
// "why is my Gnosis not in the list" has to have an answer on the page.
type TodayWaitingRow struct {
	TypeID   int32  `json:"type_id"`
	TypeName string `json:"type_name"`

	Qty         int64 `json:"qty"`
	ReservedQty int64 `json:"reserved_qty,omitempty"`

	TargetPrice       float64 `json:"target_price"`
	MarketPrice       float64 `json:"market_price"`
	TargetProgressPct float64 `json:"target_progress_pct"`
	TargetPercentile  float64 `json:"target_percentile,omitempty"`

	// UpsideISK is what waiting is worth if the target is reached: the gap
	// between the target and today's price across the tradeable units.
	UpsideISK float64 `json:"upside_isk"`

	DeepLink TodayDeepLink `json:"deep_link"`
}

// TodayTiming is the session-level answer to "is today a good day". Filled
// once the order desk publishes its day-of-week profile.
type TodayTiming struct {
	BestWeekday     int     `json:"best_weekday"` // 0 = Sunday, -1 = unknown
	BestMultiplier  float64 `json:"best_multiplier,omitempty"`
	TodayMultiplier float64 `json:"today_multiplier,omitempty"`
	Note            string  `json:"note,omitempty"`
}

// TodayBudget is the twenty-minute walk down the sorted list.
type TodayBudget struct {
	BudgetSeconds  int     `json:"budget_seconds"`
	PlannedSeconds int     `json:"planned_seconds"`
	InBudgetCount  int     `json:"in_budget_count"`
	TotalCount     int     `json:"total_count"`
	InBudgetISK7d  float64 `json:"in_budget_isk_7d"`
	BeyondISK7d    float64 `json:"beyond_isk_7d"`
}

// TodayPlan is the whole screen.
type TodayPlan struct {
	GeneratedAt string           `json:"generated_at"`
	Capital     TodayCapital     `json:"capital"`
	Performance TodayPerformance `json:"performance"`
	Actions     []TodayAction    `json:"actions"`
	// NotAdvised carries what was held back and why. Visible on purpose: a
	// risk model the user cannot audit is one they will stop believing.
	NotAdvised []TodayAction `json:"not_advised"`
	Options []TodayOption `json:"options"`
	// Waiting is stock parked behind a target price. A summary only -- the
	// rules are set on Assets -> Positions, which is where the holding lives.
	Waiting []TodayWaitingRow `json:"waiting"`
	Batches []TodayBatch      `json:"batches"`
	Timing     TodayTiming   `json:"timing"`
	Budget     TodayBudget   `json:"budget"`
	Warnings   []string      `json:"warnings,omitempty"`
}

// --- Inputs -----------------------------------------------------------
//
// Plain structs rather than the api-layer response types, so the engine
// stays free of the api package (which imports it) and the whole model can
// be exercised from a table test with no fixtures.

// TodayPosition is held stock, from the FIFO positions view, already
// stamped with its holding rule by the api layer.
type TodayPosition struct {
	TypeID        int32
	TypeName      string
	Qty           int64
	// TradeableQty is Qty less any reserved units -- the ships being flown.
	// Every sell-side figure reads this rather than Qty, which is the whole
	// point of the reserve: a position of six with two reserved is a position
	// of four as far as selling is concerned.
	TradeableQty int64
	ReservedQty  int64
	// TargetPrice is the price at or above which selling is wanted. Zero
	// means the holding trades normally. TargetMet is decided upstream
	// against the same hub price the row is quoted in, so the engine never
	// re-derives it from a possibly different price.
	TargetPrice       float64
	TargetMet         bool
	TargetProgressPct float64
	TargetPercentile  float64
	AvgUnitCost   float64
	CostBasis     float64
	MarketPrice   float64
	NetProceeds   float64
	UnrealizedISK float64
	UnrealizedPct float64
	ListedQty     int64
	DaysHeld      int
	StationID     int64
	StationName   string
}

// TodayPlanet is one planetary colony.
type TodayPlanet struct {
	CharacterID          int64
	CharacterName        string
	PlanetID             int64
	SolarSystemID        int32
	SolarSystemName      string
	ExpiredExtractorPins int
	IdleFactoryPins      int
	NetISKPerDay         float64
	NextExpiry           time.Time
	Status               string
}

// TodayIndustryJob is one finished (or finishing) manufacturing job.
type TodayIndustryJob struct {
	JobID            int64
	CharacterID      int64
	CharacterName    string
	ProductTypeID    int32
	ProductTypeName  string
	ProductQuantity  int64
	UnitValueISK     float64
	EndDate          time.Time
	Status           string
	OutputLocationID int64
	FacilityName     string
}

// TodayLocation is where one character is currently docked.
type TodayLocation struct {
	CharacterID   int64
	CharacterName string
	StationID     int64
	StationName   string
	SolarSystemID int32
}

// TodayInputs is everything BuildTodayPlan reads.
type TodayInputs struct {
	Now time.Time

	// Desk is the live order book view of orders already placed.
	Desk *OrderDeskResponse
	// Command is the ranked scan when one was run this refresh; ScanTrades
	// is the fallback from the last saved scan.
	Command    *StationCommandResult
	ScanTrades []StationTrade

	Positions              []TodayPosition
	PositionsPricingFailed bool
	Planets                []TodayPlanet
	IndustryJobs           []TodayIndustryJob

	ItemHistory map[int32]TodayItemHistory
	EdgeByType  map[int32]TodayEdge
	RiskByType  map[int32]TodayPositionRisk

	Capital  TodayCapitalInput
	DailyPnL []TodayDailyPnL

	Locations []TodayLocation

	MaxInvestmentISK float64
}

// TodayOpts tunes the walk. Zero values take the documented defaults.
type TodayOpts struct {
	BudgetSeconds int
	MaxActions    int
}

func (o TodayOpts) normalized() TodayOpts {
	if o.BudgetSeconds <= 0 {
		o.BudgetSeconds = todayDefaultBudgetSeconds
	}
	if o.MaxActions <= 0 {
		o.MaxActions = todayDefaultMaxActions
	}
	return o
}

// BuildTodayPlan assembles and ranks the session.
func BuildTodayPlan(in TodayInputs, opts TodayOpts) TodayPlan {
	opts = opts.normalized()
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	plan := TodayPlan{
		GeneratedAt: now.Format(time.RFC3339),
		Actions:     []TodayAction{},
		NotAdvised:  []TodayAction{},
		Options:     []TodayOption{},
		Waiting:     []TodayWaitingRow{},
		Batches:     []TodayBatch{},
		Timing:      TodayTiming{BestWeekday: -1},
	}

	// Performance needs the capital total to express a return rate, and the
	// capital verdict needs that rate to say what idle ISK costs — so the
	// total is computed first and both read it.
	total := math.Max(0, in.Capital.WalletISK) + math.Max(0, in.Capital.BuyOrderISK) +
		math.Max(0, in.Capital.InventoryCostISK) + math.Max(0, in.Capital.SellOrderISK)
	plan.Performance = todayPerformance(in, now, total)
	plan.Capital = todayCapital(in.Capital, plan.Performance)

	// The rate used to price capital that is stuck or freed.
	dailyReturn := todayDailyReturnRate(plan.Performance, plan.Capital.TotalISK)

	here := todayDockedStations(in.Locations)

	var all []TodayAction
	all = append(all, todayDeskActions(in, now, dailyReturn)...)
	all = append(all, todayBuyActions(in, now)...)
	listActions, waiting := todayListActions(in, now)
	all = append(all, listActions...)
	plan.Waiting = waiting
	all = append(all, todayDeliverActions(in, now)...)
	all = append(all, todayPIActions(in, now)...)

	for i := range all {
		a := &all[i]
		a.Grade = a.Reliability.Grade
		a.Here = a.LocationID != 0 && here[a.LocationID]
		if a.EstSeconds <= 0 {
			a.EstSeconds = 30
		}
		// Two discounts, and only one of them applies to everything. The
		// grade weight says how much the action is trusted at all; the
		// reality discount says how much this user's own results have
		// historically undershot the plan on this item, and that only
		// makes sense for actions whose value is a market prediction.
		// Collecting finished jobs or cancelling a dead order is not a
		// prediction, and haircutting it would be nonsense.
		weight := a.Grade.weight()
		if todayKindIsPredictive(a.Kind) {
			weight *= realityDiscount(todayEdgeFor(in, a.TypeID))
		}
		minutes := float64(a.EstSeconds) / 60.0
		if minutes > 0 {
			a.RiskAdjustedISKPerMinute = a.DownsideISK7d * weight / minutes
		}
	}

	advised := make([]TodayAction, 0, len(all))
	held := make([]TodayAction, 0)
	for _, a := range all {
		if a.Grade.Advised() {
			advised = append(advised, a)
		} else {
			held = append(held, a)
		}
	}

	sortTodayActions(advised)
	sortTodayActions(held)

	if len(advised) > opts.MaxActions {
		advised = advised[:opts.MaxActions]
	}

	// The budget walk. Actions you can do without undocking come first
	// among equals, so a session is not a series of dock-hops — that
	// reordering happens inside sortTodayActions, and this pass only
	// accumulates.
	spent := 0
	plan.Budget.BudgetSeconds = opts.BudgetSeconds
	for i := range advised {
		spent += advised[i].EstSeconds
		advised[i].CumulativeSeconds = spent
		advised[i].InBudget = spent <= opts.BudgetSeconds
		if advised[i].InBudget {
			plan.Budget.InBudgetCount++
			plan.Budget.InBudgetISK7d += advised[i].DownsideISK7d
			plan.Budget.PlannedSeconds = spent
		} else {
			plan.Budget.BeyondISK7d += advised[i].DownsideISK7d
		}
	}
	plan.Budget.TotalCount = len(advised)

	plan.Actions = advised
	plan.NotAdvised = held
	plan.Options = todayOptions(in, advised, plan.Capital, dailyReturn)
	plan.Batches = todayBatches(advised)
	plan.Warnings = todayWarnings(in)

	return plan
}

// sortTodayActions orders the queue. Urgency outranks value — an extractor
// that expires in two hours is worth doing before a better-paying reprice
// that will still be there tomorrow — and among equally urgent work, things
// you can do without undocking come first.
func sortTodayActions(actions []TodayAction) {
	rank := func(u string) int {
		switch u {
		case "now":
			return 0
		case "today":
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(actions, func(i, j int) bool {
		if r1, r2 := rank(actions[i].Urgency), rank(actions[j].Urgency); r1 != r2 {
			return r1 < r2
		}
		if actions[i].Here != actions[j].Here {
			return actions[i].Here
		}
		if actions[i].RiskAdjustedISKPerMinute != actions[j].RiskAdjustedISKPerMinute {
			return actions[i].RiskAdjustedISKPerMinute > actions[j].RiskAdjustedISKPerMinute
		}
		if actions[i].DownsideISK7d != actions[j].DownsideISK7d {
			return actions[i].DownsideISK7d > actions[j].DownsideISK7d
		}
		return actions[i].ID < actions[j].ID
	})
}

// --- Capital and performance -----------------------------------------

func todayPerformance(in TodayInputs, now time.Time, totalCapital float64) TodayPerformance {
	perf := TodayPerformance{}
	if len(in.DailyPnL) == 0 {
		return perf
	}

	// Sort a copy: the caller's slice order is not part of the contract.
	days := append([]TodayDailyPnL(nil), in.DailyPnL...)
	sort.Slice(days, func(i, j int) bool { return days[i].Date < days[j].Date })

	today := now.Format("2006-01-02")
	for _, d := range days {
		if d.Date == today {
			perf.RealizedTodayISK = d.CombinedISK
		}
	}

	mean := func(n int) (float64, int) {
		if len(days) == 0 {
			return 0, 0
		}
		from := len(days) - n
		if from < 0 {
			from = 0
		}
		window := days[from:]
		if len(window) == 0 {
			return 0, 0
		}
		var sum float64
		for _, d := range window {
			sum += d.CombinedISK
		}
		return sum / float64(len(window)), len(window)
	}

	perf.Avg7dISKPerDay, _ = mean(7)
	var n30 int
	perf.Avg30dISKPerDay, n30 = mean(30)
	perf.Measured = n30 > 0
	if totalCapital > 0 {
		perf.ReturnPctPerDay = perf.Avg30dISKPerDay / totalCapital * 100
	}
	return perf
}

func todayCapital(in TodayCapitalInput, perf TodayPerformance) TodayCapital {
	c := TodayCapital{
		WalletISK:    math.Max(0, in.WalletISK),
		BuyOrderISK:  math.Max(0, in.BuyOrderISK),
		InventoryISK: math.Max(0, in.InventoryCostISK),
		SellOrderISK: math.Max(0, in.SellOrderISK),
	}
	c.TotalISK = c.WalletISK + c.BuyOrderISK + c.InventoryISK + c.SellOrderISK
	if c.TotalISK <= 0 {
		return c
	}
	c.IdlePct = c.WalletISK / c.TotalISK * 100

	if perf.ReturnPctPerDay > 0 {
		c.IdleCostISKPerDay = c.WalletISK * perf.ReturnPctPerDay / 100
	}
	switch {
	case c.IdlePct >= 25 && c.IdleCostISKPerDay > 0:
		c.Verdict = fmt.Sprintf("%s idle (%.0f%%) — at your recent rate that is about %s a day you are not making",
			todayISK(c.WalletISK), c.IdlePct, todayISK(c.IdleCostISKPerDay))
	case c.IdlePct >= 25:
		c.Verdict = fmt.Sprintf("%s idle (%.0f%%) and earning nothing",
			todayISK(c.WalletISK), c.IdlePct)
	case c.IdlePct < 5:
		c.Verdict = "almost everything is deployed — nothing spare to put to work"
	default:
		c.Verdict = fmt.Sprintf("%s idle (%.0f%%) — most of the book is working",
			todayISK(c.WalletISK), c.IdlePct)
	}
	return c
}

// todayDailyReturnRate is the fraction of deployed capital earned per day.
// It prices two things: what idle ISK costs you, and what freeing capital
// out of a dead order is worth.
func todayDailyReturnRate(perf TodayPerformance, totalCapital float64) float64 {
	if totalCapital <= 0 || perf.Avg30dISKPerDay <= 0 {
		return todayMinDailyReturn
	}
	return clampRange(perf.Avg30dISKPerDay/totalCapital, todayMinDailyReturn, todayMaxDailyReturn)
}

func todayDockedStations(locs []TodayLocation) map[int64]bool {
	out := make(map[int64]bool, len(locs))
	for _, l := range locs {
		if l.StationID > 0 {
			out[l.StationID] = true
		}
	}
	return out
}

// --- Order desk: reprice and cancel ------------------------------------

// todayDeskActions turns the order desk into repricing and cancelling work.
//
// The economics of a reprice are not the desk's NetRelistGainISK, which is
// always negative — moving toward the top of the book concedes price on
// every remaining unit and then charges a broker fee on the change. That
// figure is the *cost*. The benefit is that the order fills at all: an order
// buried behind a queue does not clear inside the horizon, and a repriced
// one does. So the value is the margin on the units repricing actually
// unlocks, less what repricing costs.
func todayDeskActions(in TodayInputs, now time.Time, dailyReturn float64) []TodayAction {
	if in.Desk == nil {
		return nil
	}
	out := make([]TodayAction, 0, len(in.Desk.Orders))

	for _, o := range in.Desk.Orders {
		switch o.Recommendation {
		case "reprice":
			if a, ok := todayRepriceAction(in, o, now); ok {
				out = append(out, a)
			}
		case "cancel":
			if a, ok := todayCancelAction(in, o, now, dailyReturn); ok {
				out = append(out, a)
			}
		}
	}
	return out
}

func todayRepriceAction(in TodayInputs, o OrderDeskOrder, now time.Time) (TodayAction, bool) {
	if o.SuggestedPrice <= 0 || o.VolumeRemain <= 0 {
		return TodayAction{}, false
	}

	// What repricing costs: the price concession across the remaining
	// units plus the broker fee. NetRelistGainISK already carries both,
	// signed negative, so the cost is its magnitude.
	cost := 0.0
	if o.NetRelistGainISK < 0 {
		cost = -o.NetRelistGainISK
	}

	// What it unlocks. Without the reprice the queue ahead has to clear
	// first; with it, the order is at the front.
	remain := float64(o.VolumeRemain)
	flow := o.EstimatedFillPerDay
	unitsAfter := clampRange(flow*todayHorizonDays, 0, remain)
	unitsNow := clampRange(flow*todayHorizonDays-float64(o.QueueAheadQty), 0, remain)
	extraUnits := unitsAfter - unitsNow

	expected := extraUnits*o.MarginUnitISK - cost
	confidence := todayFlowConfidence(o.FlowBasis)
	downside := extraUnits*confidence*o.MarginUnitISK - cost

	a := TodayAction{
		ID:            todayActionID(TodayActionReprice, o.TypeID, o.LocationID, o.OrderID),
		Kind:          TodayActionReprice,
		TypeID:        o.TypeID,
		TypeName:      o.TypeName,
		LocationID:    o.LocationID,
		LocationName:  o.LocationName,
		CharacterID:   o.CharacterID,
		CharacterName: o.CharacterName,
		Headline:      fmt.Sprintf("Reprice to %s", todayPrice(o.SuggestedPrice)),
		Why:           todayRepriceWhy(o),
		CurrentPrice:  o.Price,
		PastePrice:    o.SuggestedPrice,
		PriceStep:     priceStep(math.Max(o.SuggestedPrice, 1)),
		Quantity:      int64(o.VolumeRemain),
		ExpectedISK7d: expected,
		DownsideISK7d: downside,
		AtRiskISK:     cost,
		EstSeconds:    todaySecondsReprice,
		Urgency:       todayUrgencyFromDays(float64(o.DaysToExpire)),
		DeepLink:      TodayDeepLink{Tab: "orders", TypeID: o.TypeID, StationID: o.LocationID, OrderID: o.OrderID},
	}
	if o.ExpiresAt != "" {
		a.Deadline = o.ExpiresAt
	}

	ev := todayEvidence{
		kind:            TodayActionReprice,
		history:         todayHistoryFor(in, o.TypeID),
		edge:            todayEdgeFor(in, o.TypeID),
		risk:            todayRiskFor(in, o.TypeID),
		expected:        expected,
		downside:        downside,
		unknownMargin:   o.MarginBasis == "none" || o.MarginBasis == "",
		noBook:          !o.BookAvailable,
		noFlow:          o.FlowBasis == "none" || o.FlowBasis == "",
		thinMargin:      o.WarnThinMargin,
		unprofitableFee: expected <= 0,
	}
	a.Reliability = gradeTodayAction(ev)
	a.Reliability.Caps = nil // a reprice has no size to cap; the order sets it
	return a, true
}

func todayRepriceWhy(o OrderDeskOrder) string {
	parts := make([]string, 0, 3)
	if o.Position > 1 && o.TotalOrders > 0 {
		parts = append(parts, fmt.Sprintf("outbid, %s of %d", todayOrdinal(o.Position), o.TotalOrders))
	}
	if o.DaysToClearQueue > 0.5 {
		parts = append(parts, fmt.Sprintf("%.1fd of queue ahead of you", o.DaysToClearQueue))
	}
	if o.ETADays > 0 {
		if o.ETACapped {
			parts = append(parts, "will not fill on current pricing")
		} else {
			parts = append(parts, fmt.Sprintf("fills in %.1fd as listed", o.ETADays))
		}
	}
	if len(parts) == 0 {
		return strings.TrimSpace(o.Reason)
	}
	return strings.Join(parts, " · ")
}

func todayCancelAction(in TodayInputs, o OrderDeskOrder, now time.Time, dailyReturn float64) (TodayAction, bool) {
	// Only a buy order returns ISK to the wallet. Cancelling a sell order
	// puts stock back in the hangar, which frees no capital — it is
	// housekeeping, and pricing it as though it released ISK would float
	// it up the queue on money that never arrives.
	freed := 0.0
	if o.IsBuyOrder {
		freed = o.Notional
	}
	value := freed * dailyReturn * todayHorizonDays

	a := TodayAction{
		ID:            todayActionID(TodayActionCancel, o.TypeID, o.LocationID, o.OrderID),
		Kind:          TodayActionCancel,
		TypeID:        o.TypeID,
		TypeName:      o.TypeName,
		LocationID:    o.LocationID,
		LocationName:  o.LocationName,
		CharacterID:   o.CharacterID,
		CharacterName: o.CharacterName,
		Headline:      "Cancel this order",
		Why:           strings.TrimSpace(o.Reason),
		CurrentPrice:  o.Price,
		Quantity:      int64(o.VolumeRemain),
		ExpectedISK7d: value,
		DownsideISK7d: value,
		EstSeconds:    todaySecondsCancel,
		Urgency:       todayUrgencyFromDays(float64(o.DaysToExpire)),
		DeepLink:      TodayDeepLink{Tab: "orders", TypeID: o.TypeID, StationID: o.LocationID, OrderID: o.OrderID},
	}
	if o.ExpiresAt != "" {
		a.Deadline = o.ExpiresAt
	}

	reason := "the ISK comes straight back to your wallet"
	if !o.IsBuyOrder {
		reason = "the stock returns to your hangar; no ISK is at stake either way"
	}
	a.Reliability = gradeTodayAction(todayEvidence{
		kind:          TodayActionCancel,
		certain:       true,
		certainReason: reason,
		expected:      value,
		downside:      value,
	})
	return a, true
}

// --- New buy orders ----------------------------------------------------

// todayBuyActions turns scan candidates into sized, priced buy orders.
//
// The quantity is not the user's problem: it is the smallest of a day of
// flow, a quarter of daily volume, the configured investment cap, free
// wallet ISK, and whatever the user's own history and portfolio risk say
// this item is worth. Whichever bound applied is named on the row.
func todayBuyActions(in TodayInputs, now time.Time) []TodayAction {
	trades, commandByKey := todayBuyCandidates(in)
	if len(trades) == 0 {
		return nil
	}

	out := make([]TodayAction, 0, len(trades))
	for _, t := range trades {
		// Bid one step above the current best buy — the patient-buy price,
		// on EVE's legal grid.
		bid := NextBuyOverbid(t.BuyPrice)
		if bid <= 0 {
			continue
		}

		lim := todayQuantityLimits{
			FlowPerDay:       math.Min(nonZeroOr(t.S2BPerDay, t.BuyUnitsPerDay), nonZeroOr(t.BfSPerDay, t.SellUnitsPerDay)),
			AvgDailyVolume:   float64(t.DailyVolume),
			UnitPrice:        bid,
			FreeWalletISK:    in.Capital.WalletISK,
			MaxInvestmentISK: in.MaxInvestmentISK,
			Edge:             todayEdgeFor(in, t.TypeID),
			Risk:             todayRiskFor(in, t.TypeID),
		}
		sized := capTodayQuantity(lim)
		if sized.Quantity <= 0 {
			continue
		}

		// Scale the scan's per-cycle profit to the size actually advised.
		// The scan quotes DailyProfit against CapitalRequired; taking it
		// whole while buying a fraction of that would overstate the row by
		// exactly the amount we just capped it by.
		scale := 1.0
		if t.CapitalRequired > 0 {
			scale = clampRange(sized.Capital/t.CapitalRequired, 0, 1)
		}
		expectedPerDay := t.DailyProfit * scale
		downsidePerDay := todayBuyDownsidePerDay(t, commandByKey) * scale

		expected := expectedPerDay * todayHorizonDays
		downside := downsidePerDay * todayHorizonDays

		a := TodayAction{
			ID:            todayActionID(TodayActionBuy, t.TypeID, t.StationID, 0),
			Kind:          TodayActionBuy,
			TypeID:        t.TypeID,
			TypeName:      t.TypeName,
			CategoryID:    t.CategoryID,
			LocationID:    t.StationID,
			LocationName:  t.StationName,
			Headline:      fmt.Sprintf("Buy order at %s", todayPrice(bid)),
			Why:           todayBuyWhy(t, sized),
			CurrentPrice:  t.BuyPrice,
			PastePrice:    bid,
			PriceStep:     priceStep(math.Max(bid, 1)),
			Quantity:      sized.Quantity,
			CapitalISK:    sized.Capital,
			ExpectedISK7d: expected,
			DownsideISK7d: downside,
			AtRiskISK:     sized.Capital,
			EstSeconds:    todaySecondsBuy,
			Urgency:       "soon",
			DeepLink:      TodayDeepLink{Tab: "station", TypeID: t.TypeID, StationID: t.StationID},
		}

		ev := todayEvidence{
			kind:      TodayActionBuy,
			history:   todayHistoryFor(in, t.TypeID),
			edge:      todayEdgeFor(in, t.TypeID),
			risk:      todayRiskFor(in, t.TypeID),
			expected:  expected,
			downside:  downside,
			noHistory: !t.HistoryAvailable,
			noFlow:    t.DailyVolume <= 0 && t.S2BPerDay <= 0,
		}
		a.Reliability = gradeTodayAction(ev)
		a.Reliability.Caps = sized.Caps

		// A scan flag is evidence in its own right, and it must survive
		// a good score rather than be averaged away by one.
		if t.IsHighRiskFlag || t.IsExtremePriceFlag {
			a.Reliability = todayDowngrade(a.Reliability, todayScanRiskReason(t))
		}
		out = append(out, a)
	}
	return out
}

// todayBuyCandidates prefers the ranked command rows when a scan ran this
// refresh, and falls back to the last saved scan otherwise. Command rows
// also carry a forecast band, which is what the downside view wants.
func todayBuyCandidates(in TodayInputs) ([]StationTrade, map[string]StationCommandRow) {
	if in.Command != nil && len(in.Command.Rows) > 0 {
		byKey := make(map[string]StationCommandRow, len(in.Command.Rows))
		trades := make([]StationTrade, 0, len(in.Command.Rows))
		for _, r := range in.Command.Rows {
			if r.RecommendedAction != StationActionNewEntry {
				continue
			}
			byKey[todayTradeKey(r.Trade.TypeID, r.Trade.StationID)] = r
			trades = append(trades, r.Trade)
		}
		return trades, byKey
	}

	trades := make([]StationTrade, 0, len(in.ScanTrades))
	for _, t := range in.ScanTrades {
		if t.DailyProfit <= 0 {
			continue
		}
		trades = append(trades, t)
	}
	sort.SliceStable(trades, func(i, j int) bool { return trades[i].DailyProfit > trades[j].DailyProfit })
	return trades, nil
}

// todayBuyDownsidePerDay is the realistic-bad daily profit. Every source is
// one the codebase already computes conservatively — nothing here invents a
// haircut.
func todayBuyDownsidePerDay(t StationTrade, byKey map[string]StationCommandRow) float64 {
	if byKey != nil {
		if row, ok := byKey[todayTradeKey(t.TypeID, t.StationID)]; ok {
			if p80 := row.Forecast.DailyProfit.P80; p80 != 0 {
				return p80
			}
		}
	}
	// RealizableDailyProfit is the scanner's own conservative figure —
	// what it believes is executable rather than what the spread implies.
	if t.RealizableDailyProfit > 0 {
		return t.RealizableDailyProfit
	}
	return t.DailyProfit
}

func todayBuyWhy(t StationTrade, sized todayQuantityCap) string {
	parts := make([]string, 0, 3)
	if t.MarginPercent > 0 {
		parts = append(parts, fmt.Sprintf("%.1f%% margin", t.MarginPercent))
	}
	if t.S2BPerDay > 0 {
		parts = append(parts, fmt.Sprintf("%.0f/day sell into buy orders", t.S2BPerDay))
	}
	if len(sized.Caps) > 0 {
		parts = append(parts, "size capped by "+strings.Join(sized.Caps, " and "))
	}
	return strings.Join(parts, " · ")
}

func todayScanRiskReason(t StationTrade) string {
	if t.IsExtremePriceFlag {
		return "the scanner flagged this price as anomalous"
	}
	return fmt.Sprintf("high scam-detection score (%d)", t.SDS)
}

// --- Listing held stock ------------------------------------------------

func todayListActions(in TodayInputs, now time.Time) ([]TodayAction, []TodayWaitingRow) {
	out := make([]TodayAction, 0, len(in.Positions))
	waiting := make([]TodayWaitingRow, 0)

	for _, p := range in.Positions {
		// Reserved units are not stock. Fall back to Qty only when the api
		// layer did not stamp a tradeable figure, so an older cached plan
		// does not read as "nothing is sellable".
		tradeable := p.TradeableQty
		if tradeable == 0 && p.ReservedQty == 0 {
			tradeable = p.Qty
		}
		if tradeable <= 0 {
			continue
		}

		// A target that has not been reached is the whole reason this
		// holding is not in the queue. Say so somewhere rather than just
		// dropping it.
		if p.TargetPrice > 0 && !p.TargetMet {
			waiting = append(waiting, TodayWaitingRow{
				TypeID:            p.TypeID,
				TypeName:          p.TypeName,
				Qty:               tradeable,
				ReservedQty:       p.ReservedQty,
				TargetPrice:       p.TargetPrice,
				MarketPrice:       p.MarketPrice,
				TargetProgressPct: p.TargetProgressPct,
				TargetPercentile:  p.TargetPercentile,
				UpsideISK:         math.Max(0, p.TargetPrice-p.MarketPrice) * float64(tradeable),
				DeepLink:          TodayDeepLink{Tab: "positions", TypeID: p.TypeID},
			})
			continue
		}

		unlisted := tradeable - p.ListedQty
		if unlisted <= 0 {
			continue
		}
		share := float64(unlisted) / float64(tradeable)

		// Undercut the hub's best ask by one legal step, so the paste is a
		// price that takes top of book rather than one that joins a queue.
		ask := NextSellUndercut(p.MarketPrice)

		expected := p.UnrealizedISK * share
		downside := expected * todayListDownsideFactor

		// A holding whose target has just been reached is the one thing on
		// this page that is genuinely time-sensitive on the sell side: it is
		// the moment you have been waiting for, and the price that produced
		// it can go away.
		headline := fmt.Sprintf("List %s at %s", todayQty(unlisted), todayPrice(ask))
		urgency := "today"
		if p.TargetPrice > 0 && p.TargetMet {
			headline = fmt.Sprintf("Target hit — list %s at %s", todayQty(unlisted), todayPrice(ask))
			urgency = "now"
		}

		a := TodayAction{
			ID:            todayActionID(TodayActionList, p.TypeID, p.StationID, 0),
			Kind:          TodayActionList,
			TypeID:        p.TypeID,
			TypeName:      p.TypeName,
			LocationID:    p.StationID,
			LocationName:  p.StationName,
			Headline:      headline,
			Why:           todayListWhy(p, unlisted),
			CurrentPrice:  p.MarketPrice,
			PastePrice:    ask,
			PriceStep:     priceStep(math.Max(ask, 1)),
			Quantity:      unlisted,
			ExpectedISK7d: expected,
			DownsideISK7d: downside,
			EstSeconds:    todaySecondsList,
			Urgency:       urgency,
			DeepLink:      TodayDeepLink{Tab: "positions", TypeID: p.TypeID, StationID: p.StationID},
		}

		ev := todayEvidence{
			kind:          TodayActionList,
			history:       todayHistoryFor(in, p.TypeID),
			edge:          todayEdgeFor(in, p.TypeID),
			risk:          todayRiskFor(in, p.TypeID),
			expected:      expected,
			downside:      downside,
			pricingFailed: in.PositionsPricingFailed || p.MarketPrice <= 0,
			// A position with no cost basis cannot be said to be in profit.
			unknownMargin: p.CostBasis <= 0,
		}
		a.Reliability = gradeTodayAction(ev)
		out = append(out, a)
	}
	return out, waiting
}

func todayListWhy(p TodayPosition, unlisted int64) string {
	parts := make([]string, 0, 3)
	if p.UnrealizedPct != 0 {
		parts = append(parts, fmt.Sprintf("%+.1f%% on cost, net of fees", p.UnrealizedPct))
	}
	if p.DaysHeld > 0 {
		parts = append(parts, fmt.Sprintf("held %dd", p.DaysHeld))
	}
	if p.ListedQty > 0 {
		parts = append(parts, fmt.Sprintf("%s of %s still unlisted", todayQty(unlisted), todayQty(p.Qty)))
	}
	if p.ReservedQty > 0 {
		parts = append(parts, fmt.Sprintf("%s reserved, not for sale", todayQty(p.ReservedQty)))
	}
	if p.TargetPrice > 0 && p.TargetMet {
		parts = append(parts, fmt.Sprintf("target of %s reached", todayPrice(p.TargetPrice)))
	}
	return strings.Join(parts, " · ")
}

// --- Industry deliveries ------------------------------------------------

// todayDeliverActions groups finished jobs by where their output landed:
// one trip collects all of them, so one action should too.
func todayDeliverActions(in TodayInputs, now time.Time) []TodayAction {
	type bucket struct {
		locationID int64
		name       string
		jobs       int
		value      float64
		charID     int64
		charName   string
		oldest     time.Time
	}
	buckets := map[int64]*bucket{}
	order := []int64{}

	for _, j := range in.IndustryJobs {
		if j.EndDate.IsZero() || j.EndDate.After(now) {
			continue
		}
		if strings.EqualFold(j.Status, "delivered") || strings.EqualFold(j.Status, "cancelled") {
			continue
		}
		b, ok := buckets[j.OutputLocationID]
		if !ok {
			b = &bucket{
				locationID: j.OutputLocationID,
				name:       j.FacilityName,
				charID:     j.CharacterID,
				charName:   j.CharacterName,
				oldest:     j.EndDate,
			}
			buckets[j.OutputLocationID] = b
			order = append(order, j.OutputLocationID)
		}
		b.jobs++
		b.value += float64(j.ProductQuantity) * j.UnitValueISK
		if j.EndDate.Before(b.oldest) {
			b.oldest = j.EndDate
		}
	}

	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })

	out := make([]TodayAction, 0, len(order))
	for _, id := range order {
		b := buckets[id]
		name := b.name
		if name == "" {
			name = fmt.Sprintf("facility %d", b.locationID)
		}
		a := TodayAction{
			ID:            todayActionID(TodayActionDeliver, 0, b.locationID, 0),
			Kind:          TodayActionDeliver,
			LocationID:    b.locationID,
			LocationName:  name,
			CharacterID:   b.charID,
			CharacterName: b.charName,
			Headline:      fmt.Sprintf("Deliver %d finished %s", b.jobs, todayPlural(b.jobs, "job", "jobs")),
			Why: fmt.Sprintf("%s of product sitting in %s, oldest finished %s",
				todayISK(b.value), name, todayAgo(now, b.oldest)),
			ExpectedISK7d: b.value,
			DownsideISK7d: b.value,
			EstSeconds:    todaySecondsDeliver,
			Urgency:       "today",
			DeepLink:      TodayDeepLink{Tab: "jobs", StationID: b.locationID},
		}
		a.Reliability = gradeTodayAction(todayEvidence{
			kind:          TodayActionDeliver,
			certain:       true,
			certainReason: "the product already exists; this is collecting it, not predicting it",
			expected:      b.value,
			downside:      b.value,
		})
		out = append(out, a)
	}
	return out
}

// --- Planetary industry -------------------------------------------------

// todayPIActions surfaces colonies that have stopped, or are about to. A
// stalled extractor is not a forecast — it is ISK per day you are already
// not earning, which is why these are graded certain and sorted on urgency.
func todayPIActions(in TodayInputs, now time.Time) []TodayAction {
	out := make([]TodayAction, 0, len(in.Planets))
	for _, p := range in.Planets {
		expiringSoon := !p.NextExpiry.IsZero() && p.NextExpiry.Sub(now) < 24*time.Hour
		if p.ExpiredExtractorPins == 0 && p.IdleFactoryPins == 0 && !expiringSoon {
			continue
		}

		// Value is what the colony makes per day, for as long as it stays
		// stopped — capped at the horizon like everything else.
		value := math.Max(0, p.NetISKPerDay) * todayHorizonDays

		urgency := "soon"
		switch {
		case p.ExpiredExtractorPins > 0:
			urgency = "now"
		case expiringSoon:
			urgency = "today"
		}

		a := TodayAction{
			ID:            todayActionID(TodayActionPIRestart, 0, p.PlanetID, 0),
			Kind:          TodayActionPIRestart,
			LocationName:  p.SolarSystemName,
			CharacterID:   p.CharacterID,
			CharacterName: p.CharacterName,
			Headline:      todayPIHeadline(p),
			Why:           todayPIWhy(p, now),
			ExpectedISK7d: value,
			DownsideISK7d: value,
			EstSeconds:    todaySecondsPIRestart,
			Urgency:       urgency,
			DeepLink:      TodayDeepLink{Tab: "pi_planets"},
		}
		if !p.NextExpiry.IsZero() {
			a.Deadline = p.NextExpiry.UTC().Format(time.RFC3339)
		}
		a.Reliability = gradeTodayAction(todayEvidence{
			kind:          TodayActionPIRestart,
			certain:       true,
			certainReason: "a stopped colony is a loss you are already taking, not a bet",
			expected:      value,
			downside:      value,
		})
		out = append(out, a)
	}
	return out
}

func todayPIHeadline(p TodayPlanet) string {
	switch {
	case p.ExpiredExtractorPins > 0:
		return fmt.Sprintf("Restart %d %s in %s", p.ExpiredExtractorPins,
			todayPlural(p.ExpiredExtractorPins, "extractor", "extractors"), p.SolarSystemName)
	case p.IdleFactoryPins > 0:
		return fmt.Sprintf("Feed %d idle %s in %s", p.IdleFactoryPins,
			todayPlural(p.IdleFactoryPins, "factory", "factories"), p.SolarSystemName)
	default:
		return fmt.Sprintf("Reset the colony in %s", p.SolarSystemName)
	}
}

func todayPIWhy(p TodayPlanet, now time.Time) string {
	parts := make([]string, 0, 3)
	if p.NetISKPerDay > 0 {
		parts = append(parts, fmt.Sprintf("%s/day at stake", todayISK(p.NetISKPerDay)))
	}
	if p.ExpiredExtractorPins > 0 {
		parts = append(parts, "extractor cycle already expired")
	} else if !p.NextExpiry.IsZero() {
		parts = append(parts, "expires "+todayIn(now, p.NextExpiry))
	}
	if p.IdleFactoryPins > 0 {
		parts = append(parts, fmt.Sprintf("%d %s idle", p.IdleFactoryPins,
			todayPlural(p.IdleFactoryPins, "factory", "factories")))
	}
	return strings.Join(parts, " · ")
}

// --- Options and batches ------------------------------------------------

// todayOptions answers "where should the idle ISK go", with every choice on
// the same return-per-day scale so trading, building and PI compare
// directly. Each option carries the weakest grade among the actions behind
// it, so a high-percentage option built on unproven rows cannot outrank a
// modest one built on proven ones.
func todayOptions(in TodayInputs, actions []TodayAction, capital TodayCapital, dailyReturn float64) []TodayOption {
	out := make([]TodayOption, 0, 4)

	var flipCapital, flipExpected, flipDownside float64
	var flipCount, flipSeconds int
	flipGrade := TodayGradeProven
	for _, a := range actions {
		if a.Kind != TodayActionBuy {
			continue
		}
		flipCapital += a.CapitalISK
		flipExpected += a.ExpectedISK7d / todayHorizonDays
		flipDownside += a.DownsideISK7d / todayHorizonDays
		flipSeconds += a.EstSeconds
		flipCount++
		if a.Grade == TodayGradeLikely {
			flipGrade = TodayGradeLikely
		}
	}
	if flipCount > 0 && flipCapital > 0 {
		out = append(out, TodayOption{
			ID:                "station_flips",
			Kind:              "station_flips",
			Label:             fmt.Sprintf("Station flips — %d %s", flipCount, todayPlural(flipCount, "item", "items")),
			Detail:            "buy orders at your hub, from the queue above",
			CapitalISK:        flipCapital,
			ExpectedISKPerDay: flipExpected,
			DownsideISKPerDay: flipDownside,
			ReturnPctPerDay:   flipDownside / flipCapital * 100,
			SetupSeconds:      flipSeconds,
			Grade:             flipGrade,
			ActionCount:       flipCount,
			DeepLink:          TodayDeepLink{Tab: "station"},
		})
	}

	var piPerDay float64
	var piCount int
	for _, p := range in.Planets {
		if p.NetISKPerDay > 0 {
			piPerDay += p.NetISKPerDay
			piCount++
		}
	}
	if piCount > 0 {
		out = append(out, TodayOption{
			ID:                "pi",
			Kind:              "pi",
			Label:             fmt.Sprintf("Planetary industry — %d %s", piCount, todayPlural(piCount, "colony", "colonies")),
			Detail:            "already running; capital is the colonies themselves",
			ExpectedISKPerDay: piPerDay,
			DownsideISKPerDay: piPerDay,
			SetupSeconds:      0,
			Grade:             TodayGradeProven,
			ActionCount:       piCount,
			DeepLink:          TodayDeepLink{Tab: "pi_planets"},
		})
	}

	// Cash is always an option, and stating its return as zero is the
	// point — it is the baseline every other card is measured against.
	if capital.WalletISK > 0 {
		out = append(out, TodayOption{
			ID:              "cash",
			Kind:            "cash",
			Label:           "Leave it in the wallet",
			Detail:          "earns nothing, risks nothing",
			CapitalISK:      capital.WalletISK,
			ReturnPctPerDay: 0,
			Grade:           TodayGradeProven,
			DeepLink:        TodayDeepLink{Tab: "wallet"},
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ReturnPctPerDay > out[j].ReturnPctPerDay
	})
	return out
}

// todayBatches collects the work that is faster done in bulk. Only advised
// actions are included — a batch is a one-paste commitment, and slipping an
// unproven row into one would bypass the grading entirely.
func todayBatches(actions []TodayAction) []TodayBatch {
	out := make([]TodayBatch, 0, 2)

	buys := TodayBatch{ID: "multibuy_buys", Kind: TodayBatchMultibuy, Label: "Every buy in the queue"}
	reprices := TodayBatch{ID: "reprice_list", Kind: TodayBatchRepriceList, Label: "Every reprice in the queue"}

	for _, a := range actions {
		switch a.Kind {
		case TodayActionBuy:
			buys.Items = append(buys.Items, TodayBatchItem{
				TypeID: a.TypeID, TypeName: a.TypeName, Quantity: a.Quantity, Price: a.PastePrice,
			})
			buys.TotalCapitalISK += a.CapitalISK
			buys.ExpectedISK7d += a.ExpectedISK7d
			buys.DownsideISK7d += a.DownsideISK7d
		case TodayActionReprice:
			reprices.Items = append(reprices.Items, TodayBatchItem{
				TypeID: a.TypeID, TypeName: a.TypeName, Quantity: a.Quantity, Price: a.PastePrice,
			})
			reprices.ExpectedISK7d += a.ExpectedISK7d
			reprices.DownsideISK7d += a.DownsideISK7d
		}
	}

	if len(buys.Items) > 0 {
		out = append(out, buys)
	}
	if len(reprices.Items) > 0 {
		out = append(out, reprices)
	}
	return out
}

func todayWarnings(in TodayInputs) []string {
	var out []string
	if in.Desk == nil {
		out = append(out, "Your open orders could not be read, so repricing and cancelling are missing from this plan.")
	}
	if in.PositionsPricingFailed {
		out = append(out, "Hub prices for your held stock could not be read, so listing suggestions are held back.")
	}
	if in.Command == nil && len(in.ScanTrades) == 0 {
		out = append(out, "No station scan to draw buy candidates from. Run one on the Trade workspace.")
	}
	return out
}

// --- Small helpers ------------------------------------------------------

func todayActionID(kind TodayActionKind, typeID int32, locationID, orderID int64) string {
	return fmt.Sprintf("%s:%d:%d:%d", kind, typeID, locationID, orderID)
}

func todayTradeKey(typeID int32, stationID int64) string {
	return fmt.Sprintf("%d-%d", typeID, stationID)
}

func todayHistoryFor(in TodayInputs, typeID int32) *TodayItemHistory {
	if in.ItemHistory == nil {
		return nil
	}
	if h, ok := in.ItemHistory[typeID]; ok {
		return &h
	}
	return nil
}

func todayEdgeFor(in TodayInputs, typeID int32) *TodayEdge {
	if in.EdgeByType == nil {
		return nil
	}
	if e, ok := in.EdgeByType[typeID]; ok {
		return &e
	}
	return nil
}

func todayRiskFor(in TodayInputs, typeID int32) *TodayPositionRisk {
	if in.RiskByType == nil {
		return nil
	}
	if r, ok := in.RiskByType[typeID]; ok {
		return &r
	}
	return nil
}

// todayDowngrade knocks a grade down one step and records why. Used for
// evidence that should survive a good score rather than be averaged into
// one — a scam-detection flag is not the sort of thing a high win rate
// elsewhere should cancel out.
func todayDowngrade(rel TodayReliability, reason string) TodayReliability {
	switch rel.Grade {
	case TodayGradeProven:
		rel.Grade = TodayGradeLikely
	case TodayGradeLikely:
		rel.Grade = TodayGradeUnproven
	}
	if !rel.Grade.Advised() {
		rel.Blockers = append(rel.Blockers, reason)
	}
	rel.Evidence = reason + "; " + rel.Evidence
	return rel
}

func todayUrgencyFromDays(days float64) string {
	switch {
	case days >= 0 && days <= 1:
		return "now"
	case days >= 0 && days <= 3:
		return "today"
	default:
		return "soon"
	}
}

func nonZeroOr(primary, fallback float64) float64 {
	if primary > 0 {
		return primary
	}
	return fallback
}

func todayISK(v float64) string {
	abs := math.Abs(v)
	switch {
	case abs >= 1e12:
		return fmt.Sprintf("%.2fT", v/1e12)
	case abs >= 1e9:
		return fmt.Sprintf("%.2fB", v/1e9)
	case abs >= 1e6:
		return fmt.Sprintf("%.1fM", v/1e6)
	case abs >= 1e3:
		return fmt.Sprintf("%.0fk", v/1e3)
	default:
		return fmt.Sprintf("%.0f", v)
	}
}

func todayPrice(v float64) string {
	return fmt.Sprintf("%.2f", v)
}

func todayQty(v int64) string {
	return fmt.Sprintf("%d", v)
}

func todayPlural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func todayOrdinal(n int) string {
	suffix := "th"
	switch {
	case n%100 >= 11 && n%100 <= 13:
	case n%10 == 1:
		suffix = "st"
	case n%10 == 2:
		suffix = "nd"
	case n%10 == 3:
		suffix = "rd"
	}
	return fmt.Sprintf("%d%s", n, suffix)
}

func todayAgo(now, then time.Time) string {
	d := now.Sub(then)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func todayIn(now, then time.Time) string {
	d := then.Sub(now)
	if d < 0 {
		return "already"
	}
	if d < time.Hour {
		return fmt.Sprintf("in %dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("in %dh", int(d.Hours()))
	}
	return fmt.Sprintf("in %dd", int(d.Hours()/24))
}

// todayKindIsPredictive reports whether an action's value rests on a market
// forecast. Only those are discounted by the user's realized-over-expected
// ratio; the rest are ISK that already exists.
func todayKindIsPredictive(k TodayActionKind) bool {
	switch k {
	case TodayActionBuy, TodayActionReprice, TodayActionList:
		return true
	default:
		return false
	}
}
