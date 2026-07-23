package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"eve-flipper/internal/db"
	"eve-flipper/internal/engine"
	"eve-flipper/internal/esi"
	"eve-flipper/internal/sde"
)

// ownedBlueprintAggregateStats is the per-call telemetry from
// aggregateOwnedBlueprints. Both the project sync handler and the profitable
// scan handler surface these counters to the client.
type ownedBlueprintAggregateStats struct {
	CharactersSelected           int
	CharactersUsed               int
	BlueprintsEndpointCharacters int
	AssetsFallbackCharacters     int
	BlueprintRowsScanned         int
	AssetsScanned                int
	CorporationsScanned          int
	CorporationsForbidden        int
	CorporationBlueprintRows     int
}

// blueprintDisplayName returns a human-readable name for a blueprint type.
// Falls back to "<product name> Blueprint" when sdeData.Types doesn't
// carry the BP itself (invented T2 BPCs and some obscure T1 BPs are absent
// from the market-published type list — see loader.go's marketGroupID gate).
func blueprintDisplayName(bpTypeID int32, sdeData *sde.Data) string {
	if sdeData != nil {
		if t, ok := sdeData.Types[bpTypeID]; ok && strings.TrimSpace(t.Name) != "" {
			return strings.TrimSpace(t.Name)
		}
		if sdeData.Industry != nil {
			if bp, ok := sdeData.Industry.Blueprints[bpTypeID]; ok && bp != nil {
				if mfg, ok := bp.Activities["manufacturing"]; ok && mfg != nil && len(mfg.Products) > 0 {
					prodID := mfg.Products[0].TypeID
					if pt, ok := sdeData.Types[prodID]; ok && strings.TrimSpace(pt.Name) != "" {
						return strings.TrimSpace(pt.Name) + " Blueprint"
					}
				}
			}
		}
	}
	return fmt.Sprintf("Type %d", bpTypeID)
}

// aggregateOwnedBlueprints fetches blueprints for the requested characters
// (with an assets-based fallback) and merges them into pool rows keyed by
// (blueprint type, location, BPO/BPC). It is the shared backend for both the
// project blueprint-sync handler and the profitable-blueprint scan handler.
//
// characterID is honored only when allScope == false. When allScope == true
// every logged-in character for the user is scanned.
//
// Returns ErrNotLoggedIn-style errors verbatim; the caller decides whether
// 401/400/500 fits its endpoint contract.
func (s *Server) aggregateOwnedBlueprints(
	userID string,
	characterID int64,
	allScope bool,
	locationIDs []int64,
	defaultBPCRuns int64,
	includeCorp bool,
) ([]db.IndustryBlueprintPoolInput, []string, ownedBlueprintAggregateStats, error) {
	var stats ownedBlueprintAggregateStats

	if defaultBPCRuns <= 0 {
		defaultBPCRuns = 1
	}
	if defaultBPCRuns > 1000 {
		defaultBPCRuns = 1000
	}

	if allScope && characterID > 0 {
		return nil, nil, stats, fmt.Errorf("character_id and scope=all cannot be combined")
	}

	selectedSessions, err := s.authSessionsForScope(userID, characterID, allScope, true)
	if err != nil {
		return nil, nil, stats, err
	}
	stats.CharactersSelected = len(selectedSessions)

	s.mu.RLock()
	sdeData := s.sdeData
	s.mu.RUnlock()
	if sdeData == nil || sdeData.Industry == nil {
		return nil, nil, stats, fmt.Errorf("industry data not ready")
	}

	locationFilter := make(map[int64]struct{}, len(locationIDs))
	for _, locationID := range locationIDs {
		if locationID <= 0 {
			continue
		}
		locationFilter[locationID] = struct{}{}
	}

	type bpKey struct {
		TypeID     int32
		LocationID int64
		IsBPO      bool
	}

	aggregated := make(map[bpKey]db.IndustryBlueprintPoolInput, 256)
	warnings := make([]string, 0, 4)
	assetsFallbackWarnAdded := false
	assetResolverWarnAdded := false

	resolveRootLocationID := func(locationID int64, assetByItemID map[int64]esi.CharacterAsset) int64 {
		if locationID <= 0 || len(assetByItemID) == 0 {
			return locationID
		}
		current := locationID
		seen := map[int64]struct{}{}
		for current > 0 {
			if _, ok := seen[current]; ok {
				return current
			}
			seen[current] = struct{}{}

			parent, ok := assetByItemID[current]
			if !ok {
				return current
			}
			parentType := strings.ToLower(strings.TrimSpace(parent.LocationType))
			if parentType != "item" {
				if parent.LocationID > 0 {
					return parent.LocationID
				}
				return current
			}
			current = parent.LocationID
		}
		return locationID
	}

	upsert := func(typeID int32, locationID int64, isBPO bool, quantity int64, availableRuns int64, me int32, te int32) {
		if typeID <= 0 {
			return
		}
		if _, ok := sdeData.Industry.Blueprints[typeID]; !ok {
			return
		}
		if quantity <= 0 {
			quantity = 1
		}
		if !isBPO {
			if availableRuns <= 0 {
				availableRuns = quantity * defaultBPCRuns
			}
			if availableRuns < quantity {
				availableRuns = quantity
			}
		} else {
			availableRuns = 0
		}
		if len(locationFilter) > 0 {
			if _, ok := locationFilter[locationID]; !ok {
				return
			}
		}

		typeName := blueprintDisplayName(typeID, sdeData)

		key := bpKey{TypeID: typeID, LocationID: locationID, IsBPO: isBPO}
		row := aggregated[key]
		if row.BlueprintTypeID == 0 {
			row.BlueprintTypeID = typeID
			row.BlueprintName = typeName
			row.LocationID = locationID
			row.IsBPO = isBPO
		}
		row.Quantity += quantity
		if !isBPO {
			row.AvailableRuns += availableRuns
		}
		if me > row.ME {
			row.ME = me
		}
		if te > row.TE {
			row.TE = te
		}
		aggregated[key] = row
	}

	for _, sess := range selectedSessions {
		token, tokenErr := s.sessions.EnsureValidTokenForUserCharacter(s.sso, userID, sess.CharacterID)
		if tokenErr != nil {
			log.Printf("[AUTH] Blueprint aggregate token error (%s): %v", sess.CharacterName, tokenErr)
			if !allScope {
				return nil, nil, stats, tokenErr
			}
			continue
		}

		sourceOK := false

		charBlueprints, bpErr := s.esi.GetCharacterBlueprints(sess.CharacterID, token)
		if bpErr == nil {
			sourceOK = true
			stats.BlueprintsEndpointCharacters++
			stats.BlueprintRowsScanned += len(charBlueprints)

			assetByItemID := map[int64]esi.CharacterAsset{}
			assets, assetErr := s.esi.GetCharacterAssets(sess.CharacterID, token)
			if assetErr == nil {
				stats.AssetsScanned += len(assets)
				assetByItemID = make(map[int64]esi.CharacterAsset, len(assets))
				for _, asset := range assets {
					if asset.ItemID > 0 {
						assetByItemID[asset.ItemID] = asset
					}
				}
			} else if !assetResolverWarnAdded {
				warnings = append(warnings, "blueprint location resolver unavailable: using raw location_id for some rows")
				assetResolverWarnAdded = true
			}

			for _, bp := range charBlueprints {
				if bp.TypeID <= 0 {
					continue
				}
				resolvedLocationID := resolveRootLocationID(bp.LocationID, assetByItemID)
				quantity := bp.Quantity
				if quantity <= 0 {
					quantity = 1
				}
				isBPO := bp.Runs < 0
				availableRuns := int64(0)
				if !isBPO {
					runsPerCopy := bp.Runs
					if runsPerCopy <= 0 {
						runsPerCopy = defaultBPCRuns
					}
					availableRuns = runsPerCopy * quantity
				}
				upsert(bp.TypeID, resolvedLocationID, isBPO, quantity, availableRuns, bp.MaterialEfficiency, bp.TimeEfficiency)
			}
		} else {
			log.Printf("[AUTH] Blueprint aggregate blueprints error (%s): %v", sess.CharacterName, bpErr)

			assets, fetchErr := s.esi.GetCharacterAssets(sess.CharacterID, token)
			if fetchErr != nil {
				log.Printf("[AUTH] Blueprint aggregate assets fallback error (%s): %v", sess.CharacterName, fetchErr)
				if !allScope {
					return nil, nil, stats, fmt.Errorf("failed to fetch blueprints/assets: %w", fetchErr)
				}
				continue
			}

			sourceOK = true
			stats.AssetsFallbackCharacters++
			stats.AssetsScanned += len(assets)
			if !assetsFallbackWarnAdded {
				warnings = append(warnings, "blueprints endpoint unavailable for some characters; assets fallback used (ME/TE/runs are estimated)")
				assetsFallbackWarnAdded = true
			}

			assetByItemID := make(map[int64]esi.CharacterAsset, len(assets))
			for _, asset := range assets {
				if asset.ItemID > 0 {
					assetByItemID[asset.ItemID] = asset
				}
			}

			for _, asset := range assets {
				if asset.TypeID <= 0 {
					continue
				}
				resolvedLocationID := resolveRootLocationID(asset.LocationID, assetByItemID)
				isBPO := true
				if asset.IsBlueprintCopy || asset.Quantity <= -2 {
					isBPO = false
				}
				quantity := asset.Quantity
				if quantity <= 0 {
					quantity = 1
				}
				upsert(asset.TypeID, resolvedLocationID, isBPO, quantity, quantity*defaultBPCRuns, 0, 0)
			}
		}

		if sourceOK {
			stats.CharactersUsed++
		}
	}

	if stats.CharactersSelected > 0 && stats.CharactersUsed == 0 {
		if allScope {
			return nil, warnings, stats, fmt.Errorf("failed to fetch blueprints/assets for selected characters")
		}
		return nil, warnings, stats, fmt.Errorf("failed to fetch blueprints/assets")
	}

	if includeCorp && len(selectedSessions) > 0 {
		corpsScanned := make(map[int32]struct{})
		corpForbiddenWarnAdded := false
		corpScopeMissingWarnAdded := false
		for _, sess := range selectedSessions {
			token, tokenErr := s.sessions.EnsureValidTokenForUserCharacter(s.sso, userID, sess.CharacterID)
			if tokenErr != nil {
				continue
			}

			corpID, corpErr := s.esi.GetCharacterCorporationID(sess.CharacterID)
			if corpErr != nil || corpID <= 0 {
				log.Printf("[AUTH] Blueprint aggregate corp lookup error (%s): %v", sess.CharacterName, corpErr)
				continue
			}
			if _, alreadyDone := corpsScanned[corpID]; alreadyDone {
				continue
			}

			roles, rolesErr := s.esi.GetCharacterRoles(sess.CharacterID, token)
			if rolesErr != nil {
				msg := strings.ToLower(rolesErr.Error())
				if strings.Contains(msg, "403") || strings.Contains(msg, "scope") {
					if !corpScopeMissingWarnAdded {
						warnings = append(warnings, "corp blueprints skipped: missing esi-characters.read_corporation_roles.v1 scope (re-authenticate)")
						corpScopeMissingWarnAdded = true
					}
				} else {
					log.Printf("[AUTH] Blueprint aggregate roles error (%s): %v", sess.CharacterName, rolesErr)
				}
				continue
			}
			hasDirector := false
			if roles != nil {
				for _, r := range roles.Roles {
					if strings.EqualFold(r, "Director") {
						hasDirector = true
						break
					}
				}
			}
			if !hasDirector {
				continue
			}

			corpBlueprints, corpBpErr := s.esi.GetCorporationBlueprints(corpID, token)
			if corpBpErr != nil {
				msg := strings.ToLower(corpBpErr.Error())
				if strings.Contains(msg, "403") {
					stats.CorporationsForbidden++
					if !corpForbiddenWarnAdded {
						warnings = append(warnings, "corp blueprints scope missing or insufficient corp role (re-authenticate to grant esi-corporations.read_blueprints.v1)")
						corpForbiddenWarnAdded = true
					}
				} else {
					log.Printf("[AUTH] Blueprint aggregate corp blueprints error (corp %d via %s): %v", corpID, sess.CharacterName, corpBpErr)
				}
				continue
			}
			corpsScanned[corpID] = struct{}{}
			stats.CorporationsScanned++
			stats.CorporationBlueprintRows += len(corpBlueprints)

			for _, bp := range corpBlueprints {
				if bp.TypeID <= 0 {
					continue
				}
				// Corp BPs have no Items endpoint to walk; use raw LocationID.
				quantity := bp.Quantity
				if quantity <= 0 {
					quantity = 1
				}
				isBPO := bp.Runs < 0
				availableRuns := int64(0)
				if !isBPO {
					runsPerCopy := bp.Runs
					if runsPerCopy <= 0 {
						runsPerCopy = defaultBPCRuns
					}
					availableRuns = runsPerCopy * quantity
				}
				upsert(bp.TypeID, bp.LocationID, isBPO, quantity, availableRuns, bp.MaterialEfficiency, bp.TimeEfficiency)
			}
		}
	}

	rows := make([]db.IndustryBlueprintPoolInput, 0, len(aggregated))
	for _, row := range aggregated {
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].BlueprintTypeID != rows[j].BlueprintTypeID {
			return rows[i].BlueprintTypeID < rows[j].BlueprintTypeID
		}
		if rows[i].LocationID != rows[j].LocationID {
			return rows[i].LocationID < rows[j].LocationID
		}
		if rows[i].IsBPO == rows[j].IsBPO {
			return rows[i].BlueprintName < rows[j].BlueprintName
		}
		return rows[i].IsBPO && !rows[j].IsBPO
	})
	return rows, warnings, stats, nil
}

