# EVE Flipper — Audit Index (August 2026)

Read-only audit pass across four dimensions. The four thematic docs live
alongside this file; this index is the entry point and the prioritized
action list.

## The four docs

| Doc | Scope | Length |
|---|---|---|
| [ARCHITECTURE.md](ARCHITECTURE.md) | Backend + frontend architecture map. Every `internal/` module, every `engine/*.go` file, the API surface, persistence, ESI client, SDE loader, frontend tab structure, three ASCII data-flow diagrams, 14 pitfalls. | ~1,850 lines |
| [EVE_ACCURACY.md](EVE_ACCURACY.md) | Every ISK-affecting formula checked against real EVE mechanics. Verdicts: MATCHES / DIVERGES / STALE / INCOMPLETE / UNVERIFIED, with numerical impact where the code diverges. | ~580 lines |
| [DUPLICATION.md](DUPLICATION.md) | Cross-cutting logic re-implemented across files, ranked by drift/bug risk. Consolidation proposals with signatures + risk. | ~440 lines |
| [UI_AUDIT.md](UI_AUDIT.md) | Terminology drift, action-verb inconsistencies, visual mismatches, hardcoded strings. Every finding proposes a canonical term grounded in EVE Online usage. | ~570 lines |

## Cross-cutting top findings

The five findings below appear in more than one audit, or have direct ISK
impact on the user. Fix these first.

### 1. Broker Relations skill coefficient is wrong (EVE_ACCURACY §Broker fee)

- File: `internal/api/character_market_fees.go:34`.
- Code: `3.0 − 0.4×level`, giving L5 = **1.0%**.
- EVE: `3.0 − 0.3×level` since March 2020, giving L5 = **1.5%**.
- Impact: every station-trading margin, route ROI, backtest profit, and
  industry sell-side revenue that ingests the suggested fee is off by 50
  bp per broker leg. On a round-trip trade, that's ~1 pp of phantom
  profit. On a 2% margin flip, that's a 25% overstatement.
- Fix scope: 1-character constant change plus test update.

### 2. Invention skill bonuses are never applied (EVE_ACCURACY §Invention)

- File: `internal/engine/industry.go:1255-1354`, `calculateInventionStep`.
- Code: `chance = base × decryptorMult`.
- EVE: `chance = base × (1 + Encryption/40) × (1 + (Datacore1 + Datacore2)/30) × decryptorMult`.
  At max skills, that's a 1.5× multiplier.
- Impact: a max-skilled inventor sees expected-attempts inflated by 50%,
  which propagates through datacore cost and invention job cost. T2 builds
  that are profitable in-game get reported as unprofitable.
- Related: no `suggestedInventionChance` API companion — the frontend has
  no server-provided path to inject skill-adjusted probability the way it
  gets suggested broker fee and sales tax.
- Fix scope: add skill-aware chance calc; extend the character-fees
  endpoint with a suggestion; wire it into the Industry tab defaults.

### 3. Industry-tab fee formula drifts from scanner (DUPLICATION §Cluster 2, EVE_ACCURACY §Fees)

- File: `internal/engine/industry.go:564-566`.
- `analyzeIndustry` computes `revenue = ask × qty × (1 − tax/100) × (1 − broker/100)` (multiplicative).
- Canonical `tradeFeeMultipliers` in `fees.go:54` uses `1 − (broker + tax)/100` (additive) — used everywhere else (scanner, station_trading, backtest, execution, contracts, ...).
- Also reads `params.BrokerFee` instead of the split-fee inputs, so
  `SplitTradeFees=true` silently degrades to legacy behavior on the
  Industry tab.
- Impact: Industry tab and Profitable Blueprints scanner disagree on the
  same blueprint by ~15 bp of revenue. The tab shows a different profit
  than the scanner-ranked profit for the identical inputs.
- Fix scope: route `industry.go:564-566` through `tradeFeeMultipliers`
  after wrapping `IndustryParams` fee fields into `tradeFeeInputs`.
  Behavior-changing; `industry_alignment_test.go` will need updated pin
  values.

### 4. Reprocessing is a UI-only stub (EVE_ACCURACY §Reprocessing)

- Fields: `IndustryParams.IncludeReprocessing` and `ReprocessingYield`
  are declared in `internal/engine/industry.go:34-35` and defaulted at
  `industry.go:420-422`.
- **No consumer.** Neither field is read anywhere in `engine/`. No
  reprocessing formula, no station-tax model, no yield-implant model.
