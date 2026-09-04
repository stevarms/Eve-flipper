import { formatISK } from "../../lib/format";
import { useI18n } from "../../lib/i18n";
import type {
  ArbitragePath,
  InjectionTier,
  MarketDepthInfo,
  PLEXDashboard,
  PLEXGlobalPrice,
  PLEXIndicators,
} from "../../lib/types";

export function SignalCard({ signal, indicators }: { signal: PLEXDashboard["signal"]; indicators: PLEXIndicators | null | undefined }) {
  const { t } = useI18n();
  const colorMap = { BUY: "text-eve-success", SELL: "text-eve-error", HOLD: "text-eve-warning" };
  const bgMap = { BUY: "bg-eve-success/10 border-eve-success/30", SELL: "bg-eve-error/10 border-eve-error/30", HOLD: "bg-eve-warning/10 border-eve-warning/30" };

  return (
    <div className={`border rounded-sm p-4 flex flex-col gap-2 ${bgMap[signal.action]}`}>
      <div className="flex items-center justify-between">
        <span className="text-xs text-eve-dim uppercase tracking-wider font-medium">{t("plexSignal")}</span>
        {indicators?.ccp_sale_signal && (
          <span className="px-2 py-0.5 text-[10px] font-bold uppercase bg-eve-success/20 text-eve-success border border-eve-success/40 rounded-sm animate-pulse">
            CCP SALE
          </span>
        )}
      </div>
      <div className={`text-3xl font-bold tracking-wider ${colorMap[signal.action]}`}>
        {signal.action}
      </div>
      <div className="flex items-center gap-2">
        <div className="flex-1 h-1.5 bg-eve-dark rounded-full overflow-hidden">
          <div
            className={`h-full rounded-full transition-all ${signal.action === "BUY" ? "bg-eve-success" : signal.action === "SELL" ? "bg-eve-error" : "bg-eve-warning"}`}
            style={{ width: `${signal.confidence}%` }}
          />
        </div>
        <span className="text-xs text-eve-dim">{signal.confidence.toFixed(0)}%</span>
      </div>
      <div className="flex flex-col gap-0.5 mt-1">
        {signal.reasons.map((r, i) => (
          <span key={i} className="text-[11px] text-eve-dim leading-tight">• {r}</span>
        ))}
      </div>
    </div>
  );
}

