import { useCallback, useEffect, useMemo, useState } from "react";
import { getAuthStatus, getOrderDesk, openMarketInGame } from "../lib/api";
import type {
  AuthCharacter,
  OrderDeskOrder,
  OrderDeskResponse,
} from "../lib/types";
import { useI18n, type TranslationKey } from "../lib/i18n";

// Orders.tsx — first-class main-tab replacement for the buried
// character-popup Order Desk. Aggregates active orders across every
// authorized character with the same per-order recommendations
// (hold / reprice / cancel) plus legal 4-sig-fig suggested prices from
// the item 1/2/3 chain. Renders one row per order with an action badge,
// copy-to-clipboard for the target price, and a ⚠ warning when the
// broker relist fee would eat the theoretical gain.

interface Props {
  isLoggedIn: boolean;
}

type ActionFilter = "all" | "needs_action" | "hold";
type SortKey = "priority" | "eta" | "expiry" | "notional" | "type";
type SortDir = "asc" | "desc";

const PRIORITY_BY_ACTION: Record<string, number> = {
  cancel: 3,
  reprice: 2,
  hold: 0,
};

function formatIsk(v: number): string {
  const abs = Math.abs(v);
  if (abs >= 1e12) return `${(v / 1e12).toFixed(2)}T`;
  if (abs >= 1e9) return `${(v / 1e9).toFixed(2)}B`;
  if (abs >= 1e6) return `${(v / 1e6).toFixed(2)}M`;
  if (abs >= 1e3) return `${(v / 1e3).toFixed(1)}K`;
  return v.toFixed(0);
}

