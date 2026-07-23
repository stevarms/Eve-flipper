package sde

import "strings"

// Rig-group-name substrings → affinity descriptors. Resolved to concrete
// numeric rig groupIDs at SDE load time by scanning d.Groups for group
// names containing the substring. This dodges the "have to know every
// groupID by heart" problem and survives CCP renumbering — as long as
// group names stay stable (they usually do).
//
// CCP names structure rig groups in the pattern:
//   "Structure Engineering Rig M/L/XL - <specialty>"    (engineering/invention)
//   "Structure Composite/Hybrid/Biochemical Reactor Rig M - TE/ME"  (reactions)
//   "Structure Reactor Rig L - Efficiency"              (generic L reactor)
//
// Bind order matters: broader-scoped binders should come AFTER narrower
// ones so a more specific match wins (first-match wins in the loop).
//
// Common EVE category IDs referenced below:
//   4  Materials (raw / minerals / reaction products)
//   6  Ship
//   7  Module
//   8  Charge (ammunition)
//   17 Commodity
//   18 Drone
//   22 Deployable
//   32 Subsystem
//   65 Structure
//   66 Structure Module
//   87 Fighter
type rigAffinityBinder struct {
	NameContains string  // case-insensitive substring match on ItemGroup.Name
	Activity     string  // one of: manufacturing, reaction, invention, copying, research_material, research_time
	Family       string  // engineering | reaction (informational)
	CategoryIDs  []int32 // whitelist; empty = any
	MetaGroupIDs []int32 // whitelist; empty = any tech tier
}

