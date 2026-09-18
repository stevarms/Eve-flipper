package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"eve-flipper/internal/db"
	"eve-flipper/internal/engine"
)

// fw_orders.go -- the campaign side of "the campaign plans, the Order Desk
// judges".
//
// The desk already knows how to judge an order: range-aware buy competition,
// per-character fees, percentile context, a verdict. None of that is repeated
// here. What this file does is answer the one question the desk cannot, because
// the desk has never heard of a campaign: which of these live orders is the
// stock I shipped, and what has happened to it.
//
// The read and the write are separate endpoints on purpose.
//
// GET reports. It is a snapshot of the book against the lots, and it changes
// nothing, so it can be polled and it can be wrong without costing anything.
//
// POST applies, and only ever the unambiguous part. A partial fill is not a
// guess: the order is still on the market and the exact remaining volume is
// visible, so the arithmetic is observation. A *vanished* order is a guess --
// filled and cancelled are indistinguishable from a snapshot -- and this code
// will not make it. Advancing a vanished lot to `sold` would hand back budget
// headroom for stock that may be sitting in a hangar, and a budget that
// releases ISK nobody received is worse than no budget at all.

// fwOrdersResponse is the reconciliation plus the context needed to act on it.
type fwOrdersResponse struct {
	CampaignID    int64 `json:"campaign_id"`
	DestStationID int64 `json:"dest_station_id"`

	Reconciliation engine.FWOrderReconciliation `json:"reconciliation"`

	// Budget as it stands now. Deliberately not "as it would stand if you
	// accepted every suggestion": headroom that moves before the user agrees is
	// how a budget stops meaning anything.
	Budget engine.FWBudget `json:"budget"`

	// DeskOrders are the desk's own rows for the orders the matches link to, and
	// only those. The campaign renders the desk's verdict from these rather than
	// deriving a second opinion -- which is the whole point of the seam, and why
	// the rows travel with the matches instead of the UI being told to go and
	// cross-reference the Orders tab by id.
	DeskOrders []engine.OrderDeskOrder `json:"desk_orders,omitempty"`

	// Counts is the one-line summary: how many lots are resting, filling, drained
	// or unaccounted for.
	Counts map[string]int `json:"counts"`

	// Warnings carries the desk's own (an unreadable corp book, a character whose
	// token failed) alongside the reconciliation's. They are merged because from
	// the campaign's point of view they are the same kind of fact: something the
	// tool could not see, which must never render as "nothing there".
	Warnings []string `json:"warnings,omitempty"`
}

// fwReconcileCampaign builds the desk and reads the campaign's lots against it.
//
// Scope is every authenticated character plus every reachable corporation, not
// the campaign's declared seller. A lot listed from the "wrong" character is the
// same stock on the same shelf, and narrowing to the declared seller would
// report it vanished and then read its order as competition -- the double fault
// the owner-blind matching in the engine exists to prevent.
func (s *Server) fwReconcileCampaign(
	ctx context.Context,
	userID string,
	campaign *db.FWCampaign,
) (fwOrdersResponse, error) {
	lots, err := s.db.GetFWLots(userID, campaign.CampaignID)
	if err != nil {
		return fwOrdersResponse{}, err
	}

	opt := orderDeskBuildOptions{
		SalesTaxPercent:    8.0,
		BrokerFeePercent:   1.0,
		TargetETADays:      3.0,
		MinMarginPercent:   campaign.MinMarginPct,
		LowballDiscountPct: 20.0,
		RepriceJumpPct:     25.0,
	}
	if opt.MinMarginPercent <= 0 {
		opt.MinMarginPercent = engine.DefaultMinMarginPct
	}
	if cfg := s.loadConfigForUser(userID); cfg != nil {
		if cfg.SalesTaxPercent > 0 {
			opt.SalesTaxPercent = cfg.SalesTaxPercent
		}
		if cfg.BrokerFeePercent > 0 {
			opt.BrokerFeePercent = cfg.BrokerFeePercent
		}
	}

	desk, err := s.buildOrderDesk(ctx, userID, 0, true, opt)
	if err != nil {
		return fwOrdersResponse{}, err
	}

	rec := engine.ReconcileFWLotsWithOrders(lots, desk.Orders)

	out := fwOrdersResponse{
		CampaignID:     campaign.CampaignID,
		DestStationID:  campaign.DestStationID,
		Reconciliation: rec,
		Budget:         engine.MeasureFWBudget(campaign.BudgetISK, lots),
		Counts:         map[string]int{},
		DeskOrders:     fwLinkedDeskOrders(rec, desk.Orders),
	}
	for _, m := range rec.Matches {
		out.Counts[m.Status]++
	}
	out.Warnings = append(out.Warnings, desk.Warnings...)
	out.Warnings = append(out.Warnings, rec.Warnings...)
	return out, nil
}

