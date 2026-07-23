import { useEffect, useMemo, useState } from "react";
import { useI18n } from "@/lib/i18n";
import { getStructureRigs } from "@/lib/api";
import type { StructureRig } from "@/lib/types";

// Structure hull typeIDs the picker supports. When a station outside this
// set is selected the picker renders a "no player structure" hint. The
// values map to SDE `groupID` so we can filter the rig catalog to entries
// whose `fits_structure_groups` includes the hull's group.
const STRUCTURE_TYPE_TO_GROUP: Record<number, number> = {
  35825: 1657, // Raitaru      → Engineering Complex (group 1657)
  35826: 1657, // Azbel
  35827: 1657, // Sotiyo
  35835: 1406, // Athanor      → Refinery (group 1406)
  35836: 1406, // Tatara
};

// Max rig size each hull accepts. Rigs whose `rig_size` exceeds this are
// filtered out of the picker. 2 = M, 3 = L, 4 = XL. Sotiyo takes all
// sizes so we cap at XL (4); Raitaru only takes M rigs. Athanor is M-only,
// Tatara up to L.
const STRUCTURE_TYPE_TO_MAX_RIG_SIZE: Record<number, number> = {
  35825: 2, // Raitaru M
  35826: 3, // Azbel L
  35827: 4, // Sotiyo XL
  35835: 2, // Athanor M
  35836: 3, // Tatara L
};

// Sec-status labels for the multiplier caption. Same thresholds the
// engine uses (hisec ≥ 0.45, lowsec > 0, else null/wh).
function secLabel(sec: number): "hisec" | "lowsec" | "nullsec" {
  if (sec >= 0.45) return "hisec";
  if (sec > 0) return "lowsec";
  return "nullsec";
}

interface Props {
  structureTypeID: number;
  selectedRigTypeIDs: number[];
  onChange: (rigTypeIDs: number[]) => void;
  /** Current system security (0.0–1.0). Drives which sec multiplier the
   *  caption previews and greys out rigs that can't operate in this sec
   *  (e.g. advanced rigs at hisec ≥ 0.45). */
  systemSecurity: number;
}

