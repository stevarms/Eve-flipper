import { useEffect, useMemo, useRef, useState } from "react";
import { useAchievements } from "../achievements/AchievementsProvider";
import { getEveLedgerDashboard, type CharacterScope } from "../../lib/api";
import { type TranslationKey } from "../../lib/i18n";
import type {
  EveLedgerCategory,
  EveLedgerCurvePoint,
  EveLedgerDashboard,
  EveLedgerInventoryItem,
} from "../../lib/types";
import { StatCard } from "./shared";

type LedgerWindow = 30 | 90 | 180 | 365;
type LedgerPeriod = "daily" | "weekly" | "monthly";
type LedgerChartMode = "capital" | "cashflow" | "pnl";
type LedgerTooltipTone = "good" | "bad" | "warn" | "info" | "dim";

interface LedgerChartTooltip {
  x: number;
  leftPct: number;
  topPct: number;
  period: string;
  subtitle: string;
  rows: Array<{ label: string; value: string; tone?: LedgerTooltipTone }>;
}

interface WalletDashboardTabProps {
  characterScope: CharacterScope;
  formatIsk: (v: number) => string;
  t: (key: TranslationKey, params?: Record<string, string | number>) => string;
  onOpenPaperTradeJournal?: () => void;
}

export function WalletDashboardTab({ characterScope, formatIsk, t, onOpenPaperTradeJournal }: WalletDashboardTabProps) {
  const [windowDays, setWindowDays] = useState<LedgerWindow>(90);
  const [period, setPeriod] = useState<LedgerPeriod>("daily");
  const [chartMode, setChartMode] = useState<LedgerChartMode>("capital");
  const [salesTax, setSalesTax] = useState(8);
  const [brokerFee, setBrokerFee] = useState(1);
  const [data, setData] = useState<EveLedgerDashboard | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const { trackAchievementEvent } = useAchievements();
  const archiveAchievementKeyRef = useRef("");

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    getEveLedgerDashboard(windowDays, {
      salesTax,
      brokerFee,
      characterId: characterScope,
    })
      .then((next) => {
        if (!cancelled) setData(next);
      })
      .catch((e) => {
        if (!cancelled) setError(e instanceof Error ? e.message : "failed to fetch ledger");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [windowDays, salesTax, brokerFee, characterScope]);

  const curve = useMemo(() => {
    if (!data) return [];
    if (period === "weekly") return data.weekly ?? [];
    if (period === "monthly") return data.monthly ?? [];
    return data.daily ?? [];
  }, [data, period]);

  useEffect(() => {
    const archive = data?.archive;
    if (!archive?.enabled) return;
    const coverageDays = Math.max(0, archive.archive_coverage_days ?? 0);
    const syncStreakDays = archive.transaction_rows > 0 && archive.journal_rows > 0 ? Math.min(30, Math.floor(coverageDays)) : 0;
    const fallbackUsed = !!archive.archive_fallback_used;
    const key = [
      Math.round(coverageDays),
      archive.transaction_rows,
      archive.journal_rows,
      Math.round(archive.transaction_turnover_isk ?? 0),
      syncStreakDays,
      fallbackUsed ? 1 : 0,
    ].join(":");
    if (archiveAchievementKeyRef.current === key) return;
    archiveAchievementKeyRef.current = key;
    void trackAchievementEvent("ledger_archive_updated", {
      archiveCoverageDays: coverageDays,
      archivedTransactions: archive.transaction_rows,
      archivedJournalEntries: archive.journal_rows,
      archiveTransactionTurnoverISK: archive.transaction_turnover_isk ?? 0,
      archiveSyncStreakDays: syncStreakDays,
      rateLimitArchiveFallback: fallbackUsed,
    });
  }, [data?.archive, trackAchievementEvent]);

  if (loading && !data) {
    return (
      <div className="flex items-center justify-center h-full text-eve-dim text-xs">
        <span className="inline-block w-4 h-4 border-2 border-eve-accent/40 border-t-eve-accent rounded-full animate-spin mr-2" />
        {t("loading")}...
      </div>
    );
  }

  if (error && !data) {
    return <div className="flex items-center justify-center h-full text-eve-error text-xs">{error}</div>;
  }

  if (!data) {
    return <div className="flex items-center justify-center h-full text-eve-dim text-xs">{t("ledgerNoCashflow")}</div>;
  }

  const summary = data.summary;
  const archive = data.archive;
  const tradingTone = summary.trading_pnl_isk >= 0 ? "text-eve-profit" : "text-eve-error";
  const otherTone = summary.other_net_isk >= 0 ? "text-eve-profit" : "text-eve-error";
  const mtmTone = summary.unrealized_pnl_isk >= 0 ? "text-eve-profit" : "text-eve-error";
  const archiveTone = archive?.using_archive ? "text-eve-accent" : "text-eve-dim";

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <div className="text-xs text-eve-dim uppercase tracking-wider">Wallet / cashflow ledger</div>
          <div className="text-[10px] text-eve-dim/80">
            Journal categories, trading P&L, inventory mark-to-market and capital curve.
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {onOpenPaperTradeJournal && (
            <button
              type="button"
              onClick={onOpenPaperTradeJournal}
              className="h-7 px-3 border border-eve-accent/60 bg-eve-accent/10 text-eve-accent hover:bg-eve-accent hover:text-black transition-colors text-[10px] uppercase tracking-wider font-semibold"
              title="Open saved Mission Control trades"
            >
              Paper Trade Journal
            </button>
          )}
          <Segmented
            values={[30, 90, 180, 365] as const}
            active={windowDays}
            label={(v) => `${v}d`}
            onChange={setWindowDays}
          />
          <Segmented
            values={["daily", "weekly", "monthly"] as const}
            active={period}
            label={(v) => v.slice(0, 1).toUpperCase() + v.slice(1)}
            onChange={setPeriod}
          />
          <NumberControl label={t("pnlSalesTax")} value={salesTax} onChange={setSalesTax} />
          <NumberControl label={t("pnlBrokerFee")} value={brokerFee} onChange={setBrokerFee} />
        </div>
      </div>

      {data.warnings && data.warnings.length > 0 && (
        <div className="border border-eve-warning/40 bg-eve-warning/5 px-3 py-2 text-[11px] text-eve-warning">
          {data.warnings.slice(0, 3).join(" / ")}
        </div>
      )}

      <div className="grid grid-cols-2 xl:grid-cols-6 gap-3">
        <StatCard label="Estimated capital" value={`${formatIsk(summary.estimated_capital_isk)} ISK`} color="text-eve-profit" large />
        <StatCard label={t("charWallet")} value={`${formatIsk(summary.wallet_isk)} ISK`} />
        <StatCard label="Trading P&L" value={`${signed(formatIsk, summary.trading_pnl_isk)} ISK`} color={tradingTone} />
        <StatCard label="Other net" value={`${signed(formatIsk, summary.other_net_isk)} ISK`} color={otherTone} />
        <StatCard label="Inventory MTM" value={`${formatIsk(summary.inventory_mtm_isk)} ISK`} subvalue={`${summary.priced_asset_types}/${summary.asset_types} priced`} color="text-eve-accent" />
        <StatCard label="Unrealized" value={`${signed(formatIsk, summary.unrealized_pnl_isk)} ISK`} color={mtmTone} />
      </div>

      <div className="grid grid-cols-2 xl:grid-cols-4 gap-3">
        <StatCard label="Journal income" value={`${formatIsk(summary.journal_income_isk)} ISK`} color="text-eve-profit" />
        <StatCard label="Journal outgoing" value={`${formatIsk(summary.journal_outgoing_isk)} ISK`} color="text-eve-error" />
        <StatCard label={t("ledgerOpenBuy")} value={`${formatIsk(summary.buy_orders_value_isk)} ISK`} color="text-eve-warning" />
        <StatCard label={t("ledgerOpenSell")} value={`${formatIsk(summary.sell_orders_value_isk)} ISK`} color="text-eve-accent" />
      </div>

      {archive && (
        <section className="border border-eve-border rounded-sm bg-eve-panel/70 p-3">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <div className="text-[10px] uppercase tracking-wider text-eve-dim">{t("walletArchiveTitle")}</div>
              <div className="text-[10px] text-eve-dim/80">
                {t("walletArchiveDescription")}
              </div>
            </div>
            <div className={`text-xs uppercase tracking-wider ${archiveTone}`}>
              {archive.source.replace("+", " + ")}
            </div>
          </div>
          <div className="grid grid-cols-2 xl:grid-cols-6 gap-2 mt-3">
            <MiniMetric label={t("walletArchiveArchivedTx")} value={archive.transaction_rows.toLocaleString()} />
            <MiniMetric label={t("walletArchiveArchivedJournal")} value={archive.journal_rows.toLocaleString()} />
            <MiniMetric label={t("walletArchiveLiveTx")} value={archive.live_transaction_rows.toLocaleString()} />
            <MiniMetric label={t("walletArchiveLiveJournal")} value={archive.live_journal_rows.toLocaleString()} />
            <MiniMetric label={t("walletArchiveCoverage")} value={archive.archive_coverage_days > 0 ? `${Math.round(archive.archive_coverage_days)}d` : t("walletArchiveNA")} />
            <MiniMetric label={t("walletArchiveLastSync")} value={shortDate(archive.last_transaction_sync || archive.last_journal_sync, t("walletArchiveNever"))} />
          </div>
          {(archive.transaction_limit_hit || archive.journal_limit_hit) && (
            <div className="mt-2 border border-eve-warning/30 bg-eve-warning/5 px-2 py-1.5 text-[10px] text-eve-warning">
              {t("walletArchiveFullPageWarning")}
            </div>
          )}
        </section>
      )}

      <section className="border border-eve-border rounded-sm bg-eve-panel p-3">
        <div className="flex flex-wrap items-center justify-between gap-2 mb-3">
          <div>
            <div className="text-[10px] uppercase tracking-wider text-eve-dim">
              {chartMode === "capital" ? "Capital curve" : chartMode === "cashflow" ? "Income / outgoing" : "Trading P&L vs other income"}
            </div>
            <div className="text-[10px] text-eve-dim/80">
              {period} view, {curve.length} buckets{curveRange(curve)}
            </div>
          </div>
          <Segmented
            values={["capital", "cashflow", "pnl"] as const}
            active={chartMode}
            label={(v) => (v === "pnl" ? "P&L" : v.slice(0, 1).toUpperCase() + v.slice(1))}
            onChange={setChartMode}
          />
        </div>
        <LedgerCurveChart data={curve} mode={chartMode} formatIsk={formatIsk} />
      </section>

      <div className="grid grid-cols-1 xl:grid-cols-2 gap-3">
        <section className="border border-eve-border rounded-sm overflow-hidden">
          <div className="px-3 py-2 bg-eve-panel text-[10px] uppercase tracking-wider text-eve-dim">
            Wallet journal categories
          </div>
          <CategoryTable categories={data.categories ?? []} formatIsk={formatIsk} />
        </section>

        <section className="border border-eve-border rounded-sm overflow-hidden">
          <div className="px-3 py-2 bg-eve-panel text-[10px] uppercase tracking-wider text-eve-dim">
            Inventory mark-to-market
          </div>
          <InventoryTable items={data.inventory ?? []} formatIsk={formatIsk} />
        </section>
      </div>
    </div>
  );
}

