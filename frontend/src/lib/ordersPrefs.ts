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

export interface OrdersPrefs {
  sortKey: OrdersSortKey;
  sortDir: OrdersSortDir;
  actionFilter: OrdersActionFilter;
  /** Sections the user has folded away, by side. */
  collapsedSell: boolean;
  collapsedBuy: boolean;
  /** Minutes between automatic refreshes; 0 disables them entirely. */
  refreshMinutes: number;
}

/** Item A→Z, matching the in-game Orders window so the two lists can be
 *  walked together. */
export const ORDERS_DEFAULT_PREFS: OrdersPrefs = {
  sortKey: "type",
  sortDir: "asc",
  actionFilter: "all",
  collapsedSell: false,
  collapsedBuy: false,
  refreshMinutes: ORDERS_DEFAULT_REFRESH_MINUTES,
};

export function normalizeOrdersPrefs(raw: string | null): OrdersPrefs {
  const prefs: OrdersPrefs = { ...ORDERS_DEFAULT_PREFS };
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

  if (ORDERS_SORT_KEYS.includes(parsed.sortKey as OrdersSortKey)) {
    prefs.sortKey = parsed.sortKey as OrdersSortKey;
  }
  if (parsed.sortDir === "asc" || parsed.sortDir === "desc") {
    prefs.sortDir = parsed.sortDir;
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
    return { ...ORDERS_DEFAULT_PREFS };
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