// --- Profitable blueprints scan ---

// profitableScanHardMaxBPs is a runaway-request guard — the absolute ceiling
// on distinct blueprint groups analyzed per scan. The SDE ships ~5k BPs and
// even with T2+T3 invention fanout the realistic upper bound is ~10k rows,
// so 20k is comfortably out of reach for legitimate use. There is no visible
// UI knob for this — filters (type category, tech tier, kind, owned) narrow
// the scope explicitly rather than by a shotgun-blast row limit.
const profitableScanHardMaxBPs = 20000

// profitableScanWorkers is the analyzer fan-out for one scan. Scaled to
// the host's core count so users on modern desktops (8-16 cores) don't sit
// waiting behind an old-conservative cap of 5. Bounded to 16 to avoid
// oversubscribing ESI rate limits on the very-large-machine case — most
// per-Analyze fetches hit the IndustryCache anyway, but market book pulls
// still hit ESI once per (region, product) uncached, so a huge fan-out
// can burst-fire enough calls to trip the client-level semaphore. Floor
// of 4 keeps behavior predictable on constrained machines.
var profitableScanWorkers = func() int {
	n := runtime.NumCPU()
	if n < 4 {
		return 4
	}
	if n > 16 {
		return 16
	}
	return n
}()

// profitableScanPeriodDays is the market-absorption horizon used to compute
// PeriodProfit / PeriodMargin on each row. Captures the "shortage fills up
// before I finish building" risk: even when per-unit ISK/h looks great, if
// the market only absorbs a handful of units per day the effective profit
// over the manufacturing horizon is capped by market volume, not by your
// production rate. 30 days is a stable window (less noisy than 7-14 day
// averages) and comfortably longer than typical T2 build times.
const profitableScanPeriodDays = 30

// profitableScanMarketShare is the share of aggressive-buy volume a single
// builder is assumed to capture when computing PeriodProfit / PeriodMargin.
// Full-market (1.0) would model "you sell 100% of what changes hands in
// Jita" which is unrealistic for one seller. 10% matches the modal's
// default runs-suggestion share and is comfortable for a mid-size hub
// presence — tune here if the whole scanner should optimize against a
// different share (later this could be lifted to a per-scan request field).
const profitableScanMarketShare = 0.10

