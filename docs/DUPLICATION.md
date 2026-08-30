# Duplication and Consolidation Audit

Read-only investigation of `internal/` (Go backend) and `frontend/src/` (React/TS
SPA). Focus: load-bearing calculations, fetch patterns, and state shapes that
are re-implemented across files — especially where copies have already drifted.

## Executive summary

The five highest-value consolidation opportunities, ranked by drift/bug risk:

1. **Frontend ISK formatters** — 12+ local `formatIsk`/`formatISK` copies in
   parallel with `lib/format.ts`, at least 4 with a real negative-value bug
   (`CharacterPopup.tsx:271-283` explicitly documents fixing it; the other
   copies never got the fix). See Cluster 1.
2. **Engine fee-multiplier drift in `industry.go`** — `analyzeIndustry`
   computes the sell-side multiplier as `(1 - salesTax) * (1 - broker)` instead
   of the canonical `1 - (broker + tax)/100` used by `tradeFeeMultipliers`,
   and reads `params.BrokerFee` rather than the split-fee inputs. Real
   numerical drift versus every other engine surface. See Cluster 2.
3. **API NDJSON scan handler boilerplate** — 8 handlers in `server.go` /
   `industry_blueprint_scan.go` re-implement the same
   `Content-Type` + `Flusher` + `sendProgress` + `type:result/progress/error`
   line-writer, and 3 of them (`handleScan`, `handleScanMultiRegion`,
   `handleScanRegionalDay`) share ~40 lines of identical post-processing
   (`filterFlipResultsExcludeStructures` → `filterFlipResultsMarketDisabled`
   → inventory enrich → KPI reduction → history insert → result emit).
   See Cluster 3.
4. **Frontend NDJSON stream readers** — `streamNdjson<T>` in `lib/api.ts`
   handles 6 endpoints, but 4 more endpoints (`analyzeIndustry`,
   `scanProfitableBlueprints`, `stationAIChatStream`, `refreshDemandData`)
   each hand-roll their own reader loop with the same buffer/split/decode
   logic. See Cluster 4.
5. **Trade-hub station lists** — `frontend/src/lib/tradeHubs.ts` and
   `components/industry/PricingHubPicker.tsx` each hardcode the same 5-hub
   list (Jita, Amarr, Dodixie, Rens, Hek) with the same station IDs. See
   Cluster 5.

---

## Cluster 1: Frontend ISK formatting (highest priority)

**Instances:**
- `frontend/src/lib/format.ts:11` — canonical `formatISK(value, locale?)`. Uses
  `Math.abs(value)` + sign, locale-aware `toLocaleString`, thresholds at
  1e9/1e6/1e3 with a **space** before the suffix (" B", " M", " K"),
  `maximumFractionDigits: 2/2/1`.
- `frontend/src/components/RouteBuilder.tsx:56` — `formatISK`. No space, no
  locale, no negative handling (`v >= 1e9` on a signed value; negative
  amounts drop through to `v.toFixed(0)` as raw digits).
- `frontend/src/components/RouteSafetyModal.tsx:11` — same shape as
  RouteBuilder. **Same negative-value bug.** Uses `.toFixed(1)/0/0`.
- `frontend/src/components/ExecutionPlannerPopup.tsx:32` — same shape.
  **Same negative-value bug.** Uses `.toFixed(2)/2/1`.
- `frontend/src/components/StationTradingExecutionCalculator.tsx:31` —
  identical body to ExecutionPlannerPopup (word-for-word duplicate).
- `frontend/src/components/CharacterPopup.tsx:271` — `formatIsk`. **Handles
  negatives correctly** via `const abs = Math.abs(value)` + `.toFixed(2)/2/1`.
  Comment L272-277 explicitly documents fixing the negative-value bug that
  the other copies still have.
- `frontend/src/components/CorpDashboardApp.tsx:32` — `formatIsk`. Handles
  negatives (`Math.abs(value)` at each threshold), no T-tier.
- `frontend/src/components/TradeJournal.tsx:54` — `formatIsk`. Handles
  negatives, **adds T (trillion) tier.**
- `frontend/src/components/ProfitPill.tsx:20` — same shape as TradeJournal
  (T-tier, negatives handled), but `.toFixed(1)/0` for M/K vs TradeJournal's
  `.toFixed(2)/1`.
- `frontend/src/components/WarTracker.tsx:54` — `formatISK`. **Adds Q
  (quadrillion) tier**, but does not handle negatives (`value >= 1e15`).
- `frontend/src/components/WatchlistTab.tsx:135` — `formatMetricValue` mixes
  ISK formatting into metric switch.
