package api

import "eve-flipper/internal/esi"

// stockpileAsset is the minimal projection of a character or corporation
// asset row needed by the roll-up walk. It exists so RollUpAtStation can
// operate on either source without an interface.
type stockpileAsset struct {
	ItemID     int64
	TypeID     int32
	LocationID int64
	Quantity   int64
}

func stockpileAssetsFromCharacter(rows []esi.CharacterAsset) []stockpileAsset {
	out := make([]stockpileAsset, 0, len(rows))
	for _, r := range rows {
		out = append(out, stockpileAsset{
			ItemID:     r.ItemID,
			TypeID:     r.TypeID,
			LocationID: r.LocationID,
			Quantity:   r.Quantity,
		})
	}
	return out
}

func stockpileAssetsFromCorporation(rows []esi.CorporationAsset) []stockpileAsset {
	out := make([]stockpileAsset, 0, len(rows))
	for _, r := range rows {
		out = append(out, stockpileAsset{
			ItemID:     r.ItemID,
			TypeID:     r.TypeID,
			LocationID: r.LocationID,
			Quantity:   r.Quantity,
		})
	}
	return out
}

// rollUpAtStation walks the asset tree rooted at stationID and returns
// {typeID -> total quantity} summed across every asset the walk reaches:
// items directly at the station, plus every container / ship cargo / assembly
// array whose ItemID chains up to the station via LocationID.
//
// Works uniformly for NPC station IDs (60M-64M) and player structure IDs —
// ESI keys parents by numeric location_id regardless of location type.
//
// Skipped intentionally in v1:
//   - No location_flag blocklist. Skill hangars / medical bays are extremely
//     unlikely to hold stockpile mats but if that becomes a problem we can add
//     a flag filter here later.
//   - Blueprints (typeID with is_blueprint_copy=true) are still counted — a
//     BP with runs=100 shows up as quantity=1. Callers that care can filter.
func rollUpAtStation(assets []stockpileAsset, stationID int64) map[int32]int64 {
	if stationID <= 0 || len(assets) == 0 {
		return map[int32]int64{}
	}

	byParent := make(map[int64][]stockpileAsset, len(assets))
	for _, a := range assets {
		byParent[a.LocationID] = append(byParent[a.LocationID], a)
	}

	totals := make(map[int32]int64)
	queue := append([]stockpileAsset(nil), byParent[stationID]...)
	visited := make(map[int64]bool, len(assets))

	for len(queue) > 0 {
		asset := queue[0]
		queue = queue[1:]
		if visited[asset.ItemID] {
			continue
		}
		visited[asset.ItemID] = true

		if asset.TypeID > 0 && asset.Quantity > 0 {
			totals[asset.TypeID] += asset.Quantity
		}
		if kids, ok := byParent[asset.ItemID]; ok {
			queue = append(queue, kids...)
		}
	}
	return totals
}
