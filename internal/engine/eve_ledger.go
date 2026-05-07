package engine

import (
	"sort"
	"strings"
	"time"

	"eve-flipper/internal/esi"
)

// EveLedgerOptions controls the character wallet/capital dashboard window.
type EveLedgerOptions struct {
	LookbackDays     int
	SalesTaxPercent  float64
	BrokerFeePercent float64
	LedgerLimit      int
}

// EveLedgerDashboard is an EveLedger-style wallet, cashflow and capital view.
type EveLedgerDashboard struct {
	Summary    EveLedgerSummary         `json:"summary"`
	Daily      []EveLedgerCurvePoint    `json:"daily"`
	Weekly     []EveLedgerCurvePoint    `json:"weekly"`
	Monthly    []EveLedgerCurvePoint    `json:"monthly"`
	Categories []EveLedgerCategory      `json:"categories"`
	Inventory  []EveLedgerInventoryItem `json:"inventory"`
	Settings   EveLedgerSettings        `json:"settings"`
	Warnings   []string                 `json:"warnings,omitempty"`
	Portfolio  *PortfolioPnL            `json:"portfolio,omitempty"`
}

type EveLedgerSettings struct {
	LookbackDays     int     `json:"lookback_days"`
	SalesTaxPercent  float64 `json:"sales_tax_percent"`
	BrokerFeePercent float64 `json:"broker_fee_percent"`
}

type EveLedgerSummary struct {
	WalletISK             float64 `json:"wallet_isk"`
	EstimatedCapitalISK   float64 `json:"estimated_capital_isk"`
	JournalIncomeISK      float64 `json:"journal_income_isk"`
	JournalOutgoingISK    float64 `json:"journal_outgoing_isk"`
	JournalNetISK         float64 `json:"journal_net_isk"`
	TradingPnLISK         float64 `json:"trading_pnl_isk"`
	TradingCashflowISK    float64 `json:"trading_cashflow_isk"`
	OtherIncomeISK        float64 `json:"other_income_isk"`
	OtherOutgoingISK      float64 `json:"other_outgoing_isk"`
	OtherNetISK           float64 `json:"other_net_isk"`
	InventoryMTMISK       float64 `json:"inventory_mtm_isk"`
	InventoryCostBasisISK float64 `json:"inventory_cost_basis_isk"`
	UnrealizedPnLISK      float64 `json:"unrealized_pnl_isk"`
	SellOrdersValueISK    float64 `json:"sell_orders_value_isk"`
	BuyOrdersValueISK     float64 `json:"buy_orders_value_isk"`
	OpenOrdersValueISK    float64 `json:"open_orders_value_isk"`
	JournalEntries        int     `json:"journal_entries"`
	TransactionCount      int     `json:"transaction_count"`
	AssetTypes            int     `json:"asset_types"`
	AssetUnits            int64   `json:"asset_units"`
	PricedAssetTypes      int     `json:"priced_asset_types"`
	UnpricedAssetTypes    int     `json:"unpriced_asset_types"`
	UnpricedAssetUnits    int64   `json:"unpriced_asset_units"`
}

type EveLedgerCurvePoint struct {
	Period         string  `json:"period"`
	StartDate      string  `json:"start_date"`
	EndDate        string  `json:"end_date"`
	IncomeISK      float64 `json:"income_isk"`
	OutgoingISK    float64 `json:"outgoing_isk"`
	NetCashflowISK float64 `json:"net_cashflow_isk"`
	TradingPnLISK  float64 `json:"trading_pnl_isk"`
	OtherNetISK    float64 `json:"other_net_isk"`
	CapitalISK     float64 `json:"capital_isk"`
	JournalEntries int     `json:"journal_entries"`
	Transactions   int     `json:"transactions"`
}

type EveLedgerCategory struct {
	Key         string  `json:"key"`
	Label       string  `json:"label"`
	IncomeISK   float64 `json:"income_isk"`
	OutgoingISK float64 `json:"outgoing_isk"`
	NetISK      float64 `json:"net_isk"`
	Entries     int     `json:"entries"`
	IsTrading   bool    `json:"is_trading"`
}

type EveLedgerInventoryItem struct {
	TypeID        int32   `json:"type_id"`
	TypeName      string  `json:"type_name"`
	Quantity      int64   `json:"quantity"`
	AdjustedPrice float64 `json:"adjusted_price"`
	MarketValue   float64 `json:"market_value"`
	CostBasis     float64 `json:"cost_basis"`
	UnrealizedPnL float64 `json:"unrealized_pnl"`
	Priced        bool    `json:"priced"`
}