- `frontend/src/components/PaperTradeJournalPopup.tsx:997`,
  `PriceAudit.tsx:25`, `industry/IndustryStockpilePanel.tsx:48`,
  `industry/IndustryAnalysisResultsPanel.tsx:593`,
  `industry/IndustryProfitableScannerPanel.tsx:755` — more local variants.

**Differences observed:**
- Negative-value bug in RouteBuilder/RouteSafetyModal/ExecutionPlannerPopup/
  StationTradingExecutionCalculator: signed `>= 1e9` skips the suffix branch
  for negative amounts, leaking `-1234567890` instead of `-1.23B`. Same bug
  the CharacterPopup comment documents was fixed there.
- Suffix format drift: "B" (no space) vs " B" (space). User-visible
  inconsistency between the Character Popup and the Trade Journal.
- Decimals drift: `.toFixed(1)` vs `.toFixed(2)` at the B tier depending on
  which surface renders it.
- Highest tier drift: `T` in Trade Journal / ProfitPill, `Q` in WarTracker,
  neither in Character Popup — a `2T` P&L renders as `2000B` in the popup
  but `2T` in the journal.
- Locale drift: `lib/format.ts` respects browser locale (`ru-RU`/`en-US`);
  every other copy hardcodes en-US number formatting or drops locale.

**Consolidation proposal:**
- Extend `lib/format.ts` with:
  - `formatIsk(value, locale?, opts?)` — signed-aware, T/Q optional via
    `opts.tiers`, decimals via `opts.decimals`, spacing via `opts.space`.
  - `formatIskSigned(value, locale?)` — adds `+` prefix for non-negative.
- Migrate the 12+ local copies to it. Ripgrep `function formatIsk|const
  formatIsk|function formatISK|const formatISK` covers the audit.
- Risk: low if the shared helper is parameterised to preserve each caller's
  current visual output. Fixing the negative-value bug in the process is a
  behaviour change (correct sign will now appear) — worth calling out in
  the PR.

**Priority:** high

---

## Cluster 2: Engine fee-multiplier drift in `industry.go`

**Instances:**
- `internal/engine/fees.go:54` — canonical `tradeFeeMultipliers` returning
  `sellRevenueMult = 1.0 - (sellBroker + sellTax)/100`. **Additive form**
  (matches EVE's real fee calculation — both fees applied to the same base).
- `internal/engine/scanner.go:641`, `regional_day_trader.go:171`,
  `route.go:266`, `backtest.go:260`, `backtest_orderbook.go:146`,
  `contracts.go:169`, `execution.go:418`, `station_trading.go:553` &
  `:1016` — all use `tradeFeeMultipliers` correctly.
- `internal/engine/industry.go:564-566` — inline
  `unitAsk * float64(totalQuantity) * (1.0 - params.SalesTaxPercent/100) *
  (1.0 - params.BrokerFee/100)`. **Multiplicative form** — subtly different
  result. Reads `params.BrokerFee` (not `BrokerFeePercent`, not any of the
  split-fee fields).
- `internal/engine/order_desk.go:173-175` — inline additive, but the buy
  side uses `Price * (1 + brokerFee/100)` (no split-fee awareness).
- `internal/engine/plex.go:388, 511` — inline additive; no split-fee awareness.

**Differences observed:**
- Numerical drift: at typical rates (broker 3%, tax 4.5%),
  - additive: `1 - 0.075 = 0.925`
  - multiplicative: `0.97 × 0.955 = 0.92635`
  Industry analysis over-taxes by ~0.15pp of revenue on every build vs. the
  scanner's identical fee inputs. Small in isolation, but the Industry tab
  and the Profitable Blueprints scanner disagree on the same blueprint.
- `industry.go` also lacks split-fee support entirely — the split-mode
  toggle silently degrades to legacy fees on the Industry tab even when the
  user has configured different buy vs. sell tax percentages.
- `order_desk.go` and `plex.go` copies are consistent with each other and
  with `tradeFeeMultipliers` in the additive part; they're missing the
  split-fee normalisation only.

**Consolidation proposal:**
- Route `industry.go:564-566` through `tradeFeeMultipliers` after moving the
  `IndustryParams` struct's fee fields into the `tradeFeeInputs` shape (or
  wrapping them at the call site). Fixes both the numerical drift and the
  split-fee ignore.
- Route `order_desk.go` and `plex.go` through `tradeFeeMultipliers` for the
  split-fee normalisation; the base multiplier is already equivalent.
- Risk: medium. This is a behaviour change — Industry analysis numbers will
  shift slightly, and any test that pinned exact revenue values will need
  updating. `industry_alignment_test.go` in particular should be re-checked.

