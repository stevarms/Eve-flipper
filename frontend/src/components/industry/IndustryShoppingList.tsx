import { useCallback, useMemo } from "react";
import { useI18n } from "@/lib/i18n";
import { formatISK } from "@/lib/format";
import type { FlatMaterial } from "@/lib/types";
import { useGlobalToast } from "../Toast";

interface IndustryShoppingListProps {
  materials: FlatMaterial[];
  regionId: number;
  onOpenExecutionPlan: (material: FlatMaterial) => void;
}

export function IndustryShoppingList({
  materials,
  regionId,
  onOpenExecutionPlan,
}: IndustryShoppingListProps) {
  const { t } = useI18n();
  const { addToast } = useGlobalToast();

  const totalCost = useMemo(
    () => materials.reduce((sum, material) => sum + material.total_price, 0),
    [materials]
  );

  const totalVolume = useMemo(
    () => materials.reduce((sum, material) => sum + material.volume, 0),
    [materials]
  );

  // Copy the full list to EVE's multi-buy format: "Name\tQty" per line.
  // Multi-buy accepts tab-separated so a single paste populates the entire
  // buy order without hand-typing anything. Item names get sanitized of
  // stray whitespace so a rogue newline in a type name (rare but possible)
  // doesn't break a row.
  const handleCopyMultibuy = useCallback(async () => {
    if (materials.length === 0) {
      addToast(t("industryShoppingListCopyEmpty"), "warning");
      return;
    }
    const text = materials
      .map((m) => `${m.type_name.replace(/[\r\n\t]/g, " ")}\t${Math.max(1, Math.round(m.quantity))}`)
      .join("\n");
    try {
      await navigator.clipboard.writeText(text);
      addToast(
        t("industryShoppingListCopyOK").replace("{count}", String(materials.length)),
        "success",
      );
    } catch {
      addToast(t("industryShoppingListCopyFail"), "error");
    }
  }, [materials, addToast, t]);

  return (
    <div>
      <div className="flex items-center justify-between px-3 py-2 border-b border-eve-border/40">
        <div className="text-xs text-eve-dim">
          {t("industryShoppingListHeader")
            .replace("{count}", String(materials.length))
            .replace("{cost}", formatISK(totalCost))}
        </div>
        <button
          type="button"
          onClick={handleCopyMultibuy}
          disabled={materials.length === 0}
          title={t("industryShoppingListCopyMultibuyHint")}
          className="px-2 py-1 text-[11px] font-semibold rounded-sm border border-eve-accent text-eve-accent
                     hover:bg-eve-accent/10 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
        >
          {t("industryShoppingListCopyMultibuy")}
        </button>
      </div>
      <table className="w-full text-sm">
        <thead className="sticky top-0 bg-eve-dark z-10">
          <tr className="text-eve-dim text-[10px] uppercase tracking-wider border-b border-eve-border">
            <th
              style={{ width: 32, minWidth: 32, maxWidth: 32 }}
              className="px-1 py-2"
            />
            <th className="px-3 py-2 text-left font-medium">Item</th>
            <th className="px-3 py-2 text-right font-medium">Quantity</th>
            <th className="px-3 py-2 text-right font-medium">Unit Price</th>
            <th className="px-3 py-2 text-right font-medium">Total</th>
            <th className="px-3 py-2 text-right font-medium">Volume</th>
          </tr>
        </thead>
        <tbody>
          {materials.map((material, index) => (
            <tr
              key={material.type_id}
              className={`border-b border-eve-border/50 hover:bg-eve-accent/5 ${
                index % 2 === 0 ? "bg-eve-panel" : "bg-eve-dark"
              }`}
            >
              <td
                style={{ width: 32, minWidth: 32, maxWidth: 32 }}
                className="px-1 py-1.5 text-center"
              >
                {regionId > 0 && (
                  <button
                    type="button"
                    onClick={() => onOpenExecutionPlan(material)}
                    className="text-eve-dim hover:text-eve-accent transition-colors text-sm"
                    title={t("execPlanTitle")}
                  >
                    📊
                  </button>
                )}
              </td>
              <td className="px-3 py-1.5 text-eve-text">{material.type_name}</td>
              <td className="px-3 py-1.5 text-right font-mono text-eve-accent">
                {material.quantity.toLocaleString()}
              </td>
              <td className="px-3 py-1.5 text-right font-mono text-eve-dim">
                {formatISK(material.unit_price)}
              </td>
              <td className="px-3 py-1.5 text-right font-mono text-eve-accent">
                {formatISK(material.total_price)}
              </td>
              <td className="px-3 py-1.5 text-right font-mono text-eve-dim">
                {material.volume.toLocaleString(undefined, { maximumFractionDigits: 1 })} m3
              </td>
            </tr>
          ))}
        </tbody>
        <tfoot className="bg-eve-dark border-t border-eve-border">
          <tr>
            <td
              style={{ width: 32, minWidth: 32, maxWidth: 32 }}
              className="px-1 py-2"
            />
            <td className="px-3 py-2 text-eve-dim font-medium">Total</td>
            <td className="px-3 py-2 text-right font-mono text-eve-accent font-semibold">
              {materials.length} items
            </td>
            <td className="px-3 py-2" />
            <td className="px-3 py-2 text-right font-mono text-eve-accent font-semibold">
              {formatISK(totalCost)}
            </td>
            <td className="px-3 py-2 text-right font-mono text-eve-dim">
              {totalVolume.toLocaleString(undefined, { maximumFractionDigits: 1 })} m3
            </td>
          </tr>
        </tfoot>
      </table>
    </div>
  );
}
