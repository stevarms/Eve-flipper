package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"eve-flipper/internal/db"
	"eve-flipper/internal/engine"
	"eve-flipper/internal/esi"
	"eve-flipper/internal/zkillboard"
)

// fw_plan.go -- generating a campaign's plan: the staging ring, the demand pass,
// the gap table and the shipping list.
//
// Every piece of the arithmetic already exists in internal/engine. This file is
// the orchestration: it decides what to fetch, in what order, and what to do
// when a fetch fails. That last part is most of the interesting content.
//
// GET reads the cache; POST generates. The split is not politeness about HTTP
// verbs. Generating means a zkill paging walk over a week of killmails plus a
// sell-order fetch for every region the ring touches, so a GET that regenerated
// would do all of it every time the tab was opened. The plan is a cache -- the
// lots are the record -- so serving a stale one and saying how stale is right.
//
// Two rules run through the whole file.
//
// A fetch that failed is a warning, never an absence. A region whose book did
// not load would otherwise read as a station with no competition, and a demand
// pass that returned nothing would read as a warzone that destroys nothing.
// Both look like opportunities. Every partial result here names what is missing.
//
// A campaign with no destination still gets a plan -- the ring alone. You cannot
// measure a gap at a station you have not chosen, and refusing to answer until
// one is chosen would mean the picker had nothing to show. The ring is the
// first panel and the first thing generated.

const (
	// fwDemandWindowSeconds is the demand sample: seven days, which is zkill's
	// maximum and what makes per-item rates stable. The existing WarTracker
	// analyzer samples 24h and at most 100 killmails, which is enough to rank
	// hulls and not enough to size an ammo order.
	fwDemandWindowSeconds = 604800

	// fwMaxDepthRegions bounds the sell-order fan-out behind the ring.
	//
	// A ring at radius 2 spans four to six regions in practice. The cap exists
	// so that a mis-set radius cannot turn one request into a scan of the
	// cluster; regions past it are dropped with a named warning rather than
	// quietly costing their candidates their depth scores.
	fwMaxDepthRegions = 8

	// fwPlanSchemaVersion is the shape of the cached payload.
	//
	// The plan is cached as JSON and read back into this struct, so a payload
	// written by an older build is missing whatever has been added since -- and a
	// missing number unmarshals to zero, which on screen is indistinguishable
	// from a real zero. That is not hypothetical: it is how the profit and margin
	// columns first came back empty. The arithmetic was right and the cached plan
	// simply predated the fields.
	//
	// Bump this whenever a field is added. A mismatch serves the plan with a
	// named warning rather than discarding it, because regenerating costs a week
	// of killmails and a page of region fetches while the ring and the gap table
	// are still true.
	//
	// A change that alters the *meaning* of an existing field is the other case
	// and must drop the cache instead: a plan that reads as current while
	// measuring something else is worse than no plan at all.
	fwPlanSchemaVersion = 3
)

// fwMilitiaNames labels a militia for a heading. Kept here rather than fetched:
// four strings that have not changed since 2008, against a universe-names call
// on every plan.
var fwMilitiaNames = map[int32]string{
	esi.MilitiaCaldari:  "Caldari State",
	esi.MilitiaMinmatar: "Minmatar Republic",
	esi.MilitiaAmarr:    "Amarr Empire",
	esi.MilitiaGallente: "Gallente Federation",
}

// fwPlanDestination is the chosen station, resolved for display.
type fwPlanDestination struct {
	StationID    int64   `json:"station_id"`
	StationName  string  `json:"station_name"`
	SystemID     int32   `json:"system_id"`
	SystemName   string  `json:"system_name"`
	RegionID     int32   `json:"region_id"`
	Security     float64 `json:"security"`
	IsBulwark    bool    `json:"is_bulwark"`
	JumpsToFront int     `json:"jumps_to_front"`
}