type profitableScanRequest struct {
	Scope                   string  `json:"scope"`
	CharacterID             int64   `json:"character_id"`
	LocationIDs             []int64 `json:"location_ids"`
	DefaultBPCRuns          int64   `json:"default_bpc_runs"`
	IncludeCorpBlueprints   bool    `json:"include_corp_blueprints"`

	BuildSystemName   string `json:"build_system_name"`
	// PricingSystemName drives the pricing region independently from the build
	// system. Empty falls back to the build system's region (legacy behavior).
	PricingSystemName string `json:"pricing_system_name"`
	PricingStationID  int64  `json:"pricing_station_id"`
	FacilityTax      float64 `json:"facility_tax"`
	StructureBonus   float64 `json:"structure_bonus"`
	BrokerFee        float64 `json:"broker_fee"`
	SalesTaxPercent  float64 `json:"sales_tax_percent"`
	RunsPerJob       int32   `json:"runs_per_job"`
	MaxDepth         int     `json:"max_depth"`

	MinISKPerHour float64 `json:"min_isk_per_hour"`
	MinProfit     float64 `json:"min_profit"`
	MinMarginPct  float64 `json:"min_margin_percent"`

	// MaxBlueprints is a hidden runaway-request guard. The visible UI no
	// longer exposes this; filters (type category, tech tier, kind, owned)
	// handle scope narrowing. Zero/negative or values above the hard cap
	// are clamped to profitableScanHardMaxBPs.
	MaxBlueprints int `json:"max_blueprints"`

	// BlueprintFilter selects which kinds of blueprints feed the analyzer:
	//   "bpo"  - originals only (default; treats stacks of BPCs as ignored)
	//   "bpc"  - copies only
	//   "both" - both
	// Empty / unknown values are treated as "bpo".
	BlueprintFilter string `json:"blueprint_filter"`

	// IncludeT2Invention, when true, fans each owned BP that has an
	// invention activity out to its T2 products and scores them in
	// invention mode alongside the T1 manufacturing rows.
	IncludeT2Invention bool `json:"include_t2_invention"`

	// IncludeT3Invention, when true, includes T3 invention paths (Tactical
	// Destroyers, Strategic Cruisers, T3 Subsystems). The invention sources
	// are relic BPs (Hull Sections, Ancient Relics) which the user rarely
	// owns as BPs — with include_unowned on, synthetic groups also cover
	// invention-only relic BPs so the T3 path surfaces even for a user
	// who has never touched a data site. Independent of IncludeT2Invention.
	IncludeT3Invention bool `json:"include_t3_invention"`

	// IncludeReactions, when true, emits reaction rows for BPs with a
	// reaction activity (fuel-block formulas, composite reactions, etc.).
	// Off by default — most builders don't run reactions themselves — but
	// users who do want to score them alongside mfg + invention rows.
	IncludeReactions bool `json:"include_reactions"`

	// SkipReactions, when true, forces reaction-only child materials to be
	// treated as base (buy) nodes rather than expanded into a reaction step.
	// Reflects the workflow of a builder who buys reaction outputs from the
	// market rather than running reactions themselves.
	SkipReactions bool `json:"skip_reactions"`

	// Structure rig loadout for the build structure. Empty RigTypeIDs =
	// no rig math applied. StructureTypeID is the hull typeID (Raitaru,
	// Sotiyo, etc.) used to validate rig fit; zero → engine skips rig math.
	StructureRigTypeIDs       []int32 `json:"structure_rig_type_ids"`
	StructureTypeID           int32   `json:"structure_type_id"`
	StructureJobCostReduction float64 `json:"structure_job_cost_reduction"`

	// TypeCategories filters rows by their product's SDE CategoryID (6=Ships,
	// 7=Modules, 8=Charges, 17=Commodity, 18=Drone, 20=Implant, 22=Deployable,
	// 32=Subsystem, 34=Material, 35=Component, 65+66=Structure).
	// Empty slice = include all. Applied after the analyzer runs so filtered
	// rows don't waste analyzer time on themselves — the filter also drops
	// the work items up front.
	TypeCategories []int32 `json:"type_categories"`

	// IncludeUnowned, when true, extends the scan with every marketable
	// SDE blueprint the user doesn't own. Lets discovery surface "you don't
	// have this BP but it'd be profitable to buy and build" opportunities.
	// Synthetic groups get UnownedDefaultME/TE and is_bpo=true.
	IncludeUnowned      bool  `json:"include_unowned"`
	UnownedDefaultME    int32 `json:"unowned_default_me"`
	UnownedDefaultTE    int32 `json:"unowned_default_te"`

	// Effective invention parameters — the frontend computes decryptor
	// deltas and sends the absolute values so the engine stays untouched.
	// Zero values mean "SDE default".
	InventionMEBase     int32   `json:"invention_me_base"`
	InventionTEBase     int32   `json:"invention_te_base"`
	InventionChanceMult float64 `json:"invention_chance_mult"`
	InventionOutputRuns int32   `json:"invention_output_runs"`
	DecryptorCost       float64 `json:"decryptor_cost"`

	// When SkipBlueprintFetch is true, the backend skips the ESI blueprint /
	// asset fetch and uses ReuseGroups verbatim as the scan input. This is how
	// the "Refresh prices" flow re-scores the existing table without paying
	// the (slow) blueprint-pool resync cost on every refresh. The BPO/BPC and
	// MaxBlueprints filters are still applied to ReuseGroups.
	SkipBlueprintFetch bool                     `json:"skip_blueprint_fetch"`
	ReuseGroups        []profitableScanReuseRow `json:"reuse_groups"`
}

// profitableScanReuseRow is the minimum the backend needs to rescore a row
// it has already scanned once: blueprint identity + ME/TE so the analyzer
// reproduces the same conditions.
type profitableScanReuseRow struct {
	BlueprintTypeID int32   `json:"blueprint_type_id"`
	IsBPO           bool    `json:"is_bpo"`
	ME              int32   `json:"me"`
	TE              int32   `json:"te"`
	OwnedQuantity   int64   `json:"owned_quantity"`
	AvailableRuns   int64   `json:"available_runs"`
	LocationIDs     []int64 `json:"location_ids"`
	// Owned preserves the "did the user own this?" flag through refresh so
	// synthetic unowned rows aren't re-tagged as owned on rescore.
	Owned bool `json:"owned"`
}

type profitableScanRow struct {
	BlueprintTypeID   int32   `json:"blueprint_type_id"`
	BlueprintName     string  `json:"blueprint_name"`
	ProductTypeID     int32   `json:"product_type_id"`
	ProductName       string  `json:"product_name"`
	// GroupName and CategoryName come from the product's SDE group + category
	// (e.g. "Heavy Assault Cruiser" / "Ship" for Ishtar). Included per-row so
	// the frontend search box can filter by any of name / group / category
	// without an extra lookup table on the client. Empty when the SDE isn't
	// loaded or the type is missing group metadata.
	GroupName    string `json:"group_name"`
	CategoryName string `json:"category_name"`
	OwnedQuantity     int64   `json:"owned_quantity"`
	IsBPO             bool    `json:"is_bpo"`
	AvailableRuns     int64   `json:"available_runs"`
	ME                int32   `json:"me"`
	TE                int32   `json:"te"`
	LocationIDs       []int64 `json:"location_ids"`
	Runs              int32   `json:"runs"`
	Profit            float64 `json:"profit"`
	ProfitPercent     float64 `json:"profit_percent"`
	ISKPerHour        float64 `json:"isk_per_hour"`
	OptimalBuildCost  float64 `json:"optimal_build_cost"`
	SellRevenue       float64 `json:"sell_revenue"`
	ManufacturingTime int32   `json:"manufacturing_time"`
	// Owned mirrors blueprintGroup.Owned — true when the row comes from an
	// actual BP in the user's inventory, false for synthetic "you don't own
	// this but here's what the profit would look like if you did" rows.
	Owned bool `json:"owned"`

	// Invention-specific fields. Empty / zero for T1 manufacturing rows.
	ScanMode              string  `json:"scan_mode"`               // "t1_mfg" | "t2_invention" | "t3_invention" | "reaction"
	InventionSourceBPID   int32   `json:"invention_source_bp_id"`  // T1 BP typeID (0 for t1_mfg)
	InventionSourceBPName string  `json:"invention_source_bp_name"`
	InventionOutputBPID   int32   `json:"invention_output_bp_id"` // T2 BPC typeID (0 for t1_mfg)
	InventionOutputBPName string  `json:"invention_output_bp_name"`
	InventionProbability  float64 `json:"invention_probability"` // effective per-attempt (0..1)
	ExpectedAttempts      float64 `json:"expected_attempts"`
	AttemptsCap           int64   `json:"attempts_cap"` // -1 = unlimited (BPO source)
	AttemptsCapExceeded   bool    `json:"attempts_cap_exceeded"`
	// BestDecryptorKey is the winning decryptor for T2 rows — the scanner
	// iterates every decryptor (None + 8 typed) and picks the one that
	// maximizes ISK/h for this specific row. Empty for T1 rows.
	BestDecryptorKey string `json:"best_decryptor_key"`

	// Market-absorption fields. Given the shortage-fills-up risk (per-unit
	// margin great, market volume tiny), these show what you'd actually
	// realize over PeriodDays. See profitableScanPeriodDays for the window.
	//   ProductDailyVolume — average units traded per day over the period
	//     window in the pricing region.
	//   PeriodProfit — ISK profit from selling min(units producible in period,
	//     units market absorbs in period) at the current per-unit profit.
	//   PeriodMargin — PeriodProfit / capital deployed (unit cost × units you
	//     produced). Equal to ProfitPercent when the market absorbs your full
	//     output; drops below it when market volume caps your realized sales
	//     — captures the "capital tied up in unsold inventory" penalty.
	//   PeriodDays — the window used, echoed to the row for display.
	// Zero when history is unavailable (fetch error, no market data, no
	// pricing region resolved).
	ProductDailyVolume int64   `json:"product_daily_volume"`
	PeriodProfit       float64 `json:"period_profit"`
	PeriodMargin       float64 `json:"period_margin"`
	PeriodDays         int32   `json:"period_days"`
	// OutputQtyPerRun is how many product units come out of one blueprint
	// run. For T1 mfg it's the mfg activity's product quantity; for T2/T3
	// invention it's the invented BPC's mfg activity's product quantity
	// (typically 1 for modules, 100 for charges, higher for drones).
	// Lets the frontend convert "units of market demand" → "BP runs" in
	// its per-row runs suggestion.
	OutputQtyPerRun int32 `json:"output_qty_per_run"`

	// Cost breakdown (all ISK, sourced from IndustryAnalysis). Lets the
	// frontend render a full profit-math tooltip without extra API calls.
	// TotalQuantity is how many units this row produces across all runs.
	TotalMaterialCost float64 `json:"total_material_cost"`
	TotalJobCost      float64 `json:"total_job_cost"`
	InventionCost     float64 `json:"invention_cost"`
	TotalQuantity     int32   `json:"total_quantity"`
	// UnitSellPrice is the per-unit revenue AFTER sales tax + broker fee
	// (i.e. sell_revenue / total_quantity). Used in the tooltip breakdown.
	UnitSellPrice float64 `json:"unit_sell_price"`
}

