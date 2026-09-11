package engine

import (
	"math"
	"testing"
	"time"

	"eve-flipper/internal/esi"
)

const dispTestType = 34

var dispTestNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// dispVenue builds a venue holding one bid and one ask of its own, which is
// the whole region book as far as these tests are concerned — so the
// station's share of regional flow is 1 and fill times stay predictable.
func dispVenue(name string, locationID int64, regionID int32, jumps int, bid, ask float64, history []esi.HistoryEntry) DispositionVenue {
	v := DispositionVenue{
		LocationID:   locationID,
		LocationName: name,
		RegionID:     regionID,
		Jumps:        jumps,
		History:      history,
	}
	if bid > 0 {
		v.RegionOrders = append(v.RegionOrders, esi.MarketOrder{
			OrderID: locationID*10 + 1, TypeID: dispTestType, RegionID: regionID,
			LocationID: locationID, Price: bid, VolumeRemain: 5000, IsBuyOrder: true,
		})
	}
	if ask > 0 {
		v.RegionOrders = append(v.RegionOrders, esi.MarketOrder{
			OrderID: locationID*10 + 2, TypeID: dispTestType, RegionID: regionID,
			LocationID: locationID, Price: ask, VolumeRemain: 5000, IsBuyOrder: false,
		})
	}
	return v
}

// dispDecliningHistory is an item on a steady slide — the shape that must
// never produce a hold plan.
func dispDecliningHistory() []esi.HistoryEntry {
	return recoveryTestHistory(180, func(day int) float64 {
		return 130 * math.Exp(-0.002*float64(day)+0.01*math.Sin(2*math.Pi*float64(day)/17))
	})
}

// dispDippedHistory is the oscillation the recovery model endorses,
// sampled near a trough.
func dispDippedHistory() []esi.HistoryEntry {
	return recoveryTestHistory(173, func(day int) float64 {
		return recoverySine(day, 0.10, 30)
	})
}

func dispInput(home DispositionVenue, elsewhere ...DispositionVenue) DispositionInput {
	return DispositionInput{
		TypeID:               dispTestType,
		TypeName:             "Tritanium",
		Qty:                  100,
		CostBasisISK:         100,
		Home:                 home,
		Elsewhere:            elsewhere,
		UnitVolumeM3:         0.01,
		ShipRateISKPerM3Jump: 1000,
		SalesTaxPercent:      8,
		BrokerFeePercent:     1,
		MinMarginPercent:     3,
		TargetETADays:        3,
		Now:                  dispTestNow,
	}
}

func planOf(t *testing.T, res DispositionResult, kind string) DispositionPlan {
	t.Helper()
	for _, p := range res.Plans {
		if p.Kind == kind {
			return p
		}
	}
	t.Fatalf("no %q plan in %+v", kind, res.Plans)
	return DispositionPlan{}
}

func hasPlan(res DispositionResult, kind string) bool {
	for _, p := range res.Plans {
		if p.Kind == kind {
			return true
		}
	}
	return false
}

func recommended(t *testing.T, res DispositionResult) DispositionPlan {
	t.Helper()
	if len(res.Plans) == 0 {
		t.Fatalf("no plans at all: %s", res.Reason)
	}
	if !res.Plans[0].Recommended {
		t.Fatalf("first plan is not flagged recommended: %+v", res.Plans[0])
	}
	return res.Plans[0]
}

func TestComputeOrderDispositionCutsAnItemInDecline(t *testing.T) {
	// Nothing to wait for and nowhere better to take it: eat the loss and
	// get the ISK back to work. This is the case the plain `cancel` verdict
	// was right about all along.
	in := dispInput(
		dispVenue("Jita IV-4", 60003760, 10000002, 0, 90, 95, dispDecliningHistory()),
		dispVenue("Amarr VIII", 60008494, 10000043, 5, 88, 92, dispDecliningHistory()),
	)

	res := ComputeOrderDisposition(in)

	if got := recommended(t, res); got.Kind != DispositionCut {
		t.Fatalf("recommended %q, want %q (plans %+v)", got.Kind, DispositionCut, res.Plans)
	}
	if hasPlan(res, DispositionHold) {
		t.Fatalf("hold was offered on a declining item: %+v", res.Recovery)
	}
	if res.Recovery.Reason != "trending down, not dipping" {
		t.Fatalf("recovery reason = %q, want the decline reason", res.Recovery.Reason)
	}
	// The haul eats far more than the extra price at Amarr is worth.
	move := planOf(t, res, DispositionMove)
	if move.HaulISK != 1000*0.01*100*5 {
		t.Fatalf("haul = %v, want rate × m³ × qty × jumps", move.HaulISK)
	}
	if move.NetISK >= planOf(t, res, DispositionCut).NetISK {
		t.Fatalf("a 5-jump haul beat selling on the spot: %+v", res.Plans)
	}
}

