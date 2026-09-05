# EVE Flipper — UI Design System

*Established during the UI overhaul (August 2026). This is the contract every
new or migrated surface follows.*

`docs/UI_AUDIT.md` fixed the **words** — tab labels, bid/ask, ROI naming — and
that work has landed. This document covers what it did not: **structure,
density, hierarchy and visual language**.

---

## 1. The problem this solves

Measured on the pre-overhaul app:

| Symptom | Measurement |
|---|---|
| DOM weight | **44,773 elements / 1,539 table rows** live at once — `TabPanel` rendered all 11 tabs and hid inactive ones with a CSS `hidden` class. Chrome needed >2 min to rasterize one frame. |
| Chrome before content | Industry → Discover stacked **ten** bands before the table header; first data row at ~45% viewport depth. Station Trade spent two-thirds of the first screen on a 12-field settings form. |
| Unreadable headers | Station Trade shipped 20 columns including `CTS`, `D.O.S.`, `SDS` — with **no hint text anywhere in the locale**. |
| No hierarchy | Every label, header, value and nav item was 11px uppercase JetBrains Mono. |
| Meaningless colour | Green item names, blue product names, orange scores, yellow "Watch", green "OK". |

---

## 2. Colour: it must mean something

Semantic tokens live in `frontend/src/index.css` under *Semantic design
tokens*, exposed through `tailwind.config.ts`. They are **palette-independent**
— green means profit in all six faction themes.

| Token | Tailwind | Means |
|---|---|---|
| `--sem-profit` | `text-profit` `bg-profit/10` | Gain, positive ROI, favourable spread |
| `--sem-loss` | `text-loss` | Loss, negative trend, unprofitable |
| `--sem-warn` | `text-warn` | Caution — thin liquidity, stale price, risk |
| `--sem-info` | `text-info` | Neutral information, links, hints |
| `--sem-neutral` | `text-muted` | De-emphasised (named `muted`; `neutral` is a built-in Tailwind scale) |

**Never** use a semantic colour decoratively. If a number is green it is
because it made ISK, not because green looked nice there.

**Text ramp** — `text-fg` / `text-fg-secondary` / `text-fg-tertiary`. Primary is
brighter (232) than the legacy `--eve-text` (192) so headings can out-rank body
copy. The old `text-eve-text` / `text-eve-dim` still work and are unchanged.

**Surface ramp** — `bg-surface-0…3`, aliased to the active faction palette, so
faction themes still drive the chrome.

**Security status** — `text-sec-high|mid|low|null`, EVE's canonical in-client
ramp. Use `securityTone()` from `lib/eveImages.ts` to pick the bucket.

---

## 3. Typography: mono is for digits

The single biggest cause of "everything looks the same" was one typeface at one
size for every role.

- **`font-ui`** (Segoe UI / system sans) — nav, labels, headings, prose,
  buttons. Proportional, already installed on Windows, zero network cost.
- **`font-num`** (JetBrains Mono, `tabular-nums`) — numbers **only**. Without
  tabular figures, columns of ISK values visually jitter because glyph advance
  widths differ. Shortcut: the `.tnum` class or `data-numeric="true"`.

**Scale** — additive and `t-` prefixed, because Tailwind's default
`xs`/`sm`/`base` scale is load-bearing across 87k LOC of existing markup and
must not shift:

| Class | Size | Role |
|---|---|---|
| `text-t-caption` | 11 | Captions, badges |
| `text-t-cell` | 12 | Table cells |
| `text-t-body` | 13 | Body copy, controls |
| `text-t-emphasis` | 15 | Row identity — the item name |
| `text-t-title` | 18 | Section titles |
| `text-t-display` | 24 | Page titles, KPI figures |

Weight carries hierarchy alongside size. Retire blanket
`uppercase tracking-widest`; it is reserved for the one caption role.

**Density** — `--row-h` (34 / 28 / 24px) driven by `data-density`, bound to the
existing `CockpitDensity` setting in `lib/cockpit.ts`. Use `h-row`.

---

## 4. The three-tier disclosure rule

Every data surface has exactly three tiers. This is what "simple face, detail on
demand" means concretely. **No data is ever removed — only re-tiered.**

1. **Decide** — at most **6 columns**: item identity (icon + name + type ID),
   the one primary metric (coloured, default sort, leftmost data column), ROI,
   capital required, a trend glyph, an action. Everything else hidden.
2. **Inspect** — clicking a row opens a right-hand drawer with *all* remaining
   columns grouped under labelled sections, the order book, a price sparkline,
   and the why-this-score reasoning that currently hides inside 200-word
   tooltips.
