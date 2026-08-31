import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { getAuthStatus, getOrderDesk, openMarketInGame } from "../lib/api";
import type { CharacterMarketFees } from "../lib/api";
import type {
  AuthCharacter,
  OrderDeskOrder,
  OrderDeskResponse,
} from "../lib/types";
import { useEsiFeeImport } from "../lib/useEsiFeeImport";
import {
  defaultDirForSortKey,
  loadOrdersPrefs,
  saveOrdersPrefs,
  ORDERS_REFRESH_CHOICES,
  type OrdersActionFilter,
  type OrdersPrefs,
  type OrdersSortDir,
  type OrdersSortKey,
} from "../lib/ordersPrefs";
import { useI18n, type TranslationKey } from "../lib/i18n";
import { useGlobalToast } from "./Toast";
import { handleEveUIError } from "../lib/handleEveUIError";

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
}

const PRIORITY_BY_ACTION: Record<string, number> = {
  cancel: 3,
  reprice: 2,
  hold: 0,
};

/** Columns rendered per row. Group header rows span all of them. */
const COLUMN_COUNT = 11;

/** Editing a fee re-runs the whole server-side computation, so wait for the
 *  user to stop typing rather than firing one request per digit. */
const FEE_DEBOUNCE_MS = 600;

function formatIsk(v: number): string {
  const abs = Math.abs(v);
  if (abs >= 1e12) return `${(v / 1e12).toFixed(2)}T`;
  if (abs >= 1e9) return `${(v / 1e9).toFixed(2)}B`;
  if (abs >= 1e6) return `${(v / 1e6).toFixed(2)}M`;
  if (abs >= 1e3) return `${(v / 1e3).toFixed(1)}K`;
  return v.toFixed(0);
}

/** Value that sorts last regardless of direction is wrong for "unknown", so
 *  unknowns get +Infinity and land at the end of an ascending sort. */
function orInfinity(v: number): number {
  return v >= 0 ? v : Number.POSITIVE_INFINITY;
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
    case "expiry":
      return orInfinity(a.days_to_expire) - orInfinity(b.days_to_expire);
  }
}

function sortOrders(
  rows: OrderDeskOrder[],
  key: OrdersSortKey,
  dir: OrdersSortDir,
): OrderDeskOrder[] {
  return rows.slice().sort((a, b) => {
    const cmp = compareBy(a, b, key);
    if (cmp !== 0) return dir === "asc" ? cmp : -cmp;
    // Ties always break alphabetically, never by direction. "Value, highest
    // first" then reads down the page in a stable, findable order.
    return (a.type_name || "").localeCompare(b.type_name || "");
  });
}

