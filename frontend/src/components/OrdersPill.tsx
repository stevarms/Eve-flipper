import { useCallback, useEffect, useRef, useState } from "react";
import { getOrderDesk } from "../lib/api";
import type { OrderDeskSummary } from "../lib/types";
import { loadOrdersPrefs } from "../lib/ordersPrefs";
import { useI18n } from "../lib/i18n";

// OrdersPill — always-visible chip in the app's top bar showing how many
// active orders need action (reprice + cancel counts). Mirrors
// ProfitPill.tsx exactly, just against /api/auth/orders/desk. Click routes
// to the Orders tab. Hidden when not logged in.

interface Props {
  isLoggedIn: boolean;
  onOpen: () => void;
  /** Increment to force a refetch (e.g. after manual sync). */
  refreshKey?: number;
}

export function OrdersPill({ isLoggedIn, onOpen, refreshKey }: Props) {
  const { t } = useI18n();
  const [summary, setSummary] = useState<OrderDeskSummary | null>(null);
  const [loading, setLoading] = useState(false);
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const lastLoadedAtRef = useRef(0);

  const load = useCallback(
    async (force = false) => {
      if (!isLoggedIn) {
        setSummary(null);
        return;
      }
      setLoading(true);
      try {
        const resp = await getOrderDesk({ characterId: "all", force });
        lastLoadedAtRef.current = Date.now();
        if (mountedRef.current) setSummary(resp.summary);
      } catch {
        // Silent — the pill is optional UI; errors shouldn't disrupt.
      } finally {
        if (mountedRef.current) setLoading(false);
      }
    },
    [isLoggedIn],
  );

  useEffect(() => {
    void load(refreshKey != null && refreshKey > 0);
  }, [load, refreshKey]);

  // The pill lives in the top bar and is therefore mounted even while the
  // Orders tab is open, so an unguarded focus listener here doubled the cost
  // of every alt-tab back from EVE. It reuses the Orders tab's own refresh
  // interval; with auto-refresh switched off it still falls back to a slow
  // poll, because a badge that silently stops counting is worse than a stale
  // one.
  useEffect(() => {
    const onFocus = () => {
      const { refreshMinutes } = loadOrdersPrefs();
      const gapMs = (refreshMinutes > 0 ? refreshMinutes : 10) * 60_000;
      if (Date.now() - lastLoadedAtRef.current < gapMs) return;
      void load();
    };
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, [load]);

  if (!isLoggedIn) return null;

  const total = summary?.total_orders ?? 0;
  const needAction =
    (summary?.needs_reprice ?? 0) + (summary?.needs_cancel ?? 0);
  const tone =
    needAction > 0
      ? "text-amber-400 border-amber-400/40"
      : "text-eve-dim border-eve-border";
  const label =
    loading && !summary
      ? "…"
      : needAction > 0
        ? `${total} · ⚠ ${needAction}`
        : String(total);
  const title = t("ordersPillTooltip", {
    total,
    reprice: summary?.needs_reprice ?? 0,
    cancel: summary?.needs_cancel ?? 0,
  });

  return (
    <button
      type="button"
      onClick={onOpen}
      title={title}
      className={`inline-flex items-center gap-1.5 h-9 px-2 rounded-sm border bg-eve-panel hover:bg-eve-accent/5 transition-colors ${tone}`}
      aria-label={t("ordersPillAria")}
    >
      <svg
        className="w-4 h-4"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        aria-hidden="true"
      >
        <path d="M3 6h18M3 12h18M3 18h18" />
      </svg>
      <span className="flex items-baseline gap-1 text-[11px]">
        <span className="text-eve-dim uppercase tracking-wider">
          {t("ordersPillLabel")}
        </span>
        <span className="font-mono font-semibold">{label}</span>
      </span>
    </button>
  );
}
