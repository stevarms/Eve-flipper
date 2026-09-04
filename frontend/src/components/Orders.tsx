import { useCallback, useEffect, useMemo, useState } from "react";
import { ExternalLink } from "lucide-react";
import { getAuthStatus, getOrderDesk, openMarketInGame } from "../lib/api";
import type {
  AuthCharacter,
  OrderDeskOrder,
  OrderDeskResponse,
} from "../lib/types";
import { useI18n, type TranslationKey } from "../lib/i18n";
import { formatIsk as formatIskLib } from "../lib/format";
import { useGlobalToast } from "./Toast";
import { handleEveUIError } from "../lib/handleEveUIError";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CopyPrice } from "@/components/ui/CopyPrice";
import { EmptyState } from "@/components/EmptyState";
import { Input } from "@/components/ui/input";
import { TypeIcon } from "@/components/ui/TypeIcon";
import { OrderRowDrawer, recommendationTone } from "@/components/orders/OrderRowDrawer";
import { cn } from "@/lib/utils";

// Orders.tsx — first-class main-tab replacement for the buried
// character-popup Order Desk. Aggregates active orders across every
// authorized character with the same per-order recommendations
// (hold / reprice / cancel) plus legal 4-sig-fig suggested prices from
// the item 1/2/3 chain.
//
// Six columns, per the three-tier disclosure rule (docs/UI_DESIGN_SYSTEM.md
// §4). The grid answers only "which orders do I touch, and what do I type
// into EVE"; the other five columns it used to carry — owner, station, side,
// best price, expiry — plus the queue and relist-fee arithmetic that was
// never in the grid at all now live in the row drawer. Side and station are
// still visible at a glance because they moved *into* the item cell rather
// than off the screen.

interface Props {
  isLoggedIn: boolean;
}

type ActionFilter = "all" | "needs_action" | "hold";
type SortKey = "priority" | "eta" | "expiry" | "notional" | "type";
type SortDir = "asc" | "desc";

/** Sorts reachable from the toolbar. Three also have a clickable header. */
const SORT_KEYS: readonly SortKey[] = ["priority", "type", "eta", "expiry", "notional"];

const SORT_LABEL_KEYS: Record<SortKey, TranslationKey> = {
  priority: "ordersColAction",
  type: "colItem",
  eta: "ordersColEta",
  expiry: "ordersColExpiry",
  notional: "ordersKpiNotional",
};

const PRIORITY_BY_ACTION: Record<string, number> = {
  cancel: 3,
  reprice: 2,
  hold: 0,
};

/** Local presentation of the shared formatter (lib/format.ts). */
function formatIsk(v: number): string {
  return formatIskLib(v, undefined, {
    maxTier: "T",
    space: false,
    decimals: { t: 2, b: 2, m: 2, k: 1, unit: 0 },
  });
}

