# Changelog

## v1.11.0 - 2026-09-11

Today stops being a dashboard and becomes a work order: one ranked queue over
trading, held stock, industry and PI, ranked on the realistic case rather than
the headline, with the price already on your clipboard.

### Today -> the work order

- **One ranked queue instead of a checklist.** Repricing, cancelling, new buy
  orders, listing stock you already hold, collecting finished jobs and
  restarting stalled colonies all land in one list, ordered by what each is
  worth per minute of your attention. A cut line marks where your twenty
  minutes run out and states what is below it, so carrying on is a decision
  rather than a wall of rows.
- Everything is normalised to **ISK over the next seven days**, computed twice:
  the median figure is displayed and the realistic-bad figure is what the queue
  is actually sorted on. Ranking on the median is how a tool ends up
  recommending a 10M opportunity in an illiquid item over a reliable 2M
  reprice.
- The conservative figure was already there and unused --
  `StationTrade.RealizableDailyProfit`, the station forecast's P80 band, and
  `OrderDeskOrder.FlowBasis` all say how much the optimistic number should be
  believed. Nothing here invents a haircut.
- **Repricing is priced correctly for the first time.** The order desk's
  `NetRelistGainISK` is always negative -- moving toward the top of the book
  concedes price on every remaining unit and then pays a broker fee on the
  change -- so it is the *cost* of repricing, not the reward. The value is the
  margin on the units repricing actually unlocks, less what it costs, which
  means an order already at the front of the queue is now correctly reported as
  not worth touching.
- **Only a buy cancel frees capital.** Cancelling a sell order returns stock to
  the hangar, not ISK to the wallet; valuing it as though it released ISK
  floated housekeeping to the top of the queue on money that never arrives.

### Risk and reward, on every row

- Every action carries a grade -- **proven / likely / unproven / avoid** -- built
  from evidence, strongest source first: realized P&L per item from the FIFO
  trade journal, then Trading Edge's reality ratio and `do_not_trade` verdict,
  then the forecast spread, then data completeness, then portfolio risk. Only
  proven and likely reach the queue.
- **Unknown is not zero.** A missing cost basis, an unreadable order book, no
  volume estimate or no price history makes the profit *unknown*, and those rows
  are graded unproven instead of appearing in the queue asserting a number.
- Rows the model held back are listed in a **Held back** panel with the reason
  spelled out -- "your last 14 sales of this lost 3.1M", "no cost basis, so the
  profit is unknown". A filter you cannot inspect is indistinguishable from a
  bug.
- Reward and risk are both on the face of every action: expected, realistic
  case, and the capital exposed if it goes wrong.
- **Quantity is capped by risk, not just capital** -- the smallest of a day of
  flow, a quarter of daily volume, your configured investment ceiling, free
  wallet ISK, the size your own trades have worked at, and portfolio
  concentration. The row names whichever bound applied, so the number is
  explained rather than asserted.

### Doing it, with almost no clicks

- **Run mode** puts one action in front of you with the price already on the
  clipboard. Enter opens the item's market window in the client and re-copies
  the price, Q swaps the clipboard to the quantity, Space marks it done and
  advances, S skips. Nothing is typed and nothing is chosen; prices arrive
  already snapped to EVE's 4-significant-digit grid.
- A refused clipboard is never reported as a successful one -- the panel says
  "click to copy" rather than claiming a price is ready to paste when it is not.
- **Location awareness.** Each action is tagged against where its character is
  docked; ones you can do without undocking lead, and anything elsewhere offers
  a one-click destination instead of leaving you to work out the route.
- Progress persists. Space records the mark, and a reload resumes where you left
  off. Marks are filtered by when they were made, so yesterday's work does not
  come back already crossed out.
- **Bulk hatches** where bulk is faster: every buy in the queue as a
  `Name<TAB>Qty` multibuy paste, and every reprice as a two-column list. Only
  advised rows are included -- a batch is one commitment with no per-row
  decision, so an unproven row in one would bypass the grading entirely.

### Where to put your ISK

- A capital bar showing free / buy orders / inventory / sell orders, with what
  idle ISK is costing you per day stated in ISK, and **return per day** -- the
  compounding rate -- as the headline figure. An unmeasured rate renders as "--"
  rather than 0.00%, which would read as a claim about performance.
