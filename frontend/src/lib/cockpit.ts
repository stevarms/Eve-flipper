import type { ScanParams } from "./types";
import type { TranslationKey } from "./i18n";
import type { CustomPalette, FontSize, ThemeMode } from "./useTheme";

export const COCKPIT_STORAGE_KEY = "eve-flipper-cockpit:v1";

export const MAIN_TAB_IDS = [
  "radius",
  "region",
  "contracts",
  "route",
  "station",
  "price_audit",
  "industry",
  "demand",
] as const;

export type MainTabId = (typeof MAIN_TAB_IDS)[number];
export type CockpitDensity = "comfortable" | "compact" | "dense";
export type CockpitDensitySetting = CockpitDensity | "inherit";
export type CockpitStartupTab = MainTabId | "last";
export type CockpitColumnPreset = "auto" | "default" | "compact" | "trader" | "hauling" | "accounting";
export type CockpitFilterPreset = "manual" | "jita" | "low_capital" | "hauling" | "industry";
export type CockpitContextTask = "any" | "station" | "regional" | "route" | "industry" | "ledger" | "mission";
export type CockpitQuickAction =
  | "watchlist"
  | "history"
  | "itemIntel"
  | "missionControl"
  | "ledger"
  | "journal"
  | "dotlan"
  | "commandPalette"
  | "shortcuts";
export type CockpitProfilePresetID =
  | "station_trader"
  | "regional_hauler"
  | "industry_builder"
  | "ledger_accountant"
  | "new_player"
  | "power_user";

export interface CockpitTabLayout {
  density: CockpitDensitySetting;
  columnPreset: CockpitColumnPreset;
  filterPreset: CockpitFilterPreset;
  hiddenPanels: string[];
  columnState: Record<string, CockpitColumnState>;
}

export interface CockpitColumnState {
  order?: number;
  visible?: boolean;
  widthPx?: number;
  pinned?: boolean;
  frozen?: boolean;
}

export interface CockpitRoleContextRule {
  id: string;
  label: string;
  task: CockpitContextTask;
  routeMode: "any" | "fastest" | "safest" | "balanced" | "max_isk_hour";
  loadoutId: string;
  presetId: CockpitProfilePresetID | "";
  priority: number;
}

export interface CockpitPreferences {
  version: 1;
  name: string;
  density: CockpitDensity;
  startupTab: CockpitStartupTab;
  layoutLocked: boolean;
  adaptiveEnabled: boolean;
  contextHintsEnabled: boolean;
  tradingEdgeEnabled: boolean;
  dismissedAdaptiveSuggestions: string[];
  favoriteTemplates: string[];
  roleBindings: Record<string, CockpitRoleBinding>;
  mainTabOrder: MainTabId[];
  hiddenMainTabs: MainTabId[];
  quickActions: CockpitQuickAction[];
  tabLayouts: Record<MainTabId, CockpitTabLayout>;
  hiddenPanels: {
    advancedFilters: boolean;
    stationAiAssistant: boolean;
    helpButtons: boolean;
    quickActions: boolean;
    statusBar: boolean;
    tabActionBars: boolean;
  };
}

export interface CockpitRoleBinding {
  characterId: string;
  label: string;
  presetId: CockpitProfilePresetID | "";
  loadoutId: string;
  contextRules: CockpitRoleContextRule[];
}

