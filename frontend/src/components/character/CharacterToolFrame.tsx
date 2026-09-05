/* Shared chrome for the character tools now mounted as first-class tabs.
 *
 * Inside the modal these tools rendered behind one guard —
 * `{tab !== "achievements" && data && (…)}` — plus the modal's own loading and
 * error lines. On the rail each tab needs that same guard, so it lives here
 * once instead of eleven times.
 *
 * `needsData` distinguishes the tools that read the CharacterInfo payload
 * (overview, orders, transactions, jobs, risk) from the ones that only need
 * the scope and fetch their own data (wallet, PI, P&L, optimizer, edge). */
import type { ReactNode } from "react";
import { useI18n } from "../../lib/i18n";
import { useCharacterScope } from "./CharacterScopeProvider";

export interface CharacterToolFrameProps {
  children: ReactNode;
  /** Gate rendering on a loaded CharacterInfo payload. */
  needsData?: boolean;
}

export function CharacterToolFrame({ children, needsData = false }: CharacterToolFrameProps) {
  const { t } = useI18n();
  const { isLoggedIn, data, loading, error } = useCharacterScope();

  if (!isLoggedIn) {
    return (
      <div className="flex h-full flex-col items-center justify-center font-ui text-t-body text-fg-tertiary">
        {t("charToolNoAuth")}
      </div>
    );
  }
  if (needsData && !data) {
    if (loading) {
      return <div className="flex h-40 items-center justify-center text-fg-tertiary">{t("loading")}...</div>;
    }
    if (error) {
      return <div className="flex h-40 items-center justify-center text-eve-error">{error}</div>;
    }
    return <div className="flex h-40 items-center justify-center text-fg-tertiary">{t("loading")}...</div>;
  }

  return <div className="min-h-0 flex-1 overflow-auto p-4">{children}</div>;
}
