package api

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"eve-flipper/internal/auth"
	"eve-flipper/internal/corp"
	"eve-flipper/internal/db"
	"eve-flipper/internal/engine"
	"eve-flipper/internal/esi"
	"eve-flipper/internal/sde"
)

// Trade Journal — FIFO realized P&L across trading + manufacturing.
//
// This file wires the api layer for the /api/auth/journal/* endpoints. The
// heavy lifting lives elsewhere:
//   - engine.ComputeTradeJournal  runs the FIFO event loop.
//   - db.ListArchivedWalletActivityForUser + friends read the archive.
//   - db.UpsertWalletTransactionsForUser + corp/industry variants sync
//     the archive from ESI. Called by handleTradeJournalSync below.
//
// Design shortcuts documented in the plan:
//   - Fees follow the existing Portfolio engine's flat-rate model.
//   - Broker fee attribution is not per-order (would require archiving
//     character orders). Sales tax uses the flat rate too for MVP.
//   - Manufacturing v1 covers activity_id = 1 only.

// tradeJournalCacheTTL bounds recomputation cost. GETs within this window
// return the previous result. Any POST (sync or link-job) clears the cache.
const tradeJournalCacheTTL = 60 * time.Second

// tradeJournalCache holds one entry per (userID, scope, sinceDate, fifoMode).
type tradeJournalCacheEntry struct {
	result   *engine.TradeJournalResult
	cachedAt time.Time
}

type tradeJournalRuntime struct {
	mu    sync.Mutex
	cache map[string]tradeJournalCacheEntry
	// singleflight collapses concurrent duplicate compute requests. The
	// Trade Journal tab fires two GETs (/journal/summary + /journal/by-type)
	// in parallel from Promise.all, and each was running the full compute
	// pipeline (SQLite reads → market prices → per-character blueprint fan
	// out → engine.ComputeTradeJournal) on a cold cache — 2× the work for
	// every fresh tab-open. With singleflight, the second caller waits on
	// the first and receives the same *TradeJournalResult.
	group singleflight.Group
}

var journalRuntime = &tradeJournalRuntime{cache: make(map[string]tradeJournalCacheEntry)}

func (rt *tradeJournalRuntime) get(key string) *engine.TradeJournalResult {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if e, ok := rt.cache[key]; ok && time.Since(e.cachedAt) < tradeJournalCacheTTL {
		return e.result
	}
	return nil
}

func (rt *tradeJournalRuntime) put(key string, r *engine.TradeJournalResult) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.cache[key] = tradeJournalCacheEntry{result: r, cachedAt: time.Now()}
}

func (rt *tradeJournalRuntime) invalidateUser(userID string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	prefix := userID + "|"
	for k := range rt.cache {
		if strings.HasPrefix(k, prefix) {
			delete(rt.cache, k)
		}
	}
}

// --- request / response types (wire) ---

type journalSyncRequest struct {
	Wallets *walletScopeFilterWire `json:"wallets,omitempty"`
}

type walletScopeFilterWire struct {
	IncludeAll        bool             `json:"include_all,omitempty"`
	IncludeCharacters []int64          `json:"include_characters,omitempty"`
	IncludeCorpDivs   []corpDivisionKW `json:"include_corp_divisions,omitempty"`
}

type corpDivisionKW struct {
	CorporationID int64 `json:"corporation_id"`
	Division      int   `json:"division"`
}

// journalSyncWalletStat is one line per synced wallet in the response.
type journalSyncWalletStat struct {
	WalletKind       string `json:"wallet_kind"` // "character" | "corporation"
	CharacterID      int64  `json:"character_id,omitempty"`
	CorporationID    int64  `json:"corporation_id,omitempty"`
	Division         int    `json:"division,omitempty"`
	SyncedAt         string `json:"synced_at"`
	LiveTxnRows      int    `json:"live_txn_rows"`
	LiveJournalRows  int    `json:"live_journal_rows"`
	LiveIndustryRows int    `json:"live_industry_rows,omitempty"`
	LimitHit         bool   `json:"limit_hit"`
	Error            string `json:"error,omitempty"`
}

type journalSyncResponse struct {
	Wallets                        []journalSyncWalletStat `json:"wallets"`
	IndustryJobsAutoLinked         int                     `json:"industry_jobs_auto_linked"`
	IndustryJobsStillUnlinkedAmbig int                     `json:"industry_jobs_still_unlinked_ambiguous"`
}

// --- handlers ---

// handleTradeJournalSync fetches wallet + journal + industry jobs for the
// scoped wallets and upserts the archive tables. Idempotent — re-running
// merges the current ESI window into the archive without wiping older rows.
func (s *Server) handleTradeJournalSync(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	if s.sessions == nil {
		writeError(w, 401, "not logged in")
		return
	}

	var req journalSyncRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, 400, "invalid json")
			return
		}
	}
	filter := requestWalletScope(req.Wallets)
	// Default is "all authorized wallets" — mirrors the plan's UX default.
	if filter == nil {
		filter = &db.WalletScopeFilter{IncludeAll: true}
	}

	sessions := s.sessions.ListForUser(userID)
	if len(sessions) == 0 {
		writeError(w, 401, "no authorized characters")
		return
	}

	resp := journalSyncResponse{Wallets: []journalSyncWalletStat{}}

	// Character-side sync (wallet + journal + industry).
	for _, sess := range sessions {
		if !filterAllowsCharacter(filter, sess.CharacterID) {
			continue
		}
		stat := s.syncOneCharacter(userID, sess)
		resp.Wallets = append(resp.Wallets, stat)
	}

	// Corp-side sync — one call per (corp, division) the user has access to.
	// The provider list is derived from the first session that has an active
	// corp membership; corp wallet access requires Accountant / Junior
	// Accountant role. Errors per-division don't fail the whole sync.
	if s.esi != nil && (filter.IncludeAll || len(filter.IncludeCorpDivisions) > 0) {
		corpStats := s.syncCorpWallets(userID, sessions, filter)
		resp.Wallets = append(resp.Wallets, corpStats...)
	}

	// After sync completes, auto-link unlinked ESI jobs to unlinked ledger jobs.
	linked, ambig := s.reconcileIndustryJobLinks(userID)
	resp.IndustryJobsAutoLinked = linked
	resp.IndustryJobsStillUnlinkedAmbig = ambig

	// Invalidate compute cache so the next GET re-reads fresh archive.
	journalRuntime.invalidateUser(userID)

	writeJSON(w, resp)
}

