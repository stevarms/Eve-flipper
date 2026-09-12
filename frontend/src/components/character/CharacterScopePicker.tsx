/* Compact character-scope control for the workspace tab strip.
 *
 * The scope selector used to be a wall of portrait pills in the character
 * modal header. On the tab strip it has to fit in one line next to the Scan
 * button, so it collapses to a single pill that opens the same list.
 *
 * On tabs that can express corporation ownership (CORP_SCOPED_TABS) the list
 * also offers the corporations whose wallets have actually been archived, plus
 * the two combining entries. On the others a stored corp selection is ignored
 * by the provider, and the pill says so rather than mislabelling what is on
 * screen. */
import { useEffect, useMemo, useRef, useState } from "react";
import { useI18n } from "../../lib/i18n";
import { getAuthOwners, ownerScopeKey, type OwnerCorporation, type OwnerScope } from "../../lib/api";
import { useCharacterScope } from "./CharacterScopeProvider";

export function CharacterScopePicker() {
  const { t } = useI18n();
  const {
    ownerScope,
    selectOwnerScope,
    scopeBusy,
    characters,
    isLoggedIn,
    corpCapable,
    ownerScopeIgnored,
  } = useCharacterScope();
  const [open, setOpen] = useState(false);
  const [corporations, setCorporations] = useState<OwnerCorporation[]>([]);
  const wrapRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!wrapRef.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  // Only fetched for tabs that can use it, and only once the list is opened —
  // most sessions never touch the corp entries at all.
  useEffect(() => {
    if (!open || !corpCapable || !isLoggedIn) return;
    let cancelled = false;
    getAuthOwners()
      .then((res) => {
        if (!cancelled) setCorporations(res.corporations ?? []);
      })
      .catch(() => {
        // A missing owners list just means no corp entries; the character
        // entries below are unaffected and still worth showing.
      });
    return () => {
      cancelled = true;
    };
  }, [open, corpCapable, isLoggedIn]);

  const activeKey = ownerScopeKey(ownerScope);
  const label = useMemo(() => {
    switch (ownerScope.kind) {
      case "character": {
        const found = characters.find((c) => c.character_id === ownerScope.characterId);
        return found?.character_name ?? t("charAllCharacters");
      }
      case "corporation": {
        const found = corporations.find((c) => c.corporation_id === ownerScope.corporationId);
        return found?.corporation_name ?? t("charOwnerCorporation");
      }
      case "all-corporations":
        return t("charAllCorporations");
      case "everything":
        return t("charAllOwners");
      default:
        return t("charAllCharacters");
    }
  }, [ownerScope, characters, corporations, t]);

  // Nothing to scope when there is no auth, or only one character and it is
  // already selected — the pill would just be noise.
  if (!isLoggedIn || characters.length === 0) return null;

  const selectedCharacter =
    ownerScope.kind === "character" ? characters.find((c) => c.character_id === ownerScope.characterId) : undefined;

  const choose = (owner: OwnerScope) => {
    void selectOwnerScope(owner);
    setOpen(false);
  };

  return (
    <div ref={wrapRef} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        disabled={scopeBusy}
        aria-haspopup="listbox"
        aria-expanded={open}
        title={ownerScopeIgnored ? t("charOwnerScopeIgnoredHint") : t("charSelectCharacter")}
        className="inline-flex h-7 max-w-[200px] items-center gap-1.5 rounded-sm border border-eve-border bg-eve-dark/70 px-2 font-ui text-t-caption text-fg-secondary transition-colors hover:border-eve-accent/50 hover:text-fg disabled:opacity-50"
      >
        {selectedCharacter ? (
          <img
            src={`https://images.evetech.net/characters/${selectedCharacter.character_id}/portrait?size=32`}
            alt=""
            className="h-4 w-4 rounded-sm border border-eve-border/50"
          />
        ) : (
          <span className="text-[10px] opacity-80">◉</span>
        )}
        <span className="truncate">{label}</span>
        {ownerScopeIgnored && <span className="text-[9px] text-amber-400/90">!</span>}
        <span className="text-[9px] opacity-70">▾</span>
      </button>

      {open && (
        <div
          role="listbox"
          className="absolute right-0 top-[calc(100%+4px)] z-50 max-h-72 w-60 overflow-auto rounded-sm border border-eve-border bg-eve-panel p-1 shadow-lg"
        >
          {ownerScopeIgnored && (
            <div className="mb-1 rounded-sm bg-amber-500/10 px-2 py-1 text-[10px] leading-snug text-amber-300">
              {t("charOwnerScopeIgnoredHint")}
            </div>
          )}
          <ScopeOption
            active={activeKey === "characters"}
            label={t("charAllCharacters")}
            onClick={() => choose({ kind: "all-characters" })}
          />
          {characters.map((character) => (
            <ScopeOption
              key={character.character_id}
              active={activeKey === `char:${character.character_id}`}
              label={character.character_name}
              portraitId={character.character_id}
              onClick={() => choose({ kind: "character", characterId: character.character_id })}
            />
          ))}
          {corpCapable && corporations.length > 0 && (
            <>
              <div className="mt-1 border-t border-eve-border/60 px-2 pb-1 pt-1.5 font-ui text-[10px] uppercase tracking-wide text-fg-muted">
                {t("charCorporationsHeading")}
              </div>
              <ScopeOption
                active={activeKey === "corps"}
                label={t("charAllCorporations")}
                onClick={() => choose({ kind: "all-corporations" })}
              />
              {corporations.map((corporation) => (
                <ScopeOption
                  key={corporation.corporation_id}
                  active={activeKey === `corp:${corporation.corporation_id}`}
                  label={corporation.corporation_name}
                  corporationId={corporation.corporation_id}
                  onClick={() => choose({ kind: "corporation", corporationId: corporation.corporation_id })}
                />
              ))}
              <div className="mt-1 border-t border-eve-border/60 pt-1">
                <ScopeOption
                  active={activeKey === "everything"}
                  label={t("charAllOwners")}
                  onClick={() => choose({ kind: "everything" })}
                />
              </div>
            </>
          )}
        </div>
      )}
    </div>
  );
}

function ScopeOption({
  active,
  label,
  portraitId,
  corporationId,
  onClick,
}: {
  active: boolean;
  label: string;
  portraitId?: number;
  corporationId?: number;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      role="option"
      aria-selected={active}
      onClick={onClick}
      className={`flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left font-ui text-t-body transition-colors ${
        active ? "bg-eve-accent/12 text-eve-accent" : "text-fg-secondary hover:bg-surface-2 hover:text-fg"
      }`}
    >
      {portraitId ? (
        <img
          src={`https://images.evetech.net/characters/${portraitId}/portrait?size=32`}
          alt=""
          className="h-5 w-5 rounded-sm border border-eve-border/50"
        />
      ) : corporationId ? (
        <img
          src={`https://images.evetech.net/corporations/${corporationId}/logo?size=32`}
          alt=""
          className="h-5 w-5 rounded-sm border border-eve-border/50"
        />
      ) : (
        <span className="flex h-5 w-5 items-center justify-center text-[10px] opacity-80">◉</span>
      )}
      <span className="truncate">{label}</span>
    </button>
  );
}
