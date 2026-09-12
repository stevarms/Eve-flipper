import { Loader2, RefreshCw } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useI18n } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import { todayMinutes, todayRelativeAge } from "./todayFormat";

export type TodayView = "run" | "list";

/**
 * Plan age, the refresh, and the Run/List switch.
 *
 * The age is stated rather than implied. Every price on this page is
 * paste-ready, which makes a stale plan actively dangerous in a way a stale
 * dashboard is not — so when the server says the plan is past its threshold,
 * the header says so in words instead of greying something out.
 */
export function TodayHeader({
  generatedAt,
  stale,
  refreshing,
  progress,
  view,
  onViewChange,
  onRefresh,
  remainingSeconds,
  doneCount,
  totalCount,
}: {
  generatedAt?: string;
  stale: boolean;
  refreshing: boolean;
  progress: string;
  view: TodayView;
  onViewChange: (view: TodayView) => void;
  onRefresh: () => void;
  remainingSeconds: number;
  doneCount: number;
  totalCount: number;
}) {
  const { t } = useI18n();
  const age = generatedAt ? todayRelativeAge(generatedAt) : "";

  return (
    <header className="mb-3 flex flex-wrap items-center gap-x-3 gap-y-2">
      <h1 className="font-ui text-t-title font-semibold text-fg">{t("homeTitle")}</h1>

      {age && (
        <span className="font-ui text-t-caption text-fg-tertiary">
          {t("todayPlanAge", { age })}
        </span>
      )}

      {totalCount > 0 && (
        <Badge tone="info">
          {t("todayBudget", {
            done: String(doneCount),
            total: String(totalCount),
            mins: String(todayMinutes(remainingSeconds)),
          })}
        </Badge>
      )}

      <div className="ml-auto flex items-center gap-2">
        {/* Two views on one control. Run is the default and the point; List
            is for surveying the session rather than working it. */}
        <div className="flex overflow-hidden rounded-sm border border-eve-border">
          {(["run", "list"] as const).map((v) => (
            <button
              key={v}
              type="button"
              onClick={() => onViewChange(v)}
              aria-pressed={view === v}
              className={cn(
                "px-2 py-1 font-ui text-t-caption",
                view === v
                  ? "bg-eve-accent text-eve-dark"
                  : "bg-surface-2 text-fg-secondary hover:bg-surface-3",
              )}
            >
              {v === "run" ? t("todayRun") : t("todayList")}
            </button>
          ))}
        </div>

        <Button variant="secondary" size="sm" onClick={onRefresh} disabled={refreshing}>
          {refreshing ? (
            <Loader2 className="h-3 w-3 animate-spin" aria-hidden="true" />
          ) : (
            <RefreshCw className="h-3 w-3" aria-hidden="true" />
          )}
          {refreshing ? t("todayRefreshing") : t("todayRefresh")}
        </Button>
      </div>

      {refreshing && progress && (
        <p className="w-full font-ui text-t-caption text-fg-tertiary" role="status">
          {progress}
        </p>
      )}

      {!refreshing && stale && age && (
        <p className="w-full font-ui text-t-caption text-warn">
          {t("todayPlanStale", { age })}
        </p>
      )}
    </header>
  );
}
