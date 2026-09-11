package api

import "testing"

// The fee rates the app charges have to track the skills the character
// actually has. They did not: rates were snapshotted into config by the
// "import from ESI" button and then treated as gospel, so training Accounting
// changed nothing and an import taken while the skill sheet read empty pinned
// the untrained 8% / 3% in place. isSkillSnapshot is what tells a stale
// snapshot apart from a rate the user typed on purpose.

func TestIsSkillSnapshotRecognizesEveryFormulaOutput(t *testing.T) {
	for level := 0; level <= 5; level++ {
		if got := suggestedSalesTax(level); !isSkillSnapshot(got, suggestedSalesTax) {
			t.Errorf("sales tax %.4f (Accounting %d) not recognized as a snapshot", got, level)
		}
		if got := suggestedBrokerFee(level); !isSkillSnapshot(got, suggestedBrokerFee) {
			t.Errorf("broker fee %.4f (Broker Relations %d) not recognized as a snapshot", got, level)
		}
	}
}

func TestIsSkillSnapshotRejectsTypedRates(t *testing.T) {
	// A citadel broker fee and a rounded sales tax: numbers a person picked,
	// none of which the formula can produce.
	for _, v := range []float64{2.5, 1.0, 0.0, 4.5, 3.5, 5.0, 2.0} {
		if isSkillSnapshot(v, suggestedBrokerFee) && isSkillSnapshot(v, suggestedSalesTax) {
			t.Errorf("%.2f treated as a skill snapshot on both scales", v)
		}
	}
	if isSkillSnapshot(2.5, suggestedBrokerFee) {
		t.Error("2.5% broker fee (a citadel rate) must survive as a deliberate choice")
	}
	if isSkillSnapshot(4.5, suggestedSalesTax) {
		t.Error("4.5% sales tax is not a formula output and must survive")
	}
}

func TestConfigFeeRateAuthority(t *testing.T) {
	cases := []struct {
		name string
		rate configFeeRate
		want bool
	}{
		{"unset falls back", configFeeRate{Value: 8.0}, false},
		{"stale import yields to skills", configFeeRate{Value: 4.48, Set: true, Snapshot: true}, false},
		{"typed citadel rate wins", configFeeRate{Value: 2.5, Set: true}, true},
	}
	for _, tc := range cases {
		if got := tc.rate.authoritative(); got != tc.want {
			t.Errorf("%s: authoritative() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The reported bug: Accounting V shown as 8%. The stored 8.00 is exactly what
// the formula produces for Accounting 0, so it is a snapshot and skills
// recompute it to 3.60.
func TestSnapshotRatesRecomputeFromSkills(t *testing.T) {
	stored := configFeeRate{Value: 8.0, Set: true}
	stored.Snapshot = isSkillSnapshot(stored.Value, suggestedSalesTax)
	if stored.authoritative() {
		t.Fatal("a stored 8.00 is the Accounting 0 formula output, not a choice")
	}
	if got := suggestedSalesTax(5); !floatEq(got, 3.6, 1e-9) {
		t.Errorf("Accounting V sales tax = %.4f, want 3.60", got)
	}
	// The other identity in the wild: 4.48 / 1.80, frozen at Accounting IV.
	frozen := configFeeRate{Value: 4.48, Set: true}
	frozen.Snapshot = isSkillSnapshot(frozen.Value, suggestedSalesTax)
	if frozen.authoritative() {
		t.Error("4.48 is the Accounting IV formula output and must not outrank Accounting V")
	}
}
