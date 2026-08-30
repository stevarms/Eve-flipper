# EVE Accuracy Audit

Deep formula-by-formula check of every ISK-affecting calculation in `internal/engine/` (and a few relevant `internal/api/` files) against real EVE Online mechanics as of the 2024/2025 patch cadence.

Scope: broker/tax, manufacturing/ME/TE, job cost, invention, PI, contracts, backtest, portfolio, station trading, undercut, route, PLEX. Nothing in this file is a code change — this is a read-only audit. All line references are current at the time of writing.

**Rating legend**
- **MATCHES** — the code implements the EVE mechanic correctly.
- **DIVERGES** — the formula is wrong or produces a different number than the game.
- **STALE** — the code implemented a historical version of the mechanic; CCP has since changed it.
- **INCOMPLETE** — the code models part of the mechanic but omits a component that changes the answer.
- **UNVERIFIED** — the code appears plausible but I can't pin it down from the code alone.

---

## Executive summary

1. **Broker Relations skill effect is wrong.** `internal/api/character_market_fees.go:34` reduces the broker fee by 0.4 percentage points per skill level. Real EVE has reduced 0.3 pp per level since the March 2020 rebalance. At L5 the code returns 1.0% when the game returns 1.5%. If the trader plugs the "suggested" fee into a station-trading or route scan, every downstream ROI/margin is inflated by 50 bp on both sides of the trade.
2. **Invention skill bonuses are not applied.** `industry.go`'s invention step (calculateInventionStep) multiplies the SDE base probability by the decryptor chance multiplier only. Real EVE multiplies base × (1 + Encryption/40) × (1 + (Datacore1+Datacore2)/30) × decryptor. A maxed inventor sees 1.5× the SDE base chance; the analyzer sees the raw SDE base. Result: invention cost and expected-attempts are inflated by ~50% for skilled inventors — the tool will systematically say "this T2 build isn't profitable" when in reality it is. The user can override via `InventionChance` but the API from the character-fees suggestion does not pre-fill it.
3. **Legacy fee mode charges broker fee to only one side of the buy.** `fees.go:38-44` — when `SplitTradeFees=false`, buy-side sales tax is zeroed and buy-side broker fee equals the sell-side rate. This mirrors real EVE (buy orders pay broker but no sales tax) so the buy side is actually right; the historical comment saying "buy side: broker only" is accurate. But an assumption slipped in: the code assumes buy-side broker rate equals sell-side broker rate. If the user trades at a structure with a different tariff, or has different standings at buy vs sell hub, legacy mode understates the true blended fee. Split mode fixes this. **No 100 ISK broker-fee minimum floor** anywhere — EVE enforces `max(brokerFee, 100 ISK)` per order, which changes the answer on ammo, PI raw materials, or any low-value order.
4. **Reprocessing is unimplemented.** `IndustryParams.IncludeReprocessing` exists (`industry.go:35`) but is never consumed. Any UI toggle labelled "consider reprocessing as alternative" is a no-op today.
5. **SCC Surcharge (industry.go:263) is pinned to 4%.** This matches the current live rate but is a hard-coded constant. CCP has already stepped this fee twice since Uprising 2022 (1.5% → 4%) and has publicly signalled further adjustments. Flag as **STALE-RISK** — the value will silently drift if CCP raises it again.

Otherwise the industry job-cost breakdown, ME/TE multipliers, EIV formula (base × runs, not ME-reduced), rig sec-multiplier handling, PI POCO base values, decryptor table, PLEX region ID, SP-farm math, portfolio Sharpe / EWMA / Cornish-Fisher VaR, and the execution-plan slippage walk are all correct.

---

## Trading / Market

### Broker fee suggestion — DIVERGES (STALE + wrong coefficient)

- File / function: `internal/api/character_market_fees.go:27-35`, `suggestedBrokerFee`
- Also depended on by: `engine/plex.go:352` (default when caller passes 0).
- What EVE actually does (post-Uprising 2022, still current in 2025):
  - NPC station base broker fee: **3.0%**.
  - Broker Relations skill reduces by **0.3 percentage points per level** (max reduction 1.5%). So L5 = 1.5%.
  - Additional reduction from Faction standing (0.03%/point) and Corporation standing (0.03%/point), capped so unmodified rate can't go below ~0.5%.
  - Structures (Upwell): owner-set base rate 0–5%, still modified by skill/standings.
- What the code does:
  ```go
  return 3.0 - 0.4*float64(brokerRelationsLevel)
  ```
  - L0: 3.0% (correct base)
  - L5: **1.0%** (real EVE: 1.5%)
- Verdict: **DIVERGES.** Off by 0.5 percentage points at L5, or by ~33% relative to the correct value. The comment above the function claims "matches engine.PLEXDashboard at L5" which cements the wrong constant.
- Impact: If a trader accepts the suggested value, every station-trading margin, route ROI, backtest profit, and industry sell-side revenue is off by 50 bp on both broker legs. On a 2% margin flip, that's a **25% overstatement of profit per unit** on the broker component alone. On round-trip trades (buy + sell) it doubles.

### Sales tax suggestion — MATCHES

