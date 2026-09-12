import { useState } from "react";
import { CopyButton } from "@/components/ui/CopyButton";
import { OpenMarketButton } from "@/components/ui/OpenMarketButton";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent } from "@/components/ui/sheet";
import { TypeIcon } from "@/components/ui/TypeIcon";
import { saveManualPosition, searchItems } from "@/lib/api";
import type { TranslationKey } from "@/lib/i18n";
import type { ItemSearchResult } from "@/lib/types";

/**
 * Hand-entry for stock the FIFO engine cannot see: loot, contract buys, corp
 * hangar transfers, anything acquired before the wallet history window.
 *
 * The item name is resolved through `GET /api/items/search`, which is pure SDE
 * name lookup with no ESI call and no auth — so this flow works before SSO,
 * which is the point of hand-entry. The obvious alternative, the stockpile
 * resolver, sits behind `requireIndustryAuthUser` and would have walled off
 * the one part of this tab a logged-out user can actually use. It also only
 * matches an exact name; search returns candidates, so a half-remembered item
 * name still gets you there.
 */

export function AddHoldingSheet({
  open,
  onClose,
  onSaved,
  t,
}: {
  open: boolean;
  onClose: () => void;
  onSaved: () => void;
  t: (key: TranslationKey, params?: Record<string, string | number>) => string;
}) {
  const [name, setName] = useState("");
  const [typeId, setTypeId] = useState<number | null>(null);
  const [resolvedName, setResolvedName] = useState("");
  const [matches, setMatches] = useState<ItemSearchResult[]>([]);
  const [qty, setQty] = useState("1");
  const [unitCost, setUnitCost] = useState("");
  const [target, setTarget] = useState("");
  const [acquired, setAcquired] = useState("");
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const reset = () => {
    setName("");
    setTypeId(null);
    setResolvedName("");
    setMatches([]);
    setQty("1");
    setUnitCost("");
    setTarget("");
    setAcquired("");
    setNote("");
    setError(null);
  };

  const resolve = async () => {
    const trimmed = name.trim();
    if (!trimmed) return;
    setBusy(true);
    setError(null);
    try {
      const hits = await searchItems(trimmed, 8);
      if (hits.length === 0) {
        setTypeId(null);
        setResolvedName("");
        setMatches([]);
        setError(t("positionsAddResolveFailed"));
        return;
      }
      // An exact name match is the answer; otherwise offer the candidates and
      // let the user say which one they meant.
      const exact = hits.find((h) => h.type_name.toLowerCase() === trimmed.toLowerCase());
      const pick = exact ?? (hits.length === 1 ? hits[0] : null);
      setMatches(pick ? [] : hits);
      setTypeId(pick?.type_id ?? null);
      setResolvedName(pick?.type_name ?? "");
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const save = async () => {
    if (!typeId) {
      setError(t("positionsAddResolveFailed"));
      return;
    }
    const quantity = Number(qty);
    const cost = Number(unitCost);
    if (!Number.isFinite(quantity) || quantity <= 0 || !Number.isFinite(cost) || cost < 0) {
      setError(t("positionsAddInvalid"));
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await saveManualPosition({
        type_id: typeId,
        type_name: resolvedName,
        quantity,
        unit_cost: cost,
        target_price: target ? Number(target) || 0 : 0,
        acquired_at: acquired || undefined,
        note: note.trim() || undefined,
      });
      reset();
      onSaved();
      onClose();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Sheet open={open} onOpenChange={(o) => !o && onClose()}>
      <SheetContent
        width="w-[440px]"
        title={t("positionsAddTitle")}
        description={t("positionsAddDesc")}
        footer={
          <>
            <Button variant="ghost" size="sm" onClick={onClose} disabled={busy}>
              {t("positionsAddCancel")}
            </Button>
            <Button variant="primary" size="sm" onClick={save} disabled={busy || !typeId}>
              {t("positionsAddSave")}
            </Button>
          </>
        }
      >
        <div className="space-y-3">
          <Field label={t("positionsAddItem")}>
            <div className="flex items-center gap-2">
              <Input
                value={name}
                onChange={(e) => setName(e.target.value)}
                onBlur={resolve}
                onKeyDown={(e) => {
                  if (e.key === "Enter") void resolve();
                }}
                placeholder="Tritanium"
              />
              <Button variant="secondary" size="sm" onClick={resolve} disabled={busy || !name.trim()}>
                {t("positionsAddResolve")}
              </Button>
            </div>
            {typeId ? (
              <div className="mt-1 flex items-center gap-1.5 font-ui text-t-caption text-fg-secondary">
                <TypeIcon typeId={typeId} size={16} />
                <span>{resolvedName}</span>
                <span className="text-fg-tertiary">#{typeId}</span>
                <OpenMarketButton typeId={typeId} label={t("openMarketHint")} />
                <CopyButton text={resolvedName} label={t("copyItem")} />
              </div>
            ) : null}
            {matches.length > 0 ? (
              <div className="mt-1 space-y-0.5">
                <div className="font-ui text-t-caption text-fg-tertiary">
                  {t("positionsAddPickMatch")}
                </div>
                {matches.map((m) => (
                  <button
                    key={m.type_id}
                    type="button"
                    onClick={() => {
                      setTypeId(m.type_id);
                      setResolvedName(m.type_name);
                      setName(m.type_name);
                      setMatches([]);
                    }}
                    className="flex w-full items-center gap-1.5 rounded-sm px-1 py-0.5 text-left font-ui text-t-caption text-fg-secondary hover:bg-surface-2"
                  >
                    <TypeIcon typeId={m.type_id} categoryId={m.category_id} size={16} />
                    <span className="truncate">{m.type_name}</span>
                    <span className="ml-auto shrink-0 text-fg-tertiary">{m.group_name}</span>
                  </button>
                ))}
              </div>
            ) : null}
          </Field>

          <div className="grid grid-cols-2 gap-3">
            <Field label={t("positionsAddQty")}>
              <Input value={qty} onChange={(e) => setQty(e.target.value)} inputMode="numeric" />
            </Field>
            <Field label={t("positionsAddUnitCost")}>
              <Input value={unitCost} onChange={(e) => setUnitCost(e.target.value)} inputMode="decimal" />
            </Field>
            <Field label={t("positionsAddTarget")}>
              <Input value={target} onChange={(e) => setTarget(e.target.value)} inputMode="decimal" />
            </Field>
            <Field label={t("positionsAddAcquired")}>
              <Input type="date" value={acquired} onChange={(e) => setAcquired(e.target.value)} />
            </Field>
          </div>

          <Field label={t("positionsAddNote")}>
            <Input value={note} onChange={(e) => setNote(e.target.value)} />
          </Field>

          {error && (
            <div className="rounded-sm border border-loss/50 bg-loss/10 px-3 py-2 font-ui text-t-cell text-loss">
              {error}
            </div>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1 block font-ui text-t-caption uppercase tracking-wide text-fg-tertiary">
        {label}
      </span>
      {children}
    </label>
  );
}
