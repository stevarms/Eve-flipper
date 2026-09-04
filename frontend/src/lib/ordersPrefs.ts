// Persisted Orders-tab preferences. Split out of Orders.tsx so the
// tolerate-anything parsing is unit-testable without mounting a table, the
// same way tablePrefs.ts carries ScanResultsTable's column state.
//
// Everything here is a user preference, not data: a corrupt or half-written
// blob must degrade to the defaults rather than break the tab. Callers get a
// fully-populated object back in every case and never need to null-check.

/** Columns the Orders table can sort on. */
export type OrdersSortKey =
  | "owner"
  | "type"
  | "station"
  | "priority"
  | "current"
  | "notional"
  | "eta"
  | "expiry";

export type OrdersSortDir = "asc" | "desc";

/** Which recommendation buckets are listed. */
export type OrdersActionFilter = "all" | "needs_action" | "hold";

export const ORDERS_SORT_KEYS: readonly OrdersSortKey[] = [
  "owner",
  "type",
  "station",
  "priority",
  "current",
  "notional",
  "eta",
  "expiry",
];

const ACTION_FILTERS: readonly OrdersActionFilter[] = ["all", "needs_action", "hold"];

/** Minutes between automatic refreshes; 0 means never. The order desk fans
 *  out an ESI order book and price history per region/type pair, so this is
 *  deliberately coarse — see the auto-refresh notes in Orders.tsx. */
export const ORDERS_REFRESH_CHOICES: readonly number[] = [0, 1, 3, 5, 10];

/** Three minutes is long enough to survive a repricing pass without the
 *  table moving under the cursor, short enough that the book is still
 *  roughly current when you come back to it. */
export const ORDERS_DEFAULT_REFRESH_MINUTES = 3;

export const ORDERS_PREFS_LS_KEY = "eve-flipper:orders-prefs:v1";

/** How long an order is allowed to take before the desk calls it slow. Also
 *  sets the depth cutoff: an order more than half this far down the queue is
 *  reported as buried rather than on track. */
export const ORDERS_DEFAULT_TARGET_ETA_DAYS = 3;
export const ORDERS_TARGET_ETA_MIN_DAYS = 0.5;
export const ORDERS_TARGET_ETA_MAX_DAYS = 30;

/** One layer of the sort stack. */
export interface OrdersSortLayer {
  key: OrdersSortKey;
  dir: OrdersSortDir;
}

/** Past three layers the ordering is decided long before the last one is
 *  consulted, and the chip strip stops being readable at a glance. */
export const ORDERS_MAX_SORT_LAYERS = 3;

export interface OrdersPrefs {
  /** Ordered sort stack, most significant first. Never empty. */
  sort: OrdersSortLayer[];
  actionFilter: OrdersActionFilter;
  /** Sections the user has folded away, by side. */
  collapsedSell: boolean;
  collapsedBuy: boolean;
  /** Minutes between automatic refreshes; 0 disables them entirely. */
  refreshMinutes: number;
  /** Days; passed through to the order desk as its target fill horizon. */
  targetEtaDays: number;
}

/** Item A→Z, matching the in-game Orders window so the two lists can be
 *  walked together. */
export const ORDERS_DEFAULT_PREFS: OrdersPrefs = {
  sort: [{ key: "type", dir: "asc" }],
  actionFilter: "all",
  collapsedSell: false,
  collapsedBuy: false,
  refreshMinutes: ORDERS_DEFAULT_REFRESH_MINUTES,
  targetEtaDays: ORDERS_DEFAULT_TARGET_ETA_DAYS,
};

/** Reads the current stack shape. Returns null — not an empty stack — when
 *  nothing usable is there, so the caller can try the legacy shape next. */
function readSortStack(value: unknown): OrdersSortLayer[] | null {
  if (!Array.isArray(value)) return null;
  const out: OrdersSortLayer[] = [];
  for (const entry of value) {
    if (!entry || typeof entry !== "object") continue;
    const { key, dir } = entry as { key?: unknown; dir?: unknown };
    if (!ORDERS_SORT_KEYS.includes(key as OrdersSortKey)) continue;
    if (dir !== "asc" && dir !== "desc") continue;
    // A column twice over would make the second copy dead weight.
    if (out.some((layer) => layer.key === key)) continue;
    out.push({ key: key as OrdersSortKey, dir });
    if (out.length >= ORDERS_MAX_SORT_LAYERS) break;
  }
  return out.length > 0 ? out : null;
}

/** Prefs written before the sort became a stack carried a single key and
 *  direction. Lift those into a one-layer stack rather than resetting the
 *  tab to defaults on the upgrade. */
