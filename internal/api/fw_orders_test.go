package api

import (
	"testing"

	"eve-flipper/internal/engine"
)

// fw_orders_test.go -- the apply endpoint's one rule, and the two shaping
// functions around it.
//
// The rule is that this endpoint writes only what a visible order proves. It is
// worth a test file of its own because getting it wrong is silent and expensive:
// a lot advanced to `sold` on a guess releases budget headroom for stock that is
// still in a hangar, and every later shipping list is sized against ISK that was
// never received.

// TestFWApplyDecisionRefusesGone is the load-bearing assertion of the whole
// endpoint. A filled order and a cancelled order are the same absence in a
// snapshot, so the tool must say so instead of picking one.
func TestFWApplyDecisionRefusesGone(t *testing.T) {
	m := engine.FWOrderMatch{
		LotID:                 4,
		Status:                engine.FWMatchGone,
		QtyRemaining:          500,
		SuggestedQtyRemaining: 500,
	}
	lot := engine.FWLot{LotID: 4, State: engine.FWLotListed, QtyRemaining: 500}

	verdict, reason := fwApplyDecision(m, lot)
	if verdict != fwApplyRefuse {
		t.Fatalf("a vanished order was acted on: verdict %v", verdict)
	}
	if reason == "" {
		t.Error("refused without a reason, which reads to the user as nothing having happened")
	}
}

// TestFWApplyDecisionRefusesGoneEvenWhenTheEngineSuggestsSold guards the seam
// itself. If the engine ever started suggesting a state on a vanished lot, this
// endpoint must still refuse -- the reason is the invisibility of the order, not
// the absence of a suggestion, so the refusal cannot be conditional on one.
func TestFWApplyDecisionRefusesGoneEvenWhenTheEngineSuggestsSold(t *testing.T) {
	m := engine.FWOrderMatch{
		LotID:                 4,
		Status:                engine.FWMatchGone,
		QtyRemaining:          500,
		SuggestedQtyRemaining: 0,
		SuggestedState:        engine.FWLotSold,
	}
	lot := engine.FWLot{LotID: 4, State: engine.FWLotListed, QtyRemaining: 500}

	if verdict, _ := fwApplyDecision(m, lot); verdict != fwApplyRefuse {
		t.Fatalf("a vanished lot was advanced to sold: verdict %v", verdict)
	}
}

// TestFWApplyDecisionWritesWhatTheBookShows: both applied statuses are backed by
// an order still on the market, so the remaining volume is read rather than
// inferred.
func TestFWApplyDecisionWritesWhatTheBookShows(t *testing.T) {
	partial := engine.FWOrderMatch{
		LotID: 1, Status: engine.FWMatchPartialFill,
		QtyRemaining: 500, SuggestedQtyRemaining: 320, FilledQty: 180,
	}
	if verdict, _ := fwApplyDecision(partial, engine.FWLot{LotID: 1, QtyRemaining: 500}); verdict != fwApplyWrite {
		t.Errorf("partial fill not written: verdict %v", verdict)
	}

	soldOut := engine.FWOrderMatch{
		LotID: 2, Status: engine.FWMatchSoldOut,
		QtyRemaining: 500, SuggestedQtyRemaining: 0, FilledQty: 500,
		SuggestedState: engine.FWLotSold,
	}
	if verdict, _ := fwApplyDecision(soldOut, engine.FWLot{LotID: 2, QtyRemaining: 500}); verdict != fwApplyWrite {
		t.Errorf("sold out not written: verdict %v", verdict)
	}
}

// TestFWApplyDecisionIsQuietWhenNothingMoved: resting is the common case, and a
// re-apply over an already-applied match must produce no row. An apply that
// listed unchanged lots as "applied" would make the response useless for telling
// whether anything actually happened.
func TestFWApplyDecisionIsQuietWhenNothingMoved(t *testing.T) {
	resting := engine.FWOrderMatch{
		LotID: 1, Status: engine.FWMatchResting,
		QtyRemaining: 500, SuggestedQtyRemaining: 500,
	}
	if verdict, _ := fwApplyDecision(resting, engine.FWLot{LotID: 1, QtyRemaining: 500}); verdict != fwApplyNoChange {
		t.Errorf("resting lot reported: verdict %v", verdict)
	}

	// Idempotence: the same partial fill applied twice. The second time the lot
	// already holds the suggested quantity.
	reapplied := engine.FWOrderMatch{
		LotID: 1, Status: engine.FWMatchPartialFill,
		QtyRemaining: 320, SuggestedQtyRemaining: 320,
	}
	if verdict, _ := fwApplyDecision(reapplied, engine.FWLot{LotID: 1, QtyRemaining: 320}); verdict != fwApplyNoChange {
		t.Errorf("re-applying an unchanged fill wrote again: verdict %v", verdict)
	}
}

