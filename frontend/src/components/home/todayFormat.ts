import { formatGridPrice, priceStep } from "@/lib/pricing";
import type { TodayActionKind, TodayGrade, TodayUrgency } from "@/lib/types";
import type { TranslationKey } from "@/lib/i18n";

/**
 * Presentation helpers shared by the Today components.
 *
 * The row *content* — headline, why, evidence, blockers, cap names — is
 * generated in internal/engine/today.go and arrives as English prose, because
 * the sentences are produced by the same code that decides the ranking. Only
 * the labels around it are translated, which is what these maps are for.
 */

export const TODAY_GRADE_LABEL: Record<TodayGrade, TranslationKey> = {
  proven: "todayGradeProven",
  likely: "todayGradeLikely",
  unproven: "todayGradeUnproven",
  avoid: "todayGradeAvoid",
};

export const TODAY_GRADE_WHY: Record<TodayGrade, TranslationKey> = {
  proven: "todayGradeWhyProven",
  likely: "todayGradeWhyLikely",
  unproven: "todayGradeWhyUnproven",
  avoid: "todayGradeWhyAvoid",
};

/** Grades are status, so they take Badge tones rather than ad-hoc colours. */
export const TODAY_GRADE_TONE: Record<TodayGrade, "profit" | "info" | "warn" | "loss"> = {
  proven: "profit",
  likely: "info",
  unproven: "warn",
  avoid: "loss",
};

export const TODAY_KIND_LABEL: Record<TodayActionKind, TranslationKey> = {
  reprice: "todayKindReprice",
  cancel: "todayKindCancel",
  buy: "todayKindBuy",
  list: "todayKindList",
  deliver: "todayKindDeliver",
  pi_restart: "todayKindPiRestart",
};

export const TODAY_URGENCY_LABEL: Record<TodayUrgency, TranslationKey> = {
  now: "todayUrgencyNow",
  today: "todayUrgencyToday",
  soon: "todayUrgencySoon",
};

/**
 * The exact digits to paste into EVE.
 *
 * The backend already snapped the price onto EVE's 4-significant-digit grid,
 * so this only has to render it without introducing a thousands separator or a
 * magnitude suffix — either of which EVE's price field rejects. Deliberately
 * not `formatIsk`: what the user reads and what lands on the clipboard are
 * different strings, which is the same split `CopyPrice` enforces.
 */
export function todayPasteText(price: number): string {
  if (!Number.isFinite(price) || price <= 0) return "";
  return formatGridPrice(price, priceStep(price));
}

/** Minutes, rounded for a human. Never "0 min" for work that remains. */
export function todayMinutes(seconds: number): number {
  if (seconds <= 0) return 0;
  return Math.max(1, Math.round(seconds / 60));
}

export function todayRelativeAge(iso: string, now = Date.now()): string {
  const then = Date.parse(iso);
  if (!Number.isFinite(then)) return "";
  const mins = Math.max(0, Math.round((now - then) / 60000));
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.round(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.round(hours / 24)}d ago`;
}

/**
 * EVE's multibuy window accepts `Name\tQty` per line, so one paste fills the
 * whole list. Type names are scrubbed of tabs and newlines: a stray one would
 * split a row and silently buy the wrong thing.
 */
export function todayMultibuyText(items: { type_name: string; quantity: number }[]): string {
  return items
    .map((i) => `${i.type_name.replace(/[\r\n\t]/g, " ")}\t${Math.max(1, Math.round(i.quantity))}`)
    .join("\n");
}

/** A two-column paste for a second monitor: name, then the price to set. */
export function todayPriceListText(items: { type_name: string; price?: number }[]): string {
  return items
    .map((i) => `${i.type_name.replace(/[\r\n\t]/g, " ")}\t${todayPasteText(i.price ?? 0)}`)
    .join("\n");
}
