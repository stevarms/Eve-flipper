import { createContext, useContext, type ReactNode } from "react";

/**
 * Whether ESI in-game actions (open market / set destination / open contract)
 * can be expected to work — i.e. whether anyone is logged in.
 *
 * Deliberately a bare boolean in its own context rather than a field on
 * `CharacterScopeValue`, for two reasons:
 *
 *  1. `useCharacterScope()` **throws** outside `CharacterScopeProvider`, and
 *     `CorpDashboardApp` (the `/corp` route in main.tsx) is a separate React
 *     root that never mounts it. A row action that hard-depends on it would
 *     crash the corp dashboard.
 *  2. That context's value is memoised on eleven dependencies including
 *     `data`, `loading` and `scope`, so every row-level consumer would
 *     re-render whenever the shared CharacterInfo fetch resolves or the scope
 *     picker moves. A boolean that changes only on login/logout does not.
 *
 * It also DEFAULTS TO TRUE instead of throwing when unprovided. `/corp` mounts
 * no auth tree and unit tests render rows bare; in both cases "render the
 * button and let the click explain itself" is the right fallback. A missing
 * provider must not be a crash.
 */
const EveUiContext = createContext<boolean | null>(null);

export function EveUiProvider({
  loggedIn,
  children,
}: {
  loggedIn: boolean;
  children: ReactNode;
}) {
  // A boolean is its own stable identity — no useMemo needed, and unlike an
  // object literal it cannot invalidate consumers on an unrelated re-render.
  return <EveUiContext.Provider value={loggedIn}>{children}</EveUiContext.Provider>;
}

export function useEveUiLoggedIn(): boolean {
  return useContext(EveUiContext) ?? true;
}