// fwPlanDemand is what the killmail pass saw, carried so a thin gap table can be
// read as thin evidence rather than a thin warzone.
type fwPlanDemand struct {
	WindowSeconds  int    `json:"window_seconds"`
	FetchedKills   int    `json:"fetched_kills"`
	InWarzoneKills int    `json:"in_warzone_kills"`
	Truncated      bool   `json:"truncated"`
	DestroyedTypes int    `json:"destroyed_types"`
	SampledAt      string `json:"sampled_at"`

	// The long window, all zero when none is configured. LongWindowSeconds is
	// what was asked for and LongCoveredSeconds what the walk actually spanned;
	// they differ when a calendar month hit zkillboard's page limit, and the gap
	// between them is the number that says how much to trust the second column.
	LongWindowSeconds  int     `json:"long_window_seconds"`
	LongCoveredSeconds float64 `json:"long_covered_seconds"`
	LongFetchedKills   int     `json:"long_fetched_kills"`
	LongInWarzoneKills int     `json:"long_in_warzone_kills"`
	LongDestroyedTypes int     `json:"long_destroyed_types"`
	LongTruncated      bool    `json:"long_truncated"`
	LongSampledAt      string  `json:"long_sampled_at,omitempty"`

	// SizeAgainst is which window drove the quantities: "short" or "long". It is
	// the campaign's setting as the plan actually applied it, which is not the
	// same thing -- asking to size against a window that failed to fetch sizes
	// against the other one.
	SizeAgainst string `json:"size_against"`
}

// fwPlanRoute is the supply run from the source hub, with gankcheck's verdict.
type fwPlanRoute struct {
	Jumps        int  `json:"jumps"`
	LowsecJumps  int  `json:"lowsec_jumps"`
	HighsecRoute bool `json:"highsec_route"`
	// Danger is gankcheck's per-system reading, and Verdict the worst of it. Both
	// are empty when the check could not run, which is why Checked exists: an
	// unchecked route and a clean one must not render the same.
	Checked bool     `json:"checked"`
	Verdict string   `json:"verdict,omitempty"`
	Hot     []string `json:"hot_systems,omitempty"`
}

// fwOccupancy is one contested system near the destination, as it stood when the
// plan was generated.
//
// This is the risk with no market signal. If the frontline moves, the militia
// that stages at the destination changes, and the demand the plan was sized
// against walks away -- with the book looking exactly the same until it does not.
// The snapshot is what a later read diffs against.
type fwOccupancy struct {
	SystemID          int32  `json:"system_id"`
	SystemName        string `json:"system_name"`
	OccupierFactionID int32  `json:"occupier_faction_id"`
	Contested         string `json:"contested"`
}

// fwPlanPayload is the whole generated plan, and the thing stored in the cache.
type fwPlanPayload struct {
	CampaignID int64 `json:"campaign_id"`
	// SchemaVersion is the payload shape this plan was written with. See
	// fwPlanSchemaVersion: it is what lets a read tell "this figure is zero"
	// apart from "the build that wrote this did not know the figure existed".
	SchemaVersion    int    `json:"schema_version"`
	GeneratedAt      string `json:"generated_at"`
	MilitiaFactionID int32  `json:"militia_faction_id"`
	MilitiaName      string `json:"militia_name"`

	Ring             []engine.FWStagingCandidate `json:"ring"`
	RingRadius       int                         `json:"ring_radius"`
	FrontlineSystems int                         `json:"frontline_systems"`

	// Destination is nil for a ring-only plan. Everything below it is nil or
	// empty in that case, and the UI shows the picker.
	Destination *fwPlanDestination `json:"destination,omitempty"`

	Demand fwPlanDemand        `json:"demand"`
	Ladder engine.MarkupLadder `json:"ladder"`

	Rows     []engine.FWSupplyRow `json:"rows"`
	Shipment engine.FWShipment    `json:"shipment"`
	Budget   engine.FWBudget      `json:"budget"`

	Route     *fwPlanRoute  `json:"route,omitempty"`
	Occupancy []fwOccupancy `json:"occupancy,omitempty"`

	// Fees is the sell-side rate pair the floor prices on every row were
	// actually computed with, and where it came from -- the same FeeProfile
	// every other realized-profit surface in the app reports. Nil for a
	// ring-only plan, since the destination pass that resolves it has not run.
	Fees *FeeProfile `json:"fees,omitempty"`

	Warnings []string `json:"warnings,omitempty"`
}

