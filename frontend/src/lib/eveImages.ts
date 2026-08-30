/**
 * EVE Image Server helpers.
 *
 * CCP serves every type icon, ship render, portrait and corp/alliance logo
 * from https://images.evetech.net — public, unauthenticated, CDN-cached.
 *
 * Why this file exists: icon URLs were previously hand-built inline in
 * `character-popup/` (CombinedOrdersTab, OptimizerTab, OverviewTab) and the
 * App header, and nowhere else. The main scan tables showed no icons at all.
 * Centralising it here means every table can render EVE's own art with one
 * import, and the blueprint special case below lives in exactly one place.
 *
 * Endpoint shape:  /types/{typeId}/{variant}?size={size}
 * Available variants for a type can be listed with `GET /types/{id}/`.
 */

const BASE = "https://images.evetech.net";

/** Sizes the image server actually serves. Anything else 404s. */
export type IconSize = 32 | 64 | 128 | 256 | 512;

/**
 * Which art variant to request.
 *
 * `icon`  — the standard inventory icon. Correct for almost everything.
 * `bp`    — blueprint ORIGINAL. Blueprint types do NOT serve `icon`; asking
 *           for it returns HTTP 400.
 * `bpc`   — blueprint COPY. Visually distinct from `bp` in-client, which is
 *           exactly what the Industry scanner needs: BPO and BPC rows for the
 *           same product currently look like duplicated rows with identical
 *           economics, and the icon is the cheapest way to tell them apart.
 * `render`— 3D render. Ships and structures only; most types 400 on this.
 */
export type TypeArt = "icon" | "bp" | "bpc" | "render";

/**
 * URL for an inventory type's artwork.
 *
 * Pass `art` explicitly for blueprints — we cannot infer BPO vs BPC from the
 * type ID alone, because both share it. The distinction lives in the row data
 * (owned original vs copy), not in the type.
 */
export function typeIconUrl(typeId: number, art: TypeArt = "icon", size: IconSize = 32): string {
  return `${BASE}/types/${typeId}/${art}?size=${size}`;
}

/** Convenience for blueprint rows, where the caller knows the copy flag. */
export function blueprintIconUrl(typeId: number, isCopy: boolean, size: IconSize = 32): string {
  return typeIconUrl(typeId, isCopy ? "bpc" : "bp", size);
}

export function characterPortraitUrl(characterId: number, size: IconSize = 32): string {
  return `${BASE}/characters/${characterId}/portrait?size=${size}`;
}

export function corporationLogoUrl(corporationId: number, size: IconSize = 32): string {
  return `${BASE}/corporations/${corporationId}/logo?size=${size}`;
}

export function allianceLogoUrl(allianceId: number, size: IconSize = 32): string {
  return `${BASE}/alliances/${allianceId}/logo?size=${size}`;
}

/**
 * EVE's canonical security-status colour buckets, matching the in-client
 * ramp. Returns a token name from the `sec` scale in tailwind.config.ts.
 *
 * Callers use it as e.g. `text-sec-${securityTone(sec)}`, so the four
 * literals below must stay in sync with the `sec` colours.
 */
export function securityTone(security: number): "high" | "mid" | "low" | "null" {
  if (security >= 0.75) return "high";
  if (security >= 0.45) return "mid";
  if (security > 0.0) return "low";
  return "null";
}

/** Security status as EVE displays it: one decimal, clamped at 0.0. */
export function formatSecurity(security: number): string {
  return (security < 0 ? security : Math.max(0, security)).toFixed(1);
}
