import { useCallback, useState } from "react";
import { Check, Copy } from "lucide-react";
import { useGlobalToast } from "@/components/Toast";
import { cn } from "@/lib/utils";

/**
 * Copy a price to the clipboard for pasting into EVE.
 *
 * Two things this gets right that a hand-rolled button keeps getting wrong:
 *
 *  1. It writes `value.toFixed(2)` — a **plain** number. EVE's price field
 *     rejects "1.23 M" and every other formatted form, so what the user sees
 *     and what lands on the clipboard are deliberately different.
 *  2. It confirms *in place* with a check mark rather than firing a toast.
 *     Copying a price is a per-row action repeated a dozen times in a sitting;
 *     a dozen toasts is noise.
 */
export function CopyPrice({
  value,
  label,
  className,
}: {
  value: number;
  /** Accessible name and tooltip — e.g. "Copy price". */
  label: string;
  className?: string;
}) {
  const [copied, setCopied] = useState(false);
  const { addToast } = useGlobalToast();

  const copy = useCallback(
    async (e: React.MouseEvent) => {
      // Rows are clickable (they open the detail drawer); copying must not
      // also open it.
      e.stopPropagation();
      try {
        await navigator.clipboard.writeText(value.toFixed(2));
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1400);
      } catch {
        addToast("Clipboard unavailable", "error", 2500);
      }
    },
    [value, addToast],
  );

  return (
    <button
      type="button"
      onClick={copy}
      aria-label={label}
      title={label}
      className={cn(
        "inline-flex h-5 w-5 items-center justify-center rounded-sm transition-colors",
        copied ? "text-profit" : "text-fg-tertiary hover:bg-surface-2 hover:text-fg",
        className,
      )}
    >
      {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
    </button>
  );
}
