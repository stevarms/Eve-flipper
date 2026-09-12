import { useState } from "react";
import { ArrowRight, ChevronDown, ChevronRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import { TypeIcon } from "@/components/ui/TypeIcon";
import { formatISK } from "@/lib/format";
import { useI18n } from "@/lib/i18n";
import type { TodayAction } from "@/lib/types";
import { RiskChip } from "./RiskChip";
import { TODAY_KIND_LABEL } from "./todayFormat";

/**
 * What the risk model held back, and why.
 *
 * Collapsed by default but never absent. A filter the user cannot inspect is
 * indistinguishable from a bug, and the first time the queue quietly drops
 * something they expected to see, an invisible filter costs more trust than
 * the bad recommendation it prevented.
 *
 * Each row leads with its blocker rather than its ISK, because the number is
 * exactly what should not be persuasive here.
 */
export function NotAdvisedPanel({
  actions,
  onDetails,
}: {
  actions: TodayAction[];
  onDetails: (action: TodayAction) => void;
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);

  if (actions.length === 0) return null;

  return (
    <section className="rounded-sm border border-eve-border bg-surface-1">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-surface-2"
      >
        {open ? (
          <ChevronDown className="h-3.5 w-3.5 text-fg-tertiary" aria-hidden="true" />
        ) : (
          <ChevronRight className="h-3.5 w-3.5 text-fg-tertiary" aria-hidden="true" />
        )}
        <span className="font-ui text-t-body font-medium text-fg">
          {t("todayNotAdvisedTitle", { n: String(actions.length) })}
        </span>
        <span className="truncate font-ui text-t-caption text-fg-tertiary">
          {t("todayNotAdvisedHint")}
        </span>
      </button>

      {open && (
        <ul className="border-t border-eve-border">
          {actions.map((a) => (
            <li
              key={a.id}
              className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-eve-border/40 px-3 py-2 last:border-b-0"
            >
              <span className="font-ui text-t-caption uppercase tracking-wide text-fg-tertiary">
                {t(TODAY_KIND_LABEL[a.kind])}
              </span>
              <span className="flex min-w-0 items-center gap-1.5">
                {a.type_id != null && <TypeIcon typeId={a.type_id} categoryId={a.category_id} />}
                <span className="truncate font-ui text-t-body text-fg-secondary">
                  {a.type_name || a.headline}
                </span>
              </span>
              <RiskChip grade={a.grade} reliability={a.reliability} />

              {/* The reason, not the reward. */}
              <span className="min-w-0 flex-1 font-ui text-t-caption text-fg-tertiary">
                {a.reliability.blockers?.[0] ?? a.reliability.evidence}
              </span>

              <span
                className="shrink-0 font-num tnum text-t-caption text-fg-tertiary"
                title={t("todayExpected")}
              >
                {formatISK(a.expected_isk_7d)}
              </span>

              <Button size="sm" variant="ghost" onClick={() => onDetails(a)}>
                <ArrowRight className="h-3 w-3" aria-hidden="true" />
              </Button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
