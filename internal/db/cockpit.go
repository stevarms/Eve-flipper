package db

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode"
)

type CockpitLoadout struct {
	UserID      string `json:"user_id"`
	LoadoutID   string `json:"loadout_id"`
	Name        string `json:"name"`
	PayloadJSON string `json:"payload_json"`
	Active      bool   `json:"active"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func cleanCockpitLoadoutID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) > 80 {
		id = id[:80]
	}
	var b strings.Builder
	for _, r := range id {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func newCockpitLoadoutID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return "loadout_" + hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("loadout_%d", time.Now().UTC().UnixNano())
}

func cleanCockpitLoadoutName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Default cockpit"
	}
	runes := []rune(name)
	if len(runes) > 80 {
		name = string(runes[:80])
	}
	return name
}

func scanCockpitLoadout(scanner interface {
	Scan(dest ...interface{}) error
}) (CockpitLoadout, error) {
	var row CockpitLoadout
	var activeInt int
	err := scanner.Scan(
		&row.UserID,
		&row.LoadoutID,
		&row.Name,
		&row.PayloadJSON,
		&activeInt,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	row.Active = activeInt != 0
	return row, err
}

func (d *DB) LoadCockpitPreferencesForUser(userID string) (payloadJSON string, updatedAt string, ok bool, err error) {
	userID = normalizeUserID(userID)
	active, ok, err := d.ActiveCockpitLoadoutForUser(userID)
	if err != nil {
		return "", "", false, err
	}
	if ok {
		return active.PayloadJSON, active.UpdatedAt, true, nil
	}

	err = d.sql.QueryRow(`
		SELECT payload_json, updated_at
		FROM cockpit_preferences
		WHERE user_id = ?
		LIMIT 1
	`, userID).Scan(&payloadJSON, &updatedAt)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return payloadJSON, updatedAt, true, nil
}

func (d *DB) SaveCockpitPreferencesForUser(userID string, payloadJSON string) (string, error) {
	row, err := d.SaveActiveCockpitLoadoutForUser(userID, "Default cockpit", payloadJSON)
	if err != nil {
		return "", err
	}
	return row.UpdatedAt, nil
}

func (d *DB) ListCockpitLoadoutsForUser(userID string) ([]CockpitLoadout, error) {
	userID = normalizeUserID(userID)
	rows, err := d.sql.Query(`
		SELECT user_id, loadout_id, name, payload_json, is_active, created_at, updated_at
		FROM cockpit_loadouts
		WHERE user_id = ?
		ORDER BY is_active DESC, name COLLATE NOCASE ASC, updated_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []CockpitLoadout{}
	for rows.Next() {
		row, err := scanCockpitLoadout(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *DB) ActiveCockpitLoadoutForUser(userID string) (CockpitLoadout, bool, error) {
	userID = normalizeUserID(userID)
	row, err := scanCockpitLoadout(d.sql.QueryRow(`
		SELECT user_id, loadout_id, name, payload_json, is_active, created_at, updated_at
		FROM cockpit_loadouts
		WHERE user_id = ? AND is_active = 1
		LIMIT 1
	`, userID))
	if err == sql.ErrNoRows {
		row, err = scanCockpitLoadout(d.sql.QueryRow(`
			SELECT user_id, loadout_id, name, payload_json, is_active, created_at, updated_at
			FROM cockpit_loadouts
			WHERE user_id = ?
			ORDER BY updated_at DESC
			LIMIT 1
		`, userID))
		if err == sql.ErrNoRows {
			return CockpitLoadout{}, false, nil
		}
		if err != nil {
			return CockpitLoadout{}, false, err
		}
		return row, true, nil
	}
	if err != nil {
		return CockpitLoadout{}, false, err
	}
	return row, true, nil
}

func (d *DB) SaveActiveCockpitLoadoutForUser(userID, name, payloadJSON string) (CockpitLoadout, error) {
	userID = normalizeUserID(userID)
	active, ok, err := d.ActiveCockpitLoadoutForUser(userID)
	if err != nil {
		return CockpitLoadout{}, err
	}
	if ok {
		return d.UpsertCockpitLoadoutForUser(userID, active.LoadoutID, name, payloadJSON, true)
	}
	return d.UpsertCockpitLoadoutForUser(userID, "default", name, payloadJSON, true)
}

func (d *DB) UpsertCockpitLoadoutForUser(userID, loadoutID, name, payloadJSON string, activate bool) (CockpitLoadout, error) {
	userID = normalizeUserID(userID)
	loadoutID = cleanCockpitLoadoutID(loadoutID)
	if loadoutID == "" {
		loadoutID = newCockpitLoadoutID()
	}
	name = cleanCockpitLoadoutName(name)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	tx, err := d.sql.Begin()
	if err != nil {
		return CockpitLoadout{}, err
	}
	defer tx.Rollback()

	var existingActive int
	var createdAt string
	err = tx.QueryRow(`
		SELECT is_active, created_at
		FROM cockpit_loadouts
		WHERE user_id = ? AND loadout_id = ?
	`, userID, loadoutID).Scan(&existingActive, &createdAt)
	if err != nil && err != sql.ErrNoRows {
		return CockpitLoadout{}, err
	}

	if err == sql.ErrNoRows {
		var count int
		if countErr := tx.QueryRow(`SELECT COUNT(*) FROM cockpit_loadouts WHERE user_id = ?`, userID).Scan(&count); countErr != nil {
			return CockpitLoadout{}, countErr
		}
		createdAt = now
		if count == 0 {
			activate = true
		}
	} else if existingActive != 0 {
		activate = true
	}

	if activate {
		if _, err := tx.Exec(`UPDATE cockpit_loadouts SET is_active = 0 WHERE user_id = ?`, userID); err != nil {
			return CockpitLoadout{}, err
		}
	}

	activeInt := existingActive
	if activate {
		activeInt = 1
	}
	if err == sql.ErrNoRows {
		_, err = tx.Exec(`
			INSERT INTO cockpit_loadouts (user_id, loadout_id, name, payload_json, is_active, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, userID, loadoutID, name, payloadJSON, activeInt, createdAt, now)
	} else {
		_, err = tx.Exec(`
			UPDATE cockpit_loadouts
			SET name = ?, payload_json = ?, is_active = ?, updated_at = ?
			WHERE user_id = ? AND loadout_id = ?
		`, name, payloadJSON, activeInt, now, userID, loadoutID)
	}
	if err != nil {
		return CockpitLoadout{}, err
	}
	if _, err := tx.Exec(`
		INSERT INTO cockpit_preferences (user_id, payload_json, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			payload_json = excluded.payload_json,
			updated_at = excluded.updated_at
	`, userID, payloadJSON, now); err != nil {
		return CockpitLoadout{}, err
	}
	if err := tx.Commit(); err != nil {
		return CockpitLoadout{}, err
	}
	return d.GetCockpitLoadoutForUser(userID, loadoutID)
}

func (d *DB) GetCockpitLoadoutForUser(userID, loadoutID string) (CockpitLoadout, error) {
	userID = normalizeUserID(userID)
	loadoutID = cleanCockpitLoadoutID(loadoutID)
	row, err := scanCockpitLoadout(d.sql.QueryRow(`
		SELECT user_id, loadout_id, name, payload_json, is_active, created_at, updated_at
		FROM cockpit_loadouts
		WHERE user_id = ? AND loadout_id = ?
	`, userID, loadoutID))
	if err != nil {
		return CockpitLoadout{}, err
	}
	return row, nil
}

func (d *DB) ActivateCockpitLoadoutForUser(userID, loadoutID string) (CockpitLoadout, error) {
	userID = normalizeUserID(userID)
	loadoutID = cleanCockpitLoadoutID(loadoutID)
	if loadoutID == "" {
		return CockpitLoadout{}, fmt.Errorf("loadout id is required")
	}
	tx, err := d.sql.Begin()
	if err != nil {
		return CockpitLoadout{}, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`
		SELECT COUNT(*) FROM cockpit_loadouts WHERE user_id = ? AND loadout_id = ?
	`, userID, loadoutID).Scan(&exists); err != nil {
		return CockpitLoadout{}, err
	}
	if exists == 0 {
		return CockpitLoadout{}, sql.ErrNoRows
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`UPDATE cockpit_loadouts SET is_active = 0 WHERE user_id = ?`, userID); err != nil {
		return CockpitLoadout{}, err
	}
	if _, err := tx.Exec(`
		UPDATE cockpit_loadouts
		SET is_active = 1, updated_at = ?
		WHERE user_id = ? AND loadout_id = ?
	`, now, userID, loadoutID); err != nil {
		return CockpitLoadout{}, err
	}
	if err := tx.Commit(); err != nil {
		return CockpitLoadout{}, err
	}
	return d.GetCockpitLoadoutForUser(userID, loadoutID)
}

func (d *DB) DeleteCockpitLoadoutForUser(userID, loadoutID string) ([]CockpitLoadout, error) {
	userID = normalizeUserID(userID)
	loadoutID = cleanCockpitLoadoutID(loadoutID)
	if loadoutID == "" {
		return nil, fmt.Errorf("loadout id is required")
	}
	tx, err := d.sql.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM cockpit_loadouts WHERE user_id = ?`, userID).Scan(&count); err != nil {
		return nil, err
	}
	if count <= 1 {
		return nil, fmt.Errorf("cannot delete the last cockpit loadout")
	}
	var wasActive int
	if err := tx.QueryRow(`
		SELECT is_active FROM cockpit_loadouts WHERE user_id = ? AND loadout_id = ?
	`, userID, loadoutID).Scan(&wasActive); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
		DELETE FROM cockpit_loadouts WHERE user_id = ? AND loadout_id = ?
	`, userID, loadoutID); err != nil {
		return nil, err
	}
	if wasActive != 0 {
		var fallbackID string
		if err := tx.QueryRow(`
			SELECT loadout_id
			FROM cockpit_loadouts
			WHERE user_id = ?
			ORDER BY updated_at DESC
			LIMIT 1
		`, userID).Scan(&fallbackID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`
			UPDATE cockpit_loadouts SET is_active = 1 WHERE user_id = ? AND loadout_id = ?
		`, userID, fallbackID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return d.ListCockpitLoadoutsForUser(userID)
}
