import { useCallback, useEffect, useMemo, useState } from "react";
import { ArrowRight, Check, Copy } from "lucide-react";
import {
  getOrderDesk,
  getScanHistory,
  getScanHistoryResults,
} from "@/lib/api";
import { formatISK, formatNumber } from "@/lib/format";
import { TypeIcon } from "@/components/ui/TypeIcon";
import { useI18n } from "@/lib/i18n";
import { useGlobalToast } from "@/components/Toast";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/EmptyState";
import { cn } from "@/lib/utils";
import type { MainTabId } from "@/lib/cockpit";
import type { OrderDeskOrder, OrderDeskResponse, ScanRecord, StationTrade } from "@/lib/types";

/**
 * Home / "Today" — the work order.
 *
 * Everything here is derived from data the app already produces: the order
 * desk (which orders are outbid or dead, and what to reprice them to) and
 * your most recent Station Trade scan, read back out of scan history. No new
 * endpoints, and deliberately no fresh scan on load — this screen must be
 * instant, and a scan is an explicit action you take on the Trade workspace.
 *
 * The design intent is a short ordered pass, not a dashboard: check what you
 * already have on the market before adding anything new, because repricing
 * an existing order earns more than a new one and costs a fraction of the
 * broker fee.
 */

const BUY_SHEET_LIMIT = 25;
const SELL_SHEET_LIMIT = 25;

