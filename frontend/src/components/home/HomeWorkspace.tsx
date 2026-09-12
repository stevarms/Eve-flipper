import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  getAccumulateResult,
  getTodayPlan,
  refreshTodayPlan,
  setTodayActionState,
} from "@/lib/api";
import { useGlobalToast } from "@/components/Toast";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/EmptyState";
import { useI18n } from "@/lib/i18n";
import type { MainTabId } from "@/lib/cockpit";
import type {
  AccumulateRow,
  TodayAction,
  TodayDeepLink,
  TodayOption,
  TodayPlan,
} from "@/lib/types";
import { ActionList } from "./ActionList";
import { BatchBar } from "./BatchBar";
import { CapitalBar } from "./CapitalBar";
import { NotAdvisedPanel } from "./NotAdvisedPanel";
import { OptionsPanel } from "./OptionsPanel";
import { RunPanel } from "./RunPanel";
import { WaitingPanel } from "./WaitingPanel";
import { AccumulatePanel } from "./AccumulatePanel";
import { TodayHeader, type TodayView } from "./TodayHeader";

/**
 * Home / "Today" — decide, then execute.
 *
 * The screen answers three questions in order: where the ISK is and what it
 * is earning, what to do next and in what order, and where the idle ISK
 * should go. The ranking, the grading and the sentences all come from
 * internal/engine/today.go; this component renders them and owns the
 * interaction.
 *
 * Two loads with deliberately different costs. The stored plan is one SQLite
 * row, so the page paints immediately. Rebuilding it fans out to ESI and
 * streams progress, which is why it happens on an explicit refresh, or
 * automatically once the server reports the plan has gone stale.
 */

const VIEW_STORAGE_KEY = "eve-flipper:today-view";

/** Done and skipped marks applied locally while the server catches up. */
type LocalMarks = Record<string, "done" | "skip">;

export interface HomeWorkspaceProps {
  isLoggedIn: boolean;
  onNavigate: (tab: MainTabId, focus?: TodayDeepLink) => void;
}

