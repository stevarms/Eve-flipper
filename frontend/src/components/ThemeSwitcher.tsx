import { useState } from "react";
import type { ReactNode } from "react";
import { Check, Pencil, Plus, Settings as SettingsIcon, Upload } from "lucide-react";
import { useI18n, type TranslationKey } from "../lib/i18n";
import {
  useTheme,
  PALETTES,
  FONT_SIZE_PX,
  PALETTE_COLOR_FIELDS,
  readCurrentColors,
  deriveAndApply,
  validateImportedPalette,
  type ThemeMode,
  type FontSize,
  type CustomPalette,
  type CustomPaletteColors,
} from "../lib/useTheme";
import { Modal } from "./Modal";
import type { InterfacePage } from "@/lib/cockpitInterfacePages";

const MODES: { id: ThemeMode; icon: string; nameKey: TranslationKey }[] = [
  { id: "dark", icon: "\u{1F319}", nameKey: "themeDark" },
  { id: "light", icon: "\u{2600}\u{FE0F}", nameKey: "themeLight" },
  { id: "auto", icon: "\u{1F4BB}", nameKey: "themeAuto" },
];

const FONT_SIZES: { id: FontSize; label: string }[] = [
  { id: "xs", label: "XS" },
  { id: "sm", label: "S" },
  { id: "md", label: "M" },
  { id: "lg", label: "L" },
  { id: "xl", label: "XL" },
];

type SettingsTab = "theme" | "settings" | `interface:${InterfacePage}`;

interface ThemeSwitcherProps {
  interfaceContent?: ReactNode;
  interfacePages?: { id: InterfacePage; label: string }[];
  activeInterfacePage?: InterfacePage;
  onInterfacePageChange?: (page: InterfacePage) => void;
  settingsContent?: ReactNode;
}