function relativeAge(iso: string): string {
  const then = Date.parse(iso);
  if (!Number.isFinite(then)) return "";
  const mins = Math.max(0, Math.round((Date.now() - then) / 60000));
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.round(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.round(hours / 24)}d ago`;
}

/** Copy-to-clipboard button that confirms inline rather than via a toast. */
function CopyPrice({ value, label }: { value: number; label: string }) {
  const [copied, setCopied] = useState(false);
  const { addToast } = useGlobalToast();

  const copy = useCallback(async () => {
    // EVE's price field wants a plain number, not a formatted one.
    const plain = value.toFixed(2);
    try {
      await navigator.clipboard.writeText(plain);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1400);
    } catch {
      addToast("Clipboard unavailable", "error", 2500);
    }
  }, [value, addToast]);

  return (
    <button
      type="button"
      onClick={copy}
      aria-label={label}
      title={label}
      className={cn(
        "inline-flex h-5 w-5 items-center justify-center rounded-sm transition-colors",
        copied ? "text-profit" : "text-fg-tertiary hover:bg-surface-2 hover:text-fg",
      )}
    >
      {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
    </button>
  );
}

function Kpi({ label, value, tone }: { label: string; value: string; tone?: "warn" | "loss" }) {
  return (
    <div className="min-w-0 flex-1 border-r border-eve-border px-3 py-2 last:border-r-0">
      <div className="font-ui text-t-caption uppercase tracking-wide text-fg-tertiary">{label}</div>
      <div
        className={cn(
          "font-num tnum text-t-title font-semibold",
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

function Step({
  n,
  label,
  done,
  action,
}: {
  n: number;
  label: string;
  done?: boolean;
  action?: React.ReactNode;
}) {
  return (
    <li className="flex items-center gap-3 border-b border-eve-border/50 py-2 last:border-b-0">
      <span
        className={cn(
          "flex h-5 w-5 shrink-0 items-center justify-center rounded-full font-num text-t-caption font-semibold",
          done ? "bg-profit/15 text-profit" : "bg-eve-accent/15 text-eve-accent",
        )}
      >
        {done ? <Check className="h-3 w-3" /> : n}
      </span>
      <span className={cn("flex-1 font-ui text-t-body", done ? "text-fg-tertiary" : "text-fg")}>
        {label}
      </span>
      {action}
    </li>
  );
}

export interface HomeWorkspaceProps {
  isLoggedIn: boolean;
  onNavigate: (tab: MainTabId) => void;
}

export function HomeWorkspace({ isLoggedIn, onNavigate }: HomeWorkspaceProps) {
  const { t } = useI18n();
  const [desk, setDesk] = useState<OrderDeskResponse | null>(null);
  const [scan, setScan] = useState<{ record: ScanRecord; rows: StationTrade[] } | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;

    (async () => {
      setLoading(true);

      // Order desk needs auth; the buy sheet does not. Load them
      // independently so a logged-out user still gets the scan-derived half.
      const deskPromise = isLoggedIn
        ? getOrderDesk().catch(() => null)
        : Promise.resolve(null);

      const scanPromise = (async () => {
        try {
          const history = await getScanHistory(25);
          const latest = history.find((r) => r.tab === "station");
          if (!latest) return null;
          const res = await getScanHistoryResults(latest.id);
          return { record: latest, rows: (res.results as StationTrade[]) ?? [] };
        } catch {
          return null;
        }
      })();

      const [deskRes, scanRes] = await Promise.all([deskPromise, scanPromise]);
      if (cancelled) return;
      setDesk(deskRes);
      setScan(scanRes);
      setLoading(false);
    })();

    return () => {
      cancelled = true;
    };
  }, [isLoggedIn]);

  /** Top candidates from the last scan, by realistic daily profit. */
  const buySheet = useMemo(() => {
    if (!scan) return [];
    return [...scan.rows]
      .filter((r) => (r.DailyProfit ?? r.RealizableDailyProfit ?? 0) > 0)
      .sort(
        (a, b) =>
          (b.DailyProfit ?? b.RealizableDailyProfit ?? 0) -
          (a.DailyProfit ?? a.RealizableDailyProfit ?? 0),
      )
      .slice(0, BUY_SHEET_LIMIT);
  }, [scan]);

  /** Sell-side orders the desk says are outbid, with the price to paste. */
  const sellSheet = useMemo(() => {
    const orders = desk?.orders ?? [];
    return orders
      .filter((o) => !o.is_buy_order && o.recommendation === "reprice" && o.suggested_price > 0)
      .sort((a, b) => b.net_notional - a.net_notional)
      .slice(0, SELL_SHEET_LIMIT);
  }, [desk]);

  const summary = desk?.summary;
  const needsReprice = summary?.needs_reprice ?? 0;
  const needsCancel = summary?.needs_cancel ?? 0;
  const nothingToDo = isLoggedIn && needsReprice === 0 && needsCancel === 0 && buySheet.length === 0;

  if (loading) {
    return (
      <div className="flex-1 min-h-0 overflow-y-auto eve-scrollbar">
        <EmptyState reason="loading" />
      </div>
    );
  }

  return (
    <div className="flex-1 min-h-0 overflow-y-auto eve-scrollbar p-3">
      {/* Status strip — where your ISK currently is. */}
      {isLoggedIn && summary && (
        <div className="mb-3 flex rounded-sm border border-eve-border bg-surface-1">
          <Kpi label={t("homeCapitalInOrders")} value={formatISK(summary.total_notional)} />
          <Kpi label={t("homeOpenOrders")} value={formatNumber(summary.total_orders)} />
          <Kpi
            label={t("homeNeedsReprice")}
            value={formatNumber(needsReprice)}
            tone={needsReprice > 0 ? "warn" : undefined}
          />
          <Kpi
            label={t("homeNeedsCancel")}
            value={formatNumber(needsCancel)}
            tone={needsCancel > 0 ? "loss" : undefined}
          />
        </div>
      )}

      {/* The routine. Ordered deliberately: existing orders before new ones. */}
      <section className="mb-3 rounded-sm border border-eve-border bg-surface-1 p-3">
        <h2 className="font-ui text-t-title font-semibold text-fg">{t("homeRoutineTitle")}</h2>
        <p className="mb-2 font-ui text-t-caption text-fg-tertiary">{t("homeRoutineHint")}</p>

        {!isLoggedIn ? (
          <p className="font-ui text-t-body text-fg-secondary">{t("homeLoginPrompt")}</p>
        ) : nothingToDo ? (
          <p className="font-ui text-t-body text-profit">{t("homeStepNothing")}</p>
        ) : (
          <ol>
            <Step
              n={1}
              done={needsReprice === 0}
              label={t("homeStepReprice", { n: String(needsReprice) })}
              action={
                needsReprice > 0 ? (
                  <Button size="sm" variant="ghost" onClick={() => onNavigate("orders")}>
                    {t("homeOpenOrdersTab")} <ArrowRight className="h-3 w-3" />
                  </Button>
                ) : undefined
              }
            />
            <Step
              n={2}
              done={needsCancel === 0}
              label={t("homeStepCancel", { n: String(needsCancel) })}
              action={
                needsCancel > 0 ? (
                  <Button size="sm" variant="ghost" onClick={() => onNavigate("orders")}>
                    {t("homeOpenOrdersTab")} <ArrowRight className="h-3 w-3" />
                  </Button>
                ) : undefined
              }
            />
            <Step
              n={3}
              done={buySheet.length === 0}
              label={t("homeStepAdd", { n: String(buySheet.length) })}
            />
            <Step n={4} done={sellSheet.length === 0} label={t("homeStepList")} />
          </ol>
        )}
      </section>

      {/* Buy sheet — from the last scan, not a fresh one. */}
      <section className="mb-3 rounded-sm border border-eve-border bg-surface-1">
        <header className="flex items-baseline justify-between gap-3 border-b border-eve-border px-3 py-2">
          <h2 className="font-ui text-t-title font-semibold text-fg">{t("homeBuySheet")}</h2>
          {scan && (
            <span className="font-ui text-t-caption text-fg-tertiary">
              {t("homeScanAge", { age: relativeAge(scan.record.timestamp) })}
            </span>
          )}
        </header>

        {buySheet.length === 0 ? (
          <div className="px-3 py-6 text-center">
            <p className="font-ui text-t-body text-fg-secondary">{t("homeNoScan")}</p>
            <p className="mt-1 font-ui text-t-caption text-fg-tertiary">{t("homeNoScanHint")}</p>
            <Button className="mt-3" size="sm" onClick={() => onNavigate("station")}>
              {t("homeOpenStationTab")} <ArrowRight className="h-3 w-3" />
            </Button>
          </div>
        ) : (
          <table className="w-full">
            <thead>
              <tr className="border-b border-eve-border text-left font-ui text-t-caption uppercase tracking-wide text-fg-tertiary">
                <th className="px-3 py-1.5 font-medium">Item</th>
                <th className="px-3 py-1.5 text-right font-medium">Buy price</th>
                <th className="px-3 py-1.5 text-right font-medium">Qty/day</th>
                <th className="px-3 py-1.5 text-right font-medium">Capital</th>
                <th className="px-3 py-1.5 text-right font-medium">Daily profit</th>
              </tr>
            </thead>
            <tbody>
              {buySheet.map((row) => {
                // SuggestedBid is the patient-buy price when the scan
                // produced one; otherwise fall back to the live buy price.
                const price = row.SuggestedBid && row.SuggestedBid > 0 ? row.SuggestedBid : row.BuyPrice;
                const daily = row.DailyProfit ?? row.RealizableDailyProfit ?? 0;
                return (
                  <tr key={`${row.TypeID}-${row.StationID}`} className="border-b border-eve-border/40">
                    <td className="px-3 py-1.5">
                      <div className="flex min-w-0 items-center gap-1.5">
                        <TypeIcon typeId={row.TypeID} categoryId={row.CategoryID} />
                        <span className="truncate font-ui text-t-body text-fg">{row.TypeName}</span>
                        {row.IsHighRiskFlag && <Badge tone="warn">Risk</Badge>}
                      </div>
                    </td>
                    <td className="px-3 py-1.5 text-right">
                      <span className="inline-flex items-center gap-1.5">
                        <span className="font-num tnum text-t-cell text-fg-secondary">
                          {formatISK(price)}
                        </span>
                        <CopyPrice value={price} label={t("homeCopyPrice")} />
                      </span>
                    </td>
                    <td className="px-3 py-1.5 text-right font-num tnum text-t-cell text-fg-secondary">
                      {formatNumber(Math.round(row.BuyUnitsPerDay ?? 0))}
                    </td>
                    <td className="px-3 py-1.5 text-right font-num tnum text-t-cell text-fg-secondary">
                      {formatISK(row.CapitalRequired)}
                    </td>
                    <td className="px-3 py-1.5 text-right font-num tnum text-t-cell font-semibold text-profit">
                      {formatISK(daily)}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </section>

      {/* Sell sheet — outbid sell orders and the price to paste. */}
      {isLoggedIn && sellSheet.length > 0 && (
        <section className="rounded-sm border border-eve-border bg-surface-1">
          <header className="border-b border-eve-border px-3 py-2">
            <h2 className="font-ui text-t-title font-semibold text-fg">{t("homeSellSheet")}</h2>
          </header>
          <table className="w-full">
            <thead>
              <tr className="border-b border-eve-border text-left font-ui text-t-caption uppercase tracking-wide text-fg-tertiary">
                <th className="px-3 py-1.5 font-medium">Item</th>
                <th className="px-3 py-1.5 text-right font-medium">Your price</th>
                <th className="px-3 py-1.5 text-right font-medium">Suggested</th>
                <th className="px-3 py-1.5 text-right font-medium">Qty</th>
                <th className="px-3 py-1.5 text-right font-medium">Position</th>
              </tr>
            </thead>
            <tbody>
              {sellSheet.map((o: OrderDeskOrder) => (
                <tr key={o.order_id} className="border-b border-eve-border/40">
                  <td className="px-3 py-1.5">
                    <div className="flex min-w-0 items-center gap-1.5">
                      <TypeIcon typeId={o.type_id} />
                      <span className="truncate font-ui text-t-body text-fg">{o.type_name}</span>
                    </div>
                  </td>
                  <td className="px-3 py-1.5 text-right font-num tnum text-t-cell text-fg-tertiary">
                    {formatISK(o.price)}
                  </td>
                  <td className="px-3 py-1.5 text-right">
                    <span className="inline-flex items-center gap-1.5">
                      <span className="font-num tnum text-t-cell font-semibold text-fg">
                        {formatISK(o.suggested_price)}
                      </span>
                      <CopyPrice value={o.suggested_price} label={t("homeCopyPrice")} />
                    </span>
                  </td>
                  <td className="px-3 py-1.5 text-right font-num tnum text-t-cell text-fg-secondary">
                    {formatNumber(o.volume_remain)}
                  </td>
                  <td className="px-3 py-1.5 text-right font-num tnum text-t-cell text-fg-secondary">
                    {o.position > 0 ? `#${o.position} / ${o.total_orders}` : "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}
    </div>
  );
}