// TestFWApplyDecisionRefusesUnknownStatus: a status this code has not been taught
// is not one it may act on, and it must surface rather than vanish into the
// "nothing to do" pile.
func TestFWApplyDecisionRefusesUnknownStatus(t *testing.T) {
	m := engine.FWOrderMatch{LotID: 9, Status: "relisted_elsewhere", QtyRemaining: 10}
	verdict, reason := fwApplyDecision(m, engine.FWLot{LotID: 9, QtyRemaining: 10})
	if verdict != fwApplyRefuse {
		t.Fatalf("unknown status acted on: verdict %v", verdict)
	}
	if reason == "" {
		t.Error("refused silently")
	}
}

// TestFWApplyDecisionCoversEveryEngineStatus: the engine owns the status list, so
// this asserts the endpoint has an opinion about each one rather than letting a
// newly added status fall through to the default and be refused by accident.
func TestFWApplyDecisionCoversEveryEngineStatus(t *testing.T) {
	want := map[string]fwApplyVerdict{
		engine.FWMatchResting:     fwApplyNoChange,
		engine.FWMatchPartialFill: fwApplyWrite,
		engine.FWMatchSoldOut:     fwApplyWrite,
		engine.FWMatchGone:        fwApplyRefuse,
	}
	for status, expect := range want {
		m := engine.FWOrderMatch{
			LotID: 1, Status: status,
			QtyRemaining: 100, SuggestedQtyRemaining: 40,
		}
		if status == engine.FWMatchResting {
			m.SuggestedQtyRemaining = 100
		}
		got, _ := fwApplyDecision(m, engine.FWLot{LotID: 1, QtyRemaining: 100})
		if got != expect {
			t.Errorf("status %q: verdict %v, want %v", status, got, expect)
		}
	}
}

// TestFWLinkedDeskOrdersReturnsOnlyLinkedRows: the desk can be hundreds of orders
// across every station the user trades at, and the campaign has no business
// rendering the ones that are not its stock.
func TestFWLinkedDeskOrdersReturnsOnlyLinkedRows(t *testing.T) {
	rec := engine.FWOrderReconciliation{Matches: []engine.FWOrderMatch{
		{LotID: 1, OrderIDs: []int64{300, 100}},
		{LotID: 2, OrderIDs: []int64{100}}, // same order, two lots behind it
		{LotID: 3},                         // gone: no linked row at all
	}}
	all := []engine.OrderDeskOrder{
		{OrderID: 100}, {OrderID: 200}, {OrderID: 300}, {OrderID: 400},
	}

	got := fwLinkedDeskOrders(rec, all)
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2 (100 and 300): %+v", len(got), got)
	}
	// Deduplicated, and stable so the UI does not reshuffle between polls.
	if got[0].OrderID != 100 || got[1].OrderID != 300 {
		t.Errorf("rows not deduplicated and sorted: %d, %d", got[0].OrderID, got[1].OrderID)
	}
}

// TestFWLinkedDeskOrdersEmptyWhenNothingListed: a campaign with no listed lots
// must send no desk rows rather than the whole desk.
func TestFWLinkedDeskOrdersEmptyWhenNothingListed(t *testing.T) {
	rec := engine.FWOrderReconciliation{Matches: []engine.FWOrderMatch{{LotID: 1}}}
	all := []engine.OrderDeskOrder{{OrderID: 100}, {OrderID: 200}}
	if got := fwLinkedDeskOrders(rec, all); len(got) != 0 {
		t.Errorf("unlinked desk rows leaked into the campaign: %+v", got)
	}
}

// TestFWMatchSummaryIsStable: the summary is a log line, so the same
// reconciliation must always read the same way, and an empty one must say so
// rather than render as a blank.
func TestFWMatchSummaryIsStable(t *testing.T) {
	counts := map[string]int{
		engine.FWMatchResting:     3,
		engine.FWMatchGone:        1,
		engine.FWMatchPartialFill: 2,
	}
	first := fwMatchSummary(counts)
	for i := 0; i < 20; i++ {
		if got := fwMatchSummary(counts); got != first {
			t.Fatalf("summary reordered between calls: %q then %q", first, got)
		}
	}
	if want := "1 gone, 2 partial_fill, 3 resting"; first != want {
		t.Errorf("summary: got %q want %q", first, want)
	}
	if got := fwMatchSummary(nil); got != "no listed lots" {
		t.Errorf("empty summary: got %q", got)
	}
}
