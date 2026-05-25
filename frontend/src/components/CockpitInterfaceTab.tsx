import { useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import {
  COCKPIT_MARKETPLACE_URL,
  buildShareableCockpitCode,
  buildWorkspacePack,
  buildWorkspaceSnapshot,
  COCKPIT_COLUMN_PRESETS,
  COCKPIT_FILTER_PRESETS,
  COCKPIT_PROFILE_PRESETS,
  COCKPIT_QUICK_ACTIONS,
  COCKPIT_WORKSPACE_TEMPLATES,
  defaultCockpitPreferences,
  fetchCockpitMarketplaceCatalog,
  fetchCockpitMarketplaceLayout,
  getCockpitAdaptiveSuggestions,
  buildCockpitBehaviorModel,
  getCockpitTabLayout,
  getVisibleMainTabs,
  loadCockpitActivityStats,
  MAIN_TAB_IDS,
  MAIN_TAB_META,
  parseWorkspaceText,
  resetCockpitActivityStats,
  sanitizeCockpitPreferences,
  type CockpitContextTask,
  type CockpitDensity,
  type CockpitColumnPreset,
  type CockpitFilterPreset,
  type CockpitLoadout,
  type CockpitMarketplaceEntry,
  type CockpitPreferences,
  type CockpitProfilePreset,
  type CockpitProfilePresetID,
  type CockpitQuickAction,
  type CockpitActivityStats,
  type MainTabId,
  type WorkspaceImportResult,
  type WorkspaceThemeSnapshot,
} from "@/lib/cockpit";
import type { ScanParams } from "@/lib/types";
import { useI18n } from "@/lib/i18n";
import { useTheme } from "@/lib/useTheme";
import { cockpitInterfacePages, type InterfacePage } from "@/lib/cockpitInterfacePages";

interface CockpitInterfaceTabProps {
  preferences: CockpitPreferences;
  loadouts: CockpitLoadout[];
  activeLoadoutID: string;
  syncStatus: "local" | "loading" | "saved" | "saving" | "error";
  onChange: (preferences: CockpitPreferences) => void;
  onActivateLoadout: (loadoutID: string) => Promise<void>;
  onCreateLoadout: (name: string, source?: CockpitPreferences, activate?: boolean) => Promise<void>;
  onDuplicateLoadout: (loadoutID: string) => Promise<void>;
  onDeleteLoadout: (loadoutID: string) => Promise<void>;
  scanParams: ScanParams;
  onScanParamsChange: (params: ScanParams) => void;
  activeCharacterId?: number;
  page?: InterfacePage;
  onPageChange?: (page: InterfacePage) => void;
  hideSidebar?: boolean;
}

function Panel({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="border border-eve-border/70 bg-eve-panel/70 rounded-sm">
      <div className="px-3 py-2 border-b border-eve-border/60 text-[11px] uppercase tracking-widest text-eve-accent font-semibold">
        {title}
      </div>
      <div className="p-3 space-y-3">{children}</div>
    </section>
  );
}

function PresetFact({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="inline-flex max-w-full items-center gap-1.5 rounded-sm border border-eve-border/50 bg-eve-dark/35 px-2 py-1 text-[11px]">
      <span className="shrink-0 font-semibold uppercase tracking-widest text-eve-dim">{label}</span>
      <span className="min-w-0 text-eve-text">{value}</span>
    </div>
  );
}

function TemplateCard({
  name,
  description,
  tags,
  rating,
  favorite,
  layoutLocked,
  busy,
  onApply,
  onCreate,
  onToggleFavorite,
}: {
  name: string;
  description: string;
  tags: string[];
  rating: number;
  favorite: boolean;
  layoutLocked: boolean;
  busy: boolean;
  onApply: () => void;
  onCreate: () => void;
  onToggleFavorite: () => void;
}) {
  return (
    <article className="flex min-h-[172px] flex-col justify-between rounded-sm border border-eve-border/60 bg-eve-dark/35 p-3 transition-colors hover:border-eve-accent/35 hover:bg-eve-panel/55">
      <div>
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <h3 className="truncate text-sm font-semibold text-eve-text">{name}</h3>
            <p className="mt-1 text-xs leading-5 text-eve-dim">{description}</p>
          </div>
          <span className="shrink-0 rounded-sm border border-eve-accent/40 px-2 py-1 text-[10px] font-semibold text-eve-accent">
            {rating.toFixed(1)}
          </span>
        </div>
        <div className="mt-3 flex flex-wrap gap-1.5">
          {tags.map((tag) => (
            <span key={tag} className="rounded-sm border border-eve-border/60 bg-eve-panel/50 px-2 py-0.5 text-[10px] text-eve-dim">
              {tag}
            </span>
          ))}
        </div>
      </div>

      <div className="mt-3 flex flex-wrap gap-2 border-t border-eve-border/45 pt-3">
        <button
          type="button"
          onClick={onApply}
          disabled={layoutLocked || busy}
          className="h-8 rounded-sm bg-eve-accent px-3 text-[11px] font-semibold uppercase tracking-wider text-eve-dark transition-colors hover:bg-eve-accent-hover disabled:opacity-40"
        >
          Apply
        </button>
        <button
          type="button"
          onClick={onCreate}
          disabled={busy}
          className="h-8 rounded-sm border border-eve-accent/70 px-3 text-[11px] font-semibold uppercase tracking-wider text-eve-accent transition-colors hover:bg-eve-accent/10 disabled:opacity-40"
        >
          Create loadout
        </button>
        <button
          type="button"
          onClick={onToggleFavorite}
          className={`h-8 rounded-sm border px-3 text-[11px] font-semibold uppercase tracking-wider transition-colors ${
            favorite
              ? "border-eve-accent/60 bg-eve-accent/10 text-eve-accent"
              : "border-eve-border text-eve-dim hover:text-eve-accent"
          }`}
        >
          {favorite ? "Favorited" : "Favorite"}
        </button>
      </div>
    </article>
  );
}

function ToggleRow({
  title,
  description,
  checked,
  onChange,
}: {
  title: string;
  description: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <label className="flex items-center justify-between gap-4 border border-eve-border/60 bg-eve-dark/35 rounded-sm px-3 py-2 cursor-pointer">
      <span>
        <span className="block text-sm text-eve-text">{title}</span>
        <span className="block text-xs text-eve-dim mt-0.5">{description}</span>
      </span>
      <input
        type="checkbox"
        checked={checked}
        onChange={(event) => onChange(event.target.checked)}
        className="accent-eve-accent"
      />
    </label>
  );
}

function FieldGrid({ children }: { children: ReactNode }) {
  return <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-3">{children}</div>;
}

function NumberField({
  label,
  value,
  onChange,
  min,
  max,
  step = 1,
  hint,
}: {
  label: string;
  value: number | undefined;
  onChange: (value: number) => void;
  min?: number;
  max?: number;
  step?: number;
  hint?: string;
}) {
  return (
    <label className="block border border-eve-border/60 bg-eve-dark/35 rounded-sm p-3">
      <span className="block text-[10px] uppercase tracking-widest text-eve-dim">{label}</span>
      <input
        type="number"
        value={Number.isFinite(value) ? value : 0}
        min={min}
        max={max}
        step={step}
        onChange={(event) => onChange(Number(event.target.value))}
        className="mt-2 w-full px-2 py-1.5 bg-eve-input border border-eve-border rounded-sm text-sm text-eve-text"
      />
      {hint && <span className="block mt-1 text-[11px] text-eve-dim">{hint}</span>}
    </label>
  );
}

function TextField({
  label,
  value,
  onChange,
  placeholder,
  hint,
}: {
  label: string;
  value: string | undefined;
  onChange: (value: string) => void;
  placeholder?: string;
  hint?: string;
}) {
  return (
    <label className="block border border-eve-border/60 bg-eve-dark/35 rounded-sm p-3">
      <span className="block text-[10px] uppercase tracking-widest text-eve-dim">{label}</span>
      <input
        value={value ?? ""}
        placeholder={placeholder}
        onChange={(event) => onChange(event.target.value)}
        className="mt-2 w-full px-2 py-1.5 bg-eve-input border border-eve-border rounded-sm text-sm text-eve-text"
      />
      {hint && <span className="block mt-1 text-[11px] text-eve-dim">{hint}</span>}
    </label>
  );
}

function SelectField({
  label,
  value,
  options,
  onChange,
  hint,
}: {
  label: string;
  value: string | undefined;
  options: { value: string; label: string }[];
  onChange: (value: string) => void;
  hint?: string;
}) {
  return (
    <label className="block border border-eve-border/60 bg-eve-dark/35 rounded-sm p-3">
      <span className="block text-[10px] uppercase tracking-widest text-eve-dim">{label}</span>
      <select
        value={value ?? options[0]?.value ?? ""}
        onChange={(event) => onChange(event.target.value)}
        className="mt-2 w-full px-2 py-1.5 bg-eve-input border border-eve-border rounded-sm text-sm text-eve-text"
      >
        {options.map((option) => (
          <option key={option.value} value={option.value}>{option.label}</option>
        ))}
      </select>
      {hint && <span className="block mt-1 text-[11px] text-eve-dim">{hint}</span>}
    </label>
  );
}

function CompactSelect({
  value,
  options,
  onChange,
  ariaLabel,
}: {
  value: string | undefined;
  options: { value: string; label: string }[];
  onChange: (value: string) => void;
  ariaLabel: string;
}) {
  return (
    <select
      aria-label={ariaLabel}
      value={value ?? options[0]?.value ?? ""}
      onChange={(event) => onChange(event.target.value)}
      className="h-8 w-full min-w-0 rounded-sm border border-eve-border bg-eve-input px-2 text-xs text-eve-text outline-none transition-colors focus:border-eve-accent/60"
    >
      {options.map((option) => (
        <option key={option.value} value={option.value}>{option.label}</option>
      ))}
    </select>
  );
}

function densityLabel(density: CockpitDensity): string {
  if (density === "dense") return "Dense";
  if (density === "compact") return "Compact";
  return "Comfortable";
}

const columnPresetLabels: Record<CockpitColumnPreset, string> = {
  auto: "Auto",
  default: "Default table",
  compact: "Compact scan",
  trader: "Trader desk",
  hauling: "Hauling route",
  accounting: "Ledger / accounting",
};

const filterPresetLabels: Record<CockpitFilterPreset, string> = {
  manual: "Manual / current",
  jita: "Jita station",
  low_capital: "Low capital",
  hauling: "Hauling",
  industry: "Industry",
};

const quickActionLabels: Record<CockpitQuickAction, { label: string; description: string }> = {
  watchlist: { label: "Watchlist", description: "Top-bar market alert list." },
  history: { label: "History", description: "Open scan and saved result history." },
  itemIntel: { label: "Item Intel", description: "Open personal item intelligence." },
  missionControl: { label: "Mission Control", description: "Jump to execution planning workflow; rows still open their own trade plan." },
  ledger: { label: "Ledger", description: "Open the character ledger / cashflow dashboard." },
  journal: { label: "Trade Journal", description: "Open saved Mission Control plans and paper/live trade tracking." },
  dotlan: { label: "DOTLAN", description: "Open DOTLAN route tools from the top bar." },
  commandPalette: { label: "Command palette", description: "Expose Ctrl+K as a visible action." },
  shortcuts: { label: "Shortcuts", description: "Open keyboard shortcut reference." },
};

function filterPresetPatch(tab: MainTabId, preset: CockpitFilterPreset): Partial<ScanParams> {
  if (preset === "jita") {
    return {
      system_name: "Jita",
      target_market_system: "Jita",
      min_margin: tab === "station" ? 2 : 5,
      min_daily_volume: 20,
      max_dos: 30,
    };
  }
  if (preset === "low_capital") {
    return {
      max_investment: 1_000_000_000,
      min_item_profit: 500_000,
      min_margin: 8,
      min_daily_volume: 5,
      cargo_capacity: 10_000,
    };
  }
  if (preset === "hauling") {
    return {
      sell_order_mode: true,
      min_margin: 10,
      cargo_capacity: 50_000,
      route_cargo_capacity: 50_000,
      route_mode: "balanced",
      route_min_isk_per_jump: 1_000_000,
      shipping_cost_per_m3_jump: 0,
      regional_diagnostic_mode: false,
    };
  }
  if (preset === "industry") {
    return {
      include_structures: true,
      min_margin: 8,
      min_daily_volume: 3,
      avg_price_period: 30,
      max_dos: 45,
    };
  }
  return {};
}

export function CockpitInterfaceTab({
  preferences,
  loadouts,
  activeLoadoutID,
  syncStatus,
  onChange,
  onActivateLoadout,
  onCreateLoadout,
  onDuplicateLoadout,
  onDeleteLoadout,
  scanParams,
  onScanParamsChange,
  activeCharacterId,
  page,
  onPageChange,
  hideSidebar = false,
}: CockpitInterfaceTabProps) {
  const { t } = useI18n();
  const theme = useTheme();
  const [internalPage, setInternalPage] = useState<InterfacePage>("overview");
  const activePage = page ?? internalPage;
  const setActivePage = onPageChange ?? setInternalPage;
  const [exportText, setExportText] = useState("");
  const [importText, setImportText] = useState("");
  const [importPreview, setImportPreview] = useState<WorkspaceImportResult | null>(null);
  const [applyImportedScanParams, setApplyImportedScanParams] = useState(true);
  const [status, setStatus] = useState("");
  const [newLoadoutName, setNewLoadoutName] = useState("");
  const [loadoutBusy, setLoadoutBusy] = useState("");
  const [activityStats, setActivityStats] = useState<CockpitActivityStats>(() => loadCockpitActivityStats());
  const [marketplaceEntries, setMarketplaceEntries] = useState<CockpitMarketplaceEntry[]>([]);
  const [marketplaceLoading, setMarketplaceLoading] = useState(false);
  const [marketplaceError, setMarketplaceError] = useState("");
  const [marketplaceLoadedAt, setMarketplaceLoadedAt] = useState("");
  const prefs = useMemo(() => sanitizeCockpitPreferences(preferences), [preferences]);
  const visibleTabs = getVisibleMainTabs(prefs);
  const adaptiveSuggestions = useMemo(
    () => getCockpitAdaptiveSuggestions(prefs, activityStats),
    [activityStats, prefs],
  );
  const behaviorModel = useMemo(() => buildCockpitBehaviorModel(activityStats), [activityStats]);
  const themeSnapshot = useMemo<WorkspaceThemeSnapshot>(() => ({
    mode: theme.mode,
    palette: theme.palette,
    fontSize: theme.fontSize,
    customPalettes: theme.customPalettes,
  }), [theme.mode, theme.palette, theme.fontSize, theme.customPalettes]);
  const normalizedLoadouts = useMemo(() => {
    const rows = loadouts.length > 0
      ? loadouts
      : [{ id: "default", name: prefs.name, preferences: prefs, active: true }];
    const resolvedActiveID = activeLoadoutID || rows.find((loadout) => loadout.active)?.id || "default";
    return rows.map((loadout) => ({
      ...loadout,
      preferences: sanitizeCockpitPreferences(loadout.preferences),
      active: loadout.id === resolvedActiveID,
    }));
  }, [activeLoadoutID, loadouts, prefs]);
  const syncLabel = syncStatus === "saving"
    ? "Saving"
    : syncStatus === "loading"
      ? "Loading"
      : syncStatus === "saved"
        ? "Saved to DB"
        : syncStatus === "error"
          ? "Sync error"
          : "Local fallback";

  const refreshMarketplace = async () => {
    setMarketplaceLoading(true);
    setMarketplaceError("");
    try {
      const catalog = await fetchCockpitMarketplaceCatalog();
      setMarketplaceEntries(catalog.entries);
      setMarketplaceLoadedAt(catalog.updatedAt);
    } catch (error) {
      setMarketplaceError(error instanceof Error ? error.message : "Marketplace fetch failed");
    } finally {
      setMarketplaceLoading(false);
    }
  };

  useEffect(() => {
    if (activePage !== "gallery" || marketplaceEntries.length > 0 || marketplaceLoading) return;
    void refreshMarketplace();
  }, [activePage, marketplaceEntries.length, marketplaceLoading]);

  const update = (patch: Partial<CockpitPreferences>) => {
    if (prefs.layoutLocked && patch.layoutLocked !== false) {
      setStatus("Layout is locked. Unlock this loadout before changing cockpit layout.");
      return;
    }
    onChange(sanitizeCockpitPreferences({ ...prefs, ...patch }));
  };

  const resetCockpitToDefault = () => {
    if (prefs.layoutLocked) {
      setStatus("Layout is locked. Unlock this loadout before resetting cockpit.");
      return;
    }
    onChange(defaultCockpitPreferences);
    setStatus("Default cockpit restored.");
  };

  const setHiddenTab = (tab: MainTabId, hidden: boolean) => {
    const nextHidden = hidden
      ? [...prefs.hiddenMainTabs, tab]
      : prefs.hiddenMainTabs.filter((item) => item !== tab);
    update({ hiddenMainTabs: nextHidden });
  };

  const moveTab = (tab: MainTabId, delta: -1 | 1) => {
    const order = [...prefs.mainTabOrder];
    const index = order.indexOf(tab);
    const nextIndex = index + delta;
    if (index < 0 || nextIndex < 0 || nextIndex >= order.length) return;
    [order[index], order[nextIndex]] = [order[nextIndex], order[index]];
    update({ mainTabOrder: order });
  };

  const setPanelHidden = (key: keyof CockpitPreferences["hiddenPanels"], hidden: boolean) => {
    update({ hiddenPanels: { ...prefs.hiddenPanels, [key]: hidden } });
  };

  const setQuickAction = (action: CockpitQuickAction, visible: boolean) => {
    const next = visible
      ? [...prefs.quickActions, action]
      : prefs.quickActions.filter((item) => item !== action);
    update({ quickActions: COCKPIT_QUICK_ACTIONS.filter((item) => next.includes(item)) });
  };

  const updateTabLayout = (tab: MainTabId, patch: Partial<ReturnType<typeof getCockpitTabLayout>>) => {
    const current = getCockpitTabLayout(prefs, tab);
    update({
      tabLayouts: {
        ...prefs.tabLayouts,
        [tab]: {
          ...current,
          ...patch,
        },
      },
    });
  };

  const resetTabLayout = (tab: MainTabId) => {
    updateTabLayout(tab, defaultCockpitPreferences.tabLayouts[tab]);
  };

  const applyFilterPreset = (tab: MainTabId, preset: CockpitFilterPreset) => {
    updateTabLayout(tab, { filterPreset: preset });
    const patch = filterPresetPatch(tab, preset);
    if (Object.keys(patch).length > 0) {
      onScanParamsChange({ ...scanParams, ...patch });
    }
  };

  const setScanParam = <K extends keyof ScanParams>(key: K, value: ScanParams[K]) => {
    onScanParamsChange({ ...scanParams, [key]: value });
  };

  const runLoadoutAction = async (label: string, action: () => Promise<void>) => {
    setLoadoutBusy(label);
    setStatus("");
    try {
      await action();
      setStatus(`${label} complete.`);
    } catch (error) {
      setStatus(error instanceof Error ? error.message : `${label} failed.`);
    } finally {
      setLoadoutBusy("");
    }
  };

  const createLoadout = async () => {
    const name = newLoadoutName.trim() || `${prefs.name || "Cockpit"} copy`;
    await runLoadoutAction("Create loadout", async () => {
      await onCreateLoadout(name, prefs);
      setNewLoadoutName("");
    });
  };

  const writeExportText = async (text: string, copiedStatus: string, generatedStatus: string) => {
    setExportText(text);
    setStatus(generatedStatus);
    try {
      await navigator.clipboard?.writeText(text);
      setStatus(copiedStatus);
    } catch {
      // Clipboard may be unavailable in desktop/webview contexts.
    }
  };

  const generateActiveExport = async () => {
    const text = JSON.stringify(buildWorkspaceSnapshot(prefs, scanParams, themeSnapshot), null, 2);
    await writeExportText(text, "Active loadout export copied to clipboard.", "Active loadout export generated.");
  };

  const generatePackExport = async () => {
    const text = JSON.stringify(buildWorkspacePack(normalizedLoadouts, activeLoadoutID, prefs, scanParams, themeSnapshot), null, 2);
    await writeExportText(text, "Workspace pack copied to clipboard.", "Workspace pack generated.");
  };

  const generateShareCode = async () => {
    const code = buildShareableCockpitCode(buildWorkspaceSnapshot(prefs, scanParams, themeSnapshot));
    await writeExportText(code, "Share code copied to clipboard.", "Share code generated.");
  };

  const applyImportedTheme = (incomingTheme: WorkspaceImportResult["theme"]) => {
    if (!incomingTheme) return;
    for (const customPalette of incomingTheme.customPalettes ?? []) {
      theme.saveCustomPalette(customPalette);
    }
    theme.setMode(incomingTheme.mode);
    theme.setPalette(incomingTheme.palette);
    theme.setFontSize(incomingTheme.fontSize);
  };

  const previewImport = () => {
    try {
      const parsed = parseWorkspaceText(importText);
      setImportPreview(parsed);
      setStatus(`Valid ${parsed.kind === "pack" ? "workspace pack" : "workspace loadout"}: ${parsed.loadouts.length} loadout${parsed.loadouts.length === 1 ? "" : "s"}.`);
    } catch (error) {
      setImportPreview(null);
      setStatus(error instanceof Error ? error.message : "Invalid workspace loadout.");
    }
  };

  const installImport = async () => {
    try {
      const parsed = importPreview ?? parseWorkspaceText(importText);
      await runLoadoutAction("Install workspace pack", async () => {
        const activeIndex = parsed.loadouts.findIndex((loadout) => loadout.active);
        const fallbackActiveIndex = activeIndex >= 0 ? activeIndex : 0;
        for (let index = 0; index < parsed.loadouts.length; index += 1) {
          const loadout = parsed.loadouts[index];
          if (!loadout) continue;
          await onCreateLoadout(loadout.name, loadout.cockpit, index === fallbackActiveIndex);
        }
        if (parsed.scanParams && applyImportedScanParams) {
          onScanParamsChange(parsed.scanParams);
        }
        applyImportedTheme(parsed.theme);
        setImportPreview(parsed);
      });
    } catch (error) {
      setImportPreview(null);
      setStatus(error instanceof Error ? error.message : "Invalid workspace loadout.");
    }
  };

  const labelForTab = (id: MainTabId) => {
    const meta = MAIN_TAB_META[id];
    return t(meta.labelKey) || meta.fallback;
  };

  const applyProfilePreset = async (preset: CockpitProfilePreset) => {
    if (prefs.layoutLocked) {
      setStatus("Layout is locked. Unlock this loadout before applying a profile preset.");
      return;
    }
    await runLoadoutAction(`Apply ${preset.name}`, async () => {
      const nextPrefs = sanitizeCockpitPreferences({ ...preset.cockpit, layoutLocked: prefs.layoutLocked });
      onChange(nextPrefs);
      onScanParamsChange({ ...scanParams, ...preset.scanParams });
    });
  };

  const createProfileLoadout = async (preset: CockpitProfilePreset) => {
    await runLoadoutAction(`Create ${preset.name}`, async () => {
      await onCreateLoadout(preset.name, preset.cockpit, true);
      onScanParamsChange({ ...scanParams, ...preset.scanParams });
    });
  };

  const dismissAdaptiveSuggestion = (suggestionID: string) => {
    update({
      dismissedAdaptiveSuggestions: [...prefs.dismissedAdaptiveSuggestions, suggestionID],
    });
  };

  const resetAdaptiveLearning = () => {
    setActivityStats(resetCockpitActivityStats());
    update({ dismissedAdaptiveSuggestions: [] });
  };

  const bindCurrentCharacter = (patch: Partial<CockpitPreferences["roleBindings"][string]>) => {
    if (!activeCharacterId) {
      setStatus("Login with a character before creating a role-aware binding.");
      return;
    }
    const characterId = String(activeCharacterId);
    const current = prefs.roleBindings[characterId] ?? {
      characterId,
      label: `Character ${characterId}`,
      presetId: "",
      loadoutId: activeLoadoutID,
      contextRules: [],
    };
    update({
      roleBindings: {
        ...prefs.roleBindings,
        [characterId]: {
          ...current,
          ...patch,
          characterId,
        },
      },
    });
  };

  const clearCurrentCharacterBinding = () => {
    if (!activeCharacterId) return;
    const characterId = String(activeCharacterId);
    const { [characterId]: _removed, ...rest } = prefs.roleBindings;
    update({ roleBindings: rest });
  };

  const applyTemplate = async (templateID: string) => {
    const template = COCKPIT_WORKSPACE_TEMPLATES.find((item) => item.id === templateID);
    const preset = template ? COCKPIT_PROFILE_PRESETS.find((item) => item.id === template.presetId) : undefined;
    if (!template || !preset) return;
    await applyProfilePreset(preset);
  };

  const createTemplateLoadout = async (templateID: string) => {
    const template = COCKPIT_WORKSPACE_TEMPLATES.find((item) => item.id === templateID);
    const preset = template ? COCKPIT_PROFILE_PRESETS.find((item) => item.id === template.presetId) : undefined;
    if (!template || !preset) return;
    await runLoadoutAction(`Create ${template.name}`, async () => {
      await onCreateLoadout(template.name, { ...preset.cockpit, name: template.name }, true);
      onScanParamsChange({ ...scanParams, ...preset.scanParams });
    });
  };

  const installMarketplaceEntry = async (entry: CockpitMarketplaceEntry) => {
    await runLoadoutAction(`Install ${entry.name}`, async () => {
      const imported = await fetchCockpitMarketplaceLayout(entry);
      const activeIndex = imported.loadouts.findIndex((loadout) => loadout.active);
      const fallbackActiveIndex = activeIndex >= 0 ? activeIndex : 0;
      for (let index = 0; index < imported.loadouts.length; index += 1) {
        const loadout = imported.loadouts[index];
        if (!loadout) continue;
        await onCreateLoadout(loadout.name || entry.name, loadout.cockpit, index === fallbackActiveIndex);
      }
      if (imported.scanParams) {
        onScanParamsChange({ ...scanParams, ...imported.scanParams });
      }
      applyImportedTheme(imported.theme);
    });
  };

  const bindCurrentTaskToCurrentLoadout = () => {
    if (!activeCharacterId) {
      setStatus("Login with a character before creating a context rule.");
      return;
    }
    const task: CockpitContextTask =
      prefs.startupTab === "region" ? "regional" :
      prefs.startupTab === "route" ? "route" :
      prefs.startupTab === "industry" ? "industry" :
      prefs.startupTab === "station" || prefs.startupTab === "radius" ? "station" :
      "any";
    const characterId = String(activeCharacterId);
    const current = prefs.roleBindings[characterId] ?? {
      characterId,
      label: `Character ${characterId}`,
      presetId: "",
      loadoutId: activeLoadoutID,
      contextRules: [],
    };
    const nextRule = {
      id: `rule-${task}-${Date.now()}`,
      label: `${task} -> ${normalizedLoadouts.find((loadout) => loadout.id === activeLoadoutID)?.name ?? "current loadout"}`,
      task,
      routeMode: "any" as const,
      loadoutId: activeLoadoutID,
      presetId: "" as const,
      priority: 80,
    };
    update({
      roleBindings: {
        ...prefs.roleBindings,
        [characterId]: {
          ...current,
          contextRules: [...(current.contextRules ?? []), nextRule],
        },
      },
    });
  };

  const removeContextRule = (ruleID: string) => {
    if (!activeCharacterId) return;
    const characterId = String(activeCharacterId);
    const current = prefs.roleBindings[characterId];
    if (!current) return;
    update({
      roleBindings: {
        ...prefs.roleBindings,
        [characterId]: {
          ...current,
          contextRules: current.contextRules.filter((rule) => rule.id !== ruleID),
        },
      },
    });
  };

  return (
    <div className={hideSidebar ? "min-h-full" : "grid grid-cols-[220px_minmax(0,1fr)] gap-4 min-h-full"}>
      {!hideSidebar && (
      <aside className="border border-eve-border/70 bg-eve-dark/45 rounded-sm overflow-hidden">
        <div className="px-3 py-2 border-b border-eve-border/60 text-[10px] uppercase tracking-widest text-eve-dim">
          Interface
        </div>
        <nav className="p-1.5 space-y-1">
          {cockpitInterfacePages.map((item) => (
            <button
              key={item.id}
              type="button"
              onClick={() => setActivePage(item.id)}
              className={`w-full text-left px-2.5 py-1.5 rounded-sm text-xs transition-colors ${
                activePage === item.id
                  ? "bg-eve-accent/15 text-eve-accent"
                  : "text-eve-dim hover:text-eve-text hover:bg-eve-panel/70"
              }`}
            >
              {item.label}
            </button>
          ))}
        </nav>
      </aside>
      )}

      <main className="min-w-0 space-y-4">
        {status && activePage !== "share" && (
          <div className="border border-eve-border/60 bg-eve-dark/45 rounded-sm px-3 py-2 text-xs text-eve-dim">
            {status}
          </div>
        )}

        {activePage === "overview" && (
          <>
            <Panel title="Cockpit Loadout">
              <div className="grid grid-cols-1 md:grid-cols-4 gap-3">
                <div className="border border-eve-border/60 bg-eve-dark/35 rounded-sm p-3">
                  <div className="text-[10px] uppercase tracking-widest text-eve-dim">Loadout</div>
                  <input
                    value={prefs.name}
                    onChange={(event) => update({ name: event.target.value })}
                    className="mt-2 w-full px-2 py-1.5 bg-eve-input border border-eve-border rounded-sm text-sm text-eve-text"
                  />
                </div>
                <div className="border border-eve-border/60 bg-eve-dark/35 rounded-sm p-3">
                  <div className="text-[10px] uppercase tracking-widest text-eve-dim">Density</div>
                  <div className="mt-2 text-lg font-mono text-eve-accent">{densityLabel(prefs.density)}</div>
                </div>
                <div className="border border-eve-border/60 bg-eve-dark/35 rounded-sm p-3">
                  <div className="text-[10px] uppercase tracking-widest text-eve-dim">Visible main tabs</div>
                  <div className="mt-2 text-lg font-mono text-eve-accent">{visibleTabs.length} / {MAIN_TAB_IDS.length}</div>
                </div>
                <div className="border border-eve-border/60 bg-eve-dark/35 rounded-sm p-3">
                  <div className="text-[10px] uppercase tracking-widest text-eve-dim">Layout lock</div>
                  <button
                    type="button"
                    onClick={() => update({ layoutLocked: !prefs.layoutLocked })}
                    className={`mt-2 px-3 py-1.5 rounded-sm border text-xs font-semibold uppercase tracking-wider ${
                      prefs.layoutLocked
                        ? "border-amber-400/60 bg-amber-950/25 text-amber-200"
                        : "border-eve-border text-eve-dim hover:text-eve-accent"
                    }`}
                  >
                    {prefs.layoutLocked ? "Unlock layout" : "Lock layout"}
                  </button>
                </div>
              </div>
              {prefs.layoutLocked && (
                <div className="border border-amber-500/40 bg-amber-950/20 rounded-sm px-3 py-2 text-xs text-amber-100">
                  Layout editing is locked for this loadout. Scan parameters still work, but cockpit layout changes are blocked until you unlock it.
                </div>
              )}
              <p className="text-xs text-eve-dim">
                This loadout controls the working cockpit: navigation, density, panel visibility and exported scan parameters.
              </p>
            </Panel>

            <Panel title="Loadout Manager">
              <div className="flex flex-col lg:flex-row lg:items-center gap-3">
                <div className={`px-2.5 py-1 border rounded-sm text-[10px] uppercase tracking-widest ${
                  syncStatus === "error"
                    ? "border-red-500/50 text-red-300 bg-red-950/20"
                    : syncStatus === "saved"
                      ? "border-emerald-500/40 text-emerald-300 bg-emerald-950/20"
                      : "border-eve-border text-eve-dim bg-eve-dark/35"
                }`}>
                  {syncLabel}
                </div>
                <div className="flex-1 grid grid-cols-1 sm:grid-cols-[minmax(0,1fr)_auto] gap-2">
                  <input
                    value={newLoadoutName}
                    onChange={(event) => setNewLoadoutName(event.target.value)}
                    placeholder="New loadout name"
                    className="px-2 py-1.5 bg-eve-input border border-eve-border rounded-sm text-sm text-eve-text"
                  />
                  <button
                    type="button"
                    onClick={() => void createLoadout()}
                    disabled={Boolean(loadoutBusy)}
                    className="px-3 py-1.5 bg-eve-accent text-eve-dark rounded-sm text-xs font-semibold uppercase tracking-wider disabled:opacity-40"
                  >
                    Create
                  </button>
                </div>
              </div>

              <div className="space-y-2">
                {normalizedLoadouts.map((loadout) => {
                  const tabCount = getVisibleMainTabs(loadout.preferences).length;
                  return (
                    <div
                      key={loadout.id}
                      className={`flex flex-col xl:flex-row xl:items-center gap-2 border rounded-sm px-3 py-2 ${
                        loadout.id === activeLoadoutID
                          ? "border-eve-accent/60 bg-eve-accent/10"
                          : "border-eve-border/60 bg-eve-dark/35"
                      }`}
                    >
                      <div className="flex-1 min-w-0">
                        <div className="flex flex-wrap items-center gap-2">
                          <span className="text-sm text-eve-text font-semibold truncate">{loadout.name || loadout.preferences.name}</span>
                          {loadout.id === activeLoadoutID && (
                            <span className="px-1.5 py-0.5 border border-eve-accent/40 text-[9px] uppercase tracking-widest text-eve-accent rounded-sm">
                              Active
                            </span>
                          )}
                        </div>
                        <div className="mt-1 text-[11px] text-eve-dim">
                          {densityLabel(loadout.preferences.density)} · {tabCount}/{MAIN_TAB_IDS.length} tabs · {loadout.updated_at ? `updated ${new Date(loadout.updated_at).toLocaleString()}` : "not stored"}
                        </div>
                      </div>
                      <div className="flex flex-wrap items-center gap-2">
                        <button
                          type="button"
                          onClick={() => void runLoadoutAction("Activate loadout", () => onActivateLoadout(loadout.id))}
                          disabled={loadout.id === activeLoadoutID || Boolean(loadoutBusy)}
                          className="px-2.5 py-1 border border-eve-border rounded-sm text-xs text-eve-dim hover:text-eve-accent disabled:opacity-30"
                        >
                          Activate
                        </button>
                        <button
                          type="button"
                          onClick={() => void runLoadoutAction("Duplicate loadout", () => onDuplicateLoadout(loadout.id))}
                          disabled={Boolean(loadoutBusy)}
                          className="px-2.5 py-1 border border-eve-border rounded-sm text-xs text-eve-dim hover:text-eve-accent disabled:opacity-30"
                        >
                          Duplicate
                        </button>
                        <button
                          type="button"
                          onClick={() => void runLoadoutAction("Delete loadout", () => onDeleteLoadout(loadout.id))}
                          disabled={normalizedLoadouts.length <= 1 || Boolean(loadoutBusy)}
                          className="px-2.5 py-1 border border-red-500/40 rounded-sm text-xs text-red-300 hover:bg-red-950/30 disabled:opacity-30"
                        >
                          Delete
                        </button>
                      </div>
                    </div>
                  );
                })}
              </div>
              {loadoutBusy && <div className="text-xs text-eve-dim">{loadoutBusy}...</div>}
            </Panel>
          </>
        )}

        {activePage === "presets" && (
          <Panel title="Profile Presets">
            <div className="grid grid-cols-1 lg:grid-cols-2 2xl:grid-cols-3 gap-2.5">
              {COCKPIT_PROFILE_PRESETS.map((preset) => {
                const startupLabel = preset.cockpit.startupTab === "last"
                  ? "Last tab"
                  : labelForTab(preset.cockpit.startupTab);
                const visibleCount = getVisibleMainTabs(preset.cockpit).length;
                const paramCount = Object.keys(preset.scanParams).length;
                return (
                  <article
                    key={preset.id}
                    className="group flex min-h-[160px] flex-col justify-between rounded-sm border border-eve-border/60 bg-eve-dark/35 p-3 transition-colors hover:border-eve-accent/35 hover:bg-eve-panel/55"
                  >
                    <div>
                      <div className="flex items-start justify-between gap-3">
                        <div className="min-w-0">
                          <h3 className="truncate text-sm font-semibold text-eve-text">{preset.name}</h3>
                          <p className="mt-1 min-h-[42px] text-xs leading-5 text-eve-dim">{preset.description}</p>
                        </div>
                        <span className="shrink-0 rounded-sm border border-eve-border/60 bg-eve-panel/70 px-2 py-1 text-[9px] font-semibold uppercase tracking-widest text-eve-dim">
                          {densityLabel(preset.cockpit.density)}
                        </span>
                      </div>

                      <div className="mt-3 flex flex-wrap gap-1.5">
                        <PresetFact label="Start" value={startupLabel} />
                        <PresetFact label="Tabs" value={`${visibleCount}/${MAIN_TAB_IDS.length}`} />
                        <PresetFact label="Params" value={paramCount} />
                      </div>
                    </div>

                    <div className="mt-3 flex items-center gap-2 border-t border-eve-border/45 pt-3">
                      <button
                        type="button"
                        onClick={() => void applyProfilePreset(preset)}
                        disabled={Boolean(loadoutBusy) || prefs.layoutLocked}
                        className="h-8 flex-1 rounded-sm bg-eve-accent px-3 text-[11px] font-semibold uppercase tracking-wider text-eve-dark transition-colors hover:bg-eve-accent-hover disabled:opacity-40"
                      >
                        Apply
                      </button>
                      <button
                        type="button"
                        onClick={() => void createProfileLoadout(preset)}
                        disabled={Boolean(loadoutBusy)}
                        className="h-8 flex-1 rounded-sm border border-eve-accent/70 px-3 text-[11px] font-semibold uppercase tracking-wider text-eve-accent transition-colors hover:bg-eve-accent/10 disabled:opacity-40"
                      >
                        Create
                      </button>
                    </div>
                  </article>
                );
              })}
            </div>
            <p className="text-xs text-eve-dim">
              Presets change cockpit layout and selected scan parameters only. They do not touch auth, orders, wallet data or local database rows.
            </p>
          </Panel>
        )}

        {activePage === "adaptive" && (
          <Panel title="Adaptive Cockpit">
            <div className="grid grid-cols-1 xl:grid-cols-[minmax(0,1fr)_320px] gap-3">
              <div className="space-y-3">
                <ToggleRow
                  title="Adaptive suggestions"
                  description="Let the app suggest cockpit changes based on local usage patterns. Nothing is uploaded."
                  checked={prefs.adaptiveEnabled}
                  onChange={(checked) => update({ adaptiveEnabled: checked })}
                />
                <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
                  <div className="border border-eve-border/60 bg-eve-dark/35 rounded-sm p-3">
                    <div className="text-[10px] uppercase tracking-widest text-eve-dim">Learned intent</div>
                    <div className="mt-1 text-lg text-eve-accent font-semibold capitalize">{behaviorModel.dominantIntent}</div>
                  </div>
                  <div className="border border-eve-border/60 bg-eve-dark/35 rounded-sm p-3">
                    <div className="text-[10px] uppercase tracking-widest text-eve-dim">Confidence</div>
                    <div className="mt-1 text-lg text-eve-accent font-semibold">{behaviorModel.confidence}%</div>
                  </div>
                  <div className="border border-eve-border/60 bg-eve-dark/35 rounded-sm p-3">
                    <div className="text-[10px] uppercase tracking-widest text-eve-dim">Recommended profile</div>
                    <div className="mt-1 text-sm text-eve-text font-semibold">
                      {COCKPIT_PROFILE_PRESETS.find((preset) => preset.id === behaviorModel.recommendedPresetId)?.name ?? "Learning"}
                    </div>
                  </div>
                </div>
                {adaptiveSuggestions.length === 0 ? (
                  <div className="border border-eve-border/60 bg-eve-dark/35 rounded-sm p-4 text-sm text-eve-dim">
                    No strong cockpit recommendation yet. Use scans, Mission Control, Ledger and route tools for a while; this panel will start suggesting layout changes.
                  </div>
                ) : (
                  <div className="space-y-2">
                    {adaptiveSuggestions.map((suggestion) => (
                      <div key={suggestion.id} className="border border-eve-border/60 bg-eve-dark/35 rounded-sm p-3">
                        <div className="flex items-start justify-between gap-3">
                          <div className="min-w-0">
                            <div className="text-sm font-semibold text-eve-text">{suggestion.title}</div>
                            <p className="mt-1 text-xs text-eve-dim">{suggestion.description}</p>
                          </div>
                          <span className="text-[10px] uppercase tracking-wider text-eve-accent border border-eve-accent/40 rounded-sm px-2 py-1">
                            {suggestion.priority}
                          </span>
                        </div>
                        <div className="mt-3 flex flex-wrap gap-2">
                          <button
                            type="button"
                            onClick={() => setActivePage(suggestion.page)}
                            className="px-3 py-1.5 bg-eve-accent text-eve-dark rounded-sm text-xs font-semibold uppercase tracking-wider"
                          >
                            {suggestion.actionLabel}
                          </button>
                          <button
                            type="button"
                            onClick={() => dismissAdaptiveSuggestion(suggestion.id)}
                            className="px-3 py-1.5 border border-eve-border text-eve-dim hover:text-eve-accent rounded-sm text-xs font-semibold uppercase tracking-wider"
                          >
                            Dismiss
                          </button>
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </div>
              <div className="border border-eve-border/60 bg-eve-dark/35 rounded-sm p-3">
                <div className="text-[10px] uppercase tracking-widest text-eve-dim">Local behavior sample</div>
                <div className="mt-3 space-y-2 max-h-[360px] overflow-auto pr-1">
                  {Object.entries(activityStats.counters).length === 0 ? (
                    <div className="text-xs text-eve-dim">No activity collected yet.</div>
                  ) : (
                    Object.entries(activityStats.counters)
                      .sort((a, b) => b[1] - a[1])
                      .slice(0, 12)
                      .map(([event, count]) => (
                        <div key={event} className="flex items-center justify-between gap-3 text-xs border-b border-eve-border/40 pb-1">
                          <span className="text-eve-dim truncate">{event}</span>
                          <span className="font-mono text-eve-text">{count}</span>
                        </div>
                      ))
                  )}
                </div>
                <button
                  type="button"
                  onClick={resetAdaptiveLearning}
                  className="mt-3 px-3 py-1.5 border border-eve-border text-eve-dim hover:text-eve-accent rounded-sm text-xs font-semibold uppercase tracking-wider"
                >
                  Reset learning
                </button>
              </div>
            </div>
          </Panel>
        )}

        {activePage === "roles" && (
          <Panel title="Role-aware Cockpit">
            <div className="grid grid-cols-1 xl:grid-cols-[minmax(0,1.1fr)_minmax(320px,0.9fr)] gap-3 items-start">
              <div className="rounded-sm border border-eve-border/60 bg-eve-dark/35 p-4 space-y-4">
                <div>
                  <p className="max-w-3xl text-sm leading-6 text-eve-text">
                    Bind a character to a cockpit loadout. When that character becomes active, the app can switch to the matching workspace.
                  </p>
                  <p className="mt-1 text-xs text-eve-dim">
                    This stores UI/workspace preferences only. It does not change ESI tokens, orders, wallet data or character skills.
                  </p>
                </div>

                <div className="grid grid-cols-1 lg:grid-cols-2 gap-3">
                  <div className="lg:col-span-2">
                    <SelectField
                      label="Current character loadout"
                      value={activeCharacterId ? (prefs.roleBindings[String(activeCharacterId)]?.loadoutId || activeLoadoutID) : ""}
                      onChange={(value) => bindCurrentCharacter({ loadoutId: value })}
                      options={[
                        { value: "", label: activeCharacterId ? "No binding" : "Login required" },
                        ...normalizedLoadouts.map((loadout) => ({ value: loadout.id, label: loadout.name || loadout.preferences.name })),
                      ]}
                      hint="Uses existing cockpit loadouts; create one from Presets/Templates first if needed."
                    />
                  </div>
                  <SelectField
                    label="Role hint"
                    value={activeCharacterId ? (prefs.roleBindings[String(activeCharacterId)]?.presetId || "") : ""}
                    onChange={(value) => bindCurrentCharacter({ presetId: value as CockpitProfilePresetID | "" })}
                    options={[
                      { value: "", label: "Unassigned" },
                      ...COCKPIT_PROFILE_PRESETS.map((preset) => ({ value: preset.id, label: preset.name })),
                    ]}
                  />
                  <TextField
                    label="Role label"
                    value={activeCharacterId ? prefs.roleBindings[String(activeCharacterId)]?.label : ""}
                    onChange={(value) => bindCurrentCharacter({ label: value })}
                    placeholder="Jita trader / Hauler / Builder"
                  />
                </div>

                <div className="flex flex-wrap gap-2 border-t border-eve-border/45 pt-3">
                  <button
                    type="button"
                    onClick={() => bindCurrentCharacter({ loadoutId: activeLoadoutID })}
                    disabled={!activeCharacterId}
                    className="px-3 py-1.5 bg-eve-accent text-eve-dark rounded-sm text-xs font-semibold uppercase tracking-wider disabled:opacity-40"
                  >
                    Bind current loadout
                  </button>
                  <button
                    type="button"
                    onClick={bindCurrentTaskToCurrentLoadout}
                    disabled={!activeCharacterId}
                    className="px-3 py-1.5 border border-eve-accent/70 text-eve-accent hover:bg-eve-accent/10 rounded-sm text-xs font-semibold uppercase tracking-wider disabled:opacity-40"
                  >
                    Bind startup task
                  </button>
                  <button
                    type="button"
                    onClick={clearCurrentCharacterBinding}
                    disabled={!activeCharacterId}
                    className="px-3 py-1.5 border border-eve-border text-eve-dim hover:text-eve-accent rounded-sm text-xs font-semibold uppercase tracking-wider disabled:opacity-40"
                  >
                    Clear binding
                  </button>
                </div>
              </div>
              <div className="self-start border border-eve-border/60 bg-eve-dark/35 rounded-sm p-4">
                <div className="text-[10px] uppercase tracking-widest text-eve-dim">Saved bindings</div>
                <div className="mt-3 space-y-2">
                  {Object.values(prefs.roleBindings).length === 0 ? (
                    <div className="rounded-sm border border-dashed border-eve-border/60 bg-eve-panel/35 px-3 py-8 text-center text-xs text-eve-dim">
                      No character bindings yet.
                    </div>
                  ) : (
                    Object.values(prefs.roleBindings).map((binding) => (
                      <div key={binding.characterId} className="border border-eve-border/50 bg-eve-panel/40 rounded-sm px-2 py-1.5 text-xs">
                        <div className="flex items-center justify-between gap-2">
                          <span className="text-eve-text">{binding.label || binding.characterId}</span>
                          <span className="font-mono text-eve-dim">{binding.characterId}</span>
                        </div>
                        <div className="mt-1 text-eve-dim">
                          {binding.presetId || "custom"} {"->"} {normalizedLoadouts.find((loadout) => loadout.id === binding.loadoutId)?.name || binding.loadoutId || "no loadout"}
                        </div>
                        {binding.contextRules.length > 0 && (
                          <div className="mt-2 space-y-1">
                            {binding.contextRules.map((rule) => (
                              <div key={rule.id} className="flex items-center gap-2 border border-eve-border/40 bg-eve-dark/45 rounded-sm px-2 py-1">
                                <span className="text-[10px] uppercase tracking-wider text-eve-accent">{rule.task}</span>
                                <span className="min-w-0 flex-1 truncate text-eve-dim">{rule.label}</span>
                                <button
                                  type="button"
                                  onClick={() => removeContextRule(rule.id)}
                                  className="text-[10px] uppercase tracking-wider text-eve-dim hover:text-eve-error"
                                >
                                  Remove
                                </button>
                              </div>
                            ))}
                          </div>
                        )}
                      </div>
                    ))
                  )}
                </div>
              </div>
            </div>
          </Panel>
        )}

        {activePage === "context" && (
          <Panel title="Context Cockpit">
            <ToggleRow
              title="Context-aware hints"
              description="Show item/route aware helper surfaces: cargo/risk for heavy hauling, build/buy for industry, capital gates for small wallets."
              checked={prefs.contextHintsEnabled}
              onChange={(checked) => update({ contextHintsEnabled: checked })}
            />
            <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
              <div className="border border-eve-border/60 bg-eve-dark/35 rounded-sm p-3">
                <div className="text-sm text-eve-text font-semibold">Heavy item / route</div>
                <p className="mt-1 text-xs text-eve-dim">Surface cargo, trips, DOTLAN and gank-risk controls when selected opportunities are cargo constrained.</p>
              </div>
              <div className="border border-eve-border/60 bg-eve-dark/35 rounded-sm p-3">
                <div className="text-sm text-eve-text font-semibold">Industry item</div>
                <p className="mt-1 text-xs text-eve-dim">Surface build-vs-buy, owned assets, blueprints and structure visibility when an item is production-related.</p>
              </div>
              <div className="border border-eve-border/60 bg-eve-dark/35 rounded-sm p-3">
                <div className="text-sm text-eve-text font-semibold">Capital pressure</div>
                <p className="mt-1 text-xs text-eve-dim">Surface reserve wallet, max exposure and quantity reduction diagnostics before creating a journal trade.</p>
              </div>
            </div>
            <p className="text-xs text-eve-dim">
              Current MVP stores the policy and exposes hints. Deeper per-row context actions plug into Mission Control and Item Intelligence next.
            </p>
          </Panel>
        )}

        {activePage === "templates" && (
          <Panel title="Workspace Templates">
            <div className="space-y-3">
              <div className="flex flex-col gap-3 rounded-sm border border-eve-border/60 bg-eve-dark/35 p-3 md:flex-row md:items-center md:justify-between">
                <div>
                  <div className="text-sm font-semibold text-eve-text">Official workspace templates</div>
                  <p className="mt-1 max-w-3xl text-xs leading-5 text-eve-dim">
                    Built-in cockpit layouts shipped with the app. Community packs are installed from Community Gallery and stay separate from these defaults.
                  </p>
                </div>
                <button
                  type="button"
                  onClick={resetCockpitToDefault}
                  disabled={prefs.layoutLocked}
                  className="h-9 shrink-0 rounded-sm bg-eve-accent px-4 text-xs font-semibold uppercase tracking-wider text-eve-dark transition-colors hover:bg-eve-accent-hover disabled:opacity-40"
                >
                  Reset to default
                </button>
              </div>

              <div className="grid grid-cols-1 2xl:grid-cols-2 gap-3">
                {COCKPIT_WORKSPACE_TEMPLATES.map((template) => (
                  <TemplateCard
                    key={template.id}
                    name={template.name}
                    description={template.description}
                    tags={template.tags}
                    rating={template.rating}
                    favorite={prefs.favoriteTemplates.includes(template.id)}
                    layoutLocked={prefs.layoutLocked}
                    busy={Boolean(loadoutBusy)}
                    onApply={() => void applyTemplate(template.id)}
                    onCreate={() => void createTemplateLoadout(template.id)}
                    onToggleFavorite={() => update({
                      favoriteTemplates: prefs.favoriteTemplates.includes(template.id)
                        ? prefs.favoriteTemplates.filter((item) => item !== template.id)
                        : [...prefs.favoriteTemplates, template.id],
                    })}
                  />
                ))}
              </div>
            </div>
          </Panel>
        )}

        {activePage === "gallery" && (
          <Panel title="Community Layout Gallery">
            <div className="border border-eve-border/60 bg-eve-dark/35 rounded-sm p-4">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <div className="text-sm text-eve-text font-semibold">Remote JSON marketplace</div>
                  <p className="mt-1 text-xs text-eve-dim">
                    Loads public cockpit packs from GitHub. Layouts are JSON only and do not include tokens, wallet history or local database data.
                  </p>
                </div>
                <div className="flex flex-wrap gap-2">
                  <button
                    type="button"
                    onClick={() => void refreshMarketplace()}
                    disabled={marketplaceLoading}
                    className="px-3 py-1.5 border border-eve-accent/70 text-eve-accent hover:bg-eve-accent/10 rounded-sm text-xs font-semibold uppercase tracking-wider disabled:opacity-40"
                  >
                    {marketplaceLoading ? "Loading" : "Refresh"}
                  </button>
                  <button
                    type="button"
                    onClick={resetCockpitToDefault}
                    disabled={prefs.layoutLocked}
                    className="px-3 py-1.5 bg-eve-accent text-eve-dark hover:bg-eve-accent-hover rounded-sm text-xs font-semibold uppercase tracking-wider disabled:opacity-40"
                  >
                    Reset to default
                  </button>
                </div>
              </div>
              <p className="mt-1 text-xs text-eve-dim">
                Catalog: {COCKPIT_MARKETPLACE_URL}
              </p>
              {marketplaceLoadedAt && <p className="mt-1 text-[11px] text-eve-dim">Updated {marketplaceLoadedAt}</p>}
              {marketplaceError && <p className="mt-2 text-xs text-eve-error">{marketplaceError}. Official templates are available in Workspace Templates.</p>}
            </div>
            {marketplaceEntries.length > 0 ? (
              <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-4 gap-3">
                {marketplaceEntries.map((entry) => (
                <button
                  key={entry.id}
                  type="button"
                  onClick={() => void installMarketplaceEntry(entry)}
                  disabled={Boolean(loadoutBusy)}
                  className="text-left border border-eve-accent/40 bg-eve-accent/5 hover:border-eve-accent rounded-sm p-3 transition-colors disabled:opacity-50"
                >
                  <div className="flex items-center justify-between gap-2">
                    <div className="text-sm text-eve-text font-semibold truncate">{entry.name}</div>
                    {entry.verified && <span className="text-[9px] uppercase tracking-wider text-eve-accent">Verified</span>}
                  </div>
                    <div className="mt-1 text-[11px] text-eve-dim">by {entry.author} · rating {entry.rating.toFixed(1)}</div>
                  <div className="mt-2 text-xs text-eve-dim line-clamp-3">{entry.description}</div>
                  <div className="mt-2 flex flex-wrap gap-1">
                    {entry.tags.slice(0, 4).map((tag) => (
                      <span key={tag} className="px-1.5 py-0.5 border border-eve-border/50 rounded-sm text-[9px] text-eve-dim">
                        {tag}
                      </span>
                    ))}
                  </div>
                </button>
                ))}
              </div>
            ) : (
              <div className="flex flex-col gap-3 rounded-sm border border-dashed border-eve-border/70 bg-eve-dark/30 p-4 md:flex-row md:items-center md:justify-between">
                <div>
                  <div className="text-sm font-semibold text-eve-text">No community layouts loaded</div>
                  <p className="mt-1 text-xs leading-5 text-eve-dim">
                    Use Refresh for the remote marketplace, or open Workspace Templates for official built-in layouts.
                  </p>
                </div>
                <button
                  type="button"
                  onClick={() => setActivePage("templates")}
                  className="h-9 shrink-0 rounded-sm border border-eve-accent/70 px-3 text-xs font-semibold uppercase tracking-wider text-eve-accent transition-colors hover:bg-eve-accent/10"
                >
                  Open templates
                </button>
              </div>
            )}
          </Panel>
        )}

        {activePage === "navigation" && (
          <Panel title="Main Navigation">
            <div className="space-y-2">
              {prefs.mainTabOrder.map((tab, index) => (
                <div key={tab} className="flex items-center gap-2 border border-eve-border/60 bg-eve-dark/35 rounded-sm px-3 py-2">
                  <input
                    type="checkbox"
                    checked={!prefs.hiddenMainTabs.includes(tab)}
                    onChange={(event) => setHiddenTab(tab, !event.target.checked)}
                    className="accent-eve-accent"
                  />
                  <div className="flex-1 min-w-0">
                    <div className="text-sm text-eve-text">{labelForTab(tab)}</div>
                    <div className="text-[10px] uppercase tracking-wider text-eve-dim">{MAIN_TAB_META[tab].group}</div>
                  </div>
                  <button type="button" onClick={() => moveTab(tab, -1)} disabled={index === 0} className="px-2 py-1 border border-eve-border rounded-sm text-xs text-eve-dim hover:text-eve-accent disabled:opacity-30">Up</button>
                  <button type="button" onClick={() => moveTab(tab, 1)} disabled={index === prefs.mainTabOrder.length - 1} className="px-2 py-1 border border-eve-border rounded-sm text-xs text-eve-dim hover:text-eve-accent disabled:opacity-30">Down</button>
                </div>
              ))}
            </div>
          </Panel>
        )}

        {activePage === "layout" && (
          <Panel title="Per-tab Layout Settings">
            <div className="overflow-hidden rounded-sm border border-eve-border/60 bg-eve-dark/35">
              <div className="hidden xl:grid grid-cols-[minmax(190px,1fr)_150px_170px_170px_92px] gap-2 border-b border-eve-border/60 bg-eve-panel/60 px-3 py-2 text-[10px] font-semibold uppercase tracking-widest text-eve-dim">
                <div>Tab</div>
                <div>Density</div>
                <div>Columns</div>
                <div>Filters</div>
                <div className="text-right">Action</div>
              </div>
              {MAIN_TAB_IDS.map((tabID) => {
                const layout = getCockpitTabLayout(prefs, tabID);
                return (
                  <div
                    key={tabID}
                    className="grid grid-cols-1 gap-2 border-b border-eve-border/45 px-3 py-3 last:border-b-0 xl:grid-cols-[minmax(190px,1fr)_150px_170px_170px_92px] xl:items-center xl:py-2"
                  >
                    <div className="min-w-0">
                      <div className="truncate text-sm font-semibold text-eve-text">{labelForTab(tabID)}</div>
                      <div className="mt-0.5 text-[10px] uppercase tracking-wider text-eve-dim">{MAIN_TAB_META[tabID].group}</div>
                    </div>
                    <div>
                      <div className="mb-1 text-[9px] uppercase tracking-widest text-eve-dim xl:hidden">Density</div>
                      <CompactSelect
                        ariaLabel={`${labelForTab(tabID)} density`}
                        value={layout.density}
                        onChange={(value) => updateTabLayout(tabID, { density: value as CockpitPreferences["tabLayouts"][MainTabId]["density"] })}
                        options={[
                          { value: "inherit", label: "Inherit" },
                          { value: "comfortable", label: "Comfortable" },
                          { value: "compact", label: "Compact" },
                          { value: "dense", label: "Dense" },
                        ]}
                      />
                    </div>
                    <div>
                      <div className="mb-1 text-[9px] uppercase tracking-widest text-eve-dim xl:hidden">Columns</div>
                      <CompactSelect
                        ariaLabel={`${labelForTab(tabID)} columns`}
                        value={layout.columnPreset}
                        onChange={(value) => updateTabLayout(tabID, { columnPreset: value as CockpitColumnPreset })}
                        options={COCKPIT_COLUMN_PRESETS.map((preset) => ({ value: preset, label: columnPresetLabels[preset] }))}
                      />
                    </div>
                    <div>
                      <div className="mb-1 text-[9px] uppercase tracking-widest text-eve-dim xl:hidden">Filters</div>
                      <CompactSelect
                        ariaLabel={`${labelForTab(tabID)} filters`}
                        value={layout.filterPreset}
                        onChange={(value) => applyFilterPreset(tabID, value as CockpitFilterPreset)}
                        options={COCKPIT_FILTER_PRESETS.map((preset) => ({ value: preset, label: filterPresetLabels[preset] }))}
                      />
                    </div>
                    <div className="flex justify-end">
                      <button
                        type="button"
                        onClick={() => resetTabLayout(tabID)}
                        className="h-8 rounded-sm border border-eve-border px-2.5 text-[10px] font-semibold uppercase tracking-wider text-eve-dim transition-colors hover:border-eve-accent/50 hover:text-eve-accent"
                      >
                        Reset
                      </button>
                    </div>
                  </div>
                );
              })}
            </div>
            <p className="text-xs text-eve-dim">Saved in the active cockpit loadout and exported with workspace packs.</p>
          </Panel>
        )}

        {activePage === "density" && (
          <Panel title="Interface Density">
            <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
              {(["comfortable", "compact", "dense"] as CockpitDensity[]).map((density) => (
                <button
                  key={density}
                  type="button"
                  onClick={() => update({ density })}
                  className={`text-left border rounded-sm p-3 transition-colors ${
                    prefs.density === density
                      ? "border-eve-accent bg-eve-accent/10 text-eve-accent"
                      : "border-eve-border bg-eve-dark/35 text-eve-text hover:border-eve-accent/50"
                  }`}
                >
                  <div className="text-sm font-semibold">{densityLabel(density)}</div>
                  <div className="mt-1 text-xs text-eve-dim">
                    {density === "comfortable" ? "Readable spacing for normal use." : density === "compact" ? "More data with restrained padding." : "Maximum information density."}
                  </div>
                </button>
              ))}
            </div>
          </Panel>
        )}

        {activePage === "panels" && (
          <Panel title="Panel Visibility">
            <ToggleRow
              title="Advanced filters"
              description="Hide advanced filter sections in Scanner and Station Trading."
              checked={!prefs.hiddenPanels.advancedFilters}
              onChange={(checked) => setPanelHidden("advancedFilters", !checked)}
            />
            <ToggleRow
              title="Station AI assistant"
              description="Show the Station Trading assistant panel."
              checked={!prefs.hiddenPanels.stationAiAssistant}
              onChange={(checked) => setPanelHidden("stationAiAssistant", !checked)}
            />
            <ToggleRow
              title="Help buttons"
              description="Reserved for the next pass: hide contextual help icons across modules."
              checked={!prefs.hiddenPanels.helpButtons}
              onChange={(checked) => setPanelHidden("helpButtons", !checked)}
            />
            <ToggleRow
              title="Quick actions bar"
              description="Show the configurable top-bar action buttons."
              checked={!prefs.hiddenPanels.quickActions}
              onChange={(checked) => setPanelHidden("quickActions", !checked)}
            />
            <ToggleRow
              title="Status bar"
              description="Show SDE/API status indicators in the header."
              checked={!prefs.hiddenPanels.statusBar}
              onChange={(checked) => setPanelHidden("statusBar", !checked)}
            />
            <ToggleRow
              title="Tab action bars"
              description="Show per-tab helper strips such as auto-refresh and diagnostic banners."
              checked={!prefs.hiddenPanels.tabActionBars}
              onChange={(checked) => setPanelHidden("tabActionBars", !checked)}
            />
          </Panel>
        )}

        {activePage === "columns" && (
          <Panel title="Column Presets">
            <div className="grid grid-cols-1 xl:grid-cols-2 gap-3">
              {MAIN_TAB_IDS.map((tabID) => {
                const layout = getCockpitTabLayout(prefs, tabID);
                return (
                  <div key={tabID} className="border border-eve-border/60 bg-eve-dark/35 rounded-sm p-3">
                    <SelectField
                      label={labelForTab(tabID)}
                      value={layout.columnPreset}
                      onChange={(value) => updateTabLayout(tabID, { columnPreset: value as CockpitColumnPreset })}
                      options={COCKPIT_COLUMN_PRESETS.map((preset) => ({ value: preset, label: columnPresetLabels[preset] }))}
                      hint={
                        tabID === "region"
                          ? "Regional presets can switch between default and hauling-oriented table profiles."
                          : "Saved as cockpit profile metadata; module-level column panels still keep exact column order."
                      }
                    />
                    <button
                      type="button"
                      onClick={() => resetTabLayout(tabID)}
                      className="mt-2 px-2 py-1 border border-eve-border rounded-sm text-[10px] text-eve-dim hover:text-eve-accent uppercase tracking-wider"
                    >
                      Reset tab
                    </button>
                  </div>
                );
              })}
            </div>
          </Panel>
        )}

        {activePage === "filters" && (
          <Panel title="Filter Presets Per Tab">
            <div className="grid grid-cols-1 xl:grid-cols-2 gap-3">
              {MAIN_TAB_IDS.map((tabID) => {
                const layout = getCockpitTabLayout(prefs, tabID);
                return (
                  <div key={tabID} className="border border-eve-border/60 bg-eve-dark/35 rounded-sm p-3 space-y-2">
                    <SelectField
                      label={labelForTab(tabID)}
                      value={layout.filterPreset}
                      onChange={(value) => applyFilterPreset(tabID, value as CockpitFilterPreset)}
                      options={COCKPIT_FILTER_PRESETS.map((preset) => ({ value: preset, label: filterPresetLabels[preset] }))}
                    />
                    <div className="text-[11px] text-eve-dim">
                      Selecting a preset applies the relevant shared scan parameters now and stores the preset choice in this cockpit loadout.
                    </div>
                    <button
                      type="button"
                      onClick={() => resetTabLayout(tabID)}
                      className="px-2 py-1 border border-eve-border rounded-sm text-[10px] text-eve-dim hover:text-eve-accent uppercase tracking-wider"
                    >
                      Reset tab
                    </button>
                  </div>
                );
              })}
            </div>
          </Panel>
        )}

        {activePage === "startup" && (
          <Panel title="Startup / Quick Actions">
            <FieldGrid>
              <SelectField
                label="Default startup view"
                value={prefs.startupTab}
                onChange={(value) => update({ startupTab: value as CockpitPreferences["startupTab"] })}
                options={[
                  { value: "last", label: "Remember last active tab" },
                  ...MAIN_TAB_IDS.map((tabID) => ({ value: tabID, label: labelForTab(tabID) })),
                ]}
                hint="Used when the app starts or when this loadout is activated."
              />
            </FieldGrid>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
              {COCKPIT_QUICK_ACTIONS.map((action) => (
                <ToggleRow
                  key={action}
                  title={quickActionLabels[action].label}
                  description={quickActionLabels[action].description}
                  checked={prefs.quickActions.includes(action)}
                  onChange={(checked) => setQuickAction(action, checked)}
                />
              ))}
            </div>
          </Panel>
        )}

        {activePage === "scanner" && (
          <Panel title="Scanner / Flipper Defaults">
            <FieldGrid>
              <TextField label="Home system" value={scanParams.system_name} onChange={(value) => setScanParam("system_name", value)} placeholder="Jita" />
              <NumberField label="Buy radius" value={scanParams.buy_radius} min={0} max={50} onChange={(value) => setScanParam("buy_radius", value)} />
              <NumberField label="Sell radius" value={scanParams.sell_radius} min={0} max={50} onChange={(value) => setScanParam("sell_radius", value)} />
              <NumberField label="Minimum margin %" value={scanParams.min_margin} min={0} max={1000} step={0.1} onChange={(value) => setScanParam("min_margin", value)} />
              <NumberField label="Minimum daily volume" value={scanParams.min_daily_volume} min={0} onChange={(value) => setScanParam("min_daily_volume", value)} />
              <NumberField label="Maximum investment" value={scanParams.max_investment} min={0} onChange={(value) => setScanParam("max_investment", value)} />
              <NumberField label="Minimum item profit" value={scanParams.min_item_profit} min={0} onChange={(value) => setScanParam("min_item_profit", value)} />
              <NumberField label="Max DOS" value={scanParams.max_dos} min={0} step={0.1} onChange={(value) => setScanParam("max_dos", value)} hint="Days of supply cap for history-aware filtering." />
              <NumberField label="Average price window" value={scanParams.avg_price_period} min={1} max={365} onChange={(value) => setScanParam("avg_price_period", value)} />
            </FieldGrid>
          </Panel>
        )}

        {activePage === "regional" && (
          <Panel title="Regional Trade Defaults">
            <FieldGrid>
              <TextField label="Target marketplace system" value={scanParams.target_market_system} onChange={(value) => setScanParam("target_market_system", value)} placeholder="Jita / Keepstar system" />
              <TextField label="Target region" value={scanParams.target_region} onChange={(value) => setScanParam("target_region", value)} placeholder="Optional" />
              <NumberField label="Cargo capacity" value={scanParams.cargo_capacity} min={0} onChange={(value) => setScanParam("cargo_capacity", value)} />
              <NumberField label="Minimum margin %" value={scanParams.min_margin} min={0} max={1000} step={0.1} onChange={(value) => setScanParam("min_margin", value)} />
              <NumberField label="Minimum daily volume" value={scanParams.min_daily_volume} min={0} onChange={(value) => setScanParam("min_daily_volume", value)} />
              <NumberField label="Shipping ISK / m3 / jump" value={scanParams.shipping_cost_per_m3_jump} min={0} step={0.01} onChange={(value) => setScanParam("shipping_cost_per_m3_jump", value)} />
            </FieldGrid>
            <div className="space-y-2">
              <ToggleRow
                title="Sell-order revenue mode"
                description="Use destination sell orders for import-and-list analysis instead of instant buy orders."
                checked={Boolean(scanParams.sell_order_mode)}
                onChange={(checked) => setScanParam("sell_order_mode", checked)}
              />
              <ToggleRow
                title="Diagnostic mode"
                description="Show rejected/negative regional rows for private-structure and filter debugging."
                checked={Boolean(scanParams.regional_diagnostic_mode)}
                onChange={(checked) => setScanParam("regional_diagnostic_mode", checked)}
              />
              <ToggleRow
                title="Include accessible player structures"
                description="Use authenticated ESI structure visibility when available."
                checked={Boolean(scanParams.include_structures)}
                onChange={(checked) => setScanParam("include_structures", checked)}
              />
            </div>
          </Panel>
        )}

        {activePage === "station" && (
          <Panel title="Station Trading Cockpit">
            <FieldGrid>
              <NumberField label="Minimum margin %" value={scanParams.min_margin} min={0} max={1000} step={0.1} onChange={(value) => setScanParam("min_margin", value)} />
              <NumberField label="Minimum daily volume" value={scanParams.min_daily_volume} min={0} onChange={(value) => setScanParam("min_daily_volume", value)} />
              <NumberField label="Maximum investment" value={scanParams.max_investment} min={0} onChange={(value) => setScanParam("max_investment", value)} />
              <NumberField label="Minimum item profit" value={scanParams.min_item_profit} min={0} onChange={(value) => setScanParam("min_item_profit", value)} />
              <NumberField label="Minimum period ROI %" value={scanParams.min_period_roi} min={0} max={1000} step={0.1} onChange={(value) => setScanParam("min_period_roi", value)} />
              <NumberField label="Max DOS" value={scanParams.max_dos} min={0} step={0.1} onChange={(value) => setScanParam("max_dos", value)} />
            </FieldGrid>
            <div className="space-y-2">
              <ToggleRow
                title="Station advanced filters"
                description="Show deeper station filters on the working tab."
                checked={!prefs.hiddenPanels.advancedFilters}
                onChange={(checked) => setPanelHidden("advancedFilters", !checked)}
              />
              <ToggleRow
                title="Station AI assistant"
                description="Show the assistant in Station Trading."
                checked={!prefs.hiddenPanels.stationAiAssistant}
                onChange={(checked) => setPanelHidden("stationAiAssistant", !checked)}
              />
            </div>
          </Panel>
        )}

        {activePage === "route" && (
          <Panel title="Route Builder Cockpit">
            <FieldGrid>
              <SelectField
                label="Route mode"
                value={scanParams.route_mode}
                onChange={(value) => setScanParam("route_mode", value)}
                options={[
                  { value: "balanced", label: "Balanced / max ISK/hour" },
                  { value: "fastest", label: "Fastest" },
                  { value: "safest", label: "Safest" },
                ]}
              />
              <SelectField
                label="Ship profile"
                value={scanParams.route_ship_profile}
                onChange={(value) => setScanParam("route_ship_profile", value)}
                options={[
                  { value: "custom", label: "Custom" },
                  { value: "fast_frigate", label: "Fast frigate" },
                  { value: "sunesis", label: "Sunesis" },
                  { value: "blockade_runner", label: "Blockade Runner" },
                  { value: "deep_space_transport", label: "Deep Space Transport" },
                  { value: "freighter", label: "Freighter" },
                ]}
              />
              <NumberField label="Route cargo capacity" value={scanParams.route_cargo_capacity} min={0} onChange={(value) => setScanParam("route_cargo_capacity", value)} />
              <NumberField label="Minimum hops" value={scanParams.route_min_hops} min={0} max={100} onChange={(value) => setScanParam("route_min_hops", value)} />
              <NumberField label="Maximum hops" value={scanParams.route_max_hops} min={0} max={100} onChange={(value) => setScanParam("route_max_hops", value)} />
              <NumberField label="Minimum ISK / jump" value={scanParams.route_min_isk_per_jump} min={0} onChange={(value) => setScanParam("route_min_isk_per_jump", value)} />
              <NumberField label="Minutes per jump" value={scanParams.route_minutes_per_jump} min={0} step={0.1} onChange={(value) => setScanParam("route_minutes_per_jump", value)} />
              <NumberField label="Dock minutes" value={scanParams.route_dock_minutes} min={0} step={0.1} onChange={(value) => setScanParam("route_dock_minutes", value)} />
              <NumberField label="Safety delay %" value={scanParams.route_safety_delay_percent} min={0} max={500} step={0.1} onChange={(value) => setScanParam("route_safety_delay_percent", value)} />
              <NumberField label="Minimum route security" value={scanParams.min_route_security} min={0} max={1} step={0.01} onChange={(value) => setScanParam("min_route_security", value)} />
              <TextField label="Target system" value={scanParams.route_target_system_name} onChange={(value) => setScanParam("route_target_system_name", value)} placeholder="Optional" />
            </FieldGrid>
            <ToggleRow
              title="Allow empty hops"
              description="Permit route rows that include repositioning/empty hauling legs."
              checked={Boolean(scanParams.route_allow_empty_hops)}
              onChange={(checked) => setScanParam("route_allow_empty_hops", checked)}
            />
          </Panel>
        )}

        {activePage === "industry" && (
          <Panel title="Industry Cockpit">
            <div className="space-y-2">
              <ToggleRow
                title="Include accessible structures"
                description="Allow Industry selectors to use private/corp structures visible to the authenticated character."
                checked={Boolean(scanParams.include_structures)}
                onChange={(checked) => setScanParam("include_structures", checked)}
              />
              <ToggleRow
                title="Advanced filters"
                description="Keep dense Industry controls visible when module-specific advanced sections are added."
                checked={!prefs.hiddenPanels.advancedFilters}
                onChange={(checked) => setPanelHidden("advancedFilters", !checked)}
              />
            </div>
            <p className="text-xs text-eve-dim">
              Production-chain and PI controls live inside their module pages; this cockpit page keeps cross-module visibility and structure behavior centralized.
            </p>
          </Panel>
        )}

        {activePage === "ledger" && (
          <Panel title="Ledger Cockpit">
            <FieldGrid>
              <NumberField label="Sales tax %" value={scanParams.sales_tax_percent} min={0} max={100} step={0.01} onChange={(value) => setScanParam("sales_tax_percent", value)} />
              <NumberField label="Broker fee %" value={scanParams.broker_fee_percent} min={0} max={100} step={0.01} onChange={(value) => setScanParam("broker_fee_percent", value)} />
              <NumberField label="Buy broker fee %" value={scanParams.buy_broker_fee_percent} min={0} max={100} step={0.01} onChange={(value) => setScanParam("buy_broker_fee_percent", value)} />
              <NumberField label="Sell broker fee %" value={scanParams.sell_broker_fee_percent} min={0} max={100} step={0.01} onChange={(value) => setScanParam("sell_broker_fee_percent", value)} />
            </FieldGrid>
            <ToggleRow
              title="Split trade fees"
              description="Use separate buy/sell fee inputs across modules that support the shared tax profile."
              checked={Boolean(scanParams.split_trade_fees)}
              onChange={(checked) => setScanParam("split_trade_fees", checked)}
            />
          </Panel>
        )}

        {activePage === "mission" && (
          <Panel title="Mission Control Defaults">
            <FieldGrid>
              <NumberField label="Maximum ISK per trade" value={scanParams.max_investment} min={0} onChange={(value) => setScanParam("max_investment", value)} />
              <NumberField label="Cargo capacity" value={scanParams.cargo_capacity} min={0} onChange={(value) => setScanParam("cargo_capacity", value)} />
              <NumberField label="Route cargo capacity" value={scanParams.route_cargo_capacity} min={0} onChange={(value) => setScanParam("route_cargo_capacity", value)} />
              <NumberField label="Route minutes per jump" value={scanParams.route_minutes_per_jump} min={0} step={0.1} onChange={(value) => setScanParam("route_minutes_per_jump", value)} />
              <NumberField label="Safety delay %" value={scanParams.route_safety_delay_percent} min={0} max={500} step={0.1} onChange={(value) => setScanParam("route_safety_delay_percent", value)} />
              <SelectField
                label="Route decision mode"
                value={scanParams.route_mode}
                onChange={(value) => setScanParam("route_mode", value)}
                options={[
                  { value: "balanced", label: "Balanced / max ISK/hour" },
                  { value: "fastest", label: "Fastest" },
                  { value: "safest", label: "Safest" },
                ]}
              />
            </FieldGrid>
          </Panel>
        )}

        {activePage === "plex" && (
          <Panel title="PLEX+ Informer">
            <div className="text-sm text-eve-text">PLEX+ is now a profile-side informer, not a main workspace tab.</div>
            <p className="text-xs text-eve-dim">
              Its market math remains isolated so another developer can continue that module without touching the main cockpit engine.
            </p>
            <FieldGrid>
              <NumberField label="Sales tax %" value={scanParams.sales_tax_percent} min={0} max={100} step={0.01} onChange={(value) => setScanParam("sales_tax_percent", value)} />
              <NumberField label="Broker fee %" value={scanParams.broker_fee_percent} min={0} max={100} step={0.01} onChange={(value) => setScanParam("broker_fee_percent", value)} />
            </FieldGrid>
          </Panel>
        )}

        {activePage === "share" && (
          <Panel title="Import / Export">
            <div className="border border-amber-500/40 bg-amber-950/20 rounded-sm px-3 py-2 text-xs text-amber-100">
              Workspace packs include cockpit loadouts, theme and scan parameters only. They do not export ESI tokens, cookies, wallet history, orders, assets, journal trades or local database rows.
            </div>
            <div className="flex flex-wrap gap-2">
              <button type="button" onClick={() => void generateActiveExport()} className="px-3 py-1.5 bg-eve-accent text-eve-dark rounded-sm text-xs font-semibold uppercase tracking-wider">
                Export active loadout
              </button>
              <button type="button" onClick={() => void generatePackExport()} className="px-3 py-1.5 border border-eve-accent/70 text-eve-accent hover:bg-eve-accent/10 rounded-sm text-xs font-semibold uppercase tracking-wider">
                Export all loadouts pack
              </button>
              <button type="button" onClick={() => void generateShareCode()} className="px-3 py-1.5 border border-eve-accent/70 text-eve-accent hover:bg-eve-accent/10 rounded-sm text-xs font-semibold uppercase tracking-wider">
                Copy Discord share code
              </button>
              <button
                type="button"
                onClick={resetCockpitToDefault}
                disabled={prefs.layoutLocked}
                className="px-3 py-1.5 border border-eve-border text-eve-dim hover:text-eve-accent rounded-sm text-xs font-semibold uppercase tracking-wider disabled:opacity-40"
              >
                Reset cockpit
              </button>
            </div>
            <textarea
              value={exportText}
              onChange={(event) => setExportText(event.target.value)}
              placeholder="Exported workspace JSON or share code appears here."
              className="w-full min-h-[150px] px-3 py-2 bg-eve-input border border-eve-border rounded-sm text-xs font-mono text-eve-text"
            />
            <textarea
              value={importText}
              onChange={(event) => {
                setImportText(event.target.value);
                setImportPreview(null);
              }}
              placeholder="Paste workspace JSON, workspace pack or EFC1 share code here, then preview and install."
              className="w-full min-h-[150px] px-3 py-2 bg-eve-input border border-eve-border rounded-sm text-xs font-mono text-eve-text"
            />
            <label className="flex items-center gap-2 text-xs text-eve-dim">
              <input
                type="checkbox"
                checked={applyImportedScanParams}
                onChange={(event) => setApplyImportedScanParams(event.target.checked)}
                className="accent-eve-accent"
              />
              Apply imported scan parameters
            </label>
            <div className="flex flex-wrap gap-2">
              <button type="button" onClick={previewImport} className="px-3 py-1.5 border border-eve-border text-eve-dim hover:text-eve-accent rounded-sm text-xs font-semibold uppercase tracking-wider">
                Preview import
              </button>
              <button
                type="button"
                onClick={() => void installImport()}
                disabled={!importText.trim() || Boolean(loadoutBusy)}
                className="px-3 py-1.5 border border-eve-accent/60 text-eve-accent hover:bg-eve-accent/10 rounded-sm text-xs font-semibold uppercase tracking-wider disabled:opacity-40"
              >
                Install loadout pack
              </button>
            </div>
            {importPreview && (
              <div className="border border-eve-border/60 bg-eve-dark/35 rounded-sm p-3 space-y-2">
                <div className="flex flex-wrap items-center gap-2 text-xs">
                  <span className="text-eve-accent font-semibold uppercase tracking-wider">
                    {importPreview.kind === "pack" ? "Workspace pack" : "Single loadout"}
                  </span>
                  <span className="text-eve-dim">{importPreview.loadouts.length} loadout{importPreview.loadouts.length === 1 ? "" : "s"}</span>
                  <span className={importPreview.scanParams ? "text-emerald-300" : "text-amber-200"}>
                    {importPreview.scanParams ? "scan params included" : "no scan params"}
                  </span>
                  <span className={importPreview.theme ? "text-emerald-300" : "text-eve-dim"}>
                    {importPreview.theme ? "theme included" : "no theme"}
                  </span>
                </div>
                <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-2">
                  {importPreview.loadouts.slice(0, 12).map((loadout, index) => (
                    <div key={`${loadout.name}-${index}`} className="border border-eve-border/50 rounded-sm px-2 py-1.5 text-xs text-eve-text bg-eve-panel/35">
                      {loadout.name}
                      {loadout.active && <span className="ml-2 text-eve-accent">active</span>}
                    </div>
                  ))}
                </div>
                {importPreview.loadouts.length > 12 && (
                  <div className="text-xs text-eve-dim">+{importPreview.loadouts.length - 12} more loadouts</div>
                )}
                {importPreview.warnings.map((warning) => (
                  <div key={warning} className="text-xs text-amber-200">{warning}</div>
                ))}
              </div>
            )}
            {status && <div className="text-xs text-eve-dim">{status}</div>}
          </Panel>
        )}
      </main>
    </div>
  );
}
