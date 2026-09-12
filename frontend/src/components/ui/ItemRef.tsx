import type { ReactNode } from "react";
import { useI18n } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import { CopyButton } from "./CopyButton";
import { OpenMarketButton, type ClickMods, type CopyOnOpen } from "./OpenMarketButton";
import { TypeIcon } from "./TypeIcon";
import type { ActionReveal } from "./rowAction";

export interface ItemRefProps {
  typeId: number;
  name?: string;
  /** SDE category when known — 9 skips a wasted `/icon` request for blueprints. */
  categoryId?: number;
  /** 18 in table rows (TypeIcon's default), 24 in drawer titles. */
  iconSize?: number;
  /** Text scale. Literal branches, never `text-${tone}`. */
  tone?: "cell" | "emphasis" | "title";
  /** Show the open-in-market button. */
  market?: boolean;
  /** Show the copy-item-name button. */
  copyName?: boolean;
  /** Threaded to OpenMarketButton — Station Trading's Shift-modifier price. */
  copyOnOpen?: CopyOnOpen | ((mods: ClickMods) => CopyOnOpen | null);
  reveal?: ActionReveal;
  /** Second line — "Manual" holding, meta group, station. */
  subtitle?: ReactNode;
  /** Override for surfaces with their own market-button wording. */
  marketLabel?: string;
  className?: string;
}

/**
 * An EVE inventory type as the user refers to it: icon, name, and the two
 * actions that name affords — open it in game, copy it to paste in game.
 *
 * Sixteen places used to hand-roll
 * `<img src="https://images.evetech.net/types/${id}/icon?size=32">`, every one
 * of which rendered **blank for blueprints** (CCP 400s on `/icon` for category
 * 9 — the exact case `TypeIcon` exists to handle).
 *
 * Renders a `<span>` with `display:flex`, never a `<div>` and never the `<td>`
 * itself. That is what lets the same component sit in a table cell, in a flex
 * row, and inside the Radix `Dialog.Title` (`<h2>`) that `sheet.tsx` uses for
 * drawer titles.
 *
 * **The caller owns width** (`<td className="max-w-[320px]">`); `ItemRef` owns
 * `min-w-0` + `truncate` + `title`. Getting that division wrong is why the
 * Order Desk's copy button used to sit pinned to the far right of a 220px cell,
 * detached from the name it belonged to.
 *
 * Actions are booleans rather than an array: an inline `["market","copyName"]`
 * literal would be a new object every render and defeat any row-level memo.
 */
export function ItemRef({
  typeId,
  name,
  categoryId,
  iconSize = 18,
  tone = "cell",
  market,
  copyName,
  copyOnOpen,
  reveal = "always",
  subtitle,
  marketLabel,
  className,
}: ItemRefProps) {
  const { t } = useI18n();
  const text = name || `#${typeId}`;
  // Literal branches — see docs/UI_DESIGN_SYSTEM.md §6.
  const nameCls =
    tone === "title"
      ? "font-ui text-t-title font-semibold text-fg"
      : tone === "emphasis"
        ? "font-ui text-t-emphasis text-fg"
        : "font-ui text-t-cell text-fg";

  return (
    <span className={cn("flex min-w-0 items-center gap-1.5", className)}>
      <TypeIcon typeId={typeId} categoryId={categoryId} size={iconSize} />
      <span className="min-w-0 flex-1">
        <span className={cn("block truncate", nameCls)} title={name}>
          {text}
        </span>
        {subtitle && (
          <span className="block font-ui text-t-caption text-fg-tertiary">{subtitle}</span>
        )}
      </span>
      {market && (
        <OpenMarketButton
          typeId={typeId}
          copyOnOpen={copyOnOpen}
          label={marketLabel}
          reveal={reveal}
        />
      )}
      {copyName && name && (
        <CopyButton text={name} label={t("copyItem")} reveal={reveal} />
      )}
    </span>
  );
}
