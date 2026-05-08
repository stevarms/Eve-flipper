import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import type { AchievementIconId } from "@/assets/achievements/achievement-icons";
import {
  getAchievementStates,
  markAchievementsSeen,
  patchAchievementProgress,
  type AchievementState,
} from "@/lib/api";
import { useI18n, type TranslationKey } from "@/lib/i18n";
import {
  achievementCategories,
  achievementDefinitionById,
  achievementDefinitions,
  type AchievementDefinition,
} from "@/lib/achievements/definitions";
import {
  buildAchievementPatches,
  type AchievementEventName,
  type AchievementEventPayload,
  type AchievementStateMap,
} from "@/lib/achievements/engine";
import { cn } from "@/lib/utils";
import { Modal } from "../Modal";
import { AchievementBadge } from "./AchievementBadge";
import "./AchievementsProvider.css";

interface AchievementToast {
  id: number;
  definition: AchievementDefinition;
}

interface AchievementsContextValue {
  states: AchievementStateMap;
  loading: boolean;
  unlockedCount: number;
  totalPoints: number;
  pendingCount: number;
  openAchievementLibrary: () => void;
  trackAchievementEvent: (event: AchievementEventName, payload?: AchievementEventPayload) => Promise<void>;
  markSeen: (ids: AchievementIconId[]) => Promise<void>;
  refreshAchievements: () => Promise<void>;
}

const AchievementsContext = createContext<AchievementsContextValue | null>(null);

let achievementToastID = 0;

function toStateMap(states: AchievementState[]): AchievementStateMap {
  const map: AchievementStateMap = {};
  for (const state of states) {
    const id = state.achievement_id as AchievementIconId;
    if (achievementDefinitionById[id]) {
      map[id] = state;
    }
  }
  return map;
}

function mergeStates(current: AchievementStateMap, updates: AchievementState[]): AchievementStateMap {
  if (updates.length === 0) return current;
  const next = { ...current };
  for (const state of updates) {
    const id = state.achievement_id as AchievementIconId;
    if (achievementDefinitionById[id]) {
      next[id] = state;
    }
  }
  return next;
}

function isUnlocked(state?: AchievementState): boolean {
  return !!state?.unlocked_at;
}

function achievementTextKey(kind: "Title" | "Description", id: AchievementIconId): TranslationKey {
  return `achievement${kind}_${id.replace(/-/g, "_")}` as TranslationKey;
}

function translateFallback(t: (key: TranslationKey) => string, key: TranslationKey, fallback: string): string {
  const translated = t(key);
  return translated === key ? fallback : translated;
}

