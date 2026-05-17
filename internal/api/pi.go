package api

import (
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"eve-flipper/internal/esi"
	"eve-flipper/internal/sde"
)

type piProductFlow struct {
	TypeID           int32   `json:"type_id"`
	TypeName         string  `json:"type_name"`
	Direction        string  `json:"direction"`
	Source           string  `json:"source"`
	UnitsPerDay      float64 `json:"units_per_day"`
	ValueISKPerDay   float64 `json:"value_isk_per_day"`
	QuantityPerCycle float64 `json:"quantity_per_cycle"`
	PinCount         int     `json:"pin_count"`
}

type piPlanetRow struct {
	CharacterID              int64           `json:"character_id"`
	CharacterName            string          `json:"character_name"`
	PlanetID                 int32           `json:"planet_id"`
	PlanetType               string          `json:"planet_type"`
	SolarSystemID            int32           `json:"solar_system_id"`
	SolarSystemName          string          `json:"solar_system_name"`
	UpgradeLevel             int32           `json:"upgrade_level"`
	NumPins                  int32           `json:"num_pins"`
	ExtractorPins            int             `json:"extractor_pins"`
	FactoryPins              int             `json:"factory_pins"`
	StoragePins              int             `json:"storage_pins"`
	IdleFactoryPins          int             `json:"idle_factory_pins"`
	ExpiredExtractorPins     int             `json:"expired_extractor_pins"`
	RoutedPins               int             `json:"routed_pins"`
	RoutedQuantity           int64           `json:"routed_quantity"`
	ExtractorUnitsPerDay     float64         `json:"extractor_units_per_day"`
	ExtractorValueISKPerDay  float64         `json:"extractor_value_isk_per_day"`
	FactoryInputISKPerDay    float64         `json:"factory_input_isk_per_day"`
	FactoryOutputISKPerDay   float64         `json:"factory_output_isk_per_day"`
	FactoryNetISKPerDay      float64         `json:"factory_net_isk_per_day"`
	GrossISKPerDay           float64         `json:"gross_isk_per_day"`
	NetISKPerDay             float64         `json:"net_isk_per_day"`
	MissingInputISKPerDay    float64         `json:"missing_input_isk_per_day"`
	CycleHealthScore         float64         `json:"cycle_health_score"`
	ProductionChains         int             `json:"production_chains"`
	StoredQuantity           int64           `json:"stored_quantity"`
	StoredValueISK           float64         `json:"stored_value_isk"`
	EstimatedDailyValueISK   float64         `json:"estimated_daily_value_isk"`
	EstimatedMonthlyValueISK float64         `json:"estimated_monthly_value_isk"`
	LastUpdate               string          `json:"last_update"`
	NextExpiry               string          `json:"next_expiry"`
	Status                   string          `json:"status"`
	EstimateBasis            string          `json:"estimate_basis"`
	ProductFlows             []piProductFlow `json:"product_flows,omitempty"`
	Warnings                 []string        `json:"warnings,omitempty"`
}

type piPlanetsResponse struct {
	Planets  []piPlanetRow `json:"planets"`
	Count    int           `json:"count"`
	Warnings []string      `json:"warnings,omitempty"`
}

