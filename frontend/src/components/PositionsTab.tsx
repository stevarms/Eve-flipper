import { useCallback, useEffect, useMemo, useState } from "react";
import { Plus } from "lucide-react";
import { deleteManualPosition, getPositions } from "../lib/api";
import type { PositionRow, PositionsResponse } from "../lib/types";
import { useI18n } from "../lib/i18n";
import { formatIsk as formatIskLib, formatNumber } from "../lib/format";
import { useGlobalToast } from "./Toast";
import { useCharacterScope } from "./character/CharacterScopeProvider";
import { AddHoldingSheet } from "./positions/AddHoldingSheet";
import { PositionRowDrawer } from "./positions/PositionRowDrawer";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/EmptyState";
import { LoadingBlock } from "@/components/ui/LoadingBlock";
import { CopyPrice } from "@/components/ui/CopyPrice";
import { ItemRef } from "@/components/ui/ItemRef";
import { cn } from "@/lib/utils";

/**
 * Assets → Positions. Everything you are holding that you paid for.
 *
 * Two sources, never merged (see internal/api/positions.go): FIFO positions
 * derived from real transactions, and hand-entered holdings for the stock
 * ESI cannot see. Each row answers one question — should I sell this today —
 * so unrealized profit is stated *net of* the sell-side broker fee and sales
 * tax. A position that only looks green before fees is not green.
 *
 * Six decide columns per docs/UI_DESIGN_SYSTEM.md §4; the lot dates, fee
 * arithmetic, target price and note live in the row drawer.
 */

const formatIsk = (value: number) =>
  formatIskLib(value, undefined, {
    maxTier: "T",
    space: false,
    decimals: { t: 2, b: 2, m: 2, k: 1, unit: 0 },
  });

/**
 * Per-unit prices, which need finer resolution than totals do.
 *
 * EVE prices run from 0.01 ISK minerals to billion-ISK hulls, and the totals
 * formatter's `unit: 0` renders a Tritanium cost of 4.00 and a Jita sell of
 * 3.91 as the same "4" — the gap vanishes from the two columns whose only job
 * is to show it, while Unrealized underneath says -13%. `maximumFractionDigits`
 * does not pad, so a 850 ISK price still prints as "850".
 */
const formatUnitPrice = (value: number) =>
  formatIskLib(value, undefined, {
    maxTier: "T",
    space: false,
    decimals: { t: 2, b: 2, m: 2, k: 2, unit: 2 },
  });

type SortKey = "unrealized" | "value" | "age" | "item";

