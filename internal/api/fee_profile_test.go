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
		if got := suggestedSalesTax(level); !isSalesTaxSnapshot(got) {
			t.Errorf("sales tax %.4f (Accounting %d) not recognized as a snapshot", got, level)
		}
		if got := suggestedBrokerFee(level); !isSkillSnapshot(got, suggestedBrokerFee) {
			t.Errorf("broker fee %.4f (Broker Relations %d) not recognized as a snapshot", got, level)
		}
	}
}

// The trap this fix set for itself.
//
// Snapshot detection asks "could our formula have produced this?", so moving
// the base from 8.0 to 7.5 silently reclassified every 3.60 and 4.48 already
// sitting in a config — values this very resolver wrote a release earlier — as
// rates the user typed on purpose. They would then outrank the character's
// skills forever: the frozen-at-8%-with-Accounting-V bug, restored by its own
// fix. salesTaxBases keeps the retired bases recognisable.
func TestLegacyBaseSalesTaxRatesAreStillSnapshots(t *testing.T) {
	for _, legacy := range []struct {
		value float64
		note  string
	}{
		{8.0, "Accounting 0 at the old 8% base"},
		{7.12, "Accounting I at the old 8% base"},
		{4.48, "Accounting IV at the old 8% base"},
		{3.6, "Accounting V at the old 8% base — what v1.10.2 wrote"},
	} {
		if !isSalesTaxSnapshot(legacy.value) {
			t.Errorf("%.2f (%s) must stay recognisable as our own snapshot, not become a user choice",
				legacy.value, legacy.note)
		}
	}
}

func TestIsSkillSnapshotRejectsTypedRates(t *testing.T) {
	// A citadel broker fee and a rounded sales tax: numbers a person picked,
	// none of which the formula can produce.
	for _, v := range []float64{2.5, 1.0, 0.0, 4.5, 3.5, 5.0, 2.0} {
		if isSkillSnapshot(v, suggestedBrokerFee) && isSalesTaxSnapshot(v) {
			t.Errorf("%.2f treated as a skill snapshot on both scales", v)
		}
	}
	if isSkillSnapshot(2.5, suggestedBrokerFee) {
		t.Error("2.5% broker fee (a citadel rate) must survive as a deliberate choice")
	}
	// Widening detection across two bases must not swallow typed rates: none of
	// these is an output of either base at any level.
	if isSalesTaxSnapshot(4.5) {
		t.Error("4.5% sales tax is not a formula output and must survive")
	}
	if isSalesTaxSnapshot(2.5) {
		t.Error("2.5% sales tax is not a formula output and must survive")
	}
}

func TestConfigFeeRateAuthority(t *testing.T) {
	cases := []struct {
		name string
		rate configFeeRate
		want bool
	}{
		{"unset falls back", configFeeRate{Value: baseSalesTaxPercent}, false},
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
// recompute it — to 3.375, the rate the wallet journal shows CCP actually
// charging.
func TestSnapshotRatesRecomputeFromSkills(t *testing.T) {
	stored := configFeeRate{Value: 8.0, Set: true}
	stored.Snapshot = isSalesTaxSnapshot(stored.Value)
	if stored.authoritative() {
		t.Fatal("a stored 8.00 is the Accounting 0 formula output, not a choice")
	}
	if got := suggestedSalesTax(5); !floatEq(got, 3.375, 1e-9) {
		t.Errorf("Accounting V sales tax = %.4f, want 3.375", got)
	}
	// The other identities in the wild: Accounting IV at either base.
	for _, v := range []float64{4.48, 4.2} {
		frozen := configFeeRate{Value: v, Set: true}
		frozen.Snapshot = isSalesTaxSnapshot(frozen.Value)
		if frozen.authoritative() {
			t.Errorf("%.2f is an Accounting IV formula output and must not outrank Accounting V", v)
		}
	}
}
