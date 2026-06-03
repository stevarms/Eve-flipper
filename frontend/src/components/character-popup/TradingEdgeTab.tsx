import { useEffect, useState } from "react";
import { getTradingEdgeSummary } from "../../lib/api";
import type { TradingEdgeRow, TradingEdgeSummary } from "../../lib/types";
import { StatCard } from "./shared";

interface TradingEdgeTabProps {
  enabled: boolean;
  onToggleEnabled: (enabled: boolean) => void;
  formatIsk: (v: number) => string;
}

const labelTone: Record<string, string> = {
  good_edge: "text-eve-profit",
  needs_bigger_margin: "text-eve-warning",
  do_not_trade: "text-eve-error",
  watch: "text-eve-accent",
  insufficient_data: "text-eve-dim",
};

const labelText: Record<string, string> = {
  good_edge: "Good edge",
  needs_bigger_margin: "Needs margin",
  do_not_trade: "Do not trade",
  watch: "Watch",
  insufficient_data: "No data",
};

export function TradingEdgeTab({ enabled, onToggleEnabled, formatIsk }: TradingEdgeTabProps) {
  const [data, setData] = useState<TradingEdgeSummary | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    setLoading(true);
    setError("");
    getTradingEdgeSummary()
      .then((next) => {
        if (!cancelled) setData(next);
      })
      .catch((e) => {
        if (!cancelled) setError(e instanceof Error ? e.message : "failed to load Trading Edge");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [enabled]);

  if (!enabled) {
    return (
      <div className="h-full flex items-center justify-center">
        <div className="max-w-xl border border-eve-border bg-eve-panel/80 p-5 text-center space-y-3">
          <div className="text-xs uppercase tracking-wider text-eve-accent">Trading Edge Engine is disabled</div>
          <p className="text-sm text-eve-dim leading-relaxed">
            This tool learns from reconciled journal trades and compares expected PnL with real PnL. Enable it if you want personal item/category recommendations.
          </p>
          <button
            type="button"
            onClick={() => onToggleEnabled(true)}
            className="px-3 py-1.5 bg-eve-accent text-black text-xs uppercase tracking-wider font-semibold"
          >
            Enable Trading Edge
          </button>
        </div>
      </div>
    );
  }

  if (loading && !data) {
    return (
      <div className="flex items-center justify-center h-full text-eve-dim text-xs">
        <span className="inline-block w-4 h-4 border-2 border-eve-accent/40 border-t-eve-accent rounded-full animate-spin mr-2" />
        Loading Trading Edge...
      </div>
    );
  }

  if (error && !data) {
    return (
      <div className="flex flex-col items-center justify-center h-full text-xs gap-3">
        <div className="text-eve-error">{error}</div>
        <button
          type="button"
          onClick={() => onToggleEnabled(false)}
          className="px-3 py-1.5 border border-eve-border text-eve-dim hover:text-eve-text"
        >
          Disable tool
        </button>
      </div>
    );
  }

  if (!data) {
    return null;
  }

  const reality = data.reality_ratio ? `${(data.reality_ratio * 100).toFixed(0)}%` : "-";
  const itemRows = data.items ?? [];
  const categoryRows = data.categories ?? [];
  const stationRows = data.stations ?? [];

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="text-xs uppercase tracking-wider text-eve-accent">Trading Edge Engine</div>
          <div className="mt-1 text-[11px] text-eve-dim max-w-3xl leading-relaxed">
            Learns from Mission Control journal trades and ESI reconciliation: expected vs actual PnL, loss buckets, per-item/category/station edge and personal scanner presets.
          </div>
        </div>
        <label className="inline-flex items-center gap-2 text-xs text-eve-dim">
          <input
            type="checkbox"
            checked={enabled}
            onChange={(e) => onToggleEnabled(e.target.checked)}
            className="accent-eve-accent"
          />
          Enabled
        </label>
      </div>

      {data.warnings && data.warnings.length > 0 && (
        <div className="border border-eve-warning/40 bg-eve-warning/5 px-3 py-2 text-[11px] text-eve-warning">
          {data.warnings.join(" / ")}
        </div>
      )}

      <div className="grid grid-cols-2 xl:grid-cols-6 gap-3">
        <StatCard label="Journal sample" value={data.sample_size.toLocaleString()} subvalue={`${data.closed_trades} closed`} />
        <StatCard label="Expected" value={`${formatIsk(data.expected_isk)} ISK`} color="text-eve-accent" />
        <StatCard label="Realized" value={`${signed(formatIsk, data.realized_isk)} ISK`} color={data.realized_isk >= 0 ? "text-eve-profit" : "text-eve-error"} />
        <StatCard label="Reality ratio" value={reality} subvalue="real / expected" color={data.reality_ratio >= 0.8 ? "text-eve-profit" : data.reality_ratio > 0 ? "text-eve-warning" : "text-eve-dim"} />
        <StatCard label="Win rate" value={`${data.win_rate.toFixed(0)}%`} color="text-eve-accent" />
        <StatCard label="Gap" value={`${signed(formatIsk, data.delta_isk)} ISK`} color={data.delta_isk >= 0 ? "text-eve-profit" : "text-eve-error"} />
      </div>

      <div className="grid grid-cols-1 xl:grid-cols-3 gap-3">
        <EdgeTable title="Personal items" rows={itemRows} formatIsk={formatIsk} />
        <EdgeTable title="Categories / groups" rows={categoryRows} formatIsk={formatIsk} />
        <EdgeTable title="Stations / systems" rows={stationRows} formatIsk={formatIsk} />
      </div>

      <div className="grid grid-cols-1 xl:grid-cols-2 gap-3">
        <section className="border border-eve-border bg-eve-panel/80 p-3">
          <div className="text-[10px] uppercase tracking-wider text-eve-accent mb-2">Where ISK leaks</div>
          <div className="space-y-2">
            {data.loss_buckets.length === 0 ? (
              <div className="text-xs text-eve-dim">No negative expected-vs-actual buckets yet.</div>
            ) : data.loss_buckets.map((bucket) => (
              <div key={bucket.key} className="space-y-1">
                <div className="flex justify-between gap-3 text-xs">
                  <span className="text-eve-text">{bucket.label}</span>
                  <span className="font-mono text-eve-error">{formatIsk(bucket.isk)} ISK</span>
                </div>
                <div className="h-1.5 bg-eve-dark border border-eve-border overflow-hidden">
                  <div className="h-full bg-eve-error/70" style={{ width: `${Math.max(2, Math.min(100, bucket.share_pct))}%` }} />
                </div>
                <div className="text-[10px] text-eve-dim">{bucket.trades} trades · {bucket.share_pct.toFixed(0)}%</div>
              </div>
            ))}
          </div>
        </section>

        <section className="border border-eve-border bg-eve-panel/80 p-3">
          <div className="text-[10px] uppercase tracking-wider text-eve-accent mb-2">Generated personal presets</div>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
            {data.presets.map((preset) => (
              <div key={preset.id} className="border border-eve-border bg-eve-dark/50 p-3 space-y-2">
                <div className="text-sm text-eve-text font-semibold">{preset.name}</div>
                <div className="text-[11px] text-eve-dim leading-relaxed">{preset.description}</div>
                <div className="grid grid-cols-3 gap-2 text-[10px]">
                  <Mini label="Min ROI" value={`${preset.min_net_roi_pct.toFixed(1)}%`} />
                  <Mini label="Max qty" value={preset.max_quantity.toLocaleString()} />
                  <Mini label="Exposure" value={formatIsk(preset.max_exposure_isk)} />
                </div>
                {preset.preferred_scopes.length > 0 && (
                  <div className="text-[10px] text-eve-profit">Use: {preset.preferred_scopes.slice(0, 3).join(", ")}</div>
                )}
                {preset.avoid_scopes.length > 0 && (
                  <div className="text-[10px] text-eve-error">Avoid: {preset.avoid_scopes.slice(0, 3).join(", ")}</div>
                )}
              </div>
            ))}
          </div>
        </section>
      </div>
    </div>
  );
}

