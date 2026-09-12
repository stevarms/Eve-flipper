package db

import (
	"time"
)

// price_percentile_cache.go — a year of prices, kept as the dozen numbers
// that get used.
//
// The raw history cache is capped at 90 days on purpose: the blueprint scanner
// holds every scanned type's series in memory simultaneously, so series length
// multiplies by type count (see marketHistoryRetentionDays). Answering "where
// does today's price sit in this item's year" from that cache would mean
// widening it four-fold and paying for it in the middle of every scan.
//
// Instead the year is looked up from ESI when it is actually needed, reduced
// immediately to the summary, and only the summary is stored — roughly 250
// bytes against roughly 32 KB of rows. The raw series is never persisted and
// goes out of scope straight away.

// How long a stored summary is trusted. ESI's own market history rolls once a
// day, and a percentile taken over 365 days does not move meaningfully inside
// one, so anything shorter would just be re-fetching a year to learn the same
// answer.
const pricePercentileTTL = 24 * time.Hour

// GetPricePercentiles returns the cached summary JSON for a (region, type),
// and false when there is none or it has gone stale.
func (d *DB) GetPricePercentiles(regionID, typeID int32) (string, bool) {
	if d == nil || d.sql == nil {
		return "", false
	}
	var payload, computedAt string
	err := d.sql.QueryRow(
		`SELECT payload_json, computed_at FROM price_percentile_cache
		 WHERE region_id = ? AND type_id = ?`, regionID, typeID,
	).Scan(&payload, &computedAt)
	if err != nil {
		return "", false
	}
	at, parseErr := time.Parse(time.RFC3339, computedAt)
	if parseErr != nil || time.Since(at) > pricePercentileTTL {
		return "", false
	}
	return payload, true
}

// SetPricePercentiles stores a computed summary.
//
// Refusals are stored too, and deliberately: "this item has too little traded
// history" costs a full ESI round trip to discover, and it is just as true
// tomorrow as today. Not caching it would mean re-fetching a year of prices
// for every thin item on every sweep, which is precisely the traffic this
// table exists to avoid.
func (d *DB) SetPricePercentiles(regionID, typeID int32, payloadJSON string) error {
	if d == nil || d.sql == nil || payloadJSON == "" {
		return nil
	}
	_, err := d.sql.Exec(`
		INSERT INTO price_percentile_cache (region_id, type_id, computed_at, payload_json)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(region_id, type_id) DO UPDATE SET
			computed_at  = excluded.computed_at,
			payload_json = excluded.payload_json`,
		regionID, typeID, time.Now().UTC().Format(time.RFC3339), payloadJSON)
	return err
}

// CleanupPricePercentiles drops summaries not recomputed in a fortnight —
// types that were looked at once and never again. Cheap to rebuild on demand,
// and it stops a market-wide sweep leaving a permanent row per type.
func (d *DB) CleanupPricePercentiles() {
	if d == nil || d.sql == nil {
		return
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -14).Format(time.RFC3339)
	_, _ = d.sql.Exec(`DELETE FROM price_percentile_cache WHERE computed_at < ?`, cutoff)
}