- Allocation options quoted on one scale, return per day on the capital they
  need, so station flips, colonies and cash compare directly. Each carries its
  own grade, so a high-percentage option built on unproven rows cannot
  out-argue a modest one built on proven ones. Cash is always listed, at zero:
  it is the baseline the others are measured against.

### Under the hood

- New `GET /api/auth/today` reads a stored plan from one SQLite row, so the page
  paints before anything touches the network. `POST /api/auth/today/refresh`
  rebuilds it over NDJSON with per-source progress, and auto-fires once when the
  server reports the plan has aged past eight hours. A plan older than three
  days is withheld entirely -- a stale paste price is worse than none.
- Ranking and grading are pure Go in `internal/engine/today.go` and
  `today_risk.go`, driven by plain input structs, so the rules are table-tested
  rather than tangled with HTTP.
- The order desk, positions and PI planet payloads were extracted from their
  handlers into reusable builders; each handler is now parse, build, respond.
  Their existing tests are unchanged, which is the proof the extraction was
  behaviour-preserving.
- Deep links: Today's rows carry the tab and row they came from, and the Orders
  tab expands and scrolls to the order you arrived for.
- `POST /api/auth/today/refresh` needs a character and says so with a 401 before
  the stream opens. Once NDJSON headers are written the status is 200 whatever
  happens next, and a client cannot tell "log in" from "the plan failed".
- Migration v45 adds `today_plan` and `today_action_state`. The second is not a
  cache: alongside done/skip it records what the plan *promised* at the moment
  you acted, because the plan is replaced on the next refresh and that is the
  only chance to write it down.

### Known limits

- Buy candidates still come from your last saved Station Trade scan rather than
  a scan of Today's own, so their prices are as fresh as that scan.
- Row prose -- the headline, the reason, the evidence, the blockers -- is
  generated server-side in English. Only the chrome around it is translated.
- Timing is still the existing ETA in days. The order desk computes a
  day-of-week volume profile internally and does not yet publish it.

## v1.10.2 - 2026-09-11

The Trade Journal answers "what did that sale actually make me?" one row at a
time, sales tax and broker fees follow the skills you have trained rather than
the ones you had when the numbers were first imported, and an API response that
cannot be encoded says so instead of arriving empty.

### Journal -> Transactions (new view)

- A third view in the Trade Journal listing every matched sale in the window,
  newest first: date, item, source, unit buy and sell, units, total buy and
  sell, broker fee on each side, sales tax, margin and net profit. The raw
  Transactions tab under a character is a wallet dump with no notion of profit,
  which is why it told you nothing the in-game wallet does not.
- The engine already computed all of it. Every one of those columns was on the
  per-sale lot the matcher produces; the only thing missing was reach, because
  `/api/auth/journal/lots` refused to answer without a `type_id`, so those rows
  existed only inside one item's drawer. Widening that endpoint rather than
  adding a second one means a figure in the list and a figure in the drawer are
  the same matcher's answer by construction.
- Filter by item name and by minimum profit, sort on any column, page at 40 /
  100 / all, export the whole current sort to CSV. The minimum-profit filter is
  on absolute value: a 4M loss moved the needle as much as a 4M win.
- Build rows are priced on the same cost basis as flips (install + materials
  divided by produced quantity), so a manufactured sale and a bought-and-flipped
  sale are comparable in one column.
- An unmatched sell shows "--" for cost and margin, not 0%. A sale we cannot
  price is not a break-even sale, and rows with no value sort last in both
  directions rather than settling into the middle of a profit ranking.
- Margin is `net profit / cost`, the same ROI the item leaderboard shows for
  that item. Note this reads a few points below eve-tycoon, which divides by
  total spend including sell-side fees.

### Fixed: fees frozen at an old skill snapshot (8% at Accounting V)

- The Trade Journal charged 8.00% sales tax and 3.00% broker fee to a character
  with Accounting V and Broker Relations V trained, where the real rates are
  3.60% and 1.50%. The formula was right and never consulted: once both rates
  were stored in config, fee resolution returned them and stopped -- and the
  stored values were an "import fees from ESI" snapshot taken at a moment when
  the skill sheet came back empty.