// fwLinkedDeskOrders returns the desk rows the matches point at, deduplicated
// and in a stable order.
//
// Only the linked rows travel. The desk can be hundreds of orders across every
// station the user trades at, and the campaign has no business rendering the
// ones that are not its stock.
func fwLinkedDeskOrders(rec engine.FWOrderReconciliation, all []engine.OrderDeskOrder) []engine.OrderDeskOrder {
	wanted := make(map[int64]bool)
	for _, m := range rec.Matches {
		for _, id := range m.OrderIDs {
			wanted[id] = true
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	out := make([]engine.OrderDeskOrder, 0, len(wanted))
	for _, o := range all {
		if wanted[o.OrderID] {
			out = append(out, o)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].OrderID < out[j].OrderID })
	return out
}

// handleFWCampaignOrders reports the campaign's lots against the live book.
// GET /api/auth/fw/campaigns/{id}/orders
func (s *Server) handleFWCampaignOrders(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	campaign, ok := s.requireFWCampaign(w, r, userID)
	if !ok {
		return
	}
	out, err := s.fwReconcileCampaign(r.Context(), userID, campaign)
	if err != nil {
		writeStatusError(w, err)
		return
	}
	writeJSON(w, out)
}

// fwApplyResult is what an apply actually changed, lot by lot.
type fwApplyResult struct {
	LotID    int64  `json:"lot_id"`
	TypeID   int32  `json:"type_id"`
	TypeName string `json:"type_name,omitempty"`

	FromQtyRemaining int64  `json:"from_qty_remaining"`
	ToQtyRemaining   int64  `json:"to_qty_remaining"`
	FromState        string `json:"from_state"`
	ToState          string `json:"to_state"`
}

// fwApplyResponse reports the applied changes and the campaign afterwards.
type fwApplyResponse struct {
	Applied []fwApplyResult `json:"applied"`

	// Skipped names every match this endpoint deliberately refused, with the
	// reason. A vanished lot lands here, and it is the most important part of the
	// response: an apply that silently ignored it would look like an apply that
	// found nothing wrong.
	Skipped []fwApplySkip `json:"skipped,omitempty"`

	Budget engine.FWBudget `json:"budget"`
	Lots   []engine.FWLot  `json:"lots"`

	Warnings []string `json:"warnings,omitempty"`
}

type fwApplySkip struct {
	LotID  int64  `json:"lot_id"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// fwApplyVerdict is what an apply may do with one reconciliation match.
type fwApplyVerdict int

const (
	// fwApplyWrite: the book proves this change; write it.
	fwApplyWrite fwApplyVerdict = iota
	// fwApplyNoChange: nothing to write and nothing to report.
	fwApplyNoChange
	// fwApplyRefuse: this endpoint will not guess; report it with the reason.
	fwApplyRefuse
)

// fwApplyDecision decides what an apply may do with one match, and is the whole
// rule of this endpoint in one place.
//
// The line is visibility, not confidence. A partial_fill and a sold_out are
// written because the order is still on the market: the remaining volume was
// read off the book, so the arithmetic is observation. A gone lot is refused
// because a filled order and a cancelled order are the same absence in a
// snapshot, and advancing it to sold would release budget headroom for stock
// that may be sitting in a hangar. A budget that hands back ISK nobody received
// is worse than no budget at all, so the refusal is reported rather than
// silently passed over -- an apply that ignored a vanished lot would look like
// an apply that found nothing wrong.
//
// An unrecognised status is refused for the same reason: a status this code has
// not been taught is not a status it may act on.
func fwApplyDecision(m engine.FWOrderMatch, lot engine.FWLot) (fwApplyVerdict, string) {
	switch m.Status {
	case engine.FWMatchResting:
		return fwApplyNoChange, ""
	case engine.FWMatchGone:
		return fwApplyRefuse, "no live order to read: filled and cancelled look " +
			"identical from here, so mark this lot sold or pulled yourself"
	case engine.FWMatchPartialFill, engine.FWMatchSoldOut:
		// Both are backed by a visible order.
	default:
		return fwApplyRefuse, "unrecognised match status"
	}
	if m.SuggestedQtyRemaining == lot.QtyRemaining && m.SuggestedState == "" {
		return fwApplyNoChange, ""
	}
	return fwApplyWrite, ""
}

// handleFWCampaignOrdersApply writes the fills the live book can prove.
// POST /api/auth/fw/campaigns/{id}/orders/apply
//
// Only `partial_fill` and `sold_out` are applied, and both only because the
// order is still visible: the remaining volume is read, not inferred. `gone` is
// always skipped with its reason, because it is exactly the case where the tool
// cannot tell a sale from a cancellation.
//
// Optional body {"lot_ids": [...]} narrows the apply to specific lots, so the
// user can accept one row without accepting the rest. An absent or empty list
// means every applicable match.
func (s *Server) handleFWCampaignOrdersApply(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	campaign, ok := s.requireFWCampaign(w, r, userID)
	if !ok {
		return
	}

	var body struct {
		LotIDs []int64 `json:"lot_ids"`
	}
	// An empty body is the common case (apply everything), and a request with no
	// body at all must not read as a malformed one.
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}
	only := make(map[int64]bool, len(body.LotIDs))
	for _, id := range body.LotIDs {
		only[id] = true
	}

	recon, err := s.fwReconcileCampaign(r.Context(), userID, campaign)
	if err != nil {
		writeStatusError(w, err)
		return
	}
	lots, err := s.db.GetFWLots(userID, campaign.CampaignID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "fw lots: "+err.Error())
		return
	}
	byID := make(map[int64]engine.FWLot, len(lots))
	for _, lot := range lots {
		byID[lot.LotID] = lot
	}

	out := fwApplyResponse{Applied: []fwApplyResult{}, Warnings: recon.Warnings}
	for _, m := range recon.Reconciliation.Matches {
		if len(only) > 0 && !only[m.LotID] {
			continue
		}
		lot, found := byID[m.LotID]
		if !found {
			continue
		}
		verdict, reason := fwApplyDecision(m, lot)
		switch verdict {
		case fwApplyNoChange:
			continue
		case fwApplyRefuse:
			out.Skipped = append(out.Skipped, fwApplySkip{
				LotID: m.LotID, Status: m.Status, Reason: reason,
			})
			continue
		}

		applied := fwApplyResult{
			LotID:            lot.LotID,
			TypeID:           lot.TypeID,
			TypeName:         lot.TypeName,
			FromQtyRemaining: lot.QtyRemaining,
			ToQtyRemaining:   m.SuggestedQtyRemaining,
			FromState:        lot.State,
			ToState:          lot.State,
		}
		lot.QtyRemaining = m.SuggestedQtyRemaining
		if m.SuggestedState != "" {
			lot.State = m.SuggestedState
			applied.ToState = lot.State
		}
		if _, err := s.db.SaveFWLot(userID, campaign.CampaignID, &lot); err != nil {
			// One bad lot must not discard the fills already written, so this is
			// reported and the loop continues.
			out.Skipped = append(out.Skipped, fwApplySkip{
				LotID: m.LotID, Status: m.Status, Reason: "could not save: " + err.Error(),
			})
			continue
		}
		out.Applied = append(out.Applied, applied)
	}

	after, err := s.db.GetFWLots(userID, campaign.CampaignID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "fw lots: "+err.Error())
		return
	}
	if after == nil {
		after = []engine.FWLot{}
	}
	out.Lots = after
	out.Budget = engine.MeasureFWBudget(campaign.BudgetISK, after)
	writeJSON(w, out)
}

// fwMatchSummary renders a reconciliation as one line per status, for a log or a
// narrow UI strip. Sorted so the same reconciliation always reads the same way.
func fwMatchSummary(counts map[string]int) string {
	if len(counts) == 0 {
		return "no listed lots"
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
	}
	return strings.Join(parts, ", ")
}
