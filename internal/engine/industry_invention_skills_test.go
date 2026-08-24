package engine

import (
	"math"
	"testing"
)

// TestInventionSkillMultiplier locks down the canonical EVE formula:
//
//	mult = (1 + encryption/40) × (1 + (datacore1 + datacore2)/30)
//
// All-L5 is the reference case: 1.125 × 1.333... ≈ 1.5.
func TestInventionSkillMultiplier(t *testing.T) {
	cases := []struct {
		name    string
		enc     int32
		dc1     int32
		dc2     int32
		want    float64
		wantTol float64
	}{
		{"all zero (identity)", 0, 0, 0, 1.0, 1e-12},
		{"all L5 (canonical max)", 5, 5, 5, 1.5, 1e-9},
		{"encryption only L5", 5, 0, 0, 1.125, 1e-12},
		{"datacores only L5+L5", 0, 5, 5, 1.0 + 10.0/30.0, 1e-12},
		{"L1 across the board", 1, 1, 1, (1 + 1.0/40) * (1 + 2.0/30), 1e-12},
		{"negative clamped to 0", -3, 0, 0, 1.0, 1e-12},
		{"over-cap clamped to 5", 9, 9, 9, 1.5, 1e-9},
	}
	for _, c := range cases {
		got := InventionSkillMultiplier(c.enc, c.dc1, c.dc2)
		if math.Abs(got-c.want) > c.wantTol {
			t.Errorf("%s: got %g, want %g (±%g)", c.name, got, c.want, c.wantTol)
		}
	}
}

// TestCalculateInventionStep_AppliesSkillMultiplier verifies the engine
// applies the skill multiplier to the SDE base probability when skill
// levels are provided AND no absolute InventionChance override is set.
func TestCalculateInventionStep_AppliesSkillMultiplier(t *testing.T) {
	a := newAlignmentAnalyzer(t)

	base := scannerLikeReq{
		runsPerJob:      10,
		outputBPCRuns:   10,
		baseProbability: 0.30, // typical T2 module base 30%
		facilityTax:     0.25,
		structureBonus:  1.0,
		brokerFee:       3.0,
		salesTaxPercent: 4.5,
	}

	// Analyze path with all-zero skills: identity multiplier.
	unskilled := analyzePathParams(a, 4001, 30000142, base)
	rUn, err := a.Analyze(unskilled, func(string) {})
	if err != nil {
		t.Fatalf("unskilled analyze: %v", err)
	}

	// Same call, but with all-L5 invention skills. Chance should scale to
	// 1.5× base; expected-attempts should halve (relative to 1.0 → 1.5),
	// which cuts both invention material cost AND invention job cost by
	// the ratio 1.0/1.5. Net effect: invention step cost lower for the
	// skilled inventor.
	skilled := unskilled
	skilled.InventionEncryptionLevel = 5
	skilled.InventionDatacoreLevel1 = 5
	skilled.InventionDatacoreLevel2 = 5
	rSk, err := a.Analyze(skilled, func(string) {})
	if err != nil {
		t.Fatalf("skilled analyze: %v", err)
	}

	if rSk.InventionCost >= rUn.InventionCost {
		t.Errorf("skilled invention cost %g should be less than unskilled %g",
			rSk.InventionCost, rUn.InventionCost)
	}
	// Verify the ratio: skilled invention cost should be ≈ unskilled / 1.5.
	// Allow a wide tolerance because JobCost / DecryptorCost / material cost
	// composition slightly perturbs the ratio, but the invention material
	// and job cost together scale with expectedAttempts which is 1/chance.
	if rUn.InventionCost > 0 {
		ratio := rSk.InventionCost / rUn.InventionCost
		if ratio < 0.6 || ratio > 0.8 {
			t.Errorf("skilled/unskilled invention cost ratio %g outside expected ~0.667 (1/1.5)",
				ratio)
		}
	}
}

// TestCalculateInventionStep_AbsoluteOverrideSkipsSkillMult verifies that
// when the caller passes InventionChance (an absolute value), the engine
// does NOT double-apply the skill multiplier — the caller is expected to
// have already folded skills into the absolute chance.
func TestCalculateInventionStep_AbsoluteOverrideSkipsSkillMult(t *testing.T) {
	a := newAlignmentAnalyzer(t)

	base := scannerLikeReq{
		runsPerJob:      10,
		outputBPCRuns:   10,
		baseProbability: 0.30,
		facilityTax:     0.25,
		structureBonus:  1.0,
		brokerFee:       3.0,
		salesTaxPercent: 4.5,
	}
	// Scanner path sends absolute chance already skill-folded.
	absoluteParams := scannerPathParams(a, 4001, 30000142, base)
	// Attach skill levels — should be a no-op because InventionChance is set.
	absoluteParams.InventionEncryptionLevel = 5
	absoluteParams.InventionDatacoreLevel1 = 5
	absoluteParams.InventionDatacoreLevel2 = 5
	rWithSkills, err := a.Analyze(absoluteParams, func(string) {})
	if err != nil {
		t.Fatalf("with skills: %v", err)
	}
	// Same call without skill fields.
	absoluteParams.InventionEncryptionLevel = 0
	absoluteParams.InventionDatacoreLevel1 = 0
	absoluteParams.InventionDatacoreLevel2 = 0
	rNoSkills, err := a.Analyze(absoluteParams, func(string) {})
	if err != nil {
		t.Fatalf("no skills: %v", err)
	}
	if math.Abs(rWithSkills.InventionCost-rNoSkills.InventionCost) > 1e-6 {
		t.Errorf("absolute-override path should ignore skill fields: with=%g no=%g",
			rWithSkills.InventionCost, rNoSkills.InventionCost)
	}
}
