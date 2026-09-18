package engine

import (
	"fmt"
	"sort"
)

// fw_orders.go — what the live book says about the campaign's listed lots.
//
// The campaign plans and the Order Desk judges. This is the seam between them:
// it takes the lots the campaign believes are on the market and the orders the
// desk actually found, and reports what changed. It never decides anything it
// cannot see.
//
// The hard part is that a filled order and a cancelled order look identical in
// a snapshot: both are simply absent. So a lot whose order has vanished is
// reported as vanished, not as sold. Guessing "sold" there would release
// headroom for stock that may be sitting in a hangar, and a budget that hands
// back ISK nobody received is worse than no budget.

// FW lot reconciliation statuses.
const (
	// FWMatchResting: the book agrees with the lot. Nothing to do.
	FWMatchResting = "resting"
	// FWMatchPartialFill: some units sold, the order is still up.
	FWMatchPartialFill = "partial_fill"
	// FWMatchSoldOut: the lot's share of the book reached zero while other
	// orders for the same type are still visible, so the fill is not a guess.
	FWMatchSoldOut = "sold_out"
	// FWMatchGone: no live sell order for this type at this station at all.
	// Filled, cancelled or relisted elsewhere -- indistinguishable from here.
	FWMatchGone = "gone"
)

// FWOrderMatch is one listed lot read against the live book.
type FWOrderMatch struct {
	LotID         int64  `json:"lot_id"`
	TypeID        int32  `json:"type_id"`
	TypeName      string `json:"type_name"`
	DestStationID int64  `json:"dest_station_id"`

	Status string `json:"status"`

	// QtyRemaining is what the lot records now; SuggestedQtyRemaining is what
	// the book implies. They differ only when units left the book.
	QtyRemaining          int64 `json:"qty_remaining"`
	SuggestedQtyRemaining int64 `json:"suggested_qty_remaining"`
	FilledQty             int64 `json:"filled_qty"`

	// SuggestedState is non-empty only for a change this code is willing to
	// stand behind -- which is FWLotSold on a clean drain, and nothing else. A
	// vanished order suggests no state at all; the note says why.
	SuggestedState string `json:"suggested_state,omitempty"`

	// The desk rows this lot is resting behind. The campaign links to these
	// rather than re-deriving a verdict: the desk already judged them, with
	// range awareness, fees and percentiles the campaign does not carry.
	OrderIDs       []int64 `json:"order_ids,omitempty"`
	PrimaryOrderID int64   `json:"primary_order_id,omitempty"`

	Note string `json:"note,omitempty"`
}

// FWOrderReconciliation is the whole answer for one campaign.
type FWOrderReconciliation struct {
	Matches  []FWOrderMatch `json:"matches"`
	Warnings []string       `json:"warnings,omitempty"`
}

// fwBookKey groups a station's orders for one type. Matching is deliberately
// blind to who owns the order: a lot listed from the "wrong" character, or out
// of the corp wallet when the campaign named a character as seller, is the same
// stock on the same shelf. Requiring the declared seller to match would report
// that lot as vanished and then double-count the order as competition.
type fwBookKey struct {
	locationID int64
	typeID     int32
}

