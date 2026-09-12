import { ArrowRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import { TypeIcon } from "@/components/ui/TypeIcon";
import { formatISK, formatNumber } from "@/lib/format";
import { useI18n } from "@/lib/i18n";
import type { TodayWaitingRow } from "@/lib/types";

/**
 * Stock parked behind a target price.
 *
 * A summary, not an editor. Today is a roll-up of the other tabs, and the
 * rules themselves are set on Assets → Positions — so every row here links
 * there rather than growing a second place to change them.
 *
 * It exists because silence is the wrong answer to "why is my Gnosis not in
 * the list". Holding something back on purpose should look like a decision
 * you made, not like the app forgetting about it.
 */
export function WaitingPanel({
  rows,
  onOpen,
}: {
  rows: TodayWaitingRow[];
  onOpen: (row: TodayWaitingRow) => void;
}) {
  const { t } = useI18n();
  if (rows.length === 0) return null;

  return (
    <section className="rounded-sm border border-eve-border bg-surface-1">
      <header className="flex flex-wrap items-baseline justify-between gap-2 border-b border-eve-border px-3 py-2">
        <h2 className="font-ui text-t-body font-medium text-fg">
          {t("todayWaitingTitle", { n: String(rows.length) })}
        </h2>
        <span className="font-ui text-t-caption text-fg-tertiary">{t("todayWaitingHint")}</span>
      </header>

      <ul>
        {rows.map((row) => (
          <li
            key={row.type_id}
            className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-eve-border/40 px-3 py-1.5 last:border-b-0"
          >
            <span className="flex min-w-0 flex-1 items-center gap-1.5">
              <TypeIcon typeId={row.type_id} />
              <span className="truncate font-ui text-t-body text-fg-secondary">{row.type_name}</span>
              <span className="font-num tnum text-t-caption text-fg-tertiary">
                ×{formatNumber(row.qty)}
              </span>
            </span>

            {/* Target, current, and how far between the two. The bar is the
                point: "78% of the way there" is the thing you want to know at
                a glance, and two ISK figures alone do not say it. */}
            <span className="flex items-center gap-2">
              <span className="font-num tnum text-t-cell text-fg-tertiary">
                {formatISK(row.market_price)}
              </span>
              <span className="h-1 w-16 overflow-hidden rounded-sm bg-surface-3">
                <span
                  className="block h-full bg-eve-accent"
                  style={{ width: `${Math.max(2, Math.min(100, row.target_progress_pct))}%` }}
                />
              </span>
              <span className="font-num tnum text-t-cell font-medium text-fg">
                {formatISK(row.target_price)}
              </span>
              {row.target_percentile ? (
                <span className="font-ui text-t-caption text-fg-tertiary">
                  p{Math.round(row.target_percentile)}
                </span>
              ) : null}
            </span>

            {row.upside_isk > 0 && (
              <span className="font-num tnum text-t-cell text-profit">
                {t("todayWaitingUpside", { isk: formatISK(row.upside_isk) })}
              </span>
            )}

            <Button size="sm" variant="ghost" onClick={() => onOpen(row)}>
              <ArrowRight className="h-3 w-3" aria-hidden="true" />
            </Button>
          </li>
        ))}
      </ul>
    </section>
  );
}
