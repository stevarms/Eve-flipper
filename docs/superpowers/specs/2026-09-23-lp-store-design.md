# LP Store — design

Date: 2026-09-23
Status: approved in brainstorming, pending spec review

## Goal

Help spend loyalty points well. The user has ~3M LP from faction warfare and
wants to know, for every offer in an LP store, what it is actually worth per
LP — whether sold as-is, or (for blueprints) sold as a copy or built and the
product sold — and then assemble a redemption basket and copy everything it
needs to buy in Jita as one multibuy.

Existing tools (Fuzzwork's LP store page) get the arithmetic of multi-run
blueprints right but price required items (tags, supply packages) at zero,
use a single price basis, ignore the real build cost (job install, structure
and rig bonuses) and ignore liquidity. This tool fixes those.

### Reference case

Store 1000180 (State Protectorate), offer 19596: one 10-run Raven Navy Issue
Blueprint copy for 200,000 LP + 200,000,000 ISK + 8× Federal Strategic Materiel
Supply Package. Fuzzwork reports −87.21 ISK/LP:

    10 × 410,850,276.24  − 3,925,945,320 − 200,000,000 = −17,442,558
    −17,442,558 / 200,000 = −87.21

which is exactly its figure with the supply packages priced at zero. Three
other offers give a 1-run copy of the same blueprint for 100,000 LP plus
either 20M ISK, 1× Estamel Tharchon's Tag, or 2× Caldari AZ-1 Nexus Chip.

## Decisions

| Question | Decision |
|---|---|
| What values per offer | All that apply, side by side: sell as-is, sell the blueprint copy, build and sell the product |
| Sell price basis | Both: **instant** (into buy orders) and **listed** (sell orders), with liquidity |
| Which store / how much LP | Read LP balances from ESI when the new scope is granted; otherwise pick a store and type LP |
| Build settings | Shared with the Profitable Blueprints scanner (same saved parameters) |
| Blueprint copy sale value | Median contract asking price per run, with sample count, plus a per-blueprint user override |
| Scope of v1 | Ranked table + selection basket with tally and multibuy. No automatic planner |
| Architecture | Backend analyzer (approach A), new tab |

## Facts the design relies on

- ESI `GET /loyalty/stores/{corporation_id}/offers/` is public and returns, per
  offer: `offer_id`, `type_id`, `quantity`, `lp_cost`, `isk_cost`, `ak_cost`,
  `required_items[] {type_id, quantity}`. Store 1000180 has 387 offers.
- **For a blueprint offer, `quantity` is the number of runs on a single copy.**
  Verified against the in-game store: offer 19596 (`quantity: 10`) is one
  10-run copy; offers 14797/14798/19397 (`quantity: 1`) are 1-run copies.
- LP store blueprints are copies at **ME 0 / TE 0**, and copies cannot be
  researched, so this is fixed.
- Blueprint copies cannot be sold on the market, only through contracts.
- ESI `GET /characters/{character_id}/loyalty/points/` needs scope
  `esi-characters.read_loyalty.v1`.
- Public contract listings include `volume` but not items; items are one
  request per contract. A blueprint copy's volume is 0.01 m³.

## Calculations (`internal/engine/lp_store.go`)

Pure Go, no HTTP or DB, JSON-tagged types.

### Inputs per offer

- The offer (above).
- Product market data in the pricing region: best bid, best ask, bid/ask depth,
  average daily volume.
- Required-item prices: best ask in the pricing region (what buying them costs).
- Fees: sales tax %, broker fee % (from the shared scanner parameters).
- For blueprint offers: the industry analysis of the product at
  `runs = quantity`, ME 0, TE 0, using the shared build settings; and the
  contract price per run (or the user's override).

### Cost

    cost = isk_cost + Σ(required_item.quantity × required_item.best_ask)

If any required item has no ask in the pricing region, the offer is
**unpriced**: every ISK/LP value for it is null (shown as "?"), and it sorts
last. It is never costed as zero.

`ak_cost` (Analysis Kredits) is ignored.

### Values

Each value is `(revenue − cost) / lp_cost`, in ISK/LP. A value is null when it
does not apply or its inputs are missing.

| Value | Revenue | Applies to |
|---|---|---|
| `instant` | `quantity × best_bid × (1 − sales_tax)` | Market-sellable products |
| `listed` | `quantity × best_ask × (1 − broker_fee − sales_tax)` | Market-sellable products |
| `bpc_sale` | `runs × price_per_run` | Blueprint offers |
| `build_instant` | see below | Blueprint offers |
| `build_listed` | see below | Blueprint offers |

`bpc_sale` carries no broker fee or sales tax: contracts pay neither, only a
small flat creation fee, which is ignored.

For the build values, the industry analyzer already returns profit =
sell revenue (after fees) − optimal build cost (materials + job install). The
analysis must be run with **no blueprint acquisition cost**
(`IndustryParams.OwnBlueprint = true`, so `BlueprintCostIncluded == 0`),
overriding whatever the shared scanner parameters say: the blueprint's cost is the offer's cost, and
counting it twice would understate every build value. So:

    build_instant = (InstantSellProfit − cost) / lp_cost
    build_listed  = (MakerSellProfit  − cost) / lp_cost

where `cost` is the offer's cost above. `InstantSellAvailable == false` makes
`build_instant` null.

Blueprint copies are not market-sellable, so `instant`/`listed` are null for
blueprint offers.

### Contract price per run

From public item-exchange contracts in the pricing region, not expired, with
positive price, whose volume is ≤ 0.011 m³ (pre-filter), whose items are
exactly one blueprint copy of the type (included, `is_blueprint_copy`,
`runs > 0`): `price / runs` per contract. The value is the **median**, with the
sample count. Zero samples → null with reason "no contracts". The user's
override, when set, replaces it (and the row says so).

### Best

`best` = the maximum non-null value, with its method label:
`sell` (instant), `list` (listed), `sell_bpc`, `build_sell`, `build_list`.

### Liquidity

`units_per_redemption` = `quantity` for items, and for blueprints the product
units (`runs × product units per run`). `days_to_sell(n)` =
`n × units_per_redemption / avg_daily_volume` of the product (null without
history). The tally warns when a selection exceeds ~7 days.

### Grouping

Offers are grouped by product `type_id`. The group's headline is its best
variant's `best`. Offers with the same product and different costs are the
point of the grouping (e.g. the four Raven Navy Issue trade-ins).

## Backend

### ESI (`internal/esi/loyalty.go`)

- `FetchLoyaltyOffers(corpID)` with a cache honouring ESI expiry.
- `FetchCharacterLoyaltyPoints(characterID, token)` → `[]{corporation_id, loyalty_points}`.
- Reuse `FetchRegionContractsCached` and `FetchContractItemsBatch` (with the
  scanner's `ContractItemsCache`) for blueprint prices.

### Scope

Add `esi-characters.read_loyalty.v1` to the SSO scope list in **both**
`main.go` and `main_wails.go`. Release notes must say a re-login is needed
once to enable automatic LP balances.

### Routes (`internal/api/lp_store.go`)

| Route | Purpose |
|---|---|
| `GET /api/lp/corporations` | Corporations with LP stores; the four militia corps first |
| `POST /api/lp/analyze` | NDJSON stream (below). Body: `{corporation_id, pricing, build}` where `build` is the scanner's saved parameters, sent by the tab |
| `GET /api/auth/lp/balances` | LP per corporation, or `{available: false}` without the scope / login |
| `GET /api/auth/lp/bpc-prices` | The user's price-per-run overrides `{type_id: price}` |
| `PUT /api/auth/lp/bpc-prices` | Set or clear one override `{type_id, price_per_run \| null}` |

Hosted-quota classification in `hosted_access.go`: `POST /api/lp/analyze` →
`"scans"`. `PUT` is not a POST and needs no entry; if implemented as POST, it
is unmetered (`"", false`). `TestHostedQuotaFeatureMappingClassifiesAllPostAPIRoutes`
must pass.

### Stream

Messages `{type: "progress" | "offers" | "build" | "bpc" | "warning" | "done" | "error"}`:

1. `offers` — all offers with cost, `instant`, `listed`, liquidity, grouping.
   Arrives first so the table is usable in seconds.
2. `build` — one message per blueprint offer as its industry analysis
   completes (worker pool; each worker shallow-copies the `IndustryAnalyzer`,
   per CLAUDE.md). Includes `FlatMaterials` for the multibuy.
3. `bpc` — per blueprint type, contract median and sample count.
4. `done`.

All writes to the `ResponseWriter` go through one mutex (the `writeMu`
pattern in `industry_blueprint_scan.go`). A failure in phase 2 or 3 for one
offer is attached to that offer; a phase that fails entirely emits a
`warning` and the stream still ends with `done`. A failure fetching offers is
an `error`.

### DB (`internal/db/lp_store.go`)

Table `lp_bpc_price_overrides(user_id, type_id, price_per_run, updated_at)`,
primary key `(user_id, type_id)`, migration in `db.go`.

## Frontend

New top-level tab **LP Store** (next to FW Supply), component
`frontend/src/components/LPStoreTab.tsx` (split into subcomponents as it
grows). API calls in `lib/api.ts`, types mirrored in `lib/types.ts`, all
strings in both `lib/locale/en.ts` and `lib/locale/ru.ts`.

### Header

Store picker (militia corps first; stores where the character has LP show the
balance). LP balance from ESI, or an input when unavailable. A note that build
settings come from the Profitable Blueprints scanner, linking there. Analyze
button.

### Table

Columns: Item, LP, ISK + items (cost), Instant, Listed, BP sale, Build
(instant), Build (listed), Best (value + method), Vol/day. All values in
ISK/LP. Inapplicable cells "—", unpriced "?", pending phases show a spinner.
The best cell per row is highlighted. Default sort: Best descending. Filters:
All / Sellable / Blueprints, min ISK/LP, search.

Offers for the same product are grouped under a parent row showing the best
variant; expanding shows each trade-in. Blueprint rows show "N runs".

Row detail (click): required items with prices, fees, contract price per run
with sample count and the **override input**, industry build cost breakdown,
depth and volume. Copy buttons: required items multibuy, build materials
multibuy (blueprints).

### Selection basket

Each offer row has a checkbox and a redemption count (default 1). A tally bar
above the table, always mounted, shows:

- Total LP vs balance (red when over; not a block).
- Total ISK needed: store ISK + required items at Jita ask.
- Expected return at each row's best method, and blended ISK/LP.
- Liquidity warnings for rows whose count exceeds ~7 days of volume.
- **Copy to multibuy**: required items across all selected offers,
  `quantity × count`, merged by type (two offers needing 4 and 8 of the same
  package → one line of 12).
- Checkbox **Include build materials for blueprints** (default off): adds each
  selected blueprint's `FlatMaterials × count` to the same paste, merged by type.

### Persistence

Results, selection and counts in `sessionStorage` (same pattern as the
Profitable Blueprints scanner). Store choice and filters in `localStorage`.
Build/pricing parameters are read from the scanner's `localStorage` key, not
duplicated.

## Error handling

| Situation | Behaviour |
|---|---|
| Offers fetch fails | Stream `error`; nothing to rank |
| Required item has no ask | Offer unpriced: values "?", sorted last |
| Product has no bid / ask | That value "—"; ranked on the rest |
| No matching contracts | `bpc_sale` shows "no contracts"; override still usable |
| Industry analysis fails for one offer | That offer's build cells show the error on hover; scan continues |
| Contract phase fails or times out | Warning naming the phase; other values stand |
| No scope / logged out | Typed LP; overrides need login; everything else works |
| Basket over LP balance | Tally red; copy still works |
| SDE loading | Existing `isReady()` gate |

## Testing

Engine first (TDD), table-driven:

- Reference case: offer 19596 with supply packages at zero reproduces
  −87.21 ISK/LP; with packages priced, the value drops by exactly
  `8 × price / 200,000`.
- `runs = quantity` for blueprint offers.
- Instant vs listed fee arithmetic.
- Contract median per run, including outliers and zero samples; override wins.
- Unpriced required item → null values, never zero cost.
- Grouping and best-method selection.
- Multibuy merge: across offers, × count, with and without build materials.

API: handler tests with a fake ESI; NDJSON phase order and the `done`
terminator; per-offer failure isolation; override save/load; the hosted-quota
classification test.

Frontend (vitest): tally totals, over-balance state, multibuy paste text,
grouping display.

Manual: run locally against store 1000180 and compare several rows with
Fuzzwork — values should agree except where this tool deliberately differs
(required items, fees, build costs).

## Out of scope (v1)

- Automatic redemption planner.
- Per-offer runs override (ESI `quantity` is reliable).
- Analysis Kredits offers.
- Selling blueprint copies via the tool (contract creation).
- Pricing in more than one region per scan.
