package db

import (
	"fmt"
	"time"
)

// IndustryBlueprintFavorite is one starred row in the Discover scanner.
//
// The identity is (blueprint, product, scan mode) rather than just the
// blueprint: a single BPO fans out to several invention products, and the
// scanner shows each as its own row. Starring the Warrior II row must not
// star the Hobgoblin II row off the same blueprint.
type IndustryBlueprintFavorite struct {
	BlueprintTypeID int32  `json:"blueprint_type_id"`
	ProductTypeID   int32  `json:"product_type_id"`
	ScanMode        string `json:"scan_mode"`
	IsBPO           bool   `json:"is_bpo"`
	BlueprintName   string `json:"blueprint_name"`
	ProductName     string `json:"product_name"`
	AddedAt         string `json:"added_at"`
}

// normalizeFavoriteScanMode keeps the stored key stable when the client
// omits the mode. The scanner's own fallback for a missing scan_mode is
// t1_mfg, so the row key it builds and the key we store agree.
func normalizeFavoriteScanMode(mode string) string {
	if mode == "" {
		return "t1_mfg"
	}
	return mode
}

// GetIndustryFavoritesForUser returns the user's starred scanner rows,
// most recently starred first. Never returns nil, so the handler can
// marshal it straight to a JSON array.
func (d *DB) GetIndustryFavoritesForUser(userID string) []IndustryBlueprintFavorite {
	userID = normalizeUserID(userID)

	rows, err := d.sql.Query(`
		SELECT blueprint_type_id, product_type_id, scan_mode, is_bpo,
		       blueprint_name, product_name, added_at
		  FROM industry_blueprint_favorites
		 WHERE user_id = ?
		 ORDER BY added_at DESC
	`, userID)
	if err != nil {
		return []IndustryBlueprintFavorite{}
	}
	defer rows.Close()

	items := []IndustryBlueprintFavorite{}
	for rows.Next() {
		var f IndustryBlueprintFavorite
		var isBPO int
		if err := rows.Scan(
			&f.BlueprintTypeID,
			&f.ProductTypeID,
			&f.ScanMode,
			&isBPO,
			&f.BlueprintName,
			&f.ProductName,
			&f.AddedAt,
		); err != nil {
			continue
		}
		f.IsBPO = isBPO != 0
		items = append(items, f)
	}
	return items
}

// AddIndustryFavoriteForUser stars a row. Re-starring an existing row
// refreshes its display names without moving it in the list, so a rescan
// that picked up a renamed type doesn't reshuffle the user's stars.
func (d *DB) AddIndustryFavoriteForUser(userID string, f IndustryBlueprintFavorite) error {
	userID = normalizeUserID(userID)
	if f.BlueprintTypeID <= 0 || f.ProductTypeID <= 0 {
		return fmt.Errorf("favorite requires blueprint_type_id and product_type_id")
	}
	f.ScanMode = normalizeFavoriteScanMode(f.ScanMode)
	if f.AddedAt == "" {
		f.AddedAt = time.Now().UTC().Format(time.RFC3339)
	}

	isBPO := 0
	if f.IsBPO {
		isBPO = 1
	}
	_, err := d.sql.Exec(`
		INSERT INTO industry_blueprint_favorites
			(user_id, blueprint_type_id, product_type_id, scan_mode, is_bpo,
			 blueprint_name, product_name, added_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, blueprint_type_id, product_type_id, scan_mode)
		DO UPDATE SET is_bpo         = excluded.is_bpo,
		              blueprint_name = excluded.blueprint_name,
		              product_name   = excluded.product_name
	`, userID, f.BlueprintTypeID, f.ProductTypeID, f.ScanMode, isBPO,
		f.BlueprintName, f.ProductName, f.AddedAt)
	if err != nil {
		return fmt.Errorf("add industry favorite: %w", err)
	}
	return nil
}

// DeleteIndustryFavoriteForUser unstars a row. Deleting a row that was
// never starred is not an error — the star is a toggle, and the client
// should not have to care whether its view was stale.
func (d *DB) DeleteIndustryFavoriteForUser(userID string, blueprintTypeID, productTypeID int32, scanMode string) error {
	userID = normalizeUserID(userID)
	_, err := d.sql.Exec(`
		DELETE FROM industry_blueprint_favorites
		 WHERE user_id = ? AND blueprint_type_id = ? AND product_type_id = ? AND scan_mode = ?
	`, userID, blueprintTypeID, productTypeID, normalizeFavoriteScanMode(scanMode))
	if err != nil {
		return fmt.Errorf("delete industry favorite: %w", err)
	}
	return nil
}
