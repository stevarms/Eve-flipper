package db

// holding_rules_test.go -- the rules are the only place in the app where the
// user overrules the market data, so a rule that is silently dropped or
// silently altered is worse than no rule at all.

import (
	"math"
	"testing"
)

const holdingRuleTestUser = "u-holding"

func TestSetHoldingRuleKeepsACeilingOnlyRule(t *testing.T) {
	// IsEmpty decides whether a rule is stored or deleted. When the buy-side
	// fields were added it had to learn about them: a rule carrying only a
	// bid ceiling constrains something, and the version that only knew about
	// target price and reserved quantity would have deleted it on save.
	d := openTestDB(t)
	defer d.Close()

	if err := d.SetHoldingRule(holdingRuleTestUser, HoldingRule{
		TypeID: 34, MaxBidPrice: 120.5,
	}); err != nil {
		t.Fatalf("SetHoldingRule: %v", err)
	}

	got, ok := d.GetHoldingRules(holdingRuleTestUser)[34]
	if !ok {
		t.Fatalf("a ceiling-only rule was not stored")
	}
	if got.MaxBidPrice != 120.5 {
		t.Fatalf("max_bid_price = %v, want 120.5", got.MaxBidPrice)
	}
	if got.TargetPrice != 0 || got.ReservedQty != 0 {
		t.Fatalf("unset fields came back populated: %+v", got)
	}
}

func TestSetHoldingRuleKeepsAPatientBidOnlyRule(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	if err := d.SetHoldingRule(holdingRuleTestUser, HoldingRule{
		TypeID: 35, PatientBid: true,
	}); err != nil {
		t.Fatalf("SetHoldingRule: %v", err)
	}

	got, ok := d.GetHoldingRules(holdingRuleTestUser)[35]
	if !ok {
		t.Fatalf("a flag-only rule was not stored")
	}
	if !got.PatientBid {
		t.Fatalf("patient_bid = false after a round trip")
	}
}

func TestSetHoldingRuleSanitisesTheCeiling(t *testing.T) {
	// These arrive from a form. A NaN is a slip, not an attack -- but it must
	// not reach the engine, where every comparison against it is false and a
	// ceiling would silently stop applying.
	d := openTestDB(t)
	defer d.Close()

	for name, bad := range map[string]float64{
		"nan":      math.NaN(),
		"inf":      math.Inf(1),
		"negative": -50,
	} {
		t.Run(name, func(t *testing.T) {
			if err := d.SetHoldingRule(holdingRuleTestUser, HoldingRule{
				TypeID: 36, TargetPrice: 500, MaxBidPrice: bad,
			}); err != nil {
				t.Fatalf("SetHoldingRule: %v", err)
			}
			got := d.GetHoldingRules(holdingRuleTestUser)[36]
			if got.MaxBidPrice != 0 {
				t.Fatalf("max_bid_price = %v, want 0", got.MaxBidPrice)
			}
			if got.TargetPrice != 500 {
				t.Fatalf("target_price = %v, want the good field kept at 500", got.TargetPrice)
			}
		})
	}
}

func TestSetHoldingRuleDeletesAnEmptyRule(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	if err := d.SetHoldingRule(holdingRuleTestUser, HoldingRule{
		TypeID: 37, TargetPrice: 500, MaxBidPrice: 100, PatientBid: true,
	}); err != nil {
		t.Fatalf("SetHoldingRule: %v", err)
	}
	if _, ok := d.GetHoldingRules(holdingRuleTestUser)[37]; !ok {
		t.Fatalf("rule was not stored")
	}

	// Clearing every field in the form has to remove the row rather than
	// leave an inert one behind.
	if err := d.SetHoldingRule(holdingRuleTestUser, HoldingRule{TypeID: 37}); err != nil {
		t.Fatalf("SetHoldingRule (cleared): %v", err)
	}
	if _, ok := d.GetHoldingRules(holdingRuleTestUser)[37]; ok {
		t.Fatalf("a cleared rule was left in place")
	}
}
