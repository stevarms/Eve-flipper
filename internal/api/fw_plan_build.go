package api

import (
	"context"
	"fmt"
	"sort"

	"eve-flipper/internal/db"
	"eve-flipper/internal/engine"
	"eve-flipper/internal/esi"
	"eve-flipper/internal/gankcheck"
	"eve-flipper/internal/sde"
	"eve-flipper/internal/zkillboard"
)

// fw_plan_build.go -- the two fetch-heavy passes behind a plan, split out of
// fw_plan.go so the orchestration reads as a sequence rather than as a wall.
//
// Both share one rule, and it is the reason they are not simpler: a book that
// did not load must never be reported as a book with nothing in it. An empty
// destination book reads as an empty market -- no competition, infinite markup
// headroom, every item a gap. That is the single most dangerous failure mode in
// this feature, because it looks exactly like the opportunity the tool exists to
// find. So every fetch failure here is named in a warning and, where it makes
// the answer unsafe rather than merely thinner, it fails the request outright.

// fwStagingDepth fetches the sell book for every region the ring touches and
// reduces it to one depth reading per station.
//
// One fetch per region, not per candidate: forty-odd candidates across four
// regions is four calls. A region that fails costs its candidates their order
// counts and their FW-item overlap, which would silently rank them last -- a
// deep market mistaken for a dead one -- so each failure is named and lists the
// candidates it affected.
func (s *Server) fwStagingDepth(
	candidates []engine.FWStagingCandidate,
	destroyed map[int32]bool,
	sdeData *sde.Data,
) (map[int64]engine.StagingDepth, []string) {
	regions, byRegion, warnings := fwDepthRegions(candidates, sdeData)

	var orders []esi.MarketOrder
	for _, region := range regions {
		regionOrders, err := s.esi.FetchRegionOrders(region, "sell")
		if err != nil {
			warnings = append(warnings, fmt.Sprintf(
				"%s did not load, so %s rank with no market depth rather than with none: %v",
				fwRegionLabel(sdeData, region), joinAnd(byRegion[region]), err))
			continue
		}
		orders = append(orders, regionOrders...)
	}
	if len(orders) == 0 {
		return nil, warnings
	}
	return engine.MeasureStagingDepth(orders, destroyed), warnings
}

// fwDepthRegions decides which regions to fetch and which to give up on.
//
// Split out of the fetching because the cap is the rule worth pinning: it is the
// difference between one request and a scan of the cluster, and the regions it
// drops have to be named. A candidate silently measured with no depth ranks last,
// which reads as a dead market rather than an unmeasured one.
//
// Regions come back in ascending id order so the same ring produces the same
// fetch sequence, and so the cap always drops the same regions rather than
// whichever ones a map happened to iterate last.
func fwDepthRegions(
	candidates []engine.FWStagingCandidate,
	sdeData *sde.Data,
) (regions []int32, byRegion map[int32][]string, warnings []string) {
	if len(candidates) == 0 {
		return nil, nil, nil
	}
	byRegion = make(map[int32][]string)
	for _, c := range candidates {
		region := c.RegionID
		if region == 0 && sdeData != nil {
			if sys, ok := sdeData.Systems[c.SystemID]; ok && sys != nil {
				region = sys.RegionID
			}
		}
		if region == 0 {
			continue
		}
		if _, seen := byRegion[region]; !seen {
			regions = append(regions, region)
		}
		byRegion[region] = append(byRegion[region], c.StationName)
	}
	sort.Slice(regions, func(i, j int) bool { return regions[i] < regions[j] })

	if len(regions) > fwMaxDepthRegions {
		dropped := regions[fwMaxDepthRegions:]
		names := make([]string, 0, len(dropped))
		for _, region := range dropped {
			names = append(names, fwRegionLabel(sdeData, region))
		}
		warnings = append(warnings, fmt.Sprintf(
			"the ring spans %d regions and only %d were measured; %s were not, so their stations rank with no market depth -- lower the radius or exclude them",
			len(regions), fwMaxDepthRegions, joinAnd(names)))
		regions = regions[:fwMaxDepthRegions]
	}
	return regions, byRegion, warnings
}

