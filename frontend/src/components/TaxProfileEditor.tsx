import { useMemo, useState, type ReactNode } from "react";
import { getCharacterInfo, type CharacterScope } from "@/lib/api";
import { normalizeTaxProfile, type TaxProfile } from "@/lib/taxProfile";
import type { ScanParams } from "@/lib/types";
import { useI18n } from "@/lib/i18n";
import {
  SettingsCheckbox,
  SettingsNumberInput,
} from "./TabSettingsPanel";

const SKILL_ACCOUNTING = 16622;
const SKILL_BROKER_RELATIONS = 3446;

interface TaxProfileEditorProps {
  value: Partial<ScanParams> | TaxProfile;
  onChange?: (profile: TaxProfile) => void;
  isLoggedIn?: boolean;
  characterScope?: CharacterScope;
  compact?: boolean;
  title?: string;
  subtitle?: string;
  className?: string;
}

function clampPercent(value: number, max = 100): number {
  if (!Number.isFinite(value)) return 0;
  return Math.max(0, Math.min(max, value));
}

function roundFee(value: number): number {
  return Number(value.toFixed(2));
}

function TaxProfileField({
  label,
  compact,
  children,
}: {
  label: string;
  compact: boolean;
  children: ReactNode;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <label
        className={`block min-w-0 text-[11px] font-medium uppercase tracking-wider text-eve-dim ${
          compact ? "h-4 whitespace-nowrap overflow-hidden text-ellipsis leading-4" : ""
        }`}
        title={label}
      >
        {label}
      </label>
      {children}
    </div>
  );
}

