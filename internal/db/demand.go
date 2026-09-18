package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// DemandRegion represents cached demand data for a region.
type DemandRegion struct {
	RegionID      int32     `json:"region_id"`
	RegionName    string    `json:"region_name"`
	HotScore      float64   `json:"hot_score"`
	Status        string    `json:"status"`
	KillsToday    int64     `json:"kills_today"`
	KillsBaseline int64     `json:"kills_baseline"`
	ISKDestroyed  float64   `json:"isk_destroyed"`
	ActivePlayers int       `json:"active_players"`
	TopShips      []string  `json:"top_ships"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// DemandItem represents cached demand data for an item.
type DemandItem struct {
	RegionID     int32     `json:"region_id"`
	TypeID       int32     `json:"type_id"`
	TypeName     string    `json:"type_name"`
	GroupID      int32     `json:"group_id"`
	GroupName    string    `json:"group_name"`
	LossesPerDay int64     `json:"losses_per_day"`
	DemandScore  float64   `json:"demand_score"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// SaveDemandRegion saves or updates demand data for a region.
func (d *DB) SaveDemandRegion(region *DemandRegion) error {
	topShipsJSON, _ := json.Marshal(region.TopShips)

	_, err := d.sql.Exec(`
		INSERT OR REPLACE INTO demand_region_cache 
		(region_id, region_name, hot_score, status, kills_today, kills_baseline, 
		 isk_destroyed, active_players, top_ships, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, region.RegionID, region.RegionName, region.HotScore, region.Status,
		region.KillsToday, region.KillsBaseline, region.ISKDestroyed,
		region.ActivePlayers, string(topShipsJSON), time.Now().Format(time.RFC3339))

	return err
}

// GetDemandRegions returns cached demand data for all regions.
func (d *DB) GetDemandRegions() ([]DemandRegion, error) {
	rows, err := d.sql.Query(`
		SELECT region_id, region_name, hot_score, status, kills_today, kills_baseline,
		       isk_destroyed, active_players, top_ships, updated_at
		FROM demand_region_cache
		ORDER BY hot_score DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var regions []DemandRegion
	for rows.Next() {
		var r DemandRegion
		var topShipsJSON string
		var updatedAtStr string

		err := rows.Scan(&r.RegionID, &r.RegionName, &r.HotScore, &r.Status,
			&r.KillsToday, &r.KillsBaseline, &r.ISKDestroyed, &r.ActivePlayers,
			&topShipsJSON, &updatedAtStr)
		if err != nil {
			continue
		}

		json.Unmarshal([]byte(topShipsJSON), &r.TopShips)
		r.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAtStr)

		regions = append(regions, r)
	}

	return regions, nil
}

// GetDemandRegion returns cached demand data for a specific region.
func (d *DB) GetDemandRegion(regionID int32) (*DemandRegion, error) {
	var r DemandRegion
	var topShipsJSON string
	var updatedAtStr string

	err := d.sql.QueryRow(`
		SELECT region_id, region_name, hot_score, status, kills_today, kills_baseline,
		       isk_destroyed, active_players, top_ships, updated_at
		FROM demand_region_cache
		WHERE region_id = ?
	`, regionID).Scan(&r.RegionID, &r.RegionName, &r.HotScore, &r.Status,
		&r.KillsToday, &r.KillsBaseline, &r.ISKDestroyed, &r.ActivePlayers,
		&topShipsJSON, &updatedAtStr)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	json.Unmarshal([]byte(topShipsJSON), &r.TopShips)
	r.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAtStr)

	return &r, nil
}

// GetHotZones returns regions with elevated activity (hot_score > 1.2).
func (d *DB) GetHotZones(limit int) ([]DemandRegion, error) {
	query := `
		SELECT region_id, region_name, hot_score, status, kills_today, kills_baseline,
		       isk_destroyed, active_players, top_ships, updated_at
		FROM demand_region_cache
		WHERE hot_score >= 1.2
		ORDER BY hot_score DESC
	`
	if limit > 0 {
		query += " LIMIT ?"
	}

	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = d.sql.Query(query, limit)
	} else {
		rows, err = d.sql.Query(query)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var regions []DemandRegion
	for rows.Next() {
		var r DemandRegion
		var topShipsJSON string
		var updatedAtStr string

		err := rows.Scan(&r.RegionID, &r.RegionName, &r.HotScore, &r.Status,
			&r.KillsToday, &r.KillsBaseline, &r.ISKDestroyed, &r.ActivePlayers,
			&topShipsJSON, &updatedAtStr)
		if err != nil {
			continue
		}

		json.Unmarshal([]byte(topShipsJSON), &r.TopShips)
		r.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAtStr)

		regions = append(regions, r)
	}

	return regions, nil
}

