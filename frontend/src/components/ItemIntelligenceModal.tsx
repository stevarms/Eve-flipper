import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import {
  AlertTriangle,
  BarChart3,
  ExternalLink,
  Loader2,
  Package,
  Search,
  WalletCards,
} from "lucide-react";
import { Modal } from "./Modal";
import { useGlobalToast } from "./Toast";
import { getItemIntelligence, openMarketInGame, searchItems } from "../lib/api";
import { formatISK, formatMargin, formatNumber } from "../lib/format";
import type { ItemIntelligence, ItemSearchResult } from "../lib/types";

interface ItemIntelligenceModalProps {
  open: boolean;
  onClose: () => void;
}

const DEFAULT_REGION_ID = 10000002;

function formatUnits(value: number): string {
  if (!Number.isFinite(value)) return "0";
  return formatNumber(Math.round(value));
}

function formatVolume(value: number): string {
  if (!Number.isFinite(value)) return "0 m3";
  return `${value.toLocaleString(undefined, { maximumFractionDigits: 2 })} m3`;
}

function metricTone(value: number): string {
  if (value > 0) return "text-green-400";
  if (value < 0) return "text-eve-error";
  return "text-eve-dim";
}

function scoreTone(value: number): string {
  if (value >= 70) return "text-green-400";
  if (value >= 45) return "text-eve-warning";
  return "text-eve-error";
}

function formatDays(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "-";
  if (value < 1)
    return `${(value * 24).toLocaleString(undefined, { maximumFractionDigits: 1 })}h`;
  return `${value.toLocaleString(undefined, { maximumFractionDigits: 1 })}d`;
}

function MetricCell({
  label,
  value,
  hint,
  tone = "text-eve-text",
}: {
  label: string;
  value: ReactNode;
  hint?: string;
  tone?: string;
}) {
  return (
    <div className="min-h-[68px] border border-eve-border bg-eve-panel/35 px-3 py-2">
      <div className="text-[10px] uppercase tracking-[0.16em] text-eve-dim">
        {label}
      </div>
      <div className={`mt-1 font-mono text-sm ${tone}`}>{value}</div>
      {hint && (
        <div className="mt-1 text-[10px] text-eve-dim leading-snug">{hint}</div>
      )}
    </div>
  );
}

function ResultRow({
  item,
  active,
  onSelect,
}: {
  item: ItemSearchResult;
  active: boolean;
  onSelect: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onSelect}
      className={`w-full text-left px-3 py-2 border-b border-eve-border/50 transition-colors ${
        active
          ? "bg-eve-accent/12 text-eve-accent"
          : "text-eve-text hover:bg-eve-panel-hover"
      }`}
    >
      <div className="flex items-center gap-2">
        <div className="flex h-8 w-8 shrink-0 items-center justify-center border border-eve-border bg-black/40 text-[9px] font-semibold text-eve-dim">
          {item.type_id}
        </div>
        <div className="min-w-0">
          <div className="truncate text-xs font-semibold">{item.type_name}</div>
          <div className="mt-0.5 truncate text-[10px] text-eve-dim">
            {item.group_name || `Group ${item.group_id}`} |{" "}
            {formatVolume(item.volume)}
          </div>
        </div>
      </div>
    </button>
  );
}