export function Orders({ isLoggedIn }: Props) {
  const { t } = useI18n();
  const { addToast } = useGlobalToast();
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
    [isLoggedIn, salesTax, brokerFee],
  );

  // First load is immediate; later reruns are only ever caused by a fee edit,
  // so they are debounced. `load` changes identity when a fee changes, which
  // is what re-arms the timer on each keystroke.
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

  const filteredRows = useMemo(() => {
    if (!data) return [] as OrderDeskOrder[];
    let rows = data.orders.slice();
    if (characterFilter.size > 0) {
      rows = rows.filter(
        (r) => r.character_id != null && characterFilter.has(r.character_id),
      );
    }
    if (prefs.actionFilter === "needs_action") {
      rows = rows.filter(
        (r) => r.recommendation === "reprice" || r.recommendation === "cancel",
      );
    } else if (prefs.actionFilter === "hold") {
      rows = rows.filter((r) => r.recommendation === "hold");
    }
    return rows;
  }, [data, characterFilter, prefs.actionFilter]);

  // Sorted per side rather than globally, so an A→Z sort really does line up
  // with the in-game list one section at a time.
  const sellRows = useMemo(
    () => sortOrders(filteredRows.filter((r) => !r.is_buy_order), prefs.sortKey, prefs.sortDir),
    [filteredRows, prefs.sortKey, prefs.sortDir],
  );
  const buyRows = useMemo(
    () => sortOrders(filteredRows.filter((r) => r.is_buy_order), prefs.sortKey, prefs.sortDir),
    [filteredRows, prefs.sortKey, prefs.sortDir],
  );

  const toggleSort = useCallback(
    (k: OrdersSortKey) => {
      if (prefs.sortKey === k) {
        updatePrefs({ sortDir: prefs.sortDir === "asc" ? "desc" : "asc" });
      } else {
        updatePrefs({ sortKey: k, sortDir: defaultDirForSortKey(k) });
      }
    },
    [prefs.sortKey, prefs.sortDir, updatePrefs],
  );

  // openMarketForType mirrors the pattern used by CombinedOrdersTab /
  // StationTrading / ScanResultsTable — surface both success ("Opened in
  // game") and failure (401 re-login / generic error) as toasts so the
  // user gets real feedback. Previously this button silently swallowed
  // errors, which made ESI 401s / token issues invisible.
  const openMarketForType = useCallback(
    async (typeID: number) => {
      if (!typeID) return;
      try {
        await openMarketInGame(typeID);
        addToast(t("actionSuccess"), "success", 2000);
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : String(err);
        const { messageKey, duration } = handleEveUIError({ message });
        if (messageKey === "actionFailed") {
          addToast(t(messageKey, { error: message || "Unknown error" }), "error", duration);
        } else {
          addToast(t(messageKey), "error", duration);
        }
      }
    },
    [addToast, t],
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

  return (
    <div className="flex flex-col h-full space-y-3 p-3">
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
        <div className="grid grid-cols-2 sm:grid-cols-5 gap-3">
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
                  cur={prefs}
                  onClick={toggleSort}
                  hint={t("ordersSortHint")}
                  align="left"
                />
                <SortableTH
                  label={t("colItem")}
                  k="type"
                  cur={prefs}
                  onClick={toggleSort}
                  hint={t("ordersSortHint")}
                  align="left"
                />
                <SortableTH
                  label={t("ordersColStation")}
                  k="station"
                  cur={prefs}
                  onClick={toggleSort}
                  hint={t("ordersSortHint")}
                  align="left"
                />
                <SortableTH
                  label={t("ordersColAction")}
                  k="priority"
                  cur={prefs}
                  onClick={toggleSort}
                  hint={t("ordersSortHint")}
                  align="left"
                />
                <SortableTH
                  label={t("ordersColCurrent")}
                  k="current"
                  cur={prefs}
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
                  cur={prefs}
                  onClick={toggleSort}
                  hint={t("ordersColValueHint")}
                  align="right"
                />
                <th className="px-2 py-1.5 text-right">{t("ordersColPosition")}</th>
                <SortableTH
                  label={t("ordersColEta")}
                  k="eta"
                  cur={prefs}
                  onClick={toggleSort}
                  hint={t("ordersSortHint")}
                  align="right"
                />
                <SortableTH
                  label={t("ordersColExpiry")}
                  k="expiry"
                  cur={prefs}
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
              t={t}
              onOpenMarket={openMarketForType}
              addToast={addToast}
            />
            <OrderSection
              title={t("ordersSectionBuy")}
              rows={buyRows}
              collapsed={prefs.collapsedBuy}
              onToggle={() => updatePrefs({ collapsedBuy: !prefs.collapsedBuy })}
              t={t}
              onOpenMarket={openMarketForType}
              addToast={addToast}
            />
          </table>
        )}
      </div>
    </div>
  );
}

type Translate = (key: TranslationKey, params?: Record<string, string | number>) => string;
type AddToast = (
  text: string,
  type?: "success" | "error" | "info",
  duration?: number,
) => number;

/** One side of the book: a header row carrying the side's own totals, then
 *  its rows. Rendered as a tbody inside the shared table so both sections
 *  keep identical column widths — two separate tables would drift apart. */
function OrderSection({
  title,
  rows,
  collapsed,
  onToggle,
  t,
  onOpenMarket,
  addToast,
}: {
  title: string;
  rows: OrderDeskOrder[];
  collapsed: boolean;
  onToggle: () => void;
  t: Translate;
  onOpenMarket: (typeID: number) => void;
  addToast: AddToast;
}) {
  const notional = rows.reduce((sum, r) => sum + r.notional, 0);
  const needsAction = rows.filter(
    (r) => r.recommendation === "reprice" || r.recommendation === "cancel",
  ).length;

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
            formatIsk={formatIsk}
            t={t}
            onOpenMarket={onOpenMarket}
            addToast={addToast}
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
  cur,
  onClick,
  hint,
  align,
}: {
  label: string;
  k: OrdersSortKey;
  cur: Pick<OrdersPrefs, "sortKey" | "sortDir">;
  onClick: (k: OrdersSortKey) => void;
  hint: string;
  align: "left" | "right";
}) {
  const active = cur.sortKey === k;
  return (
    <th
      className={`px-2 py-1.5 ${align === "right" ? "text-right" : "text-left"} cursor-pointer hover:text-eve-text select-none`}
      onClick={() => onClick(k)}
      title={hint}
      aria-sort={active ? (cur.sortDir === "asc" ? "ascending" : "descending") : "none"}
    >
      {label}
      <span className={`ml-1 ${active ? "text-eve-accent" : "text-eve-dim/40"}`}>
        {active ? (cur.sortDir === "asc" ? "▲" : "▼") : "⇅"}
      </span>
    </th>
  );
}