- A stored rate that exactly matches what the skill formula produces at some
  level is now treated as a snapshot and recomputed live from your skills. A
  rate that matches no level -- a 2.5% citadel broker fee you typed in -- is a
  deliberate choice and still wins.
- An empty ESI skill sheet is now an error rather than "level 0 in everything",
  which is what let the bad snapshot be written in the first place.
- Assets -> Positions resolved its fees by a separate path that never saw
  skills; it now goes through the same resolver, so a position and a journal row
  cannot quote different fees for the same character.
### Fixed: illegible "invalid JSON" errors from the API

- `writeJSON` discarded the encoder's error. `json.Encoder` marshals into a
  buffer before it writes, so an unmarshalable payload — in practice a NaN or
  ±Inf `float64` out of a division — sent HTTP 200, `Content-Type:
  application/json` and **zero bytes**. The browser reported only a JSON parse
  error naming no endpoint, and nothing at all reached the server log. Both
  `writeJSON` and `writeJSONStatus` now encode into a buffer first and turn a
  failure into a logged 500 with a real message.
- Float query parameters written as `if f < min || f > max` accepted NaN, since
  every comparison against NaN is false and Go's `ParseFloat` accepts the
  literal `"NaN"`. Fixed in the order-disposition, journal-fee-override and
  journal-lots parsers, which then fed the NaN into the response.
- A 2xx whose body will not parse now raises an error naming the route and
  status instead of leaking a bare browser `SyntaxError`.

## v1.10.1 - 2026-09-11

A maintenance release on top of the interface release: the Trade Journal is
readable end to end, cumulative profit is drawn as a line rather than as bars,
the item leaderboard can hide rows that netted nothing, and two stalls that
made the app look hung are gone.

### Trade Journal

- The page scrolls. It was a fixed-height flex column in which only the inner
  panels could scroll, so tall content was squeezed instead of pushing the page
  down -- which is what clipped the leaderboard off the bottom of the screen.
- Cumulative profit is a line chart, with combined, trading and manufacturing
  overlaid on one set of axes by default and a Split toggle that puts them in
  three panels side by side. The choice is remembered. A bar chart draws each
  day as an independent quantity, which a running total is not; a reader
  comparing bar heights was comparing two totals that share every day but the
  last.
- Tooltips name the two numbers separately -- the day's profit and the running
  total -- so the same calendar day reading +129M on a 30-day chart and -55M on
  a 7-day one is legible as what it is: a profitable month containing a losing
  week, not a bug.
- Charts no longer overlap their own axis labels. The Y-axis ticks now sit in a
  gutter inside the chart instead of spanning the whole component and landing on
  the date row -- harmless at full width, unreadable once three charts sat side
  by side.
- Drawdown bars were drawn from `cumulative_pnl` while the scale came from
  `drawdown_pct`, so ISK values were rendered against a percentage axis. Both
  now read through one accessor.

### Item leaderboard

- New panel in Journal -> Analytics: winners and losers of the period side by
  side, ranked, with a rank number, an ISK/ROI switch and a TRADE / BUILD /
  T+B chip saying where each item's profit came from. It replaces a flat
  top-20 that showed one side at a time, so "what am I losing money on" was a
  mode switch away. ROI ranking sets aside rows with a cost basis under 1M ISK
  and says how many: a 900% return on a 40k flip is not a finding.
- New Hide flat toggle, on by default. Exact zeros were already excluded, but an
  item bought and sold at the same price still nets a few ISK of fee residue and
  renders as a bare "0"; rows under 1K ISK either way are now held back and
  counted in the footer rather than padding the board.
- Flat rows are counted before the ROI cost-basis floor, so the two footer
  counts describe disjoint sets and cannot double-count a row.

### Navigation

- Workspace rail order is now Today, Trade, Assets, Journal, Industry, Intel.
  Industry sat second, ahead of the two workspaces used far more often. Existing
  per-tab ordering preferences are untouched -- this is a workspace-level
  reorder.

### Fixed: nine-minute stalls during orderbook cleanup

