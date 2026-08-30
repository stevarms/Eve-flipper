import { Fragment, useState, useEffect } from "react";
import type { SystemDanger, KillSummary } from "@/lib/types";
import { getGankCheckDetail } from "@/lib/api";
import { formatIsk } from "@/lib/format";
import { Modal } from "./Modal";

interface Props {
  systems: SystemDanger[];
  onClose: () => void;
}

/** Local presentation of the shared formatter (lib/format.ts). */
function formatISK(v: number): string {
  return formatIsk(v, undefined, {
    maxTier: "T",
    space: false,
    decimals: { t: 1, b: 1, m: 0, k: 0, unit: 0 },
  });
}

function formatTime(iso: string): string {
  try {
    const d = new Date(iso);
    const now = Date.now();
    const diffMin = Math.floor((now - d.getTime()) / 60000);
    if (diffMin < 60) return `${diffMin}m ago`;
    if (diffMin < 1440) return `${Math.floor(diffMin / 60)}h ago`;
    return d.toLocaleDateString();
  } catch {
    return iso;
  }
}

function SecBadge({ sec }: { sec: number }) {
  const cls =
    sec >= 0.5 ? "text-green-400" : sec >= 0.1 ? "text-yellow-400" : "text-red-400";
  return <span className={`font-mono tabular-nums ${cls}`}>{sec.toFixed(1)}</span>;
}

function DangerDot({ level }: { level: "green" | "yellow" | "red" }) {
  const cls = {
    green: "bg-green-400",
    yellow: "bg-yellow-400",
    red: "bg-red-400",
  }[level];
  return <span className={`inline-block w-2 h-2 rounded-full ${cls}`} />;
}

function ThreatBadge({ label, color }: { label: string; color: string }) {
  return (
    <span
      className={`inline-block text-[9px] font-bold px-1 py-0.5 rounded uppercase tracking-wider ${color}`}
    >
      {label}
    </span>
  );
}

function KillCard({ km }: { km: KillSummary }) {
  const topShips = km.AttackerShips.slice(0, 4);
  return (
    <div className="flex items-start gap-2 py-1.5 px-2 rounded hover:bg-eve-panel/60 transition-colors group">
      {/* Left: victim */}
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-1.5 flex-wrap">
          <span className="text-red-400 font-medium text-xs">{km.VictimShip || "Unknown"}</span>
          {km.IsSmartbomb && (
            <ThreatBadge label="SMARTBOMB" color="bg-orange-900/70 text-orange-300" />
          )}
          {km.IsInterdictor && (
            <ThreatBadge label="HIC/DICTOR" color="bg-blue-900/70 text-blue-300" />
          )}
        </div>
        {topShips.length > 0 && (
          <div className="text-[11px] text-eve-dim mt-0.5">
            <span className="text-eve-dim/60">by </span>
            {topShips.join(", ")}
            {km.AttackerCount > topShips.length && (
              <span className="text-eve-dim/50"> +{km.AttackerCount - topShips.length}</span>
            )}
          </div>
        )}
      </div>
      {/* Right: meta */}
      <div className="flex flex-col items-end gap-0.5 shrink-0">
        {km.ISKValue > 0 && (
          <span className="text-eve-accent font-mono text-xs">{formatISK(km.ISKValue)}</span>
        )}
        <div className="flex items-center gap-1.5">
          {km.KillTime && (
            <span className="text-eve-dim/60 text-[10px]">{formatTime(km.KillTime)}</span>
          )}
          <a
            href={km.ZKBLink}
            target="_blank"
            rel="noopener noreferrer"
            className="text-[10px] text-eve-accent/70 hover:text-eve-accent transition-colors"
            onClick={(e) => e.stopPropagation()}
          >
            zKB ↗
          </a>
        </div>
      </div>
    </div>
  );
}