export interface CockpitLoadout {
  id: string;
  name: string;
  preferences: CockpitPreferences;
  active: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface WorkspaceThemeSnapshot {
  mode: ThemeMode;
  palette: string;
  fontSize: FontSize;
  customPalettes?: CustomPalette[];
}

export interface WorkspaceSnapshot {
  kind: "eve-flipper-workspace";
  version: 1;
  exportedAt: string;
  app: "eve-flipper";
  cockpit: CockpitPreferences;
  scanParams: ScanParams;
  theme?: WorkspaceThemeSnapshot;
  privacy: {
    includesPrivateFields: false;
    excluded: string[];
  };
}

export interface WorkspaceLoadoutEntry {
  name: string;
  cockpit: CockpitPreferences;
  active?: boolean;
  sourceId?: string;
}

export interface WorkspaceLoadoutPack {
  kind: "eve-flipper-workspace-pack";
  version: 1;
  exportedAt: string;
  app: "eve-flipper";
  loadouts: WorkspaceLoadoutEntry[];
  scanParams?: ScanParams;
  theme?: WorkspaceThemeSnapshot;
  privacy: {
    includesPrivateFields: false;
    excluded: string[];
  };
}

export interface WorkspaceImportResult {
  kind: "single" | "pack";
  exportedAt: string;
  loadouts: WorkspaceLoadoutEntry[];
  scanParams?: ScanParams;
  theme?: WorkspaceThemeSnapshot;
  warnings: string[];
}

export interface CockpitProfilePreset {
  id: CockpitProfilePresetID;
  name: string;
  description: string;
  cockpit: CockpitPreferences;
  scanParams: Partial<ScanParams>;
}

export interface CockpitActivityStats {
  counters: Record<string, number>;
  transitions: Record<string, number>;
  samples: CockpitActivitySample[];
  lastEvent?: string;
  updatedAt?: string;
}

export interface CockpitActivitySample {
  event: string;
  previous?: string;
  at: string;
}

export interface CockpitBehaviorModel {
  dominantIntent: CockpitContextTask;
  confidence: number;
  intentScores: Record<CockpitContextTask, number>;
  nextActions: Array<{ action: string; score: number }>;
  recommendedPresetId: CockpitProfilePresetID | "";
}

export interface CockpitAdaptiveSuggestion {
  id: string;
  title: string;
  description: string;
  actionLabel: string;
  page: "presets" | "startup" | "navigation" | "panels" | "templates" | "context" | "roles";
  priority: number;
}

export interface CockpitWorkspaceTemplate {
  id: string;
  name: string;
  author: string;
  description: string;
  tags: string[];
  rating: number;
  presetId: CockpitProfilePresetID;
  shareable: boolean;
}

export interface CockpitMarketplaceEntry {
  id: string;
  name: string;
  author: string;
  description: string;
  tags: string[];
  rating: number;
  downloads: number;
  verified: boolean;
  featured: boolean;
  updatedAt: string;
  layoutUrl: string;
  sourceUrl?: string;
  minAppVersion?: string;
}

export interface CockpitMarketplaceLayout extends WorkspaceImportResult {
  sourceEntry: CockpitMarketplaceEntry;
}

export interface CockpitMarketplaceCatalog {
  kind: "eve-flipper-cockpit-marketplace";
  version: 1;
  updatedAt: string;
  entries: CockpitMarketplaceEntry[];
}

const WORKSPACE_PRIVACY_EXCLUDED = [
  "ESI tokens",
  "session cookies",
  "wallet history",
  "orders/assets",
  "journal trades",
  "private local database",
  "webhook keys",
];

export const COCKPIT_SHARE_PREFIX = "EFC1.";
export const COCKPIT_ACTIVITY_KEY = "eve-flipper-cockpit-activity:v1";
export const COCKPIT_MARKETPLACE_URL =
  "https://raw.githubusercontent.com/ilyaux/eve-flipper-cockpit-marketplace/main/marketplace.json";

export const MAIN_TAB_META: Record<MainTabId, { labelKey: TranslationKey; fallback: string; group: "scan" | "tools" }> = {
  radius: { labelKey: "tabRadius", fallback: "Flipper", group: "scan" },
  region: { labelKey: "tabRegion", fallback: "Regional Trade", group: "scan" },
  contracts: { labelKey: "tabContracts", fallback: "Contracts", group: "scan" },
  route: { labelKey: "tabRoute", fallback: "Route", group: "scan" },
  station: { labelKey: "tabStation", fallback: "Station Trading", group: "tools" },
  price_audit: { labelKey: "tabPriceAudit", fallback: "Price Audit", group: "tools" },
  industry: { labelKey: "tabIndustry", fallback: "Industry", group: "tools" },
  demand: { labelKey: "tabDemand", fallback: "War", group: "tools" },
};

export const COCKPIT_QUICK_ACTIONS: CockpitQuickAction[] = [
  "watchlist",
  "history",
  "itemIntel",
  "missionControl",
  "ledger",
  "journal",
  "dotlan",
  "commandPalette",
  "shortcuts",
];

export const COCKPIT_COLUMN_PRESETS: CockpitColumnPreset[] = [
  "auto",
  "default",
  "compact",
  "trader",
  "hauling",
  "accounting",
];

export const COCKPIT_FILTER_PRESETS: CockpitFilterPreset[] = [
  "manual",
  "jita",
  "low_capital",
  "hauling",
  "industry",
];

export const COCKPIT_PROFILE_PRESET_IDS: CockpitProfilePresetID[] = [
  "station_trader",
  "regional_hauler",
  "industry_builder",
  "ledger_accountant",
  "new_player",
  "power_user",
];

function defaultTabLayout(): CockpitTabLayout {
  return {
    density: "inherit",
    columnPreset: "auto",
    filterPreset: "manual",
    hiddenPanels: [],
    columnState: {},
  };
}

function defaultTabLayouts(): Record<MainTabId, CockpitTabLayout> {
  return MAIN_TAB_IDS.reduce((acc, tab) => {
    acc[tab] = defaultTabLayout();
    return acc;
  }, {} as Record<MainTabId, CockpitTabLayout>);
}

export const defaultCockpitPreferences: CockpitPreferences = {
  version: 1,
  name: "Default cockpit",
  density: "comfortable",
  startupTab: "last",
  layoutLocked: false,
  adaptiveEnabled: true,
  contextHintsEnabled: true,
  tradingEdgeEnabled: true,
  dismissedAdaptiveSuggestions: [],
  favoriteTemplates: [],
  roleBindings: {},
  mainTabOrder: [...MAIN_TAB_IDS],
  hiddenMainTabs: [],
  quickActions: ["watchlist", "history", "itemIntel", "journal"],
  tabLayouts: defaultTabLayouts(),
  hiddenPanels: {
    advancedFilters: false,
    stationAiAssistant: false,
    helpButtons: false,
    quickActions: false,
    statusBar: false,
    tabActionBars: false,
  },
};

function cockpitWithTabLayouts(
  base: Partial<CockpitPreferences>,
  layouts: Partial<Record<MainTabId, Partial<CockpitTabLayout>>> = {},
): CockpitPreferences {
  const tabLayouts = defaultTabLayouts();
  for (const tab of MAIN_TAB_IDS) {
    if (layouts[tab]) {
      tabLayouts[tab] = { ...tabLayouts[tab], ...layouts[tab] };
    }
  }
  return sanitizeCockpitPreferences({
    ...defaultCockpitPreferences,
    ...base,
    tabLayouts,
    hiddenPanels: {
      ...defaultCockpitPreferences.hiddenPanels,
      ...base.hiddenPanels,
    },
  });
}

export const COCKPIT_PROFILE_PRESETS: CockpitProfilePreset[] = [
  {
    id: "station_trader",
    name: "Station Trader",
    description: "Dense Jita station workflow with trader columns, Item Intel, history and Mission Control close at hand.",
    cockpit: cockpitWithTabLayouts(
      {
        name: "Station Trader",
        density: "dense",
        startupTab: "station",
        mainTabOrder: ["station", "radius", "region", "route", "contracts", "industry", "demand"],
        hiddenMainTabs: ["demand"],
        quickActions: ["watchlist", "history", "itemIntel", "missionControl", "ledger", "journal", "commandPalette"],
      },
      {
        station: { density: "dense", columnPreset: "trader", filterPreset: "jita" },
        radius: { density: "dense", columnPreset: "trader", filterPreset: "jita" },
      },
    ),
    scanParams: {
      system_name: "Jita",
      target_market_system: "Jita",
      min_margin: 2,
      min_daily_volume: 20,
      max_dos: 30,
    },
  },
  {
    id: "regional_hauler",
    name: "Regional Hauler",
    description: "Import/export desk for route-aware hauling, cargo limits, sell-order revenue and DOTLAN style checks.",
    cockpit: cockpitWithTabLayouts(
      {
        name: "Regional Hauler",
        density: "compact",
        startupTab: "region",
        mainTabOrder: ["region", "route", "radius", "station", "contracts", "industry", "demand"],
        hiddenMainTabs: ["demand"],
        quickActions: ["watchlist", "history", "itemIntel", "missionControl", "ledger", "journal", "dotlan", "commandPalette"],
      },
      {
        region: { density: "dense", columnPreset: "hauling", filterPreset: "hauling" },
        route: { density: "dense", columnPreset: "hauling", filterPreset: "hauling" },
      },
    ),
    scanParams: {
      sell_order_mode: true,
      cargo_capacity: 50_000,
      route_cargo_capacity: 50_000,
      route_mode: "balanced",
      min_margin: 10,
      route_min_isk_per_jump: 1_000_000,
      shipping_cost_per_m3_jump: 0,
    },
  },
  {
    id: "industry_builder",
    name: "Industry Builder",
    description: "Industry-first profile for structure-aware builds, material checks and production planning.",
    cockpit: cockpitWithTabLayouts(
      {
        name: "Industry Builder",
        density: "compact",
        startupTab: "industry",
        mainTabOrder: ["industry", "station", "radius", "region", "route", "contracts", "demand"],
        hiddenMainTabs: [],
        quickActions: ["history", "itemIntel", "missionControl", "ledger", "journal", "commandPalette"],
      },
      {
        industry: { density: "compact", columnPreset: "trader", filterPreset: "industry" },
        station: { density: "compact", columnPreset: "trader", filterPreset: "industry" },
      },
    ),
    scanParams: {
      include_structures: true,
      min_margin: 8,
      min_daily_volume: 3,
      avg_price_period: 30,
      max_dos: 45,
    },
  },
  {
    id: "ledger_accountant",
    name: "Ledger / Accountant",
    description: "Quiet accounting profile: wider spacing, ledger commands, tax profile controls and history-first navigation.",
    cockpit: cockpitWithTabLayouts(
      {
        name: "Ledger / Accountant",
        density: "comfortable",
        startupTab: "last",
        mainTabOrder: ["station", "radius", "region", "contracts", "industry", "route", "demand"],
        hiddenMainTabs: ["demand"],
        quickActions: ["history", "itemIntel", "ledger", "journal", "commandPalette"],
      },
      {
        station: { density: "comfortable", columnPreset: "accounting", filterPreset: "manual" },
        radius: { density: "comfortable", columnPreset: "accounting", filterPreset: "manual" },
        region: { density: "comfortable", columnPreset: "accounting", filterPreset: "manual" },
      },
    ),
    scanParams: {
      split_trade_fees: true,
      avg_price_period: 30,
      require_history: true,
    },
  },
  {
    id: "new_player",
    name: "New Player",
    description: "Safer low-capital defaults with fewer main tabs, looser spacing and conservative exposure.",
    cockpit: cockpitWithTabLayouts(
      {
        name: "New Player",
        density: "comfortable",
        startupTab: "radius",
        mainTabOrder: ["radius", "station", "region", "route", "contracts", "industry", "demand"],
        hiddenMainTabs: ["contracts", "industry", "demand"],
        quickActions: ["watchlist", "history", "itemIntel", "journal", "ledger", "shortcuts"],
      },
      {
        radius: { density: "comfortable", columnPreset: "compact", filterPreset: "low_capital" },
        station: { density: "comfortable", columnPreset: "compact", filterPreset: "low_capital" },
      },
    ),
    scanParams: {
      max_investment: 1_000_000_000,
      min_item_profit: 500_000,
      min_margin: 8,
      min_daily_volume: 5,
      cargo_capacity: 10_000,
    },
  },
  {
    id: "power_user",
    name: "Power User",
    description: "Everything visible, dense tables, command palette enabled and current filters preserved unless changed manually.",
    cockpit: cockpitWithTabLayouts(
      {
        name: "Power User",
        density: "dense",
        startupTab: "last",
        mainTabOrder: ["radius", "region", "station", "route", "industry", "contracts", "demand"],
        hiddenMainTabs: [],
        quickActions: ["watchlist", "history", "itemIntel", "missionControl", "ledger", "journal", "dotlan", "commandPalette", "shortcuts"],
      },
      {
        radius: { density: "dense", columnPreset: "trader", filterPreset: "manual" },
        region: { density: "dense", columnPreset: "hauling", filterPreset: "manual" },
        station: { density: "dense", columnPreset: "trader", filterPreset: "manual" },
        route: { density: "dense", columnPreset: "hauling", filterPreset: "manual" },
        industry: { density: "dense", columnPreset: "trader", filterPreset: "manual" },
      },
    ),
    scanParams: {},
  },
];

export const COCKPIT_WORKSPACE_TEMPLATES: CockpitWorkspaceTemplate[] = [
  {
    id: "template-jita-scalper",
    name: "Jita Scalper Setup",
    author: "Eve Flipper",
    description: "Dense station trading cockpit for quick margin checks, ledger review and item intelligence.",
    tags: ["station", "jita", "fast"],
    rating: 4.8,
    presetId: "station_trader",
    shareable: true,
  },
  {
    id: "template-null-import",
    name: "Nullsec Import Setup",
    author: "Eve Flipper",
    description: "Regional hauler workspace tuned for destination sell orders, cargo constraints and route safety.",
    tags: ["regional", "hauling", "nullsec"],
    rating: 4.7,
    presetId: "regional_hauler",
    shareable: true,
  },
  {
    id: "template-reaction-builder",
    name: "Industry Reaction Setup",
    author: "Eve Flipper",
    description: "Industry-first layout for build/buy, structure visibility and material planning.",
    tags: ["industry", "reaction", "builder"],
    rating: 4.6,
    presetId: "industry_builder",
    shareable: true,
  },
  {
    id: "template-ledger-auditor",
    name: "Ledger Auditor Setup",
    author: "Eve Flipper",
    description: "Accounting cockpit for capital curve, archive coverage, journal categories and tax profile checks.",
    tags: ["ledger", "accounting", "audit"],
    rating: 4.5,
    presetId: "ledger_accountant",
    shareable: true,
  },
];

export function isMainTabId(value: unknown): value is MainTabId {
  return typeof value === "string" && (MAIN_TAB_IDS as readonly string[]).includes(value);
}

function uniqueKnownTabs(values: unknown): MainTabId[] {
  if (!Array.isArray(values)) return [];
  const result: MainTabId[] = [];
  for (const value of values) {
    if (isMainTabId(value) && !result.includes(value)) result.push(value);
  }
  return result;
}

function isCockpitDensity(value: unknown): value is CockpitDensity {
  return value === "comfortable" || value === "compact" || value === "dense";
}

function sanitizeDensity(value: unknown): CockpitDensity {
  return isCockpitDensity(value) ? value : "comfortable";
}

function sanitizeDensitySetting(value: unknown): CockpitDensitySetting {
  return value === "inherit" || isCockpitDensity(value) ? value : "inherit";
}

function sanitizeStartupTab(value: unknown): CockpitStartupTab {
  return value === "last" || isMainTabId(value) ? value : "last";
}

function sanitizeColumnPreset(value: unknown): CockpitColumnPreset {
  return typeof value === "string" && COCKPIT_COLUMN_PRESETS.includes(value as CockpitColumnPreset)
    ? value as CockpitColumnPreset
    : "auto";
}

function sanitizeFilterPreset(value: unknown): CockpitFilterPreset {
  return typeof value === "string" && COCKPIT_FILTER_PRESETS.includes(value as CockpitFilterPreset)
    ? value as CockpitFilterPreset
    : "manual";
}

function sanitizeQuickActions(value: unknown): CockpitQuickAction[] {
  if (!Array.isArray(value)) return [...defaultCockpitPreferences.quickActions];
  const result: CockpitQuickAction[] = [];
  for (const item of value) {
    if (
      typeof item === "string" &&
      COCKPIT_QUICK_ACTIONS.includes(item as CockpitQuickAction) &&
      !result.includes(item as CockpitQuickAction)
    ) {
      result.push(item as CockpitQuickAction);
    }
  }
  return result;
}

function sanitizeHiddenPanelList(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  const result: string[] = [];
  for (const item of value) {
    if (typeof item !== "string") continue;
    const clean = item.trim().slice(0, 40);
    if (clean && !result.includes(clean)) result.push(clean);
  }
  return result.slice(0, 40);
}

function sanitizeColumnState(value: unknown): Record<string, CockpitColumnState> {
  if (!value || typeof value !== "object" || Array.isArray(value)) return {};
  const out: Record<string, CockpitColumnState> = {};
  for (const [rawKey, rawValue] of Object.entries(value as Record<string, unknown>).slice(0, 160)) {
    const key = rawKey.trim().slice(0, 80);
    if (!key) continue;
    const rec = rawValue && typeof rawValue === "object" && !Array.isArray(rawValue)
      ? rawValue as Record<string, unknown>
      : {};
    const order = typeof rec.order === "number" && Number.isFinite(rec.order)
      ? Math.max(0, Math.min(1000, Math.round(rec.order)))
      : undefined;
    const widthPx = typeof rec.widthPx === "number" && Number.isFinite(rec.widthPx)
      ? Math.max(44, Math.min(520, Math.round(rec.widthPx)))
      : undefined;
    out[key] = {
      ...(order !== undefined ? { order } : {}),
      ...(typeof rec.visible === "boolean" ? { visible: rec.visible } : {}),
      ...(widthPx !== undefined ? { widthPx } : {}),
      ...(typeof rec.pinned === "boolean" ? { pinned: rec.pinned } : {}),
      ...(typeof rec.frozen === "boolean" ? { frozen: rec.frozen } : {}),
    };
  }
  return out;
}

function sanitizeStringList(value: unknown, maxItems = 80, maxLength = 80): string[] {
  if (!Array.isArray(value)) return [];
  const result: string[] = [];
  for (const item of value) {
    if (typeof item !== "string") continue;
    const clean = item.trim().slice(0, maxLength);
    if (clean && !result.includes(clean)) result.push(clean);
    if (result.length >= maxItems) break;
  }
  return result;
}

function sanitizeContextTask(value: unknown): CockpitContextTask {
  return value === "station" ||
    value === "regional" ||
    value === "route" ||
    value === "industry" ||
    value === "ledger" ||
    value === "mission" ||
    value === "any"
    ? value
    : "any";
}

function sanitizeRouteMode(value: unknown): CockpitRoleContextRule["routeMode"] {
  return value === "fastest" ||
    value === "safest" ||
    value === "balanced" ||
    value === "max_isk_hour" ||
    value === "any"
    ? value
    : "any";
}

function sanitizePresetID(value: unknown): CockpitProfilePresetID | "" {
  if (typeof value !== "string") return "";
  return COCKPIT_PROFILE_PRESET_IDS.includes(value as CockpitProfilePresetID)
    ? value as CockpitProfilePresetID
    : "";
}

function sanitizeContextRules(value: unknown): CockpitRoleContextRule[] {
  if (!Array.isArray(value)) return [];
  return value.slice(0, 30).map((item, index) => {
    const rec = item && typeof item === "object" && !Array.isArray(item)
      ? item as Record<string, unknown>
      : {};
    const task = sanitizeContextTask(rec.task);
    const loadoutId = typeof rec.loadoutId === "string" ? rec.loadoutId.trim().slice(0, 80) : "";
    const presetId = sanitizePresetID(rec.presetId);
    return {
      id: typeof rec.id === "string" && rec.id.trim()
        ? rec.id.trim().slice(0, 80)
        : `context-${task}-${index}`,
      label: typeof rec.label === "string" && rec.label.trim()
        ? rec.label.trim().slice(0, 100)
        : `${task} cockpit`,
      task,
      routeMode: sanitizeRouteMode(rec.routeMode),
      loadoutId,
      presetId,
      priority: typeof rec.priority === "number" && Number.isFinite(rec.priority)
        ? Math.max(0, Math.min(100, Math.round(rec.priority)))
        : 50,
    };
  }).filter((rule) => rule.loadoutId || rule.presetId);
}

function sanitizeRoleBindings(value: unknown): Record<string, CockpitRoleBinding> {
  if (!value || typeof value !== "object" || Array.isArray(value)) return {};
  const out: Record<string, CockpitRoleBinding> = {};
  for (const [rawKey, rawValue] of Object.entries(value as Record<string, unknown>).slice(0, 40)) {
    const rec = rawValue && typeof rawValue === "object" && !Array.isArray(rawValue)
      ? rawValue as Record<string, unknown>
      : {};
    const characterId = (typeof rec.characterId === "string" ? rec.characterId : rawKey).trim().slice(0, 32);
    if (!characterId) continue;
    out[characterId] = {
      characterId,
      label: typeof rec.label === "string" ? rec.label.trim().slice(0, 80) : "",
      presetId: sanitizePresetID(rec.presetId),
      loadoutId: typeof rec.loadoutId === "string" ? rec.loadoutId.trim().slice(0, 80) : "",
      contextRules: sanitizeContextRules(rec.contextRules),
    };
  }
  return out;
}

function sanitizeTabLayout(value: unknown): CockpitTabLayout {
  const rec = value && typeof value === "object" ? value as Record<string, unknown> : {};
  return {
    density: sanitizeDensitySetting(rec.density),
    columnPreset: sanitizeColumnPreset(rec.columnPreset),
    filterPreset: sanitizeFilterPreset(rec.filterPreset),
    hiddenPanels: sanitizeHiddenPanelList(rec.hiddenPanels),
    columnState: sanitizeColumnState(rec.columnState),
  };
}

function sanitizeTabLayouts(value: unknown): Record<MainTabId, CockpitTabLayout> {
  const rec = value && typeof value === "object" ? value as Record<string, unknown> : {};
  return MAIN_TAB_IDS.reduce((acc, tab) => {
    acc[tab] = sanitizeTabLayout(rec[tab]);
    return acc;
  }, {} as Record<MainTabId, CockpitTabLayout>);
}

export function sanitizeCockpitPreferences(value: unknown): CockpitPreferences {
  const rec = value && typeof value === "object" ? value as Record<string, unknown> : {};
  const hidden = uniqueKnownTabs(rec.hiddenMainTabs);
  const order = uniqueKnownTabs(rec.mainTabOrder);
  const fullOrder = [...order, ...MAIN_TAB_IDS.filter((tab) => !order.includes(tab))];
  const allHidden = MAIN_TAB_IDS.every((tab) => hidden.includes(tab));
  const panels = rec.hiddenPanels && typeof rec.hiddenPanels === "object"
    ? rec.hiddenPanels as Record<string, unknown>
    : {};

  return {
    version: 1,
    name: typeof rec.name === "string" && rec.name.trim() ? rec.name.trim() : defaultCockpitPreferences.name,
    density: sanitizeDensity(rec.density),
    startupTab: sanitizeStartupTab(rec.startupTab),
    layoutLocked: Boolean(rec.layoutLocked),
    adaptiveEnabled: rec.adaptiveEnabled !== false,
    contextHintsEnabled: rec.contextHintsEnabled !== false,
    tradingEdgeEnabled: rec.tradingEdgeEnabled !== false,
    dismissedAdaptiveSuggestions: sanitizeStringList(rec.dismissedAdaptiveSuggestions, 100, 100),
    favoriteTemplates: sanitizeStringList(rec.favoriteTemplates, 100, 80),
    roleBindings: sanitizeRoleBindings(rec.roleBindings),
    mainTabOrder: fullOrder,
    hiddenMainTabs: allHidden ? [] : hidden,
    quickActions: sanitizeQuickActions(rec.quickActions),
    tabLayouts: sanitizeTabLayouts(rec.tabLayouts),
    hiddenPanels: {
      advancedFilters: Boolean(panels.advancedFilters),
      stationAiAssistant: Boolean(panels.stationAiAssistant),
      helpButtons: Boolean(panels.helpButtons),
      quickActions: Boolean(panels.quickActions),
      statusBar: Boolean(panels.statusBar),
      tabActionBars: Boolean(panels.tabActionBars),
    },
  };
}

export function loadCockpitPreferences(): CockpitPreferences {
  try {
    const raw = localStorage.getItem(COCKPIT_STORAGE_KEY);
    return raw ? sanitizeCockpitPreferences(JSON.parse(raw)) : defaultCockpitPreferences;
  } catch {
    return defaultCockpitPreferences;
  }
}

export function saveCockpitPreferences(preferences: CockpitPreferences): void {
  try {
    localStorage.setItem(COCKPIT_STORAGE_KEY, JSON.stringify(sanitizeCockpitPreferences(preferences)));
  } catch {
    // Ignore localStorage quota/access failures.
  }
}

export function getVisibleMainTabs(preferences: CockpitPreferences): MainTabId[] {
  const prefs = sanitizeCockpitPreferences(preferences);
  const tabs = prefs.mainTabOrder.filter((tab) => !prefs.hiddenMainTabs.includes(tab));
  return tabs.length > 0 ? tabs : [...MAIN_TAB_IDS];
}

export function getCockpitTabLayout(preferences: CockpitPreferences, tab: MainTabId): CockpitTabLayout {
  const prefs = sanitizeCockpitPreferences(preferences);
  return prefs.tabLayouts[tab] ?? defaultTabLayout();
}

export function getEffectiveCockpitDensity(preferences: CockpitPreferences, tab: MainTabId): CockpitDensity {
  const prefs = sanitizeCockpitPreferences(preferences);
  const layout = prefs.tabLayouts[tab];
  return layout && layout.density !== "inherit" ? layout.density : prefs.density;
}

export function isCockpitQuickActionVisible(preferences: CockpitPreferences, action: CockpitQuickAction): boolean {
  const prefs = sanitizeCockpitPreferences(preferences);
  return !prefs.hiddenPanels.quickActions && prefs.quickActions.includes(action);
}

export function loadCockpitActivityStats(): CockpitActivityStats {
  try {
    const raw = localStorage.getItem(COCKPIT_ACTIVITY_KEY);
    const parsed = raw ? JSON.parse(raw) as Partial<CockpitActivityStats> : {};
    return {
      counters: parsed.counters && typeof parsed.counters === "object" ? parsed.counters : {},
      transitions: parsed.transitions && typeof parsed.transitions === "object" ? parsed.transitions : {},
      samples: Array.isArray(parsed.samples)
        ? parsed.samples
            .filter((sample): sample is CockpitActivitySample =>
              Boolean(sample) &&
              typeof sample === "object" &&
              typeof (sample as CockpitActivitySample).event === "string" &&
              typeof (sample as CockpitActivitySample).at === "string",
            )
            .slice(-500)
        : [],
      lastEvent: typeof parsed.lastEvent === "string" ? parsed.lastEvent : undefined,
      updatedAt: typeof parsed.updatedAt === "string" ? parsed.updatedAt : undefined,
    };
  } catch {
    return { counters: {}, transitions: {}, samples: [] };
  }
}

export function trackCockpitActivity(event: string): CockpitActivityStats {
  const cleanEvent = event.trim().slice(0, 80);
  const stats = loadCockpitActivityStats();
  if (!cleanEvent) return stats;
  const previous = stats.lastEvent;
  stats.counters[cleanEvent] = (stats.counters[cleanEvent] ?? 0) + 1;
  if (previous && previous !== cleanEvent) {
    const transition = `${previous}->${cleanEvent}`;
    stats.transitions[transition] = (stats.transitions[transition] ?? 0) + 1;
  }
  stats.samples = [
    ...(stats.samples ?? []),
    { event: cleanEvent, previous, at: new Date().toISOString() },
  ].slice(-500);
  stats.lastEvent = cleanEvent;
  stats.updatedAt = new Date().toISOString();
  try {
    localStorage.setItem(COCKPIT_ACTIVITY_KEY, JSON.stringify(stats));
  } catch {
    // Ignore localStorage quota/access failures.
  }
  return stats;
}

export function resetCockpitActivityStats(): CockpitActivityStats {
  const empty: CockpitActivityStats = { counters: {}, transitions: {}, samples: [], updatedAt: new Date().toISOString() };
  try {
    localStorage.setItem(COCKPIT_ACTIVITY_KEY, JSON.stringify(empty));
  } catch {
    // Ignore localStorage quota/access failures.
  }
  return empty;
}

function classifyCockpitIntent(event: string): CockpitContextTask {
  if (event.includes("station") || event.includes("scan")) return "station";
  if (event.includes("region") || event.includes("dotlan")) return "regional";
  if (event.includes("route")) return "route";
  if (event.includes("industry")) return "industry";
  if (event.includes("ledger") || event.includes("journal")) return "ledger";
  if (event.includes("missionControl") || event.includes("itemIntel")) return "mission";
  return "any";
}

export function buildCockpitBehaviorModel(stats: CockpitActivityStats = loadCockpitActivityStats()): CockpitBehaviorModel {
  const scores: Record<CockpitContextTask, number> = {
    any: 0,
    station: 0,
    regional: 0,
    route: 0,
    industry: 0,
    ledger: 0,
    mission: 0,
  };
  const samples = (stats.samples ?? []).slice(-250);
  const now = Date.now();
  for (const sample of samples) {
    const ageHours = Math.max(0, (now - Date.parse(sample.at)) / 3_600_000);
    const recencyWeight = Number.isFinite(ageHours) ? Math.max(0.2, 1 - ageHours / 168) : 0.5;
    const intent = classifyCockpitIntent(sample.event);
    scores[intent] += recencyWeight;
    if (sample.previous) {
      const previousIntent = classifyCockpitIntent(sample.previous);
      if (previousIntent !== intent) scores[intent] += recencyWeight * 0.25;
    }
  }
  for (const [event, count] of Object.entries(stats.counters)) {
    scores[classifyCockpitIntent(event)] += Math.min(count, 30) * 0.15;
  }
  const ranked = (Object.keys(scores) as CockpitContextTask[])
    .filter((key) => key !== "any")
    .sort((a, b) => scores[b] - scores[a]);
  const dominantIntent = ranked[0] ?? "any";
  const total = Object.values(scores).reduce((sum, value) => sum + value, 0);
  const confidence = total > 0 ? Math.min(100, Math.round((scores[dominantIntent] / total) * 100)) : 0;
  const nextActions = Object.entries(stats.transitions)
    .map(([transition, score]) => {
      const action = transition.split("->")[1] ?? transition;
      return { action, score };
    })
    .sort((a, b) => b.score - a.score)
    .slice(0, 6);
  const recommendedPresetId: CockpitProfilePresetID | "" =
    dominantIntent === "regional" || dominantIntent === "route" ? "regional_hauler" :
    dominantIntent === "industry" ? "industry_builder" :
    dominantIntent === "ledger" ? "ledger_accountant" :
    dominantIntent === "station" || dominantIntent === "mission" ? "station_trader" :
    "";
  return {
    dominantIntent,
    confidence,
    intentScores: scores,
    nextActions,
    recommendedPresetId,
  };
}

export function getCockpitAdaptiveSuggestions(
  preferences: CockpitPreferences,
  stats: CockpitActivityStats = loadCockpitActivityStats(),
): CockpitAdaptiveSuggestion[] {
  const prefs = sanitizeCockpitPreferences(preferences);
  if (!prefs.adaptiveEnabled) return [];
  const dismissed = new Set(prefs.dismissedAdaptiveSuggestions);
  const suggestions: CockpitAdaptiveSuggestion[] = [];
  const model = buildCockpitBehaviorModel(stats);
  const add = (suggestion: CockpitAdaptiveSuggestion) => {
    if (!dismissed.has(suggestion.id)) suggestions.push(suggestion);
  };
  const count = (event: string) => stats.counters[event] ?? 0;
  const transition = (from: string, to: string) => stats.transitions[`${from}->${to}`] ?? 0;

  if ((model.dominantIntent === "station" || count("tab:station") >= 3) && !prefs.quickActions.includes("commandPalette")) {
    add({
      id: "pin-command-for-station",
      title: "Station workflow is becoming command-heavy",
      description: "You open Station Trading often. Pin the command palette so Mission Control, Ledger and Item Intel are one keystroke away.",
      actionLabel: "Open quick actions",
      page: "startup",
      priority: 80,
    });
  }
  if ((transition("tab:station", "command:itemIntel") + transition("tab:station", "command:ledger")) >= 2 || model.recommendedPresetId === "station_trader") {
    add({
      id: "station-trader-preset-fit",
      title: "Station Trader preset matches your flow",
      description: "Your recent station flow jumps into intelligence/accounting. The Station Trader preset moves those tools closer.",
      actionLabel: "Review presets",
      page: "presets",
      priority: 75,
    });
  }
  if (model.recommendedPresetId === "regional_hauler" || count("tab:region") >= 3 || count("command:dotlan") >= 2) {
    add({
      id: "regional-hauler-template-fit",
      title: "Regional hauling workspace detected",
      description: "You are spending time in routes/regional checks. Try the Nullsec Import or Regional Hauler template.",
      actionLabel: "Open templates",
      page: "templates",
      priority: 70,
    });
  }
  if (model.recommendedPresetId === "ledger_accountant" || count("command:ledger") >= 3 || count("tab:ledger") >= 3) {
    add({
      id: "ledger-accountant-role",
      title: "Ledger deserves its own role",
      description: "You are opening ledger views often. Bind this character to a Ledger / Accountant cockpit if this is your bookkeeping alt.",
      actionLabel: "Open role bindings",
      page: "roles",
      priority: 65,
    });
  }
  if ((model.dominantIntent === "industry" || count("tab:industry") >= 2) && !prefs.contextHintsEnabled) {
    add({
      id: "enable-industry-context",
      title: "Enable context cockpit for industry",
      description: "Industry workflows benefit from item-aware build/buy and structure hints.",
      actionLabel: "Open context cockpit",
      page: "context",
      priority: 60,
    });
  }

  return suggestions.sort((a, b) => b.priority - a.priority).slice(0, 8);
}

export function buildWorkspaceSnapshot(
  cockpit: CockpitPreferences,
  scanParams: ScanParams,
  theme?: WorkspaceThemeSnapshot,
): WorkspaceSnapshot {
  return {
    kind: "eve-flipper-workspace",
    version: 1,
    exportedAt: new Date().toISOString(),
    app: "eve-flipper",
    cockpit: sanitizeCockpitPreferences(cockpit),
    scanParams,
    theme: sanitizeWorkspaceTheme(theme),
    privacy: {
      includesPrivateFields: false,
      excluded: WORKSPACE_PRIVACY_EXCLUDED,
    },
  };
}

export function buildWorkspacePack(
  loadouts: CockpitLoadout[],
  activeLoadoutID: string,
  fallbackCockpit: CockpitPreferences,
  scanParams: ScanParams,
  theme?: WorkspaceThemeSnapshot,
): WorkspaceLoadoutPack {
  const rows = loadouts.length > 0
    ? loadouts
    : [{ id: "default", name: fallbackCockpit.name, preferences: fallbackCockpit, active: true }];
  const resolvedActiveID = activeLoadoutID || rows.find((loadout) => loadout.active)?.id || rows[0]?.id || "default";
  const exportedAt = new Date().toISOString();
  return {
    kind: "eve-flipper-workspace-pack",
    version: 1,
    exportedAt,
    app: "eve-flipper",
    loadouts: rows.map((loadout, index) => {
      const cockpit = sanitizeCockpitPreferences(loadout.preferences);
      return {
        name: loadout.name || cockpit.name || `Loadout ${index + 1}`,
        cockpit,
        active: loadout.id === resolvedActiveID,
      };
    }),
    scanParams,
    theme: sanitizeWorkspaceTheme(theme),
    privacy: {
      includesPrivateFields: false,
      excluded: WORKSPACE_PRIVACY_EXCLUDED,
    },
  };
}

function sanitizeWorkspaceTheme(value: unknown): WorkspaceThemeSnapshot | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  const rec = value as Record<string, unknown>;
  const mode = rec.mode === "light" || rec.mode === "dark" || rec.mode === "auto" ? rec.mode : undefined;
  const fontSize = rec.fontSize === "xs" || rec.fontSize === "sm" || rec.fontSize === "md" || rec.fontSize === "lg" || rec.fontSize === "xl"
    ? rec.fontSize
    : undefined;
  const palette = typeof rec.palette === "string" && rec.palette.trim() ? rec.palette.trim().slice(0, 64) : undefined;
  if (!mode || !fontSize || !palette) return undefined;
  const customPalettes = Array.isArray(rec.customPalettes)
    ? rec.customPalettes
        .filter((item): item is CustomPalette => Boolean(item) && typeof item === "object")
        .slice(0, 20)
    : undefined;
  return { mode, fontSize, palette, customPalettes };
}

