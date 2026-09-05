import {
  BookOpen,
  Factory,
  ListChecks,
  Package,
  Radar,
  TrendingUp,
  type LucideIcon,
} from "lucide-react";
import {
  WORKSPACE_META,
  type MainTabId,
  type WorkspaceId,
} from "@/lib/cockpit";
import { useI18n } from "@/lib/i18n";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

/**
 * Left icon rail — the primary navigation.
 *
 * Replaces a horizontal band of 11 tabs. The band cost a full row of
 * vertical space on every screen and read as a grab-bag; the rail costs
 * 48px of horizontal space, which we have far more of, and groups the
 * tools so you can see the whole toolset at once.
 *
 * Labels live in tooltips rather than under the icons: five icons with
 * captions would be as tall as the band we just removed.
 */

const ICONS: Record<WorkspaceMeta["icon"], LucideIcon> = {
  ListChecks,
  TrendingUp,
  Factory,
  Package,
  BookOpen,
  Radar,
};

type WorkspaceMeta = (typeof WORKSPACE_META)[WorkspaceId];

export interface WorkspaceRailProps {
  workspaces: WorkspaceId[];
  active: WorkspaceId;
  onSelect: (workspace: WorkspaceId) => void;
  /** Rendered at the bottom of the rail — settings, theme, etc. */
  footer?: React.ReactNode;
}

export function WorkspaceRail({ workspaces, active, onSelect, footer }: WorkspaceRailProps) {
  const { t } = useI18n();

  return (
    <nav
      aria-label="Workspaces"
      className="flex w-12 shrink-0 flex-col items-center gap-1 border-r border-eve-border bg-surface-1 py-2"
    >
      {workspaces.map((id) => {
        const meta = WORKSPACE_META[id];
        const Icon = ICONS[meta.icon];
        const isActive = id === active;
        const label = t(meta.labelKey) || meta.fallback;

        return (
          <Tooltip key={id}>
            <TooltipTrigger asChild>
              <button
                type="button"
                aria-label={label}
                aria-current={isActive ? "page" : undefined}
                onClick={() => onSelect(id)}
                className={cn(
                  "relative flex h-9 w-9 items-center justify-center rounded-sm transition-colors",
                  "focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-eve-accent",
                  isActive
                    ? "bg-eve-accent/15 text-eve-accent"
                    : "text-fg-tertiary hover:bg-surface-2 hover:text-fg-secondary",
                )}
              >
                {/* Active marker on the rail edge — reads at a glance without
                    relying on the accent fill alone. */}
                {isActive && (
                  <span
                    aria-hidden="true"
                    className="absolute -left-2 top-1/2 h-5 w-0.5 -translate-y-1/2 rounded-r bg-eve-accent"
                  />
                )}
                <Icon className="h-[18px] w-[18px]" strokeWidth={1.75} />
              </button>
            </TooltipTrigger>
            <TooltipContent side="right">{label}</TooltipContent>
          </Tooltip>
        );
      })}

      {footer ? <div className="mt-auto flex flex-col items-center gap-1">{footer}</div> : null}
    </nav>
  );
}

/**
 * Secondary tab row for the tabs inside the active workspace.
 * Renders nothing when the workspace has a single tab — no point spending a
 * band on a one-item list.
 */
export interface WorkspaceTabsProps {
  tabs: MainTabId[];
  active: MainTabId;
  onSelect: (tab: MainTabId) => void;
  label: (tab: MainTabId) => string;
  /** Right-aligned slot — the workspace's primary action (usually Scan). */
  actions?: React.ReactNode;
}

export function WorkspaceTabs({ tabs, active, onSelect, label, actions }: WorkspaceTabsProps) {
  if (tabs.length <= 1 && !actions) return null;

  return (
    <div className="flex items-stretch border-b border-eve-border">
      <div className="min-w-0 flex-1 overflow-x-auto scrollbar-thin">
        {/* Named because it is not the only tablist on the page — Industry
            renders its own sub-tab strip and stays mounted across workspaces
            (KEEP_ALIVE_TABS), so a bare [role="tablist"] matches both. */}
        <div className="flex min-w-max items-center gap-0.5 px-1" role="tablist" aria-label="Workspace tabs">
          {tabs.length > 1 &&
            tabs.map((tab) => {
              const isActive = tab === active;
              return (
                <button
                  key={tab}
                  type="button"
                  role="tab"
                  aria-selected={isActive}
                  onClick={() => onSelect(tab)}
                  className={cn(
                    "relative -mb-px h-8 whitespace-nowrap rounded-t-sm px-3",
                    "font-ui text-t-body font-medium transition-colors",
                    "focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-eve-accent",
                    isActive
                      ? "border-b-2 border-eve-accent text-fg"
                      : "text-fg-tertiary hover:text-fg-secondary",
                  )}
                >
                  {label(tab)}
                </button>
              );
            })}
        </div>
      </div>
      {actions ? (
        <div className="flex shrink-0 items-center gap-1.5 border-l border-eve-border px-2">
          {actions}
        </div>
      ) : null}
    </div>
  );
}
