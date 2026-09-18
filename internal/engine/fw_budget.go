package engine

import (
	"fmt"
	"math"
	"sort"
)

// The lot lifecycle. A lot is one purchase of one type for one destination, and
// its state says where the ISK is, not where the item is.
//
// The four middle states all consume budget and consume the same amount: `bought`
// is ISK sitting in the buyer's hangar, `in_transit` the same ISK inside a
// contract, `at_dest` the same ISK in the seller's hangar, `listed` the same ISK
// resting on the market. Nothing comes back until a unit actually sells, so
// moving a lot along the handoff must not change headroom by one ISK.
//
// `pulled` means the lot has left the campaign: hauled back, sold elsewhere,
// written off. It is NOT what happens when an order is taken off the market --
// that is `at_dest`, and it still consumes, because the ISK is still gone. The
// distinction matters because those two readings of "pulled" differ by the whole
// value of the lot.
const (
	FWLotPlanned   = "planned"
	FWLotBought    = "bought"
	FWLotInTransit = "in_transit"
	FWLotAtDest    = "at_dest"
	FWLotListed    = "listed"
	FWLotSold      = "sold"
	FWLotPulled    = "pulled"
)

// fwLotLifecycle fixes the order of the per-state breakdown. Lifecycle order
// rather than descending ISK, because the question that breakdown answers is
// "where is my capital stuck", and the answer is a position along this line: a
// pile in `bought` is an unflown haul, a pile in `at_dest` is stock nobody has
// listed.
var fwLotLifecycle = []string{
	FWLotPlanned, FWLotBought, FWLotInTransit, FWLotAtDest, FWLotListed, FWLotSold, FWLotPulled,
}

// FWLotStates is the lifecycle in order: what a UI offers as the next step, and
// what a store validates against.
func FWLotStates() []string {
	return append([]string(nil), fwLotLifecycle...)
}

// IsFWLotState reports whether state is one this code knows.
//
// MeasureFWBudget tolerates an unknown state rather than dropping the lot, so a
// bad row still shows up in the counts. A store should refuse to write one all
// the same: an unknown state does not consume budget, so a typo would quietly
// hand back headroom that was already spent.
func IsFWLotState(state string) bool {
	for _, known := range fwLotLifecycle {
		if state == known {
			return true
		}
	}
	return false
}

// FWLot is the durable record and the only source of the budget.
//
// It carries its own owner triple because the buyer is routinely not the holder --
// the Jita alt buys, an FW character or the corporation holds and sells -- and its
// own destination because a lot's dest is defaulted from the campaign rather than
// dictated by it.
type FWLot struct {
	LotID    int64  `json:"lot_id"`
	TypeID   int32  `json:"type_id"`
	TypeName string `json:"type_name"`
	State    string `json:"state"`

	Qty          int64 `json:"qty"`
	QtyRemaining int64 `json:"qty_remaining"`
	// UnitCostISK is what the unit was actually bought for, not what it is worth
	// now. The budget is capital at cost; revaluing it would make headroom drift
	// with the Jita market while no ISK moved.
	UnitCostISK float64 `json:"unit_cost_isk"`
	ListedPrice float64 `json:"listed_price"`

	DestStationID int64 `json:"dest_station_id"`

	AcquiredByCharacterID int64  `json:"acquired_by_character_id"`
	HolderOwnerKind       string `json:"holder_owner_kind"`
	HolderOwnerID         int64  `json:"holder_owner_id"`
	HolderName            string `json:"holder_name"`
}

// ConsumesBudget reports whether this lot's remaining units still tie up capital.
//
// `planned` does not, which is a deliberate departure from the plan's shorthand
// formula (everything not `sold` or `pulled`). A planned lot has spent nothing --
// it is a suggestion. Counting it would make the trim in BuildFWShipment
// subtract the plan from itself: generate a plan, save it, regenerate, and the
// second pass would find no headroom for the very items it just proposed. The
// plan's own state list and its budget test both name the four states below, so
// this is the reading those agree on.
func (l FWLot) ConsumesBudget() bool {
	switch l.State {
	case FWLotBought, FWLotInTransit, FWLotAtDest, FWLotListed:
		return true
	default:
		return false
	}
}

// CommittedISK is the capital this lot still ties up, at cost. It is zero for a
// state that does not consume, so the caller never has to remember to check.
func (l FWLot) CommittedISK() float64 {
	if !l.ConsumesBudget() {
		return 0
	}
	return fwLotValue(l.QtyRemaining, l.UnitCostISK)
}

