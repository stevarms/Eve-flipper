package api

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
	"sync"

	"eve-flipper/internal/sde"
)

// piTypeName resolves a typeID to a human-readable name using the SDE, or
// falls back to "Type <id>" so unresolved rows still render cleanly.
func piTypeName(data *sde.Data, typeID int32) string {
	if data == nil {
		return "Type " + strconv.Itoa(int(typeID))
	}
	if t, ok := data.Types[typeID]; ok && t.Name != "" {
		return t.Name
	}
	return "Type " + strconv.Itoa(int(typeID))
}

// piFactoryConfig is one entry in the user's factory portfolio.
type piFactoryConfig struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	SchematicID  int32  `json:"schematic_id"`
	FactoryCount int    `json:"factory_count"`
}

// piFactoryRequest bundles everything the planner needs. The station ID
// drives both the input buy-price lookups and the output sell-price
// lookups (single-hub model in v1). Tax fields are user-configurable
// because customs office rates vary by owner (5%/10% NPC, 0-100% player).
type piFactoryRequest struct {
	StationID        int64             `json:"station_id"`
	PocoTaxPercent   float64           `json:"poco_tax_percent"`   // POCO tax applied to BOTH import and output sides (default 15%)
	SalesTaxPercent  float64           `json:"sales_tax_percent"`  // your character's sales tax on the final sale
	BrokerFeePercent float64           `json:"broker_fee_percent"` // your character's broker fee
	BufferDays       float64           `json:"buffer_days"`        // shopping-list horizon; 7 default
	Factories        []piFactoryConfig `json:"factories"`
}

type piMaterialRow struct {
	TypeID       int32   `json:"type_id"`
	TypeName     string  `json:"type_name"`
	QtyPerDay    float64 `json:"qty_per_day"`
	BuyPrice     float64 `json:"buy_price,omitempty"`
	SellPrice    float64 `json:"sell_price,omitempty"`
	BasePrice    float64 `json:"base_price,omitempty"`
	Source       string  `json:"source,omitempty"` // station|region|avg|none
	CostPerDay   float64 `json:"cost_per_day,omitempty"`
	UnitVolume   float64 `json:"unit_volume,omitempty"`   // m³ per unit from SDE
	VolumePerDay float64 `json:"volume_per_day,omitempty"` // qty_per_day × unit_volume
}

// piFactoryResult is the computed economics for one factory config.
type piFactoryResult struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	SchematicID     int32           `json:"schematic_id"`
	SchematicName   string          `json:"schematic_name"`
	OutputTier      string          `json:"output_tier,omitempty"` // "P1" / "P2" / "P3" / "P4" — for card styling
	FactoryCount    int             `json:"factory_count"`
	CycleTimeSec    int32           `json:"cycle_time_sec"`
	CyclesPerDay    float64         `json:"cycles_per_day"`
	Inputs          []piMaterialRow `json:"inputs"`
	Output          piMaterialRow   `json:"output"`
	OutputUndercut  float64         `json:"output_undercut,omitempty"`   // suggested list price = undercut of low sell
	InputCostPerDay   float64 `json:"input_cost_per_day"`
	PocoTaxPerDay     float64 `json:"poco_tax_per_day"`     // combined import + export POCO tax
	PocoImportPerDay  float64 `json:"poco_import_per_day"`  // import-side breakdown for tooltip
	PocoExportPerDay  float64 `json:"poco_export_per_day"`  // export-side breakdown for tooltip
	SalesFeesPerDay float64         `json:"sales_fees_per_day"`
	GrossRevPerDay  float64         `json:"gross_rev_per_day"`
	NetProfitPerDay float64         `json:"net_profit_per_day"`
	// InputSaleValuePerDay is what you'd net if you sold the raw inputs at
	// the hub instead of feeding them to this factory. No POCO tax on this
	// path (nothing ever hits the planet). Net of sales tax + broker.
	InputSaleValuePerDay float64 `json:"input_sale_value_per_day"`
	// OutputSaleValuePerDay is what you'd net selling the output at the hub,
	// assuming you got the inputs for free (they were manufactured or
	// inherited). Net of sales tax + broker + POCO export tax. Paired with
	// InputSaleValuePerDay for a direct A/B: sell inputs raw vs process
	// them and sell the output.
	OutputSaleValuePerDay float64 `json:"output_sale_value_per_day"`
	// InputVolumePerDay is the total m³ of inputs consumed per day. Combined
	// with the frontend's launchpad-capacity setting it tells the user how
	// long one launchpad load will feed the factory.
	InputVolumePerDay float64 `json:"input_volume_per_day"`
	// BuyOutputCost is what the same qty of output would cost you if you
	// just bought it at the hub (using low sell). Lets the frontend show
	// build-vs-buy: savings = BuyOutputCost - InputCost - taxes.
	BuyOutputCostPerDay float64 `json:"buy_output_cost_per_day"`
	// SavingsVsBuy is the delta if you build vs buy (positive = building
	// wins). Includes PI taxes but excludes the sale side (this is a
	// "supply your own manufacturing" scenario).
	SavingsVsBuyPerDay float64 `json:"savings_vs_buy_per_day"`
	Unresolved         bool    `json:"unresolved,omitempty"`
	UnresolvedReason   string  `json:"unresolved_reason,omitempty"`
}

