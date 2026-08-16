import { useCallback, useEffect, useMemo, useState } from "react";
import { useI18n } from "@/lib/i18n";
import {
  createStockpile,
  deleteStockpile,
  deleteStockpileItem,
  getAuthStatus,
  getCharacterRoles,
  getStations,
  getStockpile,
  getStructures,
  listStockpiles,
  resolveStockpileNames,
  scanStockpile,
  updateStockpile,
  upsertStockpileItems,
} from "@/lib/api";
import type {
  AuthCharacter,
  StationInfo,
  Stockpile,
  StockpileItem,
  StockpileScanResult,
} from "@/lib/types";
import { parseEveClipboard } from "@/lib/parseEveClipboard";
import { SystemAutocomplete } from "../SystemAutocomplete";
import { useGlobalToast } from "../Toast";

interface Props {
  isLoggedIn: boolean;
}

type StationOption = StationInfo & { label: string };

interface CharacterCorpInfo {
  characterId: number;
  characterName: string;
  corporationId: number;
}

const SELECTED_ID_LS_KEY = "eve-settings:industry-stockpile-selected";

function fmtInt(n: number): string {
  return Math.max(0, Math.floor(n || 0)).toLocaleString();
}

function copyMultibuyToClipboard(entries: Array<{ name: string; qty: number }>): Promise<boolean> {
  const text = entries
    .map((e) => `${e.name.replace(/[\r\n\t]/g, " ")}\t${Math.max(1, Math.round(e.qty))}`)
    .join("\n");
  return navigator.clipboard
    .writeText(text)
    .then(() => true)
    .catch(() => false);
}