// Order matters: narrower substrings must precede broader ones so
// first-match wins correctly. E.g. "Advanced Small Ship" must come before
// "Small Ship" (which we don't have, but the principle holds for
// "Advanced Component" vs "Component" etc.).
var rigAffinityBinders = []rigAffinityBinder{
	// --- Manufacturing: charges/drones/modules ---
	{NameContains: "Ammunition", Activity: "manufacturing", Family: "engineering",
		CategoryIDs: []int32{8}},
	{NameContains: "Drone and Fighter", Activity: "manufacturing", Family: "engineering",
		CategoryIDs: []int32{18, 87}},
	{NameContains: "Equipment and Consumable", Activity: "manufacturing", Family: "engineering",
		CategoryIDs: []int32{7, 8}},
	{NameContains: "Equipment", Activity: "manufacturing", Family: "engineering",
		CategoryIDs: []int32{7}},

	// --- Manufacturing: ship classes (T1 = "Basic", T2 = "Advanced") ---
	{NameContains: "Advanced Small Ship", Activity: "manufacturing", Family: "engineering",
		CategoryIDs: []int32{6}, MetaGroupIDs: []int32{2}},
	{NameContains: "Advanced Medium Ship", Activity: "manufacturing", Family: "engineering",
		CategoryIDs: []int32{6}, MetaGroupIDs: []int32{2}},
	{NameContains: "Advanced Large Ship", Activity: "manufacturing", Family: "engineering",
		CategoryIDs: []int32{6}, MetaGroupIDs: []int32{2}},
	{NameContains: "Basic Small Ship", Activity: "manufacturing", Family: "engineering",
		CategoryIDs: []int32{6}, MetaGroupIDs: []int32{1}},
	{NameContains: "Basic Medium Ship", Activity: "manufacturing", Family: "engineering",
		CategoryIDs: []int32{6}, MetaGroupIDs: []int32{1}},
	{NameContains: "Basic Large Ship", Activity: "manufacturing", Family: "engineering",
		CategoryIDs: []int32{6}, MetaGroupIDs: []int32{1}},
	{NameContains: "Capital Ship", Activity: "manufacturing", Family: "engineering",
		CategoryIDs: []int32{6}},
	// XL "Ship Efficiency" is the Sotiyo omni-ship rig — applies to ALL ship
	// tiers. Must come AFTER the narrower "* Ship" binders above.
	{NameContains: "Ship Efficiency", Activity: "manufacturing", Family: "engineering",
		CategoryIDs: []int32{6}},

	// --- Manufacturing: components ---
	{NameContains: "Advanced Component", Activity: "manufacturing", Family: "engineering",
		CategoryIDs: []int32{17}, MetaGroupIDs: []int32{2, 14}},
	{NameContains: "Basic Capital Component", Activity: "manufacturing", Family: "engineering",
		CategoryIDs: []int32{17}, MetaGroupIDs: []int32{1}},
	{NameContains: "Structure and Component", Activity: "manufacturing", Family: "engineering",
		CategoryIDs: []int32{17, 65, 66}},

	// --- Manufacturing: structures ---
	// "Structure ME" and "Structure TE" and "Structure Efficiency" all target
	// structure hulls. Deliberately narrow to avoid matching "Structure and
	// Component" (handled above).
	{NameContains: "Structure ME", Activity: "manufacturing", Family: "engineering",
		CategoryIDs: []int32{65, 66}},
	{NameContains: "Structure TE", Activity: "manufacturing", Family: "engineering",
		CategoryIDs: []int32{65, 66}},
	{NameContains: "Structure Efficiency", Activity: "manufacturing", Family: "engineering",
		CategoryIDs: []int32{65, 66}},

	// --- Invention ---
	// XL "Laboratory Optimization" covers invention/copying/research together;
	// bind it to invention (most common). Copying + research still work
	// because the L-tier variants explicitly name each activity.
	{NameContains: "Invention", Activity: "invention", Family: "engineering"},
	{NameContains: "Laboratory Optimization", Activity: "invention", Family: "engineering"},

	// --- Copying ---
	{NameContains: "Blueprint Copy", Activity: "copying", Family: "engineering"},

	// --- Research (ME / TE) — not currently modelled in the analyzer, but
	// binding them means the picker still surfaces the rigs so users see
	// their loadout and we don't drop them silently. When we add research
	// activity to the engine, the binding is already in place. ---
	{NameContains: "ME Research", Activity: "research_material", Family: "engineering"},
	{NameContains: "TE Research", Activity: "research_time", Family: "engineering"},

	// --- Reactions ---
	{NameContains: "Composite Reactor", Activity: "reaction", Family: "reaction"},
	{NameContains: "Hybrid Reactor", Activity: "reaction", Family: "reaction"},
	{NameContains: "Biochemical Reactor", Activity: "reaction", Family: "reaction"},
	// Generic L-tier reactor rig — applies to all reaction types.
	{NameContains: "Reactor Rig L", Activity: "reaction", Family: "reaction"},
}

// buildRigAffinityMap resolves each name-substring binder against the
// loaded group catalog into concrete groupID → category descriptors.
// Groups that match multiple binders are bound to the FIRST matching
// binder — order the binders narrower-first.
func buildRigAffinityMap(groups map[int32]*ItemGroup) map[int32]StructureRigCategory {
	out := make(map[int32]StructureRigCategory, len(rigAffinityBinders)*4)
	if len(groups) == 0 {
		return out
	}
	for gid, g := range groups {
		if g == nil {
			continue
		}
		// Structure Module category. Anything outside it isn't a rig, so
		// skip early even if the name happens to match a binder substring.
		if g.CategoryID != 66 {
			continue
		}
		name := strings.ToLower(g.Name)
		// Ignore non-rig structure modules (turrets, ECM, service modules,
		// etc.). Every rig group name contains "rig" — cheap early filter.
		if !strings.Contains(name, "rig") {
			continue
		}
		for _, b := range rigAffinityBinders {
			if !strings.Contains(name, strings.ToLower(b.NameContains)) {
				continue
			}
			if _, already := out[gid]; already {
				continue // first-match wins
			}
			out[gid] = StructureRigCategory{
				RigGroupID:          gid,
				Family:              b.Family,
				Activity:            b.Activity,
				ProductCategoryIDs:  b.CategoryIDs,
				ProductMetaGroupIDs: b.MetaGroupIDs,
				Description:         g.Name,
			}
			break
		}
	}
	return out
}
