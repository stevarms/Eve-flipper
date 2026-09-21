package config

// StockpileSource identifies where a stockpile pulls its inventory from.
const (
	StockpileSourceCharacter   = "character"
	StockpileSourceCorporation = "corporation"
)

// Stockpile is a named per-user manufacturing stockpile at a single station or
// structure. Items live in StockpileItem records referenced by ID.
type Stockpile struct {
	ID                  int64           `json:"id"`
	Name                string          `json:"name"`
	Source              string          `json:"source"`
	SourceCharacterID   int64           `json:"source_character_id,omitempty"`
	SourceCorporationID int64           `json:"source_corporation_id,omitempty"`
	StationID           int64           `json:"station_id"`
	StationName         string          `json:"station_name,omitempty"`
	CreatedAt           string          `json:"created_at"`
	UpdatedAt           string          `json:"updated_at"`
	Items               []StockpileItem `json:"items,omitempty"`
}

// StockpileItem is a single (type_id, threshold) row inside a Stockpile.
type StockpileItem struct {
	TypeID       int32  `json:"type_id"`
	TypeName     string `json:"type_name"`
	ThresholdQty int64  `json:"threshold_qty"`
	CreatedAt    string `json:"created_at,omitempty"`
}

// WatchlistItem represents an item being tracked in the watchlist.
type WatchlistItem struct {
	TypeID         int32   `json:"type_id"`
	TypeName       string  `json:"type_name"`
	AddedAt        string  `json:"added_at"`
	AlertMinMargin float64 `json:"alert_min_margin"` // 0 = no alert
	AlertEnabled   bool    `json:"alert_enabled"`
	AlertMetric    string  `json:"alert_metric"`    // margin_percent | total_profit | profit_per_unit | daily_volume
	AlertThreshold float64 `json:"alert_threshold"` // threshold for selected metric
}

