package zkillboard

import (
	"fmt"
	"math"
	"sort"
	"time"

	"eve-flipper/internal/esi"
	"eve-flipper/internal/logger"
	"eve-flipper/internal/sde"
)

// ESIKillmail represents a full killmail from the ESI API.
type ESIKillmail struct {
	KillmailID    int64     `json:"killmail_id"`
	KillmailTime  string    `json:"killmail_time"`
	SolarSystemID int32     `json:"solar_system_id"`
	Victim        ESIVictim `json:"victim"`
}

// ESIVictim contains victim info including fitted items.
type ESIVictim struct {
	ShipTypeID int32     `json:"ship_type_id"`
	Items      []ESIItem `json:"items"`
}

// ESIItem is a single item from a killmail (fitted module, ammo, drone, cargo).
//
// The type id field is item_type_id, NOT type_id. Killmails -- both from ESI and
// inline from zkillboard -- have only ever used item_type_id; the old type_id tag
// silently unmarshalled every fitted module, charge and drone to TypeID 0, which
// categorizeItem then dropped. Hulls were unaffected because they come from
// victim.ship_type_id, so the demand tab looked like it worked while listing
// hulls only. See TestESIItem_UsesItemTypeID.
type ESIItem struct {
	TypeID            int32 `json:"item_type_id"`
	Flag              int32 `json:"flag"`
	QuantityDestroyed int32 `json:"quantity_destroyed"`
	QuantityDropped   int32 `json:"quantity_dropped"`
	Singleton         int32 `json:"singleton"`
	// Items holds the contents of a container. The ESI killmail schema nests
	// exactly one level -- the nested item object has no items property of its
	// own -- but the recursion here is general, which costs nothing.
	Items []ESIItem `json:"items"`
}

// flattenItems appends every entry in items, and everything nested inside a
// container, to out.
//
// A nested item's flag describes where it sits inside its container, and a plain
// container has no slots, so that flag is 0 (None) -- which every category test
// rejects. Contents therefore inherit the container's own flag: goods stowed in
// a cargo container are cargo. A nested item that does carry a meaningful flag
// (a fitted module on a ship in a maintenance bay) keeps it.
//
// Nesting was not observed at all in 1,000 Caldari militia losses, so this path
// is defensive rather than load-bearing. It is here because the alternative --
// recursing and then silently dropping everything found -- is worse than not
// recursing at all.
func flattenItems(items []ESIItem, out []ESIItem) []ESIItem {
	for _, item := range items {
		out = append(out, item)
		if len(item.Items) == 0 {
			continue
		}

		// Copy before rewriting flags: the caller's killmail is not ours to edit.
		children := make([]ESIItem, len(item.Items))
		copy(children, item.Items)
		for i := range children {
			if children[i].Flag == 0 {
				children[i].Flag = item.Flag
			}
		}
		out = flattenItems(children, out)
	}
	return out
}

// ItemDemandProfile aggregates destruction data for a single item type.
type ItemDemandProfile struct {
	TypeID         int32   `json:"type_id"`
	TypeName       string  `json:"type_name"`
	Category       string  `json:"category"` // "ship", "module", "ammo", "drone"
	TotalDestroyed int64   `json:"total_destroyed"`
	KillmailCount  int     `json:"killmail_count"`
	AvgPerKillmail float64 `json:"avg_per_killmail"`
	EstDailyDemand float64 `json:"est_daily_demand"`
}

// RegionDemandProfile contains aggregated fitting demand data for a region.
type RegionDemandProfile struct {
	RegionID      int32
	SampledKills  int
	TotalKills24h int
	Items         map[int32]*ItemDemandProfile
	UpdatedAt     time.Time
}

// EVE inventory category ids, from the SDE. These are what actually classify a
// killmail item; the flag only says where it was sitting.
const (
	sdeCategoryCelestial = 2
	sdeCategoryShip      = 6
	sdeCategoryModule    = 7
	sdeCategoryCharge    = 8
	sdeCategoryDrone     = 18
	sdeCategoryImplant   = 20
	sdeCategoryFighter   = 87
)