export function ThemeSwitcher({
  interfaceContent,
  interfacePages = [],
  activeInterfacePage = "overview",
  onInterfacePageChange,
  settingsContent,
}: ThemeSwitcherProps) {
  const { t } = useI18n();
  const {
    mode,
    palette,
    fontSize,
    customPalettes,
    setMode,
    setPalette,
    setFontSize,
    saveCustomPalette,
    deleteCustomPalette,
    reapplyTheme,
  } = useTheme();

  const [open, setOpen] = useState(false);
  const [tab, setTab] = useState<SettingsTab>("theme");
  const [editing, setEditing] = useState<{ cp: CustomPalette; isNew: boolean } | null>(null);
  const [toast, setToast] = useState("");
  const isSettingsHub = Boolean(interfaceContent || settingsContent);
  const activeCustom = customPalettes.find((p) => p.id === palette);

  const close = () => {
    if (editing) {
      reapplyTheme();
      setEditing(null);
    }
    setOpen(false);
  };

  const showToast = (message: string) => {
    setToast(message);
    window.setTimeout(() => setToast(""), 2200);
  };

  const openTheme = () => {
    setEditing(null);
    setTab("theme");
  };

  const openSettings = () => {
    setEditing(null);
    setTab("settings");
  };

  const openInterface = (page: InterfacePage) => {
    setEditing(null);
    onInterfacePageChange?.(page);
    setTab(`interface:${page}`);
  };

  function handleNewPalette() {
    const colors = readCurrentColors();
    const cp: CustomPalette = {
      id: `custom_${Date.now()}`,
      name: t("themeNewPalette"),
      dark: { ...colors },
      light: { ...colors },
    };
    setTab("theme");
    setEditing({ cp, isNew: true });
    deriveAndApply(document.documentElement, colors);
  }

  function handleEditPalette(cp: CustomPalette) {
    setTab("theme");
    setEditing({ cp: deepClone(cp), isNew: false });
  }

  function handleSavePalette(cp: CustomPalette) {
    saveCustomPalette(cp);
    setPalette(cp.id);
    setEditing(null);
  }

  function handleDeletePalette(id: string) {
    deleteCustomPalette(id);
    setEditing(null);
  }

  function handleCancelEdit() {
    reapplyTheme();
    setEditing(null);
  }

  async function handleImportPalette() {
    try {
      const text = await navigator.clipboard.readText();
      const data = JSON.parse(text);
      const cp = validateImportedPalette(data);
      if (!cp) {
        showToast(t("themeImportError"));
        return;
      }
      saveCustomPalette(cp);
      setPalette(cp.id);
      showToast(t("themeImported"));
    } catch {
      showToast(t("themeImportError"));
    }
  }

  async function handleExportPalette(cp: CustomPalette) {
    const exportData = { name: cp.name, dark: cp.dark, light: cp.light };
    try {
      await navigator.clipboard.writeText(JSON.stringify(exportData, null, 2));
      showToast(t("themeExported"));
    } catch {
      showToast(t("themeExportError"));
    }
  }

  const body = editing ? (
    <PaletteEditor
      cp={editing.cp}
      isNew={editing.isNew}
      onSave={handleSavePalette}
      onDelete={handleDeletePalette}
      onCancel={handleCancelEdit}
      onExport={handleExportPalette}
      onToast={showToast}
    />
  ) : tab === "theme" ? (
    <ThemePanel
      mode={mode}
      palette={palette}
      fontSize={fontSize}
      customPalettes={customPalettes}
      activeCustom={activeCustom}
      setMode={setMode}
      setPalette={setPalette}
      setFontSize={setFontSize}
      onNewPalette={handleNewPalette}
      onEditPalette={handleEditPalette}
      onImportPalette={handleImportPalette}
      onExportPalette={handleExportPalette}
    />
  ) : tab === "settings" ? (
    settingsContent
  ) : (
    interfaceContent
  );

  return (
    <div className="relative">
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="flex items-center justify-center h-[34px] w-[34px] rounded-sm bg-eve-panel border border-eve-border hover:border-eve-accent/50 transition-colors cursor-pointer"
        title={isSettingsHub ? t("settingsHubTitle") : t("themeTitle")}
        aria-label={isSettingsHub ? t("settingsHubTitle") : t("themeTitle")}
      >
        <SettingsIcon className="w-4 h-4 text-eve-dim" aria-hidden="true" />
      </button>

      {toast && (
        <div className="absolute right-0 top-[calc(100%+6px)] z-[60] px-3 py-1.5 rounded-sm bg-eve-accent text-eve-dark text-[11px] font-medium whitespace-nowrap shadow-lg">
          {toast}
        </div>
      )}

      <Modal open={open} onClose={close} title={isSettingsHub ? t("settingsHubTitle") : t("themeTitle")} width="max-w-7xl" allowFullscreen>
        <div className="grid grid-cols-1 md:grid-cols-[245px_minmax(0,1fr)] h-[82vh] md:h-[76vh] min-h-0">
          <aside className="border-b md:border-b-0 md:border-r border-eve-border bg-eve-panel/70 min-h-0 md:overflow-y-auto">
            <div className="p-2 space-y-4">
              <NavGroup label={t("settingsHubDisplay")}>
                <NavButton active={tab === "theme"} label={t("settingsHubTheme")} onClick={openTheme} />
              </NavGroup>

              {interfaceContent && (
                <NavGroup label={t("settingsHubInterface")}>
                  {interfacePages.map((item) => (
                    <NavButton
                      key={item.id}
                      active={tab === `interface:${item.id}` && activeInterfacePage === item.id}
                      label={item.label}
                      onClick={() => openInterface(item.id)}
                    />
                  ))}
                </NavGroup>
              )}

              {settingsContent && (
                <NavGroup label={t("settingsHubAccount")}>
                  <NavButton active={tab === "settings"} label={t("settingsHubTaxProfile")} onClick={openSettings} />
                </NavGroup>
              )}
            </div>
          </aside>

          <main className="min-w-0 min-h-0 overflow-y-auto bg-eve-dark/70 p-4">
            {body}
          </main>
        </div>
      </Modal>
    </div>
  );
}

function NavGroup({ label, children }: { label: string; children: ReactNode }) {
  return (
    <section>
      <div className="px-2 py-1 text-[10px] uppercase tracking-widest text-eve-dim">{label}</div>
      <div className="space-y-1">{children}</div>
    </section>
  );
}

function NavButton({ active, label, onClick }: { active: boolean; label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`w-full text-left px-3 py-2 rounded-sm text-xs transition-colors ${
        active
          ? "bg-eve-accent/15 text-eve-accent border border-eve-accent/35"
          : "text-eve-dim border border-transparent hover:text-eve-text hover:bg-eve-dark/65"
      }`}
    >
      {label}
    </button>
  );
}

