package db

import (
	"testing"

	"eve-flipper/internal/config"
)

func TestLoadConfigForUser_InvalidScalarValuesKeepDefaults(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	userID := "invalid-config-user"
	defaultCfg := config.Default()

	_, err := d.sql.Exec(`
		INSERT OR REPLACE INTO config (user_id, key, value) VALUES
			(?, 'cargo_capacity', 'not-a-number'),
			(?, 'buy_radius', 'abc'),
			(?, 'min_daily_volume', 'oops'),
			(?, 'target_market_location_id', 'bad-int'),
			(?, 'split_trade_fees', 'maybe'),
			(?, 'alert_desktop', 'definitely')
	`, userID, userID, userID, userID, userID, userID)
	if err != nil {
		t.Fatalf("insert invalid config rows: %v", err)
	}

	got := d.LoadConfigForUser(userID)

	if got.CargoCapacity != defaultCfg.CargoCapacity {
		t.Fatalf("CargoCapacity = %v, want default %v", got.CargoCapacity, defaultCfg.CargoCapacity)
	}
	if got.BuyRadius != defaultCfg.BuyRadius {
		t.Fatalf("BuyRadius = %v, want default %v", got.BuyRadius, defaultCfg.BuyRadius)
	}
	if got.MinDailyVolume != defaultCfg.MinDailyVolume {
		t.Fatalf("MinDailyVolume = %v, want default %v", got.MinDailyVolume, defaultCfg.MinDailyVolume)
	}
	if got.TargetMarketLocationID != defaultCfg.TargetMarketLocationID {
		t.Fatalf("TargetMarketLocationID = %v, want default %v", got.TargetMarketLocationID, defaultCfg.TargetMarketLocationID)
	}
	if got.SplitTradeFees != defaultCfg.SplitTradeFees {
		t.Fatalf("SplitTradeFees = %v, want default %v", got.SplitTradeFees, defaultCfg.SplitTradeFees)
	}
	if got.AlertDesktop != defaultCfg.AlertDesktop {
		t.Fatalf("AlertDesktop = %v, want default %v", got.AlertDesktop, defaultCfg.AlertDesktop)
	}
}

