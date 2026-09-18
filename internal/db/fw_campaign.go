package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"eve-flipper/internal/engine"
)

// fw_campaign.go -- persistence for FW Supply campaigns.
//
// Three tables with deliberately different characters, the same split as
// today_plan.go. fw_plan_cache is disposable: it is a gap table that can be
// rebuilt from zkill and a market fetch whenever it is wanted. fw_campaigns is
// settings, losing them costs retyping. fw_campaign_lots is neither -- it is the
// only record of ISK that actually left a wallet, and the sole input to the
// budget. Nothing here deletes a lot as a side effect of anything except
// deleting its campaign, which says so in its own doc comment.
//
// Every read and write is scoped by user_id as well as campaign_id. user_id is
// denormalized onto lots and the plan cache precisely so that can be done
// directly, without a join that one query eventually forgets.

// jitaIVMoon4StationID is Jita IV - Moon 4 - Caldari Navy Assembly Plant, the
// default source hub for a campaign that has not named one.
const jitaIVMoon4StationID = 60003760

// FWTypeOverride names a type the way the row that produced it did, so a
// settings-panel chip can show what was overridden without a second lookup --
// an excluded type may never appear in a plan again to look its name up from.
type FWTypeOverride struct {
	TypeID   int32  `json:"type_id"`
	TypeName string `json:"type_name"`
}

