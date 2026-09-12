package api

import (
	"sort"
	"strings"

	"eve-flipper/internal/auth"
	"eve-flipper/internal/esi"
	"eve-flipper/internal/sde"
)

// position_locations.go — where a holding physically is.
//
// The Positions tab aggregates a type across every station, on purpose: a cost
// basis is a property of the stack, not of one hangar, and splitting rows per
// station would report four different average costs for the same item. The
// merge sets LocationID to zero precisely so a single station's id cannot
// mislabel the whole row.
//
// The cost of that is a row saying "1,277 Multispectrum Coating II" with no
// indication of where any of it is. That is not a small gap: you cannot sell
// what you cannot find, and a stack spread over three stations and a container
// is a different job from one sitting in Jita.
//
// So location is attached as a breakdown beside the aggregate rather than by
// splitting it. The quantities are read from ESI assets, not from the journal.
// The journal knows where each lot was *bought*, which is where it usually
// still is and occasionally is not — and "usually" is not good enough for a
// field whose only job is to stop you hunting.

// PositionLocation is one place some of a holding sits.
type PositionLocation struct {
	Qty int64 `json:"qty"`

	// Station or structure. LocationName may be "Structure {id}" when the
	// character cannot resolve a private structure's name.
	LocationID   int64  `json:"location_id"`
	LocationName string `json:"location_name"`

	// ContainerName is set when the stack is inside a container rather than
	// loose in the hangar, which is the difference between "it's in Jita" and
	// "it's in Jita, in the can on the left".
	ContainerName string `json:"container_name,omitempty"`

	// Flag is ESI's location_flag — Hangar, CorpSAG1, and so on. Carried
	// because "in a ship's cargo hold" and "in the station hangar" are the same
	// station but not the same errand.
	Flag string `json:"flag,omitempty"`

	// CharacterName is who is holding it, so a multi-character stack does not
	// look like one pile.
	CharacterName string `json:"character_name,omitempty"`
}

// positionAssetIndex is one character's assets, indexed for parent lookups.
type positionAssetIndex struct {
	characterName string
	byItemID      map[int64]esi.CharacterAsset
	assets        []esi.CharacterAsset
}

// buildPositionLocations resolves where each held type actually is.
//
// Returns a map keyed by type id. A type absent from the map has no asset
// coverage — a manual row, or a character whose assets could not be read — and
// the UI must say nothing rather than imply the hangar is empty.
func (s *Server) buildPositionLocations(
	userID string,
	sessions []*auth.Session,
	sdeData *sde.Data,
	wanted map[int32]bool,
) map[int32][]PositionLocation {
	if len(sessions) == 0 || len(wanted) == 0 {
		return nil
	}

	indexes := make([]positionAssetIndex, 0, len(sessions))
	for _, sess := range sessions {
		token, err := s.sessions.EnsureValidTokenForUserCharacter(s.sso, userID, sess.CharacterID)
		if err != nil {
			continue
		}
		assets, err := s.esi.GetCharacterAssets(sess.CharacterID, token)
		if err != nil {
			// One character's assets failing must not blank the others'. The
			// map simply carries less.
			continue
		}
		idx := positionAssetIndex{
			characterName: sess.CharacterName,
			byItemID:      make(map[int64]esi.CharacterAsset, len(assets)),
			assets:        assets,
		}
		for _, a := range assets {
			idx.byItemID[a.ItemID] = a
		}
		indexes = append(indexes, idx)
	}
	if len(indexes) == 0 {
		return nil
	}

	// Keyed on everything that distinguishes a place, so two stacks in the same
	// container merge and two in different containers do not.
	type placeKey struct {
		locationID int64
		container  string
		flag       string
		character  string
	}

	out := map[int32][]PositionLocation{}
	for _, idx := range indexes {
		merged := map[int32]map[placeKey]int64{}
		for _, a := range idx.assets {
			if !wanted[a.TypeID] || a.Quantity <= 0 {
				continue
			}
			stationID, container, flag := idx.resolvePlace(a, sdeData)
			k := placeKey{
				locationID: stationID,
				container:  container,
				flag:       flag,
				character:  idx.characterName,
			}
			if merged[a.TypeID] == nil {
				merged[a.TypeID] = map[placeKey]int64{}
			}
			merged[a.TypeID][k] += a.Quantity
		}
		for typeID, places := range merged {
			for k, qty := range places {
				out[typeID] = append(out[typeID], PositionLocation{
					Qty:           qty,
					LocationID:    k.locationID,
					LocationName:  s.esi.StationName(k.locationID),
					ContainerName: k.container,
					Flag:          k.flag,
					CharacterName: k.character,
				})
			}
		}
	}

	// Biggest pile first: that is the one worth travelling to.
	for typeID := range out {
		rows := out[typeID]
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Qty != rows[j].Qty {
				return rows[i].Qty > rows[j].Qty
			}
			return rows[i].LocationName < rows[j].LocationName
		})
		out[typeID] = rows
	}
	return out
}

// resolvePlace walks an asset up to the station it ultimately sits in, naming
// the container it was found in along the way.
//
// ESI models containment by making an item's location_id the *item_id* of
// whatever holds it, so a stack in a can in a station is two hops from the
// station. Walking rather than assuming one level: a can inside a ship inside a
// station is ordinary, and stopping at the first parent would report the ship's
// item id as a location and render as "Location 1039...".
func (idx positionAssetIndex) resolvePlace(
	a esi.CharacterAsset,
	sdeData *sde.Data,
) (stationID int64, container string, flag string) {
	flag = normalizePositionFlag(a.LocationFlag)
	cur := a
	// A generous bound rather than a trusted one: ESI has produced cyclic
	// parent references, and an unbounded walk on live data is a hang.
	for hop := 0; hop < 8; hop++ {
		parent, ok := idx.byItemID[cur.LocationID]
		if !ok {
			// The parent is not an asset, so it is a station or structure.
			return cur.LocationID, container, flag
		}
		// The nearest enclosing container is the useful one to name; outer
		// hops are structure, not somewhere to look.
		if container == "" {
			container = positionContainerName(parent, sdeData)
		}
		cur = parent
	}
	return cur.LocationID, container, flag
}

func positionContainerName(parent esi.CharacterAsset, sdeData *sde.Data) string {
	if parent.TypeName != "" {
		return parent.TypeName
	}
	if sdeData != nil {
		if t, ok := sdeData.Types[parent.TypeID]; ok && t.Name != "" {
			return t.Name
		}
	}
	// Naming it as a container beats naming it as nothing: the point of the
	// field is "do not just look in the hangar".
	return "Container"
}

// normalizePositionFlag drops the flags that would only add noise.
//
// "Hangar" is where things are by default, so printing it on most rows would
// train the eye to skip the column that occasionally says CorpSAG1 or
// ShipHangar. Ship fitting slots are dropped for the same reason plus a better
// one: a module fitted to a ship is not stock.
func normalizePositionFlag(flag string) string {
	f := strings.TrimSpace(flag)
	switch f {
	case "", "Hangar", "Unlocked", "AutoFit":
		return ""
	}
	switch {
	case strings.HasPrefix(f, "HiSlot"),
		strings.HasPrefix(f, "MedSlot"),
		strings.HasPrefix(f, "LoSlot"),
		strings.HasPrefix(f, "RigSlot"),
		strings.HasPrefix(f, "SubSystemSlot"):
		return "Fitted"
	}
	return f
}
