# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

EVE Flipper is a local-first market-intelligence and industry-planning app for EVE Online. A single Go binary embeds a React/TypeScript SPA, talks to ESI directly from the user's machine, stores data in SQLite under `data/`, and serves the UI on `localhost:13370`. There is no hosted backend — every install runs the same binary, with optional EVE SSO for character-aware features.

Two runtime flavors built from the same backend:
- **Web/server** (default build, no tag) — Go binary serves the SPA + API. Entry: `main.go` (`//go:build !wails`).
- **Wails desktop** (build tag `wails`) — same backend started in-process, fronted by a Wails window. Entry: `main_wails.go` (`//go:build wails`). When the preferred port `13370` is busy, the Wails build falls back to a free port and rewrites the ESI callback URL.

## Common commands

```bash
# Backend dev loop (frontend must be built first because main.go embeds frontend/dist/*)
corepack pnpm -C frontend install --frozen-lockfile
corepack pnpm -C frontend run build
go run .

# Frontend dev with HMR (proxies API to a separately-running `go run .`)
corepack pnpm -C frontend run dev          # vite on :5173

# Tests
go test ./...                              # full Go suite
go test ./internal/engine -coverprofile=engine.cover.out  # coverage (matches CI)
go test ./internal/api/ -run TestName -count=1 -v         # single test, no cache

# Wails desktop build
corepack pnpm -C frontend run build:wails
go build -tags "wails,production" -ldflags "-s -w -X main.version=dev" -o build/eve-flipper-desktop .

# Frontend production build (tsc -b then vite build — TS errors block the build)
corepack pnpm -C frontend run build
```

PowerShell equivalents live in `make.ps1` (`.\make.ps1 build`, `.\make.ps1 wails`, etc.). Unix make targets in `Makefile` (`make build`, `make test`, `make cross`).

**Race detector requires cgo** (`CGO_ENABLED=1` + a C toolchain). `go test -race` will refuse to run otherwise.

## Architecture

### Backend layout (`internal/`)

- **`api/`** — `Server` struct holds every long-lived dependency (SDE, ESI client, DB, SSO config, session store, scanner, industry analyzer, demand analyzer, gank checker, etc.). `server.go` (~12k lines) registers HTTP routes via `net/http`'s 1.22+ method-prefixed mux (`mux.HandleFunc("POST /api/...")`) and contains most handlers inline. Long modules split into companion files (`industry_blueprint_scan.go`, `character_market_fees.go`, `hosted_access.go`, ...). The Server is constructed in `NewServer`; SDE is loaded asynchronously and attached later via `SetSDE`.
- **`engine/`** — pure-Go calculators (no HTTP, no DB). `Scanner` for market scans, `IndustryAnalyzer` for build-vs-buy/invention math, `PortfolioOptimizer`, route execution, etc. Engine types are JSON-tagged because they cross the wire directly.
- **`esi/`** — ESI HTTP client with internal connection pooling, rate-limit semaphore, structure-name caching (`structureSystems`, `structureTypes`, `structureNameFailures`), and an `IndustryCache` for adjusted prices / system cost indices. Public structure names are bootstrapped from EVERef on startup; authenticated structure details fall back to `/universe/structures/{id}` with negative caching for 403s.
- **`db/`** — SQLite (modernc.org/sqlite, pure-Go, no cgo) wrapped in a small `*DB` type. Schema migrations live in `db.go` and run on `Open`. Privacy-codec (auth vault) is plugged in via `SetPrivacyCodec` after `auth.SessionStore` is built. Industry-ledger persistence lives in `industry_ledger.go` (`ApplyIndustryPlanForUser` is the canonical write path — `Replace: true` wipes tasks/jobs/materials in one transaction).
- **`auth/`** — EVE SSO state, refresh-token storage, per-user session store with a security vault. ESI tokens never leave the local DB.
- **`sde/`** — Static Data Export loader. Loaded asynchronously after startup; `Server.isReady()` gates handlers until SDE is in. The loader writes parsed maps (`Types`, `Stations`, `Systems`, `Regions`, `Industry.Blueprints`, `SystemByName`) onto a `*sde.Data` that the rest of the code reads by pointer.
- **`telemetry/`, `corp/`, `gankcheck/`, `zkillboard/`** — optional integrations gated on env / scopes.

### Frontend layout (`frontend/src/`)