// piShoppingRow is one entry in the aggregate shopping list — all inputs
// across every factory rolled up by typeID.
type piShoppingRow struct {
	TypeID       int32   `json:"type_id"`
	TypeName     string  `json:"type_name"`
	QtyPerDay    float64 `json:"qty_per_day"`
	QtyBuffer    int64   `json:"qty_buffer"` // ceil(qty_per_day * buffer_days)
	BuyPrice     float64 `json:"buy_price,omitempty"`  // top-buy (bid), reference only
	SellPrice    float64 `json:"sell_price,omitempty"` // low-sell (ask) — what CostBuffer is priced from
	CostBuffer   float64 `json:"cost_buffer,omitempty"`
	Source       string  `json:"source,omitempty"`
	UnitVolume   float64 `json:"unit_volume,omitempty"`
	VolumeBuffer float64 `json:"volume_buffer,omitempty"` // qty_buffer × unit_volume, for hauling planning
}

type piFactoryResponse struct {
	Results     []piFactoryResult `json:"results"`
	Shopping    []piShoppingRow   `json:"shopping"`
	StationID   int64             `json:"station_id"`
	StationName string            `json:"station_name"`
	BufferDays  float64           `json:"buffer_days"`
}

// piSchematicSummary is the compact schematic form sent to the frontend so
// the user can pick a recipe from a dropdown. Kept flat for easy filtering.
type piSchematicSummary struct {
	ID           int32                  `json:"id"`
	Name         string                 `json:"name"`
	CycleTimeSec int32                  `json:"cycle_time_sec"`
	Inputs       []piSchematicMaterial  `json:"inputs"`
	Output       piSchematicMaterial    `json:"output,omitempty"`
	OutputTier   string                 `json:"output_tier,omitempty"` // "P1"/"P2"/"P3"/"P4"
}

type piSchematicMaterial struct {
	TypeID   int32  `json:"type_id"`
	TypeName string `json:"type_name"`
	Qty      int64  `json:"qty"`
	Tier     string `json:"tier,omitempty"`
}

// EVE PI market group IDs, verified against types.jsonl:
//   1333 = P0 Raw, 1334 = P1 Basic, 1335 = P2 Refined,
//   1336 = P3 Specialized, 1337 = P4 Advanced.
// P0 is included so the customs-office tax on raw inputs is computed at
// the right per-unit base value; P0 items are still not shown in the
// schematic picker because there's no P0 recipe (they're extraction-only).
func piTier(data *sde.Data, typeID int32) string {
	if data == nil {
		return ""
	}
	t, ok := data.Types[typeID]
	if !ok {
		return ""
	}
	switch t.MarketGroupID {
	case 1333:
		return "P0"
	case 1334:
		return "P1"
	case 1335:
		return "P2"
	case 1336:
		return "P3"
	case 1337:
		return "P4"
	}
	return ""
}

