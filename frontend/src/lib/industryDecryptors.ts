// T2 invention decryptor definitions.
//
// A decryptor is optional. If used, it modifies the invention job's
// probability, output-BPC runs, and the resulting BPC's ME/TE. The frontend
// pre-computes the effective values from the user's chosen decryptor and
// sends them to the scanner backend, so the engine's invention math stays
// untouched.
//
// The default prices below are rough Jita ballpark values; the UI keeps the
// cost field editable so the user can pin the current market price. Costs
// are in ISK per decryptor (one is consumed per invention attempt).

export type DecryptorKey =
  | "none"
  | "accelerant"
  | "attainment"
  | "augmentation"
  | "optimizedAttainment"
  | "optimizedAugmentation"
  | "parity"
  | "process"
  | "symmetry";

export interface DecryptorInfo {
  key: DecryptorKey;
  name: string;
  typeID: number; // EVE type ID (0 for "none")
  probMult: number; // multiplier on the invention probability
  outputRunsBonus: number; // added to the base T2 BPC runs (default 10)
  meDelta: number; // added to the T2 BPC's ME (base 2)
  teDelta: number; // added to the T2 BPC's TE (base 4)
  defaultCost: number; // ballpark Jita cost per decryptor, ISK
}

export const T2_BPC_BASE_ME = 2;
export const T2_BPC_BASE_TE = 4;
export const T2_BPC_BASE_RUNS = 10;

export const DECRYPTORS: Record<DecryptorKey, DecryptorInfo> = {
  none: {
    key: "none",
    name: "None",
    typeID: 0,
    probMult: 1.0,
    outputRunsBonus: 0,
    meDelta: 0,
    teDelta: 0,
    defaultCost: 0,
  },
  accelerant: {
    key: "accelerant",
    name: "Accelerant",
    typeID: 34201,
    probMult: 1.2,
    outputRunsBonus: 1,
    meDelta: 2,
    teDelta: 10,
    defaultCost: 400_000,
  },
  attainment: {
    key: "attainment",
    name: "Attainment",
    typeID: 34202,
    probMult: 1.8,
    outputRunsBonus: 4,
    meDelta: -1,
    teDelta: 4,
    defaultCost: 550_000,
  },
  augmentation: {
    key: "augmentation",
    name: "Augmentation",
    typeID: 34203,
    probMult: 0.6,
    outputRunsBonus: 9,
    meDelta: -2,
    teDelta: 2,
    defaultCost: 350_000,
  },
  optimizedAttainment: {
    key: "optimizedAttainment",
    name: "Optimized Attainment",
    typeID: 34204,
    probMult: 1.9,
    outputRunsBonus: 2,
    meDelta: 1,
    teDelta: -2,
    defaultCost: 800_000,
  },
  optimizedAugmentation: {
    key: "optimizedAugmentation",
    name: "Optimized Augmentation",
    typeID: 34205,
    probMult: 0.9,
    outputRunsBonus: 7,
    meDelta: 2,
    teDelta: 0,
    defaultCost: 700_000,
  },
  parity: {
    key: "parity",
    name: "Parity",
    typeID: 34206,
    probMult: 1.5,
    outputRunsBonus: 3,
    meDelta: 1,
    teDelta: -2,
    defaultCost: 500_000,
  },
  process: {
    key: "process",
    name: "Process",
    typeID: 34207,
    probMult: 1.1,
    outputRunsBonus: 0,
    meDelta: 3,
    teDelta: 6,
    defaultCost: 300_000,
  },
  symmetry: {
    key: "symmetry",
    name: "Symmetry",
    typeID: 34208,
    probMult: 1.0,
    outputRunsBonus: 2,
    meDelta: 1,
    teDelta: 8,
    defaultCost: 300_000,
  },
};

export const DECRYPTOR_ORDER: DecryptorKey[] = [
  "none",
  "accelerant",
  "attainment",
  "augmentation",
  "optimizedAttainment",
  "optimizedAugmentation",
  "parity",
  "process",
  "symmetry",
];

/** Clamp helper for ME/TE which have absolute engine bounds. */
function clamp(v: number, lo: number, hi: number): number {
  return v < lo ? lo : v > hi ? hi : v;
}

/** Effective invention parameters after applying a decryptor to the T2 base. */
export function effectiveInventionParams(key: DecryptorKey): {
  meBase: number;
  teBase: number;
  outputRuns: number;
  chanceMult: number;
  decryptorTypeID: number;
} {
  const d = DECRYPTORS[key] ?? DECRYPTORS.none;
  return {
    meBase: clamp(T2_BPC_BASE_ME + d.meDelta, 0, 10),
    teBase: clamp(T2_BPC_BASE_TE + d.teDelta, 0, 20),
    outputRuns: Math.max(1, T2_BPC_BASE_RUNS + d.outputRunsBonus),
    chanceMult: d.probMult,
    decryptorTypeID: d.typeID,
  };
}
