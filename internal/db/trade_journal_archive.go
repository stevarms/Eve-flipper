package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"eve-flipper/internal/corp"
	"eve-flipper/internal/esi"
)

// Trade Journal archive: corp wallet transactions/journal + completed
// industry jobs. Mirrors the character wallet writers in wallet_archive.go
// but keyed on (corp_id, division) or (character_id, job_id) as appropriate.
// All writers are UPSERT so re-syncing ESI's rolling window is idempotent
// and old rows outside the window are preserved forever.

const (
	corpWalletArchiveTxnSoftLimit     = 2500
	corpWalletArchiveJournalSoftLimit = 2500
	industryJobsArchiveSoftLimit      = 1000
)

// CorpWalletArchiveWriteStats mirrors WalletArchiveWriteStats but with the
// corp scope discriminator (corporation_id + division).
type CorpWalletArchiveWriteStats struct {
	CorporationID int64
	Division      int
	LiveRows      int
	LimitHit      bool
	SyncedAt      string
}

// IndustryJobArchiveWriteStats reports one sync of completed industry jobs.
type IndustryJobArchiveWriteStats struct {
	CharacterID int64
	LiveRows    int
	LimitHit    bool
	SyncedAt    string
}

// ArchivedTxn is a wallet-source-agnostic transaction row emitted by the
// unified UNION query. WalletKey encodes the source ("char:12345" or
// "corp:98765:3") so the FIFO engine can attribute lots back to a wallet.
type ArchivedTxn struct {
	WalletKey     string
	CharacterID   int64 // 0 when corp
	CorporationID int64 // 0 when character
	Division      int   // 0 when character
	TransactionID int64
	Date          string
	TypeID        int32
	TypeName      string
	LocationID    int64
	LocationName  string
	UnitPrice     float64
	Quantity      int64
	IsBuy         bool
}

// ArchivedJournalEntry is the corresponding UNION result for journal rows,
// with the fee-related fields the FIFO engine cares about. Corp-side rows
// leave the character-only fields (Tax, TaxReceiverID, ContextID,
// ContextIDType, Reason) at their zero values since ESI's corp endpoint
// doesn't return them.
type ArchivedJournalEntry struct {
	WalletKey     string
	CharacterID   int64
	CorporationID int64
	Division      int
	EntryID       int64
	Date          string
	RefType       string
	Amount        float64
	Tax           float64
	ContextID     int64
	ContextIDType string // decrypted before return
}

// ArchivedIndustryJob mirrors CharacterIndustryJob columns we archive.
type ArchivedIndustryJob struct {
	UserID          string
	CharacterID     int64
	JobID           int64
	ActivityID      int32
	BlueprintTypeID int32
	ProductTypeID   int32
	Runs            int32
	InstallCost     float64
	Status          string
	StartDate       string
	EndDate         string
	CompletedDate   string
	SuccessfulRuns  int32
	ProductTypeName string
	ExternalJobID   int64
}

// WalletScopeFilter selects which wallets to UNION into the compute input.
// Empty (with IncludeAll=false) → no rows. Use IncludeAll=true to mean
// "every wallet authorized to this user".
type WalletScopeFilter struct {
	IncludeAll           bool
	IncludeCharacters    []int64
	IncludeCorpDivisions []CorpDivisionKey
}

type CorpDivisionKey struct {
	CorporationID int64
	Division      int
}

