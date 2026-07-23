package sde

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"eve-flipper/internal/logger"
)

// StructureRig is a Standup rig fitted to an Upwell industry structure.
// Bonus fields carry the raw dogma-attribute values (negative percent for
// reductions). Sec-status multipliers scale the base bonus for the system
// the structure sits in.
type StructureRig struct {
	TypeID              int32
	Name                string
	GroupID             int32   // rig-group identity; keys into RigAffinities
	MetaGroupID         int32   // 53 = T2, 54 = T1
	RigSize             int32   // attr 1547: 2=M, 3=L, 4=XL
	MEBonus             float64 // engineering attr 2594; reaction attr 2714 — negative %
	TEBonus             float64 // engineering attr 2593; reaction attr 2713 — negative %
	CostBonus           float64 // engineering attr 2595 — negative % (reactions: 0)
	HiSecMult           float64 // attr 2355 — 0 = cannot run in hisec
	LowSecMult          float64 // attr 2356
	NullSecMult         float64 // attr 2357 — also covers wormhole space
	FitsStructureGroups []int32 // attrs 1298/1299/1300 — structure hull groupIDs this rig fits
}

// StructureRigCategory maps a rig's own groupID to what it affects: which
// activity, which product categories/groups/tech-tiers. A rig applies to a
// row iff activity matches AND (product category filter empty OR product
// category is in the filter) AND (group filter empty OR group matches)
// AND (metaGroup filter empty OR metaGroup matches).
type StructureRigCategory struct {
	RigGroupID          int32
	Family              string  // "engineering" | "reaction"
	Activity            string  // "manufacturing" | "reaction" | "invention" | "copying"
	ProductCategoryIDs  []int32 // whitelist of product categoryIDs
	ProductGroupIDs     []int32 // optional narrower group whitelist
	ProductMetaGroupIDs []int32 // optional tech-tier whitelist
	Description         string  // human-readable summary
}

