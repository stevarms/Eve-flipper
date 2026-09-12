import { useCallback, useEffect, useRef, useState } from "react";
import { ArrowRight, Check, ExternalLink, MapPin, SkipForward } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { TypeIcon } from "@/components/ui/TypeIcon";
import { useEveUiActions } from "@/lib/eveUiActions";
import { formatISK, formatNumber } from "@/lib/format";
import { useI18n } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import type { TodayAction } from "@/lib/types";
import { RiskChip } from "./RiskChip";
import { TODAY_KIND_LABEL, todayMinutes, todayPasteText } from "./todayFormat";

/**
 * One action at a time, with the price already on the clipboard.
 *
 * This is the execution half of Today. The whole interaction is meant to be:
 *
 *   Enter  → the item's market window opens in EVE and the price is copied
 *   Ctrl+V → into EVE's price field
 *   Q      → the clipboard becomes the quantity
 *   Ctrl+V → into EVE's quantity field
 *   Space  → done, next action
 *
 * Nothing is typed and nothing is decided. The price is already snapped to
 * EVE's 4-significant-digit grid by the engine and the quantity is already
 * capped by market depth, wallet, the configured investment ceiling and the
 * user's own record, so there is no arithmetic left for the user to do.
 */

/** What the clipboard currently holds, so the panel can say so truthfully. */
type ClipboardHolds = "price" | "quantity" | null;

export interface RunPanelProps {
  action: TodayAction;
  index: number;
  total: number;
  secondsRemaining: number;
  onDone: (action: TodayAction) => void;
  onSkip: (action: TodayAction) => void;
  onDetails: (action: TodayAction) => void;
}

