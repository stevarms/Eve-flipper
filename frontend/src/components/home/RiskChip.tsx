import { Badge } from "@/components/ui/badge";
import { Tooltip } from "@/components/Tooltip";
import { formatISK } from "@/lib/format";
import { useI18n } from "@/lib/i18n";
import type { TodayGrade, TodayReliability } from "@/lib/types";
import { TODAY_GRADE_LABEL, TODAY_GRADE_TONE, TODAY_GRADE_WHY } from "./todayFormat";

/**
 * The grade, with its evidence one hover away.
 *
 * Every action carries this. A queue that ranks on a risk model the user
 * cannot audit is one they stop believing the first time a row disappoints, so
 * the chip is never just a colour: the tooltip says how many real sales back
 * it, what they actually earned, and how far past results have fallen short of
 * the plan.
 */
export function RiskChip({
  grade,
  reliability,
  className,
}: {
  grade: TodayGrade;
  reliability: TodayReliability;
  className?: string;
}) {
  const { t } = useI18n();

  const lines: string[] = [t(TODAY_GRADE_WHY[grade])];
  if (reliability.evidence) lines.push(reliability.evidence);
  if (reliability.sample_trades > 0) {
    lines.push(
      `${t("todaySampleTrades", { n: String(reliability.sample_trades) })} · ${formatISK(
        reliability.realized_isk,
      )}`,
    );
  }
  if (reliability.reality_ratio && reliability.reality_ratio > 0) {
    lines.push(t("todayRealityRatio", { ratio: reliability.reality_ratio.toFixed(2) }));
  }
  for (const blocker of reliability.blockers ?? []) lines.push(`• ${blocker}`);
  if (reliability.caps?.length) {
    lines.push(t("todayCappedBy", { caps: reliability.caps.join(", ") }));
  }

  return (
    <Tooltip
      maxWidth="320px"
      content={
        // One node per line: Tooltip renders its content as HTML, so a
        // newline-joined string would collapse into one run of text.
        <span className="flex flex-col gap-1 text-left">
          {lines.map((line, i) => (
            <span key={i}>{line}</span>
          ))}
        </span>
      }
    >
      <Badge tone={TODAY_GRADE_TONE[grade]} className={className}>
        {t(TODAY_GRADE_LABEL[grade])}
      </Badge>
    </Tooltip>
  );
}
