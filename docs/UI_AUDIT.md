# UI Consistency and EVE Terminology Audit

*EVE Flipper frontend audit — Aug 2026*
*Scope: `frontend/src/components/**` and `frontend/src/lib/locale/en.ts` (2,794 keys)*
*Analysis only. No code was modified. Line/file references are load-bearing throughout.*

---

## Executive summary — top 10 highest-impact fixes

Ranked by user-visible impact for a working EVE trader/industrialist.

| # | Finding | What it costs the user | Canonical fix |
|---|---|---|---|
| 1 | **The primary "pull fresh data" button is a different verb on every tab** (Scan / Refresh / Fetch prices / Find routes / Analyze / Load Region Data / Sync). The user called this one out directly. | Muscle memory breaks on every tab switch; onboarding friction. | Split by intent: **Scan** = active market fetch (Scan, Regional, Contracts, Station, Industry Scanner, PLEX, War Tracker, Market Making, Stockpile). **Analyze** stays for Industry (it's compute, not fetch). **Find routes** stays for Route Builder (search verb is EVE-native there). **Sync** stays for wallet/ESI-owned data (Trade Journal, ESI fees). |
| 2 | **Buy/Sell price columns are labelled "Best Ask (L1)" / "Best Bid (L1)"** — bid/ask is trading-culture jargon, EVE never uses it in-client. Also inverted-feeling: `colBuyPrice = "Best Ask"` because the buy-side price is the lowest sell order, but the label reads backwards. | Every scan result reads as generic trading software instead of an EVE tool. | `colBuyPrice: "Buy @ Sell Order"` and `colSellPrice: "Sell @ Buy Order"`, or simpler `colBuyPrice: "Lowest Sell (Buy From)"` / `colSellPrice: "Highest Buy (Sell To)"`. Remove **all** `Bid` / `Ask` from user-visible labels. |
| 3 | **`Sug. bid` / `Discount bid target` / `top-buy` in Station Trading** carry the same bid/ask problem into the DS column. `Suggested bid` doesn't correspond to any in-client term. | The one column that could speak in EVE language ("Suggested Buy Order Price") speaks in NYSE. | `colSuggestedBid → colSuggestedBuyPrice: "Suggested Buy"`. `discountBidTarget → discountBuyTarget: "Discount buy target %"`. `top-buy` in tooltips → "top buy order". |
| 4 | **"Broker %" / "Broker fee (%)" / "Broker fee %"** — same field, three visible formats across tabs. Same story for **Sales Tax**: `Tax %` / `Sales Tax (%)` / `Sales tax %` / `Sell Tax %`. `Buy Tax %` is factually wrong — EVE sales tax fires only on sell. | Users trust the app less when the same fee field looks different on every screen. | Standardize to `Broker fee %` and `Sales tax %` everywhere. Kill `paramsBuyTax` / `buySalesTax` labels — call them `Round-trip sell tax %` if the field genuinely models the eventual resale. |
| 5 | **Tab title conventions are mixed**: `Flipper (radius)` uses a parenthetical qualifier, `Regional Trade` and `Contract Arbitrage` are noun-phrases, `Market Making` is a gerund, `PI Factory` is an acronym+noun, `Trade Journal` and `Watchlist` are single nouns, `War Tracker` is a compound tool name, `PLEX+` uses a plus sign. | Nav bar reads as a grab-bag. | Standardize to `<Subject> <System>` noun-phrases: **Radius Trade**, **Regional Trade**, **Station Trade**, **Contract Trade**, **Route Builder**, **Industry**, **PI Factory**, **PLEX**, **Watchlist**, **Trade Journal**, **Price Audit**, **Market Making**, **War Tracker**, **History**. |
| 6 | **"multibuy" / "multi-buy" / "Multibuy"** — three spellings for what EVE's Marketplace menu labels **Multibuy** (one word, capital M). Copy-to-clipboard buttons and toasts alternate freely. | Confuses new users about where the pasted string is supposed to go in-client. | One spelling: **Multibuy**. Update `industryLedgerRecalcCopyMultibuy: "Copy to EVE multi-buy"` → `"Copy to EVE Multibuy"`. |
| 7 | **Column headers alternate `Qty` / `Quantity` / `Units` / `Amount` and `Vol` / `Volume` / `Daily Vol` / `Daily Volume`** across tables for the same measurement. | Cross-tab table reading breaks when Station Trading says one thing and Regional Trade says another. | `Qty` (short) in table headers where space is tight; `Quantity` in tooltips/aria-labels. `m³` for cargo volume; `Daily Vol` for market volume; never bare `Vol`. |
| 8 | **`ROI %` is the canonical winning label (used ~15 places), but a handful still say `Margin` / `Min Order Margin %` / `Max Margin (%)`.** | Two words for the same number. | Rename `minMargin → minROI`, `minOrderMargin → minOrderROI`, `maxContractMargin → maxContractROI`. Keep `margin` only in prose tooltips where it's a synonym you're explaining. |
| 9 | **`Delete` vs `Remove` vs `Clear` are used inconsistently** for the three distinct destructive actions (permanently destroy, extract-from-list, reset-to-default). Delete is used for row removal in some tables (`stockpileDeleteButton`) and permanent destruction in others (`industryProjectDeleteAction`). | Users can't tell which button will lose their work. | **Delete** = permanent destroy with confirm dialog. **Remove** = drop from a list; item still exists. **Clear** = wipe editable state / reset to default. Fix `stockpileDeleteButton` → `Delete stockpile` (has permanent semantics); `stockpileRowRemove` stays `Remove` (just drops a line). |
| 10 | **Cancel is overloaded** — same word for "close this dialog" (`dialogCancel`), "abort in-progress scan" (`industryScannerCancel`), "cancel this EVE market order" (`orderDeskActionCancel`), "give up on this batch" (`industryScannerAddToProjectCancel`). The order-cancel context especially matters because Cancel Order is an EVE in-client verb with real ISK consequences (broker fee lost). | Confusion is highest exactly where the stakes are highest. | Dialog cancel: **Cancel**. Abort scan: **Stop scan** (matches existing `stop` key). EVE order cancel: **Cancel order** (never bare Cancel). Batch abandon: **Discard batch**. |

---

## Part A — Consistency findings

### Finding A1 — "Scan / Refresh / Fetch prices / Find routes / Analyze / Load / Sync" all mean the same thing

**Instances:**

| Verb used | Where | Locale key | File:line |
|---|---|---|---|
| `Scan` | Flipper/Radius scanner | `scan` | `App.tsx:2317` |
| `Scan` | Station Trading | `scan` | `StationTrading.tsx:3010` |
| `Scan` | Regional Trade & Contracts | `scan` (same button) | `App.tsx:2317` |
| `Scan Blueprints` | Industry Profitable Blueprints Scanner | `industryScannerScanBtn` | `industry/IndustryProfitableScannerPanel.tsx:1359` |
| `Scan` | Stockpile Manager | `stockpileScanButton` | `industry/IndustryStockpilePanel.tsx:619` |
| `Refresh` | PLEX+ | `plexRefresh` | `PlexTab.tsx:219` |
| `Refresh` | Market Making | `plexRefresh` (reused) | `MarketMakingTab.tsx:79` |
| `Refresh Data` | War Tracker | `refresh` | `WarTracker.tsx:96` |
| `Refresh` | Alert History | `watchlistRefresh` | `AlertHistoryViewer.tsx:106` |
| `Refresh` | Character popup | `charRefresh` | `CharacterPopup.tsx:442` |
| `Refresh` | Scan History | `historyRefresh` | `ScanHistory.tsx:254` |
| `Refresh list` | Patrons list | `patronsRefresh` | `App.tsx:2738` |
| `Refresh` | Industry Jobs project header | `refresh` | `industry/IndustryJobsProjectHeader.tsx:75` |
| `Refresh prices` | Industry Scanner (post-scan requote) | `industryScannerRefreshPricesBtn` | `industry/IndustryProfitableScannerPanel.tsx:1371` |
| `Fetch prices` | Price Audit | `priceAuditFetchBtn` | `PriceAudit.tsx:985` |
| `Fetch prices` | PI Factory | `piFactoryFetchBtn` | `PIFactory.tsx:536` |
| `Find routes` | Route Builder | `routeFind` | `RouteBuilder.tsx:650` |
| `Analyze` | Industry Analysis | `industryAnalyze` | `IndustryTab.tsx:3288` |
| `Load Region Data` | War Tracker (initial pull CTA) | `loadRegionData` | `WarTracker.tsx:228` |
| `Sync` | Trade Journal | `journalSyncBtn` | `TradeJournal.tsx:473` |
| `Sync ESI skills` | PI Factory (fee import) | `esiFeeSync` | `PIFactory.tsx:482` |
| `Sync Owned Blueprints` | Industry Materials | `industryLedgerMaterialSyncBlueprints` | `industry/IndustryMaterialDiffPanel.tsx:285` |
| `Import fees from ESI` | Industry Scanner (fee import) | `industryScannerImportFeesBtn` | `en.ts:776` |
| `Load Snapshot -> Builder` | Industry Ledger snapshot load | `industryLedgerLoadSnapshotToBuilder` | `en.ts:517` |

**Analysis:** every one of these performs an active fetch of market/ESI/DB data, then re-renders. There is no consistent axis distinguishing them.

**Recommended canonical pattern (four verbs, one axis: what is being pulled):**

- **Scan** — active *market* data pull (ESI market endpoints). Owns: Radius/Flipper, Regional, Contracts, Station Trading, Industry Scanner, Stockpile, **PLEX+**, **Market Making**, **War Tracker**, **Price Audit**, **PI Factory**. Rename `plexRefresh → plexScan`, `priceAuditFetchBtn → priceAuditScanBtn`, `piFactoryFetchBtn → piFactoryScanBtn`, `refresh → scan` in War Tracker.
- **Find** — search operation with parameters (Route Builder). Keep `routeFind: "Find routes"`.
- **Analyze** — compute-only operation on already-fetched inputs (Industry single-item analysis, backtests). Keep `industryAnalyze`.
- **Sync** — reconcile a *character-owned* dataset from ESI (wallet, blueprints, skills, jobs). Keep `journalSyncBtn`, `esiFeeSync`, `industryLedgerMaterialSyncBlueprints`; rename `industryScannerImportFeesBtn: "Import fees from ESI"` → `"Sync ESI fees"` for consistency.
- **Refresh** — reserved for read-only cache re-reads of *already-scanned* state (Scan History, Alert History, Character orders that were just displayed, Patrons feed). These are legitimately different from a full market re-scan and should stay `Refresh`.

Rationale: EVE's own market window uses **Reload** for a passive re-poll, but this app's scan is a first-class action with parameters — Scan is the honest verb. Fetch/Load/Refresh-for-market-data all bleed into each other and should collapse.

**Priority:** critical (user-called-out)

---

### Finding A2 — "Best Ask / Best Bid" in scan tables

**Instances:**

| Label | Where | Key | File:line |
|---|---|---|---|
| `Best Ask (L1)` | Radius/Regional scan buy price col | `colBuyPrice` | `en.ts:271`, used in `ScanResultsTable.tsx:151` |
| `Best Bid (L1)` | Radius/Regional scan sell price col | `colSellPrice` | `en.ts:276`, used in `ScanResultsTable.tsx:182` |
| `Buy` | Station Trading buy price col | `colStationBuyPrice` | `en.ts:292`, `StationTrading.tsx:175` |
| `Sell` | Station Trading sell price col | `colStationSellPrice` | `en.ts:293`, `StationTrading.tsx:181` |
| `Sug. bid` | Station Trading suggested-price col | `colSuggestedBid` | `en.ts:1758`, `StationTrading.tsx:163` |
| `Best Buy` / `Best Sell` | PLEX+ hub prices | `plexBestBuy` / `plexBestSell` | `en.ts:2263-2264` |
| `Best Bid` / `Best Ask` | Market Making | `mmBestBid` / `mmBestAsk` | `en.ts:2356-2357` |

Three vocabularies for one concept: `Ask/Bid` (ScanResultsTable, Market Making, Suggested Bid), `Buy/Sell` (Station Trading, PLEX+), and Station Trading's own `Suggested bid` referring to a *buy-order-price-recommendation*.

**Analysis:** EVE Online in-client uses **only** *Buy Order* / *Sell Order*. The word "bid" appears nowhere in the market UI. Even for a professional trader, Ask/Bid on EVE data reads as if the tool wasn't written by an EVE player.

Additional gotcha: `colBuyPrice: "Best Ask"` is technically correct (the buy-side price is the lowest sell order = ask), but it reads inverted at first glance. The Station Trading columns are less bad because the Station Trader is placing *their own* buy and sell orders, so labelling those slots by their **own action** (`Buy`, `Sell`) reads correctly.

**Recommended canonical pattern:**
- Scan tables (`ScanResultsTable`, `ContractResultsTable`, `RegionalDayTraderTable`): rename `colBuyPrice: "Sell Order Price"` (with helper tooltip "lowest sell order in the region — the price you pay to acquire") and `colSellPrice: "Buy Order Price"` (helper: "highest buy order at destination — the price you receive if you dump instantly"). If space is critical, use **`Buy @`** / **`Sell @`** as short headers with the order-book side in the tooltip.
- Station Trading: keep `Buy` / `Sell` (they describe *your* action). Rename `Sug. bid → Suggested Buy`, `discountBidTarget → discountBuyTarget`.
- Market Making: rename `Best Bid → Highest Buy Order`, `Best Ask → Lowest Sell Order` (or compact `Best Buy` / `Best Sell` to match PLEX+).
- PLEX+ (`Best Buy` / `Best Sell`) is closest to EVE-canonical — keep as-is.

**Priority:** critical

---

### Finding A3 — Column headers for the same concept differ per table

**Instances:** cross-table alignment for identical concepts.

| Concept | Values in the wild | Locale keys |
|---|---|---|
| Item quantity in a row | `Qty` (batch builder, character orders, contracts), `Quantity` (corp mining, contract details), `Units` (industry scanner), `Amount` (nowhere in locale but hardcoded in a few places) | `colQty`, `batchBuilderColQty`, `charQty`, `corpQuantity`, `industryScannerRowUnits`, `industryUnits` |
| Cargo volume | `Volume m³` (contracts), `Volume` (character orders / corp market), `Volume (m3)` (batch builder), `m³` (many places) | `colVolume`, `batchBuilderColVolume`, `charVolume`, `corpVolume` |
| Market daily volume | `Daily Vol` (radius scan), `Daily Volume` (watchlist), `Volume (24h)` (PLEX) | `colDailyVolume`, `watchlistMetricDailyVolume`, `plexVolume24h` |
| Return metric | `ROI %` (~15 keys), `Margin` (`industryMargin`), `Min Order Margin %`, `Max Margin (%)`, `Return` (in copy) | `colMargin`, `industryMargin`, `minMargin`, `minOrderMargin`, `maxContractMargin`, `optReasonNegativeReturns` |
| Station | `Station` (scan/contract), `Location` (character transactions — `charLocation: "Station"` — same word, different key), `Loc` (planner blueprints col) | `colStation`, `colStationName`, `charLocation`, `industryPlannerColBpLocation` |
| System security | `Security` (params), `Sec` (column), `Sec status` (nowhere) | `paramsSecurity`, `colSecurity`, `routeSecurity` |
| Region | `Region` mostly, but `Regions` in cache tooltip, `Target Region` in Regional Trade, `Source Region(s)` with parens | `region`, `sourceRegions`, `targetRegion`, `cacheTooltipRegions` |
| Fill quantity | `Fillable Qty`, `Can Fill`, `Book Depth Qty`, `L1 Ask Qty`, `L1 Bid Qty` — five different concepts in adjacent columns | `colFilledQty`, `colCanFill`, `colAcceptQty`, `colBestAskQty`, `colBestBidQty` |

**Analysis:** every one is defensible in isolation, but the accumulation is why a working trader has to re-parse every table.

**Recommended canonical pattern:**
- `Qty` in table headers (space), `Quantity` in tooltips and aria labels. Retire `Units` / `Amount` as column headers.
- `m³` bare for cargo volume in table headers; expand to `Cargo (m³)` when unambiguous is needed. Never `m3`.
- `Daily Vol` for market daily volume; expand in tooltips.
- `ROI %` everywhere for the return-percent metric. Move `Margin` into tooltip prose only.
- `Station` for station-name columns; `Location` reserved for the more general "where did this happen" case (Journal, Corp Market where structures include Upwell IDs).
- `Sec` in narrow columns, `Security` in labels, EVE's own in-client wording is `Sec Status` — consider `Sec Status` for full labels.

**Priority:** high

---

### Finding A4 — Empty states: a shared component exists but is bypassed in half the tabs

**`EmptyState.tsx`** (frontend/src/components/EmptyState.tsx) is a proper i18n-aware component with seven canonical reasons (`no_scan_yet`, `no_results`, `esi_offline`, `filters_too_strict`, `no_stations`, `no_item_selected`, `loading`) and an optional wiki link footer.

**Uses it** (`Grep EmptyState`): `industry/IndustryProfitableScannerPanel.tsx`, `IndustryTab.tsx`, `StationTrading.tsx`, `ScanResultsTable.tsx`, `PriceAudit.tsx`, `RegionalDayTraderTable.tsx`, `PIFactory.tsx`, `ContractResultsTable.tsx`. Eight components.

**Doesn't use it** — has its own inline empty rendering:
- `WatchlistTab.tsx` — `watchlistEmpty: "Watchlist is empty"` rendered inline with different visual weight.
- `WarTracker.tsx` — hardcoded `noDataYet: "No data yet"` inline.
- `character-popup/CombinedOrdersTab.tsx` and other character tabs — `charNoOrders: "No active orders"` inline.
- `AlertHistoryViewer.tsx` — `watchlistNoAlertsYet: "No alerts sent yet"` inline.
- `PlexTab.tsx` — `plexEmpty: "Click Refresh to load PLEX market data"` inline.
- `character-popup/PnLTab.tsx` — `pnlNoData: "No transaction data for this period."` inline.
- `corp-dashboard/shared.tsx` — three separate `<div>No data</div>` hardcoded (lines 199, 244, 319).
- `TradeExecutionAutopilotPopup.tsx`, `BacktestPopup.tsx` — multiple hardcoded "No data" divs.
- `PaperTradeJournalPopup.tsx` — hardcoded `"Loading..." : "Refresh"` at line 681.
- `PIFactory.tsx` — has both `EmptyState` and its own `piFactoryEmptyPortfolio`, `piFactoryEmptyState` — inconsistent within one file.

**Recommended canonical pattern:** every empty state that isn't context-specific should route through `EmptyState`. Add three new reasons: `no_wallet_data`, `no_orders`, `no_history`. Convert the character-popup tabs and corp-dashboard to use it. Convert the hardcoded "No data" divs in `shared.tsx` to `<EmptyState reason="loading" />` or a new `no_data` reason.

**Priority:** high

---

### Finding A5 — Loading indicators: three shapes for "in progress"

**Instances:**
- Trailing ellipsis with capitalized verb: `Refreshing...` (`refreshing`, en.ts:364), `Syncing...` (`journalSyncing`), `Loading...` (`plexLoading`, `emptyTitle_loading`), `Scanning blueprints...` (`industryScannerScanning`).
- Trailing UTF-8 ellipsis (`…`) with lowercase: `Importing…` (`stockpileImporting`), `Fetching…` (`priceAuditFetching`, `piFactoryFetching`), `Rebalancing…` (`industryLedgerMaterialRebalancing`), `Loading supporters...` (three-dot ASCII in the same file).
- Some components show a spinner *plus* label (`EmptyState.tsx` loading reason); others just replace the button text.
- NDJSON scanners in `ScanResultsTable`, `industry/IndustryProfitableScannerPanel`, `RegionalDayTraderTable` show a progress bar in the header; PI Factory (`PIFactory.tsx:536`) just swaps button text; Trade Journal shows a full-tab spinner.

**Recommended canonical pattern:** **all** in-progress strings use the same shape:
- Present-continuous verb + UTF-8 ellipsis `…` (`Scanning…`, `Loading…`, `Syncing…`). Never mix `...` and `…`.
- Long-running actions (NDJSON scans, industry analysis, wallet sync) always render a progress bar in the same visual slot (below the CTA button) with `{n}/{total} items` or `{pct}%` when the endpoint reports it.
- Button text becomes `Stop <action>` (see finding #10) while running.

**Priority:** medium

---

### Finding A6 — Modal / dialog footer patterns are unaligned

**Popups audited:** `ConfirmDialog`, `Modal`, `BacktestPopup`, `CharacterPopup`, `ContractDetailsPopup`, `ExecutionPlannerPopup`, `PaperTradeJournalPopup`, `SecurityVaultModal`, `RouteSafetyModal`, `BatchBuilderPopup`, `ItemIntelligenceModal`, `TradeExecutionAutopilotPopup`, `ExecutionRevalidationReportModal`, `plex-tab/PlexArbitrageModal`, `industry/IndustryRecalcRemainingModal`.

**Divergences:**
- Footer buttons appear left-aligned (`ContractDetailsPopup`), right-aligned (`ConfirmDialog`), or scattered inline (`PaperTradeJournalPopup` — `Save`, `Apply live`, `Cancel`, `Delete` all mixed in the row).
- Close-affordance is a top-right X in some (`StationAIAssistant.tsx:1741`, `ScanResultsTable.tsx:3533`), a bottom-right `Close` button in others (`ContractResultsTable.tsx:1304`), and *both* in a few.
- `PaperTradeJournalPopup` uses "Cancel" to mean "mark this paper trade cancelled" (line 933) *in the same footer* as a Delete button — the same word does opposing UI things.

**Recommended canonical pattern:**
- Footer: right-aligned. Primary action rightmost. Secondary (Cancel/Close) to its left. Destructive (Delete) far-left with a red border.
- Header X always present; bottom Close button only for content-heavy modals where the header X scrolls off (Backtest, Item Intelligence).
- Never use "Cancel" for anything but "close this dialog without saving." For "cancel this paper trade," use `Mark cancelled`.

**Priority:** high

---

### Finding A7 — Filter / search inputs are inconsistent

**Instances:**
- `filterPlaceholder: "Filter..."` (`en.ts:1254`) — used in scan tables.
- `industryScannerSearchPlaceholder: "Filter by name..."` — long form.
- `stockpilePastePlaceholder`, `priceAuditPasteLabel: "Paste items"` — paste-based input.
- `industryLedgerMaterialSearchPlaceholder: "search material / type id"` — lowercase, no ellipsis.
- `watchlistSearch: "Search..."`, `charSearchPlaceholder: "Search by name..."`, `corpSearch: "Search..."`.
- `hiddenSearchItemOrStation: "Search item or station"` — no ellipsis.
- Regex filtering support: only in ScanResultsTable's per-column filter dropdown. Every other search is a substring match.

**Recommended canonical pattern:**
- Placeholder shape: `Search <noun>…` (capitalized, UTF-8 ellipsis).
- Whether regex is supported should be indicated with a `.*` badge on the input, applied consistently across every filter box.
- Item-name filters should behave identically across tables — currently ScanResultsTable and StationTrading differ subtly on case-sensitivity.

**Priority:** medium

---

### Finding A8 — Preset panels: three shapes for the same job

**Instances:**
- `ParametersPanel` (used by Flipper, Regional) — presets bar at top: `presetLabel`, `presetSave`, `presetDelete`, `presetImport`, `presetExport`.
- `ContractParametersPanel` — separate but similar preset row.
- `TabSettingsPanel` — different UX pattern for per-tab settings.
- `PresetPicker` — a fourth abstraction.
- Route Builder uses its own preset naming (`presetRouteHighsec: "Highsec Only"`, `presetRouteAllSpace: "All Space"`).
- Station Trading presets use their own set (`presetStConservative: "Conservative"`, `presetStNormal: "Normal"`, `presetStAggressive: "Aggressive"`).
- Contract presets use another set (`presetContractSafe: "Safe"`, `presetContractNormal: "Normal"`, `presetContractRisky: "Risky"`).
- Regional presets use yet another (`presetRegionSafe: "Beginner Safe"`, `presetRegionNormal: "Beginner Balanced"`, `presetRegionEGLike: "EG Like"`, etc.).

**Recommended canonical pattern:** consolidate on `PresetPicker` as the one preset control, and standardize the built-in tier names: `Conservative` / `Balanced` / `Aggressive` (as used in Industry Ledger strategy already at `industryLedgerStrategyConservative` etc.). Retire `Beginner Safe` / `EG Like` / `Safe` — pick one vocabulary.

**Priority:** medium

---

### Finding A9 — Status badges / pill styles

**Components:** `ProfitPill`, `RouteSafetyBadge`, `AchievementBadge`, various inline chips (`industryScannerIncludeCorpChip`, `industryScannerTypeChipShips`, etc.), the `corpDemoMode: "DEMO"` badge.

**Divergences:**
- Rounded pills for ProfitPill (rounded-full), rounded-sm rectangles for scanner chips (`industryScannerTypeChip*`), ALL-CAPS for demo/live badges (`DEMO`, `LIVE`), Title Case for status labels (`Active War`, `Elevated`), lowercase for order-desk actions (`Hold`, `Reprice`, `Cancel`).
- Route safety uses a colored dot; ScanResults uses colored border+text; PLEX uses colored bg+text.

**Recommended canonical pattern:** three shapes only —
- **Pill** (rounded-full) for numeric key metrics with color-coded semantics (ProfitPill).
- **Chip** (rounded-sm) for filter toggles and inclusion tags — never for status.
- **Badge** (rounded-sm, ALL-CAPS 10px, single-word) for status/state (`ACTIVE`, `IDLE`, `DEMO`, `LIVE`).

**Priority:** low

---

### Finding A10 — Toast / notification / error surfaces

**Instances:** `Toast.tsx` (canonical), `ErrorBoundary.tsx` (crash catch), inline `throw` prompts, `industryLedgerJobUpdated: "Job #{id} -> {status}"` toasts (uses `->` ASCII arrow instead of `→`), `historyResultsLoaded: "Results loaded to tab"` (Title Case), `industryLedgerBuilderSeeded: "Visual plan builder seeded from current analysis"` (sentence case). Mix of `->` and `→` throughout — `industryLedgerAutoSplitScheduler` and `industryLedgerLoadSnapshotToBuilder: "Load Snapshot -> Builder"` use ASCII, `execPlanSummary: "... → {result}"` uses UTF-8.

**Recommended canonical pattern:**
- Toast text: sentence case, ends without period.
- Arrows: `→` (U+2192) everywhere for state transitions; `->` only in code identifiers.
- Duration: 2s (success), 4s (error) — verify `addToast` calls follow.

**Priority:** low

---

### Finding A11 — Keyboard shortcuts

**Source of truth:** `KeyboardShortcutsHelp.tsx` + `CommandPalette.tsx`, locale keys `shortcut*` and `cmd*` (en.ts:2757-2793).

**Divergence:** `cmdSwitchToRadius: "Switch to Radius tab"` and `shortcutTabRadius: "Switch to Radius tab"` are duplicates — same text, two keys, two locations. Same for Region, Contracts, Station, Route.

**Missing shortcuts:** `PLEX+`, `PI Factory`, `Trade Journal`, `Watchlist`, `Price Audit`, `Market Making`, `War Tracker`, `History` tabs — several are reachable only by mouse. `cmdOpenWatchlist` and `cmdOpenHistory` exist as command-palette entries but not as top-level keybinds.

**Recommended canonical pattern:**
- Merge `cmd*` and `shortcut*` sets — one string per action, both surfaces consume it.
- Every tab in the top nav has a numeric shortcut (1-9, 0). Locale keys become `shortcutTab<Name>`.

**Priority:** medium

---

### Finding A12 — Typography and spacing

**Divergences observed:**
- Cockpit config uses `text-[10px] uppercase tracking-widest` for section captions.
- Character popup uses `text-[10px] uppercase tracking-[0.18em]` — different arbitrary tracking.
- HostedAccessTab uses `text-[10px] uppercase tracking-[0.2em]` — third variant.
- Table headers alternate `text-xs font-medium` and `text-xs font-semibold`.
- Section titles vary: `text-sm font-semibold`, `text-base font-semibold`, `text-sm font-bold`.

**Recommended canonical pattern:** define Tailwind classes for the four common shapes in `tailwind.config.ts` and use them everywhere:
- `text-caption` = `text-[10px] uppercase tracking-widest text-eve-dim`
- `text-section-title` = `text-sm font-semibold text-eve-text`
- `text-table-header` = `text-xs font-medium uppercase text-eve-dim`
- `text-table-cell` = `text-xs text-eve-text`

**Priority:** low (visual polish)

---

## Part B — EVE terminology alignment

### Finding B1 — "Bid / Ask" (already covered in A2, restated for terminology axis)

Bid/ask never appears in EVE Online in-client. It's Wall-Street language leaking into a game UI. Also see `top-buy` (`en.ts:1806`), `top buy` (`en.ts:1796`), `Sug. bid`, `discount bid target` — every one of these should be `top buy order`, `Suggested buy price`, `discount buy target`.

**Priority:** critical.

---

### Finding B2 — "Broker fee" is right; sales tax naming is not

**Correct:** `salesTax`, `industrySalesTaxHint`, `piFactorySalesTax`, `esiFeeSyncSuccess`, `plexSpfarmSalesTaxWithPct` all say "sales tax."

**Wrong:**
- `paramsTax: "Tax %"` — ambiguous (facility? POCO? sales?).
- `paramsBuyTax: "Buy Tax %"` and `buySalesTax: "Buy tax (%)"` — EVE has no "buy tax." Sales tax fires only when a sale completes; buying pays broker fee (if you place a buy order) but no sales tax. If this is modeling round-trip sell tax on eventual resale, say so.
- `pnlSalesTax: "Tax %"` — bare "Tax" again.

**Recommended fix:**
- Everywhere the field means EVE's sales tax → `Sales tax %` (with space).
- Where the field means the sales tax on the *eventual* resale of a bought item → rename to `Resale tax %` or drop the buy-side field entirely if broker-fee-only.
- Never bare `Tax %`.

**Priority:** high.

---

### Finding B3 — "Citadel" used generically

The only place it appears is `noNpcStationsToggleHint: "…accessible Citadels, Fortizars and Keepstars."` — enumerating Upwell structure types by hull. That's **correct** usage (Citadel *is* the class name for the small-hull tier: Astrahus/Fortizar/Keepstar are `citadel` service module targets historically). But re-verify against current CCP wording:
- Post-Upwell, CCP officially uses **"Upwell structure"** as the umbrella term and specific hull names underneath. "Player structure" (the app's current `includeStructures`) is fine as a search-scope label.
- `industryScannerTypeChipStructures: "Structures"` — as an item-category filter (Upwell structure blueprints), this is unambiguous — leave it.

**Recommended fix:** current usage is defensible. Update `noNpcStationsToggleHint` to lead with "Upwell structures" and mention hull names as examples: `"…Turn on Include player structures to search accessible Upwell structures (Astrahus, Fortizar, Keepstar, engineering complexes)."` This is more inclusive of engineering complexes (Raitaru/Azbel/Sotiyo) which the industry code already models but the marketing string ignores.

**Priority:** low.

---

### Finding B4 — "Manufacturing" is used correctly; "Build" is used inconsistently

**Correct:** `industryAnalyzeModeHint` distinguishes manufacturing / reaction / invention properly; `industryMfgTime: "Manufacturing Time"`, `journalKpiManufacturingPL: "Manufacturing P&L"`, `pnlSharpeRatio` context uses "manufacturing."

**Colloquial "Build":** `industryBuildCost`, `industryBuildMode`, `industryBuildModeAuto: "Build when profitable"`, `piFactoryTotalBuild: "Total build/day"`, `industryScannerBuildSystemLabel: "Build system"`. This is defensible — "build" is EVE-community colloquial for manufacturing and no working industrialist will misread it — but be aware there's a mix.

**Not seen (good):** no "crafting" or "production" as a verb. `industrySettings: "Production Settings"` and `industrySettingsHint: "Production chain analysis"` use "production" as a noun for the manufacturing chain — acceptable but "Manufacturing chain" would be more precise.

**Recommended fix:** rename `industrySettings: "Production Settings"` → `"Manufacturing Settings"` for header-level correctness; leave "build" in verb slots.

**Priority:** low.

---

### Finding B5 — Blueprint / BPO / BPC usage

**Correct:** `industryBPO: "BPO (original)"`, `industryBPC: "BPC (copy)"`, `blueprintCopy: "Blueprint Copy"`, `blueprint: "Blueprint"`. All match EVE canon.

**Issue:** the two conventions collide inside the same tab — the Industry planner sometimes shows the acronym-only style (column `BPO` in `industryPlannerColBpBPO`) and sometimes the expanded form (`industryScannerBPFilterBoth: "BPO + BPC"`, `industryScannerScanModeT1: "T1 mfg"`, `industryScannerScanModeT2: "T2 invention"`). Two adjacent buttons with different casing conventions.

**Recommended fix:** column headers use acronym (`BPO`, `BPC`, `ME`, `TE`) — those are canonical in-game shorthand. Full-sentence prose uses the expanded form. Filter/scan-mode chips should be single-token — reformat `industryScannerScanModeT1: "T1 mfg"` → `"T1"`, and rely on tooltip for the activity.

**Priority:** low.

---

### Finding B6 — ME / TE / SP / ISK / LP casing

All canonical uses I could find are uppercase (`ME`, `TE`, `SP`, `ISK`, `LP`) — good. One suspicious lowercase in `industryPlannerColTaskIskPerRun: "isk/run"` (`en.ts:843`) and `industryPlannerSectionTasksCols: "…sec/run · isk/run…"` (`en.ts:685`). These display strings should be `ISK/run` — three-letter uppercase everywhere.

Column-header abbreviations like `industryPlannerColTaskSecPerRun: "sec/run"` mean "seconds per run" (time) not "security" — the potential confusion with the security-status `Sec` column suggests renaming to `s/run` or `Time/run`.

**Priority:** medium.

---

### Finding B7 — Pilot vs Character

EVE uses both "pilot" and "character" in-client depending on context: character selection screen, character sheet, but pilot licensing (Omega), pilot chat, etc. The app uses:
- `character` primarily (`charSelectCharacter`, `charAddCharacter`, `charViewInfo: "Character info"`) — good.
- `Pilot` in one achievement (`achievementPilotRecord: "Pilot record"`) — fine.
- `industryJobsGuideTitle: "Pilot Flow (for new industrialists)"` — awkward; nobody calls it "pilot flow." Rename to `"Getting Started"` or `"Onboarding Flow"`.

**Priority:** low.

---

### Finding B8 — Trade Hub

Correct usage: `sourceRegionsMajorHubs: "Major Trade Hub Regions"`, `tradeHubs: "Trade hubs"`, `plexHubPrices: "Hub Prices"`. The word "hub" alone in `priceAuditHubs: "Hubs to consider"`, `priceAuditHubCaps: "Volume caps (m³)"` etc. is fine in context — these all cluster in Price Audit which is entirely about hub allocation.

`industryScannerPricingHubsLabel: "Quick hubs"` is compact but semantically clear. Good.

`corpTopContributors: "Top Contributors"` — okay for corp view.

**Recommended fix:** none needed. "Hub" alone is fine when the context (Price Audit, PI Factory shopping list) makes it obviously about trade hubs.

**Priority:** none.

---

### Finding B9 — Undercut

`undercutBtn: "Undercut"`, `undercutSuggested: "Suggested price"` — undercut is community jargon, and in-client EVE uses "Modify Order" for the underlying action. This one is defensible: "Undercut" is universally understood by traders, "Modify" would be less clear.

**Recommended fix:** keep "Undercut" as the user-facing verb; make sure the tooltip mentions the in-game action is Right-click order → Modify.

**Priority:** low.

---

### Finding B10 — PLEX capitalization

All references are `PLEX` uppercase — great. One caveat: `piFactoryLaunchpad: "Launchpad m³"` uses "Launchpad" — EVE UI writes it as "Launchpad" (one word, no cap after "Launch"), so this is correct.

**Recommended fix:** none.

**Priority:** none.

---

### Finding B11 — Security status

`routeSecurityHighsec: "Highsec only (≥0.45)"` — highsec is technically ≥0.5 in EVE (the 0.45 threshold in the code is a fudge for rounding, but the label saying `Highsec ≥0.45` is misleading — EVE never lists 0.45 as high-sec). Also `structureRigSec_hisec: "hisec ×1.0"` uses `hisec` (one variant), `structureRigSec_lowsec: "lowsec ×1.9"` uses `lowsec`, `Highsec` (title case) elsewhere.

EVE's own wording: **High-Sec**, **Low-Sec**, **Null-Sec** (with hyphens and capitals) or the compact **hi-sec**/**lo-sec**/**null-sec** in chat. Never `hisec` in-client.

**Recommended fix:**
- Standardize on `High-Sec` / `Low-Sec` / `Null-Sec` in labels; `hisec`/`lowsec`/`nullsec` only in tightly-space-constrained badges and matched consistently.
- Fix `routeSecurityHighsec: "Highsec only (≥0.45)"` — either raise the label threshold to `≥0.5` (and the code) or rename to `Highsec+ (≥0.45)` with a footnote.

**Priority:** medium.

---

### Finding B12 — Multibuy spelling

`multibuy` (13 places), `multi-buy` (2 places at `industryLedgerRecalcCopyMultibuy` and its hint), `Multibuy` (title case at `multibuyPillLabel: "Multibuy ({count})"`). EVE's in-client menu is **Multibuy** (title case, one word). Standardize.

**Priority:** medium.

---

### Finding B13 — Standing, Corp, Alliance

- `corp` used throughout (`corpDashboard`, `corpDirector`, `corpMembers`). Good.
- `Corporation` in `stockpileSourceCorporation` — expanded form. Fine.
- No occurrences of "Reputation" — good.
- `Faction standing` mentioned correctly in `industryBrokerFeeHint`.

**Recommended fix:** none needed.

**Priority:** none.

---

### Finding B14 — Warp / Jump / Dock

- `Jumps` / `jumps` used consistently for gate transitions. Good.
- `routeDockMinutes: "Dock min"` — abbreviated docking-time; okay in space-constrained column but expand to `Dock (min)` for clarity.
- `openMarket: "Open Market Window"` — matches EVE menu exactly.
- `setDestination: "Set Destination"` — matches EVE menu.

**Recommended fix:** none major.

**Priority:** none.

---

### Finding B15 — Wallet / Journal / Transactions

Correct: `Wallet` (container), `Transactions` (log), `Journal` (fee/ISK-flow log). Matches EVE's Wallet window tabs.

But: `walletArchiveArchivedTx: "Archived tx"` uses `tx` abbreviation, `walletArchiveArchivedJournal: "Archived journal"` uses full word. Standardize to `Archived transactions` / `Archived journal`.

**Priority:** low.

---

### Finding B16 — Contract types

Not much surface — mostly contract *scanning* rather than contract *creation*. What appears is fine: `Contract`, `Contract ID`, `Pickup Point`, `contract price`.

`contractLiquidationPoint: "Instant Liquidation Point"` — this is app-specific jargon, not an EVE term. Acceptable since it's a computed field, but consider `Estimated Liquidation Hub` for clarity.

**Priority:** low.

---

## Glossary (recommended canonical terms)

| Term | Canonical form | Rationale |
|---|---|---|
| Scan | `Scan` | Active market-data pull. Matches EVE community usage for tool-driven market fetches. |
| Sync | `Sync` | ESI-owned dataset reconciliation (wallet, blueprints, skills). |
| Analyze | `Analyze` | Compute-only over already-fetched data. |
| Refresh | `Refresh` | Re-read cached/displayed state without re-hitting ESI (Scan History, Alerts). |
| Buy order | `Buy Order` | EVE's canonical term for the order type on the *buy* side of the book. |
| Sell order | `Sell Order` | EVE's canonical term for the *sell* side of the book. |
| Sell Order Price | `Sell Order Price` (or `Buy @` in narrow headers) | The price on the lowest sell order — what you pay to instant-buy. Never `Ask`. |
| Buy Order Price | `Buy Order Price` (or `Sell @`) | The price on the highest buy order — what you receive if you instant-sell. Never `Bid`. |
| ROI % | `ROI %` | Winning label already; use everywhere. Retire `Margin` from headers. |
| Broker fee | `Broker fee %` | EVE canonical. |
| Sales tax | `Sales tax %` | EVE canonical; only applies on sell. |
| Facility tax | `Facility tax %` | EVE canonical for job-installation surcharge. |
| ME / TE | `ME` / `TE` | In-client abbreviations for Material Efficiency / Time Efficiency. |
| BPO / BPC | `BPO` / `BPC` | Community-canonical (Blueprint Original / Copy). |
| ISK | `ISK` (always uppercase, three letters) | EVE canonical. |
| PLEX | `PLEX` (always uppercase, one word) | EVE canonical. |
| SP | `SP` | Skill Points. |
| Manufacturing | `Manufacturing` (noun for the activity) | EVE canonical industry activity. |
| Build | `Build` (verb, colloquial okay) | Community-canonical shorthand for "manufacture." |
| Trade Hub | `Trade Hub` | EVE's canonical term for Jita/Amarr/Rens/Dodixie/Hek. |
| Structure | `Upwell structure` (formal) / `Structure` (short) | CCP's post-2016 umbrella term. |
| Citadel | Reserved for the specific hull class (Astrahus/Fortizar/Keepstar) | Do not use generically. |
| Multibuy | `Multibuy` (title case, one word) | EVE's Marketplace menu entry. |
| Undercut | `Undercut` (verb) | Community-canonical trader jargon. |
| High-Sec / Low-Sec / Null-Sec | `High-Sec` / `Low-Sec` / `Null-Sec` | EVE canonical (hyphenated). |
| Wallet / Journal / Transactions | `Wallet` (container) / `Journal` (log) / `Transactions` (log) | EVE canonical Wallet-window tab labels. |
| Character | `Character` (formal) / `Pilot` (in-flight context) | EVE uses both; character is safer for menus and settings. |
| Cancel order | `Cancel order` | EVE canonical; never bare "Cancel" for this action. |
| Modify order | `Modify order` | EVE canonical for repricing/undercutting. |

---

## Appendix — hardcoded (bypassing i18n) user-visible strings

These strings live in `.tsx` files and don't route through the `t()` helper. All should move to `en.ts` / `ru.ts` (with locale-parity per CLAUDE.md's mandate).

| File | Line | String |
|---|---|---|
| `BacktestPopup.tsx` | 455 | `Item` (table header) |
| `BacktestPopup.tsx` | 457 | `Win` |
| `BacktestPopup.tsx` | 458 | `Fill` |
| `BacktestPopup.tsx` | 485 | `Exit` |
| `BacktestPopup.tsx` | 486 | `Item` |
| `BacktestPopup.tsx` | 487 | `Qty` |
| `BacktestPopup.tsx` | 488 | `Fill` |
| `BacktestPopup.tsx` | 791-793 | `Snaps`, `Levels`, `Volume` |
| `BacktestPopup.tsx` | 799 | `No data` (empty state — should use `EmptyState`) |
| `BacktestPopup.tsx` | 889-894 | `Item`, `Status`, `Src`, `Tgt`, `Pairs`, `Depth` |
| `BatchBuilderPopup.tsx` | 499 | `Fresh` (table header) |
| `CockpitInterfaceTab.tsx` | 831, 839, 1036 | `Loadout`, `Density`, `Confidence` (section captions) |
| `CockpitInterfaceTab.tsx` | 1171 | `Bind startup task` |
| `CockpitInterfaceTab.tsx` | 1184 | `Saved bindings` |
| `CockpitInterfaceTab.tsx` | 1346 | `Verified` |
| `CockpitInterfaceTab.tsx` | 1396 | `Down` (button) |
| `CockpitInterfaceTab.tsx` | 1407-1411, 1425, 1439, 1448 | `Tab`, `Density`, `Columns`, `Filters`, `Action` (table headers + duplicates) |
| `CockpitInterfaceTab.tsx` | 1469 | `Saved in the active cockpit loadout and exported with workspace packs.` |
| `CommandPalette.tsx` | 120 | `Esc` (kbd label) |
| `corp-dashboard/shared.tsx` | 199, 244, 319 | `No data` (three separate hardcoded empty states) |
| `character-popup/HostedAccessTab.tsx` | 507, 511, 519, 660, 670-677, 721 | `Expires`, `Renews`, `Payment`, `Payment history`, `Status`, `Plan`, `Required`, `Paid`, `Code`, `Created`, `Matched`, `Note`, `Usage` (entire table header set) |
| `character-popup/OptimizerTab.tsx` | 334-343, 340-341, 569, 579, 587, 591, 595, 599 | `Action`, `Item`, `Exposure`, `Target`, `Risk`, `Buy ord`, `Sell ord`, `Unrealized`, `Suggested`, `Current Portfolio`, `Minimum Variance`, `Current`, `Optimal`, `Min Var`, `Assets` |
| `character-popup/PIPlanetsTab.tsx` | 211-214+ | `Planet`, `Character`, `Pins`, `Stored`, ... (whole PI table) |
| `ExecutionRevalidationReportModal.tsx` | 194-195 | `Buy scan -> quote`, `Sell scan -> quote` |
| `PaperTradeJournalPopup.tsx` | 681 | `"Loading..." : "Refresh"` (button label, inline) |
| `PaperTradeJournalPopup.tsx` | 829-830 | `Buy`, `Sell` (headers) |
| `PaperTradeJournalPopup.tsx` | 913, 915, 933, 935 | `Save`, `Apply live`, `Cancel`, `Delete` (footer buttons — mixed conventions) |
| `plex-tab/PlexArbitrageModal.tsx` | 117, 147 | `Buy order + broker`, `Sell price (Jita)` |
| `TradeExecutionAutopilotPopup.tsx` | 1398-1399 | `Buy orders`, `Sell orders` |

Estimated total: **>60 hardcoded strings** across ~15 files. All should be added to `en.ts` and `ru.ts` per the locale-parity rule from CLAUDE.md.

---

## Priority summary

| Priority | Count | Findings |
|---|---|---|
| Critical | 3 | A1 (scan/refresh/fetch verb split), A2 (Bid/Ask), B1 (bid/ask restatement) |
| High | 6 | A3 (column headers), A4 (EmptyState bypasses), A6 (modal footers), executive summary items 3, 4, 9, 10 |
| Medium | 6 | A5 (loading indicators), A7 (filter inputs), A8 (preset panels), A11 (keyboard shortcuts), B2 (sales tax naming), B6 (ISK case), B11 (sec labels), B12 (multibuy spelling) |
| Low | 5 | A9 (badges/pills), A10 (toasts), A12 (typography), B3 (Citadel), B4 (Manufacturing), B5 (BPO/BPC casing), B7 (Pilot), B9 (Undercut), B14 (Warp/Jump), B15 (Wallet abbrev), B16 (contract types) |
