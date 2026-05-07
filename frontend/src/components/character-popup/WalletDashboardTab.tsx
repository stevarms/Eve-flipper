import { useEffect, useMemo, useState } from "react";
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

interface WalletDashboardTabProps {
  characterScope: CharacterScope;
  formatIsk: (v: number) => string;
  t: (key: TranslationKey, params?: Record<string, string | number>) => string;
}

export function WalletDashboardTab({ characterScope, formatIsk, t }: WalletDashboardTabProps) {
  const [windowDays, setWindowDays] = useState<LedgerWindow>(90);
  const [period, setPeriod] = useState<LedgerPeriod>("daily");
  const [chartMode, setChartMode] = useState<LedgerChartMode>("capital");
  const [salesTax, setSalesTax] = useState(8);
  const [brokerFee, setBrokerFee] = useState(1);
  const [data, setData] = useState<EveLedgerDashboard | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

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
  const tradingTone = summary.trading_pnl_isk >= 0 ? "text-eve-profit" : "text-eve-error";
  const otherTone = summary.other_net_isk >= 0 ? "text-eve-profit" : "text-eve-error";
  const mtmTone = summary.unrealized_pnl_isk >= 0 ? "text-eve-profit" : "text-eve-error";

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

      <section className="border border-eve-border rounded-sm bg-eve-panel p-3">
        <div className="flex flex-wrap items-center justify-between gap-2 mb-3">
          <div>
            <div className="text-[10px] uppercase tracking-wider text-eve-dim">
              {chartMode === "capital" ? "Capital curve" : chartMode === "cashflow" ? "Income / outgoing" : "Trading P&L vs other income"}
            </div>
            <div className="text-[10px] text-eve-dim/80">
              {period} view, {curve.length} buckets
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
  if (data.length === 0) {
    return <div className="h-56 flex items-center justify-center text-eve-dim text-xs">No ledger data</div>;
  }

  const width = 900;
  const height = 250;
  const padX = 34;
  const padY = 18;
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
  const y = (value: number) => padY + (1 - (value - minVal) / range) * (height - padY * 2);
  const zeroY = y(0);
  const barWidth = Math.max(3, Math.min(18, (width - padX * 2) / Math.max(1, data.length) - 3));
  const capitalLine = data.map((d, i) => `${padX + i * xStep},${y(d.capital_isk)}`).join(" ");
  const tradingLine = data.map((d, i) => `${padX + i * xStep},${y(d.trading_pnl_isk)}`).join(" ");
  const otherLine = data.map((d, i) => `${padX + i * xStep},${y(d.other_net_isk)}`).join(" ");

  return (
    <div className="relative w-full overflow-hidden">
      <svg viewBox={`0 0 ${width} ${height}`} className="w-full h-64">
        <line x1={padX} x2={width - padX} y1={zeroY} y2={zeroY} stroke="rgba(120,120,120,0.5)" strokeDasharray="3 4" />
        {[0.25, 0.5, 0.75].map((p) => (
          <line
            key={p}
            x1={padX}
            x2={width - padX}
            y1={padY + p * (height - padY * 2)}
            y2={padY + p * (height - padY * 2)}
            stroke="rgba(120,120,120,0.14)"
          />
        ))}

        {mode === "capital" && (
          <>
            <polyline points={capitalLine} fill="none" stroke="#e69500" strokeWidth="2.5" />
            {data.map((d, i) => (
              <circle key={d.period} cx={padX + i * xStep} cy={y(d.capital_isk)} r={2.3} fill="#e69500">
                <title>{`${d.period}: ${formatIsk(d.capital_isk)} ISK`}</title>
              </circle>
            ))}
          </>
        )}

        {mode === "cashflow" && data.map((d, i) => {
          const x = padX + i * xStep - barWidth / 2;
          const incTop = y(d.income_isk);
          const outBottom = y(-d.outgoing_isk);
          return (
            <g key={d.period}>
              <rect x={x} y={incTop} width={barWidth} height={Math.max(1, zeroY - incTop)} fill="rgba(34,197,94,0.65)">
                <title>{`${d.period} income: ${formatIsk(d.income_isk)} ISK`}</title>
              </rect>
              <rect x={x} y={zeroY} width={barWidth} height={Math.max(1, outBottom - zeroY)} fill="rgba(239,68,68,0.68)">
                <title>{`${d.period} outgoing: ${formatIsk(d.outgoing_isk)} ISK`}</title>
              </rect>
            </g>
          );
        })}

        {mode === "pnl" && (
          <>
            <polyline points={tradingLine} fill="none" stroke="#22c55e" strokeWidth="2" />
            <polyline points={otherLine} fill="none" stroke="#60a5fa" strokeWidth="2" strokeDasharray="5 4" />
            {data.map((d, i) => (
              <circle key={d.period} cx={padX + i * xStep} cy={y(d.trading_pnl_isk)} r={2} fill="#22c55e">
                <title>{`${d.period} trading: ${formatIsk(d.trading_pnl_isk)} ISK / other: ${formatIsk(d.other_net_isk)} ISK`}</title>
              </circle>
            ))}
          </>
        )}
      </svg>
      <div className="flex justify-between -mt-3 px-2 text-[9px] text-eve-dim">
        <span>{data[0]?.period}</span>
        {data.length > 2 && <span>{data[Math.floor(data.length / 2)]?.period}</span>}
        <span>{data[data.length - 1]?.period}</span>
      </div>
    </div>
  );
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
