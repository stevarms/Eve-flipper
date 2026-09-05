/* Workspace-tab mounts for the tools that used to live in the character modal.
 *
 * Each wrapper is deliberately thin: the tool components under
 * components/character-popup/ keep their existing signatures and are untouched.
 * All that changes is where they mount and where their props come from — the
 * shared CharacterScopeProvider instead of CharacterPopup's local state. */
import { useEffect } from "react";
import { IndustryJobsTab } from "../character-popup/IndustryJobsTab";
import { OptimizerTab } from "../character-popup/OptimizerTab";
import { PIPlanetsTab } from "../character-popup/PIPlanetsTab";
import { PnLTab } from "../character-popup/PnLTab";
import { RiskTab } from "../character-popup/RiskTab";
import { TransactionsTab } from "../character-popup/TransactionsTab";
import { TradingEdgeTab } from "../character-popup/TradingEdgeTab";
import { WalletDashboardTab } from "../character-popup/WalletDashboardTab";
import { useI18n } from "../../lib/i18n";
import { CharacterToolFrame } from "./CharacterToolFrame";
import { useCharacterInfo, useCharacterScope } from "./CharacterScopeProvider";
import { useAchievements } from "../achievements";
import type { AchievementEventName } from "../../lib/achievements/engine";

/** The modal's tab strip used to fire these on click. The tabs are on the rail
 *  now, so each one reports itself on mount instead. */
function useToolAchievement(event: AchievementEventName) {
  const { trackAchievementEvent } = useAchievements();
  useEffect(() => {
    void trackAchievementEvent(event);
  }, [trackAchievementEvent, event]);
}

export function JobsWorkspaceTab() {
  const { t } = useI18n();
  const { data, formatIsk, formatDate } = useCharacterInfo();
  return (
    <CharacterToolFrame needsData>
      <IndustryJobsTab jobs={data?.industry_jobs ?? []} formatIsk={formatIsk} formatDate={formatDate} t={t} />
    </CharacterToolFrame>
  );
}

export function TransactionsWorkspaceTab() {
  const { t } = useI18n();
  const { data, formatIsk, formatDate } = useCharacterInfo();
  return (
    <CharacterToolFrame needsData>
      <TransactionsTab transactions={data?.transactions ?? []} formatIsk={formatIsk} formatDate={formatDate} t={t} />
    </CharacterToolFrame>
  );
}

export function RiskWorkspaceTab() {
  const { t } = useI18n();
  const { data, scope, formatIsk } = useCharacterInfo();
  useToolAchievement("risk_opened");
  return (
    <CharacterToolFrame needsData>
      {data && (
        <RiskTab
          characterId={scope === "all" ? undefined : scope}
          isAllScope={scope === "all"}
          data={data}
          formatIsk={formatIsk}
          t={t}
        />
      )}
    </CharacterToolFrame>
  );
}

export function PIPlanetsWorkspaceTab() {
  const { scope, formatIsk } = useCharacterScope();
  return (
    <CharacterToolFrame>
      <PIPlanetsTab characterScope={scope} formatIsk={formatIsk} />
    </CharacterToolFrame>
  );
}

export function PnLWorkspaceTab({ onOpenPositions }: { onOpenPositions?: () => void }) {
  const { t } = useI18n();
  const { scope, formatIsk } = useCharacterScope();
  useToolAchievement("portfolio_opened");
  return (
    <CharacterToolFrame>
      <PnLTab formatIsk={formatIsk} characterScope={scope} t={t} onOpenPositions={onOpenPositions} />
    </CharacterToolFrame>
  );
}

export function OptimizerWorkspaceTab() {
  const { t } = useI18n();
  const { scope, formatIsk } = useCharacterScope();
  useToolAchievement("portfolio_opened");
  return (
    <CharacterToolFrame>
      <OptimizerTab formatIsk={formatIsk} characterScope={scope} t={t} />
    </CharacterToolFrame>
  );
}

export function WalletWorkspaceTab({ onOpenPaperTradeJournal }: { onOpenPaperTradeJournal?: () => void }) {
  const { t } = useI18n();
  const { scope, formatIsk } = useCharacterScope();
  useToolAchievement("ledger_opened");
  return (
    <CharacterToolFrame>
      <WalletDashboardTab
        characterScope={scope}
        formatIsk={formatIsk}
        t={t}
        onOpenPaperTradeJournal={onOpenPaperTradeJournal}
      />
    </CharacterToolFrame>
  );
}

export function EdgeWorkspaceTab({
  enabled,
  onToggleEnabled,
}: {
  enabled: boolean;
  onToggleEnabled: (enabled: boolean) => void;
}) {
  const { formatIsk } = useCharacterScope();
  useToolAchievement("portfolio_opened");
  return (
    <CharacterToolFrame>
      <TradingEdgeTab enabled={enabled} onToggleEnabled={onToggleEnabled} formatIsk={formatIsk} />
    </CharacterToolFrame>
  );
}
