import type { ReactNode } from "react";
import { cn } from "@/lib/utils";
import { CopyPrice } from "./CopyPrice";

/**
 * The label/value list that tier 2 of the disclosure rule is made of
 * (docs/UI_DESIGN_SYSTEM.md §4).
 *
 * Every row drawer shows the same thing: groups of labelled figures that the
 * six-column grid had no room for. Station Trade grew the first copy; rather
 * than let Orders grow a second one that drifts from it, both import these.
 *
 * Labels are prose (`font-ui`) and values are digits (`font-num tnum`), which
 * is the whole typography split in two lines — and the reason the numbers in
 * a drawer line up when the labels beside them do not.
 */

export type DetailTone = "profit" | "loss" | "warn" | "info" | "muted";

export function DetailRow({
  label,
  value,
  hint,
  tone,
  copyValue,
  copyStep,
  copyLabel,
}: {
  label: string;
  value: ReactNode;
  /** Plain-English explanation, shown on hover. Prefer this over an acronym. */
  hint?: string;
  tone?: DetailTone;
  /**
   * Raw number behind `value`, when this row shows a price the user would
   * paste into EVE. Adds a hover-revealed copy button that writes plain
   * digits — `value` is usually formatted ("1.23 M"), which EVE rejects.
   *
   * Only for prices that can actually be typed into an order: a buy, a sell,
   * a suggested bid, a ladder level. Not for totals, P&L or fees — a copy
   * button on every ISK figure in a drawer is noise.
   */
  copyValue?: number;
  /** EVE's 4-significant-digit grid step; see CopyPrice. */
  copyStep?: number;
  /**
   * Accessible name for the copy button, normally `t("copyPrice")`.
   *
   * Passed in rather than read from context on purpose: DetailRow is a pure
   * presentational primitive and the drawers that use it are rendered bare in
   * tests, where `useI18n()` returns null (the context is created with
   * `null!`). Threading the string keeps this component context-free.
   */
  copyLabel?: string;
}) {
  return (
    <div className="group flex items-baseline justify-between gap-3 py-1">
      <dt className="font-ui text-t-cell text-fg-tertiary" title={hint}>
        {label}
      </dt>
      <dd
        className={cn(
          "flex items-baseline gap-1 font-num tnum text-t-cell text-right",
          tone === "profit" && "text-profit",
          tone === "loss" && "text-loss",
          tone === "warn" && "text-warn",
          tone === "info" && "text-info",
          tone === "muted" && "text-fg-tertiary",
          !tone && "text-fg-secondary",
        )}
      >
        {value}
        {copyValue !== undefined && copyValue > 0 && (
          <CopyPrice
            value={copyValue}
            step={copyStep}
            label={copyLabel ?? "Copy price"}
            reveal="hover"
            className="self-center"
          />
        )}
      </dd>
    </div>
  );
}

export function DetailGroup({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="mb-4">
      <h3 className="mb-1 border-b border-eve-border pb-1 font-ui text-t-caption font-semibold uppercase tracking-wide text-fg-tertiary">
        {title}
      </h3>
      <dl>{children}</dl>
    </section>
  );
}
