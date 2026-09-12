import { cn } from "@/lib/utils";

/**
 * Shared shell for the two row-action buttons — `OpenMarketButton` and
 * `CopyButton`.
 *
 * Both are icon-only by design. The app previously grew five different
 * open-in-EVE treatments (🎮 emoji, `ExternalLink` + "List", `ExternalLink` +
 * "Open market", bare text, context-menu-only) and four clipboard glyphs
 * (📋, ⧉, ⎘, lucide `Copy`); the point of putting the box style here is that
 * there is exactly one of each again, and adding a third means editing this
 * file rather than inventing a sixth.
 */

/**
 * Where the button lives decides whether it is always on screen.
 *
 *  - `"always"` — tables whose purpose is acting in-game (Station Trading,
 *    Order Desk, Positions, Scan Results, Watchlist, PLEX, shopping list).
 *    The user is here *to* click these.
 *  - `"hover"`  — reference and historical surfaces (journal, scan history,
 *    backtest, drawers, dashboards). The action is available but should not
 *    compete with the data for attention.
 */
export type ActionReveal = "always" | "hover";

/** `sm` for table rows, `md` for drawer titles and card headers. */
export type ActionSize = "sm" | "md";

/**
 * Literal branches only. `h-${n}` / `opacity-${n}` do not exist — Tailwind
 * scans source *text*, so an interpolated class is in the bundle only if some
 * other file happened to mention it. See docs/UI_DESIGN_SYSTEM.md §6.
 */
const SIZE = {
  sm: { box: "h-5 w-5", icon: "h-3.5 w-3.5" },
  md: { box: "h-6 w-6", icon: "h-4 w-4" },
} as const;

export function rowActionClasses(
  reveal: ActionReveal,
  size: ActionSize,
  ...extra: (string | false | undefined)[]
): string {
  return cn(
    "inline-flex shrink-0 items-center justify-center rounded-sm transition-colors",
    "text-fg-tertiary hover:bg-surface-2 hover:text-fg",
    "focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-eve-accent",
    "disabled:pointer-events-none disabled:opacity-40",
    SIZE[size].box,
    // `action-reveal` is a CSS component class (index.css), not a utility
    // string — see the comment there for why `group-hover:` is not enough.
    reveal === "hover" && "action-reveal",
    ...extra,
  );
}

export function rowActionIcon(size: ActionSize): string {
  return SIZE[size].icon;
}
