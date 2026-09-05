/* Shared character scope + CharacterInfo fetch.
 *
 * The eleven character tools used to live inside CharacterPopup, which owned
 * the scope selector, one getCharacterInfo() fetch and three formatters, and
 * handed all of them down as props. Now that those tools are mounted as
 * first-class tabs on the workspace rail, that state has to live above both
 * the modal and the tabs — otherwise switching character in Journal would not
 * be reflected in Industry, and every tab would fire its own ESI-backed fetch.
 *
 * The CharacterInfo fetch is lazy: only overview / orders / transactions /
 * jobs / risk actually read `data`. Tabs that only need the scope (wallet, PI,
 * P&L, optimizer, edge) use useCharacterScope() and never trigger it.
 */
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { getCharacterInfo, type CharacterScope } from "../../lib/api";
import { formatIsk as formatIskLib } from "../../lib/format";
import type { AuthCharacter, CharacterInfo } from "../../lib/types";

const SCOPE_STORAGE_KEY = "eve-character-scope";

/** Local presentation of the shared formatter (lib/format.ts).
 *
 *  maxTier is T rather than B so a 2T figure renders "2T" here and in the
 *  Trade Journal alike. Negative handling lives in formatIsk itself. */
export function formatIsk(value: number): string {
  return formatIskLib(value, undefined, {
    maxTier: "T",
    space: false,
    decimals: { t: 2, b: 2, m: 2, k: 1, unit: 0 },
  });
}

export function formatNumber(value: number): string {
  return value.toLocaleString();
}

export function formatDate(dateStr: string): string {
  const d = new Date(dateStr);
  return d.toLocaleDateString() + " " + d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

export interface CharacterScopeValue {
  scope: CharacterScope;
  /** Switching to a character also makes it the active ESI character. */
  selectScope: (scope: CharacterScope) => Promise<void>;
  scopeBusy: boolean;
  scopeError: string | null;
  characters: AuthCharacter[];
  isLoggedIn: boolean;
  /** Null until a consumer calls requestData() (or useCharacterInfo). */
  data: CharacterInfo | null;
  loading: boolean;
  error: string | null;
  /** Idempotent — marks this subtree as needing CharacterInfo. */
  requestData: () => void;
  refresh: () => void;
  formatIsk: (value: number) => string;
  formatNumber: (value: number) => string;
  formatDate: (dateStr: string) => string;
}

const CharacterScopeContext = createContext<CharacterScopeValue | null>(null);

function readStoredScope(): CharacterScope | null {
  try {
    const raw = localStorage.getItem(SCOPE_STORAGE_KEY);
    if (!raw) return null;
    if (raw === "all") return "all";
    const id = Number(raw);
    return Number.isFinite(id) && id > 0 ? id : null;
  } catch {
    return null;
  }
}

function writeStoredScope(scope: CharacterScope) {
  try {
    localStorage.setItem(SCOPE_STORAGE_KEY, String(scope));
  } catch {
    // ignore
  }
}

interface ProviderProps {
  children: ReactNode;
  isLoggedIn: boolean;
  characters: AuthCharacter[];
  activeCharacterId?: number;
  onSelectCharacter: (characterId: number) => Promise<void>;
}

export function CharacterScopeProvider({
  children,
  isLoggedIn,
  characters,
  activeCharacterId,
  onSelectCharacter,
}: ProviderProps) {
  const [scope, setScope] = useState<CharacterScope>(() => readStoredScope() ?? activeCharacterId ?? "all");
  const [scopeBusy, setScopeBusy] = useState(false);
  const [scopeError, setScopeError] = useState<string | null>(null);
  const [data, setData] = useState<CharacterInfo | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [wanted, setWanted] = useState(false);
  const [reloadToken, setReloadToken] = useState(0);

  // Drop a scope that no longer exists (character removed, or logged out).
  useEffect(() => {
    if (scope === "all") return;
    if (!isLoggedIn) {
      setScope("all");
      return;
    }
    if (characters.length === 0) return;
    if (characters.some((c) => c.character_id === scope)) return;
    setScope(activeCharacterId ?? "all");
  }, [scope, characters, activeCharacterId, isLoggedIn]);

  useEffect(() => {
    writeStoredScope(scope);
  }, [scope]);

  useEffect(() => {
    if (!wanted || !isLoggedIn) return;
    let cancelled = false;
    setLoading(true);
    setError(null);
    getCharacterInfo(scope)
      .then((info) => {
        if (cancelled) return;
        setData(info);
      })
      .catch((e: any) => {
        if (cancelled) return;
        setError(e?.message || "Failed to load character");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [wanted, isLoggedIn, scope, reloadToken]);

  const requestData = useCallback(() => {
    setWanted(true);
  }, []);

  const refresh = useCallback(() => {
    setReloadToken((n) => n + 1);
  }, []);

  const selectScope = useCallback(
    async (next: CharacterScope) => {
      if (next === scope) return;
      if (next === "all") {
        setScope("all");
        return;
      }
      setScopeBusy(true);
      setScopeError(null);
      try {
        await onSelectCharacter(next);
        setScope(next);
      } catch (e: any) {
        setScopeError(e?.message || "Failed to switch character");
      } finally {
        setScopeBusy(false);
      }
    },
    [scope, onSelectCharacter],
  );

  const value = useMemo<CharacterScopeValue>(
    () => ({
      scope,
      selectScope,
      scopeBusy,
      scopeError,
      characters,
      isLoggedIn,
      data,
      loading,
      error,
      requestData,
      refresh,
      formatIsk,
      formatNumber,
      formatDate,
    }),
    [scope, selectScope, scopeBusy, scopeError, characters, isLoggedIn, data, loading, error, requestData, refresh],
  );

  return <CharacterScopeContext.Provider value={value}>{children}</CharacterScopeContext.Provider>;
}

export function useCharacterScope(): CharacterScopeValue {
  const ctx = useContext(CharacterScopeContext);
  if (!ctx) throw new Error("useCharacterScope must be used inside CharacterScopeProvider");
  return ctx;
}

/** Same as useCharacterScope, but also declares that this subtree needs the
 *  CharacterInfo payload — which triggers the shared fetch on first mount. */
export function useCharacterInfo(): CharacterScopeValue {
  const ctx = useCharacterScope();
  const { requestData } = ctx;
  useEffect(() => {
    requestData();
  }, [requestData]);
  return ctx;
}
