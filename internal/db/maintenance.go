package db

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultOrderBookSnapshotRetentionDays = 14
	DefaultOrderBookCleanupBatchSnapshots = 100
	// Level rows removed per transaction. Snapshots vary by four orders of
	// magnitude in level count, so this -- not the snapshot count -- is what
	// actually bounds a cleanup transaction.
	DefaultOrderBookCleanupBatchLevels = 20000
	DefaultOrderBookCleanupMaxSeconds  = 20
	DefaultScanHistoryRetentionDays    = 30
	DefaultCacheCleanupInterval        = 6 * time.Hour
)

func (d *DB) CleanupStartupCachesAsync(delay time.Duration) {
	if d == nil || d.sql == nil {
		return
	}
	go func() {
		if delay > 0 {
			time.Sleep(delay)
		}
		for {
			if err := d.sql.Ping(); err != nil {
				log.Printf("[DB] CleanupStartupCaches: skipped, database unavailable: %v", err)
			} else {
				d.CleanupStartupCaches()
			}
			time.Sleep(DefaultCacheCleanupInterval)
		}
	}()
}

// CleanupStartupCaches bounds the largest local cache tables on startup and
// during periodic maintenance for long-running instances.
// It avoids VACUUM because compacting multi-GB SQLite files can block the app
// for a long time; manual cleanup can still request VACUUM from the UI/API.
func (d *DB) CleanupStartupCaches() {
	if d == nil || d.sql == nil {
		return
	}

	d.CleanupOldHistory()

	orderbookDays := retentionDaysFromEnv("EVE_FLIPPER_ORDERBOOK_RETENTION_DAYS", DefaultOrderBookSnapshotRetentionDays)
	if orderbookDays > 0 {
		batchSize := retentionDaysFromEnv("EVE_FLIPPER_ORDERBOOK_CLEANUP_BATCH_SNAPSHOTS", DefaultOrderBookCleanupBatchSnapshots)
		maxDuration := durationSecondsFromEnv("EVE_FLIPPER_ORDERBOOK_CLEANUP_MAX_SECONDS", DefaultOrderBookCleanupMaxSeconds)
		plan, err := d.CleanupOrderBookSnapshotsBatches(orderbookDays, batchSize, maxDuration)
		if err != nil {
			log.Printf("[DB] CleanupStartupCaches: orderbook cleanup error: %v", err)
		} else if plan.SnapshotsDeleted > 0 || plan.LevelsDeleted > 0 {
			log.Printf("[DB] CleanupStartupCaches: kept %d days of orderbook snapshots, removed %d snapshots and %d levels", orderbookDays, plan.SnapshotsDeleted, plan.LevelsDeleted)
		}
	}

	scanDays := retentionDaysFromEnv("EVE_FLIPPER_SCAN_HISTORY_RETENTION_DAYS", DefaultScanHistoryRetentionDays)
	if scanDays > 0 {
		removed, err := d.ClearHistory(scanDays)
		if err != nil {
			log.Printf("[DB] CleanupStartupCaches: scan history cleanup error: %v", err)
		} else if removed > 0 {
			log.Printf("[DB] CleanupStartupCaches: kept %d days of scan history, removed %d scans and result sets", scanDays, removed)
		}
	}

	if _, err := d.sql.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		log.Printf("[DB] CleanupStartupCaches: wal checkpoint error: %v", err)
	}
}

func (d *DB) CleanupOrderBookSnapshotsBatches(keepDays int, batchSize int, maxDuration time.Duration) (OrderBookCleanupPlan, error) {
	if batchSize <= 0 {
		batchSize = DefaultOrderBookCleanupBatchSnapshots
	}
	if maxDuration <= 0 {
		maxDuration = time.Duration(DefaultOrderBookCleanupMaxSeconds) * time.Second
	}
	levelBudget := int64(retentionDaysFromEnv("EVE_FLIPPER_ORDERBOOK_CLEANUP_BATCH_LEVELS", DefaultOrderBookCleanupBatchLevels))
	deadline := time.Now().Add(maxDuration)
	total := OrderBookCleanupPlan{KeepDays: keepDays}
	for {
		plan, err := d.cleanupOrderBookSnapshotsBatch(keepDays, batchSize, levelBudget, deadline)
		if err != nil {
			return total, err
		}
		total.KeepDays = plan.KeepDays
		total.Cutoff = plan.Cutoff
		total.OldestRemaining = plan.OldestRemaining
		total.NewestRemaining = plan.NewestRemaining
		total.SnapshotsDeleted += plan.SnapshotsDeleted
		total.LevelsDeleted += plan.LevelsDeleted
		// A batch that removed no whole snapshot has either run out of expired
		// snapshots or run out of time part-way through one. Either way there is
		// nothing more to do this sweep; the next one resumes where this stopped.
		if plan.SnapshotsDeleted == 0 || time.Now().After(deadline) {
			return total, nil
		}
	}
}

// CleanupOrderBookSnapshotsBatch removes up to maxSnapshots expired snapshots,
// with no time limit. Callers that need to stay responsive should use
// CleanupOrderBookSnapshotsBatches, which bounds the work by wall clock.
func (d *DB) CleanupOrderBookSnapshotsBatch(keepDays int, maxSnapshots int) (OrderBookCleanupPlan, error) {
	return d.cleanupOrderBookSnapshotsBatch(keepDays, maxSnapshots, DefaultOrderBookCleanupBatchLevels, time.Time{})
}

