package db

import (
	"strings"
	"time"
)

// today_plan.go — persistence for the Today work order.
//
// Two tables with deliberately different characters. The plan is a cache
// that exists so the landing screen paints instantly instead of waiting on
// the ESI round trips that built it; losing it costs a refresh. The action
// state is a record: it is the only place the *projection at the moment of
// acting* is ever written down, and once a plan is replaced that number is
// unrecoverable.

// TodayActionState is one action the user marked done or skipped, together
// with what was promised when they did.
type TodayActionState struct {
	ActionID     string  `json:"action_id"`
	Mode         string  `json:"mode"` // done | skip
	Kind         string  `json:"kind"`
	TypeID       int32   `json:"type_id"`
	Grade        string  `json:"grade"`
	ProjectedISK float64 `json:"projected_isk"`
	Quantity     int64   `json:"quantity"`
	Price        float64 `json:"price"`
	ActedAt      string  `json:"acted_at"`
}

// SaveTodayPlan replaces the cached plan for a user.
func (d *DB) SaveTodayPlan(userID, generatedAt, payloadJSON string) error {
	if d == nil || d.sql == nil || userID == "" {
		return nil
	}
	_, err := d.sql.Exec(`
		INSERT INTO today_plan (user_id, generated_at, payload_json)
		VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			generated_at = excluded.generated_at,
			payload_json = excluded.payload_json`,
		userID, generatedAt, payloadJSON)
	return err
}

// GetTodayPlan returns the cached plan and when it was built. ok is false
// when the user has never refreshed.
func (d *DB) GetTodayPlan(userID string) (payloadJSON, generatedAt string, ok bool) {
	if d == nil || d.sql == nil || userID == "" {
		return "", "", false
	}
	row := d.sql.QueryRow(
		`SELECT payload_json, generated_at FROM today_plan WHERE user_id = ?`, userID)
	if err := row.Scan(&payloadJSON, &generatedAt); err != nil {
		return "", "", false
	}
	return payloadJSON, generatedAt, true
}

// SetTodayActionState marks one action done or skipped. Mode "clear" removes
// the row, which is how an accidental Space is undone.
//
// The projection fields are written on the way in rather than looked up
// later on purpose: the plan that produced them is replaced on every
// refresh, so a row inserted without them can never be enriched afterwards.
func (d *DB) SetTodayActionState(userID string, state TodayActionState) error {
	if d == nil || d.sql == nil || userID == "" || state.ActionID == "" {
		return nil
	}
	if strings.EqualFold(state.Mode, "clear") {
		_, err := d.sql.Exec(
			`DELETE FROM today_action_state WHERE user_id = ? AND action_id = ?`,
			userID, state.ActionID)
		return err
	}
	if state.ActedAt == "" {
		state.ActedAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := d.sql.Exec(`
		INSERT INTO today_action_state
			(user_id, action_id, mode, kind, type_id, grade, projected_isk, quantity, price, acted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, action_id) DO UPDATE SET
			mode          = excluded.mode,
			kind          = excluded.kind,
			type_id       = excluded.type_id,
			grade         = excluded.grade,
			projected_isk = excluded.projected_isk,
			quantity      = excluded.quantity,
			price         = excluded.price,
			acted_at      = excluded.acted_at`,
		userID, state.ActionID, strings.ToLower(state.Mode), state.Kind, state.TypeID,
		state.Grade, state.ProjectedISK, state.Quantity, state.Price, state.ActedAt)
	return err
}

// GetTodayActionStates returns the done/skip marks made at or after `since`,
// keyed by action id. Pass an empty `since` for all of them.
//
// Filtering on read rather than deleting on refresh is what makes both
// requirements hold at once. Action ids are deterministic, so yesterday's
// "done" would otherwise come back and tick off an identical action in
// today's plan — but the rows themselves are the only record of what was
// promised at the moment the user acted, so sweeping them would throw away
// the input to grading Today against its own results.
func (d *DB) GetTodayActionStates(userID, since string) map[string]TodayActionState {
	out := map[string]TodayActionState{}
	if d == nil || d.sql == nil || userID == "" {
		return out
	}
	query := `
		SELECT action_id, mode, kind, type_id, grade, projected_isk, quantity, price, acted_at
		FROM today_action_state WHERE user_id = ?`
	args := []interface{}{userID}
	if since != "" {
		query += ` AND acted_at >= ?`
		args = append(args, since)
	}
	rows, err := d.sql.Query(query, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var s TodayActionState
		if err := rows.Scan(&s.ActionID, &s.Mode, &s.Kind, &s.TypeID, &s.Grade,
			&s.ProjectedISK, &s.Quantity, &s.Price, &s.ActedAt); err != nil {
			continue
		}
		out[s.ActionID] = s
	}
	return out
}

// PruneTodayActionStates drops marks older than `before`, for housekeeping
// only. This is not how a stale "done" stops applying to a fresh plan —
// GetTodayActionStates filters on read for that — so the cutoff here should
// be generous enough to leave a useful window of projections to reconcile
// against.
func (d *DB) PruneTodayActionStates(userID, before string) error {
	if d == nil || d.sql == nil || userID == "" || before == "" {
		return nil
	}
	_, err := d.sql.Exec(
		`DELETE FROM today_action_state WHERE user_id = ? AND acted_at < ?`,
		userID, before)
	return err
}
