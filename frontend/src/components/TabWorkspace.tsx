import { useRef, type ReactNode } from "react";

interface TabPanelProps {
  active: boolean;
  children: ReactNode;
  /**
   * Keep the subtree mounted (hidden) after the first visit instead of
   * unmounting it on navigate-away.
   *
   * Only for tabs holding unsaved state that lives nowhere else — see
   * KEEP_ALIVE_TABS in lib/cockpit.ts. Every keep-alive tab is DOM the
   * browser pays for on every frame forever, which is exactly what made
   * the pre-overhaul app render 44,773 elements at once.
   */
  keepAlive?: boolean;
}

interface TabActionBarProps {
  children: ReactNode;
  tone?: "default" | "warning" | "accent";
  className?: string;
}

export function TabPanel({ active, children, keepAlive = false }: TabPanelProps) {
  // Lazy: never build a tab's DOM until it is first opened. On a cold start
  // this means one tab exists instead of eleven.
  const hasBeenActive = useRef(active);
  if (active) hasBeenActive.current = true;

  if (!active) {
    // Unmount unless the tab explicitly opted out. This is the fix for the
    // pre-overhaul behaviour, where every tab stayed mounted behind a CSS
    // `hidden` class and the browser laid out all of them on every frame.
    if (!keepAlive || !hasBeenActive.current) return null;
    return <div className="hidden">{children}</div>;
  }

  return <div className="flex-1 min-h-0 flex flex-col overflow-hidden">{children}</div>;
}

export function TabActionBar({ children, tone = "default", className = "" }: TabActionBarProps) {
  const toneClass = {
    default: "border-eve-border/30 bg-eve-dark/30 text-eve-dim",
    warning: "border-amber-400/30 bg-amber-400/10 text-amber-200",
    accent: "border-eve-accent/30 bg-eve-accent/10 text-eve-text",
  }[tone];

  return (
    <div className={`shrink-0 flex items-center gap-2 px-3 py-2 text-xs border-b ${toneClass} ${className}`}>
      {children}
    </div>
  );
}

export const tabWorkspaceClass = "eve-tab-workspace flex-1 min-h-0 flex flex-col p-2";