// fwPlanDestinationPass fills everything that needs a chosen station: the two
// price books, the markup ladder, the gap table, the shipping list and the
// supply route.
func (s *Server) fwPlanDestinationPass(
	ctx context.Context,
	userID string,
	campaign *db.FWCampaign,
	plan *fwPlanPayload,
	demand *zkillboard.MilitiaDemandProfile,
	longDemand *zkillboard.MilitiaDemandProfile,
	destroyed map[int32]bool,
	fwSystems []esi.FWSystem,
	sdeData *sde.Data,
) error {
	dest, err := fwResolveDestination(sdeData, campaign.DestStationID, campaign.MilitiaFactionID)
	if err != nil {
		return err
	}
	// Whatever the ring already worked out about this station is better than
	// recomputing it: same geometry, same source, one BFS.
	for _, c := range plan.Ring {
		if c.StationID == dest.StationID {
			dest.JumpsToFront = c.JumpsToFront
			dest.IsBulwark = c.IsBulwark
			plan.Route = &fwPlanRoute{
				Jumps:        c.JumpsFromSource,
				LowsecJumps:  c.LowsecJumpsFromSource,
				HighsecRoute: c.HighsecRoute,
			}
			break
		}
	}
	plan.Destination = dest
	if plan.Route == nil {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf(
			"%s is not in the current ring, so its distance to the front and its supply route are unmeasured; it may be pinned from an older radius",
			dest.StationName))
	}

	// The two books. The destination's is load-bearing: without it the tool
	// cannot tell a gap from a stocked shelf, and guessing "empty" would turn
	// every item into a gap priced at the ceiling. So this one fails the request.
	localOrders, err := s.fwStationSellBook(dest.RegionID, dest.StationID)
	if err != nil {
		return fmt.Errorf("the destination's sell book could not be read, and an unread book is not an empty one -- "+
			"every item would read as a gap with no competition: %w", err)
	}

	sourceRegion, sourceStationName := fwStationRegion(sdeData, campaign.SourceStationID)
	sourceOrders, err := s.fwStationSellBook(sourceRegion, campaign.SourceStationID)
	if err != nil {
		return fmt.Errorf("the source hub's sell book could not be read, so nothing has a cost basis and no floor price can be computed: %w", err)
	}
	// NPC seeds are deliberately kept in the source book. A 365-day seed at Jita
	// is a price you can actually pay, which is exactly what landed cost means --
	// unlike at the destination, where a seed is competition that will never move.
	sourceBest := fwBestSellByType(sourceOrders, false)
	if len(sourceBest) == 0 {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf(
			"%s has no sell orders at all, which is almost certainly a stale cache rather than an empty hub",
			sourceStationName))
	}

	plan.Ladder = engine.DeriveMarkupLadder(dest.StationID, localOrders, sourceBest, destroyed)
	if !fwLadderCalibrated(plan.Ladder) {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf(
			"%s and the source hub overlap on too few destroyed types to derive a markup ladder, so prices fall back to the category ceilings",
			dest.StationName))
	}

	// The gap table.
	localByType := make(map[int32][]esi.MarketOrder, len(localOrders))
	for _, o := range localOrders {
		localByType[o.TypeID] = append(localByType[o.TypeID], o)
	}

	items, skippedNoPrice := fwSupplyItems(demand, longDemand, sourceBest, localByType, sdeData,
		fwTypeOverrideSet(campaign.ExcludedTypes), fwTypeOverrideSet(campaign.IncludedTypes))
	if (demand != nil || longDemand != nil) && len(items) == 0 {
		plan.Warnings = append(plan.Warnings,
			"nothing the warzone is destroying has a live sell order at the source hub, so there is nothing to ship")
	}
	if skippedNoPrice > 0 {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf(
			"%d destroyed types have no sell order at the source hub and were dropped as unbuyable",
			skippedNoPrice))
	}

	cfg := campaign.SupplyConfig()
	// The campaign's setting, as the plan was actually able to apply it -- the
	// long window may have failed to fetch, and fw_plan.go has already said so.
	cfg.SizeAgainstLong = plan.Demand.SizeAgainst == engine.FWSizedByLong
	cfg.Ladder = plan.Ladder
	feeProfile, feeNote := s.fwFeeRates(userID, campaign)
	cfg.SalesTaxPercent, cfg.BrokerFeePercent = feeProfile.SalesTaxPercent, feeProfile.BrokerFeePercent
	plan.Fees = &feeProfile
	if feeNote != "" {
		plan.Warnings = append(plan.Warnings, feeNote)
	}
	plan.Rows = engine.BuildFWSupplyPlan(items, cfg)
	if plan.Rows == nil {
		plan.Rows = []engine.FWSupplyRow{}
	}

	plan.Shipment = engine.BuildFWShipment(plan.Rows, campaign.ShipmentConfig(plan.Budget.HeadroomISK))
	if plan.Budget.HeadroomISK <= 0 && campaign.BudgetISK > 0 {
		plan.Warnings = append(plan.Warnings,
			"the budget is fully committed, so the shipping list is empty until something sells or is pulled")
	}

	plan.Occupancy = s.fwOccupancyNear(sdeData, fwSystems, dest.SystemID, plan.RingRadius)
	s.fwCheckSupplyRoute(ctx, sdeData, plan, campaign.SourceStationID, dest.SystemID)
	return nil
}

