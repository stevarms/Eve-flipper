import { useCallback, useEffect, useMemo, useState } from "react";
import type { FlipResult, WatchlistItem } from "@/lib/types";
import {
  addToWatchlist,
  getWatchlist,
  removeFromWatchlist,
  updateWatchlistItem,
} from "@/lib/api";
import { formatISK, formatMargin } from "@/lib/format";
import { useI18n, type TranslationKey } from "@/lib/i18n";
import { useGlobalToast } from "./Toast";
import { ConfirmDialog } from "./ConfirmDialog";
import { Modal } from "./Modal";
import { AlertHistoryViewer } from "./AlertHistoryViewer";

type AlertChannels = {
  telegram: boolean;
  discord: boolean;
  desktop: boolean;
};

interface Props {
  latestResults: FlipResult[];
  alertChannels: AlertChannels;
  toggleAlertChannel: (channel: keyof AlertChannels) => void;
  alertTelegramToken: string;
  setAlertTelegramToken: (val: string) => void;
  alertTelegramChatID: string;
  setAlertTelegramChatID: (val: string) => void;
  alertDiscordWebhook: string;
  setAlertDiscordWebhook: (val: string) => void;
  handleTestAlert: () => void;
  alertTestLoading: boolean;
}

type SortKey =
  | "type_name"
  | "alert_min_margin"
  | "margin"
  | "profit"
  | "buy"
  | "sell"
  | "added_at";
type SortDir = "asc" | "desc";
type AlertMetric =
  | "margin_percent"
  | "total_profit"
  | "profit_per_unit"
  | "daily_volume";

type WatchlistColumnDef = {
  key: SortKey;
  label: string;
  align: string;
  width: string;
  widthPx?: number;
  pinned?: boolean;
};

const WATCHLIST_COLUMN_PREFS_STORAGE_KEY = "eve-watchlist-columns:v1";

function watchlistColumnDefaultWidthPx(width: string): number {
  const exact = width.match(/w-\[(\d+)px\]/)?.[1];
  const min = width.match(/min-w-\[(\d+)px\]/)?.[1];
  const parsed = Number(exact ?? min ?? 110);
  return Number.isFinite(parsed) ? Math.max(44, Math.min(420, parsed)) : 110;
}

function watchlistColumnWidthStyle(col: WatchlistColumnDef, left?: number) {
  const widthPx = col.widthPx ?? watchlistColumnDefaultWidthPx(col.width);
  return {
    width: widthPx,
    minWidth: widthPx,
    ...(typeof left === "number" ? { left } : {}),
  };
}

function getAlertMetric(item: WatchlistItem): AlertMetric {
  const metric = item.alert_metric;
  if (
    metric === "margin_percent" ||
    metric === "total_profit" ||
    metric === "profit_per_unit" ||
    metric === "daily_volume"
  ) {
    return metric;
  }
  return "margin_percent";
}

function getAlertThreshold(item: WatchlistItem): number {
  if ((item.alert_threshold ?? 0) > 0) {
    return item.alert_threshold ?? 0;
  }
  return item.alert_min_margin ?? 0;
}

function isAlertEnabled(item: WatchlistItem): boolean {
  if (typeof item.alert_enabled === "boolean") {
    return item.alert_enabled;
  }
  return getAlertThreshold(item) > 0;
}

function metricValue(match: FlipResult | undefined, metric: AlertMetric): number {
  if (!match) return 0;
  switch (metric) {
    case "margin_percent":
      return match.MarginPercent ?? 0;
    case "total_profit":
      return match.TotalProfit ?? 0;
    case "profit_per_unit":
      return match.ProfitPerUnit ?? 0;
    case "daily_volume":
      return match.DailyVolume ?? 0;
    default:
      return 0;
  }
}

function bestMatchForMetric(rows: FlipResult[], typeID: number, metric: AlertMetric): FlipResult | undefined {
  let best: FlipResult | undefined;
  let bestValue = 0;
  for (const row of rows) {
    if (row.TypeID !== typeID) continue;
    const value = metricValue(row, metric);
    if (!best || value > bestValue) {
      best = row;
      bestValue = value;
    }
  }
  return best;
}

function formatMetricValue(metric: AlertMetric, value: number): string {
  switch (metric) {
    case "margin_percent":
      return formatMargin(value);
    case "total_profit":
    case "profit_per_unit":
      return formatISK(value);
    case "daily_volume":
      return `${Math.round(value).toLocaleString()}`;
    default:
      return String(value);
  }
}