// syncOneCharacter pulls wallet txns + journal + industry jobs for one
// character and upserts to the archive. Returns a per-wallet stat row.
func (s *Server) syncOneCharacter(userID string, sess *auth.Session) journalSyncWalletStat {
	stat := journalSyncWalletStat{
		WalletKind:  "character",
		CharacterID: sess.CharacterID,
		SyncedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	token, err := s.sessions.EnsureValidTokenForUserCharacter(s.sso, userID, sess.CharacterID)
	if err != nil {
		stat.Error = fmt.Sprintf("token: %v", err)
		return stat
	}

	// Every fetch and every archive write reports into stat.Error. A write
	// that fails is exactly as invisible to the user as a fetch that fails,
	// and both used to be logged and dropped — which is how an empty
	// industry archive looked identical to a character with no jobs.
	fail := func(format string, args ...any) {
		if stat.Error == "" {
			stat.Error = fmt.Sprintf(format, args...)
		}
	}

	// Wallet transactions
	if txns, err := s.esi.GetWalletTransactions(sess.CharacterID, token); err == nil {
		s.enrichWalletTransactionTypeNames(txns)
		if _, aerr := s.db.UpsertWalletTransactionsForUser(userID, sess.CharacterID, txns); aerr != nil {
			log.Printf("[TradeJournal] wallet tx archive %s: %v", sess.CharacterName, aerr)
			fail("wallet archive: %v", aerr)
		}
		stat.LiveTxnRows = len(txns)
		if len(txns) >= 2500 {
			stat.LimitHit = true
		}
	} else {
		fail("wallet: %v", err)
	}

	// Wallet journal
	if entries, err := s.esi.GetWalletJournal(sess.CharacterID, token); err == nil {
		if _, aerr := s.db.UpsertWalletJournalForUser(userID, sess.CharacterID, entries); aerr != nil {
			log.Printf("[TradeJournal] wallet journal archive %s: %v", sess.CharacterName, aerr)
			fail("journal archive: %v", aerr)
		}
		stat.LiveJournalRows = len(entries)
		if len(entries) >= 2500 {
			stat.LimitHit = true
		}
	} else {
		fail("journal: %v", err)
	}

	// Industry jobs (include completed for the archive)
	if jobs, err := s.esi.GetCharacterIndustryJobs(sess.CharacterID, token, true); err == nil {
		// Only persist jobs that have actually finished (status = "delivered"
		// or "cancelled"); active/paused rows change and would churn the
		// archive.
		delivered := make([]esi.CharacterIndustryJob, 0, len(jobs))
		for _, j := range jobs {
			if j.Status == "delivered" || j.Status == "cancelled" {
				delivered = append(delivered, j)
			}
		}
		if _, aerr := s.db.UpsertIndustryJobsForUser(userID, sess.CharacterID, delivered); aerr != nil {
			log.Printf("[TradeJournal] industry archive %s: %v", sess.CharacterName, aerr)
			fail("industry archive: %v", aerr)
		}
		stat.LiveIndustryRows = len(delivered)
	} else {
		fail("industry: %v", err)
	}

	return stat
}

// syncCorpWallets iterates the corp divisions the user has access to and
// syncs each into the corp archive. Access is determined lazily by trying
// the wallet fetch — a 403 means that character lacks the Accountant /
// Junior Accountant role.
//
// Every session is tried, not just the first: nine characters can sit in
// several corporations, and stopping at the first one silently hid the rest.
// Corporations already synced in this pass are skipped, so the extra
// sessions cost one cheap corp-id lookup each.
func (s *Server) syncCorpWallets(userID string, sessions []*auth.Session, filter *db.WalletScopeFilter) []journalSyncWalletStat {
	out := []journalSyncWalletStat{}
	// Reasons collected from characters that couldn't reach a corp wallet.
	// They're only reported if *nothing* synced — otherwise they're noise
	// from alts that legitimately lack the role.
	skipped := []string{}
	syncedCorps := map[int32]bool{}
	for _, sess := range sessions {
		token, err := s.sessions.EnsureValidTokenForUserCharacter(s.sso, userID, sess.CharacterID)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: token: %v", sess.CharacterName, err))
			continue
		}
		corpID, err := s.esi.GetCharacterCorporationID(sess.CharacterID)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: corporation lookup: %v", sess.CharacterName, err))
			continue
		}
		if corpID <= 0 {
			skipped = append(skipped, fmt.Sprintf("%s: no corporation", sess.CharacterName))
			continue
		}
		if syncedCorps[corpID] {
			continue
		}
		s.mu.RLock()
		sdeData := s.sdeData
		s.mu.RUnlock()
		provider := corp.NewESICorpProvider(s.esi, sdeData, token, corpID, sess.CharacterID)
		wallets, err := provider.GetWallets()
		if err != nil {
			// Usually a 403: this character lacks Accountant /
			// Junior Accountant. Another character in the same corp may
			// still have it, so keep going.
			skipped = append(skipped, fmt.Sprintf("%s: corp %d wallets: %v", sess.CharacterName, corpID, err))
			continue
		}
		syncedCorps[corpID] = true
		for _, wallet := range wallets {
			if !filterAllowsCorpDiv(filter, int64(corpID), wallet.Division) {
				continue
			}
			stat := journalSyncWalletStat{
				WalletKind:    "corporation",
				CorporationID: int64(corpID),
				Division:      wallet.Division,
				SyncedAt:      time.Now().UTC().Format(time.RFC3339),
			}
			fail := func(format string, args ...any) {
				if stat.Error == "" {
					stat.Error = fmt.Sprintf(format, args...)
				}
			}
			if txns, err := provider.GetTransactions(wallet.Division); err == nil {
				if _, aerr := s.db.UpsertCorpWalletTransactionsForUser(userID, int64(corpID), wallet.Division, txns); aerr != nil {
					log.Printf("[TradeJournal] corp %d div %d tx archive: %v", corpID, wallet.Division, aerr)
					fail("corp txn archive: %v", aerr)
				}
				stat.LiveTxnRows = len(txns)
				if len(txns) >= 2500 {
					stat.LimitHit = true
				}
			} else {
				fail("corp txns: %v", err)
			}
			if entries, err := provider.GetJournal(wallet.Division, 0); err == nil {
				if _, aerr := s.db.UpsertCorpWalletJournalForUser(userID, int64(corpID), wallet.Division, entries); aerr != nil {
					log.Printf("[TradeJournal] corp %d div %d journal archive: %v", corpID, wallet.Division, aerr)
					fail("corp journal archive: %v", aerr)
				}
				stat.LiveJournalRows = len(entries)
				if len(entries) >= 2500 {
					stat.LimitHit = true
				}
			} else {
				fail("corp journal: %v", err)
			}
			out = append(out, stat)
		}

		// Corp industry is corp-wide rather than per-division, so it runs
		// once per corporation, after the wallet rows exist for the sidecar
		// stamp to land on.
		industryStat := journalSyncWalletStat{
			WalletKind:    "corporation",
			CorporationID: int64(corpID),
			SyncedAt:      time.Now().UTC().Format(time.RFC3339),
		}
		if jobs, err := provider.GetIndustryJobs(); err == nil {
			finished := make([]corp.CorpIndustryJob, 0, len(jobs))
			for _, j := range jobs {
				if j.Status == "delivered" || j.Status == "cancelled" {
					finished = append(finished, j)
				}
			}
			if _, aerr := s.db.UpsertCorpIndustryJobsForUser(userID, int64(corpID), finished); aerr != nil {
				log.Printf("[TradeJournal] corp %d industry archive: %v", corpID, aerr)
				industryStat.Error = fmt.Sprintf("corp industry archive: %v", aerr)
			}
			industryStat.LiveIndustryRows = len(finished)
		} else {
			industryStat.Error = fmt.Sprintf("corp industry: %v", err)
		}
		out = append(out, industryStat)
	}

	// Nothing reached a corp wallet at all. Say why — a silent zero-row
	// result is indistinguishable from "this account has no corp activity",
	// and the answer is almost always a missing Accountant role.
	if len(out) == 0 && len(skipped) > 0 {
		out = append(out, journalSyncWalletStat{
			WalletKind: "corporation",
			SyncedAt:   time.Now().UTC().Format(time.RFC3339),
			Error: "no corp wallet was readable (corp wallet access needs the Director, Accountant or Junior Accountant role): " +
				strings.Join(skipped, "; "),
		})
	}
	return out
}

