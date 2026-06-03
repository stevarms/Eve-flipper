package db

import (
	"strings"
	"testing"
	"time"

	"eve-flipper/internal/auth"
	"eve-flipper/internal/config"
	"eve-flipper/internal/esi"
)

type fakePrivacyCodec struct{}

func (fakePrivacyCodec) ProtectStringForStorage(_ string, purpose string, value string) (string, error) {
	if strings.TrimSpace(value) == "" || strings.HasPrefix(value, "testenc:") {
		return value, nil
	}
	return "testenc:" + purpose + ":" + value, nil
}

func (fakePrivacyCodec) OpenStringFromStorage(_ string, purpose string, value string) (string, error) {
	prefix := "testenc:" + purpose + ":"
	if strings.HasPrefix(value, prefix) {
		return strings.TrimPrefix(value, prefix), nil
	}
	return value, nil
}

func TestPaperTradePrivateFieldsUsePrivacyCodec(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	d.SetPrivacyCodec(fakePrivacyCodec{})

	trade, err := d.CreatePaperTradeForUser("privacy-user", PaperTradeCreateInput{
		TypeID:          34,
		TypeName:        "Tritanium",
		PlannedQuantity: 10,
		PlannedBuyPrice: 5,
		Notes:           "private mission notes",
		Source:          "mission-control",
	})
	if err != nil {
		t.Fatalf("create paper trade: %v", err)
	}
	if trade.Notes != "private mission notes" || trade.Source != "mission-control" {
		t.Fatalf("create returned plaintext mismatch: %+v", trade)
	}

	var storedNotes, storedSource string
	if err := d.sql.QueryRow(`SELECT notes, source FROM paper_trades WHERE id = ?`, trade.ID).Scan(&storedNotes, &storedSource); err != nil {
		t.Fatalf("raw query: %v", err)
	}
	if storedNotes == "private mission notes" || storedSource == "mission-control" {
		t.Fatalf("private fields stored as plaintext: notes=%q source=%q", storedNotes, storedSource)
	}

	loaded, err := d.GetPaperTradeForUser("privacy-user", trade.ID)
	if err != nil {
		t.Fatalf("get paper trade: %v", err)
	}
	if loaded.Notes != "private mission notes" || loaded.Source != "mission-control" {
		t.Fatalf("loaded plaintext mismatch: %+v", loaded)
	}

	nextNotes := "updated private notes"
	updated, err := d.UpdatePaperTradeForUser("privacy-user", trade.ID, PaperTradeUpdateInput{Notes: &nextNotes})
	if err != nil {
		t.Fatalf("update paper trade: %v", err)
	}
	if updated.Notes != nextNotes {
		t.Fatalf("updated notes=%q, want %q", updated.Notes, nextNotes)
	}
	if err := d.sql.QueryRow(`SELECT notes FROM paper_trades WHERE id = ?`, trade.ID).Scan(&storedNotes); err != nil {
		t.Fatalf("raw query after update: %v", err)
	}
	if storedNotes == nextNotes {
		t.Fatalf("updated notes stored as plaintext")
	}
}