function EdgeTable({ title, rows, formatIsk }: { title: string; rows: TradingEdgeRow[]; formatIsk: (v: number) => string }) {
  return (
    <section className="border border-eve-border bg-eve-panel/80">
      <div className="px-3 py-2 border-b border-eve-border text-[10px] uppercase tracking-wider text-eve-accent">{title}</div>
      <div className="divide-y divide-eve-border/60">
        {rows.length === 0 ? (
          <div className="p-3 text-xs text-eve-dim">No history yet.</div>
        ) : rows.slice(0, 8).map((row) => (
          <div key={`${row.scope}-${row.key}`} className="p-3 space-y-1">
            <div className="flex items-start justify-between gap-2">
              <div className="min-w-0">
                <div className="text-sm text-eve-text truncate">{row.label}</div>
                <div className="text-[10px] text-eve-dim">{row.closed_trades} closed · win {row.win_rate.toFixed(0)}% · reality {(row.reality_ratio * 100).toFixed(0)}%</div>
              </div>
              <div className={`text-[10px] uppercase tracking-wider whitespace-nowrap ${labelTone[row.label_code] ?? "text-eve-dim"}`}>
                {labelText[row.label_code] ?? row.label_code}
              </div>
            </div>
            <div className="grid grid-cols-3 gap-2 text-[10px]">
              <Mini label="Real" value={`${signed(formatIsk, row.realized_isk)}`} />
              <Mini label="Min ROI" value={`${row.min_net_roi_pct.toFixed(1)}%`} />
              <Mini label="Max qty" value={row.max_recommended_qty.toLocaleString()} />
            </div>
            <div className="text-[10px] text-eve-dim leading-relaxed">{row.advice}</div>
          </div>
        ))}
      </div>
    </section>
  );
}

function Mini({ label, value }: { label: string; value: string }) {
  return (
    <div className="border border-eve-border bg-eve-dark/50 px-2 py-1">
      <div className="text-eve-dim">{label}</div>
      <div className="font-mono text-eve-text truncate">{value}</div>
    </div>
  );
}

function signed(formatIsk: (v: number) => string, value: number): string {
  return `${value >= 0 ? "+" : ""}${formatIsk(value)}`;
}