- Cleanup batched by snapshot count, which hides an unbounded row count: a
  snapshot of a busy region carries ~100k level rows, and 100 snapshots once
  became 11.5M row deletes in a single transaction. That held the one SQLite
  connection for nine minutes -- every authenticated request queued behind it,
  the app looked hung -- and grew the WAL to 1.5GB. The deadline check could not
  help, because it sat between batches and was first consulted after the
  indivisible batch had finished.
- Deletes are now chunked by level rows (20k per transaction) with the snapshot
  row retired in the same transaction as its last chunk, so each commit is
  small, the deadline has a seam to take effect in, and a part-drained snapshot
  never leaves `level_count` over-reporting.

### Fixed: /debug/pprof answered with the SPA

- Profiler URLs fell through to the single-page-app fallback, so every request
  returned index.html with a 200 -- indistinguishable from working until you
  tried to read the dump. This is why the last stall had to be diagnosed from OS
  counters instead of a goroutine profile.

### Fixed: spurious "ESI unavailable" popup on server installs

- The full-screen "EVE Online servers are unavailable" overlay could appear on
  Docker/Unraid installs while ESI was perfectly healthy. Any failed
  `/api/status` request — a dropped LAN connection, a laptop waking from sleep —
  counted toward the ESI-down verdict, with no time window and no guard against
  overlapping polls. Once the status endpoint got slow, requests piled up and a
  single blip rejected the whole pile at once, tripping the threshold instantly.
- A failed request to the app's own backend is now reported as its own
  condition (a distinct status-bar state) instead of being blamed on CCP.
  Declaring ESI down needs both a failure streak and 15s of wall clock.
- `/api/status` is polled once for the whole app rather than once per consumer,
  never with two requests in flight, and with a request timeout.
- `esi.Client.HealthCheck` no longer holds its write lock across the ESI round
  trip, so concurrent `/api/status` requests can't serialize behind a network
  call. A rate-limited reply (420/429) counts as reachable — it proves the
  network path works — instead of blanking the UI.
- Health-probe failures are now logged and returned as `esi_error`, visible in
  the status bar tooltip and the overlay. This was the only ESI call in the
  codebase that failed silently, which is why the container logs showed nothing.
- File logging falls back to `$HOME` when the binary's own directory is
  read-only, so the distroless container writes a real logfile to the `/data`
  volume instead of warning `permission denied` and disabling file logs.

## v1.10.0 - 2026-09-10

The interface release. Navigation is a workspace rail instead of one long tab
bar, tables lead with the columns you decide on and put the rest in a row
drawer, eleven tools that were reachable only by clicking your character
portrait are now in the navigation, and the two profit-and-loss screens that
disagreed with each other are one screen that does not.

### Navigation

- Tabs are grouped into six workspaces on a left icon rail — Today, Trade,
  Industry, Assets, Journal, Intel — with the tabs inside the active workspace
  as a second row that only appears when there is more than one. Your saved tab
  order and hidden tabs still apply; the active workspace is derived from the
  active tab, so every existing shortcut and command-palette entry still works.
- **Tabs are no longer all mounted at once.** A tab's contents are built the
  first time you open it and torn down when you leave. Previously every tab was
  laid out on every frame with the inactive ones merely hidden, which in a real
  session measured 44,773 elements and left Chrome over two minutes to draw a
  single frame. A 100-row Station Trade scan now peaks at 1,610 elements and
  settles back to around 300; no workspace exceeds ~400. Industry is the one
  exception, kept alive so an unsaved plan draft survives navigation.
- New **Today** workspace, first on the rail and the landing screen on a cold
  start (a saved tab still wins). A status strip — capital in orders, open
  orders, needs reprice, needs cancel — then a numbered routine that ticks off
  as counts hit zero, a buy sheet of the top 25 candidates by realistic daily
  profit, and a sell sheet of your outbid sell orders with the price to paste.
  Existing orders come before new ones, because repricing earns more than a new
  order at a fraction of the broker fee. It reads your last scan rather than
  running its own, so the screen is instant.

### The character tools are in the navigation

- Nine tools that existed only behind a portrait click and a dialog tab strip
  are now workspace tabs: Orders, Transactions, Wallet, Industry Jobs, Planets,
  Risk, Optimizer, Trading Edge and PLEX. The tools themselves are unchanged —
  only where they live.