// ReconcileFWLotsWithOrders matches every `listed` lot against the live sell
// orders at its own destination station and reports what the book implies.
//
// orders is the desk's own-order set -- your characters and your corporation --
// so nothing here can mistake a competitor for your stock.
//
// Lots in any other state are skipped: `at_dest` is unlisted by definition and
// `planned` has not been bought. Where the book holds more units than the
// campaign's lots account for, that surplus is reported as a warning rather
// than netted off, because it is real stock somebody listed and pretending it
// away is how a cover figure starts lying.
func ReconcileFWLotsWithOrders(lots []FWLot, orders []OrderDeskOrder) FWOrderReconciliation {
	out := FWOrderReconciliation{Matches: []FWOrderMatch{}}

	book := make(map[fwBookKey][]OrderDeskOrder)
	for _, o := range orders {
		if o.IsBuyOrder || o.LocationID <= 0 || o.TypeID <= 0 {
			continue
		}
		key := fwBookKey{o.LocationID, o.TypeID}
		book[key] = append(book[key], o)
	}

	// Group the listed lots the same way, FIFO by lot id. Lot ids are handed
	// out in purchase order and the store returns them that way, so the oldest
	// lot is credited with a fill first -- the same convention the trade
	// journal's FIFO matching already uses for cost basis.
	grouped := make(map[fwBookKey][]FWLot)
	var order []fwBookKey
	for _, lot := range lots {
		if lot.State != FWLotListed {
			continue
		}
		key := fwBookKey{lot.DestStationID, lot.TypeID}
		if _, seen := grouped[key]; !seen {
			order = append(order, key)
		}
		grouped[key] = append(grouped[key], lot)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].locationID != order[j].locationID {
			return order[i].locationID < order[j].locationID
		}
		return order[i].typeID < order[j].typeID
	})

	for _, key := range order {
		group := grouped[key]
		sort.SliceStable(group, func(i, j int) bool { return group[i].LotID < group[j].LotID })

		live := book[key]
		sort.SliceStable(live, func(i, j int) bool {
			if live[i].Price != live[j].Price {
				return live[i].Price < live[j].Price
			}
			return live[i].OrderID < live[j].OrderID
		})

		var expected int64
		for _, lot := range group {
			if lot.QtyRemaining > 0 {
				expected += lot.QtyRemaining
			}
		}

		if len(live) == 0 {
			for _, lot := range group {
				out.Matches = append(out.Matches, FWOrderMatch{
					LotID:                 lot.LotID,
					TypeID:                lot.TypeID,
					TypeName:              lot.TypeName,
					DestStationID:         lot.DestStationID,
					Status:                FWMatchGone,
					QtyRemaining:          lot.QtyRemaining,
					SuggestedQtyRemaining: lot.QtyRemaining,
					Note: "no live sell order here -- sold out, cancelled or relisted; " +
						"confirm before marking sold or pulled",
				})
			}
			continue
		}

		var resting int64
		orderIDs := make([]int64, 0, len(live))
		for _, o := range live {
			if o.VolumeRemain > 0 {
				resting += int64(o.VolumeRemain)
			}
			orderIDs = append(orderIDs, o.OrderID)
		}

		filled := expected - resting
		if filled < 0 {
			out.Warnings = append(out.Warnings, fmt.Sprintf(
				"%s at station %d: %d units listed beyond this campaign's lots -- "+
					"stock listed outside the campaign, or a lot not yet marked listed",
				fwTypeLabel(group[0]), key.locationID, -filled))
			filled = 0
		}

		for _, lot := range group {
			share := int64(0)
			if filled > 0 && lot.QtyRemaining > 0 {
				share = lot.QtyRemaining
				if share > filled {
					share = filled
				}
				filled -= share
			}
			suggested := lot.QtyRemaining - share
			if suggested < 0 {
				suggested = 0
			}

			m := FWOrderMatch{
				LotID:                 lot.LotID,
				TypeID:                lot.TypeID,
				TypeName:              lot.TypeName,
				DestStationID:         lot.DestStationID,
				QtyRemaining:          lot.QtyRemaining,
				SuggestedQtyRemaining: suggested,
				FilledQty:             share,
				OrderIDs:              append([]int64(nil), orderIDs...),
				PrimaryOrderID:        fwPrimaryOrderID(lot, live),
			}
			switch {
			case share == 0:
				m.Status = FWMatchResting
			case suggested == 0:
				// The book still shows orders for this type here, so the drain
				// is a fill rather than a disappearance. This is the one
				// transition worth suggesting on its own.
				m.Status = FWMatchSoldOut
				m.SuggestedState = FWLotSold
			default:
				m.Status = FWMatchPartialFill
			}
			out.Matches = append(out.Matches, m)
		}
	}
	return out
}

// fwPrimaryOrderID picks the desk row a campaign line should link to.
//
// The declared holder's own order first, because that is the row whose fees and
// verdict belong to this lot; otherwise the cheapest resting order, which is
// the one at the front of the queue and so the one the desk's advice is about.
func fwPrimaryOrderID(lot FWLot, live []OrderDeskOrder) int64 {
	for _, o := range live {
		if lot.HolderOwnerID > 0 && o.CharacterID == lot.HolderOwnerID &&
			(lot.HolderOwnerKind == "" || o.OwnerKind == "" || o.OwnerKind == lot.HolderOwnerKind) {
			return o.OrderID
		}
	}
	if len(live) > 0 {
		return live[0].OrderID
	}
	return 0
}

// fwTypeLabel names a type for a message, falling back to the id when the SDE
// lookup that fills TypeName has not run.
func fwTypeLabel(lot FWLot) string {
	if lot.TypeName != "" {
		return lot.TypeName
	}
	return fmt.Sprintf("type %d", lot.TypeID)
}
