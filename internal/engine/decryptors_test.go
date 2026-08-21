package engine

import "testing"

// decryptorByKey is a small test helper — the exported Decryptors table is a
// slice, and the tests want to look decryptors up by their canonical key.
func decryptorByKey(t *testing.T, key string) Decryptor {
	t.Helper()
	for _, d := range Decryptors {
		if d.Key == key {
			return d
		}
	}
	t.Fatalf("decryptor %q not found", key)
	return Decryptor{}
}

// The bug this test suite pins down: the scanner used to treat every T2
// invention target as if a single invention success minted a 10-run BPC.
// That's true for T2 modules/ammo/drones but wrong for T2 ships (base 1)
// and T3 subsystems (base 3), and inflated modeled ship-invention profit
// by roughly the base-runs ratio (~10x for ships). EffectiveInventionParams
// now delegates to EffectiveInventionParamsForBase, which takes the SDE
// per-target base as an argument.

func TestEffectiveInventionParamsForBase_ShipBaseOneNoDecryptor(t *testing.T) {
	// T2 frigate / cruiser / BS / capital hulls all invent with a base of
	// 1 BPC run per success. With no decryptor the effective output must
	// remain 1 — anything else is the ~10x profit-inflation bug.
	d := decryptorByKey(t, "none")
	_, _, outputRuns, _, _ := d.EffectiveInventionParamsForBase(1)
	if outputRuns != 1 {
		t.Fatalf("ship + none: want outputRuns=1, got %d", outputRuns)
	}
}

func TestEffectiveInventionParamsForBase_ShipBaseOneWithAugmentation(t *testing.T) {
	// Augmentation adds +9 runs regardless of base tier. On a T2 ship
	// (base 1) that yields a 10-run BPC. Confirms decryptor bonuses stack
	// on the passed-in base, not on the hardcoded module constant.
	d := decryptorByKey(t, "augmentation")
	_, _, outputRuns, _, _ := d.EffectiveInventionParamsForBase(1)
	if outputRuns != 10 {
		t.Fatalf("ship + augmentation: want outputRuns=10 (1+9), got %d", outputRuns)
	}
}

func TestEffectiveInventionParamsForBase_ModuleBaseTenNoDecryptor(t *testing.T) {
	// T2 modules invent to 10-run BPCs by default. The refactor must not
	// break this — anyone still calling the no-arg variant depends on it.
	d := decryptorByKey(t, "none")
	_, _, outputRuns, _, _ := d.EffectiveInventionParamsForBase(10)
	if outputRuns != 10 {
		t.Fatalf("module + none: want outputRuns=10, got %d", outputRuns)
	}
}

func TestEffectiveInventionParamsForBase_ModuleBaseTenWithParity(t *testing.T) {
	// Parity is +3 runs. 10 + 3 = 13 for a T2 module BPC. Confirms the
	// stacking is straight addition and no clamping fires for common
	// module values.
	d := decryptorByKey(t, "parity")
	_, _, outputRuns, _, _ := d.EffectiveInventionParamsForBase(10)
	if outputRuns != 13 {
		t.Fatalf("module + parity: want outputRuns=13 (10+3), got %d", outputRuns)
	}
}

func TestEffectiveInventionParamsForBase_ZeroBaseFallsBackToModuleConstant(t *testing.T) {
	// Callers that don't know the target (older scan rows persisted before
	// the field existed, unit tests, SDE data gaps) pass 0. The helper
	// must fall back to T2BPCBaseRuns so behaviour stays identical to the
	// pre-fix code path — no silent regressions in legacy call sites.
	d := decryptorByKey(t, "none")
	_, _, outputRuns, _, _ := d.EffectiveInventionParamsForBase(0)
	if outputRuns != int32(T2BPCBaseRuns) {
		t.Fatalf("base=0 fallback: want %d, got %d", T2BPCBaseRuns, outputRuns)
	}
}

func TestEffectiveInventionParams_LegacyShimUsesModuleBase(t *testing.T) {
	// EffectiveInventionParams (no-arg) is preserved as a shim for callers
	// that legitimately want the module default. It must return the same
	// numbers the pre-refactor code did — otherwise every caller that
	// wasn't updated gets a silent behaviour change.
	d := decryptorByKey(t, "attainment") // +4 runs
	meArg, teArg, runsArg, chanceArg, costArg := d.EffectiveInventionParamsForBase(int32(T2BPCBaseRuns))
	meShim, teShim, runsShim, chanceShim, costShim := d.EffectiveInventionParams()
	if meArg != meShim || teArg != teShim || runsArg != runsShim || chanceArg != chanceShim || costArg != costShim {
		t.Fatalf("shim divergence: forBase=(me=%d,te=%d,runs=%d,chance=%v,cost=%v) noArg=(me=%d,te=%d,runs=%d,chance=%v,cost=%v)",
			meArg, teArg, runsArg, chanceArg, costArg,
			meShim, teShim, runsShim, chanceShim, costShim,
		)
	}
	// Sanity-check the actual value too so a bug that changed both call
	// sites in lockstep doesn't slip through.
	if runsShim != int32(T2BPCBaseRuns)+4 {
		t.Fatalf("attainment shim: want %d, got %d", T2BPCBaseRuns+4, runsShim)
	}
}

func TestEffectiveInventionParamsForBase_ClampsToAtLeastOne(t *testing.T) {
	// Augmentation on a "None" decryptor gives +9. On a T2 subsystem base
	// of 3 that's 12. But if someone ever adds a decryptor with a strongly
	// negative OutputRunsBonus (or an SDE data gap gives us a base of 1
	// with a hypothetical -9 bonus), the helper must floor to 1 — a BPC
	// with 0 runs would divide by zero in the scanner's amortization math.
	d := Decryptor{Key: "test", OutputRunsBonus: -100, ProbMult: 1}
	_, _, outputRuns, _, _ := d.EffectiveInventionParamsForBase(1)
	if outputRuns != 1 {
		t.Fatalf("underflow clamp: want 1, got %d", outputRuns)
	}
}