export function PositionsTab() {
  const { t, locale } = useI18n();
  const { scope } = useCharacterScope();
  const { addToast } = useGlobalToast();

  const [data, setData] = useState<PositionsResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [inspected, setInspected] = useState<PositionRow | null>(null);
  const [adding, setAdding] = useState(false);
  const [sortKey, setSortKey] = useState<SortKey>("unrealized");

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setData(await getPositions(scope));
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [scope]);

  useEffect(() => {
    void load();
  }, [load]);

  const rows = useMemo(() => {
    const list = [...(data?.rows ?? [])];
    list.sort((a, b) => {
      switch (sortKey) {
        case "value":
          return b.market_value - a.market_value;
        case "age":
          return b.days_held - a.days_held;
        case "item":
          return (a.type_name || "").localeCompare(b.type_name || "");
        default:
          return Math.abs(b.unrealized_isk) - Math.abs(a.unrealized_isk);
      }
    });
    return list;
  }, [data, sortKey]);

  const removeManual = useCallback(
    async (manualID: number) => {
      try {
        await deleteManualPosition(manualID);
        setInspected(null);
        await load();
      } catch (e: unknown) {
        addToast(e instanceof Error ? e.message : String(e), "error", 4000);
      }
    },
    [addToast, load],
  );

  return (
    <div className="flex h-full min-h-0 flex-col gap-3 p-4">
      {/* Header: states the model and its exclusions in plain English. */}
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 max-w-3xl">
          <h2 className="font-ui text-t-title font-semibold text-fg">{t("tabPositions")}</h2>
          <p className="mt-0.5 font-ui text-t-cell leading-relaxed text-fg-tertiary">
            {t("positionsSubtitle")}
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <select
            value={sortKey}
            onChange={(e) => setSortKey(e.target.value as SortKey)}
            className="h-8 rounded-sm border border-eve-border bg-surface-1 px-2 font-ui text-t-body text-fg"
            aria-label={t("positionsSortBy")}
          >
            <option value="unrealized">{t("positionsColUnrealized")}</option>
            <option value="value">{t("positionsKpiValue")}</option>
            <option value="age">{t("positionsColAge")}</option>
            <option value="item">{t("colItem")}</option>
          </select>
          <Button variant="primary" size="md" onClick={() => setAdding(true)}>
            <Plus className="mr-1 h-3.5 w-3.5" />
            {t("positionsAdd")}
          </Button>
          <Button variant="outline" size="md" onClick={() => void load()} disabled={loading}>
            {loading ? t("positionsRefreshing") : t("positionsRefresh")}
          </Button>
        </div>
      </div>

      {error && (
        <div className="rounded-sm border border-loss/50 bg-loss/10 px-3 py-2 font-ui text-t-cell text-loss">
          {error}
        </div>
      )}
      {data?.pricing_failed && (
        <div className="rounded-sm border border-warn/50 bg-warn/10 px-3 py-2 font-ui text-t-cell text-warn">
          {t("positionsPricingFailed")}
        </div>
      )}
      {data?.orders_failed && (
        <div className="rounded-sm border border-warn/50 bg-warn/10 px-3 py-2 font-ui text-t-cell text-warn">
          {t("positionsOrdersFailed")}
        </div>
      )}

      {data && rows.length > 0 && (
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <KPITile label={t("positionsKpiPositions")} value={String(rows.length)} />
          <KPITile label={t("positionsKpiCost")} value={`${formatIsk(data.total_cost_basis)} ISK`} />
          <KPITile label={t("positionsKpiValue")} value={`${formatIsk(data.total_market_value)} ISK`} />
          <KPITile
            label={t("positionsKpiUnrealized")}
            value={`${data.total_unrealized >= 0 ? "+" : ""}${formatIsk(data.total_unrealized)} ISK`}
            tone={data.total_unrealized >= 0 ? "profit" : "loss"}
          />
        </div>
      )}

      <div className="min-h-0 flex-1 overflow-auto rounded-sm border border-eve-border bg-surface-1">
        {loading && !data && <LoadingBlock label={t("positionsLoading")} />}
        {!loading && rows.length === 0 && (
          <EmptyState reason="no_data" hints={[t("positionsEmptyHint")]} />
        )}
        {rows.length > 0 && (
          <table className="w-full">
            <thead className="sticky top-0 z-10 bg-surface-0">
              <tr className="font-ui text-t-caption uppercase tracking-wide text-fg-tertiary">
                <th className="px-2 py-1.5 text-left font-medium">{t("colItem")}</th>
                <th className="px-2 py-1.5 text-right font-medium">{t("positionsColQty")}</th>
                <th className="px-2 py-1.5 text-right font-medium">{t("positionsColAvgCost")}</th>
                <th className="px-2 py-1.5 text-right font-medium">{t("positionsColNow")}</th>
                <th className="px-2 py-1.5 text-right font-medium">{t("positionsColUnrealized")}</th>
                <th className="px-2 py-1.5 text-right font-medium">{t("positionsColAge")}</th>
                <th className="w-px px-2 py-1.5" />
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <Row
                  key={`${row.source}-${row.manual_id ?? 0}-${row.type_id}`}
                  row={row}
                  onInspect={setInspected}
                  t={t}
                />
              ))}
            </tbody>
          </table>
        )}
      </div>

      <PositionRowDrawer
        row={inspected}
        salesTaxPercent={data?.sales_tax_percent ?? 0}
        brokerFeePercent={data?.broker_fee_percent ?? 0}
        onClose={() => setInspected(null)}
        onDelete={(id) => void removeManual(id)}
        t={t}
        locale={locale}
      />

      <AddHoldingSheet
        open={adding}
        onClose={() => setAdding(false)}
        onSaved={() => void load()}
        t={t}
      />
    </div>
  );
}

