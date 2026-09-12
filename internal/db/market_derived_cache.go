package db

import (
	"time"
)

// market_derived_cache.go — a year of prices, kept as the numbers that get
// used.
//
// The raw history cache is capped at 90 days on purpose: the blueprint scanner
// holds every scanned type's series in memory simultaneously, so series length
// multiplies by type count (see marketHistoryRetentionDays). Two questions need
// a longer view than that:
//
//   - Where does today's price sit in this item's own year? (percentiles)
//   - Is today's price a dip that has historically recovered, or a decline?
//     (a 180-day trend fit)
//
// Both are answered from one ESI fetch of the full ~390-day series, which is
// reduced immediately and discarded. Only the reductions are stored — a few
// hundred bytes against roughly 32 KB of rows — so neither question costs
// memory that scales with how many items are examined.

// How long a stored summary is trusted. ESI's own market history rolls once a
// day, and neither a 365-day percentile nor a 180-day trend moves meaningfully
// inside one, so a shorter TTL would re-fetch a year to learn the same answer.
const marketDerivedTTL = 24 * time.Hour

// GetMarketDerived returns the cached summary JSON for a (region, type), and
// false when there is none or it has gone stale.
func (d *DB) GetMarketDerived(regionID, typeID int32) (string, bool) {
	if d == nil || d.sql == nil {
		return "", false
	}
	var payload, computedAt string
	err := d.sql.QueryRow(
		`SELECT payload_json, computed_at FROM market_derived_cache
		 WHERE region_id = ? AND type_id = ?`, regionID, typeID,
	).Scan(&payload, &computedAt)
	if err != nil {
		return "", false
	}
	at, parseErr := time.Parse(time.RFC3339, computedAt)
	if parseErr != nil || time.Since(at) > marketDerivedTTL {
		return "", false
	}
	return payload, true
}

// SetMarketDerived stores a computed summary.
//
// Refusals are stored too, and deliberately: "this item has too little traded
// history" costs a full ESI round trip to discover, and it is just as true
// tomorrow as today. Not caching it would mean re-fetching a year of prices for
// every thin item on every market sweep, which is the traffic this table exists
// to avoid.
func (d *DB) SetMarketDerived(regionID, typeID int32, payloadJSON string) error {
	if d == nil || d.sql == nil || payloadJSON == "" {
		return nil
	}
	_, err := d.sql.Exec(`
		INSERT INTO market_derived_cache (region_id, type_id, computed_at, payload_json)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(region_id, type_id) DO UPDATE SET
			computed_at  = excluded.computed_at,
			payload_json = excluded.payload_json`,
		regionID, typeID, time.Now().UTC().Format(time.RFC3339), payloadJSON)
	return err
}

// CountFreshMarketDerived reports how many summaries for a region are still
// inside the TTL. The accumulate sweep uses it to tell the user up front
// whether a run will be mostly cache reads or mostly ESI round trips, which is
// the difference between seconds and many minutes.
func (d *DB) CountFreshMarketDerived(regionID int32) int {
	if d == nil || d.sql == nil {
		return 0
	}
	cutoff := time.Now().UTC().Add(-marketDerivedTTL).Format(time.RFC3339)
	var n int
	if err := d.sql.QueryRow(
		`SELECT COUNT(*) FROM market_derived_cache WHERE region_id = ? AND computed_at >= ?`,
		regionID, cutoff,
	).Scan(&n); err != nil {
		return 0
	}
	return n
}

// CleanupMarketDerived drops summaries not recomputed in a fortnight — types
// looked at once and never again. Cheap to rebuild on demand, and it stops a
// market-wide sweep leaving a permanent row per type in EVE.
func (d *DB) CleanupMarketDerived() {
	if d == nil || d.sql == nil {
		return
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -14).Format(time.RFC3339)
	_, _ = d.sql.Exec(`DELETE FROM market_derived_cache WHERE computed_at < ?`, cutoff)
}
