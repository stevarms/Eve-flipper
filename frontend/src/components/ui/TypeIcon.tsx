import { useEffect, useState } from "react";
import { artForCategory, typeIconUrl, type TypeArt } from "@/lib/eveImages";
import { cn } from "@/lib/utils";

/**
 * An EVE inventory type's icon, with the blueprint case handled.
 *
 * Blueprints do not serve `/icon` — they 400 — so a plain <img> renders them
 * as a broken/blank cell. `CategoryID` would tell us, but it is optional on
 * the wire and is NOT persisted in scan history, so rows read back from a
 * saved scan have no category at all. Guessing from the name is unreliable
 * ("Blueprint" appears in plenty of non-blueprint names).
 *
 * So: use the category as a fast path when it is present, and otherwise fall
 * back to trying `/bp` once when `/icon` fails. Costs one wasted request per
 * distinct blueprint, cached by the browser thereafter, and needs no schema
 * change. If both fail the icon hides rather than showing a broken image.
 */
export interface TypeIconProps {
  typeId: number;
  /** SDE category when known — category 9 goes straight to the bp variant. */
  categoryId?: number;
  size?: number;
  className?: string;
}

export function TypeIcon({ typeId, categoryId, size = 18, className }: TypeIconProps) {
  const initial = artForCategory(categoryId);
  const [art, setArt] = useState<TypeArt>(initial);
  const [failed, setFailed] = useState(false);

  // Reset when the row changes — these are recycled across virtualised /
  // re-sorted lists, and a stale "failed" would blank the wrong item.
  useEffect(() => {
    setArt(artForCategory(categoryId));
    setFailed(false);
  }, [typeId, categoryId]);

  if (!typeId || typeId <= 0 || failed) {
    return <span aria-hidden="true" className={cn("shrink-0", className)} style={{ width: size, height: size }} />;
  }

  return (
    <img
      src={typeIconUrl(typeId, art, 32)}
      alt=""
      aria-hidden="true"
      loading="lazy"
      width={size}
      height={size}
      className={cn("shrink-0 rounded-[2px]", className)}
      style={{ width: size, height: size }}
      onError={() => {
        // One retry with the other variant, then give up.
        if (art === "icon") setArt("bp");
        else setFailed(true);
      }}
    />
  );
}
