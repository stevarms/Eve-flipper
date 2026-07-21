import type { ComponentProps } from "react";
import { useI18n } from "@/lib/i18n";
import type { IndustryJobsWorkspaceTab } from "./IndustryJobsWorkspaceNav";
import { IndustryJobsGuidePanel } from "./IndustryJobsGuidePanel";
import { IndustryPlannerWarningLog } from "./IndustryPlannerWarningLog";
import { IndustryDependencyBoard } from "./IndustryDependencyBoard";
import { IndustryOperationsBoards } from "./IndustryOperationsBoards";
import { IndustryPlannerBuilderPanel } from "./IndustryPlannerBuilderPanel";
import { IndustryOperationsJobsPanel } from "./IndustryOperationsJobsPanel";
import { IndustryJobsProjectHeader } from "./IndustryJobsProjectHeader";
import { IndustryJobsPlanningActions } from "./IndustryJobsPlanningActions";
import { IndustryPlannerSchedulerPanel } from "./IndustryPlannerSchedulerPanel";
import { IndustryPlanPreviewPanel } from "./IndustryPlanPreviewPanel";
import { IndustryWorkspaceStatusBoards } from "./IndustryWorkspaceStatusBoards";

interface IndustryJobsLedgerPanelProps {
  isLoggedIn: boolean;
  ledgerProjectsLoading: boolean;
  /** Which top-level industry tab is active: "guide" (Projects), "planning"
   *  (Plan), or "operations" (Operations). The internal panels still gate on
   *  this value; the user-facing sub-nav that used to switch it was removed
   *  when Projects/Plan/Operations became top-level tabs. */
  jobsWorkspaceTab: IndustryJobsWorkspaceTab;
  projectHeaderProps: ComponentProps<typeof IndustryJobsProjectHeader>;
  guidePanelProps: ComponentProps<typeof IndustryJobsGuidePanel>;
  planningActionsProps: ComponentProps<typeof IndustryJobsPlanningActions>;
  warningLogProps: ComponentProps<typeof IndustryPlannerWarningLog>;
  workspaceStatusBoardsProps: ComponentProps<typeof IndustryWorkspaceStatusBoards>;
  dependencyBoardProps: ComponentProps<typeof IndustryDependencyBoard>;
  schedulerPanelProps: ComponentProps<typeof IndustryPlannerSchedulerPanel>;
  planPreviewPanelProps: ComponentProps<typeof IndustryPlanPreviewPanel>;
  operationsBoardsProps: ComponentProps<typeof IndustryOperationsBoards>;
  plannerBuilderProps: ComponentProps<typeof IndustryPlannerBuilderPanel>;
  operationsJobsProps: ComponentProps<typeof IndustryOperationsJobsPanel>;
}

export function IndustryJobsLedgerPanel({
  isLoggedIn,
  ledgerProjectsLoading,
  jobsWorkspaceTab,
  projectHeaderProps,
  guidePanelProps,
  planningActionsProps,
  warningLogProps,
  workspaceStatusBoardsProps,
  dependencyBoardProps,
  schedulerPanelProps,
  planPreviewPanelProps,
  operationsBoardsProps,
  plannerBuilderProps,
  operationsJobsProps,
}: IndustryJobsLedgerPanelProps) {
  const { t } = useI18n();
  // Projects tab IS the project picker — no need for the header row too.
  const showProjectHeader = jobsWorkspaceTab !== "guide";

  return (
    <div className="shrink-0 m-2 mt-0 pb-2">
      <div className="bg-eve-panel border border-eve-border rounded-sm p-3">
        <div className="flex items-center justify-between gap-2 mb-2">
          <div className="text-[10px] uppercase tracking-wider text-eve-dim">{t("industryLedgerTitle")}</div>
          {ledgerProjectsLoading && <span className="text-[10px] text-eve-dim">{t("industryLedgerSyncingProjects")}</span>}
        </div>
        {!isLoggedIn ? (
          <div className="text-xs text-eve-dim">
            {t("industryLedgerLoginRequired")}
          </div>
        ) : (
          <>
            {showProjectHeader && <IndustryJobsProjectHeader {...projectHeaderProps} />}

            {jobsWorkspaceTab === "guide" && (
              <IndustryJobsGuidePanel {...guidePanelProps} />
            )}

            <IndustryJobsPlanningActions {...planningActionsProps} />

            {(jobsWorkspaceTab === "guide" || jobsWorkspaceTab === "planning") && (
              <IndustryPlannerWarningLog {...warningLogProps} />
            )}

            <IndustryWorkspaceStatusBoards {...workspaceStatusBoardsProps} />

            {jobsWorkspaceTab === "planning" && (
              <IndustryDependencyBoard {...dependencyBoardProps} />
            )}

            <IndustryPlannerSchedulerPanel {...schedulerPanelProps} />

            <IndustryPlanPreviewPanel {...planPreviewPanelProps} />

            <IndustryOperationsBoards {...operationsBoardsProps} />

            <IndustryPlannerBuilderPanel {...plannerBuilderProps} />

            <IndustryOperationsJobsPanel {...operationsJobsProps} />
          </>
        )}
      </div>
    </div>
  );
}