**Priority:** high

---

## Cluster 3: API NDJSON scan handler boilerplate

**Instances:**
- `internal/api/server.go:2868` (`handleScan`) — sets `application/x-ndjson`
  + `Cache-Control: no-cache`, does the Flusher assert, defines `sendProgress`
  closure, emits `type:progress` / `type:error` / `type:result` lines.
- `server.go:2986` (`handleScanMultiRegion`) — same seven lines, same closure.
- `server.go:3114` (`handleScanRegionalDay`) — same seven lines.
- `server.go:3469` (`handleScanContracts`) — same, plus context-cancel guards.
- `server.go:3610` (`handleRouteFind`) — same.
- `server.go:4002` (`handleScanStation`) — same, plus `streamAlive` flag.
- `server.go:10840`, `12391`, `12917` — three more (demand refresh, achievements
  stream, other).
- `internal/api/industry_blueprint_scan.go:1314` (`handleProfitableScan`) —
  same header/flusher pattern, but wraps the writer in a `sync.Mutex` for
  concurrent goroutine emission (the only handler that fans out).

**Additional identical post-processing** across `handleScan` (server.go:2900-2960),
`handleScanMultiRegion` (3022-3084), and `handleScanRegionalDay`
(3160-3260) — the exact same sequence of
`filterFlipResultsExcludeStructures` → `filterFlipResultsMarketDisabled` →
`loadRegionalInventorySnapshot` → `EnrichFlipResultsWithInventory` →
`stationCacheMetaForFlipScan` → `for _, r := range results { kpiProfit :=
flipResultKPIProfit(r); ... }` → `trackScanFinished` → `InsertHistoryFull`
→ `go InsertFlipResults` → `processWatchlistAlerts` → result-line marshal.
About 40 near-identical lines each.

**Differences observed:**
- Two variants of the sendProgress closure: one plain, one context-aware
  (`if ctx.Err() != nil { return }` before writing). Any handler using
  `context.WithCancel(r.Context())` needs the guarded variant — a copy that
  forgets it will keep pushing bytes into a canceled response and log
  broken-pipe warnings.
- `handleProfitableScan` correctly uses a `writeMu sync.Mutex` because it
  fans out; the other 8 handlers don't need one only because they don't fan
  out. A future handler that copies the boilerplate and adds a worker pool
  will race the writer — the mutex must arrive with the copy.
- `handleScanRegionalDay` uses `historyCount = len(dayRows)` when non-empty
  (differs from the other two which always use `len(results)`).

**Consolidation proposal:**
- Add `internal/api/ndjson.go`:
  ```go
  type ndjsonEmitter struct {
      w      http.ResponseWriter
      f      http.Flusher
      mu     sync.Mutex
      ctx    context.Context
  }
  func (s *Server) beginNdjson(w http.ResponseWriter, r *http.Request)
      (*ndjsonEmitter, bool)
  func (e *ndjsonEmitter) Progress(msg string)
  func (e *ndjsonEmitter) Error(msg string)
  func (e *ndjsonEmitter) Result(payload any)
  ```
  Always mutex-serialised, always context-aware — makes the safe path free.
- For the flip-scan trio, extract a `finalizeFlipScanResults(kind, params,
  results, req, ...)` helper covering filter → enrich → cacheMeta → KPI →
  history insert → result emit. Three call sites collapse to ~15 lines each.
- Risk: medium. Header write behaviour is user-visible via
  `handleProfitableScan`'s mutex — if the shared helper is always mutex'd
  the other handlers slow by one atomic per line, which is negligible; if
  it isn't, `handleProfitableScan` needs to keep its explicit `writeMu`.

**Priority:** high — every new NDJSON endpoint currently copies 15 lines
and any of them can drop the context-cancel check without noticing.

---

## Cluster 4: Frontend NDJSON stream readers

**Instances:**
- `frontend/src/lib/api.ts:223` — generic `streamNdjson<T>()`. Used by 6
  endpoints (scan, scanMultiRegion, scanRegionalDayTrader, scanContracts,
  findRoutes, scanStation).
- `api.ts:2382` — `analyzeIndustry` — 40 lines of `reader = res.body.
  getReader()` + `decoder = new TextDecoder()` + buffer split loop, parsing
  `NdjsonIndustryMessage`. Same shape as `streamNdjson` but the "result"
  payload is a single `data` object, not `data: T[]`.
- `api.ts:2462` — `scanProfitableBlueprints` — same 40-line loop parsing
  `NdjsonProfitableScanMessage`. Same "single result object" shape.
