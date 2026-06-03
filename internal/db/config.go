package db

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"eve-flipper/internal/config"
)

// LoadConfig reads config from SQLite. If empty, returns defaults.
func (d *DB) LoadConfig() *config.Config {
	return d.LoadConfigForUser(DefaultUserID)
}

// LoadConfigForUser reads config from SQLite for a specific user.
// If empty, returns defaults.
func (d *DB) LoadConfigForUser(userID string) *config.Config {
	userID = normalizeUserID(userID)
	cfg := config.Default()

	rows, err := d.sql.Query("SELECT key, value FROM config WHERE user_id = ?", userID)
	if err != nil {
		return cfg
	}
	defer rows.Close()

	m := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			continue
		}
		m[k] = v
	}
	if err := rows.Err(); err != nil {
		return cfg
	}
	rows.Close()

	for k, v := range m {
		if !isPrivateConfigKey(k) {
			continue
		}
		opened, err := d.openPrivateString(userID, "config."+k, v)
		if err == nil {
			m[k] = opened
		} else {
			m[k] = ""
		}
	}

	if len(m) == 0 {
		return cfg
	}

	parseFloat := func(key string, current float64) float64 {
		v, ok := m[key]
		if !ok {
			return current
		}
		parsed, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return current
		}
		return parsed
	}
	parseInt := func(key string, current int) int {
		v, ok := m[key]
		if !ok {
			return current
		}
		parsed, err := strconv.Atoi(v)
		if err != nil {
			return current
		}
		return parsed
	}
	parseInt64 := func(key string, current int64) int64 {
		v, ok := m[key]
		if !ok {
			return current
		}
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return current
		}
		return parsed
	}
	parseBool := func(key string, current bool) bool {
		v, ok := m[key]
		if !ok {
			return current
		}
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			return current
		}
		return parsed
	}

	if v, ok := m["system_name"]; ok {
		cfg.SystemName = v
	}
	if v, ok := m["ignored_system_ids"]; ok {
		var ids []int32
		if err := json.Unmarshal([]byte(v), &ids); err == nil {
			cfg.IgnoredSystemIDs = ids
		}
	}
	cfg.CargoCapacity = parseFloat("cargo_capacity", cfg.CargoCapacity)
	cfg.BuyRadius = parseInt("buy_radius", cfg.BuyRadius)
	cfg.SellRadius = parseInt("sell_radius", cfg.SellRadius)
	cfg.MinMargin = parseFloat("min_margin", cfg.MinMargin)
	cfg.SalesTaxPercent = parseFloat("sales_tax_percent", cfg.SalesTaxPercent)
	cfg.BrokerFeePercent = parseFloat("broker_fee_percent", cfg.BrokerFeePercent)
	cfg.SplitTradeFees = parseBool("split_trade_fees", cfg.SplitTradeFees)
	cfg.BuyBrokerFeePercent = parseFloat("buy_broker_fee_percent", cfg.BuyBrokerFeePercent)
	cfg.SellBrokerFeePercent = parseFloat("sell_broker_fee_percent", cfg.SellBrokerFeePercent)
	cfg.BuySalesTaxPercent = parseFloat("buy_sales_tax_percent", cfg.BuySalesTaxPercent)
	cfg.SellSalesTaxPercent = parseFloat("sell_sales_tax_percent", cfg.SellSalesTaxPercent)
	cfg.MinDailyVolume = parseInt64("min_daily_volume", cfg.MinDailyVolume)
	cfg.MaxInvestment = parseFloat("max_investment", cfg.MaxInvestment)
	cfg.MinItemProfit = parseFloat("min_item_profit", cfg.MinItemProfit)
	cfg.MinS2BPerDay = parseFloat("min_s2b_per_day", cfg.MinS2BPerDay)
	cfg.MinBfSPerDay = parseFloat("min_bfs_per_day", cfg.MinBfSPerDay)
	cfg.MinS2BBfSRatio = parseFloat("min_s2b_bfs_ratio", cfg.MinS2BBfSRatio)
	cfg.MaxS2BBfSRatio = parseFloat("max_s2b_bfs_ratio", cfg.MaxS2BBfSRatio)
	cfg.MinRouteSecurity = parseFloat("min_route_security", cfg.MinRouteSecurity)
	cfg.AvgPricePeriod = parseInt("avg_price_period", cfg.AvgPricePeriod)
	cfg.MinPeriodROI = parseFloat("min_period_roi", cfg.MinPeriodROI)
	cfg.MaxDOS = parseFloat("max_dos", cfg.MaxDOS)
	cfg.MinDemandPerDay = parseFloat("min_demand_per_day", cfg.MinDemandPerDay)
	cfg.PurchaseDemandDays = parseFloat("purchase_demand_days", cfg.PurchaseDemandDays)
	cfg.ShippingCostPerM3Jump = parseFloat("shipping_cost_per_m3_jump", cfg.ShippingCostPerM3Jump)
	if v, ok := m["source_regions"]; ok {
		var regions []string
		if err := json.Unmarshal([]byte(v), &regions); err == nil {
			cfg.SourceRegions = regions
		}
	}
	if v, ok := m["target_region"]; ok {
		cfg.TargetRegion = v
	}
	if v, ok := m["target_market_system"]; ok {
		cfg.TargetMarketSystem = v
	}
	cfg.TargetMarketLocationID = parseInt64("target_market_location_id", cfg.TargetMarketLocationID)
	if v, ok := m["category_ids"]; ok {
		var ids []int32
		if err := json.Unmarshal([]byte(v), &ids); err == nil {
			cfg.CategoryIDs = ids
		}
	}
	cfg.SellOrderMode = parseBool("sell_order_mode", cfg.SellOrderMode)
	cfg.AlertTelegram = parseBool("alert_telegram", cfg.AlertTelegram)
	cfg.AlertDiscord = parseBool("alert_discord", cfg.AlertDiscord)
	cfg.AlertDesktop = parseBool("alert_desktop", cfg.AlertDesktop)
	if v, ok := m["alert_telegram_token"]; ok {
		cfg.AlertTelegramToken = v
	}
	if v, ok := m["alert_telegram_chat_id"]; ok {
		cfg.AlertTelegramChatID = v
	}
	if v, ok := m["alert_discord_webhook"]; ok {
		cfg.AlertDiscordWebhook = v
	}
	cfg.Opacity = parseInt("opacity", cfg.Opacity)
	cfg.WindowX = parseInt("window_x", cfg.WindowX)
	cfg.WindowY = parseInt("window_y", cfg.WindowY)
	cfg.WindowW = parseInt("window_w", cfg.WindowW)
	cfg.WindowH = parseInt("window_h", cfg.WindowH)

	return cfg
}

