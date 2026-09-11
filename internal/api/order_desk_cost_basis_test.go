package api

import (
	"math"
	"testing"

	"eve-flipper/internal/engine"
)

func TestBlendOpenPositionCostBasis(t *testing.T) {
	// The FIFO journal tracks bought stock and built stock as separate
	// pools, so the same type turns up twice. Averaging the two averages
	// would let 10 units bought dear outweigh 1,000 built cheap.
	got := blendOpenPositionCostBasis([]engine.JournalOpenPosition{
		{TypeID: 34, Source: engine.LotSourceTrade, Qty: 10, AvgUnitCost: 100},
		{TypeID: 34, Source: engine.LotSourceManufacture, Qty: 1000, AvgUnitCost: 10},
		{TypeID: 35, Source: engine.LotSourceTrade, Qty: 5, AvgUnitCost: 50},
	})

	want := (10*100.0 + 1000*10.0) / 1010.0
	if math.Abs(got[34]-want) > 1e-9 {
		t.Fatalf("blended cost for 34 = %v, want the qty-weighted %v", got[34], want)
	}
	if got[35] != 50 {
		t.Fatalf("cost for 35 = %v, want 50", got[35])
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
}

func TestBlendOpenPositionCostBasisSkipsUnusablePositions(t *testing.T) {
	// A zero or negative cost is not a free item, it is a gap in the
	// archive. Passing it through would have the desk call every sell order
	// on that type pure profit.
	got := blendOpenPositionCostBasis([]engine.JournalOpenPosition{
		{TypeID: 34, Qty: 0, AvgUnitCost: 100},
		{TypeID: 35, Qty: 10, AvgUnitCost: 0},
		{TypeID: 36, Qty: -5, AvgUnitCost: 100},
		{TypeID: 37, Qty: 10, AvgUnitCost: -1},
		{TypeID: 38, Qty: 4, AvgUnitCost: 25},
		// Same type, one usable pool and one not: the usable half stands
		// on its own rather than being dragged toward zero.
		{TypeID: 38, Qty: 10, AvgUnitCost: 0},
	})

	if len(got) != 1 {
		t.Fatalf("map = %v, want only the one usable type", got)
	}
	if got[38] != 25 {
		t.Fatalf("cost for 38 = %v, want 25", got[38])
	}
}

func TestBlendOpenPositionCostBasisReturnsNilWhenNothingIsHeld(t *testing.T) {
	// nil is what the Orders tab reads as "unmeasured", which is the whole
	// point: a cold wallet archive must leave sell rows exactly as they were
	// before margins existed.
	if got := blendOpenPositionCostBasis(nil); got != nil {
		t.Fatalf("blend(nil) = %v, want nil", got)
	}
	if got := blendOpenPositionCostBasis([]engine.JournalOpenPosition{
		{TypeID: 34, Qty: 0, AvgUnitCost: 0},
	}); got != nil {
		t.Fatalf("blend(unusable) = %v, want nil", got)
	}
}