// ListedValueISK is what the resting units would fetch at the price they are
// listed at. It is displayed and never gates: it is an expectation, and treating
// an expectation as headroom is how a budget stops being one.
func (l FWLot) ListedValueISK() float64 {
	if l.State != FWLotListed {
		return 0
	}
	return fwLotValue(l.QtyRemaining, l.ListedPrice)
}

// fwLotValue multiplies units by a per-unit figure, refusing to return a number
// that would poison a sum. A negative or NaN cost is bad data, and letting it
// through would make headroom read as unlimited.
func fwLotValue(qty int64, perUnit float64) float64 {
	if qty <= 0 || perUnit <= 0 || math.IsNaN(perUnit) || math.IsInf(perUnit, 0) {
		return 0
	}
	return float64(qty) * perUnit
}

// FWOwnerCommitment is one holder's share of committed capital. Stock sitting in
// a hangar for a week is ISK doing nothing, and this is what makes it visible
// rather than hidden inside a campaign total.
type FWOwnerCommitment struct {
	OwnerKind    string  `json:"owner_kind"`
	OwnerID      int64   `json:"owner_id"`
	OwnerName    string  `json:"owner_name"`
	CommittedISK float64 `json:"committed_isk"`
	Units        int64   `json:"units"`
	Lots         int     `json:"lots"`
}

// FWStateCommitment is one lifecycle state's share.
type FWStateCommitment struct {
	State        string  `json:"state"`
	CommittedISK float64 `json:"committed_isk"`
	Units        int64   `json:"units"`
	Lots         int     `json:"lots"`
}

// FWDestCommitment is one destination's share. Only one destination exists per
// campaign today, but lots carry their own dest, so this grouping is what keeps
// the two-tier option additive instead of a migration.
type FWDestCommitment struct {
	DestStationID int64   `json:"dest_station_id"`
	CommittedISK  float64 `json:"committed_isk"`
	Units         int64   `json:"units"`
	Lots          int     `json:"lots"`
}

// FWBudget is the campaign's capital position.
//
// The budget is the campaign's, not a character's: ISK the Jita alt spent on
// stock the FW character holds is still committed, and which wallet paid is
// accounting. Only the campaign total gates; the breakdowns are for reading.
type FWBudget struct {
	BudgetISK    float64 `json:"budget_isk"`
	CommittedISK float64 `json:"committed_isk"`
	// HeadroomISK may be negative, and is not clamped. An over-committed campaign
	// is a fact worth seeing; showing it as zero would hide how far over.
	HeadroomISK float64 `json:"headroom_isk"`
	// ListedValueISK is display only -- see FWLot.ListedValueISK.
	ListedValueISK float64 `json:"listed_value_isk"`

	Lots          int `json:"lots"`
	CommittedLots int `json:"committed_lots"`

	ByState []FWStateCommitment `json:"by_state"`
	ByOwner []FWOwnerCommitment `json:"by_owner"`
	ByDest  []FWDestCommitment  `json:"by_dest"`
}

// OverCommitted is true when committed capital has passed the budget, which can
// happen legitimately: lower the budget on a running campaign and every existing
// lot stays bought.
func (b FWBudget) OverCommitted() bool { return b.HeadroomISK < 0 }

// TrimHeadroom is the headroom a shipping list may spend: negative headroom
// buys nothing rather than owing something.
func (b FWBudget) TrimHeadroom() float64 {
	if b.HeadroomISK <= 0 || math.IsNaN(b.HeadroomISK) {
		return 0
	}
	return b.HeadroomISK
}

// fwOwnerKey identifies a holder. Kind and ID together, because a character and a
// corporation share an ID space only by luck and this does not rely on luck.
type fwOwnerKey struct {
	kind string
	id   int64
}

