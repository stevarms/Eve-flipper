# Duplication and Consolidation Audit

Read-only investigation of `internal/` (Go backend) and `frontend/src/` (React/TS
SPA). Focus: load-bearing calculations, fetch patterns, and state shapes that
are re-implemented across files — especially where copies have already drifted.

## Executive summary

The five highest-value consolidation opportunities, ranked by drift/bug risk:

1. ~~**Frontend ISK formatters**~~ — **RESOLVED** (UI overhaul phase 0).
   Ten local copies now delegate to a parameterised `formatIsk` in
   `lib/format.ts`; the negative-value bug is fixed everywhere and the
   highest tier is unified at `T`. See Cluster 1.
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

Two more clusters were surfaced by the tab-promotion pass, which pulled nine
tools out of the character modal and put them next to their main-tab
counterparts — proximity made the overlaps obvious:

6. ~~**Two order tabs**~~ — **RESOLVED**. `CombinedOrdersTab` (757 lines,
   portrait-click only) is deleted; its history and undercut status moved into
   the main-tab order desk. See Cluster 14.
7. ~~**`trade_journal` vs character-modal `PnLTab`**~~ — **RESOLVED**. The two
   FIFO engines are now one matcher (`ComputeTradeJournal`) feeding one
   summarizer (`summarizeRealizedLedger`), and the two tabs are one tab with a
   Summary / Analytics switch. Pinned by an anti-drift test that fails if the
   engines can ever disagree again. See Cluster 15.

---

## Cluster 1: Frontend ISK formatting — RESOLVED

> **Status: fixed in the UI overhaul, phase 0.**
>
> `lib/format.ts` now exports `formatIsk(value, locale?, opts?)` and
> `formatIskSigned`, with `maxTier` / `space` / `decimals` / `signed` options.
> Every local copy was reduced to a thin wrapper holding only its presentation
> config — one algorithm, per-surface formatting, small diffs.
>
> What changed for users:
> - The negative-value bug is gone in ExecutionPlannerPopup, RouteBuilder,
>   RouteSafetyModal and StationTradingExecutionCalculator.
> - Highest tier unified at `T`, so CharacterPopup no longer renders "2000B"
>   for a figure the Trade Journal shows as "2T".
> - `RouteBuilder.formatISKFull` no longer hardcodes `en-US`.
> - Trailing zeros are dropped ("1.2 B" not "1.20B"), matching the canonical
>   `formatISK`. Intended convergence, but visible.
>
> Pinned by `frontend/src/lib/format.test.ts`.
>
> The audit below is retained as the record of what was wrong.

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

## Cluster 12: The spinner block — RESOLVED

Thirteen copies of the same markup across twelve files:

```tsx
<div className="flex items-center justify-center h-full text-eve-dim text-xs">
  <span className="inline-block w-4 h-4 border-2 border-eve-accent/40 border-t-eve-accent rounded-full animate-spin mr-2" />
  {t("loading")}...
</div>
```

`CombinedOrdersTab` (×2), `OptimizerTab`, `OverviewTab`, `PnLTab`,
`TradingEdgeTab`, `WalletDashboardTab`, `IndustrySection`, `MarketSection`,
`MembersSection`, `MiningSection`, `WalletsSection`, `CorpDashboardApp`.

(`CombinedOrdersTab` has since been deleted outright — see Cluster 14 — so
eleven of the thirteen sites remain, all on `LoadingBlock`.)

Two real defects the copies had accumulated:

- **Three had drifted to hardcoded English** — "Loading Trading Edge...",
  "Loading members...", "Loading journal..." — invisible to the locale-parity
  build gate because they were never keys at all.
- **`CorpDashboardApp.tsx:45` asked for `border-3`**, a width Tailwind does not
  ship (0/1/2/4/8) and which `tailwind.config.ts` does not extend. The corp
  dashboard's full-page loading state rendered with no ring: nothing turning.

All thirteen now go through `components/ui/LoadingBlock.tsx`, which also adds
the `role="status"` / `aria-live="polite"` that none of the copies had.

Left as genuinely different shapes, not copies: the spin-the-refresh-icon
buttons (`CharacterPopup`, `PIPlanetsTab`), the SVG spinners in `EmptyState`
and `SystemAutocomplete`, and the inverted `border-t-transparent` in-button
ring in `IndustryTab`.

**Priority:** resolved.

---

## Cluster 13: `MarketMakingTab` vs `PlexTab` — RESOLVED

