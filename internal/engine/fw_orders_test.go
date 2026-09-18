package engine

import (
	"strings"
	"testing"
)

const (
	fwOnnamonIV  = int64(60015070)
	fwVillasenV  = int64(60015108)
	fwInfernoLM  = int32(2613)
	fwScourgeLM  = int32(210)
	fwSellerChar = int64(90000002)
	fwSellerCorp = int64(98000001)
)

func fwListedLot(lotID int64, typeID int32, name string, qty, remaining int64, dest int64) FWLot {
	return FWLot{
		LotID:                 lotID,
		TypeID:                typeID,
		TypeName:              name,
		State:                 FWLotListed,
		Qty:                   qty,
		QtyRemaining:          remaining,
		UnitCostISK:           100,
		ListedPrice:           130,
		DestStationID:         dest,
		AcquiredByCharacterID: 90000001,
		HolderOwnerKind:       "character",
		HolderOwnerID:         fwSellerChar,
		HolderName:            "FW Pilot",
	}
}

func fwSellOrder(orderID int64, typeID int32, location int64, remain int32, price float64) OrderDeskOrder {
	return OrderDeskOrder{
		OrderID:      orderID,
		TypeID:       typeID,
		LocationID:   location,
		IsBuyOrder:   false,
		Price:        price,
		VolumeRemain: remain,
		VolumeTotal:  remain,
	}
}

func fwMatchByLot(t *testing.T, rec FWOrderReconciliation, lotID int64) FWOrderMatch {
	t.Helper()
	for _, m := range rec.Matches {
		if m.LotID == lotID {
			return m
		}
	}
	t.Fatalf("no match for lot %d in %+v", lotID, rec.Matches)
	return FWOrderMatch{}
}

// A lot whose order is still up at its full size has nothing to report, and
// must link to the desk row rather than restate a verdict.
func TestFWLotRestingLinksToItsDeskRow(t *testing.T) {
	lots := []FWLot{fwListedLot(1, fwInfernoLM, "Inferno Light Missile", 5000, 5000, fwOnnamonIV)}
	orders := []OrderDeskOrder{fwSellOrder(7001, fwInfernoLM, fwOnnamonIV, 5000, 130)}

	rec := ReconcileFWLotsWithOrders(lots, orders)
	if len(rec.Matches) != 1 || len(rec.Warnings) != 0 {
		t.Fatalf("unexpected reconciliation: %+v", rec)
	}
	m := rec.Matches[0]
	if m.Status != FWMatchResting {
		t.Fatalf("status = %q, want %q", m.Status, FWMatchResting)
	}
	if m.FilledQty != 0 || m.SuggestedQtyRemaining != 5000 || m.SuggestedState != "" {
		t.Fatalf("resting lot was moved: %+v", m)
	}
	if m.PrimaryOrderID != 7001 || len(m.OrderIDs) != 1 || m.OrderIDs[0] != 7001 {
		t.Fatalf("lot does not link to its desk row: %+v", m)
	}
}

// A lot listed out of the corp wallet, when the campaign declared a character
// as seller, is still this campaign's stock. Requiring the declared seller to
// match would report it vanished and then read the same order as competition.
func TestFWLotListedFromTheWrongOwnerIsStillMatched(t *testing.T) {
	lot := fwListedLot(1, fwInfernoLM, "Inferno Light Missile", 5000, 5000, fwOnnamonIV)
	corpOrder := fwSellOrder(7001, fwInfernoLM, fwOnnamonIV, 4200, 130)
	corpOrder.OwnerKind = "corporation"
	corpOrder.CharacterID = fwSellerCorp
	corpOrder.CharacterName = "Onnamon Logistics"

	rec := ReconcileFWLotsWithOrders([]FWLot{lot}, []OrderDeskOrder{corpOrder})
	m := fwMatchByLot(t, rec, 1)
	if m.Status != FWMatchPartialFill {
		t.Fatalf("status = %q, want a partial fill against the corp order", m.Status)
	}
	if m.FilledQty != 800 || m.SuggestedQtyRemaining != 4200 {
		t.Fatalf("fill misread: filled=%d remaining=%d", m.FilledQty, m.SuggestedQtyRemaining)
	}
	// No holder-owned row exists, so the link falls to the front of the queue.
	if m.PrimaryOrderID != 7001 {
		t.Fatalf("primary order = %d, want the only live row", m.PrimaryOrderID)
	}
	// One lot, one order, counted once.
	if len(rec.Matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(rec.Matches))
	}
}

