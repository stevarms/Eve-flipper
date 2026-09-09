package db

import (
	"testing"
	"time"

	"eve-flipper/internal/esi"
)

// seedOrderBookInterest makes the given types "things the owner deals in", so
// the archive's interest filter lets them through. Without it a fresh test DB
// has no transactions, no watchlist and no stockpile, so the filter correctly
// concludes there is nothing worth archiving and every snapshot is a no-op.
func seedOrderBookInterest(t *testing.T, d *DB, typeIDs ...int32) {
	t.Helper()
	for _, typeID := range typeIDs {
		if _, err := d.sql.Exec(
			"INSERT OR REPLACE INTO watchlist (user_id, type_id, type_name, added_at) VALUES (?, ?, ?, ?)",
			DefaultUserID, typeID, "test", time.Now().UTC().Format(time.RFC3339),
		); err != nil {
			t.Fatalf("seed interest for type %d: %v", typeID, err)
		}
	}
	// The interest set is cached process-wide, so a test that seeds after
	// another test resolved an empty set would otherwise see the stale one.
	InvalidateOrderBookInterest()
	t.Cleanup(InvalidateOrderBookInterest)
}

func TestRecordMarketOrderSnapshotAggregatesAndDedupes(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	seedOrderBookInterest(t, d, 34, 35, 36, 37, 40, 50, 51, 52, 53, 54, 60)

	snapshot := esi.MarketOrderSnapshot{
		RegionID:   10000002,
		OrderType:  "all",
		Source:     "region_type",
		TypeID:     34,
		CapturedAt: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
		ExpiresAt:  time.Date(2026, 4, 30, 12, 5, 0, 0, time.UTC),
		Orders: []esi.MarketOrder{
			{OrderID: 1, TypeID: 34, LocationID: 60003760, SystemID: 30000142, Price: 5.0, VolumeRemain: 100, IsBuyOrder: false, RegionID: 10000002},
			{OrderID: 2, TypeID: 34, LocationID: 60003760, SystemID: 30000142, Price: 5.0, VolumeRemain: 50, IsBuyOrder: false, RegionID: 10000002},
			{OrderID: 3, TypeID: 34, LocationID: 60003760, SystemID: 30000142, Price: 4.8, VolumeRemain: 70, IsBuyOrder: true, RegionID: 10000002},
		},
	}
	if err := d.RecordMarketOrderSnapshot(snapshot); err != nil {
		t.Fatalf("record snapshot: %v", err)
	}

	snaps, err := d.ListOrderBookSnapshots(OrderBookSnapshotFilter{TypeID: 34, Limit: 10})
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("snapshots len=%d, want 1", len(snaps))
	}
	if snaps[0].OrderCount != 3 || snaps[0].LevelCount != 2 || snaps[0].UniqueLocationCount != 1 {
		t.Fatalf("snapshot counts = orders %d levels %d locations %d", snaps[0].OrderCount, snaps[0].LevelCount, snaps[0].UniqueLocationCount)
	}

	levels, err := d.GetOrderBookLevels(snaps[0].ID, OrderBookLevelFilter{TypeID: 34, Side: "sell"})
	if err != nil {
		t.Fatalf("get sell levels: %v", err)
	}
	if len(levels) != 1 {
		t.Fatalf("sell levels len=%d, want 1", len(levels))
	}
	if levels[0].VolumeRemain != 150 || levels[0].OrderCount != 2 {
		t.Fatalf("aggregated sell level = volume %d orders %d", levels[0].VolumeRemain, levels[0].OrderCount)
	}

	snapshot.CapturedAt = snapshot.CapturedAt.Add(5 * time.Minute)
	if err := d.RecordMarketOrderSnapshot(snapshot); err != nil {
		t.Fatalf("record duplicate snapshot: %v", err)
	}
	snaps, err = d.ListOrderBookSnapshots(OrderBookSnapshotFilter{TypeID: 34, Limit: 10})
	if err != nil {
		t.Fatalf("list after duplicate: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("duplicate should update last_seen, got %d snapshots", len(snaps))
	}
	if snaps[0].CapturedAt == snaps[0].LastSeenAt {
		t.Fatalf("duplicate did not update last_seen_at")
	}
}