// UpsertCorpWalletTransactionsForUser stores one page of corp wallet
// transactions for a given (corporation, division) into the archive.
func (d *DB) UpsertCorpWalletTransactionsForUser(userID string, corporationID int64, division int, txns []corp.CorpTransaction) (CorpWalletArchiveWriteStats, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" || corporationID <= 0 || division < 1 || division > 7 {
		return CorpWalletArchiveWriteStats{}, fmt.Errorf("invalid corp wallet transaction archive scope")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	stats := CorpWalletArchiveWriteStats{
		CorporationID: corporationID,
		Division:      division,
		LiveRows:      len(txns),
		LimitHit:      len(txns) >= corpWalletArchiveTxnSoftLimit,
		SyncedAt:      now,
	}

	tx, err := d.sql.Begin()
	if err != nil {
		return stats, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO corp_wallet_transactions_archive (
			user_id, corporation_id, division, transaction_id, date, type_id,
			location_id, unit_price, quantity, is_buy, type_name, location_name,
			first_seen_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, corporation_id, division, transaction_id) DO UPDATE SET
			date = excluded.date,
			type_id = excluded.type_id,
			location_id = excluded.location_id,
			unit_price = excluded.unit_price,
			quantity = excluded.quantity,
			is_buy = excluded.is_buy,
			type_name = CASE WHEN excluded.type_name != '' THEN excluded.type_name ELSE corp_wallet_transactions_archive.type_name END,
			location_name = CASE WHEN excluded.location_name != '' THEN excluded.location_name ELSE corp_wallet_transactions_archive.location_name END,
			last_seen_at = excluded.last_seen_at
	`)
	if err != nil {
		return stats, err
	}
	defer stmt.Close()

	for _, row := range txns {
		if row.TransactionID == 0 || strings.TrimSpace(row.Date) == "" {
			continue
		}
		isBuy := 0
		if row.IsBuy {
			isBuy = 1
		}
		if _, err := stmt.Exec(
			userID,
			corporationID,
			division,
			row.TransactionID,
			row.Date,
			row.TypeID,
			row.LocationID,
			row.UnitPrice,
			row.Quantity,
			isBuy,
			row.TypeName,
			row.LocationName,
			now,
			now,
		); err != nil {
			return stats, err
		}
	}

	if _, err := tx.Exec(`
		INSERT INTO corp_wallet_archive_sync (
			user_id, corporation_id, division, transaction_synced_at,
			transaction_live_count, transaction_limit_hit, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, corporation_id, division) DO UPDATE SET
			transaction_synced_at = excluded.transaction_synced_at,
			transaction_live_count = excluded.transaction_live_count,
			transaction_limit_hit = excluded.transaction_limit_hit,
			updated_at = excluded.updated_at
	`, userID, corporationID, division, now, len(txns), boolInt(stats.LimitHit), now); err != nil {
		return stats, err
	}

	return stats, tx.Commit()
}

// UpsertCorpWalletJournalForUser stores corp journal entries into the archive.
func (d *DB) UpsertCorpWalletJournalForUser(userID string, corporationID int64, division int, entries []corp.CorpJournalEntry) (CorpWalletArchiveWriteStats, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" || corporationID <= 0 || division < 1 || division > 7 {
		return CorpWalletArchiveWriteStats{}, fmt.Errorf("invalid corp wallet journal archive scope")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	stats := CorpWalletArchiveWriteStats{
		CorporationID: corporationID,
		Division:      division,
		LiveRows:      len(entries),
		LimitHit:      len(entries) >= corpWalletArchiveJournalSoftLimit,
		SyncedAt:      now,
	}

	// Description is user-private in the character journal; corp journal
	// data is arguably less sensitive but we mirror the protection to keep
	// the vault surface consistent.
	storedEntries := make([]corp.CorpJournalEntry, 0, len(entries))
	for _, row := range entries {
		if row.ID == 0 || strings.TrimSpace(row.Date) == "" {
			continue
		}
		desc, err := d.protectPrivateString(userID, "corp_wallet_journal_archive.description", row.Description)
		if err != nil {
			return stats, err
		}
		row.Description = desc
		storedEntries = append(storedEntries, row)
	}

	tx, err := d.sql.Begin()
	if err != nil {
		return stats, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO corp_wallet_journal_archive (
			user_id, corporation_id, division, entry_id, date, ref_type,
			first_party_id, second_party_id, amount, balance, reason,
			description, tax, tax_receiver_id, context_id, context_id_type,
			first_seen_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, corporation_id, division, entry_id) DO UPDATE SET
			date = excluded.date,
			ref_type = excluded.ref_type,
			first_party_id = excluded.first_party_id,
			second_party_id = excluded.second_party_id,
			amount = excluded.amount,
			balance = excluded.balance,
			description = excluded.description,
			last_seen_at = excluded.last_seen_at
	`)
	if err != nil {
		return stats, err
	}
	defer stmt.Close()

	for _, row := range storedEntries {
		if _, err := stmt.Exec(
			userID,
			corporationID,
			division,
			row.ID,
			row.Date,
			row.RefType,
			row.FirstPartyID,
			row.SecondPartyID,
			row.Amount,
			row.Balance,
			"", // reason (not in corp shape)
			row.Description,
			0.0, // tax (not in corp shape)
			0,   // tax_receiver_id
			0,   // context_id
			"",  // context_id_type
			now,
			now,
		); err != nil {
			return stats, err
		}
	}

	if _, err := tx.Exec(`
		INSERT INTO corp_wallet_archive_sync (
			user_id, corporation_id, division, journal_synced_at,
			journal_live_count, journal_limit_hit, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, corporation_id, division) DO UPDATE SET
			journal_synced_at = excluded.journal_synced_at,
			journal_live_count = excluded.journal_live_count,
			journal_limit_hit = excluded.journal_limit_hit,
			updated_at = excluded.updated_at
	`, userID, corporationID, division, now, len(entries), boolInt(stats.LimitHit), now); err != nil {
		return stats, err
	}

	return stats, tx.Commit()
}

