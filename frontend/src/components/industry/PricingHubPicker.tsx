import { SystemAutocomplete } from "../SystemAutocomplete";
import { useI18n } from "@/lib/i18n";

// Quick-pick presets for the major EVE trade hubs. Users can type any
// system into the autocomplete instead — these one-click the canonical
// NPC station + system so pricing lookups land at the actual trade hub
// (not the region-wide average) with a single click.
export interface PricingHubPreset {
  key: string;
  shortLabel: string;
  systemName: string;
  stationID: number;
}

export const PRICING_HUB_PRESETS: PricingHubPreset[] = [
  { key: "jita", shortLabel: "Jita", systemName: "Jita", stationID: 60003760 },
  { key: "amarr", shortLabel: "Amarr", systemName: "Amarr", stationID: 60008494 },
  { key: "dodixie", shortLabel: "Dodixie", systemName: "Dodixie", stationID: 60011866 },
  { key: "rens", shortLabel: "Rens", systemName: "Rens", stationID: 60004588 },
  { key: "hek", shortLabel: "Hek", systemName: "Hek", stationID: 60005686 },
];

interface Props {
  systemName: string;
  stationID: number;
  onChange: (systemName: string, stationID: number) => void;
  isLoggedIn: boolean;
  /** Compact renders the two controls side-by-side without labels; the
   *  parent supplies its own SettingsField wrapper. Default is the
   *  labelled two-row layout the Scanner uses. */
  compact?: boolean;
}

// Shared "Sell system + quick-hub chips" picker used by both the Scanner
// (pricing decoupled from build system) and the Analyze form (optional
// sell-side override). Typing a hub name into the autocomplete auto-pins
// its canonical station; clicking a chip does the same in one tap.
export function PricingHubPicker({ systemName, stationID: _stationID, onChange, isLoggedIn, compact }: Props) {
  const { t } = useI18n();
  const currentLower = systemName.trim().toLowerCase();
  void _stationID; // reserved for future "current station name" pill display

  const onSystemChange = (v: string) => {
    const trimmed = v.trim();
    const preset = PRICING_HUB_PRESETS.find((h) => h.systemName.toLowerCase() === trimmed.toLowerCase());
    onChange(v, preset ? preset.stationID : 0);
  };

  const hubChips = (
    <div className="flex flex-wrap gap-1">
      {PRICING_HUB_PRESETS.map((hub) => {
        const active = currentLower === hub.systemName.toLowerCase();
        return (
          <button
            key={hub.key}
            type="button"
            onClick={() => onChange(hub.systemName, hub.stationID)}
            className={`px-2 py-1 text-[11px] rounded-sm border transition-colors ${
              active
                ? "border-eve-accent text-eve-accent bg-eve-accent/10"
                : "border-eve-border text-eve-dim hover:text-eve-text hover:border-eve-border/80"
            }`}
          >
            {hub.shortLabel}
          </button>
        );
      })}
    </div>
  );

  if (compact) {
    return (
      <div className="flex flex-col gap-1 w-72">
        <SystemAutocomplete
          value={systemName}
          onChange={onSystemChange}
          showLocationButton={false}
          isLoggedIn={isLoggedIn}
          suppressInternalHint
        />
        {hubChips}
      </div>
    );
  }

  return (
    <div className="flex flex-wrap gap-3">
      <div className="flex flex-col gap-1 w-fit min-w-44">
        <label className="text-[11px] uppercase tracking-wider text-eve-dim font-medium">
          {t("industryScannerPricingSystemLabel")}
        </label>
        <div className="w-72">
          <SystemAutocomplete
            value={systemName}
            onChange={onSystemChange}
            showLocationButton={false}
            isLoggedIn={isLoggedIn}
            suppressInternalHint
          />
        </div>
      </div>
      <div className="flex flex-col gap-1 w-fit min-w-44">
        <label className="text-[11px] uppercase tracking-wider text-eve-dim font-medium">
          {t("industryScannerPricingHubsLabel")}
        </label>
        {hubChips}
      </div>
    </div>
  );
}