func TestRecordMarketOrderSnapshotStoresChangedBook(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	seedOrderBookInterest(t, d, 34, 35, 36, 37, 40, 50, 51, 52, 53, 54, 60)

	base := esi.MarketOrderSnapshot{
		RegionID:   10000002,
		OrderType:  "sell",
		Source:     "region",
		CapturedAt: time.Now().UTC(),
		Orders: []esi.MarketOrder{
			{OrderID: 1, TypeID: 35, LocationID: 60003760, SystemID: 30000142, Price: 10.0, VolumeRemain: 10, IsBuyOrder: false},
		},
	}
	if err := d.RecordMarketOrderSnapshot(base); err != nil {
		t.Fatalf("record base: %v", err)
	}
	base.Orders[0].VolumeRemain = 11
	base.CapturedAt = base.CapturedAt.Add(orderBookMinRecordInterval + time.Minute)
	if err := d.RecordMarketOrderSnapshot(base); err != nil {
		t.Fatalf("record changed: %v", err)
	}

	snaps, err := d.ListOrderBookSnapshots(OrderBookSnapshotFilter{RegionID: 10000002, OrderType: "sell", Limit: 10})
	if err != nil {
		t.Fatalf("list changed snapshots: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("changed book snapshots len=%d, want 2", len(snaps))
	}
}

func TestRecordMarketOrderSnapshotSkipsInsideCooldown(t *testing.T) {
	// ESI books expire every five minutes, so a scanning session would
	// otherwise archive twelve full region snapshots an hour — each one
	// hundreds of thousands of rows. Replay reads a much coarser grid than
	// that, so the extra samples are pure cost. A skipped snapshot still
	// refreshes last_seen_at, so coverage reporting stays truthful about how
	// recently we actually looked at the book.
	d := openTestDB(t)
	defer d.Close()
	seedOrderBookInterest(t, d, 34, 35, 36, 37, 40, 50, 51, 52, 53, 54, 60)

	base := esi.MarketOrderSnapshot{
		RegionID:   10000002,
		OrderType:  "sell",
		Source:     "region",
		CapturedAt: time.Now().UTC(),
		Orders: []esi.MarketOrder{
			{OrderID: 1, TypeID: 35, LocationID: 60003760, SystemID: 30000142, Price: 10.0, VolumeRemain: 10, IsBuyOrder: false},
		},
	}
	if err := d.RecordMarketOrderSnapshot(base); err != nil {
		t.Fatalf("record base: %v", err)
	}

	// A genuinely different book, but inside the window.
	base.Orders[0].VolumeRemain = 11
	base.Orders[0].Price = 9.5
	base.CapturedAt = base.CapturedAt.Add(orderBookMinRecordInterval - time.Minute)
	if err := d.RecordMarketOrderSnapshot(base); err != nil {
		t.Fatalf("record inside cooldown: %v", err)
	}

	snaps, err := d.ListOrderBookSnapshots(OrderBookSnapshotFilter{RegionID: 10000002, OrderType: "sell", Limit: 10})
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("snapshots len=%d, want 1 — the cooldown should have suppressed the second write", len(snaps))
	}
	if snaps[0].LastSeenAt == snaps[0].CapturedAt {
		t.Fatalf("skipped snapshot did not refresh last_seen_at")
	}

	// The stored book must still be the first one, untouched — a skip is a
	// skip, not a silent partial overwrite.
	levels, err := d.GetOrderBookLevels(snaps[0].ID, OrderBookLevelFilter{TypeID: 35, Side: "sell"})
	if err != nil {
		t.Fatalf("get levels: %v", err)
	}
	if len(levels) != 1 || levels[0].VolumeRemain != 10 || levels[0].Price != 10.0 {
		t.Fatalf("stored level = %+v, want the original 10 @ 10.0", levels)
	}
}

