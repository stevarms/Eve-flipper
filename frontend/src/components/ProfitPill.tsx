import { useCallback, useEffect, useRef, useState } from "react";
import { getJournalSummary, type JournalTotals } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { formatIskSigned as formatIskSignedLib } from "../lib/format";

// ProfitPill — always-visible 30d P&L chip in the app's top bar. Clicking
// it takes the user to the Trade Journal tab; the pill exists so the
// feature isn't buried behind a tab switch.
//
// Data: fetch /api/auth/journal/summary?scope=all&days=30 on mount, on
// window focus, and after any manual sync (parent triggers via refreshKey).
// Backend caches 60s so re-fetches are cheap.

interface Props {
  isLoggedIn: boolean;
  onOpen: () => void;
  /** Increment to force a refetch (e.g. after Trade Journal Sync). */
  refreshKey?: number;
}

/** Local presentation of the shared formatter (lib/format.ts).
 *  Tighter decimals than elsewhere — this renders in a narrow header pill. */
function formatIskSigned(v: number): string {
  return formatIskSignedLib(v, undefined, {
    maxTier: "T",
    space: false,
    decimals: { t: 2, b: 2, m: 1, k: 0, unit: 0 },
  });
}

export function ProfitPill({ isLoggedIn, onOpen, refreshKey }: Props) {
  const { t } = useI18n();
  const [totals, setTotals] = useState<JournalTotals | null>(null);
  const [loading, setLoading] = useState(false);
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const load = useCallback(async () => {
    if (!isLoggedIn) {
      setTotals(null);
      return;
    }
    setLoading(true);
    try {
      const s = await getJournalSummary({ scope: { include_all: true }, days: 30 });
      if (mountedRef.current) setTotals(s.totals);
    } catch {
      // Silent — the pill is optional UI; errors shouldn't disrupt.
    } finally {
      if (mountedRef.current) setLoading(false);
    }
  }, [isLoggedIn]);

  useEffect(() => {
    void load();
  }, [load, refreshKey]);

  // Refetch on window focus so returning to the app shows a fresh number.
  useEffect(() => {
    const onFocus = () => void load();
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, [load]);

  if (!isLoggedIn) return null;

  const value = totals?.combined_pnl ?? 0;
  const tone = value >= 0 ? "text-eve-profit border-eve-profit/40" : "text-eve-error border-eve-error/40";
  const tradingPart = totals ? formatIskSigned(totals.trading_pnl) : "…";
  const mfgPart = totals ? formatIskSigned(totals.manufacturing_pnl) : "…";
  const title = totals
    ? t("profitPillTooltip", {
        trading: tradingPart,
        mfg: mfgPart,
      })
    : t("profitPillLoading");

  return (
    <button
      type="button"
      onClick={onOpen}
      title={title}
      className={`inline-flex items-center gap-1.5 h-9 px-2 rounded-sm border bg-eve-panel hover:bg-eve-accent/5 transition-colors ${tone}`}
      aria-label={t("profitPillAria")}
    >
      <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
        <polyline points="3 17 9 11 13 15 21 7" />
        <polyline points="14 7 21 7 21 14" />
      </svg>
      <span className="flex items-baseline gap-1 text-[11px]">
        <span className="text-eve-dim uppercase tracking-wider">{t("profitPill30d")}</span>
        <span className="font-mono font-semibold">
          {loading && !totals ? "…" : formatIskSigned(value)}
        </span>
      </span>
    </button>
  );
}
