# EVE Flipper — Architecture

> Audit pass, August 2026. This document maps how the code is laid out, what
> depends on what, and where the load-bearing decisions live. It is intended for
> the person who will next open the codebase — this file plus `CLAUDE.md` should
> be enough to know where to look for anything.
>
> Not investigated in this pass is called out inline. Nothing here is invented;
> anywhere I couldn't verify a claim I've said so.

---

## 1. Top-level runtime model

EVE Flipper is a single Go binary that embeds a React/TS SPA and talks to ESI
directly from the user's machine. Two flavors are built from the same
`internal/` backend, differing only in the `main*.go` entry file and a build
tag:

| Flavor | Build tag | Entry file | UI |
|---|---|---|---|
| Web / server (default) | `!wails` | `main.go` | Chrome / Firefox on `http://localhost:13370` |
| Wails desktop | `wails` | `main_wails.go` | Native window driven by Wails v2, backend proxied |

### Common startup sequence

Both entries do the same eight steps, in this order:

1. **`loadDotEnv()`** — read `./.env` and `<exe-dir>/.env`, without overwriting
   existing OS env vars. Lets a double-clicked binary use `ESI_*` settings.
2. **`logger.InitFileLogging(logDir)`** — dual-file logger (`prod` + `debug`)
   written next to the running binary.
3. **`db.Open()`** — SQLite at `<cwd>/flipper.db` (WAL mode,
   `busy_timeout=5000`, `foreign_keys=1`, `MaxOpenConns=1`). Runs migrations.
4. **`database.MigrateFromJSON()`** — one-shot migration for old
   `config.json` → SQLite installs.
5. **`database.CleanupStartupCachesAsync(30s)`** — background cache trim so
   large existing DBs don't block startup.
6. **`esi.NewClient(database)`** + **`esiClient.LoadEVERefStructures()`** —
   ESI HTTP client wired to the DB as `StationStore`, and a background fetch
   of the public EVERef structure-name dataset.
7. **`auth.NewSessionStore(database.SqlDB())`** + **`database.SetPrivacyCodec(sessions.Vault())`** —
   session store; its `TokenVault` is registered as the DB's privacy codec so
   any subsequent `INSERT` into a "private" column is transparently encrypted.
8. **`api.NewServer(cfg, esiClient, database, ssoConfig, sessions)`**, then
   `SetAppVersion`, `SetAppFlavor("web"|"desktop")`, `SetTelemetry(...)`.
   **SDE is loaded in a separate goroutine** and attached later via
   `srv.SetSDE(data)` — handlers guarded by `s.isReady()` return 503 until it
   arrives.

Then the flavors diverge:

- **`main.go`** builds a single `http.Handler` that dispatches `/api/*` to
  `srv.Handler()` and everything else to `http.FileServer(fs.Sub(frontendFS, "frontend/dist"))`
  with an SPA fallback to `index.html`. Serves on `<host>:<port>` (default
  `127.0.0.1:13370`, both configurable via `-host`/`-port` flags). Graceful
  shutdown on SIGINT/SIGTERM with a 10s deadline.

- **`main_wails.go`** calls `startBackend()` which:
  1. Opens the DB and initializes the client / SSO / sessions.
  2. Calls `listenOnPreferredOrFreePort(host, 13370)` — tries the preferred
     port, falls back to `net.Listen("tcp", ":0")` on failure (logs a WARN).
  3. **Rewrites the SSO callback URL** when the preferred port isn't
     available: `http://localhost:<fallbackPort>/api/auth/callback`. This
     obviously requires the EVE Developer Portal application to have every
     possible port pre-registered — the fallback is a last-ditch measure and
     the log message explicitly tells the user to close the other Eve Flipper
     process for SSO to work.
  4. Starts the HTTP server on the listener in a goroutine, then blocks in
     `waitForBackendReady()` polling `/api/status` (250 ms interval, 15 s
     timeout) so the Wails window doesn't open before the backend is up.
  5. `wails.Run(&options.App{…})` opens the frame with an `AssetServer`
     whose `Assets` is the embedded frontend FS and whose `Handler` is a
     `httputil.NewSingleHostReverseProxy` pointed at the local backend base
     URL — every request the Wails window makes that isn't a static asset
     goes back through the same HTTP handler tree.

### Frontend embedding

Both `main.go` and `main_wails.go` carry:

```go
//go:embed frontend/dist/*
var frontendFS embed.FS   // wailsFrontendFS in main_wails.go
```

**The frontend must be built (`corepack pnpm -C frontend run build`) before
`go run .` / `go build`**, otherwise the embed pattern fails at build time
with `pattern frontend/dist/*: no matching files found`. There is no
graceful degradation for a missing embed. During UI iteration use
`pnpm -C frontend run dev` (Vite HMR on `:5173`), which proxies API calls to
a separately-running `go run .`.

### SDE async load

The SDE (Static Data Export, ~14M items/systems/blueprints/etc.) is loaded
off the main goroutine so the server can start accepting connections
immediately. The pattern:

```go
srv := api.NewServer(...)         // sdeData == nil, ready == false
go func() {
    data, err := sde.Load(dataDir)          // slow — several minutes on first run
    ...
    srv.SetSDE(data)                        // grabs write lock, constructs scanner+analyzers, ready = true
}()
```

`sde_runtime.go` in the repo root hooks `SetSDE`'s completion path with
`prepareShipPackagedVolumes()` (apply cached packaged volumes to
`sde.Data`) and `refreshShipPackagedVolumesInBackground()` (fetch any
missing volumes via `esi.TypeInfo` with a 45s per-batch budget). Ship
packaged volumes aren't in the SDE dumps because CCP publishes them only
via ESI's `/universe/types/{id}/`.

`Server.isReady()` is checked by every handler that needs the SDE (scans,
industry analysis, autocomplete, etc.) and returns `503 sde not ready` when
false.

---

## 2. Backend module map (`internal/`)

Sub-directories, alphabetical.

### `api/` — HTTP surface

Single `Server` struct in `server.go` holding every long-lived dependency.
See §3 for the full handler-registration story. Companion files
(`industry_blueprint_scan.go`, `hosted_access.go`, `cockpit.go`, …) hold
handler bodies — **all `mux.HandleFunc(...)` calls live in `server.go`**
(159 of them, verified via grep).

**Depends on:** every other subdirectory except `logger`/`windows`.

**Depended on by:** `main.go`, `main_wails.go`.

### `auth/` — EVE SSO + session store + vault

Three files:

- **`sso.go`** — OAuth2 flow. `SSOConfig{ClientID, ClientSecret,
  CallbackURL, Scopes}`, `BuildAuthURL(state)`, `ExchangeCode(code)`,
  `RefreshToken(refreshToken)` (all POST to
  `login.eveonline.com/v2/oauth/token`), and `VerifyToken(accessToken)` →
  `CharacterInfo{CharacterID, CharacterName}` via
  `login.eveonline.com/v2/oauth/verify`.
- **`store.go`** — `SessionStore` wraps a `*sql.DB` and owns a
  `TokenVault`. Every method has a `ForUser` variant that scopes by userID
  (empty string → `DefaultUserID = "default"`). `SaveForUser`,
  `GetForUser`, `GetByCharacterIDForUser`, `ListForUser`, `SetActiveForUser`,
  `DeleteByCharacterIDForUser`, and the token-refresh entry
  `EnsureValidTokenForUser(sso, userID)` /
  `EnsureValidTokenForUserCharacter(sso, userID, characterID)` — the latter
  transparently refreshes when `ExpiresAt` is within tolerance, re-saves,
  and returns a fresh access token.
- **`vault.go`** — `TokenVault` is the on-disk encryption boundary. Two
  modes:
  - **`VaultModeStandard`** — 32-byte data key wrapped by the OS's
    machine data-protection primitive (`machine_protect_windows.go` →
    DPAPI; `machine_protect_other.go` = stub). Unlocks automatically on
    any process running as the same OS user; no passphrase.
  - **`VaultModePrivate`** — data key wrapped by a passphrase-derived
    KEK via Argon2id (`m=64MiB, t=3, p=2`, 16-byte salt). Passphrase
    must be ≥8 chars. Requires explicit `UnlockPrivateForUser` before
    token access. Unwrapped data keys held in
    `unlocked map[string][]byte` under a `sync.Mutex` until
    `LockForUser` or process exit.

  Sealed values use AES-GCM with AAD from `vaultAAD(userID, purpose,
  characterID)`, so a ciphertext bound to one field cannot be replayed
  into another. Ciphertexts carry the prefix `evf:vault:v1:` so
  unencrypted legacy values are recognisable and pass through untouched.
  `PrepareTokenForStorage`/`OpenTokenFromStorage` handle per-character
  access + refresh tokens; `ProtectStringForStorage(userID, purpose,
  value)`/`OpenStringFromStorage` are the general-purpose entry points
  that implement `db.PrivacyCodec`. `SetupStandardForUser` /
  `SetupPrivateForUser` both call `encryptLegacyPrivateFieldsForUser`
  inside the same transaction to seal any pre-existing plaintext.
  `recordSecurityEvent` writes to `security_events` on setup / unlock /
  reset for the vault status UI's audit trail.
  `ensurePrivateFieldCheckpointForUser` bumps stored rows from
  `vaultCheckpointV1` → `vaultCheckpointV2` on next unlock — the
  mechanism for forward-compatible re-encryption sweeps.

**Depended on by:** `api/` (SSO handlers, session lookups), `db/`
(via the `PrivacyCodec` interface).

### `config/` — in-memory config types

Just DTOs. `Config` (scan filters, tax profile, alert channels), `Stockpile`,
`StockpileItem`, `WatchlistItem`. Persistence is in `db/`. Consumed by
`api/` and `engine/`.

### `corp/` — corporation dashboard providers

`Provider` interface (implicit) with two implementations:
- **`esi_provider.go`** — `ESICorpProvider` wraps `esi.Client` + `sde.Data`
  + an access token + corporationID. `GetWallets`, `GetJournal`,
  `GetTransactions`, `GetMembers`, `GetIndustryJobs`, `GetMiningLedger`,
  `GetOrders`. Uses the `esi-corporations.*` scope family.
- **`demo.go`** — `DemoCorpProvider` for the marketing/demo mode (no
  auth). Seeded RNG produces plausible members, wallets, journal, mining
  ledger, and industry jobs. `NewDemoCorpProvider()` is constructed at
  SDE-load time on `Server`.
- **`dashboard.go`** — `PriceMap` type and thin composition helpers used
  by `api/server.go::handleCorpDashboard`.

### `db/` — SQLite persistence

Covered in §4. `DB` type in `db.go` wraps a single `*sql.DB` and hosts
the migration runner (currently 41 numbered migrations). Every file in this
package is a thin table wrapper.

### `engine/` — pure calculators

Covered in §2b. No HTTP, no DB. Owns `Scanner`, `IndustryAnalyzer`, and
the majority of the app's math. Engine types are JSON-tagged because they
serialize directly onto the wire (`FlipResult`, `StationTrade`,
`ContractResult`, `IndustryAnalysis`, `RouteResult`, …).

### `esi/` — ESI HTTP client

Covered in §5. `Client` type with connection pooling, two rate-limit
semaphores (lightweight vs bulk), multi-tier structure-name caching, and
`IndustryCache` for adjusted prices / system cost indices.

### `gankcheck/` — route danger analyzer

- **`checker.go`** — `Checker` bundles `zkillboard.Client`, `esi.Client`,
  `sde.Data`, and `graph.Universe`. `CheckRoute`, `CheckSystemDetail`,
  `CheckBatch` produce per-system `SystemDanger` (green/yellow/red) based
  on kills, ISK destroyed, and presence of smartbombs / interdictors.
- **`cache.go`** — `systemDangerCache` — TTL cache keyed by system id.

Constructed at SDE-load time; consumed by `api/gankcheck.go` and by
`api/route_risk.go` (route enrichment).

### `graph/` — solar-system adjacency

- **`universe.go`** — `Universe{Adj map[int32][]int32, SystemRegion, SystemSecurity, pathCacheMu}`.
- **`bfs.go`** — `ShortestPath` with LRU cache initialized once the SDE
  finishes loading gates (`InitPathCache()`).