// chargeMaxVolumeM3 splits the Charge category into the two things the rest of
// the pipeline treats differently: small rounds fired by the handful, and bulky
// charges that are not.
//
// Volume cannot classify an item — Cap Booster 800 is 32 m3 and an Electron Bomb
// 75 m3, both Charges, while a 50mm Steel Plate is 5 m3 and a Module — but
// inside a single category it separates ammunition (0.0025-0.1 m3: missiles,
// hybrid charges, frequency crystals, scripts, nanite paste) from cap boosters,
// probes and bombs. Only the former gets the combat-consumption multiplier,
// because only the former is fired continuously through a fight.
const chargeMaxVolumeM3 = 0.1

// killmailAccum collects one item type's destruction across killmails, keeping
// the distribution rather than running a total.
//
// The distribution is what makes winsorizing possible, and the per-kill
// distribution needs it: in the sample this was calibrated against, a single
// loss accounted for 32-42% of a type's entire destroyed count. Summing as we
// go would bake that in with no way to see it afterwards.
//
// It is held as a value->count histogram rather than one entry per killmail
// because contributions are small integers that cluster hard -- a module is
// nearly always 1, ammo counts repeat -- so distinct values per type collapse
// from thousands to a handful. That is not a space optimization for its own
// sake: it is what stops retained memory from scaling with the window, which is
// what makes a 90-day walk affordable. A quarter of Caldari losses is ~69,000
// killmails, and one entry each would be tens of megabytes of int64 before any
// of it was summed.
//
// Nothing is approximated by the change. Nearest-rank percentile over a multiset
// is exactly recoverable from the histogram, and the winsorized total is exactly
// sum of min(value, ceiling) x count.
type killmailAccum struct {
	name     string
	category string
	counts   map[int64]int
	kills    int
}

// add records one killmail's contribution for this type.
func (a *killmailAccum) add(destroyed int64) {
	if a.counts == nil {
		// Four is the common shape: most types show one or two distinct
		// contributions however long the window is.
		a.counts = make(map[int64]int, 4)
	}
	a.counts[destroyed]++
	a.kills++
}

// profile collapses the accumulated distribution into an ItemDemandProfile.
//
// With winsorize set, every contribution is capped at the p90 across the
// killmails that carried this type, so one ammo barge cannot manufacture demand
// for a type nobody else is shooting. AvgPerKillmail and EstDailyDemand are left
// to the caller, which is the only thing that knows the window and the scaling.
func (a *killmailAccum) profile(typeID int32, winsorize bool) *ItemDemandProfile {
	ceiling := int64(math.MaxInt64)
	if winsorize {
		ceiling = a.percentile(90)
	}

	var total int64
	for value, n := range a.counts {
		if value > ceiling {
			value = ceiling
		}
		total += value * int64(n)
	}

	return &ItemDemandProfile{
		TypeID:         typeID,
		TypeName:       a.name,
		Category:       a.category,
		TotalDestroyed: total,
		KillmailCount:  a.kills,
	}
}

// percentile returns the nearest-rank percentile of the accumulated
// contributions -- the same value a sorted slice of one entry per killmail would
// give, read off the histogram's cumulative counts instead. Only the distinct
// values are sorted, so the cost is a function of how varied the contributions
// are rather than of how many killmails there were.
func (a *killmailAccum) percentile(pct int) int64 {
	if a.kills == 0 || len(a.counts) == 0 {
		return 0
	}

	values := make([]int64, 0, len(a.counts))
	for value := range a.counts {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })

	rank := (pct*a.kills + 99) / 100 // ceil(pct% of n), 1-based
	if rank < 1 {
		rank = 1
	}
	if rank > a.kills {
		rank = a.kills
	}

	cumulative := 0
	for _, value := range values {
		cumulative += a.counts[value]
		if cumulative >= rank {
			return value
		}
	}
	return values[len(values)-1] // unreachable: cumulative ends at a.kills
}