// SaveDemandItem saves or updates demand data for an item.
func (d *DB) SaveDemandItem(item *DemandItem) error {
	_, err := d.sql.Exec(`
		INSERT OR REPLACE INTO demand_item_cache 
		(region_id, type_id, type_name, group_id, group_name, losses_per_day, demand_score, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, item.RegionID, item.TypeID, item.TypeName, item.GroupID, item.GroupName,
		item.LossesPerDay, item.DemandScore, time.Now().Format(time.RFC3339))

	return err
}

// GetTopDemandItems returns items with highest demand scores.
func (d *DB) GetTopDemandItems(regionID int32, limit int) ([]DemandItem, error) {
	query := `
		SELECT region_id, type_id, type_name, group_id, group_name, losses_per_day, demand_score, updated_at
		FROM demand_item_cache
	`
	args := []interface{}{}

	if regionID > 0 {
		query += " WHERE region_id = ?"
		args = append(args, regionID)
	}

	query += " ORDER BY demand_score DESC"

	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := d.sql.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []DemandItem
	for rows.Next() {
		var item DemandItem
		var updatedAtStr string

		err := rows.Scan(&item.RegionID, &item.TypeID, &item.TypeName, &item.GroupID,
			&item.GroupName, &item.LossesPerDay, &item.DemandScore, &updatedAtStr)
		if err != nil {
			continue
		}

		item.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAtStr)
		items = append(items, item)
	}

	return items, nil
}

// IsDemandCacheFresh checks if the demand cache is recent enough.
func (d *DB) IsDemandCacheFresh(maxAge time.Duration) bool {
	var updatedAtStr string
	err := d.sql.QueryRow(`
		SELECT updated_at FROM demand_region_cache ORDER BY updated_at DESC LIMIT 1
	`).Scan(&updatedAtStr)

	if err != nil {
		return false
	}

	updatedAt, err := time.Parse(time.RFC3339, updatedAtStr)
	if err != nil {
		return false
	}

	return time.Since(updatedAt) < maxAge
}

// ClearDemandCache clears all cached demand data.
func (d *DB) ClearDemandCache() error {
	_, err := d.sql.Exec(`
		DELETE FROM demand_region_cache;
		DELETE FROM demand_item_cache;
	`)
	return err
}

// Demand scope kinds. `region` is WarTracker's question -- what dies in this
// region -- and `militia` is FW Supply's: what one militia loses inside its own
// warzone. The kind is stored rather than inferred because the two ID spaces
// overlap harmlessly on paper (region 10000069, faction 500001) and would not
// stay that way if a third kind arrived.
const (
	DemandScopeRegion  = "region"
	DemandScopeMilitia = "militia"
)

// defaultDemandWindowSeconds is 24h, which is what the existing region analyzer
// samples. A scope that does not state its window gets this one, so a v7-era
// caller keeps its meaning instead of silently sharing a row with a 7-day profile.
const defaultDemandWindowSeconds = 86400

// DemandScope identifies whose destruction a cached fitting profile describes.
//
// The window is part of the key, not metadata beside it. "Caldari militia over
// 24h" and "Caldari militia over 7 days" are different profiles of the same
// subject -- the per-item rates differ, because a week is what makes them stable
// -- so writing one must not overwrite the other.
type DemandScope struct {
	Kind          string `json:"scope_kind"`
	ID            int64  `json:"scope_id"`
	WindowSeconds int64  `json:"window_seconds"`
}

// RegionDemandScope is the WarTracker scope: one region over the analyzer's
// default 24h window.
func RegionDemandScope(regionID int32) DemandScope {
	return DemandScope{Kind: DemandScopeRegion, ID: int64(regionID), WindowSeconds: defaultDemandWindowSeconds}
}

// MilitiaDemandScope is the FW Supply scope: one militia's losses inside the
// warzone over an explicit window.
func MilitiaDemandScope(factionID int32, windowSeconds int64) DemandScope {
	return DemandScope{Kind: DemandScopeMilitia, ID: int64(factionID), WindowSeconds: windowSeconds}.normalize()
}

// normalize fills in the default window and trims the kind. It does not invent a
// kind or an ID: a scope missing either identifies nothing, and Valid says so.
func (s DemandScope) normalize() DemandScope {
	s.Kind = strings.ToLower(strings.TrimSpace(s.Kind))
	if s.WindowSeconds <= 0 {
		s.WindowSeconds = defaultDemandWindowSeconds
	}
	return s
}

// Valid reports whether this scope addresses anything. An invalid scope is
// refused rather than stored, because a row keyed on an empty kind is a row
// nothing will ever read back.
func (s DemandScope) Valid() bool {
	n := s.normalize()
	return n.Kind != "" && n.ID != 0
}

// String is for error messages and logs.
func (s DemandScope) String() string {
	n := s.normalize()
	return fmt.Sprintf("%s/%d/%ds", n.Kind, n.ID, n.WindowSeconds)
}

// FittingDemandItem is one type's destruction rate within a scope.
type FittingDemandItem struct {
	ScopeKind      string    `json:"scope_kind"`
	ScopeID        int64     `json:"scope_id"`
	WindowSeconds  int64     `json:"window_seconds"`
	TypeID         int32     `json:"type_id"`
	TypeName       string    `json:"type_name"`
	Category       string    `json:"category"`
	TotalDestroyed int64     `json:"total_destroyed"`
	KillmailCount  int       `json:"killmail_count"`
	AvgPerKillmail float64   `json:"avg_per_killmail"`
	EstDailyDemand float64   `json:"est_daily_demand"`
	SampledKills   int       `json:"sampled_kills"`
	TotalKills24h  int       `json:"total_kills_24h"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Scope is the key this item was stored under.
func (i FittingDemandItem) Scope() DemandScope {
	return DemandScope{Kind: i.ScopeKind, ID: i.ScopeID, WindowSeconds: i.WindowSeconds}
}

// SaveFittingDemandProfile replaces one scope's cached profile.
//
// The DELETE is scoped to all three key columns, so refreshing the 7-day militia
// profile leaves the 24h one and every region profile alone. Replacing rather
// than upserting is deliberate: a type that stopped being destroyed should
// disappear, not linger at last week's rate.
func (d *DB) SaveFittingDemandProfile(scope DemandScope, items []FittingDemandItem) error {
	if d == nil || d.sql == nil {
		return fmt.Errorf("no database")
	}
	scope = scope.normalize()
	if !scope.Valid() {
		return fmt.Errorf("fitting demand: refusing to store profile under scope %s", scope)
	}

	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		DELETE FROM demand_fitting_cache
		WHERE scope_kind = ? AND scope_id = ? AND window_seconds = ?
	`, scope.Kind, scope.ID, scope.WindowSeconds); err != nil {
		return err
	}

	stmt, err := tx.Prepare(`
		INSERT INTO demand_fitting_cache
		(scope_kind, scope_id, window_seconds, type_id, type_name, category,
		 total_destroyed, killmail_count, avg_per_killmail, est_daily_demand,
		 sampled_kills, total_kills_24h, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, item := range items {
		if _, err := stmt.Exec(
			scope.Kind, scope.ID, scope.WindowSeconds,
			item.TypeID, item.TypeName, item.Category,
			item.TotalDestroyed, item.KillmailCount, item.AvgPerKillmail,
			item.EstDailyDemand, item.SampledKills, item.TotalKills24h, now,
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetFittingDemandProfile returns one scope's cached profile, heaviest daily
// destruction first.
func (d *DB) GetFittingDemandProfile(scope DemandScope) ([]FittingDemandItem, error) {
	if d == nil || d.sql == nil {
		return nil, fmt.Errorf("no database")
	}
	scope = scope.normalize()

	rows, err := d.sql.Query(`
		SELECT scope_kind, scope_id, window_seconds, type_id, type_name, category,
		       total_destroyed, killmail_count, avg_per_killmail, est_daily_demand,
		       sampled_kills, total_kills_24h, updated_at
		FROM demand_fitting_cache
		WHERE scope_kind = ? AND scope_id = ? AND window_seconds = ?
		ORDER BY est_daily_demand DESC
	`, scope.Kind, scope.ID, scope.WindowSeconds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []FittingDemandItem
	for rows.Next() {
		var item FittingDemandItem
		var updatedAtStr string
		err := rows.Scan(&item.ScopeKind, &item.ScopeID, &item.WindowSeconds,
			&item.TypeID, &item.TypeName, &item.Category,
			&item.TotalDestroyed, &item.KillmailCount, &item.AvgPerKillmail,
			&item.EstDailyDemand, &item.SampledKills, &item.TotalKills24h, &updatedAtStr)
		if err != nil {
			continue
		}
		item.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAtStr)
		items = append(items, item)
	}
	return items, rows.Err()
}

// DemandScopeCoverage is what one cached profile's fetch actually managed, as
// distinct from what it was asked for.
//
// It exists because a long window can come back incomplete in a way the rates
// themselves cannot show: they are already scaled to the covered time, so they
// are correct and they look complete. CoveredSeconds and Warnings are what say
// otherwise, and they have to survive the cache -- a warning that is dropped on
// the way to disk is worse than no cache at all, because the second read is the
// one that looks authoritative.
//
// MonthsJSON is carried as an opaque string. Which months a walk covered is
// zkillboard's shape, and this package has no business knowing it.
type DemandScopeCoverage struct {
	Scope          DemandScope `json:"scope"`
	FetchedKills   int         `json:"fetched_kills"`
	InWarzoneKills int         `json:"in_warzone_kills"`
	CoveredSeconds float64     `json:"covered_seconds"`
	Truncated      bool        `json:"truncated"`
	Warnings       []string    `json:"warnings,omitempty"`
	MonthsJSON     string      `json:"months_json,omitempty"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

// SaveDemandScopeCoverage records what a scope's fetch covered, replacing any
// earlier record for the same scope.
func (d *DB) SaveDemandScopeCoverage(cov DemandScopeCoverage) error {
	if d == nil || d.sql == nil {
		return fmt.Errorf("no database")
	}
	scope := cov.Scope.normalize()
	if !scope.Valid() {
		return fmt.Errorf("demand coverage: refusing to store under scope %s", scope)
	}

	warnings := ""
	if len(cov.Warnings) > 0 {
		if b, err := json.Marshal(cov.Warnings); err == nil {
			warnings = string(b)
		}
	}
	_, err := d.sql.Exec(`
		INSERT OR REPLACE INTO demand_scope_coverage
		(scope_kind, scope_id, window_seconds, fetched_kills, in_warzone_kills,
		 covered_seconds, truncated, warnings_json, months_json, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, scope.Kind, scope.ID, scope.WindowSeconds, cov.FetchedKills, cov.InWarzoneKills,
		cov.CoveredSeconds, boolToInt(cov.Truncated), warnings, cov.MonthsJSON,
		time.Now().UTC().Format(time.RFC3339))
	return err
}

// GetDemandScopeCoverage returns what a scope's last fetch covered, or nil when
// nothing was recorded.
//
// Nil is not "the fetch was complete". A caller serving cached rates with no
// coverage record beside them knows only that it cannot say how complete they
// are, which is a different sentence from saying they are whole.
func (d *DB) GetDemandScopeCoverage(scope DemandScope) (*DemandScopeCoverage, error) {
	if d == nil || d.sql == nil {
		return nil, fmt.Errorf("no database")
	}
	scope = scope.normalize()

	var (
		cov        DemandScopeCoverage
		truncated  int
		warnings   string
		updatedStr string
	)
	err := d.sql.QueryRow(`
		SELECT fetched_kills, in_warzone_kills, covered_seconds, truncated,
		       warnings_json, months_json, updated_at
		FROM demand_scope_coverage
		WHERE scope_kind = ? AND scope_id = ? AND window_seconds = ?
	`, scope.Kind, scope.ID, scope.WindowSeconds).Scan(
		&cov.FetchedKills, &cov.InWarzoneKills, &cov.CoveredSeconds, &truncated,
		&warnings, &cov.MonthsJSON, &updatedStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	cov.Scope = scope
	cov.Truncated = truncated != 0
	if warnings != "" {
		// An unparseable warning list costs the warnings, not the profile -- but
		// it must not silently read as "there were none", so one is put back.
		if err := json.Unmarshal([]byte(warnings), &cov.Warnings); err != nil {
			cov.Warnings = []string{"the stored coverage warnings for this demand sample could not be read back"}
		}
	}
	if t, err := time.Parse(time.RFC3339, updatedStr); err == nil {
		cov.UpdatedAt = t
	}
	return &cov, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// IsFittingProfileFresh reports whether this scope's profile is recent enough to
// serve without refetching. A scope that was never stored is not fresh.
func (d *DB) IsFittingProfileFresh(scope DemandScope, maxAge time.Duration) bool {
	if d == nil || d.sql == nil {
		return false
	}
	scope = scope.normalize()

	var updatedAtStr string
	err := d.sql.QueryRow(`
		SELECT updated_at FROM demand_fitting_cache
		WHERE scope_kind = ? AND scope_id = ? AND window_seconds = ?
		LIMIT 1
	`, scope.Kind, scope.ID, scope.WindowSeconds).Scan(&updatedAtStr)
	if err != nil {
		return false
	}
	updatedAt, err := time.Parse(time.RFC3339, updatedAtStr)
	if err != nil {
		return false
	}
	return time.Since(updatedAt) < maxAge
}