// A picker for up to 3 Standup rigs fitted to the currently-selected
// Upwell structure. Renders nothing when the hull isn't an industry
// structure the picker recognises. Compact per-slot dropdowns; each
// option shows the rig name plus a summary of its effect (ME/TE/Cost %
// scaled by the current sec multiplier).
export function StructureRigPicker({
  structureTypeID,
  selectedRigTypeIDs,
  onChange,
  systemSecurity,
}: Props) {
  const { t } = useI18n();
  const hullGroup = STRUCTURE_TYPE_TO_GROUP[structureTypeID] ?? 0;

  const [catalog, setCatalog] = useState<StructureRig[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (structureTypeID === 0) return;
    let cancelled = false;
    getStructureRigs()
      .then((resp) => { if (!cancelled) { setCatalog(resp.rigs); setError(null); } })
      .catch((e: unknown) => {
        if (cancelled) return;
        setError(e instanceof Error ? e.message : "catalog error");
        setCatalog([]);
      });
    return () => { cancelled = true; };
  }, [structureTypeID]);

  // Rigs fittable to this hull. The catalog contains rigs for every hull
  // family (engineering + refinery); filter to the ones whose
  // fits_structure_groups includes our hull's group.
  const maxRigSize = STRUCTURE_TYPE_TO_MAX_RIG_SIZE[structureTypeID] ?? 0;
  const fittable = useMemo(() => {
    if (!catalog || hullGroup === 0) return [];
    return catalog.filter((r) => {
      if (!r.fits_structure_groups.includes(hullGroup)) return false;
      // Rig size guard: Raitaru M-only, Azbel M/L, Sotiyo any. r.rig_size
      // is 2/3/4 for M/L/XL respectively. Zero-size (attribute missing)
      // is treated as "fits any" for defensiveness.
      if (r.rig_size > 0 && maxRigSize > 0 && r.rig_size > maxRigSize) return false;
      return true;
    });
  }, [catalog, hullGroup, maxRigSize]);

  const secKind = secLabel(systemSecurity);
  const secMultFor = (rig: StructureRig): number => {
    switch (secKind) {
      case "hisec": return rig.hi_sec_mult;
      case "lowsec": return rig.low_sec_mult;
      case "nullsec": return rig.null_sec_mult;
    }
  };

  const setSlot = (slotIdx: number, rigID: number) => {
    const next = [...selectedRigTypeIDs];
    // Pad with zeros so slot writes are stable regardless of prior length.
    while (next.length <= slotIdx) next.push(0);
    next[slotIdx] = rigID;
    // Drop trailing zeros so the array stays compact.
    while (next.length > 0 && next[next.length - 1] === 0) next.pop();
    onChange(next);
  };

  const clearAll = () => onChange([]);

  if (structureTypeID === 0) {
    return null;
  }
  if (hullGroup === 0) {
    // A player structure is selected but not one this picker knows about
    // (e.g. Ansiblex / Pharolux / SKINRs). No rigs to show.
    return (
      <div className="text-[11px] text-eve-dim italic">
        {t("structureRigNoStructureSelected")}
      </div>
    );
  }

  return (
    <div className="border border-eve-border/40 rounded-sm p-3 bg-eve-panel/30 space-y-2">
      <div className="flex items-center justify-between gap-3">
        <div>
          <div className="text-[11px] uppercase tracking-wider text-eve-dim font-medium">
            {t("structureRigsTitle")}
          </div>
          <div className="text-[10px] text-eve-dim">
            {t("structureRigsIntro")} · {t("structureRigActiveMultiplierLabel").replace(
              "{sec}",
              t(`structureRigSec_${secKind}`),
            )}
          </div>
        </div>
        {selectedRigTypeIDs.length > 0 && (
          <button
            type="button"
            onClick={clearAll}
            className="px-2 py-0.5 text-[10px] rounded-sm border border-eve-border/60 text-eve-dim hover:text-eve-text hover:border-eve-border transition-colors"
          >
            {t("structureRigClearAll")}
          </button>
        )}
      </div>
      {error && (
        <div className="text-[10px] text-red-300">{t("structureRigCatalogError")}: {error}</div>
      )}
      <div className="flex flex-wrap gap-x-3 gap-y-2">
        {[0, 1, 2].map((slotIdx) => {
          const currentID = selectedRigTypeIDs[slotIdx] ?? 0;
          // Options for this slot: fittable rigs minus rigs already chosen
          // in other slots (avoid duplicate selection).
          const takenElsewhere = new Set(
            selectedRigTypeIDs.filter((_, i) => i !== slotIdx && selectedRigTypeIDs[i] > 0),
          );
          const options = fittable.filter((r) => !takenElsewhere.has(r.type_id));
          return (
            <div key={slotIdx} className="flex flex-col gap-1 w-72">
              <label className="text-[10px] uppercase tracking-wider text-eve-dim">
                {t("structureRigSlotLabel").replace("{n}", String(slotIdx + 1))}
              </label>
              <select
                value={currentID}
                onChange={(e) => setSlot(slotIdx, Number(e.target.value))}
                className="w-full px-2 py-1 bg-eve-input border border-eve-border rounded-sm text-eve-text text-xs font-mono
                           focus:outline-none focus:border-eve-accent focus:ring-1 focus:ring-eve-accent/30 transition-colors"
              >
                <option value={0}>{t("structureRigNoneOption")}</option>
                {options.map((rig) => {
                  const mult = secMultFor(rig);
                  const disabled = mult === 0;
                  const label = disabled
                    ? `${rig.name}   [${t("structureRigCantRunHere")}]`
                    : rig.name;
                  return (
                    <option key={rig.type_id} value={rig.type_id} disabled={disabled}>
                      {label}
                    </option>
                  );
                })}
              </select>
              {currentID > 0 && (() => {
                const rig = fittable.find((r) => r.type_id === currentID);
                if (!rig) return null;
                const mult = secMultFor(rig);
                const parts: string[] = [];
                if (rig.me_bonus !== 0) parts.push(`${(rig.me_bonus * mult).toFixed(1)}% ME`);
                if (rig.te_bonus !== 0) parts.push(`${(rig.te_bonus * mult).toFixed(1)}% TE`);
                if (rig.cost_bonus !== 0) parts.push(`${(rig.cost_bonus * mult).toFixed(1)}% Cost`);
                return (
                  <div className="text-[10px] text-eve-dim leading-tight">
                    {rig.affinity_description}: {parts.join(", ") || t("structureRigNoEffectHere")}
                  </div>
                );
              })()}
            </div>
          );
        })}
      </div>
    </div>
  );
}

// Helper for consumers: compute the aggregated rig-derived ME/TE/Cost
// reductions from a fitted loadout for display purposes. Mirrors the
// engine's rigContribution math (positive percentages) — with one caveat:
// this doesn't know the product category, so it returns the BEST-CASE
// total assuming every rig's product filter matches. The actual engine
// bonus per item is ≤ these numbers. Display should note this.
export function computeRigTotals(
  rigs: StructureRig[],
  selectedIDs: number[],
  systemSecurity: number,
  activity: "manufacturing" | "reaction" | "invention",
): { me: number; te: number; cost: number } {
  let me = 0, te = 0, cost = 0;
  const secKind = secLabel(systemSecurity);
  for (const id of selectedIDs) {
    const rig = rigs.find((r) => r.type_id === id);
    if (!rig) continue;
    if (rig.activity !== "" && rig.activity !== activity) continue;
    const mult = secKind === "hisec" ? rig.hi_sec_mult : secKind === "lowsec" ? rig.low_sec_mult : rig.null_sec_mult;
    if (mult === 0) continue;
    me += -rig.me_bonus * mult;
    te += -rig.te_bonus * mult;
    cost += -rig.cost_bonus * mult;
  }
  return { me, te, cost };
}