- Impact: any UI toggle labelled "consider reprocessing as alternative"
  or a "reprocessing yield" slider is a no-op today.
- Fix scope: either implement the reprocessing engine (base yield ×
  skill-stack × structure-yield × station-tax × implant) or hide the UI
  toggle and remove the fields so the API surface stops lying.

### 5. Frontend ISK formatters — 4 copies carry a real bug (DUPLICATION §Cluster 1)

- Canonical: `frontend/src/lib/format.ts:11` — locale-aware, signed-aware, correct.
- Buggy copies (leak raw digits for negative values):
  `RouteBuilder.tsx:56`, `RouteSafetyModal.tsx:11`,
  `ExecutionPlannerPopup.tsx:32`,
  `StationTradingExecutionCalculator.tsx:31`.
- `CharacterPopup.tsx:271` explicitly documents fixing this exact bug —
  the other copies never got the fix.
- Also: suffix spacing (`"B"` vs `" B"`), decimal drift
  (`.toFixed(1)` vs `.toFixed(2)`), top tier drift (T in
  TradeJournal, Q in WarTracker, neither in CharacterPopup), locale
  drift (only `lib/format.ts` respects `ru-RU`).
- Impact: negative P&Ls in Route Builder, Route Safety, Execution
  Planner, and Station Trading Execution Calculator render as
  `-1234567890` instead of `-1.23B`. Same number displays as `2T` in
  Trade Journal but `2000B` in Character Popup.
- Fix scope: extend `formatISK` in `lib/format.ts` with `opts.tiers` +
  `opts.decimals` + `opts.space`; migrate the 12+ local copies. Ripgrep
  `function formatIsk|const formatIsk|function formatISK|const formatISK`
  covers the audit surface.

## Prioritized action plan

### Phase 1 — Correctness bugs (numbers wrong, fix first)

1. Broker Relations coefficient (`character_market_fees.go:34`).
2. Invention skill bonuses (`industry.go:1255-1354` + new
   `suggestedInventionChance` API companion).
3. Industry fee formula (`industry.go:564-566` → `tradeFeeMultipliers`).
4. Negative-value bug in the 4 formatIsk copies (or migrate to the
   canonical helper).

None of these should break APIs; all are behavior-changing so update the
relevant regression tests in the same PR.

### Phase 2 — Missing features that appear to work

5. Reprocessing engine — either implement or remove the dead fields.
6. 100 ISK broker-fee minimum floor (`engine/fees.go`) — matters for PI
   raw materials, ammo, mining crystals, small flips.
7. Relist / modify order fee in undercut recommendation
   (`engine/undercut.go:105-113`) — currently the "reprice" recommendation
   ignores the cost of relisting, which can invert the recommendation on
   thin books with a 0.01 ISK undercut.

### Phase 3 — Shared infrastructure (kills duplication, hardens safe paths)

8. Consolidate 12+ ISK formatters into `lib/format.ts` behind a single
   parameterised signature.
9. Add `internal/api/ndjson.go` with a shared `ndjsonEmitter` (always
   mutex-serialised, always context-aware) and migrate the 8 existing
   NDJSON handlers.
10. Extract `finalizeFlipScanResults` for the three flip-scan handlers
    (`handleScan`, `handleScanMultiRegion`, `handleScanRegionalDay`) that
    share ~40 lines of identical post-processing.
11. Refactor `streamNdjson<T>` into a lower-level
    `streamNdjsonLines<M>(onMessage, ...)` used by all 10 stream
    endpoints (removes 4 hand-rolled reader loops).
12. Delete `PRICING_HUB_PRESETS`; import `STATION_TRADING_HUBS`.

### Phase 4 — EVE terminology alignment

13. **Bid/Ask → Buy Order / Sell Order everywhere.** `Best Ask (L1)` →
    `Sell Order Price` (or short `Buy @`); `Best Bid (L1)` → `Buy Order
    Price` (or `Sell @`); `Sug. bid` → `Suggested Buy`; `top-buy` →
    `top buy order`; `discount bid target` → `discount buy target`.
14. **Split action verbs by intent.** Scan = market fetch. Sync =
    ESI-owned dataset. Analyze = compute-only. Refresh = cache re-read.
    Find = search with params (Route Builder). Rename `plexRefresh` →
    `plexScan`, `priceAuditFetchBtn` → `priceAuditScanBtn`, etc. (See
    UI_AUDIT §A1 for the full table.)