- `api.ts:1691` — `stationAIChatStream` — same 40-line loop, but the
  message enum includes `delta` and `usage` on top of `progress/result/error`.
- `api.ts:2546` — `refreshDemandData` — same 40-line loop, no "result"
  message emitted.

**Differences observed:**
- The 4 hand-rolled readers agree on the buffer/split/decode mechanics but
  each redefines the same `while (true)` block. If a bug in reader
  handling ever needs fixing (e.g. `TextDecoder({fatal:true})`), it takes 5
  edits.
- `streamNdjson`'s constraint is that "result" contains `data: T[]`. The
  four hand-rolls all use a single object. That's the only reason they
  didn't reuse it.

**Consolidation proposal:**
- Refactor `streamNdjson` into a lower-level `streamNdjsonLines<M>(url,
  body, onMessage, signal, errorMessage)` that yields typed messages, and
  reimplement the current `streamNdjson<T>` on top of it. The 4 hand-rolls
  become ~15-line handlers over `onMessage`. Result-shape polymorphism
  (`data: T[]` vs `data: T`) stays at the handler level where it belongs.
- Risk: low. Only touches the shared file; each caller keeps its own typed
  message enum.

**Priority:** medium

---

## Cluster 5: Trade-hub station lists

**Instances:**
- `frontend/src/lib/tradeHubs.ts:12` — `STATION_TRADING_HUBS` — 5 hubs,
  station IDs `60003760/60008494/60011866/60004588/60005686`.
- `frontend/src/components/industry/PricingHubPicker.tsx:15` —
  `PRICING_HUB_PRESETS` — identical 5 hubs, identical station IDs.

**Differences observed:**
- None — the tradeHubs.ts header comment even says "Keep in sync with the
  industry scanner's pricing-hub presets in
  IndustryProfitableScannerPanel.tsx" (which now lives in
  PricingHubPicker.tsx). The two copies exist because the interface types
  are named differently (`TradeHub` vs `PricingHubPreset`) but the field
  shapes are identical.

**Consolidation proposal:**
- Delete `PRICING_HUB_PRESETS`, import `STATION_TRADING_HUBS` from
  `lib/tradeHubs.ts`. Adjust `PricingHubPicker` to use the `TradeHub` type.
- Risk: trivial. One file changes, one file loses 8 lines.

**Priority:** medium

---

## Cluster 6: `parseAuthScope` + `authSessionsForScope` — already extracted

**Instances:** `parseAuthScope` + `authSessionsForScope` are used ~20 times
across `server.go`, `industry_blueprint_scan.go`, `paper_trades_reconcile.go`,
`pi.go`.

**Differences observed:** none — this pattern is *already* consolidated
cleanly. Flagging it here so the audit doesn't get re-run for this later:
handlers correctly go through `parseAuthScope` → `authSessionsForScope` →
per-session `EnsureValidTokenForUserCharacter`. The remaining boilerplate
(`GetForUser` + `EnsureValidTokenForUser` for single-character routes) is
only ~4 lines and doesn't need further extraction.

**Priority:** n/a — no action.

---

## Cluster 7: Table column preferences

**Instances:**
- `frontend/src/lib/tablePrefs.ts:8` — `normalizeColumnPrefs<T>()` — the
  shared helper for order/hidden/widths/pinned.
- Only 1 consumer in production: `ScanResultsTable.tsx`.

**Differences observed:** the helper is well-designed but under-used. Other
sortable/reorderable tables (`RegionalDayTraderTable`, `ContractResultsTable`,
`StationTrading` rows, `IndustryProfitableScannerPanel`, `TradeJournal`)
each roll their own sort/hide state without hitting the shared helper.

**Consolidation proposal:**
- Not urgent — every table's specific sort semantics differ enough that a
  premature abstraction across all of them would trade N tolerable local
  copies for a sprawling generic table. But adding a couple more consumers
  to `normalizeColumnPrefs` (specifically for column *visibility* +
  *ordering*, which is the least-differentiated part) is worthwhile.
- Risk: low.
- Priority: low.

---

## Cluster 8: `useIndustrySharedPrefs` pattern is worth copying, not extending

**Instances:**
- `frontend/src/lib/useIndustrySharedPrefs.ts` — module-level singleton +
  subscriber set for cross-mount sync of industry fee/system/decryptor
  prefs. About 165 lines including doc comments and a well-considered
  history section on why cross-window sync was removed.
- Similar shared-state needs exist elsewhere:
  `useTheme.ts`, `useIndustrySharedPrefs.ts`, and various ad-hoc
  `useState` + `localStorage` pairs inside `PlexTab.tsx`,
  `WatchlistTab.tsx`, `PIFactory.tsx`, `PriceAudit.tsx` that could
  benefit from the same pubsub-with-persistence pattern.

