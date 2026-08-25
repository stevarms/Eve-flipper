import type { ComponentProps } from "react";
import { useI18n } from "@/lib/i18n";
import type { IndustryJobsWorkspaceTab } from "./IndustryJobsWorkspaceNav";
import { IndustryJobsGuidePanel } from "./IndustryJobsGuidePanel";
import { IndustryOperationsBoards } from "./IndustryOperationsBoards";
import { IndustryOperationsJobsPanel } from "./IndustryOperationsJobsPanel";
import { IndustryJobsProjectHeader } from "./IndustryJobsProjectHeader";
import { IndustryPlannerSchedulerPanel } from "./IndustryPlannerSchedulerPanel";
import { IndustryWorkspaceStatusBoards } from "./IndustryWorkspaceStatusBoards";

interface IndustryJobsLedgerPanelProps {
  isLoggedIn: boolean;
  ledgerProjectsLoading: boolean;
  /** Which top-level industry tab is active: "guide" (Projects) or
   *  "operations" (Operations). The old "planning" case is gone —
   *  Plan-tab responsibilities moved into Discover's reactive materials
   *  preview (pre-commit) and Operations (post-commit / scheduler / diff). */
  jobsWorkspaceTab: IndustryJobsWorkspaceTab;
  projectHeaderProps: ComponentProps<typeof IndustryJobsProjectHeader>;
  guidePanelProps: ComponentProps<typeof IndustryJobsGuidePanel>;
  workspaceStatusBoardsProps: ComponentProps<typeof IndustryWorkspaceStatusBoards>;
  schedulerPanelProps: ComponentProps<typeof IndustryPlannerSchedulerPanel>;
  operationsBoardsProps: ComponentProps<typeof IndustryOperationsBoards>;
  operationsJobsProps: ComponentProps<typeof IndustryOperationsJobsPanel>;
}

export function IndustryJobsLedgerPanel({
  isLoggedIn,
  ledgerProjectsLoading,
  jobsWorkspaceTab,
  projectHeaderProps,
  guidePanelProps,
  workspaceStatusBoardsProps,
  schedulerPanelProps,
  operationsBoardsProps,
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

            <IndustryWorkspaceStatusBoards {...workspaceStatusBoardsProps} />

            <IndustryPlannerSchedulerPanel {...schedulerPanelProps} />

            <IndustryOperationsBoards {...operationsBoardsProps} />

            <IndustryOperationsJobsPanel {...operationsJobsProps} />
          </>
        )}
      </div>
    </div>
  );
}
