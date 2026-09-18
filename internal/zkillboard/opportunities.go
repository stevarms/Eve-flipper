package zkillboard

import (
	"sort"
	"sync"

	"eve-flipper/internal/esi"
)

// TradeOpportunity represents a potential trade based on war demand.
type TradeOpportunity struct {
	TypeID        int32   `json:"type_id"`
	TypeName      string  `json:"type_name"`
	Category      string  `json:"category"` // "ship", "module", "ammo", "drone"
	KillsPerDay   int     `json:"kills_per_day"`
	JitaPrice     float64 `json:"jita_price"`      // Best sell price in Jita
	RegionPrice   float64 `json:"region_price"`    // Best sell price in target region
	ProfitPerUnit float64 `json:"profit_per_unit"` // RegionPrice - JitaPrice
	ProfitPercent float64 `json:"profit_percent"`  // Profit margin %
	DailyVolume   int     `json:"daily_volume"`    // Estimated daily demand
	DailyProfit   float64 `json:"daily_profit"`    // Potential daily profit
	JitaVolume    int32   `json:"jita_volume"`     // Available volume in Jita
	RegionVolume  int32   `json:"region_volume"`   // Available volume in region
	DataSource    string  `json:"data_source"`     // "killmail" or "static"
	Volume        float64 `json:"volume"`          // Item volume in m³ (from SDE)
}

// RegionOpportunities contains all trade opportunities for a region.
type RegionOpportunities struct {
	RegionID       int32              `json:"region_id"`
	RegionName     string             `json:"region_name"`
	Status         string             `json:"status"`
	HotScore       float64            `json:"hot_score"`
	SecurityClass  string             `json:"security_class"`  // "highsec", "lowsec", "nullsec"
	SecurityBlocks []string           `json:"security_blocks"` // ["high", "low", "null"] for display
	JumpsFromJita  int                `json:"jumps_from_jita"` // Distance from Jita
	MainSystem     string             `json:"main_system"`     // Main hub/system name
	Ships          []TradeOpportunity `json:"ships"`
	Modules        []TradeOpportunity `json:"modules"`
	Ammo           []TradeOpportunity `json:"ammo"`
	TotalPotential float64            `json:"total_potential"` // Sum of daily profits
}

// Common PvP modules -- fallback when killmail data is not available.
//
// Every ID here was wrong until 2026-09-17, and wrong in a specific way: the IDs
// were real published items, just shifted off their names by roughly one row. ID
// 2281 was labelled "Damage Control II" but is Multispectrum Shield Hardener II;
// 11269 was labelled "1600mm Steel Plates II" but is Multispectrum Energized
// Membrane II. So WarTracker's fallback path was pricing real items under other
// items' names -- the worst failure mode available, because every figure looked
// plausible. Two names were also pre-2018 and no longer resolve at all; they are
// updated to the items they were renamed to.
//
// TestStaticListsMatchTheSDE pins every pair against the local SDE.
var commonPvPModules = []struct {
	TypeID   int32
	Name     string
	Category string
}{
	// Shield modules
	{3841, "Large Shield Extender II", "module"},
	{3831, "Medium Shield Extender II", "module"},
	{2048, "Damage Control II", "module"},
	{2281, "Multispectrum Shield Hardener II", "module"}, // was "Adaptive Invulnerability Field II"

	// Armor modules
	{20353, "1600mm Steel Plates II", "module"},
	{11269, "Multispectrum Energized Membrane II", "module"}, // was "Energized Adaptive Nano Membrane II"

	// Tackle
	{3244, "Warp Disruptor II", "module"},
	{448, "Warp Scrambler II", "module"},
	{527, "Stasis Webifier II", "module"},

	// Propulsion
	{12076, "50MN Microwarpdrive II", "module"},
	{12084, "500MN Microwarpdrive II", "module"},
	{12058, "10MN Afterburner II", "module"},

	// Weapon upgrades
	{519, "Gyrostabilizer II", "module"},
	{22291, "Ballistic Control System II", "module"},
	{2364, "Heat Sink II", "module"},
}