export function RunPanel({
  action,
  index,
  total,
  secondsRemaining,
  onDone,
  onSkip,
  onDetails,
}: RunPanelProps) {
  const { t } = useI18n();
  const { openMarket, setDestination } = useEveUiActions();
  const [holds, setHolds] = useState<ClipboardHolds>(null);
  // A ref, not state: an in-flight guard must not re-render and move the
  // button out from under the cursor mid-click.
  const busy = useRef(false);

  const priceText = action.paste_price ? todayPasteText(action.paste_price) : "";
  const qtyText = action.quantity ? String(action.quantity) : "";

  const copy = useCallback(async (text: string, holding: ClipboardHolds) => {
    if (!text) return false;
    try {
      await navigator.clipboard.writeText(text);
      setHolds(holding);
      return true;
    } catch {
      // Refused — an insecure context, or no user gesture yet on a cold
      // load. Say nothing rather than claim the clipboard holds a price it
      // does not; the button below stays the way to get it there.
      setHolds(null);
      return false;
    }
  }, []);

  // Pre-load the price the moment an action comes into focus, so the common
  // case costs no click at all. The Space or Enter that advanced the queue
  // counts as the user gesture the clipboard API requires; only the very
  // first action after a cold page load can be refused, and `holds` staying
  // null is what keeps the label honest when it is.
  useEffect(() => {
    setHolds(null);
    if (!priceText) return;
    void copy(priceText, "price");
  }, [action.id, priceText, copy]);

  const openAndCopy = useCallback(async () => {
    if (busy.current) return;
    busy.current = true;
    try {
      if (action.type_id) {
        // useEveUiActions owns the not-logged-in and ESI error toasts.
        const ok = await openMarket(action.type_id);
        if (!ok) return;
      }
      await copy(priceText, "price");
    } finally {
      busy.current = false;
    }
  }, [action.type_id, copy, openMarket, priceText]);

  const copyQty = useCallback(() => void copy(qtyText, "quantity"), [copy, qtyText]);

  // Bound on the container rather than globally so the keys only apply while
  // Run mode is on screen, and registered in KeyboardShortcutsHelp so they are
  // discoverable rather than folklore.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      if (
        target &&
        (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.isContentEditable)
      ) {
        return;
      }
      if (e.ctrlKey || e.metaKey || e.altKey) return;

      const code = e.code;
      if (code === "Enter") {
        e.preventDefault();
        void openAndCopy();
      } else if (code === "KeyQ") {
        e.preventDefault();
        copyQty();
      } else if (code === "Space") {
        e.preventDefault();
        onDone(action);
      } else if (code === "KeyS") {
        e.preventDefault();
        onSkip(action);
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [action, copyQty, onDone, onSkip, openAndCopy]);

  const minutesLeft = todayMinutes(secondsRemaining);

  return (
    <section className="rounded-sm border border-eve-border bg-surface-1">
      {/* Where you are in the session, and where you are in space. */}
      <header className="flex flex-wrap items-center justify-between gap-2 border-b border-eve-border px-3 py-2">
        <span className="font-ui text-t-caption uppercase tracking-wide text-fg-tertiary">
          {t("todayActionCounter", { n: String(index + 1), total: String(total) })}
          {minutesLeft > 0 && ` · ${t("todayBudget", { done: String(index), total: String(total), mins: String(minutesLeft) })}`}
        </span>
        {action.location_name && (
          <span className="flex items-center gap-1.5 font-ui text-t-caption">
            <MapPin className="h-3 w-3 text-fg-tertiary" aria-hidden="true" />
            <span className="text-fg-secondary">{action.location_name}</span>
            {action.here ? (
              <Badge tone="profit">{t("todayHere")}</Badge>
            ) : (
              // Somewhere else. One click sets the route rather than leaving
              // the user to work out where that is. The station id is the
              // right argument despite the helper's parameter name: ESI's
              // /ui/autopilot/waypoint takes a `destination_id` that accepts a
              // station, structure or system, and a station routes you to the
              // station rather than just to the system it sits in.
              action.location_id != null && (
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => void setDestination(action.location_id as number)}
                >
                  {t("todaySetDestination")}
                </Button>
              )
            )}
          </span>
        )}
      </header>

      <div className="px-3 py-3">
        {/* The verb, the item, and how far it can be trusted. */}
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <Badge tone="accent">{t(TODAY_KIND_LABEL[action.kind])}</Badge>
          {action.type_id != null && (
            <TypeIcon typeId={action.type_id} categoryId={action.category_id} />
          )}
          <h2 className="min-w-0 flex-1 font-ui text-t-emphasis font-semibold text-fg">
            {action.type_name || action.headline}
          </h2>
          <RiskChip grade={action.grade} reliability={action.reliability} />
        </div>

        {/* The numbers to paste. Deliberately a short definition list rather
            than a table: three values, read top to bottom, once. */}
        <dl className="mb-3 grid grid-cols-[auto_1fr] items-baseline gap-x-4 gap-y-1">
          {action.current_price ? (
            <>
              <dt className="font-ui text-t-caption text-fg-tertiary">{t("todayYourPrice")}</dt>
              <dd className="font-num tnum text-t-cell text-fg-tertiary line-through">
                {formatISK(action.current_price)}
              </dd>
            </>
          ) : null}

          {priceText ? (
            <>
              <dt className="font-ui text-t-caption text-fg-tertiary">{t("todayPasteThis")}</dt>
              <dd className="flex flex-wrap items-baseline gap-2">
                <span className="font-num tnum text-t-title font-semibold text-fg">{priceText}</span>
                <span
                  className={cn(
                    "font-ui text-t-caption",
                    holds === "price" ? "text-profit" : "text-fg-tertiary",
                  )}
                >
                  {holds === "price" ? `← ${t("todayOnClipboard")}` : t("todayClickToCopy")}
                </span>
              </dd>
            </>
          ) : null}

          {action.quantity ? (
            <>
              <dt className="font-ui text-t-caption text-fg-tertiary">{t("todayQuantity")}</dt>
              <dd className="flex flex-wrap items-baseline gap-2">
                <span className="font-num tnum text-t-body font-semibold text-fg">
                  {formatNumber(action.quantity)}
                </span>
                {holds === "quantity" && (
                  <span className="font-ui text-t-caption text-profit">
                    ← {t("todayOnClipboard")}
                  </span>
                )}
                {action.reliability.caps?.length ? (
                  <span className="font-ui text-t-caption text-fg-tertiary">
                    {t("todayCappedBy", { caps: action.reliability.caps.join(", ") })}
                  </span>
                ) : null}
              </dd>
            </>
          ) : null}
        </dl>

        {/* Reward and risk, always both, on one line each. */}
        <p className="font-ui text-t-body">
          <span className="font-num tnum font-semibold text-profit">
            {formatISK(action.expected_isk_7d)}
          </span>
          <span className="text-fg-tertiary"> {t("todayExpected")} · </span>
          <span className="font-num tnum font-semibold text-fg">
            {formatISK(action.downside_isk_7d)}
          </span>
          <span className="text-fg-tertiary"> {t("todayRealisticCase")} · </span>
          <span className={cn("font-num tnum", action.at_risk_isk > 0 ? "text-warn" : "text-fg-tertiary")}>
            {action.at_risk_isk > 0
              ? `${formatISK(action.at_risk_isk)} ${t("todayAtRisk")}`
              : t("todayNothingAtRisk")}
          </span>
        </p>

        {action.reliability.evidence && (
          <p className="mt-1 font-ui text-t-caption text-fg-secondary">
            {action.reliability.evidence}
          </p>
        )}
        {action.why && <p className="mt-1 font-ui text-t-caption text-fg-tertiary">{action.why}</p>}
        {action.timing_note && (
          <p className="mt-1 font-ui text-t-caption text-info">{action.timing_note}</p>
        )}
      </div>

      {/* The controls. One primary action, then advance. */}
      <footer className="flex flex-wrap items-center gap-2 border-t border-eve-border px-3 py-2">
        {action.type_id != null ? (
          <Button variant="primary" onClick={() => void openAndCopy()}>
            <ExternalLink className="h-3.5 w-3.5" aria-hidden="true" />
            {t("todayOpenAndCopy")}
            <Kbd>Enter</Kbd>
          </Button>
        ) : (
          action.location_id != null && (
            <Button variant="primary" onClick={() => void setDestination(action.location_id as number)}>
              <MapPin className="h-3.5 w-3.5" aria-hidden="true" />
              {t("todaySetDestination")}
            </Button>
          )
        )}

        {qtyText && (
          <Button variant="secondary" onClick={copyQty}>
            {t("todayCopyQty")}
            <Kbd>Q</Kbd>
          </Button>
        )}

        <Button variant="secondary" onClick={() => onDone(action)}>
          <Check className="h-3.5 w-3.5" aria-hidden="true" />
          {t("todayDone")}
          <Kbd>Space</Kbd>
        </Button>

        <Button variant="ghost" onClick={() => onSkip(action)}>
          <SkipForward className="h-3.5 w-3.5" aria-hidden="true" />
          {t("todaySkip")}
          <Kbd>S</Kbd>
        </Button>

        <Button variant="ghost" className="ml-auto" onClick={() => onDetails(action)}>
          {t("todayDetails")}
          <ArrowRight className="h-3 w-3" aria-hidden="true" />
        </Button>
      </footer>
    </section>
  );
}

/** The key that triggers the button it sits inside. */
function Kbd({ children }: { children: React.ReactNode }) {
  return (
    <kbd className="ml-1 rounded-sm border border-current/30 px-1 font-num text-t-caption opacity-70">
      {children}
    </kbd>
  );
}