**Differences observed:** most tabs write their own `useEffect(() => {
localStorage.setItem(...) }, [state])` boilerplate. Same shape, no drift
observed, but the same disallow-cross-tab-sync bug that the industry hook
documents could recur in any of them.

**Consolidation proposal:** extract a `usePersistedSharedState<T>(key,
defaults)` helper mirroring the industry hook's shape. Not urgent — the
existing hook can be copied for the next tab that needs it, and if that
happens twice, extract then.

**Priority:** low.

---

## Cluster 9: Preset scaffolding

**Instances:**
- `frontend/src/lib/presets.ts` — BUILTIN_PRESETS for scanner tabs;
  STATION_BUILTIN_PRESETS for the station-trading tab; separate
  `loadCustomPresets` / `saveCustomPreset` / `deleteCustomPreset` /
  `exportPresets` / `importPresets` — all keyed under one localStorage
  entry.
- `frontend/src/components/PresetPicker.tsx` — the shared UI.

**Differences observed:** presets are well-centralized — there's one
storage key, one API. The only wart is that
`STATION_BUILTIN_PRESETS` sits in the same file with a completely
different param shape and cannot be applied by `getPresetApplyBase`
(returns `{}` for tab `"station"`). Station Trading presets are always
handled via a separate code path (`applyStationPreset` or similar in
`StationTrading.tsx`). Not enough duplication to lift.

**Priority:** low — no action.

---

## Cluster 10: Engine test-fixture SDE builders

**Instances:**
- `internal/engine/industry_test.go:1018` — `newTestIndustrySDE()` — one
  well-named helper.
- 9 test files (`contracts_test.go`, `industry_alignment_test.go`,
  `industry_depth_test.go`, `regional_day_trader_test.go`,
  `route_test.go`, `scanner_test.go`, `station_trading_scan_test.go`,
  `station_trading_share_test.go`, `market_restrictions_test.go`) each
  inline their own `&sde.Data{...}` literal with the same
  Jita 30000142 + Forge 10000002 boilerplate (75 occurrences of the ID
  pair total across engine).

**Differences observed:** most tests only need a slice of SDE data (a few
types, one system, one region). Small local literals are actually more
readable than a generic factory. `newTestIndustrySDE` is the exception —
it builds a specific blueprint tree for industry chain tests.

**Consolidation proposal:** minimal — add a `testutil.SmallSDE()` in
`internal/testutil/` (or `internal/engine/testfixtures.go` inside the
engine package) that returns the "Jita + Tritanium + Pyerite + one type
under test" base. Callers still layer their own additions. Only worth
doing if a third engine test starts needing invention-tree fixtures.
Otherwise the current inline literals are fine.

**Priority:** low.

---

## Cluster 11: Types drift (`lib/types.ts`)

**Instances:**
- `frontend/src/lib/types.ts` mirrors Go JSON tags; the CLAUDE.md
  convention says "when adding a backend field, mirror it here."
- One ad-hoc type still lives in `handleAuthStructures`
  (`internal/api/server.go:4382`) — the inline
  `stationInfo { ID, Name, SystemID, RegionID, IsStructure, TypeID }`
  struct is JSON-emitted but has no matching `StationInfo` in
  `types.ts`. Frontend
  (`frontend/src/lib/types.ts` `StationInfo`) has a compatible shape,
  which appears to work by accident of field-name alignment.

**Differences observed:** none of concern — CLAUDE.md's convention is
holding. The inline `stationInfo` mirroring `StationInfo` is a minor smell,
not real drift.

**Priority:** low.

---

## Not real duplication (audited and cleared)

- **`writeJSON` / `writeError`** (`server.go:1297`) — already consolidated,
  used 738 times. No competing local variants found.
- **`hostedQuotaFeatureForRequest`** — single canonical mapping in
  `hosted_access.go:695`, enforced by
  `TestHostedQuotaFeatureMappingClassifiesAllPostAPIRoutes`. No drift.
- **ESI client retries / rate-limit** — centralised inside
  `internal/esi/client.go`. Callers do not re-implement these.
- **`suggestedSalesTax` / `suggestedBrokerFee`** — single copy in
  `character_market_fees.go`. Formula appears exactly once.
- **Character-popup subtree formatters** — thread `formatIsk` down as a
  prop from CharacterPopup rather than each subtree redefining it. Good
  pattern; kept out of Cluster 1.
- **`parseAuthScope` / `authSessionsForScope`** — see Cluster 6.