- The character dialog now holds what is actually about the character: Overview,
  add/remove/scope, Achievements and the security vault.
- Character scope and the shared character fetch moved above the workspace, so
  the dialog and all the promoted tabs share one request instead of refetching
  each time you open the dialog. The scope picker rides in the tab strip.

### Assets → Positions (new)

- A new tab answering one question: should I sell this today? Item, quantity,
  average cost, price now, unrealized, age — with a List action and a drawer for
  the underlying lots, target price, fee breakdown and manual edit/delete.
- Rows come from the FIFO open positions the trade journal already derives from
  real ESI transactions, plus hand-entered rows for stock the engine cannot see
  (loot, contract buys, corp transfers, anything older than the wallet window).
  A manual entry for a type that also has FIFO history stays a separate row
  rather than being averaged in, so cost bases stay honest.
- Unrealized is net of broker fee and sales tax on the sell side. A position is
  not in profit until it clears fees, and a gross figure in the one column you
  act on would be misleading.

### Tables

- Station Trade, Flipper, Regional Trade, Contracts and the order desk now lead
  with six decide-on-it columns and put everything else in a row drawer opened
  by clicking the row (20→6, 35→6, 16→6 and 11→6 respectively). Removing a
  column never removes its sort.
- Station Trade's filters are tabbed rather than one long stack.
- Industry lost a nav row: the Discover source picker no longer sits inside the
  scroll container it controls.
- The scanner shows real blueprint art. Blueprints do not serve the plain icon
  endpoint, so they used to render as blank cells.

### One accounting surface

- **The P&L tab is gone, folded into Trade Journal.** It was a second FIFO
  engine over the same transactions — one that could not see industry jobs, so
  an item you built and sold read there as a zero-cost windfall while the
  Journal priced it correctly. Its panels are the Journal's new **Analytics**
  view, reached from a Summary | Analytics switch, with a Trading /
  Manufacturing / Combined selector that drives every figure on the tab
  including Sharpe, drawdown and profit factor. A saved layout naming the old
  tab drops it silently; no migration needed.
- Every derived statistic — daily series, per-item, per-station, Sharpe,
  drawdown, Calmar, profit factor, expectancy — is now computed in exactly one
  place, so the two views cannot report different numbers for the same trades.
  A test asserts the two engines agree field for field.
- Manufactured goods now carry a real cost basis into P&L: install cost plus
  materials divided by runs, matched as a lot like any purchase.
- **Broker fee fix.** The Trade Journal charged a hardcoded 1% broker fee and
  never read your configured rate, so every journal figure understated an
  untrained broker fee by roughly two thirds. Rates now resolve from your
  config, else from the scope's Accounting and Broker Relations skill levels,
  else from defaults — and the tab states which of those it used, with a session
  override. **Your journal numbers will change if your configured broker fee is
  not 1%.**

### Orders

- The order desk absorbed the duplicate order screen from the character dialog,
  gaining order history and undercut status, plus the order-book price ladder in
  the row drawer (fetched once, lazily, the first time you open a row).
- Active / History sub-tabs. Order history was previously reachable only inside
  the dialog.

### PLEX

- PLEX is a real tab in the Assets workspace. It never needed a character — the
  whole dashboard is public market data, so the dialog was only hiding it.
- The arbitrage matrix is three tabs keyed off the path type — NES / Market /
  Spread — each stating its model and a viable count that excludes no-data paths
  from the denominator, so a failed fetch no longer reads as a dead market.
- **The spread table was one column out of register.** Its header declared five
  columns while its rows rendered six, so every spread number sat under the
  wrong label.
- **"NES Arbitrage" was mislabelled for one of its rows.** Buying a Skill
  Extractor off the market, extracting and selling the Injector involves no New
  Eden Store purchase at all, but was filed under the heading that says you must
  spend real money.
- Spread plays now read in market-making terms — Buy Order / Sell Order / Raw
  Spread — rather than "cost / revenue", which is the wrong frame for placing
  two orders. The unreachable second PLEX dashboard that had these ideas was
  deleted; the ideas moved here.

### Ivy AI local models

- Ivy AI can now run against a local OpenAI-compatible model server — Ollama,
  LM Studio, Unsloth Studio / vLLM, or any custom endpoint — instead of
  OpenRouter. With a local provider selected, no prompt or scan context leaves
  the machine.