// reconcileIndustryJobLinks matches unlinked ESI jobs (industry_jobs_archive
// where no ledger row references them) against unlinked ledger jobs
// (industry_jobs where external_job_id = 0), heuristic on
// (product_type_id + character_id + started_at ±24h + runs). Zero-ambiguity
// matches get auto-linked. Returns (auto-linked count, ambiguous count).
func (s *Server) reconcileIndustryJobLinks(userID string) (linked int, ambiguous int) {
	if s.db == nil {
		return 0, 0
	}
	esiJobs, err := s.db.ListArchivedIndustryJobsForUser(userID, nil, time.Time{})
	if err != nil {
		return 0, 0
	}
	// Filter to jobs that aren't already linked from *any* ledger row.
	linkedSet, err := s.db.ListLinkedExternalJobIDsForUser(userID)
	if err != nil {
		linkedSet = map[int64]bool{}
	}
	unlinkedESI := make([]db.ArchivedIndustryJob, 0, len(esiJobs))
	for _, j := range esiJobs {
		if !linkedSet[j.JobID] {
			unlinkedESI = append(unlinkedESI, j)
		}
	}
	if len(unlinkedESI) == 0 {
		return 0, 0
	}
	// Load unlinked ledger jobs with their product_type_id via a join.
	unlinkedLedger, err := s.db.ListUnlinkedLedgerJobsForUser(userID)
	if err != nil || len(unlinkedLedger) == 0 {
		return 0, 0
	}

	for _, esiJob := range unlinkedESI {
		candidates := findLinkCandidates(esiJob, unlinkedLedger)
		if len(candidates) == 1 {
			if err := s.db.SetIndustryJobExternalLink(userID, candidates[0].LedgerJobID, esiJob.JobID); err == nil {
				linked++
				// Mark the ledger row as taken so it doesn't match twice.
				for i := range unlinkedLedger {
					if unlinkedLedger[i].LedgerJobID == candidates[0].LedgerJobID {
						unlinkedLedger[i].Consumed = true
					}
				}
			}
		} else if len(candidates) > 1 {
			ambiguous++
		}
	}
	return
}

// findLinkCandidates returns the ledger jobs that plausibly match an ESI
// job by the heuristic (same product + installer + runs + start ±24h).
func findLinkCandidates(esiJob db.ArchivedIndustryJob, ledger []db.LinkCandidateLedgerJob) []db.LinkCandidateLedgerJob {
	esiStart, err := time.Parse(time.RFC3339, esiJob.StartDate)
	if err != nil {
		return nil
	}
	tolerance := 24 * time.Hour
	out := []db.LinkCandidateLedgerJob{}
	for _, l := range ledger {
		if l.Consumed {
			continue
		}
		if l.CharacterID != esiJob.CharacterID {
			continue
		}
		if l.ProductTypeID != esiJob.ProductTypeID {
			continue
		}
		if l.Runs != esiJob.Runs {
			continue
		}
		lStart, err := time.Parse(time.RFC3339, l.StartedAt)
		if err != nil {
			continue
		}
		diff := esiStart.Sub(lStart)
		if diff < -tolerance || diff > tolerance {
			continue
		}
		out = append(out, l)
	}
	return out
}

// --- read handlers (summary / by-type / lots / analytics) ---

// handleTradeJournalAnalytics serves the risk-and-breakdown half of the
// journal: the daily cumulative series with drawdown, per-item and per-station
// tables, the realized ledger, Sharpe / Calmar / profit factor, and slot
// efficiency.
//
// It is a separate endpoint from /journal/summary rather than more fields on
// it because it fetches live character orders for the slot-efficiency table.
// Folding it in would make the default Summary view pay for an ESI round trip
// it never displays.
//
// `source` selects trade | manufacture | (empty) combined, and filters the
// ledger before any statistic is computed — so a Manufacturing view's Sharpe
// ratio describes manufacturing, not a share of a combined number.
func (s *Server) handleTradeJournalAnalytics(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	res, filter, sinceDate, fifoMode, err := s.loadTradeJournalResult(r)
	if err != nil {
		if strings.Contains(err.Error(), "not logged in") {
			writeError(w, 401, err.Error())
		} else {
			writeError(w, 400, err.Error())
		}
		return
	}

	source, err := parseLotSourceParam(r.URL.Query().Get("source"))
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}

	// The rates are not re-read here to be applied — they were already charged
	// per matched sell inside the compute above (loadTradeJournalResult passes
	// the same override into it). Resolving the identical profile again just
	// echoes what the numbers were computed with, so the UI can state it.
	profile := s.journalFeeProfile(userID, filter, parseJournalFeeOverride(r))

	ledgerLimit := 500
	if v := r.URL.Query().Get("ledger_limit"); v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil && n >= 0 && n <= 5000 {
			ledgerLimit = n
		}
	}

	// LookbackDays only reaches the summarizer's per-day annualisation; the
	// window itself was already applied by the journal compute via sinceDate.
	lookbackDays := 90
	if !sinceDate.IsZero() {
		if d := int(time.Since(sinceDate).Hours() / 24); d > 0 {
			lookbackDays = d
		}
	}

	opts := engine.PortfolioPnLOptions{
		LookbackDays:         lookbackDays,
		SalesTaxPercent:      profile.SalesTaxPercent,
		BrokerFeePercent:     profile.BrokerFeePercent,
		LedgerLimit:          ledgerLimit,
		IncludeUnmatchedSell: false, // strict realized mode, as the old endpoint used
	}
	out := res.ToPortfolioPnL(opts, source)

	// The leaderboard is built from the same journal, deliberately ignoring
	// `source`: it reports the trading and manufacturing halves side by side,
	// so narrowing it to one of them would leave the other column empty.
	leaderboard := res.ItemLeaderboard(opts, journalLeaderboardLimit)

	// Slot efficiency needs live orders. A failure here costs one table, not
	// the whole response, so it is logged and skipped rather than returned.
	if orders := s.characterOrdersForWalletScope(userID, filter); len(orders) > 0 {
		out.SlotEfficiency = engine.ComputePortfolioSlotEfficiency(out, orders)
	}

	writeJSON(w, map[string]any{
		"analytics":   out,
		"leaderboard": leaderboard,
		"source":      string(source),
		"fifo_mode":   string(fifoMode),
		"since":       sinceDate.Format(time.RFC3339),
		"fees":        profile,
	})
}

// journalLeaderboardLimit caps the per-item leaderboard. Well past what the
// panel shows at once, so its ROI ranking is not confined to the handful of
// biggest ISK movers, but still bounded for a response that ships on every
// analytics load.
const journalLeaderboardLimit = 200

// parseLotSourceParam maps the `source` query param onto a LotSource. An empty
// value means combined. Anything else is rejected rather than silently
// treated as combined — a typo that quietly widens the view would show the
// user manufacturing profit under a "Trading" heading.
func parseLotSourceParam(v string) (engine.LotSource, error) {
	switch v {
	case "", "combined", "all":
		return "", nil
	case string(engine.LotSourceTrade):
		return engine.LotSourceTrade, nil
	case string(engine.LotSourceManufacture):
		return engine.LotSourceManufacture, nil
	default:
		return "", fmt.Errorf("unknown source %q (want trade, manufacture or combined)", v)
	}
}

