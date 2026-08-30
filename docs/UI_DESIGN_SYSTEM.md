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

---

## 5. EVE-native flavour

Use CCP's own art. `lib/eveImages.ts` is the only place that builds these URLs.

| Helper | Use |
|---|---|
| `typeIconUrl(typeId, art?, size?)` | Inventory icon for any type |
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