function SystemRow({
  s,
  details,
  onExpand,
}: {
  s: SystemDanger;
  details: KillSummary[] | "loading" | undefined;
  onExpand: () => void;
}) {
  const isExpanded = details && details !== "loading";
  const isDangerous = s.KillsTotal > 0;

  return (
    <Fragment>
      <tr
        className={`border-b border-eve-border text-xs transition-colors ${
          isDangerous
            ? "bg-red-950/20 hover:bg-red-950/30 cursor-pointer"
            : "hover:bg-eve-panel/30"
        }`}
        onClick={() => isDangerous && onExpand()}
      >
        {/* System name + threats */}
        <td className="px-3 py-2">
          <div className="flex items-center gap-2">
            <DangerDot level={s.DangerLevel} />
            <span className={`font-medium ${isDangerous ? "text-eve-text" : "text-eve-dim"}`}>
              {s.SystemName}
            </span>
            <div className="flex gap-1">
              {s.IsSmartbomb && (
                <ThreatBadge label="SB" color="bg-orange-900/70 text-orange-300" />
              )}
              {s.IsInterdictor && (
                <ThreatBadge label="HIC" color="bg-blue-900/70 text-blue-300" />
              )}
            </div>
          </div>
        </td>
        {/* Security */}
        <td className="px-3 py-2 text-center">
          <SecBadge sec={s.Security} />
        </td>
        {/* Kills */}
        <td className="px-3 py-2 text-center font-mono tabular-nums">
          {s.KillsTotal > 0 ? (
            <span
              className={
                s.DangerLevel === "red"
                  ? "text-red-400 font-semibold"
                  : "text-yellow-400"
              }
            >
              {s.KillsTotal}
            </span>
          ) : (
            <span className="text-eve-dim/40">—</span>
          )}
        </td>
        {/* ISK */}
        <td className="px-3 py-2 text-right font-mono tabular-nums text-eve-accent/80">
          {s.TotalISK > 0 ? formatISK(s.TotalISK) : (
            <span className="text-eve-dim/40">—</span>
          )}
        </td>
        {/* Expand arrow */}
        <td className="px-2 py-2 text-center w-6">
          {isDangerous && (
            <span
              className={`text-eve-dim/60 text-[10px] transition-transform inline-block ${
                isExpanded ? "rotate-90" : ""
              }`}
            >
              ▶
            </span>
          )}
        </td>
      </tr>

      {/* Loading state */}
      {details === "loading" && (
        <tr className="border-b border-eve-border/50 bg-eve-panel/20">
          <td colSpan={5} className="px-3 py-2 pl-9 text-eve-dim text-[11px]">
            Loading kill details…
          </td>
        </tr>
      )}

      {/* Kill cards */}
      {isExpanded && (details as KillSummary[]).length > 0 && (
        <tr className="border-b border-eve-border/50 bg-eve-panel/20">
          <td colSpan={5} className="px-2 pb-1 pt-0">
            <div className="pl-6 border-l border-red-900/40 ml-3 mt-1">
              {(details as KillSummary[]).map((km) => (
                <KillCard key={km.KillmailID} km={km} />
              ))}
            </div>
          </td>
        </tr>
      )}
    </Fragment>
  );
}

export function RouteSafetyModal({ systems, onClose }: Props) {
  const [details, setDetails] = useState<Record<number, KillSummary[] | "loading">>({});

  // Auto-expand red systems on open
  useEffect(() => {
    systems.forEach((s) => {
      if (s.DangerLevel === "red") loadDetail(s.SystemID);
    });
  }, []);

  const loadDetail = (systemID: number) => {
    if (details[systemID]) return;
    setDetails((d) => ({ ...d, [systemID]: "loading" }));
    getGankCheckDetail(systemID).then((data) => {
      setDetails((d) => ({ ...d, [systemID]: data }));
    });
  };

  const toggleDetail = (systemID: number) => {
    if (details[systemID] && details[systemID] !== "loading") {
      setDetails((d) => {
        const next = { ...d };
        delete next[systemID];
        return next;
      });
    } else {
      loadDetail(systemID);
    }
  };

  // Summary stats
  const totalKills = systems.reduce((s, x) => s + x.KillsTotal, 0);
  const dangerousSystems = systems.filter((s) => s.KillsTotal > 0).length;
  const totalISK = systems.reduce((s, x) => s + (x.TotalISK ?? 0), 0);
  const hasSmartbomb = systems.some((s) => s.IsSmartbomb);
  const hasInterdictor = systems.some((s) => s.IsInterdictor);
  const overallDanger = systems.reduce<"green" | "yellow" | "red">((worst, s) => {
    if (s.DangerLevel === "red") return "red";
    if (s.DangerLevel === "yellow" && worst === "green") return "yellow";
    return worst;
  }, "green");

  return (
    <Modal open onClose={onClose} title="Route Safety — Last Hour" width="max-w-2xl">
      {/* Summary bar */}
      <div className="px-3 py-2.5 border-b border-eve-border bg-eve-panel/50 flex items-center gap-4 flex-wrap text-xs">
        <div className="flex items-center gap-1.5">
          <DangerDot level={overallDanger} />
          <span className="text-eve-dim">{systems.length} systems</span>
        </div>
        {dangerousSystems > 0 ? (
          <span className="text-red-400 font-medium">{dangerousSystems} dangerous</span>
        ) : (
          <span className="text-green-400">Route clear</span>
        )}
        {totalKills > 0 && (
          <span className="text-eve-dim">{totalKills} kills</span>
        )}
        {totalISK > 0 && (
          <span className="text-eve-accent font-mono">{formatISK(totalISK)} destroyed</span>
        )}
        {hasSmartbomb && (
          <ThreatBadge label="SMARTBOMBS ACTIVE" color="bg-orange-900/70 text-orange-300" />
        )}
        {hasInterdictor && (
          <ThreatBadge label="HIC/DICTOR" color="bg-blue-900/70 text-blue-300" />
        )}
      </div>

      {/* Table */}
      <table className="w-full text-xs">
        <thead>
          <tr className="border-b border-eve-border text-eve-dim">
            <th className="text-left px-3 py-2 font-medium">System</th>
            <th className="text-center px-3 py-2 font-medium">Sec</th>
            <th className="text-center px-3 py-2 font-medium">Kills</th>
            <th className="text-right px-3 py-2 font-medium">ISK lost</th>
            <th className="w-6" />
          </tr>
        </thead>
        <tbody>
          {systems.map((s) => (
            <SystemRow
              key={s.SystemID}
              s={s}
              details={details[s.SystemID]}
              onExpand={() => toggleDetail(s.SystemID)}
            />
          ))}
        </tbody>
      </table>
    </Modal>
  );
}