func (s *Server) handleAuthPIPlanets(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	characterID, allScope, err := parseAuthScope(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sessions, err := s.authSessionsForScope(userID, characterID, allScope, true)
	if err != nil {
		if strings.Contains(err.Error(), "not logged in") {
			writeError(w, http.StatusUnauthorized, err.Error())
		} else {
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}

	priceByType := map[int32]float64{}
	if s.esi != nil && s.industryAnalyzer != nil && s.industryAnalyzer.IndustryCache != nil {
		if prices, priceErr := s.esi.GetAllAdjustedPrices(s.industryAnalyzer.IndustryCache); priceErr == nil {
			priceByType = prices
		}
	}

	s.mu.RLock()
	sdeData := s.sdeData
	s.mu.RUnlock()
	schematics := map[int32]*sde.PlanetSchematic{}
	if sdeData != nil && sdeData.Industry != nil {
		schematics = sdeData.Industry.PlanetSchematics
	}

	var rows []piPlanetRow
	var warnings []string
	for _, sess := range sessions {
		token, tokenErr := s.sessions.EnsureValidTokenForUserCharacter(s.sso, userID, sess.CharacterID)
		if tokenErr != nil {
			warnings = append(warnings, sess.CharacterName+": "+tokenErr.Error())
			continue
		}
		planets, planetErr := s.esi.GetCharacterPlanets(sess.CharacterID, token)
		if planetErr != nil {
			warnings = append(warnings, sess.CharacterName+": "+planetErr.Error())
			continue
		}
		for _, planet := range planets {
			detail, detailErr := s.esi.GetCharacterPlanetDetail(sess.CharacterID, planet.PlanetID, token)
			row := piPlanetRow{
				CharacterID:   sess.CharacterID,
				CharacterName: sess.CharacterName,
				PlanetID:      planet.PlanetID,
				PlanetType:    planet.PlanetType,
				SolarSystemID: planet.SolarSystemID,
				UpgradeLevel:  planet.UpgradeLevel,
				NumPins:       planet.NumPins,
				LastUpdate:    planet.LastUpdate,
				Status:        "unknown",
				EstimateBasis: "extractor cycle output from ESI when available; otherwise stored inventory value from adjusted prices",
			}
			if sdeData != nil {
				if sys, ok := sdeData.Systems[planet.SolarSystemID]; ok {
					row.SolarSystemName = sys.Name
				}
			}
			if detailErr != nil {
				row.Status = "detail_unavailable"
				row.Warnings = append(row.Warnings, detailErr.Error())
				rows = append(rows, row)
				continue
			}
			summarizePIPlanet(&row, detail, priceByType, func(typeID int32) string {
				if sdeData == nil {
					return ""
				}
				if t, ok := sdeData.Types[typeID]; ok {
					return t.Name
				}
				return ""
			}, schematics)
			rows = append(rows, row)
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CharacterName != rows[j].CharacterName {
			return rows[i].CharacterName < rows[j].CharacterName
		}
		if rows[i].SolarSystemName != rows[j].SolarSystemName {
			return rows[i].SolarSystemName < rows[j].SolarSystemName
		}
		return rows[i].PlanetID < rows[j].PlanetID
	})
	writeJSON(w, piPlanetsResponse{Planets: rows, Count: len(rows), Warnings: warnings})
}

func summarizePIPlanet(row *piPlanetRow, detail *esi.CharacterPlanetDetail, priceByType map[int32]float64, typeName func(int32) string, schematics map[int32]*sde.PlanetSchematic) {
	now := time.Now().UTC()
	var nextExpiry time.Time
	routePins := make(map[int64]bool)
	incomingByPinType := make(map[int64]map[int32]int64)
	outgoingByPinType := make(map[int64]map[int32]int64)
	contentsByPinType := make(map[int64]map[int32]int64)
	flows := make(map[string]*piProductFlow)

	for _, route := range detail.Routes {
		if route.Quantity > 0 {
			row.RoutedQuantity += route.Quantity
		}
		if route.SourcePinID > 0 {
			routePins[route.SourcePinID] = true
			if route.ContentTypeID > 0 && route.Quantity > 0 {
				if outgoingByPinType[route.SourcePinID] == nil {
					outgoingByPinType[route.SourcePinID] = make(map[int32]int64)
				}
				outgoingByPinType[route.SourcePinID][route.ContentTypeID] += route.Quantity
			}
		}
		if route.DestinationPinID > 0 {
			routePins[route.DestinationPinID] = true
			if route.ContentTypeID > 0 && route.Quantity > 0 {
				if incomingByPinType[route.DestinationPinID] == nil {
					incomingByPinType[route.DestinationPinID] = make(map[int32]int64)
				}
				incomingByPinType[route.DestinationPinID][route.ContentTypeID] += route.Quantity
			}
		}
	}

	for _, pin := range detail.Pins {
		name := strings.ToLower(typeName(pin.TypeID))
		isExtractor := false
		isFactory := false
		switch {
		case strings.Contains(name, "extractor"):
			isExtractor = true
			row.ExtractorPins++
		case strings.Contains(name, "industry facility") ||
			strings.Contains(name, "production plant") ||
			strings.Contains(name, "factory") ||
			pin.SchematicID > 0:
			isFactory = true
			row.FactoryPins++
		case strings.Contains(name, "storage") || strings.Contains(name, "launchpad"):
			row.StoragePins++
		}
		if routePins[pin.PinID] {
			row.RoutedPins++
		}
		if pin.ExpiryTime != "" {
			if expiry, err := time.Parse(time.RFC3339, pin.ExpiryTime); err == nil {
				if nextExpiry.IsZero() || expiry.Before(nextExpiry) {
					nextExpiry = expiry
				}
				if isExtractor && expiry.Before(now) {
					row.ExpiredExtractorPins++
				}
			}
		}
		if isFactory && pin.SchematicID <= 0 {
			row.IdleFactoryPins++
		}
		if isExtractor && pin.ExtractorDetails != nil {
			details := pin.ExtractorDetails
			if details.CycleTime > 0 && details.QtyPerCycle > 0 && details.ProductTypeID > 0 {
				unitsPerDay := float64(details.QtyPerCycle) * 86400.0 / float64(details.CycleTime)
				row.ExtractorUnitsPerDay += unitsPerDay
				if price := priceByType[details.ProductTypeID]; price > 0 {
					row.ExtractorValueISKPerDay += unitsPerDay * price
				}
				addPIFlow(flows, details.ProductTypeID, typeName(details.ProductTypeID), "output", "extractor", unitsPerDay, float64(details.QtyPerCycle), 1, priceByType)
			}
		}
		for _, content := range pin.Contents {
			if content.Quantity <= 0 {
				continue
			}
			if contentsByPinType[pin.PinID] == nil {
				contentsByPinType[pin.PinID] = make(map[int32]int64)
			}
			contentsByPinType[pin.PinID][content.TypeID] += content.Quantity
			row.StoredQuantity += content.Quantity
			if price := priceByType[content.TypeID]; price > 0 {
				row.StoredValueISK += price * float64(content.Quantity)
			}
		}
	}

	for _, pin := range detail.Pins {
		name := strings.ToLower(typeName(pin.TypeID))
		isFactory := strings.Contains(name, "industry facility") ||
			strings.Contains(name, "production plant") ||
			strings.Contains(name, "factory") ||
			pin.SchematicID > 0
		if !isFactory {
			continue
		}
		if !routePins[pin.PinID] {
			addPIWarning(row, "factory pin without visible input/output route")
		}
		if pin.SchematicID <= 0 {
			continue
		}
		schematic := schematics[pin.SchematicID]
		if schematic == nil {
			addPIWarning(row, "factory schematic missing from SDE")
			continue
		}
		cycleSeconds := schematic.CycleTime
		if cycleSeconds <= 0 {
			cycleSeconds = 3600
		}
		cyclesPerDay := 86400.0 / float64(cycleSeconds)
		throughput := factoryThroughputFactor(schematic, incomingByPinType[pin.PinID], contentsByPinType[pin.PinID])
		if throughput <= 0 {
			addPIWarning(row, "factory input routes/contents do not satisfy schematic")
			continue
		}
		row.ProductionChains++
		for _, input := range schematic.Inputs {
			requiredUnits := float64(input.Quantity) * cyclesPerDay * throughput
			value := requiredUnits * priceByType[input.TypeID]
			row.FactoryInputISKPerDay += value
			if throughput < 0.99 {
				row.MissingInputISKPerDay += float64(input.Quantity) * cyclesPerDay * (1 - throughput) * priceByType[input.TypeID]
			}
			addPIFlow(flows, input.TypeID, typeName(input.TypeID), "input", "factory", requiredUnits, float64(input.Quantity)*throughput, 1, priceByType)
		}
		for _, output := range schematic.Outputs {
			outputUnits := float64(output.Quantity) * cyclesPerDay * throughput
			value := outputUnits * priceByType[output.TypeID]
			row.FactoryOutputISKPerDay += value
			addPIFlow(flows, output.TypeID, typeName(output.TypeID), "output", "factory", outputUnits, float64(output.Quantity)*throughput, 1, priceByType)
			if len(outgoingByPinType[pin.PinID]) > 0 && outgoingByPinType[pin.PinID][output.TypeID] <= 0 {
				addPIWarning(row, "factory output has no matching route")
			}
		}
	}

	if !nextExpiry.IsZero() {
		row.NextExpiry = nextExpiry.Format(time.RFC3339)
	}
	row.FactoryNetISKPerDay = row.FactoryOutputISKPerDay - row.FactoryInputISKPerDay
	row.GrossISKPerDay = row.ExtractorValueISKPerDay + row.FactoryOutputISKPerDay
	row.NetISKPerDay = row.ExtractorValueISKPerDay + row.FactoryNetISKPerDay
	if row.ProductionChains > 0 {
		grossFactory := row.FactoryOutputISKPerDay
		if grossFactory > 0 {
			row.CycleHealthScore = math.Max(0, math.Min(100, (row.FactoryOutputISKPerDay-row.MissingInputISKPerDay)/grossFactory*100))
		}
	} else if row.ExtractorPins > 0 && row.ExpiredExtractorPins == 0 {
		row.CycleHealthScore = 100
	}
	row.ProductFlows = sortedPIFlows(flows)
	row.Status = piPlanetStatus(row, nextExpiry, now)
	if row.NetISKPerDay != 0 {
		row.EstimatedDailyValueISK = row.NetISKPerDay
		row.EstimatedMonthlyValueISK = row.EstimatedDailyValueISK * 30
		row.EstimateBasis = "net PI projection from extractor cycles plus factory schematic outputs minus inputs, valued with adjusted prices"
	} else if row.GrossISKPerDay > 0 {
		row.EstimatedDailyValueISK = row.GrossISKPerDay
		row.EstimatedMonthlyValueISK = row.EstimatedDailyValueISK * 30
		row.EstimateBasis = "gross PI projection from visible extractor/factory outputs; input netting incomplete"
	} else if row.StoredValueISK > 0 {
		ageDays := 1.0
		if row.LastUpdate != "" {
			if updated, err := time.Parse(time.RFC3339, row.LastUpdate); err == nil {
				if hours := now.Sub(updated).Hours(); hours > 24 {
					ageDays = hours / 24
				}
			}
		}
		row.EstimatedDailyValueISK = row.StoredValueISK / ageDays
		row.EstimatedMonthlyValueISK = row.EstimatedDailyValueISK * 30
		row.EstimateBasis = "stored PI inventory value divided by time since last update; rough fallback"
	} else if len(priceByType) == 0 {
		addPIWarning(row, "market price cache unavailable")
	}
	if row.FactoryPins > 0 && row.ProductionChains == 0 {
		addPIWarning(row, "factory production could not be valued from schematic/routes")
	}
	if row.ExpiredExtractorPins > 0 {
		addPIWarning(row, "extractor program expired")
	}
	if row.IdleFactoryPins > 0 {
		addPIWarning(row, "factory pin has no schematic")
	}
}

func factoryThroughputFactor(schematic *sde.PlanetSchematic, incoming map[int32]int64, contents map[int32]int64) float64 {
	if schematic == nil || len(schematic.Inputs) == 0 {
		return 1
	}
	factor := 1.0
	hasAnyInput := false
	for _, input := range schematic.Inputs {
		if input.Quantity <= 0 {
			continue
		}
		available := int64(0)
		if incoming != nil {
			available += incoming[input.TypeID]
		}
		if contents != nil {
			available += contents[input.TypeID]
		}
		if available <= 0 {
			return 0
		}
		hasAnyInput = true
		ratio := float64(available) / float64(input.Quantity)
		if ratio < factor {
			factor = ratio
		}
	}
	if !hasAnyInput {
		return 1
	}
	return math.Max(0, math.Min(1, factor))
}

func addPIFlow(flows map[string]*piProductFlow, typeID int32, typeName string, direction string, source string, unitsPerDay float64, qtyPerCycle float64, pinCount int, priceByType map[int32]float64) {
	if typeID <= 0 || unitsPerDay <= 0 {
		return
	}
	key := direction + "|" + source + "|" + strconv.FormatInt(int64(typeID), 10)
	flow := flows[key]
	if flow == nil {
		flow = &piProductFlow{
			TypeID:    typeID,
			TypeName:  typeName,
			Direction: direction,
			Source:    source,
		}
		flows[key] = flow
	}
	flow.UnitsPerDay += unitsPerDay
	flow.ValueISKPerDay += unitsPerDay * priceByType[typeID]
	flow.QuantityPerCycle += qtyPerCycle
	flow.PinCount += pinCount
}

func sortedPIFlows(flows map[string]*piProductFlow) []piProductFlow {
	if len(flows) == 0 {
		return nil
	}
	out := make([]piProductFlow, 0, len(flows))
	for _, flow := range flows {
		out = append(out, *flow)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ValueISKPerDay != out[j].ValueISKPerDay {
			return out[i].ValueISKPerDay > out[j].ValueISKPerDay
		}
		if out[i].Direction != out[j].Direction {
			return out[i].Direction > out[j].Direction
		}
		return out[i].TypeName < out[j].TypeName
	})
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

func addPIWarning(row *piPlanetRow, warning string) {
	if warning == "" {
		return
	}
	for _, existing := range row.Warnings {
		if existing == warning {
			return
		}
	}
	if len(row.Warnings) < 8 {
		row.Warnings = append(row.Warnings, warning)
	}
}

func piPlanetStatus(row *piPlanetRow, nextExpiry time.Time, now time.Time) string {
	if row.NumPins == 0 {
		return "empty"
	}
	if row.ExtractorPins == 0 {
		return "configured"
	}
	if nextExpiry.IsZero() {
		return "needs_setup"
	}
	if nextExpiry.Before(now) {
		return "expired"
	}
	if nextExpiry.Before(now.Add(24 * time.Hour)) {
		return "expiring"
	}
	return "running"
}
