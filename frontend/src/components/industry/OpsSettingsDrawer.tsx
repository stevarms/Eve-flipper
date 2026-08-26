import type { Dispatch, SetStateAction } from "react";
import { useI18n } from "@/lib/i18n";
import type { IndustryJobStatus } from "@/lib/types";

/**
 * Collapsible settings drawer on Operations. Holds the rarely-touched
 * knobs that used to clutter the always-visible chrome (scheduler config,
 * rebalance defaults, BP-sync defaults). Actions themselves still live in
 * the materials footer — this drawer holds only the *parameters* those
 * actions use.
 */

interface OpsSettingsDrawerProps {
  open: boolean;
  // Scheduler
  enablePlanScheduler: boolean;
  setEnablePlanScheduler: Dispatch<SetStateAction<boolean>>;
  schedulerSlotCount: number;
  setSchedulerSlotCount: Dispatch<SetStateAction<number>>;
  schedulerMaxRunsPerJob: number;
  setSchedulerMaxRunsPerJob: Dispatch<SetStateAction<number>>;
  schedulerMaxDurationHours: number;
  setSchedulerMaxDurationHours: Dispatch<SetStateAction<number>>;
  schedulerQueueStatus: IndustryJobStatus;
  setSchedulerQueueStatus: Dispatch<SetStateAction<IndustryJobStatus>>;
  // Rebalance defaults
  rebalanceInventoryScope: "single" | "all";
  setRebalanceInventoryScope: Dispatch<SetStateAction<"single" | "all">>;
  rebalanceLookbackDays: number;
  setRebalanceLookbackDays: Dispatch<SetStateAction<number>>;
  rebalanceStrategy: "preserve" | "buy" | "build";
  setRebalanceStrategy: Dispatch<SetStateAction<"preserve" | "buy" | "build">>;
  rebalanceWarehouseScope: "global" | "location_first" | "strict_location";
  setRebalanceWarehouseScope: Dispatch<SetStateAction<"global" | "location_first" | "strict_location">>;
  rebalanceUseSelectedStation: boolean;
  setRebalanceUseSelectedStation: Dispatch<SetStateAction<boolean>>;
  // BP sync default
  blueprintSyncDefaultBPCRuns: number;
  setBlueprintSyncDefaultBPCRuns: Dispatch<SetStateAction<number>>;
}