15. **Broker fee % / Sales tax % standardisation.** Kill `paramsBuyTax`
    / `buySalesTax` — EVE has no buy-side sales tax; rename to
    `Resale tax %` if it models eventual-resale, else drop.
16. **Column headers.** Qty (tables) + Quantity (tooltips); m³ (cargo);
    Daily Vol (market); ROI % (never Margin); Station / Location split;
    Sec / Security / Sec Status per width.
17. **Delete / Remove / Clear semantic split.** Delete = permanent
    destroy (confirm dialog); Remove = drop from list; Clear = reset to
    default.
18. **Cancel overload.** Split into Cancel (dialog close), Stop scan
    (abort in-progress), Cancel order (EVE market order — this one
    matters, broker fee is lost), Discard batch (abandon).
19. **Multibuy spelling** — one form (`Multibuy`, title case, one word)
    matching EVE's Marketplace menu.
20. **Tab titles** to noun phrases (`Radius Trade`, `Regional Trade`,
    `Station Trade`, `Contract Trade`, `Industry`, `PI Factory`,
    `PLEX`, `Watchlist`, `Trade Journal`, `Price Audit`, `Market
    Making`, `War Tracker`, `History`).

### Phase 5 — Cleanup

21. Route every empty state through `EmptyState.tsx` (7 tabs bypass it).
22. Migrate the 60+ hardcoded `.tsx` strings into `en.ts` / `ru.ts`
    (see UI_AUDIT appendix).
23. Sec-status labels — `High-Sec` / `Low-Sec` / `Null-Sec` (hyphenated),
    or compact `hisec` / `lowsec` / `nullsec` in badges, consistently.
    Also raise `routeSecurityHighsec` threshold in the label or in the
    code so `≥0.45` and `Highsec only` agree.
24. Modal footer alignment (right-aligned, primary rightmost,
    destructive far-left).
25. Loading-indicator shape (present-continuous verb + UTF-8 ellipsis,
    consistent progress-bar slot).
26. Externalise the SCC Surcharge constant (`industry.go:263 = 4.0`) —
    currently correct but CCP has stepped it twice since 2022. Move to
    a config value so a future patch is a one-line change.
27. Merge `cmd*` and `shortcut*` locale sets; add missing tab shortcuts.

## What's verified good (no action needed)

From EVE_ACCURACY:
- Manufacturing job-cost breakdown (post-2026-07-22 fix).
- ME/TE multiplier stacking with per-material floor.
- EIV formula (base × runs, ME-agnostic).
- Decryptor table (all 8 decryptors match CCP values).
- T2 BPC base ME/TE/Runs with per-target override for T2 ships.
- Structure rig sec-status multipliers via SDE dogma attributes.
- PLEX global market region (19000001).
- SP-per-hour constants (2250 base / 2700 with +5).
- Skill injector diminishing-returns tiers.
- POCO base values per PI tier (5 / 400 / 7,200 / 60,000 / 1,200,000).
- FIFO cost basis for portfolio P&L, Sharpe, EWMA volatility,
  Cornish-Fisher VaR.
- RSI(14) with Wilder smoothing, Bollinger Bands with population σ.
- Slippage / VWAP order-book walk.
- `tradeFeeMultipliers` centralisation — one place to change if broker
  mechanics shift (with the industry.go drift noted above as the one
  exception that must be routed through it).

From DUPLICATION:
- `writeJSON` / `writeError` — one canonical helper, 738 uses, no
  competing local variants.
- `hostedQuotaFeatureForRequest` — single canonical mapping enforced by
  a test.
- ESI client retries / rate-limit — centralised in `internal/esi/`; no
  caller re-implements.
- `parseAuthScope` / `authSessionsForScope` — already cleanly extracted,
  ~20 call sites.

From UI_AUDIT:
- Corp / Alliance / Standing terminology.
- Warp / Jump / Dock verb usage (`Set Destination`, `Open Market
  Window` match EVE menus verbatim).
- Trade Hub usage.
- Wallet / Journal / Transactions structural naming.
- PLEX capitalisation (always uppercase).
- ME / TE / SP / LP capitalisation (with a couple of `isk/run`
  outliers to normalise).
- No "crafting" or "production" as a verb (both would misfire on an EVE
  audience).

## How to use this audit

Each finding above references the specific doc + section for detail.
Nothing has been changed in the codebase; every recommendation is an
audit conclusion, not a commit.

The four thematic docs are self-contained — read them individually as
reference material. This index is the "what should I fix first" view.
