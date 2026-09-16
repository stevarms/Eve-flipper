package db

import (
	"math"
	"time"
)

// holding_rules.go — the prices and quantities you have decided by hand,
// which the automated advice must not talk you out of.
//
// A target price is "hold until it is worth what I think it is worth"; a bid
// ceiling is its buy-side mirror, "do not follow the book above this"; a
// patient bid is "that order is parked on purpose"; a reserved quantity is
// "these are not stock". They live in one row because they answer the same
// question from the user's side — what of this type am I actually willing to
// trade today, and at what — and because a type usually needs several or none.
//
// Keyed by type, not by order or position id: the FIFO engine recomputes
// positions from transactions on every pass and relisting mints a new order id,
// so a rule pinned to either would evaporate.

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

	// MaxBidPrice is the buy-side mirror: the unit price above which the
	// order desk must stop advising you to bid. Zero means no ceiling.
	//
	// It exists because the desk's reprice advice reads the book and only
	// the book, so a market that has run 10x since you decided what the item
	// was worth produces a confident instruction to follow it up there.
	MaxBidPrice float64 `json:"max_bid_price"`

	// PatientBid marks a buy order as parked on purpose, overriding the
	// desk's automatic lowball test for bids that sit closer in than its
	// threshold.
	PatientBid bool `json:"patient_bid"`

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
	return r.TargetPrice <= 0 && r.MaxBidPrice <= 0 && !r.PatientBid &&
		r.ReservedQty <= 0 && r.Note == ""
}

// GetHoldingRules returns every rule for a user, keyed by type id.
func (d *DB) GetHoldingRules(userID string) map[int32]HoldingRule {
	out := map[int32]HoldingRule{}
	if d == nil || d.sql == nil || userID == "" {
		return out
	}
	rows, err := d.sql.Query(`
		SELECT type_id, target_price, target_percentile, target_basis,
		       max_bid_price, patient_bid, reserved_qty, note, updated_at
		FROM holding_rules WHERE user_id = ?`, userID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var r HoldingRule
		if err := rows.Scan(&r.TypeID, &r.TargetPrice, &r.TargetPercentile,
			&r.TargetBasis, &r.MaxBidPrice, &r.PatientBid, &r.ReservedQty,
			&r.Note, &r.UpdatedAt); err != nil {
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
	if r.MaxBidPrice < 0 || math.IsNaN(r.MaxBidPrice) || math.IsInf(r.MaxBidPrice, 0) {
		r.MaxBidPrice = 0
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
			 max_bid_price, patient_bid, reserved_qty, note, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, type_id) DO UPDATE SET
			target_price      = excluded.target_price,
			target_percentile = excluded.target_percentile,
			target_basis      = excluded.target_basis,
			max_bid_price     = excluded.max_bid_price,
			patient_bid       = excluded.patient_bid,
			reserved_qty      = excluded.reserved_qty,
			note              = excluded.note,
			updated_at        = excluded.updated_at`,
		userID, r.TypeID, r.TargetPrice, r.TargetPercentile, r.TargetBasis,
		r.MaxBidPrice, r.PatientBid, r.ReservedQty, r.Note, r.UpdatedAt)
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