function signed(formatIsk: (v: number) => string, value: number) {
  return `${value >= 0 ? "+" : ""}${formatIsk(value)}`;
}

function shortDate(value?: string, fallback = "never") {
  if (!value) return fallback;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value.slice(0, 10);
  return date.toISOString().slice(0, 10);
}

function MiniMetric({ label, value }: { label: string; value: string }) {
  return (
    <div className="border border-eve-border/70 bg-eve-dark/60 px-2 py-1.5 rounded-sm">
      <div className="text-[9px] uppercase tracking-wider text-eve-dim">{label}</div>
      <div className="text-xs text-eve-text font-mono">{value}</div>
    </div>
  );
}

function NumberControl({
  label,
  value,
  onChange,
}: {
  label: string;
  value: number;
  onChange: (value: number) => void;
}) {
  return (
    <label className="flex items-center gap-1 text-[10px] text-eve-dim">
      <span>{label}</span>
      <input
        type="number"
        min={0}
        max={100}
        step={0.1}
        value={value}
        onChange={(e) => onChange(Number.parseFloat(e.target.value) || 0)}
        className="w-14 px-1 py-0.5 rounded-sm border border-eve-border bg-eve-dark text-eve-text"
      />
    </label>
  );
}

function Segmented<T extends string | number>({
  values,
  active,
  label,
  onChange,
}: {
  values: readonly T[];
  active: T;
  label: (value: T) => string;
  onChange: (value: T) => void;
}) {
  return (
    <div className="flex gap-1">
      {values.map((value) => (
        <button
          key={String(value)}
          type="button"
          onClick={() => onChange(value)}
          className={`px-2.5 py-1 text-[10px] rounded-sm border transition-colors ${
            active === value
              ? "bg-eve-accent/20 border-eve-accent text-eve-accent"
              : "bg-eve-dark border-eve-border text-eve-dim hover:text-eve-text"
          }`}
        >
          {label(value)}
        </button>
      ))}
    </div>
  );
}

