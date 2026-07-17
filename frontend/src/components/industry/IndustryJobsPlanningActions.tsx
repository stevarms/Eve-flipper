import { useEffect, useMemo, useRef, useState } from "react";
import { useI18n } from "@/lib/i18n";
import type { Dispatch, SetStateAction } from "react";
import type { BuildableItem, IndustryAnalysis, IndustryPlanPreview } from "@/lib/types";
import type { IndustryJobsWorkspaceTab } from "./IndustryJobsWorkspaceNav";

// The planner action bar used to expose 6+ lifecycle buttons at once (Seed,
// Preview, Apply-Preview, Apply-Current, plus four +Row buttons and a stack
// of checkboxes). This version collapses the lifecycle into a single primary
// CTA whose label reflects the derived "next action" for the current plan
// state; advanced knobs and less-common paths live under a "…" menu. The
// +Row buttons moved into their corresponding section headers in
// IndustryPlannerBuilderPanel.

interface IndustryJobsPlanningActionsProps {
  jobsWorkspaceTab: IndustryJobsWorkspaceTab;
  handleGeneratePlanDraft: () => void;
  previewingLedgerPlan: boolean;
  selectedLedgerProjectId: number;
  hasVisualPlanRows: boolean;
  result: IndustryAnalysis | null;
  selectedItem: BuildableItem | null;
  handlePreviewCurrentAnalysisToLedgerPlan: () => Promise<void>;
  applyingLedgerPlan: boolean;
  lastLedgerPlanPreviewPatch: unknown;
  strictBlueprintApplyBlocked: boolean;
  isLastLedgerPreviewStale: boolean;
  handleApplyLastPreviewToLedgerPlan: (bypassStrictGate?: boolean) => Promise<void>;
  handleApplyCurrentAnalysisToLedgerPlan: (bypassStrictGate?: boolean) => Promise<void>;
  autoFixVisualTaskBlueprintBindings: () => void;
  planDraftTasksLength: number;
  planTaskBlueprintOptionsLength: number;
  visualTaskBlueprintBindingStatsMissing: number;
  visualTaskBlueprintBindingStatsFallback: number;
  clearVisualPlanBuilder: () => void;
  replaceLedgerPlanOnApply: boolean;
  setReplaceLedgerPlanOnApply: Dispatch<SetStateAction<boolean>>;
  useVisualPlanBuilder: boolean;
  setUseVisualPlanBuilder: Dispatch<SetStateAction<boolean>>;
  strictBlueprintBindingMode: boolean;
  setStrictBlueprintBindingMode: Dispatch<SetStateAction<boolean>>;
  planDraftJobsLength: number;
  planDraftMaterialsLength: number;
  planDraftBlueprintsLength: number;
  lastLedgerPlanSummary: string;
  lastLedgerPlanPreview: IndustryPlanPreview | null;
}

type CTAKind = "none" | "seed" | "preview" | "repreview" | "apply" | "blocked";

interface CTA {
  kind: CTAKind;
  label: string;
  onClick: () => void;
  disabled: boolean;
  tone: string; // tailwind classes
  hint: string;
}

