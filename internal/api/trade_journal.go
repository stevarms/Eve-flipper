package api

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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
	WalletKind        string `json:"wallet_kind"` // "character" | "corporation"
	CharacterID       int64  `json:"character_id,omitempty"`
	CorporationID     int64  `json:"corporation_id,omitempty"`
	Division          int    `json:"division,omitempty"`
	SyncedAt          string `json:"synced_at"`
	LiveTxnRows       int    `json:"live_txn_rows"`
	LiveJournalRows   int    `json:"live_journal_rows"`
	LiveIndustryRows  int    `json:"live_industry_rows,omitempty"`
	LimitHit          bool   `json:"limit_hit"`
	Error             string `json:"error,omitempty"`
}

type journalSyncResponse struct {
	Wallets                          []journalSyncWalletStat `json:"wallets"`
	IndustryJobsAutoLinked           int                     `json:"industry_jobs_auto_linked"`
	IndustryJobsStillUnlinkedAmbig   int                     `json:"industry_jobs_still_unlinked_ambiguous"`
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

	// Wallet transactions
	if txns, err := s.esi.GetWalletTransactions(sess.CharacterID, token); err == nil {
		s.enrichWalletTransactionTypeNames(txns)
		if _, aerr := s.db.UpsertWalletTransactionsForUser(userID, sess.CharacterID, txns); aerr != nil {
			log.Printf("[TradeJournal] wallet tx archive %s: %v", sess.CharacterName, aerr)
		}
		stat.LiveTxnRows = len(txns)
		if len(txns) >= 2500 {
			stat.LimitHit = true
		}
	} else if stat.Error == "" {
		stat.Error = fmt.Sprintf("wallet: %v", err)
	}

	// Wallet journal
	if entries, err := s.esi.GetWalletJournal(sess.CharacterID, token); err == nil {
		if _, aerr := s.db.UpsertWalletJournalForUser(userID, sess.CharacterID, entries); aerr != nil {
			log.Printf("[TradeJournal] wallet journal archive %s: %v", sess.CharacterName, aerr)
		}
		stat.LiveJournalRows = len(entries)
		if len(entries) >= 2500 {
			stat.LimitHit = true
		}
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
		}
		stat.LiveIndustryRows = len(delivered)
	}

	return stat
}

