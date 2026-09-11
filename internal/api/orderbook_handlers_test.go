package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"eve-flipper/internal/config"
	"eve-flipper/internal/db"
	"eve-flipper/internal/engine"
	"eve-flipper/internal/esi"
)

// seedOrderBookInterest marks types as ones the owner deals in, so the
// archive's interest filter lets their books through. A fresh test DB has no
// trading history at all, and the filter correctly reads that as "nothing
// worth archiving" — so without this every RecordMarketOrderSnapshot below is
// a silent no-op.
func seedOrderBookInterest(t *testing.T, database *db.DB, typeIDs ...int32) {
	t.Helper()
	for _, typeID := range typeIDs {
		if !database.AddWatchlistItem(config.WatchlistItem{TypeID: typeID, TypeName: "test"}) {
			t.Fatalf("seed interest for type %d", typeID)
		}
	}
	db.InvalidateOrderBookInterest()
	t.Cleanup(db.InvalidateOrderBookInterest)
}

func TestOrderBookSnapshotHandlers(t *testing.T) {
	database := openAPITestDB(t)
	seedOrderBookInterest(t, database, 34, 35, 200)
	if err := database.RecordMarketOrderSnapshot(esi.MarketOrderSnapshot{
		RegionID:   10000002,
		OrderType:  "all",
		Source:     "region_type",
		TypeID:     34,
		CapturedAt: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
		Orders: []esi.MarketOrder{
			{TypeID: 34, LocationID: 60003760, SystemID: 30000142, Price: 5.0, VolumeRemain: 100, IsBuyOrder: false},
			{TypeID: 34, LocationID: 60003760, SystemID: 30000142, Price: 4.8, VolumeRemain: 50, IsBuyOrder: true},
		},
	}); err != nil {
		t.Fatalf("record orderbook snapshot: %v", err)
	}

	srv := &Server{db: database}
	handler := srv.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/orderbook/snapshots?type_id=34", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("snapshots status=%d body=%s", rec.Code, rec.Body.String())
	}
	var snapsOut struct {
		Snapshots []db.OrderBookSnapshotMeta `json:"snapshots"`
		Count     int                        `json:"count"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&snapsOut); err != nil {
		t.Fatalf("decode snapshots: %v", err)
	}
	if snapsOut.Count != 1 || len(snapsOut.Snapshots) != 1 {
		t.Fatalf("snapshot count=%d len=%d", snapsOut.Count, len(snapsOut.Snapshots))
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/orderbook/snapshots/"+strconv.FormatInt(snapsOut.Snapshots[0].ID, 10)+"/levels?side=sell", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("levels status=%d body=%s", rec.Code, rec.Body.String())
	}
	var levelsOut struct {
		Levels []db.OrderBookLevel `json:"levels"`
		Count  int                 `json:"count"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&levelsOut); err != nil {
		t.Fatalf("decode levels: %v", err)
	}
	if levelsOut.Count != 1 || levelsOut.Levels[0].Side != "sell" {
		t.Fatalf("levels response = %#v", levelsOut)
	}
}