Populated by `sde.Load`. Consumed by every route-shaped calculation
(routes, gank check, contract liquidation destination).

### `logger/` — dual-file logger

`logger.Info/Warn/Error/Success/Section/Stats/Banner/Server`, plus
`InitFileLogging` (prod log + debug log written to the exe directory).
OS-specific `logger_windows.go`/`logger_unix.go` for line-endings and
console colouring.

### `sde/` — Static Data Export loader

Covered in §6. `sde.Load(dataDir)` returns a `*sde.Data` bundle
(`Systems`, `SystemByName`, `Regions`, `Types`, `Stations`,
`Industry.Blueprints`, `Rigs`, `Universe`, …).

### `telemetry/` — optional analytics uplink

`Client` posts `Event`s to a configurable HTTP endpoint. `LoadConfigFromEnv`
reads `TELEMETRY_ENABLED`, `TELEMETRY_ENDPOINT`, `TELEMETRY_API_KEY`,
`TELEMETRY_SALT`, `TELEMETRY_ENV`; `NewFromEnv()` returns a disabled client
when unset. Every request is timed by `Server.telemetryMiddleware` (§3);
`api/telemetry.go::handleTelemetryClient` ingests client-side events.
`sanitize.go` allow-lists client event types and strips PII.

### `windows/` — Windows resource stub

`resource.syso` — icon/version resource compiled into Windows builds.
Same story for `resource_windows_amd64.syso` in the repo root.

### `zkillboard/` — zKillboard integration

- **`client.go`** — `NewClient()`, plain HTTP client with retry. `GetRegionStats`, `GetRecentKills`, `GetSystemKills`, `HealthCheck`.
- **`demand.go`** — `DemandAnalyzer{regionNames, cache, mu}`. `NewDemandAnalyzer(sdeData.RegionNames())` at SDE load. `GetHotZones(limit)`, `GetSingleRegionStats`, `GetTopDestroyedShipGroups` — powers the War/Demand tab.
- **`fittings.go`** — fetches recent-kill fittings and rolls up per-module frequency for demand fitting profiles.
- **`opportunities.go`** — combines demand items with market data to surface "haul these to this region" recommendations.

---

## 2b. Engine, file by file

`internal/engine/` — pure Go, no HTTP, no DB. This is the mathematical heart
of the app. All types are JSON-tagged; they cross the wire directly. Files
listed alphabetically.

**`backtest.go`** — Historical flip backtester over recorded `FlipResult`
rows. `FlipBacktestParams` (strategy mode, hold-days window, quantity/budget,
fee split, safety/route cost model, cooldown), `FlipBacktestTrade`,
`FlipBacktestItemSummary`, `FlipBacktestEquityPoint`, `FlipBacktestResult`.
Public `BuildFlipBacktest(rows, params, getHistory)` replays each row
against ESI history using either `backtestHoldCycleRow` or
`backtestInstantFlipRow`, enforcing non-overlapping trades, cargo/travel
cooldowns and budgets. Emits per-trade ledger + equity curve + drawdown.

**`backtest_orderbook.go`** — Backtester variant that replays real
captured order-book snapshots instead of daily VWAPs. Types
`OrderBookReplayFilter/Level/Book`, `OrderBookReplayGetter`, and the
coverage-report types `OrderBookReplayCoverageRow/Summary/Result`.
Powers the "did we actually have matching source+target books recorded
during this window?" diagnostic and the higher-fidelity order-book flip
backtest.

**`contracts.go`** — Public-contract deal scanner. Big table of tuning
constants (min price, max margin scam guard, VWAP deviation ceiling, hold
horizon, fill participation, carry cost, ship-module discount). Helpers
`isHighsecRestrictedShipGroup`, `getRigSizeClass`, `isContractRigType`,
`estimateContractRigValue`, plus the highsec-restricted-hull set. Provides
the risk-adjusted valuation model behind the Contracts tab.

**`decryptors.go`** — Server-side mirror of the frontend's T2 invention
decryptor table. `Decryptor{probMultiplier, runsBonus, meDelta, teDelta,
costISK}`, exported `Decryptors` slice with all nine canonical decryptors
plus "None". `EffectiveInventionParams`/`EffectiveInventionParamsForBase`
give the effective ME/TE/output-runs/chance-multiplier/cost for a given
SDE base-runs number (T2 ships have 1-run base; T2 modules have 10-run
base). Lets the scanner auto-pick the ISK/h-optimal decryptor per target.

**`eve_ledger.go`** — Character wallet & capital dashboard model layer.
`EveLedgerOptions`, `EveLedgerDashboard` (summary + daily/weekly/monthly
curves, categories, inventory, portfolio, archive info, warnings),
`EveLedgerSettings`, `EveLedgerArchiveInfo`, `EveLedgerSummary`,
`EveLedgerCurvePoint`, `EveLedgerCategory`, `EveLedgerInventoryItem`.
Backs the EveLedger-style wallet page — cashflow, trading vs "other"
activity, inventory mark-to-market vs cost basis, open-order exposure.

**`execution.go`** — Slippage-aware execution planner.
`ExecutionPlanRequest`/`ExecutionPlanResult` describe walking sell or buy
orders to fill a desired quantity — produces `DepthLevel` fill curves,
best/expected price, slippage %, total cost, TWAP slice count, embedded
`ImpactEstimate`. `ExecutionQuote{Side, Fees, ShippingISK, Volume, ROI,
Decision}` is the paired buy+sell primitive used across scans, station
trading, and routes.

**`fees.go`** — Fee-math utility. `tradeFeeInputs` carries both legacy
single-broker-fee and split buy/sell broker+tax percentages; helpers
`normalizeTradeFees`, `tradeFeePercents`, `tradeFeeMultipliers` reduce to
`buyCostMult`/`sellRevenueMult` scalars used by every profitability
formula. Encapsulates EVE's asymmetric broker-fee-on-buy vs
sales-tax+broker-fee-on-sell.

**`impact.go`** — Market-impact calibration from ESI history.
`ImpactParams{Amihud, sigma, avgDailyVolume, valid}` and `ImpactEstimate`
(linear `amihud × Q` and square-root-law `σ × √(Q/V)` impact percentages,
ISK impact, recommended TWAP slice count so each slice ≤ 5% of daily
volume). `CalibrateImpact(history, days)` computes parameters over a
rolling window.

**`industry.go`** — The build-vs-buy calculator. Defines
`IndustryAnalyzer` (constructed via `NewIndustryAnalyzer(sdeData, esiClient)`)
and the large `IndustryParams` struct (target typeID, runs, ME/TE,
facility/system, tax and rig configuration, revenue/cost model,
per-product owned-blueprint overrides, invention decryptor choices, …).
`Analyze` recursively expands via `buildMaterialTree` / `calculateCosts`,
computes EIV-based system cost, rig/structure bonuses, invention chance
and per-node build-vs-buy decisions, returns profitability of
manufacturing/reaction/invention chains against Jita or other pricing
regions. ⚠️ **Not goroutine-safe on the same instance** — see §10.

**`industry_coverage.go`** — "Can I start this project today?" reconciliation.
`IndustryCoverageMaterialNeed/BlueprintNeed`, `IndustryCoverage{Material,Blueprint}Stock`,
`Coverage{Material,Blueprint}Row`, `IndustryCoverageAction/Summary/Result`.
`ComputeIndustryCoverage(materialNeeds, blueprintNeeds, assetsByType,
blueprintStock)` returns per-row coverage %, ordered action list, and a
`CanStartNow` flag. Drives the Industry planning readiness board.

**`liquidity.go`** — Liquidity-scoring primitives. Internal
`historicalFillBacktest`, `estimateFillTimeDaysFromFlow`,
`estimateCycleFillTimeDays`, `liquidityScoreFromFillTime` (0–100 with
high/medium/low/thin labels), `computeHistoricalFillBacktest`,
`aggregateLiquidity` (worst-hop reduction across a multi-hop route).
Shared by scans, routes, and station rows.

**`market_restrictions.go`** — Policy filter. `marketDisabledTypeIDs`
(currently just MPTC), player-structure locationID threshold constant
(`1_000_000_000_000`), cosmetic-category IDs. Exposes
`IsMarketDisabledTypeID`, `IsPlayerStructureLocationID`,
`isCosmeticType(typeID, sdeData)`.

**`models.go`** — Central wire-format DTOs. `FlipResult` is the biggest —
one buy-low/sell-high opportunity with dozens of JSON-tagged fields
(basic prices, best-bid/ask L1 depth, execution slippage/RealProfit,
liquidity/back-test scores, character-owned assets/orders overlay,
route-safety context, Regional-Day-Trader enrichment). `ContractResult`
is the analogous model for contracts. **This file has no logic** — it
defines the API contract the frontend consumes.

**`optimizer.go`** — Modern Portfolio Theory over the character's
realized-trade history. `PortfolioOptimization`, `AssetStats`,
`FrontierPoint`, `AllocationSuggestion`, `PortfolioCapital`,
`PortfolioPositionRisk`, `OptimizerDiagnostic`. Entry points
`ComputePortfolioOptimization(WithContext|WithRuntime)` estimate per-item
mean/vol/Sharpe, build a Ledoit-Wolf-shrunk covariance matrix, solve
long-only min-variance / tangency portfolios by projecting onto the
simplex, and recommend which items to increase or decrease with an
efficient frontier.

**`order_desk.go`** — Active-order triage dashboard. `OrderDeskOrder` (rich
row with position in book, queue-ahead, suggested reprice, ETA days,
expiry warning, hold/reprice/cancel recommendation), `OrderDeskResponse`.
`ComputeOrderDesk(playerOrders, regionOrders, historyByKey,
unavailableBooks, opt)` builds the "what to do with each live order"
list.

**`plex.go`** — PLEX/NES arbitrage and SP-farm analytics. `PLEXDashboard`
+ supporting `NESPrices`, `PLEXGlobalPrice`, `ArbitragePath`,
`SPFarmResult`, `PLEXIndicators`, `ChartOverlays`, `ArbHistoryData`,
`PLEXSignal`, `MarketDepthInfo`, `InjectionTier`, `OmegaComparison`,
`CrossHubArbitrage`. Hard-codes canonical typeIDs (PLEX 44992, Skill
Extractor, Large Skill Injector, MPTC) and the July-2025 global PLEX
region. Backs the PLEX tab.

**`portfolio.go`** — Character P&L pipeline. `PortfolioPnL{daily,
ledger, positions, coverage, slotEfficiency}`, `RealizedTrade`,
`OpenPosition`, `MatchingCoverage`, `PortfolioPnLOptions/Settings`,
`PortfolioPnLStats`. `ComputePortfolioPnL(WithOptions)` walks wallet
transactions with per-type FIFO buy-lot matching, tags unmatched flow,
builds daily P&L / drawdown curves, rolls up top items/stations.
`ComputePortfolioSlotEfficiency` scores order-slot utilization.

**`portfolio_manufacturing.go`** — Extends `portfolio.go` to also consume
industry jobs. `JournalTxn`, `JournalIndustryJob`, `TradeLot`,
`ManufacturingLot`, `FIFOMode` (strict-date / trade-first / mfg-first),
`LotSource`, `MEResolution`, `MaterialCostSource`.
`ComputeTradeJournal(txns, jobs, opts)` merges character + corp wallet
transactions with completed industry jobs into one chronological event
stream, maintaining separate trade and manufacturing FIFO pools per
typeID, and produces `TradeJournalResult` splitting Trading vs
Manufacturing vs Combined P&L.

**`regional_day_trader.go`** — Data model + helpers for the EVE-Guru-style
"regional day trader" view. `RegionalDayTradeItem`, `RegionalDayTradeHub`
(source/target station, capital required, ROI now vs period, DOS, trade
score, execution quote), `RegionalInventorySnapshot` bindings for
character assets/orders. `FlattenRegionalDayHubs` converts hub-grouped
output back to `FlipResult` rows; diagnostic/robust-price/demand-blending
routines stabilize pricing across noisy hubs before grading day-trade
opportunities.

