package db

import (
	"strings"
	"unicode"
)

// pi_factory.go — the PI Factory tab's portfolio: the list of planned
// factory lines (schematic + count), server-side so it follows your login.
// Unlike stockpiles there is exactly one implicit portfolio per user, not
// several named ones, so this is a single table rather than a header+items
// pair -- ReplacePIFactoryPortfolioForUser mirrors
// ReplaceStockpileItemsForUser's delete-then-reinsert transaction, just
// without a parent row to look up or touch first.

func cleanPIFactoryClientID(id string) string {
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

type PIFactoryEntry struct {
	ClientID     string `json:"client_id"`
	Name         string `json:"name"`
	SchematicID  int32  `json:"schematic_id"`
	FactoryCount int    `json:"factory_count"`
	SortOrder    int    `json:"sort_order"`
}

// GetPIFactoryPortfolioForUser returns a user's factory lines in display order.
func (d *DB) GetPIFactoryPortfolioForUser(userID string) ([]PIFactoryEntry, error) {
	userID = normalizeUserID(userID)
	rows, err := d.sql.Query(`
		SELECT client_id, name, schematic_id, factory_count, sort_order
		FROM pi_factory_entries
		WHERE user_id = ?
		ORDER BY sort_order ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []PIFactoryEntry{}
	for rows.Next() {
		var e PIFactoryEntry
		if err := rows.Scan(&e.ClientID, &e.Name, &e.SchematicID, &e.FactoryCount, &e.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ReplacePIFactoryPortfolioForUser replaces the whole portfolio in one
// transaction, taking sort_order from array index -- the frontend always
// sends its full current order (drag-reorder included), so there is no
// partial-update case to support.
func (d *DB) ReplacePIFactoryPortfolioForUser(userID string, entries []PIFactoryEntry) error {
	userID = normalizeUserID(userID)

	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM pi_factory_entries WHERE user_id = ?`, userID); err != nil {
		return err
	}
	if len(entries) > 0 {
		stmt, err := tx.Prepare(`
			INSERT INTO pi_factory_entries (user_id, client_id, name, schematic_id, factory_count, sort_order)
			VALUES (?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for i, e := range entries {
			clientID := cleanPIFactoryClientID(e.ClientID)
			if clientID == "" {
				continue
			}
			if _, err := stmt.Exec(userID, clientID, e.Name, e.SchematicID, e.FactoryCount, i); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
