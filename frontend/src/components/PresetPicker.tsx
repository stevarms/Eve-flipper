import { useState, useRef, useEffect, useCallback } from "react";
import { useI18n, type TranslationKey } from "@/lib/i18n";
import { useGlobalToast } from "./Toast";
import {
  filterPresetsForTab,
  exportPresets,
  parseImportedPresets,
  mapTabToPresetTab,
  getPresetApplyBase,
  sanitizePresetParams,
  isPresetTab,
  type BuiltinPreset,
} from "@/lib/presets";
import {
  getConfig,
  updateConfig,
  getSavedPresets,
  createSavedPreset,
  updateSavedPreset,
  deleteSavedPreset,
  type ServerSavedPreset,
} from "@/lib/api";

/* eslint-disable @typescript-eslint/no-explicit-any */
interface Props {
  params: Record<string, any>;
  onApply: (params: any) => void;
  tab: string;
  builtinPresets: BuiltinPreset[];
  /** Which edge the dropdown aligns to. Default "left". */
  align?: "left" | "right";
}
/* eslint-enable @typescript-eslint/no-explicit-any */

/** Parses AppConfig's active_preset_ids_json -- a {[tab]: presetId} map --
 *  tolerating anything, the same convention ordersPrefs.ts's
 *  normalizeOrdersPrefs uses for its own opaque config blob. */
function parseActivePresetIds(raw: string | undefined): Record<string, string> {
  if (!raw) return {};
  try {
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
    const out: Record<string, string> = {};
    for (const [key, value] of Object.entries(parsed)) {
      if (typeof value === "string" && value) out[key] = value;
    }
    return out;
  } catch {
    return {};
  }
}

