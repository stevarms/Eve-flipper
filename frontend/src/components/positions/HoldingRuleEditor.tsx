import { useCallback, useState } from "react";
import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useGlobalToast } from "@/components/Toast";
import { getHoldingRulePercentiles, setHoldingRule } from "@/lib/api";
import { formatISK, formatIsk } from "@/lib/format";
import { useI18n, type TranslationKey } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import type { HoldingRuleChoice, PositionRow, PricePercentiles } from "@/lib/types";

/**
 * The two reasons a holding is not for sale today.
 *
 * Lives on Assets -> Positions because that is the tab that already asks
 * "should I sell this today?"; Today is a summary of the other tabs and only
 * reflects what is set here.
 *
 * The suggested targets come from the item's own trailing year rather than a
 * multiple of what you paid. A price the item has actually traded at a
 * quarter of the time is a price it can plausibly reach again; cost-plus-40%
 * is a number about you, not about the market.
 *
 * Five named percentiles rather than a slider: the distribution they are read
 * from is not sent to the client, and "typical / strong / rare / peak" is
 * enough to express an intent without pretending to a precision a year of
 * daily averages does not support.
 */

const CHOICE_LABEL: Record<string, TranslationKey> = {
  cheap: "holdingRuleChoiceCheap",
  typical: "holdingRuleChoiceTypical",
  strong: "holdingRuleChoiceStrong",
  rare: "holdingRuleChoiceRare",
  peak: "holdingRuleChoicePeak",
};

const unit = (n: number) =>
  formatIsk(n, undefined, { maxTier: "T", space: false, decimals: { t: 2, b: 2, m: 2, k: 2, unit: 2 } });

