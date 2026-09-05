import { useEffect, useMemo, useState } from "react";
import { getOrderHistory } from "@/lib/api";
import type { HistoricalOrder } from "@/lib/types";
import type { TranslationKey } from "@/lib/i18n";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/EmptyState";
import { Input } from "@/components/ui/input";
import { LoadingBlock } from "@/components/ui/LoadingBlock";
import { TypeIcon } from "@/components/ui/TypeIcon";
import { cn } from "@/lib/utils";

/**
 * Closed orders — the half of the order desk that only existed inside the
 * character modal. Same data as the old CombinedOrdersTab history sub-tab,
 * re-tiered: side and station moved into the item cell rather than off the
 * screen, and the list is now fed by its own endpoint instead of riding along
 * with the whole character payload.
 */

type StateFilter = "all" | "fulfilled" | "cancelled" | "expired";

const STATE_TONE: Record<string, "profit" | "warn" | "neutral"> = {
  fulfilled: "profit",
  cancelled: "warn",
  expired: "neutral",
};

const STATE_LABEL_KEYS: Record<StateFilter, TranslationKey> = {
  all: "charAll",
  fulfilled: "charFulfilled",
  cancelled: "charCancelled",
  expired: "charExpired",
};

export function OrderHistoryPanel({
  formatIsk,
  t,
  locale,
}: {
  formatIsk: (v: number) => string;
  t: (key: TranslationKey, params?: Record<string, string | number>) => string;
  locale: string;
}) {
  const [orders, setOrders] = useState<HistoricalOrder[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState<StateFilter>("all");
  const [search, setSearch] = useState("");
  const [visible, setVisible] = useState(100);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    getOrderHistory("all")
      .then((rows) => {
        if (!cancelled) setOrders(rows);
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const counts = useMemo(() => {
    const all = orders ?? [];
    return {
      all: all.length,
      fulfilled: all.filter((o) => o.state === "fulfilled").length,
      cancelled: all.filter((o) => o.state === "cancelled").length,
      expired: all.filter((o) => o.state === "expired").length,
    };
  }, [orders]);

  const rows = useMemo(() => {
    let items = orders ?? [];
    if (filter !== "all") items = items.filter((o) => o.state === filter);
    const q = search.trim().toLowerCase();
    if (q) items = items.filter((o) => (o.type_name || "").toLowerCase().includes(q));
    return items;
  }, [orders, filter, search]);

  if (loading && !orders) return <LoadingBlock label={t("charOrderHistory")} />;
  if (error) {
    return (
      <div className="rounded-sm border border-loss/50 bg-loss/10 px-3 py-2 font-ui text-t-cell text-loss">
        {error}
      </div>
    );
  }
  if (!orders || orders.length === 0) return <EmptyState reason="no_history" />;

  return (
    <div className="flex h-full min-h-0 flex-col gap-2">
      <div className="flex flex-wrap items-center gap-1.5">
        {(["all", "fulfilled", "cancelled", "expired"] as StateFilter[]).map((f) => (
          <button
            key={f}
            type="button"
            onClick={() => {
              setFilter(f);
              setVisible(100);
            }}
            className={cn(
              "rounded-sm border px-2.5 py-1 font-ui text-t-caption transition-colors",
              filter === f
                ? "border-eve-accent bg-eve-accent/20 text-eve-accent"
                : "border-eve-border bg-surface-2 text-fg-tertiary hover:border-eve-accent/50 hover:text-fg",
            )}
          >
            {t(STATE_LABEL_KEYS[f])} ({counts[f]})
          </button>
        ))}
        <Input
          value={search}
          onChange={(e) => {
            setSearch(e.target.value);
            setVisible(100);
          }}
          placeholder={t("charSearchPlaceholder")}
          className="ml-auto h-6 w-40 px-2 py-0.5"
        />
      </div>

      <div className="min-h-0 flex-1 overflow-auto rounded-sm border border-eve-border bg-surface-1">
        {rows.length === 0 ? (
          <EmptyState reason="filters_too_strict" />
        ) : (
          <table className="w-full">
            <thead className="sticky top-0 z-10 bg-surface-0">
              <tr className="font-ui text-t-caption uppercase tracking-wide text-fg-tertiary">
                <th className="px-2 py-1.5 text-left font-medium">{t("charState")}</th>
                <th className="px-2 py-1.5 text-left font-medium">{t("colItem")}</th>
                <th className="px-2 py-1.5 text-right font-medium">{t("charPrice")}</th>
                <th className="px-2 py-1.5 text-right font-medium">{t("charFilled")}</th>
                <th className="px-2 py-1.5 text-right font-medium">{t("charIssued")}</th>
              </tr>
            </thead>
            <tbody>
              {rows.slice(0, visible).map((o) => (
                <tr key={o.order_id} className="h-row border-t border-eve-border/50 hover:bg-eve-accent/5">
                  <td className="px-2 py-1">
                    <Badge tone={STATE_TONE[o.state] ?? "neutral"}>{o.state}</Badge>
                  </td>
                  <td className="max-w-[320px] px-2 py-1">
                    <div className="flex items-center gap-1.5">
                      <TypeIcon typeId={o.type_id} size={18} />
                      <div className="min-w-0">
                        <div className="truncate font-ui text-t-body text-fg" title={o.type_name}>
                          {o.type_name || `Type #${o.type_id}`}
                        </div>
                        <div
                          className="truncate font-ui text-t-caption text-fg-tertiary"
                          title={o.location_name}
                        >
                          {o.is_buy_order ? t("charBuy") : t("charSell")} ·{" "}
                          {o.location_name || `#${o.location_id}`}
                        </div>
                      </div>
                    </div>
                  </td>
                  <td className="px-2 py-1 text-right font-num tnum text-t-cell text-fg">
                    {formatIsk(o.price)}
                  </td>
                  <td className="px-2 py-1 text-right font-num tnum text-t-cell text-fg-secondary">
                    {(o.volume_total - o.volume_remain).toLocaleString()}/
                    {o.volume_total.toLocaleString()}
                  </td>
                  <td className="px-2 py-1 text-right font-num tnum text-t-cell text-fg-tertiary">
                    {stamp(o.issued, locale)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {rows.length > visible && (
        <button
          type="button"
          onClick={() => setVisible((v) => v + 100)}
          className="w-full rounded-sm border border-eve-border py-2 text-center font-ui text-t-cell text-eve-accent transition-colors hover:bg-surface-2"
        >
          {t("andMore", { count: rows.length - visible })}
        </button>
      )}
    </div>
  );
}

function stamp(iso: string | undefined, locale: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "—" : d.toLocaleDateString(locale);
}