// characterOrdersForWalletScope fetches live market orders for the characters
// a journal wallet filter covers, enriched with type and station names.
//
// Corp divisions in the filter contribute no orders: slot efficiency is a
// per-character skill question, and corp orders do not consume a character's
// slots.
func (s *Server) characterOrdersForWalletScope(userID string, filter *db.WalletScopeFilter) []esi.CharacterOrder {
	if s.sessions == nil || s.esi == nil || filter == nil {
		return nil
	}
	sessions := s.sessions.ListForUser(userID)
	if len(sessions) == 0 {
		return nil
	}
	if !filter.IncludeAll {
		wanted := make(map[int64]bool, len(filter.IncludeCharacters))
		for _, id := range filter.IncludeCharacters {
			wanted[id] = true
		}
		kept := sessions[:0]
		for _, sess := range sessions {
			if wanted[sess.CharacterID] {
				kept = append(kept, sess)
			}
		}
		sessions = kept
	}

	var orders []esi.CharacterOrder
	for _, sess := range sessions {
		token, tokenErr := s.sessions.EnsureValidTokenForUserCharacter(s.sso, userID, sess.CharacterID)
		if tokenErr != nil {
			log.Printf("[JOURNAL] analytics order token error (%s): %v", sess.CharacterName, tokenErr)
			continue
		}
		part, orderErr := s.esi.GetCharacterOrders(sess.CharacterID, token)
		if orderErr != nil {
			log.Printf("[JOURNAL] analytics orders error (%s): %v", sess.CharacterName, orderErr)
			continue
		}
		orders = append(orders, part...)
	}

	s.mu.RLock()
	sdeData := s.sdeData
	s.mu.RUnlock()
	if sdeData != nil {
		for i := range orders {
			if t, ok := sdeData.Types[orders[i].TypeID]; ok {
				orders[i].TypeName = t.Name
			}
			orders[i].LocationName = s.esi.StationName(orders[i].LocationID)
		}
	}
	return orders
}

func (s *Server) handleTradeJournalSummary(w http.ResponseWriter, r *http.Request) {
	res, filter, sinceDate, fifoMode, err := s.loadTradeJournalResult(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	// Also return per-wallet tracking_since + stale_syncs for the UI banners.
	trackingSince, staleSyncs := s.walletMetaForFilter(userIDFromRequest(r), filter)

	writeJSON(w, map[string]any{
		"totals":         res.Totals,
		"daily_pnl":      res.DailyPnL,
		"tracking_since": trackingSince,
		"stale_syncs":    staleSyncs,
		"fifo_mode":      string(fifoMode),
		"since":          sinceDate.Format(time.RFC3339),
		// Echoing the rates these totals were computed with costs nothing here
		// (they were already resolved during the compute) and it is what lets
		// the tab state "3.6% / 1.5% from Accounting V" instead of showing two
		// unexplained numbers in a fee input.
		"fees": s.journalFeeProfile(userIDFromRequest(r), filter, parseJournalFeeOverride(r)),
	})
}

func (s *Server) handleTradeJournalByType(w http.ResponseWriter, r *http.Request) {
	res, _, _, _, err := s.loadTradeJournalResult(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	// Roll lots up per (typeID, source) into a single row per typeID.
	type byTypeRow struct {
		TypeID               int32   `json:"type_id"`
		TypeName             string  `json:"type_name,omitempty"`
		BuysQty              int64   `json:"buys_qty"`
		SellsQty             int64   `json:"sells_qty"`
		AvgBuyPrice          float64 `json:"avg_buy_price"`
		AvgSellPrice         float64 `json:"avg_sell_price"`
		TradingProfit        float64 `json:"trading_profit"`
		ManufacturingProfit  float64 `json:"manufacturing_profit"`
		CombinedProfit       float64 `json:"combined_profit"`
		HeldQtyTrade         int64   `json:"held_qty_trade"`
		HeldQtyManufacture   int64   `json:"held_qty_manufacture"`
		UnattributedSellsQty int64   `json:"unattributed_sells_qty"`
	}
	rows := map[int32]*byTypeRow{}
	get := func(typeID int32, name string) *byTypeRow {
		if r, ok := rows[typeID]; ok {
			return r
		}
		rows[typeID] = &byTypeRow{TypeID: typeID, TypeName: name}
		return rows[typeID]
	}
	// Aggregate realized-trade rows.
	buyGrossAcc := map[int32]float64{}
	sellGrossAcc := map[int32]float64{}
	for _, lot := range res.Lots {
		row := get(lot.TypeID, lot.TypeName)
		row.SellsQty += lot.MatchedQty
		sellGrossAcc[lot.TypeID] += lot.SellGross
		switch lot.Source {
		case engine.LotSourceTrade:
			row.TradingProfit += lot.NetProfit
			row.BuysQty += lot.MatchedQty
			buyGrossAcc[lot.TypeID] += lot.BuyUnitPrice * float64(lot.MatchedQty)
		case engine.LotSourceManufacture:
			row.ManufacturingProfit += lot.NetProfit
		case engine.LotSourceOrphan:
			row.UnattributedSellsQty += lot.MatchedQty
		}
	}
	// Open positions → held quantities per source.
	for _, op := range res.OpenPositions {
		row := get(op.TypeID, op.TypeName)
		if op.Source == engine.LotSourceTrade {
			row.HeldQtyTrade += op.Qty
		} else if op.Source == engine.LotSourceManufacture {
			row.HeldQtyManufacture += op.Qty
		}
	}
	// Combined + averages.
	list := make([]*byTypeRow, 0, len(rows))
	for typeID, row := range rows {
		row.CombinedProfit = row.TradingProfit + row.ManufacturingProfit
		if row.BuysQty > 0 {
			row.AvgBuyPrice = buyGrossAcc[typeID] / float64(row.BuysQty)
		}
		if row.SellsQty > 0 {
			row.AvgSellPrice = sellGrossAcc[typeID] / float64(row.SellsQty)
		}
		list = append(list, row)
	}
	sort.Slice(list, func(a, b int) bool {
		return list[a].CombinedProfit > list[b].CombinedProfit
	})
	writeJSON(w, map[string]any{"rows": list})
}

// journalLotsDefaultLimit / journalLotsMaxLimit bound the type-less listing.
//
// A busy month of station trading is tens of thousands of matched sells, and
// every one of them is a JSON object. The cap is applied after sorting by sell
// date descending, so it always keeps the most recent window rather than an
// arbitrary slice, and the response reports the pre-cap match count so the UI
// can say what it is not showing.
const (
	journalLotsDefaultLimit = 1000
	journalLotsMaxLimit     = 10000
)

// handleTradeJournalLots serves matched sell lots — one row per sale, with the
// cost basis it was matched against and the fees charged on it.
//
// With `type_id` it answers for one item, which is what the per-item drawer in
// the Summary view asks for. Without it, it lists every sale in the window: the
// per-transaction profit view. Both read the same `res.Lots`, so a row cannot
// disagree between the two.
//
// Filtering happens here rather than in the browser because of the cap above.
// Filtering a truncated set would report counts for a window the user cannot
// see — "3 items over 1M ISK" when there are really 40.
func (s *Server) handleTradeJournalLots(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	typeIDStr := strings.TrimSpace(q.Get("type_id"))

	var typeID int32
	if typeIDStr != "" {
		tid64, err := strconv.ParseInt(typeIDStr, 10, 32)
		if err != nil || tid64 <= 0 {
			writeError(w, 400, "invalid type_id")
			return
		}
		typeID = int32(tid64)
	}

	source, err := parseLotFilterSourceParam(q.Get("source"))
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	nameQuery := strings.ToLower(strings.TrimSpace(q.Get("q")))
	var minProfit float64
	if v := strings.TrimSpace(q.Get("min_profit")); v != "" {
		f, parseErr := strconv.ParseFloat(v, 64)
		// `NaN < 0` is false, so NaN would otherwise be accepted here.
		if parseErr != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
			writeError(w, 400, "invalid min_profit")
			return
		}
		minProfit = f
	}
	limit := journalLotsDefaultLimit
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		n, parseErr := strconv.Atoi(v)
		if parseErr != nil || n <= 0 || n > journalLotsMaxLimit {
			writeError(w, 400, fmt.Sprintf("invalid limit (want 1..%d)", journalLotsMaxLimit))
			return
		}
		limit = n
	}

	res, _, _, _, err := s.loadTradeJournalResult(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}

	lots, total := filterJournalLots(res.Lots, lotFilter{
		typeID:    typeID,
		source:    source,
		nameQuery: nameQuery,
		minProfit: minProfit,
		limit:     limit,
	})

	// Build detail is per-item and has no column in the transaction table.
	// Returning every job for a type-less listing would roughly double the
	// payload for something nothing renders.
	mfgLots := make([]engine.ManufacturingLot, 0)
	if typeID != 0 {
		for _, m := range res.ManufacturingLots {
			if m.ProductTypeID == typeID {
				mfgLots = append(mfgLots, m)
			}
		}
	}

	writeJSON(w, map[string]any{
		"lots":               lots,
		"manufacturing_lots": mfgLots,
		"total":              total,
	})
}

