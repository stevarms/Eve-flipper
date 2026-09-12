import { useCallback, useEffect, useState } from "react";
import { ChevronDown, ChevronRight, Loader2, RefreshCw } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CopyPrice } from "@/components/ui/CopyPrice";
import { OpenMarketButton } from "@/components/ui/OpenMarketButton";
import { TypeIcon } from "@/components/ui/TypeIcon";
import { EmptyState } from "@/components/EmptyState";
import { useGlobalToast } from "@/components/Toast";
import { getAccumulateResult, runAccumulateScan } from "@/lib/api";
import { formatISK, formatNumber } from "@/lib/format";
import { useI18n } from "@/lib/i18n";
import { priceStep } from "@/lib/pricing";
import { cn } from "@/lib/utils";
import type {
  AccumulateResult,
  AccumulateRow,
  AccumulateSummary,
  TodayGrade,
} from "@/lib/types";
import { todayRelativeAge } from "./home/todayFormat";

/**
 * Trade → Accumulate. Items trading near the bottom of their own year.
 *
 * The inverse of every other scan here: Station and Regional look at a spread
 * right now, this looks at one price across a year. That makes it a different
 * axis rather than a duplicate — nothing else in the app asks "is this cheap
 * for *this item*".
 *
 * The screen leads with what was refused, not just what passed, because the
 * naive version of this idea is dangerous: "cheapest all year" describes a
 * bargain and a dying item identically. A short list is the normal outcome and
 * has to look like the filters working rather than a broken scan.
 */

const GRADE_TONE: Record<TodayGrade, "profit" | "info" | "neutral" | "loss"> = {
  proven: "profit",
  likely: "info",
  unproven: "neutral",
  avoid: "loss",
};