export function HomeWorkspace({ isLoggedIn, onNavigate }: HomeWorkspaceProps) {
  const { t } = useI18n();
  const { addToast } = useGlobalToast();

  const [plan, setPlan] = useState<TodayPlan | null>(null);
  const [generatedAt, setGeneratedAt] = useState<string | undefined>(undefined);
  const [stale, setStale] = useState(false);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [progress, setProgress] = useState("");
  const [marks, setMarks] = useState<LocalMarks>({});
  // Read separately from the plan rather than embedded in it. The sweep is a
  // different tab's output on its own schedule, and one SQLite row is cheap
  // enough that coupling the two refreshes would buy nothing.
  const [accumulate, setAccumulate] = useState<AccumulateRow[]>([]);
  const [view, setView] = useState<TodayView>(() => {
    try {
      return localStorage.getItem(VIEW_STORAGE_KEY) === "list" ? "list" : "run";
    } catch {
      return "run";
    }
  });

  // Guards the once-per-load auto-refresh so a re-render cannot fire a second
  // scan-shaped request while the first is still in flight.
  const autoRefreshed = useRef(false);

  const applyPlan = useCallback((next: TodayPlan) => {
    setPlan(next);
    setGeneratedAt(next.generated_at);
    setStale(false);
    // A fresh plan supersedes every local mark: the actions were rebuilt, so
    // a "done" against the old list is no longer about anything on screen.
    setMarks({});
  }, []);

  const runRefresh = useCallback(async () => {
    setRefreshing(true);
    setProgress("");
    try {
      const next = await refreshTodayPlan(setProgress);
      applyPlan(next);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      addToast(t("todayRefreshFailed", { error: message }), "error", 4000);
    } finally {
      setRefreshing(false);
      setProgress("");
    }
  }, [addToast, applyPlan, t]);

  useEffect(() => {
    let cancelled = false;
    if (!isLoggedIn) {
      setLoading(false);
      return;
    }

    (async () => {
      setLoading(true);
      try {
        const env = await getTodayPlan();
        if (cancelled) return;
        setPlan(env.plan);
        setGeneratedAt(env.generated_at);
        setStale(env.stale);
        setLoading(false);

        // Auto-refresh once per load when the server says the plan has aged
        // out. The paint above has already happened, so this costs the user
        // no waiting on a plan they can already read.
        if (env.stale && !autoRefreshed.current) {
          autoRefreshed.current = true;
          await runRefresh();
        }
      } catch {
        if (!cancelled) setLoading(false);
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [isLoggedIn, runRefresh]);

  // The accumulate sweep, if one has been run. Its absence is the normal cold
  // state, so a failure here is silent rather than a toast on every load.
  useEffect(() => {
    let cancelled = false;
    if (!isLoggedIn) return;
    (async () => {
      try {
        const env = await getAccumulateResult();
        if (!cancelled) setAccumulate(env.result?.rows ?? []);
      } catch {
        /* no sweep yet */
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [isLoggedIn]);

  const changeView = useCallback((next: TodayView) => {
    setView(next);
    try {
      localStorage.setItem(VIEW_STORAGE_KEY, next);
    } catch {
      // A remembered view is a convenience; a browser refusing storage is
      // not a reason to fail the interaction.
    }
  }, []);

  /**
   * Mark an action and move on.
   *
   * Optimistic: the queue advances immediately and the write follows. This is
   * what the Space key hits dozens of times in a session, and a round trip
   * between keypress and the next action would be felt every time. The
   * projection travels with the mark because the plan that produced it is
   * replaced on the next refresh — this is the only moment it can be recorded.
   */
  const mark = useCallback(
    (action: TodayAction, mode: "done" | "skip") => {
      setMarks((prev) => ({ ...prev, [action.id]: mode }));
      void setTodayActionState({
        action_id: action.id,
        mode,
        kind: action.kind,
        type_id: action.type_id,
        grade: action.grade,
        projected_isk: action.downside_isk_7d,
        quantity: action.quantity,
        price: action.paste_price,
      }).catch(() => {
        // The mark did not persist, so put the action back rather than let
        // the user believe work was recorded that was not.
        setMarks((prev) => {
          const next = { ...prev };
          delete next[action.id];
          return next;
        });
        addToast(t("todayRefreshFailed", { error: "could not save that" }), "error", 3000);
      });
    },
    [addToast, t],
  );

  const onDone = useCallback((a: TodayAction) => mark(a, "done"), [mark]);
  const onSkip = useCallback((a: TodayAction) => mark(a, "skip"), [mark]);

  const navigateTo = useCallback(
    (link: TodayDeepLink) => onNavigate(link.tab as MainTabId, link),
    [onNavigate],
  );
  const onDetails = useCallback((a: TodayAction) => navigateTo(a.deep_link), [navigateTo]);
  const onOptionDetails = useCallback((o: TodayOption) => navigateTo(o.deep_link), [navigateTo]);

  /** Everything still outstanding, in the engine's order. */
  const pending = useMemo(() => {
    if (!plan) return [];
    return plan.actions.filter((a) => !a.done && !a.skipped && !marks[a.id]);
  }, [plan, marks]);

  const remainingSeconds = useMemo(
    () => pending.reduce((sum, a) => sum + a.est_seconds, 0),
    [pending],
  );

  if (!isLoggedIn) {
    return (
      <div className="flex-1 min-h-0 overflow-y-auto eve-scrollbar p-3">
        <p className="font-ui text-t-body text-fg-secondary">{t("todayLoginPrompt")}</p>
      </div>
    );
  }

  if (loading) {
    return (
      <div className="flex-1 min-h-0 overflow-y-auto eve-scrollbar">
        <EmptyState reason="loading" />
      </div>
    );
  }

  return (
    <div className="flex-1 min-h-0 overflow-y-auto eve-scrollbar p-3">
      <TodayHeader
        generatedAt={generatedAt}
        stale={stale}
        refreshing={refreshing}
        progress={progress}
        view={view}
        onViewChange={changeView}
        onRefresh={() => void runRefresh()}
        remainingSeconds={remainingSeconds}
        doneCount={(plan?.actions.length ?? 0) - pending.length}
        totalCount={plan?.actions.length ?? 0}
      />

      {!plan ? (
        <section className="rounded-sm border border-eve-border bg-surface-1 px-3 py-8 text-center">
          <p className="font-ui text-t-body text-fg-secondary">{t("todayNoPlan")}</p>
          <p className="mt-1 font-ui text-t-caption text-fg-tertiary">{t("todayNoPlanHint")}</p>
          <Button className="mt-3" onClick={() => void runRefresh()} disabled={refreshing}>
            {t("todayBuildPlan")}
          </Button>
        </section>
      ) : (
        <div className="flex flex-col gap-3">
          <CapitalBar capital={plan.capital} performance={plan.performance} />

          {plan.warnings?.map((warning) => (
            <p key={warning} className="font-ui text-t-caption text-warn-dim">
              {warning}
            </p>
          ))}

          {plan.timing.note && (
            <p className="font-ui text-t-caption text-info">{plan.timing.note}</p>
          )}

          {pending.length === 0 ? (
            <section className="rounded-sm border border-eve-border bg-surface-1 px-3 py-8 text-center">
              <p className="font-ui text-t-body text-profit">{t("todayAllDone")}</p>
              <p className="mt-1 font-ui text-t-caption text-fg-tertiary">
                {t("todayAllDoneHint")}
              </p>
            </section>
          ) : view === "run" ? (
            <>
              <RunPanel
                action={pending[0]}
                index={plan.actions.length - pending.length}
                total={plan.actions.length}
                secondsRemaining={remainingSeconds}
                onDone={onDone}
                onSkip={onSkip}
                onDetails={onDetails}
              />
              <p className="font-ui text-t-caption text-fg-tertiary">{t("todayKeyboardHint")}</p>
            </>
          ) : (
            <ActionList
              actions={pending}
              budgetSeconds={plan.budget.budget_seconds}
              onDone={onDone}
              onSkip={onSkip}
              onDetails={onDetails}
            />
          )}

          <WaitingPanel rows={plan.waiting ?? []} onOpen={(r) => navigateTo(r.deep_link)} />
          <AccumulatePanel
            rows={accumulate}
            onOpenTab={() => navigateTo({ tab: "accumulate" })}
          />
          <BatchBar batches={plan.batches} />
          <OptionsPanel options={plan.options} onNavigate={onOptionDetails} />
          <NotAdvisedPanel actions={plan.not_advised} onDetails={onDetails} />
        </div>
      )}
    </div>
  );
}
