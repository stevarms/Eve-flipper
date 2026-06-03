package db

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"eve-flipper/internal/esi"
)

const walletArchiveESITransactionSoftLimit = 2500
const walletArchiveESIJournalSoftLimit = 2500

// WalletArchiveWriteStats describes a single live ESI ingest into the archive.
type WalletArchiveWriteStats struct {
	CharacterID int64
	LiveRows    int
	LimitHit    bool
	SyncedAt    string
}

// WalletArchiveStats describes archived wallet coverage for one query scope.
type WalletArchiveStats struct {
	Characters              int      `json:"characters"`
	TransactionRows         int      `json:"transaction_rows"`
	JournalRows             int      `json:"journal_rows"`
	TransactionTurnoverISK  float64  `json:"transaction_turnover_isk"`
	LiveTransactionRows     int      `json:"live_transaction_rows"`
	LiveJournalRows         int      `json:"live_journal_rows"`
	OldestTransactionDate   string   `json:"oldest_transaction_date,omitempty"`
	NewestTransactionDate   string   `json:"newest_transaction_date,omitempty"`
	OldestJournalDate       string   `json:"oldest_journal_date,omitempty"`
	NewestJournalDate       string   `json:"newest_journal_date,omitempty"`
	LastTransactionSync     string   `json:"last_transaction_sync,omitempty"`
	LastJournalSync         string   `json:"last_journal_sync,omitempty"`
	TransactionLimitHit     bool     `json:"transaction_limit_hit"`
	JournalLimitHit         bool     `json:"journal_limit_hit"`
	ArchiveTransactionLimit int      `json:"archive_transaction_limit"`
	ArchiveJournalLimit     int      `json:"archive_journal_limit"`
	CharacterIDs            []int64  `json:"character_ids,omitempty"`
	Warnings                []string `json:"warnings,omitempty"`
}

// CharacterPrivateMetrics stores small current-account metrics that are
// sensitive but do not need SQL filtering.
type CharacterPrivateMetrics struct {
	CharacterID      int64
	WalletBalance    float64
	WalletBalanceAt  string
	HasWalletBalance bool
	TotalSP          int64
	TotalSPAt        string
	HasTotalSP       bool
}

