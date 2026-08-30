# EVE Flipper — Industry Roadmap

Living plan for the industry module. Organized around the pilot's actual
workflow, not around code modules. Every item names which workflow it
serves and which audit question it answers.

Owner: the pilot. Not a spec — a checklist we can tick off across
sessions. Keep it terse; put design detail inline in the item, not in a
separate doc.

---

## Workflows the tool must support

Everything below serves one of these four:

- **A. Discover** — "Which of my BPs (or BPs I could buy) make the most
  ISK right now?" Sort and filter across the pilot's whole owned pool.
- **B. Decide (build vs buy per component)** — "For each material in the
  BOM, should I source it from Jita or run its own BP?" Recursive.
- **C. Execute + track** — "I've committed to build 500 of X. How many
  are done, how many are cooking, and what materials do I still need to
  acquire before the next job?"
- **D. Audit** — "The tool says this is +100M profit — is it lying? Where
  is the number coming from?"

If a proposed feature doesn't map to A / B / C / D, it doesn't ship.

---

## Audit questions the pilot must be able to answer

These are the concrete "why?" questions D is trying to unblock. Every
item in the roadmap either answers one of these directly, or provides
data another item consumes to answer one:

1. **Is the item's sell price abnormally high right now?** (moon-price
   listing, temporary shortage, market rally)
2. **Is a component's price abnormally low right now?** (bait ask on the
   input side, artificial "profit" that vanishes next tick)
3. **Will my ROI collapse after I finish building?** (my output floods
   the market, sell price drops, per-unit profit shrinks to nothing)
4. **If I build N of these, will I actually be able to sell them?**
   (market absorption over a realistic timeline)
5. **Are the numbers using fresh market data or stale cache?** (5-minute
   snapshot vs 24-hour cache — different confidence)
6. **Do I actually have the blueprints I'd need for the sub-components
   this recommendation assumes I'll build?**

---

## Design principles

- **Layered data**: one glance-able number in the row → hover reveals
  the components → click reveals full audit trail. Never surface a
  metric without a path to its inputs.
- **No new columns without proof of demand**: the scanner is already
  wide. New info goes into tooltips or side panels first; graduates to
  a column only if the pilot uses it every scan.
- **EVE-native terminology everywhere user-facing**: sell orders / buy
  orders / region / system / hub / m³ / ISK, not ask/bid/regional/etc.
  Wire-level Go and JSON field names may keep `Ask/Bid` (documented) so
  we don't break cached scan results.
- **Every warning glyph is one hover from a full-sentence explanation.**
  A ⚠ by itself is noise; ⚠ with "current sell price is 20× the 30d
  traded average" is a signal.
- **Non-fatal fallbacks**: any new data source that fails silently
  degrades to the pre-fix behavior. Never block a scan or analysis on a
  new dependency.

---

## Baseline (what's shipped as of v1.8.6 + in-flight)

### Shipped in v1.8.5 / v1.8.6

- `EffectiveInventionParamsForBase` — T2 invention BPC runs read the SDE
  per-target base (1 for ships, 10 for modules) instead of the hardcoded
  10. Fixes ~10× profit inflation on T2 ship rows.
- Per-node ME/TE via `IndustryParams.OwnedBlueprints` (scanner path).
  Sub-components use the pilot's actual BP ME instead of cascading the
  root's.
- Batch-output overshoot guard: sub-nodes below 50% batch utilization
  mark base so a Ferrogel reaction (400/run) doesn't get charged in full
  against a 1-unit demand.
- Scanner + Analysis pricing alignment: "View in Analysis" propagates
  the scanner's pricing region so both panels quote from the same
  market.
- Sell price / Buy price surfaced in the scanner (column + tooltip),
  EVE-native terminology.
- `industry_alignment_test.go` — scanner-path vs analyze-path
  regression suite (3 tests, passing).

### In flight (not yet committed after v1.8.6)