function parseScanParams(value: unknown): ScanParams | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  return value as ScanParams;
}

function bytesToBase64Url(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

function base64UrlToBytes(input: string): Uint8Array {
  const clean = input.trim().replace(/^EFC1\./, "").replace(/-/g, "+").replace(/_/g, "/");
  const padded = clean.padEnd(clean.length + ((4 - (clean.length % 4)) % 4), "=");
  const binary = atob(padded);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return bytes;
}

export function buildShareableCockpitCode(snapshot: WorkspaceSnapshot | WorkspaceLoadoutPack): string {
  const json = JSON.stringify(snapshot);
  const bytes = new TextEncoder().encode(json);
  return `${COCKPIT_SHARE_PREFIX}${bytesToBase64Url(bytes)}`;
}

export function parseShareableCockpitCode(code: string): WorkspaceImportResult {
  const json = new TextDecoder().decode(base64UrlToBytes(code));
  return parseWorkspaceImport(json);
}

export function parseWorkspaceText(raw: string): WorkspaceImportResult {
  const trimmed = raw.trim();
  return trimmed.startsWith(COCKPIT_SHARE_PREFIX)
    ? parseShareableCockpitCode(trimmed)
    : parseWorkspaceImport(trimmed);
}

function importPrivacyWarnings(value: unknown): string[] {
  const warnings: string[] = [];
  const rec = value && typeof value === "object" ? value as Record<string, unknown> : {};
  const privacy = rec.privacy && typeof rec.privacy === "object" ? rec.privacy as Record<string, unknown> : {};
  if (privacy.includesPrivateFields === true) {
    warnings.push("This workspace claims to include private fields. Review the JSON before installing.");
  }
  return warnings;
}

function normalizeImportedLoadouts(values: unknown): WorkspaceLoadoutEntry[] {
  if (!Array.isArray(values) || values.length === 0) {
    throw new Error("Workspace pack has no loadouts");
  }
  if (values.length > 50) {
    throw new Error("Workspace pack has too many loadouts");
  }
  const usedNames = new Map<string, number>();
  return values.map((value, index) => {
    const rec = value && typeof value === "object" ? value as Record<string, unknown> : {};
    const cockpit = sanitizeCockpitPreferences(rec.cockpit);
    const rawName = typeof rec.name === "string" && rec.name.trim()
      ? rec.name.trim()
      : cockpit.name || `Imported loadout ${index + 1}`;
    const seen = usedNames.get(rawName) ?? 0;
    usedNames.set(rawName, seen + 1);
    const name = seen > 0 ? `${rawName} ${seen + 1}` : rawName;
    return {
      name,
      cockpit: sanitizeCockpitPreferences({ ...cockpit, name }),
      active: Boolean(rec.active),
      sourceId: typeof rec.sourceId === "string" ? rec.sourceId : undefined,
    };
  });
}

export function parseWorkspaceImport(raw: string): WorkspaceImportResult {
  const parsed = JSON.parse(raw) as Record<string, unknown>;
  if (parsed.kind === "eve-flipper-workspace" && parsed.version === 1) {
    const scanParams = parseScanParams(parsed.scanParams);
    const warnings = importPrivacyWarnings(parsed);
    if (!scanParams) {
      warnings.push("This workspace has no scan params; only the interface layout will be installed.");
    }
    const cockpit = sanitizeCockpitPreferences(parsed.cockpit);
    return {
      kind: "single",
      exportedAt: typeof parsed.exportedAt === "string" ? parsed.exportedAt : new Date().toISOString(),
      loadouts: [{ name: cockpit.name || "Imported loadout", cockpit, active: true }],
      scanParams,
      theme: sanitizeWorkspaceTheme(parsed.theme),
      warnings,
    };
  }

  if (parsed.kind === "eve-flipper-workspace-pack" && parsed.version === 1) {
    const warnings = importPrivacyWarnings(parsed);
    const scanParams = parseScanParams(parsed.scanParams);
    if (!scanParams) {
      warnings.push("This workspace pack has no scan params; only loadouts will be installed.");
    }
    const loadouts = normalizeImportedLoadouts(parsed.loadouts);
    if (!loadouts.some((loadout) => loadout.active)) {
      loadouts[0] = { ...loadouts[0], active: true };
    }
    return {
      kind: "pack",
      exportedAt: typeof parsed.exportedAt === "string" ? parsed.exportedAt : new Date().toISOString(),
      loadouts,
      scanParams,
      theme: sanitizeWorkspaceTheme(parsed.theme),
      warnings,
    };
  }

  throw new Error("Unsupported workspace loadout or pack");
}

export function parseWorkspaceSnapshot(raw: string): WorkspaceSnapshot {
  const parsed = parseWorkspaceImport(raw);
  const first = parsed.loadouts[0];
  if (!first || !parsed.scanParams) {
    throw new Error("Workspace loadout has no scan params");
  }
  return {
    kind: "eve-flipper-workspace",
    version: 1,
    exportedAt: parsed.exportedAt,
    app: "eve-flipper",
    cockpit: first.cockpit,
    scanParams: parsed.scanParams,
    theme: parsed.theme,
    privacy: {
      includesPrivateFields: false,
      excluded: WORKSPACE_PRIVACY_EXCLUDED,
    },
  };
}

function sanitizeMarketplaceEntry(value: unknown): CockpitMarketplaceEntry | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  const rec = value as Record<string, unknown>;
  const id = typeof rec.id === "string" ? rec.id.trim().slice(0, 100) : "";
  const name = typeof rec.name === "string" ? rec.name.trim().slice(0, 120) : "";
  const layoutUrl = typeof rec.layoutUrl === "string" ? rec.layoutUrl.trim() : "";
  if (!id || !name || !layoutUrl) return null;
  const tags = Array.isArray(rec.tags)
    ? rec.tags
        .filter((tag): tag is string => typeof tag === "string")
        .map((tag) => tag.trim().slice(0, 32))
        .filter(Boolean)
        .slice(0, 12)
    : [];
  return {
    id,
    name,
    author: typeof rec.author === "string" && rec.author.trim() ? rec.author.trim().slice(0, 80) : "Community",
    description: typeof rec.description === "string" ? rec.description.trim().slice(0, 500) : "",
    tags,
    rating: typeof rec.rating === "number" && Number.isFinite(rec.rating)
      ? Math.max(0, Math.min(5, rec.rating))
      : 0,
    downloads: typeof rec.downloads === "number" && Number.isFinite(rec.downloads)
      ? Math.max(0, Math.round(rec.downloads))
      : 0,
    verified: rec.verified === true,
    featured: rec.featured === true,
    updatedAt: typeof rec.updatedAt === "string" ? rec.updatedAt : "",
    layoutUrl,
    sourceUrl: typeof rec.sourceUrl === "string" ? rec.sourceUrl : undefined,
    minAppVersion: typeof rec.minAppVersion === "string" ? rec.minAppVersion : undefined,
  };
}