export function ItemIntelligenceModal({
  open,
  onClose,
}: ItemIntelligenceModalProps) {
  const { addToast } = useGlobalToast();
  const inputRef = useRef<HTMLInputElement>(null);
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<ItemSearchResult[]>([]);
  const [selected, setSelected] = useState<ItemSearchResult | null>(null);
  const [intel, setIntel] = useState<ItemIntelligence | null>(null);
  const [searchLoading, setSearchLoading] = useState(false);
  const [intelLoading, setIntelLoading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!open) return;
    window.setTimeout(() => inputRef.current?.focus(), 50);
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const trimmed = query.trim();
    setError("");
    if (trimmed.length < 2) {
      setResults([]);
      return;
    }
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      setSearchLoading(true);
      searchItems(trimmed, 30, controller.signal)
        .then((items) => {
          setResults(items);
        })
        .catch((err) => {
          if (!controller.signal.aborted) {
            setError(err instanceof Error ? err.message : "Item search failed");
          }
        })
        .finally(() => {
          if (!controller.signal.aborted) setSearchLoading(false);
        });
    }, 180);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [open, query]);

  useEffect(() => {
    if (!open || !selected) {
      setIntel(null);
      return;
    }
    const controller = new AbortController();
    setIntelLoading(true);
    setError("");
    getItemIntelligence(selected.type_id, DEFAULT_REGION_ID, controller.signal)
      .then(setIntel)
      .catch((err) => {
        if (!controller.signal.aborted) {
          setError(
            err instanceof Error ? err.message : "Item intelligence failed",
          );
          setIntel(null);
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setIntelLoading(false);
      });
    return () => controller.abort();
  }, [open, selected]);

  const diagnostics = useMemo(() => {
    if (!intel) return [];
    const out: string[] = [];
    if ((intel.market.sell_order_count ?? 0) === 0)
      out.push("No regional sell orders visible.");
    if ((intel.market.buy_order_count ?? 0) === 0)
      out.push("No regional buy orders visible.");
    if ((intel.history.days ?? 0) === 0)
      out.push("No cached/history data for this item.");
    if ((intel.character.assets ?? 0) > 0)
      out.push("You already have inventory for this type.");
    if ((intel.personal?.archived_rows_used ?? 0) === 0)
      out.push("No archived personal transactions for this type yet.");
    return [...out, ...(intel.warnings ?? [])];
  }, [intel]);

  const handleOpenMarket = async () => {
    if (!selected) return;
    try {
      await openMarketInGame(selected.type_id);
      addToast("Market window requested", "success", 1800);
    } catch (err) {
      addToast(
        err instanceof Error ? err.message : "Failed to open market",
        "error",
        3000,
      );
    }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Item Intelligence"
      width="max-w-7xl"
      allowFullscreen
    >
      <div className="flex h-full min-h-[70vh] flex-col bg-eve-dark">
        <div className="border-b border-eve-border bg-eve-panel/60 px-4 py-3">
          <div className="flex flex-col gap-3 lg:flex-row lg:items-end">
            <label className="flex-1 min-w-0">
              <span className="block text-[10px] uppercase tracking-[0.16em] text-eve-dim">
                Item search
              </span>
              <div className="mt-1 flex items-center border border-eve-border bg-eve-input">
                <Search
                  className="ml-3 h-4 w-4 shrink-0 text-eve-dim"
                  aria-hidden="true"
                />
                <input
                  ref={inputRef}
                  value={query}
                  onChange={(e) => {
                    setQuery(e.target.value);
                    setSelected(null);
                  }}
                  placeholder="Search by item name or type ID"
                  className="min-w-0 flex-1 bg-transparent px-3 py-2 text-sm text-eve-text outline-none placeholder:text-eve-dim"
                />
                {searchLoading && (
                  <Loader2
                    className="mr-3 h-4 w-4 animate-spin text-eve-accent"
                    aria-hidden="true"
                  />
                )}
              </div>
            </label>
            <div className="text-[11px] leading-relaxed text-eve-dim lg:max-w-[420px]">
              Personal item page: market depth, history signal, owned stock and
              active orders in one place.
            </div>
          </div>
        </div>

        <div className="grid flex-1 min-h-0 grid-cols-1 lg:grid-cols-[340px_minmax(0,1fr)]">
          <aside className="min-h-[220px] border-b border-eve-border lg:border-b-0 lg:border-r">
            {query.trim().length < 2 ? (
              <div className="flex h-full items-center justify-center px-4 text-center text-xs text-eve-dim">
                Type at least two characters to search the SDE item database.
              </div>
            ) : results.length === 0 && !searchLoading ? (
              <div className="flex h-full items-center justify-center px-4 text-center text-xs text-eve-dim">
                No matching items.
              </div>
            ) : (
              <div className="h-full overflow-auto">
                {results.map((item) => (
                  <ResultRow
                    key={item.type_id}
                    item={item}
                    active={selected?.type_id === item.type_id}
                    onSelect={() => setSelected(item)}
                  />
                ))}
              </div>
            )}
          </aside>

          <main className="min-h-0 overflow-auto p-4">
            {error && (
              <div className="mb-3 border border-eve-error/45 bg-eve-error/10 px-3 py-2 text-xs text-eve-error">
                {error}
              </div>
            )}

            {!selected ? (
              <div className="flex h-full min-h-[360px] items-center justify-center text-sm text-eve-dim">
                Select an item to inspect market and character context.
              </div>
            ) : (
              <div className="space-y-4">
                <div className="flex flex-col gap-3 border-b border-eve-border pb-4 sm:flex-row sm:items-center sm:justify-between">
                  <div className="flex min-w-0 items-center gap-3">
                    <div className="flex h-14 w-14 shrink-0 items-center justify-center border border-eve-border bg-black text-[11px] font-semibold text-eve-dim">
                      {selected.type_id}
                    </div>
                    <div className="min-w-0">
                      <h3 className="truncate text-lg font-semibold uppercase tracking-[0.08em] text-eve-accent">
                        {selected.type_name}
                      </h3>
                      <div className="mt-1 text-xs text-eve-dim">
                        Type {selected.type_id} |{" "}
                        {selected.group_name || `Group ${selected.group_id}`} |{" "}
                        {formatVolume(selected.volume)}
                      </div>
                    </div>
                  </div>
                  <button
                    type="button"
                    onClick={() => void handleOpenMarket()}
                    className="inline-flex items-center justify-center gap-2 border border-eve-accent/50 bg-eve-accent/10 px-3 py-2 text-xs font-semibold uppercase tracking-[0.12em] text-eve-accent hover:bg-eve-accent/20"
                  >
                    <ExternalLink className="h-4 w-4" aria-hidden="true" />
                    Open market
                  </button>
                </div>

                {intelLoading && !intel ? (
                  <div className="flex min-h-[280px] items-center justify-center gap-2 text-sm text-eve-dim">
                    <Loader2
                      className="h-4 w-4 animate-spin text-eve-accent"
                      aria-hidden="true"
                    />
                    Loading item intelligence...
                  </div>
                ) : intel ? (
                  (() => {
                    const lastTradeDate = intel.personal?.last_trade_date;
                    const recentTrades = intel.recent_trades ?? [];
                    return (
                      <>
                        <section>
                          <div className="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.16em] text-eve-accent">
                            <AlertTriangle
                              className="h-4 w-4"
                              aria-hidden="true"
                            />
                            Decision summary
                          </div>
                          <div className="grid grid-cols-2 gap-2 xl:grid-cols-5">
                            <MetricCell
                              label="Edge"
                              value={`${intel.edge?.label ?? "Unknown"} (${(intel.edge?.score ?? 0).toFixed(0)})`}
                              tone={scoreTone(intel.edge?.score ?? 0)}
                              hint={intel.edge?.recommendation}
                            />
                            <MetricCell
                              label="Confidence"
                              value={formatMargin(
                                intel.edge?.confidence_pct ??
                                  intel.restock?.confidence_pct ??
                                  0,
                              )}
                              tone={scoreTone(intel.edge?.confidence_pct ?? 0)}
                              hint="Market, history and personal archive quality."
                            />
                            <MetricCell
                              label="Action"
                              value={(
                                intel.restock?.suggested_action || "watch"
                              ).replace(/_/g, " ")}
                              tone={
                                intel.restock?.suggested_action === "avoid"
                                  ? "text-eve-error"
                                  : intel.restock?.suggested_action ===
                                      "restock"
                                    ? "text-green-400"
                                    : "text-eve-warning"
                              }
                              hint={intel.restock?.reason}
                            />
                            <MetricCell
                              label="Max entry"
                              value={`${formatISK(intel.restock?.max_entry_price ?? 0)} ISK`}
                            />
                            <MetricCell
                              label="Target sell"
                              value={`${formatISK(intel.restock?.target_sell_price ?? 0)} ISK`}
                            />
                            <MetricCell
                              label="Worst-case exit"
                              value={`${formatISK(intel.restock?.worst_case_exit_price ?? 0)} ISK`}
                            />
                            <MetricCell
                              label="Restock qty"
                              value={formatUnits(
                                intel.restock?.recommended_max_units ?? 0,
                              )}
                              hint={`${formatISK(intel.restock?.recommended_max_isk ?? 0)} ISK cap`}
                            />
                            <MetricCell
                              label="Coverage"
                              value={formatDays(
                                intel.restock?.coverage_days ?? 0,
                              )}
                              hint={`${formatUnits(intel.restock?.current_coverage ?? 0)} units owned/listed`}
                            />
                            <MetricCell
                              label="Missing units"
                              value={formatUnits(
                                intel.restock?.missing_units ?? 0,
                              )}
                            />
                            <MetricCell
                              label="Min spread"
                              value={formatMargin(
                                intel.restock?.min_spread_pct ?? 0,
                              )}
                            />
                          </div>
                          {(intel.restock?.risk_flags?.length ?? 0) > 0 && (
                            <div className="mt-2 flex flex-wrap gap-2">
                              {intel.restock?.risk_flags?.map((flag) => (
                                <span
                                  key={flag}
                                  className="border border-eve-warning/35 bg-eve-warning/10 px-2 py-1 text-[10px] uppercase tracking-[0.12em] text-eve-warning"
                                >
                                  {flag}
                                </span>
                              ))}
                            </div>
                          )}
                        </section>

                        <section>
                          <div className="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.16em] text-eve-accent">
                            <BarChart3 className="h-4 w-4" aria-hidden="true" />
                            Market snapshot
                          </div>
                          <div className="grid grid-cols-2 gap-2 xl:grid-cols-4">
                            <MetricCell
                              label="Best ask"
                              value={`${formatISK(intel.market.best_ask)} ISK`}
                            />
                            <MetricCell
                              label="Best bid"
                              value={`${formatISK(intel.market.best_bid)} ISK`}
                            />
                            <MetricCell
                              label="Raw spread"
                              value={`${formatISK(intel.market.spread)} ISK`}
                              tone={metricTone(intel.market.spread)}
                            />
                            <MetricCell
                              label="Spread"
                              value={formatMargin(intel.market.spread_percent)}
                              tone={metricTone(intel.market.spread_percent)}
                            />
                            <MetricCell
                              label="Best ask depth"
                              value={formatUnits(
                                intel.market.best_ask_volume ?? 0,
                              )}
                            />
                            <MetricCell
                              label="Best bid depth"
                              value={formatUnits(
                                intel.market.best_bid_volume ?? 0,
                              )}
                            />
                            <MetricCell
                              label="Sell orders"
                              value={formatUnits(intel.market.sell_order_count)}
                              hint={`${formatUnits(intel.market.sell_units)} units listed`}
                            />
                            <MetricCell
                              label="Buy orders"
                              value={formatUnits(intel.market.buy_order_count)}
                              hint={`${formatUnits(intel.market.buy_units)} units wanted`}
                            />
                            <MetricCell
                              label="Region"
                              value={
                                intel.market.region_name ||
                                `Region ${intel.market.region_id}`
                              }
                            />
                            <MetricCell
                              label="Item volume"
                              value={formatVolume(intel.volume)}
                            />
                            <MetricCell
                              label="Liquidity score"
                              value={(
                                intel.market.liquidity_score ?? 0
                              ).toFixed(0)}
                              tone={scoreTone(
                                intel.market.liquidity_score ?? 0,
                              )}
                            />
                            <MetricCell
                              label="Buy pressure"
                              value={formatMargin(
                                intel.market.buy_pressure_pct ?? 0,
                              )}
                            />
                            <MetricCell
                              label="5% sell depth"
                              value={formatUnits(
                                intel.market.sell_units_within_5_pct ?? 0,
                              )}
                            />
                            <MetricCell
                              label="5% buy depth"
                              value={formatUnits(
                                intel.market.buy_units_within_5_pct ?? 0,
                              )}
                            />
                            <MetricCell
                              label="Depth spread value"
                              value={`${formatISK(intel.market.estimated_spread_value ?? 0)} ISK`}
                            />
                          </div>
                          {(intel.market.depth_bands?.length ?? 0) > 0 && (
                            <div className="mt-2 overflow-hidden border border-eve-border">
                              <table className="w-full text-xs">
                                <thead className="bg-eve-panel text-[10px] uppercase tracking-[0.12em] text-eve-dim">
                                  <tr>
                                    <th className="px-3 py-2 text-left font-medium">
                                      Band
                                    </th>
                                    <th className="px-3 py-2 text-right font-medium">
                                      Sell units
                                    </th>
                                    <th className="px-3 py-2 text-right font-medium">
                                      Sell value
                                    </th>
                                    <th className="px-3 py-2 text-right font-medium">
                                      Buy units
                                    </th>
                                    <th className="px-3 py-2 text-right font-medium">
                                      Buy value
                                    </th>
                                  </tr>
                                </thead>
                                <tbody>
                                  {intel.market.depth_bands?.map((band) => (
                                    <tr
                                      key={band.band}
                                      className="border-t border-eve-border/50"
                                    >
                                      <td className="px-3 py-2 text-eve-text">
                                        {band.band}
                                      </td>
                                      <td className="px-3 py-2 text-right font-mono text-eve-text">
                                        {formatUnits(band.sell_units)}
                                      </td>
                                      <td className="px-3 py-2 text-right font-mono text-eve-dim">
                                        {formatISK(band.sell_value_isk)}
                                      </td>
                                      <td className="px-3 py-2 text-right font-mono text-eve-text">
                                        {formatUnits(band.buy_units)}
                                      </td>
                                      <td className="px-3 py-2 text-right font-mono text-eve-dim">
                                        {formatISK(band.buy_value_isk)}
                                      </td>
                                    </tr>
                                  ))}
                                </tbody>
                              </table>
                            </div>
                          )}
                        </section>

                        <section>
                          <div className="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.16em] text-eve-accent">
                            <Package className="h-4 w-4" aria-hidden="true" />
                            30d history
                          </div>
                          <div className="grid grid-cols-2 gap-2 xl:grid-cols-4">
                            <MetricCell
                              label="History days"
                              value={formatUnits(intel.history.days)}
                            />
                            <MetricCell
                              label="Avg price"
                              value={`${formatISK(intel.history.avg_price)} ISK`}
                            />
                            <MetricCell
                              label="Avg volume/day"
                              value={formatUnits(intel.history.avg_volume)}
                            />
                            <MetricCell
                              label="Avg ISK/day"
                              value={`${formatISK(intel.history.avg_value_isk ?? 0)} ISK`}
                            />
                            <MetricCell
                              label="Price change"
                              value={formatMargin(
                                intel.history.price_change_pct,
                              )}
                              tone={metricTone(intel.history.price_change_pct)}
                            />
                            <MetricCell
                              label="Volume change"
                              value={formatMargin(
                                intel.history.volume_change_pct ?? 0,
                              )}
                              tone={metricTone(
                                intel.history.volume_change_pct ?? 0,
                              )}
                            />
                            <MetricCell
                              label="Volatility"
                              value={formatMargin(intel.history.volatility_pct)}
                            />
                            <MetricCell
                              label="5% depth days"
                              value={formatDays(
                                intel.history.liquidity_days_5_pct ?? 0,
                              )}
                            />
                            <MetricCell
                              label="Low / high"
                              value={`${formatISK(intel.history.low_price ?? 0)} / ${formatISK(intel.history.high_price ?? 0)}`}
                            />
                          </div>
                        </section>

                        <section>
                          <div className="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.16em] text-eve-accent">
                            <WalletCards
                              className="h-4 w-4"
                              aria-hidden="true"
                            />
                            Character context
                          </div>
                          <div className="grid grid-cols-2 gap-2 xl:grid-cols-4">
                            <MetricCell
                              label="Owned assets"
                              value={formatUnits(intel.character.assets)}
                            />
                            <MetricCell
                              label="Active buy orders"
                              value={formatUnits(
                                intel.character.active_buy_orders,
                              )}
                            />
                            <MetricCell
                              label="Active sell orders"
                              value={formatUnits(
                                intel.character.active_sell_orders,
                              )}
                            />
                            <MetricCell
                              label="Asset value"
                              value={`${formatISK(intel.character.asset_value_isk ?? 0)} ISK`}
                            />
                            <MetricCell
                              label="Buy order value"
                              value={`${formatISK(intel.character.active_buy_isk ?? 0)} ISK`}
                            />
                            <MetricCell
                              label="Sell order value"
                              value={`${formatISK(intel.character.active_sell_isk ?? 0)} ISK`}
                            />
                            <MetricCell
                              label="Exposure"
                              value={`${formatISK(intel.character.exposure_isk ?? 0)} ISK`}
                            />
                            <MetricCell
                              label="Restock signal"
                              value={
                                intel.restock?.signal === "good_edge"
                                  ? "Good edge"
                                  : intel.restock?.signal === "avoid"
                                    ? "Avoid"
                                    : intel.character.assets <= 0 &&
                                        intel.character.active_sell_orders <= 0
                                      ? "No stock"
                                      : "Covered"
                              }
                              tone={
                                intel.restock?.signal === "good_edge"
                                  ? "text-green-400"
                                  : intel.restock?.signal === "avoid"
                                    ? "text-eve-error"
                                    : intel.character.assets <= 0 &&
                                        intel.character.active_sell_orders <= 0
                                      ? "text-eve-warning"
                                      : "text-green-400"
                              }
                              hint={intel.restock?.reason}
                            />
                          </div>
                        </section>

                        <section>
                          <div className="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.16em] text-eve-accent">
                            <WalletCards
                              className="h-4 w-4"
                              aria-hidden="true"
                            />
                            Personal trading history
                          </div>
                          <div className="grid grid-cols-2 gap-2 xl:grid-cols-4">
                            <MetricCell
                              label="Archived rows"
                              value={formatUnits(
                                intel.personal?.archived_rows_used ?? 0,
                              )}
                            />
                            <MetricCell
                              label="Turnover"
                              value={`${formatISK(intel.personal?.turnover_isk ?? 0)} ISK`}
                            />
                            <MetricCell
                              label="Realized PnL"
                              value={`${formatISK(intel.personal?.realized_pnl ?? 0)} ISK`}
                              tone={metricTone(
                                intel.personal?.realized_pnl ?? 0,
                              )}
                            />
                            <MetricCell
                              label="Realized ROI"
                              value={formatMargin(
                                intel.personal?.realized_roi_pct ?? 0,
                              )}
                              tone={metricTone(
                                intel.personal?.realized_roi_pct ?? 0,
                              )}
                              hint={
                                lastTradeDate
                                  ? `Last trade ${lastTradeDate.slice(0, 10)}`
                                  : undefined
                              }
                            />
                            <MetricCell
                              label="Bought"
                              value={formatUnits(
                                intel.personal?.buy_quantity ?? 0,
                              )}
                              hint={`${formatISK(intel.personal?.buy_isk ?? 0)} ISK`}
                            />
                            <MetricCell
                              label="Sold"
                              value={formatUnits(
                                intel.personal?.sell_quantity ?? 0,
                              )}
                              hint={`${formatISK(intel.personal?.sell_isk ?? 0)} ISK`}
                            />
                            <MetricCell
                              label="Restock qty"
                              value={formatUnits(
                                intel.restock?.recommended_max_units ?? 0,
                              )}
                            />
                            <MetricCell
                              label="Min spread"
                              value={formatMargin(
                                intel.restock?.min_spread_pct ?? 0,
                              )}
                              hint={`${formatISK(intel.restock?.recommended_max_isk ?? 0)} ISK cap`}
                            />
                            <MetricCell
                              label={`${intel.peer?.label || "Group"} edge`}
                              value={formatMargin(
                                intel.peer?.realized_roi_pct ?? 0,
                              )}
                              tone={metricTone(
                                intel.peer?.realized_roi_pct ?? 0,
                              )}
                              hint={`${formatUnits(intel.peer?.archived_rows_used ?? 0)} archived group rows`}
                            />
                            <MetricCell
                              label="Group PnL"
                              value={`${formatISK(intel.peer?.realized_pnl ?? 0)} ISK`}
                              tone={metricTone(intel.peer?.realized_pnl ?? 0)}
                            />
                          </div>
                          {recentTrades.length > 0 && (
                            <div className="mt-2 overflow-hidden border border-eve-border">
                              <table className="w-full text-xs">
                                <thead className="bg-eve-panel text-[10px] uppercase tracking-[0.12em] text-eve-dim">
                                  <tr>
                                    <th className="px-3 py-2 text-left font-medium">
                                      Date
                                    </th>
                                    <th className="px-3 py-2 text-left font-medium">
                                      Side
                                    </th>
                                    <th className="px-3 py-2 text-right font-medium">
                                      Qty
                                    </th>
                                    <th className="px-3 py-2 text-right font-medium">
                                      Unit price
                                    </th>
                                    <th className="px-3 py-2 text-right font-medium">
                                      Value
                                    </th>
                                    <th className="px-3 py-2 text-left font-medium">
                                      Location
                                    </th>
                                  </tr>
                                </thead>
                                <tbody>
                                  {recentTrades.map((trade, index) => (
                                    <tr
                                      key={`${trade.date}-${trade.side}-${index}`}
                                      className="border-t border-eve-border/50"
                                    >
                                      <td className="px-3 py-2 text-eve-dim">
                                        {trade.date.slice(0, 10)}
                                      </td>
                                      <td
                                        className={`px-3 py-2 uppercase ${trade.side === "buy" ? "text-eve-warning" : "text-green-400"}`}
                                      >
                                        {trade.side}
                                      </td>
                                      <td className="px-3 py-2 text-right font-mono text-eve-text">
                                        {formatUnits(trade.quantity)}
                                      </td>
                                      <td className="px-3 py-2 text-right font-mono text-eve-dim">
                                        {formatISK(trade.unit_price)}
                                      </td>
                                      <td className="px-3 py-2 text-right font-mono text-eve-text">
                                        {formatISK(trade.value_isk)}
                                      </td>
                                      <td className="px-3 py-2 text-eve-dim">
                                        {trade.location_name || "-"}
                                      </td>
                                    </tr>
                                  ))}
                                </tbody>
                              </table>
                            </div>
                          )}
                        </section>

                        {diagnostics.length > 0 && (
                          <section className="border border-eve-warning/35 bg-eve-warning/10 px-3 py-2">
                            <div className="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.16em] text-eve-warning">
                              <AlertTriangle
                                className="h-4 w-4"
                                aria-hidden="true"
                              />
                              Data notes
                            </div>
                            <div className="space-y-1 text-xs text-eve-text">
                              {diagnostics.map((note, index) => (
                                <div key={`${note}-${index}`}>{note}</div>
                              ))}
                            </div>
                          </section>
                        )}
                      </>
                    );
                  })()
                ) : null}
              </div>
            )}
          </main>
        </div>
      </div>
    </Modal>
  );
}
