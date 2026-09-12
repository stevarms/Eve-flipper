import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  getAuthStatus,
  getOrderDesk,
  getOrderDisposition,
  getUndercuts,
} from "../lib/api";
import type { CharacterMarketFees } from "../lib/api";
import type {
  AuthCharacter,
  BookLevel,
  DispositionPlan,
  DispositionResponse,
  OrderDeskOrder,
  OrderDeskResponse,
  OrderDeskSettings,
  TodayDeepLink,
} from "../lib/types";
import { useEsiFeeImport } from "../lib/useEsiFeeImport";
import {
  applySortClick,
  loadOrdersPrefs,
  saveOrdersPrefs,
  ORDERS_MIN_MARGIN_PCT_MAX,
  ORDERS_MIN_MARGIN_PCT_MIN,
  ORDERS_REFRESH_CHOICES,
  ORDERS_TARGET_ETA_MAX_DAYS,
  ORDERS_TARGET_ETA_MIN_DAYS,
  type OrdersActionFilter,
  type OrdersPrefs,
  type OrdersSortKey,
  type OrdersSortLayer,
} from "../lib/ordersPrefs";
import { useI18n, type TranslationKey } from "../lib/i18n";
import { formatIsk as formatIskLib } from "../lib/format";
import { formatGridPrice, priceStep } from "@/lib/pricing";
import { CopyPrice } from "@/components/ui/CopyPrice";
import { useKeyboardShortcuts } from "@/lib/useKeyboardShortcuts";
import { useEveUiActions } from "@/lib/eveUiActions";
import { useOptionalToast } from "@/components/Toast";
import { ItemRef } from "@/components/ui/ItemRef";
import { OrderRowDrawer } from "@/components/orders/OrderRowDrawer";
import { OrderHistoryPanel } from "@/components/orders/OrderHistoryPanel";
import { cn } from "@/lib/utils";

// Orders.tsx — first-class main-tab replacement for the buried
// character-popup Order Desk. Aggregates active orders across every
// authorized character with the same per-order recommendations
// (hold / reprice / cancel) plus legal 4-sig-fig suggested prices from
// the item 1/2/3 chain. Renders one row per order with an action badge,
// copy-to-clipboard for the target price, and a ⚠ warning when the
// broker relist fee would eat the theoretical gain.
//
// The tab is used side by side with the in-game Orders window, which drives
// three things that are otherwise unobvious:
//
//   - Sell and buy are separate sections, because the game separates them
//     and the two lists are walked together.
//   - The default sort is item name A→Z, for the same reason.
//   - Data is refreshed on a deliberate interval rather than on every window
//     focus. Repricing means alt-tabbing to EVE and back constantly, and the
//     endpoint behind this table takes seconds.

interface Props {
  isLoggedIn: boolean;
  /**
   * A row to open on, when the tab was reached from somewhere that already
   * knew which one — Today's actions deep-link here. Null when the user
   * navigated normally.
   */
  focus?: TodayDeepLink | null;
  /** Called once the focus has been applied, so returning later does not
   *  re-open a row that has already been dealt with. */
  onFocusConsumed?: () => void;
}

const PRIORITY_BY_ACTION: Record<string, number> = {
  cancel: 4,
  review: 3,
  reprice: 2,
  hold: 0,
};

/** Every action except "hold" wants a decision from you. `review` is one of
 *  them — the desk cannot call an underwater sell order on its own, but that
 *  is precisely a row you have to look at, not one to hide behind the filter. */
function needsAttention(row: OrderDeskOrder): boolean {
  return (
    row.recommendation === "reprice" ||
    row.recommendation === "cancel" ||
    row.recommendation === "review"
  );
}

/** One expanded disposition panel. Kept per order id in the tab rather than
 *  in the row, so collapsing a section or re-sorting does not throw away a
 *  request that cost four region books to answer. */
interface DispositionState {
  loading: boolean;
  error?: string;
  data?: DispositionResponse;
}

/** Header label for each sortable column, reused by the chip strip so a
 *  chip and its column can never drift apart. */
const SORT_LABEL_KEY: Record<OrdersSortKey, TranslationKey> = {
  owner: "ordersColOwner",
  type: "colItem",
  station: "ordersColStation",
  priority: "ordersColAction",
  current: "ordersColCurrent",
  notional: "ordersColValue",
  eta: "ordersColEta",
  margin: "ordersColMargin",
  expiry: "ordersColExpiry",
};

/** Columns rendered per row. Group header rows span all of them. */
const COLUMN_COUNT = 12;

/** Editing a fee re-runs the whole server-side computation, so wait for the
 *  user to stop typing rather than firing one request per digit. */
const FEE_DEBOUNCE_MS = 600;

/** Local presentation of the shared formatter (lib/format.ts). */
function formatIsk(v: number): string {
  return formatIskLib(v, undefined, {
    maxTier: "T",
    space: false,
    decimals: { t: 2, b: 2, m: 2, k: 1, unit: 0 },
  });
}

/** Value that sorts last regardless of direction is wrong for "unknown", so
 *  unknowns get +Infinity and land at the end of an ascending sort. */
function orInfinity(v: number): number {
  return v >= 0 ? v : Number.POSITIVE_INFINITY;
}

/** Unmeasured rows sort last in both directions: "no data" is not a rank. */
function marginSortValue(row: OrderDeskOrder): number {
  return row.margin_basis === "none" ? Number.POSITIVE_INFINITY : row.margin_percent;
}

function compareBy(a: OrderDeskOrder, b: OrderDeskOrder, key: OrdersSortKey): number {
  switch (key) {
    case "owner":
      return (a.character_name || "").localeCompare(b.character_name || "");
    case "type":
      return (a.type_name || "").localeCompare(b.type_name || "");
    case "station":
      return (a.location_name || "").localeCompare(b.location_name || "");
    case "priority":
      return (PRIORITY_BY_ACTION[a.recommendation] ?? 0) - (PRIORITY_BY_ACTION[b.recommendation] ?? 0);
    case "current":
      return a.price - b.price;
    case "notional":
      return a.notional - b.notional;
    case "eta":
      return orInfinity(a.eta_days) - orInfinity(b.eta_days);
    case "margin":
      // Ascending puts the losses first, which is the point of the column.
      // An unmeasured row is not a good one — it is a row with no answer —
      // so it sorts to the end either way rather than to the top.
      return marginSortValue(a) - marginSortValue(b);
    case "expiry":
      return orInfinity(a.days_to_expire) - orInfinity(b.days_to_expire);
  }
}

/** Walks the sort stack, first column that separates the two rows decides.
 *  "Item, then Action" is the case this exists for: alphabetical to line up
 *  with the in-game window, with the actionable rows grouped inside each. */
function sortOrders(
  rows: OrderDeskOrder[],
  sort: readonly OrdersSortLayer[],
): OrderDeskOrder[] {
  return rows.slice().sort((a, b) => {
    for (const layer of sort) {
      const cmp = compareBy(a, b, layer.key);
      if (cmp !== 0) return layer.dir === "asc" ? cmp : -cmp;
    }
    // Ties always break alphabetically, never by direction. "Value, highest
    // first" then reads down the page in a stable, findable order.
    return (a.type_name || "").localeCompare(b.type_name || "");
  });
}