3. **Configure** — a column picker for power users who want more in the grid.

**Filters are tabbed, not stacked.** `General / Profit / Risk / My Assets` with
three fields visible at a time, collapsed after the first scan.

**Every tool states its model and its exclusions in plain English**, e.g.
*"11,735 items hidden: under 5 trades or 10 units a day, or no real order on one
side of the book. Their profit could not be realised."* Teach while filtering.

### Worked example — Station Trade

The first tab taken through all three tiers, verified against a live Jita 4-4
scan (1,500 opportunities):

| | Before | After |
|---|---|---|
| Grid columns | 20, h-scrolling, uniform weight | **6**, no h-scroll |
| Leads with | `CTS` (no hint text anywhere) | `DAILY PROFIT`, coloured, default sort |
| Filters | 12 fields stacked, ⅔ of the screen | 4 tabs, 3–4 fields at a time |
| Table header at 1600×1000 | y≈600 | y≈428 |
| DOM, 100-row page | 3,165 elements | **1,610** |
| Rows visible | ~13 | ~20 |

Nothing was removed. The other fourteen columns are in the drawer *and* still
available in the grid via **Columns**.

### Rolled out

| Grid | Columns before → after | DOM, 100-row page |
|---|---|---|
| Station Trade | 20 → **6** | 3,165 → 1,610 |
| Radius / Regional | 35 → **6** | 5,299 → 2,105 |
| Contracts | 16 → **6** | — |
| Order desk (`Orders.tsx`) | 11 → **6** | — |
| Positions (`positions`, new) | built at **6** | — |

Surfaces measured and found already conforming, so deliberately left alone:
Price Audit (5 + 4 columns), Trade Journal (~8, and it already has a drawer),
War Tracker (no tables at all), and the PLEX arbitrage matrix (6 columns —
what it needed was not fewer columns but honest *headings*: see
`docs/DUPLICATION.md` cluster 13).

**A tab is only as discoverable as where it is mounted.** PLEX was a full
market dashboard rendered inside `CharacterPopup`, behind a login it does not
require — public data, `isLoggedIn` defaulting to `false`. It is now the third
Assets tab. Before adding a surface to a modal, check whether the modal is
actually the thing that owns it.

The PLEX lesson generalised: nine more tools — Jobs, PI colonies, Transactions,
Wallet, Risk, P&L, Optimizer, Edge, and the modal's order tab — were mounted in
the same dialog and are now workspace tabs. A tool whose only entry point is a
portrait click has no entry point.

**Removing a column must not remove its sort.** The order desk dropped the
Expiry and Notional headers; both sorts survive in a toolbar `<select>` that
lists every sort key, while the three columns that stayed keep clickable
headers. A sort that only existed as a table header is a feature deleted by
accident.

Decide columns are **ordered**, not merely a set — the primary metric must be
the leftmost data column, and declaration order in the column-def arrays does
not match reading order.

**Persistence rule:** a table must not write column preferences until the user
actually changes a column. Both scan tables originally wrote on mount, which
clobbered the computed default; `raw === null` has to reliably mean "never
configured". `normalizeColumnPrefs(raw, order, defaultHidden)` applies the
default only when nothing is stored, and an explicitly empty saved `hidden`
counts as a real preference so un-hiding everything sticks.

### Worked example — Positions

The first grid designed to the three tiers from the start rather than reduced
into them. **The decide row answers one question: should I sell this today?**
Every column is chosen against that question, and anything that fails the test
goes to the drawer:

| # | Column | Why it earns a decide slot |
|---|---|---|
| 1 | **Item** | Icon + name — what am I looking at |
| 2 | **Qty** | Size of the position |
| 3 | **Avg cost** | What it cost, per unit, FIFO |
| 4 | **Now** | Best sell at the pricing hub — the other half of the comparison |
| 5 | **Unrealized** | ISK, with % underneath, green/red — the answer itself |
| 6 | **Age** | Days held; a stale position is a different decision to a fresh one |

Plus a right-hand action, which is not a data column: **List**, or **Listed ×N**
when the type is already on the book.

Inspect tier (drawer): source (trade / manufacture / manual), the individual
lots and their dates, target price, listed quantity and current order price, the
fee breakdown, and edit/delete for manual entries.

Two rules this example establishes:

**Profit columns are net, or they are lies.** *Unrealized* subtracts broker fee
and sales tax on the sell side, from the character's real fee profile
(`character_market_fees.go`). A position that shows green before fees and red
after would have the grid recommending a loss.

**A hand-entered row is its own row.** A manual holding for a type that also has
FIFO history is listed separately (source `manual`) rather than averaged into
the derived one. Blending a guessed cost basis into a measured one corrupts the
measured number, and the user can no longer tell which is which.