// fwPlanResponse wraps a plan with how old it is and what has changed under it.
type fwPlanResponse struct {
	HasPlan bool           `json:"has_plan"`
	Plan    *fwPlanPayload `json:"plan,omitempty"`

	GeneratedAt string  `json:"generated_at,omitempty"`
	AgeSeconds  float64 `json:"age_seconds,omitempty"`

	// OccupancyDrift is the frontline-moved warning: systems near the destination
	// whose occupier or contested state differs from when the plan was built. It
	// is computed on read rather than stored, because the whole point is to
	// compare the plan against now.
	OccupancyDrift []string `json:"occupancy_drift,omitempty"`

	Warnings []string `json:"warnings,omitempty"`
}

// handleFWCampaignPlan serves the cached plan.
// GET /api/auth/fw/campaigns/{id}/plan
//
// A campaign that has never been planned is not an error -- it is the state every
// campaign starts in -- so this answers 200 with HasPlan false rather than 404.
func (s *Server) handleFWCampaignPlan(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	campaign, ok := s.requireFWCampaign(w, r, userID)
	if !ok {
		return
	}

	payloadJSON, generatedAt, found := s.db.GetFWPlan(userID, campaign.CampaignID)
	if !found {
		writeJSON(w, fwPlanResponse{HasPlan: false})
		return
	}

	var plan fwPlanPayload
	if err := json.Unmarshal([]byte(payloadJSON), &plan); err != nil {
		// The cache is regenerable by definition, so a payload this build cannot
		// read is a reason to re-plan, not an error to show. Saying so beats
		// serving nothing and beats pretending the campaign was never planned.
		writeJSON(w, fwPlanResponse{
			HasPlan:  false,
			Warnings: []string{"the stored plan could not be read and needs regenerating: " + err.Error()},
		})
		return
	}

	out := fwPlanResponse{
		HasPlan:     true,
		Plan:        &plan,
		GeneratedAt: generatedAt,
		Warnings:    plan.Warnings,
	}
	if t, err := time.Parse(time.RFC3339, generatedAt); err == nil {
		out.AgeSeconds = time.Since(t).Seconds()
	}
	if w := fwStalePlanWarning(plan.SchemaVersion); w != "" {
		out.Warnings = append(out.Warnings, w)
	}
	out.OccupancyDrift = s.fwOccupancyDrift(r.Context(), plan)
	writeJSON(w, out)
}

// fwStalePlanWarning names a cached plan written by an older build, or returns
// "" when the payload is current.
//
// Serve it, but say so. The ring and the gap table are still true; what is
// missing is whatever has been added since, and those figures unmarshal to zero
// -- which on screen is a blank column that looks like a broken calculation
// rather than a stale cache. That is exactly how the profit and margin columns
// first read as empty.
//
// A payload predating the field itself decodes as version 0, which is the case
// that matters most and the one a `> 0` guard here would miss.
func fwStalePlanWarning(version int) string {
	if version >= fwPlanSchemaVersion {
		return ""
	}
	return fmt.Sprintf(
		"this plan was generated by an earlier version (plan format v%d, now v%d), so figures added since "+
			"will read as blank or zero -- regenerate the plan to fill them in",
		version, fwPlanSchemaVersion)
}

