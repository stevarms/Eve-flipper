import { useCallback, useRef } from "react";
import { ExternalLink } from "lucide-react";
import { useOptionalToast } from "@/components/Toast";
import { useEveUiActions } from "@/lib/eveUiActions";
import { useEveUiLoggedIn } from "@/lib/eveUiContext";
import { useI18n, type TranslationKey } from "@/lib/i18n";
import { rowActionClasses, rowActionIcon, type ActionReveal, type ActionSize } from "./rowAction";

/** What to put on the clipboard when the market window opens. */
export interface CopyOnOpen {
  /**
   * ALREADY paste-ready plain digits. Build it with `formatGridPrice` from
   * lib/pricing (preferred — it respects EVE's 4-significant-digit grid) or
   * `Number#toFixed`. Never with `formatIsk`.
   */
  text: string;
  /** Toast key instead of the generic `actionSuccess`. */
  toastKey?: TranslationKey;
  toastParams?: Record<string, string | number>;
}

export interface ClickMods {
  shift: boolean;
  alt: boolean;
  ctrl: boolean;
}

export interface OpenMarketButtonProps {
  typeId: number;
  /**
   * Optional clipboard side effect. Pass a FUNCTION when the modifier keys
   * change the answer — Station Trading's Shift means "copy the sell price
   * instead of the buy price".
   */
  copyOnOpen?: CopyOnOpen | ((mods: ClickMods) => CopyOnOpen | null);
  /**
   * Accessible name and tooltip. Defaults to `t("openMarket")`. Override where
   * a surface has more specific wording — Positions explains the ESI scope,
   * Station Trading documents the Shift modifier.
   */
  label?: string;
  reveal?: ActionReveal;
  size?: ActionSize;
  className?: string;
}

/**
 * Open an item's market window in the running EVE client.
 *
 * The lucide `ExternalLink` square-with-arrow is the app's one open-in-EVE
 * affordance; it replaced a 🎮 emoji, two differently-styled labelled buttons
 * and a context-menu-only entry.
 *
 * **Icon only, by design.** There is no `children` and `label` never renders —
 * it feeds `aria-label`/`title`. Horizontal space in these rows is scarce, and
 * a component that cannot paint text is what stops "List" and "Open market"
 * from growing back.
 */
export function OpenMarketButton({
  typeId,
  copyOnOpen,
  label,
  reveal = "always",
  size = "sm",
  className,
}: OpenMarketButtonProps) {
  const { t } = useI18n();
  const { addToast } = useOptionalToast();
  const { openMarket } = useEveUiActions();
  const loggedIn = useEveUiLoggedIn();
  // A ref, not state: the in-flight guard must not re-render the row or the
  // button would shift under the cursor mid-click.
  const busy = useRef(false);

  const name = label ?? t("openMarket");

  const click = useCallback(
    async (e: React.MouseEvent<HTMLButtonElement>) => {
      // Rows are clickable — they open the detail drawer or toggle selection.
      e.stopPropagation();
      if (busy.current) return;

      const plan =
        typeof copyOnOpen === "function"
          ? copyOnOpen({ shift: e.shiftKey, alt: e.altKey, ctrl: e.ctrlKey })
          : (copyOnOpen ?? null);

      busy.current = true;
      let ok = false;
      try {
        // useEveUiActions owns the not-logged-in and error toasts.
        ok = await openMarket(typeId);
      } finally {
        busy.current = false;
      }
      if (!ok) return;

      if (!plan?.text) {
        addToast(t("actionSuccess"), "success", 2000);
        return;
      }
      try {
        await navigator.clipboard.writeText(plan.text);
        addToast(t(plan.toastKey ?? "actionSuccess", plan.toastParams), "success", 2400);
      } catch {
        // Clipboard rejected (insecure context, headless). The window still
        // opened, so don't report failure.
        addToast(t("actionSuccess"), "success", 2000);
      }
    },
    [typeId, copyOnOpen, openMarket, addToast, t],
  );

  // Rows built from partial data (an unmatched Price Audit line, an industry
  // action with no product) have no type to open.
  if (!typeId || typeId <= 0) return null;

  return (
    <button
      type="button"
      onClick={click}
      aria-label={name}
      /* Native title=, not the Radix Tooltip the design system prefers for
         prose: this mounts once per row, and a Tooltip.Root per row is real
         cost. Column headers, which are one node, keep the Radix tooltip. */
      title={name}
      className={rowActionClasses(reveal, size, !loggedIn && "opacity-50", className)}
    >
      <ExternalLink aria-hidden="true" className={rowActionIcon(size)} />
    </button>
  );
}
