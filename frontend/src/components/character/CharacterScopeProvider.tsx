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
import {
  characterScopeFromOwner,
  getCharacterInfo,
  ownerScopeKey,
  type CharacterScope,
  type OwnerScope,
} from "../../lib/api";
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
  /** The full owner selection. `scope` is this collapsed to characters. */
  ownerScope: OwnerScope;
  selectOwnerScope: (owner: OwnerScope) => Promise<void>;
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
  /** Whether the active tab honours a corporation selection at all. */
  corpCapable: boolean;
  /** True when a corp selection is stored but the active tab ignores it. */
  ownerScopeIgnored: boolean;
  formatIsk: (value: number) => string;
  formatNumber: (value: number) => string;
  formatDate: (dateStr: string) => string;
}

const CharacterScopeContext = createContext<CharacterScopeValue | null>(null);

/**
 * Reads the persisted owner selection, still under the original key. Values
 * written by earlier builds were a bare `"all"` or a character id, so those
 * two shapes have to keep parsing — a user upgrading must not lose (or worse,
 * silently change) which character they were looking at.
 */
export function readStoredScope(raw: string | null): OwnerScope | null {
  if (!raw) return null;
  if (raw === "all") return { kind: "all-characters" };
  if (raw === "characters") return { kind: "all-characters" };
  if (raw === "corps") return { kind: "all-corporations" };
  if (raw === "everything") return { kind: "everything" };
  if (raw.startsWith("char:")) {
    const id = Number(raw.slice(5));
    return Number.isFinite(id) && id > 0 ? { kind: "character", characterId: id } : null;
  }
  if (raw.startsWith("corp:")) {
    const id = Number(raw.slice(5));
    return Number.isFinite(id) && id > 0 ? { kind: "corporation", corporationId: id } : null;
  }
  const id = Number(raw);
  return Number.isFinite(id) && id > 0 ? { kind: "character", characterId: id } : null;
}

function loadStoredScope(): OwnerScope | null {
  try {
    return readStoredScope(localStorage.getItem(SCOPE_STORAGE_KEY));
  } catch {
    return null;
  }
}

function writeStoredScope(owner: OwnerScope) {
  try {
    localStorage.setItem(SCOPE_STORAGE_KEY, ownerScopeKey(owner));
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
  /** Set by the active tab: does a corporation selection mean anything here? */
  corpCapable?: boolean;
}

export function CharacterScopeProvider({
  children,
  isLoggedIn,
  characters,
  activeCharacterId,
  onSelectCharacter,
  corpCapable = false,
}: ProviderProps) {
  const [storedOwner, setStoredOwner] = useState<OwnerScope>(
    () =>
      loadStoredScope() ??
      (activeCharacterId ? { kind: "character", characterId: activeCharacterId } : { kind: "all-characters" }),
  );

  // What the current tab actually gets. A corp selection carried over from
  // Transactions must not silently reshape PI or Risk, so it degrades here
  // rather than at each consumer.
  const ownerScope = useMemo<OwnerScope>(
    () =>
      corpCapable || storedOwner.kind === "character" || storedOwner.kind === "all-characters"
        ? storedOwner
        : { kind: "all-characters" },
    [corpCapable, storedOwner],
  );
  const ownerScopeIgnored = ownerScope !== storedOwner;
  const scope = characterScopeFromOwner(ownerScope);
  const setScope = useCallback((next: CharacterScope) => {
    setStoredOwner(next === "all" ? { kind: "all-characters" } : { kind: "character", characterId: next });
  }, []);
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
  }, [scope, characters, activeCharacterId, isLoggedIn, setScope]);

  useEffect(() => {
    writeStoredScope(storedOwner);
  }, [storedOwner]);

  useEffect(() => {
    if (!wanted || !isLoggedIn) return;
    let cancelled = false;
    setLoading(true);
    setError(null);
    getCharacterInfo(scope, ownerScope)
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
  }, [wanted, isLoggedIn, scope, ownerScope, reloadToken]);

  const requestData = useCallback(() => {
    setWanted(true);
  }, []);

  const refresh = useCallback(() => {
    setReloadToken((n) => n + 1);
  }, []);

  const selectOwnerScope = useCallback(
    async (next: OwnerScope) => {
      if (ownerScopeKey(next) === ownerScopeKey(storedOwner)) return;
      // Only a single-character selection changes the active ESI character;
      // every other owner form is a pure read filter.
      if (next.kind !== "character") {
        setStoredOwner(next);
        return;
      }
      setScopeBusy(true);
      setScopeError(null);
      try {
        await onSelectCharacter(next.characterId);
        setStoredOwner(next);
      } catch (e: any) {
        setScopeError(e?.message || "Failed to switch character");
      } finally {
        setScopeBusy(false);
      }
    },
    [storedOwner, onSelectCharacter],
  );

  const selectScope = useCallback(
    (next: CharacterScope) =>
      selectOwnerScope(next === "all" ? { kind: "all-characters" } : { kind: "character", characterId: next }),
    [selectOwnerScope],
  );

  const value = useMemo<CharacterScopeValue>(
    () => ({
      scope,
      selectScope,
      ownerScope,
      selectOwnerScope,
      scopeBusy,
      scopeError,
      characters,
      isLoggedIn,
      data,
      loading,
      error,
      requestData,
      refresh,
      corpCapable,
      ownerScopeIgnored,
      formatIsk,
      formatNumber,
      formatDate,
    }),
    [
      scope,
      selectScope,
      ownerScope,
      selectOwnerScope,
      scopeBusy,
      scopeError,
      characters,
      isLoggedIn,
      data,
      loading,
      error,
      requestData,
      refresh,
      corpCapable,
      ownerScopeIgnored,
    ],
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
