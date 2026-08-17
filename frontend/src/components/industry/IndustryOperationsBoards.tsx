import { useState, type Dispatch, type SetStateAction } from "react";
import type {
  IndustryProjectSnapshot,
  IndustryTaskStatus,
} from "@/lib/types";
import type { IndustryTaskDependencyBoard } from "./industryHelpers";
import type { IndustryJobsWorkspaceTab } from "./IndustryJobsWorkspaceNav";
import { IndustryMaterialDiffPanel } from "./IndustryMaterialDiffPanel";
import { IndustryRecalcRemainingModal } from "./IndustryRecalcRemainingModal";
import { IndustryTaskBoardPanel } from "./IndustryTaskBoardPanel";

interface IndustryOperationsBoardsContext {
  jobsWorkspaceTab: IndustryJobsWorkspaceTab;
  ledgerSnapshot: IndustryProjectSnapshot | null;
  /** Project the Recalc Remaining modal targets. When 0/unset, the recalc
   *  button in the material diff panel stays hidden. */
  selectedLedgerProjectId: number;
  /** Optional toast surface for coverage warnings from the recalc call
   *  (e.g. missing corp asset scope) — same pattern the scanner batch
   *  add-to-project flow uses. */
  addToast?: (msg: string, tone: "info" | "success" | "warning" | "error", ttlMs?: number) => void;
  rebalanceInventoryScope: "single" | "all";
  setRebalanceInventoryScope: Dispatch<SetStateAction<"single" | "all">>;
  rebalanceLookbackDays: number;
  setRebalanceLookbackDays: Dispatch<SetStateAction<number>>;
  rebalanceStrategy: "preserve" | "buy" | "build";
  setRebalanceStrategy: Dispatch<SetStateAction<"preserve" | "buy" | "build">>;
  rebalanceWarehouseScope: "global" | "location_first" | "strict_location";
  setRebalanceWarehouseScope: Dispatch<SetStateAction<"global" | "location_first" | "strict_location">>;
  blueprintSyncDefaultBPCRuns: number;
  setBlueprintSyncDefaultBPCRuns: Dispatch<SetStateAction<number>>;
  syncingLedgerBlueprintPool: boolean;
  handleSyncLedgerBlueprintPoolFromAssets: () => Promise<void>;
  rebalanceUseSelectedStation: boolean;
  setRebalanceUseSelectedStation: Dispatch<SetStateAction<boolean>>;
  handleRebalanceLedgerMaterialsFromInventory: () => Promise<void>;
  rebalancingLedgerMaterials: boolean;
  selectedLedgerTaskIDs: number[];
  bulkLedgerTaskPriority: number;
  setBulkLedgerTaskPriority: Dispatch<SetStateAction<number>>;
  handleBulkSetLedgerTaskPriority: (priority: number) => Promise<void>;
  updatingLedgerTasksBulk: boolean;
  handleBulkSetLedgerTaskStatus: (status: IndustryTaskStatus) => Promise<void>;
  setSelectedLedgerTaskIDs: Dispatch<SetStateAction<number[]>>;
  allVisibleLedgerTasksSelected: boolean;
  handleSelectAllVisibleLedgerTasks: (selected: boolean) => void;
  selectedLedgerTaskIDSet: Set<number>;
  toggleLedgerTaskSelection: (taskId: number, selected: boolean) => void;
  industryTaskStatusClass: (status: string) => string;
  formatUtcShort: (value: string) => string;
  handleSetLedgerTaskPriority: (taskId: number, priority: number) => Promise<void>;
  updatingLedgerTaskId: number;
  handleSetLedgerTaskStatus: (taskId: number, status: IndustryTaskStatus) => Promise<void>;
  taskDependencyBoard: IndustryTaskDependencyBoard;
}

interface IndustryOperationsBoardsProps {
  ctx: IndustryOperationsBoardsContext;
}