// syncCorpWallets iterates the corp divisions the user has access to and
// syncs each into the corp archive. Access is determined lazily by trying
// the wallet fetch — 403s (missing Accountant role) are silently skipped.
func (s *Server) syncCorpWallets(userID string, sessions []*auth.Session, filter *db.WalletScopeFilter) []journalSyncWalletStat {
	out := []journalSyncWalletStat{}
	// Find the first session whose character has a corp; use their token for
	// the corp calls.
	for _, sess := range sessions {
		token, err := s.sessions.EnsureValidTokenForUserCharacter(s.sso, userID, sess.CharacterID)
		if err != nil {
			continue
		}
		corpID, err := s.esi.GetCharacterCorporationID(sess.CharacterID)
		if err != nil || corpID <= 0 {
			continue
		}
		s.mu.RLock()
		sdeData := s.sdeData
		s.mu.RUnlock()
		provider := corp.NewESICorpProvider(s.esi, sdeData, token, corpID, sess.CharacterID)
		wallets, err := provider.GetWallets()
		if err != nil {
			// Skip this character — probably lacks role. Try the next session.
			continue
		}
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
			if txns, err := provider.GetTransactions(wallet.Division); err == nil {
				if _, aerr := s.db.UpsertCorpWalletTransactionsForUser(userID, int64(corpID), wallet.Division, txns); aerr != nil {
					log.Printf("[TradeJournal] corp %d div %d tx archive: %v", corpID, wallet.Division, aerr)
				}
				stat.LiveTxnRows = len(txns)
				if len(txns) >= 2500 {
					stat.LimitHit = true
				}
			} else if stat.Error == "" {
				stat.Error = fmt.Sprintf("corp txns: %v", err)
			}
			if entries, err := provider.GetJournal(wallet.Division, 0); err == nil {
				if _, aerr := s.db.UpsertCorpWalletJournalForUser(userID, int64(corpID), wallet.Division, entries); aerr != nil {
					log.Printf("[TradeJournal] corp %d div %d journal archive: %v", corpID, wallet.Division, aerr)
				}
				stat.LiveJournalRows = len(entries)
				if len(entries) >= 2500 {
					stat.LimitHit = true
				}
			}
			out = append(out, stat)
		}
		// Only sync corp for the first character with a valid token — a corp
		// is a single set of wallets, not per-character. Break after the
		// first successful pass.
		break
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

// --- read handlers (summary / by-type / lots) ---

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
		TypeID                 int32   `json:"type_id"`
		TypeName               string  `json:"type_name,omitempty"`
		BuysQty                int64   `json:"buys_qty"`
		SellsQty               int64   `json:"sells_qty"`
		AvgBuyPrice            float64 `json:"avg_buy_price"`
		AvgSellPrice           float64 `json:"avg_sell_price"`
		TradingProfit          float64 `json:"trading_profit"`
		ManufacturingProfit    float64 `json:"manufacturing_profit"`
		CombinedProfit         float64 `json:"combined_profit"`
		HeldQtyTrade           int64   `json:"held_qty_trade"`
		HeldQtyManufacture     int64   `json:"held_qty_manufacture"`
		UnattributedSellsQty   int64   `json:"unattributed_sells_qty"`
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

func (s *Server) handleTradeJournalLots(w http.ResponseWriter, r *http.Request) {
	typeIDStr := strings.TrimSpace(r.URL.Query().Get("type_id"))
	if typeIDStr == "" {
		writeError(w, 400, "type_id required")
		return
	}
	tid64, err := strconv.ParseInt(typeIDStr, 10, 32)
	if err != nil || tid64 <= 0 {
		writeError(w, 400, "invalid type_id")
		return
	}
	typeID := int32(tid64)
	res, _, _, _, err := s.loadTradeJournalResult(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	lots := make([]engine.TradeJournalLot, 0)
	for _, l := range res.Lots {
		if l.TypeID == typeID {
			lots = append(lots, l)
		}
	}
	mfgLots := make([]engine.ManufacturingLot, 0)
	for _, m := range res.ManufacturingLots {
		if m.ProductTypeID == typeID {
			mfgLots = append(mfgLots, m)
		}
	}
	writeJSON(w, map[string]any{
		"lots":               lots,
		"manufacturing_lots": mfgLots,
	})
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

	key := tradeJournalCacheKey(userID, filter, sinceDate, fifoMode)
	if cached := journalRuntime.get(key); cached != nil {
		return cached, filter, sinceDate, fifoMode, nil
	}

	// Load archive, compute, cache.
	txns, _, err := s.db.ListArchivedWalletActivityForUser(userID, *filter, sinceDate)
	if err != nil {
		return nil, filter, sinceDate, fifoMode, err
	}
	jobs, err := s.db.ListArchivedIndustryJobsForUser(userID, filter.IncludeCharacters, time.Time{})
	if err != nil {
		return nil, filter, sinceDate, fifoMode, err
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
			// Prefer Activities["manufacturing"] if present; else use the
			// legacy Materials/Products fields.
			if act := bp.Activities["manufacturing"]; act != nil {
				materials[j.BlueprintTypeID] = act.Materials
				if len(act.Products) > 0 {
					products[j.BlueprintTypeID] = act.Products[0]
				}
			} else {
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
	meResolver := s.buildMEResolver(userID, sdeData)

	// User's flat sales-tax + broker-fee rates from config (fallback 8% / 1%).
	salesTax, brokerFee := 8.0, 1.0
	if cfg := s.loadConfigForUser(userID); cfg != nil {
		salesTax = cfg.SalesTaxPercent
	}

	opts := engine.TradeJournalOptions{
		SinceDate:        sinceDate,
		FIFOMode:         fifoMode,
		SalesTaxPercent:  salesTax,
		BrokerFeePercent: brokerFee,
		Materials:        materials,
		Products:         products,
		MEByJob:          meResolver,
		RegionAvgByType:  regionAvg,
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
	journalRuntime.put(key, result)
	return result, filter, sinceDate, fifoMode, nil
}

// buildMEResolver returns a closure implementing the plan's ME lookup chain:
// planner-link → owned-BPO → tech-level default → 0 fallback.
func (s *Server) buildMEResolver(userID string, sdeData *sde.Data) func(engine.JournalIndustryJob) engine.MEResolution {
	// Pre-load ledger job map: external_job_id → ledger row (for planner link).
	ledgerByExt := map[int64]db.IndustryLedgerJobME{}
	if s.db != nil {
		if rows, err := s.db.ListLinkedLedgerJobMEForUser(userID); err == nil {
			for _, r := range rows {
				ledgerByExt[r.ExternalJobID] = r
			}
		}
	}
	// Pre-load user's BP inventory: BlueprintTypeID → max ME across owned BPs.
	// TODO(v1.5): actually call esi.GetCharacterBlueprints per session. For
	// now this map is empty; the resolver falls through to tech-level defaults.
	meByBP := map[int32]int32{}

	return func(job engine.JournalIndustryJob) engine.MEResolution {
		// 1. Ledger link.
		if r, ok := ledgerByExt[job.JobID]; ok {
			return engine.MEResolution{ME: r.ME, Source: "planner"}
		}
		// 2. Owned BP inventory.
		if me, ok := meByBP[job.BlueprintTypeID]; ok {
			return engine.MEResolution{ME: me, Source: "bpo"}
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
// date) and stale-sync warnings (>20d) for the scoped wallets.
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
	for _, m := range meta {
		trackingSince[m.WalletKey] = m.EarliestDate
		if m.LastSyncAt != "" {
			if t, err := time.Parse(time.RFC3339, m.LastSyncAt); err == nil {
				age := now.Sub(t)
				if age > staleAt {
					staleSyncs = append(staleSyncs, map[string]any{
						"wallet_key":   m.WalletKey,
						"last_sync_at": m.LastSyncAt,
						"days_ago":     int(age.Hours() / 24),
					})
				}
			}
		}
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
func tradeJournalCacheKey(userID string, filter *db.WalletScopeFilter, since time.Time, mode engine.FIFOMode) string {
	h := sha1.New()
	h.Write([]byte(fmt.Sprintf("all=%v|chars=%v|corp=%v|since=%d|mode=%s",
		filter.IncludeAll, filter.IncludeCharacters, filter.IncludeCorpDivisions, since.Unix(), mode)))
	return userID + "|" + hex.EncodeToString(h.Sum(nil))
}
