import { useCallback, useEffect, useMemo, useState } from "react";
import { useI18n } from "@/lib/i18n";
import {
  deleteAuthIndustryProject,
  getAuthIndustryProjects,
  updateAuthIndustryProjectStatus,
} from "@/lib/api";
import type { IndustryProject } from "@/lib/types";
import { Modal } from "../Modal";

// The Guide sub-tab used to be a 4-step progress meter for a single selected
// project — its info is already surfaced in the Planner and Ops sub-tabs, so
// we repurpose it as a workshop dashboard: every project the user has, with
// progress bars, blocker counts, and per-row Open / Archive / Delete.

interface Props {
  currentProjectID: number;
  refreshCounter: number; // bump to force a reload
  onOpen: (projectID: number) => void;
  onProjectDeleted: (projectID: number) => void;
  isLoggedIn: boolean;
}

type StatusFilter = "active" | "archived" | "all";

function formatUpdated(iso: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

function StatusPill({ status }: { status: string }) {
  const tone: Record<string, string> = {
    draft: "border-slate-500/40 text-slate-300 bg-slate-500/10",
    planned: "border-cyan-500/40 text-cyan-300 bg-cyan-500/10",
    active: "border-emerald-500/40 text-emerald-300 bg-emerald-500/10",
    completed: "border-blue-500/40 text-blue-300 bg-blue-500/10",
    archived: "border-eve-border text-eve-dim bg-eve-dark/40",
  };
  const cls = tone[status] ?? tone.draft;
  return (
    <span className={`px-1.5 py-0.5 rounded-sm border text-[10px] uppercase tracking-wider ${cls}`}>
      {status || "draft"}
    </span>
  );
}

function ProgressBar({ done, total, tone }: { done: number; total: number; tone: "task" | "job" }) {
  const pct = total > 0 ? Math.min(100, Math.round((done / total) * 100)) : 0;
  const barTone = tone === "task"
    ? "bg-cyan-400/60"
    : "bg-emerald-400/60";
  return (
    <div className="flex items-center gap-1.5 min-w-[6rem]">
      <div className="flex-1 h-1.5 rounded-sm overflow-hidden bg-eve-dark/60">
        <div className={`h-full ${barTone}`} style={{ width: `${pct}%` }} />
      </div>
      <span className="text-[10px] text-eve-dim font-mono tabular-nums">
        {done}/{total}
      </span>
    </div>
  );
}

export function IndustryJobsGuidePanel({
  currentProjectID,
  refreshCounter,
  onOpen,
  onProjectDeleted,
  isLoggedIn,
}: Props) {
  const { t } = useI18n();
  const [projects, setProjects] = useState<IndustryProject[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("active");
  const [busyProjectID, setBusyProjectID] = useState<number>(0);
  const [deleteTarget, setDeleteTarget] = useState<IndustryProject | null>(null);
  const [deleteConfirmText, setDeleteConfirmText] = useState("");
  const [deleting, setDeleting] = useState(false);

  const loadProjects = useCallback(async () => {
    if (!isLoggedIn) {
      setProjects([]);
      return;
    }
    setLoading(true);
    setError(null);
    try {
      // Server-side status filter is skipped so the client can toggle
      // active/archived/all without a round-trip.
      const resp = await getAuthIndustryProjects({ limit: 200 });
      setProjects(resp.projects);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to load projects");
    } finally {
      setLoading(false);
    }
  }, [isLoggedIn]);

  useEffect(() => {
    void loadProjects();
  }, [loadProjects, refreshCounter]);

  const visibleProjects = useMemo(() => {
    if (statusFilter === "all") return projects;
    if (statusFilter === "archived") return projects.filter((p) => p.status === "archived");
    return projects.filter((p) => p.status !== "archived");
  }, [projects, statusFilter]);

  const handleArchiveToggle = useCallback(
    async (project: IndustryProject) => {
      const next = project.status === "archived" ? "active" : "archived";
      setBusyProjectID(project.id);
      try {
        await updateAuthIndustryProjectStatus(project.id, next);
        await loadProjects();
      } catch (e: unknown) {
        setError(e instanceof Error ? e.message : "Update status failed");
      } finally {
        setBusyProjectID(0);
      }
    },
    [loadProjects],
  );

  const openDeleteModal = useCallback((project: IndustryProject) => {
    setDeleteTarget(project);
    setDeleteConfirmText("");
  }, []);

  const cancelDelete = useCallback(() => {
    setDeleteTarget(null);
    setDeleteConfirmText("");
    setDeleting(false);
  }, []);

  const confirmDelete = useCallback(async () => {
    if (!deleteTarget || deleteConfirmText !== deleteTarget.name) return;
    setDeleting(true);
    setBusyProjectID(deleteTarget.id);
    try {
      await deleteAuthIndustryProject(deleteTarget.id);
      const deletedID = deleteTarget.id;
      cancelDelete();
      await loadProjects();
      onProjectDeleted(deletedID);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Delete failed");
      setDeleting(false);
    } finally {
      setBusyProjectID(0);
    }
  }, [deleteTarget, deleteConfirmText, cancelDelete, loadProjects, onProjectDeleted]);

  if (!isLoggedIn) {
    return (
      <div className="mt-2 border border-eve-border rounded-sm p-3 text-xs text-eve-dim bg-eve-panel">
        {t("industryProjectsOverviewLoginRequired")}
      </div>
    );
  }

  return (
    <div className="mt-2 border border-emerald-500/30 rounded-sm p-2 bg-emerald-500/5">
      <div className="flex flex-wrap items-center justify-between gap-2 mb-2">
        <div>
          <div className="text-[10px] uppercase tracking-wider text-emerald-300">
            {t("industryProjectsOverviewTitle")}
          </div>
          <div className="text-[11px] text-eve-dim">
            {t("industryProjectsOverviewIntro")}
          </div>
        </div>
        <div className="inline-flex rounded-sm border border-eve-border overflow-hidden">
          {(["active", "archived", "all"] as StatusFilter[]).map((k) => (
            <button
              key={k}
              type="button"
              onClick={() => setStatusFilter(k)}
              className={`px-2 py-1 text-[10px] uppercase tracking-wider transition-colors ${
                statusFilter === k
                  ? "bg-eve-accent/20 text-eve-accent"
                  : "text-eve-dim hover:text-eve-text"
              }`}
            >
              {t(`industryProjectsFilter_${k}` as never)}
            </button>
          ))}
        </div>
      </div>

      {error && (
        <div className="mb-2 text-[11px] text-red-300">{error}</div>
      )}

      {loading ? (
        <div className="text-[11px] text-eve-dim">{t("industryProjectsLoading")}</div>
      ) : visibleProjects.length === 0 ? (
        <div className="text-[11px] text-eve-dim py-4 text-center">
          {t("industryProjectsOverviewEmpty")}
        </div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-[11px]">
            <thead className="text-eve-dim">
              <tr>
                <th className="px-2 py-1 text-left">{t("industryProjectsColName")}</th>
                <th className="px-2 py-1 text-left">{t("industryProjectsColStatus")}</th>
                <th className="px-2 py-1 text-left">{t("industryProjectsColTasks")}</th>
                <th className="px-2 py-1 text-left">{t("industryProjectsColJobs")}</th>
                <th className="px-2 py-1 text-right">{t("industryProjectsColMatsToBuy")}</th>
                <th className="px-2 py-1 text-right">{t("industryProjectsColBPMissing")}</th>
                <th className="px-2 py-1 text-left">{t("industryProjectsColUpdated")}</th>
                <th className="px-2 py-1 text-right">{t("industryProjectsColActions")}</th>
              </tr>
            </thead>
            <tbody>
              {visibleProjects.map((project) => {
                const isCurrent = project.id === currentProjectID;
                const busy = busyProjectID === project.id;
                const archived = project.status === "archived";
                return (
                  <tr
                    key={project.id}
                    className={`border-t border-eve-border/30 cursor-pointer hover:bg-eve-accent/5 ${
                      isCurrent ? "bg-eve-accent/10" : ""
                    }`}
                    onClick={() => onOpen(project.id)}
                  >
                    <td className="px-2 py-1 font-medium text-eve-text">
                      {project.name}
                    </td>
                    <td className="px-2 py-1">
                      <StatusPill status={project.status} />
                    </td>
                    <td className="px-2 py-1">
                      <ProgressBar
                        done={project.tasks_done ?? 0}
                        total={project.tasks_total ?? 0}
                        tone="task"
                      />
                    </td>
                    <td className="px-2 py-1">
                      <ProgressBar
                        done={project.jobs_done ?? 0}
                        total={project.jobs_total ?? 0}
                        tone="job"
                      />
                    </td>
                    <td className="px-2 py-1 text-right font-mono">
                      {(project.materials_to_buy ?? 0) > 0 ? (
                        <span className="text-amber-300">{project.materials_to_buy}</span>
                      ) : (
                        <span className="text-eve-dim">—</span>
                      )}
                    </td>
                    <td className="px-2 py-1 text-right font-mono">
                      {(project.blueprints_missing ?? 0) > 0 ? (
                        <span className="text-red-300">{project.blueprints_missing}</span>
                      ) : (
                        <span className="text-eve-dim">—</span>
                      )}
                    </td>
                    <td className="px-2 py-1 text-eve-dim">
                      {formatUpdated(project.updated_at)}
                    </td>
                    <td className="px-2 py-1 text-right whitespace-nowrap" onClick={(e) => e.stopPropagation()}>
                      <button
                        type="button"
                        onClick={() => onOpen(project.id)}
                        className="px-1.5 py-0.5 text-[10px] rounded-sm border border-cyan-500/40 text-cyan-300 hover:bg-cyan-500/10"
                      >
                        {t("industryProjectOpenAction")}
                      </button>{" "}
                      <button
                        type="button"
                        onClick={() => handleArchiveToggle(project)}
                        disabled={busy}
                        className="px-1.5 py-0.5 text-[10px] rounded-sm border border-eve-border text-eve-dim hover:text-eve-text hover:border-eve-border/80 disabled:opacity-40"
                      >
                        {archived
                          ? t("industryProjectRestoreAction")
                          : t("industryProjectArchiveAction")}
                      </button>{" "}
                      <button
                        type="button"
                        onClick={() => openDeleteModal(project)}
                        disabled={busy}
                        className="px-1.5 py-0.5 text-[10px] rounded-sm border border-red-500/40 text-red-300 hover:bg-red-500/10 disabled:opacity-40"
                      >
                        {t("industryProjectDeleteAction")}
                      </button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* Type-name-to-confirm modal so a stray click doesn't nuke a plan. */}
      <Modal
        open={deleteTarget !== null}
        onClose={cancelDelete}
        title={t("industryProjectDeleteConfirmTitle")}
        width="max-w-md"
      >
        <div className="p-4 space-y-3 text-sm">
          <div className="text-xs">
            {t("industryProjectDeleteConfirmBody").replace(
              "{name}",
              deleteTarget?.name || "",
            )}
          </div>
          <div>
            <label className="block text-[11px] uppercase tracking-wider text-eve-dim mb-1">
              {t("industryProjectDeleteTypeToConfirm").replace(
                "{name}",
                deleteTarget?.name || "",
              )}
            </label>
            <input
              type="text"
              value={deleteConfirmText}
              onChange={(e) => setDeleteConfirmText(e.target.value)}
              autoFocus
              className="w-full px-3 py-1.5 bg-eve-input border border-eve-border rounded-sm text-eve-text text-sm
                         focus:outline-none focus:border-red-400 focus:ring-1 focus:ring-red-400/30"
            />
          </div>
          <div className="flex items-center justify-end gap-2 pt-2 border-t border-eve-border/40">
            <button
              type="button"
              onClick={cancelDelete}
              disabled={deleting}
              className="px-3 py-1.5 text-xs rounded-sm border border-eve-border text-eve-dim hover:text-eve-text disabled:opacity-50"
            >
              {t("cancel")}
            </button>
            <button
              type="button"
              onClick={confirmDelete}
              disabled={deleting || !deleteTarget || deleteConfirmText !== deleteTarget.name}
              className="px-3 py-1.5 text-xs font-semibold rounded-sm border border-red-500/60 text-red-300 hover:bg-red-500/10 disabled:opacity-40 disabled:cursor-not-allowed"
            >
              {deleting ? "..." : t("industryProjectDeleteAction")}
            </button>
          </div>
        </div>
      </Modal>
    </div>
  );
}