export default function IndustryStockpilePanel({ isLoggedIn }: Props) {
  const { t } = useI18n();
  const { addToast } = useGlobalToast();
  const pushToast = useCallback(
    ({ kind, message }: { kind: "error" | "warning" | "success" | "info"; message: string }) => {
      addToast(message, kind);
    },
    [addToast],
  );

  const [stockpiles, setStockpiles] = useState<Stockpile[]>([]);
  const [listLoading, setListLoading] = useState(false);
  const [selectedId, setSelectedId] = useState<number | null>(() => {
    if (typeof window === "undefined") return null;
    const raw = window.localStorage.getItem(SELECTED_ID_LS_KEY);
    const n = raw ? Number(raw) : NaN;
    return Number.isFinite(n) && n > 0 ? n : null;
  });
  const [selected, setSelected] = useState<Stockpile | null>(null);
  const [selectedLoading, setSelectedLoading] = useState(false);

  const [authCharacters, setAuthCharacters] = useState<AuthCharacter[]>([]);
  const [corpByChar, setCorpByChar] = useState<Map<number, CharacterCorpInfo>>(new Map());
  const [corpLookupLoading, setCorpLookupLoading] = useState(false);

  // Scan state
  const [scanResult, setScanResult] = useState<StockpileScanResult | null>(null);
  const [scanning, setScanning] = useState(false);
  const [showAll, setShowAll] = useState(false);
  const [multibuyQueue, setMultibuyQueue] = useState<Map<number, { name: string; qty: number }>>(new Map());

  // Create / rename modal state
  const [creating, setCreating] = useState(false);
  const [renaming, setRenaming] = useState(false);

  // Paste import
  const [pasteText, setPasteText] = useState("");
  const [importing, setImporting] = useState(false);

  // ---- Load stockpiles + auth characters on mount ----
  const refreshList = useCallback(async () => {
    if (!isLoggedIn) return;
    setListLoading(true);
    try {
      const list = await listStockpiles();
      setStockpiles(list);
      if (list.length === 0) {
        setSelectedId(null);
        setSelected(null);
      } else if (selectedId == null || !list.some((s) => s.id === selectedId)) {
        setSelectedId(list[0].id);
      }
    } catch (err) {
      pushToast({ kind: "error", message: (err as Error).message });
    } finally {
      setListLoading(false);
    }
  }, [isLoggedIn, selectedId, pushToast]);

  useEffect(() => {
    refreshList();
  }, [refreshList]);

  useEffect(() => {
    if (!isLoggedIn) {
      setAuthCharacters([]);
      return;
    }
    getAuthStatus()
      .then((s) => setAuthCharacters(s.characters ?? []))
      .catch(() => setAuthCharacters([]));
  }, [isLoggedIn]);

  // Look up corp id for each character (used to filter which corp options are
  // available in the create/edit form). Best-effort; a failure just omits that
  // character from the corp source list.
  useEffect(() => {
    if (!isLoggedIn || authCharacters.length === 0) {
      setCorpByChar(new Map());
      setCorpLookupLoading(false);
      return;
    }
    let cancelled = false;
    setCorpLookupLoading(true);
    (async () => {
      const results = await Promise.all(
        authCharacters.map(async (c) => {
          try {
            const roles = await getCharacterRoles(undefined, c.character_id);
            return { characterId: c.character_id, characterName: c.character_name, corporationId: roles.corporation_id };
          } catch {
            return null;
          }
        }),
      );
      if (cancelled) return;
      const map = new Map<number, CharacterCorpInfo>();
      for (const r of results) {
        if (r && r.corporationId > 0) map.set(r.characterId, r);
      }
      setCorpByChar(map);
      setCorpLookupLoading(false);
    })();
    return () => {
      cancelled = true;
    };
  }, [isLoggedIn, authCharacters]);

  // Load full stockpile detail when selection changes.
  useEffect(() => {
    if (!isLoggedIn || selectedId == null) {
      setSelected(null);
      setScanResult(null);
      setMultibuyQueue(new Map());
      return;
    }
    if (typeof window !== "undefined") {
      window.localStorage.setItem(SELECTED_ID_LS_KEY, String(selectedId));
    }
    let cancelled = false;
    setSelectedLoading(true);
    setScanResult(null);
    setMultibuyQueue(new Map());
    getStockpile(selectedId)
      .then((sp) => {
        if (!cancelled) setSelected(sp);
      })
      .catch((err) => {
        if (!cancelled) pushToast({ kind: "error", message: (err as Error).message });
      })
      .finally(() => {
        if (!cancelled) setSelectedLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [isLoggedIn, selectedId, pushToast]);

  // ---- Scan ----
  const runScan = useCallback(async () => {
    if (selectedId == null) return;
    setScanning(true);
    try {
      const res = await scanStockpile(selectedId);
      setScanResult(res);
      // Seed multibuy with shortfall qty per row.
      const seed = new Map<number, { name: string; qty: number }>();
      for (const row of res.items) {
        if (row.shortfall > 0) {
          seed.set(row.type_id, { name: row.type_name, qty: row.shortfall });
        }
      }
      setMultibuyQueue(seed);
      if (res.warnings?.length) {
        pushToast({ kind: "warning", message: res.warnings.join(" • ") });
      }
    } catch (err) {
      pushToast({ kind: "error", message: `${t("stockpileScanFailed")}: ${(err as Error).message}` });
    } finally {
      setScanning(false);
    }
  }, [selectedId, pushToast, t]);

  // ---- Paste import ----
  const runImport = useCallback(async () => {
    if (selectedId == null || !pasteText.trim()) return;
    const parsed = parseEveClipboard(pasteText);
    if (parsed.length === 0) {
      pushToast({ kind: "warning", message: t("stockpileImportEmpty") });
      return;
    }
    setImporting(true);
    try {
      const resolved = await resolveStockpileNames(parsed);
      const rows: StockpileItem[] = [];
      let unresolved = 0;
      for (const r of resolved.items) {
        if (r.unresolved || !r.type_id || !r.type_name) {
          unresolved++;
          continue;
        }
        rows.push({
          type_id: r.type_id,
          type_name: r.type_name,
          threshold_qty: Math.max(0, Math.floor(r.qty || 0)),
        });
      }
      if (rows.length === 0) {
        pushToast({ kind: "warning", message: t("stockpileImportEmpty") });
        return;
      }
      const updated = await upsertStockpileItems(selectedId, rows);
      setSelected(updated);
      setPasteText("");
      pushToast({
        kind: "success",
        message: t("stockpileImportedCount").replace("{count}", String(rows.length)),
      });
      if (unresolved > 0) {
        pushToast({
          kind: "warning",
          message: t("stockpileImportUnresolved").replace("{count}", String(unresolved)),
        });
      }
    } catch (err) {
      pushToast({ kind: "error", message: (err as Error).message });
    } finally {
      setImporting(false);
    }
  }, [selectedId, pasteText, pushToast, t]);

  // ---- Row helpers ----
  const updateItemThreshold = useCallback(
    async (typeId: number, typeName: string, qty: number) => {
      if (selectedId == null) return;
      const clean = Math.max(0, Math.floor(qty || 0));
      try {
        const updated = await upsertStockpileItems(selectedId, [
          { type_id: typeId, type_name: typeName, threshold_qty: clean },
        ]);
        setSelected(updated);
      } catch (err) {
        pushToast({ kind: "error", message: (err as Error).message });
      }
    },
    [selectedId, pushToast],
  );

  const removeItem = useCallback(
    async (typeId: number) => {
      if (selectedId == null) return;
      try {
        await deleteStockpileItem(selectedId, typeId);
        const sp = await getStockpile(selectedId);
        setSelected(sp);
      } catch (err) {
        pushToast({ kind: "error", message: (err as Error).message });
      }
    },
    [selectedId, pushToast],
  );

  const handleDelete = useCallback(async () => {
    if (selectedId == null || !selected) return;
    if (!window.confirm(t("stockpileDeleteConfirm"))) return;
    try {
      await deleteStockpile(selectedId);
      setSelectedId(null);
      setSelected(null);
      await refreshList();
    } catch (err) {
      pushToast({ kind: "error", message: (err as Error).message });
    }
  }, [selectedId, selected, refreshList, pushToast, t]);

  // ---- Multibuy ----
  const bumpMultibuy = useCallback((typeId: number, name: string, delta: number) => {
    setMultibuyQueue((prev) => {
      const next = new Map(prev);
      const existing = next.get(typeId);
      const current = existing?.qty ?? 0;
      const nextQty = Math.max(0, current + delta);
      if (nextQty <= 0) {
        next.delete(typeId);
      } else {
        next.set(typeId, { name, qty: nextQty });
      }
      return next;
    });
  }, []);

  const setMultibuyQty = useCallback((typeId: number, name: string, qty: number) => {
    setMultibuyQueue((prev) => {
      const next = new Map(prev);
      const cleanQty = Math.max(0, Math.floor(qty || 0));
      if (cleanQty <= 0) {
        next.delete(typeId);
      } else {
        next.set(typeId, { name, qty: cleanQty });
      }
      return next;
    });
  }, []);

  const multibuyEntries = useMemo(
    () => Array.from(multibuyQueue.entries()).map(([typeId, v]) => ({ typeId, ...v })),
    [multibuyQueue],
  );

  const copyMultibuy = useCallback(async () => {
    if (multibuyEntries.length === 0) return;
    const ok = await copyMultibuyToClipboard(multibuyEntries);
    pushToast({
      kind: ok ? "success" : "error",
      message: ok
        ? t("multibuyCopied").replace("{count}", String(multibuyEntries.length))
        : t("multibuyCopyFailed"),
    });
  }, [multibuyEntries, pushToast, t]);

  const clearMultibuy = useCallback(() => setMultibuyQueue(new Map()), []);

  // ---- Filtered shortfall rows ----
  const displayRows = useMemo(() => {
    if (!scanResult) return [] as StockpileScanResult["items"];
    if (showAll) return scanResult.items;
    return scanResult.items.filter((r) => r.shortfall > 0);
  }, [scanResult, showAll]);

  if (!isLoggedIn) {
    return (
      <div className="m-4 text-sm text-eve-dim">
        Sign in with EVE SSO to use stockpile scanning.
      </div>
    );
  }

  return (
    <div className="p-3 space-y-3">
      {/* Config selector row */}
      <div className="flex items-center gap-2 flex-wrap">
        <select
          className="bg-eve-panel border border-eve-border rounded px-2 py-1 text-sm"
          value={selectedId ?? ""}
          disabled={stockpiles.length === 0}
          onChange={(e) => {
            const n = Number(e.target.value);
            setSelectedId(Number.isFinite(n) && n > 0 ? n : null);
          }}
        >
          {stockpiles.length === 0 && <option value="">—</option>}
          {stockpiles.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
              {s.station_name ? ` — ${s.station_name}` : ""}
            </option>
          ))}
        </select>
        <button
          type="button"
          className="px-2 py-1 text-xs bg-eve-accent text-black font-semibold rounded"
          onClick={() => setCreating(true)}
        >
          + {t("stockpileNewButton")}
        </button>
        {selected && (
          <>
            <button
              type="button"
              className="px-2 py-1 text-xs border border-eve-border rounded"
              onClick={() => setRenaming(true)}
            >
              {t("stockpileRenameButton")}
            </button>
            <button
              type="button"
              className="px-2 py-1 text-xs border border-red-800 text-red-300 rounded"
              onClick={handleDelete}
            >
              {t("stockpileDeleteButton")}
            </button>
          </>
        )}
        {listLoading && <span className="text-xs text-eve-dim">…</span>}
      </div>

      {/* Empty state when nothing exists */}
      {stockpiles.length === 0 && !listLoading && (
        <div className="m-4 text-center text-sm text-eve-dim">
          <div className="font-semibold text-eve-text mb-1">{t("stockpileEmptyTitle")}</div>
          <div>{t("stockpileEmptyBody")}</div>
        </div>
      )}

      {/* Detail body */}
      {selected && (
        <div className="space-y-4">
          {/* Header info */}
          <div className="text-xs text-eve-dim">
            <span className="mr-3">
              {t("stockpileSourceLabel")}:{" "}
              <span className="text-eve-text">
                {selected.source === "corporation"
                  ? t("stockpileSourceCorporation")
                  : t("stockpileSourceCharacter")}
              </span>
            </span>
            <span>
              {t("stockpileStationLabel")}:{" "}
              <span className="text-eve-text">
                {selected.station_name ?? `#${selected.station_id}`}
              </span>
            </span>
          </div>

          {/* Paste import */}
          <div>
            <label className="block text-xs text-eve-dim mb-1">{t("stockpilePasteLabel")}</label>
            <textarea
              className="w-full bg-eve-panel border border-eve-border rounded px-2 py-1 text-sm font-mono"
              rows={4}
              value={pasteText}
              placeholder={t("stockpilePastePlaceholder")}
              onChange={(e) => setPasteText(e.target.value)}
            />
            <div className="mt-1">
              <button
                type="button"
                className="px-2 py-1 text-xs bg-eve-accent text-black font-semibold rounded disabled:opacity-50"
                onClick={runImport}
                disabled={importing || !pasteText.trim()}
              >
                {importing ? t("stockpileImporting") : t("stockpileImportButton")}
              </button>
            </div>
          </div>

          {/* Items list */}
          <div>
            <div className="text-sm font-semibold mb-1">{t("stockpileItemsHeading")}</div>
            {selectedLoading ? (
              <div className="text-xs text-eve-dim">…</div>
            ) : selected.items && selected.items.length > 0 ? (
              <div className="overflow-x-auto">
                <table className="w-full text-sm border-collapse">
                  <thead className="text-left text-xs text-eve-dim border-b border-eve-border">
                    <tr>
                      <th className="py-1 pr-3">{t("stockpileTypeCol")}</th>
                      <th className="py-1 pr-3">{t("stockpileThresholdCol")}</th>
                      <th className="py-1"></th>
                    </tr>
                  </thead>
                  <tbody>
                    {selected.items.map((it) => (
                      <tr key={it.type_id} className="border-b border-eve-border/50">
                        <td className="py-1 pr-3">{it.type_name}</td>
                        <td className="py-1 pr-3">
                          <ThresholdInput
                            value={it.threshold_qty}
                            onCommit={(n) => updateItemThreshold(it.type_id, it.type_name, n)}
                          />
                        </td>
                        <td className="py-1 text-right">
                          <button
                            type="button"
                            className="text-xs text-red-300 hover:text-red-200"
                            onClick={() => removeItem(it.type_id)}
                          >
                            {t("stockpileRowRemove")}
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <div className="text-xs text-eve-dim">{t("stockpileItemsEmpty")}</div>
            )}
          </div>

          {/* Scan */}
          <div className="pt-2 border-t border-eve-border">
            <div className="flex items-center gap-2 flex-wrap">
              <button
                type="button"
                className="px-3 py-1.5 text-sm bg-eve-accent text-black font-semibold rounded disabled:opacity-50"
                onClick={runScan}
                disabled={scanning || !selected.items || selected.items.length === 0}
              >
                {scanning ? t("stockpileScanning") : t("stockpileScanButton")}
              </button>
              {scanResult && scanResult.items.length > 0 && (
                <label className="text-xs text-eve-dim flex items-center gap-1 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={showAll}
                    onChange={(e) => setShowAll(e.target.checked)}
                  />
                  {showAll ? t("stockpileShowShortfallOnly") : t("stockpileShowAll")}
                </label>
              )}
            </div>

            {scanResult && (
              <div className="mt-2">
                {scanResult.warnings && scanResult.warnings.length > 0 && (
                  <div className="mb-2 text-xs text-yellow-300">
                    <div className="font-semibold">{t("stockpileWarningsHeading")}</div>
                    <ul className="list-disc list-inside">
                      {scanResult.warnings.map((w, i) => (
                        <li key={i}>{w}</li>
                      ))}
                    </ul>
                  </div>
                )}
                {displayRows.length === 0 ? (
                  <div className="text-xs text-eve-dim">{t("stockpileShortfallEmpty")}</div>
                ) : (
                  <div className="overflow-x-auto">
                    <table className="w-full text-sm border-collapse">
                      <thead className="text-left text-xs text-eve-dim border-b border-eve-border">
                        <tr>
                          <th className="py-1 pr-3">{t("stockpileTypeCol")}</th>
                          <th className="py-1 pr-3 text-right">{t("stockpileThresholdCol")}</th>
                          <th className="py-1 pr-3 text-right">{t("stockpileCurrentCol")}</th>
                          <th className="py-1 pr-3 text-right">{t("stockpileShortfallCol")}</th>
                          <th className="py-1">{t("stockpileMultibuyAddedLabel")}</th>
                        </tr>
                      </thead>
                      <tbody>
                        {displayRows.map((row) => {
                          const inQueue = multibuyQueue.get(row.type_id);
                          return (
                            <tr key={row.type_id} className="border-b border-eve-border/50">
                              <td className="py-1 pr-3">{row.type_name}</td>
                              <td className="py-1 pr-3 text-right">{fmtInt(row.threshold_qty)}</td>
                              <td className="py-1 pr-3 text-right">{fmtInt(row.current_qty)}</td>
                              <td
                                className={`py-1 pr-3 text-right ${
                                  row.shortfall > 0 ? "text-yellow-300 font-semibold" : "text-eve-dim"
                                }`}
                              >
                                {fmtInt(row.shortfall)}
                              </td>
                              <td className="py-1">
                                <div className="inline-flex items-center gap-1">
                                  <button
                                    type="button"
                                    className="px-1.5 py-0.5 text-xs border border-eve-border rounded"
                                    onClick={() =>
                                      bumpMultibuy(
                                        row.type_id,
                                        row.type_name,
                                        -Math.max(1, Math.floor(row.shortfall / 10) || 1),
                                      )
                                    }
                                    aria-label="-"
                                  >
                                    −
                                  </button>
                                  <input
                                    className="w-24 bg-eve-panel border border-eve-border rounded px-1 py-0.5 text-xs text-right"
                                    type="number"
                                    min={0}
                                    step={1}
                                    value={inQueue?.qty ?? 0}
                                    onChange={(e) =>
                                      setMultibuyQty(row.type_id, row.type_name, Number(e.target.value))
                                    }
                                  />
                                  <button
                                    type="button"
                                    className="px-1.5 py-0.5 text-xs border border-eve-border rounded"
                                    onClick={() =>
                                      bumpMultibuy(
                                        row.type_id,
                                        row.type_name,
                                        Math.max(1, Math.floor(row.shortfall / 10) || 1),
                                      )
                                    }
                                    aria-label="+"
                                  >
                                    +
                                  </button>
                                </div>
                              </td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>
                )}
              </div>
            )}
          </div>
        </div>
      )}

      {/* Floating multibuy pill */}
      {multibuyEntries.length > 0 && (
        <div className="fixed bottom-4 right-4 z-40 bg-eve-panel border border-eve-accent shadow-lg rounded-full px-3 py-1.5 text-xs flex items-center gap-2">
          <span className="font-semibold text-eve-accent">
            {multibuyEntries.length} {multibuyEntries.length === 1 ? "row" : "rows"}
          </span>
          <button
            type="button"
            className="px-2 py-0.5 bg-eve-accent text-black font-semibold rounded"
            onClick={copyMultibuy}
          >
            {t("multibuyCopyBtn")}
          </button>
          <button
            type="button"
            className="text-eve-dim hover:text-eve-text"
            onClick={clearMultibuy}
            aria-label="clear"
          >
            ×
          </button>
        </div>
      )}

      {/* Create modal */}
      {creating && (
        <CreateStockpileModal
          onClose={() => setCreating(false)}
          onCreated={async (id) => {
            setCreating(false);
            setSelectedId(id);
            await refreshList();
          }}
          authCharacters={authCharacters}
          corpByChar={corpByChar}
          corpLookupLoading={corpLookupLoading}
        />
      )}

      {/* Rename modal (reuses same form for edits) */}
      {renaming && selected && (
        <RenameStockpileModal
          initial={selected}
          onClose={() => setRenaming(false)}
          onSaved={async (updated) => {
            setRenaming(false);
            setSelected(updated);
            await refreshList();
          }}
        />
      )}
    </div>
  );
}

// ---------- Sub components ----------

function ThresholdInput({
  value,
  onCommit,
}: {
  value: number;
  onCommit: (n: number) => void;
}) {
  const [draft, setDraft] = useState<string>(String(value));
  useEffect(() => {
    setDraft(String(value));
  }, [value]);
  return (
    <input
      className="w-32 bg-eve-panel border border-eve-border rounded px-1 py-0.5 text-sm text-right"
      type="number"
      min={0}
      step={1}
      value={draft}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={() => {
        const n = Number(draft);
        if (Number.isFinite(n) && n !== value) onCommit(n);
      }}
    />
  );
}

function RenameStockpileModal({
  initial,
  onClose,
  onSaved,
}: {
  initial: Stockpile;
  onClose: () => void;
  onSaved: (updated: Stockpile) => void;
}) {
  const { t } = useI18n();
  const { addToast } = useGlobalToast();
  const pushToast = ({ kind, message }: { kind: "error" | "warning" | "success" | "info"; message: string }) => addToast(message, kind);
  const [name, setName] = useState(initial.name);
  const [saving, setSaving] = useState(false);
  const save = async () => {
    const clean = name.trim();
    if (!clean || clean === initial.name) {
      onClose();
      return;
    }
    setSaving(true);
    try {
      const updated = await updateStockpile(initial.id, { name: clean });
      onSaved(updated);
    } catch (err) {
      pushToast({ kind: "error", message: (err as Error).message });
      setSaving(false);
    }
  };
  return (
    <ModalShell onClose={onClose}>
      <div className="text-sm font-semibold mb-2">{t("stockpileRenameButton")}</div>
      <label className="block text-xs text-eve-dim mb-1">{t("stockpileNameLabel")}</label>
      <input
        className="w-full bg-eve-panel border border-eve-border rounded px-2 py-1 text-sm mb-3"
        value={name}
        onChange={(e) => setName(e.target.value)}
      />
      <div className="flex justify-end gap-2">
        <button type="button" className="px-3 py-1 text-xs border border-eve-border rounded" onClick={onClose}>
          Cancel
        </button>
        <button
          type="button"
          className="px-3 py-1 text-xs bg-eve-accent text-black font-semibold rounded disabled:opacity-50"
          onClick={save}
          disabled={saving || !name.trim()}
        >
          Save
        </button>
      </div>
    </ModalShell>
  );
}

function CreateStockpileModal({
  onClose,
  onCreated,
  authCharacters,
  corpByChar,
  corpLookupLoading,
}: {
  onClose: () => void;
  onCreated: (id: number) => void;
  authCharacters: AuthCharacter[];
  corpByChar: Map<number, CharacterCorpInfo>;
  corpLookupLoading: boolean;
}) {
  // Manual structure/station ID entry — fallback when the character list can't
  // discover the structure (e.g. new citadel the user just got docking rights
  // to; no assets or orders there yet).
  const [manualId, setManualId] = useState("");
  const { t } = useI18n();
  const { addToast } = useGlobalToast();
  const pushToast = ({ kind, message }: { kind: "error" | "warning" | "success" | "info"; message: string }) => addToast(message, kind);
  const [name, setName] = useState("");
  const [source, setSource] = useState<"character" | "corporation">("character");
  const [characterId, setCharacterId] = useState<number>(
    authCharacters[0]?.character_id ?? 0,
  );

  // Auth may still be loading when the modal opens — pick a default character
  // as soon as one is available so structure lookups can succeed.
  useEffect(() => {
    if (characterId === 0 && authCharacters.length > 0) {
      setCharacterId(authCharacters[0].character_id);
    }
  }, [authCharacters, characterId]);

  // Distinct corp options derived from characters that have a role response.
  const corpOptions = useMemo(() => {
    const seen = new Map<number, string>();
    for (const info of corpByChar.values()) {
      if (!seen.has(info.corporationId)) {
        seen.set(info.corporationId, info.characterName);
      }
    }
    return Array.from(seen.entries()).map(([id, viaName]) => ({ id, viaName }));
  }, [corpByChar]);
  const [corpId, setCorpId] = useState<number>(corpOptions[0]?.id ?? 0);

  useEffect(() => {
    if (corpOptions.length > 0 && !corpOptions.some((o) => o.id === corpId)) {
      setCorpId(corpOptions[0].id);
    }
  }, [corpOptions, corpId]);

  // Station picker
  const [systemName, setSystemName] = useState("");
  const [systemId, setSystemId] = useState<number | null>(null);
  const [regionId, setRegionId] = useState<number | null>(null);
  const [stationOptions, setStationOptions] = useState<StationOption[]>([]);
  const [stationsLoading, setStationsLoading] = useState(false);
  const [stationId, setStationId] = useState<number>(0);

  useEffect(() => {
    if (!systemName) {
      setStationOptions([]);
      setSystemId(null);
      setRegionId(null);
      return;
    }
    let cancelled = false;
    setStationsLoading(true);
    (async () => {
      try {
        const npc = await getStations(systemName);
        // Fetch structures for EVERY logged-in character and merge, so a
        // citadel visible to any one character shows up in the picker.
        // Falling back to the active session with undefined would only ever
        // return one char's view.
        const charsToTry = authCharacters.length > 0
          ? authCharacters.map((c) => c.character_id)
          : [undefined as number | undefined];
        const structureLists = await Promise.all(
          charsToTry.map((cid) =>
            getStructures(npc.system_id, npc.region_id, undefined, cid).catch(
              () => [] as StationInfo[],
            ),
          ),
        );
        if (cancelled) return;
        const byId = new Map<number, StationInfo>();
        for (const list of structureLists) {
          for (const s of list) {
            if (!byId.has(s.id)) byId.set(s.id, s);
          }
        }
        const structures = Array.from(byId.values());
        setSystemId(npc.system_id);
        setRegionId(npc.region_id);
        const opts: StationOption[] = [
          ...npc.stations.map((s) => ({ ...s, label: s.name })),
          ...structures.map((s) => ({ ...s, label: `${s.name} (structure)` })),
        ];
        setStationOptions(opts);
        // Force explicit user pick — don't auto-select the first result. The
        // Create button gates on stationId > 0, so a stale default from a
        // previous system would let users click Create without noticing they
        // hadn't chosen anything.
        setStationId(0);
      } catch (err) {
        if (!cancelled) pushToast({ kind: "error", message: (err as Error).message });
      } finally {
        if (!cancelled) setStationsLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
    // Refetch when the auth character list changes so a newly-added character
    // can surface citadels only they can see.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [systemName, authCharacters]);

  const [saving, setSaving] = useState(false);
  const create = async () => {
    if (!name.trim()) return;
    if (source === "character" && !characterId) return;
    if (source === "corporation" && !corpId) return;
    if (!stationId) return;
    setSaving(true);
    try {
      const created = await createStockpile({
        name: name.trim(),
        source,
        source_character_id: source === "character" ? characterId : undefined,
        source_corporation_id: source === "corporation" ? corpId : undefined,
        station_id: stationId,
      });
      onCreated(created.id);
    } catch (err) {
      const msg = (err as Error).message;
      pushToast({
        kind: "error",
        message: msg.includes("already exists")
          ? t("stockpileNameConflict")
          : `${t("stockpileCreateFailed")}: ${msg}`,
      });
      setSaving(false);
    }
  };

  const canSave =
    !!name.trim() &&
    ((source === "character" && characterId > 0) ||
      (source === "corporation" && corpId > 0)) &&
    stationId > 0;

  return (
    <ModalShell onClose={onClose}>
      <div className="text-sm font-semibold mb-3">+ {t("stockpileNewButton")}</div>

      <label className="block text-xs text-eve-dim mb-1">{t("stockpileNameLabel")}</label>
      <input
        className="w-full bg-eve-panel border border-eve-border rounded px-2 py-1 text-sm mb-3"
        value={name}
        onChange={(e) => setName(e.target.value)}
        autoFocus
      />

      <label className="block text-xs text-eve-dim mb-1">{t("stockpileSourceLabel")}</label>
      <div className="inline-flex rounded-sm border border-eve-border overflow-hidden mb-3">
        {(["character", "corporation"] as const).map((s) => (
          <button
            key={s}
            type="button"
            className={`px-2 py-1 text-xs uppercase tracking-wide ${
              source === s ? "bg-eve-accent/20 text-eve-accent" : "bg-eve-panel text-eve-dim"
            }`}
            onClick={() => setSource(s)}
          >
            {s === "character"
              ? t("stockpileSourceCharacter")
              : t("stockpileSourceCorporation")}
          </button>
        ))}
      </div>

      {source === "character" ? (
        <>
          <label className="block text-xs text-eve-dim mb-1">
            {t("stockpileCharacterPickLabel")}
          </label>
          <select
            className="w-full bg-eve-panel border border-eve-border rounded px-2 py-1 text-sm mb-3"
            value={characterId}
            onChange={(e) => setCharacterId(Number(e.target.value))}
          >
            {authCharacters.map((c) => (
              <option key={c.character_id} value={c.character_id}>
                {c.character_name}
              </option>
            ))}
          </select>
        </>
      ) : (
        <>
          <label className="block text-xs text-eve-dim mb-1">
            {t("stockpileCorporationLabel")}
          </label>
          <select
            className="w-full bg-eve-panel border border-eve-border rounded px-2 py-1 text-sm mb-3"
            value={corpId}
            onChange={(e) => setCorpId(Number(e.target.value))}
            disabled={corpOptions.length === 0}
          >
            {corpOptions.length === 0 && (
              <option value={0}>
                {corpLookupLoading
                  ? t("stockpileCorpLookupLoading")
                  : t("stockpileCorpNoRoleFound")}
              </option>
            )}
            {corpOptions.map((c) => (
              <option key={c.id} value={c.id}>
                Corp #{c.id} (via {c.viaName})
              </option>
            ))}
          </select>
        </>
      )}

      {/* Two-step location picker: pick the SYSTEM, then a station/structure
          inside it. Fenced with a border so the two-step relationship is
          visually obvious — previous version put a single "Station or
          structure" label above the SystemAutocomplete, which read as if
          the autocomplete itself searched station names. */}
      <div className="border border-eve-border rounded p-2 mb-3">
        <div className="text-xs text-eve-dim mb-2 font-semibold">
          {t("stockpileLocationHeading")}
        </div>

        <label className="block text-[11px] text-eve-dim mb-1">
          {t("stockpileSystemLabel")}
        </label>
        <div className="mb-2">
          <SystemAutocomplete
            value={systemName}
            onChange={setSystemName}
            isLoggedIn={true}
            hasAccessibleLocations={stationOptions.length > 0}
            suppressInternalHint={true}
          />
        </div>

        <label className="block text-[11px] text-eve-dim mb-1">
          {t("stockpileStationLabel")}
        </label>
        {stationsLoading ? (
          <div className="text-xs text-eve-dim mb-2">{t("stockpileStationsLoading")}</div>
        ) : stationOptions.length === 0 ? (
          <div className="text-xs text-eve-dim mb-2">
            {systemName
              ? t("stockpileNoStationsInSystem")
              : t("stockpileStationPickPromptSystem")}
          </div>
        ) : (
          <select
            className="w-full bg-eve-panel border border-eve-border rounded px-2 py-1 text-sm mb-2"
            value={manualId ? 0 : stationId}
            onChange={(e) => {
              setStationId(Number(e.target.value));
              setManualId("");
            }}
          >
            <option value={0}>{t("stockpileStationPickPromptStation")}</option>
            {stationOptions.map((s) => (
              <option key={s.id} value={s.id}>
                {s.label}
              </option>
            ))}
          </select>
        )}

        {/* Manual ID fallback — always visible so the user has an escape hatch
            when a citadel isn't discoverable (no assets/orders there yet). */}
        <label className="block text-[11px] text-eve-dim mb-1">
          {t("stockpileManualStationIdLabel")}
        </label>
        <input
          className="w-full bg-eve-panel border border-eve-border rounded px-2 py-1 text-sm mb-1 font-mono"
          placeholder={t("stockpileManualStationIdPlaceholder")}
          value={manualId}
          onChange={(e) => {
            const v = e.target.value.replace(/[^\d]/g, "");
            setManualId(v);
            const n = Number(v);
            if (Number.isFinite(n) && n > 0) setStationId(n);
            else if (v === "") setStationId(0);
          }}
        />
        <div className="text-[10px] text-eve-dim">
          {t("stockpileManualStationIdHint")}
        </div>
      </div>

      {!canSave && stationId <= 0 && !!systemName && (
        <div className="text-[11px] text-yellow-300 mb-2">
          {t("stockpileMissingStation")}
        </div>
      )}
      <div className="flex justify-end gap-2">
        <button
          type="button"
          className="px-3 py-1 text-xs border border-eve-border rounded"
          onClick={onClose}
        >
          Cancel
        </button>
        <button
          type="button"
          className="px-3 py-1 text-xs bg-eve-accent text-black font-semibold rounded disabled:opacity-50"
          onClick={create}
          disabled={!canSave || saving}
        >
          Create
        </button>
      </div>
      {/* systemId / regionId reserved for future edits (e.g. showing region badge next to structures) */}
      <input type="hidden" value={systemId ?? ""} readOnly />
      <input type="hidden" value={regionId ?? ""} readOnly />
    </ModalShell>
  );
}

function ModalShell({ children, onClose }: { children: React.ReactNode; onClose: () => void }) {
  return (
    <div
      className="fixed inset-0 bg-black/70 z-50 flex items-center justify-center p-4"
      onClick={onClose}
    >
      <div
        className="bg-eve-bg border border-eve-border rounded-sm p-4 w-full max-w-md"
        onClick={(e) => e.stopPropagation()}
      >
        {children}
      </div>
    </div>
  );
}
