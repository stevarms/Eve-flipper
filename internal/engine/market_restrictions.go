package engine

import "eve-flipper/internal/sde"

// marketDisabledTypeIDs lists item types that may appear in ESI market data
// but are not practically tradable via normal sell-side execution.
// Keep this list conservative: only hard-verified market-disabled types.
var marketDisabledTypeIDs = map[int32]struct{}{
	MPTCTypeID: {}, // Multiple Pilot Training Certificate
}

const playerStructureLocationIDMin int64 = 1_000_000_000_000

// EVE SDE category IDs treated as "cosmetic" — ship SKINs (including new
// Skin Studio paint variants) and character Apparel. These behave like
// trade goods in market data but have very thin, niche turnover and crowd
// out actionable opportunities in station-flip scans.
const (
	categoryApparel int32 = 30
	categorySKIN    int32 = 91
)

func isMarketDisabledType(typeID int32) bool {
	_, blocked := marketDisabledTypeIDs[typeID]
	return blocked
}

// isCosmeticType reports whether the type belongs to a cosmetic category
// (SKINs or Apparel). Returns false when SDE is unavailable for the type —
// it's safer to keep an unclassified type than to drop a tradable one.
func isCosmeticType(typeID int32, data *sde.Data) bool {
	if data == nil {
		return false
	}
	t, ok := data.Types[typeID]
	if !ok {
		return false
	}
	return t.CategoryID == categorySKIN || t.CategoryID == categoryApparel
}

// isPlayerStructureLocationID reports whether a market location id belongs to an Upwell structure.
func isPlayerStructureLocationID(locationID int64) bool {
	return locationID > playerStructureLocationIDMin
}

// IsMarketDisabledTypeID reports whether the given type is known market-disabled.
// Exported for API-level safety filters.
func IsMarketDisabledTypeID(typeID int32) bool {
	return isMarketDisabledType(typeID)
}

// IsPlayerStructureLocationID is exported for API-level safety filters.
func IsPlayerStructureLocationID(locationID int64) bool {
	return isPlayerStructureLocationID(locationID)
}