export function AchievementsProvider({ children }: { children: ReactNode }) {
  const [states, setStates] = useState<AchievementStateMap>({});
  const [loading, setLoading] = useState(false);
  const [libraryOpen, setLibraryOpen] = useState(false);
  const [toasts, setToasts] = useState<AchievementToast[]>([]);
  const statesRef = useRef<AchievementStateMap>({});
  const timersRef = useRef<Map<number, ReturnType<typeof setTimeout>>>(new Map());
  const lastEventRef = useRef<{ key: string; ts: number }>({ key: "", ts: 0 });

  const setStateMap = useCallback((updater: AchievementStateMap | ((prev: AchievementStateMap) => AchievementStateMap)) => {
    setStates((prev) => {
      const next = typeof updater === "function" ? updater(prev) : updater;
      statesRef.current = next;
      return next;
    });
  }, []);

  const refreshAchievements = useCallback(async () => {
    setLoading(true);
    try {
      const data = await getAchievementStates();
      setStateMap(toStateMap(data.states));
    } catch {
      setStateMap({});
    } finally {
      setLoading(false);
    }
  }, [setStateMap]);

  useEffect(() => {
    void refreshAchievements();
  }, [refreshAchievements]);

  useEffect(() => {
    const timers = timersRef.current;
    return () => {
      for (const timer of timers.values()) {
        clearTimeout(timer);
      }
      timers.clear();
    };
  }, []);

  const pushUnlockToasts = useCallback((unlocked: AchievementState[]) => {
    for (const state of unlocked) {
      const definition = achievementDefinitionById[state.achievement_id as AchievementIconId];
      if (!definition) continue;
      const id = ++achievementToastID;
      setToasts((prev) => [...prev, { id, definition }]);
      const timer = setTimeout(() => {
        setToasts((prev) => prev.filter((toast) => toast.id !== id));
        timersRef.current.delete(id);
      }, 5200);
      timersRef.current.set(id, timer);
    }
  }, []);

  const trackAchievementEvent = useCallback(
    async (event: AchievementEventName, payload: AchievementEventPayload = {}) => {
      const eventKey = `${event}:${JSON.stringify(payload)}`;
      const now = Date.now();
      if (lastEventRef.current.key === eventKey && now - lastEventRef.current.ts < 1500) return;
      lastEventRef.current = { key: eventKey, ts: now };
      const patches = buildAchievementPatches(event, payload, statesRef.current);
      if (patches.length === 0) return;
      try {
        const result = await patchAchievementProgress(patches);
        setStateMap((prev) => mergeStates(prev, result.states));
        pushUnlockToasts(result.unlocked);
      } catch {
        // Achievement tracking is non-critical; never break the trading workflow.
      }
    },
    [pushUnlockToasts, setStateMap],
  );

  const markSeen = useCallback(
    async (ids: AchievementIconId[]) => {
      if (ids.length === 0) return;
      try {
        const result = await markAchievementsSeen(ids);
        setStateMap((prev) => mergeStates(prev, result.states));
      } catch {
        // ignore
      }
    },
    [setStateMap],
  );

  const unlockedCount = useMemo(
    () => achievementDefinitions.reduce((count, definition) => count + (isUnlocked(states[definition.id]) ? 1 : 0), 0),
    [states],
  );
  const totalPoints = useMemo(
    () =>
      achievementDefinitions.reduce(
        (points, definition) => points + (isUnlocked(states[definition.id]) ? definition.points : 0),
        0,
      ),
    [states],
  );
  const pendingCount = useMemo(
    () =>
      achievementDefinitions.reduce(
        (count, definition) => count + (isUnlocked(states[definition.id]) && !states[definition.id]?.seen ? 1 : 0),
        0,
      ),
    [states],
  );

  const value = useMemo<AchievementsContextValue>(
    () => ({
      states,
      loading,
      unlockedCount,
      totalPoints,
      pendingCount,
      openAchievementLibrary: () => setLibraryOpen(true),
      trackAchievementEvent,
      markSeen,
      refreshAchievements,
    }),
    [
      loading,
      markSeen,
      pendingCount,
      refreshAchievements,
      states,
      totalPoints,
      trackAchievementEvent,
      unlockedCount,
    ],
  );

  return (
    <AchievementsContext.Provider value={value}>
      {children}
      <AchievementLibraryModal open={libraryOpen} onClose={() => setLibraryOpen(false)} />
      <AchievementToastStack toasts={toasts} />
    </AchievementsContext.Provider>
  );
}

export function useAchievements() {
  const context = useContext(AchievementsContext);
  if (!context) {
    throw new Error("useAchievements must be used within AchievementsProvider");
  }
  return context;
}

function AchievementToastStack({ toasts }: { toasts: AchievementToast[] }) {
  const { t } = useI18n();
  if (toasts.length === 0) return null;
  return (
    <div className="achievement-toast-stack">
      {toasts.slice(-3).map((toast) => {
        const title = translateFallback(t, achievementTextKey("Title", toast.definition.id), toast.definition.title);
        return (
          <div key={toast.id} className="achievement-toast" role="status" aria-live="polite">
            <span className="achievement-toast__shine" aria-hidden="true" />
            <AchievementBadge
              iconId={toast.definition.iconId}
              rarity={toast.definition.rarity}
              state="unlocked"
              size="sm"
              label={title}
            />
            <div className="achievement-toast__copy">
              <div className="achievement-toast__eyebrow">{t("achievementUnlocked")}</div>
              <div className="achievement-toast__title">{title}</div>
              <div className="achievement-toast__meta">
                {t(`achievementRarity_${toast.definition.rarity}` as TranslationKey)} · {t("achievementPoints", { points: toast.definition.points })}
              </div>
            </div>
            <span className="achievement-toast__timer" aria-hidden="true" />
          </div>
        );
      })}
    </div>
  );
}

function AchievementLibraryModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t } = useI18n();
  return (
    <Modal open={open} onClose={onClose} title={t("achievementsTitle")} width="max-w-7xl">
      <AchievementLibraryPanel active={open} />
    </Modal>
  );
}