interface ThemePanelProps {
  mode: ThemeMode;
  palette: string;
  fontSize: FontSize;
  customPalettes: CustomPalette[];
  activeCustom?: CustomPalette;
  setMode: (mode: ThemeMode) => void;
  setPalette: (palette: string) => void;
  setFontSize: (fontSize: FontSize) => void;
  onNewPalette: () => void;
  onEditPalette: (palette: CustomPalette) => void;
  onImportPalette: () => void;
  onExportPalette: (palette: CustomPalette) => void;
}

function ThemePanel({
  mode,
  palette,
  fontSize,
  customPalettes,
  activeCustom,
  setMode,
  setPalette,
  setFontSize,
  onNewPalette,
  onEditPalette,
  onImportPalette,
  onExportPalette,
}: ThemePanelProps) {
  const { t } = useI18n();

  return (
    <div className="max-w-4xl space-y-4">
      <Panel title={t("themeMode")}>
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-2">
          {MODES.map((item) => (
            <button
              key={item.id}
              type="button"
              onClick={() => setMode(item.id)}
              className={`flex items-center justify-center gap-2 px-3 py-2 rounded-sm text-sm font-medium transition-all border ${
                mode === item.id
                  ? "border-eve-accent/60 bg-eve-accent/10 text-eve-accent"
                  : "border-eve-border bg-eve-panel text-eve-dim hover:text-eve-text hover:border-eve-border-light"
              }`}
            >
              <span className="text-base leading-none">{item.icon}</span>
              <span>{t(item.nameKey)}</span>
            </button>
          ))}
        </div>
      </Panel>

      <Panel
        title={t("themePalette")}
        action={
          <div className="flex flex-wrap items-center gap-2">
            <button
              type="button"
              onClick={onImportPalette}
              className="inline-flex h-8 items-center gap-2 rounded-sm border border-dashed border-eve-border bg-eve-dark/45 px-3 text-[11px] font-medium text-eve-dim transition-colors hover:border-eve-accent/45 hover:text-eve-accent"
            >
              <Upload className="h-3.5 w-3.5" aria-hidden="true" />
              <span>{t("themeImport")}</span>
            </button>
            <button
              type="button"
              onClick={onNewPalette}
              className="inline-flex h-8 items-center gap-2 rounded-sm border border-eve-accent/45 bg-eve-accent/10 px-3 text-[11px] font-semibold text-eve-accent transition-colors hover:bg-eve-accent/15"
            >
              <Plus className="h-3.5 w-3.5" aria-hidden="true" />
              <span>{t("themeNew")}</span>
            </button>
          </div>
        }
      >
        <div className="grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-2">
          {PALETTES.map((item) => (
            <button
              key={item.id}
              type="button"
              onClick={() => setPalette(item.id)}
              className={`group flex h-12 min-w-0 items-center justify-between gap-3 rounded-sm border px-3 text-left text-xs font-medium transition-all ${
                palette === item.id
                  ? "border-eve-accent/70 bg-eve-accent/10 text-eve-accent shadow-[inset_0_0_0_1px_rgb(var(--eve-accent)/0.12)]"
                  : "border-eve-border bg-eve-panel/90 text-eve-dim hover:border-eve-border-light hover:bg-eve-panel-hover/70 hover:text-eve-text"
              }`}
            >
              <span className="flex min-w-0 items-center gap-3">
                <PaletteSwatch bg={item.bg} accent={item.accent} />
                <span className="truncate">{t(item.nameKey as TranslationKey)}</span>
              </span>
              {palette === item.id && <Check className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />}
            </button>
          ))}

          {customPalettes.map((item) => (
            <button
              key={item.id}
              type="button"
              onClick={() => setPalette(item.id)}
              className={`group flex h-12 min-w-0 items-center justify-between gap-3 rounded-sm border px-3 text-left text-xs font-medium transition-all ${
                palette === item.id
                  ? "border-eve-accent/70 bg-eve-accent/10 text-eve-accent shadow-[inset_0_0_0_1px_rgb(var(--eve-accent)/0.12)]"
                  : "border-eve-border bg-eve-panel/90 text-eve-dim hover:border-eve-border-light hover:bg-eve-panel-hover/70 hover:text-eve-text"
              }`}
            >
              <span className="flex min-w-0 items-center gap-3">
                <PaletteSwatch bg={item.dark.bg} accent={item.dark.accent} />
                <span className="truncate">{item.name}</span>
              </span>
              <span className="flex shrink-0 items-center gap-1">
                {palette === item.id && <Check className="h-3.5 w-3.5" aria-hidden="true" />}
                <span
                onClick={(event) => {
                  event.stopPropagation();
                  onEditPalette(item);
                }}
                  className="inline-flex h-7 w-7 items-center justify-center rounded-sm text-eve-dim opacity-0 transition-opacity hover:bg-eve-dark/70 hover:text-eve-accent group-hover:opacity-100"
                title={t("themeEditPalette")}
              >
                  <Pencil className="h-3.5 w-3.5" aria-hidden="true" />
                </span>
              </span>
            </button>
          ))}
        </div>

        {activeCustom && (
          <div className="flex flex-wrap items-center gap-2 border-t border-eve-border/60 pt-3">
            <span className="text-[11px] text-eve-dim">{activeCustom.name}</span>
            <button type="button" onClick={() => onEditPalette(activeCustom)} className="inline-flex h-8 items-center gap-2 rounded-sm border border-eve-border px-3 text-[11px] text-eve-dim hover:text-eve-accent">
              <Pencil className="h-3.5 w-3.5" aria-hidden="true" />
              {t("themeEditPalette")}
            </button>
            <button type="button" onClick={() => onExportPalette(activeCustom)} className="inline-flex h-8 items-center gap-2 rounded-sm border border-eve-border px-3 text-[11px] text-eve-dim hover:text-eve-accent">
              {t("themeExport")}
            </button>
          </div>
        )}
      </Panel>

      <Panel title={t("themeFontSize")}>
        <div className="grid grid-cols-5 gap-2 max-w-md">
          {FONT_SIZES.map((item) => (
            <button
              key={item.id}
              type="button"
              onClick={() => setFontSize(item.id)}
              className={`flex flex-col items-center gap-1 px-2 py-2 rounded-sm transition-all border ${
                fontSize === item.id
                  ? "border-eve-accent/60 bg-eve-accent/10 text-eve-accent"
                  : "border-eve-border bg-eve-panel text-eve-dim hover:text-eve-text hover:border-eve-border-light"
              }`}
            >
              <span style={{ fontSize: `${FONT_SIZE_PX[item.id]}px` }} className="font-semibold leading-none">A</span>
              <span className="text-[10px] leading-none">{item.label}</span>
            </button>
          ))}
        </div>
      </Panel>
    </div>
  );
}

