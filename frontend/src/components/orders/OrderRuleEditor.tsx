import { useCallback, useState } from "react";
import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useOptionalToast } from "@/components/Toast";
import { getHoldingRulePercentiles, setHoldingRule } from "@/lib/api";
import { formatISK, formatIsk } from "@/lib/format";
import { useI18n, type TranslationKey } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import type { HoldingRuleChoice, OrderDeskOrder, PricePercentiles } from "@/lib/types";

/**
 * The prices you have decided by hand, edited from the order that provoked the
 * decision.
 *
 * This is the same per-type rule Assets -> Positions edits and Today reads, not
 * a second copy: a price pinned to an order id would evaporate the moment you
 * relist, since relisting mints a new one. Positions asks "should I sell this
 * holding today?"; the desk asks "should I move this order?" — the same rule
 * answers both, and setting it in either place sets it everywhere.
 *
 * Only the fields that change *this* order's advice are shown, because the
 * order has a side: a bid ceiling on a sell row is noise, and a target on a bid
 * cannot stop the desk from following the book up. The PUT merges rather than
 * replaces, so the reserve and the note this form does not show survive it.
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

export function OrderRuleEditor({
  row,
  onSaved,
}: {
  row: OrderDeskOrder;
  /** Called once the rule is stored or cleared. The desk's recommendation is
   *  computed server-side, so the tab has to refetch — passing the rule back
   *  would only describe what was sent, not what the server kept. */
  onSaved: () => void;
}) {
  const { t } = useI18n();
  const { addToast } = useOptionalToast();

  const [target, setTarget] = useState(row.target_price ? String(row.target_price) : "");
  const [percentile, setPercentile] = useState(0);
  const [ceiling, setCeiling] = useState(row.max_bid_price ? String(row.max_bid_price) : "");
  const [patient, setPatient] = useState(!!row.patient_bid);
  const [choices, setChoices] = useState<HoldingRuleChoice[] | null>(null);
  const [stats, setStats] = useState<PricePercentiles | null>(null);
  const [loadingChoices, setLoadingChoices] = useState(false);
  const [saving, setSaving] = useState(false);

  const buy = row.is_buy_order;

  const loadChoices = useCallback(async () => {
    if (choices || loadingChoices) return;
    setLoadingChoices(true);
    try {
      // Quoted against the order's own region rather than the default hub: a
      // target you can hit in Jita is not one you can hit where the order is.
      const res = await getHoldingRulePercentiles(row.type_id, row.region_id);
      setStats(res.percentiles);
      setChoices(res.percentiles.basis === "history" ? res.choices : []);
    } catch {
      setChoices([]);
    } finally {
      setLoadingChoices(false);
    }
  }, [choices, loadingChoices, row.region_id, row.type_id]);

  const save = useCallback(
    async (clear = false) => {
      setSaving(true);
      try {
        // Only the fields this form owns are sent. An absent field is left
        // alone by the API and an explicit zero clears it, so clearing here
        // does not take the reserve or the note with it.
        await setHoldingRule(
          row.type_id,
          buy
            ? {
                max_bid_price: clear ? 0 : Number(ceiling) || 0,
                patient_bid: clear ? false : patient,
              }
            : {
                target_price: clear ? 0 : Number(target) || 0,
                target_percentile: clear ? 0 : percentile,
                target_basis: clear ? "" : percentile > 0 ? "percentile" : "manual",
              },
        );
        addToast(t(clear ? "holdingRuleCleared" : "holdingRuleSaved"), "success", 2000);
        if (clear) {
          setTarget("");
          setPercentile(0);
          setCeiling("");
          setPatient(false);
        }
        onSaved();
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        addToast(t("holdingRuleSaveFailed", { error: message }), "error", 4000);
      } finally {
        setSaving(false);
      }
    },
    [addToast, buy, ceiling, onSaved, patient, percentile, row.type_id, t, target],
  );

  // Whether there is anything to clear, from what the form can see. A rule set
  // from Positions on the other side of this type is not shown here and is not
  // this button's business — clearing sends zeros only for the two or three
  // fields above.
  const hasRule = buy
    ? (row.max_bid_price ?? 0) > 0 || !!row.patient_bid
    : (row.target_price ?? 0) > 0;

  return (
    <section className="mt-3 rounded-sm border border-eve-border bg-surface-2 p-2.5">
      <h3 className="font-ui text-t-body font-medium text-fg">{t("ordersRuleTitle")}</h3>
      <p className="mb-2 font-ui text-t-caption text-fg-tertiary">{t("ordersRuleHint")}</p>

      {buy ? (
        <>
          {/* Bid ceiling. The reprice advice reads the book and only the book,
              so this is the only thing that can stop it following a runaway
              market up past what you decided the item was worth. */}
          <label className="mb-1 block font-ui text-t-caption text-fg-secondary">
            {t("ordersRuleCeiling")}
          </label>
          <Input
            type="number"
            min={0}
            step="any"
            value={ceiling}
            onChange={(e) => setCeiling(e.target.value)}
            className="mb-1 w-36"
          />
          <p className="mb-3 font-ui text-t-caption text-fg-tertiary">
            {t("ordersRuleCeilingHint")}
          </p>

          <label className="mb-1 flex items-center gap-2 font-ui text-t-caption text-fg-secondary">
            <input
              type="checkbox"
              checked={patient}
              onChange={(e) => setPatient(e.target.checked)}
              className="accent-eve-accent"
            />
            {t("ordersRulePatient")}
          </label>
          <p className="mb-3 font-ui text-t-caption text-fg-tertiary">
            {t("ordersRulePatientHint")}
          </p>
        </>
      ) : (
        <>
          {/* Target price, with the same five named percentiles Positions
              offers — read from the item's own trailing year, because a price
              it has genuinely traded at is one it can plausibly reach again. */}
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
                // Typing overrides a suggestion, so stop claiming the price
                // came from a percentile it no longer matches.
                setPercentile(0);
              }}
              className="w-36"
            />
            {!choices && (
              <Button
                size="sm"
                variant="ghost"
                onClick={() => void loadChoices()}
                disabled={loadingChoices}
              >
                {loadingChoices && <Loader2 className="h-3 w-3 animate-spin" aria-hidden="true" />}
                {loadingChoices ? t("holdingRuleSuggestLoading") : t("holdingRuleSuggest")}
              </Button>
            )}
          </div>
          <p className="mb-2 font-ui text-t-caption text-fg-tertiary">
            {t("ordersRuleTargetHint")}
          </p>

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
                  {t("holdingRuleCurrentPct", {
                    pct: String(Math.round(stats.current_percentile)),
                  })}{" "}
                  ·{" "}
                  {t("holdingRuleYearRange", {
                    min: formatISK(stats.min),
                    max: formatISK(stats.max),
                  })}
                </p>
              )}
            </>
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