export function IndustryJobsPlanningActions({
  jobsWorkspaceTab,
  handleGeneratePlanDraft,
  previewingLedgerPlan,
  selectedLedgerProjectId,
  hasVisualPlanRows,
  result,
  selectedItem,
  handlePreviewCurrentAnalysisToLedgerPlan,
  applyingLedgerPlan,
  lastLedgerPlanPreviewPatch,
  strictBlueprintApplyBlocked,
  isLastLedgerPreviewStale,
  handleApplyLastPreviewToLedgerPlan,
  handleApplyCurrentAnalysisToLedgerPlan,
  autoFixVisualTaskBlueprintBindings,
  planDraftTasksLength,
  planTaskBlueprintOptionsLength,
  visualTaskBlueprintBindingStatsMissing,
  visualTaskBlueprintBindingStatsFallback,
  clearVisualPlanBuilder,
  replaceLedgerPlanOnApply,
  setReplaceLedgerPlanOnApply,
  useVisualPlanBuilder,
  setUseVisualPlanBuilder,
  strictBlueprintBindingMode,
  setStrictBlueprintBindingMode,
  planDraftJobsLength,
  planDraftMaterialsLength,
  planDraftBlueprintsLength,
  lastLedgerPlanSummary,
  lastLedgerPlanPreview,
}: IndustryJobsPlanningActionsProps) {
  const { t } = useI18n();
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);

  // Close menu on outside click.
  useEffect(() => {
    if (!menuOpen) return;
    const handler = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setMenuOpen(false);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [menuOpen]);

  const hasSeedSource = Boolean(result && selectedItem);
  const hasPreview = Boolean(lastLedgerPlanPreviewPatch);

  // The stateful primary CTA: single button whose label + handler reflect
  // the most obvious next step for the current plan state. Advanced flows
  // (Apply-Current-skipping-Preview, bypass strict gate, replace vs append)
  // stay reachable through the "…" menu.
  const cta: CTA = useMemo(() => {
    if (selectedLedgerProjectId <= 0) {
      return {
        kind: "none",
        label: t("industryPlanCTA_pickProject"),
        onClick: () => {},
        disabled: true,
        tone: "border-eve-border text-eve-dim",
        hint: t("industryPlanCTA_pickProjectHint"),
      };
    }
    if (strictBlueprintApplyBlocked && (hasVisualPlanRows || hasSeedSource)) {
      return {
        kind: "blocked",
        label: t("industryPlanCTA_fixBindings").replace(
          "{count}",
          String(visualTaskBlueprintBindingStatsMissing),
        ),
        onClick: autoFixVisualTaskBlueprintBindings,
        disabled: false,
        tone: "border-red-500/60 text-red-300 hover:bg-red-500/10",
        hint: t("industryPlanCTA_fixBindingsHint"),
      };
    }
    if (!hasVisualPlanRows && !hasSeedSource) {
      return {
        kind: "none",
        label: t("industryPlanCTA_nothingToPlan"),
        onClick: () => {},
        disabled: true,
        tone: "border-eve-border text-eve-dim",
        hint: t("industryPlanCTA_nothingToPlanHint"),
      };
    }
    if (!hasVisualPlanRows && hasSeedSource) {
      return {
        kind: "seed",
        label: t("industryPlanCTA_seed"),
        onClick: handleGeneratePlanDraft,
        disabled: false,
        tone: "border-eve-accent/60 text-eve-accent hover:bg-eve-accent/10",
        hint: t("industryPlanCTA_seedHint"),
      };
    }
    if (hasPreview && !isLastLedgerPreviewStale) {
      return {
        kind: "apply",
        label: applyingLedgerPlan
          ? t("industryLedgerApplying")
          : t("industryPlanCTA_apply"),
        onClick: () => { void handleApplyLastPreviewToLedgerPlan(); },
        disabled: applyingLedgerPlan,
        tone: "border-emerald-500/60 text-emerald-300 hover:bg-emerald-500/10",
        hint: t("industryPlanCTA_applyHint"),
      };
    }
    if (hasPreview && isLastLedgerPreviewStale) {
      return {
        kind: "repreview",
        label: previewingLedgerPlan
          ? t("industryLedgerPreviewing")
          : t("industryPlanCTA_rePreview"),
        onClick: () => { void handlePreviewCurrentAnalysisToLedgerPlan(); },
        disabled: previewingLedgerPlan,
        tone: "border-yellow-500/60 text-yellow-300 hover:bg-yellow-500/10",
        hint: t("industryPlanCTA_rePreviewHint"),
      };
    }
    return {
      kind: "preview",
      label: previewingLedgerPlan
        ? t("industryLedgerPreviewing")
        : t("industryPlanCTA_preview"),
      onClick: () => { void handlePreviewCurrentAnalysisToLedgerPlan(); },
      disabled: previewingLedgerPlan,
      tone: "border-cyan-500/60 text-cyan-300 hover:bg-cyan-500/10",
      hint: t("industryPlanCTA_previewHint"),
    };
  }, [
    selectedLedgerProjectId,
    strictBlueprintApplyBlocked,
    hasVisualPlanRows,
    hasSeedSource,
    hasPreview,
    isLastLedgerPreviewStale,
    applyingLedgerPlan,
    previewingLedgerPlan,
    visualTaskBlueprintBindingStatsMissing,
    autoFixVisualTaskBlueprintBindings,
    handleGeneratePlanDraft,
    handleApplyLastPreviewToLedgerPlan,
    handlePreviewCurrentAnalysisToLedgerPlan,
    t,
  ]);

  if (jobsWorkspaceTab !== "planning") {
    return null;
  }

  const canApplyCurrentDirect =
    selectedLedgerProjectId > 0
    && (hasVisualPlanRows || hasSeedSource)
    && !applyingLedgerPlan
    && !strictBlueprintApplyBlocked;

  const canBypassStrictWithPreview =
    strictBlueprintApplyBlocked
    && Boolean(lastLedgerPlanPreviewPatch)
    && !applyingLedgerPlan;

  const canBypassStrictWithCurrent =
    strictBlueprintApplyBlocked
    && (hasVisualPlanRows || hasSeedSource)
    && !applyingLedgerPlan;

  return (
    <div className="mt-2 flex flex-wrap items-center gap-2">
      {/* Primary stateful CTA */}
      <button
        type="button"
        onClick={cta.onClick}
        disabled={cta.disabled}
        title={cta.hint}
        className={`px-3 py-1.5 rounded-sm text-xs font-semibold border transition-colors disabled:opacity-50 disabled:cursor-not-allowed ${cta.tone}`}
      >
        {cta.label}
      </button>

      {/* "…" menu with advanced knobs + less-common flows */}
      <div ref={menuRef} className="relative">
        <button
          type="button"
          onClick={() => setMenuOpen((v) => !v)}
          className="px-2 py-1.5 rounded-sm text-xs font-semibold border border-eve-border text-eve-dim hover:text-eve-accent hover:border-eve-accent/40"
          title={t("industryPlanCTA_moreMenu")}
        >
          ⋯
        </button>
        {menuOpen && (
          <div className="absolute left-0 top-full mt-1 z-40 min-w-[16rem] bg-eve-panel border border-eve-border rounded-sm shadow-eve-glow p-2 space-y-2">
            <div className="text-[10px] uppercase tracking-wider text-eve-dim">
              {t("industryPlanCTA_menuTitle")}
            </div>

            <button
              type="button"
              onClick={() => { setMenuOpen(false); handleGeneratePlanDraft(); }}
              disabled={!hasSeedSource}
              className="w-full text-left px-2 py-1 text-[11px] rounded-sm hover:bg-eve-accent/10 disabled:opacity-40 disabled:cursor-not-allowed"
            >
              {t("industryLedgerSeedBuilder")}
            </button>

            <button
              type="button"
              onClick={() => { setMenuOpen(false); void handleApplyCurrentAnalysisToLedgerPlan(); }}
              disabled={!canApplyCurrentDirect}
              title={t("industryPlanCTA_applyCurrentHint")}
              className="w-full text-left px-2 py-1 text-[11px] rounded-sm hover:bg-eve-accent/10 disabled:opacity-40 disabled:cursor-not-allowed"
            >
              {t("industryLedgerApplyCurrentPlan")}
            </button>

            <button
              type="button"
              onClick={() => {
                setMenuOpen(false);
                autoFixVisualTaskBlueprintBindings();
              }}
              disabled={
                planDraftTasksLength === 0 ||
                planTaskBlueprintOptionsLength === 0 ||
                (visualTaskBlueprintBindingStatsMissing === 0 && visualTaskBlueprintBindingStatsFallback === 0)
              }
              className="w-full text-left px-2 py-1 text-[11px] rounded-sm text-amber-300 hover:bg-amber-500/10 disabled:opacity-40 disabled:cursor-not-allowed"
            >
              {t("industryPlanCTA_autoFixBindings")}
            </button>

            <button
              type="button"
              onClick={() => { setMenuOpen(false); clearVisualPlanBuilder(); }}
              disabled={!hasVisualPlanRows}
              className="w-full text-left px-2 py-1 text-[11px] rounded-sm text-red-300 hover:bg-red-500/10 disabled:opacity-40 disabled:cursor-not-allowed"
            >
              {t("industryLedgerClearBuilder")}
            </button>

            <div className="border-t border-eve-border/40 pt-2 space-y-1">
              <label className="flex items-center gap-2 text-[11px] text-eve-dim select-none cursor-pointer">
                <input
                  type="checkbox"
                  checked={replaceLedgerPlanOnApply}
                  onChange={(e) => setReplaceLedgerPlanOnApply(e.target.checked)}
                  className="accent-eve-accent"
                />
                {t("industryLedgerReplacePlanRows")}
              </label>
              <label className="flex items-center gap-2 text-[11px] text-eve-dim select-none cursor-pointer">
                <input
                  type="checkbox"
                  checked={useVisualPlanBuilder}
                  onChange={(e) => setUseVisualPlanBuilder(e.target.checked)}
                  className="accent-eve-accent"
                />
                {t("industryLedgerUseVisualBuilder")}
              </label>
              <label className="flex items-center gap-2 text-[11px] text-eve-dim select-none cursor-pointer">
                <input
                  type="checkbox"
                  checked={strictBlueprintBindingMode}
                  onChange={(e) => setStrictBlueprintBindingMode(e.target.checked)}
                  className="accent-eve-accent"
                />
                {t("industryPlanCTA_strictBpGate")}
              </label>
            </div>

            {strictBlueprintApplyBlocked && (
              <div className="border-t border-eve-border/40 pt-2 space-y-1">
                <div className="text-[10px] text-red-300">
                  {t("industryPlanCTA_bypassStrictWarning").replace(
                    "{count}",
                    String(visualTaskBlueprintBindingStatsMissing),
                  )}
                </div>
                {canBypassStrictWithPreview && (
                  <button
                    type="button"
                    onClick={() => {
                      if (!window.confirm(t("industryPlanCTA_bypassConfirmPreview"))) return;
                      setMenuOpen(false);
                      void handleApplyLastPreviewToLedgerPlan(true);
                    }}
                    className="w-full text-left px-2 py-1 text-[11px] rounded-sm text-red-300 hover:bg-red-500/10"
                  >
                    {t("industryPlanCTA_applyPreviewAnyway")}
                  </button>
                )}
                {canBypassStrictWithCurrent && (
                  <button
                    type="button"
                    onClick={() => {
                      if (!window.confirm(t("industryPlanCTA_bypassConfirmCurrent"))) return;
                      setMenuOpen(false);
                      void handleApplyCurrentAnalysisToLedgerPlan(true);
                    }}
                    className="w-full text-left px-2 py-1 text-[11px] rounded-sm text-red-300 hover:bg-red-500/10"
                  >
                    {t("industryPlanCTA_applyCurrentAnyway")}
                  </button>
                )}
              </div>
            )}
          </div>
        )}
      </div>

      {/* Compact status pills stay visible next to the primary CTA. */}
      <span className="text-[11px] text-eve-dim font-mono tabular-nums">
        {t("industryLedgerRowsPrefix")}: T{planDraftTasksLength} J{planDraftJobsLength} M{planDraftMaterialsLength} B{planDraftBlueprintsLength}
      </span>

      {lastLedgerPlanSummary && (
        <span className="text-[11px] text-eve-dim">{lastLedgerPlanSummary}</span>
      )}
      {lastLedgerPlanPreview && (
        <span className={`text-[11px] ${isLastLedgerPreviewStale ? "text-yellow-400" : "text-emerald-400"}`}>
          {isLastLedgerPreviewStale
            ? t("industryLedgerPreviewStale")
            : t("industryLedgerPreviewInSync")}
        </span>
      )}
    </div>
  );
}