function PaletteSwatch({ bg, accent }: { bg: string; accent: string }) {
  return (
    <span className="relative inline-flex h-6 w-9 shrink-0 items-center" aria-hidden="true">
      <span
        className="absolute left-0 h-5 w-5 rounded-full border border-white/10 shadow-[0_0_0_1px_rgb(0_0_0/0.35)]"
        style={{ backgroundColor: bg }}
      />
      <span
        className="absolute left-4 h-5 w-5 rounded-full border border-white/10 shadow-[0_0_0_1px_rgb(0_0_0/0.35)]"
        style={{ backgroundColor: accent }}
      />
    </span>
  );
}

function Panel({ title, children, action }: { title: string; children: ReactNode; action?: ReactNode }) {
  return (
    <section className="border border-eve-border/70 bg-eve-panel/70 rounded-sm">
      <div className="flex min-h-12 items-center justify-between gap-3 border-b border-eve-border/60 px-3 py-2">
        <div className="text-[11px] font-semibold uppercase tracking-widest text-eve-accent">
          {title}
        </div>
        {action}
      </div>
      <div className="p-3 space-y-3">{children}</div>
    </section>
  );
}

interface PaletteEditorProps {
  cp: CustomPalette;
  isNew: boolean;
  onSave: (cp: CustomPalette) => void;
  onDelete: (id: string) => void;
  onCancel: () => void;
  onExport: (cp: CustomPalette) => void;
  onToast: (msg: string) => void;
}

