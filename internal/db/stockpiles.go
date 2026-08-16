package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"eve-flipper/internal/config"
)

// ErrStockpileNotFound is returned when a stockpile lookup misses.
var ErrStockpileNotFound = errors.New("stockpile not found")

// ErrStockpileNameConflict is returned when a create/rename collides with an
// existing stockpile for the same user.
var ErrStockpileNameConflict = errors.New("stockpile name already in use")

func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func normalizeStockpileSource(source string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(source))
	switch s {
	case config.StockpileSourceCharacter, config.StockpileSourceCorporation:
		return s, nil
	default:
		return "", fmt.Errorf("invalid stockpile source %q", source)
	}
}

// ListStockpilesForUser returns all stockpile headers for a user (no items).
func (d *DB) ListStockpilesForUser(userID string) ([]config.Stockpile, error) {
	userID = normalizeUserID(userID)

	rows, err := d.sql.Query(`
		SELECT id, name, source,
		       COALESCE(source_character_id, 0),
		       COALESCE(source_corporation_id, 0),
		       station_id, created_at, updated_at
		  FROM stockpiles
		 WHERE user_id = ?
		 ORDER BY name COLLATE NOCASE ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []config.Stockpile{}
	for rows.Next() {
		var sp config.Stockpile
		if err := rows.Scan(
			&sp.ID, &sp.Name, &sp.Source,
			&sp.SourceCharacterID, &sp.SourceCorporationID,
			&sp.StationID, &sp.CreatedAt, &sp.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// GetStockpileForUser returns a single stockpile with its items.
func (d *DB) GetStockpileForUser(userID string, id int64) (*config.Stockpile, error) {
	userID = normalizeUserID(userID)

	var sp config.Stockpile
	err := d.sql.QueryRow(`
		SELECT id, name, source,
		       COALESCE(source_character_id, 0),
		       COALESCE(source_corporation_id, 0),
		       station_id, created_at, updated_at
		  FROM stockpiles
		 WHERE user_id = ? AND id = ?
	`, userID, id).Scan(
		&sp.ID, &sp.Name, &sp.Source,
		&sp.SourceCharacterID, &sp.SourceCorporationID,
		&sp.StationID, &sp.CreatedAt, &sp.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrStockpileNotFound
	}
	if err != nil {
		return nil, err
	}

	itemRows, err := d.sql.Query(`
		SELECT type_id, type_name, threshold_qty, created_at
		  FROM stockpile_items
		 WHERE stockpile_id = ?
		 ORDER BY type_name COLLATE NOCASE ASC
	`, id)
	if err != nil {
		return nil, err
	}
	defer itemRows.Close()

	items := []config.StockpileItem{}
	for itemRows.Next() {
		var it config.StockpileItem
		if err := itemRows.Scan(&it.TypeID, &it.TypeName, &it.ThresholdQty, &it.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	if err := itemRows.Err(); err != nil {
		return nil, err
	}
	sp.Items = items
	return &sp, nil
}

// CreateStockpileForUser inserts a new stockpile header. Items are added via
// UpsertStockpileItemsForUser after creation.
func (d *DB) CreateStockpileForUser(userID string, sp config.Stockpile) (*config.Stockpile, error) {
	userID = normalizeUserID(userID)
	name := strings.TrimSpace(sp.Name)
	if name == "" {
		return nil, errors.New("stockpile name is required")
	}
	source, err := normalizeStockpileSource(sp.Source)
	if err != nil {
		return nil, err
	}
	if sp.StationID <= 0 {
		return nil, errors.New("stockpile station_id is required")
	}
	if source == config.StockpileSourceCharacter && sp.SourceCharacterID <= 0 {
		return nil, errors.New("source_character_id is required for character-source stockpiles")
	}
	if source == config.StockpileSourceCorporation && sp.SourceCorporationID <= 0 {
		return nil, errors.New("source_corporation_id is required for corporation-source stockpiles")
	}

	now := nowUTC()
	var charID, corpID any
	if sp.SourceCharacterID > 0 {
		charID = sp.SourceCharacterID
	}
	if sp.SourceCorporationID > 0 {
		corpID = sp.SourceCorporationID
	}

	res, err := d.sql.Exec(`
		INSERT INTO stockpiles
		  (user_id, name, source, source_character_id, source_corporation_id, station_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, userID, name, source, charID, corpID, sp.StationID, now, now)
	if err != nil {
		if isUniqueConflict(err) {
			return nil, ErrStockpileNameConflict
		}
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	sp.ID = id
	sp.Name = name
	sp.Source = source
	sp.CreatedAt = now
	sp.UpdatedAt = now
	sp.Items = nil
	return &sp, nil
}

// UpdateStockpileForUser updates the mutable header fields (name, source,
// source ids, station_id). The four zero-valued arguments are ignored so
// callers may PATCH-style update a subset.
func (d *DB) UpdateStockpileForUser(userID string, id int64, patch config.Stockpile) (*config.Stockpile, error) {
	userID = normalizeUserID(userID)
	existing, err := d.GetStockpileForUser(userID, id)
	if err != nil {
		return nil, err
	}

	if patch.Name != "" {
		existing.Name = strings.TrimSpace(patch.Name)
	}
	if patch.Source != "" {
		src, err := normalizeStockpileSource(patch.Source)
		if err != nil {
			return nil, err
		}
		existing.Source = src
	}
	if patch.StationID > 0 {
		existing.StationID = patch.StationID
	}
	if patch.SourceCharacterID > 0 {
		existing.SourceCharacterID = patch.SourceCharacterID
	}
	if patch.SourceCorporationID > 0 {
		existing.SourceCorporationID = patch.SourceCorporationID
	}
	if existing.Source == config.StockpileSourceCharacter && existing.SourceCharacterID <= 0 {
		return nil, errors.New("source_character_id is required for character-source stockpiles")
	}
	if existing.Source == config.StockpileSourceCorporation && existing.SourceCorporationID <= 0 {
		return nil, errors.New("source_corporation_id is required for corporation-source stockpiles")
	}

	var charID, corpID any
	if existing.SourceCharacterID > 0 {
		charID = existing.SourceCharacterID
	}
	if existing.SourceCorporationID > 0 {
		corpID = existing.SourceCorporationID
	}
	now := nowUTC()
	_, err = d.sql.Exec(`
		UPDATE stockpiles
		   SET name = ?, source = ?, source_character_id = ?, source_corporation_id = ?, station_id = ?, updated_at = ?
		 WHERE user_id = ? AND id = ?
	`, existing.Name, existing.Source, charID, corpID, existing.StationID, now, userID, id)
	if err != nil {
		if isUniqueConflict(err) {
			return nil, ErrStockpileNameConflict
		}
		return nil, err
	}
	existing.UpdatedAt = now
	return existing, nil
}

// UpsertStockpileItemsForUser merges the given items into the stockpile's
// item list, replacing threshold_qty on typeID conflict.
func (d *DB) UpsertStockpileItemsForUser(userID string, id int64, items []config.StockpileItem) error {
	userID = normalizeUserID(userID)
	if _, err := d.GetStockpileForUser(userID, id); err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := nowUTC()
	stmt, err := tx.Prepare(`
		INSERT INTO stockpile_items (stockpile_id, type_id, type_name, threshold_qty, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(stockpile_id, type_id) DO UPDATE SET
		  type_name = excluded.type_name,
		  threshold_qty = excluded.threshold_qty
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, it := range items {
		if it.TypeID <= 0 || it.ThresholdQty < 0 {
			continue
		}
		if _, err := stmt.Exec(id, it.TypeID, it.TypeName, it.ThresholdQty, now); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE stockpiles SET updated_at = ? WHERE id = ?`, now, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ReplaceStockpileItemsForUser wipes and replaces the entire item list in one
// transaction. Kept alongside Upsert for the "replace all" UX path.
func (d *DB) ReplaceStockpileItemsForUser(userID string, id int64, items []config.StockpileItem) error {
	userID = normalizeUserID(userID)
	if _, err := d.GetStockpileForUser(userID, id); err != nil {
		return err
	}
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM stockpile_items WHERE stockpile_id = ?`, id); err != nil {
		return err
	}
	now := nowUTC()
	if len(items) > 0 {
		stmt, err := tx.Prepare(`
			INSERT INTO stockpile_items (stockpile_id, type_id, type_name, threshold_qty, created_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(stockpile_id, type_id) DO UPDATE SET
			  type_name = excluded.type_name,
			  threshold_qty = excluded.threshold_qty
		`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, it := range items {
			if it.TypeID <= 0 || it.ThresholdQty < 0 {
				continue
			}
			if _, err := stmt.Exec(id, it.TypeID, it.TypeName, it.ThresholdQty, now); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`UPDATE stockpiles SET updated_at = ? WHERE id = ?`, now, id); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteStockpileItemForUser removes one row.
func (d *DB) DeleteStockpileItemForUser(userID string, id int64, typeID int32) error {
	userID = normalizeUserID(userID)
	if _, err := d.GetStockpileForUser(userID, id); err != nil {
		return err
	}
	_, err := d.sql.Exec(`DELETE FROM stockpile_items WHERE stockpile_id = ? AND type_id = ?`, id, typeID)
	if err != nil {
		return err
	}
	_, err = d.sql.Exec(`UPDATE stockpiles SET updated_at = ? WHERE id = ?`, nowUTC(), id)
	return err
}

// DeleteStockpileForUser removes a stockpile and its items.
// Items are deleted explicitly (not relying on ON DELETE CASCADE) so behavior
// is identical whether or not PRAGMA foreign_keys is enabled.
func (d *DB) DeleteStockpileForUser(userID string, id int64) error {
	userID = normalizeUserID(userID)
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM stockpile_items WHERE stockpile_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM stockpiles WHERE user_id = ? AND id = ?`, userID, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrStockpileNotFound
	}
	return tx.Commit()
}

func isUniqueConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") && strings.Contains(msg, "constraint")
}
