package engine

import (
	"eve-flipper/internal/sde"
)

// EVE canonical reprocessing yield formula (post-2014 rework):
//
//	yield = base_station_yield
//	      × (1 + 0.03 × Reprocessing_level)
//	      × (1 + 0.02 × ReprocessingEfficiency_level)
//	      × (1 + 0.02 × specific_ore/scrap_processing_level)
//	      × (1 + implant_bonus_pct / 100)
//
// Then station tax reduces the effective output:
//
//	net = yield × (1 - station_tax_pct / 100)
//
// Base station yield varies by facility:
//   - NPC station: 0.50
//   - Athanor (with standing 0): 0.52
//   - Tatara: 0.54
//   - Upwell rigs add another 0-8 pp depending on rig + sec status
//
// At max skills, no rigs, NPC station, 0% tax:
//
//	0.50 × 1.15 × 1.10 × 1.10 × 1.00 = 0.66550
//
// At max skills + 4% implant + NPC station + 0% tax:
//
//	0.50 × 1.15 × 1.10 × 1.10 × 1.04 = 0.69212
//
// At max skills + 4% implant + Tatara (0.54 base, no rigs) + 0% tax:
//
//	0.54 × 1.15 × 1.10 × 1.10 × 1.04 = 0.74749

// ReprocessingYieldMultiplier returns the net yield fraction (0-1) after
// applying character skills, implants, and station tax to the given base
// station yield. All arguments are optional (zero produces the identity
// factor for that component).
//
// The result is clamped to [0, 1]. Callers pass raw skill levels; the
// function clamps them to [0, 5].
func ReprocessingYieldMultiplier(
	baseStationYield float64,
	reprocessingLevel int32,
	efficiencyLevel int32,
	specificProcessingLevel int32,
	implantBonusPercent float64,
	stationTaxPercent float64,
) float64 {
	if baseStationYield <= 0 {
		baseStationYield = 0.50 // NPC station default
	}
	if baseStationYield > 1 {
		baseStationYield = 1
	}
	clamp5 := func(v int32) int32 {
		if v < 0 {
			return 0
		}
		if v > 5 {
			return 5
		}
		return v
	}
	repro := clamp5(reprocessingLevel)
	eff := clamp5(efficiencyLevel)
	spec := clamp5(specificProcessingLevel)
	if implantBonusPercent < 0 {
		implantBonusPercent = 0
	}
	if stationTaxPercent < 0 {
		stationTaxPercent = 0
	}
	if stationTaxPercent > 100 {
		stationTaxPercent = 100
	}

	gross := baseStationYield *
		(1.0 + 0.03*float64(repro)) *
		(1.0 + 0.02*float64(eff)) *
		(1.0 + 0.02*float64(spec)) *
		(1.0 + implantBonusPercent/100.0)

	net := gross * (1.0 - stationTaxPercent/100.0)
	if net < 0 {
		return 0
	}
	if net > 1 {
		return 1
	}
	return net
}

// ReprocessingNetYield returns the net yield for a set of reprocessing
// parameters, honoring the fallback ReprocessingYield override when no
// skill / tax / implant fields are set.
func ReprocessingNetYield(params IndustryParams) float64 {
	skillsAllZero := params.ReprocessingSkillLevel == 0 &&
		params.ReprocessingEfficiencySkillLevel == 0 &&
		params.SpecificProcessingSkillLevel == 0
	taxAndImplantZero := params.ReprocessingImplantYieldBonus == 0 &&
		params.ReprocessingStationTaxPercent == 0
	baseZero := params.ReprocessingBaseStationYield == 0
	if skillsAllZero && taxAndImplantZero && baseZero && params.ReprocessingYield > 0 {
		// Legacy caller: they set a direct net-yield fraction and no
		// skill fields. Trust it.
		y := params.ReprocessingYield
		if y > 1 {
			y = 1
		}
		return y
	}
	return ReprocessingYieldMultiplier(
		params.ReprocessingBaseStationYield,
		params.ReprocessingSkillLevel,
		params.ReprocessingEfficiencySkillLevel,
		params.SpecificProcessingSkillLevel,
		params.ReprocessingImplantYieldBonus,
		params.ReprocessingStationTaxPercent,
	)
}