function LedgerCurveChart({
  data,
  mode,
  formatIsk,
}: {
  data: EveLedgerCurvePoint[];
  mode: LedgerChartMode;
  formatIsk: (v: number) => string;
}) {
  const [tooltip, setTooltip] = useState<LedgerChartTooltip | null>(null);

  if (data.length === 0) {
    return <div className="h-56 flex items-center justify-center text-eve-dim text-xs">No ledger data</div>;
  }

  const width = 900;
  const height = 270;
  const padX = 34;
  const padTop = 18;
  const padBottom = 34;
  const chartBottom = height - padBottom;
  const values =
    mode === "capital"
      ? data.map((d) => d.capital_isk)
      : mode === "cashflow"
        ? data.flatMap((d) => [d.income_isk, -d.outgoing_isk])
        : data.flatMap((d) => [d.trading_pnl_isk, d.other_net_isk]);
  const minVal = Math.min(0, ...values);
  const maxVal = Math.max(1, ...values);
  const range = maxVal - minVal || 1;
  const xStep = data.length > 1 ? (width - padX * 2) / (data.length - 1) : 0;
  const pointX = (index: number) => padX + index * xStep;
  const y = (value: number) => padTop + (1 - (value - minVal) / range) * (chartBottom - padTop);
  const zeroY = y(0);
  const barWidth = Math.max(3, Math.min(18, (width - padX * 2) / Math.max(1, data.length) - 3));
  const capitalLine = data.map((d, i) => `${pointX(i)},${y(d.capital_isk)}`).join(" ");
  const tradingLine = data.map((d, i) => `${pointX(i)},${y(d.trading_pnl_isk)}`).join(" ");
  const otherLine = data.map((d, i) => `${pointX(i)},${y(d.other_net_isk)}`).join(" ");
  const hitWidth = data.length > 1 ? Math.min(Math.max(12, xStep), width - padX * 2) : width - padX * 2;
  const clampPct = (value: number, min: number, max: number) => Math.min(max, Math.max(min, value));
  const valueWithIsk = (value: number) => `${formatIsk(value)} ISK`;
  const signedWithIsk = (value: number) => `${value >= 0 ? "+" : ""}${formatIsk(value)} ISK`;
  const tooltipSubtitle = (d: EveLedgerCurvePoint) => {
    if (d.start_date && d.end_date && d.start_date !== d.end_date) return `${d.start_date} -> ${d.end_date}`;
    return d.start_date || d.end_date || d.period;
  };
  const buildTooltip = (d: EveLedgerCurvePoint, index: number): LedgerChartTooltip => {
    let anchorY = y(d.capital_isk);
    let rows: LedgerChartTooltip["rows"];
    if (mode === "capital") {
      rows = [
        { label: "Estimated capital", value: valueWithIsk(d.capital_isk), tone: "info" },
        { label: "Net cashflow", value: signedWithIsk(d.net_cashflow_isk), tone: d.net_cashflow_isk >= 0 ? "good" : "bad" },
        { label: "Transactions", value: d.transactions.toLocaleString(), tone: "dim" },
        { label: "Journal entries", value: d.journal_entries.toLocaleString(), tone: "dim" },
      ];
    } else if (mode === "cashflow") {
      anchorY = Math.min(y(d.income_isk), y(-d.outgoing_isk), zeroY);
      rows = [
        { label: "Income", value: valueWithIsk(d.income_isk), tone: "good" },
        { label: "Outgoing", value: valueWithIsk(d.outgoing_isk), tone: "bad" },
        { label: "Net cashflow", value: signedWithIsk(d.net_cashflow_isk), tone: d.net_cashflow_isk >= 0 ? "good" : "bad" },
        { label: "Transactions", value: d.transactions.toLocaleString(), tone: "dim" },
      ];
    } else {
      anchorY = Math.min(y(d.trading_pnl_isk), y(d.other_net_isk));
      const combined = d.trading_pnl_isk + d.other_net_isk;
      rows = [
        { label: "Trading P&L", value: signedWithIsk(d.trading_pnl_isk), tone: d.trading_pnl_isk >= 0 ? "good" : "bad" },
        { label: "Other net", value: signedWithIsk(d.other_net_isk), tone: d.other_net_isk >= 0 ? "good" : "bad" },
        { label: "Combined", value: signedWithIsk(combined), tone: combined >= 0 ? "good" : "bad" },
        { label: "Journal entries", value: d.journal_entries.toLocaleString(), tone: "dim" },
      ];
    }
    const x = pointX(index);
    return {
      x,
      leftPct: clampPct((x / width) * 100, 9, 91),
      topPct: clampPct((anchorY / height) * 100, 13, 92),
      period: d.period,
      subtitle: tooltipSubtitle(d),
      rows,
    };
  };

  return (
    <div className="relative w-full overflow-visible" onMouseLeave={() => setTooltip(null)}>
      <svg viewBox={`0 0 ${width} ${height}`} className="w-full h-64">
        <line x1={padX} x2={width - padX} y1={zeroY} y2={zeroY} stroke="rgba(120,120,120,0.5)" strokeDasharray="3 4" />
        {[0.25, 0.5, 0.75].map((p) => (
          <line
            key={p}
            x1={padX}
            x2={width - padX}
            y1={padTop + p * (chartBottom - padTop)}
            y2={padTop + p * (chartBottom - padTop)}
            stroke="rgba(120,120,120,0.14)"
          />
        ))}

        {mode === "capital" && (
          <>
            <polyline points={capitalLine} fill="none" stroke="#e69500" strokeWidth="2.5" />
            {data.map((d, i) => (
              <circle key={d.period} cx={pointX(i)} cy={y(d.capital_isk)} r={2.3} fill="#e69500" />
            ))}
          </>
        )}

        {mode === "cashflow" && data.map((d, i) => {
          const x = pointX(i) - barWidth / 2;
          const incTop = y(d.income_isk);
          const outBottom = y(-d.outgoing_isk);
          return (
            <g key={d.period}>
              <rect x={x} y={incTop} width={barWidth} height={Math.max(1, zeroY - incTop)} fill="rgba(34,197,94,0.65)" />
              <rect x={x} y={zeroY} width={barWidth} height={Math.max(1, outBottom - zeroY)} fill="rgba(239,68,68,0.68)" />
            </g>
          );
        })}

        {mode === "pnl" && (
          <>
            <polyline points={tradingLine} fill="none" stroke="#22c55e" strokeWidth="2" />
            <polyline points={otherLine} fill="none" stroke="#60a5fa" strokeWidth="2" strokeDasharray="5 4" />
            {data.map((d, i) => (
              <circle key={d.period} cx={pointX(i)} cy={y(d.trading_pnl_isk)} r={2} fill="#22c55e" />
            ))}
          </>
        )}

        {tooltip && (
          <line
            x1={tooltip.x}
            x2={tooltip.x}
            y1={padTop}
            y2={chartBottom}
            stroke="rgba(230,149,0,0.45)"
            strokeDasharray="3 3"
          />
        )}

        {data.map((d, i) => {
          const x = pointX(i);
          const hitX = Math.max(padX, Math.min(width - padX - hitWidth, x - hitWidth / 2));
          return (
            <rect
              key={`${d.period}-hit`}
              x={hitX}
              y={padTop}
              width={hitWidth}
              height={chartBottom - padTop}
              fill="transparent"
              cursor="crosshair"
              onPointerEnter={() => setTooltip(buildTooltip(d, i))}
              onPointerMove={() => setTooltip(buildTooltip(d, i))}
            />
          );
        })}

        <g fill="rgba(170,170,170,0.9)" fontSize="10" fontFamily="ui-monospace, SFMono-Regular, Menlo, monospace">
          <text x={padX} y={height - 9} textAnchor="start">{data[0]?.period}</text>
          {data.length > 2 && (
            <text x={width / 2} y={height - 9} textAnchor="middle">{data[Math.floor(data.length / 2)]?.period}</text>
          )}
          <text x={width - padX} y={height - 9} textAnchor="end">{data[data.length - 1]?.period}</text>
        </g>
      </svg>
      {tooltip && (
        <div
          className="pointer-events-none absolute z-30 min-w-[210px] border border-eve-border bg-eve-dark/95 px-3 py-2 text-[10px] shadow-[0_14px_34px_rgba(0,0,0,0.55)] backdrop-blur-sm"
          style={{
            left: `${tooltip.leftPct}%`,
            top: `${tooltip.topPct}%`,
            transform: "translate(-50%, calc(-100% - 10px))",
          }}
        >
          <div className="mb-1 flex items-start justify-between gap-3">
            <div className="font-mono text-[11px] text-eve-text">{tooltip.period}</div>
            <div className="text-right text-[9px] uppercase tracking-wider text-eve-accent">
              {mode === "pnl" ? "P&L" : mode}
            </div>
          </div>
          <div className="mb-2 border-b border-eve-border/60 pb-1 text-[9px] text-eve-dim">{tooltip.subtitle}</div>
          <div className="space-y-1">
            {tooltip.rows.map((row) => (
              <div key={row.label} className="flex items-center justify-between gap-4">
                <span className="text-eve-dim">{row.label}</span>
                <span className={`font-mono ${ledgerTooltipToneClass(row.tone)}`}>{row.value}</span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function ledgerTooltipToneClass(tone?: LedgerTooltipTone) {
  switch (tone) {
    case "good":
      return "text-eve-profit";
    case "bad":
      return "text-eve-error";
    case "warn":
      return "text-eve-warning";
    case "info":
      return "text-eve-accent";
    case "dim":
      return "text-eve-dim";
    default:
      return "text-eve-text";
  }
}

function curveRange(data: EveLedgerCurvePoint[]) {
  if (data.length === 0) return "";
  const first = data[0]?.period || data[0]?.start_date;
  const last = data[data.length - 1]?.period || data[data.length - 1]?.end_date;
  if (!first || !last || first === last) return "";
  return ` | ${first} -> ${last}`;
}

function CategoryTable({
  categories,
  formatIsk,
}: {
  categories: EveLedgerCategory[];
  formatIsk: (v: number) => string;
}) {
  if (categories.length === 0) {
    return <div className="py-8 text-center text-xs text-eve-dim">No wallet journal categories</div>;
  }
  const maxAbs = Math.max(...categories.map((c) => Math.abs(c.net_isk)), 1);
  return (
    <table className="w-full text-xs">
      <thead className="bg-eve-dark/60 text-eve-dim">
        <tr>
          <th className="px-3 py-2 text-left">Category</th>
          <th className="px-3 py-2 text-right">Income</th>
          <th className="px-3 py-2 text-right">Outgoing</th>
          <th className="px-3 py-2 text-right">Net</th>
        </tr>
      </thead>
      <tbody>
        {categories.slice(0, 14).map((category) => {
          const positive = category.net_isk >= 0;
          const width = Math.max(4, (Math.abs(category.net_isk) / maxAbs) * 100);
          return (
            <tr key={category.key} className="border-t border-eve-border/50 hover:bg-eve-panel/40">
              <td className="px-3 py-2 text-eve-text">
                <div className="flex items-center gap-2">
                  <span className={`w-1.5 h-1.5 rounded-full ${category.is_trading ? "bg-eve-accent" : "bg-sky-400"}`} />
                  <span>{category.label}</span>
                  <span className="text-eve-dim">({category.entries})</span>
                </div>
              </td>
              <td className="px-3 py-2 text-right text-eve-profit">{formatIsk(category.income_isk)}</td>
              <td className="px-3 py-2 text-right text-eve-error">{formatIsk(category.outgoing_isk)}</td>
              <td className={`px-3 py-2 text-right ${positive ? "text-eve-profit" : "text-eve-error"}`}>
                <div className="flex items-center justify-end gap-2">
                  <div className="w-16 h-1.5 bg-eve-dark overflow-hidden rounded-full">
                    <div className={`h-full ${positive ? "bg-eve-profit" : "bg-eve-error"}`} style={{ width: `${width}%` }} />
                  </div>
                  <span>{positive ? "+" : ""}{formatIsk(category.net_isk)}</span>
                </div>
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

function InventoryTable({
  items,
  formatIsk,
}: {
  items: EveLedgerInventoryItem[];
  formatIsk: (v: number) => string;
}) {
  if (items.length === 0) {
    return <div className="py-8 text-center text-xs text-eve-dim">No asset snapshot</div>;
  }
  return (
    <table className="w-full text-xs">
      <thead className="bg-eve-dark/60 text-eve-dim">
        <tr>
          <th className="px-3 py-2 text-left">Item</th>
          <th className="px-3 py-2 text-right">Qty</th>
          <th className="px-3 py-2 text-right">MTM</th>
          <th className="px-3 py-2 text-right">Unrealized</th>
        </tr>
      </thead>
      <tbody>
        {items.slice(0, 16).map((item) => {
          const positive = item.unrealized_pnl >= 0;
          return (
            <tr key={item.type_id} className="border-t border-eve-border/50 hover:bg-eve-panel/40">
              <td className="px-3 py-2 text-eve-text">
                <div className="flex items-center gap-2 min-w-0">
                  <img src={`https://images.evetech.net/types/${item.type_id}/icon?size=32`} alt="" className="w-5 h-5" />
                  <span className="truncate">{item.type_name || `Type #${item.type_id}`}</span>
                  {!item.priced && <span className="text-[10px] text-eve-warning">unpriced</span>}
                </div>
              </td>
              <td className="px-3 py-2 text-right text-eve-dim">{item.quantity.toLocaleString()}</td>
              <td className="px-3 py-2 text-right text-eve-accent">{formatIsk(item.market_value)}</td>
              <td className={`px-3 py-2 text-right ${positive ? "text-eve-profit" : "text-eve-error"}`}>
                {item.cost_basis > 0 ? `${positive ? "+" : ""}${formatIsk(item.unrealized_pnl)}` : "n/a"}
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