// pocoBaseValueByTier is CCP's fixed per-unit "base value" per PI tier,
// used by customs offices for import/export tax calculation. Independent of
// market price — a P2 is always 7,200 ISK for tax purposes even if the
// hub sell price is 3× that. Source: EVE PI mechanics (community-verified).
var pocoBaseValueByTier = map[string]float64{
	"P0": 5,
	"P1": 400,
	"P2": 7200,
	"P3": 60000,
	"P4": 1200000,
}

// pocoBaseValue returns the fixed customs-office base value for a given
// typeID's PI tier, or 0 for anything not in the PI tier system.
func pocoBaseValue(data *sde.Data, typeID int32) float64 {
	return pocoBaseValueByTier[piTier(data, typeID)]
}

func (s *Server) handlePISchematics(w http.ResponseWriter, r *http.Request) {
	if !s.isReady() {
		writeError(w, http.StatusServiceUnavailable, "SDE not loaded yet")
		return
	}
	s.mu.RLock()
	sdeData := s.sdeData
	s.mu.RUnlock()
	if sdeData == nil || sdeData.Industry == nil {
		writeError(w, http.StatusInternalServerError, "industry data not loaded")
		return
	}

	out := make([]piSchematicSummary, 0, len(sdeData.Industry.PlanetSchematics))
	for _, sc := range sdeData.Industry.PlanetSchematics {
		item := piSchematicSummary{
			ID:           sc.ID,
			Name:         sc.Name,
			CycleTimeSec: sc.CycleTime,
		}
		for _, in := range sc.Inputs {
			item.Inputs = append(item.Inputs, piSchematicMaterial{
				TypeID:   in.TypeID,
				TypeName: piTypeName(sdeData, in.TypeID),
				Qty:      in.Quantity,
				Tier:     piTier(sdeData, in.TypeID),
			})
		}
		if len(sc.Outputs) > 0 {
			o := sc.Outputs[0]
			item.Output = piSchematicMaterial{
				TypeID:   o.TypeID,
				TypeName: piTypeName(sdeData, o.TypeID),
				Qty:      o.Quantity,
				Tier:     piTier(sdeData, o.TypeID),
			}
			item.OutputTier = item.Output.Tier
		}
		out = append(out, item)
	}
	// Sort P4 → P1 (highest tier first) so the more valuable schematics
	// bubble to the top; alphabetical within each tier.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].OutputTier != out[j].OutputTier {
			return out[i].OutputTier > out[j].OutputTier
		}
		return out[i].Name < out[j].Name
	})
	writeJSON(w, map[string]any{"schematics": out})
}

