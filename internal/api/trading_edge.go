package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"eve-flipper/internal/db"
)

type tradingEdgeSummary struct {
	Ok             bool                       `json:"ok"`
	Enabled        bool                       `json:"enabled"`
	SampleSize     int                        `json:"sample_size"`
	ClosedTrades   int                        `json:"closed_trades"`
	Reconciled     int                        `json:"reconciled"`
	ExpectedISK    float64                    `json:"expected_isk"`
	RealizedISK    float64                    `json:"realized_isk"`
	DeltaISK       float64                    `json:"delta_isk"`
	WinRate        float64                    `json:"win_rate"`
	RealityRatio   float64                    `json:"reality_ratio"`
	Items          []tradingEdgeRow           `json:"items"`
	Categories     []tradingEdgeRow           `json:"categories"`
	Stations       []tradingEdgeRow           `json:"stations"`
	LossBuckets    []tradingEdgeLossBucket    `json:"loss_buckets"`
	Presets        []tradingEdgePreset        `json:"presets"`
	Recommendation *tradingEdgeRecommendation `json:"recommendation,omitempty"`
	Warnings       []string                   `json:"warnings,omitempty"`
}

type tradingEdgeRow struct {
	Key               string   `json:"key"`
	Label             string   `json:"label"`
	Scope             string   `json:"scope"`
	Trades            int      `json:"trades"`
	ClosedTrades      int      `json:"closed_trades"`
	WinRate           float64  `json:"win_rate"`
	ExpectedISK       float64  `json:"expected_isk"`
	RealizedISK       float64  `json:"realized_isk"`
	DeltaISK          float64  `json:"delta_isk"`
	RealityRatio      float64  `json:"reality_ratio"`
	AvgROI            float64  `json:"avg_roi"`
	AvgHoldDays       float64  `json:"avg_hold_days"`
	AvgQuantity       float64  `json:"avg_quantity"`
	AvgCapitalISK     float64  `json:"avg_capital_isk"`
	MaxRecommendedQty int64    `json:"max_recommended_qty"`
	MinNetROIPct      float64  `json:"min_net_roi_pct"`
	MaxExposureISK    float64  `json:"max_exposure_isk"`
	LabelCode         string   `json:"label_code"`
	Confidence        float64  `json:"confidence"`
	Advice            string   `json:"advice"`
	Reasons           []string `json:"reasons"`
}

type tradingEdgeLossBucket struct {
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	ISK      float64 `json:"isk"`
	Trades   int     `json:"trades"`
	SharePct float64 `json:"share_pct"`
}

type tradingEdgePreset struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	MinNetROIPct    float64  `json:"min_net_roi_pct"`
	MaxExposureISK  float64  `json:"max_exposure_isk"`
	MaxQuantity     int64    `json:"max_quantity"`
	PreferredScopes []string `json:"preferred_scopes"`
	AvoidScopes     []string `json:"avoid_scopes"`
}

type tradingEdgeRecommendation struct {
	TypeID            int32    `json:"type_id"`
	TypeName          string   `json:"type_name"`
	Source            string   `json:"source"`
	LabelCode         string   `json:"label_code"`
	Confidence        float64  `json:"confidence"`
	Advice            string   `json:"advice"`
	Reasons           []string `json:"reasons"`
	WinRate           float64  `json:"win_rate"`
	RealityRatio      float64  `json:"reality_ratio"`
	AvgROI            float64  `json:"avg_roi"`
	MinNetROIPct      float64  `json:"min_net_roi_pct"`
	MaxRecommendedQty int64    `json:"max_recommended_qty"`
	MaxExposureISK    float64  `json:"max_exposure_isk"`
	SampleTrades      int      `json:"sample_trades"`
}

type tradingEdgeAgg struct {
	key           string
	label         string
	scope         string
	trades        int
	closed        int
	wins          int
	reconciled    int
	expected      float64
	realized      float64
	roiSum        float64
	holdDaysSum   float64
	quantitySum   float64
	capitalSum    float64
	profitableQty int64
	profitableN   int64
	reasons       map[string]struct{}
}

