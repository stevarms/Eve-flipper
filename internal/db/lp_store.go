package db

import (
	"fmt"
	"strings"
	"time"
)

// lp_store.go -- the user's own price per run for selling a blueprint copy.
//
// Blueprint copies only sell on contract, and the contract-based estimate is
// built from asking prices, often only a handful. When the user knows better,
// their figure replaces the estimate for that blueprint type everywhere.

// GetLPBPCPriceOverrides returns type_id -> price per run for one user.
func (d *DB) GetLPBPCPriceOverrides(userID string) (map[int32]float64, error) {
	if d == nil || d.sql == nil {
		return nil, fmt.Errorf("no database")
	}
	rows, err := d.sql.Query(`SELECT type_id, price_per_run FROM lp_bpc_price_overrides WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int32]float64{}
	for rows.Next() {
		var typeID int32
		var price float64
		if err := rows.Scan(&typeID, &price); err != nil {
			return nil, err
		}
		out[typeID] = price
	}
	return out, rows.Err()
}

// SetLPBPCPriceOverride sets or replaces one override. A price of zero or less
// is refused: clearing an override is DeleteLPBPCPriceOverride, and a zero
// stored here would read as "worthless", not "unknown".
func (d *DB) SetLPBPCPriceOverride(userID string, typeID int32, pricePerRun float64) error {
	if d == nil || d.sql == nil {
		return fmt.Errorf("no database")
	}
	if strings.TrimSpace(userID) == "" || typeID <= 0 {
		return fmt.Errorf("lp bpc price: user and type are required")
	}
	if pricePerRun <= 0 {
		return fmt.Errorf("lp bpc price: price per run must be positive")
	}
	_, err := d.sql.Exec(`
		INSERT INTO lp_bpc_price_overrides (user_id, type_id, price_per_run, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, type_id) DO UPDATE SET
			price_per_run = excluded.price_per_run,
			updated_at = excluded.updated_at`,
		userID, typeID, pricePerRun, time.Now().UTC().Format(time.RFC3339))
	return err
}

// DeleteLPBPCPriceOverride clears one override. Clearing one that is not set
// is not an error.
func (d *DB) DeleteLPBPCPriceOverride(userID string, typeID int32) error {
	if d == nil || d.sql == nil {
		return fmt.Errorf("no database")
	}
	_, err := d.sql.Exec(`DELETE FROM lp_bpc_price_overrides WHERE user_id = ? AND type_id = ?`, userID, typeID)
	return err
}
