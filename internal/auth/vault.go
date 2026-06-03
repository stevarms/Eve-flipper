package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	VaultModeStandard = "standard"
	VaultModePrivate  = "private"

	vaultStatusConfigured  = "configured"
	vaultSchemaVersion     = 1
	vaultCheckpointV1      = 1
	vaultCheckpointV2      = 2
	vaultCheckpointCurrent = vaultCheckpointV2
	vaultTokenPrefix       = "evf:vault:v1:"
	vaultCheckPlaintext    = "eve-flipper-vault-check-v1"

	privateKDFAlg = "argon2id:v1:m=64MiB,t=3,p=2"
)

// VaultStatus describes the user-facing state of the local security vault.
type VaultStatus struct {
	Configured          bool   `json:"configured"`
	Mode                string `json:"mode"`
	Status              string `json:"status"`
	SchemaVersion       int    `json:"schema_version"`
	CheckpointVersion   int    `json:"checkpoint_version"`
	Locked              bool   `json:"locked"`
	LegacyPlaintextAuth bool   `json:"legacy_plaintext_auth"`
	PlaintextPurgedAt   string `json:"plaintext_purged_at"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at"`
}

type vaultRow struct {
	UserID            string
	Mode              string
	Status            string
	SchemaVersion     int
	CheckpointVersion int
	KDFAlg            string
	KDFSalt           string
	WrappedKey        string
	KeyCheck          string
	PlaintextPurgedAt string
	CreatedAt         string
	UpdatedAt         string
}

type vaultSQLRunner interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

// TokenVault encrypts/decrypts sensitive auth material stored in SQLite.
type TokenVault struct {
	db *sql.DB

	mu       sync.Mutex
	unlocked map[string][]byte
}

func NewTokenVault(db *sql.DB) *TokenVault {
	return &TokenVault{
		db:       db,
		unlocked: map[string][]byte{},
	}
}

func normalizeVaultUserID(userID string) string {
	trimmed := strings.TrimSpace(userID)
	if trimmed == "" {
		return defaultUserID
	}
	return trimmed
}

func (v *TokenVault) TableReady() bool {
	if v == nil || v.db == nil {
		return false
	}
	var name string
	err := v.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'vault_state' LIMIT 1`).Scan(&name)
	return err == nil && name == "vault_state"
}

func (v *TokenVault) StatusForUser(userID string) VaultStatus {
	userID = normalizeVaultUserID(userID)
	status := VaultStatus{}
	if v == nil || !v.TableReady() {
		status.LegacyPlaintextAuth = v.hasLegacyPlaintextAuth(userID)
		return status
	}

	row, ok, err := v.getState(userID)
	if err != nil || !ok {
		status.LegacyPlaintextAuth = v.hasLegacyPlaintextAuth(userID)
		return status
	}

	status.Configured = true
	status.Mode = row.Mode
	status.Status = row.Status
	status.SchemaVersion = row.SchemaVersion
	status.CheckpointVersion = row.CheckpointVersion
	status.PlaintextPurgedAt = row.PlaintextPurgedAt
	status.CreatedAt = row.CreatedAt
	status.UpdatedAt = row.UpdatedAt
	status.LegacyPlaintextAuth = v.hasLegacyPlaintextAuth(userID)
	status.Locked = row.Mode == VaultModePrivate && !v.isUnlocked(userID)
	return status
}

func (v *TokenVault) IsConfiguredForUser(userID string) bool {
	status := v.StatusForUser(userID)
	return status.Configured
}

func (v *TokenVault) SetupStandardForUser(userID string) error {
	userID = normalizeVaultUserID(userID)
	if v == nil || !v.TableReady() {
		return fmt.Errorf("vault table unavailable")
	}
	dataKey, err := randomBytes(32)
	if err != nil {
		return err
	}
	wrapped, err := protectMachineData(dataKey)
	if err != nil {
		return fmt.Errorf("protect local vault key: %w", err)
	}
	keyCheck, err := sealVaultValue(dataKey, []byte(vaultCheckPlaintext), vaultAAD(userID, "check", 0))
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := v.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO vault_state (
			user_id, mode, status, schema_version, checkpoint_version,
			kdf_alg, kdf_salt, wrapped_key, key_check, plaintext_purged_at, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, '', '', ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			mode = excluded.mode,
			status = excluded.status,
			schema_version = excluded.schema_version,
			checkpoint_version = excluded.checkpoint_version,
			kdf_alg = excluded.kdf_alg,
			kdf_salt = excluded.kdf_salt,
			wrapped_key = excluded.wrapped_key,
			key_check = excluded.key_check,
			plaintext_purged_at = excluded.plaintext_purged_at,
			updated_at = excluded.updated_at`,
		userID, VaultModeStandard, vaultStatusConfigured, vaultSchemaVersion, vaultCheckpointCurrent, wrapped, keyCheck, now, now, now,
	)
	if err != nil {
		return err
	}
	if err := v.encryptLegacyPrivateFieldsForUserWith(tx, userID, dataKey); err != nil {
		return err
	}
	if err := v.deleteAuthSessionsWith(tx, userID); err != nil {
		return err
	}
	if err := v.recordSecurityEventWith(tx, userID, "vault_setup_standard", "old auth sessions purged"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	v.lockUser(userID)
	return nil
}