func TestRecordMarketOrderSnapshotBatchesLevelsAcrossBatchBoundary(t *testing.T) {
	// Levels go in multi-row INSERTs rather than one statement per level, and
	// the last batch is a partial one. An off-by-one in the flush would drop
	// or duplicate the tail of the book, which replay would read as real
	// depth that is not there.
	d := openTestDB(t)
	defer d.Close()
	seedOrderBookInterest(t, d, 34, 35, 36, 37, 40, 50, 51, 52, 53, 54, 60)

	const levelCount = orderBookInsertBatch*2 + 7
	snapshot := esi.MarketOrderSnapshot{
		RegionID:   10000002,
		OrderType:  "sell",
		Source:     "region",
		CapturedAt: time.Now().UTC(),
	}
	for i := 0; i < levelCount; i++ {
		snapshot.Orders = append(snapshot.Orders, esi.MarketOrder{
			OrderID: int64(i + 1), TypeID: 35, LocationID: 60003760, SystemID: 30000142,
			Price: 10.0 + float64(i), VolumeRemain: int32(i + 1), IsBuyOrder: false,
		})
	}
	if err := d.RecordMarketOrderSnapshot(snapshot); err != nil {
		t.Fatalf("record snapshot: %v", err)
	}

	snaps, err := d.ListOrderBookSnapshots(OrderBookSnapshotFilter{RegionID: 10000002, OrderType: "sell", Limit: 10})
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("snapshots len=%d, want 1", len(snaps))
	}
	if snaps[0].LevelCount != levelCount {
		t.Fatalf("level_count = %d, want %d", snaps[0].LevelCount, levelCount)
	}

	levels, err := d.GetOrderBookLevels(snaps[0].ID, OrderBookLevelFilter{TypeID: 35, Side: "sell", Limit: levelCount * 2})
	if err != nil {
		t.Fatalf("get levels: %v", err)
	}
	if len(levels) != levelCount {
		t.Fatalf("stored levels = %d, want %d", len(levels), levelCount)
	}
	var totalVolume int64
	for _, level := range levels {
		totalVolume += level.VolumeRemain
	}
	if want := int64(levelCount) * int64(levelCount+1) / 2; totalVolume != want {
		t.Fatalf("total volume = %d, want %d", totalVolume, want)
	}
}

