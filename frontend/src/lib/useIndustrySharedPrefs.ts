// Shared industry preferences accessible to both the Analyze form and the
// Scanner panel. Changes made in one surface propagate to the other so the
// user configures broker fee, build system, etc. once and it sticks.
//
// Storage strategy: single JSON blob in localStorage, module-level in-memory
// copy, and a tiny pubsub so multiple mounted consumers stay in sync within
// one tab. (Only one industry sub-tab is normally mounted at a time — the
// pubsub is defensive.)

import { useEffect, useState, useCallback } from "react";
import { DECRYPTORS, type DecryptorKey } from "./industryDecryptors";

export type IndustryBuildMode = "auto" | "buy_all" | "build_all";

export interface IndustrySharedPrefs {
  buildSystem: string;
  buildStationID: number;
  facilityTax: number;
  structureBonus: number;
  brokerFee: number;
  salesTaxPercent: number;
  buildMode: IndustryBuildMode;
  // Decryptor is the single source of truth for invention-related params
  // across Analyze and Scanner. Effective chance/output_runs/ME/TE are
  // derived from this key via effectiveInventionParams(); the market cost
  // per attempt is decryptorCost (defaults to picker's canonical price,
  // user can override).
  decryptor: DecryptorKey;
  decryptorCost: number;
  /** When true, reaction-only child materials are treated as buy-from-market
   *  instead of expanded into a reaction step. Matches the workflow of a
   *  builder who never runs reactions themselves. */
  skipReactions: boolean;
}

const STORAGE_KEY = "eve-settings:industry-shared-prefs";

const DEFAULTS: IndustrySharedPrefs = {
  buildSystem: "Jita",
  buildStationID: 0,
  facilityTax: 0,
  structureBonus: 0,
  brokerFee: 3,
  salesTaxPercent: 4.5,
  buildMode: "auto",
  decryptor: "none",
  decryptorCost: DECRYPTORS.none.defaultCost,
  skipReactions: false,
};

function loadFromStorage(): IndustrySharedPrefs {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return { ...DEFAULTS };
    const parsed = JSON.parse(raw) as Partial<IndustrySharedPrefs>;
    return { ...DEFAULTS, ...parsed };
  } catch {
    return { ...DEFAULTS };
  }
}

function saveToStorage(prefs: IndustrySharedPrefs) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(prefs));
  } catch {
    /* ignore quota / unavailable */
  }
}

// Module-level current state + subscribers. All hook instances read/write
// through this so cross-component updates stay in sync without a Context.
let current: IndustrySharedPrefs = loadFromStorage();
const subscribers = new Set<(v: IndustrySharedPrefs) => void>();

function publish() {
  saveToStorage(current);
  for (const fn of subscribers) fn(current);
}

/**
 * useIndustrySharedPrefs returns the current shared prefs and an updater.
 * The updater accepts a partial patch (merge semantics) so components only
 * need to touch the fields they care about.
 */
export function useIndustrySharedPrefs(): [
  IndustrySharedPrefs,
  (patch: Partial<IndustrySharedPrefs>) => void,
] {
  const [state, setState] = useState<IndustrySharedPrefs>(current);

  useEffect(() => {
    // Subscribe on mount so this instance re-renders when another instance
    // (or another tab, via storage event) updates the shared blob.
    subscribers.add(setState);
    return () => {
      subscribers.delete(setState);
    };
  }, []);

  useEffect(() => {
    // Cross-window sync via the storage event so a change in one browser
    // tab shows up in another.
    const handler = (e: StorageEvent) => {
      if (e.key !== STORAGE_KEY || e.newValue == null) return;
      try {
        const parsed = JSON.parse(e.newValue) as Partial<IndustrySharedPrefs>;
        current = { ...DEFAULTS, ...parsed };
        for (const fn of subscribers) fn(current);
      } catch {
        /* ignore */
      }
    };
    window.addEventListener("storage", handler);
    return () => window.removeEventListener("storage", handler);
  }, []);

  const update = useCallback((patch: Partial<IndustrySharedPrefs>) => {
    current = { ...current, ...patch };
    publish();
  }, []);

  return [state, update];
}