// Config holds application settings (in-memory representation).
// Persistence is handled by internal/db package.
type Config struct {
	SystemName           string  `json:"system_name"`
	IgnoredSystemIDs     []int32 `json:"ignored_system_ids"`
	CargoCapacity        float64 `json:"cargo_capacity"`
	BuyRadius            int     `json:"buy_radius"`
	SellRadius           int     `json:"sell_radius"`
	MinMargin            float64 `json:"min_margin"`
	SalesTaxPercent      float64 `json:"sales_tax_percent"`
	BrokerFeePercent     float64 `json:"broker_fee_percent"`
	SplitTradeFees       bool    `json:"split_trade_fees"`
	BuyBrokerFeePercent  float64 `json:"buy_broker_fee_percent"`
	SellBrokerFeePercent float64 `json:"sell_broker_fee_percent"`
	BuySalesTaxPercent   float64 `json:"buy_sales_tax_percent"`
	SellSalesTaxPercent  float64 `json:"sell_sales_tax_percent"`

	// Shared advanced scan filters.
	MinDailyVolume   int64   `json:"min_daily_volume"`
	MaxInvestment    float64 `json:"max_investment"`
	MinItemProfit    float64 `json:"min_item_profit"`
	MinS2BPerDay     float64 `json:"min_s2b_per_day"`
	MinBfSPerDay     float64 `json:"min_bfs_per_day"`
	MinS2BBfSRatio   float64 `json:"min_s2b_bfs_ratio"`
	MaxS2BBfSRatio   float64 `json:"max_s2b_bfs_ratio"`
	MinRouteSecurity float64 `json:"min_route_security"`

	// Regional day-trader parameters.
	AvgPricePeriod         int      `json:"avg_price_period"`
	MinPeriodROI           float64  `json:"min_period_roi"`
	MaxDOS                 float64  `json:"max_dos"`
	MinDemandPerDay        float64  `json:"min_demand_per_day"`
	PurchaseDemandDays     float64  `json:"purchase_demand_days"`
	ShippingCostPerM3Jump  float64  `json:"shipping_cost_per_m3_jump"`
	SourceRegions          []string `json:"source_regions"`
	TargetRegion           string   `json:"target_region"`
	TargetMarketSystem     string   `json:"target_market_system"`
	TargetMarketLocationID int64    `json:"target_market_location_id"`
	CategoryIDs            []int32  `json:"category_ids"`
	SellOrderMode          bool     `json:"sell_order_mode"`

	AlertTelegram       bool   `json:"alert_telegram"`
	AlertDiscord        bool   `json:"alert_discord"`
	AlertDesktop        bool   `json:"alert_desktop"`
	AlertTelegramToken  string `json:"alert_telegram_token"`
	AlertTelegramChatID string `json:"alert_telegram_chat_id"`
	AlertDiscordWebhook string `json:"alert_discord_webhook"`
	Opacity             int    `json:"opacity"`
	WindowX             int    `json:"window_x"`
	WindowY             int    `json:"window_y"`
	WindowW             int    `json:"window_w"`
	WindowH             int    `json:"window_h"`

	// PLEX alert thresholds (PlexAlerts.tsx). Below/Above are ISK prices;
	// 0 means that side is off, the same convention the client already used
	// when this lived in localStorage.
	PlexAlertsEnabled       bool    `json:"plex_alerts_enabled"`
	PlexAlertBelowPrice     float64 `json:"plex_alert_below_price"`
	PlexAlertAbovePrice     float64 `json:"plex_alert_above_price"`
	PlexAlertOnCCPSale      bool    `json:"plex_alert_on_ccp_sale"`
	PlexAlertOnSignalChange bool    `json:"plex_alert_on_signal_change"`

	// Station Trading's persisted working context (StationTrading.tsx).
	// StationIgnoredCategories is a set, stored and sent as a plain slice.
	StationSystemName        string  `json:"station_system_name"`
	StationStationID         int64   `json:"station_station_id"`
	StationDiscountBidTarget float64 `json:"station_discount_bid_target"`
	StationOperatorMode      bool    `json:"station_operator_mode"`
	StationIgnoredCategories []int32 `json:"station_ignored_categories"`

	// PI Factory's settings panel (PIFactory.tsx's PersistedSettings).
	PIFactorySystemName   string  `json:"pi_factory_system_name"`
	PIFactoryStationID    int64   `json:"pi_factory_station_id"`
	PIFactoryPocoTaxPct   float64 `json:"pi_factory_poco_tax_pct"`
	PIFactorySalesTaxPct  float64 `json:"pi_factory_sales_tax_pct"`
	PIFactoryBrokerFeePct float64 `json:"pi_factory_broker_fee_pct"`
	PIFactoryBufferDays   float64 `json:"pi_factory_buffer_days"`
	PIFactoryLaunchpadM3  float64 `json:"pi_factory_launchpad_m3"`

	// OrdersPrefsJSON is the Order Desk's sort/filter/threshold prefs, stored
	// opaquely -- ordersPrefs.ts's normalizeOrdersPrefs already tolerates
	// anything on the way in, so the server does not need to understand the
	// shape, only round-trip it. Empty means "nothing saved yet."
	OrdersPrefsJSON string `json:"orders_prefs_json"`

	// ActivePresetIDsJSON maps a PresetPicker tab to the id of the preset
	// currently applied there -- a builtin preset's id (e.g. "flip-normal")
	// or a saved_presets row's id, PresetPicker.tsx treats both the same way.
	// Opaque JSON for the same reason OrdersPrefsJSON is: the id space is the
	// frontend's to define, the server only round-trips it. This is the
	// "current selection" pointer that used to live in a separate
	// eve-flipper-active-preset-${tab} localStorage key per tab; the actual
	// saved preset data it can point to lives in the saved_presets table.
	ActivePresetIDsJSON string `json:"active_preset_ids_json"`
}

// Default returns a Config with sensible defaults.
func Default() *Config {
	return &Config{
		CargoCapacity:        5000,
		BuyRadius:            5,
		SellRadius:           10,
		MinMargin:            5,
		SalesTaxPercent:      8,
		BrokerFeePercent:     0,
		SplitTradeFees:       false,
		BuyBrokerFeePercent:  0,
		SellBrokerFeePercent: 0,
		BuySalesTaxPercent:   0,
		SellSalesTaxPercent:  8,
		MinRouteSecurity:     0.45,
		AvgPricePeriod:       14,
		PurchaseDemandDays:   0.5,
		SourceRegions: []string{
			"The Forge",
			"Domain",
			"Sinq Laison",
			"Metropolis",
			"Heimatar",
		},
		TargetMarketSystem: "Jita",
		AlertDesktop:       true,
		Opacity:            230,
		WindowW:            800,
		WindowH:            600,

		StationDiscountBidTarget: 0.5,

		// Matches PIFactory.tsx's own DEFAULT_SETTINGS -- Jita IV-4, the same
		// hub the rest of the app defaults new campaigns and scans to.
		PIFactorySystemName:   "Jita",
		PIFactoryStationID:    60003760,
		PIFactoryPocoTaxPct:   15,
		PIFactorySalesTaxPct:  4.5,
		PIFactoryBrokerFeePct: 3,
		PIFactoryBufferDays:   7,
		PIFactoryLaunchpadM3:  10000,
	}
}