// MeasureFWBudget totals committed capital at cost and breaks it down by state,
// holder and destination.
//
// Every breakdown is a breakdown *of committed capital*, so only consuming lots
// appear in ByOwner and ByDest -- a sold lot under a holder's name would read as
// stranded stock. ByState is the exception: it lists every state that has lots,
// including `sold` and `pulled` at zero ISK, because "40 lots sold" is the answer
// to a question the panel is being asked.
func MeasureFWBudget(budgetISK float64, lots []FWLot) FWBudget {
	if budgetISK < 0 || math.IsNaN(budgetISK) {
		budgetISK = 0
	}
	budget := FWBudget{BudgetISK: budgetISK, Lots: len(lots)}

	byState := make(map[string]*FWStateCommitment, len(fwLotLifecycle))
	byOwner := make(map[fwOwnerKey]*FWOwnerCommitment)
	byDest := make(map[int64]*FWDestCommitment)

	for _, lot := range lots {
		state := byState[lot.State]
		if state == nil {
			state = &FWStateCommitment{State: lot.State}
			byState[lot.State] = state
		}
		state.Lots++
		budget.ListedValueISK += lot.ListedValueISK()

		if !lot.ConsumesBudget() {
			continue
		}

		committed := lot.CommittedISK()
		units := lot.QtyRemaining
		if units < 0 {
			units = 0
		}

		budget.CommittedISK += committed
		budget.CommittedLots++
		state.CommittedISK += committed
		state.Units += units

		key := fwOwnerKey{kind: lot.HolderOwnerKind, id: lot.HolderOwnerID}
		owner := byOwner[key]
		if owner == nil {
			owner = &FWOwnerCommitment{OwnerKind: key.kind, OwnerID: key.id, OwnerName: lot.HolderName}
			byOwner[key] = owner
		}
		if owner.OwnerName == "" {
			owner.OwnerName = lot.HolderName
		}
		owner.CommittedISK += committed
		owner.Units += units
		owner.Lots++

		dest := byDest[lot.DestStationID]
		if dest == nil {
			dest = &FWDestCommitment{DestStationID: lot.DestStationID}
			byDest[lot.DestStationID] = dest
		}
		dest.CommittedISK += committed
		dest.Units += units
		dest.Lots++
	}

	budget.HeadroomISK = budget.BudgetISK - budget.CommittedISK

	for _, state := range fwLotLifecycle {
		if row := byState[state]; row != nil {
			budget.ByState = append(budget.ByState, *row)
			delete(byState, state)
		}
	}
	// Anything left is a state this code does not know about. It still shows,
	// after the known ones, because dropping it would make the lot counts in the
	// breakdown disagree with the lot count above it.
	unknown := make([]string, 0, len(byState))
	for state := range byState {
		unknown = append(unknown, state)
	}
	sort.Strings(unknown)
	for _, state := range unknown {
		budget.ByState = append(budget.ByState, *byState[state])
	}

	for _, owner := range byOwner {
		budget.ByOwner = append(budget.ByOwner, *owner)
	}
	sort.Slice(budget.ByOwner, func(i, j int) bool {
		if budget.ByOwner[i].CommittedISK != budget.ByOwner[j].CommittedISK {
			return budget.ByOwner[i].CommittedISK > budget.ByOwner[j].CommittedISK
		}
		if budget.ByOwner[i].OwnerKind != budget.ByOwner[j].OwnerKind {
			return budget.ByOwner[i].OwnerKind < budget.ByOwner[j].OwnerKind
		}
		return budget.ByOwner[i].OwnerID < budget.ByOwner[j].OwnerID
	})

	for _, dest := range byDest {
		budget.ByDest = append(budget.ByDest, *dest)
	}
	sort.Slice(budget.ByDest, func(i, j int) bool {
		if budget.ByDest[i].CommittedISK != budget.ByDest[j].CommittedISK {
			return budget.ByDest[i].CommittedISK > budget.ByDest[j].CommittedISK
		}
		return budget.ByDest[i].DestStationID < budget.ByDest[j].DestStationID
	})

	return budget
}

// FWShipmentConfig bounds a shipping list.
//
// The cargo bound is off unless both CargoCapacityM3 and MaxTrips are set,
// because a hold size alone is not a limit -- it is a number of trips. Freight is
// where hull economics live: a packaged frigate is 2,500 m3, so a deep space
// transport carries 24 of them while a full ammo restock is a rounding error.
type FWShipmentConfig struct {
	HeadroomISK     float64 `json:"headroom_isk"`
	CargoCapacityM3 float64 `json:"cargo_capacity_m3"`
	MaxTrips        int     `json:"max_trips"`
}