export function AchievementLibraryPanel({ active = true }: { active?: boolean }) {
  const { t } = useI18n();
  const { states, unlockedCount, totalPoints, pendingCount, markSeen } = useAchievements();

  useEffect(() => {
    if (!active) return;
    const unseen = achievementDefinitions
      .filter((definition) => isUnlocked(states[definition.id]) && !states[definition.id]?.seen)
      .map((definition) => definition.id);
    if (unseen.length > 0) {
      void markSeen(unseen);
    }
  }, [active, markSeen, states]);

  const visibleDefinitions = achievementDefinitions.filter((definition) => {
    const state = states[definition.id];
    return !definition.hidden || isUnlocked(state);
  });

  return (
    <div className="achievement-library">
      <div className="achievement-library__summary">
        <div>
          <div className="achievement-library__kicker">{t("achievementPilotRecord")}</div>
          <div className="achievement-library__score">
            {unlockedCount}/{achievementDefinitions.length}
          </div>
        </div>
        <div>
          <div className="achievement-library__kicker">{t("achievementPointsLabel")}</div>
          <div className="achievement-library__score">{totalPoints}</div>
        </div>
        <div>
          <div className="achievement-library__kicker">{t("achievementNewLabel")}</div>
          <div className="achievement-library__score">{pendingCount}</div>
        </div>
      </div>

      {achievementCategories.map((category) => {
        const items = visibleDefinitions.filter((definition) => definition.category === category.id);
        if (items.length === 0) return null;
        return (
          <section key={category.id} className="achievement-library__section">
            <div className="achievement-library__section-title">{t(`achievementCategory_${category.id}` as TranslationKey)}</div>
            <div className="achievement-library__grid">
              {items.map((definition) => (
                <AchievementLibraryCard key={definition.id} definition={definition} state={states[definition.id]} />
              ))}
            </div>
          </section>
        );
      })}

      <section className="achievement-library__section">
        <div className="achievement-library__section-title">{t("achievementHiddenRecords")}</div>
        <div className="achievement-library__grid">
          {achievementDefinitions
            .filter((definition) => definition.hidden && !isUnlocked(states[definition.id]))
            .map((definition) => (
              <AchievementLibraryCard key={definition.id} definition={definition} state={states[definition.id]} classified />
            ))}
        </div>
      </section>
    </div>
  );
}

function AchievementLibraryCard({
  definition,
  state,
  classified = false,
}: {
  definition: AchievementDefinition;
  state?: AchievementState;
  classified?: boolean;
}) {
  const { t } = useI18n();
  const unlocked = isUnlocked(state);
  const progress = Math.max(0, Math.min(definition.progressMax, state?.progress ?? 0));
  const progressPct = definition.progressMax > 0 ? Math.min(100, (progress / definition.progressMax) * 100) : 0;
  const concealed = !unlocked;
  const visualState = unlocked ? "unlocked" : "locked";
  const title = concealed
    ? t("achievementLockedTitle")
    : translateFallback(t, achievementTextKey("Title", definition.id), definition.title);
  const description = concealed
    ? t("achievementLockedDescription")
    : translateFallback(t, achievementTextKey("Description", definition.id), definition.description);
  const points = concealed ? t("achievementPointsHidden") : t("achievementPoints", { points: definition.points });

  return (
    <article className={cn("achievement-card", unlocked && "achievement-card--unlocked", concealed && "achievement-card--concealed")}>
      <AchievementBadge
        iconId={definition.iconId}
        rarity={concealed || classified ? "classified" : definition.rarity}
        state={visualState}
        size="md"
        progress={unlocked ? progressPct : 0}
        label={title}
      />
      <div className="achievement-card__body">
        <div className="achievement-card__topline">
          <span className="achievement-card__title">{title}</span>
          <span className="achievement-card__points">{points}</span>
        </div>
        <div className="achievement-card__description">{description}</div>
        <div className="achievement-card__progress" aria-label={unlocked ? `${progress} of ${definition.progressMax}` : t("achievementProgressHidden")}>
          <span style={{ width: `${unlocked ? progressPct : 0}%` }} />
        </div>
        <div className="achievement-card__meta">
          <span>{unlocked ? t("achievementStateUnlocked") : t("achievementStateLocked")}</span>
          <span>{unlocked ? `${progress}/${definition.progressMax}` : t("achievementProgressHidden")}</span>
        </div>
      </div>
    </article>
  );
}