func TestOrderBookRecordingSwitchDefaultsOffAndRoundTrips(t *testing.T) {
	// On is the default: what makes archiving affordable is the interest
	// filter and the retention sweep, not refusing to archive. The switch
	// still has to round-trip so somebody who wants it off can have it off.
	d := openTestDB(t)
	defer d.Close()
	seedOrderBookInterest(t, d, 34, 35, 36, 37, 40, 50, 51, 52, 53, 54, 60)

	if !d.OrderBookRecordingEnabled() {
		t.Fatal("recording defaulted to off")
	}
	if err := d.SetOrderBookRecordingEnabled(false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if d.OrderBookRecordingEnabled() {
		t.Fatal("disable did not stick")
	}
	if err := d.SetOrderBookRecordingEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !d.OrderBookRecordingEnabled() {
		t.Fatal("enable did not stick")
	}
}

func TestListOrderBookReplayBooksFindsRegionWideSnapshotsByLevelType(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	seedOrderBookInterest(t, d, 34, 35, 36, 37, 40, 50, 51, 52, 53, 54, 60)

	capturedAt := time.Now().UTC().Add(-time.Minute)
	if err := d.RecordMarketOrderSnapshot(esi.MarketOrderSnapshot{
		RegionID:   10000002,
		OrderType:  "sell",
		Source:     "region",
		CapturedAt: capturedAt,
		Orders: []esi.MarketOrder{
			{OrderID: 1, TypeID: 35, LocationID: 60003760, SystemID: 30000142, Price: 10.0, VolumeRemain: 10, IsBuyOrder: false},
			{OrderID: 2, TypeID: 36, LocationID: 60003760, SystemID: 30000142, Price: 20.0, VolumeRemain: 10, IsBuyOrder: false},
		},
	}); err != nil {
		t.Fatalf("record region-wide snapshot: %v", err)
	}

	snaps, err := d.ListOrderBookSnapshots(OrderBookSnapshotFilter{TypeID: 35, Limit: 10})
	if err != nil {
		t.Fatalf("list snapshots by level type: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("snapshots len=%d, want 1", len(snaps))
	}
	if snaps[0].TypeID != 0 {
		t.Fatalf("region-wide snapshot type_id=%d, want 0", snaps[0].TypeID)
	}

	books, err := d.ListOrderBookReplayBooks(OrderBookReplayFilter{
		RegionID:       10000002,
		TypeID:         35,
		LocationID:     60003760,
		Side:           "sell",
		FromCapturedAt: capturedAt.Add(-time.Minute),
		ToCapturedAt:   capturedAt.Add(time.Minute),
		Limit:          10,
	})
	if err != nil {
		t.Fatalf("list replay books: %v", err)
	}
	if len(books) != 1 || len(books[0].Levels) != 1 {
		t.Fatalf("books = %#v, want one book with one level", books)
	}
	if books[0].Levels[0].TypeID != 35 || books[0].Levels[0].Price != 10 {
		t.Fatalf("level = %#v, want type 35 price 10", books[0].Levels[0])
	}
}

func TestOrderBookStatsAndCleanup(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	seedOrderBookInterest(t, d, 34, 35, 36, 37, 40, 50, 51, 52, 53, 54, 60)

	now := time.Now().UTC()
	if err := d.RecordMarketOrderSnapshot(esi.MarketOrderSnapshot{
		RegionID:   10000002,
		OrderType:  "sell",
		Source:     "region",
		CapturedAt: now.AddDate(0, 0, -120),
		Orders: []esi.MarketOrder{
			{OrderID: 1, TypeID: 34, LocationID: 60003760, SystemID: 30000142, Price: 5.0, VolumeRemain: 100, IsBuyOrder: false},
		},
	}); err != nil {
		t.Fatalf("record old snapshot: %v", err)
	}
	if err := d.RecordMarketOrderSnapshot(esi.MarketOrderSnapshot{
		RegionID:   10000002,
		OrderType:  "buy",
		Source:     "region",
		CapturedAt: now.Add(-time.Hour),
		Orders: []esi.MarketOrder{
			{OrderID: 2, TypeID: 35, LocationID: 60008494, SystemID: 30000144, Price: 8.0, VolumeRemain: 50, IsBuyOrder: true},
		},
	}); err != nil {
		t.Fatalf("record new snapshot: %v", err)
	}

	stats, err := d.GetOrderBookStats(5)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.SnapshotCount != 2 || stats.LevelCount != 2 || stats.UniqueTypeCount != 2 || stats.UniqueLocationCount != 2 {
		t.Fatalf("stats counts = %#v", stats)
	}
	if stats.TotalVolumeRemain != 150 || stats.ApproxBytes <= 0 || len(stats.TopTypes) != 2 || len(stats.TopLocations) != 2 {
		t.Fatalf("stats detail = %#v", stats)
	}

	if err := d.RecordMarketOrderSnapshot(esi.MarketOrderSnapshot{
		RegionID:   10000002,
		OrderType:  "sell",
		Source:     "region",
		CapturedAt: now.Add(-30 * time.Minute),
		Orders: []esi.MarketOrder{
			{OrderID: 3, TypeID: 36, LocationID: 60008494, SystemID: 30000144, Price: 9.0, VolumeRemain: 25, IsBuyOrder: false},
		},
	}); err != nil {
		t.Fatalf("record cache invalidating snapshot: %v", err)
	}
	stats, err = d.GetOrderBookStats(5)
	if err != nil {
		t.Fatalf("stats after cache invalidating snapshot: %v", err)
	}
	if stats.SnapshotCount != 3 || stats.LevelCount != 3 || stats.TotalVolumeRemain != 175 {
		t.Fatalf("stats after cache invalidating snapshot = %#v", stats)
	}

	preview, err := d.CleanupOrderBookSnapshots(30, true, false)
	if err != nil {
		t.Fatalf("preview cleanup: %v", err)
	}
	if !preview.DryRun || preview.SnapshotsDeleted != 1 || preview.LevelsDeleted != 1 {
		t.Fatalf("preview = %#v, want one old snapshot and level", preview)
	}
	snaps, err := d.ListOrderBookSnapshots(OrderBookSnapshotFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list after preview: %v", err)
	}
	if len(snaps) != 3 {
		t.Fatalf("dry-run deleted snapshots, got %d", len(snaps))
	}

	removed, err := d.CleanupOrderBookSnapshots(30, false, false)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if removed.DryRun || removed.SnapshotsDeleted != 1 || removed.LevelsDeleted != 1 {
		t.Fatalf("cleanup = %#v, want one removed snapshot and level", removed)
	}
	stats, err = d.GetOrderBookStats(5)
	if err != nil {
		t.Fatalf("stats after cleanup: %v", err)
	}
	if stats.SnapshotCount != 2 || stats.LevelCount != 2 || stats.UniqueTypeCount != 2 || stats.TopTypes[0].TypeID != 35 {
		t.Fatalf("stats after cleanup = %#v", stats)
	}
}

func TestCleanupOrderBookSnapshotsBatch(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	seedOrderBookInterest(t, d, 34, 35, 36, 37, 40, 50, 51, 52, 53, 54, 60)

	old := time.Now().UTC().AddDate(0, 0, -60)
	for i := 0; i < 3; i++ {
		if err := d.RecordMarketOrderSnapshot(esi.MarketOrderSnapshot{
			RegionID:   10000002,
			OrderType:  "sell",
			Source:     "region",
			CapturedAt: old.Add(time.Duration(i) * time.Hour),
			Orders: []esi.MarketOrder{
				{OrderID: int64(100 + i), TypeID: int32(35 + i), LocationID: 60008494, SystemID: 30000142, Price: float64(10 + i), VolumeRemain: 10, IsBuyOrder: false},
			},
		}); err != nil {
			t.Fatalf("record old snapshot %d: %v", i, err)
		}
	}
	if err := d.RecordMarketOrderSnapshot(esi.MarketOrderSnapshot{
		RegionID:   10000002,
		OrderType:  "sell",
		Source:     "region",
		CapturedAt: time.Now().UTC(),
		Orders: []esi.MarketOrder{
			{OrderID: 200, TypeID: 40, LocationID: 60008494, SystemID: 30000142, Price: 99, VolumeRemain: 10, IsBuyOrder: false},
		},
	}); err != nil {
		t.Fatalf("record fresh snapshot: %v", err)
	}

	plan, err := d.CleanupOrderBookSnapshotsBatch(30, 2)
	if err != nil {
		t.Fatalf("batch cleanup: %v", err)
	}
	if plan.SnapshotsDeleted != 2 || plan.LevelsDeleted != 2 {
		t.Fatalf("first batch = %#v, want 2 old snapshots and levels", plan)
	}
	stats, err := d.GetOrderBookStats(5)
	if err != nil {
		t.Fatalf("stats after first batch: %v", err)
	}
	if stats.SnapshotCount != 2 || stats.LevelCount != 2 {
		t.Fatalf("stats after first batch = %#v, want 2 remaining", stats)
	}

	plan, err = d.CleanupOrderBookSnapshotsBatch(30, 2)
	if err != nil {
		t.Fatalf("second batch cleanup: %v", err)
	}
	if plan.SnapshotsDeleted != 1 || plan.LevelsDeleted != 1 {
		t.Fatalf("second batch = %#v, want final old snapshot and level", plan)
	}
	stats, err = d.GetOrderBookStats(5)
	if err != nil {
		t.Fatalf("stats after second batch: %v", err)
	}
	if stats.SnapshotCount != 1 || stats.LevelCount != 1 || stats.TopTypes[0].TypeID != 40 {
		t.Fatalf("stats after second batch = %#v, want only fresh snapshot", stats)
	}
}

func TestCleanupOrderBookSnapshotsBatches(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	seedOrderBookInterest(t, d, 34, 35, 36, 37, 40, 50, 51, 52, 53, 54, 60)

	old := time.Now().UTC().AddDate(0, 0, -60)
	for i := 0; i < 5; i++ {
		if err := d.RecordMarketOrderSnapshot(esi.MarketOrderSnapshot{
			RegionID:   10000002,
			OrderType:  "sell",
			Source:     "region",
			CapturedAt: old.Add(time.Duration(i) * time.Hour),
			Orders: []esi.MarketOrder{
				{OrderID: int64(300 + i), TypeID: int32(50 + i), LocationID: 60008494, SystemID: 30000142, Price: float64(10 + i), VolumeRemain: 10, IsBuyOrder: false},
			},
		}); err != nil {
			t.Fatalf("record old snapshot %d: %v", i, err)
		}
	}
	if err := d.RecordMarketOrderSnapshot(esi.MarketOrderSnapshot{
		RegionID:   10000002,
		OrderType:  "sell",
		Source:     "region",
		CapturedAt: time.Now().UTC(),
		Orders: []esi.MarketOrder{
			{OrderID: 400, TypeID: 60, LocationID: 60008494, SystemID: 30000142, Price: 99, VolumeRemain: 10, IsBuyOrder: false},
		},
	}); err != nil {
		t.Fatalf("record fresh snapshot: %v", err)
	}

	plan, err := d.CleanupOrderBookSnapshotsBatches(30, 2, 5*time.Second)
	if err != nil {
		t.Fatalf("cleanup batches: %v", err)
	}
	if plan.SnapshotsDeleted != 5 || plan.LevelsDeleted != 5 {
		t.Fatalf("cleanup batches = %#v, want all 5 old snapshots and levels", plan)
	}
	stats, err := d.GetOrderBookStats(5)
	if err != nil {
		t.Fatalf("stats after cleanup batches: %v", err)
	}
	if stats.SnapshotCount != 1 || stats.LevelCount != 1 || stats.TopTypes[0].TypeID != 60 {
		t.Fatalf("stats after cleanup batches = %#v, want only fresh snapshot", stats)
	}
}

func TestRecordMarketOrderSnapshotArchivesOnlyInterestingTypes(t *testing.T) {
	// A region book covers ~19,000 types; a trader replays a few hundred. On a
	// real 19.5M-level archive the owner's traded types were 4.3% of the rows,
	// so this filter is the difference between a multi-gigabyte table and a
	// few hundred megabytes. It has to drop the rest before they are staged,
	// and it must not distort the metadata of what it does keep.
	d := openTestDB(t)
	defer d.Close()
	seedOrderBookInterest(t, d, 34)

	if err := d.RecordMarketOrderSnapshot(esi.MarketOrderSnapshot{
		RegionID:   10000002,
		OrderType:  "sell",
		Source:     "region",
		CapturedAt: time.Now().UTC(),
		Orders: []esi.MarketOrder{
			{OrderID: 1, TypeID: 34, LocationID: 60003760, SystemID: 30000142, Price: 5.0, VolumeRemain: 100, IsBuyOrder: false},
			{OrderID: 2, TypeID: 34, LocationID: 60003760, SystemID: 30000142, Price: 5.0, VolumeRemain: 50, IsBuyOrder: false},
			{OrderID: 3, TypeID: 999, LocationID: 60003760, SystemID: 30000142, Price: 7.0, VolumeRemain: 10, IsBuyOrder: false},
			{OrderID: 4, TypeID: 1000, LocationID: 60011866, SystemID: 30002659, Price: 8.0, VolumeRemain: 10, IsBuyOrder: true},
		},
	}); err != nil {
		t.Fatalf("record snapshot: %v", err)
	}

	snaps, err := d.ListOrderBookSnapshots(OrderBookSnapshotFilter{RegionID: 10000002, Limit: 10})
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("snapshots len=%d, want 1", len(snaps))
	}
	// Metadata describes the book we kept, not the one we saw: two orders, one
	// level, one type, one location. Reporting the raw four would make the
	// Backtest tab's coverage numbers describe data that is not there.
	if snaps[0].OrderCount != 2 || snaps[0].LevelCount != 1 {
		t.Fatalf("counts = orders %d levels %d, want 2/1", snaps[0].OrderCount, snaps[0].LevelCount)
	}
	if snaps[0].UniqueTypeCount != 1 || snaps[0].UniqueLocationCount != 1 {
		t.Fatalf("unique = types %d locations %d, want 1/1", snaps[0].UniqueTypeCount, snaps[0].UniqueLocationCount)
	}

	levels, err := d.GetOrderBookLevels(snaps[0].ID, OrderBookLevelFilter{Limit: 100})
	if err != nil {
		t.Fatalf("get levels: %v", err)
	}
	if len(levels) != 1 {
		t.Fatalf("levels len=%d, want only the watched type", len(levels))
	}
	if levels[0].TypeID != 34 || levels[0].VolumeRemain != 150 {
		t.Fatalf("level = %+v, want type 34 with the two orders aggregated", levels[0])
	}
}

func TestRecordMarketOrderSnapshotArchivesNothingWithoutInterest(t *testing.T) {
	// A fresh install has no transactions, no watchlist and no stockpile. The
	// filter must read that as "nothing worth keeping" rather than falling
	// back to archiving the whole region — that fallback would hand the one
	// user with no use for the data the full multi-gigabyte behaviour.
	d := openTestDB(t)
	defer d.Close()
	InvalidateOrderBookInterest()
	t.Cleanup(InvalidateOrderBookInterest)

	if len(d.OrderBookInterestTypes()) != 0 {
		t.Fatal("a fresh DB reported types worth archiving")
	}
	if err := d.RecordMarketOrderSnapshot(esi.MarketOrderSnapshot{
		RegionID:   10000002,
		OrderType:  "sell",
		Source:     "region",
		CapturedAt: time.Now().UTC(),
		Orders: []esi.MarketOrder{
			{OrderID: 1, TypeID: 34, LocationID: 60003760, SystemID: 30000142, Price: 5.0, VolumeRemain: 100},
		},
	}); err != nil {
		t.Fatalf("record snapshot: %v", err)
	}
	snaps, err := d.ListOrderBookSnapshots(OrderBookSnapshotFilter{RegionID: 10000002, Limit: 10})
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snaps) != 0 {
		t.Fatalf("archived %d snapshots with an empty interest set", len(snaps))
	}
}