// fwOccupancyDrift compares the plan's occupancy snapshot against the live one.
//
// It is best-effort on purpose: the FW systems call is cached for thirty minutes
// and cheap, but if it fails the plan is still worth showing. Reporting no drift
// because the check could not run would be the wrong kind of quiet, so a failed
// check returns nothing and the caller's other warnings stand -- the drift list
// only ever contains things actually observed to have changed.
func (s *Server) fwOccupancyDrift(ctx context.Context, plan fwPlanPayload) []string {
	if len(plan.Occupancy) == 0 {
		return nil
	}
	systems, err := s.esi.FetchFWSystemsCached(ctx, s.fwSystems)
	if err != nil || len(systems) == 0 {
		return nil
	}
	live := make(map[int32]esi.FWSystem, len(systems))
	for _, sys := range systems {
		live[sys.SolarSystemID] = sys
	}
	return fwDiffOccupancy(plan.Occupancy, live)
}

// fwDiffOccupancy is the comparison itself, split from the fetch so it can be
// asserted: what changed, said in terms of what it costs the plan.
//
// An occupier change is the expensive one and gets the long sentence, because it
// is the only entry that means the demand behind the whole gap table may have
// left. A contested-state change is worth mentioning and nothing more. A system
// that has dropped out of the FW list entirely is reported as no longer
// contested rather than as missing data, which is what it is: the endpoint lists
// contested systems, so absence is the observation.
func fwDiffOccupancy(was []fwOccupancy, live map[int32]esi.FWSystem) []string {
	var drift []string
	for _, w := range was {
		now, still := live[w.SystemID]
		switch {
		case !still:
			drift = append(drift, fmt.Sprintf(
				"%s is no longer contested", fwSystemLabel(w.SystemName, w.SystemID)))
		case now.OccupierFactionID != w.OccupierFactionID:
			drift = append(drift, fmt.Sprintf(
				"%s is now held by %s -- the militia staging here has changed, so the demand this plan was sized against may have left with them",
				fwSystemLabel(w.SystemName, w.SystemID), fwMilitiaLabel(now.OccupierFactionID)))
		case now.Contested != w.Contested:
			drift = append(drift, fmt.Sprintf("%s went from %s to %s",
				fwSystemLabel(w.SystemName, w.SystemID), w.Contested, now.Contested))
		}
	}
	return drift
}

func fwSystemLabel(name string, id int32) string {
	if name != "" {
		return name
	}
	return fmt.Sprintf("system %d", id)
}

func fwMilitiaLabel(factionID int32) string {
	if name, ok := fwMilitiaNames[factionID]; ok {
		return name
	}
	if factionID == 0 {
		return "nobody"
	}
	return fmt.Sprintf("faction %d", factionID)
}

// handleFWCampaignPlanGenerate builds a fresh plan and caches it.
// POST /api/auth/fw/campaigns/{id}/plan
func (s *Server) handleFWCampaignPlanGenerate(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	campaign, ok := s.requireFWCampaign(w, r, userID)
	if !ok {
		return
	}

	plan, err := s.buildFWPlan(r.Context(), userID, campaign)
	if err != nil {
		writeStatusError(w, err)
		return
	}

	payload, err := json.Marshal(plan)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encode plan: "+err.Error())
		return
	}
	if err := s.db.SaveFWPlan(userID, campaign.CampaignID, plan.GeneratedAt, string(payload)); err != nil {
		// The plan is in hand and correct; only the cache write failed. Returning
		// it with the failure named is better than discarding work that cost a
		// week of killmails and several region fetches.
		plan.Warnings = append(plan.Warnings, "the plan could not be cached and will be rebuilt next time: "+err.Error())
	}

	writeJSON(w, fwPlanResponse{
		HasPlan:     true,
		Plan:        plan,
		GeneratedAt: plan.GeneratedAt,
		Warnings:    plan.Warnings,
	})
}