- Added a provider picker, an editable base URL, and a "Refresh models" button
  that lists what the local server is actually serving (`POST
  /api/auth/station/ai/models`). The custom-model field still works as a
  fallback.
- The API key is now optional for local providers and OpenRouter-specific
  request details (attribution headers, `stream_options`) are no longer sent to
  them. Local requests get much longer timeouts to suit CPU inference.
- Base URLs are restricted to loopback, RFC1918/ULA and `host.docker.internal`,
  with every resolved address checked; link-local metadata addresses are
  refused. Local providers are disabled on hosted deployments unless
  `STATION_AI_ALLOW_LOCAL_PROVIDERS=1` is set.

## v1.6.6 - 2026-06-08

This release introduces the local security vault, expands encrypted storage for sensitive local data, and tightens desktop/web API boundaries.

### Security Vault and Local Privacy

- Added the Security Vault setup and unlock flow with standard machine-protected storage and optional private passphrase mode.
- Purged legacy plaintext EVE auth sessions during vault setup so new logins are stored through the selected vault.
- Encrypted EVE auth tokens, sensitive config secrets, paper trade notes/source, wallet journal text fields, industry project/job notes, cockpit payloads, current wallet balance, and current total SP.
- Migrated legacy plaintext private fields into vault-protected storage where possible, including old current wallet balance values.
- Added a profile encryption chip so the active vault mode is visible near the character name.

### App and API Hardening

- Added security vault API endpoints and passphrase unlock coverage.
- Restricted unsigned user-id headers to the desktop flavor, added state-changing request origin checks, body limits, and common security headers.
- Allowed packaged Wails desktop origins through the vault setup CORS/origin guard so production desktop users can choose a vault mode.
- Improved startup/security modal behavior so the loader no longer covers the vault popup and users can continue after vault setup without getting stuck behind a forced auth screen.

### Trading and Workflow Updates

- Added Trading Edge character popup wiring and related backend API support.
- Improved contract, station trading, cockpit, and station AI workflows with additional UI/API model support.

### Tests

- Added coverage for vault setup, private passphrase unlock, legacy plaintext migration, encrypted private fields, current wallet balance/SP privacy, origin checks, and related archive behavior.

## v1.6.5 - 2026-05-29

This is a maintenance release focused on stability fixes after v1.6.4.

### ESI and Private Structures

- Fixed an infinite player-structure name lookup loop when ESI returns `403 Forbidden` for private or inaccessible Upwell structures.
- Added negative caching for inaccessible structure lookups so the app does not retry the same forbidden structure repeatedly.
- Added global cooldown handling for ESI `420/429` structure-name rate limits to prevent request storms across many structure IDs.
- Limited concurrent player-structure name resolution in structure prefetch and system-structure discovery paths.
- Prefer EVERef structure names before authenticated ESI lookup when a public fallback name is already available.
- Applied the same structure lookup suppression to structure detail resolution used by private/corp structure selectors.

### Tests

- Added regression coverage for forbidden structure suppression, global rate-limit suppression, and EVERef fallback behavior.

## v1.6.4 - 2026-05-17

This release expands Eve Flipper into a configurable trading cockpit and adds several community-requested intelligence and diagnostics tools.

### Re-upload Note

- Re-uploaded the v1.6.4 release build on 2026-05-18 to include small bug fixes found after the initial release.
- Fixed light-mode shell styling where the app frame/header could stay black while the rest of the UI used the light palette.
- Fixed PI planet detail decoding when ESI returns route quantities as integer-valued decimals such as `20.0`.
- Included frontend security dependency patches for Vite and PostCSS.
- Migrated frontend tooling to Node.js `24+` and pnpm for more reproducible installs and CI builds.

### Cockpit Engine

