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
  /** When true, the station picker in both Analyze and Scanner queries
   *  ESI for accessible player structures in the selected system and
   *  includes them in the dropdown. Off by default. Persisted so the
   *  toggle survives page reloads. */
  includeStructures: boolean;
  /** Hull-inherent job-cost reduction % for the selected build structure.
   *  Auto-filled from structure type (Raitaru 3, Azbel 4, Sotiyo 5). */
  structureJobCostReduction: number;
  /** Structure hull typeID (0 = NPC or nothing selected). Feeds rig-fit
   *  validation and the auto-fills for structureBonus + structureJobCostReduction. */
  structureTypeID: number;
  /** Up to 3 Standup rig typeIDs fitted to the selected structure. Cleared
   *  whenever `structureTypeID` changes (rigs bound to the old hull are
   *  meaningless on the new one). */
  structureRigTypeIDs: number[];
  /** Pricing (sell) system for the Analyze form — lets users build in one
   *  system and read market prices from another (e.g. build in Botane, sell
   *  in Jita). Empty string = use the build system's region (legacy behaviour). */
  analyzePricingSystem: string;
  /** Specific NPC station within the pricing system (0 = region-wide). */
  analyzePricingStationID: number;
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
  includeStructures: false,
  structureJobCostReduction: 0,
  structureTypeID: 0,
  structureRigTypeIDs: [],
  analyzePricingSystem: "",
  analyzePricingStationID: 0,
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
    // Subscribe on mount so this instance re-renders when another consumer
    // in the same window updates the shared blob (Scanner ↔ Analyze sync).
    subscribers.add(setState);
    return () => {
      subscribers.delete(setState);
    };
  }, []);

  // Cross-window sync via storage events was intentionally removed:
  //   When two instances of the app are open (e.g. Wails desktop + browser,
  //   or two browser tabs at the same origin), a sibling window's stale
  //   write would arrive via the storage event and overwrite a fresh
  //   local change. That surfaced as "select a structure → immediately
  //   reverts to All Stations" because the idle sibling's saveToStorage
  //   with buildStationID:0 raced our fresh pick. Each window/instance
  //   now maintains its own state; localStorage is used only for load-
  //   at-startup persistence, not runtime cross-tab sync.

  const update = useCallback((patch: Partial<IndustrySharedPrefs>) => {
    current = { ...current, ...patch };
    publish();
  }, []);

  return [state, update];
}