export function Orders({ isLoggedIn }: Props) {
  const { t, locale } = useI18n();
  const { addToast } = useGlobalToast();
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
  const [inspected, setInspected] = useState<OrderDeskOrder | null>(null);

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

  const applySort = useCallback((k: SortKey) => {
    setSortKey((cur) => {
      if (cur === k) {
        setSortDir((d) => (d === "asc" ? "desc" : "asc"));
      } else {
        // Priority + notional feel natural high-to-low; ETA + expiry low-to-high.
        setSortDir(k === "priority" || k === "notional" ? "desc" : "asc");
      }
      return k;
    });
  }, []);

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
      <div className="flex h-full flex-col items-center justify-center space-y-2 font-ui text-t-body text-fg-tertiary">
        <div>{t("ordersNoAuth")}</div>
      </div>
    );
  }

  const multiCharacter = authCharacters.length > 1;

  return (
    <div className="flex h-full flex-col space-y-3 p-3">
      {/* Toolbar */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-ui text-t-caption uppercase tracking-wide text-fg-tertiary">
            {t("ordersActionFilterLabel")}
          </span>
          {(["all", "needs_action", "hold"] as ActionFilter[]).map((a) => (
            <button
              key={a}
              onClick={() => setActionFilter(a)}
              className={cn(
                "rounded-sm border px-2.5 py-1 font-ui text-t-caption transition-colors",
                actionFilter === a
                  ? "border-eve-accent bg-eve-accent/20 text-eve-accent"
                  : "border-eve-border bg-surface-2 text-fg-tertiary hover:border-eve-accent/50 hover:text-fg",
              )}
            >
              {t(`ordersActionFilter_${a}` as TranslationKey)}
            </button>
          ))}
        </div>
        <div className="flex items-center gap-2">
          <label className="flex items-center gap-1 font-ui text-t-caption text-fg-tertiary">
            {t("ordersSortLabel")}
            <select
              value={sortKey}
              onChange={(e) => applySort(e.target.value as SortKey)}
              className="rounded-sm border border-eve-border bg-surface-0 px-1 py-0.5 font-ui text-t-caption text-fg"
            >
              {SORT_KEYS.map((k) => (
                <option key={k} value={k}>
                  {t(SORT_LABEL_KEYS[k])}
                </option>
              ))}
            </select>
          </label>
          <label className="flex items-center gap-1 font-ui text-t-caption text-fg-tertiary">
            {t("ordersSalesTax")}
            <Input
              numeric
              type="number"
              min={0}
              max={100}
              step={0.1}
              value={salesTax}
              onChange={(e) => setSalesTax(parseFloat(e.target.value) || 0)}
              className="h-6 w-16 px-1 py-0.5"
            />
          </label>
          <label className="flex items-center gap-1 font-ui text-t-caption text-fg-tertiary">
            {t("ordersBrokerFee")}
            <Input
              numeric
              type="number"
              min={0}
              max={100}
              step={0.1}
              value={brokerFee}
              onChange={(e) => setBrokerFee(parseFloat(e.target.value) || 0)}
              className="h-6 w-16 px-1 py-0.5"
            />
          </label>
          <Button size="sm" variant="outline" onClick={() => void load()} disabled={loading}>
            {loading ? t("ordersRefreshing") : t("ordersRefresh")}
          </Button>
        </div>
      </div>

      {/* Character filter chips */}
      {multiCharacter && (
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="font-ui text-t-caption uppercase tracking-wide text-fg-tertiary">
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
                className={cn(
                  "inline-flex items-center gap-1 rounded-sm border px-2 py-0.5 font-ui text-t-caption transition-colors",
                  active
                    ? "border-eve-accent/50 bg-eve-accent/10 text-eve-accent"
                    : "border-eve-border/40 bg-surface-0 text-fg-tertiary opacity-60",
                )}
              >
                <img
                  src={`https://images.evetech.net/characters/${c.character_id}/portrait?size=32`}
                  alt=""
                  className="h-4 w-4 rounded-full"
                />
                <span>{c.character_name}</span>
              </button>
            );
          })}
          {characterFilter.size > 0 && (
            <button
              onClick={() => setCharacterFilter(new Set())}
              className="font-ui text-t-caption text-eve-accent hover:underline"
            >
              {t("ordersCharacterFilterClear")}
            </button>
          )}
        </div>
      )}

      {error && (
        <div className="rounded-sm border border-loss/50 bg-loss/10 px-3 py-2 font-ui text-t-cell text-loss">
          {error}
        </div>
      )}

      {/* KPI strip */}
      {data && (
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-5">
          <KPITile
            label={t("ordersKpiTotal")}
            value={String(data.summary.total_orders)}
          />
          <KPITile
            label={t("ordersKpiReprice")}
            value={String(data.summary.needs_reprice)}
            tone={data.summary.needs_reprice > 0 ? "warn" : undefined}
          />
          <KPITile
            label={t("ordersKpiCancel")}
            value={String(data.summary.needs_cancel)}
            tone={data.summary.needs_cancel > 0 ? "loss" : undefined}
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
      <div className="min-h-0 flex-1 overflow-auto rounded-sm border border-eve-border bg-surface-1">
        {loading && (
          <div className="p-4 text-center font-ui text-t-cell text-fg-tertiary">
            {t("ordersLoading")}
          </div>
        )}
        {!loading && filteredRows.length === 0 && (
          <EmptyState
            reason={data && data.orders.length > 0 ? "filters_too_strict" : "no_orders"}
            hints={data && data.orders.length > 0 ? [t("ordersEmpty")] : []}
          />
        )}
        {!loading && filteredRows.length > 0 && (
          <table className="w-full">
            <thead className="sticky top-0 z-10 bg-surface-0">
              <tr className="font-ui text-t-caption uppercase tracking-wide text-fg-tertiary">
                <SortableTH
                  label={t("colItem")}
                  k="type"
                  curKey={sortKey}
                  curDir={sortDir}
                  onClick={applySort}
                  align="left"
                />
                <SortableTH
                  label={t("ordersColAction")}
                  k="priority"
                  curKey={sortKey}
                  curDir={sortDir}
                  onClick={applySort}
                  align="left"
                />
                <th className="px-2 py-1.5 text-right font-medium">
                  {t("ordersColCurrent")}
                </th>
                <th className="px-2 py-1.5 text-right font-medium">
                  {t("operatorSuggestedPriceCol")}
                </th>
                <th className="px-2 py-1.5 text-right font-medium">
                  {t("ordersColPosition")}
                </th>
                <SortableTH
                  label={t("ordersColEta")}
                  k="eta"
                  curKey={sortKey}
                  curDir={sortDir}
                  onClick={applySort}
                  align="right"
                />
              </tr>
            </thead>
            <tbody>
              {filteredRows.map((r) => (
                <OrderRow
                  key={r.order_id}
                  row={r}
                  formatIsk={formatIsk}
                  t={t}
                  showOwner={multiCharacter}
                  onOpenMarket={openMarketForType}
                  onInspect={setInspected}
                />
              ))}
            </tbody>
          </table>
        )}
      </div>

      <OrderRowDrawer
        row={inspected}
        onClose={() => setInspected(null)}
        t={t}
        locale={locale}
      />
    </div>
  );
}