// Common ammo types -- fallback when killmail data is not available.
//
// Corrected 2026-09-17 alongside commonPvPModules, and the same failure: 248 was
// labelled "Void M" but is Microwave M, 233 was "Antimatter Charge L" but is
// Iridium Charge L, and 2203 was "EMP L" but is Acolyte I -- a drone sitting in
// the projectile block. "Fury Heavy Missile" is not an item at all; Fury missiles
// carry a damage type, and the ID that entry held is Scourge Fury Heavy Missile.
var commonAmmo = []struct {
	TypeID   int32
	Name     string
	Category string
}{
	// Hybrid charges
	{238, "Antimatter Charge L", "ammo"},
	{230, "Antimatter Charge M", "ammo"},
	{222, "Antimatter Charge S", "ammo"},
	{12791, "Void L", "ammo"},
	{12789, "Void M", "ammo"},
	{12612, "Void S", "ammo"},
	{12787, "Null L", "ammo"},
	{12785, "Null M", "ammo"},
	{12614, "Null S", "ammo"},

	// Projectile ammo
	{201, "EMP L", "ammo"},
	{193, "EMP M", "ammo"},
	{185, "EMP S", "ammo"},
	{12779, "Hail L", "ammo"},
	{12777, "Hail M", "ammo"},
	{12608, "Hail S", "ammo"},
	{12775, "Barrage L", "ammo"},
	{12773, "Barrage M", "ammo"},
	{12625, "Barrage S", "ammo"},

	// Laser crystals
	{12820, "Scorch L", "ammo"},
	{12818, "Scorch M", "ammo"},
	{12563, "Scorch S", "ammo"},
	{12816, "Conflagration L", "ammo"},
	{12814, "Conflagration M", "ammo"},
	{12565, "Conflagration S", "ammo"},

	// Missiles
	{27441, "Caldari Navy Scourge Heavy Missile", "ammo"},
	{27435, "Caldari Navy Mjolnir Heavy Missile", "ammo"},
	{2629, "Scourge Fury Heavy Missile", "ammo"}, // was the non-existent "Fury Heavy Missile"
	{24519, "Nova Rage Torpedo", "ammo"},

	// Drones. Categorized "ammo" because the static path appends straight into
	// result.Ammo, which is also where the killmail path sends drones for display.
	{2456, "Hobgoblin II", "ammo"},
	{2185, "Hammerhead II", "ammo"},
	{2446, "Ogre II", "ammo"},
	{2488, "Warrior II", "ammo"},
	{21640, "Valkyrie II", "ammo"},
	{2478, "Berserker II", "ammo"},

	// Nanite paste
	{28668, "Nanite Repair Paste", "ammo"},

	// Cap boosters
	{11289, "Cap Booster 800", "ammo"},
	{11287, "Cap Booster 400", "ammo"},
}

const jitaRegionID = int32(10000002) // The Forge