type profitableScanStats struct {
	OwnedBlueprintGroups int `json:"owned_blueprint_groups"`
	Analyzed             int `json:"analyzed"`
	SkippedNoActivity    int `json:"skipped_no_activity"`
	SkippedFiltered      int `json:"skipped_filtered"`
	Errors               int `json:"errors"`
	CapHit               int `json:"cap_hit"`
}

type profitableScanResponse struct {
	Rows     []profitableScanRow `json:"rows"`
	Warnings []string            `json:"warnings"`
	Stats    profitableScanStats `json:"stats"`
}

// blueprintGroup is one (blueprint type, BPO/BPC) bucket aggregated across
// locations. We score one row per blueprint type per BPO/BPC kind.
type blueprintGroup struct {
	BlueprintTypeID int32
	BlueprintName   string
	IsBPO           bool
	OwnedQuantity   int64
	AvailableRuns   int64
	ME              int32
	TE              int32
	LocationIDs     []int64
	// Owned is true when this group came from the user's actual BP inventory
	// (aggregated from ESI). False when synthesized to represent an unowned
	// SDE blueprint the user might consider buying.
	Owned bool
}

// groupBlueprintsByType collapses pool rows across locations into one entry
// per (blueprintTypeID, isBPO). ME/TE pick the best copy; quantity and runs
// sum; location ids are merged distinctly.
func groupBlueprintsByType(rows []db.IndustryBlueprintPoolInput) []blueprintGroup {
	type key struct {
		TypeID int32
		IsBPO  bool
	}
	byKey := make(map[key]*blueprintGroup, len(rows))
	for _, r := range rows {
		k := key{TypeID: r.BlueprintTypeID, IsBPO: r.IsBPO}
		g, ok := byKey[k]
		if !ok {
			g = &blueprintGroup{
				BlueprintTypeID: r.BlueprintTypeID,
				BlueprintName:   r.BlueprintName,
				IsBPO:           r.IsBPO,
				// aggregateOwnedBlueprints only yields rows from ESI, so
				// every group produced here is genuinely owned.
				Owned: true,
			}
			byKey[k] = g
		}
		g.OwnedQuantity += r.Quantity
		if !r.IsBPO {
			g.AvailableRuns += r.AvailableRuns
		}
		if r.ME > g.ME {
			g.ME = r.ME
		}
		if r.TE > g.TE {
			g.TE = r.TE
		}
		if r.LocationID > 0 {
			seen := false
			for _, id := range g.LocationIDs {
				if id == r.LocationID {
					seen = true
					break
				}
			}
			if !seen {
				g.LocationIDs = append(g.LocationIDs, r.LocationID)
			}
		}
	}
	out := make([]blueprintGroup, 0, len(byKey))
	for _, g := range byKey {
		out = append(out, *g)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].BlueprintName != out[j].BlueprintName {
			return out[i].BlueprintName < out[j].BlueprintName
		}
		return out[i].IsBPO && !out[j].IsBPO
	})
	return out
}

// scanAnalyzeWork is one row's worth of work for the analyzer pool. For T1
// manufacturing rows, group is the owned BP, productTypeID is what it makes,
// and the invention-specific fields stay zero. For T2 invention rows, group
// is the OWNED T1 BP source, productTypeID is the T2 MODULE (not the T2 BPC),
// and the invention fields carry the source identity, invented BPC identity,
// and per-attempt chance.
type scanAnalyzeWork struct {
	group         blueprintGroup
	productTypeID int32
	productName   string
	// outputQtyPerRun mirrors the emitting BP activity's product quantity —
	// so the row can carry it forward for the "runs suggestion" UI without
	// re-consulting the SDE per row on the frontend.
	outputQtyPerRun int32

	scanMode            string // "t1_mfg" | "t2_invention" | "t3_invention" | "reaction"
	sourceBlueprintID   int32  // T1 BP typeID (invention only)
	sourceBlueprintName string
	outputBlueprintID   int32 // T2 BPC typeID (invention only)
	outputBlueprintName string
	// baseProbability is the T2/T3 product's per-attempt SDE base probability
	// (0..1). The worker multiplies this by each decryptor's chance mult
	// when auto-picking the winning decryptor per row.
	baseProbability float64
	attemptsCap     int64 // -1 = unlimited (BPO source)
}