// SaveConfig writes config to SQLite (upsert all fields).
func (d *DB) SaveConfig(cfg *config.Config) error {
	return d.SaveConfigForUser(DefaultUserID, cfg)
}

// SaveConfigForUser writes config to SQLite (upsert all fields) for a specific user.
func (d *DB) SaveConfigForUser(userID string, cfg *config.Config) error {
	userID = normalizeUserID(userID)

	sourceRegionsJSON := "[]"
	if b, err := json.Marshal(cfg.SourceRegions); err == nil {
		sourceRegionsJSON = string(b)
	}
	ignoredSystemsJSON := "[]"
	if b, err := json.Marshal(cfg.IgnoredSystemIDs); err == nil {
		ignoredSystemsJSON = string(b)
	}
	categoryIDsJSON := "[]"
	if b, err := json.Marshal(cfg.CategoryIDs); err == nil {
		categoryIDsJSON = string(b)
	}

	pairs := map[string]string{
		"system_name":               cfg.SystemName,
		"ignored_system_ids":        ignoredSystemsJSON,
		"cargo_capacity":            fmt.Sprintf("%g", cfg.CargoCapacity),
		"buy_radius":                strconv.Itoa(cfg.BuyRadius),
		"sell_radius":               strconv.Itoa(cfg.SellRadius),
		"min_margin":                fmt.Sprintf("%g", cfg.MinMargin),
		"sales_tax_percent":         fmt.Sprintf("%g", cfg.SalesTaxPercent),
		"broker_fee_percent":        fmt.Sprintf("%g", cfg.BrokerFeePercent),
		"split_trade_fees":          strconv.FormatBool(cfg.SplitTradeFees),
		"buy_broker_fee_percent":    fmt.Sprintf("%g", cfg.BuyBrokerFeePercent),
		"sell_broker_fee_percent":   fmt.Sprintf("%g", cfg.SellBrokerFeePercent),
		"buy_sales_tax_percent":     fmt.Sprintf("%g", cfg.BuySalesTaxPercent),
		"sell_sales_tax_percent":    fmt.Sprintf("%g", cfg.SellSalesTaxPercent),
		"min_daily_volume":          strconv.FormatInt(cfg.MinDailyVolume, 10),
		"max_investment":            fmt.Sprintf("%g", cfg.MaxInvestment),
		"min_item_profit":           fmt.Sprintf("%g", cfg.MinItemProfit),
		"min_s2b_per_day":           fmt.Sprintf("%g", cfg.MinS2BPerDay),
		"min_bfs_per_day":           fmt.Sprintf("%g", cfg.MinBfSPerDay),
		"min_s2b_bfs_ratio":         fmt.Sprintf("%g", cfg.MinS2BBfSRatio),
		"max_s2b_bfs_ratio":         fmt.Sprintf("%g", cfg.MaxS2BBfSRatio),
		"min_route_security":        fmt.Sprintf("%g", cfg.MinRouteSecurity),
		"avg_price_period":          strconv.Itoa(cfg.AvgPricePeriod),
		"min_period_roi":            fmt.Sprintf("%g", cfg.MinPeriodROI),
		"max_dos":                   fmt.Sprintf("%g", cfg.MaxDOS),
		"min_demand_per_day":        fmt.Sprintf("%g", cfg.MinDemandPerDay),
		"purchase_demand_days":      fmt.Sprintf("%g", cfg.PurchaseDemandDays),
		"shipping_cost_per_m3_jump": fmt.Sprintf("%g", cfg.ShippingCostPerM3Jump),
		"source_regions":            sourceRegionsJSON,
		"target_region":             cfg.TargetRegion,
		"target_market_system":      cfg.TargetMarketSystem,
		"target_market_location_id": strconv.FormatInt(cfg.TargetMarketLocationID, 10),
		"category_ids":              categoryIDsJSON,
		"sell_order_mode":           strconv.FormatBool(cfg.SellOrderMode),
		"alert_telegram":            strconv.FormatBool(cfg.AlertTelegram),
		"alert_discord":             strconv.FormatBool(cfg.AlertDiscord),
		"alert_desktop":             strconv.FormatBool(cfg.AlertDesktop),
		"alert_telegram_token":      cfg.AlertTelegramToken,
		"alert_telegram_chat_id":    cfg.AlertTelegramChatID,
		"alert_discord_webhook":     cfg.AlertDiscordWebhook,
		"opacity":                   strconv.Itoa(cfg.Opacity),
		"window_x":                  strconv.Itoa(cfg.WindowX),
		"window_y":                  strconv.Itoa(cfg.WindowY),
		"window_w":                  strconv.Itoa(cfg.WindowW),
		"window_h":                  strconv.Itoa(cfg.WindowH),
	}

	storedPairs := make(map[string]string, len(pairs))
	for k, v := range pairs {
		stored := v
		if isPrivateConfigKey(k) {
			var err error
			stored, err = d.protectPrivateString(userID, "config."+k, v)
			if err != nil {
				return err
			}
		}
		storedPairs[k] = stored
	}

	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare("INSERT OR REPLACE INTO config (user_id, key, value) VALUES (?, ?, ?)")
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for k, stored := range storedPairs {
		if _, err := stmt.Exec(userID, k, stored); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func isPrivateConfigKey(key string) bool {
	switch key {
	case "alert_telegram_token", "alert_telegram_chat_id", "alert_discord_webhook":
		return true
	default:
		return false
	}
}

// MigrateFromJSON checks for config.json and imports it into SQLite.
func (d *DB) MigrateFromJSON() {
	wd, _ := os.Getwd()
	jsonPath := filepath.Join(wd, "config.json")

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return // no config.json, nothing to migrate
	}

	// Check if config table already has data
	var count int
	d.sql.QueryRow("SELECT COUNT(*) FROM config").Scan(&count)
	if count > 0 {
		// Already migrated, just rename the file
		os.Rename(jsonPath, jsonPath+".bak")
		return
	}

	log.Println("[DB] Migrating config.json → SQLite...")

	// Parse the old config
	var old struct {
		SystemName           string                 `json:"system_name"`
		CargoCapacity        float64                `json:"cargo_capacity"`
		BuyRadius            int                    `json:"buy_radius"`
		SellRadius           int                    `json:"sell_radius"`
		MinMargin            float64                `json:"min_margin"`
		SalesTaxPercent      float64                `json:"sales_tax_percent"`
		BrokerFeePercent     *float64               `json:"broker_fee_percent"`
		SplitTradeFees       *bool                  `json:"split_trade_fees"`
		BuyBrokerFeePercent  *float64               `json:"buy_broker_fee_percent"`
		SellBrokerFeePercent *float64               `json:"sell_broker_fee_percent"`
		BuySalesTaxPercent   *float64               `json:"buy_sales_tax_percent"`
		SellSalesTaxPercent  *float64               `json:"sell_sales_tax_percent"`
		AlertTelegram        bool                   `json:"alert_telegram"`
		AlertDiscord         bool                   `json:"alert_discord"`
		AlertDesktop         bool                   `json:"alert_desktop"`
		AlertTelegramToken   string                 `json:"alert_telegram_token"`
		AlertTelegramChatID  string                 `json:"alert_telegram_chat_id"`
		AlertDiscordWebhook  string                 `json:"alert_discord_webhook"`
		Opacity              int                    `json:"opacity"`
		WindowX              int                    `json:"window_x"`
		WindowY              int                    `json:"window_y"`
		WindowW              int                    `json:"window_w"`
		WindowH              int                    `json:"window_h"`
		Watchlist            []config.WatchlistItem `json:"watchlist"`
	}
	if err := json.Unmarshal(data, &old); err != nil {
		log.Printf("[DB] Failed to parse config.json: %v", err)
		return
	}

	// Save config
	cfg := config.Default()
	cfg.SystemName = old.SystemName
	cfg.CargoCapacity = old.CargoCapacity
	cfg.BuyRadius = old.BuyRadius
	cfg.SellRadius = old.SellRadius
	cfg.MinMargin = old.MinMargin
	cfg.SalesTaxPercent = old.SalesTaxPercent
	if old.BrokerFeePercent != nil {
		cfg.BrokerFeePercent = *old.BrokerFeePercent
	}
	if old.SplitTradeFees != nil {
		cfg.SplitTradeFees = *old.SplitTradeFees
	}
	if old.BuyBrokerFeePercent != nil {
		cfg.BuyBrokerFeePercent = *old.BuyBrokerFeePercent
	}
	if old.SellBrokerFeePercent != nil {
		cfg.SellBrokerFeePercent = *old.SellBrokerFeePercent
	}
	if old.BuySalesTaxPercent != nil {
		cfg.BuySalesTaxPercent = *old.BuySalesTaxPercent
	}
	if old.SellSalesTaxPercent != nil {
		cfg.SellSalesTaxPercent = *old.SellSalesTaxPercent
	}
	cfg.AlertTelegram = old.AlertTelegram
	cfg.AlertDiscord = old.AlertDiscord
	cfg.AlertDesktop = old.AlertDesktop
	cfg.AlertTelegramToken = old.AlertTelegramToken
	cfg.AlertTelegramChatID = old.AlertTelegramChatID
	cfg.AlertDiscordWebhook = old.AlertDiscordWebhook
	if !cfg.AlertTelegram && !cfg.AlertDiscord && !cfg.AlertDesktop {
		cfg.AlertDesktop = true
	}
	cfg.Opacity = old.Opacity
	cfg.WindowX = old.WindowX
	cfg.WindowY = old.WindowY
	cfg.WindowW = old.WindowW
	cfg.WindowH = old.WindowH
	d.SaveConfig(cfg)

	// Migrate watchlist
	for _, item := range old.Watchlist {
		d.AddWatchlistItem(item)
	}

	// Rename old file
	os.Rename(jsonPath, jsonPath+".bak")
	log.Printf("[DB] Migrated config.json → SQLite (%d watchlist items)", len(old.Watchlist))
}