// UpsertWalletTransactionsForUser stores the latest ESI wallet transaction page.
func (d *DB) UpsertWalletTransactionsForUser(userID string, characterID int64, txns []esi.WalletTransaction) (WalletArchiveWriteStats, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" || characterID <= 0 {
		return WalletArchiveWriteStats{}, fmt.Errorf("invalid wallet transaction archive scope")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	stats := WalletArchiveWriteStats{
		CharacterID: characterID,
		LiveRows:    len(txns),
		LimitHit:    len(txns) >= walletArchiveESITransactionSoftLimit,
		SyncedAt:    now,
	}

	tx, err := d.sql.Begin()
	if err != nil {
		return stats, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO wallet_transactions_archive (
			user_id, character_id, transaction_id, date, type_id, location_id,
			unit_price, quantity, is_buy, type_name, location_name, first_seen_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, character_id, transaction_id) DO UPDATE SET
			date = excluded.date,
			type_id = excluded.type_id,
			location_id = excluded.location_id,
			unit_price = excluded.unit_price,
			quantity = excluded.quantity,
			is_buy = excluded.is_buy,
			type_name = CASE WHEN excluded.type_name != '' THEN excluded.type_name ELSE wallet_transactions_archive.type_name END,
			location_name = CASE WHEN excluded.location_name != '' THEN excluded.location_name ELSE wallet_transactions_archive.location_name END,
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
			characterID,
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
		INSERT INTO wallet_archive_sync (
			user_id, character_id, transaction_synced_at, transaction_live_count,
			transaction_limit_hit, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, character_id) DO UPDATE SET
			transaction_synced_at = excluded.transaction_synced_at,
			transaction_live_count = excluded.transaction_live_count,
			transaction_limit_hit = excluded.transaction_limit_hit,
			updated_at = excluded.updated_at
	`, userID, characterID, now, len(txns), boolInt(stats.LimitHit), now); err != nil {
		return stats, err
	}

	return stats, tx.Commit()
}

// UpsertWalletJournalForUser stores all ESI wallet journal rows returned by the current sync.
func (d *DB) UpsertWalletJournalForUser(userID string, characterID int64, entries []esi.WalletJournalEntry) (WalletArchiveWriteStats, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" || characterID <= 0 {
		return WalletArchiveWriteStats{}, fmt.Errorf("invalid wallet journal archive scope")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	stats := WalletArchiveWriteStats{
		CharacterID: characterID,
		LiveRows:    len(entries),
		LimitHit:    len(entries) >= walletArchiveESIJournalSoftLimit,
		SyncedAt:    now,
	}

	storedEntries := make([]esi.WalletJournalEntry, 0, len(entries))
	for _, row := range entries {
		if row.ID == 0 || strings.TrimSpace(row.Date) == "" {
			continue
		}
		reason, err := d.protectPrivateString(userID, "wallet_journal_archive.reason", row.Reason)
		if err != nil {
			return stats, err
		}
		description, err := d.protectPrivateString(userID, "wallet_journal_archive.description", row.Description)
		if err != nil {
			return stats, err
		}
		contextIDType, err := d.protectPrivateString(userID, "wallet_journal_archive.context_id_type", row.ContextIDType)
		if err != nil {
			return stats, err
		}
		row.Reason = reason
		row.Description = description
		row.ContextIDType = contextIDType
		storedEntries = append(storedEntries, row)
	}

	tx, err := d.sql.Begin()
	if err != nil {
		return stats, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO wallet_journal_archive (
			user_id, character_id, entry_id, date, ref_type, first_party_id,
			second_party_id, amount, balance, reason, description, tax,
			tax_receiver_id, context_id, context_id_type, first_seen_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, character_id, entry_id) DO UPDATE SET
			date = excluded.date,
			ref_type = excluded.ref_type,
			first_party_id = excluded.first_party_id,
			second_party_id = excluded.second_party_id,
			amount = excluded.amount,
			balance = excluded.balance,
			reason = excluded.reason,
			description = excluded.description,
			tax = excluded.tax,
			tax_receiver_id = excluded.tax_receiver_id,
			context_id = excluded.context_id,
			context_id_type = excluded.context_id_type,
			last_seen_at = excluded.last_seen_at
	`)
	if err != nil {
		return stats, err
	}
	defer stmt.Close()

	for _, row := range storedEntries {
		if _, err := stmt.Exec(
			userID,
			characterID,
			row.ID,
			row.Date,
			row.RefType,
			row.FirstPartyID,
			row.SecondPartyID,
			row.Amount,
			row.Balance,
			row.Reason,
			row.Description,
			row.Tax,
			row.TaxReceiverID,
			row.ContextID,
			row.ContextIDType,
			now,
			now,
		); err != nil {
			return stats, err
		}
	}

	if _, err := tx.Exec(`
		INSERT INTO wallet_archive_sync (
			user_id, character_id, journal_synced_at, journal_live_count,
			journal_limit_hit, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, character_id) DO UPDATE SET
			journal_synced_at = excluded.journal_synced_at,
			journal_live_count = excluded.journal_live_count,
			journal_limit_hit = excluded.journal_limit_hit,
			updated_at = excluded.updated_at
	`, userID, characterID, now, len(entries), boolInt(stats.LimitHit), now); err != nil {
		return stats, err
	}

	return stats, tx.Commit()
}