// buildFWPlan is the generation, in the order the data depends on itself.
//
// The frontline comes first because everything else is measured against it: the
// ring is derived from it, and the demand pass is filtered to it. Demand comes
// before the ring's scoring because "how many of this station's stocked types are
// actually being destroyed" needs the destroyed set. The destination's own book
// comes last, because until a destination is chosen there is nothing to fetch.
func (s *Server) buildFWPlan(ctx context.Context, userID string, campaign *db.FWCampaign) (*fwPlanPayload, error) {
	s.mu.RLock()
	sdeData := s.sdeData
	s.mu.RUnlock()
	if sdeData == nil || sdeData.Universe == nil {
		return nil, fmt.Errorf("the SDE is still loading; the staging ring is derived from the stargate graph, so this cannot be answered yet")
	}

	plan := &fwPlanPayload{
		CampaignID:       campaign.CampaignID,
		SchemaVersion:    fwPlanSchemaVersion,
		GeneratedAt:      time.Now().UTC().Format(time.RFC3339),
		MilitiaFactionID: campaign.MilitiaFactionID,
		MilitiaName:      fwMilitiaLabel(campaign.MilitiaFactionID),
		RingRadius:       campaign.MaxJumpsFromFront,
		Rows:             []engine.FWSupplyRow{},
	}
	if plan.RingRadius <= 0 {
		plan.RingRadius = engine.DefaultMaxJumpsFromFront
	}

	fwSystems, err := s.esi.FetchFWSystemsCached(ctx, s.fwSystems)
	if err != nil {
		// Without the frontline there is no ring and no warzone filter, so this
		// is the one fetch the plan cannot proceed without.
		return nil, fmt.Errorf("faction warfare systems: %w", err)
	}
	frontline := esi.OccupiedBy(fwSystems, campaign.MilitiaFactionID)
	warzone := esi.WarzoneSystems(fwSystems, campaign.MilitiaFactionID)
	plan.FrontlineSystems = len(frontline)
	if len(frontline) == 0 {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf(
			"%s currently occupies no systems, so there is no frontline to stage behind",
			plan.MilitiaName))
	}

	// Demand. A failed pass is survivable -- the ring still ranks on depth and
	// geometry -- so it degrades to a warning rather than failing the request.
	//
	// Two passes when a long window is configured, and the long one is allowed to
	// fail on its own: a ninety-day walk is three hundred requests and the tool is
	// still usable on seven days without it. What is not allowed is for its
	// failure to be invisible, because sizing would then quietly fall back to the
	// short rate.
	longWindow := fwNormalizeLongWindow(campaign.LongDemandWindowSeconds)
	var demand, longDemand *zkillboard.MilitiaDemandProfile
	if len(warzone) > 0 && s.demandAnalyzer != nil {
		demand, err = s.fwMilitiaDemand(ctx, campaign.MilitiaFactionID, warzone, fwDemandWindowSeconds, sdeData)
		if err != nil {
			plan.Warnings = append(plan.Warnings,
				"the killmail pass failed, so the gap table has no demand behind it and every cover figure is unknowable: "+err.Error())
			demand = nil
		}
		if longWindow > 0 {
			longDemand, err = s.fwMilitiaDemand(ctx, campaign.MilitiaFactionID, warzone, longWindow, sdeData)
			if err != nil {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf(
					"the %d-day demand pass failed, so there is no long-window column and quantities are sized off the seven-day rate whatever the campaign asks for: %s",
					longWindow/86400, err.Error()))
				longDemand = nil
			}
		}
	} else if s.demandAnalyzer == nil {
		plan.Warnings = append(plan.Warnings, "no killmail analyzer is configured, so demand could not be measured")
	}

	// Whichever window the campaign asked to size against, the plan can only use
	// one it actually has.
	sizeAgainst := engine.FWSizedByShort
	if longDemand != nil && campaign.SizesAgainstLong() {
		sizeAgainst = engine.FWSizedByLong
	} else if longDemand == nil && campaign.SizesAgainstLong() {
		plan.Warnings = append(plan.Warnings,
			"this campaign sizes against the long window, but the long window has no measurement, so quantities came from the seven-day rate")
	}
	plan.Demand.SizeAgainst = sizeAgainst

	destroyed := map[int32]bool{}
	if demand != nil {
		plan.Demand.WindowSeconds = demand.WindowSeconds
		plan.Demand.FetchedKills = demand.FetchedKills
		plan.Demand.InWarzoneKills = demand.InWarzoneKills
		plan.Demand.Truncated = demand.Truncated
		plan.Demand.DestroyedTypes = len(demand.Items)
		plan.Demand.SampledAt = demand.UpdatedAt.UTC().Format(time.RFC3339)
		for typeID := range demand.Items {
			destroyed[typeID] = true
		}
		plan.Warnings = append(plan.Warnings, demand.Warnings...)
		if demand.InWarzoneKills == 0 && demand.FetchedKills > 0 {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf(
				"none of the %d sampled losses happened inside the warzone, so there is no in-warzone demand to size against",
				demand.FetchedKills))
		}
	}
	if longDemand != nil {
		plan.Demand.LongWindowSeconds = longDemand.WindowSeconds
		plan.Demand.LongCoveredSeconds = longDemand.CoveredSeconds
		plan.Demand.LongFetchedKills = longDemand.FetchedKills
		plan.Demand.LongInWarzoneKills = longDemand.InWarzoneKills
		plan.Demand.LongTruncated = longDemand.Truncated
		plan.Demand.LongDestroyedTypes = len(longDemand.Items)
		plan.Demand.LongSampledAt = longDemand.UpdatedAt.UTC().Format(time.RFC3339)
		// The ladder calibrates over destroyed types, so the union serves it too:
		// a staple the long window saw is a type this station has a price history
		// for, whether or not it died this week.
		for typeID := range longDemand.Items {
			destroyed[typeID] = true
		}
		// Every sentence the walk produced about what it missed, verbatim. These
		// are the only thing standing between a hole in the sample and a rate that
		// reads as complete.
		plan.Warnings = append(plan.Warnings, longDemand.Warnings...)
	}

	// The ring.
	sourceSystemID := int32(0)
	if st, ok := sdeData.Stations[campaign.SourceStationID]; ok && st != nil {
		sourceSystemID = st.SystemID
	} else if campaign.SourceStationID > 0 {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf(
			"source station %d is not in the SDE, so supply routes are not scored",
			campaign.SourceStationID))
	}

	ringOpts := engine.RingOptions{
		MaxJumpsFromFront: plan.RingRadius,
		SourceSystemID:    sourceSystemID,
		PinnedSystems:     int32Set(campaign.PinnedSystems),
		ExcludedSystems:   int32Set(campaign.ExcludedSystems),
		BulwarkSystemID:   esi.BulwarkSystems[campaign.MilitiaFactionID],
	}
	candidates := engine.DeriveStagingRing(sdeData.Universe, sdeData, frontline, ringOpts)

	depth, depthWarnings := s.fwStagingDepth(candidates, destroyed, sdeData)
	plan.Warnings = append(plan.Warnings, depthWarnings...)
	plan.Ring = engine.ScoreStagingCandidates(candidates, depth, plan.RingRadius)

	// Budget is the campaign's regardless of whether a destination is set: a
	// campaign holding stock it has not listed still has that ISK committed.
	lots, err := s.db.GetFWLots(userID, campaign.CampaignID)
	if err != nil {
		return nil, fmt.Errorf("fw lots: %w", err)
	}
	plan.Budget = engine.MeasureFWBudget(campaign.BudgetISK, lots)

	if campaign.DestStationID <= 0 {
		plan.Warnings = append(plan.Warnings,
			"no destination chosen yet, so this is the staging ring only -- pick a station to get a gap table")
		return plan, nil
	}

	if err := s.fwPlanDestinationPass(ctx, userID, campaign, plan, demand, longDemand, destroyed, fwSystems, sdeData); err != nil {
		return nil, err
	}
	return plan, nil
}

// int32Set turns a stored list into the set the ring options want. A nil list
// yields a nil set, which the ring reads as "none", not "all".
func int32Set(ids []int32) map[int32]bool {
	if len(ids) == 0 {
		return nil
	}
	out := make(map[int32]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}
