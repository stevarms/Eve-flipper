package db

import (
	"strconv"
	"sync"
	"time"
)

// Order-book archiving is a process-wide switch, not a per-user preference:
// one ESI client fills one shared archive, so it lives under the default user
// rather than in config.Config. Keeping it out of config.Config also means an
// ordinary per-user settings save can't flip it by omission.
const orderBookRecordingKey = "orderbook_recording"

// OrderBookRecordingEnabled reports whether order-book archiving is switched
// on. Defaults to true: what makes archiving affordable is the interest filter
// and the retention sweep below, not refusing to archive at all. An unreadable
// or malformed value falls back to the default rather than silently disabling
// a feature the user never turned off.
func (d *DB) OrderBookRecordingEnabled() bool {
	if d == nil || d.sql == nil {
		return false
	}
	var raw string
	err := d.sql.QueryRow(
		"SELECT value FROM config WHERE user_id = ? AND key = ?",
		DefaultUserID, orderBookRecordingKey,
	).Scan(&raw)
	if err != nil {
		return orderBookRecordingDefault
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return orderBookRecordingDefault
	}
	return enabled
}

// orderBookRecordingDefault is the switch's value before anyone sets it.
const orderBookRecordingDefault = true

// SetOrderBookRecordingEnabled persists the archiving switch.
func (d *DB) SetOrderBookRecordingEnabled(enabled bool) error {
	if d == nil || d.sql == nil {
		return nil
	}
	_, err := d.sql.Exec(
		"INSERT OR REPLACE INTO config (user_id, key, value) VALUES (?, ?, ?)",
		DefaultUserID, orderBookRecordingKey, strconv.FormatBool(enabled),
	)
	// Switching archiving on is exactly when a stale interest set would hurt:
	// the next scan is the one that fills the archive.
	InvalidateOrderBookInterest()
	return err
}

// --- what is worth archiving -------------------------------------------------
//
// A region book covers every type on the market — about 19,000 of them — but a
// trader only ever replays the few hundred they actually deal in. Archiving the
// rest is the single biggest source of growth in this table: measured against a
// real 19.5M-level archive, the types the owner had actually traded accounted
// for 4.3% of the rows. So the write path keeps levels for types you have
// bought, sold, planned, stocked or watched, and drops the rest on the floor
// before they ever reach SQLite.
//
// This narrows what can be replayed, and that is the trade: a type you have
// never touched has no recorded book until you touch it once.

// orderBookInterestTTL is how long a resolved interest set is reused. The set
// only changes when you trade something new, and re-running six queries for
// every snapshot in a scan would put them on the hot path.
const orderBookInterestTTL = 5 * time.Minute

var (
	orderBookInterestMu      sync.Mutex
	orderBookInterestTypes   map[int32]bool
	orderBookInterestExpires time.Time
)

// orderBookInterestQueries are unioned into the archive's type set. Each is a
// different sense of "I deal in this": traded it, plan to, hold it, build with
// it, or watch it.
var orderBookInterestQueries = []string{
	"SELECT DISTINCT type_id FROM wallet_transactions_archive",
	"SELECT DISTINCT type_id FROM corp_wallet_transactions_archive",
	"SELECT DISTINCT type_id FROM watchlist",
	"SELECT DISTINCT type_id FROM paper_trades",
	"SELECT DISTINCT type_id FROM stockpile_items",
	"SELECT DISTINCT type_id FROM user_trade_state",
	"SELECT DISTINCT type_id FROM industry_material_plan",
}

// InvalidateOrderBookInterest drops the cached interest set so the next
// snapshot re-resolves it. Call after anything that could widen it.
func InvalidateOrderBookInterest() {
	orderBookInterestMu.Lock()
	orderBookInterestTypes = nil
	orderBookInterestExpires = time.Time{}
	orderBookInterestMu.Unlock()
}

// OrderBookInterestTypes returns the set of type IDs worth archiving.
//
// An empty set means "we do not know what you trade yet" and archives nothing.
// That is deliberate: falling back to archiving everything would hand a brand
// new install the exact multi-gigabyte behaviour this filter exists to stop,
// and it would do it to the one user who has no use for the data.
func (d *DB) OrderBookInterestTypes() map[int32]bool {
	if d == nil || d.sql == nil {
		return nil
	}
	orderBookInterestMu.Lock()
	defer orderBookInterestMu.Unlock()
	if orderBookInterestTypes != nil && time.Now().Before(orderBookInterestExpires) {
		return orderBookInterestTypes
	}

	types := make(map[int32]bool)
	for _, query := range orderBookInterestQueries {
		rows, err := d.sql.Query(query)
		if err != nil {
			// A missing or unreadable source narrows the set; it must not
			// widen it into "archive everything".
			continue
		}
		for rows.Next() {
			var typeID int32
			if err := rows.Scan(&typeID); err == nil && typeID > 0 {
				types[typeID] = true
			}
		}
		rows.Close()
	}

	orderBookInterestTypes = types
	orderBookInterestExpires = time.Now().Add(orderBookInterestTTL)
	return types
}
