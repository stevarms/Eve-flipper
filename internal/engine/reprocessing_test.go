package engine

import (
	"math"
	"testing"

	"eve-flipper/internal/sde"
)

func almostEq(a, b, tol float64) bool { return math.Abs(a-b) < tol }

// TestReprocessingYieldMultiplier locks the EVE canonical formula:
//
//	yield = base × (1 + 0.03×Repro) × (1 + 0.02×Eff) × (1 + 0.02×Spec)
//	      × (1 + implant%/100) × (1 - tax%/100)
func TestReprocessingYieldMultiplier(t *testing.T) {
	cases := []struct {
		name        string
		base        float64
		repro       int32
		eff         int32
		spec        int32
		implantPct  float64
		taxPct      float64
		want        float64
	}{
		{"defaults (NPC, no skills, no tax, no implant)", 0, 0, 0, 0, 0, 0, 0.50},
		{"all zero, base=0 fallback to 0.50", 0, 0, 0, 0, 0, 0, 0.50},
		{"max skills, no implant, no tax", 0.50, 5, 5, 5, 0, 0, 0.50 * 1.15 * 1.10 * 1.10},
		{"max skills + 4% implant, no tax", 0.50, 5, 5, 5, 4, 0, 0.50 * 1.15 * 1.10 * 1.10 * 1.04},
		{"Tatara max skills + 4% implant", 0.54, 5, 5, 5, 4, 0, 0.54 * 1.15 * 1.10 * 1.10 * 1.04},
		{"NPC max skills + 5% station tax", 0.50, 5, 5, 5, 0, 5, 0.50 * 1.15 * 1.10 * 1.10 * 0.95},
		{"skills clamped to 5", 0.50, 9, 9, 9, 0, 0, 0.50 * 1.15 * 1.10 * 1.10},
		{"negative levels clamped to 0", 0.50, -2, -3, -1, 0, 0, 0.50},
		{"tax > 100 clamped to 100 (yield = 0)", 0.50, 5, 5, 5, 0, 150, 0},
		{"base > 1 clamped to 1", 2.0, 5, 5, 5, 0, 0, 1.15 * 1.10 * 1.10},
	}
	for _, c := range cases {
		got := ReprocessingYieldMultiplier(c.base, c.repro, c.eff, c.spec, c.implantPct, c.taxPct)
		// Clamp expectation to [0,1] because ReprocessingYieldMultiplier does.
		want := c.want
		if want > 1 {
			want = 1
		}
		if want < 0 {
			want = 0
		}
		if !almostEq(got, want, 1e-9) {
			t.Errorf("%s: got %.6f, want %.6f", c.name, got, want)
		}
	}
}

// TestReprocessingNetYield honors the legacy ReprocessingYield override
// when no skill fields are set, and computes the canonical formula
// otherwise.
func TestReprocessingNetYield(t *testing.T) {
	// Legacy override: skill fields all zero, direct yield used.
	legacy := IndustryParams{ReprocessingYield: 0.72}
	if got := ReprocessingNetYield(legacy); !almostEq(got, 0.72, 1e-9) {
		t.Errorf("legacy override: got %v, want 0.72", got)
	}

	// Skill-aware: overrides ignored, formula wins.
	skilled := IndustryParams{
		ReprocessingBaseStationYield:     0.54, // Tatara
		ReprocessingSkillLevel:           5,
		ReprocessingEfficiencySkillLevel: 5,
		SpecificProcessingSkillLevel:     5,
		ReprocessingImplantYieldBonus:    4,
		// Legacy field also set — should be ignored since skill fields
		// are non-zero.
		ReprocessingYield: 0.99,
	}
	want := 0.54 * 1.15 * 1.10 * 1.10 * 1.04
	if got := ReprocessingNetYield(skilled); !almostEq(got, want, 1e-9) {
		t.Errorf("skill-aware: got %v, want %v", got, want)
	}
}

// TestBuildReprocessingSources verifies the reverse index inverts the
// ore→material map correctly.
func TestBuildReprocessingSources(t *testing.T) {
	sdeData := &sde.Data{
		Industry: sde.NewIndustryData(),
	}
	sdeData.Industry.Reprocessing[1230] = &sde.ReprocessingMaterial{ // Veldspar
		TypeID: 1230,
		Yields: []sde.MaterialYield{
			{TypeID: 34, Quantity: 415}, // Tritanium
		},
	}
	sdeData.Industry.Reprocessing[1228] = &sde.ReprocessingMaterial{ // Scordite
		TypeID: 1228,
		Yields: []sde.MaterialYield{
			{TypeID: 34, Quantity: 346}, // Tritanium
			{TypeID: 35, Quantity: 173}, // Pyerite
		},
	}
	idx := BuildReprocessingSources(sdeData)
	if idx == nil {
		t.Fatal("BuildReprocessingSources returned nil")
	}
	// Tritanium sources: Veldspar + Scordite.
	tritOres := idx[34]
	if len(tritOres) != 2 {
		t.Errorf("Tritanium sources = %v, want 2 entries", tritOres)
	}
	// Pyerite sources: Scordite only.
	pyeOres := idx[35]
	if len(pyeOres) != 1 || pyeOres[0] != 1228 {
		t.Errorf("Pyerite sources = %v, want [1228]", pyeOres)
	}
}

// TestBuildReprocessingSources_NilSafe verifies no panic on empty SDE.
func TestBuildReprocessingSources_NilSafe(t *testing.T) {
	if idx := BuildReprocessingSources(nil); idx != nil {
		t.Errorf("nil SDE: idx = %v, want nil", idx)
	}
	empty := &sde.Data{Industry: sde.NewIndustryData()}
	if idx := BuildReprocessingSources(empty); idx != nil {
		t.Errorf("empty SDE: idx = %v, want nil", idx)
	}
}