export function Orders({ isLoggedIn }: Props) {
  const { t } = useI18n();
  const [data, setData] = useState<OrderDeskResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [salesTax, setSalesTax] = useState<number>(8);
  const [brokerFee, setBrokerFee] = useState<number>(1);
  const [characterFilter, setCharacterFilter] = useState<Set<number>>(new Set());
  const [actionFilter, setActionFilter] = useState<ActionFilter>("all");
  const [sortKey, setSortKey] = useState<SortKey>("priority");
  const [sortDir, setSortDir] = useState<SortDir>("desc");
  const [authCharacters, setAuthCharacters] = useState<AuthCharacter[]>([]);

  useEffect(() => {
    if (!isLoggedIn) return;
    void getAuthStatus()
      .then((s) => setAuthCharacters(s.characters ?? []))
      .catch(() => setAuthCharacters([]));
  }, [isLoggedIn]);

  const load = useCallback(async () => {
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
      });
      setData(resp);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [isLoggedIn, salesTax, brokerFee]);

  useEffect(() => {
    void load();
  }, [load]);

  // Refetch on window focus so returning to the app shows a fresh number.
  useEffect(() => {
    if (!isLoggedIn) return;
    const onFocus = () => void load();
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, [load, isLoggedIn]);

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
    if (actionFilter === "needs_action") {
      rows = rows.filter(
        (r) => r.recommendation === "reprice" || r.recommendation === "cancel",
      );
    } else if (actionFilter === "hold") {
      rows = rows.filter((r) => r.recommendation === "hold");
    }
    rows.sort((a, b) => {
      let cmp = 0;
      switch (sortKey) {
        case "priority":
          cmp =
            (PRIORITY_BY_ACTION[a.recommendation] ?? 0) -
            (PRIORITY_BY_ACTION[b.recommendation] ?? 0);
          if (cmp === 0) {
            // Tie-break: ETA ascending (soonest expiring first).
            const aEta = a.eta_days >= 0 ? a.eta_days : Number.POSITIVE_INFINITY;
            const bEta = b.eta_days >= 0 ? b.eta_days : Number.POSITIVE_INFINITY;
            cmp = bEta - aEta;
          }
          break;
        case "eta": {
          const aEta = a.eta_days >= 0 ? a.eta_days : Number.POSITIVE_INFINITY;
          const bEta = b.eta_days >= 0 ? b.eta_days : Number.POSITIVE_INFINITY;
          cmp = aEta - bEta;
          break;
        }
        case "expiry": {
          const aE =
            a.days_to_expire >= 0 ? a.days_to_expire : Number.POSITIVE_INFINITY;
          const bE =
            b.days_to_expire >= 0 ? b.days_to_expire : Number.POSITIVE_INFINITY;
          cmp = aE - bE;
          break;
        }
        case "notional":
          cmp = a.notional - b.notional;
          break;
        case "type":
          cmp = (a.type_name || "").localeCompare(b.type_name || "");
          break;
      }
      return sortDir === "asc" ? cmp : -cmp;
    });
    return rows;
  }, [data, characterFilter, actionFilter, sortKey, sortDir]);

  const toggleSort = (k: SortKey) => {
    if (sortKey === k) setSortDir(sortDir === "asc" ? "desc" : "asc");
    else {
      setSortKey(k);
      // Priority + notional feel natural high-to-low; ETA + expiry low-to-high.
      setSortDir(k === "priority" || k === "notional" ? "desc" : "asc");
    }
  };

  if (!isLoggedIn) {
    return (
      <div className="flex flex-col items-center justify-center h-full text-eve-dim text-sm space-y-2">
        <div>{t("ordersNoAuth")}</div>
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full space-y-3 p-3">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-xs text-eve-dim uppercase tracking-wider">
            {t("ordersActionFilterLabel")}
          </span>
          {(["all", "needs_action", "hold"] as ActionFilter[]).map((a) => (
            <button
              key={a}
              onClick={() => setActionFilter(a)}
              className={`px-2.5 py-1 text-[11px] rounded-sm border transition-colors ${
                actionFilter === a
                  ? "bg-eve-accent/20 border-eve-accent text-eve-accent"
                  : "bg-eve-panel border-eve-border text-eve-dim hover:text-eve-text hover:border-eve-accent/50"
              }`}
            >
              {t(`ordersActionFilter_${a}` as TranslationKey)}
            </button>
          ))}
        </div>
        <div className="flex items-center gap-2">
          <label className="text-[11px] text-eve-dim">
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
          <label className="text-[11px] text-eve-dim">
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
            onClick={() => void load()}
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
        {loading && (
          <div className="p-4 text-center text-eve-dim text-xs">
            {t("ordersLoading")}
          </div>
        )}
        {!loading && filteredRows.length === 0 && (
          <div className="p-4 text-center text-eve-dim text-xs">
            {t("ordersEmpty")}
          </div>
        )}
        {!loading && filteredRows.length > 0 && (
          <table className="w-full text-xs">
            <thead className="bg-eve-dark sticky top-0 z-10">
              <tr className="text-eve-dim">
                <th className="px-2 py-1.5 text-left">{t("ordersColOwner")}</th>
                <SortableTH
                  label={t("colItem")}
                  k="type"
                  curKey={sortKey}
                  curDir={sortDir}
                  onClick={toggleSort}
                  align="left"
                />
                <th className="px-2 py-1.5 text-left">{t("ordersColStation")}</th>
                <th className="px-2 py-1.5 text-left">{t("ordersColSide")}</th>
                <SortableTH
                  label={t("ordersColAction")}
                  k="priority"
                  curKey={sortKey}
                  curDir={sortDir}
                  onClick={toggleSort}
                  align="left"
                />
                <th className="px-2 py-1.5 text-right">{t("ordersColCurrent")}</th>
                <th className="px-2 py-1.5 text-right">{t("ordersColBest")}</th>
                <th className="px-2 py-1.5 text-right">
                  {t("operatorSuggestedPriceCol")}
                </th>
                <th className="px-2 py-1.5 text-right">{t("ordersColPosition")}</th>
                <SortableTH
                  label={t("ordersColEta")}
                  k="eta"
                  curKey={sortKey}
                  curDir={sortDir}
                  onClick={toggleSort}
                  align="right"
                />
                <SortableTH
                  label={t("ordersColExpiry")}
                  k="expiry"
                  curKey={sortKey}
                  curDir={sortDir}
                  onClick={toggleSort}
                  align="right"
                />
              </tr>
            </thead>
            <tbody>
              {filteredRows.map((r) => (
                <OrderRow key={r.order_id} row={r} formatIsk={formatIsk} t={t} />
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
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

function SortableTH<K extends string>({
  label,
  k,
  curKey,
  curDir,
  onClick,
  align,
}: {
  label: string;
  k: K;
  curKey: K;
  curDir: SortDir;
  onClick: (k: K) => void;
  align: "left" | "right";
}) {
  const active = curKey === k;
  return (
    <th
      className={`px-2 py-1.5 text-${align} cursor-pointer hover:text-eve-text select-none`}
      onClick={() => onClick(k)}
    >
      {label}
      {active && <span className="ml-1">{curDir === "asc" ? "▲" : "▼"}</span>}
    </th>
  );
}

function OrderRow({
  row,
  formatIsk,
  t,
}: {
  row: OrderDeskOrder;
  formatIsk: (v: number) => string;
  t: (key: TranslationKey, params?: Record<string, string | number>) => string;
}) {
  const atTop = row.position === 1;
  const priceCls = atTop ? "text-eve-dim font-mono" : "text-eve-accent font-mono";
  const copy = () => {
    if (!row.book_available || row.suggested_price <= 0 || atTop) return;
    void navigator.clipboard.writeText(row.suggested_price.toFixed(2));
  };
  const badgeClass =
    row.recommendation === "cancel"
      ? "bg-red-500/20 text-red-400"
      : row.recommendation === "reprice"
        ? "bg-amber-500/20 text-amber-400"
        : row.book_available
          ? "bg-emerald-500/20 text-emerald-400"
          : "bg-eve-dim/20 text-eve-dim";
  const sideLabel = row.is_buy_order ? t("charBuy") : t("charSell");
  const sideClass = row.is_buy_order ? "text-eve-profit" : "text-eve-error";
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
        </div>
      </td>
      <td className="px-2 py-1 text-eve-dim max-w-[200px] truncate" title={row.location_name}>
        {row.location_name || `#${row.location_id}`}
      </td>
      <td className={`px-2 py-1 ${sideClass}`}>{sideLabel}</td>
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
              onClick={() => {
                void openMarketInGame(row.type_id).catch(() => {
                  // Silent — ESI open-window fails when the client isn't
                  // running / user isn't logged in in-game. Users will
                  // notice the game didn't respond and re-check EVE.
                });
              }}
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