export function GlobalPriceCard({ price, indicators: ind }: { price: PLEXGlobalPrice; indicators: PLEXIndicators | null | undefined }) {
  const { t } = useI18n();
  const hasData = price.buy_price > 0 || price.sell_price > 0;

  // Percentile color: green if <30 (cheap), red if >70 (expensive)
  const pctColor = price.percentile_90d < 30 ? "text-eve-success" : price.percentile_90d > 70 ? "text-eve-error" : "text-eve-text";

  // Volatility regime color
  const volColor = ind?.vol_regime === "low" ? "text-eve-success" : ind?.vol_regime === "high" ? "text-eve-error" : "text-eve-warning";
  const volLabel = ind?.vol_regime === "low" ? t("plexVolLow") : ind?.vol_regime === "high" ? t("plexVolHigh") : t("plexVolMedium");

  return (
    <div className="bg-eve-dark border border-eve-accent/30 rounded-sm p-3">
      <h3 className="text-xs font-semibold text-eve-dim uppercase tracking-wider mb-3">{t("plexGlobalPrice")}</h3>
      {hasData ? (
        <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-4">
          <div>
            <div className="text-[10px] text-eve-dim uppercase tracking-wider mb-0.5">{t("plexBestBuy")}</div>
            <div className="text-lg font-mono font-bold text-eve-success">{formatISK(price.buy_price)}</div>
            <div className="text-[10px] text-eve-dim">{price.buy_orders} {t("plexOrders")}</div>
          </div>
          <div>
            <div className="text-[10px] text-eve-dim uppercase tracking-wider mb-0.5">{t("plexBestSell")}</div>
            <div className="text-lg font-mono font-bold text-eve-error">{formatISK(price.sell_price)}</div>
            <div className="text-[10px] text-eve-dim">{price.sell_orders} {t("plexOrders")}</div>
          </div>
          <div>
            <div className="text-[10px] text-eve-dim uppercase tracking-wider mb-0.5">{t("plexSpread")}</div>
            <div className="text-lg font-mono font-bold text-eve-text">{formatISK(price.spread)}</div>
            <div className="text-[10px] text-eve-dim">{price.spread_pct.toFixed(2)}%</div>
          </div>
          <div>
            <div className="text-[10px] text-eve-dim uppercase tracking-wider mb-0.5">{t("plexVolume24h")}</div>
            <div className="text-lg font-mono font-bold text-eve-text">{price.volume_24h.toLocaleString()}</div>
          </div>
          {/* 90d Percentile */}
          {price.percentile_90d > 0 && (
            <div title={t("plexPercentileHint").replace("{pct}", (100 - price.percentile_90d).toFixed(0))}>
              <div className="text-[10px] text-eve-dim uppercase tracking-wider mb-0.5">{t("plexPercentile")}</div>
              <div className={`text-lg font-mono font-bold ${pctColor}`}>{price.percentile_90d.toFixed(0)}th</div>
            </div>
          )}
          {/* Volatility */}
          {ind && ind.volatility_20d > 0 && (
            <div>
              <div className="text-[10px] text-eve-dim uppercase tracking-wider mb-0.5">{t("plexVolatility")}</div>
              <div className={`text-lg font-mono font-bold ${volColor}`}>{(ind.volatility_20d * 100).toFixed(1)}%</div>
              <div className={`text-[10px] ${volColor}`}>{volLabel}</div>
            </div>
          )}
        </div>
      ) : (
        <div className="text-sm text-eve-dim">{t("plexNoData")}</div>
      )}

      {/* Technical Indicators (inline) */}
      {ind && (
        <>
          <div className="border-t border-eve-border/30 my-3" />
          <h4 className="text-[10px] font-semibold text-eve-dim uppercase tracking-wider mb-2">{t("plexIndicators")}</h4>
          <div className="grid grid-cols-3 sm:grid-cols-4 lg:grid-cols-8 gap-2">
            <MetricCell label="SMA(7)" value={formatISK(ind.sma7)} />
            <MetricCell label="SMA(30)" value={formatISK(ind.sma30)} />
            <MetricCell label="RSI(14)" value={ind.rsi.toFixed(1)} color={ind.rsi < 30 ? "text-eve-success" : ind.rsi > 70 ? "text-eve-error" : "text-eve-text"} />
            <MetricCell label="BB Upper" value={formatISK(ind.bollinger_upper)} />
            <MetricCell label="BB Lower" value={formatISK(ind.bollinger_lower)} />
            <MetricCell label="1d" value={`${ind.change_24h >= 0 ? "+" : ""}${ind.change_24h.toFixed(2)}%`} color={ind.change_24h >= 0 ? "text-eve-success" : "text-eve-error"} />
            <MetricCell label="7d" value={`${ind.change_7d >= 0 ? "+" : ""}${ind.change_7d.toFixed(2)}%`} color={ind.change_7d >= 0 ? "text-eve-success" : "text-eve-error"} />
            <MetricCell label="30d" value={`${ind.change_30d >= 0 ? "+" : ""}${ind.change_30d.toFixed(2)}%`} color={ind.change_30d >= 0 ? "text-eve-success" : "text-eve-error"} />
          </div>
        </>
      )}
    </div>
  );
}