// The link prefers the holder's own row when there is one, because that is the
// row whose fees and verdict belong to this lot.
func TestFWPrimaryOrderPrefersTheHolder(t *testing.T) {
	lot := fwListedLot(1, fwInfernoLM, "Inferno Light Missile", 5000, 5000, fwOnnamonIV)

	cheaper := fwSellOrder(7001, fwInfernoLM, fwOnnamonIV, 2000, 120)
	cheaper.OwnerKind = "corporation"
	cheaper.CharacterID = fwSellerCorp
	mine := fwSellOrder(7002, fwInfernoLM, fwOnnamonIV, 3000, 130)
	mine.OwnerKind = "character"
	mine.CharacterID = fwSellerChar

	rec := ReconcileFWLotsWithOrders([]FWLot{lot}, []OrderDeskOrder{cheaper, mine})
	m := fwMatchByLot(t, rec, 1)
	if m.PrimaryOrderID != 7002 {
		t.Fatalf("primary order = %d, want the holder's own row 7002", m.PrimaryOrderID)
	}
	// Both rows are this campaign's stock, so the book is 5,000 and nothing sold.
	if m.Status != FWMatchResting || m.FilledQty != 0 {
		t.Fatalf("two of our own rows read as a fill: %+v", m)
	}
}

// A clean drain, with the type still visible at the station, is the one
// transition worth suggesting on its own.
func TestFWLotDrainedWhileOtherOrdersRemainSuggestsSold(t *testing.T) {
	lots := []FWLot{
		fwListedLot(1, fwInfernoLM, "Inferno Light Missile", 2000, 2000, fwOnnamonIV),
		fwListedLot(2, fwInfernoLM, "Inferno Light Missile", 3000, 3000, fwOnnamonIV),
	}
	// 1,200 units left across the station. FIFO: lot 1 is fully credited first.
	orders := []OrderDeskOrder{fwSellOrder(7001, fwInfernoLM, fwOnnamonIV, 1200, 130)}

	rec := ReconcileFWLotsWithOrders(lots, orders)
	first := fwMatchByLot(t, rec, 1)
	second := fwMatchByLot(t, rec, 2)

	if first.Status != FWMatchSoldOut || first.SuggestedState != FWLotSold {
		t.Fatalf("oldest lot not drained first: %+v", first)
	}
	if first.FilledQty != 2000 || first.SuggestedQtyRemaining != 0 {
		t.Fatalf("FIFO share wrong on lot 1: %+v", first)
	}
	if second.Status != FWMatchPartialFill || second.SuggestedState != "" {
		t.Fatalf("younger lot should be a partial fill that stays listed: %+v", second)
	}
	if second.FilledQty != 1800 || second.SuggestedQtyRemaining != 1200 {
		t.Fatalf("FIFO remainder wrong on lot 2: %+v", second)
	}
	if first.FilledQty+second.FilledQty != 3800 {
		t.Fatal("the two lots do not add up to the observed fill")
	}
}

// A vanished order is the ambiguous case, and the whole reason this code does
// not write state. Filled and cancelled look identical from a snapshot, so it
// reports and asks rather than releasing headroom for stock that may be sitting
// in a hangar.
func TestFWLotWithNoLiveOrderIsReportedNotSold(t *testing.T) {
	lots := []FWLot{fwListedLot(1, fwInfernoLM, "Inferno Light Missile", 5000, 5000, fwOnnamonIV)}
	// A buy order for the same type, and a sell order at another station:
	// neither is evidence about this lot.
	buy := fwSellOrder(7001, fwInfernoLM, fwOnnamonIV, 5000, 90)
	buy.IsBuyOrder = true
	elsewhere := fwSellOrder(7002, fwInfernoLM, fwVillasenV, 5000, 130)

	rec := ReconcileFWLotsWithOrders(lots, []OrderDeskOrder{buy, elsewhere})
	m := fwMatchByLot(t, rec, 1)
	if m.Status != FWMatchGone {
		t.Fatalf("status = %q, want %q", m.Status, FWMatchGone)
	}
	if m.SuggestedState != "" {
		t.Fatalf("a vanished order must not suggest a state, got %q", m.SuggestedState)
	}
	if m.SuggestedQtyRemaining != 5000 || m.FilledQty != 0 {
		t.Fatalf("a vanished order must not invent a fill: %+v", m)
	}
	if m.Note == "" || !strings.Contains(m.Note, "confirm") {
		t.Fatalf("note does not ask for confirmation: %q", m.Note)
	}
}