// ReprocessingSources indexes materials → the ores that reprocess into
// them. Built lazily from sde.Industry.Reprocessing (which maps ore →
// materials). Nil-safe: returns nil when SDE isn't loaded yet.
type ReprocessingSources map[int32][]int32

// BuildReprocessingSources builds the reverse index from an SDE snapshot.
// Runs in O(sum-of-yields); typical dataset is a few thousand entries.
func BuildReprocessingSources(sdeData *sde.Data) ReprocessingSources {
	if sdeData == nil || sdeData.Industry == nil || len(sdeData.Industry.Reprocessing) == 0 {
		return nil
	}
	out := make(ReprocessingSources, len(sdeData.Industry.Reprocessing))
	for oreID, entry := range sdeData.Industry.Reprocessing {
		if entry == nil {
			continue
		}
		for _, yield := range entry.Yields {
			out[yield.TypeID] = append(out[yield.TypeID], oreID)
		}
	}
	return out
}

// reprocessingBatchSize is the SDE typeMaterials batch base — the yield
// numbers are stated for a batch of this many source units. In real EVE
// this is the type's portionSize (100 for standard ores, 1 for ice
// products, varies for moon materials). We approximate at 100 because
// portionSize isn't loaded in this build; documented as a follow-up.
// Being off by this factor for non-100-portionSize ores makes the
// ore-derived cost look better or worse than reality — the analyzer's
// build-vs-buy still picks the LOWER of direct-buy and reprocessed, so
// the impact is bounded: at worst the tool prefers direct-buy when
// reprocess would be cheaper (or vice versa). Users can also override
// with the direct ReprocessingYield fraction if they've computed the
// right value externally.
const reprocessingBatchSize = 100

// cheapestReprocessedCostPerUnit returns the per-unit cost of `materialID`
// obtainable by reprocessing an ore, using the cheapest ore available on
// the analyzer's market data. Returns (cost, ok=true) when at least one
// ore source has a market price; otherwise (0, false).
//
// Formula:
//
//	batch_output_units = yield.Quantity × netYield
//	cost_per_unit = ore_market_price × batchSize / batch_output_units
//
// where yield.Quantity is what the SDE says the ore produces of the
// target material per batchSize units of ore at 100% yield.
func (a *IndustryAnalyzer) cheapestReprocessedCostPerUnit(
	materialID int32,
	netYield float64,
	costModel string,
) (float64, bool) {
	if a == nil || a.SDE == nil || a.SDE.Industry == nil || netYield <= 0 {
		return 0, false
	}
	if a.reprocessingSources == nil {
		return 0, false
	}
	ores, ok := a.reprocessingSources[materialID]
	if !ok || len(ores) == 0 {
		return 0, false
	}

	best := 0.0
	haveBest := false
	for _, oreID := range ores {
		orePrice := a.materialCostRaw(oreID, 1, costModel)
		if orePrice <= 0 {
			continue
		}
		entry := a.SDE.Industry.Reprocessing[oreID]
		if entry == nil {
			continue
		}
		var perBatch float64
		for _, y := range entry.Yields {
			if y.TypeID == materialID {
				perBatch = float64(y.Quantity)
				break
			}
		}
		if perBatch <= 0 {
			continue
		}
		effectiveOutput := perBatch * netYield
		if effectiveOutput <= 0 {
			continue
		}
		costPerUnit := orePrice * reprocessingBatchSize / effectiveOutput
		if !haveBest || costPerUnit < best {
			best = costPerUnit
			haveBest = true
		}
	}
	return best, haveBest
}