// UpdateWalletArchiveBalance stores the latest wallet balance metadata.
func (d *DB) UpdateWalletArchiveBalance(userID string, characterID int64, balance float64) error {
	userID = strings.TrimSpace(userID)
	if userID == "" || characterID <= 0 {
		return fmt.Errorf("invalid wallet balance archive scope")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	legacyBalance := balance
	protectedBalance := ""
	if d != nil && d.privacy != nil {
		var err error
		protectedBalance, err = d.protectPrivateString(userID, "wallet_archive_sync.wallet_balance", strconv.FormatFloat(balance, 'f', -1, 64))
		if err != nil {
			return err
		}
		legacyBalance = 0
	}
	_, err := d.sql.Exec(`
		INSERT INTO wallet_archive_sync (
			user_id, character_id, wallet_balance, wallet_balance_private, wallet_balance_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, character_id) DO UPDATE SET
			wallet_balance = excluded.wallet_balance,
			wallet_balance_private = excluded.wallet_balance_private,
			wallet_balance_at = excluded.wallet_balance_at,
			updated_at = excluded.updated_at
	`, userID, characterID, legacyBalance, protectedBalance, now, now)
	return err
}

// UpdateCharacterTotalSP stores the latest total SP as an encrypted current metric.
func (d *DB) UpdateCharacterTotalSPForUser(userID string, characterID int64, totalSP int64) error {
	userID = strings.TrimSpace(userID)
	if userID == "" || characterID <= 0 {
		return fmt.Errorf("invalid character SP archive scope")
	}
	if d == nil || d.privacy == nil {
		return nil
	}
	protectedSP, err := d.protectPrivateString(userID, "wallet_archive_sync.total_sp", strconv.FormatInt(totalSP, 10))
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = d.sql.Exec(`
		INSERT INTO wallet_archive_sync (
			user_id, character_id, total_sp_private, total_sp_at, updated_at
		) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id, character_id) DO UPDATE SET
			total_sp_private = excluded.total_sp_private,
			total_sp_at = excluded.total_sp_at,
			updated_at = excluded.updated_at
	`, userID, characterID, protectedSP, now, now)
	return err
}

func (d *DB) GetCharacterPrivateMetricsForUser(userID string, characterID int64) (CharacterPrivateMetrics, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" || characterID <= 0 {
		return CharacterPrivateMetrics{}, fmt.Errorf("invalid character private metrics scope")
	}
	out := CharacterPrivateMetrics{CharacterID: characterID}
	var legacyBalance float64
	var protectedBalance, balanceAt, protectedSP, spAt string
	err := d.sql.QueryRow(`
		SELECT wallet_balance, wallet_balance_private, wallet_balance_at, total_sp_private, total_sp_at
		  FROM wallet_archive_sync
		 WHERE user_id = ? AND character_id = ?
	`, userID, characterID).Scan(&legacyBalance, &protectedBalance, &balanceAt, &protectedSP, &spAt)
	if err != nil {
		return out, err
	}
	if strings.TrimSpace(protectedBalance) != "" {
		opened, err := d.openPrivateString(userID, "wallet_archive_sync.wallet_balance", protectedBalance)
		if err != nil {
			return out, err
		}
		parsed, err := strconv.ParseFloat(opened, 64)
		if err != nil {
			return out, fmt.Errorf("parse private wallet balance: %w", err)
		}
		out.WalletBalance = parsed
		out.WalletBalanceAt = balanceAt
		out.HasWalletBalance = true
	} else if legacyBalance != 0 {
		out.WalletBalance = legacyBalance
		out.WalletBalanceAt = balanceAt
		out.HasWalletBalance = true
	}
	if strings.TrimSpace(protectedSP) != "" {
		opened, err := d.openPrivateString(userID, "wallet_archive_sync.total_sp", protectedSP)
		if err != nil {
			return out, err
		}
		parsed, err := strconv.ParseInt(opened, 10, 64)
		if err != nil {
			return out, fmt.Errorf("parse private total SP: %w", err)
		}
		out.TotalSP = parsed
		out.TotalSPAt = spAt
		out.HasTotalSP = true
	}
	return out, nil
}

