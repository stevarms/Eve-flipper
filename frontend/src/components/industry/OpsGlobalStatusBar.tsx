import { useMemo } from "react";
import type { IndustryLedger, IndustryProjectSnapshot } from "@/lib/types";
import { deriveTaskBlockStatus, type IndustryTaskDependencyBoard } from "./industryHelpers";

/**
 * Compact single-line status strip for the Operations tab. Replaces the
 * IndustryWorkspaceStatusBoards' 3 chip-strip cards with a one-line
 * "6 tasks · 2 ready · 3 soft · 1 hard · 3 active jobs · Settings ▸".
 * Answers "how am I doing overall / am I blocked?" at a glance.
 */

interface OpsGlobalStatusBarProps {
  ledgerSnapshot: IndustryProjectSnapshot | null;
  ledgerData: IndustryLedger | null;
  taskDependencyBoard: IndustryTaskDependencyBoard;
  onOpenSettings: () => void;
  settingsOpen: boolean;
}

export function OpsGlobalStatusBar({
  ledgerSnapshot,
  ledgerData,
  taskDependencyBoard,
  onOpenSettings,
  settingsOpen,
}: OpsGlobalStatusBarProps) {
  const counts = useMemo(() => {
    if (!ledgerSnapshot) {
      return { total: 0, ready: 0, soft: 0, hard: 0, unknown: 0 };
    }
    let ready = 0;
    let soft = 0;
    let hard = 0;
    let unknown = 0;
    for (const task of ledgerSnapshot.tasks) {
      if (task.status === "completed" || task.status === "cancelled") {
        continue; // completed/cancelled tasks aren't blockers — omit from block counts
      }
      const level = deriveTaskBlockStatus(task.id, ledgerSnapshot, taskDependencyBoard.parent_by_task).level;
      if (level === "ready") ready++;
      else if (level === "soft") soft++;
      else if (level === "hard") hard++;
      else unknown++;
    }
    return {
      total: ledgerSnapshot.tasks.length,
      ready,
      soft,
      hard,
      unknown,
    };
  }, [ledgerSnapshot, taskDependencyBoard.parent_by_task]);

  const activeJobs = ledgerData?.active ?? 0;

  return (
    <div className="mt-2 flex items-center flex-wrap gap-3 px-3 py-1.5 border border-eve-border/40 rounded-sm bg-eve-dark/20 text-[11px]">
      <span className="text-eve-text font-semibold">
        {counts.total} <span className="text-eve-dim font-normal">tasks</span>
      </span>
      <span className="text-eve-border">·</span>
      <span className="text-emerald-300">
        {counts.ready} <span className="text-eve-dim">ready</span>
      </span>
      <span className="text-eve-border">·</span>
      <span className="text-amber-300">
        {counts.soft} <span className="text-eve-dim">soft-blocked</span>
      </span>
      <span className="text-eve-border">·</span>
      <span className="text-red-300">
        {counts.hard} <span className="text-eve-dim">hard-blocked</span>
      </span>
      {counts.unknown > 0 && (
        <>
          <span className="text-eve-border">·</span>
          <span className="text-slate-300">
            {counts.unknown} <span className="text-eve-dim">unknown</span>
          </span>
        </>
      )}
      <span className="text-eve-border">·</span>
      <span className="text-blue-300">
        {activeJobs} <span className="text-eve-dim">active jobs</span>
      </span>

      <div className="flex-1" />

      <button
        type="button"
        onClick={onOpenSettings}
        className={`px-2 py-0.5 text-[10px] uppercase tracking-wider rounded-sm border transition-colors ${
          settingsOpen
            ? "border-eve-accent text-eve-accent bg-eve-accent/10"
            : "border-eve-border text-eve-dim hover:text-eve-text"
        }`}
        title="Scheduler + rebalance defaults"
      >
        Settings {settingsOpen ? "▾" : "▸"}
      </button>
    </div>
  );
}