function KPITile({
  label,
  value,
  tone,
}: {
  label: string;
  value: string;
  tone?: "warn" | "loss";
}) {
  return (
    <div
      className={cn(
        "rounded-sm border p-2",
        tone === "warn" && "border-warn/50 bg-warn/5",
        tone === "loss" && "border-loss/50 bg-loss/5",
        !tone && "border-eve-border bg-surface-1",
      )}
    >
      <div className="font-ui text-t-caption uppercase tracking-wide text-fg-tertiary">
        {label}
      </div>
      <div
        className={cn(
          "font-num tnum text-t-display font-semibold",
          tone === "warn" && "text-warn",
          tone === "loss" && "text-loss",
          !tone && "text-fg",
        )}
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
      // Static class names, not `text-${align}` — Tailwind scans source text,
      // so an interpolated utility is only in the bundle by accident.
      className={cn(
        "cursor-pointer select-none px-2 py-1.5 font-medium hover:text-fg",
        align === "right" ? "text-right" : "text-left",
      )}
      aria-sort={active ? (curDir === "asc" ? "ascending" : "descending") : "none"}
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
  showOwner,
  onOpenMarket,
  onInspect,
}: {
  row: OrderDeskOrder;
  formatIsk: (v: number) => string;
  t: (key: TranslationKey, params?: Record<string, string | number>) => string;
  /** Only worth a portrait when more than one character is authorized. */
  showOwner: boolean;
  onOpenMarket: (typeID: number) => void;
  onInspect: (row: OrderDeskOrder) => void;
}) {
  const atTop = row.position === 1;
  const hasCopyablePrice = row.book_available && row.suggested_price > 0 && !atTop;
  // Opening the market window is almost always paired with pasting the
  // suggested price into the modify-order dialog. Copy the price at the
  // same time so the user doesn't need a second click.
  const openMarketAndCopyPrice = (e: React.MouseEvent) => {
    e.stopPropagation();
    if (hasCopyablePrice) {
      void navigator.clipboard.writeText(row.suggested_price.toFixed(2));
    }
    onOpenMarket(row.type_id);
  };
  const sideLabel = row.is_buy_order ? t("charBuy") : t("charSell");

  return (
    <tr
      className="h-row cursor-pointer border-t border-eve-border/50 hover:bg-eve-accent/5"
      onClick={(e) => {
        // Don't hijack the copy / open-market buttons.
        if ((e.target as HTMLElement).closest("button, a")) return;
        onInspect(row);
      }}
    >
      <td className="max-w-[320px] px-2 py-1">
        <div className="flex items-center gap-1.5">
          {showOwner && row.character_id ? (
            <img
              src={`https://images.evetech.net/characters/${row.character_id}/portrait?size=32`}
              alt=""
              title={row.character_name || `#${row.character_id}`}
              className="h-4 w-4 shrink-0 rounded-full"
            />
          ) : null}
          <TypeIcon typeId={row.type_id} size={18} />
          <div className="min-w-0">
            <div className="truncate font-ui text-t-body text-fg" title={row.type_name}>
              {row.type_name || `Type #${row.type_id}`}
            </div>
            <div
              className="truncate font-ui text-t-caption text-fg-tertiary"
              title={row.location_name}
            >
              {sideLabel} · {row.location_name || `#${row.location_id}`}
            </div>
          </div>
        </div>
      </td>
      <td className="px-2 py-1">
        <Badge
          tone={recommendationTone(row.recommendation, row.book_available)}
          className="cursor-help"
          title={row.reason}
        >
          {row.recommendation}
        </Badge>
      </td>
      <td className="px-2 py-1 text-right font-num tnum text-t-cell text-fg">
        {formatIsk(row.price)}
      </td>
      <td className="px-2 py-1 text-right">
        {row.book_available && row.suggested_price > 0 ? (
          <div className="inline-flex items-center justify-end gap-1.5">
            {row.warn_unprofitable_relist && (
              <span
                title={t("operatorUnprofitableRelistHint", {
                  fee: formatIsk(row.relist_fee_isk ?? 0),
                })}
                className="cursor-help text-warn"
              >
                ⚠
              </span>
            )}
            <span
              className={cn(
                "font-num tnum text-t-cell",
                atTop ? "text-fg-tertiary" : "text-info",
              )}
            >
              {formatIsk(row.suggested_price)}
            </span>
            {!atTop && (
              <CopyPrice
                value={row.suggested_price}
                label={t("operatorSuggestedPriceCopyHint")}
              />
            )}
            <button
              type="button"
              onClick={openMarketAndCopyPrice}
              className="inline-flex h-5 w-5 items-center justify-center rounded-sm text-fg-tertiary transition-colors hover:bg-surface-2 hover:text-fg"
              title={t("ordersOpenMarketHint")}
              aria-label={t("ordersOpenMarketHint")}
            >
              <ExternalLink className="h-3.5 w-3.5" />
            </button>
          </div>
        ) : (
          <span className="text-fg-tertiary">—</span>
        )}
      </td>
      <td className="px-2 py-1 text-right font-num tnum text-t-cell text-fg-secondary">
        {row.book_available ? `${row.position}/${row.total_orders}` : "—"}
      </td>
      <td className="px-2 py-1 text-right font-num tnum text-t-cell text-fg-secondary">
        {row.eta_days >= 0 ? `${row.eta_days.toFixed(1)}d` : "—"}
      </td>
    </tr>
  );
}
