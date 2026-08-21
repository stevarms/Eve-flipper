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
import { formatISK } from "@/lib/format";
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

function fmtISKOrDash(n: number | null | undefined): string {
  if (n == null || !Number.isFinite(n) || n <= 0) return "—";
  return formatISK(n);
}

const UNCATEGORIZED = "Uncategorized";

type SortKey = "name" | "threshold" | "current" | "shortfall" | "refill";
type SortDir = "asc" | "desc";

// Unified row that renders whether or not a scan has been run. `null` on the
// dynamic columns means "no scan yet" and the UI shows an em dash.
interface MergedRow {
  typeId: number;
  typeName: string;
  groupName: string;
  categoryName: string;
  threshold: number;
  current: number | null;
  shortfall: number | null;
  unitPrice: number | null;
  onHandValue: number | null;
  refillCost: number | null;
}

function bucketOf(groupName: string | undefined): string {
  const t = (groupName ?? "").trim();
  return t === "" ? UNCATEGORIZED : t;
}

function compareRows(a: MergedRow, b: MergedRow, key: SortKey, dir: SortDir): number {
  const mul = dir === "asc" ? 1 : -1;
  const numOr = (n: number | null | undefined) => (n == null ? -1 : n);
  switch (key) {
    case "name":
      return a.typeName.localeCompare(b.typeName) * mul;
    case "threshold":
      return (a.threshold - b.threshold) * mul;
    case "current":
      return (numOr(a.current) - numOr(b.current)) * mul;
    case "shortfall":
      return (numOr(a.shortfall) - numOr(b.shortfall)) * mul;
    case "refill":
      return (numOr(a.refillCost) - numOr(b.refillCost)) * mul;
  }
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

  // ---- Sort + grouping state ----
  const [sortKey, setSortKey] = useState<SortKey>("refill");
  const [sortDir, setSortDir] = useState<SortDir>("desc");
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(new Set());

  const toggleSort = useCallback((key: SortKey) => {
    setSortKey((prevKey) => {
      if (prevKey === key) {
        setSortDir((d) => (d === "asc" ? "desc" : "asc"));
        return prevKey;
      }
      // Default direction: descending for numeric columns, ascending for name.
      setSortDir(key === "name" ? "asc" : "desc");
      return key;
    });
  }, []);

  const toggleGroup = useCallback((name: string) => {
    setCollapsedGroups((prev) => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  }, []);

  // Merge stored items with scan results into one row model. Before scan,
  // dynamic columns are null and render as "—". After scan, we take the
  // scan rows verbatim.
  const mergedRows: MergedRow[] = useMemo(() => {
    if (scanResult && scanResult.items.length > 0) {
      return scanResult.items.map((r) => ({
        typeId: r.type_id,
        typeName: r.type_name,
        groupName: bucketOf(r.group_name),
        categoryName: r.category_name ?? "",
        threshold: r.threshold_qty,
        current: r.current_qty,
        shortfall: r.shortfall,
        unitPrice: r.unit_price ?? null,
        onHandValue: r.on_hand_value ?? null,
        refillCost: r.refill_cost ?? null,
      }));
    }
    if (!selected?.items) return [];
    return selected.items.map((it) => ({
      typeId: it.type_id,
      typeName: it.type_name,
      groupName: bucketOf(it.group_name),
      categoryName: it.category_name ?? "",
      threshold: it.threshold_qty,
      current: null,
      shortfall: null,
      unitPrice: null,
      onHandValue: null,
      refillCost: null,
    }));
  }, [scanResult, selected]);

  // Apply the "only shortfalls" filter (post-scan) then bucket by group,
  // then sort rows within each bucket by the current sort column. Groups
  // themselves are ordered by their total refill cost desc if a scan is
  // present, alphabetically otherwise — so the buckets you're spending the
  // most on rise to the top.
  const groupedRows: Array<{ group: string; rows: MergedRow[]; subtotalRefill: number }> = useMemo(() => {
    const filtered = scanResult && !showAll ? mergedRows.filter((r) => (r.shortfall ?? 0) > 0) : mergedRows;
    const byGroup = new Map<string, MergedRow[]>();
    for (const r of filtered) {
      const bucket = r.groupName || UNCATEGORIZED;
      const arr = byGroup.get(bucket) ?? [];
      arr.push(r);
      byGroup.set(bucket, arr);
    }
    const groups = Array.from(byGroup.entries()).map(([group, rows]) => ({
      group,
      rows: [...rows].sort((a, b) => compareRows(a, b, sortKey, sortDir)),
      subtotalRefill: rows.reduce((sum, r) => sum + (r.refillCost ?? 0), 0),
    }));
    groups.sort((a, b) => {
      if (scanResult) return b.subtotalRefill - a.subtotalRefill;
      return a.group.localeCompare(b.group);
    });
    return groups;
  }, [mergedRows, scanResult, showAll, sortKey, sortDir]);

  const addAllShortfallsToMultibuy = useCallback(() => {
    if (!scanResult) return;
    const next = new Map<number, { name: string; qty: number }>();
    for (const row of scanResult.items) {
      if (row.shortfall > 0) {
        next.set(row.type_id, { name: row.type_name, qty: row.shortfall });
      }
    }
    setMultibuyQueue(next);
  }, [scanResult]);

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

          {/* Scan controls + summary + chart */}
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
              <>
                <label className="text-xs text-eve-dim flex items-center gap-1 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={showAll}
                    onChange={(e) => setShowAll(e.target.checked)}
                  />
                  {showAll ? t("stockpileShowShortfallOnly") : t("stockpileShowAll")}
                </label>
                <button
                  type="button"
                  className="px-2 py-1 text-xs border border-eve-border rounded"
                  onClick={addAllShortfallsToMultibuy}
                  disabled={scanResult.summary.rows_short === 0}
                >
                  {t("stockpileQueueAllShortfalls")}
                </button>
              </>
            )}
          </div>

          {scanResult?.warnings && scanResult.warnings.length > 0 && (
            <div className="text-xs text-yellow-300">
              <div className="font-semibold">{t("stockpileWarningsHeading")}</div>
              <ul className="list-disc list-inside">
                {scanResult.warnings.map((w, i) => (
                  <li key={i}>{w}</li>
                ))}
              </ul>
            </div>
          )}

          {scanResult && (
            <StockpileSummaryTiles summary={scanResult.summary} />
          )}
          {scanResult && Object.keys(scanResult.summary.refill_by_group ?? {}).length > 0 && (
            <RefillByGroupChart data={scanResult.summary.refill_by_group ?? {}} />
          )}

          {/* Merged items table */}
          <div>
            {selectedLoading ? (
              <div className="text-xs text-eve-dim">…</div>
            ) : mergedRows.length === 0 ? (
              <div className="text-xs text-eve-dim">{t("stockpileItemsEmpty")}</div>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-sm border-collapse">
                  <thead className="text-left text-xs text-eve-dim border-b border-eve-border">
                    <tr>
                      <th className="py-1 pr-3">
                        <SortHeader label={t("stockpileTypeCol")} col="name" sortKey={sortKey} sortDir={sortDir} onToggle={toggleSort} />
                      </th>
                      <th className="py-1 pr-3 text-right">
                        <SortHeader label={t("stockpileThresholdCol")} col="threshold" sortKey={sortKey} sortDir={sortDir} onToggle={toggleSort} align="right" />
                      </th>
                      <th className="py-1 pr-3 text-right">
                        <SortHeader label={t("stockpileCurrentCol")} col="current" sortKey={sortKey} sortDir={sortDir} onToggle={toggleSort} align="right" />
                      </th>
                      <th className="py-1 pr-3 text-right">
                        <SortHeader label={t("stockpileShortfallCol")} col="shortfall" sortKey={sortKey} sortDir={sortDir} onToggle={toggleSort} align="right" />
                      </th>
                      <th className="py-1 pr-3 text-right">
                        <SortHeader label={t("stockpileRefillCostCol")} col="refill" sortKey={sortKey} sortDir={sortDir} onToggle={toggleSort} align="right" />
                      </th>
                      <th className="py-1 text-right">{scanResult ? t("stockpileMultibuyAddedLabel") : ""}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {groupedRows.map(({ group, rows, subtotalRefill }) => {
                      const collapsed = collapsedGroups.has(group);
                      return (
                        <>
                          <tr
                            key={`hdr-${group}`}
                            className="bg-eve-panel/50 border-t border-eve-border cursor-pointer"
                            onClick={() => toggleGroup(group)}
                          >
                            <td colSpan={4} className="py-1 pr-3 text-xs font-semibold uppercase tracking-wide text-eve-accent">
                              <span className="inline-block w-3 text-eve-dim">{collapsed ? "▸" : "▾"}</span>{" "}
                              {group}{" "}
                              <span className="text-eve-dim font-normal normal-case ml-1">
                                ({rows.length})
                              </span>
                            </td>
                            <td className="py-1 pr-3 text-right text-xs text-eve-dim">
                              {scanResult ? fmtISKOrDash(subtotalRefill) : ""}
                            </td>
                            <td />
                          </tr>
                          {!collapsed &&
                            rows.map((row) => {
                              const inQueue = multibuyQueue.get(row.typeId);
                              const hasShortfall = (row.shortfall ?? 0) > 0;
                              return (
                                <tr key={row.typeId} className="border-b border-eve-border/50">
                                  <td className="py-1 pr-3">{row.typeName}</td>
                                  <td className="py-1 pr-3 text-right">
                                    <ThresholdInput
                                      value={row.threshold}
                                      onCommit={(n) => updateItemThreshold(row.typeId, row.typeName, n)}
                                    />
                                  </td>
                                  <td className="py-1 pr-3 text-right">
                                    {row.current == null ? <span className="text-eve-dim">—</span> : fmtInt(row.current)}
                                    {row.onHandValue != null && row.onHandValue > 0 && (
                                      <div className="text-[10px] text-eve-dim">{formatISK(row.onHandValue)}</div>
                                    )}
                                  </td>
                                  <td className={`py-1 pr-3 text-right ${hasShortfall ? "text-yellow-300 font-semibold" : "text-eve-dim"}`}>
                                    {row.shortfall == null ? "—" : fmtInt(row.shortfall)}
                                  </td>
                                  <td className="py-1 pr-3 text-right">
                                    {fmtISKOrDash(row.refillCost)}
                                  </td>
                                  <td className="py-1 pr-1">
                                    {scanResult ? (
                                      <div className="inline-flex items-center gap-1 justify-end w-full">
                                        <button
                                          type="button"
                                          className="px-1.5 py-0.5 text-xs border border-eve-border rounded disabled:opacity-30"
                                          onClick={() =>
                                            bumpMultibuy(
                                              row.typeId,
                                              row.typeName,
                                              -Math.max(1, Math.floor((row.shortfall ?? 0) / 10) || 1),
                                            )
                                          }
                                          disabled={!inQueue}
                                          aria-label="-"
                                        >
                                          −
                                        </button>
                                        <input
                                          className="w-20 bg-eve-panel border border-eve-border rounded px-1 py-0.5 text-xs text-right"
                                          type="number"
                                          min={0}
                                          step={1}
                                          value={inQueue?.qty ?? 0}
                                          onChange={(e) =>
                                            setMultibuyQty(row.typeId, row.typeName, Number(e.target.value))
                                          }
                                        />
                                        <button
                                          type="button"
                                          className="px-1.5 py-0.5 text-xs border border-eve-border rounded"
                                          onClick={() =>
                                            bumpMultibuy(
                                              row.typeId,
                                              row.typeName,
                                              Math.max(1, Math.floor((row.shortfall ?? 0) / 10) || 1),
                                            )
                                          }
                                          aria-label="+"
                                        >
                                          +
                                        </button>
                                        <button
                                          type="button"
                                          className="text-xs text-red-300 hover:text-red-200 ml-1"
                                          onClick={() => removeItem(row.typeId)}
                                        >
                                          ×
                                        </button>
                                      </div>
                                    ) : (
                                      <button
                                        type="button"
                                        className="text-xs text-red-300 hover:text-red-200"
                                        onClick={() => removeItem(row.typeId)}
                                      >
                                        {t("stockpileRowRemove")}
                                      </button>
                                    )}
                                  </td>
                                </tr>
                              );
                            })}
                        </>
                      );
                    })}
                  </tbody>
                </table>
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

function SortHeader({
  label,
  col,
  sortKey,
  sortDir,
  onToggle,
  align = "left",
}: {
  label: string;
  col: SortKey;
  sortKey: SortKey;
  sortDir: SortDir;
  onToggle: (c: SortKey) => void;
  align?: "left" | "right";
}) {
  const active = sortKey === col;
  const arrow = active ? (sortDir === "asc" ? "▲" : "▼") : "";
  return (
    <button
      type="button"
      className={`w-full ${align === "right" ? "text-right" : "text-left"} text-xs font-semibold uppercase tracking-wide ${
        active ? "text-eve-accent" : "text-eve-dim"
      } hover:text-eve-text`}
      onClick={() => onToggle(col)}
    >
      {label} <span className="text-[10px]">{arrow}</span>
    </button>
  );
}

// Compact 3-tile summary. Only rendered when a scan has run.
function StockpileSummaryTiles({
  summary,
}: {
  summary: import("@/lib/types").StockpileScanSummary;
}) {
  const tiles = [
    { label: "Inventory value", value: summary.total_inventory_value, color: "text-eve-accent" },
    { label: "Refill cost", value: summary.total_refill_cost, color: "text-yellow-300" },
  ];
  return (
    <div className="grid grid-cols-2 sm:grid-cols-3 gap-2">
      {tiles.map((t) => (
        <div key={t.label} className="border border-eve-border rounded p-2">
          <div className="text-[10px] uppercase text-eve-dim tracking-wide">{t.label}</div>
          <div className={`text-lg font-semibold ${t.color}`}>{formatISK(t.value)}</div>
        </div>
      ))}
      <div className="border border-eve-border rounded p-2">
        <div className="text-[10px] uppercase text-eve-dim tracking-wide">Rows short</div>
        <div className="text-lg font-semibold">
          {summary.rows_short}
          <span className="text-eve-dim text-sm"> / {summary.row_count}</span>
        </div>
      </div>
      {summary.pricing_failed && (
        <div className="col-span-full text-[10px] text-yellow-300">
          Jita 4-4 sell price fetch failed — value/cost columns may be 0.
        </div>
      )}
    </div>
  );
}

// Horizontal bar chart, one bar per group, width proportional to refill
// cost. Inline SVG so it themes automatically and needs no chart library.
function RefillByGroupChart({ data }: { data: Record<string, number> }) {
  const rows = Object.entries(data)
    .filter(([, v]) => v > 0)
    .sort((a, b) => b[1] - a[1])
    .slice(0, 12); // top 12 groups keeps the chart legible

  if (rows.length === 0) return null;
  const max = rows[0][1];
  const rowH = 18;
  const gap = 4;
  const chartH = rows.length * (rowH + gap);
  const labelW = 140;
  const valueW = 90;

  return (
    <div className="border border-eve-border rounded p-2">
      <div className="text-[10px] uppercase text-eve-dim tracking-wide mb-1">
        Refill cost by group
      </div>
      <svg width="100%" height={chartH} viewBox={`0 0 500 ${chartH}`} preserveAspectRatio="none">
        {rows.map(([name, value], i) => {
          const y = i * (rowH + gap);
          const barMaxW = 500 - labelW - valueW - 8;
          const w = Math.max(1, (value / max) * barMaxW);
          return (
            <g key={name} transform={`translate(0, ${y})`}>
              <text
                x={labelW - 6}
                y={rowH * 0.75}
                textAnchor="end"
                className="fill-eve-text"
                fontSize={11}
              >
                {name.length > 22 ? `${name.slice(0, 20)}…` : name}
              </text>
              <rect x={labelW} y={2} width={w} height={rowH - 4} className="fill-eve-accent" opacity={0.75} />
              <text
                x={labelW + w + 4}
                y={rowH * 0.75}
                className="fill-eve-dim"
                fontSize={10}
              >
                {formatISK(value)}
              </text>
            </g>
          );
        })}
      </svg>
    </div>
  );
}

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