- `/api/auth/industry/owned-blueprints` endpoint + Analysis-tab wiring:
  Analysis tab now threads `OwnedBlueprints` into every analyze call
  (closes the last silent divergence with the scanner).
- `AskDepthUnits` / `BidDepthUnits` on `IndustryAnalysis` + scanner row.
  Sell price cell shows amber ⚠ when sell-side depth < batch size.
- `AskOrdersCount` / `BidOrdersCount` + `RegionalAvgPrice30d` on scanner
  row. `PeriodProfit` now uses the 30d traded avg (not best sell order)
  for revenue when history is present — fixes the Small Hybrid Burst
  Aerator II class of "30d ROI still inflated" bug.
- Moon-price ⚠ flag: fires when best sell price > 2× the 30d traded
  avg, surfaced in the Sell price cell tooltip with the ratio inline.
- Enriched Sell price cell tooltip: `Sell price / Buy price / Region
  30d avg / Batch size` with distinct-order counts alongside depth.

---

## Roadmap by workflow

Priority: **P0** = fixes broken accuracy or unblocks a stated audit
question. **P1** = high user leverage, next in queue. **P2** = polish or
scope-audit follow-ups.

### Workflow A — Discover ("what's profitable right now?")

#### P0: Confidence score aggregation
Serves: A, D. Answers: 1, 2, 4, 5.
Roll depth + order count + moon-price ratio + history availability +
required-market-share into one 0-100 number per row. Optional single
`Conf` column (small digit) or replace the row's row-color with a
confidence tint. Sort/filter by it. Directly addresses the "overview
already bloated" concern — one glance-able number replaces four
individual glyph checks. All raw signals stay in the tooltip.
Formula draft (each 0-1, weight-summed):
- depth-vs-batch: `min(askDepth / batch, 1)`
- order-diversity: `min(askOrders / 5, 1)`
- price-vs-30d: `1 - clamp((bestAsk - avg30d) / avg30d, 0, 1)`
- history freshness: 1 if history age < 24h, 0.5 if < 7d, 0.1 else
- required-share: `1 - clamp(sellable_needed / market_absorbs, 0, 1)`

#### P1: Multi-horizon price bands (30d + 90d)
Serves: A, D. Answers: 1, 2.
Extend `RegionalAvgPrice30d` to also compute 90d avg. Show both in the
tooltip: `30d avg: 5M · 90d avg: 4.2M`. Ratchets moon-price detection
against long-term normal, not just this month. Same history fetch —
no additional ESI calls.

#### P1: Days-to-clear indicator
Serves: A, D. Answers: 4.
For a given batch, at 10% market share of daily volume, how many days
until the whole batch is sold? Already computed inside `PeriodProfit`
as `sellable / market_daily`. Surface it: `Sellout: ~47 days` in the
row tooltip. Rows with sellout > 30 days flag amber; > 90 days flag
red. Answers "if I build 100, will I struggle to sell them?" directly.

#### P1: Watchlist / flagged BPs
Serves: A.
Persistent "star" flag on rows the pilot wants to keep watching between
scans. Sessions store which BPs are starred; the scanner can filter to
"starred only." Financial-screener pattern. Cheap: `blueprint_type_id`
list in `industry_shared_prefs`.

#### P2: Category-level trend view
Serves: A.
"Your T2 module category has 34 profitable rows this week, 8 more than
last week." Weekly diff surfaced in the scanner header. Requires
persisting scan snapshots — significant new persistence.

### Workflow B — Decide (build vs buy per component)