func (v *TokenVault) SetupPrivateForUser(userID, passphrase string) error {
	userID = normalizeVaultUserID(userID)
	if v == nil || !v.TableReady() {
		return fmt.Errorf("vault table unavailable")
	}
	passphrase = strings.TrimSpace(passphrase)
	if len(passphrase) < 8 {
		return fmt.Errorf("vault passphrase must be at least 8 characters")
	}
	dataKey, err := randomBytes(32)
	if err != nil {
		return err
	}
	salt, err := randomBytes(16)
	if err != nil {
		return err
	}
	kek := derivePrivateKEK(passphrase, salt)
	wrapped, err := sealVaultValue(kek, dataKey, vaultAAD(userID, "private-wrap", 0))
	if err != nil {
		return err
	}
	keyCheck, err := sealVaultValue(dataKey, []byte(vaultCheckPlaintext), vaultAAD(userID, "check", 0))
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := v.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO vault_state (
			user_id, mode, status, schema_version, checkpoint_version,
			kdf_alg, kdf_salt, wrapped_key, key_check, plaintext_purged_at, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			mode = excluded.mode,
			status = excluded.status,
			schema_version = excluded.schema_version,
			checkpoint_version = excluded.checkpoint_version,
			kdf_alg = excluded.kdf_alg,
			kdf_salt = excluded.kdf_salt,
			wrapped_key = excluded.wrapped_key,
			key_check = excluded.key_check,
			plaintext_purged_at = excluded.plaintext_purged_at,
			updated_at = excluded.updated_at`,
		userID, VaultModePrivate, vaultStatusConfigured, vaultSchemaVersion, vaultCheckpointCurrent,
		privateKDFAlg, base64.RawURLEncoding.EncodeToString(salt), wrapped, keyCheck, now, now, now,
	)
	if err != nil {
		return err
	}
	if err := v.encryptLegacyPrivateFieldsForUserWith(tx, userID, dataKey); err != nil {
		return err
	}
	if err := v.deleteAuthSessionsWith(tx, userID); err != nil {
		return err
	}
	if err := v.recordSecurityEventWith(tx, userID, "vault_setup_private", "old auth sessions purged"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	v.setUnlocked(userID, dataKey)
	return nil
}

func (v *TokenVault) UnlockPrivateForUser(userID, passphrase string) error {
	userID = normalizeVaultUserID(userID)
	row, ok, err := v.getState(userID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("vault is not configured")
	}
	if row.Mode != VaultModePrivate {
		return nil
	}
	salt, err := base64.RawURLEncoding.DecodeString(row.KDFSalt)
	if err != nil {
		return fmt.Errorf("invalid vault salt")
	}
	kek := derivePrivateKEK(passphrase, salt)
	dataKey, err := openVaultValue(kek, row.WrappedKey, vaultAAD(userID, "private-wrap", 0))
	if err != nil {
		return fmt.Errorf("invalid vault passphrase")
	}
	if err := validateVaultKey(dataKey, row, userID); err != nil {
		return fmt.Errorf("invalid vault passphrase")
	}
	if row.CheckpointVersion < vaultCheckpointCurrent {
		if err := v.ensurePrivateFieldCheckpointForUser(userID, dataKey, row); err != nil {
			return err
		}
	}
	v.setUnlocked(userID, dataKey)
	v.recordSecurityEvent(userID, "vault_unlocked", "")
	return nil
}

func (v *TokenVault) LockForUser(userID string) {
	v.lockUser(normalizeVaultUserID(userID))
}

func (v *TokenVault) ResetForUser(userID string, wipePrivateData bool) error {
	userID = normalizeVaultUserID(userID)
	if v == nil || v.db == nil {
		return nil
	}
	tx, err := v.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	tables := []string{
		"auth_session",
	}
	if wipePrivateData {
		tables = append(tables,
			"config",
			"alert_history",
			"watchlist_alerts",
			"watchlist",
			"user_trade_state",
			"wallet_transactions_archive",
			"wallet_journal_archive",
			"wallet_archive_sync",
			"paper_trades",
			"achievements",
			"cockpit_loadouts",
			"cockpit_preferences",
			"industry_material_plan",
			"industry_blueprint_pool",
			"industry_jobs",
			"industry_tasks",
			"industry_projects",
		)
	}
	for _, table := range tables {
		if table == "" || !v.tableExistsTx(tx, table) {
			continue
		}
		if _, err := tx.Exec("DELETE FROM "+table+" WHERE user_id = ?", userID); err != nil {
			return fmt.Errorf("reset %s: %w", table, err)
		}
	}
	if v.tableExistsTx(tx, "vault_state") {
		if _, err := tx.Exec("DELETE FROM vault_state WHERE user_id = ?", userID); err != nil {
			return fmt.Errorf("reset vault_state: %w", err)
		}
	}
	if v.tableExistsTx(tx, "security_events") {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		_, _ = tx.Exec("INSERT INTO security_events (user_id, event_type, detail, created_at) VALUES (?, ?, ?, ?)", userID, "vault_reset", fmt.Sprintf("wipe_private_data=%t", wipePrivateData), now)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	v.lockUser(userID)
	return nil
}

func (v *TokenVault) PrepareTokenForStorage(userID string, characterID int64, kind, token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", nil
	}
	if strings.HasPrefix(token, vaultTokenPrefix) {
		return token, nil
	}
	if v == nil || !v.TableReady() {
		return token, nil
	}
	dataKey, err := v.dataKeyForUser(userID)
	if err != nil {
		return "", err
	}
	return sealVaultValue(dataKey, []byte(token), vaultAAD(userID, kind, characterID))
}

func (v *TokenVault) OpenTokenFromStorage(userID string, characterID int64, kind, token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", nil
	}
	if !strings.HasPrefix(token, vaultTokenPrefix) {
		return token, nil
	}
	dataKey, err := v.dataKeyForUser(userID)
	if err != nil {
		return "", err
	}
	plain, err := openVaultValue(dataKey, token, vaultAAD(userID, kind, characterID))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (v *TokenVault) ProtectStringForStorage(userID, purpose, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return value, nil
	}
	if strings.HasPrefix(value, vaultTokenPrefix) {
		return value, nil
	}
	if v == nil {
		return value, nil
	}
	userID = normalizeVaultUserID(userID)
	purpose = strings.TrimSpace(purpose)
	if dataKey := v.getUnlocked(userID); len(dataKey) > 0 {
		return sealVaultValue(dataKey, []byte(value), vaultAAD(userID, "field:"+purpose, 0))
	}
	if !v.TableReady() {
		return value, nil
	}
	dataKey, err := v.dataKeyForUser(userID)
	if err != nil {
		return "", err
	}
	return sealVaultValue(dataKey, []byte(value), vaultAAD(userID, "field:"+purpose, 0))
}

func (v *TokenVault) OpenStringFromStorage(userID, purpose, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return value, nil
	}
	if !strings.HasPrefix(value, vaultTokenPrefix) {
		return value, nil
	}
	if v == nil {
		return "", fmt.Errorf("security vault unavailable")
	}
	dataKey, err := v.dataKeyForUser(userID)
	if err != nil {
		return "", err
	}
	plain, err := openVaultValue(dataKey, value, vaultAAD(userID, "field:"+strings.TrimSpace(purpose), 0))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (v *TokenVault) HasLegacyPlaintextAuth(userID string) bool {
	return v.hasLegacyPlaintextAuth(normalizeVaultUserID(userID))
}

func (v *TokenVault) dataKeyForUser(userID string) ([]byte, error) {
	userID = normalizeVaultUserID(userID)
	if cached := v.getUnlocked(userID); len(cached) > 0 {
		return cached, nil
	}
	row, ok, err := v.getState(userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("security vault not configured")
	}
	var dataKey []byte
	switch row.Mode {
	case VaultModeStandard:
		dataKey, err = unprotectMachineData(row.WrappedKey)
	case VaultModePrivate:
		dataKey = v.getUnlocked(userID)
		if len(dataKey) == 0 {
			return nil, fmt.Errorf("private vault locked")
		}
	default:
		return nil, fmt.Errorf("unsupported vault mode")
	}
	if err != nil {
		return nil, err
	}
	if err := validateVaultKey(dataKey, row, userID); err != nil {
		return nil, err
	}
	if row.CheckpointVersion < vaultCheckpointCurrent {
		if err := v.ensurePrivateFieldCheckpointForUser(userID, dataKey, row); err != nil {
			return nil, err
		}
	}
	if row.Mode == VaultModeStandard {
		v.setUnlocked(userID, dataKey)
	}
	return dataKey, nil
}

func (v *TokenVault) ensurePrivateFieldCheckpointForUser(userID string, dataKey []byte, row vaultRow) error {
	if v == nil || v.db == nil || len(dataKey) == 0 || row.CheckpointVersion >= vaultCheckpointCurrent {
		return nil
	}
	if err := v.encryptLegacyPrivateFieldsForUser(userID, dataKey); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := v.db.Exec(`
		UPDATE vault_state
		   SET checkpoint_version = ?, updated_at = ?
		 WHERE user_id = ?
	`, vaultCheckpointCurrent, now, normalizeVaultUserID(userID)); err != nil {
		return fmt.Errorf("update vault checkpoint: %w", err)
	}
	return nil
}

func validateVaultKey(dataKey []byte, row vaultRow, userID string) error {
	plain, err := openVaultValue(dataKey, row.KeyCheck, vaultAAD(userID, "check", 0))
	if err != nil {
		return err
	}
	if string(plain) != vaultCheckPlaintext {
		return errors.New("vault key check failed")
	}
	return nil
}

func (v *TokenVault) getState(userID string) (vaultRow, bool, error) {
	var row vaultRow
	if v == nil || !v.TableReady() {
		return row, false, nil
	}
	err := v.db.QueryRow(`
		SELECT user_id, mode, status, schema_version, checkpoint_version, kdf_alg, kdf_salt,
		       wrapped_key, key_check, plaintext_purged_at, created_at, updated_at
		FROM vault_state
		WHERE user_id = ?
		LIMIT 1`, normalizeVaultUserID(userID)).
		Scan(
			&row.UserID,
			&row.Mode,
			&row.Status,
			&row.SchemaVersion,
			&row.CheckpointVersion,
			&row.KDFAlg,
			&row.KDFSalt,
			&row.WrappedKey,
			&row.KeyCheck,
			&row.PlaintextPurgedAt,
			&row.CreatedAt,
			&row.UpdatedAt,
		)
	if err == sql.ErrNoRows {
		return row, false, nil
	}
	if err != nil {
		return row, false, err
	}
	return row, true, nil
}

func (v *TokenVault) hasLegacyPlaintextAuth(userID string) bool {
	if v == nil || v.db == nil {
		return false
	}
	var count int
	err := v.db.QueryRow(`
		SELECT COUNT(*)
		FROM auth_session
		WHERE user_id = ?
		  AND (
			access_token NOT LIKE ?
			OR refresh_token NOT LIKE ?
		  )`, normalizeVaultUserID(userID), vaultTokenPrefix+"%", vaultTokenPrefix+"%").Scan(&count)
	return err == nil && count > 0
}

func (v *TokenVault) deleteAuthSessions(userID string) {
	if v == nil || v.db == nil {
		return
	}
	_ = v.deleteAuthSessionsWith(v.db, userID)
}

func (v *TokenVault) deleteAuthSessionsWith(q vaultSQLRunner, userID string) error {
	if q == nil {
		return nil
	}
	if _, err := q.Exec("DELETE FROM auth_session WHERE user_id = ?", normalizeVaultUserID(userID)); err != nil {
		return fmt.Errorf("purge legacy auth sessions: %w", err)
	}
	return nil
}

func (v *TokenVault) encryptLegacyPrivateFieldsForUser(userID string, dataKey []byte) error {
	if v == nil || v.db == nil || len(dataKey) == 0 {
		return nil
	}
	return v.encryptLegacyPrivateFieldsForUserWith(v.db, userID, dataKey)
}

func (v *TokenVault) encryptLegacyPrivateFieldsForUserWith(q vaultSQLRunner, userID string, dataKey []byte) error {
	if q == nil || len(dataKey) == 0 {
		return nil
	}
	userID = normalizeVaultUserID(userID)

	configKeys := []string{
		"alert_telegram_token",
		"alert_telegram_chat_id",
		"alert_discord_webhook",
	}
	if tableExistsWith(q, "config") {
		for _, key := range configKeys {
			if err := encryptLegacyPrivateColumnRowsWith(q, userID, dataKey, "config", "value", "config."+key, "key = ?", key); err != nil {
				return err
			}
		}
	}

	type privateColumn struct {
		table   string
		column  string
		purpose string
	}
	columns := []privateColumn{
		{table: "wallet_journal_archive", column: "reason", purpose: "wallet_journal_archive.reason"},
		{table: "wallet_journal_archive", column: "description", purpose: "wallet_journal_archive.description"},
		{table: "wallet_journal_archive", column: "context_id_type", purpose: "wallet_journal_archive.context_id_type"},
		{table: "paper_trades", column: "notes", purpose: "paper_trades.notes"},
		{table: "paper_trades", column: "source", purpose: "paper_trades.source"},
		{table: "industry_projects", column: "notes", purpose: "industry_projects.notes"},
		{table: "industry_jobs", column: "notes", purpose: "industry_jobs.notes"},
		{table: "cockpit_preferences", column: "payload_json", purpose: "cockpit_preferences.payload_json"},
		{table: "cockpit_loadouts", column: "payload_json", purpose: "cockpit_loadouts.payload_json"},
	}
	for _, col := range columns {
		if !tableExistsWith(q, col.table) {
			continue
		}
		if err := encryptLegacyPrivateColumnRowsWith(q, userID, dataKey, col.table, col.column, col.purpose, "", nil); err != nil {
			return err
		}
	}
	if err := encryptLegacyWalletArchiveMetricsForUserWith(q, userID, dataKey); err != nil {
		return err
	}
	return nil
}

func (v *TokenVault) encryptLegacyWalletArchiveMetricsForUser(userID string, dataKey []byte) error {
	if v == nil || v.db == nil {
		return nil
	}
	return encryptLegacyWalletArchiveMetricsForUserWith(v.db, normalizeVaultUserID(userID), dataKey)
}

func encryptLegacyWalletArchiveMetricsForUserWith(q vaultSQLRunner, userID string, dataKey []byte) error {
	if q == nil ||
		!tableExistsWith(q, "wallet_archive_sync") ||
		!columnExistsWith(q, "wallet_archive_sync", "wallet_balance") ||
		!columnExistsWith(q, "wallet_archive_sync", "wallet_balance_private") {
		return nil
	}
	rows, err := q.Query(`
		SELECT rowid, wallet_balance
		  FROM wallet_archive_sync
		 WHERE user_id = ?
		   AND wallet_balance != 0
		   AND TRIM(wallet_balance_private) = ''
	`, userID)
	if err != nil {
		return fmt.Errorf("scan legacy private wallet balance: %w", err)
	}

	type legacyBalanceRow struct {
		rowID   int64
		balance float64
	}
	pending := []legacyBalanceRow{}
	for rows.Next() {
		var row legacyBalanceRow
		if err := rows.Scan(&row.rowID, &row.balance); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan legacy private wallet balance row: %w", err)
		}
		pending = append(pending, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("scan legacy private wallet balance rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy private wallet balance rows: %w", err)
	}

	for _, row := range pending {
		protected, err := sealVaultValue(
			dataKey,
			[]byte(strconv.FormatFloat(row.balance, 'f', -1, 64)),
			vaultAAD(userID, "field:wallet_archive_sync.wallet_balance", 0),
		)
		if err != nil {
			return fmt.Errorf("protect legacy private wallet balance: %w", err)
		}
		if _, err := q.Exec(`
			UPDATE wallet_archive_sync
			   SET wallet_balance_private = ?, wallet_balance = 0
			 WHERE user_id = ? AND rowid = ?
		`, protected, userID, row.rowID); err != nil {
			return fmt.Errorf("update legacy private wallet balance: %w", err)
		}
	}
	return nil
}

func (v *TokenVault) encryptLegacyPrivateColumnRows(userID string, dataKey []byte, table, column, purpose, extraWhere string, extraArg interface{}) error {
	if v == nil || v.db == nil {
		return nil
	}
	return encryptLegacyPrivateColumnRowsWith(v.db, normalizeVaultUserID(userID), dataKey, table, column, purpose, extraWhere, extraArg)
}

func encryptLegacyPrivateColumnRowsWith(q vaultSQLRunner, userID string, dataKey []byte, table, column, purpose, extraWhere string, extraArg interface{}) error {
	if !isSafeIdentifier(table) || !isSafeIdentifier(column) {
		return fmt.Errorf("unsafe vault migration identifier")
	}
	where := "user_id = ? AND TRIM(" + column + ") != '' AND " + column + " NOT LIKE ?"
	args := []interface{}{userID, vaultTokenPrefix + "%"}
	if strings.TrimSpace(extraWhere) != "" {
		where += " AND " + extraWhere
		args = append(args, extraArg)
	}
	rows, err := q.Query("SELECT rowid, "+column+" FROM "+table+" WHERE "+where, args...)
	if err != nil {
		return fmt.Errorf("scan legacy private %s.%s: %w", table, column, err)
	}

	type legacyRow struct {
		rowID int64
		value string
	}
	pending := []legacyRow{}
	for rows.Next() {
		var row legacyRow
		if err := rows.Scan(&row.rowID, &row.value); err != nil {
			return fmt.Errorf("scan legacy private %s.%s row: %w", table, column, err)
		}
		if strings.TrimSpace(row.value) == "" || strings.HasPrefix(row.value, vaultTokenPrefix) {
			continue
		}
		pending = append(pending, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("scan legacy private %s.%s rows: %w", table, column, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy private %s.%s rows: %w", table, column, err)
	}

	for _, row := range pending {
		protected, err := sealVaultValue(dataKey, []byte(row.value), vaultAAD(userID, "field:"+strings.TrimSpace(purpose), 0))
		if err != nil {
			return fmt.Errorf("protect legacy private %s.%s: %w", table, column, err)
		}
		if _, err := q.Exec("UPDATE "+table+" SET "+column+" = ? WHERE user_id = ? AND rowid = ?", protected, userID, row.rowID); err != nil {
			return fmt.Errorf("update legacy private %s.%s: %w", table, column, err)
		}
	}
	return nil
}

func isSafeIdentifier(v string) bool {
	if v == "" {
		return false
	}
	for _, ch := range v {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' {
			continue
		}
		return false
	}
	return true
}

func (v *TokenVault) recordSecurityEvent(userID, eventType, detail string) {
	if v == nil || v.db == nil || !v.TableReady() {
		return
	}
	_ = v.recordSecurityEventWith(v.db, userID, eventType, detail)
}

func (v *TokenVault) tableExists(tableName string) bool {
	if v == nil || v.db == nil {
		return false
	}
	return tableExistsWith(v.db, tableName)
}

func (v *TokenVault) columnExists(tableName, columnName string) bool {
	if !isSafeIdentifier(tableName) || !isSafeIdentifier(columnName) || v == nil || v.db == nil {
		return false
	}
	return columnExistsWith(v.db, tableName, columnName)
}

func (v *TokenVault) recordSecurityEventWith(q vaultSQLRunner, userID, eventType, detail string) error {
	if q == nil || !tableExistsWith(q, "security_events") {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := q.Exec(
		"INSERT INTO security_events (user_id, event_type, detail, created_at) VALUES (?, ?, ?, ?)",
		normalizeVaultUserID(userID), eventType, detail, now,
	); err != nil {
		return fmt.Errorf("record security event: %w", err)
	}
	return nil
}

func tableExistsWith(q vaultSQLRunner, tableName string) bool {
	if q == nil {
		return false
	}
	var name string
	err := q.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ? LIMIT 1`, tableName).Scan(&name)
	return err == nil && name == tableName
}