function OrderRow({
  row,
  formatIsk,
  t,
  onOpenMarket,
  addToast,
}: {
  row: OrderDeskOrder;
  formatIsk: (v: number) => string;
  t: Translate;
  onOpenMarket: (typeID: number) => void;
  addToast: AddToast;
}) {
  const atTop = row.position === 1;
  const priceCls = atTop ? "text-eve-dim font-mono" : "text-eve-accent font-mono";
  const hasCopyablePrice = row.book_available && row.suggested_price > 0 && !atTop;
  const copy = () => {
    if (!hasCopyablePrice) return;
    void navigator.clipboard.writeText(row.suggested_price.toFixed(2));
  };
  // Opening the market window is almost always paired with pasting the
  // suggested price into the modify-order dialog. Copy the price at the
  // same time so the user doesn't need a second click on 📋.
  const openMarketAndCopyPrice = () => {
    if (hasCopyablePrice) {
      void navigator.clipboard.writeText(row.suggested_price.toFixed(2));
    }
    onOpenMarket(row.type_id);
  };
  // Fallback for when 🎮 silently fails (some hosts don't deliver the
  // UI command): copy the item name so the user can paste it into the
  // in-game market search.
  const copyName = () => {
    if (!row.type_name) return;
    void navigator.clipboard.writeText(row.type_name);
    addToast(t("copied"), "success", 1400);
  };
  const badgeClass =
    row.recommendation === "cancel"
      ? "bg-red-500/20 text-red-400"
      : row.recommendation === "reprice"
        ? "bg-amber-500/20 text-amber-400"
        : row.book_available
          ? "bg-emerald-500/20 text-emerald-400"
          : "bg-eve-dim/20 text-eve-dim";
  return (
    <tr className="border-t border-eve-border/50 hover:bg-eve-accent/5">
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
      <td className="px-2 py-1 text-eve-text max-w-[220px]" title={row.type_name}>
        <div className="flex items-center gap-1.5">
          <img
            src={`https://images.evetech.net/types/${row.type_id}/icon?size=32`}
            alt=""
            className="w-4 h-4"
          />
          <span className="truncate">{row.type_name || `Type #${row.type_id}`}</span>
          {row.type_name && (
            <button
              type="button"
              onClick={copyName}
              className="ml-auto shrink-0 text-[10px] px-1 py-0.5 rounded-sm border border-eve-border text-eve-dim hover:text-eve-accent hover:border-eve-accent transition-colors"
              title={t("ordersCopyNameHint")}
              aria-label={t("ordersCopyNameHint")}
            >
              📋
            </button>
          )}
        </div>
      </td>
      <td className="px-2 py-1 text-eve-dim max-w-[200px] truncate" title={row.location_name}>
        {row.location_name || `#${row.location_id}`}
      </td>
      <td className="px-2 py-1">
        <span
          className={`inline-flex px-1.5 py-0.5 rounded-sm text-[10px] font-medium uppercase tracking-wide ${badgeClass} cursor-help`}
          title={row.reason}
        >
          {row.recommendation}
        </span>
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
            {!atTop && (
              <button
                type="button"
                onClick={copy}
                className="text-[10px] px-1 py-0.5 rounded-sm border border-eve-border text-eve-dim hover:text-eve-accent hover:border-eve-accent transition-colors"
                title={t("operatorSuggestedPriceCopyHint")}
              >
                📋
              </button>
            )}
            <button
              type="button"
              onClick={openMarketAndCopyPrice}
              className="text-[10px] px-1 py-0.5 rounded-sm border border-eve-border text-eve-dim hover:text-eve-accent hover:border-eve-accent transition-colors"
              title={t("ordersOpenMarketHint")}
              aria-label={t("ordersOpenMarketHint")}
            >
              🎮
            </button>
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
      <td className="px-2 py-1 text-right text-eve-dim font-mono">
        {row.book_available ? `${row.position}/${row.total_orders}` : "—"}
      </td>
      <td className="px-2 py-1 text-right text-eve-dim font-mono">
        {row.eta_days >= 0 ? `${row.eta_days.toFixed(1)}d` : "—"}
      </td>
      <td className="px-2 py-1 text-right text-eve-dim font-mono">
        {row.days_to_expire >= 0 ? `${row.days_to_expire}d` : "—"}
      </td>
    </tr>
  );
}
