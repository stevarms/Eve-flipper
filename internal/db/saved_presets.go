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

// saved_presets.go — the scan-parameter presets a user builds up over time on
// top of the built-in ones, plus which preset each tab currently has applied.
// Modeled on cockpit_loadouts.go's loadout half: a named, opaque JSON payload
// per user with one "active" flag. The difference is scope -- a cockpit has
// one layout, so its active flag is per user; a preset is per *tab* (flipper
// and station each remember their own active choice today), so the partial
// unique index carries tab as well as user_id.

type SavedPreset struct {
	UserID      string `json:"user_id"`
	PresetID    string `json:"preset_id"`
	Tab         string `json:"tab"`
	Name        string `json:"name"`
	PayloadJSON string `json:"payload_json"`
	Active      bool   `json:"active"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func cleanSavedPresetID(id string) string {
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

func newSavedPresetID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return "preset_" + hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("preset_%d", time.Now().UTC().UnixNano())
}

func cleanSavedPresetName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Untitled preset"
	}
	runes := []rune(name)
	if len(runes) > 80 {
		name = string(runes[:80])
	}
	return name
}

func cleanSavedPresetTab(tab string) string {
	tab = strings.TrimSpace(tab)
	if len(tab) > 40 {
		tab = tab[:40]
	}
	return tab
}

func scanSavedPreset(scanner interface {
	Scan(dest ...interface{}) error
}) (SavedPreset, error) {
	var row SavedPreset
	var activeInt int
	err := scanner.Scan(
		&row.UserID,
		&row.PresetID,
		&row.Tab,
		&row.Name,
		&row.PayloadJSON,
		&activeInt,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	row.Active = activeInt != 0
	return row, err
}

const savedPresetColumns = `user_id, preset_id, tab, name, payload_json, is_active, created_at, updated_at`

// ListSavedPresetsForUser returns every saved preset for a user, across every
// tab -- the caller (PresetPicker) already filters by tab client-side, the
// same way it filtered the single localStorage array before this moved
// server-side.
func (d *DB) ListSavedPresetsForUser(userID string) ([]SavedPreset, error) {
	userID = normalizeUserID(userID)
	rows, err := d.sql.Query(`
		SELECT `+savedPresetColumns+`
		FROM saved_presets
		WHERE user_id = ?
		ORDER BY tab ASC, updated_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []SavedPreset{}
	for rows.Next() {
		row, err := scanSavedPreset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (d *DB) GetSavedPresetForUser(userID, presetID string) (SavedPreset, error) {
	userID = normalizeUserID(userID)
	presetID = cleanSavedPresetID(presetID)
	return scanSavedPreset(d.sql.QueryRow(`
		SELECT `+savedPresetColumns+`
		FROM saved_presets
		WHERE user_id = ? AND preset_id = ?
	`, userID, presetID))
}

// CreateSavedPresetForUser inserts a new preset. When activate is true (the
// default from the picker's "Save current" action) it clears whichever
// preset that tab had active first, so a tab is never left with two.
func (d *DB) CreateSavedPresetForUser(userID, tab, name, payloadJSON string, activate bool) (SavedPreset, error) {
	userID = normalizeUserID(userID)
	tab = cleanSavedPresetTab(tab)
	name = cleanSavedPresetName(name)
	presetID := newSavedPresetID()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	tx, err := d.sql.Begin()
	if err != nil {
		return SavedPreset{}, err
	}
	defer tx.Rollback()

	if activate {
		if _, err := tx.Exec(`UPDATE saved_presets SET is_active = 0 WHERE user_id = ? AND tab = ?`, userID, tab); err != nil {
			return SavedPreset{}, err
		}
	}
	activeInt := 0
	if activate {
		activeInt = 1
	}
	if _, err := tx.Exec(`
		INSERT INTO saved_presets (user_id, preset_id, tab, name, payload_json, is_active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, userID, presetID, tab, name, payloadJSON, activeInt, now, now); err != nil {
		return SavedPreset{}, err
	}
	if err := tx.Commit(); err != nil {
		return SavedPreset{}, err
	}
	return d.GetSavedPresetForUser(userID, presetID)
}

// UpdateSavedPresetForUser merges onto whatever is stored: a rename (name
// set, payloadJSON nil) never touches the params, and vice versa -- the same
// pointer-patch idiom handleAuthHoldingRuleSave uses, needed because the
// picker's rename and "update active preset" actions are separate buttons
// that each only mean to change the one field they show.
func (d *DB) UpdateSavedPresetForUser(userID, presetID string, name, payloadJSON *string, activate *bool) (SavedPreset, error) {
	userID = normalizeUserID(userID)
	presetID = cleanSavedPresetID(presetID)
	if presetID == "" {
		return SavedPreset{}, sql.ErrNoRows
	}

	tx, err := d.sql.Begin()
	if err != nil {
		return SavedPreset{}, err
	}
	defer tx.Rollback()

	existing, err := scanSavedPreset(tx.QueryRow(`
		SELECT `+savedPresetColumns+`
		FROM saved_presets
		WHERE user_id = ? AND preset_id = ?
	`, userID, presetID))
	if err != nil {
		return SavedPreset{}, err
	}

	newName := existing.Name
	if name != nil {
		newName = cleanSavedPresetName(*name)
	}
	newPayload := existing.PayloadJSON
	if payloadJSON != nil {
		newPayload = *payloadJSON
	}
	newActive := existing.Active
	if activate != nil {
		newActive = *activate
	}

	if newActive && !existing.Active {
		if _, err := tx.Exec(`UPDATE saved_presets SET is_active = 0 WHERE user_id = ? AND tab = ?`, userID, existing.Tab); err != nil {
			return SavedPreset{}, err
		}
	}
	activeInt := 0
	if newActive {
		activeInt = 1
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`
		UPDATE saved_presets
		SET name = ?, payload_json = ?, is_active = ?, updated_at = ?
		WHERE user_id = ? AND preset_id = ?
	`, newName, newPayload, activeInt, now, userID, presetID); err != nil {
		return SavedPreset{}, err
	}
	if err := tx.Commit(); err != nil {
		return SavedPreset{}, err
	}
	return d.GetSavedPresetForUser(userID, presetID)
}

// ActivateSavedPresetForUser marks one preset active for its tab, clearing
// whatever that tab had active before.
func (d *DB) ActivateSavedPresetForUser(userID, presetID string) (SavedPreset, error) {
	userID = normalizeUserID(userID)
	presetID = cleanSavedPresetID(presetID)
	if presetID == "" {
		return SavedPreset{}, sql.ErrNoRows
	}

	tx, err := d.sql.Begin()
	if err != nil {
		return SavedPreset{}, err
	}
	defer tx.Rollback()

	var tab string
	if err := tx.QueryRow(`SELECT tab FROM saved_presets WHERE user_id = ? AND preset_id = ?`, userID, presetID).Scan(&tab); err != nil {
		return SavedPreset{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`UPDATE saved_presets SET is_active = 0 WHERE user_id = ? AND tab = ?`, userID, tab); err != nil {
		return SavedPreset{}, err
	}
	if _, err := tx.Exec(`
		UPDATE saved_presets SET is_active = 1, updated_at = ?
		WHERE user_id = ? AND preset_id = ?
	`, now, userID, presetID); err != nil {
		return SavedPreset{}, err
	}
	if err := tx.Commit(); err != nil {
		return SavedPreset{}, err
	}
	return d.GetSavedPresetForUser(userID, presetID)
}

// DeleteSavedPresetForUser removes a preset. Unlike cockpit loadouts, a tab
// left with zero saved presets (or zero active ones) is a normal state --
// that is exactly what "no custom presets yet" already means today -- so
// there is no minimum-row guard and no fallback promotion here.
func (d *DB) DeleteSavedPresetForUser(userID, presetID string) ([]SavedPreset, error) {
	userID = normalizeUserID(userID)
	presetID = cleanSavedPresetID(presetID)
	if presetID == "" {
		return nil, sql.ErrNoRows
	}
	res, err := d.sql.Exec(`DELETE FROM saved_presets WHERE user_id = ? AND preset_id = ?`, userID, presetID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, sql.ErrNoRows
	}
	return d.ListSavedPresetsForUser(userID)
}
