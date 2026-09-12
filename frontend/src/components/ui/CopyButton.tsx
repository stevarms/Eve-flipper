import { useCallback, useEffect, useRef, useState } from "react";
import { Check, Copy } from "lucide-react";
import { useOptionalToast } from "@/components/Toast";
import { useI18n } from "@/lib/i18n";
import { rowActionClasses, rowActionIcon, type ActionReveal, type ActionSize } from "./rowAction";

export interface CopyButtonProps {
  /**
   * Copied VERBATIM. Must already be paste-ready.
   *
   * For a price use `CopyPrice` — it owns the plain-digits guarantee. EVE's
   * price field rejects "1.23 M" and every other formatted form.
   */
  text: string;
  /** Accessible name and tooltip. An icon-only button has no other name. */
  label: string;
  reveal?: ActionReveal;
  size?: ActionSize;
  disabled?: boolean;
  className?: string;
}

/**
 * Copy a string to the clipboard.
 *
 * The lucide `Copy` glyph (two overlapping squares) is the app's one copy
 * affordance; it replaced 📋, ⧉ and ⎘, which had drifted into four visuals and
 * two confirmation models across six hand-rolled implementations.
 *
 * Confirms **in place** with a check mark rather than firing a toast. Copying
 * is a per-row action repeated a dozen times in a sitting, and a dozen toasts
 * is noise.
 */
export function CopyButton({
  text,
  label,
  reveal = "always",
  size = "sm",
  disabled,
  className,
}: CopyButtonProps) {
  const { t } = useI18n();
  const { addToast } = useOptionalToast();
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // Rows are recycled across re-sorted and paginated lists; a pending timer
  // firing after unmount would set state on a dead component.
  useEffect(() => () => clearTimeout(timer.current), []);

  const copy = useCallback(
    async (e: React.MouseEvent) => {
      // Rows are clickable — they open the detail drawer or toggle selection.
      // Copying must not also do that.
      e.stopPropagation();
      try {
        await navigator.clipboard.writeText(text);
        setCopied(true);
        clearTimeout(timer.current);
        timer.current = setTimeout(() => setCopied(false), 1400);
      } catch {
        addToast(t("clipboardUnavailable"), "error", 2500);
      }
    },
    [text, addToast, t],
  );

  return (
    <button
      type="button"
      onClick={copy}
      disabled={disabled || !text}
      aria-label={label}
      title={label}
      className={rowActionClasses(reveal, size, copied && "text-profit", className)}
    >
      {copied ? (
        <Check aria-hidden="true" className={rowActionIcon(size)} />
      ) : (
        <Copy aria-hidden="true" className={rowActionIcon(size)} />
      )}
      {/* The check mark is the confirmation and it is purely visual, so a
          screen reader would otherwise hear nothing at all happen. */}
      <span role="status" aria-live="polite" className="sr-only">
        {copied ? t("copied") : ""}
      </span>
    </button>
  );
}