export function WatchlistTab({
  latestResults,
  alertChannels,
  toggleAlertChannel,
  alertTelegramToken,
  setAlertTelegramToken,
  alertTelegramChatID,
  setAlertTelegramChatID,
  alertDiscordWebhook,
  setAlertDiscordWebhook,
  handleTestAlert,
  alertTestLoading,
}: Props) {
  const { t } = useI18n();
  const { addToast } = useGlobalToast();
  const [items, setItems] = useState<WatchlistItem[]>([]);
  const [search, setSearch] = useState("");
  const [sortKey, setSortKey] = useState<SortKey>("added_at");
  const [sortDir, setSortDir] = useState<SortDir>("desc");
  const [showColumnPanel, setShowColumnPanel] = useState(false);
  const [columnOrder, setColumnOrder] = useState<SortKey[]>([
    "type_name",
    "alert_min_margin",
    "margin",
    "profit",
    "buy",
    "sell",
    "added_at",
  ]);
  const [hiddenColumns, setHiddenColumns] = useState<Set<SortKey>>(new Set());
  const [columnWidths, setColumnWidths] = useState<Partial<Record<SortKey, number>>>({});
  const [pinnedColumns, setPinnedColumns] = useState<Set<SortKey>>(new Set());
  const [draggedColumnKey, setDraggedColumnKey] = useState<SortKey | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<{
    id: number;
    name: string;
  } | null>(null);

  const [editorItem, setEditorItem] = useState<WatchlistItem | null>(null);
  const [historyViewer, setHistoryViewer] = useState<{
    typeId?: number;
    typeName?: string;
  } | null>(null);
  const [showAlertConfig, setShowAlertConfig] = useState(false);
  const [editorEnabled, setEditorEnabled] = useState(false);
  const [editorMetric, setEditorMetric] = useState<AlertMetric>("margin_percent");
  const [editorThreshold, setEditorThreshold] = useState("0");
  const editorMatch = useMemo(() => {
    if (!editorItem) return undefined;
    return bestMatchForMetric(latestResults, editorItem.type_id, getAlertMetric(editorItem));
  }, [editorItem, latestResults]);

  const reload = useCallback(() => {
    getWatchlist()
      .then(setItems)
      .catch(() =>
        addToast(
          t("watchlistError" as TranslationKey) || "Failed to load watchlist",
          "error",
          3000,
        ),
      );
  }, [addToast, t]);

  useEffect(() => {
    reload();
  }, [reload]);

  useEffect(() => {
    try {
      const raw = localStorage.getItem(WATCHLIST_COLUMN_PREFS_STORAGE_KEY);
      if (!raw) return;
      const parsed = JSON.parse(raw) as {
        order?: SortKey[];
        hidden?: SortKey[];
        widths?: Partial<Record<SortKey, number>>;
        pinned?: SortKey[];
      };
      const defaults: SortKey[] = ["type_name", "alert_min_margin", "margin", "profit", "buy", "sell", "added_at"];
      const available = new Set(defaults);
      const nextOrder = (parsed.order ?? []).filter((key) => available.has(key));
      for (const key of defaults) {
        if (!nextOrder.includes(key)) nextOrder.push(key);
      }
      const nextHidden = new Set((parsed.hidden ?? []).filter((key) => available.has(key)));
      const nextPinned = new Set((parsed.pinned ?? []).filter((key) => available.has(key)));
      const nextWidths: Partial<Record<SortKey, number>> = {};
      for (const [key, value] of Object.entries(parsed.widths ?? {}) as [SortKey, number][]) {
        if (available.has(key) && Number.isFinite(value)) {
          nextWidths[key] = Math.max(44, Math.min(420, Math.round(value)));
        }
      }
      setColumnOrder(nextOrder);
      setHiddenColumns(nextHidden);
      setPinnedColumns(nextPinned);
      setColumnWidths(nextWidths);
    } catch {
      // ignore broken local preferences
    }
  }, []);

  useEffect(() => {
    try {
      localStorage.setItem(
        WATCHLIST_COLUMN_PREFS_STORAGE_KEY,
        JSON.stringify({
          order: columnOrder,
          hidden: [...hiddenColumns],
          widths: columnWidths,
          pinned: [...pinnedColumns],
        }),
      );
    } catch {
      // ignore storage failures
    }
  }, [columnOrder, hiddenColumns, columnWidths, pinnedColumns]);

  const handleRemove = (typeId: number) => {
    removeFromWatchlist(typeId)
      .then((list) => {
        setItems(list);
        addToast(
          t("watchlistRemoved" as TranslationKey) || "Removed from watchlist",
          "success",
          2000,
        );
      })
      .catch(() =>
        addToast(
          t("watchlistError" as TranslationKey) || "Operation failed",
          "error",
          3000,
        ),
      );
  };

  const openAlertEditor = (item: WatchlistItem) => {
    setEditorItem(item);
    setEditorEnabled(isAlertEnabled(item));
    setEditorMetric(getAlertMetric(item));
    setEditorThreshold(String(getAlertThreshold(item)));
  };

  const saveAlertEditor = () => {
    if (!editorItem) return;
    const threshold = Number(editorThreshold);
    if (!Number.isFinite(threshold) || threshold < 0) {
      addToast(t("watchlistError"), "error", 2500);
      return;
    }
    updateWatchlistItem(editorItem.type_id, {
      alert_enabled: editorEnabled,
      alert_metric: editorMetric,
      alert_threshold: threshold,
      alert_min_margin: editorMetric === "margin_percent" ? threshold : 0,
    })
      .then((list) => {
        setItems(list);
        setEditorItem(null);
        addToast(
          t("watchlistThresholdSaved" as TranslationKey) || "Saved",
          "success",
          2000,
        );
      })
      .catch(() =>
        addToast(
          t("watchlistError" as TranslationKey) || "Operation failed",
          "error",
          3000,
        ),
      );
  };

  const enriched = useMemo(
    () =>
      items.map((item) => {
        const metric = getAlertMetric(item);
        const match = bestMatchForMetric(latestResults, item.type_id, metric);
        const threshold = getAlertThreshold(item);
        const enabled = isAlertEnabled(item);
        const current = metricValue(match, metric);
        const isAlert = enabled && threshold > 0 && !!match && current >= threshold;
        return { ...item, match, metric, threshold, enabled, current, isAlert };
      }),
    [items, latestResults],
  );

  const displayed = useMemo(() => {
    let list = enriched;
    if (search.trim()) {
      const q = search.toLowerCase();
      list = list.filter((item) => item.type_name.toLowerCase().includes(q));
    }
    list = [...list].sort((a, b) => {
      let cmp = 0;
      switch (sortKey) {
        case "type_name":
          cmp = a.type_name.localeCompare(b.type_name);
          break;
        case "alert_min_margin":
          cmp = a.threshold - b.threshold;
          break;
        case "margin":
          cmp = a.current - b.current;
          break;
        case "profit":
          cmp = (a.match?.TotalProfit ?? -1) - (b.match?.TotalProfit ?? -1);
          break;
        case "buy":
          cmp = (a.match?.BuyPrice ?? -1) - (b.match?.BuyPrice ?? -1);
          break;
        case "sell":
          cmp = (a.match?.SellPrice ?? -1) - (b.match?.SellPrice ?? -1);
          break;
        case "added_at":
          cmp = new Date(a.added_at).getTime() - new Date(b.added_at).getTime();
          break;
      }
      return sortDir === "asc" ? cmp : -cmp;
    });
    return list;
  }, [enriched, search, sortKey, sortDir]);

  const toggleSort = (key: SortKey) => {
    if (sortKey === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortKey(key);
      setSortDir("desc");
    }
  };
  const sortIndicator = (key: SortKey) =>
    sortKey === key ? (sortDir === "asc" ? " ▲" : " ▼") : "";

  const handleExport = () => {
    const data = items.map((i) => ({
      type_id: i.type_id,
      type_name: i.type_name,
      alert_min_margin: i.alert_min_margin,
      alert_enabled: isAlertEnabled(i),
      alert_metric: getAlertMetric(i),
      alert_threshold: getAlertThreshold(i),
    }));
    navigator.clipboard.writeText(JSON.stringify(data, null, 2));
    addToast(
      t("watchlistExported" as TranslationKey) ||
        "Watchlist copied to clipboard",
      "success",
      2000,
    );
  };

  const handleImport = async () => {
    try {
      const json = await navigator.clipboard.readText();
      const parsed = JSON.parse(json);
      if (!Array.isArray(parsed)) throw new Error("not array");
      let imported = 0;
      for (const item of parsed) {
        if (!item.type_id || !item.type_name) continue;
        try {
          const r = await addToWatchlist(
            item.type_id,
            item.type_name,
            item.alert_min_margin ?? 0,
          );
          if (r.inserted) imported++;
          if (item.alert_metric || item.alert_threshold || item.alert_enabled) {
            const importedThreshold = Number(item.alert_threshold ?? item.alert_min_margin ?? 0);
            await updateWatchlistItem(item.type_id, {
              alert_enabled:
                typeof item.alert_enabled === "boolean"
                  ? item.alert_enabled
                  : importedThreshold > 0,
              alert_metric: item.alert_metric ?? "margin_percent",
              alert_threshold: importedThreshold,
              alert_min_margin: Number(item.alert_min_margin ?? 0),
            });
          }
        } catch {
          // skip invalid row
        }
      }
      reload();
      addToast(
        `${t("watchlistImported" as TranslationKey) || "Imported"}: ${imported}`,
        "success",
        2000,
      );
    } catch {
      addToast(t("watchlistImportInvalid"), "error", 3000);
    }
  };

  const metricOptions: { value: AlertMetric; label: string; unit: string }[] = [
    { value: "margin_percent", label: t("watchlistMetricMargin"), unit: "%" },
    { value: "total_profit", label: t("watchlistMetricTotalProfit"), unit: "ISK" },
    { value: "profit_per_unit", label: t("watchlistMetricProfitPerUnit"), unit: "ISK" },
    { value: "daily_volume", label: t("watchlistMetricDailyVolume"), unit: t("watchlistMetricDailyVolumeUnit") },
  ];

  const baseColumns = useMemo<WatchlistColumnDef[]>(
    () => [
      { key: "type_name", label: t("colItem"), align: "text-left", width: "min-w-[150px]" },
      { key: "alert_min_margin", label: t("watchlistThreshold"), align: "text-right", width: "min-w-[130px]" },
      { key: "margin", label: t("watchlistAlertCurrentValue"), align: "text-right", width: "min-w-[80px]" },
      { key: "profit", label: t("watchlistCurrentProfit"), align: "text-right", width: "min-w-[90px]" },
      { key: "buy", label: t("watchlistBuyAt"), align: "text-right", width: "min-w-[90px]" },
      { key: "sell", label: t("watchlistSellAt"), align: "text-right", width: "min-w-[90px]" },
      { key: "added_at", label: t("watchlistAdded"), align: "text-center", width: "min-w-[80px]" },
    ],
    [t],
  );

  const columns = useMemo(() => {
    const byKey = new Map(baseColumns.map((col) => [col.key, col] as const));
    const ordered = columnOrder
      .map((key) => byKey.get(key))
      .filter((col): col is WatchlistColumnDef => Boolean(col));
    for (const col of baseColumns) {
      if (!ordered.some((existing) => existing.key === col.key)) ordered.push(col);
    }
    return ordered
      .filter((col) => !hiddenColumns.has(col.key))
      .map((col) => ({
        ...col,
        widthPx: columnWidths[col.key] ?? watchlistColumnDefaultWidthPx(col.width),
        pinned: pinnedColumns.has(col.key),
      }))
      .sort((a, b) => {
        if (a.pinned === b.pinned) return 0;
        return a.pinned ? -1 : 1;
      });
  }, [baseColumns, columnOrder, columnWidths, hiddenColumns, pinnedColumns]);

  const pinnedLeftByKey = useMemo(() => {
    let left = 0;
    const offsets = new Map<SortKey, number>();
    for (const col of columns) {
      if (!col.pinned) continue;
      offsets.set(col.key, left);
      left += col.widthPx ?? watchlistColumnDefaultWidthPx(col.width);
    }
    return offsets;
  }, [columns]);

  const setColumnWidth = useCallback((key: SortKey, widthPx: number) => {
    setColumnWidths((prev) => ({ ...prev, [key]: Math.max(44, Math.min(420, Math.round(widthPx))) }));
  }, []);

  const resetColumns = useCallback(() => {
    setColumnOrder(baseColumns.map((col) => col.key));
    setHiddenColumns(new Set());
    setColumnWidths({});
    setPinnedColumns(new Set());
  }, [baseColumns]);

  const toggleColumnVisibility = useCallback((key: SortKey, visible: boolean) => {
    setHiddenColumns((prev) => {
      const next = new Set(prev);
      if (visible) next.delete(key);
      else if (columns.length > 1) next.add(key);
      return next;
    });
  }, [columns.length]);

  const toggleColumnPin = useCallback((key: SortKey) => {
    setPinnedColumns((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }, []);

  const moveColumn = useCallback((key: SortKey, delta: -1 | 1) => {
    setColumnOrder((prev) => {
      const next = [...prev];
      const idx = next.indexOf(key);
      if (idx < 0) return prev;
      const target = Math.max(0, Math.min(next.length - 1, idx + delta));
      if (target === idx) return prev;
      next.splice(idx, 1);
      next.splice(target, 0, key);
      return next;
    });
  }, []);

  const dropColumn = useCallback((fromKey: SortKey, toKey: SortKey) => {
    if (fromKey === toKey) return;
    setColumnOrder((prev) => {
      const next = [...prev];
      const from = next.indexOf(fromKey);
      const to = next.indexOf(toKey);
      if (from < 0 || to < 0) return prev;
      next.splice(from, 1);
      next.splice(to, 0, fromKey);
      return next;
    });
  }, []);

  const startColumnResize = useCallback((key: SortKey, event: import("react").MouseEvent) => {
    event.preventDefault();
    event.stopPropagation();
    const startX = event.clientX;
    const startWidth = columnWidths[key] ?? watchlistColumnDefaultWidthPx(baseColumns.find((col) => col.key === key)?.width ?? "");
    const onMove = (moveEvent: MouseEvent) => setColumnWidth(key, startWidth + moveEvent.clientX - startX);
    const onUp = () => {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
  }, [baseColumns, columnWidths, setColumnWidth]);

  const renderWatchlistCell = (col: WatchlistColumnDef, item: (typeof displayed)[number]) => {
    switch (col.key) {
      case "type_name":
        return (
          <>
            {item.isAlert && <span className="mr-1 text-green-400">!</span>}
            {item.type_name}
          </>
        );
      case "alert_min_margin":
        return item.enabled && item.threshold > 0 ? (
          <span className="text-eve-accent">
            {`${metricOptions.find((m) => m.value === item.metric)?.label ?? item.metric}: ${formatMetricValue(item.metric, item.threshold)}`}
          </span>
        ) : (
          <span className="text-eve-dim">{t("watchlistAlertOff")}</span>
        );
      case "margin":
        return item.match ? (
          <span className={item.isAlert ? "text-green-400" : "text-eve-accent"}>
            {formatMetricValue(item.metric, item.current)}
          </span>
        ) : (
          <span className="text-eve-dim">-</span>
        );
      case "profit":
        return item.match ? <span className="text-green-400">{formatISK(item.match.TotalProfit)}</span> : <span className="text-eve-dim">-</span>;
      case "buy":
        return item.match ? formatISK(item.match.BuyPrice) : "-";
      case "sell":
        return item.match ? formatISK(item.match.SellPrice) : "-";
      case "added_at":
        return new Date(item.added_at).toLocaleDateString();
      default:
        return "";
    }
  };

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-3 border-b border-eve-border px-3 py-2 flex-wrap">
        <span className="shrink-0 text-[10px] uppercase tracking-wider text-eve-dim font-medium">
          ⭐ {t("tabWatchlist")} ({items.length})
        </span>

        {items.length > 0 && (
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={t("watchlistSearch" as TranslationKey) || "Search..."}
            className="w-full sm:w-36 px-2 py-1 bg-eve-input border border-eve-border rounded-sm text-eve-text text-xs focus:outline-none focus:border-eve-accent focus:ring-1 focus:ring-eve-accent/30 transition-colors"
          />
        )}
        <div className="flex-1" />
        <div className="flex items-center gap-1.5">
          <button
            onClick={() => setShowAlertConfig(true)}
            className="px-2 py-1 rounded-sm text-[11px] text-eve-dim hover:text-eve-accent transition-colors"
            title={t("alertConfigTitle")}
          >
            🔔 {t("alertConfigShort")}
          </button>
          {items.length > 0 && (
            <>
              <span className="text-eve-border">|</span>
              <button
                onClick={() => setHistoryViewer({})}
                className="px-2 py-1 rounded-sm text-[11px] text-eve-dim hover:text-eve-accent transition-colors"
                title={t("watchlistViewHistory")}
              >
                📋 {t("watchlistAlertHistory")}
              </button>
              <span className="text-eve-border">|</span>
              <button
                onClick={handleExport}
                className="px-2 py-1 rounded-sm text-[11px] text-eve-dim hover:text-eve-text transition-colors"
                title={t("watchlistExport" as TranslationKey) || "Export"}
              >
                {t("presetExport" as TranslationKey) || "Export"}
              </button>
              <span className="text-eve-border">|</span>
            </>
          )}
          <button
            onClick={handleImport}
            className="px-2 py-1 rounded-sm text-[11px] text-eve-dim hover:text-eve-text transition-colors"
            title={t("watchlistImport" as TranslationKey) || "Import"}
          >
            {t("presetImport" as TranslationKey) || "Import"}
          </button>
          {items.length > 0 && (
            <button
              type="button"
              onClick={() => setShowColumnPanel((value) => !value)}
              className={`px-2 py-1 rounded-sm text-[11px] border transition-colors ${
                showColumnPanel
                  ? "border-eve-accent/50 bg-eve-accent/10 text-eve-accent"
                  : "border-eve-border text-eve-dim hover:text-eve-text"
              }`}
            >
              Columns
            </button>
          )}
          <button
            onClick={reload}
            className="px-3 py-1 rounded-sm text-xs text-eve-dim hover:text-eve-accent border border-eve-border hover:border-eve-accent/30 transition-colors cursor-pointer"
          >
            ↻
          </button>
        </div>
      </div>

      {showColumnPanel && items.length > 0 && (
        <div className="border-b border-eve-border bg-eve-panel/80 px-3 py-2 text-xs">
          <div className="mb-2 flex items-center gap-2">
            <span className="text-[10px] uppercase tracking-widest text-eve-dim">Watchlist columns</span>
            <div className="flex-1" />
            <button type="button" onClick={() => setHiddenColumns(new Set())} className="border border-eve-border px-2 py-0.5 text-[10px] uppercase tracking-wider text-eve-dim hover:text-eve-text">
              Show all
            </button>
            <button type="button" onClick={resetColumns} className="border border-eve-border px-2 py-0.5 text-[10px] uppercase tracking-wider text-eve-dim hover:text-eve-text">
              Reset
            </button>
          </div>
          <div className="grid gap-1 md:grid-cols-2 xl:grid-cols-3">
            {baseColumns
              .slice()
              .sort((a, b) => columnOrder.indexOf(a.key) - columnOrder.indexOf(b.key))
              .map((col) => {
                const visible = !hiddenColumns.has(col.key);
                const pinned = pinnedColumns.has(col.key);
                return (
                  <div
                    key={col.key}
                    draggable
                    onDragStart={() => setDraggedColumnKey(col.key)}
                    onDragOver={(event) => event.preventDefault()}
                    onDrop={() => {
                      if (draggedColumnKey) dropColumn(draggedColumnKey, col.key);
                      setDraggedColumnKey(null);
                    }}
                    onDragEnd={() => setDraggedColumnKey(null)}
                    className="flex items-center gap-2 border border-eve-border/70 bg-eve-dark/60 px-2 py-1"
                  >
                    <span className="cursor-grab text-eve-dim">::</span>
                    <input
                      type="checkbox"
                      checked={visible}
                      onChange={(event) => toggleColumnVisibility(col.key, event.target.checked)}
                      className="accent-eve-accent"
                    />
                    <button type="button" onClick={() => moveColumn(col.key, -1)} className="text-eve-dim hover:text-eve-accent">&lt;</button>
                    <button type="button" onClick={() => moveColumn(col.key, 1)} className="text-eve-dim hover:text-eve-accent">&gt;</button>
                    <span className="min-w-0 flex-1 truncate text-eve-text">{col.label}</span>
                    <button
                      type="button"
                      onClick={() => toggleColumnPin(col.key)}
                      className={`border px-1.5 py-0.5 text-[10px] uppercase tracking-wider ${
                        pinned ? "border-eve-accent text-eve-accent" : "border-eve-border text-eve-dim"
                      }`}
                    >
                      Pin
                    </button>
                    <input
                      type="number"
                      min={44}
                      max={420}
                      value={columnWidths[col.key] ?? watchlistColumnDefaultWidthPx(col.width)}
                      onChange={(event) => setColumnWidth(col.key, Number(event.target.value))}
                      className="w-16 border border-eve-border bg-eve-input px-1 py-0.5 text-right font-mono text-eve-text"
                    />
                  </div>
                );
              })}
          </div>
        </div>
      )}

      <div className="flex-1 min-h-0 overflow-auto table-scroll-wrapper table-scroll-container">
        {items.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center text-eve-dim text-xs">
            <span>{t("watchlistEmpty")}</span>
            <span className="mt-1 text-[10px] text-eve-dim/70">
              {t("watchlistHint")}
            </span>
          </div>
        ) : (
          <table className="w-full text-xs">
            <thead className="sticky top-0 bg-eve-panel z-10">
              <tr className="text-eve-dim text-[10px] uppercase tracking-wider border-b border-eve-border">
                {columns.map((col) => (
                  <th
                    key={col.key}
                    style={watchlistColumnWidthStyle(col, col.pinned ? pinnedLeftByKey.get(col.key) : undefined)}
                    className={`relative px-3 py-2 font-medium cursor-pointer hover:text-eve-accent transition-colors select-none ${col.align} ${
                      col.pinned ? "sticky z-20 bg-eve-panel shadow-[4px_0_0_rgba(0,0,0,0.25)]" : ""
                    }`}
                    onClick={() => toggleSort(col.key)}
                  >
                    {col.label}
                    {sortIndicator(col.key)}
                    <span
                      role="separator"
                      aria-orientation="vertical"
                      onMouseDown={(event) => startColumnResize(col.key, event)}
                      className="absolute right-0 top-1/2 h-5 w-1 -translate-y-1/2 cursor-col-resize hover:bg-eve-accent/50"
                    />
                  </th>
                ))}
                <th className="px-3 py-2 text-center text-[10px] text-eve-dim w-16">{t("watchlistAlertActions")}</th>
              </tr>
            </thead>
            <tbody>
              {displayed.map((item, i) => (
                <tr
                  key={item.type_id}
                  onDoubleClick={() => openAlertEditor(item)}
                  className={`border-b border-eve-border/30 transition-colors cursor-pointer ${
                    item.isAlert
                      ? "bg-green-900/20 hover:bg-green-900/30"
                      : i % 2 === 0
                        ? "bg-eve-panel hover:bg-eve-accent/5"
                        : "bg-eve-dark hover:bg-eve-accent/5"
                  }`}
                  title={t("watchlistDoubleClickHint")}
                >
                  {columns.map((col) => (
                    <td
                      key={col.key}
                      style={watchlistColumnWidthStyle(col, col.pinned ? pinnedLeftByKey.get(col.key) : undefined)}
                      className={`px-3 py-2 truncate ${
                        col.key === "type_name" ? "text-eve-text font-medium" : `${col.align} font-mono text-eve-text`
                      } ${col.pinned ? "sticky z-10 bg-inherit shadow-[4px_0_0_rgba(0,0,0,0.25)]" : ""}`}
                    >
                      {renderWatchlistCell(col, item)}
                    </td>
                  ))}
                  <td className="hidden">
                    {item.isAlert && <span className="mr-1">🔔</span>}
                    {item.type_name}
                  </td>
                  <td className="hidden">
                    {item.enabled && item.threshold > 0 ? (
                      <span className="text-eve-accent">
                        {`${metricOptions.find((m) => m.value === item.metric)?.label ?? item.metric}: ${formatMetricValue(item.metric, item.threshold)}`}
                      </span>
                    ) : (
                      <span className="text-eve-dim">{t("watchlistAlertOff")}</span>
                    )}
                  </td>
                  <td className="hidden">
                    {item.match ? (
                      <span
                        className={
                          item.isAlert ? "text-green-400" : "text-eve-accent"
                        }
                      >
                        {formatMetricValue(item.metric, item.current)}
                      </span>
                    ) : (
                      <span className="text-eve-dim">—</span>
                    )}
                  </td>
                  <td className="hidden">
                    {item.match ? (
                      <span className="text-green-400">{formatISK(item.match.TotalProfit)}</span>
                    ) : (
                      <span className="text-eve-dim">—</span>
                    )}
                  </td>
                  <td className="hidden">
                    {item.match ? formatISK(item.match.BuyPrice) : "—"}
                  </td>
                  <td className="hidden">
                    {item.match ? formatISK(item.match.SellPrice) : "—"}
                  </td>
                  <td className="hidden">
                    {new Date(item.added_at).toLocaleDateString()}
                  </td>
                  <td className="px-3 py-2 text-center">
                    <div className="flex items-center justify-center gap-1">
                      <button
                        onClick={(e) => {
                          e.stopPropagation();
                          setHistoryViewer({ typeId: item.type_id, typeName: item.type_name });
                        }}
                        className="text-eve-dim hover:text-eve-accent transition-colors cursor-pointer text-xs px-1"
                        title={t("watchlistViewHistory")}
                      >
                        📋
                      </button>
                      <button
                        onClick={(e) => {
                          e.stopPropagation();
                          setConfirmDelete({ id: item.type_id, name: item.type_name });
                        }}
                        className="text-eve-dim hover:text-eve-error transition-colors cursor-pointer text-sm px-1"
                        title={t("removeFromWatchlist")}
                      >
                        ✕
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {enriched.some((e) => e.match) && (
        <div className="shrink-0 flex items-center gap-6 px-3 py-1.5 border-t border-eve-border text-xs">
          <span className="text-eve-dim">
            {t("watchlistTracked")}:{" "}
            <span className="text-eve-accent font-mono">
              {enriched.filter((e) => e.match).length}/{items.length}
            </span>
          </span>
          <span className="text-eve-dim">
            {t("watchlistAlerts")}:{" "}
            <span className="text-green-400 font-mono">
              {enriched.filter((e) => e.isAlert).length}
            </span>
          </span>
        </div>
      )}

      {editorItem && (
        <Modal
          open={true}
          onClose={() => setEditorItem(null)}
          title={t("watchlistAlertSetupTitle")}
          width="max-w-lg"
        >
          <div className="p-4 space-y-3">
            <div className="text-sm text-eve-text font-medium">{editorItem.type_name}</div>
            <label className="flex items-center gap-2 text-sm text-eve-text">
              <input
                type="checkbox"
                checked={editorEnabled}
                onChange={(e) => setEditorEnabled(e.target.checked)}
                className="rounded border-eve-border bg-eve-input text-eve-accent focus:ring-eve-accent/40"
              />
              <span>{t("watchlistAlertEnable")}</span>
            </label>
            <div>
              <div className="mb-1 text-[11px] uppercase tracking-wider text-eve-dim">
                {t("watchlistAlertMetric")}
              </div>
              <select
                value={editorMetric}
                onChange={(e) => setEditorMetric(e.target.value as AlertMetric)}
                className="w-full px-2 py-2 text-sm bg-eve-input border border-eve-border rounded-sm text-eve-text focus:outline-none focus:border-eve-accent focus:ring-1 focus:ring-eve-accent/30"
              >
                {metricOptions.map((opt) => (
                  <option key={opt.value} value={opt.value}>
                    {opt.label}
                  </option>
                ))}
              </select>
            </div>
            <div>
              <div className="mb-1 text-[11px] uppercase tracking-wider text-eve-dim">
                {t("watchlistAlertThreshold")}
              </div>
              <div className="flex items-center gap-2">
                <input
                  type="number"
                  min={0}
                  value={editorThreshold}
                  onChange={(e) => setEditorThreshold(e.target.value)}
                  className="w-full px-2 py-2 text-sm bg-eve-input border border-eve-border rounded-sm text-eve-text focus:outline-none focus:border-eve-accent focus:ring-1 focus:ring-eve-accent/30"
                />
                <span className="text-xs text-eve-dim shrink-0">
                  {metricOptions.find((m) => m.value === editorMetric)?.unit ?? ""}
                </span>
              </div>
            </div>
            {editorMatch && (
              <div className="text-xs text-eve-dim">
                {t("watchlistAlertCurrentValue")}:{" "}
                <span className="text-eve-accent font-mono">
                  {formatMetricValue(editorMetric, metricValue(editorMatch, editorMetric))}
                </span>
              </div>
            )}
            <div className="flex items-center justify-end gap-2 pt-2">
              <button
                onClick={() => setEditorItem(null)}
                className="px-3 py-1.5 border border-eve-border rounded-sm text-xs text-eve-dim hover:text-eve-text hover:border-eve-accent/30 transition-colors"
              >
                {t("cancel")}
              </button>
              <button
                onClick={saveAlertEditor}
                className="px-3 py-1.5 border border-eve-accent/40 bg-eve-accent/10 rounded-sm text-xs text-eve-accent hover:bg-eve-accent/20 transition-colors"
              >
                {t("presetSaveBtn")}
              </button>
            </div>
          </div>
        </Modal>
      )}

      {confirmDelete && (
        <ConfirmDialog
          open={true}
          title={t("removeFromWatchlist")}
          message={`${t("watchlistConfirmRemove" as TranslationKey) || "Remove"} "${confirmDelete.name}"?`}
          onConfirm={() => {
            handleRemove(confirmDelete.id);
            setConfirmDelete(null);
          }}
          onClose={() => setConfirmDelete(null)}
          variant="danger"
        />
      )}

      {historyViewer && (
        <AlertHistoryViewer
          typeId={historyViewer.typeId}
          typeName={historyViewer.typeName}
          onClose={() => setHistoryViewer(null)}
        />
      )}

      {showAlertConfig && (
        <Modal
          open={true}
          onClose={() => setShowAlertConfig(false)}
          title={t("alertConfigTitle")}
          width="max-w-xl"
        >
          <div className="p-4 space-y-4">
            <p className="text-xs text-eve-dim">{t("alertConfigHint")}</p>
            <div className="space-y-2">
              <label className="flex items-center gap-3 p-2 rounded-sm border border-eve-border bg-eve-panel/40">
                <input
                  type="checkbox"
                  checked={alertChannels.telegram}
                  onChange={() => toggleAlertChannel("telegram")}
                  className="accent-eve-accent"
                />
                <span className="text-sm text-eve-text">{t("alertChannelTelegram")}</span>
              </label>
              <div className="pl-9 pr-1">
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                  <input
                    type="password"
                    value={alertTelegramToken}
                    onChange={(e) => setAlertTelegramToken(e.target.value)}
                    placeholder={t("alertConfigTelegramToken")}
                    className="w-full px-2 py-1 rounded-sm border border-eve-border bg-eve-dark text-eve-text text-xs"
                  />
                  <input
                    type="text"
                    value={alertTelegramChatID}
                    onChange={(e) => setAlertTelegramChatID(e.target.value)}
                    placeholder={t("alertConfigTelegramChatID")}
                    className="w-full px-2 py-1 rounded-sm border border-eve-border bg-eve-dark text-eve-text text-xs"
                  />
                </div>
                <div className="mt-1 text-[10px] text-eve-dim">{t("alertConfigTelegramHint")}</div>
              </div>
              <label className="flex items-center gap-3 p-2 rounded-sm border border-eve-border bg-eve-panel/40">
                <input
                  type="checkbox"
                  checked={alertChannels.discord}
                  onChange={() => toggleAlertChannel("discord")}
                  className="accent-eve-accent"
                />
                <span className="text-sm text-eve-text">{t("alertChannelDiscord")}</span>
              </label>
              <div className="pl-9 pr-1">
                <input
                  type="password"
                  value={alertDiscordWebhook}
                  onChange={(e) => setAlertDiscordWebhook(e.target.value)}
                  placeholder={t("alertConfigDiscordWebhook")}
                  className="w-full px-2 py-1 rounded-sm border border-eve-border bg-eve-dark text-eve-text text-xs"
                />
                <div className="mt-1 text-[10px] text-eve-dim">{t("alertConfigDiscordHint")}</div>
              </div>
              <label className="flex items-center gap-3 p-2 rounded-sm border border-eve-border bg-eve-panel/40">
                <input
                  type="checkbox"
                  checked={alertChannels.desktop}
                  onChange={() => toggleAlertChannel("desktop")}
                  className="accent-eve-accent"
                />
                <span className="text-sm text-eve-text">{t("alertChannelDesktop")}</span>
              </label>
            </div>
            <div className="flex items-center justify-between text-xs">
              <span className="text-eve-dim">
                {t("alertConfigSelected", {
                  count:
                    Number(alertChannels.telegram) +
                    Number(alertChannels.discord) +
                    Number(alertChannels.desktop),
                })}
              </span>
              <div className="flex items-center gap-2">
                <button
                  onClick={handleTestAlert}
                  disabled={alertTestLoading}
                  className="px-3 py-1.5 rounded-sm border border-eve-border text-eve-dim hover:text-eve-accent hover:border-eve-accent/50 transition-colors disabled:opacity-50"
                >
                  {alertTestLoading ? `${t("loading")}...` : t("alertConfigTest")}
                </button>
                <button
                  onClick={() => setShowAlertConfig(false)}
                  className="px-3 py-1.5 rounded-sm border border-eve-border text-eve-dim hover:text-eve-accent hover:border-eve-accent/50 transition-colors"
                >
                  {t("dialogOk")}
                </button>
              </div>
            </div>
          </div>
        </Modal>
      )}
    </div>
  );
}
