import { formatISK } from "@/lib/format";
import { useI18n } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import type { TodayCapital, TodayPerformance } from "@/lib/types";

/**
 * Where the ISK is, and what it is earning.
 *
 * One stacked bar rather than four KPI tiles, because the question is a
 * proportion — how much of the book is working — and four separate figures
 * make the reader do that division in their head. The verdict underneath
 * turns it into ISK: idle capital has a daily cost, and stating it is the
 * whole argument for doing anything on this page.
 *
 * The bar is one hue at four weights, not four colours. Semantic colour has
 * to mean something (docs/UI_DESIGN_SYSTEM.md §2) and these segments are
 * parts of a single quantity — painting "in sell orders" green would claim
 * a profit that has not happened. Weight runs solid to faint as capital gets
 * further from being spendable, so the eye reads deployment, not verdict.
 *
 * Return per day is the compounding rate. It is the one number on the screen
 * worth maximising, which is why it gets the display size.
 */

interface Segment {
  key: string;
  label: string;
  value: number;
  className: string;
}

export function CapitalBar({
  capital,
  performance,
}: {
  capital: TodayCapital;
  performance: TodayPerformance;
}) {
  const { t } = useI18n();

  // One hue, four weights. See the note at the top of this file: these are
  // proportions of a single quantity, not four states with their own valence.
  const segments: Segment[] = [
    { key: "free", label: t("todayFree"), value: capital.wallet_isk, className: "bg-eve-accent" },
    { key: "buy", label: t("todayInBuyOrders"), value: capital.buy_order_isk, className: "bg-eve-accent/60" },
    { key: "inv", label: t("todayInventory"), value: capital.inventory_isk, className: "bg-eve-accent/35" },
    { key: "sell", label: t("todayInSellOrders"), value: capital.sell_order_isk, className: "bg-eve-accent/20" },
  ];
  const total = capital.total_isk;

  return (
    <section className="rounded-sm border border-eve-border bg-surface-1 p-3">
      <header className="mb-2 flex items-baseline justify-between gap-3">
        <h2 className="font-ui text-t-title font-semibold text-fg">{t("todayMoneyTitle")}</h2>
        <span className="font-num tnum text-t-body text-fg-secondary">{formatISK(total)}</span>
      </header>

      {total > 0 && (
        <>
          <div className="flex h-2 w-full overflow-hidden rounded-sm bg-surface-3">
            {segments.map((s) =>
              s.value > 0 ? (
                <div
                  key={s.key}
                  className={s.className}
                  style={{ width: `${(s.value / total) * 100}%` }}
                  title={`${s.label}: ${formatISK(s.value)}`}
                />
              ) : null,
            )}
          </div>

          <ul className="mt-2 flex flex-wrap gap-x-4 gap-y-1">
            {segments.map((s) => (
              <li key={s.key} className="flex items-center gap-1.5">
                <span className={cn("h-2 w-2 rounded-sm", s.className)} aria-hidden="true" />
                <span className="font-ui text-t-caption text-fg-tertiary">{s.label}</span>
                <span className="font-num tnum text-t-caption text-fg-secondary">
                  {formatISK(s.value)}
                </span>
              </li>
            ))}
          </ul>
        </>
      )}

      {capital.verdict && (
        <p
          className={cn(
            "mt-2 font-ui text-t-body",
            capital.idle_pct >= 25 ? "text-warn-dim" : "text-fg-secondary",
          )}
        >
          {capital.verdict}
        </p>
      )}

      <div className="mt-3 flex flex-wrap gap-x-6 gap-y-2 border-t border-eve-border pt-2">
        <Figure label={t("todayRealizedToday")} value={formatISK(performance.realized_today_isk)} />
        <Figure label={t("todayAvg7d")} value={`${formatISK(performance.avg_7d_isk_per_day)}/d`} />
        <Figure
          label={t("todayReturnPerDay")}
          value={
            // An unmeasured rate is not a rate of zero, and rendering "0.00%"
            // would read as a claim about performance rather than an absence
            // of data.
            performance.measured ? `${performance.return_pct_per_day.toFixed(2)}%` : "—"
          }
          hint={performance.measured ? undefined : t("todayReturnUnmeasured")}
          emphasis
        />
      </div>
    </section>
  );
}

function Figure({
  label,
  value,
  hint,
  emphasis,
}: {
  label: string;
  value: string;
  hint?: string;
  emphasis?: boolean;
}) {
  return (
    <div className="min-w-0">
      <div className="font-ui text-t-caption uppercase tracking-wide text-fg-tertiary">{label}</div>
      <div
        className={cn(
          "font-num tnum font-semibold text-fg",
          emphasis ? "text-t-display" : "text-t-title",
        )}
      >
        {value}
      </div>
      {hint && <div className="font-ui text-t-caption text-fg-tertiary">{hint}</div>}
    </div>
  );
}