// Matches reports whether this rig's affinity applies to the given
// (activity, product) pair.
func (aff StructureRigCategory) Matches(activity string, product *ItemType) bool {
	if aff.Activity != "" && aff.Activity != activity {
		return false
	}
	if product == nil {
		// Activity-only rigs (e.g. Copying) match any product.
		return len(aff.ProductCategoryIDs) == 0 && len(aff.ProductGroupIDs) == 0 && len(aff.ProductMetaGroupIDs) == 0
	}
	if len(aff.ProductCategoryIDs) > 0 {
		found := false
		for _, c := range aff.ProductCategoryIDs {
			if c == product.CategoryID {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(aff.ProductGroupIDs) > 0 {
		found := false
		for _, g := range aff.ProductGroupIDs {
			if g == product.GroupID {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(aff.ProductMetaGroupIDs) > 0 {
		found := false
		for _, m := range aff.ProductMetaGroupIDs {
			if m == product.MetaGroupID {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// SecMultiplier returns the appropriate sec-scaled multiplier for this rig
// in a system of the given security status. Returns 0 if the rig cannot
// operate at that sec (e.g. advanced rigs in hisec).
func (r *StructureRig) SecMultiplier(security float64) float64 {
	if r == nil {
		return 0
	}
	if security >= 0.45 {
		return r.HiSecMult
	}
	if security > 0.0 {
		return r.LowSecMult
	}
	return r.NullSecMult
}

// Dogma attribute IDs we care about when scanning typeDogma.jsonl.
const (
	dogmaAttrRigSize            = 1547
	dogmaAttrHiSecMult          = 2355
	dogmaAttrLowSecMult         = 2356
	dogmaAttrNullSecMult        = 2357
	dogmaAttrEngRigTEBonus      = 2593 // engineering: TE reduction %
	dogmaAttrEngRigMEBonus      = 2594 // engineering: ME reduction %
	dogmaAttrEngRigCostBonus    = 2595 // engineering: job-cost reduction %
	dogmaAttrRxnRigTEBonus      = 2713 // reaction: time reduction %
	dogmaAttrRxnRigMEBonus      = 2714 // reaction: material reduction %
	dogmaAttrFitsShipGroup01    = 1298
	dogmaAttrFitsShipGroup02    = 1299
	dogmaAttrFitsShipGroup03    = 1300
)

// loadRigs parses Standup structure rigs from typeDogma.jsonl. Must run
// after loadTypes so d.Types is populated. Builds d.Rigs, d.RigsByFitsGroup,
// and d.RigAffinities. Missing files/attributes are logged and skipped —
// rig support silently degrades to "no rigs known" rather than failing
// SDE load.
func (d *Data) loadRigs(dir string) error {
	if d.Types == nil || d.Groups == nil {
		return fmt.Errorf("loadRigs: Types + Groups must be loaded first")
	}
	d.Rigs = make(map[int32]*StructureRig, 400)
	d.RigsByFitsGroup = make(map[int32][]*StructureRig, 16)
	d.RigAffinities = buildRigAffinityMap(d.Groups)

	// Identify rig typeIDs up front: types in groups that appear in our
	// affinity map. This lets the dogma-parse pass skip the vast majority
	// of type entries without allocating maps for them.
	rigGroupIDs := make(map[int32]bool, len(d.RigAffinities))
	for gid := range d.RigAffinities {
		rigGroupIDs[gid] = true
	}
	rigTypeIDs := make(map[int32]bool, 400)
	rigNameByID := make(map[int32]string, 400)
	rigGroupByID := make(map[int32]int32, 400)
	rigMetaByID := make(map[int32]int32, 400)
	for id, t := range d.Types {
		if t == nil {
			continue
		}
		if rigGroupIDs[t.GroupID] {
			rigTypeIDs[id] = true
			rigNameByID[id] = t.Name
			rigGroupByID[id] = t.GroupID
			rigMetaByID[id] = t.MetaGroupID
		}
	}
	if len(rigTypeIDs) == 0 {
		logger.Info("SDE", "No Standup rigs found in types — rig catalog empty")
		return nil
	}

	// Stream typeDogma.jsonl once. Only process entries whose _key is a
	// known rig typeID.
	path := filepath.Join(dir, "typeDogma.jsonl")
	found, err := readOptionalJSONL(dir, "typeDogma", func(raw json.RawMessage) error {
		var line struct {
			Key             int32 `json:"_key"`
			DogmaAttributes []struct {
				AttributeID int32   `json:"attributeID"`
				Value       float64 `json:"value"`
			} `json:"dogmaAttributes"`
		}
		if err := json.Unmarshal(raw, &line); err != nil {
			return nil // skip malformed line
		}
		if !rigTypeIDs[line.Key] {
			return nil
		}
		rig := &StructureRig{
			TypeID:      line.Key,
			Name:        rigNameByID[line.Key],
			GroupID:     rigGroupByID[line.Key],
			MetaGroupID: rigMetaByID[line.Key],
		}
		// Track which family the attrs come from — an engineering rig has
		// 2593/2594/2595, a reaction rig has 2713/2714.
		for _, a := range line.DogmaAttributes {
			switch a.AttributeID {
			case dogmaAttrRigSize:
				rig.RigSize = int32(a.Value)
			case dogmaAttrHiSecMult:
				rig.HiSecMult = a.Value
			case dogmaAttrLowSecMult:
				rig.LowSecMult = a.Value
			case dogmaAttrNullSecMult:
				rig.NullSecMult = a.Value
			case dogmaAttrEngRigTEBonus, dogmaAttrRxnRigTEBonus:
				rig.TEBonus = a.Value
			case dogmaAttrEngRigMEBonus, dogmaAttrRxnRigMEBonus:
				rig.MEBonus = a.Value
			case dogmaAttrEngRigCostBonus:
				rig.CostBonus = a.Value
			case dogmaAttrFitsShipGroup01, dogmaAttrFitsShipGroup02, dogmaAttrFitsShipGroup03:
				if a.Value > 0 {
					rig.FitsStructureGroups = append(rig.FitsStructureGroups, int32(a.Value))
				}
			}
		}
		d.Rigs[line.Key] = rig
		for _, g := range rig.FitsStructureGroups {
			d.RigsByFitsGroup[g] = append(d.RigsByFitsGroup[g], rig)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("loadRigs: read %s: %w", path, err)
	}
	if !found {
		logger.Info("SDE", "typeDogma.jsonl not found — rig catalog empty")
		return nil
	}

	logger.Info("SDE", fmt.Sprintf("Loaded %d structure rigs across %d rig groups", len(d.Rigs), len(d.RigAffinities)))
	return nil
}

// StructureRigsByHull returns rigs fittable to the given structure hull
// groupID. Empty slice when the hull has no rigs (or SDE doesn't know
// about the hull).
func (d *Data) StructureRigsByHull(hullGroupID int32) []*StructureRig {
	if d == nil || d.RigsByFitsGroup == nil {
		return nil
	}
	return d.RigsByFitsGroup[hullGroupID]
}

// _ tighten unused-imports guard — keep strings for future use in rig
// filtering helpers.
var _ = strings.TrimSpace