- File / function: `internal/api/character_market_fees.go:17-25`, `suggestedSalesTax`.
- What EVE actually does: base 8.0%, Accounting reduces by 11% per level, so L5 = 8 × (1 − 0.55) = 3.6%. Only affects the seller. No structure or standing effect.
- What the code does: `8.0 * (1 - 0.11 * accountingLevel)` → L5 = 3.60% exactly.
- Verdict: **MATCHES.**

### 100 ISK broker-fee minimum floor — MISSING

- File: `engine/fees.go` (whole file).
- What EVE does: per-order broker fee = `max(price × qty × rate, 100 ISK)` regardless of skill, standings, or structure.
- What the code does: pure percentage-of-notional, no floor.
- Verdict: **INCOMPLETE.**
- Impact: For any order under `100 / (rate / 100) = 10,000 ISK` at 1% or under `2,500 ISK` at 4%, the code understates fees. Matters for PI raw materials (P0/P1), ammo, mining crystals, and small-ticket flips. Ignore for anything > 1M ISK/order.

### Relist / modify order fee — MISSING

- File: `engine/undercut.go:105-113` suggests a new price but doesn't quote what modifying the order costs.
- What EVE does: modifying an active order costs a broker fee proportional to the price delta (`max(brokerFee × |newPrice − oldPrice| / newPrice, 100 ISK)` for sell; `broker fee on the new price - refund from old` semantics vary). Effectively you pay full broker again on the price change portion.
- Verdict: **INCOMPLETE.**
- Impact: The order-desk `Recommendation: "reprice"` triggers without evaluating whether the relist fee eats the undercut. On thin books with a 0.01 ISK undercut, the recommendation is often a net loss.

### Split-fee vs legacy fee modes — MATCHES (with a caveat)

- File: `engine/fees.go:30-47`, `normalizeTradeFees`.
- Legacy mode: buy-side gets broker only (no sales tax), sell-side gets broker + sales tax. **This matches real EVE**: buyers who place buy orders pay broker fee, sellers pay broker + sales tax. Instant-buy (hitting a sell order as market taker) pays no broker fee, only shipping — the split-mode / instant-sell code path in `contracts.go:181-186` and `station_trading.go:846-853` gets this right too.
- Caveat: legacy mode forces `BuyBrokerFeePercent = SellBrokerFeePercent = BrokerFeePercent`. A trader who buys in one region and sells in Jita at different fee rates cannot express this until they turn on split mode. Not a bug — a UX limitation of the legacy path.

### tradeFeeMultipliers formula — MATCHES

- File / function: `engine/fees.go:54-62`.
- `buyCostMult = 1 + (buyBroker + buyTax)/100`
- `sellRevenueMult = 1 − (sellBroker + sellTax)/100`
- Both used consistently across `station_trading.go`, `scanner.go`, `route.go`, `regional_day_trader.go`, `backtest.go`, `execution.go`, `contracts.go`. No copy-paste divergence detected. **MATCHES** the standard EVE fee model.

### Undercut / suggested price — MATCHES the game mechanic, but silent on relist cost

- File: `engine/undercut.go:105-113`.
- Sell: `suggestedPrice = bestPrice - 0.01` (floor 0.01).
- Buy: `suggestedPrice = bestPrice + 0.01`.
- Real EVE tick is 0.01 ISK. Correct. See note on relist-fee absence above.
- Verdict: **MATCHES** the price mechanic; **INCOMPLETE** on economics.

---

## Industry — Manufacturing

### Material Efficiency (ME) formula — MATCHES

- File / function: `engine/industry.go:1065-1105`, `calculateActivityMaterials`.
- Code:
  ```go
  qty = ceil(base × runs × (1 - ME/100) × (1 - structureBonus/100) × (1 - rigMEReduction/100))
  if qty < runs { qty = runs }     // floor: at least 1 unit per material per run
  ```
- Real EVE: `max(runs, ceil(round(base × runs × (1 − ME) × (1 − structure) × (1 − rig), 2)))`. Applied per-material.
- Differences:
  - The code does not perform the intermediate `round(_, 2)` before `ceil` that EVEIndustry uses. In practice this only produces an off-by-one on materials where the pre-rounded value is `x.00000000001`, so the impact is a single unit of one cheap material per job — inconsequential for cost math but worth knowing when reconciling against the in-game preview.
  - Reactions correctly skip blueprint ME (set to 1) while still applying structure + rig bonuses (line 1088-1091). Historical EVE bug: reactions used to have no ME; the code's comment says this was fixed. **MATCHES current mechanic.**
- Structure and rig bonuses are applied multiplicatively, not additively. That matches how the game stacks them.
- Verdict: **MATCHES** (with a 1-unit rounding pedantry).

### Time Efficiency (TE) formula — MATCHES

- File / function: `engine/industry.go:1111-1134`.
- `time = base × runs × (1 - TE/100) × (1 - rigTE/100)` for manufacturing; reactions and invention skip blueprint TE.
- ME clamped 0-10, TE clamped 0-20. ✓ correct BPO caps.
- Verdict: **MATCHES.**

### System Cost Index (SCI) fetch — MATCHES