- Added the Cockpit Interface settings panel behind the header gear button.
- Added persistent cockpit loadouts for navigation, density, visible panels, quick actions, columns, filters, and startup view.
- Added profile presets for Station Trader, Regional Hauler, Industry Builder, Ledger/Accountant, New Player, and Power User workflows.
- Added per-tab layout settings for Scanner, Regional Trade, Station Trading, Route Builder, Industry, Ledger, and related tools.
- Added import/export support for cockpit profiles as JSON.
- Added shareable cockpit packs and a remote JSON community layout gallery.
- Added built-in workspace templates as local fallback when the remote gallery is unavailable.
- Added role-aware cockpit bindings so different characters can switch to different workspaces.
- Added context/adaptive cockpit hints and quick action configuration.
- Added a compact command palette for fast navigation and common actions.

### Item Intelligence

- Added an Item Intelligence modal with item search, market depth context, history signal, owned stock, active orders, and personal trading context.
- Added reusable item intelligence links from trading tables and top-level navigation.
- Added item-level diagnostics to connect market data with personal assets, orders, and journal history.

### Regional Trade Diagnostics

- Added Regional Trade diagnostic mode for checking rejected or negative opportunities.
- Added clearer visibility into market data status, source/destination prices, margin, and filter rejection reasons.
- Improved nullsec/private-structure troubleshooting by making missing or weak destination data easier to identify.

### Ledger, Watchlist, and Character Data

- Added graph tooltips and clearer date range display for Ledger capital/cashflow/P&L charts.
- Added PI planets tab in the character popup with ESI-backed planet data and MVP production/profit context.
- Added reusable tax profile editor as a shared source for fee/tax configuration across modules.
- Improved Watchlist alert trigger handling and UI flow.

### Industry and Structures

- Improved custom/private structure resolution for Industry where ESI/ACL data is available.
- Added industry structure awareness hooks for corporation/private station workflows.
- Added additional SDE and ESI support for PI and industry-oriented item metadata.

### UI and UX

- Moved PLEX+ out of the main navigation and into the character/profile popup as secondary information.
- Added wider/fullscreen modal support for large workflows.
- Improved light-mode contrast for warning/info states.
- Standardized tabs, filters, and action layout across major workflows.
- Fixed multiple cockpit/layout overflow cases in the header, settings panels, profile presets, workspace gallery, and station trading table tools.

## v1.6.3 - 2026-05-15

This release focuses on wallet history reliability, route responsiveness, DOTLAN navigation, achievement expansion, and removing legacy desktop code.

### Wallet Archive and Ledger Reliability

- Added local wallet transaction and journal archive storage.
- Added incremental wallet sync so future rows are preserved locally once seen.
- Added archive fallback when live ESI wallet calls fail or return rate limit errors.
- Added archive coverage metadata including live rows, archived rows, coverage days, and last sync information.
- Improved Ledger graph clarity so fixed date ticks are not mistaken for missing history.

### Route and DOTLAN

- Added DOTLAN route opening support from route workflows.
- Added route history counters for DOTLAN/navigation-related achievement tracking.
- Changed hauling gank-risk scoring to capped best-effort work with timeouts and partial results so slow zKillboard responses do not block route scans.
- Added tests for route risk timeout behavior.

### Achievements

- Added advanced ledger, audit, DOTLAN, archive, and discipline achievements.
- Added new achievement glyph assets and localized EN/RU achievement text.
- Added classified/hidden achievement handling for unrevealed achievements.

### Backtest and Mission Control

- Improved Paper Backtest result diagnostics and historical snapshot replay handling.
- Improved Mission Control expected-vs-actual and journal integration details.
- Added refinements to station and route execution wording around fill assumptions and quantity constraints.

### Cleanup

- Removed the legacy Tauri shell and vendored Tauri/Rust desktop files.
- Kept the Wails desktop path as the supported desktop runtime.
- Disabled wiki RAG autostart in API tests to keep CI cleanup stable.

## v1.6.2 - 2026-05-09

This release turns the new execution workflow into a full decision-support layer: plan the trade, record it, reconcile the result, and track progress through achievements.

### Mission Control

- Added Trade Execution Autopilot / Mission Control for scanner, route, and station-trading rows.
- Added depth-aware executable quantity, gross spread, net per-unit math, fees/taxes, worst-case PnL, and quantity-reduction diagnostics.
- Added capital constraints including max ISK per trade, wallet reserve, and max item exposure.
- Added station-trading order variants for fast fill, safer spread, and max ISK/hour.
- Added route execution planning with ship profile, cargo capacity, trips, execution minutes, safety delay, and ISK/hour modes.
- Added one-click journal trade creation from execution plans.