// GetRegionOpportunities analyzes trade opportunities for a war region.
// If fittingProfile is non-nil, uses real killmail data instead of hardcoded lists.
func (d *DemandAnalyzer) GetRegionOpportunities(regionID int32, esiClient *esi.Client, fittingProfile *RegionDemandProfile) (*RegionOpportunities, error) {
	// Get region stats from zkillboard
	stats, err := d.client.GetRegionStats(regionID)
	if err != nil {
		return nil, err
	}

	zone := d.analyzeRegion(regionID, stats)
	if zone == nil {
		return nil, nil
	}

	result := &RegionOpportunities{
		RegionID:   regionID,
		RegionName: zone.RegionName,
		Status:     zone.Status,
		HotScore:   zone.HotScore,
	}

	// Determine data source
	useFittingData := fittingProfile != nil && len(fittingProfile.Items) > 0
	dataSource := "static"
	if useFittingData {
		dataSource = "killmail"
	}

	// Collect all type IDs we need prices for
	typeIDs := make(map[int32]struct{})
	shipKills := make(map[int32]int)          // typeID -> kills (from zkillboard stats)
	fittingDemand := make(map[int32]float64)  // typeID -> est_daily_demand (from killmails)
	fittingCategory := make(map[int32]string) // typeID -> category
	fittingNames := make(map[int32]string)    // typeID -> name

	if useFittingData {
		// Use real killmail data
		for typeID, profile := range fittingProfile.Items {
			if profile.EstDailyDemand < 1 {
				continue
			}
			typeIDs[typeID] = struct{}{}
			fittingDemand[typeID] = profile.EstDailyDemand
			fittingCategory[typeID] = profile.Category
			fittingNames[typeID] = profile.TypeName
		}
	} else {
		// Fallback: use hardcoded lists + top ships from zkillboard
		for _, list := range stats.TopLists {
			if list.Type == "shipType" {
				for _, v := range list.Values {
					if v.ShipTypeID > 0 {
						typeIDs[v.ShipTypeID] = struct{}{}
						shipKills[v.ShipTypeID] = v.Kills
					}
				}
			}
		}
		for _, m := range commonPvPModules {
			typeIDs[m.TypeID] = struct{}{}
		}
		for _, a := range commonAmmo {
			typeIDs[a.TypeID] = struct{}{}
		}
	}

	// Convert to slice
	typeIDSlice := make([]int32, 0, len(typeIDs))
	for id := range typeIDs {
		typeIDSlice = append(typeIDSlice, id)
	}

	// Fetch prices in parallel
	jitaPrices := make(map[int32]priceInfo)
	regionPrices := make(map[int32]priceInfo)
	var wg sync.WaitGroup
	var mu sync.Mutex

	wg.Add(1)
	go func() {
		defer wg.Done()
		prices := fetchBestPrices(esiClient, jitaRegionID, typeIDSlice, priceFilter{})
		mu.Lock()
		jitaPrices = prices
		mu.Unlock()
	}()

	if regionID != jitaRegionID {
		wg.Add(1)
		go func() {
			defer wg.Done()
			prices := fetchBestPrices(esiClient, regionID, typeIDSlice, priceFilter{})
			mu.Lock()
			regionPrices = prices
			mu.Unlock()
		}()
	}

	wg.Wait()

	// Build opportunities
	if useFittingData {
		// Dynamic mode: use real killmail data
		for typeID, demand := range fittingDemand {
			jita := jitaPrices[typeID]
			region := regionPrices[typeID]
			if jita.sellPrice <= 0 {
				continue
			}

			category := fittingCategory[typeID]
			name := fittingNames[typeID]
			dailyDemand := int(demand)
			if dailyDemand < 1 {
				dailyDemand = 1
			}

			opp := buildOpportunity(typeID, name, category, dailyDemand, jita, region)
			if opp == nil {
				continue
			}
			opp.DataSource = dataSource

			// Apply category-specific margin thresholds
			switch category {
			case "ship":
				if opp.ProfitPercent > 5 {
					result.Ships = append(result.Ships, *opp)
				}
			case "module":
				if opp.ProfitPercent > 10 {
					result.Modules = append(result.Modules, *opp)
				}
			case "ammo":
				if opp.ProfitPercent > 15 {
					result.Ammo = append(result.Ammo, *opp)
				}
			case "drone":
				// Drones go into ammo category for display
				if opp.ProfitPercent > 10 {
					opp.Category = "ammo"
					result.Ammo = append(result.Ammo, *opp)
				}
			}
		}
	} else {
		// Static mode: use hardcoded lists (original behavior)
		for typeID, kills := range shipKills {
			jita := jitaPrices[typeID]
			region := regionPrices[typeID]
			if jita.sellPrice > 0 {
				opp := buildOpportunity(typeID, "", "ship", kills, jita, region)
				if opp != nil && opp.ProfitPercent > 5 {
					opp.DataSource = dataSource
					result.Ships = append(result.Ships, *opp)
				}
			}
		}

		for _, m := range commonPvPModules {
			jita := jitaPrices[m.TypeID]
			region := regionPrices[m.TypeID]
			if jita.sellPrice > 0 {
				estimatedKills := int(float64(zone.KillsToday) * 0.5)
				opp := buildOpportunity(m.TypeID, m.Name, m.Category, estimatedKills, jita, region)
				if opp != nil && opp.ProfitPercent > 10 {
					opp.DataSource = dataSource
					result.Modules = append(result.Modules, *opp)
				}
			}
		}

		for _, a := range commonAmmo {
			jita := jitaPrices[a.TypeID]
			region := regionPrices[a.TypeID]
			if jita.sellPrice > 0 {
				estimatedKills := int(float64(zone.KillsToday) * 100)
				opp := buildOpportunity(a.TypeID, a.Name, a.Category, estimatedKills, jita, region)
				if opp != nil && opp.ProfitPercent > 15 {
					opp.DataSource = dataSource
					result.Ammo = append(result.Ammo, *opp)
				}
			}
		}
	}

	// Sort by daily profit
	sort.Slice(result.Ships, func(i, j int) bool {
		return result.Ships[i].DailyProfit > result.Ships[j].DailyProfit
	})
	sort.Slice(result.Modules, func(i, j int) bool {
		return result.Modules[i].DailyProfit > result.Modules[j].DailyProfit
	})
	sort.Slice(result.Ammo, func(i, j int) bool {
		return result.Ammo[i].DailyProfit > result.Ammo[j].DailyProfit
	})

	// Limit results
	if len(result.Ships) > 10 {
		result.Ships = result.Ships[:10]
	}
	if len(result.Modules) > 10 {
		result.Modules = result.Modules[:10]
	}
	if len(result.Ammo) > 10 {
		result.Ammo = result.Ammo[:10]
	}

	// Calculate total potential
	for _, s := range result.Ships {
		result.TotalPotential += s.DailyProfit
	}
	for _, m := range result.Modules {
		result.TotalPotential += m.DailyProfit
	}
	for _, a := range result.Ammo {
		result.TotalPotential += a.DailyProfit
	}

	return result, nil
}