// FWShipmentLine is one supply row with what the budget could actually afford.
//
// The embedded row is never mutated. PlannedQty mirrors its SuggestedQty so the
// pair reads together on screen -- what cover asked for beside what fits -- and
// so a trimmed line still says what it was trimmed from. Hiding the difference
// by overwriting SuggestedQty would make a half-funded gap look like a small one.
type FWShipmentLine struct {
	FWSupplyRow
	PlannedQty  int64   `json:"planned_qty"`
	ShipQty     int64   `json:"ship_qty"`
	ShipCargoM3 float64 `json:"ship_cargo_m3"`
	ShipCostISK float64 `json:"ship_cost_isk"`
	// ShipProfitISK is what the trimmed quantity earns, not what the row asked
	// for. A line cut to a third of its cover deficit earns a third as much, and
	// showing the untrimmed figure would make the budget look better than it is.
	ShipProfitISK float64 `json:"ship_profit_isk"`
	// TrimReason is empty when the line ships in full.
	TrimReason string `json:"trim_reason"`
}

// Funded reports whether the line ships what cover asked for.
func (l FWShipmentLine) Funded() bool { return l.ShipQty >= l.PlannedQty }

// FWShipment is the buy list: what to buy, what it costs, what it takes to move.
type FWShipment struct {
	Lines        []FWShipmentLine `json:"lines"`
	TotalCostISK float64          `json:"total_cost_isk"`
	TotalCargoM3 float64          `json:"total_cargo_m3"`
	// TotalLandedISK is the stock plus the freight to move it -- what the
	// shipment has to earn back before any of it is profit, and what MarginPct
	// is taken against. The budget still gates on TotalCostISK: freight buys no
	// stock, so it commits no capital the campaign can recover.
	TotalLandedISK float64 `json:"total_landed_isk"`
	// TotalProfitISK is the sum of the lines, so a line the budget dropped
	// contributes nothing. It is what this shipment earns if it all sells at the
	// suggested prices -- a ceiling on the outcome, not a forecast of it.
	TotalProfitISK float64 `json:"total_profit_isk"`
	MarginPct      float64 `json:"margin_pct"`
	// Trips is zero for an empty shipment. RouteCargoTrips returns 1 for an empty
	// cargo, which is right for a route hop and wrong for a shipment: there is no
	// trip to make when there is nothing to carry.
	Trips int `json:"trips"`

	HeadroomISK  float64 `json:"headroom_isk"`
	RemainingISK float64 `json:"remaining_isk"`

	FullyFunded int `json:"fully_funded"`
	Trimmed     int `json:"trimmed"`
	Dropped     int `json:"dropped"`

	// Notes are facts about the shape of this list, not advice. The trim is
	// greedy in rank order by design, and its one uncomfortable consequence is
	// worth stating rather than silently smoothing over.
	Notes []string `json:"notes"`
}

// concentrationNoteThreshold is the share of the budget one line has to absorb
// before the list says so. Half: below that, "the biggest hole first" is just the
// list working, and above it the answer to "why is nothing else funded" is a
// single row.
const concentrationNoteThreshold = 0.5