export function TaxProfileEditor({
  value,
  onChange,
  isLoggedIn = false,
  characterScope,
  compact = false,
  title = "Tax profile",
  subtitle = "Single source for scanner, station, route, backtest, PLEX and Mission Control.",
  className = "",
}: TaxProfileEditorProps) {
  const { t } = useI18n();
  const tax = useMemo(() => normalizeTaxProfile(value), [value]);
  const [loading, setLoading] = useState(false);
  const [message, setMessage] = useState<string | null>(null);

  const disabled = !onChange;
  const update = (patch: Partial<TaxProfile>) => {
    if (!onChange) return;
    const next = normalizeTaxProfile({ ...tax, ...patch });
    onChange({
      ...next,
      broker_fee_percent: clampPercent(next.broker_fee_percent, 100),
      sales_tax_percent: clampPercent(next.sales_tax_percent, 100),
      buy_broker_fee_percent: clampPercent(next.buy_broker_fee_percent, 100),
      sell_broker_fee_percent: clampPercent(next.sell_broker_fee_percent, 100),
      buy_sales_tax_percent: clampPercent(next.buy_sales_tax_percent, 100),
      sell_sales_tax_percent: clampPercent(next.sell_sales_tax_percent, 100),
    });
  };

  const setSplit = (enabled: boolean) => {
    if (enabled) {
      update({
        split_trade_fees: true,
        buy_broker_fee_percent: tax.broker_fee_percent,
        sell_broker_fee_percent: tax.broker_fee_percent,
        buy_sales_tax_percent: 0,
        sell_sales_tax_percent: tax.sales_tax_percent,
      });
      return;
    }
    update({
      split_trade_fees: false,
      broker_fee_percent: tax.sell_broker_fee_percent,
      sales_tax_percent: tax.sell_sales_tax_percent,
    });
  };

  const syncFromESI = async () => {
    if (!isLoggedIn || !onChange) return;
    setLoading(true);
    setMessage(null);
    try {
      const info = await getCharacterInfo(characterScope);
      const skills = info.skills?.skills ?? [];
      const accounting = skills.find((s) => s.skill_id === SKILL_ACCOUNTING)?.active_skill_level ?? 0;
      const brokerRelations = skills.find((s) => s.skill_id === SKILL_BROKER_RELATIONS)?.active_skill_level ?? 0;
      const salesTax = roundFee(8 * (1 - 0.11 * accounting));
      const brokerFee = roundFee(Math.max(0, 3 - brokerRelations * 0.3));
      update({
        broker_fee_percent: brokerFee,
        sales_tax_percent: salesTax,
        buy_broker_fee_percent: brokerFee,
        sell_broker_fee_percent: brokerFee,
        buy_sales_tax_percent: 0,
        sell_sales_tax_percent: salesTax,
      });
      setMessage(`Accounting L${accounting}: ${salesTax}% tax, Broker Relations L${brokerRelations}: ${brokerFee}% broker`);
    } catch {
      setMessage("ESI fee sync failed");
    } finally {
      setLoading(false);
    }
  };

  const buyFee = tax.split_trade_fees
    ? tax.buy_broker_fee_percent + tax.buy_sales_tax_percent
    : tax.broker_fee_percent;
  const sellFee = tax.split_trade_fees
    ? tax.sell_broker_fee_percent + tax.sell_sales_tax_percent
    : tax.broker_fee_percent + tax.sales_tax_percent;

  return (
    <div className={`space-y-3 ${className}`}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-eve-accent text-sm">%</span>
            <h3 className="text-sm font-semibold uppercase tracking-wider text-eve-text">
              {title}
            </h3>
          </div>
          {!compact && (
            <p className="mt-1 text-xs text-eve-dim max-w-3xl">
              {subtitle}
            </p>
          )}
        </div>
        <button
          type="button"
          disabled={!isLoggedIn || loading || disabled}
          onClick={() => void syncFromESI()}
          title={isLoggedIn ? "Auto-fill from Accounting and Broker Relations skill levels" : "Login via ESI to use skill sync"}
          className="px-2.5 py-1 rounded-sm border border-eve-accent/40 bg-eve-accent/10 text-[11px] font-semibold uppercase tracking-wider text-eve-accent hover:bg-eve-accent/20 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
        >
          {loading ? "Syncing..." : "Sync ESI skills"}
        </button>
      </div>

      <div
        className={`grid items-end gap-x-3 gap-y-3 ${
          compact ? "" : "grid-cols-1 sm:grid-cols-2 lg:grid-cols-4"
        }`}
        style={
          compact
            ? { gridTemplateColumns: "repeat(auto-fit, minmax(158px, 1fr))" }
            : undefined
        }
      >
        <TaxProfileField label={compact ? "Split fees" : t("splitTradeFees")} compact={compact}>
          <div className="h-[34px] px-2.5 py-1.5 bg-eve-input border border-eve-border rounded-sm text-eve-text text-sm flex items-center justify-between gap-3">
            <span className="text-eve-dim text-xs truncate">
              {compact ? (tax.split_trade_fees ? "On" : "Off") : tax.split_trade_fees ? "Split" : "Legacy"}
            </span>
            <SettingsCheckbox checked={tax.split_trade_fees} onChange={setSplit} />
          </div>
        </TaxProfileField>

        {!tax.split_trade_fees ? (
          <>
            <TaxProfileField label={compact ? "Broker %" : t("brokerFee")} compact={compact}>
              <SettingsNumberInput
                value={tax.broker_fee_percent}
                onChange={(v) => update({ broker_fee_percent: clampPercent(v, 10) })}
                min={0}
                max={10}
                step={0.1}
              />
            </TaxProfileField>
            <TaxProfileField label={compact ? "Sales tax %" : t("salesTax")} compact={compact}>
              <SettingsNumberInput
                value={tax.sales_tax_percent}
                onChange={(v) => update({ sales_tax_percent: clampPercent(v, 100), sell_sales_tax_percent: clampPercent(v, 100) })}
                min={0}
                max={100}
                step={0.1}
              />
            </TaxProfileField>
          </>
        ) : (
          <>
            <TaxProfileField label={compact ? "Buy broker %" : t("buyBrokerFee")} compact={compact}>
              <SettingsNumberInput
                value={tax.buy_broker_fee_percent}
                onChange={(v) => update({ buy_broker_fee_percent: clampPercent(v, 10) })}
                min={0}
                max={10}
                step={0.1}
              />
            </TaxProfileField>
            <TaxProfileField label={compact ? "Sell broker %" : t("sellBrokerFee")} compact={compact}>
              <SettingsNumberInput
                value={tax.sell_broker_fee_percent}
                onChange={(v) => update({ sell_broker_fee_percent: clampPercent(v, 10), broker_fee_percent: clampPercent(v, 10) })}
                min={0}
                max={10}
                step={0.1}
              />
            </TaxProfileField>
            <TaxProfileField label={compact ? "Buy tax %" : t("buySalesTax")} compact={compact}>
              <SettingsNumberInput
                value={tax.buy_sales_tax_percent}
                onChange={(v) => update({ buy_sales_tax_percent: clampPercent(v, 100) })}
                min={0}
                max={100}
                step={0.1}
              />
            </TaxProfileField>
            <TaxProfileField label={compact ? "Sell tax %" : t("sellSalesTax")} compact={compact}>
              <SettingsNumberInput
                value={tax.sell_sales_tax_percent}
                onChange={(v) => update({ sell_sales_tax_percent: clampPercent(v, 100), sales_tax_percent: clampPercent(v, 100) })}
                min={0}
                max={100}
                step={0.1}
              />
            </TaxProfileField>
          </>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-2 text-[11px] text-eve-dim">
        <span className="px-2 py-1 border border-eve-border/60 bg-eve-dark/45 rounded-sm">
          Buy cost: <span className="text-eve-text font-mono">{buyFee.toFixed(2)}%</span>
        </span>
        <span className="px-2 py-1 border border-eve-border/60 bg-eve-dark/45 rounded-sm">
          Sell cost: <span className="text-eve-text font-mono">{sellFee.toFixed(2)}%</span>
        </span>
        {message && (
          <span className={`font-mono ${message.includes("failed") ? "text-eve-error" : "text-eve-success"}`}>
            {message}
          </span>
        )}
      </div>
    </div>
  );
}