func (s *Server) handleAuthTradingEdge(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	if s.db == nil {
		writeJSON(w, tradingEdgeSummary{Ok: true, Enabled: true})
		return
	}

	trades, err := s.db.ListPaperTradesForUser(userID, "all", 1000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	typeID := int32(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("type_id")); raw != "" {
		parsed, parseErr := strconv.ParseInt(raw, 10, 32)
		if parseErr != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "invalid type_id")
			return
		}
		typeID = int32(parsed)
	}
	groupName := strings.TrimSpace(r.URL.Query().Get("group_name"))
	station := strings.TrimSpace(r.URL.Query().Get("station"))

	summary := s.buildTradingEdgeSummary(trades, typeID, groupName, station)
	writeJSON(w, summary)
}

func (s *Server) buildTradingEdgeSummary(trades []db.PaperTrade, typeID int32, groupName, station string) tradingEdgeSummary {
	itemAggs := map[string]*tradingEdgeAgg{}
	categoryAggs := map[string]*tradingEdgeAgg{}
	stationAggs := map[string]*tradingEdgeAgg{}
	buckets := map[string]*tradingEdgeLossBucket{}
	overall := &tradingEdgeAgg{key: "overall", label: "Overall", scope: "overall", reasons: map[string]struct{}{}}

	for _, trade := range trades {
		if trade.Status == db.PaperTradeStatusCancelled {
			continue
		}
		itemLabel := strings.TrimSpace(trade.TypeName)
		if itemLabel == "" {
			itemLabel = "Unknown item"
		}
		categoryLabel := s.tradingEdgeCategoryLabel(trade)
		stationLabel := tradingEdgeStationLabel(trade)

		aggs := []*tradingEdgeAgg{
			overall,
			getTradingEdgeAgg(itemAggs, strconv.Itoa(int(trade.TypeID)), itemLabel, "item"),
			getTradingEdgeAgg(categoryAggs, strings.ToLower(categoryLabel), categoryLabel, "category"),
			getTradingEdgeAgg(stationAggs, strings.ToLower(stationLabel), stationLabel, "station"),
		}
		for _, agg := range aggs {
			addTradeToEdgeAgg(agg, trade)
		}
		addLossBuckets(buckets, trade)
	}

	items := finalizeTradingEdgeRows(itemAggs, 12)
	categories := finalizeTradingEdgeRows(categoryAggs, 10)
	stations := finalizeTradingEdgeRows(stationAggs, 10)
	lossBuckets := finalizeLossBuckets(buckets)
	presets := buildTradingEdgePresets(items, categories)
	recommendation := buildTradingEdgeRecommendation(typeID, groupName, station, itemAggs, categoryAggs, stationAggs, overall, trades)
	overallRow := finalizeTradingEdgeAgg(overall)

	warnings := []string{}
	if overall.closed < 5 {
		warnings = append(warnings, "Not enough reconciled/sold journal trades yet. Recommendations will become stronger after more closed trades.")
	}

	return tradingEdgeSummary{
		Ok:             true,
		Enabled:        true,
		SampleSize:     overall.trades,
		ClosedTrades:   overall.closed,
		Reconciled:     overall.reconciled,
		ExpectedISK:    overall.expected,
		RealizedISK:    overall.realized,
		DeltaISK:       overall.realized - overall.expected,
		WinRate:        overallRow.WinRate,
		RealityRatio:   overallRow.RealityRatio,
		Items:          items,
		Categories:     categories,
		Stations:       stations,
		LossBuckets:    lossBuckets,
		Presets:        presets,
		Recommendation: recommendation,
		Warnings:       warnings,
	}
}