React 19 + TypeScript 5 + Vite + Tailwind. Single SPA with tab-based navigation; each major feature is a tab (`StationTrading`, `IndustryTab`, `PlexTab`, `WatchlistTab`, etc.) under `components/`.

- **`lib/api.ts`** — every API call. NDJSON streaming helper `streamNdjson<T>` for scan-shaped endpoints (`/api/scan`, `/api/industry/analyze`, the profitable-blueprints scanner). Long-running analyzers use NDJSON with `{type: "progress" | "result" | "error"}` messages.
- **`lib/i18n.tsx`** — `TranslationKey = keyof typeof ru`, so **every key must exist in both `lib/locale/en.ts` AND `lib/locale/ru.ts`** or the frontend build fails type-checking. The PR template enforces this; adding an EN-only key is a compile error.
- **`lib/types.ts`** — shared TypeScript types matching the Go JSON tags. When adding a backend field, mirror it here.
- **`components/industry/`** — Industry tab subtree. Three big concepts that share state in `IndustryTab.tsx`:
  - **Analysis** — single-item build-vs-buy via `analyzeIndustry()`.
  - **Planning (visual builder)** — `planDraftTasks/Jobs/Materials/Blueprints` arrays. **Local React state only**, not persisted until "Apply" is clicked.
  - **Operations** — reads `ledgerSnapshot` (committed DB state) via `getAuthIndustryProjectSnapshot`. The dependency board, task board, material diff, job ledger all read from this snapshot.
- **Scanner state persistence** — `IndustryProfitableScannerPanel` saves transient scan state (rows, selection, sort, search) to `sessionStorage` so tab switches don't wipe it. Params (fees, system, hub) live in `localStorage`.

### How frontend embedding works

`main.go` and `main_wails.go` each have `//go:embed frontend/dist/*`. **The frontend must be built before `go run .` or `go build`** or the embed fails with "pattern frontend/dist/*: no matching files found". Iterating on UI without rebuilding requires the vite dev server (`pnpm run dev`).

### IndustryAnalyzer concurrency caveat

`engine.IndustryAnalyzer.Analyze` stores per-call mutable state (`adjustedPrices`, `marketPrices`, `marketSellOrders`, `marketBuyOrders`, `systemCostIndices`) on the receiver. Calling it from multiple goroutines on the **same** analyzer instance races these fields and produces inconsistent results. The Profitable Blueprints scanner works around this by shallow-copying the analyzer per worker goroutine (`localAnalyzer := *analyzer`). When fanning out new concurrent analyses, do the same — the shared SDE / ESI client / IndustryCache are all goroutine-safe.

### Hosted-quota classification

`internal/api/hosted_access.go::hostedQuotaFeatureForRequest` requires every POST `/api/...` route to be classified as `"scans"`, `"station_ai"`, or explicitly returned `("", false)` for unmetered. `TestHostedQuotaFeatureMappingClassifiesAllPostAPIRoutes` enforces this — new POST endpoints must be added to one of the switch cases or the API test suite fails.

### ESI scopes

Both `main.go` and `main_wails.go` declare the SSO scope list. They must stay in sync (CI does not enforce, but the SSO config is duplicated by design — keep both files updated when adding/removing scopes). Adding a new scope means users must re-authenticate; flag this in any PR description.

## Conventions worth knowing

- **Don't update `assets/badges/*`** — those are auto-generated by the upstream release pipeline; they conflict on every fork rebase. Skip those commits.
- **Locale parity is mandatory.** Add new strings to both `en.ts` and `ru.ts`. Russian translations can stay terse / use English jargon — what matters is the key exists.
- **Build tags matter for tests.** Wails-only files are guarded by `//go:build wails`; CI runs `go test ./...` without the tag, so Wails-specific tests need `go test -tags wails ./...`.
- **`Replace: true` on `IndustryPlanPatch` wipes the entire project's tasks/jobs/materials** in one transaction before inserting. There is no per-row DELETE endpoint as of now — removal is via replace-mode apply, or by setting `status="cancelled"` (which keeps the row).
- **NDJSON-streaming handlers must serialize writes to `http.ResponseWriter`.** Concurrent `flusher.Flush()` calls on a shared writer corrupt `bufio` state and panic. See the `writeMu` mutex pattern in `industry_blueprint_scan.go`.