// UpsertIndustryJobsForUser stores completed industry jobs into the archive.
// Callers should filter to jobs whose Status == "delivered" (or "cancelled"
// if they want to track failed ones) and pass only those in the slice —
// active jobs shouldn't land in this table.
func (d *DB) UpsertIndustryJobsForUser(userID string, characterID int64, jobs []esi.CharacterIndustryJob) (IndustryJobArchiveWriteStats, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" || characterID <= 0 {
		return IndustryJobArchiveWriteStats{}, fmt.Errorf("invalid industry job archive scope")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	stats := IndustryJobArchiveWriteStats{
		CharacterID: characterID,
		LiveRows:    len(jobs),
		LimitHit:    len(jobs) >= industryJobsArchiveSoftLimit,
		SyncedAt:    now,
	}

	tx, err := d.sql.Begin()
	if err != nil {
		return stats, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO industry_jobs_archive (
			user_id, character_id, job_id, activity_id, blueprint_type_id,
			product_type_id, runs, install_cost, status, start_date, end_date,
			completed_date, successful_runs, product_type_name,
			first_seen_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, character_id, job_id) DO UPDATE SET
			activity_id = excluded.activity_id,
			blueprint_type_id = excluded.blueprint_type_id,
			product_type_id = excluded.product_type_id,
			runs = excluded.runs,
			install_cost = excluded.install_cost,
			status = excluded.status,
			start_date = excluded.start_date,
			end_date = excluded.end_date,
			completed_date = excluded.completed_date,
			successful_runs = excluded.successful_runs,
			product_type_name = CASE WHEN excluded.product_type_name != '' THEN excluded.product_type_name ELSE industry_jobs_archive.product_type_name END,
			last_seen_at = excluded.last_seen_at
	`)
	if err != nil {
		return stats, err
	}
	defer stmt.Close()

	for _, job := range jobs {
		if job.JobID == 0 {
			continue
		}
		if _, err := stmt.Exec(
			userID,
			characterID,
			job.JobID,
			job.ActivityID,
			job.BlueprintTypeID,
			job.ProductTypeID,
			job.Runs,
			job.Cost,
			job.Status,
			job.StartDate,
			job.EndDate,
			job.CompletedDate,
			job.SuccessfulRuns,
			job.ProductTypeName,
			now,
			now,
		); err != nil {
			return stats, err
		}
	}

	if _, err := tx.Exec(`
		INSERT INTO wallet_archive_sync (
			user_id, character_id, industry_synced_at, industry_live_count, updated_at
		) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id, character_id) DO UPDATE SET
			industry_synced_at = excluded.industry_synced_at,
			industry_live_count = excluded.industry_live_count,
			updated_at = excluded.updated_at
	`, userID, characterID, now, len(jobs), now); err != nil {
		return stats, err
	}

	return stats, tx.Commit()
}

// SetIndustryJobExternalLink updates a ledger IndustryJob row's
// external_job_id so the FIFO engine can look up the user's planned ME/TE
// via the ledger. Used by both the manual-link UI and the auto-heuristic
// reconciliation pass.
func (d *DB) SetIndustryJobExternalLink(userID string, ledgerJobID int64, esiJobID int64) error {
	userID = strings.TrimSpace(userID)
	if userID == "" || ledgerJobID <= 0 || esiJobID <= 0 {
		return fmt.Errorf("invalid industry job external-link scope")
	}
	res, err := d.sql.Exec(`
		UPDATE industry_jobs SET external_job_id = ?, updated_at = ?
		WHERE user_id = ? AND id = ?
	`, esiJobID, time.Now().UTC().Format(time.RFC3339), userID, ledgerJobID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("industry_jobs row %d not found for user", ledgerJobID)
	}
	return nil
}

// ListArchivedWalletActivityForUser returns transactions + journal entries
// from every wallet in the scope, ordered by (date ASC, transaction_id ASC)
// for txns and (date ASC, entry_id ASC) for journal. FIFO ingest happy path.
func (d *DB) ListArchivedWalletActivityForUser(userID string, filter WalletScopeFilter, since time.Time) ([]ArchivedTxn, []ArchivedJournalEntry, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, nil, fmt.Errorf("empty userID")
	}

	sinceStr := ""
	if !since.IsZero() {
		sinceStr = since.UTC().Format(time.RFC3339)
	}

	txns, err := d.queryArchivedTxns(userID, filter, sinceStr)
	if err != nil {
		return nil, nil, err
	}
	journal, err := d.queryArchivedJournal(userID, filter, sinceStr)
	if err != nil {
		return nil, nil, err
	}
	return txns, journal, nil
}

func (d *DB) queryArchivedTxns(userID string, filter WalletScopeFilter, sinceStr string) ([]ArchivedTxn, error) {
	charScope, corpScope, hasAny := buildWalletScopeSQL(filter)
	if !hasAny {
		return nil, nil
	}

	var parts []string
	var args []any

	if charScope != "" {
		q := `SELECT character_id, transaction_id, date, type_id, location_id,
			unit_price, quantity, is_buy, type_name, location_name
		FROM wallet_transactions_archive
		WHERE user_id = ? AND ` + charScope
		if sinceStr != "" {
			q += ` AND date >= ?`
			args = append(args, userID)
			args = append(args, sinceStr)
		} else {
			args = append(args, userID)
		}
		parts = append(parts, q)
	}
	if corpScope != "" {
		q := `SELECT 0 AS character_id, corporation_id, division, transaction_id, date, type_id,
			location_id, unit_price, quantity, is_buy, type_name, location_name
		FROM corp_wallet_transactions_archive
		WHERE user_id = ? AND ` + corpScope
		if sinceStr != "" {
			q += ` AND date >= ?`
			args = append(args, userID)
			args = append(args, sinceStr)
		} else {
			args = append(args, userID)
		}
		parts = append(parts, q)
	}

	// Character and corp rows have different column counts, so run them as
	// separate queries and merge in Go instead of a UNION with padding.
	out := make([]ArchivedTxn, 0)
	if charScope != "" {
		rows, err := d.sql.Query(parts[0], sliceFirst(args, charScope != "", sinceStr != "")...)
		if err != nil {
			return nil, err
		}
		if err := readCharTxnRows(rows, &out); err != nil {
			return nil, err
		}
	}
	if corpScope != "" {
		start := 0
		if charScope != "" {
			start = 1
		}
		rows, err := d.sql.Query(parts[start], sliceSecond(args, charScope != "", corpScope != "", sinceStr != "")...)
		if err != nil {
			return nil, err
		}
		if err := readCorpTxnRows(rows, &out); err != nil {
			return nil, err
		}
	}

	sortArchivedTxns(out)
	return out, nil
}

func (d *DB) queryArchivedJournal(userID string, filter WalletScopeFilter, sinceStr string) ([]ArchivedJournalEntry, error) {
	charScope, corpScope, hasAny := buildWalletScopeSQL(filter)
	if !hasAny {
		return nil, nil
	}
	out := make([]ArchivedJournalEntry, 0)

	if charScope != "" {
		q := `SELECT character_id, entry_id, date, ref_type, amount, tax, context_id, context_id_type
			FROM wallet_journal_archive
			WHERE user_id = ? AND ` + charScope
		args := []any{userID}
		if sinceStr != "" {
			q += ` AND date >= ?`
			args = append(args, sinceStr)
		}
		rows, err := d.sql.Query(q, args...)
		if err != nil {
			return nil, err
		}
		if err := readCharJournalRows(rows, &out, d, userID); err != nil {
			return nil, err
		}
	}
	if corpScope != "" {
		q := `SELECT corporation_id, division, entry_id, date, ref_type, amount
			FROM corp_wallet_journal_archive
			WHERE user_id = ? AND ` + corpScope
		args := []any{userID}
		if sinceStr != "" {
			q += ` AND date >= ?`
			args = append(args, sinceStr)
		}
		rows, err := d.sql.Query(q, args...)
		if err != nil {
			return nil, err
		}
		if err := readCorpJournalRows(rows, &out); err != nil {
			return nil, err
		}
	}

	sortArchivedJournal(out)
	return out, nil
}

// ListArchivedIndustryJobsForUser returns completed industry jobs for the
// given characters (empty = all authorized), completed on or after `since`.
func (d *DB) ListArchivedIndustryJobsForUser(userID string, characterIDs []int64, since time.Time) ([]ArchivedIndustryJob, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("empty userID")
	}
	q := `SELECT character_id, job_id, activity_id, blueprint_type_id,
		product_type_id, runs, install_cost, status, start_date, end_date,
		completed_date, successful_runs, product_type_name, external_job_id
		FROM industry_jobs_archive WHERE user_id = ?`
	args := []any{userID}
	if len(characterIDs) > 0 {
		placeholders := strings.Repeat("?,", len(characterIDs))
		placeholders = placeholders[:len(placeholders)-1]
		q += " AND character_id IN (" + placeholders + ")"
		for _, id := range characterIDs {
			args = append(args, id)
		}
	}
	if !since.IsZero() {
		q += " AND (completed_date >= ? OR start_date >= ?)"
		s := since.UTC().Format(time.RFC3339)
		args = append(args, s, s)
	}
	q += " ORDER BY completed_date ASC, start_date ASC, job_id ASC"

	rows, err := d.sql.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ArchivedIndustryJob, 0)
	for rows.Next() {
		var j ArchivedIndustryJob
		j.UserID = userID
		if err := rows.Scan(
			&j.CharacterID, &j.JobID, &j.ActivityID, &j.BlueprintTypeID,
			&j.ProductTypeID, &j.Runs, &j.InstallCost, &j.Status,
			&j.StartDate, &j.EndDate, &j.CompletedDate,
			&j.SuccessfulRuns, &j.ProductTypeName, &j.ExternalJobID,
		); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// --- scope SQL builders + row readers (implementation detail) ---

func buildWalletScopeSQL(filter WalletScopeFilter) (charScope, corpScope string, hasAny bool) {
	if filter.IncludeAll {
		return "1=1", "1=1", true
	}
	if len(filter.IncludeCharacters) > 0 {
		charScope = "character_id IN (" + int64Placeholders(len(filter.IncludeCharacters)) + ")"
		hasAny = true
	}
	if len(filter.IncludeCorpDivisions) > 0 {
		parts := make([]string, len(filter.IncludeCorpDivisions))
		for i := range filter.IncludeCorpDivisions {
			parts[i] = "(corporation_id = ? AND division = ?)"
		}
		corpScope = strings.Join(parts, " OR ")
		hasAny = true
	}
	return
}

func int64Placeholders(n int) string {
	if n == 0 {
		return ""
	}
	return strings.Repeat("?,", n)[:2*n-1]
}

// sliceFirst / sliceSecond exist so buildWalletScopeSQL can be assembled
// with args in one flat slice — kept private to avoid leaking helpers.
func sliceFirst(args []any, _, _ bool) []any {
	// The first (char) query always uses args[0:] up to and including its
	// bindings. Since we build args in order (userID, sinceStr) for each
	// query, we consume from index 0.
	return args
}

func sliceSecond(args []any, hasChar, _, _ bool) []any {
	// If both scopes present, corp args start after char args.
	if !hasChar {
		return args
	}
	// char has: userID + optional sinceStr
	skip := 1
	// This helper is only called when args includes both blocks; the
	// caller ensured that. Returning args[skip:] slices past the char query's args.
	return args[skip:]
}

func readCharTxnRows(rows *sql.Rows, out *[]ArchivedTxn) error {
	defer rows.Close()
	for rows.Next() {
		var r ArchivedTxn
		var isBuy int64
		if err := rows.Scan(
			&r.CharacterID, &r.TransactionID, &r.Date, &r.TypeID, &r.LocationID,
			&r.UnitPrice, &r.Quantity, &isBuy, &r.TypeName, &r.LocationName,
		); err != nil {
			return err
		}
		r.IsBuy = isBuy != 0
		r.WalletKey = fmt.Sprintf("char:%d", r.CharacterID)
		*out = append(*out, r)
	}
	return rows.Err()
}

func readCorpTxnRows(rows *sql.Rows, out *[]ArchivedTxn) error {
	defer rows.Close()
	for rows.Next() {
		var r ArchivedTxn
		var isBuy int64
		if err := rows.Scan(
			&r.CharacterID, &r.CorporationID, &r.Division, &r.TransactionID,
			&r.Date, &r.TypeID, &r.LocationID, &r.UnitPrice, &r.Quantity,
			&isBuy, &r.TypeName, &r.LocationName,
		); err != nil {
			return err
		}
		r.IsBuy = isBuy != 0
		r.WalletKey = fmt.Sprintf("corp:%d:%d", r.CorporationID, r.Division)
		*out = append(*out, r)
	}
	return rows.Err()
}

func readCharJournalRows(rows *sql.Rows, out *[]ArchivedJournalEntry, d *DB, userID string) error {
	defer rows.Close()
	for rows.Next() {
		var r ArchivedJournalEntry
		var contextIDType string
		if err := rows.Scan(
			&r.CharacterID, &r.EntryID, &r.Date, &r.RefType, &r.Amount,
			&r.Tax, &r.ContextID, &contextIDType,
		); err != nil {
			return err
		}
		// context_id_type is protectPrivateString-wrapped in v34+ writers.
		if plain, err := d.openPrivateString(userID, "wallet_journal_archive.context_id_type", contextIDType); err == nil {
			r.ContextIDType = plain
		} else {
			r.ContextIDType = contextIDType
		}
		r.WalletKey = fmt.Sprintf("char:%d", r.CharacterID)
		*out = append(*out, r)
	}
	return rows.Err()
}

func readCorpJournalRows(rows *sql.Rows, out *[]ArchivedJournalEntry) error {
	defer rows.Close()
	for rows.Next() {
		var r ArchivedJournalEntry
		if err := rows.Scan(
			&r.CorporationID, &r.Division, &r.EntryID, &r.Date, &r.RefType, &r.Amount,
		); err != nil {
			return err
		}
		r.WalletKey = fmt.Sprintf("corp:%d:%d", r.CorporationID, r.Division)
		*out = append(*out, r)
	}
	return rows.Err()
}

func sortArchivedTxns(rows []ArchivedTxn) {
	// FIFO wants (date ASC, transaction_id ASC).
	sortStable(rows, func(a, b ArchivedTxn) bool {
		if a.Date != b.Date {
			return a.Date < b.Date
		}
		return a.TransactionID < b.TransactionID
	})
}

func sortArchivedJournal(rows []ArchivedJournalEntry) {
	sortStable(rows, func(a, b ArchivedJournalEntry) bool {
		if a.Date != b.Date {
			return a.Date < b.Date
		}
		return a.EntryID < b.EntryID
	})
}

// sortStable is a small generic wrapper so tests can rely on stable order.
func sortStable[T any](s []T, less func(a, b T) bool) {
	// Simple insertion sort — fine for the row counts here (thousands
	// pre-filtered by scope) and avoids importing sort into the file.
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && less(s[j], s[j-1]); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