function PaletteEditor({ cp: initial, isNew, onSave, onDelete, onCancel, onExport, onToast }: PaletteEditorProps) {
  const { t } = useI18n();
  const { mode } = useTheme();
  const [name, setName] = useState(initial.name);
  const [darkColors, setDarkColors] = useState<CustomPaletteColors>({ ...initial.dark });
  const [lightColors, setLightColors] = useState<CustomPaletteColors>({ ...initial.light });
  const [editMode, setEditMode] = useState<"dark" | "light">(mode === "light" ? "light" : "dark");
  const colors = editMode === "dark" ? darkColors : lightColors;

  function updateColor(key: keyof CustomPaletteColors, value: string) {
    const newColors = { ...colors, [key]: value };
    if (editMode === "dark") setDarkColors(newColors);
    else setLightColors(newColors);
    deriveAndApply(document.documentElement, newColors);
  }

  async function handleImportInEditor() {
    try {
      const text = await navigator.clipboard.readText();
      const data = JSON.parse(text);
      const imported = validateImportedPalette(data);
      if (!imported) {
        onToast(t("themeImportError"));
        return;
      }
      setName(imported.name);
      setDarkColors({ ...imported.dark });
      setLightColors({ ...imported.light });
      deriveAndApply(document.documentElement, editMode === "dark" ? imported.dark : imported.light);
      onToast(t("themeImported"));
    } catch {
      onToast(t("themeImportError"));
    }
  }

  return (
    <div className="max-w-3xl space-y-4">
      <Panel title={isNew ? t("themeNewPalette") : t("themeEditPalette")}>
        <div className="grid grid-cols-1 md:grid-cols-[minmax(0,1fr)_auto] gap-3">
          <input
            type="text"
            value={name}
            onChange={(event) => setName(event.target.value)}
            maxLength={24}
            className="w-full bg-eve-input border border-eve-border rounded-sm px-3 py-2 text-sm text-eve-text placeholder:text-eve-dim/50 focus:outline-none focus:border-eve-accent/50"
            placeholder={t("themePaletteName")}
          />
          <div className="flex gap-1">
            {(["dark", "light"] as const).map((item) => (
              <button
                key={item}
                type="button"
                onClick={() => {
                  setEditMode(item);
                  deriveAndApply(document.documentElement, item === "dark" ? darkColors : lightColors);
                }}
                className={`px-3 py-2 rounded-sm border text-xs font-medium transition-all ${
                  editMode === item
                    ? "border-eve-accent/50 bg-eve-accent/10 text-eve-accent"
                    : "border-eve-border bg-eve-dark text-eve-dim hover:text-eve-text"
                }`}
              >
                {item === "dark" ? t("themeDark") : t("themeLight")}
              </button>
            ))}
          </div>
        </div>

        <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
          {PALETTE_COLOR_FIELDS.map((field) => (
            <label key={field.key} className="border border-eve-border/60 bg-eve-dark/35 rounded-sm p-3 cursor-pointer">
              <span className="block text-[10px] uppercase tracking-widest text-eve-dim">{t(field.nameKey as TranslationKey)}</span>
              <div className="mt-2 flex items-center gap-2">
                <input
                  type="color"
                  value={colors[field.key]}
                  onChange={(event) => updateColor(field.key, event.target.value)}
                  className="w-8 h-8 rounded-sm border border-eve-border cursor-pointer"
                />
                <span className="text-xs font-mono text-eve-text">{colors[field.key]}</span>
              </div>
            </label>
          ))}
        </div>

        <div className="flex flex-wrap gap-2">
          <button type="button" onClick={handleImportInEditor} className="px-3 py-1.5 border border-eve-border rounded-sm text-xs text-eve-dim hover:text-eve-accent">
            {t("themeImport")}
          </button>
          <button type="button" onClick={() => onExport({ id: initial.id, name, dark: darkColors, light: lightColors })} className="px-3 py-1.5 border border-eve-border rounded-sm text-xs text-eve-dim hover:text-eve-accent">
            {t("themeExport")}
          </button>
          <button type="button" onClick={() => onSave({ id: initial.id, name, dark: darkColors, light: lightColors })} className="px-3 py-1.5 border border-eve-accent/50 bg-eve-accent/10 rounded-sm text-xs text-eve-accent hover:bg-eve-accent/20">
            {t("themeSavePalette")}
          </button>
          {!isNew && (
            <button type="button" onClick={() => onDelete(initial.id)} className="px-3 py-1.5 border border-eve-error/40 rounded-sm text-xs text-eve-error hover:bg-eve-error/10">
              {t("themeDeletePalette")}
            </button>
          )}
          <button type="button" onClick={onCancel} className="px-3 py-1.5 border border-eve-border rounded-sm text-xs text-eve-dim hover:text-eve-text">
            {t("themeCancel")}
          </button>
        </div>
      </Panel>
    </div>
  );
}

function deepClone<T>(obj: T): T {
  return JSON.parse(JSON.stringify(obj));
}
