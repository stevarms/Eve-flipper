// Shared canonical NPC trade-hub stations used by Station Trading and
// Price Audit quick-select rows. Keep in sync with the industry scanner's
// pricing-hub presets in IndustryProfitableScannerPanel.tsx.

export interface TradeHub {
  key: string;
  shortLabel: string;
  systemName: string;
  stationID: number;
}

export const STATION_TRADING_HUBS: TradeHub[] = [
  { key: "jita", shortLabel: "Jita", systemName: "Jita", stationID: 60003760 },
  { key: "amarr", shortLabel: "Amarr", systemName: "Amarr", stationID: 60008494 },
  { key: "dodixie", shortLabel: "Dodixie", systemName: "Dodixie", stationID: 60011866 },
  { key: "rens", shortLabel: "Rens", systemName: "Rens", stationID: 60004588 },
  { key: "hek", shortLabel: "Hek", systemName: "Hek", stationID: 60005686 },
];