// More on the shelf than the campaign bought is real stock somebody listed.
// Netting it off would manufacture a negative fill and quietly understate cover.
func TestFWSurplusOnTheShelfWarnsAndNeverFillsNegative(t *testing.T) {
	lots := []FWLot{fwListedLot(1, fwScourgeLM, "Scourge Light Missile", 1000, 1000, fwOnnamonIV)}
	orders := []OrderDeskOrder{fwSellOrder(7001, fwScourgeLM, fwOnnamonIV, 4000, 130)}

	rec := ReconcileFWLotsWithOrders(lots, orders)
	m := fwMatchByLot(t, rec, 1)
	if m.Status != FWMatchResting || m.FilledQty != 0 || m.SuggestedQtyRemaining != 1000 {
		t.Fatalf("surplus disturbed the lot: %+v", m)
	}
	if len(rec.Warnings) != 1 {
		t.Fatalf("expected one surplus warning, got %v", rec.Warnings)
	}
	if !strings.Contains(rec.Warnings[0], "Scourge Light Missile") ||
		!strings.Contains(rec.Warnings[0], "3000") {
		t.Fatalf("warning does not name the type and the surplus: %q", rec.Warnings[0])
	}
}

// Only listed lots are reconciled. `at_dest` stock is unlisted by definition
// and `planned` has not been bought, so neither can be judged by the book.
func TestFWReconcileIgnoresUnlistedLots(t *testing.T) {
	atDest := fwListedLot(1, fwInfernoLM, "Inferno Light Missile", 5000, 5000, fwOnnamonIV)
	atDest.State = FWLotAtDest
	planned := fwListedLot(2, fwScourgeLM, "Scourge Light Missile", 1000, 1000, fwOnnamonIV)
	planned.State = FWLotPlanned
	sold := fwListedLot(3, fwInfernoLM, "Inferno Light Missile", 100, 0, fwOnnamonIV)
	sold.State = FWLotSold

	rec := ReconcileFWLotsWithOrders(
		[]FWLot{atDest, planned, sold},
		[]OrderDeskOrder{fwSellOrder(7001, fwInfernoLM, fwOnnamonIV, 5000, 130)})
	if len(rec.Matches) != 0 || len(rec.Warnings) != 0 {
		t.Fatalf("unlisted lots were reconciled: %+v", rec)
	}
}

// A lot's own destination decides which book it is read against, not the
// campaign's -- that is what keeps the two-tier option open.
func TestFWLotsAtDifferentStationsReadDifferentBooks(t *testing.T) {
	lots := []FWLot{
		fwListedLot(1, fwInfernoLM, "Inferno Light Missile", 5000, 5000, fwOnnamonIV),
		fwListedLot(2, fwInfernoLM, "Inferno Light Missile", 1000, 1000, fwVillasenV),
	}
	orders := []OrderDeskOrder{
		fwSellOrder(7001, fwInfernoLM, fwOnnamonIV, 5000, 130),
		fwSellOrder(7002, fwInfernoLM, fwVillasenV, 400, 150),
	}

	rec := ReconcileFWLotsWithOrders(lots, orders)
	onnamon := fwMatchByLot(t, rec, 1)
	villasen := fwMatchByLot(t, rec, 2)
	if onnamon.Status != FWMatchResting || onnamon.PrimaryOrderID != 7001 {
		t.Fatalf("Onnamon lot read the wrong book: %+v", onnamon)
	}
	if villasen.Status != FWMatchPartialFill || villasen.FilledQty != 600 || villasen.PrimaryOrderID != 7002 {
		t.Fatalf("Villasen lot read the wrong book: %+v", villasen)
	}
}

// The buyer stays home. A lot the Jita alt paid for, held and listed by the FW
// character, reconciles from a book containing only the FW character's orders --
// no buyer session anywhere in the inputs. This is the seam's share of the
// "don't make the Jita alt wander" constraint.
func TestFWReconcileNeedsNothingFromTheBuyer(t *testing.T) {
	lot := fwListedLot(1, fwInfernoLM, "Inferno Light Missile", 5000, 5000, fwOnnamonIV)
	lot.AcquiredByCharacterID = 90000001 // the Jita alt, not logged in

	sellerOnly := fwSellOrder(7001, fwInfernoLM, fwOnnamonIV, 4000, 130)
	sellerOnly.OwnerKind = "character"
	sellerOnly.CharacterID = fwSellerChar
	sellerOnly.CharacterName = "FW Pilot"

	rec := ReconcileFWLotsWithOrders([]FWLot{lot}, []OrderDeskOrder{sellerOnly})
	m := fwMatchByLot(t, rec, 1)
	if m.Status != FWMatchPartialFill || m.FilledQty != 1000 {
		t.Fatalf("seller-only book did not reconcile the buyer's lot: %+v", m)
	}
	if m.PrimaryOrderID != 7001 {
		t.Fatalf("primary order = %d, want the seller's own row", m.PrimaryOrderID)
	}
}