export function AccumulateTab() {
  const { t } = useI18n();
  const { addToast } = useGlobalToast();

  const [result, setResult] = useState<AccumulateResult | null>(null);
  const [generatedAt, setGeneratedAt] = useState<string | undefined>();
  const [stale, setStale] = useState(false);
  const [loading, setLoading] = useState(true);
  const [running, setRunning] = useState(false);
  const [progress, setProgress] = useState("");
  const [showRejected, setShowRejected] = useState(false);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const env = await getAccumulateResult();
        if (cancelled) return;
        setResult(env.result);
        setGeneratedAt(env.generated_at);
        setStale(env.stale);
      } catch {
        // A missing sweep is the normal cold state, not an error worth a toast.
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const run = useCallback(async () => {
    setRunning(true);
    setProgress("");
    try {
      const res = await runAccumulateScan({}, setProgress);
      setResult(res);
      setGeneratedAt(res.summary.generated_at);
      setStale(false);
    } catch (err) {
      addToast(t("accumFailed", { error: err instanceof Error ? err.message : String(err) }), "error", 5000);
    } finally {
      setRunning(false);
      setProgress("");
    }
  }, [addToast, t]);

  if (loading) {
    return (
      <div className="flex-1 min-h-0 overflow-y-auto eve-scrollbar">
        <EmptyState reason="loading" />
      </div>
    );
  }

  const s = result?.summary;

  return (
    <div className="flex-1 min-h-0 overflow-y-auto eve-scrollbar p-3">
      <header className="mb-3 flex flex-wrap items-center gap-x-3 gap-y-2">
        <div className="min-w-0">
          <h1 className="font-ui text-t-title font-semibold text-fg">{t("accumTitle")}</h1>
          <p className="font-ui text-t-caption text-fg-tertiary">{t("accumHint")}</p>
        </div>

        {generatedAt && (
          <span className="font-ui text-t-caption text-fg-tertiary">
            {t("accumSweepAge", { age: todayRelativeAge(generatedAt) })}
          </span>
        )}
        {s && (
          <span className="font-ui text-t-caption text-fg-tertiary">
            {t("accumExamined", { n: formatNumber(s.examined) })} ·{" "}
            {t("accumAccepted", { n: formatNumber(s.accepted) })}
          </span>
        )}

        <Button className="ml-auto" variant="secondary" size="sm" onClick={() => void run()} disabled={running}>
          {running ? (
            <Loader2 className="h-3 w-3 animate-spin" aria-hidden="true" />
          ) : (
            <RefreshCw className="h-3 w-3" aria-hidden="true" />
          )}
          {running ? t("accumRunning") : t("accumRun")}
        </Button>

        {running && progress && (
          <p className="w-full font-ui text-t-caption text-fg-tertiary" role="status">
            {progress}
          </p>
        )}
        {!running && stale && generatedAt && (
          <p className="w-full font-ui text-t-caption text-warn-dim">
            {t("accumStale", { age: todayRelativeAge(generatedAt) })}
          </p>
        )}
      </header>

      {!result ? (
        <section className="rounded-sm border border-eve-border bg-surface-1 px-3 py-8 text-center">
          <p className="font-ui text-t-body text-fg-secondary">{t("accumNoScan")}</p>
          <p className="mx-auto mt-1 max-w-xl font-ui text-t-caption text-fg-tertiary">
            {t("accumNoScanHint")}
          </p>
          <Button className="mt-3" onClick={() => void run()} disabled={running}>
            {t("accumRun")}
          </Button>
        </section>
      ) : (
        <div className="flex flex-col gap-3">
          {result.warnings?.map((warning) => (
            <p key={warning} className="font-ui text-t-caption text-warn-dim">
              {warning}
            </p>
          ))}

          {result.rows.length > 0 && s && (
            <p className="font-ui text-t-body text-fg-secondary">
              {t("accumTotals", {
                capital: formatISK(s.total_capital_isk),
                expected: formatISK(s.total_expected_isk),
              })}
            </p>
          )}

          {result.rows.length > 0 && (
            <section className="overflow-x-auto rounded-sm border border-eve-border bg-surface-1">
              <table className="w-full">
                <thead>
                  <tr className="border-b border-eve-border text-left font-ui text-t-caption uppercase tracking-wide text-fg-tertiary">
                    <th className="px-2 py-1.5 font-medium">{t("accumColItem")}</th>
                    <th className="px-2 py-1.5 text-right font-medium">{t("accumColNow")}</th>
                    <th className="px-2 py-1.5 text-right font-medium">{t("accumColYear")}</th>
                    <th className="px-2 py-1.5 text-right font-medium">{t("accumColDiscount")}</th>
                    <th className="px-2 py-1.5 text-right font-medium">{t("accumColTarget")}</th>
                    <th className="px-2 py-1.5 text-right font-medium">{t("accumColUpside")}</th>
                    <th className="px-2 py-1.5 text-right font-medium">{t("accumColRecovery")}</th>
                    <th className="px-2 py-1.5 text-right font-medium">{t("accumColLiquidity")}</th>
                    <th className="px-2 py-1.5 text-right font-medium">{t("accumColSize")}</th>
                    <th className="px-2 py-1.5" />
                  </tr>
                </thead>
                <tbody>
                  {result.rows.map((row) => (
                    <AccumRow key={row.type_id} row={row} />
                  ))}
                </tbody>
              </table>
            </section>
          )}

          {/* What was refused. Deliberately prominent: the gates doing their
              job is the feature, and an empty accepted list needs context. */}
          {s && (
            <section className="rounded-sm border border-eve-border bg-surface-1">
              <button
                type="button"
                onClick={() => setShowRejected((v) => !v)}
                aria-expanded={showRejected}
                className="flex w-full flex-wrap items-center gap-2 px-3 py-2 text-left hover:bg-surface-2"
              >
                {showRejected ? (
                  <ChevronDown className="h-3.5 w-3.5 text-fg-tertiary" aria-hidden="true" />
                ) : (
                  <ChevronRight className="h-3.5 w-3.5 text-fg-tertiary" aria-hidden="true" />
                )}
                <span className="font-ui text-t-body font-medium text-fg">
                  {t("accumRejectedTitle", { n: formatNumber(rejectedTotal(s)) })}
                </span>
                {/* Each gate separately: a decline and a missing track record are
                    opposite findings, and one combined figure hid which filter
                    was actually binding. */}
                <span className="font-ui text-t-caption text-fg-tertiary">
                  {t("accumRejectThin", { n: formatNumber(s.rejected_thin) })} ·{" "}
                  {t("accumRejectPrice", { n: formatNumber(s.rejected_not_cheap) })} ·{" "}
                  {t("accumRejectUpside", { n: formatNumber(s.rejected_thin_upside) })} ·{" "}
                  {t("accumRejectTrend", { n: formatNumber(s.rejected_declining) })} ·{" "}
                  {t("accumRejectNoRecord", { n: formatNumber(s.rejected_no_record) })} ·{" "}
                  {t("accumRejectNoData", { n: formatNumber(s.rejected_no_data) })}
                </span>
              </button>

              {showRejected && (
                <div className="border-t border-eve-border">
                  <p className="px-3 py-1.5 font-ui text-t-caption text-fg-tertiary">
                    {t("accumRejectedHint")}
                  </p>
                  <ul>
                    {(result.rejected ?? []).map((row) => (
                      <li
                        key={row.type_id}
                        className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-eve-border/40 px-3 py-1.5"
                      >
                        <span className="flex min-w-0 items-center gap-1.5">
                          <TypeIcon typeId={row.type_id} categoryId={row.category_id} />
                          <span className="truncate font-ui text-t-body text-fg-secondary">
                            {row.type_name}
                          </span>
                        </span>
                        <span className="font-num tnum text-t-caption text-fg-tertiary">
                          −{row.discount_pct.toFixed(0)}%
                        </span>
                        <span className="min-w-0 flex-1 font-ui text-t-caption text-fg-tertiary">
                          {row.blockers?.[0] ?? row.recovery_reason}
                        </span>
                      </li>
                    ))}
                  </ul>
                </div>
              )}
            </section>
          )}
        </div>
      )}
    </div>
  );
}

