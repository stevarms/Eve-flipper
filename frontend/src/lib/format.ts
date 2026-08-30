import type { Locale } from "./i18n";

// Get browser locale or use provided locale
function getLocaleString(locale?: Locale): string {
  if (locale === "ru") return "ru-RU";
  if (locale === "en") return "en-US";
  // Fallback to browser locale
  return navigator.language || "en-US";
}

export function formatISK(value: number, locale?: Locale): string {
  if (value == null || isNaN(value)) return "0";
  const localeStr = getLocaleString(locale);
  const abs = Math.abs(value);
  const sign = value < 0 ? "-" : "";
  if (abs >= 1_000_000_000) {
    return sign + (abs / 1_000_000_000).toLocaleString(localeStr, { maximumFractionDigits: 2 }) + " B";
  }
  if (abs >= 1_000_000) {
    return sign + (abs / 1_000_000).toLocaleString(localeStr, { maximumFractionDigits: 2 }) + " M";
  }
  if (abs >= 1_000) {
    return sign + (abs / 1_000).toLocaleString(localeStr, { maximumFractionDigits: 1 }) + " K";
  }
  return value.toLocaleString(localeStr, { maximumFractionDigits: 1 });
}

/* ------------------------------------------------------------------
   formatIsk — the parameterised formatter that replaces the ten local
   copies catalogued in docs/DUPLICATION.md cluster 1.

   Those copies had drifted on four axes, all user-visible:
     - highest tier:  T in TradeJournal/ProfitPill, Q in WarTracker, neither
                      in CharacterPopup — so 2T rendered as "2000B" on one
                      screen and "2T" on another;
     - suffix spacing: "1.23B" vs "1.23 B";
     - decimals:      .toFixed(1) vs .toFixed(2) at the same tier;
     - locale:        only format.ts respected ru-RU.

   Four of them also shared a real bug: they compared the SIGNED value
   against the tier thresholds (`v >= 1e9`), so negative amounts fell
   through every branch and leaked raw digits — "-1234567890" instead of
   "-1.23 B". CharacterPopup.tsx documented fixing it locally; the others
   never got the fix. Routing everything through here fixes it everywhere.
   ------------------------------------------------------------------ */

export type IskTier = "K" | "M" | "B" | "T" | "Q";

const TIER_STEPS: ReadonlyArray<{ tier: IskTier; value: number }> = [
  { tier: "Q", value: 1e15 },
  { tier: "T", value: 1e12 },
  { tier: "B", value: 1e9 },
  { tier: "M", value: 1e6 },
  { tier: "K", value: 1e3 },
];

const TIER_RANK: Record<IskTier, number> = { K: 1, M: 2, B: 3, T: 4, Q: 5 };

export interface IskFormatOptions {
  /** Highest suffix to use. Values above it stay in this tier rather than
   *  promoting — so maxTier "B" renders 2e12 as "2,000 B". Default "B". */
  maxTier?: IskTier;
  /** Space between number and suffix: "1.23 B" vs "1.23B". Default true. */
  space?: boolean;
  /** Fraction digits per tier, and for un-suffixed values. */
  decimals?: Partial<Record<Lowercase<IskTier> | "unit", number>>;
  /** Prefix non-negative values with "+". Default false. */
  signed?: boolean;
}

const DEFAULT_DECIMALS: Record<Lowercase<IskTier> | "unit", number> = {
  q: 2,
  t: 2,
  b: 2,
  m: 2,
  k: 1,
  unit: 1,
};

/**
 * Abbreviated ISK. Sign-safe, locale-aware, tier/decimal configurable.
 *
 * Callers migrating off a local copy should pass whatever options preserve
 * their previous output — except the sign handling, which is always fixed.
 */
export function formatIsk(value: number, locale?: Locale, opts: IskFormatOptions = {}): string {
  if (value == null || !isFinite(value)) return "0";

  const { maxTier = "B", space = true, signed = false } = opts;
  const decimals = { ...DEFAULT_DECIMALS, ...opts.decimals };
  const localeStr = getLocaleString(locale);

  // Compare on the ABSOLUTE value — this is the negative-value bug fix.
  const abs = Math.abs(value);
  const sign = value < 0 ? "-" : signed ? "+" : "";
  const gap = space ? " " : "";
  const ceiling = TIER_RANK[maxTier];

  for (const { tier, value: step } of TIER_STEPS) {
    if (TIER_RANK[tier] > ceiling) continue; // caller opted out of this tier
    if (abs < step) continue;
    const digits = decimals[tier.toLowerCase() as Lowercase<IskTier>];
    return (
      sign + (abs / step).toLocaleString(localeStr, { maximumFractionDigits: digits }) + gap + tier
    );
  }

  return sign + abs.toLocaleString(localeStr, { maximumFractionDigits: decimals.unit });
}

/** Abbreviated ISK with an explicit +/- prefix. For deltas and P&L. */
export function formatIskSigned(value: number, locale?: Locale, opts: IskFormatOptions = {}): string {
  return formatIsk(value, locale, { ...opts, signed: true });
}

export function formatMargin(value: number, locale?: Locale): string {
  const localeStr = getLocaleString(locale);
  return value.toLocaleString(localeStr, { minimumFractionDigits: 1, maximumFractionDigits: 1 }) + "%";
}

export function formatNumber(value: number, locale?: Locale): string {
  const localeStr = getLocaleString(locale);
  return value.toLocaleString(localeStr);
}

// Format ISK with full precision (no abbreviations)
export function formatISKFull(value: number, locale?: Locale): string {
  const localeStr = getLocaleString(locale);
  return value.toLocaleString(localeStr, { maximumFractionDigits: 0 });
}