export function IndustryOperationsBoards({ ctx }: IndustryOperationsBoardsProps) {
  const {
    jobsWorkspaceTab,
    ledgerSnapshot,
    selectedLedgerProjectId,
    addToast,
    rebalanceInventoryScope,
    setRebalanceInventoryScope,
    rebalanceLookbackDays,
    setRebalanceLookbackDays,
    rebalanceStrategy,
    setRebalanceStrategy,
    rebalanceWarehouseScope,
    setRebalanceWarehouseScope,
    blueprintSyncDefaultBPCRuns,
    setBlueprintSyncDefaultBPCRuns,
    syncingLedgerBlueprintPool,
    handleSyncLedgerBlueprintPoolFromAssets,
    rebalanceUseSelectedStation,
    setRebalanceUseSelectedStation,
    handleRebalanceLedgerMaterialsFromInventory,
    rebalancingLedgerMaterials,
    selectedLedgerTaskIDs,
    bulkLedgerTaskPriority,
    setBulkLedgerTaskPriority,
    handleBulkSetLedgerTaskPriority,
    updatingLedgerTasksBulk,
    handleBulkSetLedgerTaskStatus,
    setSelectedLedgerTaskIDs,
    allVisibleLedgerTasksSelected,
    handleSelectAllVisibleLedgerTasks,
    selectedLedgerTaskIDSet,
    toggleLedgerTaskSelection,
    industryTaskStatusClass,
    formatUtcShort,
    handleSetLedgerTaskPriority,
    updatingLedgerTaskId,
    handleSetLedgerTaskStatus,
    taskDependencyBoard,
  } = ctx;

  const [recalcOpen, setRecalcOpen] = useState(false);

  return (
    <>
<IndustryMaterialDiffPanel
  jobsWorkspaceTab={jobsWorkspaceTab}
  materialRows={ledgerSnapshot?.material_diff ?? []}
  onOpenRecalcRemaining={selectedLedgerProjectId > 0 ? () => setRecalcOpen(true) : undefined}
  rebalanceInventoryScope={rebalanceInventoryScope}
  setRebalanceInventoryScope={setRebalanceInventoryScope}
  rebalanceLookbackDays={rebalanceLookbackDays}
  setRebalanceLookbackDays={setRebalanceLookbackDays}
  rebalanceStrategy={rebalanceStrategy}
  setRebalanceStrategy={setRebalanceStrategy}
  rebalanceWarehouseScope={rebalanceWarehouseScope}
  setRebalanceWarehouseScope={setRebalanceWarehouseScope}
  blueprintSyncDefaultBPCRuns={blueprintSyncDefaultBPCRuns}
  setBlueprintSyncDefaultBPCRuns={setBlueprintSyncDefaultBPCRuns}
  syncingLedgerBlueprintPool={syncingLedgerBlueprintPool}
  handleSyncLedgerBlueprintPoolFromAssets={handleSyncLedgerBlueprintPoolFromAssets}
  rebalanceUseSelectedStation={rebalanceUseSelectedStation}
  setRebalanceUseSelectedStation={setRebalanceUseSelectedStation}
  handleRebalanceLedgerMaterialsFromInventory={handleRebalanceLedgerMaterialsFromInventory}
  rebalancingLedgerMaterials={rebalancingLedgerMaterials}
/>
<IndustryTaskBoardPanel
  jobsWorkspaceTab={jobsWorkspaceTab}
  ledgerSnapshot={ledgerSnapshot}
  selectedLedgerTaskIDs={selectedLedgerTaskIDs}
  bulkLedgerTaskPriority={bulkLedgerTaskPriority}
  setBulkLedgerTaskPriority={setBulkLedgerTaskPriority}
  handleBulkSetLedgerTaskPriority={handleBulkSetLedgerTaskPriority}
  updatingLedgerTasksBulk={updatingLedgerTasksBulk}
  handleBulkSetLedgerTaskStatus={handleBulkSetLedgerTaskStatus}
  setSelectedLedgerTaskIDs={setSelectedLedgerTaskIDs}
  allVisibleLedgerTasksSelected={allVisibleLedgerTasksSelected}
  handleSelectAllVisibleLedgerTasks={handleSelectAllVisibleLedgerTasks}
  selectedLedgerTaskIDSet={selectedLedgerTaskIDSet}
  toggleLedgerTaskSelection={toggleLedgerTaskSelection}
  industryTaskStatusClass={industryTaskStatusClass}
  formatUtcShort={formatUtcShort}
  handleSetLedgerTaskPriority={handleSetLedgerTaskPriority}
  updatingLedgerTaskId={updatingLedgerTaskId}
  handleSetLedgerTaskStatus={handleSetLedgerTaskStatus}
  taskDependencyBoard={taskDependencyBoard}
/>
      <IndustryRecalcRemainingModal
        open={recalcOpen}
        onClose={() => setRecalcOpen(false)}
        projectID={selectedLedgerProjectId}
        onWarnings={addToast ? (ws) => ws.forEach((w) => addToast(w, "warning", 10000)) : undefined}
      />
    </>
  );
}