// FWCampaign is one supply line: a militia's warzone, a destination to stock,
// a budget, and the parameters that shape the plan.
//
// The ownership pair is a default for generated work, not a constraint on what
// is recognised. The buyer is who the buy list is written for; the seller is who
// the listing work is written for. A lot held by anyone else is still counted --
// see FWLot's own owner triple.
type FWCampaign struct {
	CampaignID int64  `json:"campaign_id"`
	UserID     string `json:"-"`
	Name       string `json:"name"`

	MilitiaFactionID int32 `json:"militia_faction_id"`
	SourceStationID  int64 `json:"source_station_id"`
	DestStationID    int64 `json:"dest_station_id"`

	BudgetISK float64 `json:"budget_isk"`

	TargetCoverDays   float64            `json:"target_cover_days"`
	CoveredMultiple   float64            `json:"covered_multiple"`
	MinMarginPct      float64            `json:"min_margin_pct"`
	FreightISKPerM3   float64            `json:"freight_isk_per_m3"`
	StepOverDaysCover float64            `json:"step_over_days_cover"`
	CategoryCeilings  map[string]float64 `json:"category_ceilings"`

	ShipProfile string `json:"ship_profile"`
	MaxTrips    int    `json:"max_trips"`

	// LongDemandWindowSeconds is the second demand window, or 0 for none. Stored
	// in seconds so changing 30 days to 45 is a settings change rather than a
	// migration. SizeAgainst -- "short" or "long" -- picks which of the two rates
	// drives cover and quantities; the other is still measured and still shown,
	// because the pair is the point.
	LongDemandWindowSeconds int    `json:"long_demand_window_seconds"`
	SizeAgainst             string `json:"size_against"`

	MaxJumpsFromFront int     `json:"max_jumps_from_front"`
	PinnedSystems     []int32 `json:"pinned_systems"`
	ExcludedSystems   []int32 `json:"excluded_systems"`

	// IncludedTypes always get a price and a quantity when there is a real
	// cover gap, past the automatic caution -- thin evidence, a competitor's
	// depth -- that would otherwise drop them. ExcludedTypes never appear at
	// all. Both are your judgment overriding the model's, per item: destruction
	// is a proxy for demand, not demand itself, and only you know which proxy
	// failures are worth betting against. Named rather than bare IDs because an
	// excluded type may never appear in a plan again to look its name up from.
	IncludedTypes []FWTypeOverride `json:"included_types"`
	ExcludedTypes []FWTypeOverride `json:"excluded_types"`

	BuyerCharacterID int64  `json:"buyer_character_id"`
	SellerOwnerKind  string `json:"seller_owner_kind"`
	SellerOwnerID    int64  `json:"seller_owner_id"`

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// SupplyConfig is the campaign's half of the plan's inputs.
//
// Fee rates are left at zero deliberately. Broker fee and sales tax follow the
// issuing character's standings and skills, so they belong to whoever is
// listing, not to the campaign -- the caller fills them in from the Order Desk's
// per-character rates, along with the ladder derived from the destination's own
// book. Everything the campaign genuinely owns is set here, and the engine
// defaults the rest.
func (c FWCampaign) SupplyConfig() engine.FWSupplyConfig {
	return engine.FWSupplyConfig{
		DestStationID:     c.DestStationID,
		TargetCoverDays:   c.TargetCoverDays,
		CoveredMultiple:   c.CoveredMultiple,
		MinMarginPct:      c.MinMarginPct,
		FreightISKPerM3:   c.FreightISKPerM3,
		StepOverDaysCover: c.StepOverDaysCover,
		CategoryCeilings:  c.CategoryCeilings,
		SizeAgainstLong:   c.SizesAgainstLong(),
	}
}

// SizesAgainstLong reports whether quantities follow the long window.
//
// It is false whenever no long window is configured, whatever size_against
// says. A campaign that asked to size against a window it is not measuring would
// otherwise size against zero, and a zero rate is unbounded cover -- every row
// would read as already stocked. The settings pair can be inconsistent; the
// plan cannot.
func (c FWCampaign) SizesAgainstLong() bool {
	return c.LongDemandWindowSeconds > 0 && c.SizeAgainst == engine.FWSizedByLong
}

// ShipmentConfig bounds a shipping list with this campaign's hold and headroom.
//
// Capacity is resolved from the stored ship profile name; a campaign with no
// profile gets zero, which turns the cargo bound off and leaves the budget as
// the only limit. That is the honest answer -- an unstated hull is not a hull of
// unlimited size, it is a hull nobody has chosen yet.
func (c FWCampaign) ShipmentConfig(headroomISK float64) engine.FWShipmentConfig {
	return engine.FWShipmentConfig{
		HeadroomISK:     headroomISK,
		CargoCapacityM3: engine.ShipProfileCargoCapacityM3(c.ShipProfile),
		MaxTrips:        c.MaxTrips,
	}
}

// SaveFWCampaign inserts a new campaign or updates an existing one, returning
// the campaign ID.
//
// An update is scoped by user_id as well as campaign_id, so passing another
// user's ID changes nothing and reports that it changed nothing rather than
// silently succeeding.
func (d *DB) SaveFWCampaign(c *FWCampaign) (int64, error) {
	if d == nil || d.sql == nil {
		return 0, fmt.Errorf("no database")
	}
	if c == nil || strings.TrimSpace(c.UserID) == "" {
		return 0, fmt.Errorf("fw campaign: user is required")
	}
	if c.SourceStationID <= 0 {
		c.SourceStationID = jitaIVMoon4StationID
	}
	// Only "long" is long; everything else, empty included, is the short window.
	// The column's own default covers the rows migration v53 found, but a client
	// that never heard of the setting writes an empty string over it, and an empty
	// string in a two-valued column is a third value nothing knows how to read.
	if c.SizeAgainst != engine.FWSizedByLong {
		c.SizeAgainst = engine.FWSizedByShort
	}
	if c.LongDemandWindowSeconds < 0 {
		c.LongDemandWindowSeconds = 0
	}

	now := time.Now().UTC().Format(time.RFC3339)
	c.UpdatedAt = now
	ceilings := marshalJSONOrEmpty(c.CategoryCeilings)
	pinned := marshalJSONOrEmpty(c.PinnedSystems)
	excluded := marshalJSONOrEmpty(c.ExcludedSystems)
	includedTypes := marshalJSONOrEmpty(c.IncludedTypes)
	excludedTypes := marshalJSONOrEmpty(c.ExcludedTypes)

	if c.CampaignID == 0 {
		if c.CreatedAt == "" {
			c.CreatedAt = now
		}
		res, err := d.sql.Exec(`
			INSERT INTO fw_campaigns
			(user_id, name, militia_faction_id, source_station_id, dest_station_id,
			 budget_isk, target_cover_days, covered_multiple, min_margin_pct,
			 freight_isk_per_m3, step_over_days_cover, category_ceilings_json,
			 ship_profile, max_trips, max_jumps_from_front, pinned_systems_json,
			 excluded_systems_json, buyer_character_id, seller_owner_kind,
			 seller_owner_id, long_demand_window_seconds, size_against,
			 included_types_json, excluded_types_json,
			 created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			c.UserID, c.Name, c.MilitiaFactionID, c.SourceStationID, c.DestStationID,
			c.BudgetISK, c.TargetCoverDays, c.CoveredMultiple, c.MinMarginPct,
			c.FreightISKPerM3, c.StepOverDaysCover, ceilings,
			c.ShipProfile, c.MaxTrips, c.MaxJumpsFromFront, pinned,
			excluded, c.BuyerCharacterID, c.SellerOwnerKind,
			c.SellerOwnerID, c.LongDemandWindowSeconds, c.SizeAgainst,
			includedTypes, excludedTypes,
			c.CreatedAt, c.UpdatedAt)
		if err != nil {
			return 0, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return 0, err
		}
		c.CampaignID = id
		return id, nil
	}

	res, err := d.sql.Exec(`
		UPDATE fw_campaigns SET
			name = ?, militia_faction_id = ?, source_station_id = ?, dest_station_id = ?,
			budget_isk = ?, target_cover_days = ?, covered_multiple = ?, min_margin_pct = ?,
			freight_isk_per_m3 = ?, step_over_days_cover = ?, category_ceilings_json = ?,
			ship_profile = ?, max_trips = ?, max_jumps_from_front = ?, pinned_systems_json = ?,
			excluded_systems_json = ?, buyer_character_id = ?, seller_owner_kind = ?,
			seller_owner_id = ?, long_demand_window_seconds = ?, size_against = ?,
			included_types_json = ?, excluded_types_json = ?,
			updated_at = ?
		WHERE campaign_id = ? AND user_id = ?`,
		c.Name, c.MilitiaFactionID, c.SourceStationID, c.DestStationID,
		c.BudgetISK, c.TargetCoverDays, c.CoveredMultiple, c.MinMarginPct,
		c.FreightISKPerM3, c.StepOverDaysCover, ceilings,
		c.ShipProfile, c.MaxTrips, c.MaxJumpsFromFront, pinned,
		excluded, c.BuyerCharacterID, c.SellerOwnerKind,
		c.SellerOwnerID, c.LongDemandWindowSeconds, c.SizeAgainst,
		includedTypes, excludedTypes,
		c.UpdatedAt,
		c.CampaignID, c.UserID)
	if err != nil {
		return 0, err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return 0, fmt.Errorf("fw campaign %d not found for this user", c.CampaignID)
	}
	return c.CampaignID, nil
}

// GetFWCampaign returns one campaign, or nil when it does not exist or belongs
// to someone else. Those two cases are deliberately indistinguishable.
func (d *DB) GetFWCampaign(userID string, campaignID int64) (*FWCampaign, error) {
	if d == nil || d.sql == nil || userID == "" || campaignID == 0 {
		return nil, nil
	}
	row := d.sql.QueryRow(fwCampaignSelect+` WHERE campaign_id = ? AND user_id = ?`, campaignID, userID)
	c, err := scanFWCampaign(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// ListFWCampaigns returns the user's campaigns, most recently touched first.
func (d *DB) ListFWCampaigns(userID string) ([]FWCampaign, error) {
	if d == nil || d.sql == nil || userID == "" {
		return nil, nil
	}
	rows, err := d.sql.Query(fwCampaignSelect+`
		WHERE user_id = ?
		ORDER BY updated_at DESC, campaign_id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FWCampaign
	for rows.Next() {
		c, err := scanFWCampaign(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// DeleteFWCampaign removes a campaign together with its lots and cached plan.
//
// This destroys the spend record, which is the one thing here that cannot be
// rebuilt: after it, nothing knows what was bought for that destination or what
// it cost. The cascade is written out rather than left to a foreign key so that
// is visible at the call site, and the caller is expected to have asked first.
func (d *DB) DeleteFWCampaign(userID string, campaignID int64) error {
	if d == nil || d.sql == nil || userID == "" || campaignID == 0 {
		return nil
	}
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range []string{
		`DELETE FROM fw_plan_cache WHERE campaign_id = ? AND user_id = ?`,
		`DELETE FROM fw_campaign_lots WHERE campaign_id = ? AND user_id = ?`,
		`DELETE FROM fw_campaigns WHERE campaign_id = ? AND user_id = ?`,
	} {
		if _, err := tx.Exec(stmt, campaignID, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const fwCampaignSelect = `
	SELECT campaign_id, user_id, name, militia_faction_id, source_station_id,
	       dest_station_id, budget_isk, target_cover_days, covered_multiple,
	       min_margin_pct, freight_isk_per_m3, step_over_days_cover,
	       category_ceilings_json, ship_profile, max_trips, max_jumps_from_front,
	       pinned_systems_json, excluded_systems_json, buyer_character_id,
	       seller_owner_kind, seller_owner_id, long_demand_window_seconds,
	       size_against, included_types_json, excluded_types_json,
	       created_at, updated_at
	FROM fw_campaigns`

// rowScanner is what *sql.Row and *sql.Rows have in common, so one scan function
// serves both the single-row read and the list.
type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanFWCampaign(row rowScanner) (*FWCampaign, error) {
	var c FWCampaign
	var ceilings, pinned, excluded, includedTypes, excludedTypes string
	err := row.Scan(&c.CampaignID, &c.UserID, &c.Name, &c.MilitiaFactionID, &c.SourceStationID,
		&c.DestStationID, &c.BudgetISK, &c.TargetCoverDays, &c.CoveredMultiple,
		&c.MinMarginPct, &c.FreightISKPerM3, &c.StepOverDaysCover,
		&ceilings, &c.ShipProfile, &c.MaxTrips, &c.MaxJumpsFromFront,
		&pinned, &excluded, &c.BuyerCharacterID,
		&c.SellerOwnerKind, &c.SellerOwnerID, &c.LongDemandWindowSeconds,
		&c.SizeAgainst, &includedTypes, &excludedTypes,
		&c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	// A column that will not parse reads as unset rather than failing the whole
	// campaign: an unreadable pin list should cost the pins, not the budget.
	if ceilings != "" {
		_ = json.Unmarshal([]byte(ceilings), &c.CategoryCeilings)
	}
	if pinned != "" {
		_ = json.Unmarshal([]byte(pinned), &c.PinnedSystems)
	}
	if excluded != "" {
		_ = json.Unmarshal([]byte(excluded), &c.ExcludedSystems)
	}
	if includedTypes != "" {
		_ = json.Unmarshal([]byte(includedTypes), &c.IncludedTypes)
	}
	if excludedTypes != "" {
		_ = json.Unmarshal([]byte(excludedTypes), &c.ExcludedTypes)
	}
	return &c, nil
}

// marshalJSONOrEmpty stores an empty collection as "" rather than "null" or
// "[]", so a column read back by anything that does not parse JSON still reads
// as empty.
func marshalJSONOrEmpty(v interface{}) string {
	switch t := v.(type) {
	case map[string]float64:
		if len(t) == 0 {
			return ""
		}
	case []int32:
		if len(t) == 0 {
			return ""
		}
	case []FWTypeOverride:
		if len(t) == 0 {
			return ""
		}
	case nil:
		return ""
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// SaveFWLot inserts or updates one lot, returning its ID.
//
// The state is validated rather than trusted. An unrecognised state does not
// consume budget, so writing one would hand back headroom for ISK that has
// already gone -- the failure mode is a campaign that believes it can afford a
// second shipment it cannot.
func (d *DB) SaveFWLot(userID string, campaignID int64, lot *engine.FWLot) (int64, error) {
	if d == nil || d.sql == nil {
		return 0, fmt.Errorf("no database")
	}
	if lot == nil || strings.TrimSpace(userID) == "" || campaignID == 0 {
		return 0, fmt.Errorf("fw lot: user and campaign are required")
	}
	if lot.State == "" {
		lot.State = engine.FWLotPlanned
	}
	if !engine.IsFWLotState(lot.State) {
		return 0, fmt.Errorf("fw lot: unknown state %q", lot.State)
	}
	if lot.QtyRemaining > lot.Qty {
		return 0, fmt.Errorf("fw lot: %d remaining of %d bought", lot.QtyRemaining, lot.Qty)
	}
	// A lot that consumes budget with nothing remaining is a contradiction:
	// bought stock exists until it sells, and a sold-out lot belongs in `sold`.
	// Reading it as "all of it" is the only interpretation that does not make
	// real ISK vanish from the committed total.
	if lot.QtyRemaining == 0 && lot.Qty > 0 && lot.ConsumesBudget() {
		lot.QtyRemaining = lot.Qty
	}

	now := time.Now().UTC().Format(time.RFC3339)

	if lot.LotID == 0 {
		res, err := d.sql.Exec(`
			INSERT INTO fw_campaign_lots
			(campaign_id, user_id, type_id, type_name, state, qty, qty_remaining,
			 unit_cost_isk, listed_price, dest_station_id, acquired_by_character_id,
			 holder_owner_kind, holder_owner_id, holder_name, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			campaignID, userID, lot.TypeID, lot.TypeName, lot.State, lot.Qty, lot.QtyRemaining,
			lot.UnitCostISK, lot.ListedPrice, lot.DestStationID, lot.AcquiredByCharacterID,
			lot.HolderOwnerKind, lot.HolderOwnerID, lot.HolderName, now, now)
		if err != nil {
			return 0, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return 0, err
		}
		lot.LotID = id
		return id, nil
	}

	res, err := d.sql.Exec(`
		UPDATE fw_campaign_lots SET
			type_id = ?, type_name = ?, state = ?, qty = ?, qty_remaining = ?,
			unit_cost_isk = ?, listed_price = ?, dest_station_id = ?,
			acquired_by_character_id = ?, holder_owner_kind = ?, holder_owner_id = ?,
			holder_name = ?, updated_at = ?
		WHERE lot_id = ? AND campaign_id = ? AND user_id = ?`,
		lot.TypeID, lot.TypeName, lot.State, lot.Qty, lot.QtyRemaining,
		lot.UnitCostISK, lot.ListedPrice, lot.DestStationID,
		lot.AcquiredByCharacterID, lot.HolderOwnerKind, lot.HolderOwnerID,
		lot.HolderName, now,
		lot.LotID, campaignID, userID)
	if err != nil {
		return 0, err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return 0, fmt.Errorf("fw lot %d not found in campaign %d for this user", lot.LotID, campaignID)
	}
	return lot.LotID, nil
}

// GetFWLots returns a campaign's lots in the order they were created, which is
// the order the handoff happened in.
func (d *DB) GetFWLots(userID string, campaignID int64) ([]engine.FWLot, error) {
	if d == nil || d.sql == nil || userID == "" || campaignID == 0 {
		return nil, nil
	}
	rows, err := d.sql.Query(`
		SELECT lot_id, type_id, type_name, state, qty, qty_remaining, unit_cost_isk,
		       listed_price, dest_station_id, acquired_by_character_id,
		       holder_owner_kind, holder_owner_id, holder_name
		FROM fw_campaign_lots
		WHERE campaign_id = ? AND user_id = ?
		ORDER BY lot_id`, campaignID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []engine.FWLot
	for rows.Next() {
		var lot engine.FWLot
		if err := rows.Scan(&lot.LotID, &lot.TypeID, &lot.TypeName, &lot.State,
			&lot.Qty, &lot.QtyRemaining, &lot.UnitCostISK,
			&lot.ListedPrice, &lot.DestStationID, &lot.AcquiredByCharacterID,
			&lot.HolderOwnerKind, &lot.HolderOwnerID, &lot.HolderName); err != nil {
			return nil, err
		}
		out = append(out, lot)
	}
	return out, rows.Err()
}

// SetFWLotState advances one lot along the handoff without touching anything
// else about it.
//
// Quantities are left alone on purpose. Moving `bought` -> `in_transit` ->
// `at_dest` -> `listed` must not change headroom by one ISK, and the way to
// guarantee that is for the state transition not to be able to write a quantity.
// A fill is a different operation: it changes qty_remaining, so it goes through
// SaveFWLot with the whole lot in hand.
func (d *DB) SetFWLotState(userID string, campaignID, lotID int64, state string) error {
	if d == nil || d.sql == nil || userID == "" || campaignID == 0 || lotID == 0 {
		return nil
	}
	if !engine.IsFWLotState(state) {
		return fmt.Errorf("fw lot: unknown state %q", state)
	}
	res, err := d.sql.Exec(`
		UPDATE fw_campaign_lots SET state = ?, updated_at = ?
		WHERE lot_id = ? AND campaign_id = ? AND user_id = ?`,
		state, time.Now().UTC().Format(time.RFC3339), lotID, campaignID, userID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("fw lot %d not found in campaign %d for this user", lotID, campaignID)
	}
	return nil
}

// DeleteFWLot removes one lot.
//
// This is for a lot that should never have existed -- a mistyped planned row --
// not for one that has been dealt with. A lot whose stock was hauled back or
// written off is `pulled`: that keeps the history of what the campaign spent,
// which deleting the row throws away.
func (d *DB) DeleteFWLot(userID string, campaignID, lotID int64) error {
	if d == nil || d.sql == nil || userID == "" || campaignID == 0 || lotID == 0 {
		return nil
	}
	_, err := d.sql.Exec(`
		DELETE FROM fw_campaign_lots WHERE lot_id = ? AND campaign_id = ? AND user_id = ?`,
		lotID, campaignID, userID)
	return err
}

// SetFWLotStates advances many lots at once, in one transaction.
//
// This is not a convenience wrapper over a loop of SetFWLotState. The budget is
// derived from the lots, so a bulk move that applied to six of ten rows would
// leave headroom describing a position that never existed, with no way to tell
// from the result which six landed. All or nothing is the only answer that keeps
// the budget meaningful.
//
// A lot id that is not this user's lot in this campaign fails the whole batch.
// A stale selection means the caller asked for an end state that has not been
// reached, and a bulk action that quietly does less than it was asked is worse
// than one that does nothing and says so.
func (d *DB) SetFWLotStates(userID string, campaignID int64, lotIDs []int64, state string) error {
	if d == nil || d.sql == nil || userID == "" || campaignID == 0 || len(lotIDs) == 0 {
		return nil
	}
	if !engine.IsFWLotState(state) {
		return fmt.Errorf("fw lot: unknown state %q", state)
	}
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		UPDATE fw_campaign_lots SET state = ?, updated_at = ?
		WHERE lot_id = ? AND campaign_id = ? AND user_id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, lotID := range lotIDs {
		if lotID == 0 {
			return fmt.Errorf("fw lot: lot id is required")
		}
		res, err := stmt.Exec(state, now, lotID, campaignID, userID)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			return fmt.Errorf("fw lot %d not found in campaign %d for this user", lotID, campaignID)
		}
	}
	return tx.Commit()
}

// DeleteFWLots removes many lots at once, in one transaction.
//
// Same all-or-nothing reasoning as SetFWLotStates, and the same warning as
// DeleteFWLot: this is for rows that should never have existed. Stock that was
// hauled back or written off is `pulled`, which keeps the record of what the
// campaign spent.
//
// Unlike SetFWLotStates this does not fail on a lot that is already gone. The
// asymmetry is deliberate: a missing row means the requested end state has
// already been reached here, whereas for a state change it means it has not.
func (d *DB) DeleteFWLots(userID string, campaignID int64, lotIDs []int64) error {
	if d == nil || d.sql == nil || userID == "" || campaignID == 0 || len(lotIDs) == 0 {
		return nil
	}
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(
		`DELETE FROM fw_campaign_lots WHERE lot_id = ? AND campaign_id = ? AND user_id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, lotID := range lotIDs {
		if lotID == 0 {
			return fmt.Errorf("fw lot: lot id is required")
		}
		if _, err := stmt.Exec(lotID, campaignID, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetFWBudget measures a campaign's capital position from its stored lots.
//
// It exists so "the budget comes from the lots, at cost" is written in one place
// rather than reassembled by every caller that wants headroom.
func (d *DB) GetFWBudget(userID string, campaignID int64) (engine.FWBudget, error) {
	campaign, err := d.GetFWCampaign(userID, campaignID)
	if err != nil {
		return engine.FWBudget{}, err
	}
	if campaign == nil {
		return engine.FWBudget{}, fmt.Errorf("fw campaign %d not found for this user", campaignID)
	}
	lots, err := d.GetFWLots(userID, campaignID)
	if err != nil {
		return engine.FWBudget{}, err
	}
	return engine.MeasureFWBudget(campaign.BudgetISK, lots), nil
}

// SaveFWPlan replaces a campaign's cached plan.
func (d *DB) SaveFWPlan(userID string, campaignID int64, generatedAt, payloadJSON string) error {
	if d == nil || d.sql == nil || userID == "" || campaignID == 0 {
		return nil
	}
	if generatedAt == "" {
		generatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := d.sql.Exec(`
		INSERT INTO fw_plan_cache (campaign_id, user_id, generated_at, payload_json)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(campaign_id) DO UPDATE SET
			user_id = excluded.user_id,
			generated_at = excluded.generated_at,
			payload_json = excluded.payload_json`,
		campaignID, userID, generatedAt, payloadJSON)
	return err
}

// GetFWPlan returns the cached plan and when it was built. ok is false when the
// campaign has never been planned, which is the signal to generate one.
func (d *DB) GetFWPlan(userID string, campaignID int64) (payloadJSON, generatedAt string, ok bool) {
	if d == nil || d.sql == nil || userID == "" || campaignID == 0 {
		return "", "", false
	}
	row := d.sql.QueryRow(`
		SELECT payload_json, generated_at FROM fw_plan_cache
		WHERE campaign_id = ? AND user_id = ?`, campaignID, userID)
	if err := row.Scan(&payloadJSON, &generatedAt); err != nil {
		return "", "", false
	}
	return payloadJSON, generatedAt, true
}

// DeleteFWPlan drops the cached plan. The lots are untouched -- that is the
// whole point of the split.
func (d *DB) DeleteFWPlan(userID string, campaignID int64) error {
	if d == nil || d.sql == nil || userID == "" || campaignID == 0 {
		return nil
	}
	_, err := d.sql.Exec(
		`DELETE FROM fw_plan_cache WHERE campaign_id = ? AND user_id = ?`, campaignID, userID)
	return err
}