export function Orders({ isLoggedIn, focus, onFocusConsumed }: Props) {
  const { t, locale } = useI18n();
  const [data, setData] = useState<OrderDeskResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [salesTax, setSalesTax] = useState<number>(8);
  const [brokerFee, setBrokerFee] = useState<number>(1);
  const [characterFilter, setCharacterFilter] = useState<Set<number>>(new Set());
  const [authCharacters, setAuthCharacters] = useState<AuthCharacter[]>([]);
  const [prefs, setPrefs] = useState<OrdersPrefs>(() => loadOrdersPrefs());
  const [lastLoadedAt, setLastLoadedAt] = useState<number>(0);
  const [now, setNow] = useState<number>(() => Date.now());

  const [feesReady, setFeesReady] = useState(false);
  const [feeSource, setFeeSource] = useState<string | null>(null);
  const [expandedOrders, setExpandedOrders] = useState<Set<number>>(new Set());
  const [dispositions, setDispositions] = useState<Record<number, DispositionState>>({});
  const [inspected, setInspected] = useState<OrderDeskOrder | null>(null);
  const [selectedOrderID, setSelectedOrderID] = useState<number | null>(null);
  // Active / History. History was only ever reachable inside the character
  // modal; the tab is the single order surface now, so it lives here.
  const [subTab, setSubTab] = useState<"active" | "history">("active");
  // Order-book depth for the drawer. The desk payload already carries
  // position, best price and the undercut delta; only the price ladder needs
  // the extra call, so it is fetched once, lazily, the first time a row is
  // inspected.
  const [bookLevels, setBookLevels] = useState<Record<number, BookLevel[]> | null>(null);
  const [bookLoading, setBookLoading] = useState(false);

  const lastLoadedAtRef = useRef(0);
  const startedRef = useRef(false);
  const feesRequestedRef = useRef(false);

  const { importFees, loading: importingFees } = useEsiFeeImport();

  const refreshMs = prefs.refreshMinutes * 60_000;

  const updatePrefs = useCallback((patch: Partial<OrdersPrefs>) => {
    setPrefs((prev) => {
      const next = { ...prev, ...patch };
      saveOrdersPrefs(next);
      return next;
    });
  }, []);

  useEffect(() => {
    if (!isLoggedIn) return;
    void getAuthStatus()
      .then((s) => setAuthCharacters(s.characters ?? []))
      .catch(() => setAuthCharacters([]));
  }, [isLoggedIn]);

  useEffect(() => {
    if (!inspected || bookLevels || bookLoading) return;
    setBookLoading(true);
    void getUndercuts("all")
      .then((rows) => {
        const map: Record<number, BookLevel[]> = {};
        for (const u of rows) map[u.order_id] = u.book_levels ?? [];
        setBookLevels(map);
      })
      // A failed depth call just means no ladder — the rest of the drawer,
      // which is fed by the desk payload, is unaffected.
      .catch(() => {})
      .finally(() => setBookLoading(false));
  }, [inspected, bookLevels, bookLoading]);

  const applyFees = useCallback((fees: CharacterMarketFees) => {
    setSalesTax(Number(fees.suggested_sales_tax_percent.toFixed(2)));
    setBrokerFee(Number(fees.suggested_broker_fee_percent.toFixed(2)));
    setFeeSource(fees.character_name || null);
  }, []);

  // Fees come from the active character's Accounting and Broker Relations
  // levels rather than a guess, since they decide every suggested price and
  // the unprofitable-relist warning. The order desk waits for them: it is the
  // most expensive call in the app and running it twice — once on placeholder
  // numbers, once on the real ones — is exactly what this tab was fixed to
  // stop doing. No success toast on arrival; opening a tab is not an event.
  useEffect(() => {
    if (!isLoggedIn || feesRequestedRef.current) return;
    feesRequestedRef.current = true;
    void importFees(applyFees, { successToast: false }).finally(() =>
      setFeesReady(true),
    );
  }, [isLoggedIn, importFees, applyFees]);

  const resyncFees = useCallback(() => {
    void importFees(applyFees);
  }, [importFees, applyFees]);

  const load = useCallback(
    async (force = false) => {
      if (!isLoggedIn) {
        setData(null);
        return;
      }
      setLoading(true);
      setError(null);
      try {
        const resp = await getOrderDesk({
          salesTax,
          brokerFee,
          targetEtaDays: prefs.targetEtaDays,
          minMarginPct: prefs.minMarginPct,
          characterId: "all",
          force,
        });
        setData(resp);
        const at = Date.now();
        lastLoadedAtRef.current = at;
        setLastLoadedAt(at);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      } finally {
        setLoading(false);
      }
    },
    [isLoggedIn, salesTax, brokerFee, prefs.targetEtaDays, prefs.minMarginPct],
  );

  // First load is immediate; later reruns are only ever caused by a fee or
  // target edit, so they are debounced. `load` changes identity when one of
  // those changes, which is what re-arms the timer on each keystroke.
  useEffect(() => {
    if (!isLoggedIn) {
      setData(null);
      startedRef.current = false;
      feesRequestedRef.current = false;
      setFeesReady(false);
      return;
    }
    if (!feesReady) return;
    if (!startedRef.current) {
      startedRef.current = true;
      void load();
      return;
    }
    const id = setTimeout(() => void load(true), FEE_DEBOUNCE_MS);
    return () => clearTimeout(id);
  }, [load, isLoggedIn, feesReady]);

  // Scheduled refresh. Off is a real choice — during a long repricing pass
  // the last thing wanted is the table changing underfoot.
  useEffect(() => {
    if (!isLoggedIn || refreshMs === 0) return;
    const id = setInterval(() => void load(), refreshMs);
    return () => clearInterval(id);
  }, [isLoggedIn, refreshMs, load]);

  // Refetch on focus, but only once the interval has genuinely elapsed.
  // Repricing means returning to this window every few seconds; without the
  // gate every one of those cost a full reload.
  useEffect(() => {
    if (!isLoggedIn || refreshMs === 0) return;
    const onFocus = () => {
      if (Date.now() - lastLoadedAtRef.current < refreshMs) return;
      void load();
    };
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, [isLoggedIn, refreshMs, load]);

  // Keeps the "updated N minutes ago" stamp honest between loads.
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(id);
  }, []);

  const toggleCharacter = (id: number) => {
    setCharacterFilter((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  // Every number in a panel is quoted against the fees, the target ETA and
  // the min margin in force when it was fetched — the hurdle rate is derived
  // from the last two. Change any of them and the cached answers are not
  // stale-but-close, they are answers to a different question.
  useEffect(() => {
    setDispositions({});
    setExpandedOrders(new Set());
  }, [salesTax, brokerFee, prefs.targetEtaDays, prefs.minMarginPct]);

  const toggleDisposition = useCallback(
    (orderId: number) => {
      const open = expandedOrders.has(orderId);
      setExpandedOrders((prev) => {
        const next = new Set(prev);
        if (open) next.delete(orderId);
        else next.add(orderId);
        return next;
      });
      // Only the first open pays. A failed one is retried on the next open,
      // since the usual cause is a hub fetch timing out.
      if (open || dispositions[orderId]?.data) return;
      setDispositions((prev) => ({ ...prev, [orderId]: { loading: true } }));
      void getOrderDisposition(orderId, {
        salesTax,
        brokerFee,
        targetEtaDays: prefs.targetEtaDays,
        minMarginPct: prefs.minMarginPct,
        characterId: "all",
      })
        .then((resp) =>
          setDispositions((prev) => ({ ...prev, [orderId]: { loading: false, data: resp } })),
        )
        .catch((e) =>
          setDispositions((prev) => ({
            ...prev,
            [orderId]: { loading: false, error: e instanceof Error ? e.message : String(e) },
          })),
        );
    },
    [
      expandedOrders,
      dispositions,
      salesTax,
      brokerFee,
      prefs.targetEtaDays,
      prefs.minMarginPct,
    ],
  );

  // Open the row Today sent us to. Waits on `data` because the row has to
  // exist in the DOM before it can be expanded or scrolled to, and the desk is
  // several seconds of ESI away on a cold load.
  useEffect(() => {
    if (!focus?.order_id || !data) return;
    const orderId = focus.order_id;
    if (!data.orders.some((o) => o.order_id === orderId)) {
      // The order is gone — filled or cancelled since the plan was built.
      // Consume the focus anyway so it does not sit around waiting for a row
      // that is never coming back.
      onFocusConsumed?.();
      return;
    }
    setExpandedOrders((prev) => new Set(prev).add(orderId));
    const node = document.querySelector(`[data-order-id="${orderId}"]`);
    node?.scrollIntoView({ block: "center", behavior: "smooth" });
    onFocusConsumed?.();
  }, [focus, data, onFocusConsumed]);

  const filteredRows = useMemo(() => {
    if (!data) return [] as OrderDeskOrder[];
    let rows = data.orders.slice();
    if (characterFilter.size > 0) {
      rows = rows.filter(
        (r) => r.character_id != null && characterFilter.has(r.character_id),
      );
    }
    if (prefs.actionFilter === "needs_action") {
      rows = rows.filter(needsAttention);
    } else if (prefs.actionFilter === "hold") {
      rows = rows.filter((r) => r.recommendation === "hold");
    }
    return rows;
  }, [data, characterFilter, prefs.actionFilter]);

  // Sorted per side rather than globally, so an A→Z sort really does line up
  // with the in-game list one section at a time.
  const sellRows = useMemo(
    () => sortOrders(filteredRows.filter((r) => !r.is_buy_order), prefs.sort),
    [filteredRows, prefs.sort],
  );
  const buyRows = useMemo(
    () => sortOrders(filteredRows.filter((r) => r.is_buy_order), prefs.sort),
    [filteredRows, prefs.sort],
  );

  /**
   * Repricing loop: Shift+C opens the selected order's market window with its
   * new price already on the clipboard, then moves to the next row. The hands
   * never leave the pattern Shift+C, Ctrl+V, Shift+C, Ctrl+V.
   *
   * The walk order is the on-screen order -- sells, then buys -- so the
   * selection never jumps somewhere the eye is not. Which rows are in the walk
   * is the existing action filter's job rather than a second hidden rule: set
   * it to "needs action" and this steps the reprice queue exactly.
   */
  const walkRows = useMemo(() => [...sellRows, ...buyRows], [sellRows, buyRows]);

  const { openMarket } = useEveUiActions();
  const { addToast } = useOptionalToast();

  // A ref so the handler can read the current row without being rebuilt on
  // every selection change, which would tear down and re-add the key listener.
  const walkRef = useRef<{ rows: OrderDeskOrder[]; selected: number | null }>({
    rows: [],
    selected: null,
  });
  walkRef.current = { rows: walkRows, selected: selectedOrderID };

  const selectRow = useCallback((orderID: number | null) => {
    setSelectedOrderID(orderID);
    if (orderID == null) return;
    // Keep the selection visible; the list is long and the loop is blind
    // otherwise.
    requestAnimationFrame(() => {
      document
        .querySelector(`[data-order-id="${orderID}"]`)
        ?.scrollIntoView({ block: "nearest", behavior: "smooth" });
    });
  }, []);

  const stepReprice = useCallback(() => {
    const { rows, selected } = walkRef.current;
    if (rows.length === 0) return;

    const at = selected == null ? -1 : rows.findIndex((r) => r.order_id === selected);
    // Nothing selected yet: the first press selects rather than acting, so a
    // stray keypress cannot open a market window for an order you never chose.
    if (at < 0) {
      selectRow(rows[0].order_id);
      return;
    }

    const row = rows[at];
    const price =
      row.book_available && row.suggested_price > 0 && row.position !== 1
        ? formatGridPrice(row.suggested_price, priceStep(row.suggested_price))
        : null;

    void (async () => {
      // useEveUiActions owns the not-logged-in and failure toasts.
      const ok = await openMarket(row.type_id);
      if (!ok) return;
      if (price) {
        try {
          await navigator.clipboard.writeText(price);
        } catch {
          // Insecure context or a refused clipboard. The market window still
          // opened, so this is not a failure worth interrupting the loop for.
        }
      }
      // Advance only once the window actually opened, so a failed call leaves
      // the selection where it was and the next press retries the same row.
      if (at + 1 < rows.length) {
        selectRow(rows[at + 1].order_id);
      } else {
        // Deliberately does not wrap: silently restarting is how you reprice
        // the same order twice without noticing.
        addToast(t("ordersRepriceWalkEnd"), "info", 2200);
      }
    })();
  }, [addToast, openMarket, selectRow, t]);

  // Selection must not survive the row disappearing under it -- a filter
  // change or a refresh that drops the order would otherwise leave the walk
  // pointing at nothing and silently restart from the top.
  useEffect(() => {
    if (selectedOrderID == null) return;
    if (!walkRows.some((r) => r.order_id === selectedOrderID)) setSelectedOrderID(null);
  }, [walkRows, selectedOrderID]);

  useKeyboardShortcuts([
    {
      key: "c",
      modifiers: ["shift"],
      handler: stepReprice,
      description: "Open market for the selected order and advance",
    },
  ]);

  const toggleSort = useCallback(
    (k: OrdersSortKey, additive: boolean) => {
      updatePrefs({ sort: applySortClick(prefs.sort, k, additive) });
    },
    [prefs.sort, updatePrefs],
  );

  const removeSortLayer = useCallback(
    (k: OrdersSortKey) => {
      const next = prefs.sort.filter((layer) => layer.key !== k);
      if (next.length > 0) updatePrefs({ sort: next });
    },
    [prefs.sort, updatePrefs],
  );

  if (!isLoggedIn) {
    return (
      <div className="flex flex-col items-center justify-center h-full text-eve-dim text-sm space-y-2">
        <div>{t("ordersNoAuth")}</div>
      </div>
    );
  }

  // Only the very first load is allowed to replace the table. Every later
  // one leaves the rows in place: waiting three seconds between repricing
  // two orders was the single worst thing about this tab.
  const firstLoad = (loading || !feesReady) && !data;
  const feeTitle = feeSource
    ? t("ordersFeesFromEsi", { char: feeSource })
    : t("ordersFeesNoEsi");
  const ageMs = lastLoadedAt > 0 ? Math.max(0, now - lastLoadedAt) : 0;
  const updatedLabel =
    lastLoadedAt === 0
      ? null
      : ageMs < 60_000
        ? t("ordersUpdatedJustNow")
        : t("ordersUpdatedAgo", { ago: `${Math.floor(ageMs / 60_000)}m` });

  if (subTab === "history") {
    return (
      <div className="flex flex-col h-full space-y-3 p-3">
        <SubTabs
          subTab={subTab}
          setSubTab={setSubTab}
          activeCount={data?.orders.length ?? 0}
          t={t}
        />
        <div className="min-h-0 flex-1">
          <OrderHistoryPanel formatIsk={formatIsk} t={t} locale={locale} />
        </div>
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full space-y-3 p-3">
      <SubTabs
        subTab={subTab}
        setSubTab={setSubTab}
        activeCount={data?.orders.length ?? 0}
        t={t}
      />

      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-xs text-eve-dim uppercase tracking-wider">
            {t("ordersActionFilterLabel")}
          </span>
          {(["all", "needs_action", "hold"] as OrdersActionFilter[]).map((a) => (
            <button
              key={a}
              onClick={() => updatePrefs({ actionFilter: a })}
              className={`px-2.5 py-1 text-[11px] rounded-sm border transition-colors ${
                prefs.actionFilter === a
                  ? "bg-eve-accent/20 border-eve-accent text-eve-accent"
                  : "bg-eve-panel border-eve-border text-eve-dim hover:text-eve-text hover:border-eve-accent/50"
              }`}
            >
              {t(`ordersActionFilter_${a}` as TranslationKey)}
            </button>
          ))}
        </div>
        <div className="flex items-center gap-2 flex-wrap">
          <label className="text-[11px] text-eve-dim" title={feeTitle}>
            {t("ordersSalesTax")}
            <input
              type="number"
              min={0}
              max={100}
              step={0.1}
              value={salesTax}
              onChange={(e) => setSalesTax(parseFloat(e.target.value) || 0)}
              className="ml-1 w-16 bg-eve-dark border border-eve-border rounded-sm px-1 py-0.5 text-eve-text"
            />
          </label>
          <label className="text-[11px] text-eve-dim" title={feeTitle}>
            {t("ordersBrokerFee")}
            <input
              type="number"
              min={0}
              max={100}
              step={0.1}
              value={brokerFee}
              onChange={(e) => setBrokerFee(parseFloat(e.target.value) || 0)}
              className="ml-1 w-16 bg-eve-dark border border-eve-border rounded-sm px-1 py-0.5 text-eve-text"
            />
          </label>
          <label className="text-[11px] text-eve-dim" title={t("ordersTargetEtaHint")}>
            {t("ordersTargetEta")}
            <input
              type="number"
              min={ORDERS_TARGET_ETA_MIN_DAYS}
              max={ORDERS_TARGET_ETA_MAX_DAYS}
              step={0.5}
              value={prefs.targetEtaDays}
              onChange={(e) => {
                const v = parseFloat(e.target.value);
                if (!Number.isFinite(v)) return;
                updatePrefs({
                  targetEtaDays: Math.min(
                    ORDERS_TARGET_ETA_MAX_DAYS,
                    Math.max(ORDERS_TARGET_ETA_MIN_DAYS, v),
                  ),
                });
              }}
              className="ml-1 w-16 bg-eve-dark border border-eve-border rounded-sm px-1 py-0.5 text-eve-text"
            />
          </label>
          <label className="text-[11px] text-eve-dim" title={t("ordersMinMarginHint")}>
            {t("ordersMinMargin")}
            <input
              type="number"
              min={ORDERS_MIN_MARGIN_PCT_MIN}
              max={ORDERS_MIN_MARGIN_PCT_MAX}
              step={0.5}
              value={prefs.minMarginPct}
              onChange={(e) => {
                const v = parseFloat(e.target.value);
                if (!Number.isFinite(v)) return;
                updatePrefs({
                  minMarginPct: Math.min(
                    ORDERS_MIN_MARGIN_PCT_MAX,
                    Math.max(ORDERS_MIN_MARGIN_PCT_MIN, v),
                  ),
                });
              }}
              className="ml-1 w-16 bg-eve-dark border border-eve-border rounded-sm px-1 py-0.5 text-eve-text"
            />
          </label>
          <button
            type="button"
            onClick={resyncFees}
            disabled={importingFees}
            title={t("ordersFeesResyncHint")}
            aria-label={t("ordersFeesResyncHint")}
            className="px-1.5 py-0.5 text-[11px] rounded-sm border border-eve-border text-eve-dim hover:text-eve-accent hover:border-eve-accent disabled:opacity-50 transition-colors"
          >
            {importingFees ? "…" : "↻"}
          </button>
          <label className="text-[11px] text-eve-dim" title={t("ordersAutoRefreshHint")}>
            {t("ordersAutoRefresh")}
            <select
              value={prefs.refreshMinutes}
              onChange={(e) => updatePrefs({ refreshMinutes: Number(e.target.value) })}
              className="ml-1 bg-eve-dark border border-eve-border rounded-sm px-1 py-0.5 text-eve-text"
            >
              {ORDERS_REFRESH_CHOICES.map((m) => (
                <option key={m} value={m}>
                  {m === 0 ? t("ordersAutoRefreshOff") : t("ordersAutoRefreshMinutes", { n: m })}
                </option>
              ))}
            </select>
          </label>
          {updatedLabel && (
            <span className="text-[10px] text-eve-dim tabular-nums">{updatedLabel}</span>
          )}
          <button
            onClick={() => void load(true)}
            disabled={loading}
            className="px-3 py-1 text-xs rounded-sm border border-eve-accent/60 bg-eve-accent/10 text-eve-accent hover:bg-eve-accent/20 disabled:opacity-50"
          >
            {loading ? t("ordersRefreshing") : t("ordersRefresh")}
          </button>
        </div>
      </div>

      {/* Character filter chips */}
      {authCharacters.length > 1 && (
        <div className="flex items-center gap-1.5 flex-wrap">
          <span className="text-[10px] text-eve-dim uppercase tracking-wider">
            {t("ordersCharacterFilter")}
          </span>
          {authCharacters.map((c) => {
            const active =
              characterFilter.size === 0 || characterFilter.has(c.character_id);
            return (
              <button
                key={c.character_id}
                type="button"
                onClick={() => toggleCharacter(c.character_id)}
                className={`inline-flex items-center gap-1 px-2 py-0.5 text-[11px] rounded-sm border transition-colors ${
                  active
                    ? "border-eve-accent/50 bg-eve-accent/10 text-eve-accent"
                    : "border-eve-border/40 bg-eve-dark text-eve-dim opacity-60"
                }`}
              >
                <img
                  src={`https://images.evetech.net/characters/${c.character_id}/portrait?size=32`}
                  alt=""
                  className="w-4 h-4 rounded-full"
                />
                <span>{c.character_name}</span>
              </button>
            );
          })}
          {characterFilter.size > 0 && (
            <button
              onClick={() => setCharacterFilter(new Set())}
              className="text-[10px] text-eve-accent hover:underline"
            >
              {t("ordersCharacterFilterClear")}
            </button>
          )}
        </div>
      )}

      {error && (
        <div className="rounded-sm border border-red-500/50 bg-red-500/10 px-3 py-2 text-xs text-red-300">
          {error}
        </div>
      )}

      {/* KPI strip */}
      {data && (
        <div className="grid grid-cols-2 sm:grid-cols-6 gap-3">
          <KPITile
            label={t("ordersKpiTotal")}
            value={String(data.summary.total_orders)}
          />
          <KPITile
            label={t("ordersKpiReprice")}
            value={String(data.summary.needs_reprice)}
            emphasis={data.summary.needs_reprice > 0}
          />
          <KPITile
            label={t("ordersKpiCancel")}
            value={String(data.summary.needs_cancel)}
            emphasis={data.summary.needs_cancel > 0}
          />
          <KPITile
            label={t("ordersKpiReview")}
            value={String(data.summary.needs_review)}
            emphasis={data.summary.needs_review > 0}
          />
          <KPITile
            label={t("ordersKpiNotional")}
            value={`${formatIsk(data.summary.total_notional)} ISK`}
          />
          <KPITile
            label={t("ordersKpiCharacters")}
            value={String(
              new Set(
                data.orders.map((o) => o.character_id).filter((v) => v != null),
              ).size || authCharacters.length,
            )}
          />
        </div>
      )}

      {/* Sort stack. Shift-clicking headers is not discoverable on its own,
          so the layers are shown and removable here. Only worth showing
          alongside the headers it describes. */}
      {!firstLoad && data && data.orders.length > 0 && (
        <div className="flex items-center gap-1.5 flex-wrap text-[11px]">
          <span className="text-eve-dim uppercase tracking-wider">
            {t("ordersSortStackLabel")}
          </span>
          {prefs.sort.map((layer, i) => (
            <span
              key={layer.key}
              className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded-sm border border-eve-border bg-eve-panel text-eve-text"
            >
              {/* The ordinal is redundant on a single-layer sort, but the
                  chip is also the thing that teaches the feature exists. */}
              <span className="text-eve-dim tabular-nums">{i + 1}</span>
              {t(SORT_LABEL_KEY[layer.key])}
              <span className="text-eve-accent">{layer.dir === "asc" ? "▲" : "▼"}</span>
              {prefs.sort.length > 1 && (
                <button
                  type="button"
                  onClick={() => removeSortLayer(layer.key)}
                  title={t("ordersSortRemoveLayer")}
                  aria-label={t("ordersSortRemoveLayer")}
                  className="text-eve-dim hover:text-eve-error"
                >
                  ×
                </button>
              )}
            </span>
          ))}
          <span className="text-eve-dim/60">{t("ordersSortStackHint")}</span>
        </div>
      )}

      {/* Table */}
      <div className="flex-1 min-h-0 overflow-auto border border-eve-border rounded-sm bg-eve-panel">
        {firstLoad && (
          <div className="p-4 text-center text-eve-dim text-xs">
            {t("ordersLoading")}
          </div>
        )}
        {!firstLoad && data && data.orders.length === 0 && (
          <div className="p-4 text-center text-eve-dim text-xs">
            {t("ordersEmpty")}
          </div>
        )}
        {!firstLoad && data && data.orders.length > 0 && (
          <table
            className={`w-full text-xs transition-opacity ${loading ? "opacity-60" : ""}`}
            aria-busy={loading}
          >
            <thead className="bg-eve-dark sticky top-0 z-10">
              <tr className="text-eve-dim">
                <SortableTH
                  label={t("ordersColOwner")}
                  k="owner"
                  sort={prefs.sort}
                  onClick={toggleSort}
                  hint={t("ordersSortHint")}
                  align="left"
                />
                <SortableTH
                  label={t("colItem")}
                  k="type"
                  sort={prefs.sort}
                  onClick={toggleSort}
                  hint={t("ordersSortHint")}
                  align="left"
                />
                <SortableTH
                  label={t("ordersColStation")}
                  k="station"
                  sort={prefs.sort}
                  onClick={toggleSort}
                  hint={t("ordersSortHint")}
                  align="left"
                />
                <SortableTH
                  label={t("ordersColAction")}
                  k="priority"
                  sort={prefs.sort}
                  onClick={toggleSort}
                  hint={t("ordersSortHint")}
                  align="left"
                />
                <SortableTH
                  label={t("ordersColCurrent")}
                  k="current"
                  sort={prefs.sort}
                  onClick={toggleSort}
                  hint={t("ordersSortHint")}
                  align="right"
                />
                <th className="px-2 py-1.5 text-right">{t("ordersColBest")}</th>
                <th className="px-2 py-1.5 text-right">
                  {t("operatorSuggestedPriceCol")}
                </th>
                <SortableTH
                  label={t("ordersColValue")}
                  k="notional"
                  sort={prefs.sort}
                  onClick={toggleSort}
                  hint={t("ordersColValueHint")}
                  align="right"
                />
                <SortableTH
                  label={t("ordersColMargin")}
                  k="margin"
                  sort={prefs.sort}
                  onClick={toggleSort}
                  hint={t("ordersColMarginHint")}
                  align="right"
                />
                <th className="px-2 py-1.5 text-right">{t("ordersColPosition")}</th>
                <SortableTH
                  label={t("ordersColEta")}
                  k="eta"
                  sort={prefs.sort}
                  onClick={toggleSort}
                  hint={t("ordersSortHint")}
                  align="right"
                />
                <SortableTH
                  label={t("ordersColExpiry")}
                  k="expiry"
                  sort={prefs.sort}
                  onClick={toggleSort}
                  hint={t("ordersSortHint")}
                  align="right"
                />
              </tr>
            </thead>
            <OrderSection
              title={t("ordersSectionSell")}
              rows={sellRows}
              collapsed={prefs.collapsedSell}
              onToggle={() => updatePrefs({ collapsedSell: !prefs.collapsedSell })}
              settings={data.settings}
              t={t}
              expandedOrders={expandedOrders}
              dispositions={dispositions}
              onToggleDisposition={toggleDisposition}
              onInspect={setInspected}
              selectedOrderID={selectedOrderID}
              onSelect={selectRow}
            />
            <OrderSection
              title={t("ordersSectionBuy")}
              rows={buyRows}
              collapsed={prefs.collapsedBuy}
              onToggle={() => updatePrefs({ collapsedBuy: !prefs.collapsedBuy })}
              settings={data.settings}
              t={t}
              expandedOrders={expandedOrders}
              dispositions={dispositions}
              onToggleDisposition={toggleDisposition}
              onInspect={setInspected}
              selectedOrderID={selectedOrderID}
              onSelect={selectRow}
            />
          </table>
        )}
      </div>

      <OrderRowDrawer
        row={inspected}
        onClose={() => setInspected(null)}
        t={t}
        locale={locale}
        bookLevels={inspected ? bookLevels?.[inspected.order_id] : undefined}
        bookLevelsLoading={bookLoading}
      />
    </div>
  );
}

/** Active / History. Order history was only ever reachable inside the
 *  character modal; this tab is the single order surface now. */
function SubTabs({
  subTab,
  setSubTab,
  activeCount,
  t,
}: {
  subTab: "active" | "history";
  setSubTab: (v: "active" | "history") => void;
  activeCount: number;
  t: Translate;
}) {
  return (
    <div className="flex gap-1 border-b border-eve-border">
      {(["active", "history"] as const).map((v) => (
        <button
          key={v}
          type="button"
          onClick={() => setSubTab(v)}
          className={cn(
            "px-3 py-1.5 text-xs transition-colors",
            subTab === v
              ? "border-b-2 border-eve-accent text-eve-accent"
              : "text-eve-dim hover:text-eve-text",
          )}
        >
          {v === "active" ? `${t("charActiveOrders")} (${activeCount})` : t("charOrderHistory")}
        </button>
      ))}
    </div>
  );
}

type Translate = (key: TranslationKey, params?: Record<string, string | number>) => string;
/** One side of the book: a header row carrying the side's own totals, then
 *  its rows. Rendered as a tbody inside the shared table so both sections
 *  keep identical column widths — two separate tables would drift apart. */
function OrderSection({
  title,
  rows,
  collapsed,
  onToggle,
  settings,
  t,
  expandedOrders,
  dispositions,
  onToggleDisposition,
  onInspect,
  selectedOrderID,
  onSelect,
}: {
  title: string;
  rows: OrderDeskOrder[];
  collapsed: boolean;
  onToggle: () => void;
  settings: OrderDeskSettings;
  t: Translate;
  expandedOrders: Set<number>;
  dispositions: Record<number, DispositionState>;
  onToggleDisposition: (orderId: number) => void;
  onInspect: (row: OrderDeskOrder) => void;
  selectedOrderID: number | null;
  onSelect: (orderId: number) => void;
}) {
  const notional = rows.reduce((sum, r) => sum + r.notional, 0);
  const needsAction = rows.filter(needsAttention).length;

  return (
    <tbody>
      <tr className="border-t border-eve-border bg-eve-dark/60">
        <td colSpan={COLUMN_COUNT} className="px-0 py-0">
          <button
            type="button"
            onClick={onToggle}
            aria-expanded={!collapsed}
            className="w-full flex items-center gap-2 px-2 py-1.5 text-left hover:bg-eve-accent/5 transition-colors"
          >
            <span className="w-3 text-eve-dim text-[10px]">{collapsed ? "▶" : "▼"}</span>
            <span className="text-[11px] font-semibold uppercase tracking-wider text-eve-text">
              {title}
            </span>
            <span className="text-[11px] text-eve-dim">
              {t("ordersSectionSummary", {
                count: rows.length,
                isk: formatIsk(notional),
              })}
            </span>
            {needsAction > 0 && (
              <span className="ml-auto inline-flex px-1.5 py-0.5 rounded-sm text-[10px] font-medium bg-amber-500/20 text-amber-400">
                {t("ordersSectionNeedsAction", { count: needsAction })}
              </span>
            )}
          </button>
        </td>
      </tr>
      {!collapsed &&
        rows.map((r) => (
          <OrderRow
            key={r.order_id}
            row={r}
            settings={settings}
            formatIsk={formatIsk}
            t={t}
            expanded={expandedOrders.has(r.order_id)}
            disposition={dispositions[r.order_id]}
            onToggleDisposition={onToggleDisposition}
            onInspect={onInspect}
            selected={r.order_id === selectedOrderID}
            onSelect={onSelect}
          />
        ))}
    </tbody>
  );
}

function KPITile({
  label,
  value,
  emphasis,
}: {
  label: string;
  value: string;
  emphasis?: boolean;
}) {
  return (
    <div
      className={`rounded-sm border ${emphasis ? "border-amber-500/50 bg-amber-500/5" : "border-eve-border bg-eve-panel"} p-2`}
    >
      <div className="text-[10px] text-eve-dim uppercase tracking-wider">{label}</div>
      <div
        className={`font-mono ${emphasis ? "text-amber-400" : "text-eve-text"} text-lg font-semibold`}
      >
        {value}
      </div>
    </div>
  );
}

/** A sortable header always shows an affordance: ⇅ when idle, ▲/▼ when it is
 *  the active sort. Without the idle marker the columns look fixed, which is
 *  exactly how this table used to read. */
function SortableTH({
  label,
  k,
  sort,
  onClick,
  hint,
  align,
}: {
  label: string;
  k: OrdersSortKey;
  sort: readonly OrdersSortLayer[];
  onClick: (k: OrdersSortKey, additive: boolean) => void;
  hint: string;
  align: "left" | "right";
}) {
  const index = sort.findIndex((layer) => layer.key === k);
  const layer = index >= 0 ? sort[index] : null;
  return (
    <th
      className={`px-2 py-1.5 ${align === "right" ? "text-right" : "text-left"} cursor-pointer hover:text-eve-text select-none`}
      onClick={(e) => onClick(k, e.shiftKey)}
      title={hint}
      aria-sort={layer ? (layer.dir === "asc" ? "ascending" : "descending") : "none"}
    >
      {label}
      <span className={`ml-1 ${layer ? "text-eve-accent" : "text-eve-dim/40"}`}>
        {layer ? (layer.dir === "asc" ? "▲" : "▼") : "⇅"}
      </span>
      {/* Which layer this is only means anything when there is more than
          one; a lone "1" on every table would just be noise. */}
      {layer && sort.length > 1 && (
        <sup className="ml-0.5 text-eve-accent tabular-nums">{index + 1}</sup>
      )}
    </th>
  );
}

/** Matches orderDeskETACapDays in internal/engine/order_desk.go. Past this
 *  the walk stops rather than quoting a number nobody would act on. */
const ORDERS_ETA_CAP_DAYS = 90;

/** The ETA is four corrections deep by the time it reaches the table, and
 *  the whole point of the rework was that the old single number could not be
 *  argued with. Hovering shows the derivation so it can be checked against
 *  the in-game book mid-reprice. */
function etaBreakdown(row: OrderDeskOrder, t: Translate): string {
  if (row.eta_days < 0) return t("ordersEtaUnknownHint");
  // The history volume is blended across both sides; a buy order is filled
  // by the half the sell share does not describe.
  const sideShare = row.is_buy_order ? 1 - row.sell_side_share : row.sell_side_share;
  const basis =
    row.flow_basis === "weekday" ? t("ordersFlowBasisWeekday") : t("ordersFlowBasisFlat");
  return t("ordersEtaBreakdown", {
    regional: row.avg_daily_volume.toFixed(1),
    side: Math.round(sideShare * 100),
    station: Math.round(row.station_flow_share * 100),
    perDay: row.estimated_fill_per_day.toFixed(1),
    basis,
    queue: row.days_to_clear_queue.toFixed(1),
    total: row.eta_capped
      ? `${ORDERS_ETA_CAP_DAYS}+`
      : row.eta_days.toFixed(1),
  });
}

/** The margin is two fees and an assumed exit price deep by the time it
 *  reaches the table, and it is now the thing that turns an order red. The
 *  derivation has to be checkable against the in-game book from the row. */
function marginBreakdown(row: OrderDeskOrder, settings: OrderDeskSettings, t: Translate): string {
  const fees = settings.sales_tax_percent + settings.broker_fee_percent;
  const feeLabel = fees.toFixed(2);
  const pct = row.margin_percent.toFixed(1);

  if (row.margin_basis === "book") {
    const exit = row.exit_price ?? 0;
    return t("ordersMarginBuyBreakdown", {
      exit: formatIsk(exit),
      fees: feeLabel,
      net: formatIsk(exit * (1 - fees / 100)),
      bid: formatIsk(row.price),
      margin: formatIsk(row.margin_unit_isk),
      pct,
    });
  }
  if (row.margin_basis === "cost_basis") {
    return t("ordersMarginSellBreakdown", {
      price: formatIsk(row.price),
      fees: feeLabel,
      net: formatIsk(row.price * (1 - fees / 100)),
      cost: formatIsk(row.cost_basis_isk ?? 0),
      margin: formatIsk(row.margin_unit_isk),
      pct,
    });
  }
  // Unmeasured. Which input was missing depends on the side, and the two
  // have different fixes, so the hint has to say which one applies.
  return row.is_buy_order ? t("ordersMarginNoneBuyHint") : t("ordersMarginNoneSellHint");
}

function OrderRow({
  row,
  settings,
  formatIsk,
  t,
  expanded,
  disposition,
  onToggleDisposition,
  onInspect,
  selected,
  onSelect,
}: {
  row: OrderDeskOrder;
  settings: OrderDeskSettings;
  formatIsk: (v: number) => string;
  t: Translate;
  expanded: boolean;
  disposition?: DispositionState;
  onToggleDisposition: (orderId: number) => void;
  onInspect: (row: OrderDeskOrder) => void;
  selected: boolean;
  onSelect: (orderId: number) => void;
}) {
  const atTop = row.position === 1;
  const priceCls = atTop ? "text-eve-dim font-mono" : "text-eve-accent font-mono";
  const hasCopyablePrice = row.book_available && row.suggested_price > 0 && !atTop;
  // Thin reads amber rather than green: it is still a profit, but it is the
  // one the floor was set to catch.
  const marginCls =
    row.margin_basis === "none"
      ? "text-eve-dim"
      : row.margin_unit_isk <= 0
        ? "text-red-400"
        : row.warn_thin_margin
          ? "text-amber-400"
          : "text-emerald-400";
  // `review` is deliberately not red. The ISK is already spent, so the row is
  // not "you are losing money by leaving this up" — it is "the desk cannot
  // call this one, come and look".
  const badgeClass =
    row.recommendation === "cancel"
      ? "bg-red-500/20 text-red-400"
      : row.recommendation === "review"
        ? "bg-sky-500/20 text-sky-400"
        : row.recommendation === "reprice"
          ? "bg-amber-500/20 text-amber-400"
          : row.book_available
            ? "bg-emerald-500/20 text-emerald-400"
            : "bg-eve-dim/20 text-eve-dim";
  const reviewable = row.recommendation === "review";
  return (
    <>
    <tr
      // Anchors a deep link from Today, which expands and scrolls to this row.
      data-order-id={row.order_id}
      aria-selected={selected}
      className={
        "cursor-pointer border-t border-eve-border/50 hover:bg-eve-accent/5" +
        (selected ? " bg-eve-accent/10 shadow-[inset_3px_0_0_0_rgb(var(--eve-accent))]" : "")
      }
      onClick={(e) => {
        // Don't hijack the copy / open-market / disposition-expander buttons.
        if ((e.target as HTMLElement).closest("button, a")) return;
        // Clicking moves the reprice cursor here too, so Shift+C continues
        // from the row you are looking at rather than the top of the list.
        onSelect(row.order_id);
        onInspect(row);
      }}
    >
      <td className="px-2 py-1 text-eve-text">
        {row.character_id ? (
          <div className="flex items-center gap-1.5">
            <img
              src={`https://images.evetech.net/characters/${row.character_id}/portrait?size=32`}
              alt=""
              className="w-4 h-4 rounded-full"
            />
            <span className="text-[11px]">{row.character_name || `#${row.character_id}`}</span>
          </div>
        ) : (
          <span className="text-eve-dim">—</span>
        )}
      </td>
      {/* The market button lives here rather than in the suggested-price cell,
          where it used to be skipped entirely for any order that had no
          suggested price. It still copies that price when there is one. */}
      <td className="px-2 py-1 text-eve-text max-w-[220px]">
        <ItemRef
          typeId={row.type_id}
          name={row.type_name}
          iconSize={16}
          market
          copyName
          copyOnOpen={
            hasCopyablePrice
              ? {
                  text: formatGridPrice(row.suggested_price, priceStep(row.suggested_price)),
                }
              : undefined
          }
          marketLabel={t("ordersOpenMarketHint")}
        />
      </td>
      <td className="px-2 py-1 text-eve-dim max-w-[200px] truncate" title={row.location_name}>
        {row.location_name || `#${row.location_id}`}
      </td>
      <td className="px-2 py-1">
        <div className="flex items-center gap-1">
          <span
            className={`inline-flex px-1.5 py-0.5 rounded-sm text-[10px] font-medium uppercase tracking-wide ${badgeClass} cursor-help`}
            title={row.reason}
          >
            {row.recommendation}
          </span>
          {reviewable && (
            <button
              type="button"
              onClick={() => onToggleDisposition(row.order_id)}
              aria-expanded={expanded}
              className="text-[10px] px-1 rounded-sm text-eve-dim hover:text-eve-accent transition-colors"
              title={t("ordersDispositionExpandHint")}
              aria-label={t("ordersDispositionExpandHint")}
            >
              {expanded ? "▾" : "▸"}
            </button>
          )}
        </div>
      </td>
      <td className="px-2 py-1 text-right font-mono text-eve-text">{formatIsk(row.price)}</td>
      <td className="px-2 py-1 text-right font-mono text-eve-dim">
        {row.book_available && row.best_price > 0 ? formatIsk(row.best_price) : "—"}
      </td>
      <td className="px-2 py-1 text-right">
        {row.book_available && row.suggested_price > 0 ? (
          <div className="inline-flex items-center gap-1.5 justify-end">
            {row.warn_unprofitable_relist && (
              <span
                title={t("operatorUnprofitableRelistHint", {
                  fee: formatIsk(row.relist_fee_isk ?? 0),
                })}
                className="cursor-help text-yellow-400"
              >
                ⚠
              </span>
            )}
            <span className={priceCls}>{formatIsk(row.suggested_price)}</span>
            {/* Already top of book — there is no price to move to. The step
                keeps a legal sub-10-ISK undercut from rounding back above the
                price it was undercutting. */}
            {!atTop && (
              <CopyPrice
                value={row.suggested_price}
                step={priceStep(row.suggested_price)}
                label={t("operatorSuggestedPriceCopyHint")}
              />
            )}
          </div>
        ) : (
          <span className="text-eve-dim">—</span>
        )}
      </td>
      <td
        className="px-2 py-1 text-right font-mono text-eve-text"
        title={t("ordersColValueNet", {
          isk: formatIsk(row.net_notional),
          qty: row.volume_remain.toLocaleString(),
        })}
      >
        {formatIsk(row.notional)}
      </td>
      <td className="px-2 py-1 text-right font-mono" title={marginBreakdown(row, settings, t)}>
        {row.margin_basis === "none" ? (
          <span className="text-eve-dim cursor-help">—</span>
        ) : (
          <span className={`inline-flex items-center gap-1 justify-end cursor-help ${marginCls}`}>
            {row.warn_thin_margin && (
              <span
                className="text-yellow-400"
                title={t("ordersThinMarginHint", { floor: settings.min_margin_percent })}
              >
                ⚠
              </span>
            )}
            {`${row.margin_percent >= 0 ? "+" : ""}${row.margin_percent.toFixed(1)}%`}
          </span>
        )}
      </td>
      <td className="px-2 py-1 text-right text-eve-dim font-mono">
        {row.book_available ? `${row.position}/${row.total_orders}` : "—"}
      </td>
      <td
        className="px-2 py-1 text-right text-eve-dim font-mono"
        title={etaBreakdown(row, t)}
      >
        {row.eta_days < 0
          ? "—"
          : row.eta_capped
            ? `${ORDERS_ETA_CAP_DAYS}d+`
            : `${row.eta_days.toFixed(1)}d`}
      </td>
      <td className="px-2 py-1 text-right text-eve-dim font-mono">
        {row.days_to_expire >= 0 ? `${row.days_to_expire}d` : "—"}
      </td>
    </tr>
    {reviewable && expanded && (
      <tr className="bg-eve-dark/40">
        <td colSpan={COLUMN_COUNT} className="px-3 py-2">
          <DispositionPanel state={disposition} formatIsk={formatIsk} t={t} />
        </td>
      </tr>
    )}
    </>
  );
}

const DISPOSITION_KIND_LABEL: Record<string, TranslationKey> = {
  cut: "ordersDispositionKindCut",
  hold: "ordersDispositionKindHold",
  move: "ordersDispositionKindMove",
};

const DISPOSITION_KIND_HINT: Record<string, TranslationKey> = {
  cut: "ordersDispositionKindCutHint",
  hold: "ordersDispositionKindHoldHint",
  move: "ordersDispositionKindMoveHint",
};

/** The three answers to "what is this ISK worth at a common future date",
 *  side by side with the arithmetic that produced them. It shows its working
 *  because the whole point is that you make the call, not the desk — and
 *  where a plan is missing it says why rather than quietly dropping it. */
function DispositionPanel({
  state,
  formatIsk,
  t,
}: {
  state?: DispositionState;
  formatIsk: (v: number) => string;
  t: Translate;
}) {
  if (!state || state.loading) {
    return <div className="text-[11px] text-eve-dim">{t("ordersDispositionLoading")}</div>;
  }
  if (state.error) {
    return (
      <div className="text-[11px] text-red-400">
        {t("ordersDispositionFailed", { error: state.error })}
      </div>
    );
  }
  const d = state.data;
  if (!d) return null;

  const holdOffered = d.plans.some((p) => p.kind === "hold");

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-eve-dim">
        <span>
          {t("ordersDispositionPosition", {
            qty: d.qty.toLocaleString(),
            cost: formatIsk(d.cost_basis_isk),
            total: formatIsk(d.position_isk),
          })}
        </span>
        {d.held_since && (
          <span>{t("ordersDispositionHeld", { date: d.held_since })}</span>
        )}
        <span className="cursor-help" title={t("ordersDispositionHorizonHint")}>
          {t("ordersDispositionHorizon", { days: d.horizon_days.toFixed(1) })}
        </span>
        <span className="cursor-help" title={t("ordersDispositionHurdleHint")}>
          {t("ordersDispositionHurdle", { pct: d.hurdle_pct_day.toFixed(2) })}
        </span>
        {d.venues_priced > 0 && (
          <span>
            {t("ordersDispositionVenues", {
              priced: d.venues_priced,
              skipped: d.venues_skipped,
            })}
          </span>
        )}
      </div>

      {d.plans.length === 0 ? (
        <div className="text-[11px] text-eve-dim">{d.reason || t("ordersDispositionNoPlans")}</div>
      ) : (
        <table className="text-[11px] w-full">
          <thead>
            <tr className="text-eve-dim uppercase tracking-wide text-[10px]">
              <th className="text-left font-medium px-1 py-0.5">{t("ordersDispositionColPlan")}</th>
              <th className="text-left font-medium px-1 py-0.5">{t("ordersDispositionColVenue")}</th>
              <th className="text-right font-medium px-1 py-0.5">{t("ordersDispositionColExit")}</th>
              <th className="text-right font-medium px-1 py-0.5">{t("ordersDispositionColNet")}</th>
              <th className="text-right font-medium px-1 py-0.5">{t("ordersDispositionColProfit")}</th>
              <th className="text-right font-medium px-1 py-0.5">{t("ordersDispositionColDays")}</th>
              <th className="text-right font-medium px-1 py-0.5">
                {t("ordersDispositionColTerminal")}
              </th>
            </tr>
          </thead>
          <tbody>
            {d.plans.map((p) => (
              <DispositionRow key={p.kind + (p.venue ?? "")} plan={p} formatIsk={formatIsk} t={t} />
            ))}
          </tbody>
        </table>
      )}

      {d.too_close && (
        <div className="text-[11px] text-amber-400">{t("ordersDispositionTooClose")}</div>
      )}
      {!holdOffered && d.recovery.basis === "none" && d.recovery.reason && (
        <div className="text-[11px] text-eve-dim">
          {t("ordersDispositionNoHold", { reason: d.recovery.reason })}
        </div>
      )}
      {holdOffered && d.recovery.basis === "history" && (
        <div className="text-[11px] text-eve-dim">
          {t("ordersDispositionRecovery", {
            episodes: d.recovery.episodes,
            window: d.recovery.window_days,
            days: d.recovery.median_days.toFixed(0),
            target: formatIsk(d.recovery.target_price),
          })}
        </div>
      )}
    </div>
  );
}

function DispositionRow({
  plan,
  formatIsk,
  t,
}: {
  plan: DispositionPlan;
  formatIsk: (v: number) => string;
  t: Translate;
}) {
  const kindKey = DISPOSITION_KIND_LABEL[plan.kind];
  const hintKey = DISPOSITION_KIND_HINT[plan.kind];
  const profitCls = plan.profit_isk >= 0 ? "text-emerald-400" : "text-red-400";
  return (
    <tr
      className={
        plan.recommended
          ? "bg-eve-accent/10 text-eve-text"
          : "text-eve-dim hover:bg-eve-accent/5"
      }
    >
      <td className="px-1 py-0.5">
        <span className="inline-flex items-center gap-1">
          <span
            className={`font-medium uppercase tracking-wide cursor-help ${plan.recommended ? "text-eve-accent" : ""}`}
            title={hintKey ? t(hintKey) : undefined}
          >
            {kindKey ? t(kindKey) : plan.kind}
          </span>
          {plan.recommended && (
            <span className="px-1 rounded-sm bg-eve-accent/20 text-eve-accent text-[9px] uppercase">
              {t("ordersDispositionBest")}
            </span>
          )}
        </span>
      </td>
      <td className="px-1 py-0.5 max-w-[260px]">
        <div className="truncate" title={plan.venue}>
          {plan.venue || "—"}
          {plan.jumps != null && plan.jumps > 0 && (
            <span className="ml-1 text-eve-dim">
              {t("ordersDispositionJumps", { jumps: plan.jumps })}
            </span>
          )}
        </div>
        {/* Server-side notes are how a plan explains a choice the numbers
            alone hide — "took the standing bid" versus "listed under the
            best ask" price very differently and read identically otherwise. */}
        {plan.notes && plan.notes.length > 0 && (
          <div className="truncate text-[10px] text-eve-dim" title={plan.notes.join(" · ")}>
            {plan.notes.join(" · ")}
          </div>
        )}
      </td>
      <td className="px-1 py-0.5 text-right font-mono">{formatIsk(plan.exit_price)}</td>
      <td
        className="px-1 py-0.5 text-right font-mono cursor-help"
        title={
          plan.haul_isk
            ? t("ordersDispositionNetHint", {
                gross: formatIsk(plan.gross_isk),
                haul: formatIsk(plan.haul_isk),
              })
            : t("ordersDispositionNetHintNoHaul", { gross: formatIsk(plan.gross_isk) })
        }
      >
        {formatIsk(plan.net_isk)}
      </td>
      <td className={`px-1 py-0.5 text-right font-mono ${profitCls}`}>
        {plan.profit_isk >= 0 ? "+" : "−"}
        {formatIsk(Math.abs(plan.profit_isk))}
      </td>
      <td className="px-1 py-0.5 text-right font-mono">{plan.days_to_realise.toFixed(1)}d</td>
      <td className="px-1 py-0.5 text-right font-mono">{formatIsk(plan.terminal_isk)}</td>
    </tr>
  );
}