// TestConfigRoundTrip_FollowsYourLogin covers the fields this round moved out
// of localStorage: PLEX alert thresholds, Station Trading's working context,
// PI Factory's settings and the Order Desk's opaque prefs blob. The whole
// point of storing these server-side is that a second browser sees exactly
// what the first one saved, so this asserts values survive a save and a
// fresh load byte-for-byte, not just "some value."
func TestConfigRoundTrip_FollowsYourLogin(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	userID := "follows-your-login-user"
	cfg := config.Default()

	cfg.PlexAlertsEnabled = true
	cfg.PlexAlertBelowPrice = 4_100_000
	cfg.PlexAlertAbovePrice = 5_500_000
	cfg.PlexAlertOnCCPSale = false
	cfg.PlexAlertOnSignalChange = true

	cfg.StationSystemName = "Amarr"
	cfg.StationStationID = 60008494
	cfg.StationDiscountBidTarget = 0.62
	cfg.StationOperatorMode = true
	cfg.StationIgnoredCategories = []int32{6, 25, 87}

	cfg.PIFactorySystemName = "Dodixie"
	cfg.PIFactoryStationID = 60011866
	cfg.PIFactoryPocoTaxPct = 10
	cfg.PIFactorySalesTaxPct = 3.6
	cfg.PIFactoryBrokerFeePct = 2.5
	cfg.PIFactoryBufferDays = 14
	cfg.PIFactoryLaunchpadM3 = 12000

	cfg.OrdersPrefsJSON = `{"sort":[{"key":"margin","dir":"desc"}],"refreshMinutes":5}`
	cfg.ActivePresetIDsJSON = `{"flipper":"flip-aggressive","station":"preset_deadbeef01234567"}`

	if err := d.SaveConfigForUser(userID, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	got := d.LoadConfigForUser(userID)

	if got.PlexAlertsEnabled != true || got.PlexAlertBelowPrice != 4_100_000 ||
		got.PlexAlertAbovePrice != 5_500_000 || got.PlexAlertOnCCPSale != false ||
		got.PlexAlertOnSignalChange != true {
		t.Errorf("PLEX alert fields did not round-trip: %+v", got)
	}

	if got.StationSystemName != "Amarr" || got.StationStationID != 60008494 ||
		got.StationDiscountBidTarget != 0.62 || got.StationOperatorMode != true {
		t.Errorf("Station Trading scalar fields did not round-trip: %+v", got)
	}
	if len(got.StationIgnoredCategories) != 3 ||
		got.StationIgnoredCategories[0] != 6 || got.StationIgnoredCategories[1] != 25 || got.StationIgnoredCategories[2] != 87 {
		t.Errorf("StationIgnoredCategories = %v, want [6 25 87]", got.StationIgnoredCategories)
	}

	if got.PIFactorySystemName != "Dodixie" || got.PIFactoryStationID != 60011866 ||
		got.PIFactoryPocoTaxPct != 10 || got.PIFactorySalesTaxPct != 3.6 ||
		got.PIFactoryBrokerFeePct != 2.5 || got.PIFactoryBufferDays != 14 || got.PIFactoryLaunchpadM3 != 12000 {
		t.Errorf("PI Factory settings did not round-trip: %+v", got)
	}

	if got.OrdersPrefsJSON != cfg.OrdersPrefsJSON {
		t.Errorf("OrdersPrefsJSON = %q, want %q", got.OrdersPrefsJSON, cfg.OrdersPrefsJSON)
	}
	if got.ActivePresetIDsJSON != cfg.ActivePresetIDsJSON {
		t.Errorf("ActivePresetIDsJSON = %q, want %q", got.ActivePresetIDsJSON, cfg.ActivePresetIDsJSON)
	}
}

// TestConfigRoundTrip_EmptyStationIgnoredCategoriesIsNotNil pins the
// zero-item case: a user who has cleared every ignored category should get
// back an empty slice, not nil that then panics or is silently absent from a
// JSON response the frontend expects an array in.
func TestConfigRoundTrip_EmptyStationIgnoredCategoriesIsNotNil(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	userID := "empty-ignored-categories-user"
	cfg := config.Default()
	cfg.StationIgnoredCategories = []int32{6}
	if err := d.SaveConfigForUser(userID, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	cfg.StationIgnoredCategories = nil
	if err := d.SaveConfigForUser(userID, cfg); err != nil {
		t.Fatalf("save config (cleared): %v", err)
	}

	got := d.LoadConfigForUser(userID)
	if len(got.StationIgnoredCategories) != 0 {
		t.Errorf("StationIgnoredCategories = %v, want empty after clearing", got.StationIgnoredCategories)
	}
}

// TestConfigDefaults_NewFieldsMatchClientDefaults pins the defaults a
// pre-migration user (or a brand-new one) reads before ever saving anything,
// against the same numbers PlexAlerts.tsx / StationTrading.tsx /
// PIFactory.tsx already default to client-side -- so a first load never
// silently disagrees with what the UI showed before this round.
func TestConfigDefaults_NewFieldsMatchClientDefaults(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	got := d.LoadConfigForUser("never-saved-anything-user")

	if got.PlexAlertsEnabled {
		t.Error("PlexAlertsEnabled defaulted to true, want false (off until explicitly armed)")
	}
	if got.PlexAlertBelowPrice != 0 || got.PlexAlertAbovePrice != 0 {
		t.Errorf("PLEX price thresholds defaulted to nonzero: below=%v above=%v", got.PlexAlertBelowPrice, got.PlexAlertAbovePrice)
	}
	if got.StationDiscountBidTarget != 0.5 {
		t.Errorf("StationDiscountBidTarget default = %v, want 0.5", got.StationDiscountBidTarget)
	}
	if got.PIFactorySystemName != "Jita" || got.PIFactoryStationID != 60003760 {
		t.Errorf("PI Factory station default = %q/%d, want Jita/60003760", got.PIFactorySystemName, got.PIFactoryStationID)
	}
	if got.PIFactoryPocoTaxPct != 15 || got.PIFactorySalesTaxPct != 4.5 || got.PIFactoryBrokerFeePct != 3 {
		t.Errorf("PI Factory tax/fee defaults = %v/%v/%v, want 15/4.5/3",
			got.PIFactoryPocoTaxPct, got.PIFactorySalesTaxPct, got.PIFactoryBrokerFeePct)
	}
	if got.PIFactoryBufferDays != 7 {
		t.Errorf("PIFactoryBufferDays default = %v, want 7", got.PIFactoryBufferDays)
	}
	if got.PIFactoryLaunchpadM3 != 10000 {
		t.Errorf("PIFactoryLaunchpadM3 default = %v, want 10000", got.PIFactoryLaunchpadM3)
	}
	if got.OrdersPrefsJSON != "" {
		t.Errorf("OrdersPrefsJSON default = %q, want empty", got.OrdersPrefsJSON)
	}
	if got.ActivePresetIDsJSON != "" {
		t.Errorf("ActivePresetIDsJSON default = %q, want empty", got.ActivePresetIDsJSON)
	}
}
