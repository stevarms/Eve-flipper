// The Profitable Blueprints scanner's own saved parameters live under this
// key. Other tools that price "the way the scanner does" (the LP Store tab)
// read the pricing location from here rather than keeping a second copy that
// could drift from it.
export const INDUSTRY_SCANNER_PARAMS_KEY = "eve-settings:industry-scanner";

export interface ScannerPricingLocation {
  pricingSystem: string;
  pricingStationID: number;
}

/** The scanner's pricing system and station, defaulting to Jita 4-4. */
export function loadScannerPricingLocation(): ScannerPricingLocation {
  const fallback: ScannerPricingLocation = { pricingSystem: "Jita", pricingStationID: 60003760 };
  try {
    const raw = localStorage.getItem(INDUSTRY_SCANNER_PARAMS_KEY);
    if (!raw) return fallback;
    const parsed = JSON.parse(raw) as Partial<ScannerPricingLocation>;
    return {
      pricingSystem: typeof parsed.pricingSystem === "string" && parsed.pricingSystem.trim() ? parsed.pricingSystem : fallback.pricingSystem,
      pricingStationID: typeof parsed.pricingStationID === "number" ? parsed.pricingStationID : fallback.pricingStationID,
    };
  } catch {
    return fallback;
  }
}