// BuildFWShipment trims a supply plan to what the budget and the hold can carry.
//
// It walks rows in the order given and never re-sorts. BuildFWSupplyPlan already
// ranks them gap-before-thin and then by cover deficit, which is what "1.5B buys
// the biggest holes first" means, and re-deriving that order here would put the
// ranking in two places where they could drift apart.
//
// Greedy in that order has a consequence worth being explicit about: one
// expensive top-ranked row can absorb the entire budget and leave six cheap
// fast-moving gaps unfunded. That is faithful to biggest-holes-first, so it is
// what happens -- but the line says what it was trimmed from, the shipment counts
// what got dropped, and Notes names the row that ate the budget.
//
// Shippable is enforced here rather than in the plan. A row seen on fewer than
// MinKillsWithItem losses keeps its quantity in the plan so thin evidence stays
// visible with a number beside it, and this is the point where nothing gets
// bought on one killmail's worth of evidence.
func BuildFWShipment(rows []FWSupplyRow, cfg FWShipmentConfig) FWShipment {
	headroom := cfg.HeadroomISK
	if headroom <= 0 || math.IsNaN(headroom) {
		headroom = 0
	}
	shipment := FWShipment{HeadroomISK: headroom, RemainingISK: headroom}

	remainingM3 := math.Inf(1)
	if cfg.MaxTrips > 0 && cfg.CargoCapacityM3 > 0 {
		remainingM3 = float64(cfg.MaxTrips) * cfg.CargoCapacityM3
	}

	for _, row := range rows {
		if row.SuggestedQty <= 0 || !row.Shippable {
			continue
		}

		line := FWShipmentLine{FWSupplyRow: row, PlannedQty: row.SuggestedQty}
		qty := row.SuggestedQty
		bound, limit := "", ""

		if unit := row.JitaBestSell; unit > 0 && !math.IsNaN(unit) && !math.IsInf(unit, 0) {
			if affordable := int64(math.Floor(shipment.RemainingISK / unit)); affordable < qty {
				qty, bound = affordable, "budget"
				limit = fmt.Sprintf("%.0f ISK of headroom left at %.2f a unit", shipment.RemainingISK, unit)
			}
		}
		if unit := row.VolumeM3; unit > 0 && !math.IsInf(remainingM3, 1) {
			if fits := int64(math.Floor(remainingM3 / unit)); fits < qty {
				qty, bound = fits, "cargo"
				limit = fmt.Sprintf("%.0f m3 of hold left at %.2f m3 a unit", remainingM3, unit)
			}
		}
		if qty < 0 {
			qty = 0
		}

		switch {
		case qty == 0:
			line.TrimReason = fmt.Sprintf("dropped -- %s: %s", bound, limit)
			shipment.Dropped++
		case qty < line.PlannedQty:
			line.TrimReason = fmt.Sprintf("%d of %d -- %s: %s", qty, line.PlannedQty, bound, limit)
			shipment.Trimmed++
		default:
			shipment.FullyFunded++
		}

		line.ShipQty = qty
		line.ShipCargoM3 = float64(qty) * row.VolumeM3
		line.ShipCostISK = float64(qty) * row.JitaBestSell
		line.ShipProfitISK = float64(qty) * row.UnitProfitISK

		shipment.RemainingISK -= line.ShipCostISK
		if shipment.RemainingISK < 0 {
			shipment.RemainingISK = 0
		}
		if !math.IsInf(remainingM3, 1) {
			remainingM3 -= line.ShipCargoM3
			if remainingM3 < 0 {
				remainingM3 = 0
			}
		}
		shipment.TotalCostISK += line.ShipCostISK
		shipment.TotalCargoM3 += line.ShipCargoM3
		shipment.TotalLandedISK += float64(qty) * row.LandedCost
		shipment.TotalProfitISK += line.ShipProfitISK
		shipment.Lines = append(shipment.Lines, line)
	}

	if shipment.TotalCargoM3 > 0 {
		shipment.Trips = RouteCargoTrips(shipment.TotalCargoM3, cfg.CargoCapacityM3)
	}
	if shipment.TotalLandedISK > 0 {
		shipment.MarginPct = shipment.TotalProfitISK / shipment.TotalLandedISK * 100
	}
	shipment.Notes = fwShipmentNotes(shipment, cfg)
	return shipment
}

// fwShipmentNotes states the things about a greedy list that are not visible
// from any single line.
func fwShipmentNotes(shipment FWShipment, cfg FWShipmentConfig) []string {
	var notes []string

	// An unbounded list must not read as a list someone sized a hauler for. m3
	// and trips are still reported when the bound is off -- they just stop
	// trimming -- and a reported "7 trips" with nothing trimmed to fit looks
	// exactly like a 7-trip haul that was approved. So say which it is.
	//
	// Only past one hold, and only with a hold size to measure against: a
	// single-trip list has no trip count to misread, and with no ship profile
	// there is nothing to compare the volume to, so either note would be noise
	// on every plan that does not set a trip limit.
	if cfg.MaxTrips <= 0 && cfg.CargoCapacityM3 > 0 && shipment.Trips > 1 {
		notes = append(notes, fmt.Sprintf(
			"The hauler limit is off, so nothing was trimmed to fit: %.0f m3 is about %d trips "+
				"at %.0f m3. Set a trip limit to size the list to the hauls you will actually fly.",
			shipment.TotalCargoM3, shipment.Trips, cfg.CargoCapacityM3))
	}

	starved := shipment.Trimmed + shipment.Dropped
	if starved > 0 && shipment.TotalCostISK > 0 && len(shipment.Lines) > 0 {
		top := shipment.Lines[0]
		if share := top.ShipCostISK / shipment.TotalCostISK; share >= concentrationNoteThreshold {
			notes = append(notes, fmt.Sprintf(
				"%s took %.0f%% of the list at cost; %d further item(s) were trimmed or dropped. "+
					"The list buys the biggest holes first, so raising the budget or excluding that "+
					"item is what funds the cheaper gaps.",
				top.TypeName, share*100, starved))
		}
	}
	if shipment.Dropped > 0 {
		notes = append(notes, fmt.Sprintf("%d item(s) got nothing at this budget.", shipment.Dropped))
	}
	return notes
}