/** Every gate's count. Written once so adding a gate cannot leave the header
 *  quietly under-reporting. */
function rejectedTotal(s: AccumulateSummary): number {
  return (
    s.rejected_thin +
    s.rejected_not_cheap +
    s.rejected_thin_upside +
    s.rejected_declining +
    s.rejected_no_record +
    s.rejected_no_data +
    s.rejected_suspect
  );
}

function AccumRow({ row }: { row: AccumulateRow }) {
  const { t } = useI18n();
  return (
    <tr className="border-b border-eve-border/40">
      <td className="px-2 py-1.5">
        <span className="flex min-w-0 items-center gap-1.5">
          <TypeIcon typeId={row.type_id} categoryId={row.category_id} />
          <span className="truncate font-ui text-t-body text-fg">{row.type_name}</span>
          <Badge tone={GRADE_TONE[row.grade]}>{row.grade}</Badge>
        </span>
        <span className="mt-0.5 block font-ui text-t-caption text-fg-tertiary">{row.why}</span>
      </td>

      <td className="px-2 py-1.5 text-right">
        <span className="inline-flex items-center gap-1.5">
          <span className="font-num tnum text-t-cell text-fg">{formatISK(row.best_sell)}</span>
          {/* The exact digits EVE accepts, for the buy order you would place. */}
          <CopyPrice value={row.best_sell} step={priceStep(row.best_sell)} label={t("copyPrice")} />
        </span>
      </td>

      {/* Where it sits in its own year: the range, and the position in it. */}
      <td className="px-2 py-1.5 text-right font-num tnum text-t-caption text-fg-tertiary">
        {formatISK(row.year_low)}–{formatISK(row.year_high)}
        <span className="ml-1 text-fg-secondary">p{row.current_percentile.toFixed(0)}</span>
      </td>

      <td className="px-2 py-1.5 text-right font-num tnum text-t-cell font-semibold text-profit">
        {row.discount_pct.toFixed(0)}%
      </td>

      <td className="px-2 py-1.5 text-right font-num tnum text-t-cell text-fg-secondary">
        {formatISK(row.target_price)}
      </td>

      <td className="px-2 py-1.5 text-right">
        <span className="font-num tnum text-t-cell font-semibold text-profit">
          {row.upside_pct.toFixed(0)}%
        </span>
        <span className="ml-1 font-num tnum text-t-caption text-fg-tertiary">
          {formatISK(row.expected_isk)}
        </span>
      </td>

      {/* The gate that separates a bargain from a dying item. */}
      <td className="px-2 py-1.5 text-right font-num tnum text-t-cell text-fg-secondary">
        {t("accumEpisodes", {
          n: String(row.recovery_episodes),
          days: row.recovery_days.toFixed(0),
        })}
      </td>

      <td className="px-2 py-1.5 text-right font-num tnum text-t-caption text-fg-tertiary">
        {t("accumPerDay", { units: formatNumber(Math.round(row.avg_daily_volume)) })}
        <span className="ml-1">{formatISK(row.avg_daily_isk)}</span>
      </td>

      <td className="px-2 py-1.5 text-right">
        <span className="font-num tnum text-t-cell text-fg">×{formatNumber(row.suggested_qty)}</span>
        <span className="ml-1 font-num tnum text-t-caption text-fg-tertiary">
          {formatISK(row.capital_isk)}
        </span>
        <span
          className={cn(
            "ml-1 font-num tnum text-t-caption",
            row.days_to_unwind > 14 ? "text-warn-dim" : "text-fg-tertiary",
          )}
        >
          {t("accumUnwind", { days: row.days_to_unwind.toFixed(1) })}
        </span>
      </td>

      <td className="px-2 py-1.5 text-right">
        <OpenMarketButton
          typeId={row.type_id}
          copyOnOpen={{ text: row.best_sell.toFixed(2) }}
          label={t("openMarket")}
        />
      </td>
    </tr>
  );
}
