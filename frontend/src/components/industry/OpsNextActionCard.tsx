import { useMemo, useState } from "react";
import { formatISK } from "@/lib/format";
import type {
  IndustryBlueprintPoolRecord,
  IndustryProjectSnapshot,
  IndustryTaskRecord,
  IndustryTaskStatus,
} from "@/lib/types";
import {
  deriveTaskBlockStatus,
  formatDuration,
  sortOperationsTasks,
  splitTaskDecryptorSuffix,
  taskConstraintNumber,
  taskConstraintRecord,
  type IndustryTaskDependencyBoard,
  type TaskBlockLevel,
  type TaskBlockStatus,
} from "./industryHelpers";

/**
 * "Do this next" card at the top of Operations.
 *
 * The task list below answers "what does the whole plan look like"; this
 * answers "what do I click in EVE right now". It picks the first task in
 * canonical Operations order that isn't finished, prefers one whose
 * materials are actually on hand, and spells out every field the in-game
 * industry window asks for — blueprint, activity, runs, ME/TE, decryptor,
 * facility — each with a copy button so nothing has to be retyped.
 */

// Two genuinely different states, and conflating them is how the card ends
// up lying: either there is something to install right now, or there isn't
// but jobs are still cooking in EVE.
type NextAction =
  | {
      kind: "task";
      task: IndustryTaskRecord;
      block: TaskBlockStatus;
      stepIndex: number;
      totalSteps: number;
      doneSteps: number;
    }
  | { kind: "none"; inFlight: number; doneSteps: number; totalSteps: number };

interface OpsNextActionCardProps {
  ledgerSnapshot: IndustryProjectSnapshot | null;
  taskDependencyBoard: IndustryTaskDependencyBoard;
  handleSetLedgerTaskStatus: (taskId: number, status: IndustryTaskStatus) => Promise<void>;
  updatingLedgerTaskId: number;
  updatingLedgerTasksBulk: boolean;
}

// EVE's own verbs for the industry window, so the card reads like the UI
// the user is about to operate.
const ACTIVITY_VERB: Record<string, string> = {
  copy: "Copy blueprint",
  invention: "Invent",
  manufacturing: "Manufacture",
  reaction: "React",
  research_material: "Research material efficiency",
  research_time: "Research time efficiency",
};

function CopyField({
  label,
  value,
  mono = false,
  accent = "text-eve-text",
}: {
  label: string;
  value: string;
  mono?: boolean;
  accent?: string;
}) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    // Clipboard access can be denied (insecure origin, permissions). Failing
    // to flash "copied" is the whole error path — the value is still on
    // screen to read off manually.
    void navigator.clipboard
      .writeText(value)
      .then(() => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1200);
      })
      .catch(() => undefined);
  };
  return (
    <div className="flex flex-col gap-0.5 min-w-0">
      <span className="text-[9px] uppercase tracking-wider text-eve-dim">{label}</span>
      <button
        type="button"
        onClick={copy}
        title={`Copy "${value}"`}
        className="group inline-flex items-center gap-1 text-left min-w-0"
      >
        <span className={`truncate text-[12px] ${mono ? "font-mono" : ""} ${accent}`}>{value}</span>
        <span
          className={`text-[9px] shrink-0 ${copied ? "text-emerald-300" : "text-eve-dim group-hover:text-eve-accent"}`}
        >
          {copied ? "✓" : "⧉"}
        </span>
      </button>
    </div>
  );
}

