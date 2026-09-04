import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

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
}: {
  label: string;
  value: ReactNode;
  /** Plain-English explanation, shown on hover. Prefer this over an acronym. */
  hint?: string;
  tone?: DetailTone;
}) {
  return (
    <div className="flex items-baseline justify-between gap-3 py-1">
      <dt className="font-ui text-t-cell text-fg-tertiary" title={hint}>
        {label}
      </dt>
      <dd
        className={cn(
          "font-num tnum text-t-cell text-right",
          tone === "profit" && "text-profit",
          tone === "loss" && "text-loss",
          tone === "warn" && "text-warn",
          tone === "info" && "text-info",
          tone === "muted" && "text-fg-tertiary",
          !tone && "text-fg-secondary",
        )}
      >
        {value}
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