export function OpsSettingsDrawer(props: OpsSettingsDrawerProps) {
  const { t } = useI18n();
  const {
    open,
    enablePlanScheduler,
    setEnablePlanScheduler,
    schedulerSlotCount,
    setSchedulerSlotCount,
    schedulerMaxRunsPerJob,
    setSchedulerMaxRunsPerJob,
    schedulerMaxDurationHours,
    setSchedulerMaxDurationHours,
    schedulerQueueStatus,
    setSchedulerQueueStatus,
    rebalanceInventoryScope,
    setRebalanceInventoryScope,
    rebalanceLookbackDays,
    setRebalanceLookbackDays,
    rebalanceStrategy,
    setRebalanceStrategy,
    rebalanceWarehouseScope,
    setRebalanceWarehouseScope,
    rebalanceUseSelectedStation,
    setRebalanceUseSelectedStation,
    blueprintSyncDefaultBPCRuns,
    setBlueprintSyncDefaultBPCRuns,
  } = props;

  if (!open) return null;

  return (
    <div className="mt-2 border border-eve-border/40 rounded-sm p-3 bg-eve-dark/40 space-y-3">
      {/* Scheduler */}
      <section>
        <div className="text-[10px] uppercase tracking-wider text-eve-dim mb-1.5">Scheduler</div>
        <div className="flex flex-wrap items-center gap-2 text-[11px]">
          <label className="flex items-center gap-1 text-eve-dim select-none">
            <input
              type="checkbox"
              checked={enablePlanScheduler}
              onChange={(e) => setEnablePlanScheduler(e.target.checked)}
              className="accent-eve-accent"
            />
            {t("industryLedgerAutoSplitScheduler")}
          </label>
          {enablePlanScheduler && (
            <>
              <span className="text-eve-dim">{t("industryLedgerSchedulerSlots")}</span>
              <input
                type="number"
                min={1}
                max={64}
                value={schedulerSlotCount}
                onChange={(e) => setSchedulerSlotCount(Math.max(1, Math.min(64, Number(e.target.value) || 1)))}
                className="w-16 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-eve-text font-mono"
              />
              <span className="text-eve-dim">{t("industryLedgerSchedulerMaxRuns")}</span>
              <input
                type="number"
                min={1}
                value={schedulerMaxRunsPerJob}
                onChange={(e) => setSchedulerMaxRunsPerJob(Math.max(1, Number(e.target.value) || 1))}
                className="w-20 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-eve-text font-mono"
              />
              <span className="text-eve-dim">{t("industryLedgerSchedulerMaxHours")}</span>
              <input
                type="number"
                min={1}
                value={schedulerMaxDurationHours}
                onChange={(e) => setSchedulerMaxDurationHours(Math.max(1, Number(e.target.value) || 1))}
                className="w-20 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-eve-text font-mono"
              />
              <span className="text-eve-dim">{t("industryLedgerSchedulerQueueStatus")}</span>
              <select
                value={schedulerQueueStatus}
                onChange={(e) => setSchedulerQueueStatus(e.target.value as IndustryJobStatus)}
                className="px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-eve-text"
              >
                <option value="queued">{t("industryLedgerStatusQueued")}</option>
                <option value="planned">{t("industryLedgerStatusPlanned")}</option>
              </select>
            </>
          )}
        </div>
      </section>

      {/* Rebalance defaults */}
      <section>
        <div className="text-[10px] uppercase tracking-wider text-eve-dim mb-1.5">Rebalance defaults</div>
        <div className="flex flex-wrap items-center gap-2 text-[11px]">
          <span className="text-eve-dim">Inventory scope</span>
          <select
            value={rebalanceInventoryScope}
            onChange={(e) => setRebalanceInventoryScope(e.target.value as "single" | "all")}
            className="px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-eve-text"
          >
            <option value="single">Single project</option>
            <option value="all">All projects</option>
          </select>

          <span className="text-eve-dim">Lookback days</span>
          <input
            type="number"
            min={1}
            value={rebalanceLookbackDays}
            onChange={(e) => setRebalanceLookbackDays(Math.max(1, Number(e.target.value) || 1))}
            className="w-16 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-eve-text font-mono"
          />

          <span className="text-eve-dim">Strategy</span>
          <select
            value={rebalanceStrategy}
            onChange={(e) => setRebalanceStrategy(e.target.value as "preserve" | "buy" | "build")}
            className="px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-eve-text"
          >
            <option value="preserve">Preserve mix</option>
            <option value="buy">Prefer buy</option>
            <option value="build">Prefer build</option>
          </select>

          <span className="text-eve-dim">Warehouse</span>
          <select
            value={rebalanceWarehouseScope}
            onChange={(e) => setRebalanceWarehouseScope(e.target.value as "global" | "location_first" | "strict_location")}
            className="px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-eve-text"
          >
            <option value="global">Global</option>
            <option value="location_first">Location first</option>
            <option value="strict_location">Strict location</option>
          </select>

          <label className="flex items-center gap-1 text-eve-dim select-none">
            <input
              type="checkbox"
              checked={rebalanceUseSelectedStation}
              onChange={(e) => setRebalanceUseSelectedStation(e.target.checked)}
              className="accent-eve-accent"
            />
            Use selected station
          </label>
        </div>
      </section>

      {/* BP sync default */}
      <section>
        <div className="text-[10px] uppercase tracking-wider text-eve-dim mb-1.5">BP pool sync defaults</div>
        <div className="flex flex-wrap items-center gap-2 text-[11px]">
          <span className="text-eve-dim">Default BPC runs</span>
          <input
            type="number"
            min={1}
            value={blueprintSyncDefaultBPCRuns}
            onChange={(e) => setBlueprintSyncDefaultBPCRuns(Math.max(1, Number(e.target.value) || 1))}
            className="w-16 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-eve-text font-mono"
          />
        </div>
      </section>
    </div>
  );
}