**`risk.go`** — Portfolio VaR/ES estimator. `PortfolioRiskSummary`
(0–100 RiskScore + label, VaR/ES at 95/99, typical daily P&L, worst day,
sample count, window days, capacity multiplier).
`ComputePortfolioRiskFromTransactions(txns)` builds a daily realized-P&L
series via per-typeID FIFO over a 180-day lookback, estimates historical
1-day 95%/99% VaR and ES.

**`route.go`** — Multi-hop route search core. `orderIndex` (per-system
cheapest-sell and highest-buy indices plus full books keyed by
`routeBookKey` for slippage-aware hops), `orderEntry`, `regionDistance`,
`sourceSystemCandidate`, constants `MaxTradeJumps` /
`MaxRouteSearchRegions`, helpers `selectClosestRouteRegions`,
`buildOrderIndex(WithFilters)`, `routeMinISKPerJumpPass`. Backs the
Route tab's multi-region "buy in A, sell in B, then move to C" planner.

**`route_execution.go`** — Realistic hauling-time estimates.
`RouteExecutionProfile` (ship profile, cargo capacity, minutes-per-jump,
dock minutes, safety delay); `RouteExecutionProfileFromParams` derives
one from `RouteParams`. `EnrichRouteExecutionEstimates(WithProfile)`
walks each hop, computes cargo m³, round-trip cargo hauls, per-hop and
per-route execution minutes with bounded gank-risk safety multiplier,
updates ISK/hour and courier collateral.

**`route_liquidity.go`** — Post-processes route results with market-history
depth. `Scanner.enrichRoutesWithLiquidity` fetches history for every
(region, typeID) touched by any hop in a bounded worker pool of 10,
computes daily volume via `esi.ComputeMarketStats`, and back-fills each
hop with `DailyVolume`, `FillTimeDays`, `LiquidityScore`,
`LiquidityLabel`, plus a per-route liquidity summary.

**`route_mode.go`** — Encodes balanced / fastest / safest ranking.
`RouteModeBalanced/Fastest/Safest`, `NormalizeRouteMode`.
`routeSearchScore` (used during search — jump-count exponent penalties),
`SortRouteResultsByMode`, `routeRiskSortScore` (danger colour + safety
multiplier), `routeBalancedSortScore` (mixes profit-per-hour + liquidity
+ risk).

**`route_time.go`** — Small helper used exclusively by the backtester.
`RouteTimeEstimate{minutes, jumps, trips, cargo, safety, danger, kills}`
and `estimateBacktestRouteTime(row, params, qty)`. Gives the backtester
realistic per-trade travel-time so cycle throughput and profit-per-hour
are honest.

**`scanner.go`** — Top-level `Scanner` orchestrator. Pairs the SDE
pointer with the ESI client and contract/history caches plus a
`HistoryProvider` interface for pluggable market-history storage.
`Scan(params ScanParams, progress func(string))` discovers candidate
systems within configurable buy/sell radii (optionally security-filtered),
applies ignore lists, returns a bounded set of profitable inter-station
flip opportunities. The entry point behind `POST /api/scan`.

**`station_actions.go`** — Rule engine deciding what to do with each
station-trading row: `StationActionNewEntry/Reprice/Hold/Cancel`.
`evaluateStationAction(row)` applies prioritized rules considering active
orders at the exact station vs elsewhere, open inventory, daily profit,
confidence label, competition index and days-of-supply, then applies
`stationRiskPenalty` for low confidence / high scam-detection scores.

**`station_command_center.go`** — Top-level assembly of the Station
Command Center payload. `StationCommandAction`, `StationForecastBand`
(P50/P80/P95), `StationCommandForecast`, `StationCommandRow`,
`StationCommandSummary`, `StationCommandResult`.
`BuildStationCommand(trades, activeOrders, openPositions)` cross-references
scanned trades against the character's active orders and open positions,
invokes `evaluateStationAction` and `buildStationForecast`, returns a
prioritized personalized recommendation feed.

**`station_forecast.go`** — Uncertainty-aware forecasting for
station-command rows. Estimates base daily volume, daily profit, and
ETA-days from trade fundamentals (falling back through DailyVolume →
S2B/BfS → order counts); computes an `stationForecastUncertainty` blend
of confidence, competition index, scam score, DOS and volatility;
produces P50/P80/P95 bands.

**`station_metrics.go`** — Statistical primitives. `filterLastNDays`
(UTC-truncated ESI-history window slice), `CalcVWAP`, `CalcDRVI` (intraday
range volatility σ; formerly `CalcPVI`), `stdDev` (Bessel-corrected
sample σ), `CalcOBDS` (order-book depth within ±5% of best bid/ask
scored against a capital requirement). Ingredients for the Composite
Trading Score.

**`station_trading.go`** — Defines `StationTrade` — a same-station flip
with EVE-Guru-style analytics (VWAP, DRVI, OBDS, Scam Detection Score,
Competition Index, Composite Trading Score). Installs pluggable ESI
hooks (`stationFetchRegionOrders`, `stationFetchMarketHistory`,
structure/station name resolvers) used by the scanner to build
station-level opportunity rows, capped at `maxStationReturnedResults`.

**`undercut.go`** — 0.01-ISK-war detector. `UndercutStatus` (player order
position, total competing orders, best competing price, undercut ISK/%,
`SuggestedPrice` that beats best by 0.01, top-5 `BookLevel` with an
`IsPlayer` flag). `AnalyzeUndercuts(playerOrders, regionOrders)` indexes
the regional book by `(locationID, typeID, side)`, sorts each stack
correctly per side, finds the player's rank, returns the actionable
status list.

**Test files**: every calculator has a `_test.go` sibling with unit
tests; coverage is measured via `go test ./internal/engine
-coverprofile=engine.cover.out`. Not audited in this pass.

---

## 3. API layer — `internal/api/`

### The `Server` struct and constructor

`server.go` lines 44-98. `Server` bundles every long-lived dependency the
app touches:

| Field | Type | Where set |
|---|---|---|
| `cfg` | `*config.Config` | `NewServer` |
| `sdeData` | `*sde.Data` | `SetSDE` |
| `scanner` | `*engine.Scanner` | `SetSDE` |
| `industryAnalyzer` | `*engine.IndustryAnalyzer` | `SetSDE` |
| `demandAnalyzer` | `*zkillboard.DemandAnalyzer` | `SetSDE` |
| `esi` | `*esi.Client` | `NewServer` |
| `db` | `*db.DB` | `NewServer` |
| `sso` | `*auth.SSOConfig` | `NewServer` |
| `sessions` | `*auth.SessionStore` | `NewServer` |
| `wikiRAG` | `*stationAIWikiRAG` | `NewServer` |
| `demoCorpProvider` | `*corp.DemoCorpProvider` | `SetSDE` |
| `ganker` | `*gankcheck.Checker` | `SetSDE` |
| `telemetry` | `telemetrySink` | `SetTelemetry` |

Plus in-memory caches guarded by their own mutexes: a per-user SSO CSRF
state map (`ssoStates`, `ssoStatesMu`, ~15 min TTL), wallet-transaction
cache (`txnCache*`, TTL 2 min), PLEX-dashboard cache (`plexCache*`, TTL
5 min, protected by a `singleflight.Group` and a 1-slot semaphore
`plexBuildSem`), per-user cookie-signing secret, auth-revision map,
update-dismissal map. `mu sync.RWMutex` gates `sdeData`/scanner/analyzers/
`ready`.

`NewServer(cfg, esiClient, database, ssoConfig, sessions)` allocates the
struct and its maps. `SetSDE(*sde.Data)` (`server.go:750`) is called by
the SDE-loader goroutine — it constructs `Scanner`, `IndustryAnalyzer`,
`DemandAnalyzer`, `DemoCorpProvider`, `Checker` under the write lock,
flips `ready=true`, and fires a background `backgroundJournalSync()`
that iterates authorized characters and refreshes any wallet archive
older than 24h.

### The method-prefixed mux

Every route is registered in `Server.Handler()` (starts `server.go:862`)
using Go 1.22+'s method-prefixed pattern:

```go
mux.HandleFunc("POST /api/scan", s.handleScan)
mux.HandleFunc("GET /api/scan/history/{id}/results", s.handleGetHistoryResults)
```

There are **159 route registrations** (verified via `grep -c
"mux.HandleFunc(" server.go`). All 159 live in `server.go` — **no
companion file registers routes**. Companion files only hold handler
bodies referenced from `server.go`. Path parameters use the `{name}`
syntax; extract with `r.PathValue("name")`.

