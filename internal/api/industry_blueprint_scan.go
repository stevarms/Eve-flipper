package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
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

// profitableScanDefaultMaxBPs is the default cap on distinct blueprint groups
// analyzed per scan when the request does not supply a value.
const profitableScanDefaultMaxBPs = 500

// profitableScanHardMaxBPs is the absolute ceiling regardless of what the
// client asks for — protection against pathological requests.
const profitableScanHardMaxBPs = 20000

// profitableScanWorkers is the analyzer fan-out for one scan.
const profitableScanWorkers = 5

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

	// MaxBlueprints caps how many BP groups this scan will analyze. Zero or
	// negative falls back to profitableScanDefaultMaxBPs. Capped at
	// profitableScanHardMaxBPs.
	MaxBlueprints int `json:"max_blueprints"`

	// BlueprintFilter selects which kinds of blueprints feed the analyzer:
	//   "bpo"  - originals only (default; treats stacks of BPCs as ignored)
	//   "bpc"  - copies only
	//   "both" - both
	// Empty / unknown values are treated as "bpo".
	BlueprintFilter string `json:"blueprint_filter"`

	// IncludeT2Invention, when true, fans each owned T1 BP that has an
	// invention activity out to its T2 products and scores them in
	// invention mode alongside the T1 manufacturing rows.
	IncludeT2Invention bool `json:"include_t2_invention"`

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
}

type profitableScanRow struct {
	BlueprintTypeID   int32   `json:"blueprint_type_id"`
	BlueprintName     string  `json:"blueprint_name"`
	ProductTypeID     int32   `json:"product_type_id"`
	ProductName       string  `json:"product_name"`
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

	// Invention-specific fields. Empty / zero for T1 manufacturing rows.
	ScanMode              string  `json:"scan_mode"`               // "t1_mfg" | "t2_invention"
	InventionSourceBPID   int32   `json:"invention_source_bp_id"`  // T1 BP typeID (0 for t1_mfg)
	InventionSourceBPName string  `json:"invention_source_bp_name"`
	InventionOutputBPID   int32   `json:"invention_output_bp_id"` // T2 BPC typeID (0 for t1_mfg)
	InventionOutputBPName string  `json:"invention_output_bp_name"`
	InventionProbability  float64 `json:"invention_probability"` // effective per-attempt (0..1)
	ExpectedAttempts      float64 `json:"expected_attempts"`
	AttemptsCap           int64   `json:"attempts_cap"` // -1 = unlimited (BPO source)
	AttemptsCapExceeded   bool    `json:"attempts_cap_exceeded"`
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

	scanMode             string // "t1_mfg" | "t2_invention"
	sourceBlueprintID    int32  // T1 BP typeID (invention only)
	sourceBlueprintName  string
	outputBlueprintID    int32 // T2 BPC typeID (invention only)
	outputBlueprintName  string
	effectiveProbability float64 // base × mult, clamped to (0, 1]
	attemptsCap          int64   // -1 = unlimited (BPO source)
}

