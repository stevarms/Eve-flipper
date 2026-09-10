import { useEffect, useState } from "react";
import type { JournalFeeProfile } from "../../lib/api";
import { useI18n } from "../../lib/i18n";

// JournalFeeStrip — states the sell-side rates every profit figure on this tab
// was computed with, and lets the user override them for the session.
//
// The tab it replaces showed two bare number inputs defaulted to 8% / 1%.
// Nothing said where those came from, and 1% silently understated an untrained
// broker fee by two thirds — so the displayed profit was optimistic for anyone
// short of Broker Relations V, with no way to tell from the screen. The rates
// now come from the user's config or, failing that, their actual skills, and
// the strip names which.

interface Props {
  /** The profile the last response was computed with, if one has arrived. */
  profile: JournalFeeProfile | null;
  override: { salesTax: number; brokerFee: number } | null;
  onChange: (next: { salesTax: number; brokerFee: number } | null) => void;
}

export function JournalFeeStrip({ profile, override, onChange }: Props) {
  const { t } = useI18n();
  const [editing, setEditing] = useState(false);
  const [salesTax, setSalesTax] = useState("");
  const [brokerFee, setBrokerFee] = useState("");

  // Seed the inputs from whatever is currently in force, so opening the editor
  // starts from the real rates rather than from a blank pair the user has to
  // reconstruct.
  useEffect(() => {
    if (!editing || !profile) return;
    setSalesTax(String(override?.salesTax ?? profile.sales_tax_percent));
    setBrokerFee(String(override?.brokerFee ?? profile.broker_fee_percent));
  }, [editing, profile, override]);

  if (!profile) return null;

  const provenance = () => {
    switch (profile.source) {
      case "override":
        return t("journalFeesFromOverride");
      case "config":
        return t("journalFeesFromConfig");
      case "skills":
        return t("journalFeesFromSkills", {
          accounting: romanLevel(profile.accounting_level ?? 0),
          broker: romanLevel(profile.broker_relations_level ?? 0),
        });
      default:
        return t("journalFeesFromDefault");
    }
  };

  const apply = () => {
    const st = parseFloat(salesTax);
    const bf = parseFloat(brokerFee);
    // Both or neither: the backend charges these inside the FIFO match, and a
    // half-pair would leave the strip unable to say what it is overriding.
    if (!Number.isFinite(st) || !Number.isFinite(bf)) return;
    if (st < 0 || st > 100 || bf < 0 || bf > 100) return;
    onChange({ salesTax: st, brokerFee: bf });
    setEditing(false);
  };

  return (
    <div className="flex flex-wrap items-center gap-2 text-[11px]">
      <span className="text-eve-dim uppercase tracking-wider">{t("journalFeesLabel")}</span>
      <span className="font-mono text-eve-text">
        {profile.sales_tax_percent.toFixed(2)}% / {profile.broker_fee_percent.toFixed(2)}%
      </span>
      <span className="text-eve-dim">{provenance()}</span>
      {!editing && (
        <button
          type="button"
          onClick={() => setEditing(true)}
          className="text-eve-accent hover:underline"
        >
          {t("journalFeesOverrideBtn")}
        </button>
      )}
      {!editing && override && (
        <button
          type="button"
          onClick={() => onChange(null)}
          className="text-eve-dim hover:text-eve-text hover:underline"
        >
          {t("journalFeesResetBtn")}
        </button>
      )}
      {editing && (
        <span className="flex flex-wrap items-center gap-1">
          <span className="text-eve-dim">{t("pnlSalesTax")}</span>
          <input
            type="number"
            min={0}
            max={100}
            step={0.1}
            value={salesTax}
            onChange={(e) => setSalesTax(e.target.value)}
            className="w-16 px-1 py-0.5 rounded-sm border border-eve-border bg-eve-dark text-eve-text"
          />
          <span className="text-eve-dim">{t("pnlBrokerFee")}</span>
          <input
            type="number"
            min={0}
            max={100}
            step={0.1}
            value={brokerFee}
            onChange={(e) => setBrokerFee(e.target.value)}
            className="w-16 px-1 py-0.5 rounded-sm border border-eve-border bg-eve-dark text-eve-text"
          />
          <button
            type="button"
            onClick={apply}
            className="rounded-sm border border-eve-accent/60 bg-eve-accent/10 px-2 py-0.5 text-eve-accent hover:bg-eve-accent/20"
          >
            {t("journalFeesApplyBtn")}
          </button>
          <button
            type="button"
            onClick={() => setEditing(false)}
            className="text-eve-dim hover:text-eve-text"
          >
            {t("journalFeesCancelBtn")}
          </button>
        </span>
      )}
    </div>
  );
}

const ROMAN = ["0", "I", "II", "III", "IV", "V"];

function romanLevel(level: number): string {
  return ROMAN[Math.max(0, Math.min(5, level))];
}
