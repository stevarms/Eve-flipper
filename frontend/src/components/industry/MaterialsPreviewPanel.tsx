import { useCallback, type ReactNode } from "react";
import type { IndustryCoverageMaterialRow } from "@/lib/types";

/**
 * Shared materials-preview drawer used by both:
 *
 *   - Discover (scanner) — the reactive preview of what a committing batch
 *     would require, driven by `previewBatch` output.
 *   - Operations — the committed-plan material_diff, wired through the
 *     `materialDiffToCoverageRows` adapter in `industryHelpers.ts`.
 *
 * The panel is fixed-position at the bottom of the viewport, resizable via
 * a top drag-handle, collapsible via the header chevron, and switches
 * between a "Full" and "Shortfall" view via a tab strip. The parent owns
 * every piece of state (tab, height, collapsed) so both callsites can
 * persist those independently to sessionStorage.
 */
export interface MaterialsPreviewPanelProps {
  /** Full materials list (post-coverage overlay). Presenter picks the tab. */
  materials: IndustryCoverageMaterialRow[];
  /** Filtered to only materials with `missing_qty > 0`. */
  shortfall: IndustryCoverageMaterialRow[];
  /** Optional async state. Operations doesn't need these — the snapshot
   *  arrives with material_diff already populated. */
  loading?: boolean;
  error?: string | null;
  collapsed: boolean;
  onToggleCollapse: () => void;
  activeTab: "full" | "shortfall";
  onTabChange: (t: "full" | "shortfall") => void;
  /** Panel max-height as vh. User-adjustable via the top drag handle.
   *  Clamped 15–80 to keep the panel above the CommitFooter (when present)
   *  and below the top nav. */
  heightVh: number;
  onHeightChange: (vh: number) => void;
  /** Header title. Defaults to "Materials preview" — Discover uses default,
   *  Operations passes "Committed materials" or similar. */
  title?: string;
  /** Right side of the header — action buttons on Operations
   *  (Rebalance / Recalc / Sync BP / Export CSV / Export Multibuy). */
  headerActions?: ReactNode;
  /** Extra bottom offset in pixels. Scanner passes 38 to stack the panel
   *  above its CommitFooter; Operations passes 0 (no footer below). */
  bottomPx?: number;
  /** Presenter-supplied friendly text for the empty state (per tab).
   *  Discover uses "select rows to preview" style; Operations uses
   *  "Nothing missing" / "No materials". Sensible defaults if omitted. */
  emptyFullMessage?: string;
  emptyShortMessage?: string;
  emptyPreloadMessage?: string;
  /** Placeholder for the header note when no data is present yet. */
  emptyHeaderNote?: string;
}