func TestOrderBookInterestTypesUnionsEverySenseOfDealingInIt(t *testing.T) {
	// Traded, watched, stocked, planned and paper-traded are all "I deal in
	// this". Missing one silently costs the user replay history for a type
	// they would reasonably expect to be covered.
	d := openTestDB(t)
	defer d.Close()
	InvalidateOrderBookInterest()
	t.Cleanup(InvalidateOrderBookInterest)

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := d.sql.Exec(
		"INSERT INTO watchlist (user_id, type_id, type_name, added_at) VALUES (?, ?, ?, ?)",
		DefaultUserID, 34, "Tritanium", now,
	); err != nil {
		t.Fatalf("seed watchlist: %v", err)
	}
	if _, err := d.sql.Exec(`
		INSERT INTO wallet_transactions_archive
			(user_id, character_id, transaction_id, date, type_id, location_id,
			 unit_price, quantity, is_buy, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, DefaultUserID, 90000001, 5001, now, 35, 60003760, 10.0, 100, 1, now, now); err != nil {
		t.Fatalf("seed transaction: %v", err)
	}
	InvalidateOrderBookInterest()

	types := d.OrderBookInterestTypes()
	if !types[34] {
		t.Error("watchlisted type is not archived")
	}
	if !types[35] {
		t.Error("traded type is not archived")
	}
	if types[36] {
		t.Error("an untouched type is archived")
	}
}