function readLegacySort(parsed: Record<string, unknown>): OrdersSortLayer[] | null {
  const key = parsed.sortKey;
  if (!ORDERS_SORT_KEYS.includes(key as OrdersSortKey)) return null;
  const dir =
    parsed.sortDir === "asc" || parsed.sortDir === "desc"
      ? parsed.sortDir
      : defaultDirForSortKey(key as OrdersSortKey);
  return [{ key: key as OrdersSortKey, dir }];
}

/**
 * The whole header-click behaviour, kept out of the component so it can be
 * reasoned about on its own.
 *
 * A plain click means "sort by just this": it collapses the stack to the one
 * column, and clicking again flips its direction. A shift-click builds the
 * stack up instead, cycling each column it touches through
 * default → reversed → gone, so a column can be added and dropped again
 * without reaching for the chip strip.
 *
 * The stack is never returned empty — a table with no ordering at all is not
 * a state worth being able to reach by accident.
 */
export function applySortClick(
  current: readonly OrdersSortLayer[],
  key: OrdersSortKey,
  additive: boolean,
): OrdersSortLayer[] {
  const fallback = defaultDirForSortKey(key);
  const existing = current.find((layer) => layer.key === key);

  if (!additive) {
    if (current.length === 1 && existing) {
      return [{ key, dir: existing.dir === "asc" ? "desc" : "asc" }];
    }
    return [{ key, dir: fallback }];
  }

  if (!existing) {
    const next = [...current, { key, dir: fallback }];
    // Adding a fourth layer displaces the least significant one: the click
    // always does something, and it never reorders what was already there.
    return next.length > ORDERS_MAX_SORT_LAYERS
      ? [...next.slice(0, ORDERS_MAX_SORT_LAYERS - 1), next[next.length - 1]]
      : next;
  }

  if (existing.dir === fallback) {
    return current.map((layer) =>
      layer.key === key ? { key, dir: fallback === "asc" ? "desc" : "asc" } : layer,
    );
  }

  const pruned = current.filter((layer) => layer.key !== key);
  return pruned.length > 0 ? pruned : [{ key, dir: fallback }];
}

/** A spread alone would hand every caller the same `sort` array object. */
function defaultPrefs(): OrdersPrefs {
  return {
    ...ORDERS_DEFAULT_PREFS,
    sort: ORDERS_DEFAULT_PREFS.sort.map((layer) => ({ ...layer })),
  };
}

export function normalizeOrdersPrefs(raw: string | null): OrdersPrefs {
  const prefs: OrdersPrefs = defaultPrefs();
  if (!raw) return prefs;

  let parsed: Record<string, unknown>;
  try {
    const value: unknown = JSON.parse(raw);
    if (!value || typeof value !== "object" || Array.isArray(value)) return prefs;
    parsed = value as Record<string, unknown>;
  } catch {
    // A malformed blob is indistinguishable from a first run.
    return prefs;
  }

  const sort = readSortStack(parsed.sort) ?? readLegacySort(parsed);
  if (sort) prefs.sort = sort;

  if (
    typeof parsed.targetEtaDays === "number" &&
    Number.isFinite(parsed.targetEtaDays) &&
    parsed.targetEtaDays >= ORDERS_TARGET_ETA_MIN_DAYS &&
    parsed.targetEtaDays <= ORDERS_TARGET_ETA_MAX_DAYS
  ) {
    prefs.targetEtaDays = parsed.targetEtaDays;
  }
  if (ACTION_FILTERS.includes(parsed.actionFilter as OrdersActionFilter)) {
    prefs.actionFilter = parsed.actionFilter as OrdersActionFilter;
  }
  if (typeof parsed.collapsedSell === "boolean") prefs.collapsedSell = parsed.collapsedSell;
  if (typeof parsed.collapsedBuy === "boolean") prefs.collapsedBuy = parsed.collapsedBuy;
  if (
    typeof parsed.refreshMinutes === "number" &&
    ORDERS_REFRESH_CHOICES.includes(parsed.refreshMinutes)
  ) {
    prefs.refreshMinutes = parsed.refreshMinutes;
  }

  return prefs;
}

export function loadOrdersPrefs(): OrdersPrefs {
  try {
    return normalizeOrdersPrefs(localStorage.getItem(ORDERS_PREFS_LS_KEY));
  } catch {
    // Storage can throw outright in a locked-down browser profile.
    return defaultPrefs();
  }
}

export function saveOrdersPrefs(prefs: OrdersPrefs): void {
  try {
    localStorage.setItem(ORDERS_PREFS_LS_KEY, JSON.stringify(prefs));
  } catch {
    // Preferences are a convenience; losing them must not break the tab.
  }
}

/** Direction a column should take when it is first clicked. Names read
 *  naturally A→Z; money, priority and "how long is left" read worst-first. */
export function defaultDirForSortKey(key: OrdersSortKey): OrdersSortDir {
  switch (key) {
    case "priority":
    case "notional":
    case "current":
      return "desc";
    default:
      return "asc";
  }
}