// ListArchivedWalletTransactions returns archived transactions for selected characters.
func (d *DB) ListArchivedWalletTransactions(userID string, characterIDs []int64, since time.Time, limit int) ([]esi.WalletTransaction, error) {
	args := []interface{}{strings.TrimSpace(userID)}
	if args[0] == "" {
		return nil, fmt.Errorf("user id is required")
	}
	where := "user_id = ?"
	if len(characterIDs) > 0 {
		where += " AND character_id IN (" + placeholders(len(characterIDs)) + ")"
		for _, id := range characterIDs {
			args = append(args, id)
		}
	}
	if !since.IsZero() {
		where += " AND date >= ?"
		args = append(args, since.UTC().Format(time.RFC3339))
	}
	query := `
		SELECT transaction_id, date, type_id, location_id, unit_price, quantity, is_buy, type_name, location_name
		FROM wallet_transactions_archive
		WHERE ` + where + `
		ORDER BY date DESC, transaction_id DESC`
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := d.sql.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []esi.WalletTransaction{}
	for rows.Next() {
		var row esi.WalletTransaction
		var isBuy int
		if err := rows.Scan(
			&row.TransactionID,
			&row.Date,
			&row.TypeID,
			&row.LocationID,
			&row.UnitPrice,
			&row.Quantity,
			&isBuy,
			&row.TypeName,
			&row.LocationName,
		); err != nil {
			return nil, err
		}
		row.IsBuy = isBuy != 0
		out = append(out, row)
	}
	return out, rows.Err()
}

