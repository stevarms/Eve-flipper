package db

import (
	"fmt"
	"time"
)

// ManualPosition is a holding the user entered by hand.
//
// The FIFO cost-basis engine derives positions from ESI market transactions,
// which means it cannot see loot, contract buys, corp transfers, or anything
// acquired before the wallet-journal history window — exactly the stock a
// patient trader tends to be sitting on. These rows fill that gap. They are
// never merged into a derived position: a manual entry appears as its own row
// with source "manual" so the two cost bases stay honest.
type ManualPosition struct {
	ID          int64   `json:"id"`
	TypeID      int32   `json:"type_id"`
	TypeName    string  `json:"type_name"`
	Quantity    int64   `json:"quantity"`
	UnitCost    float64 `json:"unit_cost"`
	TargetPrice float64 `json:"target_price"`
	AcquiredAt  string  `json:"acquired_at"`
	Note        string  `json:"note"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

// GetManualPositionsForUser returns the user's hand-entered holdings, oldest
// acquisition first. Never returns nil so handlers can marshal it directly.
func (d *DB) GetManualPositionsForUser(userID string) []ManualPosition {
	userID = normalizeUserID(userID)

	rows, err := d.sql.Query(`
		SELECT id, type_id, type_name, quantity, unit_cost, target_price,
		       acquired_at, note, created_at, updated_at
		  FROM manual_positions
		 WHERE user_id = ?
		 ORDER BY acquired_at ASC, id ASC
	`, userID)
	if err != nil {
		return []ManualPosition{}
	}
	defer rows.Close()

	items := []ManualPosition{}
	for rows.Next() {
		var m ManualPosition
		if err := rows.Scan(
			&m.ID, &m.TypeID, &m.TypeName, &m.Quantity, &m.UnitCost,
			&m.TargetPrice, &m.AcquiredAt, &m.Note, &m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			continue
		}
		items = append(items, m)
	}
	return items
}

// SaveManualPositionForUser inserts a new holding, or updates the existing one
// when m.ID is set. Returns the stored row (with its id and timestamps).
func (d *DB) SaveManualPositionForUser(userID string, m ManualPosition) (ManualPosition, error) {
	userID = normalizeUserID(userID)
	if m.TypeID <= 0 {
		return m, fmt.Errorf("type_id is required")
	}
	if m.Quantity <= 0 {
		return m, fmt.Errorf("quantity must be positive")
	}
	if m.UnitCost < 0 || m.TargetPrice < 0 {
		return m, fmt.Errorf("prices cannot be negative")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if m.AcquiredAt == "" {
		m.AcquiredAt = now
	}
	m.UpdatedAt = now

	if m.ID > 0 {
		res, err := d.sql.Exec(`
			UPDATE manual_positions
			   SET type_id = ?, type_name = ?, quantity = ?, unit_cost = ?,
			       target_price = ?, acquired_at = ?, note = ?, updated_at = ?
			 WHERE id = ? AND user_id = ?
		`, m.TypeID, m.TypeName, m.Quantity, m.UnitCost, m.TargetPrice,
			m.AcquiredAt, m.Note, m.UpdatedAt, m.ID, userID)
		if err != nil {
			return m, err
		}
		// A row belonging to another user must not be silently created here.
		if n, _ := res.RowsAffected(); n == 0 {
			return m, fmt.Errorf("position %d not found", m.ID)
		}
		return m, nil
	}

	m.CreatedAt = now
	res, err := d.sql.Exec(`
		INSERT INTO manual_positions
			(user_id, type_id, type_name, quantity, unit_cost, target_price,
			 acquired_at, note, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, userID, m.TypeID, m.TypeName, m.Quantity, m.UnitCost, m.TargetPrice,
		m.AcquiredAt, m.Note, m.CreatedAt, m.UpdatedAt)
	if err != nil {
		return m, err
	}
	if id, err := res.LastInsertId(); err == nil {
		m.ID = id
	}
	return m, nil
}

// DeleteManualPositionForUser removes one hand-entered holding.
func (d *DB) DeleteManualPositionForUser(userID string, id int64) error {
	userID = normalizeUserID(userID)
	_, err := d.sql.Exec(`DELETE FROM manual_positions WHERE id = ? AND user_id = ?`, id, userID)
	return err
}
