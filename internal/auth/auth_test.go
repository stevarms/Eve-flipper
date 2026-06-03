package auth

import (
	"database/sql"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestBuildAuthURL_Exact(t *testing.T) {
	c := &SSOConfig{
		ClientID:    "test-client",
		CallbackURL: "http://localhost:13370/callback",
		Scopes:      "esi-markets.read_character_orders.v1",
	}
	u := c.BuildAuthURL("abc123")
	if !strings.HasPrefix(u, "https://login.eveonline.com/v2/oauth/authorize?") {
		t.Errorf("BuildAuthURL prefix wrong: %q", u)
	}
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	q := parsed.Query()
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q", q.Get("response_type"))
	}
	if q.Get("client_id") != "test-client" {
		t.Errorf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("redirect_uri") != "http://localhost:13370/callback" {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	if q.Get("scope") != c.Scopes {
		t.Errorf("scope = %q", q.Get("scope"))
	}
	if q.Get("state") != "abc123" {
		t.Errorf("state = %q", q.Get("state"))
	}
}

func TestGenerateState_LengthAndEncoding(t *testing.T) {
	s := GenerateState()
	decoded, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		t.Errorf("GenerateState not valid base64 URL: %v", err)
	}
	if len(decoded) != 16 {
		t.Errorf("GenerateState decoded length = %d, want 16", len(decoded))
	}
	// Two calls should differ (with very high probability)
	s2 := GenerateState()
	if s == s2 {
		t.Error("GenerateState should return different values")
	}
}

func TestSessionStore_SaveGetDelete(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer sqlDB.Close()

	_, err = sqlDB.Exec(`
		CREATE TABLE auth_session (
			user_id         TEXT NOT NULL,
			character_id    INTEGER NOT NULL,
			character_name  TEXT NOT NULL,
			access_token    TEXT NOT NULL,
			refresh_token   TEXT NOT NULL,
			expires_at      INTEGER NOT NULL,
			is_active       INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (user_id, character_id)
		)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	store := NewSessionStore(sqlDB)

	if store.Get() != nil {
		t.Error("Get() on empty store should return nil")
	}

	sess := &Session{
		CharacterID:   12345,
		CharacterName: "Test Char",
		AccessToken:   "at",
		RefreshToken:  "rt",
		ExpiresAt:     time.Now().Add(time.Hour),
	}
	if err := store.SaveAndActivate(sess); err != nil {
		t.Fatalf("SaveAndActivate: %v", err)
	}

	got := store.Get()
	if got == nil {
		t.Fatal("Get() after Save returned nil")
	}
	if got.CharacterID != 12345 || got.CharacterName != "Test Char" {
		t.Errorf("Get() = %+v", got)
	}
	if got.AccessToken != "at" || got.RefreshToken != "rt" {
		t.Errorf("tokens = %q / %q", got.AccessToken, got.RefreshToken)
	}
	if !got.Active {
		t.Errorf("expected active session")
	}

	second := &Session{
		CharacterID:   67890,
		CharacterName: "Alt Char",
		AccessToken:   "at2",
		RefreshToken:  "rt2",
		ExpiresAt:     time.Now().Add(time.Hour),
	}
	if err := store.Save(second); err != nil {
		t.Fatalf("Save second: %v", err)
	}

	list := store.List()
	if len(list) != 2 {
		t.Fatalf("List() len = %d, want 2", len(list))
	}
	if list[0].CharacterID != 12345 || !list[0].Active {
		t.Fatalf("expected first list entry to be active character 12345, got %+v", list[0])
	}

	if err := store.SetActive(67890); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if active := store.Get(); active == nil || active.CharacterID != 67890 {
		t.Fatalf("active after SetActive = %+v, want character 67890", active)
	}

	if err := store.DeleteByCharacterID(67890); err != nil {
		t.Fatalf("DeleteByCharacterID: %v", err)
	}
	if active := store.Get(); active == nil || active.CharacterID != 12345 {
		t.Fatalf("active after deleting 67890 = %+v, want character 12345", active)
	}

	store.Delete()
	if store.Get() != nil {
		t.Error("Get() after Delete should return nil")
	}
}

func TestSessionStore_UserIsolation(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer sqlDB.Close()

	_, err = sqlDB.Exec(`
		CREATE TABLE auth_session (
			user_id         TEXT NOT NULL,
			character_id    INTEGER NOT NULL,
			character_name  TEXT NOT NULL,
			access_token    TEXT NOT NULL,
			refresh_token   TEXT NOT NULL,
			expires_at      INTEGER NOT NULL,
			is_active       INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (user_id, character_id)
		)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	store := NewSessionStore(sqlDB)

	if err := store.SaveAndActivateForUser("u1", &Session{
		CharacterID:   1001,
		CharacterName: "User One",
		AccessToken:   "at1",
		RefreshToken:  "rt1",
		ExpiresAt:     time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveAndActivateForUser u1: %v", err)
	}
	if err := store.SaveAndActivateForUser("u2", &Session{
		CharacterID:   2002,
		CharacterName: "User Two",
		AccessToken:   "at2",
		RefreshToken:  "rt2",
		ExpiresAt:     time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveAndActivateForUser u2: %v", err)
	}

	if got := store.GetForUser("u1"); got == nil || got.CharacterID != 1001 {
		t.Fatalf("GetForUser(u1) = %+v, want character 1001", got)
	}
	if got := store.GetForUser("u2"); got == nil || got.CharacterID != 2002 {
		t.Fatalf("GetForUser(u2) = %+v, want character 2002", got)
	}
	if got := store.Get(); got != nil {
		t.Fatalf("default Get() should be empty for non-default users, got %+v", got)
	}
}

func newSessionStoreForTokenTest(t *testing.T) *SessionStore {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	_, err = sqlDB.Exec(`
		CREATE TABLE auth_session (
			user_id         TEXT NOT NULL,
			character_id    INTEGER NOT NULL,
			character_name  TEXT NOT NULL,
			access_token    TEXT NOT NULL,
			refresh_token   TEXT NOT NULL,
			expires_at      INTEGER NOT NULL,
			is_active       INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (user_id, character_id)
		)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	return NewSessionStore(sqlDB)
}

func newVaultSessionStoreForTest(t *testing.T) *SessionStore {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	_, err = sqlDB.Exec(`
		CREATE TABLE auth_session (
			user_id         TEXT NOT NULL,
			character_id    INTEGER NOT NULL,
			character_name  TEXT NOT NULL,
			access_token    TEXT NOT NULL,
			refresh_token   TEXT NOT NULL,
			expires_at      INTEGER NOT NULL,
			is_active       INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (user_id, character_id)
		);
		CREATE TABLE vault_state (
			user_id              TEXT PRIMARY KEY,
			mode                 TEXT NOT NULL,
			status               TEXT NOT NULL,
			schema_version       INTEGER NOT NULL DEFAULT 1,
			checkpoint_version   INTEGER NOT NULL DEFAULT 1,
			kdf_alg              TEXT NOT NULL DEFAULT '',
			kdf_salt             TEXT NOT NULL DEFAULT '',
			wrapped_key          TEXT NOT NULL,
			key_check            TEXT NOT NULL,
			plaintext_purged_at  TEXT NOT NULL DEFAULT '',
			created_at           TEXT NOT NULL,
			updated_at           TEXT NOT NULL
		);
		CREATE TABLE security_events (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id     TEXT NOT NULL,
			event_type  TEXT NOT NULL,
			detail      TEXT NOT NULL DEFAULT '',
			created_at  TEXT NOT NULL
		);`)
	if err != nil {
		t.Fatalf("create tables: %v", err)
	}
	return NewSessionStore(sqlDB)
}

func TestSessionStore_StandardVaultEncryptsStoredTokens(t *testing.T) {
	store := newVaultSessionStoreForTest(t)
	userID := "u-vault-standard"
	if err := store.Vault().SetupStandardForUser(userID); err != nil {
		t.Fatalf("SetupStandardForUser: %v", err)
	}
	if err := store.SaveAndActivateForUser(userID, &Session{
		CharacterID:   101,
		CharacterName: "Vault Pilot",
		AccessToken:   "access-token",
		RefreshToken:  "refresh-token",
		ExpiresAt:     time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveAndActivateForUser: %v", err)
	}

	var rawAccess, rawRefresh string
	if err := store.db.QueryRow(`SELECT access_token, refresh_token FROM auth_session WHERE user_id = ?`, userID).Scan(&rawAccess, &rawRefresh); err != nil {
		t.Fatalf("query raw tokens: %v", err)
	}
	if !strings.HasPrefix(rawAccess, vaultTokenPrefix) || !strings.HasPrefix(rawRefresh, vaultTokenPrefix) {
		t.Fatalf("raw tokens are not vault encrypted: %q / %q", rawAccess, rawRefresh)
	}
	if got := store.GetForUser(userID); got == nil || got.AccessToken != "access-token" || got.RefreshToken != "refresh-token" {
		t.Fatalf("GetForUser decrypted session = %+v", got)
	}
	status := store.Vault().StatusForUser(userID)
	if !status.Configured || status.Mode != VaultModeStandard || status.Locked {
		t.Fatalf("standard vault status = %+v", status)
	}
}

func TestSessionStore_ListForUserWithVaultDoesNotReenterRows(t *testing.T) {
	store := newVaultSessionStoreForTest(t)
	userID := "u-vault-list"
	if err := store.Vault().SetupStandardForUser(userID); err != nil {
		t.Fatalf("SetupStandardForUser: %v", err)
	}
	for _, sess := range []*Session{
		{
			CharacterID:   401,
			CharacterName: "Active Pilot",
			AccessToken:   "active-access",
			RefreshToken:  "active-refresh",
			ExpiresAt:     time.Now().Add(time.Hour),
		},
		{
			CharacterID:   402,
			CharacterName: "Alt Pilot",
			AccessToken:   "alt-access",
			RefreshToken:  "alt-refresh",
			ExpiresAt:     time.Now().Add(time.Hour),
		},
	} {
		if err := store.SaveAndActivateForUser(userID, sess); err != nil {
			t.Fatalf("SaveAndActivateForUser(%s): %v", sess.CharacterName, err)
		}
	}

	done := make(chan []*Session, 1)
	go func() {
		done <- store.ListForUser(userID)
	}()

	select {
	case sessions := <-done:
		if len(sessions) != 2 {
			t.Fatalf("ListForUser returned %d sessions, want 2: %+v", len(sessions), sessions)
		}
		if sessions[0].AccessToken == "" || sessions[0].RefreshToken == "" {
			t.Fatalf("ListForUser did not decrypt tokens: %+v", sessions[0])
		}
	case <-time.After(2 * time.Second):
		store.db.SetMaxOpenConns(2)
		t.Fatal("ListForUser hung while decrypting vault tokens with rows still open")
	}
}

func TestSessionStore_PrivateVaultRequiresUnlock(t *testing.T) {
	store := newVaultSessionStoreForTest(t)
	userID := "u-vault-private"
	passphrase := "correct horse battery staple"
	if err := store.Vault().SetupPrivateForUser(userID, passphrase); err != nil {
		t.Fatalf("SetupPrivateForUser: %v", err)
	}
	if err := store.SaveAndActivateForUser(userID, &Session{
		CharacterID:   202,
		CharacterName: "Private Pilot",
		AccessToken:   "private-access",
		RefreshToken:  "private-refresh",
		ExpiresAt:     time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveAndActivateForUser: %v", err)
	}
	store.Vault().LockForUser(userID)
	if got := store.GetForUser(userID); got != nil {
		t.Fatalf("locked private vault returned session: %+v", got)
	}
	if err := store.Vault().UnlockPrivateForUser(userID, "wrong passphrase"); err == nil {
		t.Fatal("expected wrong passphrase to fail")
	}
	if err := store.Vault().UnlockPrivateForUser(userID, passphrase); err != nil {
		t.Fatalf("UnlockPrivateForUser: %v", err)
	}
	if got := store.GetForUser(userID); got == nil || got.AccessToken != "private-access" || got.RefreshToken != "private-refresh" {
		t.Fatalf("unlocked private vault session = %+v", got)
	}
}

func TestTokenVault_SetupPurgesLegacyPlaintextAuth(t *testing.T) {
	store := newVaultSessionStoreForTest(t)
	userID := "u-vault-migration"
	if _, err := store.db.Exec(`
		INSERT INTO auth_session (user_id, character_id, character_name, access_token, refresh_token, expires_at, is_active)
		VALUES (?, 303, 'Legacy Pilot', 'plain-access', 'plain-refresh', ?, 1)`, userID, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatalf("insert legacy auth: %v", err)
	}
	if !store.Vault().HasLegacyPlaintextAuth(userID) {
		t.Fatal("expected legacy plaintext auth before setup")
	}
	if err := store.Vault().SetupStandardForUser(userID); err != nil {
		t.Fatalf("SetupStandardForUser: %v", err)
	}
	if store.Vault().HasLegacyPlaintextAuth(userID) {
		t.Fatal("legacy plaintext auth should be purged after setup")
	}
	if got := store.GetForUser(userID); got != nil {
		t.Fatalf("setup should force relogin, got session %+v", got)
	}
}

func TestTokenVault_ProtectPrivateFieldRequiresConfiguredVault(t *testing.T) {
	store := newVaultSessionStoreForTest(t)
	if _, err := store.Vault().ProtectStringForStorage("u-no-vault", "config.alert_telegram_token", "secret-token"); err == nil {
		t.Fatal("expected private field protection to fail before vault setup")
	}
}

func TestTokenVault_SetupEncryptsLegacyPrivateFields(t *testing.T) {
	store := newVaultSessionStoreForTest(t)
	userID := "u-vault-legacy-fields"
	if _, err := store.db.Exec(`
		CREATE TABLE config (
			user_id TEXT NOT NULL,
			key     TEXT NOT NULL,
			value   TEXT NOT NULL,
			PRIMARY KEY (user_id, key)
		);
		INSERT INTO config (user_id, key, value)
		VALUES (?, 'alert_telegram_token', 'plain-telegram-token')`, userID); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if _, err := store.db.Exec(`
		CREATE TABLE industry_projects (
			id      INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			notes   TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE industry_jobs (
			id      INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			notes   TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO industry_projects (user_id, notes)
		VALUES (?, 'plain-project-notes');
		INSERT INTO industry_jobs (user_id, notes)
		VALUES (?, 'plain-job-notes')`, userID, userID); err != nil {
		t.Fatalf("seed industry notes: %v", err)
	}
	if _, err := store.db.Exec(`
		CREATE TABLE wallet_archive_sync (
			user_id                TEXT NOT NULL,
			character_id           INTEGER NOT NULL,
			wallet_balance         REAL NOT NULL DEFAULT 0,
			wallet_balance_private TEXT NOT NULL DEFAULT '',
			updated_at             TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (user_id, character_id)
		);
		INSERT INTO wallet_archive_sync (user_id, character_id, wallet_balance, updated_at)
		VALUES (?, 9001, 123456.78, '2026-06-03T00:00:00Z')`, userID); err != nil {
		t.Fatalf("seed wallet balance: %v", err)
	}

	if err := store.Vault().SetupStandardForUser(userID); err != nil {
		t.Fatalf("SetupStandardForUser: %v", err)
	}

	var stored string
	if err := store.db.QueryRow(`SELECT value FROM config WHERE user_id = ? AND key = 'alert_telegram_token'`, userID).Scan(&stored); err != nil {
		t.Fatalf("query stored config: %v", err)
	}
	if !strings.HasPrefix(stored, vaultTokenPrefix) {
		t.Fatalf("legacy private config was not encrypted: %q", stored)
	}
	opened, err := store.Vault().OpenStringFromStorage(userID, "config.alert_telegram_token", stored)
	if err != nil {
		t.Fatalf("OpenStringFromStorage: %v", err)
	}
	if opened != "plain-telegram-token" {
		t.Fatalf("opened legacy private config = %q", opened)
	}
	for _, row := range []struct {
		name      string
		table     string
		purpose   string
		plaintext string
	}{
		{name: "industry project notes", table: "industry_projects", purpose: "industry_projects.notes", plaintext: "plain-project-notes"},
		{name: "industry job notes", table: "industry_jobs", purpose: "industry_jobs.notes", plaintext: "plain-job-notes"},
	} {
		var storedNotes string
		if err := store.db.QueryRow(`SELECT notes FROM `+row.table+` WHERE user_id = ?`, userID).Scan(&storedNotes); err != nil {
			t.Fatalf("query stored %s: %v", row.name, err)
		}
		if !strings.HasPrefix(storedNotes, vaultTokenPrefix) {
			t.Fatalf("legacy %s was not encrypted: %q", row.name, storedNotes)
		}
		openedNotes, err := store.Vault().OpenStringFromStorage(userID, row.purpose, storedNotes)
		if err != nil {
			t.Fatalf("open %s: %v", row.name, err)
		}
		if openedNotes != row.plaintext {
			t.Fatalf("opened %s = %q, want %q", row.name, openedNotes, row.plaintext)
		}
	}
	var legacyBalance float64
	var protectedBalance string
	if err := store.db.QueryRow(`SELECT wallet_balance, wallet_balance_private FROM wallet_archive_sync WHERE user_id = ? AND character_id = 9001`, userID).
		Scan(&legacyBalance, &protectedBalance); err != nil {
		t.Fatalf("query stored wallet balance: %v", err)
	}
	if legacyBalance != 0 {
		t.Fatalf("legacy wallet balance plaintext=%v, want 0", legacyBalance)
	}
	if !strings.HasPrefix(protectedBalance, vaultTokenPrefix) {
		t.Fatalf("legacy wallet balance was not encrypted: %q", protectedBalance)
	}
	openedBalance, err := store.Vault().OpenStringFromStorage(userID, "wallet_archive_sync.wallet_balance", protectedBalance)
	if err != nil {
		t.Fatalf("open wallet balance: %v", err)
	}
	if openedBalance != "123456.78" {
		t.Fatalf("opened wallet balance = %q, want 123456.78", openedBalance)
	}
}

func TestTokenVault_SetupRollsBackWhenLegacyMigrationFails(t *testing.T) {
	store := newVaultSessionStoreForTest(t)
	userID := "u-vault-rollback"
	if _, err := store.db.Exec(`
		INSERT INTO auth_session (user_id, character_id, character_name, access_token, refresh_token, expires_at, is_active)
		VALUES (?, 404, 'Rollback Pilot', 'plain-access', 'plain-refresh', ?, 1);
		CREATE TABLE config (
			user_id TEXT NOT NULL,
			key     TEXT NOT NULL,
			PRIMARY KEY (user_id, key)
		);
		INSERT INTO config (user_id, key)
		VALUES (?, 'alert_telegram_token')`, userID, time.Now().Add(time.Hour).Unix(), userID); err != nil {
		t.Fatalf("seed malformed legacy config: %v", err)
	}

	if err := store.Vault().SetupStandardForUser(userID); err == nil {
		t.Fatal("expected setup to fail on malformed legacy config")
	}

	var vaultRows int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM vault_state WHERE user_id = ?`, userID).Scan(&vaultRows); err != nil {
		t.Fatalf("query vault_state: %v", err)
	}
	if vaultRows != 0 {
		t.Fatalf("vault_state rows=%d, want rollback to 0", vaultRows)
	}
	if !store.Vault().HasLegacyPlaintextAuth(userID) {
		t.Fatal("legacy auth should remain when setup transaction rolls back")
	}
}

func TestTokenVault_CheckpointEncryptsNewLegacyWalletBalance(t *testing.T) {
	store := newVaultSessionStoreForTest(t)
	userID := "u-vault-checkpoint-wallet"
	if err := store.Vault().SetupStandardForUser(userID); err != nil {
		t.Fatalf("SetupStandardForUser: %v", err)
	}
	if _, err := store.db.Exec(`
		CREATE TABLE wallet_archive_sync (
			user_id                TEXT NOT NULL,
			character_id           INTEGER NOT NULL,
			wallet_balance         REAL NOT NULL DEFAULT 0,
			wallet_balance_private TEXT NOT NULL DEFAULT '',
			updated_at             TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (user_id, character_id)
		);
		INSERT INTO wallet_archive_sync (user_id, character_id, wallet_balance, updated_at)
		VALUES (?, 9002, 987654.32, '2026-06-03T00:00:00Z');
		UPDATE vault_state
		   SET checkpoint_version = 1
		 WHERE user_id = ?`, userID, userID); err != nil {
		t.Fatalf("seed checkpoint wallet balance: %v", err)
	}

	if _, err := store.Vault().ProtectStringForStorage(userID, "config.alert_telegram_token", "fresh-secret"); err != nil {
		t.Fatalf("trigger checkpoint: %v", err)
	}

	var checkpoint int
	var legacyBalance float64
	var protectedBalance string
	if err := store.db.QueryRow(`
		SELECT v.checkpoint_version, w.wallet_balance, w.wallet_balance_private
		  FROM vault_state v
		  JOIN wallet_archive_sync w ON w.user_id = v.user_id
		 WHERE v.user_id = ? AND w.character_id = 9002
	`, userID).Scan(&checkpoint, &legacyBalance, &protectedBalance); err != nil {
		t.Fatalf("query checkpoint wallet balance: %v", err)
	}
	if checkpoint != vaultCheckpointCurrent {
		t.Fatalf("checkpoint=%d, want %d", checkpoint, vaultCheckpointCurrent)
	}
	if legacyBalance != 0 {
		t.Fatalf("legacy wallet balance plaintext=%v, want 0", legacyBalance)
	}
	openedBalance, err := store.Vault().OpenStringFromStorage(userID, "wallet_archive_sync.wallet_balance", protectedBalance)
	if err != nil {
		t.Fatalf("open checkpoint wallet balance: %v", err)
	}
	if openedBalance != "987654.32" {
		t.Fatalf("opened checkpoint wallet balance = %q, want 987654.32", openedBalance)
	}
}

func TestSessionStore_EnsureValidTokenForUser_UsesUnexpiredTokenWithoutSSO(t *testing.T) {
	store := newSessionStoreForTokenTest(t)
	err := store.SaveAndActivateForUser("u1", &Session{
		CharacterID:   101,
		CharacterName: "Pilot One",
		AccessToken:   "access-token",
		RefreshToken:  "refresh-token",
		ExpiresAt:     time.Now().Add(15 * time.Minute),
	})
	if err != nil {
		t.Fatalf("SaveAndActivateForUser: %v", err)
	}

	token, err := store.EnsureValidTokenForUser(nil, "u1")
	if err != nil {
		t.Fatalf("EnsureValidTokenForUser: %v", err)
	}
	if token != "access-token" {
		t.Fatalf("token = %q, want access-token", token)
	}
}

func TestSessionStore_EnsureValidTokenForUser_ExpiredTokenRequiresSSO(t *testing.T) {
	store := newSessionStoreForTokenTest(t)
	err := store.SaveAndActivateForUser("u1", &Session{
		CharacterID:   101,
		CharacterName: "Pilot One",
		AccessToken:   "access-token",
		RefreshToken:  "refresh-token",
		ExpiresAt:     time.Now().Add(-2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("SaveAndActivateForUser: %v", err)
	}

	_, err = store.EnsureValidTokenForUser(nil, "u1")
	if err == nil {
		t.Fatal("expected error for expired token without sso")
	}
	if !strings.Contains(err.Error(), "sso not configured") {
		t.Fatalf("error = %v, want contains %q", err, "sso not configured")
	}
}