export function PresetPicker({ params, onApply, tab, builtinPresets, align = "left" }: Props) {
  const { t } = useI18n();
  const { addToast } = useGlobalToast();
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveName, setSaveName] = useState("");
  const presetTab = mapTabToPresetTab(tab);

  // Which preset (builtin or custom) is currently applied on this tab --
  // server-side (AppConfig's active_preset_ids_json) so it follows your
  // login, replacing what used to be a separate
  // eve-flipper-active-preset-${tab} localStorage key per tab. The saved
  // presets themselves live in a different place (saved_presets, below).
  const [activePresetId, setActivePresetIdState] = useState<string | null>(null);
  const [customPresets, setCustomPresets] = useState<ServerSavedPreset[]>([]);
  const builtinPresetsRef = useRef(builtinPresets);
  useEffect(() => {
    builtinPresetsRef.current = builtinPresets;
  }, [builtinPresets]);

  const ref = useRef<HTMLDivElement>(null);
  const autoAppliedRef = useRef<string | null>(null);
  const paramsRef = useRef(params);
  useEffect(() => {
    paramsRef.current = params;
  }, [params]);
  const onApplyRef = useRef(onApply);
  useEffect(() => {
    onApplyRef.current = onApply;
  }, [onApply]);

  // Re-fetches the pointer fresh right before merging in this tab's choice,
  // rather than writing back a copy of the map read at mount -- this tab is
  // the only one this component ever changes, but another tab's picker may
  // be mounted (hidden, kept alive) and could have changed a different key
  // in the same JSON blob since this component's own last fetch.
  const setActivePresetId = useCallback(
    async (id: string | null) => {
      setActivePresetIdState(id);
      try {
        const cfg = await getConfig();
        const map = parseActivePresetIds(cfg.active_preset_ids_json);
        if (id) map[presetTab] = id;
        else delete map[presetTab];
        await updateConfig({ active_preset_ids_json: JSON.stringify(map) });
      } catch {
        // A UI selection pointer, not data -- worth trying again next
        // change, not worth surfacing to the user.
      }
    },
    [presetTab],
  );

  // Load this tab's saved presets and active pointer whenever the tab
  // changes (or on mount). Also replays the active preset's params onto the
  // current params once, the same way switching back to a tab used to.
  useEffect(() => {
    let cancelled = false;
    autoAppliedRef.current = null;
    void (async () => {
      const [cfgResult, presetsResult] = await Promise.allSettled([getConfig(), getSavedPresets()]);
      if (cancelled) return;

      const allPresets = presetsResult.status === "fulfilled" ? presetsResult.value.presets : [];
      const tabPresets = filterPresetsForTab(allPresets, tab);
      setCustomPresets(tabPresets);

      const map = cfgResult.status === "fulfilled" ? parseActivePresetIds(cfgResult.value.active_preset_ids_json) : {};
      let resolvedId = map[presetTab] ?? null;
      if (!resolvedId) {
        const defaultBuiltin =
          builtinPresetsRef.current.find((p) => p.id.includes("normal")) ?? builtinPresetsRef.current[0];
        if (defaultBuiltin) {
          resolvedId = defaultBuiltin.id;
          void setActivePresetId(defaultBuiltin.id);
        }
      }
      setActivePresetIdState(resolvedId);

      if (!resolvedId) return;
      const applyKey = `${tab}:${resolvedId}`;
      if (autoAppliedRef.current === applyKey) return;
      const builtin = builtinPresetsRef.current.find((p) => p.id === resolvedId);
      const custom = tabPresets.find((p) => p.id === resolvedId);
      const presetParams = builtin?.params ?? custom?.params;
      if (!presetParams) return;
      onApplyRef.current({
        ...paramsRef.current,
        ...getPresetApplyBase(tab),
        ...sanitizePresetParams(presetParams),
      });
      autoAppliedRef.current = applyKey;
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab, presetTab, setActivePresetId]);

  // Close on outside click
  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
        setSaving(false);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open]);

  // Close on Escape
  useEffect(() => {
    if (!open) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setOpen(false);
        setSaving(false);
      }
    };
    document.addEventListener("keydown", handler);
    return () => document.removeEventListener("keydown", handler);
  }, [open]);

  const refreshCustomPresets = useCallback(async () => {
    const resp = await getSavedPresets();
    setCustomPresets(filterPresetsForTab(resp.presets, tab));
  }, [tab]);

  const handleApply = useCallback(
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (id: string, presetParams: Record<string, any>) => {
      onApply({
        ...params,
        ...getPresetApplyBase(tab),
        ...sanitizePresetParams(presetParams),
      });
      void setActivePresetId(id);
      autoAppliedRef.current = `${tab}:${id}`;
      setOpen(false);
    },
    [params, onApply, setActivePresetId, tab],
  );

  const handleDelete = async (e: React.MouseEvent, id: string) => {
    e.stopPropagation();
    try {
      const resp = await deleteSavedPreset(id);
      setCustomPresets(filterPresetsForTab(resp.presets, tab));
      if (activePresetId === id) void setActivePresetId(null);
      addToast(t("presetDeleted" as TranslationKey) || "Preset deleted", "success", 2000);
    } catch {
      addToast(t("presetDeleteFailed" as TranslationKey) || "Failed to delete preset", "error", 3000);
    }
  };

  const handleSave = async () => {
    if (!saveName.trim()) return;
    try {
      const resp = await createSavedPreset({
        tab: presetTab,
        name: saveName.trim(),
        params: sanitizePresetParams({ ...params }),
        activate: true,
      });
      setCustomPresets(filterPresetsForTab(resp.presets, tab));
      void setActivePresetId(resp.preset.id);
      autoAppliedRef.current = `${tab}:${resp.preset.id}`;
      setSaveName("");
      setSaving(false);
      addToast(t("presetSaved"), "success", 2000);
    } catch {
      addToast(t("presetSaveFailed" as TranslationKey) || "Failed to save preset", "error", 3000);
    }
  };

  const handleUpdate = async () => {
    if (!activePresetId) return;
    const existing = customPresets.find((p) => p.id === activePresetId);
    if (!existing) return;
    try {
      const resp = await updateSavedPreset(activePresetId, {
        params: sanitizePresetParams({ ...params }),
      });
      setCustomPresets(filterPresetsForTab(resp.presets, tab));
      addToast(t("presetUpdated" as TranslationKey) || "Preset updated", "success", 2000);
    } catch {
      addToast(t("presetUpdateFailed" as TranslationKey) || "Failed to update preset", "error", 3000);
    }
  };

  const handleOverwritePreset = async (e: React.MouseEvent, id: string) => {
    e.stopPropagation();
    const existing = customPresets.find((p) => p.id === id);
    if (!existing) return;
    try {
      const resp = await updateSavedPreset(id, {
        params: sanitizePresetParams({ ...params }),
      });
      setCustomPresets(filterPresetsForTab(resp.presets, tab));
      void setActivePresetId(id);
      autoAppliedRef.current = `${tab}:${id}`;
      addToast(t("presetUpdated" as TranslationKey) || "Preset updated", "success", 2000);
    } catch {
      addToast(t("presetUpdateFailed" as TranslationKey) || "Failed to update preset", "error", 3000);
    }
  };

  const handleExport = async () => {
    try {
      const resp = await getSavedPresets();
      const json = exportPresets(
        resp.presets.map((p) => ({
          id: p.id,
          name: p.name,
          tab: isPresetTab(p.tab) ? p.tab : "flipper",
          params: p.params,
          createdAt: p.created_at ? Date.parse(p.created_at) || undefined : undefined,
        })),
      );
      await navigator.clipboard.writeText(json);
      addToast(t("presetExported" as TranslationKey) || "Presets copied to clipboard", "success", 2000);
    } catch {
      addToast("Clipboard access denied", "error", 3000);
    }
    setOpen(false);
  };

  const handleImport = async () => {
    try {
      const json = await navigator.clipboard.readText();
      const { presets: parsed, error } = parseImportedPresets(json);
      if (error) {
        addToast(error, "error", 3000);
        setOpen(false);
        return;
      }
      let imported = 0;
      for (const preset of parsed) {
        try {
          await createSavedPreset({
            tab: preset.tab,
            name: preset.name,
            params: preset.params,
            activate: false,
          });
          imported++;
        } catch {
          // Skip presets the server rejects; report the count that stuck.
        }
      }
      await refreshCustomPresets();
      addToast(
        `${t("presetImported" as TranslationKey) || "Imported"}: ${imported}`,
        "success",
        2000,
      );
    } catch {
      addToast("Clipboard access denied", "error", 3000);
    }
    setOpen(false);
  };

  // Active preset label
  const activeLabel = (() => {
    if (!activePresetId) return null;
    const b = builtinPresets.find((p) => p.id === activePresetId);
    if (b) return t(b.nameKey as TranslationKey);
    const c = customPresets.find((p) => p.id === activePresetId);
    return c?.name || null;
  })();

  const isCustomActive = activePresetId
    ? customPresets.some((p) => p.id === activePresetId)
    : false;

  return (
    <div className="relative" ref={ref}>
      {/* Trigger button */}
      <div className="flex items-center gap-1.5">
        <span className="text-[10px] uppercase tracking-wider text-eve-dim font-medium shrink-0">
          {t("presetLabel")}
        </span>
        <button
          type="button"
          onClick={() => {
            setOpen(!open);
            setSaving(false);
          }}
          className={`flex items-center gap-1.5 min-w-0 max-w-[160px] px-2.5 py-1 bg-eve-input border rounded text-sm transition-colors ${
            open
              ? "border-eve-accent text-eve-accent"
              : "border-eve-border text-eve-text hover:border-eve-accent/50"
          }`}
        >
          <span className="truncate">{activeLabel || "—"}</span>
          <span className="text-[10px] text-eve-dim shrink-0">▾</span>
        </button>
      </div>

      {/* Dropdown panel */}
      {open && (
        <div className={`absolute top-full mt-1 w-72 bg-eve-panel border border-eve-border rounded-sm shadow-2xl z-50 overflow-hidden ${align === "right" ? "right-0" : "left-0"}`}>
          {/* Built-in presets */}
          <div className="px-3 pt-2.5 pb-1.5">
            <div className="text-[10px] uppercase tracking-wider text-eve-dim font-medium mb-1.5">
              {t("presetBuiltin" as TranslationKey) || "Built-in"}
            </div>
            <div className="space-y-0.5">
              {builtinPresets.map((p) => (
                <button
                  key={p.id}
                  onClick={() => handleApply(p.id, p.params)}
                  className={`w-full flex items-center justify-between px-2 py-1.5 rounded-sm text-sm transition-colors ${
                    activePresetId === p.id
                      ? "bg-eve-accent/15 text-eve-accent"
                      : "text-eve-text hover:bg-eve-dark/50"
                  }`}
                >
                  <span>{t(p.nameKey as TranslationKey)}</span>
                  {activePresetId === p.id && (
                    <span className="text-eve-accent text-xs">✓</span>
                  )}
                </button>
              ))}
            </div>
          </div>

          <div className="border-t border-eve-border/50" />

          {/* Custom presets */}
          <div className="px-3 pt-2 pb-1.5">
            <div className="text-[10px] uppercase tracking-wider text-eve-dim font-medium mb-1.5">
              {t("presetCustom" as TranslationKey) || "Custom"}
            </div>
            {customPresets.length === 0 ? (
              <div className="text-xs text-eve-dim py-1 px-2">
                {t("presetNoCustom" as TranslationKey) || "No custom presets yet"}
              </div>
            ) : (
              <div className="space-y-0.5 max-h-[160px] overflow-y-auto">
                {customPresets.map((p) => (
                  <div key={p.id} className="flex items-center gap-1 group">
                    <button
                      onClick={() => handleApply(p.id, p.params)}
                      className={`flex-1 flex items-center justify-between px-2 py-1.5 rounded-sm text-sm transition-colors text-left min-w-0 ${
                        activePresetId === p.id
                          ? "bg-eve-accent/15 text-eve-accent"
                          : "text-eve-text hover:bg-eve-dark/50"
                      }`}
                    >
                      <span className="truncate">{p.name}</span>
                      {activePresetId === p.id && (
                        <span className="text-eve-accent text-xs shrink-0 ml-1">
                          ✓
                        </span>
                      )}
                    </button>
                    <button
                      onClick={(e) => handleOverwritePreset(e, p.id)}
                      className="shrink-0 w-6 h-6 flex items-center justify-center text-eve-dim hover:text-eve-accent opacity-0 group-hover:opacity-100 transition-all rounded-sm hover:bg-eve-accent/10"
                      title={
                        t("presetUpdate" as TranslationKey) ||
                        "Update active preset"
                      }
                    >
                      ↻
                    </button>
                    <button
                      onClick={(e) => handleDelete(e, p.id)}
                      className="shrink-0 w-6 h-6 flex items-center justify-center text-eve-dim hover:text-red-400 opacity-0 group-hover:opacity-100 transition-all rounded-sm hover:bg-red-500/10"
                      title={
                        t("presetDelete" as TranslationKey) || "Delete"
                      }
                    >
                      ✕
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>

          <div className="border-t border-eve-border/50" />

          {/* Save section */}
          <div className="px-3 py-2">
            {saving ? (
              <div className="space-y-1.5">
                <input
                  type="text"
                  value={saveName}
                  onChange={(e) => setSaveName(e.target.value)}
                  onKeyDown={(e) => e.key === "Enter" && handleSave()}
                  placeholder={
                    t("presetNamePlaceholder" as TranslationKey) ||
                    "Preset name..."
                  }
                  className="w-full px-2 py-1.5 bg-eve-input border border-eve-border rounded text-sm text-eve-text focus:outline-none focus:border-eve-accent focus:ring-1 focus:ring-eve-accent/30"
                  autoFocus
                />
                <div className="flex gap-1.5">
                  <button
                    onClick={handleSave}
                    disabled={!saveName.trim()}
                    className="flex-1 px-2 py-1 text-xs bg-eve-accent text-eve-on-accent rounded-sm hover:bg-eve-accent-hover disabled:opacity-40 disabled:cursor-not-allowed transition-colors font-medium"
                  >
                    {t("presetSaveBtn" as TranslationKey) || "Save"}
                  </button>
                  <button
                    onClick={() => {
                      setSaving(false);
                      setSaveName("");
                    }}
                    className="px-2 py-1 text-xs bg-eve-dark text-eve-dim rounded-sm hover:text-eve-text transition-colors"
                  >
                    {t("cancel" as TranslationKey) || "Cancel"}
                  </button>
                </div>
              </div>
            ) : (
              <div className="flex gap-1.5">
                <button
                  onClick={() => setSaving(true)}
                  className="flex-1 px-2 py-1.5 text-xs bg-eve-dark border border-eve-border rounded-sm text-eve-text hover:border-eve-accent/50 transition-colors"
                >
                  +{" "}
                  {t("presetSaveNew" as TranslationKey) || "Save current"}
                </button>
                {isCustomActive && (
                  <button
                    onClick={handleUpdate}
                    className="px-2 py-1.5 text-xs bg-eve-dark border border-eve-border rounded-sm text-eve-accent hover:border-eve-accent/50 transition-colors"
                    title={
                      t("presetUpdate" as TranslationKey) ||
                      "Update active preset"
                    }
                  >
                    ↻
                  </button>
                )}
              </div>
            )}
          </div>

          <div className="border-t border-eve-border/50" />

          {/* Export / Import */}
          <div className="px-3 py-2 flex gap-1.5">
            <button
              onClick={handleExport}
              className="flex-1 px-2 py-1 text-[11px] text-eve-dim hover:text-eve-text transition-colors"
            >
              {t("presetExport" as TranslationKey) || "Export"}
            </button>
            <span className="text-eve-border">|</span>
            <button
              onClick={handleImport}
              className="flex-1 px-2 py-1 text-[11px] text-eve-dim hover:text-eve-text transition-colors"
            >
              {t("presetImport" as TranslationKey) || "Import"}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
