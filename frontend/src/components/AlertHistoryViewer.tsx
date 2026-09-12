import { useCallback, useEffect, useState } from "react";
import { OpenMarketButton } from "@/components/ui/OpenMarketButton";
import { CopyButton } from "@/components/ui/CopyButton";
import { getAlertHistory } from "@/lib/api";
import type { AlertHistoryEntry } from "@/lib/types";
import { useI18n } from "@/lib/i18n";
import { formatISK } from "@/lib/format";
import { Modal } from "./Modal";

interface Props {
  typeId?: number;
  typeName?: string;
  onClose: () => void;
}

export function AlertHistoryViewer({ typeId, typeName, onClose }: Props) {
  const { t } = useI18n();
  const [history, setHistory] = useState<AlertHistoryEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [page, setPage] = useState(0);
  const [hasMore, setHasMore] = useState(false);
  const pageSize = 50;

  const loadHistory = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await getAlertHistory(typeId, pageSize, page * pageSize);
      setHistory(data);
      setHasMore(data.length === pageSize);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load history");
    } finally {
      setLoading(false);
    }
  }, [typeId, page, pageSize]);

  useEffect(() => {
    setPage(0);
  }, [typeId]);

  useEffect(() => {
    loadHistory();
  }, [loadHistory]);

  const formatMetricValue = (metric: string, value: number) => {
    switch (metric) {
      case "margin_percent":
        return `${value.toFixed(2)}%`;
      case "total_profit":
      case "profit_per_unit":
        return formatISK(value);
      case "daily_volume":
        return Math.round(value).toLocaleString();
      default:
        return value.toFixed(2);
    }
  };

  const formatChannel = (channel: string) => {
    const icons: Record<string, string> = {
      telegram: "📱",
      discord: "💬",
      desktop: "🖥️",
    };
    return icons[channel] || channel;
  };

  const formatTimestamp = (timestamp: string) => {
    const date = new Date(timestamp);
    const now = new Date();
    const diffMs = now.getTime() - date.getTime();
    const diffMins = Math.floor(diffMs / 60000);
    const diffHours = Math.floor(diffMs / 3600000);
    const diffDays = Math.floor(diffMs / 86400000);

    if (diffMins < 1) return t("watchlistAlertJustNow");
    if (diffMins < 60) return `${diffMins}${t("watchlistAlertMinutesAgo")}`;
    if (diffHours < 24) return `${diffHours}${t("watchlistAlertHoursAgo")}`;
    if (diffDays < 7) return `${diffDays}${t("watchlistAlertDaysAgo")}`;
    return date.toLocaleString();
  };

  const getMetricLabel = (metric: string) => {
    const labels: Record<string, string> = {
      margin_percent: t("watchlistMetricMargin"),
      total_profit: t("watchlistMetricTotalProfit"),
      profit_per_unit: t("watchlistMetricProfitPerUnit"),
      daily_volume: t("watchlistMetricDailyVolume"),
    };
    return labels[metric] || metric;
  };

  return (
    <Modal open={true} onClose={onClose} title={typeName ? `${t("watchlistAlertHistoryFor")} ${typeName}` : t("watchlistAlertHistoryTitle")}>
      <div className="flex flex-col gap-3 max-h-[70vh]">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-eve-border pb-2">
          <div className="text-sm text-eve-dim">
            {typeId ? `${t("watchlistShowingAlertsFor")} ${typeName}` : t("watchlistShowingAllAlerts")}
          </div>
          <button
            onClick={loadHistory}
            className="px-2 py-1 text-xs text-eve-dim hover:text-eve-accent transition-colors"
            title={t("watchlistRefresh")}
          >
            ↻ {t("watchlistRefresh")}
          </button>
        </div>

        {/* Content */}
        <div className="flex-1 overflow-y-auto">
          {loading && (
            <div className="text-center text-eve-dim py-8">{t("loading")}</div>
          )}

          {error && (
            <div className="text-center text-red-400 py-8">
              {t("watchlistError")}: {error}
            </div>
          )}

          {!loading && !error && history.length === 0 && (
            <div className="text-center text-eve-dim py-8">
              {t("watchlistNoAlertsYet")}
            </div>
          )}

          {!loading && !error && history.length > 0 && (
            <div className="space-y-2">
              {history.map((entry) => (
                <div
                  key={entry.id}
                  className="group bg-eve-card border border-eve-border rounded p-3 hover:border-eve-accent/30 transition-colors"
                >
                  {/* Alert Message */}
                  <div className="flex items-start justify-between gap-2 mb-2">
                    <div className="flex-1">
                      <div className="flex items-center gap-1 text-sm font-medium text-eve-text">
                        <span className="truncate">{entry.type_name}</span>
                        {/* Every entry in this viewer is the same type, so the
                            id comes from the prop rather than the row. */}
                        <OpenMarketButton
                          typeId={typeId ?? 0}
                          reveal="hover"
                          label={t("openMarketHint")}
                        />
                        {entry.type_name && (
                          <CopyButton
                            text={entry.type_name}
                            label={t("copyItem")}
                            reveal="hover"
                          />
                        )}
                      </div>
                      <div className="text-xs text-eve-dim mt-0.5">
                        {getMetricLabel(entry.alert_metric)}:{" "}
                        <span className="text-eve-accent">
                          {formatMetricValue(entry.alert_metric, entry.current_value)}
                        </span>
                        {" ≥ "}
                        <span className="text-eve-dim">
                          {formatMetricValue(entry.alert_metric, entry.alert_threshold)}
                        </span>
                      </div>
                    </div>
                    <div className="text-xs text-eve-dim whitespace-nowrap">
                      {formatTimestamp(entry.sent_at)}
                    </div>
                  </div>

                  {/* Channels */}
                  <div className="flex items-center gap-2 flex-wrap">
                    <span className="text-xs text-eve-dim">{t("watchlistAlertSentVia")}:</span>
                    {entry.channels_sent.map((ch) => (
                      <span
                        key={ch}
                        className="text-xs px-1.5 py-0.5 bg-eve-bg/50 rounded border border-eve-border text-eve-text"
                        title={ch}
                      >
                        {formatChannel(ch)} {ch}
                      </span>
                    ))}
                    {entry.channels_failed && Object.keys(entry.channels_failed).length > 0 && (
                      <>
                        <span className="text-xs text-eve-dim">{t("watchlistAlertFailed")}:</span>
                        {Object.entries(entry.channels_failed).map(([ch, err]) => (
                          <span
                            key={ch}
                            className="text-xs px-1.5 py-0.5 bg-red-900/20 rounded border border-red-800 text-red-400"
                            title={err}
                          >
                            {formatChannel(ch)} {ch}
                          </span>
                        ))}
                      </>
                    )}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Footer Stats */}
        {!loading && !error && history.length > 0 && (
          <div className="border-t border-eve-border pt-2 text-xs text-eve-dim">
            <div className="text-center">
              {history.length} {history.length === 1 ? t("watchlistAlertDisplayed") : t("watchlistAlertsDisplayed")}
            </div>
            <div className="mt-2 flex items-center justify-center gap-2">
              <button
                onClick={() => setPage((p) => Math.max(0, p - 1))}
                disabled={loading || page === 0}
                className="px-2 py-1 border border-eve-border rounded disabled:opacity-40 hover:border-eve-accent/40 transition-colors"
              >
                {t("watchlistPrevPage")}
              </button>
              <span>{t("watchlistPage")} {page + 1}</span>
              <button
                onClick={() => setPage((p) => p + 1)}
                disabled={loading || !hasMore}
                className="px-2 py-1 border border-eve-border rounded disabled:opacity-40 hover:border-eve-accent/40 transition-colors"
              >
                {t("watchlistNextPage")}
              </button>
            </div>
          </div>
        )}
      </div>
    </Modal>
  );
}