// buildScanWork turns blueprint groups into per-row analyzer work items.
// Emissions per group (any combination):
//   - T1 manufacturing row, when the BP has a manufacturing activity whose
//     product is marketable.
//   - One row per invention output whose eventual product is marketable,
//     gated by includeT2Invention / includeT3Invention based on the
//     product's MetaGroupID (2 = T2, 14 = T3; unknown treated as T2).
//   - Reaction row (scan_mode="reaction"), when includeReactions is on and
//     the BP has a reaction activity whose product is marketable. Reaction
//     BPs typically have ONLY a reaction activity (fuel-block formulas,
//     moon-composite reactions, hybrid polymers, etc.).
// skipped is the number of groups that produced no work items at all —
// the caller adds this to stats.SkippedNoActivity.
//
// The invention worker later loops through every decryptor and picks the
// winning one per row, so no chance multiplier / decryptor selection is
// baked in here.
//
// Pure — extracted from handleAuthIndustryProfitableScan so tests can exercise
// the fan-out without spinning up an HTTP server or an SDE loader.
func buildScanWork(groups []blueprintGroup, sdeData *sde.Data, includeT2Invention, includeT3Invention, includeReactions bool) (work []scanAnalyzeWork, skipped int) {
	if sdeData == nil || sdeData.Industry == nil {
		return nil, len(groups)
	}

	work = make([]scanAnalyzeWork, 0, len(groups))
	// Defensive group dedupe by (BlueprintTypeID, IsBPO): should already be
	// unique post groupBlueprintsByType, but if any upstream path (reuse
	// groups from a naive client, etc.) leaks duplicates, we don't want the
	// fan-out to multiply.
	seenGroups := make(map[struct {
		ID    int32
		IsBPO bool
	}]struct{}, len(groups))
	for _, g := range groups {
		gk := struct {
			ID    int32
			IsBPO bool
		}{g.BlueprintTypeID, g.IsBPO}
		if _, dup := seenGroups[gk]; dup {
			continue
		}
		seenGroups[gk] = struct{}{}
		bp, ok := sdeData.Industry.Blueprints[g.BlueprintTypeID]
		if !ok {
			skipped++
			continue
		}
		emitted := false

		if mfg, mok := bp.Activities["manufacturing"]; mok && mfg != nil && len(mfg.Products) > 0 {
			productTypeID := mfg.Products[0].TypeID
			outputQty := mfg.Products[0].Quantity
			if outputQty <= 0 {
				outputQty = 1
			}
			// Require the product to be marketable — a published Types entry
			// with a real name. Dev-only items and unreleased content have
			// no market, so surfacing them as scan rows would just show
			// "Type 12345" labels with nonsense ISK/h. Owned rows previously
			// skipped this filter (only synth applied it); now symmetric.
			if productTypeID > 0 {
				if pt, ok := sdeData.Types[productTypeID]; ok && pt != nil && strings.TrimSpace(pt.Name) != "" {
					work = append(work, scanAnalyzeWork{
						group:           g,
						productTypeID:   productTypeID,
						productName:     strings.TrimSpace(pt.Name),
						outputQtyPerRun: outputQty,
						scanMode:        "t1_mfg",
					})
					emitted = true
				}
			}
		}

		if includeT2Invention || includeT3Invention {
			if inv, iok := bp.Activities["invention"]; iok && inv != nil {
				// Defensive dedupe: some SDE dumps have the same BPC
				// appearing multiple times in an invention activity's product
				// list; without this, the same row is emitted repeatedly.
				seenInv := make(map[int32]struct{}, len(inv.Products))
				for _, invProduct := range inv.Products {
					if invProduct.TypeID <= 0 {
						continue
					}
					if _, dup := seenInv[invProduct.TypeID]; dup {
						continue
					}
					seenInv[invProduct.TypeID] = struct{}{}
					invBP, tbok := sdeData.Industry.Blueprints[invProduct.TypeID]
					if !tbok || invBP == nil {
						continue
					}
					invMfg, tmok := invBP.Activities["manufacturing"]
					if !tmok || invMfg == nil || len(invMfg.Products) == 0 {
						continue
					}
					moduleID := invMfg.Products[0].TypeID
					if moduleID <= 0 {
						continue
					}
					// Units per single mfg run of the invented BPC. Same
					// semantics as T1 mfg rows so "Per run" reads consistently
					// regardless of invention vs direct mfg. The analyzer's
					// Runs param = desired mfg runs of the invented item; it
					// derives the required invention attempts internally.
					moduleQty := invMfg.Products[0].Quantity
					if moduleQty <= 0 {
						moduleQty = 1
					}
					baseChance := invProduct.Probability
					if baseChance <= 0 {
						continue
					}
					// Same marketable-product filter as the T1 mfg branch:
					// drop invention outputs whose eventual product isn't in
					// Types (dev-only / unpublished). Also captures the
					// classify-by-metaGroup lookup in one pass.
					pt, hasProduct := sdeData.Types[moduleID]
					if !hasProduct || pt == nil || strings.TrimSpace(pt.Name) == "" {
						continue
					}
					metaGroup := pt.MetaGroupID
					isT3 := metaGroup == sde.MetaGroupT3
					if isT3 && !includeT3Invention {
						continue
					}
					if !isT3 && !includeT2Invention {
						continue
					}
					mode := "t2_invention"
					if isT3 {
						mode = "t3_invention"
					}

					var cap int64 = -1
					if !g.IsBPO {
						cap = g.AvailableRuns
					}

					work = append(work, scanAnalyzeWork{
						group:               g,
						productTypeID:       moduleID,
						productName:         strings.TrimSpace(pt.Name),
						outputQtyPerRun:     moduleQty,
						scanMode:            mode,
						sourceBlueprintID:   g.BlueprintTypeID,
						sourceBlueprintName: g.BlueprintName,
						outputBlueprintID:   invProduct.TypeID,
						outputBlueprintName: blueprintDisplayName(invProduct.TypeID, sdeData),
						baseProbability:     baseChance,
						attemptsCap:         cap,
					})
					emitted = true
				}
			}
		}

		if includeReactions {
			if rxn, rok := bp.Activities["reaction"]; rok && rxn != nil && len(rxn.Products) > 0 {
				productTypeID := rxn.Products[0].TypeID
				productQty := rxn.Products[0].Quantity
				if productQty <= 0 {
					productQty = 1
				}
				if productTypeID > 0 {
					if pt, ok := sdeData.Types[productTypeID]; ok && pt != nil && strings.TrimSpace(pt.Name) != "" {
						work = append(work, scanAnalyzeWork{
							group:           g,
							productTypeID:   productTypeID,
							productName:     strings.TrimSpace(pt.Name),
							outputQtyPerRun: productQty,
							scanMode:        "reaction",
						})
						emitted = true
					}
				}
			}
		}

		if !emitted {
			skipped++
		}
	}
	return work, skipped
}

// synthesizeUnownedGroups adds one synthetic BPO group per marketable SDE
// blueprint the user doesn't already own. A BP is "marketable enough to
// synthesize" when:
//   - it has a manufacturing activity whose product is in Types (T1/T2/T3 BPs
//     covering the standard build path), OR
//   - includeInvention is true AND it has an invention activity with at
//     least one output whose eventual product is in Types (relic BPs like
//     the Hull Sections used for T3 destroyer invention — these have NO
//     manufacturing activity, only invention, and would otherwise be silently
//     dropped, hiding the T3 discovery path entirely), OR
//   - includeReactions is true AND it has a reaction activity whose product
//     is marketable (fuel-block formulas, moon-composite reactions, etc.
//     that a user might want to run themselves rather than buy the output).
//
// Synthetic groups get IsBPO=true and the caller-provided default ME/TE
// (typically 10/20 — assume the user would buy a fully-researched BPO).
// Rows are tagged Owned=false so the frontend can style + filter them.
func synthesizeUnownedGroups(sdeData *sde.Data, ownedGroups []blueprintGroup, defaultME, defaultTE int32, includeInvention, includeReactions bool) []blueprintGroup {
	if sdeData == nil || sdeData.Industry == nil {
		return nil
	}
	// Bound ME/TE to their legal engine ranges.
	if defaultME < 0 {
		defaultME = 0
	}
	if defaultME > 10 {
		defaultME = 10
	}
	if defaultTE < 0 {
		defaultTE = 0
	}
	if defaultTE > 20 {
		defaultTE = 20
	}
	// Skip anything the user already owns (any BPO/BPC of the type). Owned
	// groups already carry the user's actual ME/TE and shouldn't be replaced
	// by a synthetic 10/20 row that would confuse the profit numbers.
	ownedTypeIDs := make(map[int32]struct{}, len(ownedGroups))
	for _, g := range ownedGroups {
		ownedTypeIDs[g.BlueprintTypeID] = struct{}{}
	}

	// isMarketableTypeID checks the standard "marketable product" filter —
	// the product must have a published Types entry with a non-empty name.
	// Drops obscure / unpublished items that no one would build for ISK.
	isMarketableTypeID := func(typeID int32) bool {
		if typeID <= 0 {
			return false
		}
		pt, ok := sdeData.Types[typeID]
		return ok && pt != nil && strings.TrimSpace(pt.Name) != ""
	}

	// inventionSourceIsMarketable returns true when an invention BP produces
	// at least one output whose eventual mfg product is marketable. Used to
	// filter relic-style BPs (invention-only, no manufacturing activity) so
	// only invention paths with a real buildable target survive.
	inventionSourceIsMarketable := func(bp *sde.Blueprint) bool {
		if bp == nil {
			return false
		}
		inv, ok := bp.Activities["invention"]
		if !ok || inv == nil {
			return false
		}
		for _, p := range inv.Products {
			outBP, ok := sdeData.Industry.Blueprints[p.TypeID]
			if !ok || outBP == nil {
				continue
			}
			outMfg, ok := outBP.Activities["manufacturing"]
			if !ok || outMfg == nil || len(outMfg.Products) == 0 {
				continue
			}
			if isMarketableTypeID(outMfg.Products[0].TypeID) {
				return true
			}
		}
		return false
	}

	// reactionSourceIsMarketable returns true when the BP has a reaction
	// activity whose product is marketable. Reaction BPs (fuel-block formulas,
	// composite reactions, hybrid polymers) typically have ONLY a reaction
	// activity — no mfg — so they'd otherwise be silently skipped.
	reactionSourceIsMarketable := func(bp *sde.Blueprint) bool {
		if bp == nil {
			return false
		}
		rxn, ok := bp.Activities["reaction"]
		if !ok || rxn == nil || len(rxn.Products) == 0 {
			return false
		}
		return isMarketableTypeID(rxn.Products[0].TypeID)
	}

	out := make([]blueprintGroup, 0)
	for bpTypeID, bp := range sdeData.Industry.Blueprints {
		if bp == nil {
			continue
		}
		if _, alreadyOwned := ownedTypeIDs[bpTypeID]; alreadyOwned {
			continue
		}
		mfg, hasMfg := bp.Activities["manufacturing"]
		if hasMfg && mfg != nil && len(mfg.Products) > 0 && isMarketableTypeID(mfg.Products[0].TypeID) {
			// Standard manufacturable BP path.
		} else if includeInvention && inventionSourceIsMarketable(bp) {
			// Invention-only relic BP path (e.g. T3 Hull Sections). Skipped
			// unless the caller asked for invention discovery — otherwise
			// these would produce zero output rows via buildScanWork anyway.
		} else if includeReactions && reactionSourceIsMarketable(bp) {
			// Reaction-only BP path (fuel-block formulas, composites, etc.).
			// Skipped unless the caller asked for reactions.
		} else {
			continue
		}
		out = append(out, blueprintGroup{
			BlueprintTypeID: bpTypeID,
			BlueprintName:   blueprintDisplayName(bpTypeID, sdeData),
			IsBPO:           true,
			OwnedQuantity:   0,
			AvailableRuns:   0,
			ME:              defaultME,
			TE:              defaultTE,
			Owned:           false,
		})
	}
	// Stable order by typeID so responses are deterministic across scans.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].BlueprintTypeID < out[j].BlueprintTypeID
	})
	return out
}