// typeFacts looks up what the classifier needs about a type, tolerating a
// missing SDE so analysis still runs on ids alone. A zero categoryID means
// unknown, which sends categorizeItem down its flag-based fallback.
func typeFacts(sdeData *sde.Data, typeID int32) (name string, volume float64, categoryID int32) {
	if sdeData == nil {
		return "", 0, 0
	}
	t, ok := sdeData.Types[typeID]
	if !ok {
		return "", 0, 0
	}
	return t.Name, t.Volume, t.CategoryID
}

// accumulateKillmail folds one killmail into acc: the hull from
// victim.ship_type_id, then every destroyed item, including those nested inside
// containers and bays.
func accumulateKillmail(acc map[int32]*killmailAccum, km *ESIKillmail, sdeData *sde.Data) {
	if km == nil {
		return
	}

	// Sum within the killmail before recording it. One type can appear several
	// times on a single loss -- the same charge loaded in two launchers, or in
	// cargo as well as a slot -- and treating those as separate killmails would
	// inflate KillmailCount, which is the "how many losses actually carried
	// this" signal the shippability gate reads.
	type entry struct {
		name      string
		category  string
		destroyed int64
	}
	perKill := make(map[int32]*entry)

	add := func(typeID int32, name, category string, destroyed int64) {
		if e, ok := perKill[typeID]; ok {
			e.destroyed += destroyed
			return
		}
		perKill[typeID] = &entry{name: name, category: category, destroyed: destroyed}
	}

	if shipID := km.Victim.ShipTypeID; shipID > 0 {
		name, _, _ := typeFacts(sdeData, shipID)
		add(shipID, name, "ship", 1)
	}

	for _, item := range flattenItems(km.Victim.Items, nil) {
		destroyed := int64(item.QuantityDestroyed)
		if destroyed <= 0 {
			continue
		}

		name, volume, categoryID := typeFacts(sdeData, item.TypeID)
		category := categorizeItem(categoryID, volume, item.Flag, destroyed)
		if category == "" {
			continue
		}
		add(item.TypeID, name, category, destroyed)
	}

	for typeID, e := range perKill {
		a, ok := acc[typeID]
		if !ok {
			a = &killmailAccum{name: e.name, category: e.category}
			acc[typeID] = a
		}
		a.add(e.destroyed)
	}
}

// categorizeItem classifies a killmail item. categoryID is the SDE inventory
// category, volume the packaged unit volume, flag the inventory location, and
// quantity the stack size destroyed.
//
// The SDE category decides it wherever we have one, because the flag cannot and
// volume must not. The flag says where a thing was sitting, and a charge loaded
// into a launcher sits in that launcher's slot — which is why every loaded
// charge in the game used to be counted as a module. Volume is worse: Cap
// Booster 800 is 32 m3 and an Electron Bomb 75 m3, both Charges, while a 50mm
// Steel Plate is 5 m3 and a Module.
//
// flag and quantity are the fallback for a type the SDE does not have, and only
// then. See: https://docs.esi.evetech.net/docs/asset_location_flag
func categorizeItem(categoryID int32, volume float64, flag int32, quantity int64) string {
	switch categoryID {
	case sdeCategoryShip:
		return "ship"
	case sdeCategoryModule:
		return "module"
	case sdeCategoryDrone, sdeCategoryFighter:
		return "drone"
	case sdeCategoryCharge:
		if volume > 0 && volume > chargeMaxVolumeM3 {
			return "consumable" // cap boosters, probes, bombs
		}
		return "ammo"
	case sdeCategoryImplant, sdeCategoryCelestial:
		// Implants are pod losses, not ship restocks, and a container is
		// packaging. Neither is demand this tool supplies.
		return ""
	case 0:
		return categorizeItemByFlag(volume, flag, quantity)
	}
	return ""
}

