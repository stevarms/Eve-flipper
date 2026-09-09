// pricing.ts — EVE-legal price helpers. A direct port of
// internal/engine/pricing.go, which is the source of truth for "what price
// would EVE actually accept".
//
// EVE enforces a 4-significant-digit rule on market order prices: at
// magnitude M, the smallest legal step is 10^(M-3). So a price near 12.3M
// steps in 10k increments; near 12.3B in 1M increments; near 12.3 ISK in
// 0.01 increments.
//
// Keep this in lockstep with the Go original. If the two ever disagree, the
// Order Desk's suggested reprice and the price we drop on the clipboard stop
// agreeing about the same book, which is worse than either being wrong alone.

/** The smallest legal price increment at `price`'s magnitude. */
export function priceStep(price: number): number {
  const magnitude = Math.floor(Math.log10(price));
  return Math.pow(10, magnitude - 3);
}

/**
 * Rounds `value` onto the `place`-sized grid, scrubbing IEEE-754 noise
 * (19.17 - 0.01 naïvely yields 19.169999999999998).
 */
export function snapToGrid(value: number, place: number): number {
  if (place <= 0) return value;
  return Math.round(value / place) * place;
}

/**
 * The highest EVE-legal price strictly below `lowestSell` — what you list at
 * to take the top of the sell side. Returns 0 for anything unusable.
 */
export function nextSellUndercut(lowestSell: number): number {
  if (!(lowestSell > 0) || !Number.isFinite(lowestSell)) return 0;
  const place = priceStep(lowestSell);
  if (!(place > 0)) return 0;

  // Snap down to the nearest 4-sig-fig grid.
  const floored = Math.floor(lowestSell / place) * place;
  if (floored < lowestSell) return snapToGrid(floored, place);

  // Already on a valid boundary — step down one place.
  const stepped = lowestSell - place;
  if (stepped <= 0) return 0;
  return snapToGrid(stepped, place);
}

/**
 * The lowest EVE-legal price strictly above `topBuy` — what you bid at to take
 * the top of the buy side. Returns 0 for anything unusable.
 */
export function nextBuyOverbid(topBuy: number): number {
  if (!(topBuy > 0) || !Number.isFinite(topBuy)) return 0;
  const place = priceStep(topBuy);
  if (!(place > 0)) return 0;

  const ceiled = Math.ceil(topBuy / place) * place;
  if (ceiled > topBuy) return snapToGrid(ceiled, place);
  return snapToGrid(topBuy + place, place);
}

/**
 * Renders a grid price with exactly the decimals its step needs, so pasting it
 * into EVE reproduces the number we computed. `toFixed(2)` would round 5.499
 * (a legal sub-10-ISK undercut) up to 5.50 — back above the price we were
 * trying to undercut.
 */
export function formatGridPrice(value: number, place: number): string {
  if (!Number.isFinite(value)) return "";
  const decimals = place > 0 ? Math.min(4, Math.max(0, -Math.floor(Math.log10(place)))) : 2;
  return value.toFixed(decimals);
}
