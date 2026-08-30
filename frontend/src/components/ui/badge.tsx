import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

/**
 * Badge — status and state only.
 *
 * `docs/UI_AUDIT.md` finding A9 called out that pills, chips and badges had
 * drifted into interchangeable use. The rule this restores:
 *
 *   Badge (here)  — status / state.        ACTIVE, WATCH, DEMO, STALE
 *   Chip          — filter toggles, tags.  (rounded-sm, interactive)
 *   Pill          — numeric key metrics.   (rounded-full, see ProfitPill)
 *
 * Tones are semantic. `profit` for a favourable state, `loss` for a blocking
 * one, `warn` for caution, `info` for neutral information.
 */
const badgeVariants = cva(
  cn(
    "inline-flex items-center gap-1 rounded-sm border px-1.5 py-0.5",
    "font-ui text-t-caption font-medium uppercase tracking-wide leading-none",
  ),
  {
    variants: {
      tone: {
        neutral: "border-eve-border bg-surface-3 text-fg-secondary",
        profit: "border-profit/40 bg-profit/10 text-profit",
        loss: "border-loss/40 bg-loss/10 text-loss",
        warn: "border-warn/40 bg-warn/10 text-warn",
        info: "border-info/40 bg-info/10 text-info",
        accent: "border-eve-accent/40 bg-eve-accent/10 text-eve-accent",
      },
    },
    defaultVariants: { tone: "neutral" },
  },
);

export interface BadgeProps
  extends React.HTMLAttributes<HTMLSpanElement>,
    VariantProps<typeof badgeVariants> {}

export function Badge({ className, tone, ...props }: BadgeProps) {
  return <span className={cn(badgeVariants({ tone }), className)} {...props} />;
}

export { badgeVariants };