export function HoldingRuleEditor({
  row,
  onSaved,
}: {
  row: PositionRow;
  /** Called with the type id once a rule is stored or cleared, so the tab can
   *  refetch. The rule itself is not passed back: the server sanitises and may
   *  delete, and a refetch is the only honest source. */
  onSaved: (typeId: number) => void;
}) {
  const { t } = useI18n();
  const { addToast } = useGlobalToast();

  const [reserved, setReserved] = useState(String(row.reserved_qty ?? 0));
  const [target, setTarget] = useState(row.target_price ? String(row.target_price) : "");
  const [percentile, setPercentile] = useState(row.target_percentile ?? 0);
  const [choices, setChoices] = useState<HoldingRuleChoice[] | null>(null);
  const [stats, setStats] = useState<PricePercentiles | null>(null);
  const [loadingChoices, setLoadingChoices] = useState(false);
  const [saving, setSaving] = useState(false);

  const loadChoices = useCallback(async () => {
    if (choices || loadingChoices) return;
    setLoadingChoices(true);
    try {
      const res = await getHoldingRulePercentiles(row.type_id);
      setStats(res.percentiles);
      // No basis means too little traded history. Offer nothing rather than
      // numbers built from a handful of days.
      setChoices(res.percentiles.basis === "history" ? res.choices : []);
    } catch {
      setChoices([]);
    } finally {
      setLoadingChoices(false);
    }
  }, [choices, loadingChoices, row.type_id]);

  const save = useCallback(
    async (clear = false) => {
      setSaving(true);
      try {
        await setHoldingRule(row.type_id, {
          target_price: clear ? 0 : Number(target) || 0,
          target_percentile: clear ? 0 : percentile,
          target_basis: clear ? "" : percentile > 0 ? "percentile" : "manual",
          reserved_qty: clear ? 0 : Math.max(0, Math.floor(Number(reserved) || 0)),
          note: clear ? "" : row.rule_note ?? "",
        });
        addToast(t(clear ? "holdingRuleCleared" : "holdingRuleSaved"), "success", 2000);
        if (clear) {
          setReserved("0");
          setTarget("");
          setPercentile(0);
        }
        onSaved(row.type_id);
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        addToast(t("holdingRuleSaveFailed", { error: message }), "error", 4000);
      } finally {
        setSaving(false);
      }
    },
    [addToast, onSaved, percentile, reserved, row.rule_note, row.type_id, t, target],
  );

  const hasRule = (row.target_price ?? 0) > 0 || (row.reserved_qty ?? 0) > 0;

  return (
    <section className="mt-3 rounded-sm border border-eve-border bg-surface-2 p-2.5">
      <h3 className="font-ui text-t-body font-medium text-fg">{t("holdingRuleTitle")}</h3>
      <p className="mb-2 font-ui text-t-caption text-fg-tertiary">{t("holdingRuleHint")}</p>

      {/* Reserved units. A count, not a flag: owning six and flying two is
          the normal case, and a flag would hide the other four. */}
      <label className="mb-1 block font-ui text-t-caption text-fg-secondary">
        {t("holdingRuleReserved")}
      </label>
      <div className="mb-1 flex items-center gap-2">
        <Input
          type="number"
          min={0}
          max={row.qty}
          value={reserved}
          onChange={(e) => setReserved(e.target.value)}
          className="w-24"
        />
        <span className="font-ui text-t-caption text-fg-tertiary">
          {t("holdingRuleTradeable", {
            n: String(Math.max(0, row.qty - (Math.floor(Number(reserved) || 0) || 0))),
            total: String(row.qty),
          })}
        </span>
      </div>
      <p className="mb-3 font-ui text-t-caption text-fg-tertiary">{t("holdingRuleReservedHint")}</p>

      {/* Target price. */}
      <label className="mb-1 block font-ui text-t-caption text-fg-secondary">
        {t("holdingRuleTarget")}
      </label>
      <div className="mb-1 flex flex-wrap items-center gap-2">
        <Input
          type="number"
          min={0}
          step="any"
          value={target}
          onChange={(e) => {
            setTarget(e.target.value);
            // Typing overrides a suggestion, so stop claiming the price came
            // from a percentile it no longer matches.
            setPercentile(0);
          }}
          className="w-36"
        />
        {!choices && (
          <Button size="sm" variant="ghost" onClick={() => void loadChoices()} disabled={loadingChoices}>
            {loadingChoices && <Loader2 className="h-3 w-3 animate-spin" aria-hidden="true" />}
            {loadingChoices ? t("holdingRuleSuggestLoading") : t("holdingRuleSuggest")}
          </Button>
        )}
      </div>
      <p className="mb-2 font-ui text-t-caption text-fg-tertiary">{t("holdingRuleTargetHint")}</p>

      {choices?.length === 0 && (
        <p className="mb-2 font-ui text-t-caption text-warn-dim">{t("holdingRuleNoHistory")}</p>
      )}

      {choices && choices.length > 0 && (
        <>
          <div className="mb-1 flex flex-wrap gap-1">
            {choices.map((c) => (
              <button
                key={c.percentile}
                type="button"
                onClick={() => {
                  setTarget(String(Math.round(c.price)));
                  setPercentile(c.percentile);
                }}
                className={cn(
                  "rounded-sm border px-1.5 py-1 text-left font-ui text-t-caption",
                  percentile === c.percentile
                    ? "border-eve-accent bg-eve-accent/15 text-fg"
                    : "border-eve-border bg-surface-3 text-fg-secondary hover:bg-surface-1",
                )}
              >
                <span className="block font-num tnum text-fg">{unit(c.price)}</span>
                <span className="block text-fg-tertiary">
                  p{c.percentile} · {t(CHOICE_LABEL[c.label] ?? "holdingRuleChoiceTypical")}
                </span>
              </button>
            ))}
          </div>
          {stats && stats.basis === "history" && (
            <p className="mb-2 font-ui text-t-caption text-fg-tertiary">
              {t("holdingRuleCurrentPct", { pct: String(Math.round(stats.current_percentile)) })} ·{" "}
              {t("holdingRuleYearRange", { min: formatISK(stats.min), max: formatISK(stats.max) })}
            </p>
          )}
        </>
      )}

      <div className="flex gap-2">
        <Button size="sm" variant="primary" onClick={() => void save(false)} disabled={saving}>
          {t("holdingRuleSave")}
        </Button>
        {hasRule && (
          <Button size="sm" variant="ghost" onClick={() => void save(true)} disabled={saving}>
            {t("holdingRuleClear")}
          </Button>
        )}
      </div>
    </section>
  );
}