func TestComputeOrderDispositionHoldsAnEvidencedDip(t *testing.T) {
	// The local book is well below where this item usually trades, and its
	// history says dips this deep have come back within a couple of weeks.
	in := dispInput(dispVenue("Jita IV-4", 60003760, 10000002, 0, 80, 84, dispDippedHistory()))
	in.CostBasisISK = 95

	res := ComputeOrderDisposition(in)

	if got := recommended(t, res); got.Kind != DispositionHold {
		t.Fatalf("recommended %q, want %q (plans %+v)", got.Kind, DispositionHold, res.Plans)
	}
	if res.Recovery.Basis != RecoveryBasisHistory {
		t.Fatalf("recovery basis = %q (%s), want evidenced", res.Recovery.Basis, res.Recovery.Reason)
	}

	hold := planOf(t, res, DispositionHold)
	if hold.ExitPrice != res.Recovery.TargetPrice {
		t.Fatalf("hold exits at %v but the recovery target is %v", hold.ExitPrice, res.Recovery.TargetPrice)
	}
	// Waiting for the bounce has to include waiting for the bounce.
	if hold.DaysToRealise < res.Recovery.MedianDays {
		t.Fatalf("hold realises in %.1fd, sooner than the %.1fd recovery it depends on",
			hold.DaysToRealise, res.Recovery.MedianDays)
	}
	if res.HorizonDays != hold.DaysToRealise {
		t.Fatalf("horizon %.2f is not the slowest plan's %.2f", res.HorizonDays, hold.DaysToRealise)
	}
	// Cutting is the fast plan, so it is the one credited with the hurdle.
	cut := planOf(t, res, DispositionCut)
	if cut.TerminalISK <= cut.NetISK {
		t.Fatalf("cut earned no idle-ISK credit over a %.1fd horizon: %+v", res.HorizonDays, cut)
	}
	if hold.TerminalISK != hold.NetISK {
		t.Fatalf("the slowest plan was credited idle ISK it never had: %+v", hold)
	}
}

func TestComputeOrderDispositionMovesToAHubWorthTheHaul(t *testing.T) {
	// Local market has collapsed, the hub has not, and the freight is small
	// against the gap. This is option 2 actually paying off.
	in := dispInput(
		dispVenue("Hek VIII", 60005686, 10000042, 0, 50, 55, dispDecliningHistory()),
		dispVenue("Amarr VIII", 60008494, 10000043, 3, 95, 100, dispDecliningHistory()),
	)
	in.ShipRateISKPerM3Jump = 100
	in.CostBasisISK = 90

	res := ComputeOrderDisposition(in)

	got := recommended(t, res)
	if got.Kind != DispositionMove {
		t.Fatalf("recommended %q, want %q (plans %+v)", got.Kind, DispositionMove, res.Plans)
	}
	if got.Venue != "Amarr VIII" || got.Jumps != 3 {
		t.Fatalf("move names %q at %d jumps, want Amarr at 3", got.Venue, got.Jumps)
	}
	// Undercut the hub ask rather than matching it — you cannot sell at it.
	if want := NextSellUndercut(100); got.ExitPrice != want {
		t.Fatalf("exit %v, want one step under the ask (%v)", got.ExitPrice, want)
	}
	if got.HaulISK != 100*0.01*100*3 {
		t.Fatalf("haul = %v, want 300", got.HaulISK)
	}
	if got.NetISK != got.GrossISK-got.HaulISK {
		t.Fatalf("net %v is not gross %v less haul %v", got.NetISK, got.GrossISK, got.HaulISK)
	}
	if res.VenuesPriced != 1 || res.VenuesSkipped != 0 {
		t.Fatalf("venue accounting = %d priced / %d skipped, want 1/0", res.VenuesPriced, res.VenuesSkipped)
	}
}