// fwResolveDestination names the chosen station from the SDE.
//
// A station the SDE does not know is fatal rather than a warning: its system,
// region and security are what every later fetch is addressed by, so carrying on
// would mean fetching the wrong region's book and calling it the destination's.
func fwResolveDestination(sdeData *sde.Data, stationID int64, militiaFactionID int32) (*fwPlanDestination, error) {
	station, ok := sdeData.Stations[stationID]
	if !ok || station == nil {
		return nil, fmt.Errorf("destination station %d is not in the SDE, so its region and security are unknown", stationID)
	}
	out := &fwPlanDestination{
		StationID:   stationID,
		StationName: station.Name,
		SystemID:    station.SystemID,
		IsBulwark:   esi.BulwarkSystems[militiaFactionID] == station.SystemID,
	}
	if sys, okSys := sdeData.Systems[station.SystemID]; okSys && sys != nil {
		out.SystemName = sys.Name
		out.RegionID = sys.RegionID
		out.Security = sys.Security
	}
	if out.RegionID == 0 {
		return nil, fmt.Errorf("%s has no region in the SDE, so its market cannot be fetched", station.Name)
	}
	return out, nil
}

// fwStationSellBook fetches one region's sell orders and keeps the station's.
//
// The region fetch is what ESI offers and what the shared order cache is keyed
// on, so narrowing afterwards costs nothing beyond the filter.
func (s *Server) fwStationSellBook(regionID int32, stationID int64) ([]esi.MarketOrder, error) {
	if regionID == 0 || stationID <= 0 {
		return nil, fmt.Errorf("station %d has no region", stationID)
	}
	orders, err := s.esi.FetchRegionOrders(regionID, "sell")
	if err != nil {
		return nil, err
	}
	out := make([]esi.MarketOrder, 0, 256)
	for _, o := range orders {
		if o.LocationID == stationID && !o.IsBuyOrder {
			out = append(out, o)
		}
	}
	return out, nil
}

// fwStationRegion resolves a station's region and name, tolerating a station the
// SDE does not carry -- the source hub's absence is survivable in a way the
// destination's is not, because the caller turns an empty price map into a
// warning rather than a wrong answer.
func fwStationRegion(sdeData *sde.Data, stationID int64) (int32, string) {
	station, ok := sdeData.Stations[stationID]
	if !ok || station == nil {
		return 0, fmt.Sprintf("station %d", stationID)
	}
	if sys, okSys := sdeData.Systems[station.SystemID]; okSys && sys != nil {
		return sys.RegionID, station.Name
	}
	return 0, station.Name
}