function Row({
  row,
  onInspect,
  t,
}: {
  row: PositionRow;
  onInspect: (row: PositionRow) => void;
  t: ReturnType<typeof useI18n>["t"];
}) {
  const priced = row.market_price > 0;
  return (
    <tr
      className="h-row cursor-pointer border-t border-eve-border/50 hover:bg-eve-accent/5"
      onClick={(e) => {
        if ((e.target as HTMLElement).closest("button, a")) return;
        onInspect(row);
      }}
    >
      <td className="max-w-[320px] px-2 py-1">
        <ItemRef
          typeId={row.type_id}
          name={row.type_name}
          tone="emphasis"
          market
          copyName
          marketLabel={t("positionsListHint")}
          subtitle={row.source === "manual" ? t("positionsSourceManual") : undefined}
        />
      </td>
      <td className="px-2 py-1 text-right font-num tnum text-t-cell text-fg-secondary">
        {formatNumber(row.qty)}
      </td>
      <td className="px-2 py-1 text-right font-num tnum text-t-cell text-fg-secondary">
        {formatUnitPrice(row.avg_unit_cost)}
      </td>
      <td className="px-2 py-1 text-right font-num tnum text-t-cell text-fg">
        {priced ? (
          <span className="inline-flex items-center justify-end gap-1">
            {formatUnitPrice(row.market_price)}
            <CopyPrice value={row.market_price} label={t("copyPrice")} />
          </span>
        ) : (
          <span className="cursor-help text-warn" title={t("positionsNoPrice")}>
            —
          </span>
        )}
      </td>
      <td className="px-2 py-1 text-right">
        {priced ? (
          <div className="leading-tight">
            <div
              className={cn(
                "font-num tnum text-t-cell",
                row.unrealized_isk >= 0 ? "text-profit" : "text-loss",
              )}
            >
              {row.unrealized_isk >= 0 ? "+" : ""}
              {formatIsk(row.unrealized_isk)}
            </div>
            <div className="font-num tnum text-t-caption text-fg-tertiary">
              {row.unrealized_pct >= 0 ? "+" : ""}
              {row.unrealized_pct.toFixed(1)}%
            </div>
          </div>
        ) : (
          <span className="text-fg-tertiary">—</span>
        )}
      </td>
      <td className="px-2 py-1 text-right font-num tnum text-t-cell text-fg-secondary">
        {t("positionsAgeDays", { n: row.days_held })}
      </td>
      <td className="px-2 py-1 text-right">
        {row.listed_qty > 0 ? (
          <Badge tone="accent" title={t("positionsListedHint")}>
            {t("positionsListed", { n: formatNumber(row.listed_qty) })}
          </Badge>
        ) : (
          <span className="text-fg-tertiary">—</span>
        )}
      </td>
    </tr>
  );
}

function KPITile({
  label,
  value,
  tone,
}: {
  label: string;
  value: string;
  tone?: "profit" | "loss";
}) {
  return (
    <div className="rounded-sm border border-eve-border bg-surface-1 px-3 py-2">
      <div className="font-ui text-t-caption uppercase tracking-wide text-fg-tertiary">{label}</div>
      <div
        className={cn(
          "font-num tnum text-t-title",
          tone === "profit" && "text-profit",
          tone === "loss" && "text-loss",
          !tone && "text-fg",
        )}
      >
        {value}
      </div>
    </div>
  );
}