func TestComputeOrderDispositionTakesAHubBidWhenItBeatsListingThere(t *testing.T) {
	// A fat standing bid at the hub pays more, sooner, than undercutting a
	// thin ask — and skips the broker fee on the way.
	in := dispInput(
		dispVenue("Hek VIII", 60005686, 10000042, 0, 50, 55, dispDecliningHistory()),
		dispVenue("Amarr VIII", 60008494, 10000043, 2, 120, 100, dispDecliningHistory()),
	)
	in.ShipRateISKPerM3Jump = 10

	move := planOf(t, ComputeOrderDisposition(in), DispositionMove)

	if move.ExitPrice != 120 {
		t.Fatalf("exit %v, want the 120 bid", move.ExitPrice)
	}
	if want := 120 * 0.92 * 100.0; math.Abs(move.GrossISK-want) > 1e-9 {
		t.Fatalf("gross %v, want %v — sales tax only when hitting a bid", move.GrossISK, want)
	}
	// Hitting a bid is instantaneous; only the travel counts.
	if move.DaysToRealise > 0.02 {
		t.Fatalf("taking a standing bid took %.3fd, want travel time only", move.DaysToRealise)
	}
}

func TestComputeOrderDispositionSkipsVenuesItCannotRouteTo(t *testing.T) {
	// An Upwell structure the SDE has never heard of, or no highsec path at
	// the configured minimum security. Either way the haul cost is unknown,
	// and a move plan priced without one would be a fabrication.
	in := dispInput(
		dispVenue("Jita IV-4", 60003760, 10000002, 0, 90, 95, dispDecliningHistory()),
		dispVenue("Somewhere deep", 1035466617946, 10000043, -1, 200, 210, dispDecliningHistory()),
	)

	res := ComputeOrderDisposition(in)

	if hasPlan(res, DispositionMove) {
		t.Fatalf("priced a move to an unroutable venue: %+v", res.Plans)
	}
	if res.VenuesSkipped != 1 || res.VenuesPriced != 0 {
		t.Fatalf("venue accounting = %d priced / %d skipped, want 0/1", res.VenuesPriced, res.VenuesSkipped)
	}
	if got := recommended(t, res); got.Kind != DispositionCut {
		t.Fatalf("recommended %q, want the only real option", got.Kind)
	}
}

func TestComputeOrderDispositionAdmitsWhenTwoPlansAreTheSame(t *testing.T) {
	// Selling here and hauling one jump to sell there come out within a
	// rounding error of each other. Declaring a winner would be inventing a
	// distinction the numbers do not support.
	in := dispInput(
		dispVenue("Jita IV-4", 60003760, 10000002, 0, 100, 105, dispDecliningHistory()),
		dispVenue("Perimeter", 60015068, 10000002, 1, 90, 101.7, dispDecliningHistory()),
	)
	in.ShipRateISKPerM3Jump = 0

	res := ComputeOrderDisposition(in)

	if len(res.Plans) != 2 {
		t.Fatalf("plans = %+v, want exactly cut and move", res.Plans)
	}
	if !res.TooClose {
		t.Fatalf("gap between %v and %v was not flagged too close",
			res.Plans[0].TerminalISK, res.Plans[1].TerminalISK)
	}
}

func TestComputeOrderDispositionRefusesWithoutACostBasis(t *testing.T) {
	// Every plan here is quoted against what the stock cost. Without that
	// the honest output is a reason, not three confident numbers — the same
	// contract the desk's margin column already keeps.
	in := dispInput(dispVenue("Jita IV-4", 60003760, 10000002, 0, 90, 95, dispDippedHistory()))
	in.CostBasisISK = 0

	res := ComputeOrderDisposition(in)

	if len(res.Plans) != 0 {
		t.Fatalf("plans = %+v, want none without a cost basis", res.Plans)
	}
	if res.Reason == "" {
		t.Fatalf("refused with no reason: %+v", res)
	}
	if res.Recovery.Basis != RecoveryBasisNone {
		t.Fatalf("recovery = %q, want none when we never got as far as fitting it", res.Recovery.Basis)
	}
}