// categorizeItemByFlag is the no-SDE fallback: guess from where the item sat and
// how many of it there were. A slot holds exactly one module, and killmails
// group entries by (flag, type), so a stack of more than one under a slot flag
// is always what that module consumes.
//
// Cargo is unclassifiable this way — a hold holds anything — so it is dropped
// rather than guessed at.
func categorizeItemByFlag(volume float64, flag int32, quantity int64) string {
	switch {
	// High slots (11-18), Medium slots (19-26), Low slots (27-34), Rig slots (92-99), Subsystem (125-132)
	case (flag >= 11 && flag <= 34) || (flag >= 92 && flag <= 99) || (flag >= 125 && flag <= 132):
		if quantity > 1 {
			return "ammo"
		}
		if volume > 0 && volume <= chargeMaxVolumeM3 {
			return "ammo" // a charge down to its last round
		}
		return "module"
	// Drone bay (87), Fighter bay (158-162)
	case flag == 87 || (flag >= 158 && flag <= 162):
		return "drone"
	default:
		return ""
	}
}

// AnalyzeRegionFittings fetches recent killmails and aggregates destroyed items.
//
// esiClient is retained for call-site compatibility and is no longer used:
// zkillboard's list response embeds each killmail's items, so the per-kill ESI
// fan-out this function used to run has been removed.
func (d *DemandAnalyzer) AnalyzeRegionFittings(regionID int32, esiClient *esi.Client, sdeData *sde.Data, maxKillmails int) (*RegionDemandProfile, error) {
	if maxKillmails <= 0 {
		maxKillmails = 100
	}

	// Get recent killmails from zkillboard (last 24 hours)
	recentKills, err := d.client.GetRecentKillmails(regionID, 86400)
	if err != nil {
		return nil, fmt.Errorf("get recent kills: %w", err)
	}

	totalKills24h := len(recentKills)
	if totalKills24h == 0 {
		return &RegionDemandProfile{
			RegionID:      regionID,
			SampledKills:  0,
			TotalKills24h: 0,
			Items:         make(map[int32]*ItemDemandProfile),
			UpdatedAt:     time.Now(),
		}, nil
	}

	// Sample up to maxKillmails (most recent first — zkillboard returns newest first)
	if len(recentKills) > maxKillmails {
		recentKills = recentKills[:maxKillmails]
	}

	logger.Info("Demand", fmt.Sprintf("Analyzing %d killmails for region %d (total 24h: %d)...", len(recentKills), regionID, totalKills24h))

	// Aggregate destroyed items. The killmails already carry their items, so
	// there is nothing left to fetch.
	acc := make(map[int32]*killmailAccum)
	sampledCount := 0
	for i := range recentKills {
		accumulateKillmail(acc, &recentKills[i], sdeData)
		sampledCount++
	}

	items := make(map[int32]*ItemDemandProfile, len(acc))
	for typeID, a := range acc {
		items[typeID] = a.profile(typeID, false)
	}

	// Calculate per-killmail averages and extrapolate daily demand
	scaleFactor := 1.0
	if sampledCount > 0 && totalKills24h > sampledCount {
		scaleFactor = float64(totalKills24h) / float64(sampledCount)
	}

	// The window here is exactly 24 hours, so the sample-extrapolation factor is
	// already a per-day scale.
	finalizeProfiles(items, scaleFactor)

	logger.Success("Demand", fmt.Sprintf("Region %d: analyzed %d killmails, found %d unique items", regionID, sampledCount, len(items)))

	return &RegionDemandProfile{
		RegionID:      regionID,
		SampledKills:  sampledCount,
		TotalKills24h: totalKills24h,
		Items:         items,
		UpdatedAt:     time.Now(),
	}, nil
}