func getTradingEdgeAgg(m map[string]*tradingEdgeAgg, key, label, scope string) *tradingEdgeAgg {
	if key == "" {
		key = "unknown"
	}
	if label == "" {
		label = "Unknown"
	}
	if agg, ok := m[key]; ok {
		return agg
	}
	agg := &tradingEdgeAgg{
		key:     key,
		label:   label,
		scope:   scope,
		reasons: map[string]struct{}{},
	}
	m[key] = agg
	return agg
}

func addTradeToEdgeAgg(agg *tradingEdgeAgg, trade db.PaperTrade) {
	agg.trades++
	if trade.Status == db.PaperTradeStatusReconciled {
		agg.reconciled++
	}
	closed := trade.Status == db.PaperTradeStatusSold || trade.Status == db.PaperTradeStatusReconciled
	if !closed {
		return
	}
	agg.closed++
	expected := trade.ExpectedProfitISK
	if expected == 0 {
		expected = trade.PlannedProfitISK
	}
	actual := trade.RealizedProfitISK
	agg.expected += expected
	agg.realized += actual
	if actual > 0 {
		agg.wins++
		if trade.ActualQuantity > 0 {
			agg.profitableQty += trade.ActualQuantity
		} else {
			agg.profitableQty += trade.PlannedQuantity
		}
		agg.profitableN++
	}
	agg.roiSum += trade.ROIPercent
	agg.holdDaysSum += paperTradeHoldDays(trade)
	if trade.ActualQuantity > 0 {
		agg.quantitySum += float64(trade.ActualQuantity)
	} else {
		agg.quantitySum += float64(trade.PlannedQuantity)
	}
	agg.capitalSum += trade.CapitalISK
	for _, reason := range edgeReasonsForTrade(trade, expected, actual) {
		agg.reasons[reason] = struct{}{}
	}
}