func TestComputeOrderDispositionRefusesWhenThereIsNothingToCompare(t *testing.T) {
	// No bid to hit, no bounce to wait for, nowhere to take it. The panel
	// has to say so rather than render an empty list that reads as "fine".
	in := dispInput(dispVenue("Backwater V", 60099999, 10000069, 0, 0, 0, dispDecliningHistory()))

	res := ComputeOrderDisposition(in)

	if len(res.Plans) != 0 {
		t.Fatalf("plans = %+v, want none", res.Plans)
	}
	if res.Reason == "" {
		t.Fatalf("empty plan list with no explanation: %+v", res)
	}
}

func TestComputeOrderDispositionChargesTheRightFeesPerPlan(t *testing.T) {
	// Taking a standing order pays sales tax. Placing one pays sales tax and
	// broker fee. Getting this backwards is worth about a percent, which is
	// the same order as the gaps being ranked.
	in := dispInput(
		dispVenue("Jita IV-4", 60003760, 10000002, 0, 90, 95, dispDippedHistory()),
		dispVenue("Amarr VIII", 60008494, 10000043, 2, 60, 96, dispDecliningHistory()),
	)
	in.ShipRateISKPerM3Jump = 0

	res := ComputeOrderDisposition(in)

	cut := planOf(t, res, DispositionCut)
	if want := 90 * 0.92 * 100.0; math.Abs(cut.GrossISK-want) > 1e-9 {
		t.Fatalf("cut gross %v, want %v (8%% tax, no broker fee)", cut.GrossISK, want)
	}

	hold := planOf(t, res, DispositionHold)
	if want := res.Recovery.TargetPrice * 0.91 * 100.0; math.Abs(hold.GrossISK-want) > 1e-9 {
		t.Fatalf("hold gross %v, want %v (8%% tax and 1%% broker fee)", hold.GrossISK, want)
	}

	move := planOf(t, res, DispositionMove)
	if want := NextSellUndercut(96) * 0.91 * 100.0; math.Abs(move.GrossISK-want) > 1e-9 {
		t.Fatalf("move gross %v, want %v (listed, so both fees)", move.GrossISK, want)
	}

	for _, p := range res.Plans {
		if want := p.NetISK - 100*float64(in.Qty); math.Abs(p.ProfitISK-want) > 1e-9 {
			t.Fatalf("%s profit %v is not net less the %v position cost", p.Kind, p.ProfitISK, want)
		}
	}
}

func TestComputeOrderDispositionHurdleComesFromTheTabSettings(t *testing.T) {
	// The hurdle is not its own knob: it is one normal trade cycle on this
	// tab. Retuning Target ETA has to move it, or the derivation is a
	// decoration rather than a wiring.
	base := dispInput(dispVenue("Jita IV-4", 60003760, 10000002, 0, 80, 84, dispDippedHistory()))
	base.CostBasisISK = 95

	tight := ComputeOrderDisposition(base)

	slow := base
	slow.TargetETADays = 12
	relaxed := ComputeOrderDisposition(slow)

	if tight.HurdlePctDay != 1.0 {
		t.Fatalf("hurdle = %.3f%%/day, want 3%%/3d", tight.HurdlePctDay)
	}
	if relaxed.HurdlePctDay != 0.25 {
		t.Fatalf("hurdle = %.3f%%/day, want 3%%/12d", relaxed.HurdlePctDay)
	}
	// Same position, same book: only the value of freed ISK changed, so
	// only the fast plan's terminal figure should move.
	tightCut := planOf(t, tight, DispositionCut)
	relaxedCut := planOf(t, relaxed, DispositionCut)
	if relaxedCut.TerminalISK >= tightCut.TerminalISK {
		t.Fatalf("a lower hurdle did not reduce cut's credit: %v vs %v",
			relaxedCut.TerminalISK, tightCut.TerminalISK)
	}
	if relaxedCut.NetISK != tightCut.NetISK {
		t.Fatalf("the hurdle changed the sale itself: %v vs %v", relaxedCut.NetISK, tightCut.NetISK)
	}
}
