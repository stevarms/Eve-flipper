package db

import (
	"testing"
	"time"

	"eve-flipper/internal/corp"
	"eve-flipper/internal/esi"
)

// A corp job is visible from two ESI endpoints at once: the corporation's
// industry list and the installer character's own list. Keying the archive on
// (user, installer, job_id) is what makes that harmless — the second write
// updates the first row instead of adding a duplicate that would double every
// manufactured unit in the Trade Journal.
func TestCorpAndCharacterIndustryJobConverge(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	const userID = "corp-job-user"
	const installer = int64(4001)
	const corpID = int64(98000001)
	const jobID = int64(700000123)

	if _, err := d.UpsertCorpIndustryJobsForUser(userID, corpID, []corp.CorpIndustryJob{{
		JobID: jobID, InstallerID: installer, ActivityID: 1, Status: "delivered",
		BlueprintTypeID: 1002, ProductTypeID: 34, ProductName: "Tritanium",
		Runs: 10, SuccessfulRuns: 10, Cost: 12345,
		StartDate: "2026-05-01T00:00:00Z", EndDate: "2026-05-02T00:00:00Z",
		CompletedDate: "2026-05-02T00:00:00Z", LocationID: 60003760,
	}}); err != nil {
		t.Fatalf("corp upsert: %v", err)
	}

	// Same job, now seen from the installer's character endpoint.
	if _, err := d.UpsertIndustryJobsForUser(userID, installer, []esi.CharacterIndustryJob{{
		JobID: jobID, InstallerID: installer, ActivityID: 1, Status: "delivered",
		BlueprintTypeID: 1002, ProductTypeID: 34, ProductTypeName: "Tritanium",
		Runs: 10, SuccessfulRuns: 10, Cost: 12345,
		StartDate: "2026-05-01T00:00:00Z", EndDate: "2026-05-02T00:00:00Z",
		CompletedDate: "2026-05-02T00:00:00Z", OutputLocationID: 60003760,
	}}); err != nil {
		t.Fatalf("character upsert: %v", err)
	}

	jobs, err := d.ListArchivedIndustryJobsForUser(userID, []int64{installer}, time.Time{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d rows, want exactly 1 — the job must not be counted twice: %+v", len(jobs), jobs)
	}
	// Provenance must survive the character-side write, which knows nothing
	// about the corporation and passes 0.
	if jobs[0].CorporationID != corpID {
		t.Errorf("corporation_id = %d after character re-upsert, want %d (must not be blanked)", jobs[0].CorporationID, corpID)
	}
}

// Selecting only a corporation used to pull in every character's jobs, because
// the DB layer reads an empty character list as "all characters". Manufacturing
// figures for a corp-only scope were therefore the whole account's.
func TestListArchivedIndustryJobsForScopeDoesNotLeakCharacters(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	const userID = "scope-job-user"
	const corpInstaller = int64(4101)
	const soloChar = int64(4102)
	const corpID = int64(98000002)

	if _, err := d.UpsertCorpIndustryJobsForUser(userID, corpID, []corp.CorpIndustryJob{{
		JobID: 1, InstallerID: corpInstaller, ActivityID: 1, Status: "delivered",
		ProductTypeID: 34, Runs: 1, SuccessfulRuns: 1,
		StartDate: "2026-05-01T00:00:00Z", EndDate: "2026-05-02T00:00:00Z",
	}}); err != nil {
		t.Fatalf("corp upsert: %v", err)
	}
	if _, err := d.UpsertIndustryJobsForUser(userID, soloChar, []esi.CharacterIndustryJob{{
		JobID: 2, InstallerID: soloChar, ActivityID: 1, Status: "delivered",
		ProductTypeID: 35, Runs: 1, SuccessfulRuns: 1,
		StartDate: "2026-05-01T00:00:00Z", EndDate: "2026-05-02T00:00:00Z",
	}}); err != nil {
		t.Fatalf("character upsert: %v", err)
	}

	corpOnly, err := d.ListArchivedIndustryJobsForScope(userID, WalletScopeFilter{
		IncludeCorpDivisions: []CorpDivisionKey{{CorporationID: corpID, Division: 1}},
	}, time.Time{})
	if err != nil {
		t.Fatalf("corp-only read: %v", err)
	}
	if len(corpOnly) != 1 || corpOnly[0].JobID != 1 {
		t.Fatalf("corp-only read returned %+v, want only the corp job", corpOnly)
	}

	charOnly, err := d.ListArchivedIndustryJobsForScope(userID, WalletScopeFilter{
		IncludeCharacters: []int64{soloChar},
	}, time.Time{})
	if err != nil {
		t.Fatalf("character-only read: %v", err)
	}
	if len(charOnly) != 1 || charOnly[0].JobID != 2 {
		t.Fatalf("character-only read returned %+v, want only the character job", charOnly)
	}

	// A filter naming no owner at all must return nothing rather than
	// everything — that inversion is the bug this function exists to avoid.
	empty, err := d.ListArchivedIndustryJobsForScope(userID, WalletScopeFilter{}, time.Time{})
	if err != nil {
		t.Fatalf("empty read: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty filter returned %d rows, want 0", len(empty))
	}

	all, err := d.ListArchivedIndustryJobsForScope(userID, WalletScopeFilter{IncludeAll: true}, time.Time{})
	if err != nil {
		t.Fatalf("all read: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("IncludeAll returned %d rows, want 2", len(all))
	}
}

// The owner-aware transaction reader has to merge two tables with different
// shapes and tell the caller which wallet each row came from.
func TestListArchivedWalletTransactionsForScopeUnionsOwners(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	const userID = "owner-txn-user"
	const charID = int64(4201)
	const corpID = int64(98000003)

	if _, err := d.UpsertWalletTransactionsForUser(userID, charID, []esi.WalletTransaction{{
		TransactionID: 11, Date: "2026-05-01T10:00:00Z", TypeID: 34,
		LocationID: 60003760, UnitPrice: 5, Quantity: 100, IsBuy: true,
	}}); err != nil {
		t.Fatalf("char upsert: %v", err)
	}
	if _, err := d.UpsertCorpWalletTransactionsForUser(userID, corpID, 2, []corp.CorpTransaction{{
		TransactionID: 22, Date: "2026-05-03T10:00:00Z", TypeID: 35,
		LocationID: 60008494, UnitPrice: 9, Quantity: 50, IsBuy: false,
	}}); err != nil {
		t.Fatalf("corp upsert: %v", err)
	}

	both, err := d.ListArchivedWalletTransactionsForScope(userID, WalletScopeFilter{IncludeAll: true}, time.Time{}, 0)
	if err != nil {
		t.Fatalf("combined read: %v", err)
	}
	if len(both) != 2 {
		t.Fatalf("combined read returned %d rows, want 2: %+v", len(both), both)
	}
	// Newest first, across both tables.
	if both[0].TransactionID != 22 || both[0].WalletKey != "corp:98000003:2" {
		t.Errorf("first row = %+v, want the corp row keyed corp:98000003:2", both[0])
	}
	if both[1].WalletKey != "char:4201" {
		t.Errorf("second row wallet key = %q, want char:4201", both[1].WalletKey)
	}

	corpOnly, err := d.ListArchivedWalletTransactionsForScope(userID, WalletScopeFilter{
		IncludeCorpDivisions: []CorpDivisionKey{{CorporationID: corpID, Division: 2}},
	}, time.Time{}, 0)
	if err != nil {
		t.Fatalf("corp-only read: %v", err)
	}
	if len(corpOnly) != 1 || corpOnly[0].TransactionID != 22 {
		t.Fatalf("corp-only read returned %+v, want only the corp transaction", corpOnly)
	}

	none, err := d.ListArchivedWalletTransactionsForScope(userID, WalletScopeFilter{}, time.Time{}, 0)
	if err != nil {
		t.Fatalf("empty read: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("empty filter returned %d rows, want 0", len(none))
	}
}
