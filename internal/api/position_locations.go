package api

import (
	"fmt"
	"log"
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
//
// That difference turns this into a reconciliation check, which was not the
// original intent and is the more valuable half. A position exists because the
// FIFO ledger saw a buy and has not seen enough sells; nothing ever confirmed
// the stock is still there. Items consumed as industry materials, reprocessed,
// or sold before the archive's window all leave a holding the ledger still
// believes in. When assets are readable and the type is absent from them, that
// is worth saying out loud rather than rendering as a blank cell.
//
// Which makes the difference between "we could not read your assets" and "your
// assets do not contain this" load-bearing. Both produced an empty list in the
// first cut, so a missing scope looked exactly like missing stock. The response
// now carries AssetsFailed alongside the existing PricingFailed / OrdersFailed,
// and every read failure is logged rather than swallowed.

// sdeShipCategoryID is the SDE category for ships. Anything whose ancestor
// chain passes through one is aboard a hull rather than in stock.
const sdeShipCategoryID = int32(6)

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
// Returns the per-type breakdown and whether every asset source was read. When
// ok is false the caller must not treat an absent type as missing stock: some
// hangar simply could not be seen.
func (s *Server) buildPositionLocations(
	userID string,
	sessions []*auth.Session,
	sdeData *sde.Data,
	wanted map[int32]bool,
) (map[int32][]PositionLocation, bool) {
	if len(sessions) == 0 || len(wanted) == 0 {
		return nil, false
	}
	complete := true

	indexes := make([]positionAssetIndex, 0, len(sessions)+1)
	seenCorp := map[int32]bool{}
	for _, sess := range sessions {
		token, err := s.sessions.EnsureValidTokenForUserCharacter(s.sso, userID, sess.CharacterID)
		if err != nil {
			log.Printf("[POSITIONS] assets: token for %s: %v", sess.CharacterName, err)
			complete = false
			continue
		}
		assets, err := s.esi.GetCharacterAssets(sess.CharacterID, token)
		if err != nil {
			// One character's assets failing must not blank the others', but
			// it does mean absence no longer proves anything.
			log.Printf("[POSITIONS] assets: %s: %v", sess.CharacterName, err)
			complete = false
		} else {
			indexes = append(indexes, newPositionAssetIndex(sess.CharacterName, assets))
		}

		// Corp hangars. Positions are built from an IncludeAll wallet scope, so
		// corp-bought stock is already in the rows; without this it would be a
		// holding with a cost basis and nowhere to be, which reads as a phantom.
		corpID, corpErr := s.esi.GetCharacterCorporationID(sess.CharacterID)
		if corpErr != nil || corpID <= 0 || seenCorp[corpID] {
			continue
		}
		seenCorp[corpID] = true
		corpAssets, corpAssetErr := s.esi.GetCorporationAssets(corpID, token)
		if corpAssetErr != nil {
			// Usually a 403: this character lacks the role. Another character
			// in the same corp may have it, but we have already marked the
			// corp seen, so retry through the next session instead.
			log.Printf("[POSITIONS] corp assets: corp %d via %s: %v", corpID, sess.CharacterName, corpAssetErr)
			delete(seenCorp, corpID)
			continue
		}
		converted := make([]esi.CharacterAsset, 0, len(corpAssets))
		for _, a := range corpAssets {
			converted = append(converted, esi.CharacterAsset{
				ItemID:       a.ItemID,
				TypeID:       a.TypeID,
				LocationID:   a.LocationID,
				LocationType: a.LocationType,
				LocationFlag: a.LocationFlag,
				Quantity:     a.Quantity,
				IsSingleton:  a.IsSingleton,
				TypeName:     a.TypeName,
			})
		}
		indexes = append(indexes, newPositionAssetIndex(corpLabelFor(corpID), converted))
	}
	if len(indexes) == 0 {
		return nil, false
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
			stationID, container, flag, inShip := idx.resolvePlace(a, sdeData)
			// Anything aboard a hull is equipment, not stock. A module in a
			// fitting slot, ammo in a hold, drones in a bay -- none of it is
			// for sale, and prompting to list it is worse than silence.
			if inShip {
				continue
			}
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
	return out, complete
}

func newPositionAssetIndex(owner string, assets []esi.CharacterAsset) positionAssetIndex {
	idx := positionAssetIndex{
		characterName: owner,
		byItemID:      make(map[int64]esi.CharacterAsset, len(assets)),
		assets:        assets,
	}
	for _, a := range assets {
		idx.byItemID[a.ItemID] = a
	}
	return idx
}

// corpLabelFor names the corp holding a stack. The id rather than a fetched
// name: this is a per-row suffix, and a corporation lookup per position row to
// prettify it is not worth the calls.
func corpLabelFor(corpID int32) string {
	return fmt.Sprintf("Corp %d", corpID)
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
) (stationID int64, container string, flag string, inShip bool) {
	flag = normalizePositionFlag(a.LocationFlag)
	cur := a
	// A generous bound rather than a trusted one: ESI has produced cyclic
	// parent references, and an unbounded walk on live data is a hang.
	for hop := 0; hop < 8; hop++ {
		parent, ok := idx.byItemID[cur.LocationID]
		if !ok {
			// The parent is not an asset, so it is a station or structure.
			return cur.LocationID, container, flag, inShip
		}
		// Anywhere in the chain, not just the immediate parent: a can inside a
		// freighter is still cargo, and ammo in a drone bay is two hops from
		// the hull.
		if isShipType(parent.TypeID, sdeData) {
			inShip = true
		}
		// The nearest enclosing container is the useful one to name; outer
		// hops are structure, not somewhere to look.
		if container == "" {
			container = positionContainerName(parent, sdeData)
		}
		cur = parent
	}
	return cur.LocationID, container, flag, inShip
}

// isShipType reports whether a type is a hull.
//
// Without the SDE loaded this returns false, which keeps the stack visible.
// Showing something that turns out to be in a cargo hold is a smaller error
// than hiding stock because the type table had not finished loading.
func isShipType(typeID int32, sdeData *sde.Data) bool {
	if sdeData == nil {
		return false
	}
	t, ok := sdeData.Types[typeID]
	return ok && t != nil && t.CategoryID == sdeShipCategoryID
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
