import { useState } from "react";
import { ArrowRight, Check, MapPin, SkipForward } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/CopyButton";
import { OpenMarketButton } from "@/components/ui/OpenMarketButton";
import { TypeIcon } from "@/components/ui/TypeIcon";
import { formatISK, formatNumber } from "@/lib/format";
import { useI18n } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import type { TodayAction } from "@/lib/types";
import { RiskChip } from "./RiskChip";
import { TODAY_KIND_LABEL, todayMinutes, todayPasteDisplay, todayPasteText } from "./todayFormat";

/**
 * The whole queue at once, for surveying rather than working.
 *
 * Run mode is the default because it removes the choosing; this view exists
 * for the times you want to see the shape of the session. It still carries the
 * one-click affordances per row — the market window and the price — so nothing
 * is only reachable from Run mode.
 *
 * The cut line is the point of the ordering. Everything above it fits the time
 * budget; everything below is collapsed behind a count and a total, because
 * "there is more, and here is what it is worth" is a decision, whereas forty
 * more rows is a wall.
 */
export function ActionList({
  actions,
  budgetSeconds,
  onDone,
  onSkip,
  onDetails,
}: {
  actions: TodayAction[];
  budgetSeconds: number;
  onDone: (action: TodayAction) => void;
  onSkip: (action: TodayAction) => void;
  onDetails: (action: TodayAction) => void;
}) {
  const { t } = useI18n();
  const [showBeyond, setShowBeyond] = useState(false);

  const inBudget = actions.filter((a) => a.in_budget);
  const beyond = actions.filter((a) => !a.in_budget);
  const beyondValue = beyond.reduce((sum, a) => sum + a.downside_isk_7d, 0);

  return (
    <section className="rounded-sm border border-eve-border bg-surface-1">
      <ul>
        {inBudget.map((a) => (
          <ActionRow key={a.id} action={a} onDone={onDone} onSkip={onSkip} onDetails={onDetails} />
        ))}
      </ul>

      {beyond.length > 0 && (
        <>
          <div className="flex flex-wrap items-center justify-between gap-2 border-y border-eve-border bg-surface-2 px-3 py-1.5">
            <span className="font-ui text-t-caption text-fg-secondary">
              {t("todayCutLine", { mins: String(todayMinutes(budgetSeconds)) })}{" "}
              <span className="text-fg-tertiary">
                {t("todayBeyondCut", {
                  n: String(beyond.length),
                  isk: formatISK(beyondValue),
                })}
              </span>
            </span>
            <Button size="sm" variant="ghost" onClick={() => setShowBeyond((v) => !v)}>
              {showBeyond ? t("todayHideBeyond") : t("todayShowBeyond")}
            </Button>
          </div>
          {showBeyond && (
            <ul>
              {beyond.map((a) => (
                <ActionRow
                  key={a.id}
                  action={a}
                  dimmed
                  onDone={onDone}
                  onSkip={onSkip}
                  onDetails={onDetails}
                />
              ))}
            </ul>
          )}
        </>
      )}
    </section>
  );
}

function ActionRow({
  action,
  dimmed,
  onDone,
  onSkip,
  onDetails,
}: {
  action: TodayAction;
  dimmed?: boolean;
  onDone: (action: TodayAction) => void;
  onSkip: (action: TodayAction) => void;
  onDetails: (action: TodayAction) => void;
}) {
  const { t } = useI18n();
  const priceText = action.paste_price ? todayPasteText(action.paste_price) : "";
  const priceDisplay = action.paste_price ? todayPasteDisplay(action.paste_price) : "";

  return (
    <li
      className={cn(
        "flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-eve-border/40 px-3 py-2 last:border-b-0",
        dimmed && "opacity-60",
        action.done && "opacity-40",
      )}
    >
      <Badge tone="accent">{t(TODAY_KIND_LABEL[action.kind])}</Badge>

      <span className="flex min-w-0 flex-1 items-center gap-1.5">
        {action.type_id != null && (
          <TypeIcon typeId={action.type_id} categoryId={action.category_id} />
        )}
        <span className="truncate font-ui text-t-body text-fg">
          {action.type_name || action.headline}
        </span>
        {action.here && <MapPin className="h-3 w-3 shrink-0 text-profit" aria-hidden="true" />}
      </span>

      <RiskChip grade={action.grade} reliability={action.reliability} />

      {priceText ? (
        <span className="inline-flex items-center gap-1.5">
          <span className="font-num tnum text-t-cell text-fg">{priceDisplay}</span>
          {/* The exact digits EVE accepts, not the formatted display value. */}
          <CopyButton text={priceText} label={t("todayPasteThis")} />
        </span>
      ) : null}

      {action.quantity ? (
        <span className="inline-flex items-center gap-1.5">
          <span className="font-num tnum text-t-cell text-fg-secondary">
            ×{formatNumber(action.quantity)}
          </span>
          <CopyButton text={String(action.quantity)} label={t("todayCopyQty")} />
        </span>
      ) : null}

      {/* Ranked on the downside, so that is the figure the row leads with. */}
      <span
        className="w-24 shrink-0 text-right font-num tnum text-t-cell font-semibold text-profit"
        title={`${formatISK(action.expected_isk_7d)} ${t("todayExpected")}`}
      >
        {formatISK(action.downside_isk_7d)}
      </span>

      <span className="inline-flex shrink-0 items-center gap-0.5">
        {action.type_id != null && (
          <OpenMarketButton
            typeId={action.type_id}
            label={t("todayOpenAndCopy")}
            copyOnOpen={priceText ? { text: priceText } : undefined}
          />
        )}
        <Button size="sm" variant="ghost" onClick={() => onDone(action)} title={t("todayDone")}>
          <Check className="h-3 w-3" aria-hidden="true" />
        </Button>
        <Button size="sm" variant="ghost" onClick={() => onSkip(action)} title={t("todaySkip")}>
          <SkipForward className="h-3 w-3" aria-hidden="true" />
        </Button>
        <Button size="sm" variant="ghost" onClick={() => onDetails(action)} title={t("todayDetails")}>
          <ArrowRight className="h-3 w-3" aria-hidden="true" />
        </Button>
      </span>
    </li>
  );
}