type eveLedgerCategoryClass struct {
	key       string
	label     string
	isTrading bool
}

// ComputeEveLedgerDashboard combines wallet journal, market transactions,
// active orders and assets into a single capital/cashflow dashboard.
func ComputeEveLedgerDashboard(
	journal []esi.WalletJournalEntry,
	txns []esi.WalletTransaction,
	orders []esi.CharacterOrder,
	assets []esi.CharacterAsset,
	adjustedPrices map[int32]float64,
	walletISK float64,
	opt EveLedgerOptions,
) *EveLedgerDashboard {
	if opt.LookbackDays <= 0 {
		opt.LookbackDays = 90
	}
	if opt.LookbackDays > 365 {
		opt.LookbackDays = 365
	}
	if opt.LedgerLimit <= 0 {
		opt.LedgerLimit = 500
	}

	portfolio := ComputePortfolioPnLWithOptions(txns, PortfolioPnLOptions{
		LookbackDays:         opt.LookbackDays,
		SalesTaxPercent:      opt.SalesTaxPercent,
		BrokerFeePercent:     opt.BrokerFeePercent,
		LedgerLimit:          opt.LedgerLimit,
		IncludeUnmatchedSell: false,
	})

	now := time.Now().UTC()
	start := dateOnly(now).AddDate(0, 0, -(opt.LookbackDays - 1))
	daily := make([]EveLedgerCurvePoint, 0, opt.LookbackDays)
	dayIndex := make(map[string]int, opt.LookbackDays)
	for i := 0; i < opt.LookbackDays; i++ {
		d := start.AddDate(0, 0, i)
		key := d.Format("2006-01-02")
		dayIndex[key] = len(daily)
		daily = append(daily, EveLedgerCurvePoint{
			Period:    key,
			StartDate: key,
			EndDate:   key,
		})
	}

	categoriesByKey := make(map[string]*EveLedgerCategory)
	addCategory := func(class eveLedgerCategoryClass, amount float64) {
		cat := categoriesByKey[class.key]
		if cat == nil {
			cat = &EveLedgerCategory{
				Key:       class.key,
				Label:     class.label,
				IsTrading: class.isTrading,
			}
			categoriesByKey[class.key] = cat
		}
		if amount >= 0 {
			cat.IncomeISK += amount
		} else {
			cat.OutgoingISK += -amount
		}
		cat.NetISK += amount
		cat.Entries++
	}

	var summary EveLedgerSummary
	summary.WalletISK = walletISK
	summary.TransactionCount = len(txns)

	for _, entry := range journal {
		t, err := time.Parse(time.RFC3339, entry.Date)
		if err != nil {
			continue
		}
		if t.Before(start) {
			continue
		}
		amount := entry.Amount
		class := classifyWalletRefType(entry.RefType)
		addCategory(class, amount)

		if amount >= 0 {
			summary.JournalIncomeISK += amount
		} else {
			summary.JournalOutgoingISK += -amount
		}
		summary.JournalNetISK += amount
		summary.JournalEntries++
		if class.isTrading {
			summary.TradingCashflowISK += amount
		} else if amount >= 0 {
			summary.OtherIncomeISK += amount
			summary.OtherNetISK += amount
		} else {
			summary.OtherOutgoingISK += -amount
			summary.OtherNetISK += amount
		}

		key := t.UTC().Format("2006-01-02")
		if idx, ok := dayIndex[key]; ok {
			if amount >= 0 {
				daily[idx].IncomeISK += amount
			} else {
				daily[idx].OutgoingISK += -amount
			}
			daily[idx].NetCashflowISK += amount
			if !class.isTrading {
				daily[idx].OtherNetISK += amount
			}
			daily[idx].JournalEntries++
		}
	}

	for _, d := range portfolio.DailyPnL {
		if idx, ok := dayIndex[d.Date]; ok {
			daily[idx].TradingPnLISK += d.NetPnL
			daily[idx].Transactions += d.Transactions
		}
	}
	summary.TradingPnLISK = portfolio.Summary.TotalPnL

	inventory, invSummary := buildLedgerInventory(assets, adjustedPrices, portfolio.OpenPositions)
	summary.InventoryMTMISK = invSummary.InventoryMTMISK
	summary.InventoryCostBasisISK = invSummary.InventoryCostBasisISK
	summary.UnrealizedPnLISK = invSummary.UnrealizedPnLISK
	summary.AssetTypes = invSummary.AssetTypes
	summary.AssetUnits = invSummary.AssetUnits
	summary.PricedAssetTypes = invSummary.PricedAssetTypes
	summary.UnpricedAssetTypes = invSummary.UnpricedAssetTypes
	summary.UnpricedAssetUnits = invSummary.UnpricedAssetUnits

	for _, order := range orders {
		value := order.Price * float64(order.VolumeRemain)
		if order.IsBuyOrder {
			summary.BuyOrdersValueISK += value
		} else {
			summary.SellOrdersValueISK += value
		}
	}
	summary.OpenOrdersValueISK = summary.BuyOrdersValueISK + summary.SellOrdersValueISK
	summary.EstimatedCapitalISK = walletISK + summary.InventoryMTMISK + summary.SellOrdersValueISK + summary.BuyOrdersValueISK

	applyCapitalCurve(daily, summary.EstimatedCapitalISK)

	categories := make([]EveLedgerCategory, 0, len(categoriesByKey))
	for _, cat := range categoriesByKey {
		categories = append(categories, *cat)
	}
	sort.Slice(categories, func(i, j int) bool {
		absI := categories[i].NetISK
		if absI < 0 {
			absI = -absI
		}
		absJ := categories[j].NetISK
		if absJ < 0 {
			absJ = -absJ
		}
		if absI == absJ {
			return categories[i].Label < categories[j].Label
		}
		return absI > absJ
	})

	return &EveLedgerDashboard{
		Summary:    summary,
		Daily:      daily,
		Weekly:     aggregateLedgerCurve(daily, "weekly"),
		Monthly:    aggregateLedgerCurve(daily, "monthly"),
		Categories: categories,
		Inventory:  inventory,
		Settings: EveLedgerSettings{
			LookbackDays:     opt.LookbackDays,
			SalesTaxPercent:  opt.SalesTaxPercent,
			BrokerFeePercent: opt.BrokerFeePercent,
		},
		Portfolio: portfolio,
	}
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func classifyWalletRefType(refType string) eveLedgerCategoryClass {
	ref := strings.ToLower(strings.TrimSpace(refType))
	hasAny := func(parts ...string) bool {
		for _, part := range parts {
			if strings.Contains(ref, part) {
				return true
			}
		}
		return false
	}

	switch {
	case hasAny("market", "broker", "escrow", "transaction_tax"):
		return eveLedgerCategoryClass{key: "market", label: "Market / trading", isTrading: true}
	case hasAny("contract"):
		return eveLedgerCategoryClass{key: "contracts", label: "Contracts"}
	case hasAny("industry", "manufacturing", "factory", "research", "job"):
		return eveLedgerCategoryClass{key: "industry", label: "Industry"}
	case hasAny("bounty", "mission", "agent", "incursion", "npc"):
		return eveLedgerCategoryClass{key: "pve", label: "PvE income"}
	case hasAny("mining", "resource"):
		return eveLedgerCategoryClass{key: "mining", label: "Mining / resources"}
	case hasAny("donation", "deposit", "withdrawal", "transfer", "corporation_account", "player_trading"):
		return eveLedgerCategoryClass{key: "transfers", label: "Transfers"}
	case hasAny("insurance"):
		return eveLedgerCategoryClass{key: "insurance", label: "Insurance"}
	case hasAny("clone", "skill", "character"):
		return eveLedgerCategoryClass{key: "character", label: "Character costs"}
	case hasAny("office", "structure", "asset_safety"):
		return eveLedgerCategoryClass{key: "structures", label: "Structures / logistics"}
	case hasAny("tax"):
		return eveLedgerCategoryClass{key: "taxes", label: "Taxes"}
	default:
		if ref == "" {
			return eveLedgerCategoryClass{key: "other", label: "Other"}
		}
		return eveLedgerCategoryClass{key: "other", label: "Other"}
	}
}

func applyCapitalCurve(points []EveLedgerCurvePoint, finalCapital float64) {
	futureNet := 0.0
	for i := len(points) - 1; i >= 0; i-- {
		points[i].CapitalISK = finalCapital - futureNet
		futureNet += points[i].TradingPnLISK + points[i].OtherNetISK
	}
}

func aggregateLedgerCurve(points []EveLedgerCurvePoint, mode string) []EveLedgerCurvePoint {
	if len(points) == 0 {
		return nil
	}
	type bucket struct {
		key string
		row EveLedgerCurvePoint
	}
	buckets := make([]bucket, 0)
	index := make(map[string]int)
	for _, point := range points {
		t, err := time.Parse("2006-01-02", point.StartDate)
		if err != nil {
			continue
		}
		key := point.StartDate
		if mode == "weekly" {
			year, week := t.ISOWeek()
			key = formatWeekKey(year, week)
		} else if mode == "monthly" {
			key = t.Format("2006-01")
		}
		idx, ok := index[key]
		if !ok {
			idx = len(buckets)
			index[key] = idx
			buckets = append(buckets, bucket{
				key: key,
				row: EveLedgerCurvePoint{
					Period:    key,
					StartDate: point.StartDate,
					EndDate:   point.EndDate,
				},
			})
		}
		row := &buckets[idx].row
		row.EndDate = point.EndDate
		row.IncomeISK += point.IncomeISK
		row.OutgoingISK += point.OutgoingISK
		row.NetCashflowISK += point.NetCashflowISK
		row.TradingPnLISK += point.TradingPnLISK
		row.OtherNetISK += point.OtherNetISK
		row.CapitalISK = point.CapitalISK
		row.JournalEntries += point.JournalEntries
		row.Transactions += point.Transactions
	}

	out := make([]EveLedgerCurvePoint, len(buckets))
	for i := range buckets {
		out[i] = buckets[i].row
	}
	return out
}

func formatWeekKey(year int, week int) string {
	if week < 10 {
		return "W0" + strconvItoa(week) + " " + strconvItoa(year)
	}
	return "W" + strconvItoa(week) + " " + strconvItoa(year)
}

func strconvItoa(v int) string {
	const digits = "0123456789"
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	n := v
	for n > 0 {
		i--
		buf[i] = digits[n%10]
		n /= 10
	}
	return string(buf[i:])
}

func buildLedgerInventory(assets []esi.CharacterAsset, adjustedPrices map[int32]float64, positions []OpenPosition) ([]EveLedgerInventoryItem, EveLedgerSummary) {
	type invAgg struct {
		typeID   int32
		name     string
		quantity int64
	}
	byType := make(map[int32]*invAgg)
	for _, asset := range assets {
		if asset.TypeID <= 0 {
			continue
		}
		qty := asset.Quantity
		if qty <= 0 && asset.IsSingleton {
			qty = 1
		}
		if qty <= 0 {
			continue
		}
		agg := byType[asset.TypeID]
		if agg == nil {
			name := strings.TrimSpace(asset.TypeName)
			if name == "" {
				name = "Type #" + strconvItoa(int(asset.TypeID))
			}
			agg = &invAgg{typeID: asset.TypeID, name: name}
			byType[asset.TypeID] = agg
		}
		agg.quantity += qty
	}

	costByType := make(map[int32]float64)
	for _, pos := range positions {
		costByType[pos.TypeID] += pos.CostBasis
	}

	out := make([]EveLedgerInventoryItem, 0, len(byType))
	summary := EveLedgerSummary{AssetTypes: len(byType)}
	for _, agg := range byType {
		price := adjustedPrices[agg.typeID]
		value := price * float64(agg.quantity)
		cost := costByType[agg.typeID]
		unrealized := 0.0
		if cost > 0 {
			unrealized = value - cost
			summary.UnrealizedPnLISK += unrealized
		}
		item := EveLedgerInventoryItem{
			TypeID:        agg.typeID,
			TypeName:      agg.name,
			Quantity:      agg.quantity,
			AdjustedPrice: price,
			MarketValue:   value,
			CostBasis:     cost,
			UnrealizedPnL: unrealized,
			Priced:        price > 0,
		}
		out = append(out, item)
		summary.AssetUnits += agg.quantity
		summary.InventoryCostBasisISK += cost
		if item.Priced {
			summary.PricedAssetTypes++
			summary.InventoryMTMISK += value
		} else {
			summary.UnpricedAssetTypes++
			summary.UnpricedAssetUnits += agg.quantity
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].MarketValue == out[j].MarketValue {
			return out[i].TypeName < out[j].TypeName
		}
		return out[i].MarketValue > out[j].MarketValue
	})
	if len(out) > 40 {
		out = out[:40]
	}
	return out, summary
}
