package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"eve-flipper/internal/db"
)

// saveHoldingRuleJSON drives the PUT handler the way the router does, since
// the type id arrives as a path value rather than in the body.
func saveHoldingRuleJSON(t *testing.T, s *Server, userID string, typeID string, body string) {
	t.Helper()
	req := requestWithUserID("PUT", "/api/auth/holding-rules/"+typeID, strings.NewReader(body), userID)
	req.SetPathValue("typeID", typeID)
	rec := httptest.NewRecorder()
	s.handleAuthHoldingRuleSave(rec, req)
	if rec.Code != 200 {
		t.Fatalf("save %s: status %d, body %s", body, rec.Code, rec.Body.String())
	}
}

// Two editors write these rules now — Positions sets the target and the
// reserve, the Orders drawer sets the bid ceiling and the patient flag — and
// neither sends the fields it does not show. A full replace would therefore
// have each save silently wipe the other's work.
func TestHoldingRuleSaveLeavesUnmentionedFieldsAlone(t *testing.T) {
	database := openAPITestDB(t)
	s := &Server{db: database}
	const userID = "user-1"

	// Positions: a target and a reserve.
	saveHoldingRuleJSON(t, s, userID, "34", `{"target_price":620,"target_percentile":75,"target_basis":"percentile","reserved_qty":12,"note":"flying two"}`)

	// Orders drawer: a ceiling and the patient flag, and nothing else.
	saveHoldingRuleJSON(t, s, userID, "34", `{"max_bid_price":120,"patient_bid":true}`)

	got := s.holdingRulesFor(userID)[34]
	if got.TargetPrice != 620 || got.TargetPercentile != 75 || got.TargetBasis != "percentile" {
		t.Fatalf("target lost: %+v", got)
	}
	if got.ReservedQty != 12 || got.Note != "flying two" {
		t.Fatalf("reserve lost: %+v", got)
	}
	if got.MaxBidPrice != 120 || !got.PatientBid {
		t.Fatalf("ceiling not stored: %+v", got)
	}

	// And the reverse direction: Positions saving again must not drop them.
	saveHoldingRuleJSON(t, s, userID, "34", `{"target_price":700,"target_percentile":0,"target_basis":"manual","reserved_qty":12,"note":"flying two"}`)
	got = s.holdingRulesFor(userID)[34]
	if got.MaxBidPrice != 120 || !got.PatientBid {
		t.Fatalf("ceiling wiped by a Positions save: %+v", got)
	}
	if got.TargetPrice != 700 {
		t.Fatalf("target = %v, want the new 700", got.TargetPrice)
	}
}

// An explicit zero is still a clear — that is the whole reason the body uses
// pointers rather than treating any zero as absent.
func TestHoldingRuleSaveClearsOnAnExplicitZero(t *testing.T) {
	database := openAPITestDB(t)
	s := &Server{db: database}
	const userID = "user-1"

	if err := database.SetHoldingRule(userID, db.HoldingRule{
		TypeID: 34, TargetPrice: 620, MaxBidPrice: 120, PatientBid: true, ReservedQty: 3,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	saveHoldingRuleJSON(t, s, userID, "34", `{"max_bid_price":0,"patient_bid":false}`)

	got := s.holdingRulesFor(userID)[34]
	if got.MaxBidPrice != 0 || got.PatientBid {
		t.Fatalf("explicit zero did not clear: %+v", got)
	}
	if got.TargetPrice != 620 || got.ReservedQty != 3 {
		t.Fatalf("clearing one field disturbed the others: %+v", got)
	}
}

// Clearing everything a rule constrains still deletes the row, which is what
// stops a cleared form leaving an inert rule behind.
func TestHoldingRuleSaveDeletesWhenTheMergeIsEmpty(t *testing.T) {
	database := openAPITestDB(t)
	s := &Server{db: database}
	const userID = "user-1"

	if err := database.SetHoldingRule(userID, db.HoldingRule{TypeID: 34, MaxBidPrice: 120}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	saveHoldingRuleJSON(t, s, userID, "34", `{"max_bid_price":0}`)

	if _, ok := s.holdingRulesFor(userID)[34]; ok {
		t.Fatalf("rule survived being emptied: %+v", s.holdingRulesFor(userID)[34])
	}
}