---

## 5. EVE-native flavour

Use CCP's own art. `lib/eveImages.ts` is the only place that builds these URLs.

| Helper | Use |
|---|---|
| `<TypeIcon typeId categoryId? />` | **Use this in grids.** Blueprints 400 on `/icon`, and `CategoryID` is optional on the wire *and not persisted in scan history* — so the category alone cannot be trusted. TypeIcon uses it when present and otherwise retries `/bp` once before hiding. |
| `typeIconUrl(typeId, art?, size?)` | Raw URL, when you know the variant |
| `blueprintIconUrl(typeId, isCopy)` | **Blueprints do not serve `/icon`** — they 400. They serve `/bp` (original) and `/bpc` (copy). Using the right one is what separates the BPO/BPC row pairs in the Industry scanner that otherwise look like duplicates with identical economics. |
| `characterPortraitUrl` / `corporationLogoUrl` / `allianceLogoUrl` | Entity art |
| `securityTone` / `formatSecurity` | EVE's security-status ramp and 1-decimal display |

Item icons belong in **every** row that names an item. Before the overhaul they
appeared only inside `character-popup/` and never in a scan table — the cheapest
readability win available.

Panels take EVE Photon UI cues: dark desaturated ground, thin borders, angular
corners, restrained accent. The six faction palettes already map to real faction
colours and remain as accent-hue overrides on one tuned base theme.

---

## 6. Components

Built on **shadcn/ui** (Radix primitives, source copied into the repo — not a
runtime dependency) and **TanStack Table v9** + **TanStack Virtual v3**.

The repo was already most of the way there before the overhaul: `@/` alias in
both `vite.config.ts` and `tsconfig.json`, the exact shadcn `cn()` helper in
`lib/utils.ts`, and `manualChunks` entries for `@tanstack`, `@radix-ui` and
`cmdk` already present in the vite config.

**Virtualize any table over ~100 rows.** This is not optional; it is the
difference between 44k and <5k DOM nodes.

Reuse before building: `lib/format.ts` (ISK/number formatting — the one true
copy), `components/EmptyState.tsx` (i18n-aware, 7 canonical reasons),
`components/PresetPicker.tsx`, `components/journal/PnLPrimitives.tsx`.

Shared pieces the overhaul added, each hoisted the moment a second copy was
about to exist:

| Primitive | What it is, and the mistake it prevents |
|---|---|
| `ui/DetailList.tsx` — `DetailGroup` / `DetailRow` | The label/value pair every tier-2 drawer is made of. Both row drawers had grown a private `Row`/`Group`; they can no longer drift apart. |
| `ui/CopyPrice.tsx` | Copies `value.toFixed(2)` — a **plain** number, because EVE's price field rejects "1.23 M". Confirms in place with a check mark rather than a toast (this is a per-row action repeated a dozen times a sitting), and stops propagation because rows are clickable. |
| `ui/LoadingBlock.tsx` — `LoadingBlock` / `Spinner` | The one spinner. Replaced thirteen hand-copied blocks across twelve files, three of which had drifted to hardcoded English and one of which asked for `border-3` (Tailwind ships 0/1/2/4/8) and so rendered no ring at all. Carries `role="status"` / `aria-live="polite"` — a spinner is precisely what a screen-reader user cannot see. |

**Empty is not the same as loading.** Roughly ten sites that looked like
candidates for `EmptyState` turned out to be loading spinners; routing those
through `EmptyState` would swap a live indicator for static text. Waiting →
`LoadingBlock`. Finished with nothing to show → `EmptyState`.

**Interpolated utility classes do not exist.** Tailwind scans source *text*, so
`text-${align}` is in the bundle only by luck — some other file happened to
mention `text-right` literally. Write the branch out: `align === "right" ?
"text-right" : "text-left"`. The order desk shipped the interpolated form for
months and got away with it; the corp dashboard's `border-3` did not.

---

## 7. Non-negotiables

- **Locale parity is a build gate.** `TranslationKey = keyof typeof ru`, so a
  key added to `en.ts` but not `ru.ts` is a TypeScript compile error. Both files
  sit at 2,857 keys. Russian may stay terse — the key must exist.
- **No data removed.** Re-tiering only. Every column reachable before the
  overhaul stays reachable after it.
- **Tailwind stays on 3.4** for this overhaul. The RGB-triplet CSS-var system is
  already the right shape for tokens and shadcn/ui supports v3 fully; a v4
  migration across 87k LOC of utility classes is orthogonal risk. Separate
  opt-in later.