// fwBestSellByType reduces a book to the cheapest sell price per type.
func fwBestSellByType(orders []esi.MarketOrder, excludeNPCSeeded bool) map[int32]float64 {
	best := make(map[int32]float64, 512)
	for _, o := range orders {
		if o.IsBuyOrder || o.Price <= 0 || o.VolumeRemain <= 0 {
			continue
		}
		if excludeNPCSeeded && o.IsNPCSeeded() {
			continue
		}
		if current, seen := best[o.TypeID]; !seen || o.Price < current {
			best[o.TypeID] = o.Price
		}
	}
	return best
}

// fwLadderCalibrated reports whether any band has enough samples to price from.
// A ladder of entirely uncalibrated bands is not a measurement of the station,
// and the user should know the prices came from the ceilings instead.
func fwLadderCalibrated(ladder engine.MarkupLadder) bool {
	for _, band := range ladder.Bands {
		if band.Calibrated() {
			return true
		}
	}
	return false
}

// fwSupplyItems turns the demand passes into the engine's input, and returns how
// many destroyed types had to be dropped for having no price.
//
// The candidate set is the UNION of the two windows, not their intersection. A
// staple that happened not to die this week is precisely what the long window was
// added to find, and intersecting would hide exactly the item the feature exists
// for; a spike that only this week saw is the other half of the same comparison.
// Either window measuring something is enough to earn a row, and the row carries
// both numbers so the reader can see which one is speaking.
//
// The thin-evidence gate is deliberately NOT applied here. The engine marks a row
// Shippable or not and keeps it either way, so an item seen on two losses appears
// with its counts rather than vanishing -- dropping it here would hide exactly the
// thinness the gap table exists to show. Only an item with no source price is
// dropped, because it cannot be bought at any quantity.
//
// excludedTypes is the one exception to all of the above: it is checked first,
// before demand or price, because omitting a type is a different ask from
// showing it thin or unpriceable. includedTypes carries the opposite judgment
// through onto the item so the engine can relax its own caution for it -- see
// FWSupplyItem.Included for exactly what that does and does not unlock.
func fwSupplyItems(
	demand *zkillboard.MilitiaDemandProfile,
	longDemand *zkillboard.MilitiaDemandProfile,
	sourceBest map[int32]float64,
	localByType map[int32][]esi.MarketOrder,
	sdeData *sde.Data,
	excludedTypes map[int32]bool,
	includedTypes map[int32]bool,
) ([]engine.FWSupplyItem, int) {
	short := fwDemandItems(demand)
	long := fwDemandItems(longDemand)
	if len(short) == 0 && len(long) == 0 {
		return nil, 0
	}

	typeIDs := make([]int32, 0, len(short)+len(long))
	for typeID := range short {
		typeIDs = append(typeIDs, typeID)
	}
	for typeID := range long {
		if _, both := short[typeID]; !both {
			typeIDs = append(typeIDs, typeID)
		}
	}
	// Sorted so a regenerated plan with the same inputs produces the same row
	// order. Map iteration would reshuffle a table the user is reading down.
	sort.Slice(typeIDs, func(i, j int) bool { return typeIDs[i] < typeIDs[j] })

	items := make([]engine.FWSupplyItem, 0, len(typeIDs))
	skipped := 0
	for _, typeID := range typeIDs {
		// Excluded before anything else is even looked up: the ask was to omit
		// the type, not to badge it as covered or unpriceable.
		if excludedTypes[typeID] {
			continue
		}
		profile, longProfile := short[typeID], long[typeID]
		// A rate of zero in one window is not a reason to drop the type; a rate of
		// zero in both is, because there is nothing to size against either way.
		// Included does not change this -- it unlocks a candidate that already
		// has a real rate, it does not invent one for a type nothing measures.
		if fwDailyDemand(profile) <= 0 && fwDailyDemand(longProfile) <= 0 {
			continue
		}
		price, priced := sourceBest[typeID]
		if !priced || price <= 0 {
			skipped++
			continue
		}
		item := engine.FWSupplyItem{
			TypeID:       typeID,
			JitaBestSell: price,
			LocalOrders:  localByType[typeID],
			Included:     includedTypes[typeID],
		}
		// Naming prefers the short window only because it is the one that always
		// exists; the two agree, since both come from the same SDE lookup.
		for _, p := range []*zkillboard.ItemDemandProfile{profile, longProfile} {
			if p == nil {
				continue
			}
			if item.TypeName == "" {
				item.TypeName = p.TypeName
			}
			if item.Category == "" {
				item.Category = p.Category
			}
		}
		if profile != nil {
			item.DailyDestroyed = profile.EstDailyDemand
			item.KillsWithItem = profile.KillmailCount
		}
		if longProfile != nil {
			item.DailyDestroyedLong = longProfile.EstDailyDemand
			item.KillsWithItemLong = longProfile.KillmailCount
		}
		if t, ok := sdeData.Types[typeID]; ok && t != nil {
			item.VolumeM3 = t.Volume
			if item.TypeName == "" {
				item.TypeName = t.Name
			}
		}
		items = append(items, item)
	}
	return items, skipped
}

