import { useEffect, useState, useCallback } from "react";
import { Modal } from "./Modal";
import {
  cancelHostedPayment,
  getCharacterRoles,
  getHostedAccess,
  markHostedPaymentSent,
  requestHostedPayment,
  type CharacterScope,
} from "../lib/api";
import { useI18n } from "../lib/i18n";
import { trackClientTelemetry } from "../lib/telemetry";
import type { AuthCharacter, CharacterRoles, HostedAccessStatus, SecurityVaultStatus } from "../lib/types";
import { HostedAccessTab } from "./character-popup/HostedAccessTab";
import { OverviewTab } from "./character-popup/OverviewTab";
import { TabBtn } from "./character-popup/TabButton";
import { AchievementLibraryPanel, useAchievements } from "./achievements";
import { useCharacterInfo } from "./character/CharacterScopeProvider";

/* The dialog is now about the *character*, not about trading.
 *
 * Orders, Transactions, Ledger, Industry Jobs, PI, P&L, Edge, Risk and
 * Optimizer were nine working tools with no entry point in the navigation;
 * they are workspace tabs now (see lib/cockpit.ts WORKSPACE_META and
 * components/character/CharacterTools.tsx). What is left here is what is
 * genuinely about the account: who you are logged in as, what the scope is,
 * the security vault, and achievements.
 *
 * Scope and the character payload come from the shared CharacterScopeProvider,
 * so switching character here switches it for every promoted tab too. */

interface CharacterPopupProps {
  open: boolean;
  onClose: () => void;
  characters: AuthCharacter[];
  onDeleteCharacter: (characterId: number) => Promise<void>;
  onAddCharacter: () => Promise<void>;
  onAuthRefresh: () => Promise<void>;
  securityVault?: SecurityVaultStatus;
}

type CharTab = "overview" | "achievements" | "access";
const SCOPE_COLLAPSE_KEY = "eve-character-scope-collapsed";

