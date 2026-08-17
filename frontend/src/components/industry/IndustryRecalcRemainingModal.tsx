import { useCallback, useEffect, useMemo, useState } from "react";
import { Modal } from "../Modal";
import { useI18n } from "@/lib/i18n";
import {
  recalcRemainingAuthIndustryProjectMaterials,
  type IndustryProjectRecalcRemainingResponse,
} from "@/lib/api";

interface Props {
  open: boolean;
  onClose: () => void;
  projectID: number;
  /** Optional callback for surfacing warning toasts through the parent's
   *  toast system so this component can stay self-contained. If omitted,
   *  warnings render inline in the modal only. */
  onWarnings?: (warnings: string[]) => void;
}

// Non-destructive "what do I still need to procure?" view for a project.
// Recomputes materials from the unfinished jobs' blueprints + runs against
// live personal + corp inventory. Never writes back to the plan — closing
// the modal discards nothing because nothing was stored. Users who want the
// recomputed numbers baked into the plan can re-apply through the visual
// planner (out of scope here).
export function IndustryRecalcRemainingModal({ open, onClose, projectID, onWarnings }: Props) {
  const { t } = useI18n();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [data, setData] = useState<IndustryProjectRecalcRemainingResponse | null>(null);
  const [includeActiveJobs, setIncludeActiveJobs] = useState(false);
  const [copyState, setCopyState] = useState<"idle" | "copied" | "empty" | "failed">("idle");

  const fetchRecalc = useCallback(async () => {
    if (!open || projectID <= 0) return;
    setLoading(true);
    setError(null);
    setCopyState("idle");
    try {
      const resp = await recalcRemainingAuthIndustryProjectMaterials(projectID, {
        include_corp_assets: true,
        include_active_jobs: includeActiveJobs,
      });
      setData(resp);
      if (onWarnings && resp.warnings && resp.warnings.length > 0) {
        onWarnings(resp.warnings);
      }
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "recalc failed";
      setError(msg);
      setData(null);
    } finally {
      setLoading(false);
    }
  }, [open, projectID, includeActiveJobs, onWarnings]);

  useEffect(() => {
    void fetchRecalc();
  }, [fetchRecalc]);

  const totals = useMemo(() => {
    const materials = data?.materials ?? [];
    let required = 0;
    let stock = 0;
    let missing = 0;
    for (const m of materials) {
      required += m.required_qty ?? 0;
      stock += m.available_qty ?? 0;
      missing += m.missing_qty ?? 0;
    }
    return { rows: materials.length, required, stock, missing };
  }, [data]);

  const handleCopyMultibuy = useCallback(async () => {
    const materials = data?.materials ?? [];
    // EVE's Multibuy paste format is one row per line, tab-separated as
    // "Type Name<TAB>Qty". Ships accept a bare "Name Qty" too but the tab
    // is more robust across item names that contain spaces (which is
    // almost all of them). Only include rows we actually need to buy —
    // fully covered materials shouldn't clutter the paste.
    const missingRows = materials.filter((m) => (m.missing_qty ?? 0) > 0);
    if (missingRows.length === 0) {
      setCopyState("empty");
      return;
    }
    const text = missingRows.map((m) => `${m.type_name}\t${m.missing_qty}`).join("\n");
    try {
      await navigator.clipboard.writeText(text);
      setCopyState("copied");
      // Auto-revert the "copied" chip so successive copies re-trigger the
      // visual feedback.
      window.setTimeout(() => setCopyState("idle"), 2400);
    } catch {
      setCopyState("failed");
    }
  }, [data]);

  const copyDisabled = loading || !data || totals.missing <= 0;
  const copyButtonLabel = (() => {
    if (copyState === "copied") {
      return t("industryLedgerRecalcCopyMultibuyDone").replace(
        "{n}",
        String((data?.materials ?? []).filter((m) => (m.missing_qty ?? 0) > 0).length),
      );
    }
    if (copyState === "empty") return t("industryLedgerRecalcCopyMultibuyEmpty");
    if (copyState === "failed") return "Copy failed — try again";
    return t("industryLedgerRecalcCopyMultibuy");
  })();

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("industryLedgerRecalcRemainingTitle")}
      width="max-w-4xl"
    >
      <div className="p-4 space-y-3 text-sm">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="text-xs text-eve-dim">
            {t("industryLedgerRecalcRemainingSummary")
              .replace("{jobs}", String(data?.summary?.unfinished_jobs ?? 0))
              .replace("{materials}", String(totals.rows))
              .replace("{missing}", totals.missing.toLocaleString())}
          </div>
          <label className="flex items-center gap-2 text-xs text-eve-dim cursor-pointer select-none">
            <input
              type="checkbox"
              checked={includeActiveJobs}
              onChange={(e) => setIncludeActiveJobs(e.target.checked)}
              className="accent-eve-accent"
            />
            {t("industryLedgerRecalcIncludeActiveJobs")}
          </label>
        </div>

        {data?.summary?.skipped_jobs ? (
          <div className="text-[11px] text-amber-300">
            {t("industryLedgerRecalcSkipped").replace("{n}", String(data.summary.skipped_jobs))}
          </div>
        ) : null}

        {data?.warnings && data.warnings.length > 0 && (
          <div className="rounded-sm border border-amber-500/40 bg-amber-500/10 p-2 space-y-1">
            {data.warnings.map((w, i) => (
              <div key={i} className="text-[11px] text-amber-200">• {w}</div>
            ))}
          </div>
        )}

        {error && (
          <div className="rounded-sm border border-red-500/40 bg-red-500/10 p-2 text-[11px] text-red-300">
            {error}
          </div>
        )}

        {loading && !data && (
          <div className="py-6 text-center text-xs text-eve-dim">Loading…</div>
        )}

        {!loading && data && data.materials.length === 0 && (
          <div className="py-6 text-center text-xs text-eve-dim">
            {t("industryLedgerRecalcEmpty")}
          </div>
        )}

        {data && data.materials.length > 0 && (
          <>
            <div className="flex items-center justify-end gap-2">
              <button
                type="button"
                onClick={handleCopyMultibuy}
                disabled={copyDisabled}
                title={t("industryLedgerRecalcCopyMultibuyHint")}
                className="px-2.5 py-1 text-[11px] font-semibold rounded-sm border border-eve-accent text-eve-accent
                           hover:bg-eve-accent/10 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
              >
                {copyButtonLabel}
              </button>
            </div>

            <div className="overflow-x-auto">
              <table className="w-full text-xs">
                <thead>
                  <tr className="text-left text-[10px] uppercase tracking-wider text-eve-dim border-b border-eve-border">
                    <th className="px-1.5 py-1">Item</th>
                    <th className="px-1.5 py-1 text-right">Required</th>
                    <th className="px-1.5 py-1 text-right">Have</th>
                    <th className="px-1.5 py-1 text-right">Missing</th>
                  </tr>
                </thead>
                <tbody>
                  {data.materials.map((row) => {
                    const missing = row.missing_qty ?? 0;
                    return (
                      <tr key={row.type_id} className="border-b border-eve-border/30">
                        <td className="px-1.5 py-1 text-eve-text">
                          <div className="truncate">{row.type_name || `Type ${row.type_id}`}</div>
                          <div className="text-[10px] text-eve-dim">#{row.type_id}</div>
                        </td>
                        <td className="px-1.5 py-1 text-right font-mono text-eve-accent">
                          {(row.required_qty ?? 0).toLocaleString()}
                        </td>
                        <td className="px-1.5 py-1 text-right font-mono text-cyan-300">
                          {(row.available_qty ?? 0).toLocaleString()}
                        </td>
                        <td
                          className={`px-1.5 py-1 text-right font-mono ${
                            missing > 0 ? "text-red-300" : "text-eve-dim"
                          }`}
                        >
                          {missing.toLocaleString()}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </>
        )}
      </div>
    </Modal>
  );
}
