package db

import (
	"math"
	"time"
)

// holding_rules.go — the two reasons something you own should not be sold
// today.
//
// A target price is "hold until it is worth what I think it is worth". A
// reserved quantity is "these are not stock". They live in one row because
// they answer the same question from the user's side — what of this pile is
// actually for sale — and because a holding usually needs both or neither.
//
// Keyed by type, not by position id: the FIFO engine recomputes positions from
// transactions on every pass, so a rule pinned to a position would evaporate.

// HoldingRule is one type's selling constraints.
type HoldingRule struct {
	TypeID int32 `json:"type_id"`

	// TargetPrice is the unit price at or above which selling is wanted.
	// Zero means no target and the holding trades normally.
	TargetPrice float64 `json:"target_price"`
	// TargetPercentile records which percentile of the trailing year the
	// target came from, so the editor can show what it picked and move it.
	// Zero means the price was typed in rather than suggested.
	TargetPercentile float64 `json:"target_percentile"`
	// TargetBasis is how the target was arrived at: "percentile" or "manual".
	TargetBasis string `json:"target_basis"`

	// ReservedQty is units held back from trading entirely — the ships you
	// fly. Subtracted from the tradeable quantity, never from the position.
	ReservedQty int64 `json:"reserved_qty"`

	Note      string `json:"note"`
	UpdatedAt string `json:"updated_at"`
}

// IsEmpty reports whether a rule constrains anything. An empty rule is
// deleted rather than stored, so a cleared form does not leave a row that
// silently does nothing.
func (r HoldingRule) IsEmpty() bool {
	return r.TargetPrice <= 0 && r.ReservedQty <= 0 && r.Note == ""
}

// GetHoldingRules returns every rule for a user, keyed by type id.
func (d *DB) GetHoldingRules(userID string) map[int32]HoldingRule {
	out := map[int32]HoldingRule{}
	if d == nil || d.sql == nil || userID == "" {
		return out
	}
	rows, err := d.sql.Query(`
		SELECT type_id, target_price, target_percentile, target_basis,
		       reserved_qty, note, updated_at
		FROM holding_rules WHERE user_id = ?`, userID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var r HoldingRule
		if err := rows.Scan(&r.TypeID, &r.TargetPrice, &r.TargetPercentile,
			&r.TargetBasis, &r.ReservedQty, &r.Note, &r.UpdatedAt); err != nil {
			continue
		}
		out[r.TypeID] = r
	}
	return out
}

// SetHoldingRule stores or replaces one rule. A rule that constrains nothing
// is deleted instead, so clearing the form in the UI removes the row rather
// than leaving an inert one behind.
func (d *DB) SetHoldingRule(userID string, r HoldingRule) error {
	if d == nil || d.sql == nil || userID == "" || r.TypeID <= 0 {
		return nil
	}
	if r.IsEmpty() {
		return d.DeleteHoldingRule(userID, r.TypeID)
	}

	// Sanitise rather than reject. These arrive from a form, and a negative
	// reserve or a NaN target is a slip, not an attack — but neither must
	// reach the engine, where a NaN would poison every comparison it touches.
	if r.TargetPrice < 0 || math.IsNaN(r.TargetPrice) || math.IsInf(r.TargetPrice, 0) {
		r.TargetPrice = 0
	}
	if r.TargetPercentile < 0 || r.TargetPercentile > 100 ||
		math.IsNaN(r.TargetPercentile) || math.IsInf(r.TargetPercentile, 0) {
		r.TargetPercentile = 0
	}
	if r.ReservedQty < 0 {
		r.ReservedQty = 0
	}
	if len(r.Note) > 500 {
		r.Note = r.Note[:500]
	}
	if r.UpdatedAt == "" {
		r.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	_, err := d.sql.Exec(`
		INSERT INTO holding_rules
			(user_id, type_id, target_price, target_percentile, target_basis,
			 reserved_qty, note, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, type_id) DO UPDATE SET
			target_price      = excluded.target_price,
			target_percentile = excluded.target_percentile,
			target_basis      = excluded.target_basis,
			reserved_qty      = excluded.reserved_qty,
			note              = excluded.note,
			updated_at        = excluded.updated_at`,
		userID, r.TypeID, r.TargetPrice, r.TargetPercentile, r.TargetBasis,
		r.ReservedQty, r.Note, r.UpdatedAt)
	return err
}

// DeleteHoldingRule removes a type's rule, returning it to normal trading.
func (d *DB) DeleteHoldingRule(userID string, typeID int32) error {
	if d == nil || d.sql == nil || userID == "" {
		return nil
	}
	_, err := d.sql.Exec(
		`DELETE FROM holding_rules WHERE user_id = ? AND type_id = ?`, userID, typeID)
	return err
}