export function CharacterPopup({
  open,
  onClose,
  characters,
  onDeleteCharacter,
  onAddCharacter,
  onAuthRefresh,
  securityVault,
}: CharacterPopupProps) {
  const { t } = useI18n();
  const { pendingCount: achievementPendingCount, unlockedCount: achievementUnlockedCount } = useAchievements();
  const {
    scope: selectedScope,
    selectScope,
    scopeBusy: contextScopeBusy,
    scopeError,
    data,
    loading,
    error: dataError,
    refresh,
    formatIsk,
    formatNumber,
  } = useCharacterInfo();
  const [error, setError] = useState<string | null>(null);
  const [tab, setTab] = useState<CharTab>("overview");
  const [corpRoles, setCorpRoles] = useState<CharacterRoles | null>(null);
  const [corpRolesLoading, setCorpRolesLoading] = useState(false);
  const [hostedAccess, setHostedAccess] = useState<HostedAccessStatus | null>(null);
  const [hostedAccessLoading, setHostedAccessLoading] = useState(false);
  const [hostedAccessError, setHostedAccessError] = useState<string | null>(null);
  const [hostedAccessCheckedAt, setHostedAccessCheckedAt] = useState<Date | null>(null);
  const [scopeBusy, setScopeBusy] = useState(false);
  const [deletingCharacterId, setDeletingCharacterId] = useState<number | null>(null);
  const [scopeCollapsed, setScopeCollapsed] = useState(() => {
    try {
      return localStorage.getItem(SCOPE_COLLAPSE_KEY) === "1";
    } catch {
      return false;
    }
  });

  const selectedCharacter = selectedScope === "all"
    ? null
    : characters.find((c) => c.character_id === selectedScope);
  const modalTitle = selectedScope === "all"
    ? t("charAllCharacters")
    : selectedCharacter?.character_name ?? t("charOverview");
  const hostedBillingEnabled = hostedAccess?.hosted === true;

  const loadHostedAccess = useCallback(() => {
    setHostedAccessLoading(true);
    setHostedAccessError(null);
    getHostedAccess(selectedScope)
      .then(setHostedAccess)
      .catch((e) => setHostedAccessError(e.message))
      .finally(() => {
        setHostedAccessCheckedAt(new Date());
        setHostedAccessLoading(false);
      });
  }, [selectedScope]);

  const handleHostedPaymentRequest = useCallback(async (planId: string) => {
    await requestHostedPayment(planId, selectedScope);
    loadHostedAccess();
  }, [loadHostedAccess, selectedScope]);

  const handleHostedPaymentCancel = useCallback(async () => {
    await cancelHostedPayment(selectedScope);
    loadHostedAccess();
  }, [loadHostedAccess, selectedScope]);

  const handleHostedPaymentMarkSent = useCallback(async (code: string) => {
    await markHostedPaymentSent(code, selectedScope);
    loadHostedAccess();
  }, [loadHostedAccess, selectedScope]);

  useEffect(() => {
    if (!open) return;
    loadHostedAccess();
    if (selectedScope === "all") {
      setCorpRoles(null);
      setCorpRolesLoading(false);
      return;
    }
    // Also check corp roles for selected character
    setCorpRolesLoading(true);
    getCharacterRoles(undefined, selectedScope)
      .then(setCorpRoles)
      .catch(() => setCorpRoles(null))
      .finally(() => setCorpRolesLoading(false));
  }, [open, loadHostedAccess, selectedScope]);

  useEffect(() => {
    if (!open || tab !== "access" || !hostedAccess || hostedBillingEnabled) return;
    setTab("overview");
  }, [open, tab, hostedAccess, hostedBillingEnabled]);

  // Billing telemetry used to hang off the tab-strip click handler; the tab can
  // also be reached from the plan pill, so it hangs off the tab itself now.
  useEffect(() => {
    if (!open || tab !== "access" || !hostedBillingEnabled) return;
    trackClientTelemetry({
      event_type: "billing_panel_opened",
      module: "hosted",
      character_id: typeof selectedScope === "number" ? selectedScope : undefined,
      properties: {
        plan: hostedAccess?.plan.id,
        subscription_status: hostedAccess?.status,
        scope: selectedScope,
      },
    });
    // Fires once per visit to the tab, not on every hostedAccess poll.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, tab, hostedBillingEnabled, selectedScope]);

  useEffect(() => {
    if (!open || tab !== "access" || !hostedBillingEnabled || !hostedAccess?.payment) return;
    const timer = window.setInterval(() => {
      loadHostedAccess();
    }, 60_000);
    return () => window.clearInterval(timer);
  }, [open, tab, hostedBillingEnabled, hostedAccess?.payment, loadHostedAccess]);

  // Scope switching is the provider's job now, so the rail's scope pill and
  // this dialog cannot disagree about who is selected.
  const handleSelectScope = useCallback(
    (scope: CharacterScope) => selectScope(scope),
    [selectScope],
  );

  const handleDeleteScope = useCallback(async (characterId: number) => {
    setDeletingCharacterId(characterId);
    setError(null);
    try {
      await onDeleteCharacter(characterId);
      await onAuthRefresh();
      if (selectedScope === characterId) {
        await selectScope("all");
      }
    } catch (e: any) {
      setError(e?.message || "Failed to remove character");
    } finally {
      setDeletingCharacterId(null);
    }
  }, [onDeleteCharacter, onAuthRefresh, selectedScope, selectScope]);

  const handleAdd = useCallback(async () => {
    setScopeBusy(true);
    try {
      await onAddCharacter();
    } finally {
      setScopeBusy(false);
    }
  }, [onAddCharacter]);

  const toggleScopeCollapsed = useCallback(() => {
    setScopeCollapsed((prev) => {
      const next = !prev;
      try {
        localStorage.setItem(SCOPE_COLLAPSE_KEY, next ? "1" : "0");
      } catch {
        // ignore
      }
      return next;
    });
  }, []);

  const shownError = error ?? scopeError ?? dataError;
  const busy = scopeBusy || contextScopeBusy;

  const buyOrders = data?.orders.filter((o) => o.is_buy_order) ?? [];
  const sellOrders = data?.orders.filter((o) => !o.is_buy_order) ?? [];
  const totalBuyValue = buyOrders.reduce((sum, o) => sum + o.price * o.volume_remain, 0);
  const totalSellValue = sellOrders.reduce((sum, o) => sum + o.price * o.volume_remain, 0);

  // Calculate profit from recent transactions
  const recentTxns = data?.transactions ?? [];
  const buyTxns = recentTxns.filter((t) => t.is_buy);
  const sellTxns = recentTxns.filter((t) => !t.is_buy);
  const totalBought = buyTxns.reduce((sum, t) => sum + t.unit_price * t.quantity, 0);
  const totalSold = sellTxns.reduce((sum, t) => sum + t.unit_price * t.quantity, 0);

  return (
    <Modal open={open} onClose={onClose} title={modalTitle} width="max-w-6xl" allowFullscreen>
      <div className="flex h-[calc(100dvh-5.25rem)] min-h-0 flex-col sm:h-[calc(100dvh-6rem)]">
        {/* Character selector */}
        <div className="shrink-0 border-b border-eve-border bg-gradient-to-r from-eve-panel/90 to-eve-dark/70 px-4 py-3 space-y-2.5">
          <div className="flex items-center justify-between gap-2">
            <div className="flex items-center gap-2 min-w-0">
              <button
                type="button"
                onClick={toggleScopeCollapsed}
                className="inline-flex items-center gap-1.5 text-[10px] text-eve-dim uppercase tracking-wider hover:text-eve-accent transition-colors"
              >
                <span className="text-[11px]">{scopeCollapsed ? "▸" : "▾"}</span>
                <span>{t("charSelectCharacter")}</span>
              </button>
              {scopeCollapsed && (
                <span className="text-[10px] text-eve-dim/80 truncate">
                  {selectedScope === "all"
                    ? t("charAllCharacters")
                    : selectedCharacter?.character_name ?? t("charOverview")}
                </span>
              )}
            </div>
            {hostedBillingEnabled && (
              <button
                type="button"
                onClick={() => setTab("access")}
                className={`hidden sm:inline-flex items-center gap-1.5 px-2.5 py-1 text-[10px] uppercase tracking-[0.14em] border rounded-sm transition-colors ${
                  tab === "access"
                    ? "border-eve-accent/70 bg-eve-accent/12 text-eve-accent"
                    : "border-eve-border bg-eve-dark/80 text-eve-dim hover:text-eve-accent hover:border-eve-accent/50"
                }`}
              >
                <span>{hostedAccess?.plan.name ?? "Access"}</span>
                <span className="text-[9px] opacity-75">{hostedAccess?.status ?? "sync"}</span>
              </button>
            )}
            <button
              onClick={() => { void handleAdd(); }}
              disabled={busy}
              className="px-2.5 py-1 text-[10px] rounded-sm border border-eve-border bg-eve-dark/80 text-eve-dim hover:text-eve-accent hover:border-eve-accent/50 transition-colors disabled:opacity-50"
            >
              {t("charAddCharacter")}
            </button>
          </div>
          {!scopeCollapsed && (
            <div className="flex flex-wrap gap-2 p-2 rounded-sm border border-eve-border/60 bg-eve-dark/35">
            <button
              onClick={() => { void handleSelectScope("all"); }}
              className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-sm border text-[11px] transition-colors ${
                selectedScope === "all"
                  ? "border-eve-accent/80 bg-eve-accent/15 text-eve-accent shadow-[0_0_0_1px_rgba(230,149,0,0.15)]"
                  : "border-eve-border bg-eve-dark/70 text-eve-dim hover:text-eve-text hover:border-eve-accent/50"
              }`}
            >
              <span className="text-[10px] opacity-80">◉</span>
              {t("charAllCharacters")}
            </button>
            {characters.map((character) => (
              <div
                key={character.character_id}
                className={`inline-flex items-center rounded-sm border overflow-hidden transition-colors ${
                  selectedScope === character.character_id
                    ? "border-eve-accent/70 bg-eve-accent/12 shadow-[0_0_0_1px_rgba(230,149,0,0.12)]"
                    : "border-eve-border bg-eve-dark/70"
                }`}
              >
                <button
                  onClick={() => { void handleSelectScope(character.character_id); }}
                  className={`inline-flex items-center gap-1.5 px-2.5 py-1 text-[11px] transition-colors max-w-[260px] ${
                    selectedScope === character.character_id
                      ? "text-eve-accent"
                      : "text-eve-dim hover:text-eve-text"
                  }`}
                >
                  <img
                    src={`https://images.evetech.net/characters/${character.character_id}/portrait?size=32`}
                    alt=""
                    className="w-5 h-5 rounded-sm border border-eve-border/50"
                  />
                  <span className="truncate">{character.character_name}</span>
                  {character.active && (
                    <span className="inline-flex items-center gap-1 text-[9px] text-eve-dim/85">
                      <span className="w-1.5 h-1.5 rounded-full bg-eve-success" />
                      {t("charActive")}
                    </span>
                  )}
                </button>
                <button
                  onClick={(event) => {
                    event.stopPropagation();
                    void handleDeleteScope(character.character_id);
                  }}
                  disabled={deletingCharacterId === character.character_id}
                  className="px-1.5 py-1 border-l border-eve-border/50 text-eve-dim hover:text-eve-error hover:bg-eve-error/5 transition-colors disabled:opacity-50"
                  title={t("charRemoveCharacter")}
                  aria-label={t("charRemoveCharacter")}
                >
                  <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                  </svg>
                </button>
              </div>
            ))}
            </div>
          )}
        </div>

        {/* Tabs + Refresh */}
        <div className="flex shrink-0 items-center border-b border-eve-border bg-eve-panel">
          <div className="flex flex-1 overflow-x-auto scrollbar-thin">
            <TabBtn active={tab === "overview"} onClick={() => setTab("overview")} label={t("charOverview")} />
            <TabBtn
              active={tab === "achievements"}
              onClick={() => setTab("achievements")}
              label={
                achievementPendingCount > 0
                  ? `${t("achievementsTitle")} (${achievementPendingCount} ${t("achievementNewLabel").toLowerCase()})`
                  : `${t("achievementsTitle")} (${achievementUnlockedCount})`
              }
            />
            {hostedBillingEnabled && <TabBtn active={tab === "access"} onClick={() => setTab("access")} label="Access" />}
          </div>
          {/* Refresh button */}
          <button
            onClick={refresh}
            disabled={loading || busy}
            className="px-2 py-1.5 mr-2 text-eve-dim hover:text-eve-accent transition-colors disabled:opacity-50"
            title={t("charRefresh")}
          >
            <svg className={`w-4 h-4 ${loading ? "animate-spin" : ""}`} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
            </svg>
          </button>
        </div>

        {/* Content */}
        <div className="flex-1 overflow-auto p-4">
          {loading && !data && tab === "overview" && (
            <div className="flex items-center justify-center h-full text-eve-dim">{t("loading")}...</div>
          )}
          {shownError && !data && tab === "overview" && (
            <div className="flex items-center justify-center h-full text-eve-error">{shownError}</div>
          )}
          {tab === "achievements" && <AchievementLibraryPanel />}
          {hostedBillingEnabled && tab === "access" && (
            <HostedAccessTab
              access={hostedAccess}
              loading={hostedAccessLoading}
              error={hostedAccessError}
              lastCheckedAt={hostedAccessCheckedAt}
              onReload={loadHostedAccess}
              onRequestPayment={handleHostedPaymentRequest}
              onMarkPaymentSent={handleHostedPaymentMarkSent}
              onCancelPayment={handleHostedPaymentCancel}
              formatIsk={formatIsk}
            />
          )}
          {tab === "overview" && data && (
            <OverviewTab
              data={data}
              characterId={selectedScope === "all" ? undefined : selectedScope}
              isAllScope={selectedScope === "all"}
              securityVault={securityVault}
              formatIsk={formatIsk}
              formatNumber={formatNumber}
              buyOrders={buyOrders}
              sellOrders={sellOrders}
              totalBuyValue={totalBuyValue}
              totalSellValue={totalSellValue}
              totalBought={totalBought}
              totalSold={totalSold}
              corpRoles={corpRoles}
              corpRolesLoading={corpRolesLoading}
              t={t}
            />
          )}
        </div>
      </div>
    </Modal>
  );
}