502 lines of a second PLEX dashboard that no user has ever seen. `git log -S`
puts its birth in `67b844d` — the same commit that added the PLEX+ dashboard —
with its import already commented out at `App.tsx:22`. It was dead on arrival,
and it then drifted for the whole life of the repo.

Panel by panel it was a strict subset of `PlexTab`: global price card, spread
table, order-book depth, injection tiers, the same `getPLEXDashboard()` call,
the same `ArbitragePath` rows. Its 23 `mm*` locale keys were used nowhere else.

Deleted rather than wired up — but three of its ideas were real, and moved
into `PlexTab` instead of dying with it:

- **Market-making vocabulary for spread plays.** "Cost / Revenue" is the wrong
  frame for placing two orders; `SpreadRow` in `plex-tab/PlexMarketCards.tsx`
  now reads Buy Order / Sell Order / Raw Spread.
- **The market-making tips card**, now shown under the spread tab.
- **Three keys found a home fixing real gaps** — `mmMarketBuyCost`,
  `mmRequiresSP` and `mmAfterFees` replaced hardcoded English in
  `PlexArbitrageModal.tsx`. The other eight orphans were pruned from both
  locale files.

Two live bugs in `PlexTab` surfaced only because the comparison forced a close
read of it, and are fixed in the same change:

- **The spread table was one column out of register.** Its header had five
  `<th>` but it rendered rows with `ArbitrageRow`, which emits six `<td>` (the
  second being PLEX-needed, always 0 for a spread). Every spread number sat
  under the wrong label.
- **"NES Arbitrage" was a lie for one of its rows.** The filter was
  `type !== "spread"`, which swept in `market_process` — buy a Skill Extractor
  *off the market*, extract, sell the Injector: no New Eden Store purchase, no
  PLEX. The most accessible play on the screen was filed under the heading that
  says you must spend real money. The matrix is now three tabs (NES / Market /
  Spread) keyed off the engine's own path type, each with a one-line statement
  of its model and a viable-count summary.

**Priority:** resolved.

---

## Cluster 14: Two order tabs — RESOLVED

Two independent surfaces rendered the same character's market orders:
`components/Orders.tsx` (the main-tab order desk) and
`components/character-popup/CombinedOrdersTab.tsx` (757 lines, reachable only
by clicking the portrait). Neither was a strict subset of the other, which is
why both survived:

| | `Orders.tsx` | `CombinedOrdersTab` |
|---|---|---|
| Active orders | yes | yes |
| Order history | **no** | yes (`getOrderHistory`) |
| Undercut status | **no** | yes (`getUndercuts`) |
| Price ladder | no | yes (`book_levels`) |
| Decide-tier grid + drawer | yes | no — 11 flat columns |

The merge went in the direction of the *feature-poorer* file, because layout is
cheaper to port than data plumbing: `Orders.tsx` had already been through the
three-tier pass, so it gained history (`OrderHistoryPanel`) and undercut status,
and `CombinedOrdersTab` was deleted.

Column parity was checked field by field before deleting. Everything the modal
grid showed was already in the desk grid or its row drawer except one thing —
the **price ladder**, which needs `book_levels` from `getUndercuts`. That is now
`BookLadder` inside `orders/OrderRowDrawer.tsx`, fed by a single
`getUndercuts("all")` call fired lazily the first time a row is inspected and
cached by `order_id`. A failed depth call renders no ladder and leaves the rest
of the drawer — which comes from the desk payload — untouched.

**Priority:** resolved.

---

## Cluster 15: `trade_journal` vs character-modal `PnLTab` — RESOLVED

