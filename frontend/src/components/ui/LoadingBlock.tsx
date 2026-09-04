import { cn } from "@/lib/utils";

/**
 * The one spinner.
 *
 * This exact markup — an `inline-block` square with a transparent-ish ring
 * and a solid top edge, spun by `animate-spin` — was hand-copied into
 * thirteen places across twelve files. Three of those copies had drifted to
 * hardcoded English labels, and one asked for `border-3`, which Tailwind does
 * not ship (widths are 0/1/2/4/8), so it rendered with no ring at all: a
 * full-page loading state that showed nothing turning.
 *
 * Also announces itself. Every hand-rolled copy was silent to a screen
 * reader, which is the one audience that cannot see a spinner.
 */

const RING = {
  sm: "h-3 w-3 border-2",
  md: "h-4 w-4 border-2",
  lg: "h-8 w-8 border-4",
} as const;

export function Spinner({
  size = "md",
  className,
}: {
  size?: keyof typeof RING;
  className?: string;
}) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "inline-block shrink-0 animate-spin rounded-full",
        "border-eve-accent/40 border-t-eve-accent",
        RING[size],
        className,
      )}
    />
  );
}

export interface LoadingBlockProps {
  /** What is loading, in the user's language. Omit for a bare spinner. */
  label?: string;
  size?: keyof typeof RING;
  /** "column" stacks the label under the spinner — for full-page waits. */
  layout?: "row" | "column";
  /** Centre in the parent's full height rather than adding padding of its own. */
  fill?: boolean;
  className?: string;
}

export function LoadingBlock({
  label,
  size = "md",
  layout = "row",
  fill = false,
  className,
}: LoadingBlockProps) {
  return (
    <div
      role="status"
      aria-live="polite"
      className={cn(
        "flex items-center justify-center font-ui text-t-cell text-fg-tertiary",
        layout === "column" ? "flex-col gap-3" : "gap-2",
        fill ? "h-full" : "py-8",
        className,
      )}
    >
      <Spinner size={size} />
      {label}
    </div>
  );
}