func TestBacktestFlipsRecordedOrderbook(t *testing.T) {
	database := openAPITestDB(t)
	seedOrderBookInterest(t, database, 34, 35, 200)
	now := time.Now().UTC().Add(-time.Hour)
	if err := database.RecordMarketOrderSnapshot(esi.MarketOrderSnapshot{
		RegionID:   1,
		OrderType:  "sell",
		Source:     "region",
		CapturedAt: now,
		Orders: []esi.MarketOrder{
			{TypeID: 34, LocationID: 100, SystemID: 10, Price: 5, VolumeRemain: 5, IsBuyOrder: false},
			{TypeID: 34, LocationID: 100, SystemID: 10, Price: 6, VolumeRemain: 10, IsBuyOrder: false},
		},
	}); err != nil {
		t.Fatalf("record source snapshot: %v", err)
	}
	if err := database.RecordMarketOrderSnapshot(esi.MarketOrderSnapshot{
		RegionID:   2,
		OrderType:  "buy",
		Source:     "region",
		CapturedAt: now.Add(time.Minute),
		Orders: []esi.MarketOrder{
			{TypeID: 34, LocationID: 200, SystemID: 20, Price: 8, VolumeRemain: 10, IsBuyOrder: true},
		},
	}); err != nil {
		t.Fatalf("record target snapshot: %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"strategy_mode":              "instant_flip",
		"instant_price_mode":         "recorded_orderbook",
		"window_days":                1,
		"max_rows":                   10,
		"quantity_mode":              "scan",
		"orderbook_max_age_minutes":  5,
		"orderbook_cooldown_minutes": 1,
		"rows": []engine.FlipResult{{
			TypeID:         34,
			TypeName:       "Tritanium",
			BuyRegionID:    1,
			SellRegionID:   2,
			BuyLocationID:  100,
			SellLocationID: 200,
			BuyPrice:       5,
			SellPrice:      8,
			FilledQty:      10,
		}},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	srv := &Server{db: database}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/backtest/flips", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("recorded backtest status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out engine.FlipBacktestResult
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode backtest: %v", err)
	}
	if out.Summary.Trades != 1 || out.Ledger[0].PnL != 25 {
		t.Fatalf("backtest result = %#v", out)
	}
}

func TestOrderBookCoverageHandler(t *testing.T) {
	database := openAPITestDB(t)
	seedOrderBookInterest(t, database, 34, 35, 200)
	now := time.Now().UTC().Add(-time.Hour)
	if err := database.RecordMarketOrderSnapshot(esi.MarketOrderSnapshot{
		RegionID:   1,
		OrderType:  "sell",
		Source:     "region",
		CapturedAt: now,
		Orders: []esi.MarketOrder{
			{TypeID: 34, LocationID: 100, SystemID: 10, Price: 5, VolumeRemain: 5, IsBuyOrder: false},
		},
	}); err != nil {
		t.Fatalf("record source snapshot: %v", err)
	}
	if err := database.RecordMarketOrderSnapshot(esi.MarketOrderSnapshot{
		RegionID:   2,
		OrderType:  "buy",
		Source:     "region",
		CapturedAt: now.Add(time.Minute),
		Orders: []esi.MarketOrder{
			{TypeID: 34, LocationID: 200, SystemID: 20, Price: 8, VolumeRemain: 5, IsBuyOrder: true},
		},
	}); err != nil {
		t.Fatalf("record target snapshot: %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"window_days":                1,
		"max_rows":                   10,
		"orderbook_max_age_minutes":  5,
		"orderbook_cooldown_minutes": 1,
		"rows": []engine.FlipResult{{
			TypeID:         34,
			TypeName:       "Tritanium",
			BuyRegionID:    1,
			SellRegionID:   2,
			BuyLocationID:  100,
			SellLocationID: 200,
		}},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	srv := &Server{db: database}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/orderbook/coverage", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("coverage status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out engine.OrderBookReplayCoverageResult
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode coverage: %v", err)
	}
	if out.Summary.RowsReady != 1 || out.Summary.PairedBooks != 1 || out.Rows[0].Status != "ready" {
		t.Fatalf("coverage result = %#v", out)
	}
}

func TestOrderBookMaintenanceHandlers(t *testing.T) {
	database := openAPITestDB(t)
	seedOrderBookInterest(t, database, 34, 35, 200)
	now := time.Now().UTC()
	if err := database.RecordMarketOrderSnapshot(esi.MarketOrderSnapshot{
		RegionID:   10000002,
		OrderType:  "sell",
		Source:     "region",
		CapturedAt: now.AddDate(0, 0, -90),
		Orders: []esi.MarketOrder{
			{TypeID: 34, LocationID: 60003760, SystemID: 30000142, Price: 5.0, VolumeRemain: 100, IsBuyOrder: false},
		},
	}); err != nil {
		t.Fatalf("record old snapshot: %v", err)
	}
	if err := database.RecordMarketOrderSnapshot(esi.MarketOrderSnapshot{
		RegionID:   10000002,
		OrderType:  "buy",
		Source:     "region",
		CapturedAt: now.Add(-time.Hour),
		Orders: []esi.MarketOrder{
			{TypeID: 35, LocationID: 60008494, SystemID: 30000144, Price: 8.0, VolumeRemain: 50, IsBuyOrder: true},
		},
	}); err != nil {
		t.Fatalf("record new snapshot: %v", err)
	}

	handler := (&Server{db: database}).Handler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/orderbook/stats?limit=2", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats status=%d body=%s", rec.Code, rec.Body.String())
	}
	var stats db.OrderBookStats
	if err := json.NewDecoder(rec.Body).Decode(&stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if stats.SnapshotCount != 2 || stats.LevelCount != 2 || len(stats.TopTypes) != 2 {
		t.Fatalf("stats = %#v", stats)
	}

	body, err := json.Marshal(map[string]any{
		"keep_days": 30,
		"dry_run":   true,
	})
	if err != nil {
		t.Fatalf("marshal preview: %v", err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/orderbook/cleanup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", rec.Code, rec.Body.String())
	}
	var preview db.OrderBookCleanupPlan
	if err := json.NewDecoder(rec.Body).Decode(&preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if !preview.DryRun || preview.SnapshotsDeleted != 1 || preview.LevelsDeleted != 1 {
		t.Fatalf("preview = %#v", preview)
	}

	body, err = json.Marshal(map[string]any{
		"keep_days": 30,
		"dry_run":   false,
	})
	if err != nil {
		t.Fatalf("marshal cleanup: %v", err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/orderbook/cleanup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cleanup status=%d body=%s", rec.Code, rec.Body.String())
	}
	var cleanup db.OrderBookCleanupPlan
	if err := json.NewDecoder(rec.Body).Decode(&cleanup); err != nil {
		t.Fatalf("decode cleanup: %v", err)
	}
	if cleanup.DryRun || cleanup.SnapshotsDeleted != 1 || cleanup.LevelsDeleted != 1 {
		t.Fatalf("cleanup = %#v", cleanup)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/orderbook/stats", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats after cleanup status=%d body=%s", rec.Code, rec.Body.String())
	}
	stats = db.OrderBookStats{}
	if err := json.NewDecoder(rec.Body).Decode(&stats); err != nil {
		t.Fatalf("decode stats after cleanup: %v", err)
	}
	if stats.SnapshotCount != 1 || stats.LevelCount != 1 || stats.TopTypes[0].TypeID != 35 {
		t.Fatalf("stats after cleanup = %#v", stats)
	}
}

func TestOrderBookCleanupHandlerUsesBatchedCleanupOptions(t *testing.T) {
	database := openAPITestDB(t)
	seedOrderBookInterest(t, database, 34, 35, 200)

	seedOrderBookInterest(t, database, 100, 101, 102, 103)

	old := time.Now().UTC().AddDate(0, 0, -90)
	for i := 0; i < 4; i++ {
		if err := database.RecordMarketOrderSnapshot(esi.MarketOrderSnapshot{
			RegionID:   10000002,
			OrderType:  "sell",
			Source:     "region",
			CapturedAt: old.Add(time.Duration(i) * time.Minute),
			Orders: []esi.MarketOrder{
				{OrderID: int64(10 + i), TypeID: int32(100 + i), LocationID: 60008494, SystemID: 30000142, Price: 100, VolumeRemain: 1},
			},
		}); err != nil {
			t.Fatalf("record old snapshot %d: %v", i, err)
		}
	}
	if err := database.RecordMarketOrderSnapshot(esi.MarketOrderSnapshot{
		RegionID:   10000002,
		OrderType:  "sell",
		Source:     "region",
		CapturedAt: time.Now().UTC(),
		Orders: []esi.MarketOrder{
			{OrderID: 20, TypeID: 200, LocationID: 60008494, SystemID: 30000142, Price: 100, VolumeRemain: 1},
		},
	}); err != nil {
		t.Fatalf("record fresh snapshot: %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"keep_days":       30,
		"batch_size":      1,
		"max_duration_ms": 1,
	})
	if err != nil {
		t.Fatalf("marshal cleanup: %v", err)
	}
	handler := (&Server{db: database}).Handler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/orderbook/cleanup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cleanup status=%d body=%s", rec.Code, rec.Body.String())
	}
	var cleanup db.OrderBookCleanupPlan
	if err := json.NewDecoder(rec.Body).Decode(&cleanup); err != nil {
		t.Fatalf("decode cleanup: %v", err)
	}
	if cleanup.DryRun || cleanup.SnapshotsDeleted <= 0 || cleanup.SnapshotsDeleted > 4 {
		t.Fatalf("cleanup = %#v, want bounded non-dry run deletion", cleanup)
	}
	if cleanup.LevelsDeleted != cleanup.SnapshotsDeleted {
		t.Fatalf("cleanup levels = %#v, want one level per snapshot", cleanup)
	}
}

func TestOrderBookRecordingHandlerRoundTripsAndPushesToESI(t *testing.T) {
	// The switch has to reach two places: the DB, so it survives a restart,
	// and the live ESI client, so the very next scan honours it. Persisting
	// only one of the two is the failure mode that looks fine in the UI and
	// keeps writing gigabytes anyway.
	database := openAPITestDB(t)
	seedOrderBookInterest(t, database, 34, 35, 200)
	client := esi.NewClient(database)
	srv := &Server{db: database, esi: client}
	handler := srv.Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/orderbook/recording", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Enabled       bool `json:"enabled"`
		RetentionDays int  `json:"retention_days"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if out.Enabled {
		t.Fatal("recording defaulted to on")
	}
	if out.RetentionDays != orderBookRetentionDays {
		t.Fatalf("retention_days=%d, want %d", out.RetentionDays, orderBookRetentionDays)
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/orderbook/recording", bytes.NewReader([]byte(`{"enabled":true}`)))
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("post status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode post: %v", err)
	}
	if !out.Enabled {
		t.Fatal("post did not report recording on")
	}
	if !client.MarketOrderRecordingEnabled() {
		t.Fatal("switch did not reach the live ESI client")
	}
	if !database.OrderBookRecordingEnabled() {
		t.Fatal("switch was not persisted")
	}

	// A fresh Server over the same DB is what a restart looks like.
	restarted := &Server{db: database, esi: esi.NewClient(database)}
	if !restarted.applyOrderBookRecording() {
		t.Fatal("recording did not survive a restart")
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/orderbook/recording", bytes.NewReader([]byte(`{"enabled":false}`)))
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("post off status=%d body=%s", rec.Code, rec.Body.String())
	}
	if client.MarketOrderRecordingEnabled() || database.OrderBookRecordingEnabled() {
		t.Fatal("switching off did not stick")
	}
}
