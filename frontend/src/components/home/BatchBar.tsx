import { useCallback } from "react";
import { ClipboardList, MapPin } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useGlobalToast } from "@/components/Toast";
import { useEveUiActions } from "@/lib/eveUiActions";
import { formatISK } from "@/lib/format";
import { useI18n } from "@/lib/i18n";
import type { TodayBatch } from "@/lib/types";
import { todayMultibuyText, todayPriceListText } from "./todayFormat";

/**
 * The work that is faster in bulk than one action at a time.
 *
 * EVE's multibuy window takes a `Name\tQty` paste and fills the whole list at
 * once, so a session's buys are one paste rather than N trips through the
 * market window. The engine only puts advised rows in a batch: a batch is a
 * single commitment with no per-row decision, so slipping an unproven row into
 * one would bypass the grading entirely.
 */
export function BatchBar({ batches }: { batches: TodayBatch[] }) {
  const { t } = useI18n();
  const { addToast } = useGlobalToast();
  const { setDestination } = useEveUiActions();

  const copy = useCallback(
    async (text: string, count: number) => {
      try {
        await navigator.clipboard.writeText(text);
        addToast(t("todayBatchCopied", { n: String(count) }), "success", 2200);
      } catch {
        addToast(t("clipboardUnavailable"), "error", 2500);
      }
    },
    [addToast, t],
  );

  if (batches.length === 0) return null;

  return (
    <section className="rounded-sm border border-eve-border bg-surface-1 p-3">
      <h2 className="mb-2 font-ui text-t-title font-semibold text-fg">{t("todayBatchTitle")}</h2>
      <ul className="flex flex-col gap-2">
        {batches.map((b) => {
          const items = b.items ?? [];
          return (
            <li key={b.id} className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <span className="min-w-0 flex-1 font-ui text-t-body text-fg-secondary">
                {b.label}
                <span className="ml-2 font-num tnum text-t-caption text-fg-tertiary">
                  {items.length > 0 && `${items.length} × `}
                  {b.total_capital_isk > 0 && `${formatISK(b.total_capital_isk)} · `}
                  {formatISK(b.downside_isk_7d)}
                </span>
              </span>

              {b.kind === "multibuy" && items.length > 0 && (
                <Button
                  size="sm"
                  variant="secondary"
                  onClick={() => void copy(todayMultibuyText(items), items.length)}
                >
                  <ClipboardList className="h-3 w-3" aria-hidden="true" />
                  {t("todayBatchCopyMultibuy")}
                </Button>
              )}

              {b.kind === "reprice_list" && items.length > 0 && (
                <Button
                  size="sm"
                  variant="secondary"
                  onClick={() => void copy(todayPriceListText(items), items.length)}
                >
                  <ClipboardList className="h-3 w-3" aria-hidden="true" />
                  {t("todayBatchCopyList")}
                </Button>
              )}

              {b.kind === "waypoint" && b.destination_id != null && (
                <Button
                  size="sm"
                  variant="secondary"
                  onClick={() => void setDestination(b.destination_id as number)}
                >
                  <MapPin className="h-3 w-3" aria-hidden="true" />
                  {t("todaySetDestination")}
                </Button>
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}