type priceInfo struct {
	sellPrice  float64
	sellVolume int32
	// totalVolume is every unit listed, not just those at the best price. Days of
	// cover is stocked units over units destroyed per day, and "stocked" means the
	// whole book -- 500,035 rounds of Void M spread over many orders is 28 days of
	// cover whether or not they share a price.
	totalVolume int64
	// orderCount and sellerLevels describe the shape of the book at the best
	// price, which is what decides whether a cheaper competitor is worth
	// undercutting or stepping over.
	orderCount int
}

// priceFilter narrows what fetchBestPrices counts. The zero value is region-wide
// and counts everything, which is exactly what WarTracker asked for before this
// existed -- so its results are unchanged.
type priceFilter struct {
	// LocationID restricts to one station. A campaign prices against the book at
	// its own destination, not the region: Onnamon and Villasen are both Black
	// Rise and their markups differ by 12 points.
	LocationID int64
	// ExcludeNPCSeeded drops 365-day orders. Off by default because turning it on
	// silently would change WarTracker's numbers.
	ExcludeNPCSeeded bool
}

func fetchBestPrices(esiClient *esi.Client, regionID int32, typeIDs []int32, filter priceFilter) map[int32]priceInfo {
	orders, err := esiClient.FetchRegionOrders(regionID, "sell")
	if err != nil {
		return make(map[int32]priceInfo)
	}
	return aggregateSellBook(orders, filter)
}

// aggregateSellBook reduces a sell book to one priceInfo per type. It is the
// whole of fetchBestPrices except the fetch, so the filters and the depth
// arithmetic are testable without ESI.
func aggregateSellBook(orders []esi.MarketOrder, filter priceFilter) map[int32]priceInfo {
	prices := make(map[int32]priceInfo)

	// Find best (lowest) sell price for each type
	for _, order := range orders {
		if filter.LocationID != 0 && order.LocationID != filter.LocationID {
			continue
		}
		if filter.ExcludeNPCSeeded && order.IsNPCSeeded() {
			continue
		}

		existing, ok := prices[order.TypeID]
		// Stocked depth accumulates across every order, independently of price.
		existing.totalVolume += int64(order.VolumeRemain)

		switch {
		case !ok || order.Price < existing.sellPrice:
			existing.sellPrice = order.Price
			existing.sellVolume = order.VolumeRemain
			existing.orderCount = 1
		case order.Price == existing.sellPrice:
			// Same price, add volume
			existing.sellVolume += order.VolumeRemain
			existing.orderCount++
		}
		prices[order.TypeID] = existing
	}

	return prices
}

// defaultSellFeePercent is the estimated total sell-side fee (broker + sales tax).
// Assumes moderate skills: ~3% broker fee + ~5% sales tax = ~8%.
const defaultSellFeePercent = 8.0

func buildOpportunity(typeID int32, name string, category string, kills int, jita, region priceInfo) *TradeOpportunity {
	if jita.sellPrice <= 0 {
		return nil
	}

	sellPrice := region.sellPrice
	noSupply := false

	if sellPrice <= 0 || sellPrice <= jita.sellPrice {
		if region.sellVolume == 0 && jita.sellPrice > 0 {
			// No supply in region — use Jita price as reference but mark as "no supply"
			// The frontend already shows "NO COMPETITION" badge and suggested price.
			// We don't fabricate a fake profit; instead use Jita price so margin = 0
			// and let the frontend handle the display.
			noSupply = true
			sellPrice = 0
		} else {
			return nil
		}
	}

	// FIX #1: Account for fees when selling in the target region.
	// Net revenue per unit = regionPrice * (1 - feePercent/100) - jitaPrice
	var profitPerUnit float64
	if noSupply {
		// For empty regions, estimate minimum viable margin (30% markup over Jita + fees)
		profitPerUnit = jita.sellPrice * 0.30 * (1 - defaultSellFeePercent/100)
		sellPrice = jita.sellPrice * 1.30
	} else {
		netSellPrice := sellPrice * (1 - defaultSellFeePercent/100)
		profitPerUnit = netSellPrice - jita.sellPrice
		if profitPerUnit <= 0 {
			return nil
		}
	}

	profitPercent := (profitPerUnit / jita.sellPrice) * 100
	dailyVolume := kills
	if dailyVolume < 1 {
		dailyVolume = 1
	}

	return &TradeOpportunity{
		TypeID:        typeID,
		TypeName:      name,
		Category:      category,
		KillsPerDay:   kills,
		JitaPrice:     jita.sellPrice,
		RegionPrice:   sellPrice,
		ProfitPerUnit: profitPerUnit,
		ProfitPercent: profitPercent,
		DailyVolume:   dailyVolume,
		DailyProfit:   profitPerUnit * float64(dailyVolume),
		JitaVolume:    jita.sellVolume,
		RegionVolume:  region.sellVolume,
	}
}