// fwDemandItems is the profile's item map, or nil for a pass that did not run.
func fwDemandItems(profile *zkillboard.MilitiaDemandProfile) map[int32]*zkillboard.ItemDemandProfile {
	if profile == nil {
		return nil
	}
	return profile.Items
}

func fwDailyDemand(item *zkillboard.ItemDemandProfile) float64 {
	if item == nil {
		return 0
	}
	return item.EstDailyDemand
}

// fwTypeOverrideSet reduces a campaign's named type-override list to the set
// fwSupplyItems actually tests against. The name each override carries is a
// UI concern -- for a settings-panel chip that has to say what was overridden
// without a type it may never see as a row again -- and has nothing to do with
// membership, so it is dropped here rather than threaded any further.
func fwTypeOverrideSet(overrides []db.FWTypeOverride) map[int32]bool {
	if len(overrides) == 0 {
		return nil
	}
	out := make(map[int32]bool, len(overrides))
	for _, o := range overrides {
		out[o.TypeID] = true
	}
	return out
}

// fwFeeRates resolves the sell-side rates the floor prices are computed with,
// and returns the full profile -- not just the two numbers -- so the plan can
// show where they came from rather than only warning when they didn't.
//
// The seller's own skills where the seller is a character: Accounting and Broker
// Relations are worth several percent, and a floor computed at the untrained 8%
// would refuse trades that are in fact profitable -- exactly the wrong error for
// a tool whose whole job is finding thin margins worth taking.
//
// A corporation has no skills of its own. Its fees follow whichever character
// issues each order, and at plan time nobody has issued anything, so it falls
// back to the configured rates and says so rather than quoting a floor it cannot
// stand behind.
func (s *Server) fwFeeRates(userID string, campaign *db.FWCampaign) (profile FeeProfile, note string) {
	sellerCharacterID := int64(0)
	if campaign.SellerOwnerKind == orderOwnerKindCharacter {
		sellerCharacterID = campaign.SellerOwnerID
	}
	profile = s.resolveFeeProfile(userID, sellerCharacterID)
	if sellerCharacterID == 0 {
		note = "the seller has no skills to read, so floor prices use your configured rates of " +
			fmt.Sprintf("%.2f%% sales tax and %.2f%% broker fee", profile.SalesTaxPercent, profile.BrokerFeePercent)
	}
	return profile, note
}