// lotFilter is what a lots request asks for, separated from how it was spelled
// on the query string so the selection rules can be tested without a database.
//
// A zero typeID means every item, which also turns on the sort and the cap: the
// per-item drawer wants one item's matches in match order and all of them, while
// the transaction list wants the most recent N sales across everything.
type lotFilter struct {
	typeID int32
	source engine.LotSource
	// nameQuery is matched case-insensitively and must already be lowercased.
	nameQuery string
	minProfit float64
	limit     int
}

// filterJournalLots selects the lots a request asked for and reports how many
// matched before the cap.
//
// The pre-cap count is returned rather than derived from the slice because the
// UI has to be able to say "showing 1000 of 4213". A truncated list presented as
// the whole window is the one failure mode here that a user cannot see.
func filterJournalLots(lots []engine.TradeJournalLot, f lotFilter) ([]engine.TradeJournalLot, int) {
	out := make([]engine.TradeJournalLot, 0, len(lots))
	for _, l := range lots {
		if f.typeID != 0 && l.TypeID != f.typeID {
			continue
		}
		if f.source != "" && l.Source != f.source {
			continue
		}
		if f.nameQuery != "" && !strings.Contains(strings.ToLower(l.TypeName), f.nameQuery) {
			continue
		}
		// Absolute value: a minimum-profit filter is asking "show me the sales
		// that moved the needle", and a 4M ISK loss moved it as much as a 4M
		// ISK win.
		if f.minProfit > 0 && math.Abs(l.NetProfit) < f.minProfit {
			continue
		}
		out = append(out, l)
	}
	total := len(out)

	// One item's drawer shows every match in match order, so neither the sort
	// nor the cap applies to it.
	if f.typeID != 0 {
		return out, total
	}
	// Sell dates are RFC3339 UTC, so lexical order is chronological order.
	sort.SliceStable(out, func(a, b int) bool { return out[a].SellDate > out[b].SellDate })
	if f.limit > 0 && len(out) > f.limit {
		out = out[:f.limit]
	}
	return out, total
}

// parseLotFilterSourceParam maps the `source` query param onto a LotSource for
// the lots listing.
//
// Unlike parseLotSourceParam (which feeds the analytics projection) this one
// accepts "orphan": an unmatched sell is a row the transaction list has to be
// able to isolate, because a sale with no cost basis is exactly the thing worth
// hunting down. Unknown values are still rejected rather than widened to
// combined, so a typo cannot show build profit under a "Trading" heading.
func parseLotFilterSourceParam(v string) (engine.LotSource, error) {
	switch strings.TrimSpace(v) {
	case "", "combined", "all":
		return "", nil
	case string(engine.LotSourceTrade):
		return engine.LotSourceTrade, nil
	case string(engine.LotSourceManufacture):
		return engine.LotSourceManufacture, nil
	case string(engine.LotSourceOrphan):
		return engine.LotSourceOrphan, nil
	default:
		return "", fmt.Errorf("unknown source %q (want trade, manufacture, orphan or combined)", v)
	}
}