func (s *Server) handlePIFactoryPlan(w http.ResponseWriter, r *http.Request) {
	var req piFactoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !s.isReady() {
		writeError(w, http.StatusServiceUnavailable, "SDE not loaded yet")
		return
	}
	if req.StationID <= 0 {
		writeError(w, http.StatusBadRequest, "station_id required")
		return
	}
	if len(req.Factories) == 0 {
		writeError(w, http.StatusBadRequest, "factories array required")
		return
	}
	if req.BufferDays <= 0 {
		req.BufferDays = 7
	}

	s.mu.RLock()
	sdeData := s.sdeData
	s.mu.RUnlock()

	station, ok := sdeData.Stations[req.StationID]
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown station")
		return
	}
	system, ok := sdeData.Systems[station.SystemID]
	if !ok {
		writeError(w, http.StatusInternalServerError, "station has no system")
		return
	}
	regionID := system.RegionID

	// Fallback price cache (populated on demand, once per typeID).
	avgByType := map[int32]float64{}
	if prices, err := s.esi.FetchMarketPrices(); err == nil {
		for _, p := range prices {
			if p.AveragePrice > 0 {
				avgByType[p.TypeID] = p.AveragePrice
			}
		}
	}

	// Collect every unique typeID we need a price for (all inputs + all outputs).
	type priced struct {
		buy       float64
		sell      float64
		source    string
		basePrice float64
	}
	needed := map[int32]bool{}
	for _, f := range req.Factories {
		schem, ok := sdeData.Industry.PlanetSchematics[f.SchematicID]
		if !ok {
			continue
		}
		for _, in := range schem.Inputs {
			needed[in.TypeID] = true
		}
		for _, out := range schem.Outputs {
			needed[out.TypeID] = true
		}
	}
	typeIDs := make([]int32, 0, len(needed))
	for id := range needed {
		typeIDs = append(typeIDs, id)
	}
	sort.Slice(typeIDs, func(i, j int) bool { return typeIDs[i] < typeIDs[j] })

	// Fan out per-type order fetches (worker pool of 8, mirrors price-audit).
	priceByType := make(map[int32]priced, len(typeIDs))
	var mu sync.Mutex
	const workers = 8
	jobCh := make(chan int32)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for typeID := range jobCh {
				p := priced{}
				if it, ok := sdeData.Types[typeID]; ok {
					p.basePrice = it.BasePrice
				}
				orders, err := s.esi.FetchRegionOrdersByType(regionID, typeID)
				if err == nil {
					top, low := aggregateStationBook(orders, req.StationID)
					if top > 0 || low > 0 {
						p.buy = top
						p.sell = low
						p.source = "station"
					} else {
						regionLow := aggregateRegionLowSell(orders)
						if regionLow > 0 {
							p.sell = regionLow
							p.source = "region"
						}
					}
				}
				if p.source == "" {
					if avg, ok := avgByType[typeID]; ok && avg > 0 {
						p.buy = avg
						p.sell = avg
						p.source = "avg"
					} else {
						p.source = "none"
					}
				}
				// If we found a station/region sell but no station buy, fall
				// back to the same sell as the input-purchase price (the user
				// would end up instant-buying to fill the launchpad).
				if p.buy == 0 && p.sell > 0 {
					p.buy = p.sell
				}
				mu.Lock()
				priceByType[typeID] = p
				mu.Unlock()
			}
		}()
	}
	for _, id := range typeIDs {
		jobCh <- id
	}
	close(jobCh)
	wg.Wait()

	// Compute per-factory economics.
	results := make([]piFactoryResult, 0, len(req.Factories))
	// Aggregate shopping list across all factories, keyed by input typeID.
	aggQty := map[int32]float64{}
	aggName := map[int32]string{}
	for _, f := range req.Factories {
		res := piFactoryResult{
			ID:           f.ID,
			Name:         f.Name,
			SchematicID:  f.SchematicID,
			FactoryCount: f.FactoryCount,
		}
		schem, ok := sdeData.Industry.PlanetSchematics[f.SchematicID]
		if !ok {
			res.Unresolved = true
			res.UnresolvedReason = "unknown schematic"
			results = append(results, res)
			continue
		}
		res.SchematicName = schem.Name
		res.CycleTimeSec = schem.CycleTime

		if f.FactoryCount <= 0 || schem.CycleTime <= 0 {
			res.Unresolved = true
			res.UnresolvedReason = "invalid factory count or cycle"
			results = append(results, res)
			continue
		}

		cyclesPerDay := 86400.0 / float64(schem.CycleTime) * float64(f.FactoryCount)
		res.CyclesPerDay = cyclesPerDay

		saleFeeMult := 1 - (req.SalesTaxPercent+req.BrokerFeePercent)/100.0
		if saleFeeMult < 0 {
			saleFeeMult = 0
		}
		for _, in := range schem.Inputs {
			qtyPerDay := float64(in.Quantity) * cyclesPerDay
			p := priceByType[in.TypeID]
			var unitVol float64
			if it, ok := sdeData.Types[in.TypeID]; ok {
				unitVol = it.Volume
			}
			row := piMaterialRow{
				TypeID:       in.TypeID,
				TypeName:     piTypeName(sdeData, in.TypeID),
				QtyPerDay:    qtyPerDay,
				BuyPrice:     p.buy,
				SellPrice:    p.sell,
				BasePrice:    p.basePrice,
				Source:       p.source,
				CostPerDay:   qtyPerDay * p.sell,
				UnitVolume:   unitVol,
				VolumePerDay: qtyPerDay * unitVol,
			}
			res.Inputs = append(res.Inputs, row)
			res.InputCostPerDay += row.CostPerDay
			res.InputVolumePerDay += row.VolumePerDay
			// POCO import tax = rate * qty * CCP's fixed per-tier base value.
			// Independent of market price — a P1 always taxes on 400 ISK/unit.
			importTax := qtyPerDay * pocoBaseValue(sdeData, in.TypeID) * (req.PocoTaxPercent / 100.0)
			res.PocoImportPerDay += importTax
			res.PocoTaxPerDay += importTax
			// "Just sell the raw inputs" comparison — user posts sell
			// orders at the hub's ask price, same as they would for the
			// output. Symmetric with input cost (also uses ask): both
			// sides of the three-way decision — sell raw, feed factory
			// then sell output, or buy raw + feed + sell — are priced
			// off the same market side.
			res.InputSaleValuePerDay += qtyPerDay * p.sell * saleFeeMult
			aggQty[in.TypeID] += qtyPerDay
			aggName[in.TypeID] = row.TypeName
		}

		if len(schem.Outputs) > 0 {
			out := schem.Outputs[0] // schematics have a single output row in practice
			qtyPerDay := float64(out.Quantity) * cyclesPerDay
			p := priceByType[out.TypeID]
			res.OutputTier = piTier(sdeData, out.TypeID)
			var outUnitVol float64
			if it, ok := sdeData.Types[out.TypeID]; ok {
				outUnitVol = it.Volume
			}
			res.Output = piMaterialRow{
				TypeID:       out.TypeID,
				TypeName:     piTypeName(sdeData, out.TypeID),
				QtyPerDay:    qtyPerDay,
				SellPrice:    p.sell,
				BasePrice:    p.basePrice,
				Source:       p.source,
				UnitVolume:   outUnitVol,
				VolumePerDay: qtyPerDay * outUnitVol,
			}
			res.GrossRevPerDay = qtyPerDay * p.sell
			// POCO export tax = rate * qty * fixed per-tier base value.
			exportTax := qtyPerDay * pocoBaseValue(sdeData, out.TypeID) * (req.PocoTaxPercent / 100.0)
			res.PocoExportPerDay = exportTax
			res.PocoTaxPerDay += exportTax
			res.SalesFeesPerDay = res.GrossRevPerDay * ((req.SalesTaxPercent + req.BrokerFeePercent) / 100.0)
			res.OutputUndercut = nextSellUndercut(p.sell)
			// Build cost inclusive of POCO taxes.
			buildCost := res.InputCostPerDay + res.PocoTaxPerDay
			res.NetProfitPerDay = res.GrossRevPerDay - res.SalesFeesPerDay - buildCost
			// Build-vs-buy: if you just bought this same qty of output at
			// the hub instead of making it, what would it cost?
			res.BuyOutputCostPerDay = qtyPerDay * p.sell
			res.SavingsVsBuyPerDay = res.BuyOutputCostPerDay - buildCost
			// If inputs were free, this is what selling the output nets you
			// per day — grossRev minus sales/broker fees minus JUST the
			// output-side POCO tax (no import tax since inputs are "free").
			res.OutputSaleValuePerDay = res.GrossRevPerDay*saleFeeMult - exportTax
		}
		results = append(results, res)
	}

	// Build the aggregate shopping list.
	shopping := make([]piShoppingRow, 0, len(aggQty))
	for typeID, qty := range aggQty {
		p := priceByType[typeID]
		buffer := int64(math.Ceil(qty * req.BufferDays))
		var unitVol float64
		if it, ok := sdeData.Types[typeID]; ok {
			unitVol = it.Volume
		}
		shopping = append(shopping, piShoppingRow{
			TypeID:       typeID,
			TypeName:     aggName[typeID],
			QtyPerDay:    qty,
			QtyBuffer:    buffer,
			BuyPrice:     p.buy,
			SellPrice:    p.sell,
			CostBuffer:   float64(buffer) * p.sell,
			Source:       p.source,
			UnitVolume:   unitVol,
			VolumeBuffer: float64(buffer) * unitVol,
		})
	}
	// Descending by cost so the biggest line items surface first.
	sort.SliceStable(shopping, func(i, j int) bool {
		return shopping[i].CostBuffer > shopping[j].CostBuffer
	})

	writeJSON(w, piFactoryResponse{
		Results:     results,
		Shopping:    shopping,
		StationID:   req.StationID,
		StationName: station.Name,
		BufferDays:  req.BufferDays,
	})
}
