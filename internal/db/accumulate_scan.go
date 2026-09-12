package db

import "time"

// accumulate_scan.go — the last "near a yearly low" sweep, per user and region.
//
// One row holding the whole ranked result, unlike flip_results and the other
// per-row results tables. Nothing queries an individual row: the tab renders
// the ranking and Today shows its top few, both from the same payload. A sweep
// is one verdict at one moment, not a set of records to join against.

// SaveAccumulateScan replaces the stored sweep for a user and region.
func (d *DB) SaveAccumulateScan(userID string, regionID int32, generatedAt, payloadJSON string) error {
	if d == nil || d.sql == nil || userID == "" {
		return nil
	}
	_, err := d.sql.Exec(`
		INSERT INTO accumulate_scan (user_id, region_id, generated_at, payload_json)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, region_id) DO UPDATE SET
			generated_at = excluded.generated_at,
			payload_json = excluded.payload_json`,
		userID, regionID, generatedAt, payloadJSON)
	return err
}

// GetAccumulateScan returns the stored sweep and when it was built.
func (d *DB) GetAccumulateScan(userID string, regionID int32) (payloadJSON, generatedAt string, ok bool) {
	if d == nil || d.sql == nil || userID == "" {
		return "", "", false
	}
	row := d.sql.QueryRow(
		`SELECT payload_json, generated_at FROM accumulate_scan
		 WHERE user_id = ? AND region_id = ?`, userID, regionID)
	if err := row.Scan(&payloadJSON, &generatedAt); err != nil {
		return "", "", false
	}
	return payloadJSON, generatedAt, true
}

// LatestAccumulateScan returns the newest sweep across regions, which is what
// Today wants: it has no region of its own and should surface whatever was last
// swept rather than nothing.
func (d *DB) LatestAccumulateScan(userID string) (payloadJSON, generatedAt string, regionID int32, ok bool) {
	if d == nil || d.sql == nil || userID == "" {
		return "", "", 0, false
	}
	row := d.sql.QueryRow(
		`SELECT payload_json, generated_at, region_id FROM accumulate_scan
		 WHERE user_id = ? ORDER BY generated_at DESC LIMIT 1`, userID)
	if err := row.Scan(&payloadJSON, &generatedAt, &regionID); err != nil {
		return "", "", 0, false
	}
	return payloadJSON, generatedAt, regionID, true
}

// PruneAccumulateScans drops sweeps older than the given age. A stale sweep is
// worse than none: it quotes entry prices that have moved.
func (d *DB) PruneAccumulateScans(maxAge time.Duration) {
	if d == nil || d.sql == nil {
		return
	}
	cutoff := time.Now().UTC().Add(-maxAge).Format(time.RFC3339)
	_, _ = d.sql.Exec(`DELETE FROM accumulate_scan WHERE generated_at < ?`, cutoff)
}