The middleware chain (top-most first, from `Handler()`'s return):

```
securityHeadersMiddleware
  → corsMiddleware
    → originGuardMiddleware
      → requestBodyLimitMiddleware
        → userScopeMiddleware
          → telemetryMiddleware
            → hostedQuotaMiddleware
              → mux
```

- `securityHeadersMiddleware` — `X-Content-Type-Options: nosniff`,
  `X-Frame-Options: DENY`, `Referrer-Policy: same-origin`,
  `Cross-Origin-Resource-Policy: same-origin`.
- `corsMiddleware` — Access-Control-Allow-Origin echoed only when the
  origin matches `isAllowedRequestOrigin`; credentials allowed.
- `originGuardMiddleware` — on state-changing methods, rejects mismatched
  Origin/Referer with 403.
- `requestBodyLimitMiddleware` — `defaultAPIRequestBodyMaxBytes = 2 MiB`.
- `userScopeMiddleware` — attaches userID to request context via
  `userIDContextKey`; source is `X-EveFlipper-UID` header, signed cookie
  (`eveflipper_uid`, HMAC-signed with per-DB secret), or `DefaultUserID`.
- `telemetryMiddleware` — wraps `ResponseWriter` in a
  `telemetryResponseWriter` that records status + duration and emits an
  `api_request` event via `s.telemetry.Track()`.
- `hostedQuotaMiddleware` — for hosted deployments, calls
  `hostedQuotaFeatureForRequest(r)` and either 401
  (`hosted_identity_required`), 429 (`quota_exhausted`), or 403 for other
  quota denials.

### Hosted-quota classification (`hosted_access.go`)

`hostedQuotaFeatureForRequest(r) (string, bool)` at `hosted_access.go:695`
is a switch that MUST return either `("scans", true)`, `("station_ai",
true)`, or `("", false)` for every POST `/api/...` route. Test
`TestHostedQuotaFeatureMappingClassifiesAllPostAPIRoutes` (in
`hosted_access_test.go`) enforces this — every new POST endpoint must be
added to one of the switch cases or the API test suite fails.

Currently classified as `"scans"`: `/api/scan`, `/api/scan/multi-region`,
`/api/scan/regional-day`, `/api/scan/contracts`, `/api/scan/station`,
`/api/market/price-audit`, `/api/market/hub-allocate`,
`/api/pi/factory-plan`, `/api/backtest/flips`, `/api/orderbook/coverage`,
`/api/route/find`, `/api/industry/analyze`, `/api/execution/plan`,
`/api/demand/refresh`, `/api/auth/station/cache/reboot`,
`/api/auth/station/command`, `/api/auth/industry/coverage`,
`/api/auth/industry/blueprints/profitable-scan`, `/api/auth/journal/sync`,
`/api/auth/journal/link-job`, plus two prefix-matched groups:
`isHostedQuotaIndustryProjectComputePath` (`/api/auth/industry/projects/…/plan[/preview]`,
`…/materials/rebalance`, `…/materials/recalc-remaining`,
`…/blueprints/sync`) and `isHostedQuotaStockpileScanPath`
(`/api/auth/stockpiles/{numericID}/scan` — the numeric-id gate excludes
`/resolve/scan`).

`"station_ai"`: `/api/auth/station/ai/chat` and
`/api/auth/station/ai/chat/stream`.

Everything else is unmetered (`("", false)`).

### NDJSON streaming and `writeMu`

Long-running analyzers (scans, industry analyze, profitable-blueprints
scanner, station scan, route find) stream over
`Content-Type: application/x-ndjson`. The wire format is one JSON object
per newline-terminated line, of `{type: "progress" | "result" | "error"}`.

Because `http.ResponseWriter` is not safe for concurrent use, any
handler that fans work out to goroutines guards every write with a
mutex. The canonical pattern lives at `industry_blueprint_scan.go:1326`:

```go
var writeMu sync.Mutex
ctx := r.Context()
writeLine := func(payload interface{}) {
    line, err := json.Marshal(payload)
    if err != nil { return }
    writeMu.Lock()
    defer writeMu.Unlock()
    select {
    case <-ctx.Done():
        return                       // handler ctx cancelled — don't write
    default:
    }
    fmt.Fprintf(w, "%s\n", line)
    flusher.Flush()
}
```

Two invariants:
1. **Every write goes through the mutex.** Concurrent `flusher.Flush()`
   on a shared writer corrupts `bufio` state and panics.
2. **The `select { case <-ctx.Done() }` check is inside the critical
   section**, so a worker goroutine that outlives the handler doesn't
   touch a closed writer.

Any future streaming endpoint fanning work into goroutines must follow
this pattern.

### Companion files (handlers live here, registration in `server.go`)

- **`achievements.go`** — `handleAuthListAchievements`,
  `handleAuthPatchAchievements`, `handleAuthMarkAchievementsSeen`.
- **`alerts.go`** — *no HTTP handlers.* Exposes `CheckWatchlistAlerts`,
  `processWatchlistAlerts`, `SendAlert`. Called from scan handlers to
  fan alerts to Telegram/Discord/desktop and record history.
- **`backtest.go`** — `handleBacktestFlips` (delegates to
  `engine.BuildFlipBacktest` or `BuildOrderBookReplayBacktest`),
  `handleOrderBookCoverage`. Enriches rows with
  `routeSafetyMultiplier`/`RouteSafetyDanger` via
  `rowsWithBacktestRouteRisk`.
- **`character_market_fees.go`** — `handleAuthCharacterMarketFees`.
  Reads Accounting (16622) and Broker Relations (3446) skill levels,
  returns suggested tax/broker fees so the UI can auto-populate them.
- **`cockpit.go`** — `handleGetCockpitPreferences`,
  `handlePutCockpitPreferences`, `handleGetCockpitLoadouts`,
  `handleCreateCockpitLoadout`, `handleUpdateCockpitLoadout`,
  `handleActivateCockpitLoadout`, `handleDeleteCockpitLoadout`.
  Persistence in `db.SaveActiveCockpitLoadoutForUser` /
  `ListCockpitLoadoutsForUser` / `UpsertCockpitLoadoutForUser` /
  `ActivateCockpitLoadoutForUser` / `DeleteCockpitLoadoutForUser`.
  128KB request-body cap.
- **`contracts.go`** — `handleGetContractItems` for
  `GET /api/contracts/{contract_id}/items`. Prefers the scanner's
  `ContractItemsCache`, falls back to direct
  `esi.FetchContractItems`. Enriches from `sdeData.Types`/`Groups`,
  filters out `engine.IsMarketDisabledTypeID` types.
- **`gankcheck.go`** — `handleGankCheck`, `handleGankCheckDetail`,
  `handleGankCheckBatch`. Fronts `s.ganker` (a `gankcheck.Checker`).
- **`hosted_access.go`** — hosted-billing surface.
  `handleHostedAccess`, `handleHostedPaymentRequest`,
  `handleHostedPaymentCancel`, `handleHostedPaymentMarkSent`. Also
  hosts `hostedQuotaMiddleware`, `hostedQuotaFeatureForRequest`,
  `consumeHostedUsage`. Env config: `EVEFLIPPER_ENTITLEMENTS_URL`,
  `EVEFLIPPER_ENTITLEMENTS_KEY`, `EVEFLIPPER_PAYMENTS_URL`,
  `EVEFLIPPER_USAGE_URL`. `defaultHostedAccess()` returns a `local`
  plan with 25 daily scans when hosted billing isn't configured.
- **`hub_allocate.go`** — `handleHubAllocate`. Decodes
  `hubAllocateRequest`, resolves stations to `hubContext` via SDE, fans
  `(item, hub)` pairs to 8 workers for region-orders + history, prices
  via `aggregateStationBook` → `aggregateRegionLowSell` → adjusted-price
  fallback, runs a strategy-specific allocator (`profit`/`balanced`/
  `volume`/`percent`) with volume caps + overflow retry.
- **`industry_blueprint_scan.go`** (2004 lines) — profitable-blueprints
  scanner. `aggregateOwnedBlueprints` (shared with project blueprint
  sync — fetches blueprints per character, falls back to assets on 403,
  optionally corp), `handleAuthIndustryProfitableScan` (streams NDJSON,
  hosts the canonical `writeMu` pattern, per-scan analyzer-copy +
  `sync.Once` overrides to memoize the three heavy per-Analyze fetches).
- **`industry_structure_rigs.go`** —
  `handleIndustryStructureRigs` returns the Standup rig catalog from
  `sdeData.Rigs` + `sdeData.RigAffinities`. Cached client-side for 1h.
- **`items.go`** — `handleItemSearch`, `handleItemIntelligence`.
  Assembles the "everything you might want to know about type X in
  region Y" bundle: live orders, 30-day history, personal FIFO trade
  summary, peer trade summary, restock recommendation, edge score.
- **`orderbook.go`** — `handleOrderBookSnapshots`,
  `handleOrderBookLevels`, `handleOrderBookStats`,
  `handleOrderBookCleanup`. Snapshot storage lives in
  `db/orderbook.go`; cleanup blocked in hosted deployments unless
  `EVEFLIPPER_ALLOW_HOSTED_MAINTENANCE` is set.
- **`paper_trades.go`** — `handleAuthListPaperTrades`,
  `handleAuthCreatePaperTrade`, `handleAuthUpdatePaperTrade`,
  `handleAuthDeletePaperTrade`. Thin CRUD over `db/paper_trades.go`.
- **`paper_trades_reconcile.go`** — `handleAuthReconcilePaperTrades`.
  Reconciles active paper trades against live wallet transactions +
  active character orders + character assets across all sessions in
  scope. Suggests a live status (`reconciled` → `sold` → `listed` →
  `hauled` → `bought` → `planned`) with a confidence label.
- **`pi.go`** — `handleAuthPIPlanets` (planetary-industry rollup —
  per-planet extractor/factory/storage summary, schematic resolution
  from `sdeData.Industry.PlanetSchematics`, values inputs and outputs at
  adjusted prices, GrossISKPerDay / NetISKPerDay /
  CycleHealthScore).
- **`pi_factory.go`** — `handlePISchematics`, `handlePIFactoryPlan`.
  Fans per-typeID region-order fetches out to 8 workers; POCO tax
  computed against `pocoBaseValueByTier` (P0=5, P1=400, P2=7200,
  P3=60000, P4=1200000 ISK).
- **`price_audit.go`** — `handlePriceAudit`. Decodes
  `{station_id, items:[{name, type_id, qty}]}`, 8-worker fan-out,
  price ladder `station low-sell → region low-sell → adjusted-price`.
  Exports the shared `aggregateStationBook`, `aggregateRegionLowSell`,
  `nextSellUndercut`, `snapToGrid` used by hub-allocate and PI factory.
- **`route_risk.go`** — pure helpers, no HTTP. `enrichRouteHaulingRisk`
  walks up to `maxRouteHaulingRiskEnrich` (~80) candidate routes,
  per-hop calling `routeDangerSystemsCached` under a 12s total / 2s
  per-leg budget with a segment cache. Writes `HaulingDanger`,
  `HaulingKills`, `HaulingISK`, `HaulingRiskScore`,
  `HaulingSafetyMultiplier` onto `engine.RouteResult`.
- **`security_vault.go`** — `handleSecurityVaultStatus`,
  `handleSecurityVaultSetup`, `handleSecurityVaultUnlock`,
  `handleSecurityVaultLock`, `handleSecurityVaultReset`. Delegates to
  `TokenVault.SetupStandardForUser`/`SetupPrivateForUser`/
  `UnlockPrivateForUser`/`LockForUser`/`ResetForUser`. Reset requires
  the magic string `"RESET"`. Payload includes a hard-coded
  `protected_fields` list of encrypted columns.
- **`station_ai_wiki_rag.go`** (1256 lines) — Station AI wiki RAG
  subsystem. *No HTTP handlers, no route registration.* Owns a private
  `*stationAIWikiRAG` type used by the AI-chat handlers in `server.go`.
  Hourly background sync of `ilyaux/Eve-flipper` wiki into
  `data/wiki-rag/` (gated by `EVE_FLIPPER_DISABLE_WIKI_RAG`), markdown
  section extraction, recursive token-aware splitting with 120-token
  overlap into ~800-token chunks, embedding via OpenAI
  (`text-embedding-3-small`) or a 384-dim local fallback, BM25 lexical
  retrieval, reciprocal-rank-fusion hybrid ranking (`RRFK=60`).
- **`stockpile_rollup.go`** — pure helpers, no handlers. `stockpileAsset`
  + `rollUpAtStation(assets, stationID)` BFS-walks the asset tree
  rooted at a station id — items directly at the station plus every
  container/ship cargo/assembly array chained via `ItemID` — returns
  `{typeID → total qty}`. Works uniformly for NPC stations and player
  structures.
- **`stockpiles.go`** — `handleListStockpiles`,
  `handleCreateStockpile`, `handleGetStockpile`,
  `handleUpdateStockpile`, `handleDeleteStockpile`,
  `handleUpsertStockpileItems`, `handleReplaceStockpileItems`,
  `handleDeleteStockpileItem`, `handleResolveStockpileNames`,
  `handleScanStockpile`. Scan gathers character + corp assets, runs
  `rollUpAtStation` per session in scope, computes shortfall, fetches
  Jita 4-4 prices (`stockpilePriceStationID=60003760`, region
  `10000002`).
- **`sysproc_other.go` / `sysproc_windows.go`** — build-tag-guarded
  process spawning helpers used by the updater.
- **`telemetry.go`** — middleware (`telemetryMiddleware`) + client
  telemetry ingest (`handleTelemetryClient`), plus tracker helpers
  (`trackScanStarted`, `trackScanFinished`, etc.). Emits `api_request`
  events with `Path`, `Method`, `Status`, `DurationMS`, IP (parsed from
  `CF-Connecting-IP`/`X-Forwarded-For`/`X-Real-IP`), country
  (`CF-IPCountry`, ignoring `XX`/`T1`), user agent, user id, character
  id. `normalizedTelemetryPath` rewrites numeric path segments to
  `{id}`.
- **`trade_journal.go`** (1029 lines) — Trade Journal / FIFO P&L
  endpoints. `handleTradeJournalSync` (per-character wallet + journal +
  industry jobs → archive tables, plus corp wallets for accountant/
  director scope, then `reconcileIndustryJobLinks`),
  `handleTradeJournalSummary`, `handleTradeJournalByType`,
  `handleTradeJournalLots`, `handleTradeJournalLinkJob`,
  `handleTradeJournalLinkCandidates`. `journalRuntime` is a 60s TTL
  package-global cache with `singleflight.Group` collapsing concurrent
  duplicate computes (the frontend fires `/summary` + `/by-type` in
  parallel).
- **`trading_edge.go`** — `handleAuthTradingEdge`. Aggregates paper
  trades into `tradingEdgeAgg` buckets (per-item / per-category /
  per-station), computes `win_rate`, `expected_isk`, `realized_isk`,
  `delta_isk`, `reality_ratio`, `avg_roi`, per-preset advice
  (min-net-ROI %, max-exposure ISK, max-qty, preferred/avoid scopes).
- **`ui.go`** — `handleUIOpenMarket`, `handleUISetWaypoint`,
  `handleUIOpenContract`. Thin proxies to ESI's UI-open endpoints (each
  refreshes the SSO token first).
- **`update.go`** (733 lines) — auto-updater against GitHub Releases
  (`ilyaux/Eve-flipper`). `handleUpdateCheck`,
  `handleUpdateSkipForSession`, `handleUpdateApply` (rejected in hosted
  deployments — downloads platform-specific asset into `TempDir`,
  verifies SHA256 against the checksum asset, writes an updater script
  via `writeUpdaterScript(runtime.GOOS, tmpPath, exePath)`, launches
  it, replies, then `os.Exit(0)` 1.2 s later so the updater can swap
  the binary).

---

## 4. Persistence — `internal/db/`

### `DB` wrapper

