import { ArrowRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { formatISK } from "@/lib/format";
import { useI18n } from "@/lib/i18n";
import type { TodayOption } from "@/lib/types";
import { TODAY_GRADE_LABEL, TODAY_GRADE_TONE, todayMinutes } from "./todayFormat";

/**
 * Where to put idle ISK, with every choice on one scale.
 *
 * Return per day is the comparison, because it is the only figure that makes
 * a station flip, a build and a colony commensurable: they need different
 * capital and pay out over different horizons, and ISK per day per ISK
 * invested normalises both away.
 *
 * The rate quoted is the downside one, and the grade sits next to it, so a
 * high-percentage option resting on unproven rows cannot out-argue a modest
 * one resting on proven ones just by being a bigger number.
 *
 * Cash is always listed, at zero. It is the baseline the others are measured
 * against, and leaving it out would make "do nothing" invisible.
 */
export function OptionsPanel({
  options,
  onNavigate,
}: {
  options: TodayOption[];
  onNavigate: (option: TodayOption) => void;
}) {
  const { t } = useI18n();
  if (options.length === 0) return null;

  return (
    <section className="rounded-sm border border-eve-border bg-surface-1 p-3">
      <header className="mb-1">
        <h2 className="font-ui text-t-title font-semibold text-fg">{t("todayOptionsTitle")}</h2>
        <p className="font-ui text-t-caption text-fg-tertiary">{t("todayOptionsHint")}</p>
      </header>

      <ul className="mt-2 grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
        {options.map((o) => (
          <li
            key={o.id}
            className="flex min-w-0 flex-col gap-1 rounded-sm border border-eve-border bg-surface-2 p-2"
          >
            <div className="flex items-start justify-between gap-2">
              <span className="min-w-0 font-ui text-t-body font-medium text-fg">{o.label}</span>
              <Badge tone={TODAY_GRADE_TONE[o.grade]}>{t(TODAY_GRADE_LABEL[o.grade])}</Badge>
            </div>

            <div className="font-num tnum text-t-title font-semibold text-fg">
              {o.return_pct_per_day > 0 ? `${o.return_pct_per_day.toFixed(2)}%` : "0%"}
              <span className="ml-1 font-ui text-t-caption font-normal text-fg-tertiary">
                {t("todayOptionPerDay")}
              </span>
            </div>

            <dl className="flex flex-wrap gap-x-3 gap-y-0.5 font-ui text-t-caption text-fg-tertiary">
              {o.capital_isk > 0 && (
                <span>
                  <dt className="inline">{t("todayOptionCapital")} </dt>
                  <dd className="inline font-num tnum text-fg-secondary">
                    {formatISK(o.capital_isk)}
                  </dd>
                </span>
              )}
              {o.downside_isk_per_day > 0 && (
                <span>
                  <dt className="inline">ISK </dt>
                  <dd className="inline font-num tnum text-profit">
                    {formatISK(o.downside_isk_per_day)}/d
                  </dd>
                </span>
              )}
              {o.setup_seconds > 0 && (
                <span>
                  <dt className="inline">{t("todayOptionSetup")} </dt>
                  <dd className="inline font-num tnum text-fg-secondary">
                    {todayMinutes(o.setup_seconds)}m
                  </dd>
                </span>
              )}
            </dl>

            {o.detail && (
              <p className="font-ui text-t-caption text-fg-tertiary">{o.detail}</p>
            )}

            <Button
              size="sm"
              variant="ghost"
              className="mt-auto self-start"
              onClick={() => onNavigate(o)}
            >
              {t("todayDetails")}
              <ArrowRight className="h-3 w-3" aria-hidden="true" />
            </Button>
          </li>
        ))}
      </ul>
    </section>
  );
}
