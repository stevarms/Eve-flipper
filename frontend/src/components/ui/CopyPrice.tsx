import { formatGridPrice } from "@/lib/pricing";
import { CopyButton } from "./CopyButton";
import type { ActionReveal, ActionSize } from "./rowAction";

/**
 * Copy a price to the clipboard for pasting into EVE.
 *
 * Two things this gets right that a hand-rolled button keeps getting wrong:
 *
 *  1. It writes a **plain** number. EVE's price field rejects "1.23 M" and
 *     every other formatted form, so what the user sees and what lands on the
 *     clipboard are deliberately different.
 *  2. It confirms in place rather than firing a toast — see `CopyButton`.
 *
 * `value` is a NUMBER and always will be. That is not an oversight to be
 * "fixed" by widening it to `number | string`: a formatted string is exactly
 * what EVE rejects, and this narrow type is the only thing stopping
 * `value={formatIsk(p)}` from type-checking. Strings go through `CopyButton`,
 * which is named for text and cannot be mistaken for this. `CopyPrice.test.tsx`
 * guards the output format.
 */
export function CopyPrice({
  value,
  step,
  label,
  reveal = "always",
  size = "sm",
  className,
}: {
  value: number;
  /**
   * EVE's price grid: prices carry 4 significant digits, so the smallest legal
   * move on a 12.3M item is 10k, not 0.01. Pass `priceStep(basis)` from
   * lib/pricing and the clipboard gets `formatGridPrice`, which — unlike
   * `toFixed(2)` — will not round a legal 5.499 undercut up to 5.50 and back
   * above the price it was undercutting. Omit it for a display price that is
   * not being used to outbid anything.
   */
  step?: number;
  /** Accessible name and tooltip — e.g. `t("copyPrice")`. */
  label: string;
  reveal?: ActionReveal;
  size?: ActionSize;
  className?: string;
}) {
  const text = step && step > 0 ? formatGridPrice(value, step) : value.toFixed(2);
  return (
    <CopyButton text={text} label={label} reveal={reveal} size={size} className={className} />
  );
}