// buildScanWork turns blueprint groups into per-row analyzer work items. For
// each group it emits at most one T1 manufacturing item plus (when
// includeT2Invention is true) one item per T2 product the group's BP can
// invent. skipped is the number of groups that produced no work items at all
// — the caller adds this to stats.SkippedNoActivity.
//
// Pure — extracted from handleAuthIndustryProfitableScan so tests can exercise
// the fan-out without spinning up an HTTP server or an SDE loader.
func buildScanWork(groups []blueprintGroup, sdeData *sde.Data, includeT2Invention bool, chanceMult float64) (work []scanAnalyzeWork, skipped int) {
	if sdeData == nil || sdeData.Industry == nil {
		return nil, len(groups)
	}
	if chanceMult <= 0 {
		chanceMult = 1.0
	}
	// Modules and other tradeable products are always in Types; BPs may not
	// be (invented T2 BPCs, some obscure T1 BPs) so route via the fallback.
	typeName := func(id int32) string {
		if t, ok := sdeData.Types[id]; ok && strings.TrimSpace(t.Name) != "" {
			return strings.TrimSpace(t.Name)
		}
		return fmt.Sprintf("Type %d", id)
	}

	work = make([]scanAnalyzeWork, 0, len(groups))
	for _, g := range groups {
		bp, ok := sdeData.Industry.Blueprints[g.BlueprintTypeID]
		if !ok {
			skipped++
			continue
		}
		emitted := false

		if mfg, mok := bp.Activities["manufacturing"]; mok && mfg != nil && len(mfg.Products) > 0 {
			productTypeID := mfg.Products[0].TypeID
			if productTypeID > 0 {
				work = append(work, scanAnalyzeWork{
					group:         g,
					productTypeID: productTypeID,
					productName:   typeName(productTypeID),
					scanMode:      "t1_mfg",
				})
				emitted = true
			}
		}

		if includeT2Invention {
			if inv, iok := bp.Activities["invention"]; iok && inv != nil {
				for _, invProduct := range inv.Products {
					if invProduct.TypeID <= 0 {
						continue
					}
					t2BP, tbok := sdeData.Industry.Blueprints[invProduct.TypeID]
					if !tbok || t2BP == nil {
						continue
					}
					t2Mfg, tmok := t2BP.Activities["manufacturing"]
					if !tmok || t2Mfg == nil || len(t2Mfg.Products) == 0 {
						continue
					}
					t2ModuleID := t2Mfg.Products[0].TypeID
					if t2ModuleID <= 0 {
						continue
					}
					baseChance := invProduct.Probability
					if baseChance <= 0 {
						continue
					}
					effChance := baseChance * chanceMult
					if effChance > 1 {
						effChance = 1
					}

					var cap int64 = -1
					if !g.IsBPO {
						cap = g.AvailableRuns
					}

					work = append(work, scanAnalyzeWork{
						group:                g,
						productTypeID:        t2ModuleID,
						productName:          typeName(t2ModuleID),
						scanMode:             "t2_invention",
						sourceBlueprintID:    g.BlueprintTypeID,
						sourceBlueprintName:  g.BlueprintName,
						outputBlueprintID:    invProduct.TypeID,
						outputBlueprintName:  blueprintDisplayName(invProduct.TypeID, sdeData),
						effectiveProbability: effChance,
						attemptsCap:          cap,
					})
					emitted = true
				}
			}
		}

		if !emitted {
			skipped++
		}
	}
	return work, skipped
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
	req.BrokerFee = clampFloat64(req.BrokerFee, 0, 100)
	req.SalesTaxPercent = clampFloat64(req.SalesTaxPercent, 0, 100)
	if req.PricingStationID < 0 {
		req.PricingStationID = 0
	}
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
		groups = make([]blueprintGroup, 0, len(req.ReuseGroups))
		for _, rg := range req.ReuseGroups {
			if rg.BlueprintTypeID <= 0 {
				continue
			}
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

	work, skippedNoActivity := buildScanWork(groups, sdeData, req.IncludeT2Invention, req.InventionChanceMult)
	stats.SkippedNoActivity += skippedNoActivity

	maxBPs := req.MaxBlueprints
	if maxBPs <= 0 {
		maxBPs = profitableScanDefaultMaxBPs
	}
	if maxBPs > profitableScanHardMaxBPs {
		maxBPs = profitableScanHardMaxBPs
	}
	if len(work) > maxBPs {
		stats.CapHit = len(work) - maxBPs
		work = work[:maxBPs]
	}

	rows := make([]profitableScanRow, 0, len(work))
	var rowsMu sync.Mutex

	sem := make(chan struct{}, profitableScanWorkers)
	var wg sync.WaitGroup
	var progressMu sync.Mutex
	progressDone := 0

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

			params := engine.IndustryParams{
				TypeID:             item.productTypeID,
				Runs:               req.RunsPerJob,
				ActivityMode:       "manufacturing",
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
			}

			if item.scanMode == "t2_invention" {
				params.ActivityMode = "invention"
				// The manufacturing step for T2 rows applies to the invented
				// T2 BPC, whose ME/TE come from the request (T2 base 2/4 plus
				// any decryptor deltas the frontend baked in).
				params.MaterialEfficiency = req.InventionMEBase
				params.TimeEfficiency = req.InventionTEBase
				// Engine expects InventionChance as a percent (0-100); we
				// carry the effective probability as a 0..1 fraction.
				params.InventionChance = item.effectiveProbability * 100
				params.InventionOutputRuns = req.InventionOutputRuns
				params.DecryptorCost = req.DecryptorCost
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
			localAnalyzer := *analyzer
			result, analyzeErr := localAnalyzer.Analyze(params, func(string) { /* discard inner progress */ })

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
			row := profitableScanRow{
				BlueprintTypeID:   item.group.BlueprintTypeID,
				BlueprintName:     item.group.BlueprintName,
				ProductTypeID:     item.productTypeID,
				ProductName:       item.productName,
				OwnedQuantity:     item.group.OwnedQuantity,
				IsBPO:             item.group.IsBPO,
				AvailableRuns:     item.group.AvailableRuns,
				ME:                item.group.ME,
				TE:                item.group.TE,
				LocationIDs:       append([]int64(nil), item.group.LocationIDs...),
				Runs:              req.RunsPerJob,
				Profit:            result.Profit,
				ProfitPercent:     result.ProfitPercent,
				ISKPerHour:        result.ISKPerHour,
				OptimalBuildCost:  result.OptimalBuildCost,
				SellRevenue:       result.SellRevenue,
				ManufacturingTime: result.ManufacturingTime,
				ScanMode:          scanMode,
			}
			if scanMode == "t2_invention" {
				row.InventionSourceBPID = item.sourceBlueprintID
				row.InventionSourceBPName = item.sourceBlueprintName
				row.InventionOutputBPID = item.outputBlueprintID
				row.InventionOutputBPName = item.outputBlueprintName
				row.InventionProbability = result.InventionProbability
				row.ExpectedAttempts = result.InventionAttempts
				row.AttemptsCap = item.attemptsCap
				if item.attemptsCap >= 0 && result.InventionAttempts > float64(item.attemptsCap) {
					row.AttemptsCapExceeded = true
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

	log.Printf("[API] IndustryProfitableScan: groups=%d analyzed=%d filtered_out=%d errors=%d capHit=%d duration=%dms",
		stats.OwnedBlueprintGroups, stats.Analyzed, stats.SkippedFiltered, stats.Errors, stats.CapHit,
		time.Since(startTime).Milliseconds())

	writeLine(map[string]interface{}{"type": "result", "data": resp})
}