// ListArchivedWalletJournal returns archived journal rows for selected characters.
func (d *DB) ListArchivedWalletJournal(userID string, characterIDs []int64, since time.Time, limit int) ([]esi.WalletJournalEntry, error) {
	queryUserID := strings.TrimSpace(userID)
	args := []interface{}{queryUserID}
	if queryUserID == "" {
		return nil, fmt.Errorf("user id is required")
	}
	where := "user_id = ?"
	if len(characterIDs) > 0 {
		where += " AND character_id IN (" + placeholders(len(characterIDs)) + ")"
		for _, id := range characterIDs {
			args = append(args, id)
		}
	}
	if !since.IsZero() {
		where += " AND date >= ?"
		args = append(args, since.UTC().Format(time.RFC3339))
	}
	query := `
		SELECT entry_id, date, ref_type, first_party_id, second_party_id, amount, balance,
		       reason, description, tax, tax_receiver_id, context_id, context_id_type
		FROM wallet_journal_archive
		WHERE ` + where + `
		ORDER BY date DESC, entry_id DESC`
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := d.sql.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []esi.WalletJournalEntry{}
	for rows.Next() {
		var row esi.WalletJournalEntry
		if err := rows.Scan(
			&row.ID,
			&row.Date,
			&row.RefType,
			&row.FirstPartyID,
			&row.SecondPartyID,
			&row.Amount,
			&row.Balance,
			&row.Reason,
			&row.Description,
			&row.Tax,
			&row.TaxReceiverID,
			&row.ContextID,
			&row.ContextIDType,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range out {
		var err error
		out[i].Reason, err = d.openPrivateString(queryUserID, "wallet_journal_archive.reason", out[i].Reason)
		if err != nil {
			return nil, err
		}
		out[i].Description, err = d.openPrivateString(queryUserID, "wallet_journal_archive.description", out[i].Description)
		if err != nil {
			return nil, err
		}
		out[i].ContextIDType, err = d.openPrivateString(queryUserID, "wallet_journal_archive.context_id_type", out[i].ContextIDType)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// GetWalletArchiveStats summarizes full local archive coverage for selected characters.
func (d *DB) GetWalletArchiveStats(userID string, characterIDs []int64) (WalletArchiveStats, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return WalletArchiveStats{}, fmt.Errorf("user id is required")
	}
	stats := WalletArchiveStats{
		ArchiveTransactionLimit: walletArchiveESITransactionSoftLimit,
		ArchiveJournalLimit:     walletArchiveESIJournalSoftLimit,
		CharacterIDs:            normalizedCharacterIDs(characterIDs),
	}
	stats.Characters = len(stats.CharacterIDs)

	if err := d.scanArchiveCounts(userID, characterIDs, "wallet_transactions_archive", "transaction_id", &stats.TransactionRows, &stats.OldestTransactionDate, &stats.NewestTransactionDate); err != nil {
		return stats, err
	}
	if err := d.scanArchiveCounts(userID, characterIDs, "wallet_journal_archive", "entry_id", &stats.JournalRows, &stats.OldestJournalDate, &stats.NewestJournalDate); err != nil {
		return stats, err
	}
	if err := d.scanWalletTransactionTurnover(userID, characterIDs, &stats.TransactionTurnoverISK); err != nil {
		return stats, err
	}

	args := []interface{}{userID}
	where := "user_id = ?"
	if len(characterIDs) > 0 {
		where += " AND character_id IN (" + placeholders(len(characterIDs)) + ")"
		for _, id := range characterIDs {
			args = append(args, id)
		}
	}
	rows, err := d.sql.Query(`
		SELECT character_id, transaction_synced_at, journal_synced_at,
		       transaction_live_count, journal_live_count, transaction_limit_hit, journal_limit_hit
		FROM wallet_archive_sync
		WHERE `+where, args...)
	if err != nil {
		return stats, err
	}
	defer rows.Close()

	seenChars := make(map[int64]bool)
	for _, id := range stats.CharacterIDs {
		seenChars[id] = true
	}
	for rows.Next() {
		var characterID int64
		var txSync, journalSync string
		var txLive, journalLive, txLimit, journalLimit int
		if err := rows.Scan(&characterID, &txSync, &journalSync, &txLive, &journalLive, &txLimit, &journalLimit); err != nil {
			return stats, err
		}
		if characterID > 0 && !seenChars[characterID] {
			stats.CharacterIDs = append(stats.CharacterIDs, characterID)
			seenChars[characterID] = true
		}
		stats.LiveTransactionRows += txLive
		stats.LiveJournalRows += journalLive
		if txLimit != 0 {
			stats.TransactionLimitHit = true
		}
		if journalLimit != 0 {
			stats.JournalLimitHit = true
		}
		if txSync > stats.LastTransactionSync {
			stats.LastTransactionSync = txSync
		}
		if journalSync > stats.LastJournalSync {
			stats.LastJournalSync = journalSync
		}
	}
	if err := rows.Err(); err != nil {
		return stats, err
	}
	sort.Slice(stats.CharacterIDs, func(i, j int) bool { return stats.CharacterIDs[i] < stats.CharacterIDs[j] })
	if stats.Characters == 0 {
		stats.Characters = len(stats.CharacterIDs)
	}
	return stats, nil
}

func (d *DB) scanArchiveCounts(userID string, characterIDs []int64, table string, idCol string, total *int, oldest *string, newest *string) error {
	args := []interface{}{userID}
	where := "user_id = ?"
	if len(characterIDs) > 0 {
		where += " AND character_id IN (" + placeholders(len(characterIDs)) + ")"
		for _, id := range characterIDs {
			args = append(args, id)
		}
	}
	row := d.sql.QueryRow(fmt.Sprintf(`
		SELECT COUNT(%s), COALESCE(MIN(date), ''), COALESCE(MAX(date), '')
		FROM %s
		WHERE %s
	`, idCol, table, where), args...)
	return row.Scan(total, oldest, newest)
}

func (d *DB) scanWalletTransactionTurnover(userID string, characterIDs []int64, total *float64) error {
	args := []interface{}{userID}
	where := "user_id = ?"
	if len(characterIDs) > 0 {
		where += " AND character_id IN (" + placeholders(len(characterIDs)) + ")"
		for _, id := range characterIDs {
			args = append(args, id)
		}
	}
	row := d.sql.QueryRow(`
		SELECT COALESCE(SUM(unit_price * ABS(quantity)), 0)
		FROM wallet_transactions_archive
		WHERE `+where, args...)
	return row.Scan(total)
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('?')
	}
	return b.String()
}

func normalizedCharacterIDs(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