### EveLedger and Paper Backtest

- Added EveLedger-style wallet/cashflow dashboard with income/outgoing views, journal categories, trading PnL separation, inventory mark-to-market, and capital curves.
- Improved Paper Backtest diagnostics beyond PnL, including fill assumptions, open MTM controls, instant-flip simulation, and recorded orderbook snapshot replay when local data exists.
- Added clearer expected-vs-actual reconciliation data for planned trades.

### Achievements

- Added the achievement system with persistent SQLite progress, unlock state, seen state, and event tracking.
- Added achievement library inside the character popup with categories, rarity, progress bars, locked/classified states, and EN/RU localization.
- Added animated achievement unlock toasts and reusable badge/icon components.
- Added achievement events for scans, Mission Control, journal creation, reconcile, backtests, route checks, and industry analysis.

### Fixed

- Fixed a Wails desktop startup collision where an already-running local backend on `127.0.0.1:13370` could make a release build talk to the wrong process and display `dev`.
- Wails desktop builds now use a relative API base and proxy API calls through the Wails asset server to the backend instance started by the current desktop process.
- Desktop backend startup now binds the listener before readiness checks, preserving `13370` when available and falling back to a free local port instead of accepting another process as ready.
- Fixed concurrent achievement unlock writes that could return SQLite `database is locked` during bursty UI event tracking.
- Fixed Station Trading empty-state text so it no longer looks like a scan is running before the user starts one.

## v1.6.1 - 2026-05-04

This release focuses on making Eve Flipper less optimistic on paper and more useful for real execution decisions.

### Market Scanning

- Fixed inflated profit reporting after depth and slippage calculations.
- Added stricter handling for partial or broken ESI data so bad pages are less likely to create false opportunities.
- Added execution-aware liquidity, fill-rate, fill-time, and confidence signals.
- Improved target-market restriction handling between frontend and backend.
- Added character-aware enrichment from active orders and assets in trading views.

### Route Trading

- Reworked route execution math toward deeper VWAP-style liquidity instead of only top-of-book pricing.
- Added route execution estimates for cargo trips, travel time, safety delay, ISK/hour, and route mode sorting.
- Added hauling and gank-risk signals including route danger, recent kills, and hot-zone warnings.
- Added courier/collateral risk fields for hauling-oriented route evaluation.

### Paper Backtest and Trade Journal

- Added the Paper Backtest popup with configurable hold/instant flip modes, entry cadence, volume limits, price assumptions, fees, ROI filters, and chart output.
- Added instant-flip simulation for repeated buy-haul-sell opportunities with cooldown control.
- Added orderbook snapshot storage, coverage reporting, cleanup/stats, and recorded snapshot replay support.
- Added Paper/Live Trade Journal foundation with manual entries, scanner-row drafts, live ESI drafts, reconciliation, and suggested status patches.

### Portfolio, Wallet, and Risk

- Improved realized PnL matching so unmatched sells are not treated as zero-cost profit in strict API mode.
- Added portfolio optimizer support for wallet balance, active orders, assets, exposure, and runtime warnings.
- Added wallet/cashflow dashboard foundations for income, outgoing, inventory mark-to-market, and category views.
- Fixed empty transaction handling so P&L shows an empty state instead of an error when ESI returns no transactions.

### Industry

- Improved industry analysis with depth-aware material buying and clearer sell modes.
- Added reaction and invention-oriented analysis inputs.
- Added character-aware industry coverage for owned materials and blueprints.
- Added industry project execution planning, task/job status controls, material rebalancing, blueprint sync, and coverage-aware ledger draft generation.
- Added active industry job sync from ESI into the character industry workflow.

### Updates and Release Safety

- Auto-update now requires SHA256 checksum verification before replacing the local binary.
- GitHub release workflow now publishes `SHA256SUMS.txt` for release assets.
- Added tests for checksum selection and parsing.

### Known Limits

- Historical orderbook replay only becomes meaningful after enough local snapshots have been recorded. ESI does not provide old orderbook depth retroactively.
- Route execution planning now includes core time/risk/cargo fields, but full ship-specific navigation remains an area for future tuning.