export function MaterialsPreviewPanel({
  materials,
  shortfall,
  loading = false,
  error = null,
  collapsed,
  onToggleCollapse,
  activeTab,
  onTabChange,
  heightVh,
  onHeightChange,
  title = "Materials preview",
  headerActions,
  bottomPx = 0,
  emptyFullMessage = "No materials to show.",
  emptyShortMessage = "Nothing missing.",
  emptyPreloadMessage = "Nothing to preview yet.",
  emptyHeaderNote = "nothing to preview",
}: MaterialsPreviewPanelProps) {
  const fullCount = materials.length;
  const shortCount = shortfall.length;
  const hasAnything = fullCount > 0;
  const rows = activeTab === "shortfall" ? shortfall : materials;

  // Haul volume. The m3 column answers one question — "will what I still
  // have to buy fit in the hauler" — so it is always the MISSING quantity,
  // on both tabs. The full required volume rides along in the tooltip
  // rather than making the column mean two different things per tab.
  const haulM3 = materials.reduce((sum, m) => sum + volumeOf(m, m.missing_qty ?? 0), 0);
  const totalM3 = materials.reduce((sum, m) => sum + volumeOf(m, m.required_qty ?? 0), 0);
  const unknownVolumes = materials.filter(
    (m) => !(m.unit_volume && m.unit_volume > 0) && (m.required_qty ?? 0) > 0,
  ).length;

  const headerNote = loading
    ? "computing…"
    : error
      ? `error — ${error}`
      : hasAnything
        ? `${fullCount} materials, ${shortCount} short`
        : emptyHeaderNote;

  const volumeNote = hasAnything && (haulM3 > 0 || totalM3 > 0) ? `${formatM3(haulM3)} m³ to haul` : "";
  const volumeTitle = [
    `${formatM3(haulM3)} m³ still to buy`,
    `${formatM3(totalM3)} m³ for the whole bill, owned stock included`,
    unknownVolumes > 0 ? `${unknownVolumes} material(s) have no SDE volume and are not counted` : "",
  ]
    .filter(Boolean)
    .join("\n");

  const handleResizeStart = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      const startY = e.clientY;
      const startVh = heightVh;
      const move = (ev: MouseEvent) => {
        // Drag UP → panel gets bigger. Convert pixel delta to vh so the
        // sensitivity is consistent across viewport heights.
        const deltaVh = ((startY - ev.clientY) / window.innerHeight) * 100;
        const nextVh = Math.max(15, Math.min(80, startVh + deltaVh));
        onHeightChange(nextVh);
      };
      const up = () => {
        window.removeEventListener("mousemove", move);
        window.removeEventListener("mouseup", up);
      };
      window.addEventListener("mousemove", move);
      window.addEventListener("mouseup", up);
    },
    [heightVh, onHeightChange],
  );

  return (
    <div
      className="fixed left-0 right-0 z-40 border-t border-eve-border/80
                 bg-eve-panel/95 backdrop-blur shadow-[0_-4px_12px_rgba(0,0,0,0.35)]"
      style={{ bottom: `${bottomPx}px` }}
    >
      {/* Drag handle — only when expanded (dragging a collapsed panel has
          no visible effect). */}
      {!collapsed && (
        <div
          onMouseDown={handleResizeStart}
          className="h-1.5 w-full cursor-ns-resize bg-eve-border/40 hover:bg-eve-accent/50 transition-colors"
          title="Drag to resize preview"
        />
      )}
      <div className="flex items-center justify-between px-3 py-1.5 border-b border-eve-border/40">
        <button
          type="button"
          onClick={onToggleCollapse}
          className="flex items-center gap-2 text-xs text-eve-dim hover:text-eve-text transition-colors"
          title={collapsed ? `Expand ${title.toLowerCase()}` : `Collapse ${title.toLowerCase()}`}
        >
          <span className="text-[10px]">{collapsed ? "▸" : "▾"}</span>
          <span className="text-[10px] uppercase tracking-wider">{title}</span>
          <span className="text-eve-dim">·</span>
          <span className={loading ? "text-eve-accent" : error ? "text-red-300" : "text-eve-text"}>
            {headerNote}
          </span>
          {volumeNote && (
            <>
              <span className="text-eve-dim">·</span>
              <span className="text-eve-text font-mono" title={volumeTitle}>
                {volumeNote}
              </span>
            </>
          )}
        </button>
        <div className="flex items-center gap-2">
          {!collapsed && hasAnything && (
            <div className="flex items-center gap-1">
              <button
                type="button"
                onClick={() => onTabChange("full")}
                className={`px-2 py-0.5 text-[10px] uppercase tracking-wider rounded-sm border ${
                  activeTab === "full"
                    ? "border-eve-accent text-eve-accent bg-eve-accent/10"
                    : "border-eve-border text-eve-dim hover:text-eve-text"
                }`}
              >
                Full ({fullCount})
              </button>
              <button
                type="button"
                onClick={() => onTabChange("shortfall")}
                className={`px-2 py-0.5 text-[10px] uppercase tracking-wider rounded-sm border ${
                  activeTab === "shortfall"
                    ? "border-red-500/60 text-red-300 bg-red-900/20"
                    : "border-eve-border text-eve-dim hover:text-eve-text"
                }`}
              >
                Short ({shortCount})
              </button>
            </div>
          )}
          {headerActions && <div className="flex items-center gap-1.5">{headerActions}</div>}
        </div>
      </div>
      {!collapsed && (
        <div style={{ maxHeight: `${heightVh}vh` }} className="overflow-y-auto">
          {rows.length === 0 ? (
            <div className="px-3 py-4 text-xs text-eve-dim text-center">
              {loading
                ? "Analyzing…"
                : error
                  ? "Preview unavailable — the underlying flow still works, just no pre-commit view."
                  : !hasAnything
                    ? emptyPreloadMessage
                    : activeTab === "shortfall"
                      ? emptyShortMessage
                      : emptyFullMessage}
            </div>
          ) : (
            <table className="w-full text-xs">
              <thead className="text-eve-dim bg-eve-dark/40 sticky top-0">
                <tr>
                  <th className="px-3 py-1 text-left font-normal text-[10px] uppercase tracking-wider">Material</th>
                  <th className="px-3 py-1 text-right font-normal text-[10px] uppercase tracking-wider w-24">Need</th>
                  <th className="px-3 py-1 text-right font-normal text-[10px] uppercase tracking-wider w-24">Have</th>
                  <th className="px-3 py-1 text-right font-normal text-[10px] uppercase tracking-wider w-24">Missing</th>
                  <th
                    className="px-3 py-1 text-right font-normal text-[10px] uppercase tracking-wider w-24"
                    title="Packaged volume of the quantity you still have to buy — what the hauler has to carry."
                  >
                    m³ to buy
                  </th>
                  <th className="px-3 py-1 text-right font-normal text-[10px] uppercase tracking-wider w-20">Status</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((m) => {
                  const missing = m.missing_qty ?? 0;
                  const statusColor =
                    m.status === "covered"
                      ? "text-emerald-300"
                      : m.status === "partial"
                        ? "text-amber-300"
                        : missing > 0
                          ? "text-red-300"
                          : "text-eve-dim";
                  return (
                    <tr key={m.type_id} className="border-t border-eve-border/20 hover:bg-eve-accent/5">
                      <td className="px-3 py-1 truncate">{m.type_name || `Type ${m.type_id}`}</td>
                      <td className="px-3 py-1 text-right font-mono">{Math.ceil(m.required_qty ?? 0).toLocaleString()}</td>
                      <td className="px-3 py-1 text-right font-mono text-eve-dim">
                        {Math.floor(m.available_qty ?? 0).toLocaleString()}
                      </td>
                      <td className={`px-3 py-1 text-right font-mono ${missing > 0 ? "text-red-300" : "text-eve-dim"}`}>
                        {missing > 0 ? Math.ceil(missing).toLocaleString() : "—"}
                      </td>
                      <td
                        className="px-3 py-1 text-right font-mono text-eve-dim"
                        title={
                          m.unit_volume && m.unit_volume > 0
                            ? `${m.unit_volume} m³/unit · ${formatM3(volumeOf(m, m.required_qty ?? 0))} m³ for the full ${Math.ceil(m.required_qty ?? 0).toLocaleString()}`
                            : "No packaged volume in the SDE for this type"
                        }
                      >
                        {m.unit_volume && m.unit_volume > 0
                          ? missing > 0
                            ? formatM3(volumeOf(m, missing))
                            : "—"
                          : "?"}
                      </td>
                      <td className={`px-3 py-1 text-right font-mono text-[10px] uppercase tracking-wider ${statusColor}`}>
                        {m.status || (missing > 0 ? "missing" : "covered")}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          )}
        </div>
      )}
    </div>
  );
}

/** Packaged m³ for `qty` units of a material row. Zero when the SDE has no
 *  volume for the type, so unknowns drop out of a total instead of
 *  silently counting as free to haul. */
function volumeOf(m: IndustryCoverageMaterialRow, qty: number): number {
  const unit = m.unit_volume ?? 0;
  if (unit <= 0 || qty <= 0) return 0;
  return unit * qty;
}

/** m³ reads as a capacity against a hauler's hold, so precision only
 *  matters at the small end — a 62,400 m³ freighter load does not need
 *  two decimals. */
function formatM3(v: number): string {
  if (v <= 0) return "0";
  if (v < 10) return v.toFixed(2);
  if (v < 1000) return v.toFixed(1);
  return Math.round(v).toLocaleString();
}