func finalizeTradingEdgeRows(aggs map[string]*tradingEdgeAgg, limit int) []tradingEdgeRow {
	rows := make([]tradingEdgeRow, 0, len(aggs))
	for _, agg := range aggs {
		rows = append(rows, finalizeTradingEdgeAgg(agg))
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].LabelCode != rows[j].LabelCode {
			return edgeLabelRank(rows[i].LabelCode) > edgeLabelRank(rows[j].LabelCode)
		}
		if rows[i].ClosedTrades != rows[j].ClosedTrades {
			return rows[i].ClosedTrades > rows[j].ClosedTrades
		}
		return rows[i].RealizedISK > rows[j].RealizedISK
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

func finalizeTradingEdgeAgg(agg *tradingEdgeAgg) tradingEdgeRow {
	row := tradingEdgeRow{
		Key:          agg.key,
		Label:        agg.label,
		Scope:        agg.scope,
		Trades:       agg.trades,
		ClosedTrades: agg.closed,
		ExpectedISK:  agg.expected,
		RealizedISK:  agg.realized,
		DeltaISK:     agg.realized - agg.expected,
	}
	if agg.closed > 0 {
		row.WinRate = float64(agg.wins) / float64(agg.closed) * 100
		row.AvgROI = agg.roiSum / float64(agg.closed)
		row.AvgHoldDays = agg.holdDaysSum / float64(agg.closed)
		row.AvgQuantity = agg.quantitySum / float64(agg.closed)
		row.AvgCapitalISK = agg.capitalSum / float64(agg.closed)
	}
	if agg.expected != 0 {
		row.RealityRatio = agg.realized / agg.expected
	}
	if agg.profitableN > 0 {
		row.MaxRecommendedQty = maxInt64(1, agg.profitableQty/agg.profitableN)
	} else if row.AvgQuantity > 0 {
		row.MaxRecommendedQty = maxInt64(1, int64(row.AvgQuantity))
	}
	if row.MaxRecommendedQty <= 0 {
		row.MaxRecommendedQty = 1
	}
	row.MaxExposureISK = row.AvgCapitalISK
	if row.MaxExposureISK <= 0 && row.MaxRecommendedQty > 0 {
		row.MaxExposureISK = float64(row.MaxRecommendedQty) * 1_000_000
	}
	row.LabelCode, row.Advice, row.MinNetROIPct, row.Confidence = classifyTradingEdge(row)
	row.Reasons = edgeReasonList(agg.reasons, row)
	return row
}

func classifyTradingEdge(row tradingEdgeRow) (label, advice string, minROI float64, confidence float64) {
	if row.ClosedTrades <= 0 {
		return "insufficient_data", "No personal closed trades yet. Treat scanner math as unverified.", 0, 10
	}
	confidence = clampFloat(float64(row.ClosedTrades)*12, 20, 95)
	minROI = clampFloat(row.AvgROI, 0, 60)
	if row.ClosedTrades < 3 {
		return "watch", "Small personal sample. Use normal filters and keep position size modest.", clampFloat(minROI+2, 3, 60), confidence
	}
	if row.RealizedISK < 0 || row.WinRate < 40 || row.RealityRatio < 0.35 {
		return "do_not_trade", "Personal history is weak here. Avoid unless spread is much higher than normal.", clampFloat(minROI+12, 12, 80), confidence
	}
	if row.RealizedISK > 0 && row.WinRate >= 60 && row.RealityRatio >= 0.8 {
		return "good_edge", "Your real results are close to the plan. This is a personal edge candidate.", clampFloat(minROI+1, 3, 50), confidence
	}
	if row.RealizedISK >= 0 && (row.RealityRatio < 0.8 || row.AvgHoldDays > 7) {
		return "needs_bigger_margin", "Profitable, but actual result is weaker or slower than expected. Demand bigger spread.", clampFloat(minROI+6, 8, 70), confidence
	}
	return "watch", "Mixed personal result. Keep size limited until more reconciled trades confirm it.", clampFloat(minROI+4, 5, 60), confidence
}

func edgeReasonsForTrade(trade db.PaperTrade, expected, actual float64) []string {
	if trade.Status != db.PaperTradeStatusSold && trade.Status != db.PaperTradeStatusReconciled {
		return nil
	}
	reasons := []string{}
	if expected > 0 && actual < expected*0.9 {
		reasons = append(reasons, "actual below expected")
	}
	if trade.FeesISK > 0 {
		reasons = append(reasons, "fees/taxes")
	}
	if trade.HaulingCostISK > 0 {
		reasons = append(reasons, "hauling cost")
	}
	if trade.ActualQuantity > 0 && trade.ActualQuantity < trade.PlannedQuantity {
		reasons = append(reasons, "partial fill")
	}
	if trade.ActualBuyPrice > trade.PlannedBuyPrice && trade.PlannedBuyPrice > 0 {
		reasons = append(reasons, "buy slippage")
	}
	if trade.ActualSellPrice > 0 && trade.ActualSellPrice < trade.PlannedSellPrice {
		reasons = append(reasons, "sell price moved")
	}
	if paperTradeHoldDays(trade) > 7 {
		reasons = append(reasons, "slow fill")
	}
	if len(reasons) == 0 && actual < expected {
		reasons = append(reasons, "execution gap")
	}
	return reasons
}

func edgeReasonList(reasons map[string]struct{}, row tradingEdgeRow) []string {
	out := make([]string, 0, len(reasons)+2)
	for reason := range reasons {
		out = append(out, reason)
	}
	sort.Strings(out)
	if row.ClosedTrades < 3 {
		out = append(out, "small sample")
	}
	if row.RealityRatio > 0 && row.RealityRatio < 0.8 {
		out = append(out, "expected vs actual gap")
	}
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func addLossBuckets(buckets map[string]*tradingEdgeLossBucket, trade db.PaperTrade) {
	if trade.Status != db.PaperTradeStatusSold && trade.Status != db.PaperTradeStatusReconciled {
		return
	}
	expected := trade.ExpectedProfitISK
	if expected == 0 {
		expected = trade.PlannedProfitISK
	}
	actual := trade.RealizedProfitISK
	loss := expected - actual
	if loss <= 0 {
		return
	}
	qty := trade.ActualQuantity
	if qty <= 0 {
		qty = trade.PlannedQuantity
	}
	added := false
	remaining := loss
	addBucket := func(key, label string, amount float64) {
		if amount <= 0 || remaining <= 0 {
			return
		}
		if amount > remaining {
			amount = remaining
		}
		bucket, ok := buckets[key]
		if !ok {
			bucket = &tradingEdgeLossBucket{Key: key, Label: label}
			buckets[key] = bucket
		}
		bucket.ISK += amount
		bucket.Trades++
		remaining -= amount
		added = true
	}
	addBucket("fees_taxes", "Fees / taxes", trade.FeesISK)
	addBucket("hauling", "Hauling cost", trade.HaulingCostISK)
	if qty > 0 && trade.ActualBuyPrice > trade.PlannedBuyPrice {
		addBucket("buy_slippage", "Buy slippage", (trade.ActualBuyPrice-trade.PlannedBuyPrice)*float64(qty))
	}
	if qty > 0 && trade.ActualSellPrice > 0 && trade.ActualSellPrice < trade.PlannedSellPrice {
		addBucket("price_moved", "Sell price moved", (trade.PlannedSellPrice-trade.ActualSellPrice)*float64(qty))
	}
	if trade.ActualQuantity > 0 && trade.ActualQuantity < trade.PlannedQuantity && trade.PlannedQuantity > 0 {
		addBucket("partial_fill", "Partial fill", expected*(float64(trade.PlannedQuantity-trade.ActualQuantity)/float64(trade.PlannedQuantity)))
	}
	if paperTradeHoldDays(trade) > 7 {
		addBucket("slow_fill", "Slow fill / undercut", loss*0.25)
	}
	if !added || remaining > loss*0.1 {
		addBucket("execution_gap", "Execution gap", remaining)
	}
}

func finalizeLossBuckets(buckets map[string]*tradingEdgeLossBucket) []tradingEdgeLossBucket {
	out := make([]tradingEdgeLossBucket, 0, len(buckets))
	total := 0.0
	for _, bucket := range buckets {
		total += bucket.ISK
	}
	for _, bucket := range buckets {
		row := *bucket
		if total > 0 {
			row.SharePct = row.ISK / total * 100
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ISK > out[j].ISK
	})
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func buildTradingEdgePresets(items, categories []tradingEdgeRow) []tradingEdgePreset {
	goodScopes := []string{}
	avoidScopes := []string{}
	maxQty := int64(1)
	maxExposure := 0.0
	minROI := 8.0
	for _, row := range append(items, categories...) {
		if row.LabelCode == "good_edge" && len(goodScopes) < 5 {
			goodScopes = append(goodScopes, row.Label)
			if row.MaxRecommendedQty > maxQty {
				maxQty = row.MaxRecommendedQty
			}
			if row.MaxExposureISK > maxExposure {
				maxExposure = row.MaxExposureISK
			}
			if row.MinNetROIPct > minROI {
				minROI = row.MinNetROIPct
			}
		}
		if row.LabelCode == "do_not_trade" && len(avoidScopes) < 5 {
			avoidScopes = append(avoidScopes, row.Label)
		}
	}
	if maxExposure <= 0 {
		maxExposure = 250_000_000
	}
	return []tradingEdgePreset{
		{
			ID:              "personal_conservative",
			Name:            "Personal conservative",
			Description:     "Uses your realized journal history to keep size and spread conservative.",
			MinNetROIPct:    clampFloat(minROI+3, 8, 80),
			MaxExposureISK:  maxExposure * 0.75,
			MaxQuantity:     maxInt64(1, maxQty/2),
			PreferredScopes: goodScopes,
			AvoidScopes:     avoidScopes,
		},
		{
			ID:              "personal_edge",
			Name:            "Personal edge",
			Description:     "Focuses on items/categories where actual PnL stayed close to expected PnL.",
			MinNetROIPct:    clampFloat(minROI, 5, 70),
			MaxExposureISK:  maxExposure,
			MaxQuantity:     maxQty,
			PreferredScopes: goodScopes,
			AvoidScopes:     avoidScopes,
		},
	}
}

func buildTradingEdgeRecommendation(typeID int32, groupName, station string, itemAggs, categoryAggs, stationAggs map[string]*tradingEdgeAgg, overall *tradingEdgeAgg, trades []db.PaperTrade) *tradingEdgeRecommendation {
	if typeID <= 0 && strings.TrimSpace(groupName) == "" && strings.TrimSpace(station) == "" {
		return nil
	}
	var row tradingEdgeRow
	source := "overall"
	if typeID > 0 {
		if agg, ok := itemAggs[strconv.Itoa(int(typeID))]; ok {
			row = finalizeTradingEdgeAgg(agg)
			source = "item"
		}
	}
	if row.Key == "" && strings.TrimSpace(groupName) != "" {
		if agg, ok := categoryAggs[strings.ToLower(strings.TrimSpace(groupName))]; ok {
			row = finalizeTradingEdgeAgg(agg)
			source = "category"
		}
	}
	if row.Key == "" && strings.TrimSpace(station) != "" {
		if agg, ok := stationAggs[strings.ToLower(strings.TrimSpace(station))]; ok {
			row = finalizeTradingEdgeAgg(agg)
			source = "station"
		}
	}
	if row.Key == "" {
		row = finalizeTradingEdgeAgg(overall)
		source = "overall"
		if row.ClosedTrades <= 0 && len(trades) == 0 {
			row.LabelCode = "insufficient_data"
			row.Advice = "No journal history yet. Save and reconcile trades to build a personal edge model."
			row.Confidence = 0
		}
	}
	typeName := ""
	if typeID > 0 {
		for _, trade := range trades {
			if trade.TypeID == typeID {
				typeName = trade.TypeName
				break
			}
		}
	}
	return &tradingEdgeRecommendation{
		TypeID:            typeID,
		TypeName:          typeName,
		Source:            source,
		LabelCode:         row.LabelCode,
		Confidence:        row.Confidence,
		Advice:            row.Advice,
		Reasons:           row.Reasons,
		WinRate:           row.WinRate,
		RealityRatio:      row.RealityRatio,
		AvgROI:            row.AvgROI,
		MinNetROIPct:      row.MinNetROIPct,
		MaxRecommendedQty: row.MaxRecommendedQty,
		MaxExposureISK:    row.MaxExposureISK,
		SampleTrades:      row.ClosedTrades,
	}
}

func (s *Server) tradingEdgeCategoryLabel(trade db.PaperTrade) string {
	if s.sdeData == nil {
		return "Unknown category"
	}
	item, ok := s.sdeData.Types[trade.TypeID]
	if !ok || item == nil {
		return "Unknown category"
	}
	if group, ok := s.sdeData.Groups[item.GroupID]; ok && group != nil && strings.TrimSpace(group.Name) != "" {
		return group.Name
	}
	return "Group " + strconv.Itoa(int(item.GroupID))
}

func tradingEdgeStationLabel(trade db.PaperTrade) string {
	for _, value := range []string{trade.SellStation, trade.SellSystemName, trade.BuyStation, trade.BuySystemName} {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return "Unknown station"
}

func paperTradeHoldDays(trade db.PaperTrade) float64 {
	start, ok := parsePaperTradeEdgeTime(trade.CreatedAt)
	if !ok {
		return 0
	}
	end := time.Time{}
	for _, raw := range []string{trade.ClosedAt, trade.UpdatedAt} {
		if parsed, parsedOK := parsePaperTradeEdgeTime(raw); parsedOK {
			end = parsed
			break
		}
	}
	if end.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start).Hours() / 24
}

func parsePaperTradeEdgeTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func edgeLabelRank(label string) int {
	switch label {
	case "good_edge":
		return 4
	case "needs_bigger_margin":
		return 3
	case "watch":
		return 2
	case "do_not_trade":
		return 1
	default:
		return 0
	}
}

func clampFloat(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