func columnExistsWith(q vaultSQLRunner, tableName, columnName string) bool {
	if !isSafeIdentifier(tableName) || !isSafeIdentifier(columnName) || q == nil {
		return false
	}
	rows, err := q.Query("PRAGMA table_info(" + tableName + ")")
	if err != nil {
		return false
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return false
		}
		if strings.EqualFold(name, columnName) {
			return true
		}
	}
	return false
}

func (v *TokenVault) tableExistsTx(tx *sql.Tx, tableName string) bool {
	return tableExistsWith(tx, tableName)
}

func (v *TokenVault) setUnlocked(userID string, key []byte) {
	v.mu.Lock()
	defer v.mu.Unlock()
	copied := append([]byte(nil), key...)
	v.unlocked[normalizeVaultUserID(userID)] = copied
}

func (v *TokenVault) getUnlocked(userID string) []byte {
	v.mu.Lock()
	defer v.mu.Unlock()
	key := v.unlocked[normalizeVaultUserID(userID)]
	return append([]byte(nil), key...)
}

func (v *TokenVault) isUnlocked(userID string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.unlocked[normalizeVaultUserID(userID)]) > 0
}

func (v *TokenVault) lockUser(userID string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.unlocked, normalizeVaultUserID(userID))
}

func derivePrivateKEK(passphrase string, salt []byte) []byte {
	return argon2.IDKey([]byte(passphrase), salt, 3, 64*1024, 2, 32)
}