> **Status: fixed in the UI overhaul.** One matcher, one summarizer, one tab.
>
> - `ComputeTradeJournal` is the only matcher a user is shown. Its output is
>   projected into the analytics shape by `TradeJournalResult.ToPortfolioPnL`
>   (`internal/engine/portfolio_projection.go`), which filters by `LotSource`
>   before summarizing — so Trading / Manufacturing / Combined each get their
>   own Sharpe ratio, drawdown and profit factor rather than a share of a
>   combined figure.
> - Every derived statistic now comes from `summarizeRealizedLedger`
>   (`portfolio.go`), called by both engines. The daily series, per-item and
>   per-station breakdowns, Sharpe, drawdown, Calmar, profit factor and
>   expectancy are computed in exactly one place.
> - `ComputePortfolioPnLWithOptions` survives for three in-memory callers
>   (`eve_ledger.go`, `optimizer.go`, two `ComputePortfolioPnL` calls in
>   `server.go`) that score raw ESI transactions with no DB round-trip. It is
>   no longer a number the user is shown.
> - `PnLTab` is deleted. Its panels live in
>   `components/journal/JournalAnalyticsView.tsx`, reached from the Trade
>   Journal tab's Summary | Analytics switch, and the `pnl` main tab is gone
>   from `MAIN_TAB_IDS` — stored layouts naming it drop it silently via
>   `uniqueKnownTabs`.
> - New `GET /api/auth/journal/analytics` serves it. It is a separate route
>   from `/journal/summary` so the default Summary view does not pay for the
>   per-character ESI order fetch that slot efficiency needs.
>
> **Fees.** The claim below that Trade Journal used "the character's real fee
> profile" was wrong: `brokerFee` was hardcoded to 1.0 and `cfg.BrokerFeePercent`
> was never read, so every journal figure understated an untrained broker fee by
> two thirds. Fees now resolve config → skills (Accounting / Broker Relations,
> 30-minute cache) → default, with an explicit session override, and the tab
> states which of those it used. See `internal/api/fee_profile.go`.
>
> **Pinned by** `internal/engine/portfolio_projection_test.go`:
> `TestToPortfolioPnL_MatchesLegacyEngineWithoutJobs` asserts that given zero
> industry jobs the projection equals `ComputePortfolioPnLWithOptions` field for
> field. Writing it immediately caught a real divergence — the journal engine
> computed fees as `gross*(pct/100)` where the legacy engine used
> `gross*pct/100`, differing in the last float64 bits. That is precisely the
> silent drift this cluster was about, found at the smallest scale it can occur.
>
> The audit below is retained as the record of what was wrong.

Two accounting surfaces over the same ESI transaction history, built at
different times for different questions, now sitting two tabs apart in the same
Journal workspace — and, contrary to a first reading, running **two separate
FIFO implementations**:

| | Trade Journal (`trade_journal`) | P&L (`pnl`) |
|---|---|---|
| Component | `components/TradeJournal.tsx` | `components/character-popup/PnLTab.tsx` |
| API | `/api/journal/summary`, `/by-type`, `/lots` | `/api/portfolio/pnl` |
| Engine | `engine.ComputeTradeJournal` (`portfolio_manufacturing.go`) | `engine.ComputePortfolioPnLWithOptions` (`portfolio.go`) |
| Inputs | buys, sells **and industry jobs** | wallet transactions only |
| Cost basis of a built item | install cost + materials ÷ runs, matched as a lot | invisible — a manufactured sell has no basis |
| Scope | `WalletScopeFilter`: many characters + corp divisions pooled | one `characterScope` |
| Fees | the character's real fee profile | two UI sliders, defaulting 8% / 1% |
| FIFO ordering | three modes (strict date / trade first / manufacture first) | strict date, fixed |

So the two tabs can legitimately disagree, and the reason is not a bug in
either: Trade Journal knows what you built, P&L does not. On a manufacturing
character the gap is the whole industry side of the business. P&L's fee sliders
are also a what-if control, not a record of what you actually paid.

What P&L still has that Trade Journal does not: the drawdown chart mode, slot
efficiency, the per-station table, and the raw ledger. Those are real analyses
and none of them exist elsewhere — which is why this is a merge, not a
deletion.

**Already closed in this pass:** the duplicate open-positions table.
`PnLOpenPositionsTable` (`journal/PnLPrimitives.tsx`) answered *what did I pay*
with five ledger columns and no live price; the new Assets → Positions tab
answers *should I sell this today*. Keeping both meant the same question got two
different answers depending on which tab you opened, so the primitive was
deleted and `PnLTab` now links to Positions.

**Priority: high** — raised from medium once the two engines were traced. This
is not merely navigational duplication. Two independent FIFO implementations
over the same transactions is exactly the shape that drifts, and it drifts
silently: both tabs render a confident ISK figure and nothing tells the user
they were computed by different code with different inputs.

The merge — one Journal surface, `ComputeTradeJournal` as the single engine,
P&L's unique panels ported onto it — was deliberately **not** attempted in the
tab-promotion pass, because moving a tool's mount point and rewriting it in the
same diff makes a near-mechanical change unreviewable. It landed as its own
pass; see the status block above.

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
  prop rather than each subtree redefining it. Good pattern; kept out of
  Cluster 1. Since the tab promotion the props come from
  `character/CharacterScopeProvider.tsx` instead of `CharacterPopup`'s local
  state, so the modal and the nine promoted workspace tabs share one set of
  formatters and one `getCharacterInfo` fetch.
- **`parseAuthScope` / `authSessionsForScope`** — see Cluster 6.