// fwOccupancyNear snapshots the contested systems around the destination.
//
// Radius is the ring's own, because that is the definition of "near enough to
// stage here": the systems whose militia buys at this station are the ones the
// ring considered it reachable from. If one of them flips, the customers change.
func (s *Server) fwOccupancyNear(
	sdeData *sde.Data,
	fwSystems []esi.FWSystem,
	destSystemID int32,
	radius int,
) []fwOccupancy {
	if destSystemID == 0 || sdeData.Universe == nil || len(fwSystems) == 0 {
		return nil
	}
	within := sdeData.Universe.SystemsWithinRadius(destSystemID, radius)
	if len(within) == 0 {
		return nil
	}
	out := make([]fwOccupancy, 0, 8)
	for _, sys := range fwSystems {
		if _, near := within[sys.SolarSystemID]; !near {
			continue
		}
		entry := fwOccupancy{
			SystemID:          sys.SolarSystemID,
			OccupierFactionID: sys.OccupierFactionID,
			Contested:         sys.Contested,
		}
		if s, ok := sdeData.Systems[sys.SolarSystemID]; ok && s != nil {
			entry.SystemName = s.Name
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SystemID < out[j].SystemID })
	return out
}

// fwCheckSupplyRoute annotates the haul with gankcheck's verdict.
//
// Best-effort and last, because it is the slowest thing here: gankcheck reads
// recent kills per system on the route. A failure leaves Checked false, which is
// why that field exists -- an unchecked route and a clean one must not render the
// same, or the tool would be quietly promising safety it never looked for.
func (s *Server) fwCheckSupplyRoute(
	ctx context.Context,
	sdeData *sde.Data,
	plan *fwPlanPayload,
	sourceStationID int64,
	destSystemID int32,
) {
	if plan.Route == nil || s.ganker == nil || destSystemID == 0 {
		return
	}
	station, ok := sdeData.Stations[sourceStationID]
	if !ok || station == nil || station.SystemID == destSystemID {
		return
	}
	select {
	case <-ctx.Done():
		return
	default:
	}

	danger, err := s.ganker.CheckRoute(station.SystemID, destSystemID, 0)
	if err != nil {
		plan.Warnings = append(plan.Warnings, "the route could not be checked for recent kills: "+err.Error())
		return
	}
	plan.Route.Checked = true
	plan.Route.Verdict, plan.Route.Hot = fwWorstDanger(danger)
}

// fwWorstDanger reduces gankcheck's per-system readings to one verdict and the
// systems that earned it.
//
// Red outranks yellow outranks green regardless of order, which is the whole
// point: a route whose one red system happens to be listed first and whose last
// nine are green must not read as green. An empty reading is green because
// gankcheck answered and found nothing -- the case where it did not answer is
// Checked false, decided by the caller.
func fwWorstDanger(danger []gankcheck.SystemDanger) (verdict string, hot []string) {
	verdict = "green"
	for _, d := range danger {
		switch d.DangerLevel {
		case "red":
			verdict = "red"
		case "yellow":
			if verdict != "red" {
				verdict = "yellow"
			}
		default:
			continue
		}
		hot = append(hot, fmt.Sprintf("%s (%s, %d kills)",
			fwSystemLabel(d.SystemName, d.SystemID), d.DangerLevel, d.KillsTotal))
	}
	return verdict, hot
}

// fwRegionLabel names a region for a warning, falling back to its id.
func fwRegionLabel(sdeData *sde.Data, regionID int32) string {
	if sdeData != nil {
		if r, ok := sdeData.Regions[regionID]; ok && r != nil && r.Name != "" {
			return r.Name
		}
	}
	return fmt.Sprintf("region %d", regionID)
}

// joinAnd renders a list the way a sentence needs it, and truncates a long one
// rather than printing forty station names into a warning.
func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return "nothing"
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	}
	if len(items) > 4 {
		return fmt.Sprintf("%s, %s, %s and %d others",
			items[0], items[1], items[2], len(items)-3)
	}
	out := ""
	for i, item := range items[:len(items)-1] {
		if i > 0 {
			out += ", "
		}
		out += item
	}
	return out + " and " + items[len(items)-1]
}