func TestIndustryNotesUsePrivacyCodec(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	d.SetPrivacyCodec(fakePrivacyCodec{})

	userID := "privacy-industry"
	project, err := d.CreateIndustryProjectForUser(userID, IndustryProjectCreateInput{
		Name:  "Private industry",
		Notes: "project private notes",
	})
	if err != nil {
		t.Fatalf("create industry project: %v", err)
	}
	if project.Notes != "project private notes" {
		t.Fatalf("created project notes=%q, want plaintext", project.Notes)
	}

	var storedProjectNotes string
	if err := d.sql.QueryRow(`SELECT notes FROM industry_projects WHERE id = ?`, project.ID).Scan(&storedProjectNotes); err != nil {
		t.Fatalf("raw project notes: %v", err)
	}
	if !strings.HasPrefix(storedProjectNotes, "testenc:industry_projects.notes:") {
		t.Fatalf("project notes were not protected: %q", storedProjectNotes)
	}
	loadedProject, err := d.GetIndustryProjectForUser(userID, project.ID)
	if err != nil {
		t.Fatalf("get industry project: %v", err)
	}
	if loadedProject.Notes != "project private notes" {
		t.Fatalf("loaded project notes=%q, want plaintext", loadedProject.Notes)
	}
	projects, err := d.ListIndustryProjectsForUser(userID, "", 10)
	if err != nil {
		t.Fatalf("list industry projects: %v", err)
	}
	if len(projects) != 1 || projects[0].Notes != "project private notes" {
		t.Fatalf("listed projects mismatch: %+v", projects)
	}

	if _, err := d.ApplyIndustryPlanForUser(userID, project.ID, IndustryPlanPatch{
		Replace: true,
		Jobs: []IndustryJobPlanInput{
			{
				Activity: "manufacturing",
				Runs:     1,
				Status:   IndustryJobStatusPlanned,
				Notes:    "job private notes",
			},
		},
	}); err != nil {
		t.Fatalf("apply industry plan: %v", err)
	}
	snapshot, err := d.GetIndustryProjectSnapshotForUser(userID, project.ID)
	if err != nil {
		t.Fatalf("snapshot industry project: %v", err)
	}
	if len(snapshot.Jobs) != 1 {
		t.Fatalf("snapshot jobs len=%d, want 1", len(snapshot.Jobs))
	}
	job := snapshot.Jobs[0]
	if job.Notes != "job private notes" {
		t.Fatalf("snapshot job notes=%q, want plaintext", job.Notes)
	}
	var storedJobNotes string
	if err := d.sql.QueryRow(`SELECT notes FROM industry_jobs WHERE id = ?`, job.ID).Scan(&storedJobNotes); err != nil {
		t.Fatalf("raw job notes: %v", err)
	}
	if !strings.HasPrefix(storedJobNotes, "testenc:industry_jobs.notes:") {
		t.Fatalf("job notes were not protected: %q", storedJobNotes)
	}

	ledger, err := d.GetIndustryLedgerForUser(userID, IndustryLedgerOptions{ProjectID: project.ID, Limit: 10})
	if err != nil {
		t.Fatalf("industry ledger: %v", err)
	}
	if len(ledger.Entries) != 1 || ledger.Entries[0].Notes != "job private notes" {
		t.Fatalf("ledger entries mismatch: %+v", ledger.Entries)
	}

	updated, err := d.UpdateIndustryJobStatusForUser(userID, job.ID, IndustryJobStatusActive, "", "", "updated job private notes")
	if err != nil {
		t.Fatalf("update industry job status: %v", err)
	}
	if updated.Notes != "updated job private notes" {
		t.Fatalf("updated job notes=%q, want plaintext", updated.Notes)
	}
	if err := d.sql.QueryRow(`SELECT notes FROM industry_jobs WHERE id = ?`, job.ID).Scan(&storedJobNotes); err != nil {
		t.Fatalf("raw job notes after update: %v", err)
	}
	if !strings.HasPrefix(storedJobNotes, "testenc:industry_jobs.notes:") {
		t.Fatalf("updated job notes were not protected: %q", storedJobNotes)
	}

	bulkUpdated, err := d.UpdateIndustryJobStatusesForUser(userID, []int64{job.ID}, IndustryJobStatusCompleted, "", "", "bulk job private notes")
	if err != nil {
		t.Fatalf("bulk update industry job status: %v", err)
	}
	if len(bulkUpdated) != 1 || bulkUpdated[0].Notes != "bulk job private notes" {
		t.Fatalf("bulk updated jobs mismatch: %+v", bulkUpdated)
	}
	if err := d.sql.QueryRow(`SELECT notes FROM industry_jobs WHERE id = ?`, job.ID).Scan(&storedJobNotes); err != nil {
		t.Fatalf("raw job notes after bulk update: %v", err)
	}
	if !strings.HasPrefix(storedJobNotes, "testenc:industry_jobs.notes:") {
		t.Fatalf("bulk updated job notes were not protected: %q", storedJobNotes)
	}
}