`db.go`. Owns a single `*sql.DB` (`MaxOpenConns=1` — SQLite is a
single-writer store) opened with pragmas
`journal_mode(WAL)&busy_timeout(5000)&foreign_keys(1)`. `Open()` locates
the DB at `<cwd>/flipper.db` (fallback `<exe-dir>/flipper.db`) and runs
migrations. `Close()`, `SqlDB()`, `SetPrivacyCodec(codec PrivacyCodec)`.

### Migrations

Runner in `db.go::migrate()`. Sequential integer versions read from
`schema_version` (max-version-wins). Currently **41 migrations**, all
guarded `if version < N { … INSERT OR IGNORE INTO schema_version (version)
VALUES (N) }`. There is no down-migration path — this is forward-only.

The list, from the labels in the logger calls:

| v | Description |
|---|---|
| 1 | initial: `config`, `watchlist`, `scan_history`, `flip_results`, `contract_results`, `station_cache` |
| 2 | market history (`market_history`, `market_history_meta`) |
| 3 | auth session (single-character) |
| 4 | scan history |
| 5 | demand cache |
| 6 | station_results ensure |
| 7 | demand fitting cache |
| 8 | demand history |
| 10 | route results |
| 11 | station_results execution fields |
| 12 | contract_results long-horizon fields |
| 13 | watchlist alert model |
| 14 | alert history |
| 15 | multi-character auth sessions |
| 16 | user-scoped auth/config/watchlist/alerts |
| 17 | scan history liquidity + execution fields |
| 18 | station_results full metric persistence |
| 19 | contract system/region persistence |
| 20 | flip_results L1 price/qty fields |
| 21 | contract liquidation destination persistence |
| 22 | station_results system/region persistence |
| 23 | station_results daily volume / item volume split |
| 24 | user trade-state persistence |
| 25 | industry ledger foundation |
| 26 | industry task/job FK integrity |
| 27 | regional day-trader history rows |
| 28 | liquidity, backtest, hauling risk fields |
| 29 | paper trade journal |
| 30 | historical orderbook snapshots |
| 31 | route execution timing fields |
| 32 | route courier collateral fields |
| 33 | achievements |
| 34 | wallet ledger archive |
| 35 | orderbook stats cache |
| 36 | cockpit preferences |
| 37 | cockpit loadouts |
| 38 | security vault |
| 39 | private wallet balance and SP metrics |
| 40 | stockpile manager |
| 41 | trade journal: corp wallet + industry job archive |

(v9 is intentionally skipped; the ladder goes v8 → v10.)

Migration mechanics live in `db.go` too — `ensureTableColumn` gates
`ALTER TABLE ... ADD COLUMN` by scanning `PRAGMA table_info` first
(idempotent when re-run against a partly-migrated DB), and
`tableExists` protects columns from being added to tables that were
never created on a stale install.

### Privacy codec

`privacy.go` defines the tiny interface:

```go
type PrivacyCodec interface {
    ProtectStringForStorage(userID, purpose, value string) (string, error)
    OpenStringFromStorage(userID, purpose, value string) (string, error)
}
```

`DB.protectPrivateString` / `openPrivateString` / `warmPrivateString`
are internal helpers that no-op when `d.privacy == nil` or the value is
empty. The codec is registered post-construction by
`database.SetPrivacyCodec(sessions.Vault())` — this is the only reason
`db/` doesn't depend on `auth/`. `auth.TokenVault` implements the
interface directly (§2b).

The list of currently-protected columns is enumerated hard-coded in
`api/security_vault.go`'s `securityVaultPayload.protected_fields`:
`auth_session.access_token`, `auth_session.refresh_token`,
`config.alert_telegram_token`, `config.alert_telegram_chat_id`,
`config.alert_discord_webhook`, `wallet_archive_sync.wallet_balance`,
`wallet_archive_sync.total_sp`, `wallet_journal_archive.reason`,
`wallet_journal_archive.description`, `wallet_journal_archive.context_id_type`,
`paper_trades.notes`, `paper_trades.source`, `industry_projects.notes`,
`industry_jobs.notes`, `cockpit_preferences.payload_json`,
`cockpit_loadouts.payload_json`.

### `user_scope.go`

Two lines that matter:

```go
const DefaultUserID = "default"
func normalizeUserID(userID string) string { /* "" → DefaultUserID */ }
```

Every user-scoped `*ForUser` DB method starts with `userID =
normalizeUserID(userID)` so an empty string is safe.

### Industry ledger — `industry_ledger.go` (3988 lines)

The largest DB file, home of the project → task → job → material →
blueprint-pool hierarchy backing the Industry tab. Table set (from
migrations v25/v26/v40):

- `industry_projects(id, user_id, name, strategy, notes, status,
  created_at, updated_at)`
- `industry_tasks(id, user_id, project_id, parent_task_id, name,
  activity, product_type_id, target_runs, planned_start, planned_end,
  priority, status, constraints_json, created_at, updated_at)`
- `industry_jobs(...)`
- `industry_material_plan(...)`
- `industry_blueprint_pool(user_id, project_id, blueprint_type_id,
  location_id, is_bpo, runs_remaining, ...)`

**`ApplyIndustryPlanForUser(userID, projectID, patch IndustryPlanPatch)`
is the canonical write path.** Preview mode goes via
`PreviewIndustryPlanForUser` which returns an `IndustryPlanPreview`
without writing. Both accept the same `IndustryPlanPatch`.

The `Replace: true` semantic (per CLAUDE.md, verified at line 2434):

```go
if patch.Replace {
    tx.Exec(`DELETE FROM industry_jobs           WHERE user_id = ? AND project_id = ?`, userID, projectID)
    tx.Exec(`DELETE FROM industry_tasks          WHERE user_id = ? AND project_id = ?`, userID, projectID)
    tx.Exec(`DELETE FROM industry_material_plan  WHERE user_id = ? AND project_id = ?`, userID, projectID)
}
if patch.Replace || patch.ReplaceBlueprintPool {
    tx.Exec(`DELETE FROM industry_blueprint_pool WHERE user_id = ? AND project_id = ?`, userID, projectID)
}
```

**Blast radius**: `Replace: true` **wipes the project's entire
tasks/jobs/materials list in a single transaction** before inserting the
new rows. There is no per-row DELETE endpoint. Removal today is either
via replace-mode apply (destructive re-plan) or by setting a row's
`status = "cancelled"` (keeps the row). Task IDs are re-issued when
re-inserted, so any client-side references to old task IDs are stale
after a replace.

The rest of the file provides granular per-row mutators
(`UpdateIndustryTaskStatusForUser`, `UpdateIndustryTaskPrioritiesForUser`,
`UpdateIndustryJobStatusForUser`, `RebalanceIndustryProjectMaterialsFromStockForUser`,
…) that keep task/job IDs stable.

### Other files (one-line summaries)

- `achievements.go` — `List/Apply/MarkSeen` for the achievement store.
- `alert_history.go` — watchlist alert log; `SaveAlertHistoryForUser`,
  `GetLastAlertTimeForUser` (1h cooldown lookup), `CleanupOldAlertHistory`.
- `cockpit.go` — cockpit preferences and named loadouts CRUD (payload
  stored as encrypted JSON blob).
- `config.go` — per-user config load/save, plus `MigrateFromJSON()`
  (one-shot import of legacy `config.json`).
- `demand.go` — zKillboard-derived hot-zone cache
  (`SaveDemandRegion/Item`, `GetHotZones`, `IsDemandCacheFresh`),
  fitting-demand cache (`SaveFittingDemandProfile`).
- `history.go` — scan history (`InsertHistoryFull`, `GetHistory`,
  `DeleteHistory`, `ClearHistory`).
- `maintenance.go` — DB-level maintenance (WAL checkpoint, VACUUM,
  startup cache cleanup called via `CleanupStartupCachesAsync`).
- `market_history.go` — cached ESI market history per (region, type).
- `orderbook.go` — historical order-book snapshot storage with hash
  dedupe and level rows. `RecordMarketOrderSnapshot` (called from
  `esi.Client` via `MarketOrderRecorder` interface),
  `CleanupOrderBookSnapshots(Batches)`, `GetOrderBookStats`. A package
  global `orderbookRecordMu` serializes writes.
- `paper_trades.go` — paper-trade CRUD (`CreatePaperTradeForUser`,
  `ListPaperTradesForUser`, `UpdatePaperTradeForUser`,
  `DeletePaperTradeForUser`).
- `results.go` — scan-result persistence (flip / contract / station /
  route rows tied to a `scan_history` row).
- `stations.go` — 16-line file: `GetStation(locationID)` /
  `SetStation(locationID, name)` for the persistent L2 station-name
  cache used by `esi.Client`.
- `stockpiles.go` — stockpile persistence (`CreateStockpileForUser`,
  `UpsertStockpileItemsForUser`, `ReplaceStockpileItemsForUser`,
  `ListStockpilesForUser`, `GetStockpileForUser`,
  `DeleteStockpileForUser`, …).
- `trade_journal_archive.go` — archive tables for wallet transactions /
  journal / industry jobs (per-character and per-corp-division).
  `wallet_archive_sync` metadata for staleness.
- `trade_state.go` — per-user, per-station trade state (hidden /
  starred / notes) for the Station Trading tab.
- `wallet_archive.go` — wallet balance & SP archive
  (`ListWalletArchiveMetaForUser`), private wallet-balance and SP
  metrics.
- `watchlist.go` — watchlist CRUD.

---

## 5. ESI client — `internal/esi/`

### `Client` structure

`client.go`. The heart is the `Client` struct with **two rate-limit
semaphores**:

```go
sem       chan struct{}  // size 50 — lightweight API calls (history, stations, auth)
scanSem   chan struct{}  // size 50 — bulk market-order page fetches (GetPaginatedDirect)
```