func sealVaultValue(key, plain, aad []byte) (string, error) {
	sealed, err := sealRaw(key, plain, aad)
	if err != nil {
		return "", err
	}
	return vaultTokenPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func openVaultValue(key []byte, encoded string, aad []byte) ([]byte, error) {
	if !strings.HasPrefix(encoded, vaultTokenPrefix) {
		return nil, fmt.Errorf("invalid encrypted vault value")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encoded, vaultTokenPrefix))
	if err != nil {
		return nil, err
	}
	return openRaw(key, raw, aad)
}

func sealRaw(key, plain, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce, err := randomBytes(gcm.NonceSize())
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(nonce)+len(plain)+gcm.Overhead())
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, plain, aad)
	return out, nil
}

func openRaw(key, sealed, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, fmt.Errorf("encrypted value too short")
	}
	nonce := sealed[:gcm.NonceSize()]
	ciphertext := sealed[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ciphertext, aad)
}

func randomBytes(n int) ([]byte, error) {
	out := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, out); err != nil {
		return nil, err
	}
	return out, nil
}

func vaultAAD(userID, purpose string, characterID int64) []byte {
	return []byte(fmt.Sprintf("eve-flipper:vault:v1:%s:%s:%d", normalizeVaultUserID(userID), purpose, characterID))
}

func machineWrappingAAD() []byte {
	return []byte("eve-flipper:vault-machine-wrap:v1")
}