export function ArbitrageRow({ arb, onClick }: { arb: ArbitragePath; onClick: () => void }) {
  const { t } = useI18n();
  // Break-even color: green if current PLEX price is below break-even (profitable zone)
  const beColor = arb.break_even_plex > 0 ? "text-eve-dim" : "";

  return (
    <tr className={`border-b border-eve-border/50 hover:bg-eve-panel/50 transition-colors cursor-pointer ${arb.viable ? "" : arb.no_data ? "opacity-40" : "opacity-50"}`} onClick={onClick}>
      <td className="py-1.5 px-2">
        <div className="flex flex-col gap-0">
          <div className="flex items-center gap-1.5">
            <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${arb.no_data ? "bg-eve-warning" : arb.viable ? "bg-eve-success" : "bg-eve-error"}`} />
            <span className="text-eve-text hover:text-eve-accent transition-colors">{arb.name}</span>
            {arb.no_data && <span className="text-[9px] text-eve-warning uppercase tracking-wider">{t("plexNoDataShort")}</span>}
          </div>
          {!arb.no_data && arb.break_even_plex > 0 && (
            <span className={`text-[9px] ${beColor} ml-3`}>BE: {formatISK(arb.break_even_plex)}/PLEX</span>
          )}
        </div>
      </td>
      <td className="py-1.5 px-2 text-right font-mono text-eve-dim">{arb.plex_cost > 0 ? arb.plex_cost : "—"}</td>
      <td className="py-1.5 px-2 text-right font-mono text-eve-text">{arb.no_data ? "—" : formatISK(arb.cost_isk)}</td>
      <td className="py-1.5 px-2 text-right font-mono text-eve-text">{arb.no_data ? "—" : formatISK(arb.revenue_isk)}</td>
      <td className={`py-1.5 px-2 text-right font-mono font-semibold ${arb.no_data ? "text-eve-dim" : arb.profit_isk >= 0 ? "text-eve-success" : "text-eve-error"}`}>
        <div>{arb.no_data ? "—" : `${arb.profit_isk >= 0 ? "+" : ""}${formatISK(arb.profit_isk)}`}</div>
        {!arb.no_data && arb.slippage_pct !== 0 && (
          <div className="text-[9px] text-eve-warning">{arb.slippage_pct.toFixed(2)}% slip</div>
        )}
      </td>
      <td className={`py-1.5 px-2 text-right font-mono font-semibold ${arb.no_data ? "text-eve-dim" : arb.roi >= 0 ? "text-eve-success" : "text-eve-error"}`}>
        <div>{arb.no_data ? "—" : `${arb.roi >= 0 ? "+" : ""}${arb.roi.toFixed(1)}%`}</div>
        {!arb.no_data && arb.isk_per_hour > 0 && (
          <div className="text-[9px] text-eve-dim">{formatISK(arb.isk_per_hour)}/{t("plexISKPerHour").split("/")[1] || "hr"}</div>
        )}
        {!arb.no_data && arb.est_minutes === 0 && arb.type === "spread" && (
          <div className="text-[9px] text-eve-dim italic">{t("plexPassive")}</div>
        )}
      </td>
    </tr>
  );
}

/**
 * A spread play reads differently from an arbitrage path, so it gets its own row.
 *
 * `ArbitrageRow` renders six cells, the second of which is PLEX-needed — always
 * zero for a spread path, because these plays touch neither PLEX nor the NES.
 * It was being used under a five-column spread header, which shifted the whole
 * body one column left of its labels.
 *
 * The columns also change: "cost / revenue" is the wrong vocabulary for market
 * making. What you want to see is the buy order you place, the sell order you
 * place, and the raw gap between them before fees eat into it.
 */
export function SpreadRow({ arb, onClick }: { arb: ArbitragePath; onClick: () => void }) {
  const { t } = useI18n();
  const dim = arb.no_data ? "opacity-40" : arb.viable ? "" : "opacity-50";
  const signed = (n: number) => `${n >= 0 ? "+" : ""}${formatISK(n)}`;

  return (
    <tr
      className={`border-b border-eve-border/50 hover:bg-eve-panel/50 transition-colors cursor-pointer ${dim}`}
      onClick={onClick}
    >
      <td className="py-2 px-2">
        <div className="flex items-center gap-1.5">
          <span
            className={`w-1.5 h-1.5 rounded-full shrink-0 ${arb.no_data ? "bg-eve-warning" : arb.viable ? "bg-eve-success" : "bg-eve-error"}`}
          />
          {/* Every spread path is named "<Item> Spread (Market Make)"; inside a
              table already titled Spread Trading, only the item is news. */}
          <span className="text-eve-text font-medium hover:text-eve-accent transition-colors">
            {arb.name.replace(" Spread (Market Make)", "")}
          </span>
          {arb.no_data && (
            <span className="text-[9px] text-eve-warning uppercase tracking-wider">{t("plexNoDataShort")}</span>
          )}
        </div>
      </td>
      <td className="py-2 px-2 text-right font-mono text-eve-success">
        {arb.no_data ? "—" : formatISK(arb.cost_isk)}
      </td>
      <td className="py-2 px-2 text-right font-mono text-eve-error">
        {arb.no_data ? "—" : formatISK(arb.revenue_gross)}
      </td>
      <td className="py-2 px-2 text-right font-mono text-eve-text">
        {arb.no_data ? "—" : formatISK(arb.revenue_gross - arb.cost_isk)}
      </td>
      <td
        className={`py-2 px-2 text-right font-mono font-semibold ${arb.no_data ? "text-eve-dim" : arb.profit_isk >= 0 ? "text-eve-success" : "text-eve-error"}`}
      >
        {arb.no_data ? "—" : signed(arb.profit_isk)}
      </td>
      <td
        className={`py-2 px-2 text-right font-mono font-semibold ${arb.no_data ? "text-eve-dim" : arb.roi >= 0 ? "text-eve-success" : "text-eve-error"}`}
      >
        <div>{arb.no_data ? "—" : `${arb.roi >= 0 ? "+" : ""}${arb.roi.toFixed(1)}%`}</div>
        {!arb.no_data && <div className="text-[9px] text-eve-dim italic">{t("plexPassive")}</div>}
      </td>
    </tr>
  );
}

export function MarketDepthCard({ depth }: { depth: MarketDepthInfo }) {
  const { t } = useI18n();
  const fmtHrs = (h: number) => h > 0 ? `~${h < 1 ? "<1" : h.toFixed(1)} ${t("plexHours")}` : "";
  return (
    <div className="bg-eve-dark border border-eve-border rounded-sm p-3">
      <h3 className="text-xs font-semibold text-eve-dim uppercase tracking-wider mb-1">{t("plexMarketDepth")}</h3>
      <p className="text-[10px] text-eve-dim mb-2">{t("plexMarketDepthHint")}</p>
      <div className="space-y-2 text-xs">
        {/* PLEX sell depth */}
        <div className="border border-eve-border/50 rounded-sm p-2">
          <div className="text-[10px] text-eve-dim uppercase tracking-wider font-medium mb-1">{t("plexDepthPLEX")} {t("plexDepthSellOrders")}</div>
          <div className="flex justify-between">
            <span className="text-eve-dim">{t("plexDepthVolume")}</span>
            <span className="font-mono text-eve-text">{depth.plex_sell_depth_5.total_volume.toLocaleString()}</span>
          </div>
          <div className="flex justify-between">
            <span className="text-eve-dim">{t("plexDepthLevels")}</span>
            <span className="font-mono text-eve-text">{depth.plex_sell_depth_5.levels}</span>
          </div>
          {depth.plex_sell_depth_5.best_price > 0 && (
            <div className="flex justify-between">
              <span className="text-eve-dim">Best → Worst</span>
              <span className="font-mono text-eve-text text-[11px]">{formatISK(depth.plex_sell_depth_5.best_price)} → {formatISK(depth.plex_sell_depth_5.worst_price)}</span>
            </div>
          )}
          {depth.plex_fill_hours > 0 && (
            <div className="flex justify-between">
              <span className="text-eve-dim">{t("plexEstFillTime")} (100x)</span>
              <span className="font-mono text-eve-dim">{fmtHrs(depth.plex_fill_hours)}</span>
            </div>
          )}
        </div>

        {/* Item depth grid */}
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-1.5">
          <DepthItem label={t("plexDepthExtractor")} sell={depth.extractor_sell_qty} buy={depth.extractor_buy_qty} fillHours={depth.extractor_fill_hours} />
          <DepthItem label={t("plexDepthInjector")} sell={depth.injector_sell_qty} buy={depth.injector_buy_qty} fillHours={depth.injector_fill_hours} />
        </div>
      </div>
    </div>
  );
}

function DepthItem({ label, sell, buy, fillHours }: { label: string; sell: number; buy: number; fillHours?: number }) {
  const { t } = useI18n();
  return (
    <div className="border border-eve-border/30 rounded-sm p-1.5 text-center">
      <div className="text-[10px] text-eve-dim uppercase tracking-wider font-medium mb-1">{label}</div>
      <div className="text-[10px]">
        <span className="text-eve-error">{t("plexDepthSellOrders").charAt(0)}: </span>
        <span className="font-mono text-eve-text">{sell.toLocaleString()}</span>
      </div>
      <div className="text-[10px]">
        <span className="text-eve-success">{t("plexDepthBuyOrders").charAt(0)}: </span>
        <span className="font-mono text-eve-text">{buy.toLocaleString()}</span>
      </div>
      {fillHours != null && fillHours > 0 && (
        <div className="text-[9px] text-eve-dim mt-0.5">
          ~{fillHours < 1 ? "<1" : fillHours.toFixed(1)} {t("plexHours")}
        </div>
      )}
    </div>
  );
}

// ===================================================================
// Injection Tiers Card
// ===================================================================

export function InjectionTiersCard({ tiers }: { tiers: InjectionTier[] }) {
  const { t } = useI18n();
  return (
    <div className="bg-eve-dark border border-eve-border rounded-sm p-3">
      <h3 className="text-xs font-semibold text-eve-dim uppercase tracking-wider mb-1">{t("plexInjectionTiers")}</h3>
      <p className="text-[10px] text-eve-dim mb-2">{t("plexInjectionTiersHint")}</p>
      <table className="w-full text-xs">
        <thead>
          <tr className="text-eve-dim border-b border-eve-border">
            <th className="text-left py-1 px-2 font-medium">{t("plexTierLabel")}</th>
            <th className="text-right py-1 px-2 font-medium">{t("plexSPReceived")}</th>
            <th className="text-right py-1 px-2 font-medium">{t("plexISKPerSP")}</th>
            <th className="text-right py-1 px-2 font-medium">{t("plexEfficiency")}</th>
          </tr>
        </thead>
        <tbody>
          {tiers.map((tier, i) => {
            const effColor = tier.efficiency >= 80 ? "text-eve-success" : tier.efficiency >= 50 ? "text-eve-warning" : "text-eve-error";
            return (
              <tr key={i} className="border-b border-eve-border/30">
                <td className="py-1 px-2 text-eve-text">{tier.label}</td>
                <td className="py-1 px-2 text-right font-mono text-eve-text">{tier.sp_received.toLocaleString()}</td>
                <td className="py-1 px-2 text-right font-mono text-eve-text">{formatISK(tier.isk_per_sp)}</td>
                <td className={`py-1 px-2 text-right font-mono font-semibold ${effColor}`}>{tier.efficiency.toFixed(0)}%</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function MetricCell({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div className="text-center">
      <div className="text-[10px] text-eve-dim uppercase tracking-wider">{label}</div>
      <div className={`text-sm font-mono font-semibold ${color || "text-eve-text"}`}>{value}</div>
    </div>
  );
}