export function OpsNextActionCard({
  ledgerSnapshot,
  taskDependencyBoard,
  handleSetLedgerTaskStatus,
  updatingLedgerTaskId,
  updatingLedgerTasksBulk,
}: OpsNextActionCardProps) {
  const picked = useMemo((): NextAction | null => {
    if (!ledgerSnapshot) return null;
    const ordered = sortOperationsTasks(ledgerSnapshot.tasks, taskDependencyBoard.depth_by_task);
    const finished = (tk: IndustryTaskRecord) => tk.status === "completed" || tk.status === "cancelled";
    // "active" means the job is installed and running in EVE — hours of wall
    // clock away from needing attention. It is outstanding but it is NOT the
    // next thing to install, so it can't be the card's candidate: otherwise
    // hitting "Started it" leaves the card parked on the job you just queued,
    // which is useless when you're filling slots across nine characters.
    const installable = ordered.filter((tk) => !finished(tk) && tk.status !== "active");
    const doneSteps = ordered.filter(finished).length;
    const totalSteps = ordered.length;
    if (installable.length === 0) {
      // Everything left is already running. Distinguish that from a genuinely
      // finished project — claiming "nothing left to build" while nine jobs
      // are in the oven would be a lie.
      return { kind: "none", inFlight: totalSteps - doneSteps, doneSteps, totalSteps };
    }

    const levelOf = (tk: IndustryTaskRecord): TaskBlockLevel =>
      deriveTaskBlockStatus(tk.id, ledgerSnapshot, taskDependencyBoard.prereqs_by_task).level;

    // Prefer work that can actually start: fully-stocked first, then
    // partially-stocked (a partial build still makes progress), and only
    // then fall back to the head of the queue so the card never goes blank
    // while there's installable work.
    const byLevel = (want: TaskBlockLevel) => installable.find((tk) => levelOf(tk) === want);
    const task = byLevel("ready") ?? byLevel("soft") ?? installable[0];
    return {
      kind: "task",
      task,
      block: deriveTaskBlockStatus(task.id, ledgerSnapshot, taskDependencyBoard.prereqs_by_task),
      stepIndex: ordered.findIndex((tk) => tk.id === task.id) + 1,
      totalSteps,
      doneSteps,
    };
  }, [ledgerSnapshot, taskDependencyBoard.depth_by_task, taskDependencyBoard.prereqs_by_task]);

  // Jobs bound to the picked task. Auto-split turns one long task into
  // several install-sized jobs, and the card has to say how many separate
  // installs that is — "141 runs" is not something EVE lets you queue in
  // one go.
  const taskJobs = useMemo(() => {
    if (picked?.kind !== "task") return [];
    const taskID = picked.task.id;
    return (ledgerSnapshot?.jobs ?? []).filter(
      (j) => j.task_id === taskID && j.status !== "cancelled" && j.status !== "completed",
    );
  }, [ledgerSnapshot, picked]);

  const blueprintByTypeID = useMemo(() => {
    const map = new Map<number, IndustryBlueprintPoolRecord>();
    for (const bp of ledgerSnapshot?.blueprints ?? []) map.set(bp.blueprint_type_id, bp);
    return map;
  }, [ledgerSnapshot]);

  if (!ledgerSnapshot) return null;

  if (!picked || picked.kind === "none") {
    // Nothing installable. Either the project is genuinely done, or every
    // remaining step is already running in EVE and there's simply nothing to
    // queue until one of them pops.
    const inFlight = picked?.inFlight ?? 0;
    if (inFlight > 0) {
      return (
        <div className="mt-2 px-3 py-2 border border-sky-500/40 rounded-sm bg-sky-900/10 text-[12px] text-sky-300">
          <span className="font-mono">{inFlight}</span> job{inFlight === 1 ? "" : "s"} in flight —
          nothing new to install. Mark one complete when it pops and the next step appears here.
        </div>
      );
    }
    return (
      <div className="mt-2 px-3 py-2 border border-emerald-500/40 rounded-sm bg-emerald-900/10 text-[12px] text-emerald-300">
        Every task in this project is complete. Nothing left to build.
      </div>
    );
  }

  const { task, block, stepIndex, totalSteps, doneSteps } = picked;
  const level = block.level;
  const constraints = taskConstraintRecord(task.constraints);
  const bpTypeID = taskConstraintNumber(task.constraints, "blueprint_type_id");
  const bpRecord = bpTypeID > 0 ? blueprintByTypeID.get(bpTypeID) : undefined;
  const bpName = bpRecord?.blueprint_name || (bpTypeID > 0 ? `Type ${bpTypeID}` : "");
  const me = taskConstraintNumber(task.constraints, "me");
  const te = taskConstraintNumber(task.constraints, "te");
  const systemName = typeof constraints.system_name === "string" ? constraints.system_name : "";
  const runs = task.target_runs || 0;
  const perRunSeconds = taskConstraintNumber(task.constraints, "duration_seconds_per_run");
  const perRunCost = taskConstraintNumber(task.constraints, "cost_isk_per_run");
  const { base, decryptor } = splitTaskDecryptorSuffix(task.name || "", task.activity);
  const verb = ACTIVITY_VERB[task.activity] ?? task.activity;
  const busy = updatingLedgerTaskId === task.id || updatingLedgerTasksBulk;

  // ME/TE only apply to jobs run off a blueprint's efficiency; invention and
  // copying ignore them, and showing 0/0 there just invites the user to go
  // hunting for a setting that doesn't exist.
  const showEfficiency = task.activity === "manufacturing" || task.activity === "reaction";

  const levelChrome =
    level === "ready"
      ? {
          border: "border-emerald-500/50",
          bg: "bg-emerald-900/10",
          text: "text-emerald-300",
          label: "READY TO START",
        }
      : level === "soft"
        ? {
            border: "border-amber-500/50",
            bg: "bg-amber-900/10",
            text: "text-amber-300",
            // An unfinished upstream step is the more useful headline when
            // both apply — materials can be bought, a pending invention can't.
            label: block.prereqPending > 0 ? "PARTIAL — PREREQ PENDING" : "PARTIAL — SHORT ON MATERIALS",
          }
        : level === "hard"
          ? { border: "border-red-500/50", bg: "bg-red-900/10", text: "text-red-300", label: "BLOCKED" }
          : {
              border: "border-slate-500/50",
              bg: "bg-slate-800/20",
              text: "text-slate-300",
              label: "NO MATERIAL PLAN",
            };

  return (
    <div className={`mt-2 border rounded-sm ${levelChrome.border} ${levelChrome.bg}`}>
      <div className="flex items-center flex-wrap gap-2 px-3 py-1.5 border-b border-eve-border/30">
        <span className="text-[10px] uppercase tracking-wider text-eve-dim">Do this next</span>
        <span
          className={`px-1.5 py-0.5 text-[10px] uppercase rounded-sm border font-bold ${levelChrome.border} ${levelChrome.text}`}
        >
          {levelChrome.label}
        </span>
        <span className="text-eve-border">·</span>
        <span className="text-[11px] text-eve-dim">
          step <span className="font-mono text-eve-text">{stepIndex}</span> of{" "}
          <span className="font-mono text-eve-text">{totalSteps}</span>
          {doneSteps > 0 && <span className="ml-1">({doneSteps} done)</span>}
        </span>

        <div className="flex-1" />

        {task.status !== "active" && (
          <button
            type="button"
            onClick={() => {
              void handleSetLedgerTaskStatus(task.id, "active");
            }}
            disabled={busy}
            className="px-2 py-0.5 text-[10px] uppercase tracking-wider border border-blue-500/40 text-blue-300 rounded-sm hover:bg-blue-500/10 disabled:opacity-50"
          >
            Started it
          </button>
        )}
        <button
          type="button"
          onClick={() => {
            void handleSetLedgerTaskStatus(task.id, "completed");
          }}
          disabled={busy}
          className="px-2 py-0.5 text-[10px] uppercase tracking-wider border border-emerald-500/40 text-emerald-300 rounded-sm hover:bg-emerald-500/10 disabled:opacity-50"
        >
          Done
        </button>
      </div>

      <div className="px-3 py-2">
        <div className="text-[13px] text-eve-text font-semibold">
          {verb}
          {base ? <span className="text-eve-dim font-normal"> — {base}</span> : null}
        </div>

        <div className="mt-2 grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-x-4 gap-y-2">
          {bpName && <CopyField label="Blueprint" value={bpName} />}
          <CopyField label="Runs" value={String(runs)} mono accent="text-eve-accent" />
          {showEfficiency && (
            <div className="flex flex-col gap-0.5">
              <span className="text-[9px] uppercase tracking-wider text-eve-dim">ME / TE</span>
              <span className="text-[12px] font-mono text-eve-text">
                {me} / {te}
              </span>
            </div>
          )}
          {decryptor && <CopyField label="Decryptor" value={decryptor} accent="text-sky-300" />}
          {systemName && <CopyField label="System" value={systemName} />}
          <div className="flex flex-col gap-0.5">
            <span className="text-[9px] uppercase tracking-wider text-eve-dim">Est. duration</span>
            <span className="text-[12px] font-mono text-eve-text">
              {perRunSeconds > 0 && runs > 0 ? formatDuration(perRunSeconds * runs) : "—"}
            </span>
          </div>
          <div className="flex flex-col gap-0.5">
            <span className="text-[9px] uppercase tracking-wider text-eve-dim">Est. job fee</span>
            <span className="text-[12px] font-mono text-eve-text">
              {perRunCost > 0 && runs > 0 ? formatISK(perRunCost * runs) : "—"}
            </span>
          </div>
        </div>

        {taskJobs.length > 1 && (
          <div className="mt-2 text-[11px] text-eve-dim">
            Queue <span className="font-mono text-eve-text">{taskJobs.length}</span> separate jobs:{" "}
            <span className="font-mono text-eve-accent">
              {taskJobs.map((j) => j.runs).join(" + ")}
            </span>{" "}
            runs
          </div>
        )}

        {level !== "ready" && block.reason && (
          <div className={`mt-2 text-[11px] ${levelChrome.text}`}>{block.reason}</div>
        )}
      </div>
    </div>
  );
}