// handleTradeJournalLinkJob is the manual-link endpoint powering the
// "Link to planner job..." button in the per-lot drawer.
func (s *Server) handleTradeJournalLinkJob(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	if userID == "" {
		writeError(w, 401, "not logged in")
		return
	}
	var req struct {
		ESIJobID    int64 `json:"esi_job_id"`
		LedgerJobID int64 `json:"ledger_job_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid json")
		return
	}
	if req.ESIJobID <= 0 || req.LedgerJobID <= 0 {
		writeError(w, 400, "esi_job_id and ledger_job_id required")
		return
	}
	if err := s.db.SetIndustryJobExternalLink(userID, req.LedgerJobID, req.ESIJobID); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	journalRuntime.invalidateUser(userID)
	writeJSON(w, map[string]any{"ok": true})
}

// handleTradeJournalLinkCandidates returns the unlinked ledger jobs that
// plausibly match an ESI job, for the manual-link picker.
func (s *Server) handleTradeJournalLinkCandidates(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	if userID == "" {
		writeError(w, 401, "not logged in")
		return
	}
	esiIDStr := strings.TrimSpace(r.URL.Query().Get("esi_job_id"))
	if esiIDStr == "" {
		writeError(w, 400, "esi_job_id required")
		return
	}
	esiID, err := strconv.ParseInt(esiIDStr, 10, 64)
	if err != nil {
		writeError(w, 400, "invalid esi_job_id")
		return
	}
	esiJobs, err := s.db.ListArchivedIndustryJobsForUser(userID, nil, time.Time{})
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	var esiJob *db.ArchivedIndustryJob
	for i := range esiJobs {
		if esiJobs[i].JobID == esiID {
			esiJob = &esiJobs[i]
			break
		}
	}
	if esiJob == nil {
		writeError(w, 404, "esi job not found in archive")
		return
	}
	ledger, err := s.db.ListUnlinkedLedgerJobsForUser(userID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	candidates := findLinkCandidates(*esiJob, ledger)
	writeJSON(w, map[string]any{"candidates": candidates})
}

// --- shared compute path ---

// loadTradeJournalResult loads or computes a TradeJournalResult for the
// scoped read endpoints. Uses the 60s in-memory cache when hot.
func (s *Server) loadTradeJournalResult(r *http.Request) (*engine.TradeJournalResult, *db.WalletScopeFilter, time.Time, engine.FIFOMode, error) {
	userID := userIDFromRequest(r)
	if userID == "" {
		return nil, nil, time.Time{}, "", fmt.Errorf("not logged in")
	}
	filter, err := parseScopeQueryParam(r.URL.Query().Get("scope"))
	if err != nil {
		return nil, nil, time.Time{}, "", err
	}
	if filter == nil {
		filter = &db.WalletScopeFilter{IncludeAll: true}
	}
	sinceDate := parseSinceParam(r.URL.Query().Get("days"))
	fifoMode := parseFIFOMode(r.URL.Query().Get("fifo_mode"))

	result, err := s.loadTradeJournalResultFor(userID, filter, sinceDate, fifoMode, parseJournalFeeOverride(r))
	return result, filter, sinceDate, fifoMode, err
}

// loadTradeJournalResultFor is loadTradeJournalResult without the HTTP
// request, for the callers that hold no *http.Request.
//
// The Positions tab is one: it needs FIFO open positions but arrives with the
// character-scope query convention (character_id / scope=all) rather than the
// journal's scope tokens, so it builds the filter itself and calls in here.
// The Orders desk is the other: it wants OpenPositions as a sell-side cost
// basis. A caller passing the Trade Journal tab's own defaults (IncludeAll,
// zero since, strict-date FIFO, no fee override) lands on the same cache key
// that tab uses, so the two share one computed result rather than each paying
// for its own.
//
// `fees` overrides the resolved fee profile for this compute; the zero value
// means "resolve it". It is part of the cache key, because two rate pairs
// produce two different sets of realized profits over the same archive.
func (s *Server) loadTradeJournalResultFor(userID string, filter *db.WalletScopeFilter, sinceDate time.Time, fifoMode engine.FIFOMode, fees journalFeeRates) (*engine.TradeJournalResult, error) {
	key := tradeJournalCacheKey(userID, filter, sinceDate, fifoMode, fees)
	if cached := journalRuntime.get(key); cached != nil {
		return cached, nil
	}

	// Coalesce concurrent duplicate compute requests. The Trade Journal
	// tab's Promise.all fires /journal/summary and /journal/by-type at
	// the same moment; without singleflight both miss the cache
	// simultaneously and each does the full compute independently
	// (SQLite reads → market prices → per-character blueprint fetch →
	// engine.ComputeTradeJournal). The second caller now just waits.
	shared, err, _ := journalRuntime.group.Do(key, func() (interface{}, error) {
		if cached := journalRuntime.get(key); cached != nil {
			return cached, nil
		}
		return s.computeTradeJournalResult(userID, filter, sinceDate, fifoMode, fees, key)
	})
	if err != nil {
		return nil, err
	}
	result, ok := shared.(*engine.TradeJournalResult)
	if !ok || result == nil {
		return nil, fmt.Errorf("trade journal compute returned no result")
	}
	return result, nil
}

// orderDeskCostBasisByType reduces the journal's open positions to one
// average unit cost per type, which is what the Orders desk needs to say
// whether a sell order is still above water.
//
// A type can appear twice: ComputeTradeJournal keeps the trading and
// manufacturing pools separate, so the two are blended by quantity rather
// than letting one silently win.
//
// Best-effort by design. Any failure returns nil, sell rows then report no
// margin, and the tab behaves as it did before. The Orders desk must never
// fail or stall because the wallet archive happens to be cold.
func (s *Server) orderDeskCostBasisByType(userID string) map[int32]float64 {
	if s == nil || s.db == nil || strings.TrimSpace(userID) == "" {
		return nil
	}
	result, err := s.loadTradeJournalResultFor(
		userID,
		&db.WalletScopeFilter{IncludeAll: true},
		time.Time{},
		engine.FIFOModeStrictDate,
		journalFeeRates{},
	)
	if err != nil {
		log.Printf("[AUTH] OrderDesk cost basis unavailable: %v", err)
		return nil
	}
	if result == nil {
		return nil
	}
	return blendOpenPositionCostBasis(result.OpenPositions)
}

// blendOpenPositionCostBasis collapses the journal's open positions to one
// average unit cost per type. A type can appear more than once — the
// trading pool and the manufacturing pool are tracked separately — so the
// pools are blended by quantity rather than averaged, or 10 units bought
// dear would outweigh 10,000 built cheap. Returns nil when nothing is held,
// which the desk reads as "unmeasured" rather than "free".
func blendOpenPositionCostBasis(positions []engine.JournalOpenPosition) map[int32]float64 {
	type acc struct {
		qty  int64
		cost float64
	}
	byType := make(map[int32]*acc, len(positions))
	for _, p := range positions {
		if p.Qty <= 0 || p.AvgUnitCost <= 0 {
			continue
		}
		a := byType[p.TypeID]
		if a == nil {
			a = &acc{}
			byType[p.TypeID] = a
		}
		a.qty += p.Qty
		a.cost += p.AvgUnitCost * float64(p.Qty)
	}
	if len(byType) == 0 {
		return nil
	}
	out := make(map[int32]float64, len(byType))
	for typeID, a := range byType {
		if a.qty > 0 {
			out[typeID] = a.cost / float64(a.qty)
		}
	}
	return out
}

// computeTradeJournalResult is the raw compute path — extracted from
// loadTradeJournalResult so the singleflight closure can call it without
// re-parsing HTTP request state. Populates the cache on success.
func (s *Server) computeTradeJournalResult(userID string, filter *db.WalletScopeFilter, sinceDate time.Time, fifoMode engine.FIFOMode, fees journalFeeRates, key string) (*engine.TradeJournalResult, error) {
	// Load archive, compute, cache.
	// The journal entries alongside the transactions are what CCP actually
	// charged. They were being loaded and dropped on the floor; the fees below
	// come out of them wherever they can be tied to a sale.
	txns, journalEntries, err := s.db.ListArchivedWalletActivityForUser(userID, *filter, sinceDate)
	if err != nil {
		return nil, err
	}
	actual := actualFeesFromJournal(journalEntries)
	// Scope-aware, not character-aware. Passing filter.IncludeCharacters here
	// meant a corp-only scope sent an empty slice, which the DB layer reads
	// as "every character" — so selecting one corp wallet silently mixed in
	// every character's manufacturing and inflated the cost basis.
	jobs, err := s.db.ListArchivedIndustryJobsForScope(userID, *filter, time.Time{})
	if err != nil {
		return nil, err
	}

	// Convert db → engine types.
	engineTxns := make([]engine.JournalTxn, len(txns))
	for i, t := range txns {
		engineTxns[i] = engine.JournalTxn{
			WalletKey:     t.WalletKey,
			TransactionID: t.TransactionID,
			Date:          t.Date,
			TypeID:        t.TypeID,
			TypeName:      t.TypeName,
			UnitPrice:     t.UnitPrice,
			Quantity:      int32(t.Quantity),
			IsBuy:         t.IsBuy,
			LocationID:    t.LocationID,
			LocationName:  t.LocationName,
		}
	}
	engineJobs := make([]engine.JournalIndustryJob, len(jobs))
	for i, j := range jobs {
		engineJobs[i] = engine.JournalIndustryJob{
			JobID:           j.JobID,
			CharacterID:     j.CharacterID,
			ActivityID:      j.ActivityID,
			BlueprintTypeID: j.BlueprintTypeID,
			ProductTypeID:   j.ProductTypeID,
			ProductTypeName: j.ProductTypeName,
			Runs:            j.Runs,
			InstallCost:     j.InstallCost,
			Status:          j.Status,
			StartDate:       j.StartDate,
			CompletedDate:   j.CompletedDate,
			SuccessfulRuns:  j.SuccessfulRuns,
		}
	}

	// SDE lookups for materials + products.
	s.mu.RLock()
	sdeData := s.sdeData
	s.mu.RUnlock()
	materials := map[int32][]sde.BlueprintMaterial{}
	products := map[int32]sde.BlueprintProduct{}
	if sdeData != nil {
		for _, j := range engineJobs {
			bp := sdeData.Industry.Blueprints[j.BlueprintTypeID]
			if bp == nil {
				continue
			}
			// Activity name lookup: 1 = manufacturing, 11 = reaction. Both
			// share the same cost-basis semantics; the SDE keys them by
			// name under Activities.
			activityName := "manufacturing"
			if j.ActivityID == 11 {
				activityName = "reaction"
			}
			if act := bp.Activities[activityName]; act != nil {
				materials[j.BlueprintTypeID] = act.Materials
				if len(act.Products) > 0 {
					products[j.BlueprintTypeID] = act.Products[0]
				}
			} else if j.ActivityID == 1 {
				// Legacy fallback for older SDE dumps where manufacturing
				// activity isn't in the Activities map.
				materials[j.BlueprintTypeID] = bp.Materials
				products[j.BlueprintTypeID] = sde.BlueprintProduct{TypeID: bp.ProductTypeID, Quantity: bp.ProductQuantity}
			}
		}
	}

	// Region-average fallback map for materials not in the trading pool.
	// Uses the cached ESI /markets/prices lookup already threaded through
	// the DS pipeline.
	regionAvg := map[int32]float64{}
	if s.esi != nil {
		if prices, err := s.esi.FetchMarketPrices(); err == nil {
			for _, p := range prices {
				if p.AveragePrice > 0 {
					regionAvg[p.TypeID] = p.AveragePrice
				}
			}
			// Backfill from SDE base price for anything ESI missed.
			if sdeData != nil {
				for tid, t := range sdeData.Types {
					if _, ok := regionAvg[tid]; !ok && t.BasePrice > 0 {
						regionAvg[tid] = t.BasePrice
					}
				}
			}
		}
	}

	// ME resolver — builds per-request from ledger + BP inventory.
	meResolver := s.buildMEResolver(userID, sdeData, filter)

	// The one fee profile: the user's configured rates, else the rates their
	// skills imply, else the fallback — or an explicit per-request override.
	// Every surface that shows realized profit resolves it the same way, so
	// none of them can report a different profit for the same trade.
	profile := s.journalFeeProfile(userID, filter, fees)

	opts := engine.TradeJournalOptions{
		SinceDate:        sinceDate,
		FIFOMode:         fifoMode,
		SalesTaxPercent:  profile.SalesTaxPercent,
		BrokerFeePercent: profile.BrokerFeePercent,
		// Modelled rates above are the fallback; a sale the journal can prove
		// is charged what it was charged.
		ActualSellTaxRateByTxnID: actual.SellTaxRateByTxnID,
		Materials:                materials,
		Products:                 products,
		MEByJob:                  meResolver,
		RegionAvgByType:          regionAvg,
		TypeNameFor: func(id int32) string {
			if sdeData == nil {
				return ""
			}
			if t, ok := sdeData.Types[id]; ok {
				return t.Name
			}
			return ""
		},
	}
	result := engine.ComputeTradeJournal(engineTxns, engineJobs, opts)
	// Period fee facts. Sales tax is also attributed per row above; broker fee
	// and provider tax are charged at order placement and can only ever be
	// period figures. They are reported rather than subtracted from any row —
	// prorating them would make a row's profit move whenever an unrelated sale
	// in the same window changed.
	result.Totals.ActualSalesTaxISK = actual.SalesTaxISK
	result.Totals.ActualBrokerFeeISK = actual.BrokerFeeISK
	result.Totals.ActualProviderTaxISK = actual.ProviderTaxISK
	journalRuntime.put(key, result)
	return result, nil
}

// buildMEResolver returns a closure implementing the plan's ME lookup chain:
// planner-link → owned-BPO → tech-level default → 0 fallback.
func (s *Server) buildMEResolver(userID string, sdeData *sde.Data, filter *db.WalletScopeFilter) func(engine.JournalIndustryJob) engine.MEResolution {
	// Pre-load ledger job map: external_job_id → ledger row (for planner link).
	ledgerByExt := map[int64]db.IndustryLedgerJobME{}
	if s.db != nil {
		if rows, err := s.db.ListLinkedLedgerJobMEForUser(userID); err == nil {
			for _, r := range rows {
				ledgerByExt[r.ExternalJobID] = r
			}
		}
	}
	// Fetch each authorized character's blueprint inventory and build a
	// max-ME-per-BlueprintTypeID map, tagging whether the winner is a BPO
	// (Runs == -1 in ESI's shape) or a BPC (positive Runs). Consumed at
	// step 2 of the ME lookup chain. Fan-out concurrently across sessions
	// — the ESI blueprint endpoint is per-character, so N characters used
	// to serialize into N round trips (2-3 s each on a cold call). With
	// a goroutine per session the same fetch cost is amortized in
	// parallel, cutting cold-cache compute latency roughly N×.
	type bpEntry struct {
		ME    int32
		IsBPO bool
	}
	bpMap := map[int32]bpEntry{}
	if s.sessions != nil && s.esi != nil && s.sso != nil {
		sessions := s.sessions.ListForUser(userID)
		type fetchResult struct {
			bps []esi.CharacterBlueprint
			err error
		}
		type sessionFetch struct {
			ch chan fetchResult
		}
		fetches := make([]sessionFetch, 0, len(sessions))
		for _, sess := range sessions {
			if filter != nil && !filterAllowsCharacter(filter, sess.CharacterID) {
				continue
			}
			token, err := s.sessions.EnsureValidTokenForUserCharacter(s.sso, userID, sess.CharacterID)
			if err != nil {
				continue
			}
			ch := make(chan fetchResult, 1)
			fetches = append(fetches, sessionFetch{ch: ch})
			go func(charID int64, tok string, ch chan<- fetchResult) {
				bps, err := s.esi.GetCharacterBlueprints(charID, tok)
				ch <- fetchResult{bps: bps, err: err}
			}(sess.CharacterID, token, ch)
		}
		// Drain in order — merge is deterministic per session which keeps
		// the max-ME/BPO-tiebreak stable across runs.
		for _, f := range fetches {
			r := <-f.ch
			if r.err != nil {
				continue
			}
			for _, bp := range r.bps {
				isBPO := bp.Runs == -1
				cur, ok := bpMap[bp.TypeID]
				if !ok {
					bpMap[bp.TypeID] = bpEntry{ME: bp.MaterialEfficiency, IsBPO: isBPO}
					continue
				}
				// Prefer higher ME; on tie, prefer BPO (more usable — a BPC
				// has finite runs and might be gone by the next job).
				if bp.MaterialEfficiency > cur.ME || (bp.MaterialEfficiency == cur.ME && isBPO && !cur.IsBPO) {
					bpMap[bp.TypeID] = bpEntry{ME: bp.MaterialEfficiency, IsBPO: isBPO}
				}
			}
		}
	}

	return func(job engine.JournalIndustryJob) engine.MEResolution {
		// 1. Ledger link.
		if r, ok := ledgerByExt[job.JobID]; ok {
			return engine.MEResolution{ME: r.ME, Source: "planner"}
		}
		// 2. Owned BP inventory (BPO preferred over BPC on ties).
		if e, ok := bpMap[job.BlueprintTypeID]; ok {
			src := "bpc"
			if e.IsBPO {
				src = "bpo"
			}
			return engine.MEResolution{ME: e.ME, Source: src}
		}
		// 3. Tech-level default via SDE metaGroupID.
		if sdeData != nil {
			if t, ok := sdeData.Types[job.ProductTypeID]; ok {
				switch t.MetaGroupID {
				case 1, 4, 54:
					// T1 / Faction / Storyline — vanilla ME10 BPO baseline.
					return engine.MEResolution{ME: 10, Source: "t1_default"}
				case 2:
					// T2 — vanilla no-decryptor invention output.
					return engine.MEResolution{ME: 4, Source: "t2_default"}
				}
			}
		}
		return engine.MEResolution{ME: 0, Source: "fallback"}
	}
}

// walletMetaForFilter returns per-wallet-key `tracking_since` (min archive
// date) and stale-sync warnings for the scoped wallets.
//
// Staleness is judged per kind, not off a single timestamp. The wallet-
// transaction timestamp is stamped by seven endpoints that do no industry or
// corp work at all (the character popup, the wallet tab, the order desk, the
// ledger, …), so simply opening the app keeps it fresh forever. Gating the
// journal's auto-sync on that one column is why the industry and corp
// archives were still empty after months of use: the sync it was supposed to
// trigger had never run once. A blank timestamp counts as stale for the same
// reason — "never synced" is the case that most needs syncing.
func (s *Server) walletMetaForFilter(userID string, filter *db.WalletScopeFilter) (map[string]string, []map[string]any) {
	trackingSince := map[string]string{}
	staleSyncs := []map[string]any{}
	if s.db == nil {
		return trackingSince, staleSyncs
	}
	meta, err := s.db.ListWalletArchiveMetaForUser(userID, *filter)
	if err != nil {
		return trackingSince, staleSyncs
	}
	now := time.Now().UTC()
	staleAt := 20 * 24 * time.Hour

	// stale reports whether a sidecar timestamp needs a resync, and how old
	// it is. Unparseable is treated the same as blank: we can't prove it's
	// current, so sync.
	stale := func(ts string) (bool, int) {
		if strings.TrimSpace(ts) == "" {
			return true, 0
		}
		t, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			return true, 0
		}
		age := now.Sub(t)
		if age > staleAt {
			return true, int(age.Hours() / 24)
		}
		return false, int(age.Hours() / 24)
	}

	sawCorp := false
	// Evidence that a sync has actually run for this user. Every wallet sync
	// attempts corp wallets too, so once one has completed, the absence of a
	// corp sidecar row means there is no corp wallet to reach -- not a sync
	// that was never done.
	sawAnySync := false
	for _, m := range meta {
		trackingSince[m.WalletKey] = m.EarliestDate
		if strings.HasPrefix(m.WalletKey, "corp:") {
			sawCorp = true
		}
		if strings.TrimSpace(m.LastSyncAt) != "" {
			sawAnySync = true
		}
		// One entry per wallet, naming the kind that drove it — the wallet
		// kind wins when both are stale, since it's the one the banner has
		// always talked about.
		walletStale, walletDays := stale(m.LastSyncAt)
		industryStale, industryDays := stale(m.IndustrySyncAt)
		if !walletStale && !industryStale {
			continue
		}
		kind, lastSync, days := "industry", m.IndustrySyncAt, industryDays
		if walletStale {
			kind, lastSync, days = "wallet", m.LastSyncAt, walletDays
		}
		if strings.TrimSpace(lastSync) == "" {
			kind = "never"
		}
		staleSyncs = append(staleSyncs, map[string]any{
			"wallet_key":   m.WalletKey,
			"kind":         kind,
			"last_sync_at": lastSync,
			"days_ago":     days,
		})
	}

	// A corp division that has never been synced has no sidecar row, so it
	// can't report itself stale — the corp archives would stay empty
	// forever. One synthetic entry breaks that; once the first sync writes
	// real rows, the loop above takes over and this stops firing.
	//
	// But only before the first sync. Corp access is discovered by *trying*
	// the fetch (see syncCorpWallets — a 403 means the character lacks
	// Accountant), so there is no cheap way to ask whether a corp wallet
	// exists. That left a user with no corp wallet, or no Accountant role,
	// staring at "1 wallet has never been synced" that no amount of syncing
	// could clear, because the row it was waiting for could never be written.
	// A completed sync is proof corp was attempted; if no corp row came back,
	// there is nothing there to warn about.
	if !sawCorp && !sawAnySync && (filter.IncludeAll || len(filter.IncludeCorpDivisions) > 0) {
		staleSyncs = append(staleSyncs, map[string]any{
			"wallet_key":   "corp:*",
			"kind":         "never",
			"last_sync_at": "",
			"days_ago":     0,
		})
	}
	return trackingSince, staleSyncs
}

// --- scope + FIFO parsing helpers ---

// parseScopeQueryParam turns "char:12345,corp:98765:3" → WalletScopeFilter.
// Empty → nil (caller decides default).
func parseScopeQueryParam(raw string) (*db.WalletScopeFilter, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "all" {
		return &db.WalletScopeFilter{IncludeAll: true}, nil
	}
	filter := &db.WalletScopeFilter{}
	for _, tok := range strings.Split(raw, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		parts := strings.Split(tok, ":")
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid scope token %q", tok)
		}
		switch parts[0] {
		case "char":
			id, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid char id %q", parts[1])
			}
			filter.IncludeCharacters = append(filter.IncludeCharacters, id)
		case "corp":
			if len(parts) != 3 {
				return nil, fmt.Errorf("corp scope needs corp:corpID:division, got %q", tok)
			}
			corpID, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid corp id %q", parts[1])
			}
			div, err := strconv.Atoi(parts[2])
			if err != nil || div < 1 || div > 7 {
				return nil, fmt.Errorf("invalid division %q", parts[2])
			}
			filter.IncludeCorpDivisions = append(filter.IncludeCorpDivisions, db.CorpDivisionKey{CorporationID: corpID, Division: div})
		default:
			return nil, fmt.Errorf("unknown scope prefix %q", parts[0])
		}
	}
	return filter, nil
}

func requestWalletScope(wire *walletScopeFilterWire) *db.WalletScopeFilter {
	if wire == nil {
		return nil
	}
	out := &db.WalletScopeFilter{IncludeAll: wire.IncludeAll, IncludeCharacters: wire.IncludeCharacters}
	for _, d := range wire.IncludeCorpDivs {
		out.IncludeCorpDivisions = append(out.IncludeCorpDivisions, db.CorpDivisionKey{CorporationID: d.CorporationID, Division: d.Division})
	}
	return out
}

// parseSinceParam turns "30" / "all" → cutoff time.
func parseSinceParam(v string) time.Time {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" || v == "all" || v == "0" {
		return time.Time{}
	}
	days, err := strconv.Atoi(v)
	if err != nil || days <= 0 {
		return time.Time{}
	}
	return time.Now().UTC().AddDate(0, 0, -days)
}

func parseFIFOMode(v string) engine.FIFOMode {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "trade_first":
		return engine.FIFOModeTradeFirst
	case "manufacture_first":
		return engine.FIFOModeManufactureFirst
	default:
		return engine.FIFOModeStrictDate
	}
}

func filterAllowsCharacter(f *db.WalletScopeFilter, charID int64) bool {
	if f.IncludeAll {
		return true
	}
	for _, id := range f.IncludeCharacters {
		if id == charID {
			return true
		}
	}
	return false
}

func filterAllowsCorpDiv(f *db.WalletScopeFilter, corpID int64, div int) bool {
	if f.IncludeAll {
		return true
	}
	for _, d := range f.IncludeCorpDivisions {
		if d.CorporationID == corpID && d.Division == div {
			return true
		}
	}
	return false
}

// tradeJournalCacheKey builds a deterministic key for the result cache.
func tradeJournalCacheKey(userID string, filter *db.WalletScopeFilter, since time.Time, mode engine.FIFOMode, fees journalFeeRates) string {
	h := sha1.New()
	h.Write([]byte(fmt.Sprintf("all=%v|chars=%v|corp=%v|since=%d|mode=%s|fees=%v/%g/%g",
		filter.IncludeAll, filter.IncludeCharacters, filter.IncludeCorpDivisions, since.Unix(), mode,
		fees.set, fees.salesTax, fees.brokerFee)))
	return userID + "|" + hex.EncodeToString(h.Sum(nil))
}