- File / function: `engine/industry.go:1234-1253`, `costIndexForActivity`.
- Pulls the per-activity SCI (manufacturing / reaction / invention) from ESI's `/industry/systems/`. Falls back to the parameter's global cost index only when SCI is missing.
- Verdict: **MATCHES** ESI's data model.

### Job Installation Cost — MATCHES (post-2026-07-22 fix)

- File / function: `engine/industry.go:825-877`.
- Line 826-828 confirms this was corrected after a bug in prior code that applied `FacilityTax` to Gross Install instead of EIV. The current formula is:
  ```
  SystemCost     = EIV × SCI
  StructureBonus = SystemCost × structureJobCostReduction%   (reduction)
  RigBonus       = SystemCost × rigCost%                     (reduction, additive)
  GrossInstall   = SystemCost − StructureBonus − RigBonus
  FacilityTax    = EIV × facilityTax%                        (% of EIV)
  SCCSurcharge   = EIV × 4%                                  (flat, % of EIV)
  NetInstall     = GrossInstall + FacilityTax + SCCSurcharge
  ```
- This matches CCP's canonical formula as documented in the in-game Industry window since Uprising.
- Verdict: **MATCHES.**

### EIV (Estimated Item Value) — MATCHES

- File / function: `engine/industry.go:918-953`, `calculateEIV`.
- Formula: `EIV = Σ(adjustedPrice × baseQty × runs)` — uses **base** material quantities (pre-ME).
- Real EVE: identical. EIV is deliberately ME-agnostic so job cost doesn't reward heavy ME research.
- Adjusted prices are pulled from ESI's `/markets/prices/` (`esi.Client.GetAllAdjustedPrices`).
- Verdict: **MATCHES.**

### Structure hull job-cost bonus values — MATCHES

- File / function: `engine/industry.go:64-67` (comment): Raitaru 3, Azbel 4, Sotiyo 5.
- Values are user-supplied at the API layer via `StructureJobCostReduction`. The comment lists correct EVE values.
- Verdict: **MATCHES**, assuming the frontend passes the right constant per hull.

### Structure rigs — MATCHES

- File / function: `engine/industry.go:1165-1219`, `rigContribution`.
- Reads MEBonus / TEBonus / CostBonus from parsed SDE (`sde.StructureRig`) which sources them from `typeDogma.jsonl` attributes 2593/2594/2595 (engineering) or 2713/2714 (reaction). See `sde/rigs.go:111-125`.
- Applies sec-status multiplier via `StructureRig.SecMultiplier(security)`:
  - `security >= 0.45` → HiSecMult (attr 2355)
  - `0 < security < 0.45` → LowSecMult (attr 2356)
  - `security <= 0` → NullSecMult (attr 2357)
- Additive stacking across up to 3 rigs, no stacking penalty. **Matches EVE** — structure rigs don't stacking-penalize.
- Fit check enforces `FitsStructureGroups` matches the structure hull's typeGroupID.
- Advanced rigs with HiSecMult = 0 return 0 (correctly excluding hisec use). **MATCHES.**
- Verdict: **MATCHES.**

### Batch-utilization guard — HEURISTIC (not an EVE mechanic; sane guardrail)

- File / function: `engine/industry.go:279`, `batchUtilizationThreshold = 0.5`.
- If a sub-node would consume less than half a batch's output (e.g. needs 12 Fernite Carbide but a reaction run produces 10,000), the analyzer refuses to build and marks it as buy-only.
- Not an EVE mechanic — a planner heuristic to stop the tree from charging a full reaction cost against a tiny demand. Sound reasoning.
- Verdict: **N/A (heuristic).**

### Root activity time vs total activity time — MATCHES intended definition

- File / function: `engine/industry.go:598-640`.
- `ManufacturingTime` = root blueprint's own activity time × runs × (1 - TE/100) × (1 - rigTE/100).
- `TotalActivityTime` = sum of every step's time in the plan, for the serial-worst-case planner view.
- `ISKPerHour` = `profit / (rootTime / 3600)` — divides only by the root time, because sub-material builds run in parallel slots and don't gate this job. This is a reasonable throughput proxy but assumes unlimited parallel slots (real EVE caps at 1+Advanced Mass Production+Mass Production = 11 slots per character). Not wrong, just an idealized upper bound.
- Verdict: **MATCHES** the stated model; **INCOMPLETE** for characters near the slot cap.

### SCC Surcharge — STALE-RISK

- File / function: `engine/industry.go:260-263`.
- Constant `jobCostSCCSurchargePercent = 4.0`.
- History: introduced at 1.5%, raised to 4% during industry-cost rework (2024). Publicly discussed further changes at Fanfest 2025 keynote (implementation TBD).
- Verdict: **MATCHES current live rate** but pinned to a hard-coded constant. If CCP steps it again, the code silently uses the old value.
- Impact: Every 1 pp change in SCC is a 1% change in job cost on the EIV base. For a Rorqual build worth 1B ISK EIV, that's 10M ISK per job.

### Blueprint cost amortization — MATCHES