// profitableScanRowPassesFilters returns true when the row meets all the
// caller's minimum-profit thresholds. Pure function — extracted for tests.
func profitableScanRowPassesFilters(row profitableScanRow, req profitableScanRequest) bool {
	if req.MinISKPerHour > 0 && row.ISKPerHour < req.MinISKPerHour {
		return false
	}
	if req.MinProfit > 0 && row.Profit < req.MinProfit {
		return false
	}
	if req.MinMarginPct > 0 && row.ProfitPercent < req.MinMarginPct {
		return false
	}
	return true
}

func (s *Server) handleAuthIndustryProfitableScan(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	if !s.isReady() {
		writeError(w, 503, "SDE not loaded yet")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, defaultAPIRequestBodyMaxBytes)
	var req profitableScanRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, 400, "invalid json")
			return
		}
	}

	scope := strings.ToLower(strings.TrimSpace(req.Scope))
	if scope == "" {
		scope = "single"
	}
	if scope != "single" && scope != "all" {
		writeError(w, 400, "scope must be single or all")
		return
	}
	allScope := scope == "all"

	req.RunsPerJob = clampInt32(req.RunsPerJob, 1, industryAnalyzeMaxRuns)
	if req.MaxDepth <= 0 {
		req.MaxDepth = 10
	}
	req.MaxDepth = clampInt(req.MaxDepth, 1, industryAnalyzeMaxDepth)
	req.FacilityTax = clampFloat64(req.FacilityTax, 0, 100)
	req.StructureBonus = clampFloat64(req.StructureBonus, -100, 100)
	req.StructureJobCostReduction = clampFloat64(req.StructureJobCostReduction, 0, 100)
	req.BrokerFee = clampFloat64(req.BrokerFee, 0, 100)
	req.SalesTaxPercent = clampFloat64(req.SalesTaxPercent, 0, 100)
	if req.PricingStationID < 0 {
		req.PricingStationID = 0
	}
	if len(req.StructureRigTypeIDs) > 3 {
		req.StructureRigTypeIDs = req.StructureRigTypeIDs[:3]
	}
	cleanedRigs := req.StructureRigTypeIDs[:0]
	for _, id := range req.StructureRigTypeIDs {
		if id > 0 {
			cleanedRigs = append(cleanedRigs, id)
		}
	}
	req.StructureRigTypeIDs = cleanedRigs
	req.BuildSystemName = strings.TrimSpace(req.BuildSystemName)

	// Stream progress and the final result over NDJSON, matching the
	// existing analyzeIndustry contract.
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, flushOK := w.(http.Flusher)
	if !flushOK {
		writeError(w, 500, "streaming not supported")
		return
	}

	// http.ResponseWriter is not safe for concurrent use. The worker pool below
	// emits progress lines from multiple goroutines, so every write goes through
	// this mutex. Also short-circuit on cancelled context so a goroutine that
	// outlives the handler doesn't touch a closed writer.
	var writeMu sync.Mutex
	ctx := r.Context()
	writeLine := func(payload interface{}) {
		line, err := json.Marshal(payload)
		if err != nil {
			return
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		select {
		case <-ctx.Done():
			return
		default:
		}
		fmt.Fprintf(w, "%s\n", line)
		flusher.Flush()
	}
	emitProgress := func(msg string) {
		writeLine(map[string]string{"type": "progress", "message": msg})
	}
	emitError := func(msg string) {
		writeLine(map[string]string{"type": "error", "message": msg})
	}

	var groups []blueprintGroup
	// Initialize to empty slice (not nil) so the response always marshals
	// `warnings: []` — the frontend iterates it directly.
	aggWarnings := []string{}

	if req.SkipBlueprintFetch {
		emitProgress("Re-pricing scanned blueprints...")
		// Look up blueprint names from the SDE so the response rows are
		// labelled the same as the original scan.
		s.mu.RLock()
		preFetchSDE := s.sdeData
		s.mu.RUnlock()
		// Dedupe reuse groups by (blueprintTypeID, IsBPO) — a single source
		// BP can produce multiple output rows (1 T1 mfg + N T2 invention
		// fan-out), so a naive client that echoes every output row back as
		// a reuse group would cause the fan-out to multiply on each refresh.
		type reuseKey struct {
			TypeID int32
			IsBPO  bool
		}
		seen := make(map[reuseKey]struct{}, len(req.ReuseGroups))
		groups = make([]blueprintGroup, 0, len(req.ReuseGroups))
		for _, rg := range req.ReuseGroups {
			if rg.BlueprintTypeID <= 0 {
				continue
			}
			k := reuseKey{TypeID: rg.BlueprintTypeID, IsBPO: rg.IsBPO}
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			name := blueprintDisplayName(rg.BlueprintTypeID, preFetchSDE)
			groups = append(groups, blueprintGroup{
				BlueprintTypeID: rg.BlueprintTypeID,
				BlueprintName:   name,
				IsBPO:           rg.IsBPO,
				OwnedQuantity:   rg.OwnedQuantity,
				AvailableRuns:   rg.AvailableRuns,
				ME:              rg.ME,
				TE:              rg.TE,
				LocationIDs:     append([]int64(nil), rg.LocationIDs...),
				Owned:           rg.Owned,
			})
		}
	} else {
		emitProgress("Fetching owned blueprints...")
		pool, warnings, _, err := s.aggregateOwnedBlueprints(userID, req.CharacterID, allScope, req.LocationIDs, req.DefaultBPCRuns, req.IncludeCorpBlueprints)
		if err != nil {
			emitError(err.Error())
			return
		}
		aggWarnings = warnings
		groups = groupBlueprintsByType(pool)
	}

	// Apply BPO/BPC filter. Default is BPO-only.
	bpFilter := strings.ToLower(strings.TrimSpace(req.BlueprintFilter))
	if bpFilter != "bpo" && bpFilter != "bpc" && bpFilter != "both" {
		bpFilter = "bpo"
	}
	if bpFilter != "both" {
		filtered := groups[:0]
		wantBPO := bpFilter == "bpo"
		for _, g := range groups {
			if g.IsBPO == wantBPO {
				filtered = append(filtered, g)
			}
		}
		groups = filtered
	}

	// Extend with "you don't own this but here's what the profit would look
	// like" rows. Only on a fresh scan — refresh uses reuse_groups verbatim
	// and the frontend has already tagged those with the owned flag. Synthetic
	// groups are always BPO with the user's chosen default ME/TE, suppressed
	// when the BPO/BPC filter is "bpc" (unowned BPCs would need invention).
	if req.IncludeUnowned && !req.SkipBlueprintFetch && bpFilter != "bpc" {
		s.mu.RLock()
		unownedSDE := s.sdeData
		s.mu.RUnlock()
		unownedGroups := synthesizeUnownedGroups(
			unownedSDE, groups,
			req.UnownedDefaultME, req.UnownedDefaultTE,
			req.IncludeT2Invention || req.IncludeT3Invention,
			req.IncludeReactions,
		)
		groups = append(groups, unownedGroups...)
	}

	stats := profitableScanStats{OwnedBlueprintGroups: len(groups)}

	// Map blueprint groups to their manufacturing product up front so we can
	// drop ones without a manufacturing activity (e.g. invention-only BPCs)
	// and report the count cleanly.
	s.mu.RLock()
	sdeData := s.sdeData
	analyzer := s.industryAnalyzer
	systemID := int32(0)
	if req.BuildSystemName != "" {
		systemID = sdeData.SystemByName[strings.ToLower(req.BuildSystemName)]
	}
	pricingSystemID := int32(0)
	if req.PricingSystemName != "" {
		pricingSystemID = sdeData.SystemByName[strings.ToLower(req.PricingSystemName)]
	}
	s.mu.RUnlock()

	work, skippedNoActivity := buildScanWork(groups, sdeData, req.IncludeT2Invention, req.IncludeT3Invention, req.IncludeReactions)
	stats.SkippedNoActivity += skippedNoActivity

	// Type-category filter — drop work items whose product isn't in the
	// caller's whitelist. Applied before the max-blueprints cap and before
	// the analyzer runs so filtered-out categories don't count toward either.
	// Empty slice or nil = include all categories (backward compatible).
	if len(req.TypeCategories) > 0 {
		allowed := make(map[int32]bool, len(req.TypeCategories))
		for _, c := range req.TypeCategories {
			allowed[c] = true
		}
		filtered := work[:0]
		for _, w := range work {
			pt, ok := sdeData.Types[w.productTypeID]
			if !ok || pt == nil {
				continue
			}
			if allowed[pt.CategoryID] {
				filtered = append(filtered, w)
			}
		}
		stats.SkippedFiltered += len(work) - len(filtered)
		work = filtered
	}

	// Default to the hard cap when unspecified — the UI no longer surfaces
	// this knob, and filters do the narrowing. Clamp to the hard cap so
	// any legacy client sending a large value stays inside the guard.
	maxBPs := req.MaxBlueprints
	if maxBPs <= 0 || maxBPs > profitableScanHardMaxBPs {
		maxBPs = profitableScanHardMaxBPs
	}
	if len(work) > maxBPs {
		stats.CapHit = len(work) - maxBPs
		work = work[:maxBPs]
	}

	rows := make([]profitableScanRow, 0, len(work))
	var rowsMu sync.Mutex

	// Scan-scoped memoization for the three heavy per-Analyze fetches.
	// Without this, every worker's Analyze() call re-fetches AND re-groups
	// the entire pricing region's order book (~500k orders per side in
	// The Forge). ESI itself caches the raw fetch, but groupIndustryOrdersByType
	// still iterates the whole list every call — for a 100-row scan that's
	// ~100M redundant iterations. Same story for adjusted prices and the
	// per-region best-ask price map: cached network, uncached aggregation.
	//
	// Every row in one scan uses the same PricingSystemID / StationID /
	// BuildSystem, so a single cached tuple covers the whole scan. Compute
	// lazy-on-first-hit rather than eagerly: work items that don't survive
	// filtering never trigger the fetch. sync.Once gives us "compute once,
	// block later callers until done, then return the cached value" for free.
	var (
		booksOnce                                 sync.Once
		cachedSell, cachedBuy                     map[int32][]esi.MarketOrder
		booksErr                                  error
		pricesOnce                                sync.Once
		cachedPrices                              map[int32]float64
		pricesErr                                 error
		adjustedOnce                              sync.Once
		cachedAdjusted                            map[int32]float64
		adjustedErr                               error
	)
	scanAnalyzer := *analyzer
	scanAnalyzer.SetMarketBooksOverride(func(p engine.IndustryParams) (map[int32][]esi.MarketOrder, map[int32][]esi.MarketOrder, error) {
		booksOnce.Do(func() {
			// Call the default path once via a temporary analyzer with
			// no override, so we don't recurse.
			tmp := *analyzer
			tmp.SetMarketBooksOverride(nil)
			cachedSell, cachedBuy, booksErr = tmp.LoadMarketBooksForParams(p)
		})
		return cachedSell, cachedBuy, booksErr
	})
	scanAnalyzer.SetMarketPricesOverride(func(p engine.IndustryParams) (map[int32]float64, error) {
		pricesOnce.Do(func() {
			tmp := *analyzer
			tmp.SetMarketPricesOverride(nil)
			cachedPrices, pricesErr = tmp.LoadMarketPricesForParams(p)
		})
		return cachedPrices, pricesErr
	})
	scanAnalyzer.SetAdjustedPricesOverride(func(_ *esi.IndustryCache) (map[int32]float64, error) {
		adjustedOnce.Do(func() {
			tmp := *analyzer
			tmp.SetAdjustedPricesOverride(nil)
			cachedAdjusted, adjustedErr = tmp.LoadAdjustedPrices()
		})
		return cachedAdjusted, adjustedErr
	})

	sem := make(chan struct{}, profitableScanWorkers)
	var wg sync.WaitGroup
	var progressMu sync.Mutex
	progressDone := 0

	// Per-scan history cache keyed by (regionID, productTypeID). Prevents
	// duplicate ESI fetches when the same product surfaces via multiple work
	// items (e.g. an unowned + owned pair, or T2 mfg + T2 invention of the
	// same output). The DB is authoritative long-term (24h cache); this map
	// is only for the duration of one scan.
	type historyKey struct {
		regionID int32
		typeID   int32
	}
	var historyCache sync.Map // historyKey → []esi.HistoryEntry

	startTime := time.Now()

	for _, w := range work {
		// If the client disconnected, stop dispatching new work but let
		// already-launched goroutines drain (they early-exit on ctx, and all
		// writes are gated by writeLine's ctx check anyway).
		if ctx.Err() != nil {
			break
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(item scanAnalyzeWork) {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}

			// ActivityMode: manufacturing by default; reaction for reaction
			// rows so the engine follows the reaction production path. T2/T3
			// invention rows override to "invention" per-decryptor below.
			rootActivity := "manufacturing"
			if item.scanMode == "reaction" {
				rootActivity = "reaction"
			}
			baseParams := engine.IndustryParams{
				TypeID:             item.productTypeID,
				Runs:               req.RunsPerJob,
				ActivityMode:       rootActivity,
				MaterialEfficiency: item.group.ME,
				TimeEfficiency:     item.group.TE,
				SystemID:           systemID,
				PricingSystemID:    pricingSystemID,
				StationID:          req.PricingStationID,
				FacilityTax:        req.FacilityTax,
				StructureBonus:     req.StructureBonus,
				BrokerFee:          req.BrokerFee,
				SalesTaxPercent:    req.SalesTaxPercent,
				MaxDepth:           req.MaxDepth,
				OwnBlueprint:       true,
				SkipReactions:      req.SkipReactions,
				StructureJobCostReduction: req.StructureJobCostReduction,
				StructureRigs: engine.StructureRigConfig{
					RigTypeIDs:      req.StructureRigTypeIDs,
					StructureTypeID: req.StructureTypeID,
				},
			}

			// IndustryAnalyzer stores per-call mutable state on the receiver
			// (adjustedPrices, marketPrices, marketSellOrders, marketBuyOrders,
			// systemCostIndices). Sharing one instance across the worker pool
			// makes those fields race, so different rows in the same scan end
			// up scoring against each other's price snapshots — the source of
			// the "wildly different ISK/h between rescans" symptom.
			//
			// Shallow-copy so each worker gets its own state. The underlying
			// SDE pointer, ESI client and IndustryCache are all goroutine-safe
			// (sync.Map / RWMutex internally), so it's safe to share them.
			// For T2 rows we reuse this single copy across all 9 decryptor
			// probes so the price cache warms once per row. Copies from
			// scanAnalyzer so each worker inherits the scan-scoped memoized
			// fetcher closures (books, prices, adjusted prices) — the first
			// worker to hit each closure pays the fetch/group cost; the rest
			// return the cached map instantly.
			localAnalyzer := scanAnalyzer

			var (
				result     *engine.IndustryAnalysis
				analyzeErr error
				bestDecKey string
			)

			if item.scanMode == "t2_invention" || item.scanMode == "t3_invention" {
				bestISKPerHour := -1.0e300
				for _, dec := range engine.Decryptors {
					meBase, teBase, outputRuns, chanceMult, cost := dec.EffectiveInventionParams()
					params := baseParams
					params.ActivityMode = "invention"
					params.MaterialEfficiency = meBase
					params.TimeEfficiency = teBase
					// Engine expects InventionChance as a percent (0-100);
					// baseProbability is a 0..1 fraction. Clamp to 100.
					chance := item.baseProbability * chanceMult * 100
					if chance > 100 {
						chance = 100
					}
					params.InventionChance = chance
					params.InventionOutputRuns = outputRuns
					params.DecryptorCost = cost
					// Round Runs UP to the nearest full BPC's worth so
					// invention amortization always spreads over a real
					// output. A default RunsPerJob of 1 against a 10-run
					// T2 BPC otherwise inflates unit invention cost by
					// 10x and flips a profitable row into a loss. If the
					// user set RunsPerJob higher (e.g. 30 wanting 3 BPCs
					// of a 10-run module) we still keep that intent and
					// only round up to the next full multiple.
					if outputRuns > 0 {
						bpcs := (req.RunsPerJob + outputRuns - 1) / outputRuns
						if bpcs < 1 {
							bpcs = 1
						}
						params.Runs = bpcs * outputRuns
					}

					r, err := localAnalyzer.Analyze(params, func(string) {})
					if err != nil {
						analyzeErr = err
						continue
					}
					if r.ISKPerHour > bestISKPerHour {
						bestISKPerHour = r.ISKPerHour
						result = r
						bestDecKey = dec.Key
						analyzeErr = nil
					}
				}
				// If every decryptor probe errored, analyzeErr remains set and
				// bestDecKey is empty — treated as a row error below.
				if bestDecKey == "" && analyzeErr == nil {
					analyzeErr = fmt.Errorf("no viable decryptor for %s", item.productName)
				}
			} else {
				result, analyzeErr = localAnalyzer.Analyze(baseParams, func(string) {})
			}

			progressMu.Lock()
			progressDone++
			done := progressDone
			progressMu.Unlock()
			emitProgress(fmt.Sprintf("Analyzed %d/%d: %s", done, len(work), item.group.BlueprintName))

			if analyzeErr != nil {
				rowsMu.Lock()
				stats.Errors++
				rowsMu.Unlock()
				return
			}

			scanMode := item.scanMode
			if scanMode == "" {
				scanMode = "t1_mfg"
			}
			var groupName, categoryName string
			if pt, ok := sdeData.Types[item.productTypeID]; ok && pt != nil {
				if g, ok := sdeData.Groups[pt.GroupID]; ok && g != nil {
					groupName = g.Name
					if c, ok := sdeData.Categories[g.CategoryID]; ok && c != nil {
						categoryName = c.Name
					}
				}
			}

			row := profitableScanRow{
				BlueprintTypeID:   item.group.BlueprintTypeID,
				BlueprintName:     item.group.BlueprintName,
				ProductTypeID:     item.productTypeID,
				ProductName:       item.productName,
				GroupName:         groupName,
				CategoryName:      categoryName,
				OwnedQuantity:     item.group.OwnedQuantity,
				IsBPO:             item.group.IsBPO,
				AvailableRuns:     item.group.AvailableRuns,
				ME:                item.group.ME,
				TE:                item.group.TE,
				LocationIDs:       append([]int64(nil), item.group.LocationIDs...),
				// For invention rows the analyzer bumps Runs up to a full
				// BPC's worth so amortization spreads correctly; result.Runs
				// reflects that bump. For non-invention rows it's just
				// req.RunsPerJob.
				Runs: result.Runs,
				Profit:            result.Profit,
				ProfitPercent:     result.ProfitPercent,
				ISKPerHour:        result.ISKPerHour,
				OptimalBuildCost:  result.OptimalBuildCost,
				SellRevenue:       result.SellRevenue,
				ManufacturingTime: result.ManufacturingTime,
				ScanMode:          scanMode,
				Owned:             item.group.Owned,
				OutputQtyPerRun:   item.outputQtyPerRun,
				TotalJobCost:      result.TotalJobCost,
				InventionCost:     result.InventionCost,
				TotalQuantity:     result.TotalQuantity,
			}
			row.TotalMaterialCost = result.TotalMaterialCost
			if result.TotalQuantity > 0 {
				row.UnitSellPrice = result.SellRevenue / float64(result.TotalQuantity)
			}
			if scanMode == "t2_invention" || scanMode == "t3_invention" {
				row.InventionSourceBPID = item.sourceBlueprintID
				row.InventionSourceBPName = item.sourceBlueprintName
				row.InventionOutputBPID = item.outputBlueprintID
				row.InventionOutputBPName = item.outputBlueprintName
				row.InventionProbability = result.InventionProbability
				row.ExpectedAttempts = result.InventionAttempts
				row.AttemptsCap = item.attemptsCap
				row.BestDecryptorKey = bestDecKey
				if item.attemptsCap >= 0 && result.InventionAttempts > float64(item.attemptsCap) {
					row.AttemptsCapExceeded = true
				}
			}

			// Period-profit / period-margin. Model: over the past N days the
			// market absorbed X units in this region; over N days at your
			// production rate you could produce Y units. Sellable = min(X, Y).
			//   PeriodProfit = per-unit profit × sellable
			//   PeriodMargin = PeriodProfit / (per-unit cost × Y)  — dividing
			//     by Y (not sellable) surfaces the "capital sitting in unsold
			//     inventory" penalty when Y > X.
			// Silently zero when history is unavailable (frontend renders "—").
			if result.RegionID > 0 && result.ManufacturingTime > 0 && item.productTypeID > 0 {
				var entries []esi.HistoryEntry
				hk := historyKey{regionID: result.RegionID, typeID: item.productTypeID}
				if cached, ok := historyCache.Load(hk); ok {
					entries = cached.([]esi.HistoryEntry)
				} else {
					// DB cache first (24h TTL); ESI on miss.
					if cachedEntries, ok := s.db.GetMarketHistory(result.RegionID, item.productTypeID); ok {
						entries = cachedEntries
					} else if fetched, err := s.esi.FetchMarketHistory(result.RegionID, item.productTypeID); err == nil {
						entries = fetched
						s.db.SetMarketHistory(result.RegionID, item.productTypeID, fetched)
					}
					historyCache.Store(hk, entries)
				}

				if len(entries) > 0 {
					cutoff := time.Now().UTC().AddDate(0, 0, -profitableScanPeriodDays).Format("2006-01-02")
					// Estimate the aggressive-buy fraction of daily volume using
					// the day's price position: how far up between low and high
					// the day's average traded. Average near the high → trades
					// clustered at the ask → mostly sell-order fills (aggressive
					// buys). Average near the low → mostly buy-order fills.
					//   fraction = (avg - low) / (high - low), clamped to [0, 1]
					// When high == low (thin day, one price), assume 0.5 as a
					// neutral default. This is what a builder actually cares
					// about: how many buyers came and hit their sell orders,
					// not total loot-liquidation churn.
					var volumeSum int64
					for _, e := range entries {
						if e.Date < cutoff {
							continue
						}
						spread := e.Highest - e.Lowest
						fraction := 0.5
						if spread > 0 {
							fraction = (e.Average - e.Lowest) / spread
							if fraction < 0 {
								fraction = 0
							} else if fraction > 1 {
								fraction = 1
							}
						}
						volumeSum += int64(float64(e.Volume) * fraction)
					}
					row.ProductDailyVolume = volumeSum / int64(profitableScanPeriodDays)

					totalQty := int32(1)
					if result.TotalQuantity > 0 {
						totalQty = result.TotalQuantity
					}
					periodSeconds := int64(profitableScanPeriodDays) * 86400
					secsPerUnit := float64(result.ManufacturingTime) / float64(totalQty)
					producible := int64(0)
					if secsPerUnit > 0 {
						producible = int64(float64(periodSeconds) / secsPerUnit)
					}
					// Scale the market's absorption cap by our realistic share
					// of it — a single builder captures a slice of daily
					// aggressive-buy volume, not the whole thing.
					sellable := int64(float64(volumeSum) * profitableScanMarketShare)
					if producible < sellable {
						sellable = producible
					}
					profitPerUnit := result.Profit / float64(totalQty)
					costPerUnit := result.OptimalBuildCost / float64(totalQty)
					row.PeriodProfit = profitPerUnit * float64(sellable)
					if producible > 0 && costPerUnit > 0 {
						totalCapital := costPerUnit * float64(producible)
						row.PeriodMargin = row.PeriodProfit / totalCapital * 100
					}
					row.PeriodDays = int32(profitableScanPeriodDays)
				}
			}

			passes := profitableScanRowPassesFilters(row, req)
			rowsMu.Lock()
			defer rowsMu.Unlock()
			stats.Analyzed++
			if !passes {
				stats.SkippedFiltered++
				return
			}
			rows = append(rows, row)
		}(w)
	}

	wg.Wait()

	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].ISKPerHour > rows[j].ISKPerHour
	})

	resp := profitableScanResponse{
		Rows:     rows,
		Warnings: aggWarnings,
		Stats:    stats,
	}

	log.Printf("[API] IndustryProfitableScan: groups=%d work_items=%d analyzed=%d filtered_out=%d errors=%d capHit=%d duration=%dms",
		stats.OwnedBlueprintGroups, len(work), stats.Analyzed, stats.SkippedFiltered, stats.Errors, stats.CapHit,
		time.Since(startTime).Milliseconds())

	// Diagnostic: if any single (source-BP, is_bpo) pair produced more than 5
	// rows, log the breakdown so we can catch fan-out multipliers early.
	if len(rows) > 0 {
		byKey := make(map[string]int, len(rows))
		nameByKey := make(map[string]string, len(rows))
		for _, r := range rows {
			k := fmt.Sprintf("%d-%v", r.BlueprintTypeID, r.IsBPO)
			byKey[k]++
			if _, ok := nameByKey[k]; !ok {
				nameByKey[k] = r.BlueprintName
			}
		}
		for k, n := range byKey {
			if n > 5 {
				log.Printf("[API] IndustryProfitableScan: high row count for source %s (%s): %d rows", k, nameByKey[k], n)
			}
		}
	}

	writeLine(map[string]interface{}{"type": "result", "data": resp})
}
