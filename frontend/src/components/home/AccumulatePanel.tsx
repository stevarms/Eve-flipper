import { ArrowRight } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { TypeIcon } from "@/components/ui/TypeIcon";
import { formatISK } from "@/lib/format";
import { useI18n } from "@/lib/i18n";
import type { AccumulateRow } from "@/lib/types";
import { TODAY_GRADE_LABEL, TODAY_GRADE_TONE } from "./todayFormat";

/** How many make it onto Today. This is a roll-up, not the tab. */
const TOP_N = 3;

/**
 * The top few items trading near the bottom of their own year.
 *
 * Deliberately outside the action queue. Those actions are ranked on seven-day
 * ISK per minute of attention, and a hold that pays out over a month has no
 * honest place on that scale — amortising it would bury it, and ranking it at
 * full value would float it above work that actually pays this week.
 *
 * So it sits on its own with its own figures, and links to the Accumulate tab
 * for the rest. Today summarises the other tabs; it does not replace them.
 */
export function AccumulatePanel({
  rows,
  onOpenTab,
}: {
  rows: AccumulateRow[];
  onOpenTab: () => void;
}) {
  const { t } = useI18n();
  if (rows.length === 0) return null;

  const top = rows.slice(0, TOP_N);

  return (
    <section className="rounded-sm border border-eve-border bg-surface-1">
      <header className="flex flex-wrap items-baseline justify-between gap-2 border-b border-eve-border px-3 py-2">
        <h2 className="font-ui text-t-body font-medium text-fg">
          {t("todayAccumTitle", { n: String(rows.length) })}
        </h2>
        <span className="truncate font-ui text-t-caption text-fg-tertiary">
          {t("todayAccumHint")}
        </span>
      </header>

      <ul>
        {top.map((row) => (
          <li
            key={row.type_id}
            className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-eve-border/40 px-3 py-1.5 last:border-b-0"
          >
            <span className="flex min-w-0 flex-1 items-center gap-1.5">
              <TypeIcon typeId={row.type_id} categoryId={row.category_id} />
              <span className="truncate font-ui text-t-body text-fg-secondary">{row.type_name}</span>
              <Badge tone={TODAY_GRADE_TONE[row.grade]}>{t(TODAY_GRADE_LABEL[row.grade])}</Badge>
            </span>

            {/* Cheapness first, because that is the finding. */}
            <span className="font-num tnum text-t-cell font-semibold text-profit">
              −{row.discount_pct.toFixed(0)}%
            </span>
            <span className="font-num tnum text-t-caption text-fg-tertiary">
              p{row.current_percentile.toFixed(0)} of its year
            </span>

            {/* Then the cost and the wait, so it is never mistaken for a flip. */}
            <span className="font-num tnum text-t-caption text-fg-tertiary">
              {formatISK(row.capital_isk)} · ~{row.recovery_days.toFixed(0)}d
            </span>
            <span className="font-num tnum text-t-cell text-profit">
              +{formatISK(row.expected_isk)}
            </span>
          </li>
        ))}
      </ul>

      <footer className="border-t border-eve-border px-3 py-1.5">
        <Button size="sm" variant="ghost" onClick={onOpenTab}>
          {t("tabAccumulate")}
          <ArrowRight className="h-3 w-3" aria-hidden="true" />
        </Button>
      </footer>
    </section>
  );
}