The split is intentional: bulk scan operations (300+ pages per region)
never starve lightweight calls like profile / station names / auth. The
underlying `http.Transport` has `MaxIdleConns=200`,
`MaxIdleConnsPerHost=100`, HTTP/2 explicitly disabled ("for bulk
market-order fetching HTTP/1.1 with a large connection pool is faster
than HTTP/2 multiplexing through a single TCP connection"). Client
timeout 30 s.

There is also a bounded `orderRecorderSem` (size 4) that gates the
fire-and-forget snapshot recorder goroutine: each in-flight snapshot
pins its full Orders slice (up to ~28 MB for a Forge-wide fetch) alive
until the DB write completes. Sizing rationale is documented in the
client.go comments — this is a known 32 GB RSS spike that a queue would
reintroduce.

### Structure-name caching (four tiers)

`StationName` and `StructureName` walk a four-tier cache. For a player
structure the ladder is:

1. **L1 (in-memory)** — `stationCache sync.Map[int64]string`. Placeholders
   like `"Structure 12345"` are ignored on hit.
2. **L2 (persistent)** — `stationStore` interface, backed by
   `db.stations.go` (`station_cache` table). Placeholders skipped
   similarly.
3. **L3 (EVERef fallback)** — `everefNames sync.Map[int64]string`.
   Populated at startup by `LoadEVERefStructures()` which fetches a
   public JSON dump from EVERef. Checked **before** the authenticated
   ESI call so a previously-denied lookup can still resolve from the
   public dataset.
4. **L4 (authenticated ESI)** — `GET /universe/structures/{id}/` with
   Bearer token. Requires `esi-universe.read_structures.v1` scope. On
   failure, `rememberStructureNameFailure` populates the negative
   cache (`structureNameFailures sync.Map[int64]structureNameFailure`)
   with a TTL based on the classified error:
   - 403 / 404 → 24 h ("inaccessible structure")
   - 420 / 429 → 15 min ("ESI rate limit") — sets a *global* negative
     cache entry too, so subsequent lookups suppress immediately
   - 502/503/504/520 → 2 min ("transient ESI failure") — also global
   - anything else → 5 min

Additional caches on the client:

- `structureSystems sync.Map[int64]int32` — structure → solar_system_id
  mapping, populated from EVERef and from authenticated ESI structure
  detail lookups.
- `structureTypes sync.Map[int64]int32` — structure → type_id (e.g.
  35827 for Sotiyo), populated opportunistically by `StructureDetails`.

### `IndustryCache` (`esi/industry.go`)

Wraps three time-bucketed maps behind an `RWMutex`:

- `costIndices map[int32]*SystemCostIndices` — system → manufacturing /
  copying / invention / reaction / ME / TE cost indices. 1h TTL.
- `prices map[int32]*IndustryPrice` — typeID → (adjusted_price,
  average_price). Bulk-fetched from `/markets/prices/`.
- `marketPrices map[int32]float64` — typeID → min sell price for a
  specific `marketPricesRegionID`. `MarketPricesCacheTTL = 10 min`.
- `locationMarketPrices map[string]locationMarketPricesEntry` —
  station/structure specific prices keyed by `"regionID:locationID"`.

`GetSystemCostIndex(cache, systemID)`, `GetAdjustedPrice(cache, typeID)`,
`GetAllAdjustedPrices(cache)`, `GetCachedMarketPrices(cache, regionID)`,
`GetCachedMarketPricesByLocation(cache, regionID, locationID)`.

### Other files

- **`character.go`** — every SSO-authenticated character endpoint:
  `GetCharacterOrders`, `GetWalletBalance`, `GetSkills`,
  `GetOrderHistory`, `GetWalletTransactions`, `GetWalletJournal`,
  `GetCharacterAssets`, `GetCharacterBlueprints`,
  `GetCharacterIndustryJobs`, `GetCharacterLocation`,
  `GetCharacterRoles`, `GetCharacterCorporationID`. Uses `AuthGetJSON`
  which passes `Authorization: Bearer <token>`.
- **`contracts.go`** — `FetchRegionContracts(regionID)`,
  `FetchContractItems(contractID)`, `FetchContractItemsBatch(ids,
  cache, progress)` (concurrent-fetch with a `ContractItemsCache`),
  `ContractsCache` / `ContractItemsCache` types.
- **`corporation.go`** — `GetCorporationBlueprints(corpID, token)`,
  `GetCorporationAssets(corpID, token)`.
- **`history.go`** — `FetchMarketHistory(regionID, typeID)` plus
  `ComputeMarketStats` helper used by liquidity enrichment. `PriceTrend`
  is computed with Theil–Sen (median-of-pairwise-slopes) rather than
  OLS — comment cites ~29% breakdown point for outlier robustness on
  EVE's spiky daily-volume series.
- **`industry.go`** — see above.
- **`market.go`** — `FetchRegionOrders`, `FetchRegionOrdersByType`
  (context-scoped variant). Underlying network is pageable and goes
  through `GetPaginatedDirect` which uses `scanSem`.
- **`order_cache.go`** — `OrderCache` with ETag/Expires-aware caching.
  Keyed by `orderCacheKey{RegionID, OrderType, Scope, TypeID,
  LocationID, TokenHash}` — the token hash keeps structure-market caches
  per-user. Wraps a `*singleflight.Group` so duplicate in-flight fetches
  coalesce. `EvictExpired` keeps entries for 30 minutes past expiry so
  ETag revalidation can succeed after TTL. `OrderCacheWindow` reports
  coverage; `ClearOrderCache` atomically swaps the singleflight group
  under `groupMu`.
- **`planetary.go`** — `GetCharacterPlanets(charID, token)`,
  `GetCharacterPlanetDetail(charID, planetID, token)`.
- **`structure_markets.go`** — `FetchStructureOrders(structureID, token)`
  (context variant). Requires `esi-markets.structure_markets.v1` scope.
  ESI sends no caching headers on this endpoint, so a fixed 5-minute
  cache expiry is applied. Results are gated per access token via a
  SHA-256-first-8-bytes token hash so two users on the same box don't
  see each other's structure orders, and the singleflight group in
  `OrderCache` coalesces duplicate fetches.
- **`ui.go`** — `OpenMarketWindow`, `SetWaypoint`, `OpenContractWindow`.
  Fronts `esi-ui.*` scopes.

### Goroutine-safety notes

- The `Client` itself is safe for concurrent use — its maps are `sync.Map`,
  its counters are guarded, and the two semaphores handle rate-limiting.
- `IndustryCache` methods are `RWMutex`-guarded.
- **`engine.IndustryAnalyzer.Analyze` stores per-call mutable state on
  the receiver**, so calling it from multiple goroutines on the same
  analyzer instance races. See §10 and CLAUDE.md.

---

## 6. SDE loader — `internal/sde/`

`loader.go::Load(dataDir)` downloads
`https://developers.eveonline.com/static-data/eve-online-static-data-latest-jsonl.zip`
(3 retries, 5 min per attempt, atomic extract to `data/sde/` — any old
extract is quarantined as `data/sde.corrupt.<timestamp>` before rename),
then reads a fixed set of JSONL files:

```
mapRegions, mapSolarSystems, groups, types, npcStations, mapStargates
```

It fills a `*sde.Data`:

| Map | Value | Populated by |
|---|---|---|
| `Systems` | `map[int32]*SolarSystem` | `loadSystems` |
| `SystemByName` | `map[string]int32` (lowercased) | `loadSystems` |
| `SystemNames` | `[]string` | (indirectly for autocomplete) |
| `Regions` | `map[int32]*Region` | `loadRegions` |
| `RegionByName` | `map[string]int32` (lowercased) | `loadRegions` |
| `Types` | `map[int32]*ItemType` | `loadTypes` |
| `TypeByName` | `map[string]int32` (lowercased) | `loadTypes` |
| `Groups` | `map[int32]*ItemGroup` | `loadTypes` |
| `Categories` | `map[int32]*ItemCategory` | `loadTypes` |
| `Contraband` | `map[int32]bool` | `loadTypes` |
| `Stations` | `map[int64]*Station` | `loadStations` |
| `Universe` | `*graph.Universe` (adj + region + security) | `loadSystems`+`loadStargates` |
| `Industry` | `*IndustryData` (Blueprints, PlanetSchematics, …) | `LoadIndustry(dir)` (`industry.go`) |
| `Rigs` | `map[int32]*StructureRig` | `loadRigs(dir)` (`rigs.go` / `rig_affinities.go`) |
| `RigsByFitsGroup`, `RigAffinities` | rig lookups | `loadRigs` |

Post-load: station names are resolved from their system
(`"Station in <SystemName>"`), the BFS path cache is initialized
(`Universe.InitPathCache()`).

### Async load pattern

`main.go` / `main_wails.go` call `sde.Load` in a goroutine and hand the
result to `srv.SetSDE(data)`. Between server start and `SetSDE`, the
server is up but every handler that calls `s.isReady()` returns 503
`"sde not ready"`. `SetSDE` grabs `s.mu.Lock()`, constructs the
`Scanner`, `IndustryAnalyzer`, `DemandAnalyzer`, `DemoCorpProvider`,
`Checker`, and sets `s.ready = true`.

### Ship packaged volume cache

`ship_volume_cache.go` (with `ship_volume_cache_test.go`). CCP doesn't
put packaged volumes in the SDE dumps — they come from ESI's
`/universe/types/{id}/`. The cache lives at `data/sde/ship_packaged_volumes.json`
and is applied by `sde.ApplyCachedShipPackagedVolumes(dir, data)`; any
missing typeIDs are fetched in the background via
`RefreshShipPackagedVolumeCacheForTypes(dir, missing, fetchFn)`. Both
are invoked from the repo-root `sde_runtime.go`.

---

## 7. Frontend — `frontend/src/`

React 19 + TypeScript 5 + Vite + Tailwind SPA.
`main.tsx` mounts `<App />` inside an `I18nProvider` (§7c) and an
`AchievementsProvider`.

### 7a. Tabs

The visible tabs come from `MAIN_TAB_IDS` in `lib/cockpit.ts` (line 7),
filtered by `getVisibleMainTabs(cockpitPreferences)`. `App.tsx` renders
each `TabPanel active={tab === "..."}` — **all tabs stay mounted** so
per-tab state survives switches.

| tab id | Label key (fallback) | Component |
|---|---|---|
| `radius` | `tabRadius` (Flipper) | `ScanResultsTable` + `ParametersPanel` |
| `region` | `tabRegion` (Regional Trade) | `ScanResultsTable` (columnProfile `region_eveguru`) |
| `contracts` | `tabContracts` | `ContractResultsTable` + `ContractParametersPanel` |
| `route` | `tabRoute` | `RouteBuilder` |
| `station` | `tabStation` (Station Trading) | `StationTrading` |
| `price_audit` | `tabPriceAudit` | `PriceAudit` |
| `pi_factory` | `tabPIFactory` | `PIFactory` |
| `industry` | `tabIndustry` | `IndustryTab` |
| `trade_journal` | `tabTradeJournal` | `TradeJournal` |
| `demand` | `tabDemand` (War) | `WarTracker` |

`MarketMakingTab` is imported and commented out in `App.tsx:20`.
`PlexTab` is embedded inside `CharacterPopup`, not a main tab.
`CorpDashboardApp` is a separate SPA entry point.

### 7b. Top-level components (one-liners)

**Tab bodies (registered above)**:

- `IndustryTab.tsx` — build-vs-buy analyzer + visual planner +
  operations workspace; owns the local plan drafts and a shared ledger
  snapshot. Lazy-loads its heavy child panels via `React.lazy`.
- `StationTrading.tsx` — live station-trading dashboard: scan,
  order-desk, per-trade command matrix, CTS batch builder, trade-state
  persistence, in-game market open/waypoint, hidden-trade tabs,
  tax-profile editor, optional `StationAIAssistant`.
- `RouteBuilder.tsx` — multi-region route optimizer.
- `WarTracker.tsx` — demand/war heat-map (zKillboard-derived).
- `RegionalDayTraderTable.tsx` — legacy EveGuru-style regional grid;
  kept for old regional payload shapes.
- `PriceAudit.tsx` — item-level price comparison across a station.
- `PIFactory.tsx` — planetary-industry factory planner.
- `TradeJournal.tsx` — Eve-Tycoon-style profit tracker: wallet-scoped
  FIFO trades + manufacturing lots.
- `CockpitInterfaceTab.tsx` — settings/interface workspace embedded in
  the `ThemeSwitcher` overlay.

**Tables / scan plumbing**:

- `ScanResultsTable.tsx` (4388 lines) — main results grid used by
  radius/region tabs.
- `ContractResultsTable.tsx`, `ContractParametersPanel.tsx`,
  `ParametersPanel.tsx`, `StationTradingExecutionCalculator.tsx`,
  `RegionAutocomplete.tsx`, `SystemAutocomplete.tsx`,
  `SystemBlacklistButton.tsx`, `PresetPicker.tsx`,
  `TabSettingsPanel.tsx`, `TabWorkspace.tsx`, `TabHelp.tsx`,
  `TaxProfileEditor.tsx`.

**Popups / modals / overlays**:

- `Modal.tsx`, `ConfirmDialog.tsx`, `ContextMenu.tsx`, `Tooltip.tsx`,
  `Toast.tsx`, `EmptyState.tsx`, `ErrorBoundary.tsx`, `StatusBar.tsx`,
  `ProfitPill.tsx` (30-day P&L badge in header), `LanguageSwitcher.tsx`,
  `ThemeSwitcher.tsx`, `KeyboardShortcutsHelp.tsx`, `CommandPalette.tsx`
  (Ctrl+K).
- `CharacterPopup.tsx` — big character workspace modal with tabs
  overview / orders / transactions / ledger / industry / pi / pnl /
  edge / risk / optimizer / achievements / plex / access.
- `PaperTradeJournalPopup.tsx`, `TradeExecutionAutopilotPopup.tsx`,
  `ExecutionPlannerPopup.tsx`, `ExecutionRevalidationReportModal.tsx`,
  `BatchBuilderPopup.tsx`, `BacktestPopup.tsx`,
  `ContractDetailsPopup.tsx`, `RouteSafetyModal.tsx` +
  `RouteSafetyBadge.tsx`, `ItemIntelligenceModal.tsx`,
  `SecurityVaultModal.tsx`, `ScanHistory.tsx`,
  `AlertHistoryViewer.tsx`, `PlexAlerts.tsx`, `StationAIAssistant.tsx`
  (draggable Ivy-AI chat sidebar).

**Non-tab SPA entry**:

- `CorpDashboardApp.tsx` — separate SPA entry point (`?mode=live|demo`);
  loads `Overview/Wallets/Members/Industry/Mining/Market` sections.

### 7c. Subdirectories

- **`components/industry/`** — the IndustryTab subtree. Big panels:
  `IndustryAnalysisResultsPanel`, `IndustryDependencyBoard`,
  `IndustryJobsGuidePanel`, `IndustryJobsLedgerPanel`,
  `IndustryJobsPlanningActions`, `IndustryJobsProjectHeader`,
  `IndustryJobsWorkspaceNav`, `IndustryMaterialDiffPanel`,
  `IndustryMaterialTree`, `IndustryOperationsBoards`,
  `IndustryOperationsJobsPanel`, `IndustryPlanPreviewPanel`,
  `IndustryPlannerBuilderPanel`, `IndustryPlannerSchedulerPanel`,
  `IndustryPlannerWarningLog`, `IndustryProfitableScannerPanel`
  (§7d), `IndustryRecalcRemainingModal`, `IndustryShoppingList`,
  `IndustryStockpilePanel`, `IndustrySummaryCard`,
  `IndustryTaskBoardPanel`, `IndustryWorkspaceStatusBoards`,
  `AddBlueprintsToProjectModal`, `PricingHubPicker`,
  `StructureRigPicker`. `industryHelpers.ts` holds shared
  types/formatters (`planPatchSignature`, task/job status classes,
  dependency-board type).
- **`components/achievements/`** — `AchievementBadge`,
  `AchievementsProvider` (context + `useAchievements` hook +
  event-driven engine + toast popups + `AchievementLibraryPanel`),
  `index.ts` barrel.
- **`components/character-popup/`** — one file per popup tab:
  `OverviewTab`, `CombinedOrdersTab`, `IndustryJobsTab`,
  `OptimizerTab`, `PIPlanetsTab`, `PnLTab`, `RiskTab`, `TradingEdgeTab`,
  `TransactionsTab`, `WalletDashboardTab`, `HostedAccessTab`;
  `TabButton` (shared between popup and main tab bar), `shared.tsx`.
- **`components/corp-dashboard/`** — sections composed by
  `CorpDashboardApp`: `OverviewSection`, `WalletsSection`,
  `MembersSection`, `IndustrySection`, `MiningSection`,
  `MarketSection`, plus `shared.tsx` (KpiCard, MiniKpi, DailyPnLChart,
  IncomeSourceChart, TopContributorsTable, BarChart, CsvExportButton,
  DateRangeSelector) and `types.ts`.
- **`components/journal/`** — `PnLPrimitives.tsx` exports
  `PnLChart`, `PnLItemsTable`, `PnLLedgerTable`,
  `PnLOpenPositionsTable`, `PnLStationsTable`, `SlotEfficiencyTable`,
  shared between `TradeJournal.tsx` and `character-popup/PnLTab.tsx` so
  the two surfaces render identical widgets.
- **`components/plex-tab/`** — `PlexAnalyticsCards`, `PlexArbitrageModal`,
  `PlexCharts`, `PlexMarketCards`, `SPFarmCard`, all mounted by
  `PlexTab.tsx`.

### 7c-lib. `lib/`

- **`api.ts`** — every API call. Uses `apiFetch` which adds an
  `X-EveFlipper-UID` header + `credentials: "include"` (desktop user id
  stored in `localStorage` under `eveflipper_uid_v1`; the Wails
  desktop build uses the constant `"eveflipper_desktop"`).
  `handleResponse<T>` normalizes JSON errors and maps hosted-quota
  codes (`quota_exhausted`, `hosted_identity_required`, `feature_denied`)
  to friendly messages.

- **`streamNdjson<T>` contract** (`api.ts:223`):

  ```ts
  async function streamNdjson<T>(
      url: string, body: object,
      onProgress: (msg: string) => void,
      signal?: AbortSignal,
      errorMessage = "Request failed",
      onResult?: (msg: Extract<NdjsonGenericMessage<T>, {type: "result"}>) => void,
  ): Promise<T[]>
  ```

  POSTs JSON `body`, reads `res.body` with a
  `ReadableStreamDefaultReader`, splits on `\n`, JSON-parses each line
  as `{type: "progress"|"result"|"error"}`. `progress` calls
  `onProgress`; `result` sets the return array and calls `onResult`;
  `error` throws. Used by `scan`, `scanMultiRegion`,
  `scanRegionalDayTrader`, `scanContracts`, `findRoutes`, `scanStation`,
  and (from `IndustryProfitableScannerPanel`) `scanProfitableBlueprints`.

- **`i18n.tsx`** —

  ```ts
  export type TranslationKey = keyof typeof ru;
  ```

  Russian is the source-of-truth key set. `t(key, params?)` looks up
  `translations[locale][key] ?? key`. This is the compile-time gate
  behind CLAUDE.md's "locale parity is mandatory" rule — adding a key
  to `en.ts` without also adding it to `ru.ts` (or vice versa when a
  consumer passes a `TranslationKey`) breaks the frontend build.
  `getDefaultLocale()` reads `localStorage["eve-flipper-locale"]`,
  otherwise falls back to `ru` iff `navigator.language.startsWith("ru")`
  else `en`.

- **`types.ts`** — hand-mirrored TypeScript types matching Go structs.
  **Field names use the exact Go tag verbatim**, which is why the file
  is a mix of PascalCase and snake_case: engine types marshaled with
  default `json.Marshal` on capitalized fields stay PascalCase
  (`FlipResult { TypeID, TypeName, Volume, BuyPrice, ... }`), while
  handler-produced types with explicit `json:"snake_case"` tags come
  through as snake_case (`RegionalDayTradeItem { type_id,
  target_now_profit, ... }`). Adding a backend field means declaring
  the same key here with the corresponding TS type.

Other `lib/` files (one-liners):

`achievements/` (`definitions.ts`, `engine.ts`), `cockpit.ts`
(preferences/loadouts state — `MAIN_TAB_IDS`, `MAIN_TAB_META`),
`cockpitInterfacePages.ts`, `domSafety.ts`, `executionRevalidation.ts`,
`format.ts`, `handleEveUIError.ts`, `industryDecryptors.ts` (mirrors
`engine/decryptors.go`), `industryPlanPatch.ts`
(`buildIndustryPlanPatch`, `applyCoverageToIndustryPlanPatch`),
`parseEveClipboard.ts`, `presets.ts`, `scanLifecycle.ts` (invalidate /
start / isCurrent tracking to cancel superseded scans),
`scanResultsLogic.ts`, `stationLookup.ts`, `tablePrefs.ts`,
`taxProfile.ts`, `telemetry.ts` (`trackClientTelemetry`), `tradeHubs.ts`
(canonical hub metadata), `useAuth.ts`, `useEsiFeeImport.ts`,
`useEsiStatus.ts`, `useEveContextMenu.ts`, `useIndustrySharedPrefs.ts`,
`useKeyboardShortcuts.ts`, `useTheme.ts`, `useVersionCheck.ts`,
`utils.ts`.

### 7d. IndustryTab: three concepts

`IndustryTab.tsx` composes three distinct workflows that share state
locally:

1. **Analysis** — single-item build-vs-buy via `analyzeIndustry()`.
   Streaming NDJSON. Result rendered by `IndustryAnalysisResultsPanel`.
2. **Planning (visual builder)** — `planDraftTasks/Jobs/Materials/Blueprints`
   arrays are **local React state only**. Nothing is persisted until
   the user hits "Apply", which calls `planAuthIndustryProject` →
   `db.ApplyIndustryPlanForUser`. Preview via
   `previewAuthIndustryProjectPlan`.
3. **Operations** — reads `ledgerSnapshot` (committed DB state) via
   `getAuthIndustryProjectSnapshot`. The dependency board, task board,
   material diff, and job ledger all read from this snapshot; edits go
   via the granular DB mutators (`updateAuthIndustryTaskStatus[Bulk]`,
   `updateAuthIndustryTaskPriority[Bulk]`,
   `updateAuthIndustryJobStatus[Bulk]`).

Losing the draft is easy — a full-page reload wipes everything not yet
applied.

### 7e. Scanner storage split

`IndustryProfitableScannerPanel.tsx` uses **two** distinct storage
layers with different lifetimes:

- `PARAMS_LS_KEY = "eve-settings:industry-scanner"` in **`localStorage`**
  — persistent user settings: fees (broker/sales tax), system, hub,
  structure/rig config, filters. Written on every param change; survives
  browser restarts.
- `SCAN_STATE_SS_KEY = "eve-flipper:scanner-state"` in **`sessionStorage`**
  — transient scan state: rows, selection, sort, search. Written on every
  state change; survives tab switches inside the Industry workspace but
  **not** page reload or a new browser session.

The scanner shares fees/system prefs with the rest of the Industry tab
via `useIndustrySharedPrefs(SCANNER_PERSIST_KEY)`.

---

## 8. Cross-cutting infrastructure

### EVE SSO scopes

`main.go:160-162` and `main_wails.go:184-186` both declare the scope
list as a hard-coded space-separated string. **The two lists must stay
in sync**. CI doesn't enforce this. The current set:

```
esi-location.read_location.v1
esi-skills.read_skills.v1
esi-skills.read_skillqueue.v1
esi-wallet.read_character_wallet.v1
esi-assets.read_assets.v1
esi-characters.read_blueprints.v1
esi-industry.read_character_jobs.v1
esi-planets.manage_planets.v1
esi-markets.structure_markets.v1
esi-universe.read_structures.v1
esi-markets.read_character_orders.v1
esi-characters.read_corporation_roles.v1
esi-wallet.read_corporation_wallets.v1
esi-corporations.read_corporation_membership.v1
esi-corporations.read_blueprints.v1
esi-industry.read_corporation_jobs.v1
esi-industry.read_corporation_mining.v1
esi-markets.read_corporation_orders.v1
esi-corporations.read_divisions.v1
esi-corporations.track_members.v1
esi-assets.read_corporation_assets.v1
esi-ui.open_window.v1
esi-ui.write_waypoint.v1
```

Adding or removing a scope forces every user to re-authenticate. Flag it
in the PR description.

### Telemetry gating

`internal/telemetry` is off unless `TELEMETRY_ENABLED=1` (see
`LoadConfigFromEnv`). Endpoint defaults to `http://127.0.0.1:13371/v1/events`,
env prefixed `hosted`. Client events (submitted via
`POST /api/telemetry/client`) pass through `sanitize.go`'s allow-list.
Backend `api_request` events include HTTP path (with numeric segments
rewritten to `{id}` via `normalizedTelemetryPath`), method, status,
duration, and coarse IP/country from `CF-*` / `X-Forwarded-For` /
`X-Real-IP` headers.

### Error handling

- Backend: `writeError(w, status, msg)` emits `{error: msg}` JSON.
  Long-running handlers stream `{type: "error", message}` via NDJSON.
- Frontend: `apiFetch` + `handleResponse<T>` normalize; hosted-quota
  responses carry semantic `code` fields the frontend maps to
  actionable UI (`quota_exhausted` → "upgrade / wait until reset",
  `hosted_identity_required` → "log in with EVE SSO",
  `feature_denied` → "not available on your plan").
- `ErrorBoundary.tsx` is the top-level React catch.

### Test layout

Every backend subdirectory follows Go convention — `foo.go` +
`foo_test.go` in the same package. Currently **79 `*_test.go` files**
across the tree. Coverage is measured via `go test ./internal/engine
-coverprofile=engine.cover.out` (matches CI). Wails-only files are
build-tag-guarded — CI runs `go test ./...` without `-tags wails`, so
Wails-specific tests need `go test -tags wails ./...`. Race-detector
runs require `CGO_ENABLED=1` and a C toolchain — `go test -race`
refuses otherwise.

Notable tests that guard invariants:

- `internal/api/hosted_access_test.go` /
  `TestHostedQuotaFeatureMappingClassifiesAllPostAPIRoutes` — asserts
  every POST route is classified.
- `internal/api/server_test.go` — end-to-end handler tests.
- `internal/api/industry_blueprint_scan_test.go` — NDJSON stream
  contract for the profitable-blueprints scanner.
- `internal/db/industry_ledger_test.go` — the `ApplyIndustryPlanForUser`
  replace-mode semantics.
- `internal/db/privacy_test.go` — vault round-trip.

---

## 9. Data-flow diagrams

### 9a. Market scan — from click to result

```
User clicks "Scan" in ParametersPanel
    │
    ▼
frontend/src/App.tsx::handleScan(params)
    │  → invalidateScanRequest(...)  // supersede any in-flight scan
    │  → startScanRequest(...)       // scan token for cancellation
    ▼
lib/api.ts::scan(params, onProgress, signal, onMeta)
    │  → apiFetch("/api/scan", {method:"POST", body:JSON, signal})
    │  → streamNdjson<FlipResult>(...)
    ▼
POST /api/scan  (hostedQuotaMiddleware → "scans")
    │
    ▼
internal/api/server.go::handleScan(w, r)
    │  → s.isReady() gate                     (503 until SDE loaded)
    │  → hostedQuotaMiddleware already ran
    │  → decode ScanParams, resolve system via sdeData.SystemByName
    ▼
internal/engine/scanner.go::Scanner.Scan(params, progress)
    │  → engine.Scanner runs BFS over graph.Universe within buy/sell radii
    │  → per candidate pair, esi.Client.FetchRegionOrders*  (uses scanSem)
    │  → per row, computes profit / margin / L1 depth / route via engine.route
    │  → progress callbacks stream over writeMu-guarded writer (per §3)
    │  → optional: esi.Client.SetMarketOrderRecorder writes snapshots via
    │    db/orderbook.go::RecordMarketOrderSnapshot (bounded by
    │    esi.Client.orderRecorderSem, 4 slots × ~28MB max)
    ▼
handleScan writes {type:"result", data: []FlipResult, scan_id}
    │  → db.InsertHistoryFull + db.InsertResults (via db/results.go)
    │  → processWatchlistAlerts(...) fires Telegram/Discord/desktop via
    │    api/alerts.go::SendAlert
    ▼
streamNdjson resolves; App.tsx setResults(...)
    │  → ScanResultsTable re-renders
    │  → useAchievements.trackAchievementEvent("scan-completed")
```

### 9b. Industry analysis + plan apply

```
IndustryTab: user selects target + params, clicks "Analyze"
    │
    ▼
lib/api.ts::analyzeIndustry(params, onProgress, signal)
    │  → streamNdjson<IndustryAnalysis>("/api/industry/analyze", ...)
    ▼
POST /api/industry/analyze  (hostedQuotaMiddleware → "scans")
    │
    ▼
internal/api/server.go::handleIndustryAnalyze
    │  → s.isReady() + industryAnalyzeMaxBodyBytes (64 KB) guard
    │  → decode IndustryParams  (clamp runs to industryAnalyzeMaxRuns,
    │      depth to industryAnalyzeMaxDepth)
    ▼
engine.IndustryAnalyzer.Analyze(params, progress)
    │  ⚠ per-call state on receiver — not goroutine-safe on same instance
    │  → esi.Client.GetAdjustedPrice / GetCachedMarketPrices via IndustryCache
    │  → esi.Client.GetSystemCostIndex(cache, systemID)
    │  → recursive buildMaterialTree → calculateCosts
    │  → returns IndustryAnalysis (engine/models plus industry.go DTOs)
    ▼
handleIndustryAnalyze writes {type:"result", data: IndustryAnalysis}
    │
    ▼
IndustryTab receives result, user edits into planDraftTasks/Jobs/Materials/Blueprints
    │  (all local React state — nothing persisted yet)
    │
    ▼
User clicks "Preview"
    │  → lib/api.ts::previewAuthIndustryProjectPlan(projectID, patch)
    ▼
POST /api/auth/industry/projects/{projectID}/plan/preview  (→ "scans")
    │  → db.PreviewIndustryPlanForUser  (no writes; returns IndustryPlanPreview)
    │
    ▼
User approves → clicks "Apply"
    │  → lib/api.ts::planAuthIndustryProject(projectID, patch)
    ▼
POST /api/auth/industry/projects/{projectID}/plan  (→ "scans")
    │
    ▼
internal/db/industry_ledger.go::ApplyIndustryPlanForUser(userID, projectID, patch)
    │  Begin tx
    │  if patch.Replace:
    │      DELETE FROM industry_jobs          WHERE user_id=? AND project_id=?
    │      DELETE FROM industry_tasks         WHERE user_id=? AND project_id=?
    │      DELETE FROM industry_material_plan WHERE user_id=? AND project_id=?
    │  if patch.Replace || patch.ReplaceBlueprintPool:
    │      DELETE FROM industry_blueprint_pool WHERE ...
    │  INSERT new tasks, remap ParentTaskID via inputIndexToID / sourceTaskIDToID
    │  INSERT jobs (remapped task refs), materials, blueprint_pool
    │  Commit
    │  → returns IndustryPlanSummary
    ▼
handleAuthPlanIndustryProject → JSON summary
    ▼
IndustryTab clears local drafts → refetches ledgerSnapshot via
getAuthIndustryProjectSnapshot → Operations views refresh from committed state
```

### 9c. Authenticated ESI call (character wallet)

```
Frontend calls e.g. lib/api.ts::getWalletTransactions(...)
    │
    ▼
GET /api/auth/journal/... etc.
    │
    ▼
handler in api/server.go or api/trade_journal.go
    │  → resolve userID via userScopeMiddleware
    │  → look up active session:
    │       sess := s.sessions.GetForUser(userID)
    │  → refresh token if needed:
    │       token, err := s.sessions.EnsureValidTokenForUser(s.sso, userID)
    │           │
    │           ▼
    │       auth/store.go::EnsureValidTokenForUser
    │           │  → load session (auth token decrypted via vault
    │           │    OpenTokenFromStorage — DB privacy codec)
    │           │  → if ExpiresAt within tolerance:
    │           │       auth/sso.go::SSOConfig.RefreshToken(refresh)
    │           │       → POST login.eveonline.com/v2/oauth/token
    │           │       → new access + refresh tokens
    │           │  → re-encrypt via vault ProtectStringForStorage
    │           │  → UPDATE auth_session SET ... (privacy codec runs)
    │           │  → return access token
    ▼
esi.Client.AuthGetJSON(url, token, dst)
    │  → acquires sem (lightweight)
    │  → HTTP GET with Authorization: Bearer <token>
    │  → handles ESI errors (403 → 401/permission, 420/429 → backoff)
    │  → decodes into dst
    ▼
handler serializes to JSON, writeJSON(w, response)
    │
    ▼
Frontend receives typed response (types.ts mirror)
```

---

## 10. Non-obvious pitfalls (the "how did I get burned" list)

1. **`engine.IndustryAnalyzer.Analyze` is not goroutine-safe.**
   The receiver stores per-call mutable state (`adjustedPrices`,
   `marketPrices`, `marketSellOrders`, `marketBuyOrders`,
   `systemCostIndices`). Calling `Analyze` from multiple goroutines on
   the same instance races those fields and produces inconsistent
   results. The profitable-blueprints scanner works around this by
   shallow-copying the analyzer per worker (`localAnalyzer :=
   *analyzer`) at `industry_blueprint_scan.go:1533` and installing
   `SetMarketBooksOverride` / `SetMarketPricesOverride` /
   `SetAdjustedPricesOverride` closures that share a
   `sync.Once`-guarded fetch. **Any new concurrent analysis site must
   do the same.** The shared `SDE`, `esi.Client`, and `IndustryCache`
   are all goroutine-safe.

2. **The Wails build tag rules.**
   `main.go` is `//go:build !wails`; `main_wails.go` is `//go:build wails`.
   CI's default `go test ./...` runs without the tag, so any test
   protected by `//go:build wails` needs `go test -tags wails ./...`.
   Ditto for the desktop build itself:
   `go build -tags "wails,production"`. `main_wails_windows_icon.go`
   and `main_wails_test.go` are wails-only.

3. **The frontend/dist embed requirement.**
   `//go:embed frontend/dist/*` fails at compile time if the directory
   is empty. **Build the frontend first** (`pnpm -C frontend run
   build`). No graceful fallback exists. For UI iteration use the Vite
   dev server which proxies API calls.

4. **SSO scope list duplication.**
   Declared verbatim in both `main.go` and `main_wails.go`. Keep them
   identical. Adding a scope forces users to re-authenticate — mention
   it in the PR description.

5. **The `writeMu` mutex for NDJSON handlers.**
   Any handler that fans work into goroutines while writing to a
   shared `http.ResponseWriter` must serialize every write behind a
   `sync.Mutex` and short-circuit on `<-ctx.Done()` inside the critical
   section (§3, `industry_blueprint_scan.go:1326`). Concurrent
   `flusher.Flush()` on a shared writer corrupts `bufio` state and
   panics; a goroutine that outlives the handler and writes into a
   closed writer panics identically.

6. **`Replace: true` on `IndustryPlanPatch` wipes the entire project's
   tasks/jobs/materials in one transaction** before inserting new
   rows. There is no per-row DELETE endpoint. Removal is via
   replace-mode apply (destructive re-plan) or by setting
   `status = "cancelled"` (keeps the row). Task IDs are re-issued on
   re-insert, so client-side references to old task IDs are stale after
   a replace.

7. **Frontend embed must be rebuilt after every frontend change** for
   `go run .` to reflect the change. Vite's dev server exists for
   exactly this reason.

8. **Locale parity.** Every new string added to `lib/locale/en.ts` must
   also exist in `lib/locale/ru.ts` (Russian can stay terse / use
   English jargon — what matters is the key exists). The compile-time
   guard is `TranslationKey = keyof typeof ru`.

9. **SDE ship packaged volumes** aren't in the SDE dumps — they come
   via ESI. `sde_runtime.go` applies a local cache
   (`data/sde/ship_packaged_volumes.json`) and refreshes any missing
   typeIDs in the background from `esi.Client.TypeInfo`. Wiping this
   cache means the app will refetch on next start.

10. **The Wails backend-port fallback rewrites the SSO callback URL** to
    the fallback port. This *does not* silently keep SSO working — the
    EVE Developer Portal application is registered against a fixed
    callback URL, so the fallback path is essentially "you need to
    close the other Eve Flipper process to sign in". The warning log
    on `SERVER` / `SSO` makes this explicit.

11. **`db.stations.go` is where `esi.Client` gets its L2 station cache**
    via the `StationStore` interface. If you swap the DB backend, you
    also need to satisfy `esi.StationStore.{GetStation, SetStation}` or
    the ESI client's structure-name resolution will hit ESI far more
    aggressively.

12. **Wallet transaction / PLEX dashboard caches are per-Server (not
    per-user).** They key by character id and cache TTL respectively.
    Multi-user hosted deployments should not rely on these for
    isolation — every character sees its own key, but a warm cache
    from a different character can leak nothing (the cache key
    includes character id) — the risk is only latency, not access.

13. **The `data/` directory contains everything mutable** — SQLite
    (`flipper.db*`), SDE extract (`data/sde/`), wiki RAG mirror
    (`data/wiki-rag/`), ship-packaged-volume cache. It lives next to
    the running binary. Wiping it forces a re-download of the SDE
    (several minutes).

14. **`db.go` sets `MaxOpenConns=1`.** SQLite is a single-writer store
    and this is deliberate. Any pattern that expects connection
    parallelism (`errgroup.Go`'ing many DB writes) will serialize.

---

## Appendix — File index

Machine-generated (approximate line counts as of audit time):

- Backend total: ~65k lines under `internal/engine/`, ~13k in
  `internal/api/server.go`, ~17k across `internal/db/`.
- Frontend total: `frontend/src/components/` ~52k lines,
  `frontend/src/lib/` ~7k lines (rough).
- Tests: 79 `*_test.go` files backend-side.

Entry points:

- `main.go` — web/server (default).
- `main_wails.go` — desktop (`//go:build wails`).
- `sde_runtime.go` — ship-packaged-volume prep hooks called from both.
- `main_wails_windows_icon.go`, `main_wails_test.go`,
  `resource_windows_amd64.syso` — Windows / Wails specifics.

Doc collateral (`docs/`):

- `docs/INDUSTRY_ROADMAP.md` — feature roadmap for the Industry tab.
- `docs/ARCHITECTURE.md` — this file.