- File / function: `engine/industry.go:527-535`.
- BPO: `bpCostIncluded = blueprintCost / runs` (amortized).
- BPC: `bpCostIncluded = blueprintCost` (full charge, one-time).
- Verdict: **MATCHES** the correct model. Assumes BPO amortization over exactly `runs` — for a real BPO you'd amortize over the BPO's projected lifetime, but that's outside the scope of a single-job cost model.

---

## Industry — Invention

### Base invention probability — DIVERGES (skills not applied)

- File / function: `engine/industry.go:1255-1354`, `calculateInventionStep`.
- Code:
  ```go
  chance := normalizeProbability(product.Probability)         // SDE base
  if params.InventionChance > 0 {
      chance = normalizeProbability(params.InventionChance)   // user override
  } else if params.InventionChanceMult > 0 {
      chance = chance * params.InventionChanceMult            // decryptor mult only
  }
  ```
- What EVE actually does:
  ```
  chance = base × (1 + Encryption/40) × (1 + (DatacoreSkill1 + DatacoreSkill2)/30) × decryptorMult
  ```
  - All L5: `(1 + 5/40) × (1 + 10/30) = 1.125 × 1.333 = 1.50`
  - So max-skilled probability is 1.5× the SDE base.
- Verdict: **DIVERGES** unless the caller pre-computes the skill-adjusted chance and passes it as `InventionChance`. The analyzer never reads the character's invention skills.
- The `suggestedBrokerFee` API companion doesn't have a `suggestedInventionChance` — the frontend has no server-provided path to inject skill-adjusted probability.
- Impact: For an unskilled inventor, math is right. For a max-skilled inventor, expected-attempts is inflated by 50%, so invention-material and invention-install cost are inflated by 50%. On a T2 module worth 5M ISK to invent, that's ~2.5M ISK of phantom cost per attempt cycle — enough to turn a profitable build into a "buy from market" recommendation.

### Decryptor table — MATCHES

- File / function: `engine/decryptors.go:34-44`.
- Every entry cross-checked against EVE's Invention Decryptors group (Accelerant, Attainment, Augmentation, Optimized Attainment, Optimized Augmentation, Parity, Process, Symmetry) — ProbMult, OutputRunsBonus, MEDelta, TEDelta all match CCP's canonical values.
- Verdict: **MATCHES.**

### T2 BPC base values — MATCHES

- File / function: `engine/decryptors.go:23-29`.
- `T2BPCBaseME = 2`, `T2BPCBaseTE = 4`, `T2BPCBaseRuns = 10`.
- Real EVE: freshly-invented T2 module BPC has ME 2, TE 4, 10 runs. Ship BPCs have 1 run (see next).
- `EffectiveInventionParamsForBase(baseRuns)` overloads correctly: passes T2 ship's per-target base runs (1) instead of the module default (10). Otherwise T2 ship invention profit is inflated 10×.
- The `industry_alignment_test.go:311-360` regression test explicitly locks down the T2 ship case.
- Verdict: **MATCHES.**

### Invention job cost — MATCHES

- File / function: `engine/industry.go:1290-1329`.
- Same job-cost formula as manufacturing (SystemCost, StructureBonus, RigBonus, GrossInstall, FacilityTax, SCC). Applied per attempt, then scaled by `expectedAttempts = successesNeeded / chance`.
- ME rigs correctly do **not** apply to invention (datacore materials are not ME-reduced in EVE). Only TE-time and cost-reduction rigs affect invention. Code confirms this at line 1291-1293.
- Verdict: **MATCHES.**

### Datacore consumption on failure — MATCHES (implicitly)

- The code models `expectedAttempts = successesNeeded / chance`, then charges `materialCostPerAttempt × expectedAttempts` for datacores.
- Real EVE: datacores are consumed on both success and failure. `expectedAttempts × cost` is the correct expectation of total datacore spend.
- Verdict: **MATCHES.**

---

## Planetary Interaction

### Extractor cycle output — MATCHES (delegates to ESI)

- File / function: `internal/api/pi.go:231-241`.
- `unitsPerDay = QtyPerCycle × 86400 / CycleTime` — uses ESI's `extractorDetails` directly. No client-side modeling of the extractor productivity curve (which decays over the program length).
- Verdict: **MATCHES** for currently-running extractors (ESI already delivers the per-cycle quantity). Does **NOT** simulate extractor decay — if you want "what if I set a 24h program starting now on P0 X", you'd need the curve. Not implemented.

### Factory throughput — HEURISTIC (bottleneck ratio)

- File / function: `internal/api/pi.go:358-388`, `factoryThroughputFactor`.
- For each input: `ratio = available / required`; take the min across inputs; clamp to [0, 1].
- Reasonable proxy: throughput is bottlenecked by the scarcest input.
- Real EVE: a factory runs a full cycle iff all inputs meet the required quantity; otherwise it idles that cycle. Averaged over a day this approximates `min-input-ratio`. Correct as a monetized-average estimate.
- Verdict: **MATCHES** as a fair value estimator.

### POCO base values per PI tier — MATCHES

- File / function: `internal/api/pi_factory.go:180-186`.
  - P0: 5, P1: 400, P2: 7200, P3: 60000, P4: 1200000