export async function fetchCockpitMarketplaceCatalog(url = COCKPIT_MARKETPLACE_URL): Promise<CockpitMarketplaceCatalog> {
  const resp = await fetch(url, { cache: "no-store" });
  if (!resp.ok) {
    throw new Error(`Marketplace HTTP ${resp.status}`);
  }
  const parsed = await resp.json() as Record<string, unknown>;
  if (parsed.kind !== "eve-flipper-cockpit-marketplace" || parsed.version !== 1) {
    throw new Error("Unsupported cockpit marketplace catalog");
  }
  const entries = Array.isArray(parsed.entries)
    ? parsed.entries.map(sanitizeMarketplaceEntry).filter((entry): entry is CockpitMarketplaceEntry => Boolean(entry))
    : [];
  return {
    kind: "eve-flipper-cockpit-marketplace",
    version: 1,
    updatedAt: typeof parsed.updatedAt === "string" ? parsed.updatedAt : new Date().toISOString(),
    entries,
  };
}

export async function fetchCockpitMarketplaceLayout(entry: CockpitMarketplaceEntry): Promise<CockpitMarketplaceLayout> {
  const resp = await fetch(entry.layoutUrl, { cache: "no-store" });
  if (!resp.ok) {
    throw new Error(`Layout HTTP ${resp.status}`);
  }
  const raw = await resp.text();
  return {
    ...parseWorkspaceText(raw),
    sourceEntry: entry,
  };
}
