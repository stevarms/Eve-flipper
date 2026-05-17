import type { ReactNode } from "react";

interface TabPanelProps {
  active: boolean;
  children: ReactNode;
}

interface TabActionBarProps {
  children: ReactNode;
  tone?: "default" | "warning" | "accent";
  className?: string;
}

export function TabPanel({ active, children }: TabPanelProps) {
  return (
    <div className={`flex-1 min-h-0 flex flex-col overflow-hidden ${active ? "" : "hidden"}`}>
      {children}
    </div>
  );
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