- Community-verified against `dgmTypeAttributes.basePrice` for PI types. All correct.
- Verdict: **MATCHES.**

### POCO tax formula — MATCHES

- File / function: `internal/api/pi_factory.go:436-437, 471`.
- `tax = qtyPerDay × baseValue × (pocoTaxPercent / 100)`.
- Real EVE customs office tax: applied on both import (input into planet) and export (output off planet), each at the owner-set rate. NPC POCO base rate: 5% both ways = 10% total. Player POCOs: 0-100% each.
- The code separately tracks import + export as symmetric taxes with a single user-supplied rate. Correct if the user enters their combined "typical" rate. The default helper text hints at 15% which is a realistic combined figure for a moderately-taxed player POCO region.
- Verdict: **MATCHES** as a flexible model; users must know POCO tax is **per-direction** so entering 5% means "5% each way" not "5% total".

### PI skill effects — MISSING

- No modeling of Command Center Upgrades (which unlocks the CC tier), Interplanetary Consolidation (extra planets), or Remote Sensing / Planetology (extractor scan detail). These don't affect ISK/day directly on an already-running planet, so their absence is fine.
- Customs Code Expertise reduces NPC POCO tax by 10% per level (max 50% at L5). **NOT modeled** — the user's suggested POCO tax input would need to reflect this manually.
- Verdict: **INCOMPLETE** for NPC POCO users; **N/A** for player POCO users (skill doesn't apply).

### Extractor programs, storage capacity, launchpad capacity — NOT MODELED IN ENGINE

- Only surfaced as m³ figures for planning (`InputVolumePerDay`). The user is expected to derive "how long a launchpad load lasts" externally. Not wrong — a modeling scope decision.

---

## Reprocessing — INCOMPLETE (unimplemented)

- File / function: `engine/industry.go:34, 35` — `IncludeReprocessing bool` and `ReprocessingYield float64` are declared on `IndustryParams`.
- Grep across `engine/` shows **no consumer** of either field. `IncludeReprocessing` is never read; `ReprocessingYield` defaults to 0.50 (`industry.go:420-422`) but is never used in cost math.
- No reprocessing calculator: no formula for `baseYield × (1 + 0.02×L_Reprocessing) × (1 + 0.02×L_ScrapMetal) × (1 + 0.02×L_OreProcessing) × (1 - stationTax) × (1 + implantYield)`.
- No station-tax / structure-yield / Reprocessing Efficiency implant model.
- Verdict: **INCOMPLETE / UNIMPLEMENTED.** If the UI toggles this field, nothing changes downstream.
- Impact: A UI checkbox labelled "consider reprocessing as alternative" or a settable "reprocessing yield" is a UI-only widget with zero backend consequence.

---

## Contracts

### Fee model — MATCHES (as a buyer)

- File / function: `engine/contracts.go:168-196`, `contractSellValueMultiplier`.
- Instant-liquidation path: `sellValueMult = 1 - sellTax/100` (no broker fee — user sells into buy orders as market taker). ✓
- Market-estimate path: `sellValueMult = 1 - (sellTax + sellBroker)/100` (user posts sell orders and pays both). ✓
- **The tool scans public contracts to BUY.** It does not model the fee the LISTER pays (contract creation broker fee), because that's not in the user's cost.
- Verdict: **MATCHES** for the buyer-side use case. Contract creation fee is **N/A** here.

### Contract price validation — HEURISTIC

- File / function: `engine/contracts.go` throughout.
- Constants (`DefaultMaxContractMargin = 100`, `DefaultMinContractPrice = 10M`, `MinSellOrderVolume = 5`, `MaxVWAPDeviation = 30`, `ContractShipModuleValueFactor = 0.55`) are anti-scam / anti-manipulation guardrails, not EVE mechanics.
- The 55% "ship module value factor" haircut (line 41) reflects the fact that public ESI doesn't expose fitted-state metadata reliably — modules attached to a ship that can't be trivially unfitted (rigs) get discounted. Judgment call, not an EVE formula.
- Verdict: **N/A (heuristic).**

### Highsec-restricted capitals — MATCHES

- File / function: `engine/contracts.go:44-67`, `isHighsecRestrictedShipGroup`.
- Group IDs: Titan (30), Dreadnought (485), Carrier (547), Supercarrier (659), Rorqual (883), Force Auxiliary (1538). Name-based fallback for group names not in the ID list.
- Real EVE: these hulls cannot enter highsec via gates. Refusing to route a liquidation through highsec is correct.
- Verdict: **MATCHES.**

### Full-liquidation probability — HEURISTIC (Poisson-like)

- File / function: `engine/contracts.go:247-265`, `fillProbabilityWithinDays`.
- `p = 1 - exp(-horizonDays / fillDays)` per item, then multiplied across items for `fullLiquidationProb`.
- This is an exponential CDF assuming Poisson arrivals — reasonable proxy for market fill time. Multiplying independent per-item probabilities assumes uncorrelated fills.
- Not an EVE mechanic; sound probabilistic model.
- Verdict: **N/A (heuristic).**

---

## PLEX

### Global PLEX region — MATCHES

- File / function: `engine/plex.go:21`, `GlobalPLEXRegionID = 19000001`.
- Real EVE: CCP moved PLEX to a global market region (19000001) on 7 July 2025. Correct.
- Verdict: **MATCHES.**

### NES default PLEX prices — MATCHES

- File / function: `engine/plex.go:24-28`.
- `DefaultNESExtractorPLEX = 293`, `DefaultNESMPTCPLEX = 485`, `DefaultNESOmegaPLEX = 500`.
- These match current CCP NES store prices (as of 2024-2025). User-overrideable via `NESPrices` if CCP changes them.
- Verdict: **MATCHES.**

### Skill Extractor mechanics — MATCHES

- File / function: `engine/plex.go:68-74`.
- `SPPerExtractor = 500,000` ✓
- `MinSPForExtraction = 5,000,000` ✓
- `BaseSPPerHour = 2250` (optimal 27/21 remap, no implants, Omega) ✓
- `SPPerHourPlus5 = 2700` (32/26, +5 implants) ✓

### Skill Injector diminishing returns — MATCHES

- File / function: `engine/plex.go:676-684`.
- < 5M SP → 500,000 SP received per injector
- 5–50M SP → 400,000
- 50–80M SP → 300,000
- \> 80M SP → 150,000
- All correct against CCP's Large Skill Injector table.
- Verdict: **MATCHES.**

### SP farm profitability — MATCHES

- File / function: `engine/plex.go:798-905`, `computeSPFarm`.
- Extractors/month = `BaseSPPerHour × HoursPerMonth / SPPerExtractor` = 2250 × 720 / 500,000 = **3.24** extractors/month (base). ✓
- With +5 implants: 2700 × 720 / 500,000 = **3.888**. ✓
- Startup cost model uses `ceil(startupDays/30)` months of Omega — captures the real "buy 1 more month" step function.
- Break-even PLEX price and payback days are algebraically correct inversions of the profit formula.
- Verdict: **MATCHES.**

### Instant sell vs sell-order — MATCHES

- File / function: `engine/plex.go:830-853`.
- Instant-sell path uses `salesTaxOnly = 1 - salesTax/100` (no broker fee for a market-taker seller). ✓
- Sell-order path uses `netMult = 1 - salesTax/100 - brokerFee/100`. ✓
- Verdict: **MATCHES.**

### PLEX RSI(14) — MATCHES

- File / function: `engine/plex.go:967-1003`.
- Wilder's smoothed RSI seeded with SMA of first 14 changes, then Wilder EMA for the rest.
- Handles avgLoss = 0 (RSI = 100) and both zero (RSI = 50). ✓
- Verdict: **MATCHES** standard TradingView RSI.

### Bollinger Bands — MATCHES

- File / function: `engine/plex.go:944-963`.
- Window 20, population std dev (matches TradingView/Bloomberg convention), ±2σ bands. ✓
- Verdict: **MATCHES.**

### Volume anomaly (CCP sale detection) — SOUND

- File / function: `engine/plex.go:1017-1052`.
- Log-normal z-score model (correct for market volume distributions).
- Signal fires when `volumeSigma > 2 AND change24h < -3%`.
- Verdict: **SOUND (not an EVE mechanic; sensible statistical model).**

---

## Portfolio / Backtest

### FIFO cost basis — MATCHES

- File / function: `engine/portfolio.go:250-520`, `ComputePortfolioPnLWithOptions`.
- Standard FIFO: oldest buy lots consumed first. ✓
- Sales tax applied to sell gross, broker fee applied to both sides. `sellTotal = sellGross - sellBrokerFee - sellTax`. `buyTotal = buyGross + buyFee`. Realized PnL = sellTotal - buyTotal. ✓
- Verdict: **MATCHES** standard accounting.

### Trade Journal (merged trading + manufacturing) — MATCHES

- File / function: `engine/portfolio_manufacturing.go:341-620`.
- Event loop processes buys / job starts / job completes / sells in chronological order with a deterministic tiebreaker.
- Job-start event consumes materials from the trade pool via FIFO (real cost basis) with a fallback to ESI region average when the trade pool is empty. ME reduction uses `meFactor = 1 - ME/100` with a 1% floor. Applied via `ceil(base × runs × meFactor)`.
- Sell fees: `perUnitFees = sellGrossPerUnit × (brokerRate + salesTaxRate)`. Buy fees: broker only, tracked per lot for later netting.
- Trading side and manufacturing side share one lot pool per typeID with source tagging; the `FIFOMode` selector controls which side wins on a same-day tie.
- Verdict: **MATCHES.** Sound accounting.

### Portfolio Sharpe ratio — MATCHES

- File / function: `engine/portfolio.go:640`.
- `Sharpe = (mu / sigma) × sqrt(365)` — annualized from daily returns.
- Verdict: **MATCHES** the standard convention (assumes 365 trading days/year; some conventions use 252 which is stock-market-specific; 365 is right for EVE which trades every day).

### EWMA volatility (RiskMetrics) — MATCHES

- File / function: `engine/risk.go:372-396`.
- λ = 0.94 (RiskMetrics convention). Recursive `σ²_t = λ×σ²_{t-1} + (1-λ)×dev²`.
- Seeded with sample variance instead of a single squared deviation — smart, avoids the small-N "warm-up" noise.
- Verdict: **MATCHES.**

### Cornish-Fisher VaR (small samples) — MATCHES

- File / function: `engine/risk.go:259-341`, `portfolioVarEs`.
- For N < 20: computes CF-adjusted normal quantile using sample skewness (adjusted G1) and excess kurtosis (adjusted G2), then applies `VaR_α = μ + cf_α × σ` and `ES_α = μ - σ × φ(cf_α)/α`.
- For N ≥ 20: empirical (historical simulation) quantiles at 5% and 1%.
- Verdict: **MATCHES** standard financial statistics.

### Portfolio Optimizer — Markowitz mean-variance

- File / function: `engine/optimizer.go`.
- Standard Markowitz MVO on the covariance matrix of item-level daily P&L.
- Minimum 3 trading days per item (`minOptimizerDays = 3`) is very permissive — sample covariance on 3 days is unstable.
- Verdict: **SOUND** as a first-order suggestion tool; **INCOMPLETE** for statistically-reliable allocations.

### Backtest — MATCHES the fee model

- File / function: `engine/backtest.go:260-529`.
- Uses `tradeFeeMultipliers` (same as everywhere else): `buyCost = buyPrice × buyCostMult × qty`, `sellRevenue = sellPrice × sellRevenueMult × qty`, `pnl = sellRevenue - buyCost`.
- Uses order-book replay when available (`backtest_orderbook.go`) or history-based estimation otherwise.
- Cargo trips and route time properly amortize opportunity cost.
- Verdict: **MATCHES.** Legacy-fee-mode caveat (no buy-side sales tax) is correct per EVE, not a bug.

---

## Route / Execution

### Slippage walk (VWAP) — MATCHES

- File / function: `engine/execution.go:132-268`, `ComputeExecutionPlan`.
- Aggregates orders by price level, sorts ascending (for buy = consuming asks) or descending (for sell = consuming bids), walks the book until fill, computes `expectedPrice = costSum / filled`. ✓
- Slippage percent flipped sign for sell side so it's always reported as a positive-value cost. ✓
- Optimal slicing: `n = ceil(qty / (0.05 × totalDepth))`, clamped 1-20. Reasonable participation-rate model.
- Verdict: **MATCHES.**

### Route time — SIMPLE LINEAR (parametric)

- File / function: `engine/route_time.go:15-56`.
- `minutes = jumps × RouteMinutesPerJump + dockStops × RouteDockMinutes`; multiplied by a safety factor for danger; floored at `RouteMinCooldownMin`.
- User provides per-jump minutes. No modeling of warp distance, gate travel time, align time, or specific hull speeds.
- Verdict: **INCOMPLETE** as a physics model; **SOUND** as a user-parametric estimator.

### Highsec ganking / safety model — HEURISTIC

- File / function: `engine/route_time.go:58-68`.
- Multiplies route time by `RouteSafetyMultiplier` (derived from external gankcheck data).
- Not an EVE mechanic; a scaling factor to reflect "you'll actually take X% longer through this route".
- Verdict: **N/A (heuristic).**

### Regional day trader — MATCHES the fee application

- File / function: `engine/regional_day_trader.go:551-587`.
- `unitProfit = targetPrice × sellRevenueMult - sourcePrice × buyCostMult - shipping`. ✓
- ROI clamped at ±10,000% to prevent micro-cap outliers dominating rankings — sensible.
- Verdict: **MATCHES** fee application.

---

## Station Trading

### Margin / ROI formula — MATCHES with a caveat

- File / function: `engine/station_trading.go:601-620`.
- `costToBuy = highestBuy.Price × buyCostMult` (you place a buy at bid, pay bid + broker)
- `revenueFromSell = lowestSell.Price × sellRevenueMult` (you place a sell at ask, receive ask - broker - tax)
- `profitPerUnit = revenue - cost`, `margin = profit / cost × 100`
- The margin definition is markup-on-cost, not gross-margin-on-revenue. That's a semantic choice — either is defensible. Numerically consistent.
- Verdict: **MATCHES.**

### Confidence score / CTS — HEURISTIC

- File / function: `engine/station_trading.go:230-278`, `stationConfidenceScore`.
- Composite of history availability, order-book depth, volatility, side-balance, execution evidence, and manipulation flags.
- Not an EVE mechanic; a scoring model.
- Verdict: **N/A (heuristic).**

### DRVI (Daily Range Volatility Index) — RE-USED FIELD NAME

- File `station_trading.go:115`: JSON tag is `PVI` but the field carries DRVI values. Comment acknowledges this is preserved for backward compat. Not a math bug — a naming legacy.

---

## Skill and standing effects

### What the code applies

- **Accounting** — reduces sales tax by 11%/level. ✓ correct (`suggestedSalesTax`)
- **Broker Relations** — reduces broker fee. ✗ code uses 0.4 pp/level; game uses 0.3 pp/level (Section: Broker fee suggestion).
- **Manufacturing / Advanced Industry / Industry** — implicit in ESI-provided job times; not modeled in the engine's time calc directly (relies on blueprint TE + rig TE).
- **Research skills** (Metallurgy, Research) — irrelevant for planning existing BPO ME/TE, so N/A.
- **Invention skills** (Encryption, Datacores) — **not applied** (Section: Base invention probability).
- **Reprocessing** (Reprocessing, Scrap Metal Processing, per-ore Processing skills) — **not applied** (Reprocessing feature is unimplemented).
- **Customs Code Expertise** — **not applied** to POCO tax input.

### Standings

- No character standing input to broker-fee suggestion. Real broker fee depends on faction + corp standing. User must enter their effective fee manually if standings matter.
- Verdict: **INCOMPLETE.** Broker-fee suggestion is a "no-standings estimate"; the code comment even says so ("No standings adjustment in this estimate; user can tweak after"). Acceptable as a starting point, but the tool never re-derives the fee from character data even when it could (character skills endpoint is already called).

---

## Structure / facility handling

- **Structure hull** (Raitaru / Azbel / Sotiyo / Athanor / Tatara etc.): user passes `StructureJobCostReduction` and `StructureTypeID`. Code applies hull job-cost bonus as a `SystemCost × pct/100` reduction. **MATCHES** current EVE.
- **Structure rigs**: fully modeled via SDE-parsed rig catalog with sec-status multipliers, fit checks (rigs whose `FitsStructureGroups` doesn't match the hull are silently dropped), and additive stacking. **MATCHES.**
- **NPC station vs Upwell structure** for market orders: no differential fee model. Real EVE: NPC stations use CCP's fixed broker/tax; Upwell structures can set their own tariffs. The `SplitTradeFees` mode lets a user express side-specific fees, so a user trading at an Upwell can input the actual structure rate. **MATCHES** with user input; **INCOMPLETE** if the code ever tried to derive fees from the location automatically (it doesn't).
- **Facility tax** vs **SCC**: correctly separated. Facility tax is user-configurable (per structure); SCC is hard-coded 4%. See STALE-RISK note.

---

## Areas requiring author confirmation

1. **Broker Relations coefficient.** Is `0.4 pp/level` intentional (perhaps modeling a specific pre-2020 patch or a private house rule), or a copy-paste error from historical CCP docs? The scanner-suggested fee at L5 (1.0%) does not match the game's L5 (1.5%).
2. **Invention skill bonuses.** Is the assumption "user overrides `InventionChance` when skills matter" documented in the UI, or is the tool silently understating profitability of T2 invention for skilled inventors?
3. **SCC Surcharge constant.** The comment says "post-Uprising" — verify against the current live rate. If CCP announced a change at Fanfest 2026 or later, this needs updating.
4. **Reprocessing feature.** Is the `IncludeReprocessing` flag intentionally dormant (planned for later) or was the implementation lost during a refactor?
5. **Contract broker fee (creation side).** If the tool is ever extended to a "list this as a contract" workflow, the creation broker fee (currently ~1% of the contract's total value, min 10,000 ISK) will need to be added.
6. **100 ISK broker fee floor.** For a scanner catering to bulk trading this rarely bites, but low-value PI/ammo flips will overreport ROI in the current model.
7. **Relist / modify order fee.** The order-desk "reprice" recommendation doesn't gate on the relist cost. Consider whether the recommendation should compute `expected_daily_profit_from_reprice - relist_fee`.

---

## Verified good — headline confirmations

- **Manufacturing job cost breakdown** (post-2026-07-22 fix): EIV × SCI, structure bonus, rig bonus, gross install, facility tax on EIV, SCC on EIV → net. **Matches CCP's canonical formula.**
- **ME multiplier stacking**: `(1 − ME) × (1 − structure) × (1 − rig)` with `max(runs, ceil(...))` per material. **Matches.**
- **EIV uses base quantities**, not ME-reduced. **Matches.**
- **Decryptor table**: every value matches CCP's canonical decryptor stats.
- **T2 BPC base ME/TE/Runs**: 2 / 4 / 10 modules, with per-target override for T2 ships (base 1 run). **Matches** and lock-down tested.
- **PLEX Global market region (19000001)**: correct post-July-2025.
- **SP-per-hour constants** (2250 base, 2700 with +5 implants): correct.
- **Skill injector diminishing returns tiers**: correct.
- **POCO base values per PI tier**: correct.
- **Structure rig sec-status multipliers**: parsed from SDE dogma attributes 2355/2356/2357, correctly gates advanced rigs out of highsec.
- **Sales tax reduction (Accounting × 11%/level)**: correct L5 = 3.6%.
- **Cornish-Fisher small-sample VaR**: standard formula, correctly implemented.
- **RSI(14) with Wilder smoothing**: matches TradingView.
- **Bollinger Bands with population std dev**: matches Bloomberg convention.
- **Slippage via order-book VWAP walk**: correct.
- **FIFO cost basis for portfolio P&L**: standard accounting.
- **`tradeFeeMultipliers` consistency across all engine files**: single source of truth in `fees.go`, no drift between station_trading, scanner, route, regional_day_trader, backtest, execution, contracts, portfolio, portfolio_manufacturing. **This is a big one — one place to change if broker mechanics shift.**