// cleanupOrderBookSnapshotsBatch deletes expired snapshots, removing their
// level rows in chunks of at most maxLevels per transaction and stopping once
// deadline passes. A zero deadline means "no limit".
//
// The level chunking is the whole point. A snapshot of a busy region carries
// on the order of 100k level rows, so a batch sized only in *snapshots* hides
// an unbounded *row* count. Sizing by snapshots alone once turned 100
// snapshots into 11.5M row deletes inside one transaction, which held the
// single SQLite connection for nine minutes -- every authenticated request
// queued behind it and the app looked hung -- and grew the WAL to 1.5GB.
// The old deadline check could not help: it sat between batches, so it was
// first consulted after that one indivisible batch had already finished.
// Chunking by rows keeps each commit small and gives the deadline a seam to
// take effect in.
func (d *DB) cleanupOrderBookSnapshotsBatch(keepDays int, maxSnapshots int, maxLevels int64, deadline time.Time) (OrderBookCleanupPlan, error) {
	if maxSnapshots <= 0 {
		maxSnapshots = DefaultOrderBookCleanupBatchSnapshots
	}
	if maxSnapshots > 200 {
		maxSnapshots = 200
	}
	if maxLevels <= 0 {
		maxLevels = DefaultOrderBookCleanupBatchLevels
	}
	if keepDays <= 0 {
		return OrderBookCleanupPlan{}, fmt.Errorf("keep_days must be positive")
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -keepDays)
	plan := OrderBookCleanupPlan{
		KeepDays: keepDays,
		Cutoff:   utcRFC3339(cutoff),
	}
	if d == nil || d.sql == nil {
		return plan, nil
	}

	rows, err := d.sql.Query(`
		SELECT id
		  FROM orderbook_snapshots
		 WHERE captured_at < ?
		 ORDER BY captured_at ASC
		 LIMIT ?
	`, plan.Cutoff, maxSnapshots)
	if err != nil {
		return plan, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return plan, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return plan, err
	}
	if err := rows.Close(); err != nil {
		return plan, err
	}
	if len(ids) == 0 {
		if err := d.scanOrderBookRemainingRange(&plan); err != nil {
			return plan, err
		}
		return plan, nil
	}

	// One snapshot at a time: a partially drained snapshot is still valid
	// state, so stopping early never leaves levels orphaned from their parent.
	for _, id := range ids {
		levels, complete, err := d.deleteOrderBookSnapshot(id, maxLevels, deadline)
		plan.LevelsDeleted += levels
		if complete {
			plan.SnapshotsDeleted++
		}
		if err != nil {
			return plan, err
		}
		if !complete {
			break
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			break
		}
	}
	if plan.LevelsDeleted > 0 || plan.SnapshotsDeleted > 0 {
		d.invalidateOrderBookStatsCache()
	}
	if err := d.scanOrderBookRemainingRange(&plan); err != nil {
		return plan, err
	}
	return plan, nil
}

// deleteOrderBookSnapshot removes one snapshot's levels in chunks of at most
// maxLevels rows per transaction, then the snapshot row itself. It reports how
// many level rows it removed and whether it got all the way through; a false
// `complete` means the deadline stopped it and the snapshot still exists.
//
// The rowid subselect is how the chunk is bounded: SQLite only honours
// `DELETE ... LIMIT` when built with SQLITE_ENABLE_UPDATE_DELETE_LIMIT, which
// is not something to rely on across drivers.
func (d *DB) deleteOrderBookSnapshot(id int64, maxLevels int64, deadline time.Time) (int64, bool, error) {
	var deleted int64
	for {
		tx, err := d.sql.Begin()
		if err != nil {
			return deleted, false, err
		}
		res, err := tx.Exec(`
			DELETE FROM orderbook_levels
			 WHERE rowid IN (
			       SELECT rowid FROM orderbook_levels WHERE snapshot_id = ? LIMIT ?
			 )
		`, id, maxLevels)
		if err != nil {
			tx.Rollback()
			return deleted, false, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			tx.Rollback()
			return deleted, false, err
		}
		if n > 0 {
			// orderbook_snapshots.level_count is denormalized, and
			// GetOrderBookStats sums it rather than counting level rows. The
			// old code only ever removed whole snapshots, so the counter left
			// with its row and could not drift; a part-drained snapshot has to
			// be decremented here or stats over-report the rows still held.
			if _, err := tx.Exec(`
				UPDATE orderbook_snapshots
				   SET level_count = MAX(level_count - ?, 0)
				 WHERE id = ?
			`, n, id); err != nil {
				tx.Rollback()
				return deleted, false, err
			}
		}
		if n < maxLevels {
			// A short chunk means the LIMIT was not reached, so this snapshot
			// has no levels left. Retire the parent in the same transaction:
			// the two can then never disagree, and the common case of a small
			// snapshot costs one transaction rather than two. Waiting for a
			// separate zero-row chunk would also let a tight deadline drain
			// every level and still leave the parent row standing.
			if _, err := tx.Exec(`DELETE FROM orderbook_snapshots WHERE id = ?`, id); err != nil {
				tx.Rollback()
				return deleted, false, err
			}
			if err := tx.Commit(); err != nil {
				return deleted, false, err
			}
			return deleted + n, true, nil
		}
		if err := tx.Commit(); err != nil {
			return deleted, false, err
		}
		deleted += n
		if !deadline.IsZero() && time.Now().After(deadline) {
			return deleted, false, nil
		}
	}
}

func durationSecondsFromEnv(name string, fallback int) time.Duration {
	seconds := retentionDaysFromEnv(name, fallback)
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func retentionDaysFromEnv(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	days, err := strconv.Atoi(raw)
	if err != nil {
		log.Printf("[DB] invalid %s=%q, using %d", name, raw, fallback)
		return fallback
	}
	if days < 0 {
		log.Printf("[DB] invalid %s=%q, using %d", name, raw, fallback)
		return fallback
	}
	return days
}