#### P0: Split-the-stack — mixed buy+build per material
Serves: B, D. Answers: nothing directly, but is a load-bearing correctness
fix for every ROI number.
Current behavior: for each material node the analyzer picks a BINARY
choice — either all-buy (walked market cost) or all-build (BOM cost).
When Jita has a small cheap tranche then jumps to expensive orders,
the optimal is a mixed strategy: buy the cheap head at market prices,
build the rest. Example: need 100 units, book is 30 units at 5M then
70 units at 20M, build cost 700M. Current picks all-build at 700M;
optimal is 30 bought (150M) + 70 built (490M) = 640M.
Direction: analyzer OVER-estimates cost → UNDER-states ROI. Safe
direction for the pilot's "50% → -30%" fear but still an accuracy hole
that hides genuinely-good rows behind modest ones, and undermines D
("why is this build cost so high — should I have grabbed some at
market?"). Implementation:
- Add `BuyUnits` / `BuildUnits` / `BuyPortionCost` / `BuildPortionCost`
  / `ShouldSplit` to MaterialNode.
- New engine helper `computeMixedCost` walks the sell-order book: take
  each order whose price beats per-unit build cost, cap at needed qty.
- `calculateCosts` evaluates mixed strategy before falling into the
  existing all-buy / all-build compare; uses mixed when it beats both.
- Parent aggregation sums the split correctly.
- `collectBaseMaterials` scales children by `BuildUnits / Quantity` so
  the flat shopping list reflects "buy 30 of X directly + buy raw
  materials for 70 built X's."
Assumes linear per-unit build cost across the split, which holds
past the batch-overshoot guard threshold. Reactions and other
step-function costs stay in the binary decision (guard forces buy
below 50% batch utilization anyway).

#### P0: Post-build ROI (uses 30d traded avg, not best sell)
Serves: B, D. Answers: 3.
Same substitution that fixed `PeriodProfit`: compute a second
`ProfitRealistic` on `IndustryAnalysis` that uses `RegionalAvgPrice30d`
for revenue. Show BOTH on the analysis tab: "Profit at current sell:
+100M · Profit at 30d avg: +8M." A moon-price row instantly shows the
gap. Answers "will my ROI drop to 0.1% after building?" — because it
tells you what ROI looks like at the price things actually trade at.

#### P1: Per-material price-vs-30d flag in the material tree
Serves: B, D. Answers: 2.
For each material node in `IndustryAnalysisResultsPanel`, if the buy
price is >2× or <0.5× the material's 30d avg, badge it amber and put
the ratio in the tooltip. "This build looks profitable because Ferrogel
is at 25M vs its 60M 30d avg — the ROI drops back to normal when the
bait ask clears." Requires fetching history for each flat material —
one-off per Analyze call, cache heavily.

#### P1: Ingredient price freshness age
Serves: B, D. Answers: 5.
Show "last price refresh: 12 minutes ago" in the material tree per
node, based on the market-price cache TTL. Rows using stale (>24h)
cached prices tinted differently. Restaurant-recipe-costing pattern —
the pilot can tell if a decision is based on live vs stale data.

#### P2: Recipe review flag on stale project rows
Serves: B, C, D.
For each active project item, if the underlying analysis's inputs have
moved >X% since the project was created, flag "This project's
profitability estimate is stale — re-run analysis." Requires storing
the analysis snapshot with the project.

### Workflow C — Execute + track

#### P0: Discount scanner rows by "already in flight"
Serves: A, C.
Join scanner output against active `industry_tasks` for the same
product typeID; subtract committed units from the market-absorption cap
before computing `PeriodProfit`. If you're already building 500 of X in
Project A, don't propose building another 500 of X. Backend-only —
`PeriodProfit` computation adds a subtraction step. Closes the "I keep
seeing the same profitable BPs I've already committed to" loop.

#### P0: Portfolio-wide shopping list
Serves: C.
Cross-project aggregate: total materials needed across all active
projects − stockpile − in-flight jobs = net-to-buy. Split into Jita
(components) and Dodixie (minerals) clipboard buckets — the "Delta
Shopping List" from the earlier T2 Manufacturing spec.
Backend: new endpoint `GET /api/auth/industry/portfolio-shopping-list`.
Frontend: could live under a new "Buy" sub-tab or as a floating panel.

#### P1: Live P&L per active project
Serves: C, D. Answers: 3.
For each active project: locked-in cost (materials already bought) +
projected cost (still to buy at current prices) vs projected revenue at
current sell prices AND at 30d avg. "This project was estimated at
+300M when queued; current market puts it at +50M projected, -30M at
30d avg." Alerts when a project flips to unprofitable.

#### P1: Stockpile-driven build suggestions
Serves: C.
Toyota / Kanban pattern. When Stockpile threshold > on-hand, generate
a shortfall list with a one-click "add to project" action. Ties the
Stockpiles tab into the project flow it currently doesn't touch.

#### P2: ETA to completion + slot utilization
Serves: C.
For active projects: given N remaining jobs and M available slots
across the pilot's characters, ETA = ceil(N/M) × avg_job_time. Simple
scheduler math; useful in the project header.

### Workflow D — Audit (already a cross-cutting concern; specific items)

Most items above already serve D. Additional D-only work:

#### P0: "Why this profit?" popover on each scanner row
Serves: D.
Click a profit cell → modal showing the full derivation:
```
Sell revenue:      2 × 100M/unit          = 200M
  - 3.6% sales tax                        = -7.2M
  - 1.0% broker fee                       = -1.98M
Net revenue                                = 190.82M
Materials (12 Fernite + 1 Ferrogel + 3 Phenolic)
  = 730 + 25,910 + 2,435 (from Jita sell orders)
                                           = 29,076
Job install (Botane Sotiyo, 25% tax)     = 1,002
Invention cost (None decryptor, 34% chance × 2.94 attempts × 2 datacores)
                                           = 15,300
Total build cost                           = 45,378
────────────────────────────────────────
Profit                                    = 190M - 45k = 190.77M
```
Every input in that popover has a `?` icon linking to the source
(scanner row / analysis panel / owned-BP list). Directly answers "why
is this row's profit what it is?" with no code-reading required.

#### P1: Explain the confidence score
Serves: D.
When the pilot hovers the confidence score (from Workflow A), tooltip
shows each contributing sub-score and its weight. If confidence is 45,
"depth 0.9 × 0.3 + orders 0.2 × 0.2 + price-vs-avg 0.1 × 0.3 +
history 1.0 × 0.1 + share 0.5 × 0.1 = 0.45 → 45." Prevents the score
from feeling like a black box.

---

## Cross-cutting: information architecture

Not features — restructures that make the above easier to consume.

#### P1: Flatten Projects / Plan / Operations
Sub-tabs already share `IndustryJobsLedgerPanel`. Present as one
top-level tab with a `Guide / Planning / Operations` sub-picker
matching the internal `IndustryJobsWorkspaceNav`. Reduces the tab bar
from 5 to 3 without touching logic.

#### P1: Unify Stockpiles + Coverage
One "Inventory" concept: on-hand from ESI + target thresholds + what
active projects need. Coverage endpoint reads from the same store as
Stockpiles. Removes the two-concepts-for-in-stock problem the audit
called out.

#### P2: Deprecate / gate under-used surface
- `IndustryPlannerSchedulerPanel` (857 lines) — hide behind a flag if
  unused; delete if confirmed unused.
- `IndustryDependencyBoard` (~500 lines) — same.
- `IndustryJobsGuidePanel` (356 lines) — same.
- Consolidate `materials/rebalance` and `materials/recalc-remaining`
  into one endpoint; fix the underlying stale-material-plan issue that
  requires them.

---

## Cross-cutting: testing

The moon-price / ME-cascade / batch-overshoot bugs all slipped past
because the tests exercised each layer in isolation. New tests to add:

- Scanner-level integration test (currently only the engine has an
  alignment test). Extract the `PeriodProfit` computation into a
  testable helper. Add a moon-price scenario: history says 5M, current
  best sell says 100M, verify `PeriodProfit` uses 5M.
- Confidence-score test: given known depth/orders/price/history
  fixtures, verify the score computation is stable and monotonic in
  each input.
- End-to-end scanner test via the HTTP endpoint (NDJSON streaming
  harness). Would catch anything the two per-layer suites miss.

---

## Real-world analogs referenced

For future design decisions, when in doubt look at:

- **Keepa / Jungle Scout** (Amazon FBA arbitrage) — price history bands,
  sales-rank velocity, competition depth. Best analog for Workflow A +
  audit questions 1, 2, 5.
- **SAP / NetSuite MRP** — BOM explosion, netting (gross − on-hand −
  scheduled receipts), consolidated PO plan. Best analog for
  Workflow B + C.
- **Restaurant recipe costing** (MarginEdge, Chef+) — recipe cost per
  plate + contribution margin + ingredient-price freshness alerts.
  Direct model for our material tree + freshness age.
- **Toyota / Kanban** — reorder-point signaling. Model for Stockpiles →
  auto-suggest builds.
- **Theory of Constraints (Goldratt)** — profit per unit of constrained
  resource, not gross margin. Justifies keeping ISK/h/slot as the
  primary discovery sort.
- **Financial trading screeners (Finviz / TradingView)** — multi-metric
  filter grids + confidence flags + watchlists. Direct model for the
  scanner UI.

---

## How to work this doc

- Pick a P0 item, drop it into a session prompt.
- After landing, tick off (strike-through or move to a "Shipped" section
  at the top under the version tag).
- If a new idea appears mid-session, add it here rather than losing it
  in chat scrollback.
- Re-prioritize whenever the pilot's workflow assessment changes —
  P0/P1/P2 are directional, not sacred.

---

## Appendix: EVE-IPH comparison audit (2026-08-24)

Cross-referenced the open-source EVE Isk Per Hour tool (VB.NET, ~138
files, unmaintained since ~2024) against Flipper's Go industry engine.
EVE-IPH used as a second opinion, not gospel — several of its formulas
are demonstrably pre-2022 industry rework. This appendix is the raw
audit output; specific action items are pulled into the P0/P1/P2 tiers
below.

### Action items pulled from the audit into the roadmap

The two findings that go the UNSAFE direction (over-state ROI) get
promoted to P0 verification tasks:

#### P0: Verify + fix invention skill chance formula (mult vs additive)
Serves: A, D. Answers: 3.
Flipper (`industry.go:392-406`, `InventionSkillMultiplier`) computes:
`chance × (1 + Enc/40) × (1 + (DC1+DC2)/30)` — multiplicative. EVE-IPH
(`Blueprint.vb:2550`) computes: `chance × (1 + (DC1+DC2)/30 + Enc/40)`
— additive. At all-L5, Flipper = 1.500 mult, EVE-IPH = 1.458.
Direction: Flipper OVER-STATES invention chance by ~2.9% → UNDER-STATES
expected attempts → UNDER-STATES invention cost → OVER-STATES profit.
Same class of unsafe direction the pilot's "ROI 50% → -30%" fear
describes. Verify against a controlled in-game invention preview at
known skill levels; if EVE-IPH's additive form matches CCP's display,
fix `InventionSkillMultiplier` to return `1 + e/40 + (d1+d2)/30`.

#### P0: Verify + fix structure/rig cost bonus composition (mult vs additive)
Serves: B, D.
Flipper (`industry.go:1007-1015`) applies structure and rig job-cost
reductions ADDITIVELY: `grossInstall = SystemCost − (SystemCost ×
structRed) − (SystemCost × rigRed)`. EVE-IPH (`Blueprint.vb:2086`,
`ManufacturingFacility.vb:2367-2369`) multiplies: `CostMultiplier =
∏(1 − rigBonus)`. Example: Raitaru 3% + one T1 20% rig — additive
=23%, multiplicative =22.4%. Direction: Flipper UNDER-estimates job
cost by ~0.6pp of SystemCost on the typical hisec setup, larger on
stacked rigs. Small magnitude, but demonstrably not matching CCP's
composition rule for stacking bonuses. Verify against in-game Industry
window totals; fix `calculateCosts`' job cost math to compose
multiplicatively if confirmed.

### Full audit report

#### A. Job installation cost math

1. **Structure + rig cost bonuses: multiplicative vs additive.** (See
   P0 verification above.)

2. **Alpha clone industry tax not modelled.** EVE-IPH
   (`Blueprint.vb:2089-2091`, `ProgramSettings.vb:108`) adds
   `AlphaCloneTax = 0.0025` (0.25%) into the fee sum for alpha
   accounts. Flipper: no equivalent on `IndustryParams`. Direction:
   Flipper UNDER-estimates job cost for alpha players by 0.25% of EIV
   — small but real. **Recommended:** add feature (config flag; low
   priority — most active users are omega).

3. **Faction Warfare system-upgrade cost bonus not modelled.** EVE-IPH
   (`Blueprint.vb:2086, 2564, 455-467`) `FWManufacturingCostBonus` /
   `FWInventionCostBonus` scale from 1.0 down to 0.5 based on FW
   upgrade level 1-5, applied multiplicatively into `Indexbonuses`.
   Flipper: no FW awareness anywhere. Direction: Flipper OVER-estimates
   job cost by up to 50% in FW pipeline systems. Missed opportunity for
   FW-based industry pilots. **Recommended:** add feature.

4. **Pirate-faction Fulcrum SCC 90% discount not modelled.** EVE-IPH
   (`Blueprint.vb:2093-2098`) applies a 90% SCC reduction at facility
   60015187 (Zarzakh) when corp faction ∈ {500010 Guristas, 500011
   Angel Cartel}. Flipper: flat 4% SCC everywhere. Direction: Flipper
   OVER-estimates SCC by ~3.6pp of EIV for the tiny pirate-FW builder
   segment. **Recommended:** no action unless asked.

5. **Separate facility per activity tier.** EVE-IPH tracks four
   distinct facilities: main mfg, component mfg, cap component mfg,
   reaction, plus separate invention and copy facilities. Flipper: one
   `SystemID` + `StructureRigs` config per Analyze call. Real
   industrialists split (Sotiyo for mfg, Athanor for reactions, lab
   for invention). Flipper's tree currently charges reaction sub-nodes
   at the mfg system's cost index. **Recommended:** further
   investigation — medium value feature.

6. **Facility tax + SCC = EIV × rate — MATCHES.** Both apply as `EIV ×
   rate` (`industry.go:1019-1020`, `Blueprint.vb:2100`). Already
   validated against in-game Industry window.

#### B. Invention math

1. **Skill-chance formula: additive vs multiplicative.** (See P0
   verification above.)

2. **Invention job cost base — different EIV entirely.** EVE-IPH
   (`Blueprint.vb:2564`): `JobGrossCost = (T1_manufacturing_EIV × 0.02)
   × InventionSCI × …` — legacy pre-2018 formula ("invention = 2% of
   what you'd build"). Flipper (`industry.go:1521-1546`): `eivPerAttempt
   = Σ(adjustedPrice × qty)` over the invention materials themselves
   (datacores + optional decryptor), then standard SCI/tax/rig layers.
   **Flipper's model is correct** per CCP's 2022 industry rework — EVE-IPH
   is demonstrably outdated here. Worth adding a scenario test that a
   real T2 module invention preview matches Flipper's output.

3. **Decryptor table — MATCHES.** EVE-IPH (`DecryptorList.vb`) loads 8
   decryptors from SDE `groupID = 1304`; Flipper (`decryptors.go:34-44`)
   hardcodes the same 8 with identical modifiers.

4. **Expected-attempts math.** EVE-IPH ceilings expected attempts to
   whole invention jobs (`Blueprint.vb:2384`). Flipper returns
   fractional expected attempts (`industry.go:1511-1515`) — statistical-
   average interpretation, more appropriate for the scanner's ranking.
   **No action — different but defensible.**

5. **Invention BPC copy cost handling.** EVE-IPH treats T1 copy time +
   copy job installation as a separate step (`Blueprint.vb:2403-2438`).
   Flipper folds invention into one step and treats the T1 source BPC
   as free/pre-owned. **Recommended:** further investigation — for
   long-copy items this can be material.

#### C. Material tree / build-vs-buy

1. **Per-blueprint ME/TE lookup.** EVE-IPH silently falls back to
   defaults or T2 base 2/4 when unowned (`Blueprint.vb:1972-2015`).
   Flipper's `OwnedBlueprints` (`industry.go:920-934`) marks base when
   the pilot doesn't own the BP — refuses to hallucinate a build path
   the pilot can't run. **Flipper's model is stronger.**

2. **Build-vs-buy decision.** EVE-IPH is strictly binary
   (`Blueprint.vb:2160-2210`). Flipper tries a mixed split first
   (v1.8.8 split-the-stack). **Flipper's model is correct.**

3. **Batch reaction / batch ammo overshoot.** EVE-IPH tracks excess
   material and optionally subtracts the sale value; Flipper marks
   sub-nodes base below 50% batch utilization. Design difference, both
   defensible. **No action.**

4. **Reactions get structure/rig ME.** Both apply structure+rig ME to
   reactions. **No discrepancy.**

5. **Fulcrum ME 6% reaction reduction not modelled.** Related to A4/A5
   — same recommendation as A4.

#### D. Blueprint scanner / batch analysis

EVE-IPH has NO batch profitable-BP scanner. Its owned-BP list doesn't
rank by profit; only per-item analysis. **Flipper's Profitable
Blueprints Scanner (batch-scoring, auto-decryptor optimizer per row,
period-profit, order-book depth) is unique.**

#### E. Pricing sources

EVE-IPH aggregates via EVEMarketer / Fuzzworks / MarketPriceInterface
— region-scoped best/avg only, no order-book walking, no anti-moon-
price signal. Flipper walks the actual book (`industry.go:1904-1956`),
computes `RegionalAvgPrice30d`, flags moon-price. **Flipper is
materially stronger for large-batch valuations.**

#### F. Features EVE-IPH has that Flipper doesn't (ranked by relevance)

1. **In-app owned-BP ME/TE editor** — Flipper has ESI sync but no
   in-app editor. High value.
2. **Multi-facility routing** — see A5.
3. **T2 BPC copy cost/time step** — see B5.
4. **Reprocessing Plant tool** (`ReprocessingPlant.vb`) — standalone
   refine-vs-buy calculator.
5. **Convert-to-ore calculator** (`ConvertToOre.vb`) — alternative
   sourcing.
6. **Ore/Ice Belt Flip** (`frmIndustryBeltFlip.vb`, `frmIceBeltFlip.vb`)
   — mining vs buying comparison.
7. **Loyalty Points store viewer** (`EVELoyaltyPoints.vb`) — LP → item
   profitability.
8. **Research Agents module** (`EVEResearchAgents.vb`) — datacore agent
   LP planning.
9. **Industry Jobs viewer from ESI** — live running-job dashboard.
10. **Assets viewer** — cross-character asset browser.
11. **Shopping list export** (`frmShoppingList.vb`) — Flipper has flat
    materials but no explicit multi-buy export flow.
12. **Character standings + skills viewer** — read-only.
13. **Datacore selection optimizer tab** — dedicated invention-decryptor
    comparator.
14. **Historical market history viewer** (`frmMarketHistoryViewer.vb`).
15. **Cost split viewer** — per-material contribution to final cost.
    (Flipper covers this in the tree tooltip.)

#### G. Features Flipper has that EVE-IPH doesn't

Batch Profitable Blueprints Scanner (with per-row auto-decryptor
optimizer). Split buy+build strategy per material. Region 30d
volume-weighted average as anti-moon-price signal. Order-book depth
+ count exposure. Period-profit / market-absorption model. `OwnedBlueprints`
"mark base if unowned" safety. Structure rig affinity + sec-status
+ hull-fit validation. Split trade-fee model. Reprocessing-sourcing
comparison. `SkipReactions` builder mode.