func TestWalletJournalPrivateFieldsUsePrivacyCodec(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	d.SetPrivacyCodec(fakePrivacyCodec{})

	const userID = "privacy-wallet"
	const characterID int64 = 9001
	if _, err := d.UpsertWalletJournalForUser(userID, characterID, []esi.WalletJournalEntry{
		{
			ID:            77,
			Date:          "2026-06-01T12:00:00Z",
			RefType:       "player_donation",
			Amount:        123,
			Reason:        "private reason",
			Description:   "private description",
			ContextIDType: "private_context",
		},
	}); err != nil {
		t.Fatalf("upsert wallet journal: %v", err)
	}

	var reason, description, contextIDType string
	if err := d.sql.QueryRow(`SELECT reason, description, context_id_type FROM wallet_journal_archive WHERE user_id = ? AND entry_id = ?`, userID, 77).
		Scan(&reason, &description, &contextIDType); err != nil {
		t.Fatalf("raw query: %v", err)
	}
	if reason == "private reason" || description == "private description" || contextIDType == "private_context" {
		t.Fatalf("journal private fields stored as plaintext: %q/%q/%q", reason, description, contextIDType)
	}

	rows, err := d.ListArchivedWalletJournal(userID, []int64{characterID}, time.Time{}, 10)
	if err != nil {
		t.Fatalf("list wallet journal: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("journal len=%d, want 1", len(rows))
	}
	row := rows[0]
	if row.Reason != "private reason" || row.Description != "private description" || row.ContextIDType != "private_context" {
		t.Fatalf("decrypted journal mismatch: %+v", row)
	}
}

func TestCurrentWalletBalanceAndSPUsePrivacyCodec(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	d.SetPrivacyCodec(fakePrivacyCodec{})

	const userID = "privacy-current-metrics"
	const characterID int64 = 9002
	if err := d.UpdateWalletArchiveBalance(userID, characterID, 123456789.25); err != nil {
		t.Fatalf("update wallet balance: %v", err)
	}
	if err := d.UpdateCharacterTotalSPForUser(userID, characterID, 4853852); err != nil {
		t.Fatalf("update total SP: %v", err)
	}

	var legacyBalance float64
	var protectedBalance, protectedSP string
	if err := d.sql.QueryRow(`
		SELECT wallet_balance, wallet_balance_private, total_sp_private
		  FROM wallet_archive_sync
		 WHERE user_id = ? AND character_id = ?
	`, userID, characterID).Scan(&legacyBalance, &protectedBalance, &protectedSP); err != nil {
		t.Fatalf("raw current metrics: %v", err)
	}
	if legacyBalance != 0 {
		t.Fatalf("legacy wallet balance plaintext=%v, want 0", legacyBalance)
	}
	if !strings.HasPrefix(protectedBalance, "testenc:wallet_archive_sync.wallet_balance:") {
		t.Fatalf("wallet balance was not protected: %q", protectedBalance)
	}
	if !strings.HasPrefix(protectedSP, "testenc:wallet_archive_sync.total_sp:") {
		t.Fatalf("total SP was not protected: %q", protectedSP)
	}

	metrics, err := d.GetCharacterPrivateMetricsForUser(userID, characterID)
	if err != nil {
		t.Fatalf("get private metrics: %v", err)
	}
	if !metrics.HasWalletBalance || metrics.WalletBalance != 123456789.25 {
		t.Fatalf("wallet metrics=%+v", metrics)
	}
	if !metrics.HasTotalSP || metrics.TotalSP != 4853852 {
		t.Fatalf("SP metrics=%+v", metrics)
	}
}

func TestConfigPrivateFieldsUsePrivacyCodec(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	d.SetPrivacyCodec(fakePrivacyCodec{})

	userID := "privacy-config"
	cfg := config.Default()
	cfg.AlertTelegramToken = "telegram-secret"
	cfg.AlertTelegramChatID = "chat-secret"
	cfg.AlertDiscordWebhook = "discord-secret"
	if err := d.SaveConfigForUser(userID, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	for _, key := range []string{"alert_telegram_token", "alert_telegram_chat_id", "alert_discord_webhook"} {
		var stored string
		if err := d.sql.QueryRow(`SELECT value FROM config WHERE user_id = ? AND key = ?`, userID, key).Scan(&stored); err != nil {
			t.Fatalf("raw config %s: %v", key, err)
		}
		if !strings.HasPrefix(stored, "testenc:config."+key+":") {
			t.Fatalf("config %s was not protected: %q", key, stored)
		}
	}

	loaded := d.LoadConfigForUser(userID)
	if loaded.AlertTelegramToken != "telegram-secret" ||
		loaded.AlertTelegramChatID != "chat-secret" ||
		loaded.AlertDiscordWebhook != "discord-secret" {
		t.Fatalf("loaded config secrets mismatch: %+v", loaded)
	}
}

func TestCockpitPayloadUsesPrivacyCodec(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	d.SetPrivacyCodec(fakePrivacyCodec{})

	userID := "privacy-cockpit"
	payload := `{"startupTab":"station","scanParams":{"system":"Jita"}}`
	row, err := d.UpsertCockpitLoadoutForUser(userID, "default", "Default cockpit", payload, true)
	if err != nil {
		t.Fatalf("upsert cockpit: %v", err)
	}
	if row.PayloadJSON != payload {
		t.Fatalf("returned payload=%q, want plaintext", row.PayloadJSON)
	}

	var storedLoadout, storedPrefs string
	if err := d.sql.QueryRow(`SELECT payload_json FROM cockpit_loadouts WHERE user_id = ? AND loadout_id = ?`, userID, "default").Scan(&storedLoadout); err != nil {
		t.Fatalf("raw loadout: %v", err)
	}
	if err := d.sql.QueryRow(`SELECT payload_json FROM cockpit_preferences WHERE user_id = ?`, userID).Scan(&storedPrefs); err != nil {
		t.Fatalf("raw prefs: %v", err)
	}
	if storedLoadout == payload || storedPrefs == payload {
		t.Fatalf("cockpit payload stored as plaintext: loadout=%q prefs=%q", storedLoadout, storedPrefs)
	}

	active, ok, err := d.ActiveCockpitLoadoutForUser(userID)
	if err != nil {
		t.Fatalf("active cockpit: %v", err)
	}
	if !ok || active.PayloadJSON != payload {
		t.Fatalf("active cockpit mismatch ok=%v row=%+v", ok, active)
	}
	loadedPayload, _, ok, err := d.LoadCockpitPreferencesForUser(userID)
	if err != nil {
		t.Fatalf("load cockpit prefs: %v", err)
	}
	if !ok || loadedPayload != payload {
		t.Fatalf("loaded cockpit prefs mismatch ok=%v payload=%q", ok, loadedPayload)
	}
}

func TestRealVaultPrivacyCodecWritesWithSingleDBConnection(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	store := auth.NewSessionStore(d.SqlDB())
	d.SetPrivacyCodec(store.Vault())
	const userID = "real-vault-privacy"
	if err := store.Vault().SetupStandardForUser(userID); err != nil {
		t.Fatalf("SetupStandardForUser: %v", err)
	}

	cfg := config.Default()
	cfg.AlertTelegramToken = "real-token"
	if err := d.SaveConfigForUser(userID, cfg); err != nil {
		t.Fatalf("SaveConfigForUser: %v", err)
	}
	if _, err := d.UpsertCockpitLoadoutForUser(userID, "default", "Default cockpit", `{"startupTab":"station"}`, true); err != nil {
		t.Fatalf("UpsertCockpitLoadoutForUser: %v", err)
	}
	if _, err := d.UpsertWalletJournalForUser(userID, 42, []esi.WalletJournalEntry{{
		ID:            900,
		Date:          "2026-06-01T12:00:00Z",
		RefType:       "player_donation",
		Reason:        "real reason",
		Description:   "real description",
		ContextIDType: "real_context",
	}}); err != nil {
		t.Fatalf("UpsertWalletJournalForUser: %v", err)
	}
	if err := d.UpdateWalletArchiveBalance(userID, 42, 987654321.75); err != nil {
		t.Fatalf("UpdateWalletArchiveBalance: %v", err)
	}
	if err := d.UpdateCharacterTotalSPForUser(userID, 42, 4853852); err != nil {
		t.Fatalf("UpdateCharacterTotalSPForUser: %v", err)
	}
	if _, err := d.CreatePaperTradeForUser(userID, PaperTradeCreateInput{
		TypeID:          34,
		TypeName:        "Tritanium",
		PlannedQuantity: 10,
		PlannedBuyPrice: 5,
		Notes:           "real notes",
		Source:          "real source",
	}); err != nil {
		t.Fatalf("CreatePaperTradeForUser: %v", err)
	}
	project, err := d.CreateIndustryProjectForUser(userID, IndustryProjectCreateInput{
		Name:  "Real vault industry",
		Notes: "real project notes",
	})
	if err != nil {
		t.Fatalf("CreateIndustryProjectForUser: %v", err)
	}
	if _, err := d.ApplyIndustryPlanForUser(userID, project.ID, IndustryPlanPatch{
		Replace: true,
		Jobs: []IndustryJobPlanInput{
			{
				Activity: "manufacturing",
				Runs:     1,
				Status:   IndustryJobStatusPlanned,
				Notes:    "real job notes",
			},
		},
	}); err != nil {
		t.Fatalf("ApplyIndustryPlanForUser: %v", err)
	}

	if got := d.LoadConfigForUser(userID); got.AlertTelegramToken != "real-token" {
		t.Fatalf("LoadConfigForUser token = %q", got.AlertTelegramToken)
	}
	if rows, err := d.ListCockpitLoadoutsForUser(userID); err != nil || len(rows) != 1 || rows[0].PayloadJSON != `{"startupTab":"station"}` {
		t.Fatalf("ListCockpitLoadoutsForUser rows=%+v err=%v", rows, err)
	}
	if rows, err := d.ListArchivedWalletJournal(userID, []int64{42}, time.Time{}, 10); err != nil || len(rows) != 1 || rows[0].Reason != "real reason" {
		t.Fatalf("ListArchivedWalletJournal rows=%+v err=%v", rows, err)
	}
	if metrics, err := d.GetCharacterPrivateMetricsForUser(userID, 42); err != nil || !metrics.HasWalletBalance || metrics.WalletBalance != 987654321.75 || !metrics.HasTotalSP || metrics.TotalSP != 4853852 {
		t.Fatalf("GetCharacterPrivateMetricsForUser metrics=%+v err=%v", metrics, err)
	}
	if rows, err := d.ListPaperTradesForUser(userID, "all", 10); err != nil || len(rows) != 1 || rows[0].Notes != "real notes" {
		t.Fatalf("ListPaperTradesForUser rows=%+v err=%v", rows, err)
	}
	if rows, err := d.ListIndustryProjectsForUser(userID, "", 10); err != nil || len(rows) != 1 || rows[0].Notes != "real project notes" {
		t.Fatalf("ListIndustryProjectsForUser rows=%+v err=%v", rows, err)
	}
	snapshot, err := d.GetIndustryProjectSnapshotForUser(userID, project.ID)
	if err != nil {
		t.Fatalf("GetIndustryProjectSnapshotForUser: %v", err)
	}
	if len(snapshot.Jobs) != 1 || snapshot.Jobs[0].Notes != "real job notes" {
		t.Fatalf("GetIndustryProjectSnapshotForUser jobs=%+v", snapshot.Jobs)
	}
	jobID := snapshot.Jobs[0].ID
	if ledger, err := d.GetIndustryLedgerForUser(userID, IndustryLedgerOptions{ProjectID: project.ID, Limit: 10}); err != nil || len(ledger.Entries) != 1 || ledger.Entries[0].Notes != "real job notes" {
		t.Fatalf("GetIndustryLedgerForUser ledger=%+v err=%v", ledger, err)
	}

	for _, query := range []struct {
		name string
		sql  string
		args []any
	}{
		{
			name: "config token",
			sql:  `SELECT value FROM config WHERE user_id = ? AND key = 'alert_telegram_token'`,
			args: []any{userID},
		},
		{
			name: "cockpit payload",
			sql:  `SELECT payload_json FROM cockpit_loadouts WHERE user_id = ? AND loadout_id = 'default'`,
			args: []any{userID},
		},
		{
			name: "journal reason",
			sql:  `SELECT reason FROM wallet_journal_archive WHERE user_id = ? AND entry_id = 900`,
			args: []any{userID},
		},
		{
			name: "wallet balance",
			sql:  `SELECT wallet_balance_private FROM wallet_archive_sync WHERE user_id = ? AND character_id = 42`,
			args: []any{userID},
		},
		{
			name: "total SP",
			sql:  `SELECT total_sp_private FROM wallet_archive_sync WHERE user_id = ? AND character_id = 42`,
			args: []any{userID},
		},
		{
			name: "industry project notes",
			sql:  `SELECT notes FROM industry_projects WHERE user_id = ? AND id = ?`,
			args: []any{userID, project.ID},
		},
		{
			name: "industry job notes",
			sql:  `SELECT notes FROM industry_jobs WHERE user_id = ? AND id = ?`,
			args: []any{userID, jobID},
		},
	} {
		var stored string
		if err := d.sql.QueryRow(query.sql, query.args...).Scan(&stored); err != nil {
			t.Fatalf("%s raw query: %v", query.name, err)
		}
		if !strings.HasPrefix(stored, "evf:vault:v1:") {
			t.Fatalf("%s was not vault encrypted: %q", query.name, stored)
		}
	}
}
