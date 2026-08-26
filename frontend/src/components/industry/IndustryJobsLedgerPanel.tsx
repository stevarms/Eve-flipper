import type { ComponentProps } from "react";
import { useI18n } from "@/lib/i18n";
import { IndustryJobsGuidePanel } from "./IndustryJobsGuidePanel";
import { IndustryJobsProjectHeader } from "./IndustryJobsProjectHeader";

/**
 * Projects sub-tab shell. Renders the project header (create/select) plus
 * the Guide panel that lists existing projects.
 *
 * Operations no longer routes through this panel — it renders inline from
 * IndustryTab.tsx with its own status bar + task list + materials footer.
 * Plan was deleted earlier in the Industry-workspace consolidation arc.
 */
interface IndustryJobsLedgerPanelProps {
  isLoggedIn: boolean;
  ledgerProjectsLoading: boolean;
  projectHeaderProps: ComponentProps<typeof IndustryJobsProjectHeader>;
  guidePanelProps: ComponentProps<typeof IndustryJobsGuidePanel>;
}

export function IndustryJobsLedgerPanel({
  isLoggedIn,
  ledgerProjectsLoading,
  projectHeaderProps,
  guidePanelProps,
}: IndustryJobsLedgerPanelProps) {
  const { t } = useI18n();

  return (
    <div className="shrink-0 m-2 mt-0 pb-2">
      <div className="bg-eve-panel border border-eve-border rounded-sm p-3">
        <div className="flex items-center justify-between gap-2 mb-2">
          <div className="text-[10px] uppercase tracking-wider text-eve-dim">{t("industryLedgerTitle")}</div>
          {ledgerProjectsLoading && (
            <span className="text-[10px] text-eve-dim">{t("industryLedgerSyncingProjects")}</span>
          )}
        </div>
        {!isLoggedIn ? (
          <div className="text-xs text-eve-dim">{t("industryLedgerLoginRequired")}</div>
        ) : (
          <>
            <IndustryJobsProjectHeader {...projectHeaderProps} />
            <IndustryJobsGuidePanel {...guidePanelProps} />
          </>
        )}
      </div>
    </div>
  );
}
