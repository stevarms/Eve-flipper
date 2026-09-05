/* Compact character-scope control for the workspace tab strip.
 *
 * The scope selector used to be a wall of portrait pills in the character
 * modal header. On the tab strip it has to fit in one line next to the Scan
 * button, so it collapses to a single pill that opens the same list. */
import { useEffect, useRef, useState } from "react";
import { useI18n } from "../../lib/i18n";
import { useCharacterScope } from "./CharacterScopeProvider";

export function CharacterScopePicker() {
  const { t } = useI18n();
  const { scope, selectScope, scopeBusy, characters, isLoggedIn } = useCharacterScope();
  const [open, setOpen] = useState(false);
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

  // Nothing to scope when there is no auth, or only one character and it is
  // already selected — the pill would just be noise.
  if (!isLoggedIn || characters.length === 0) return null;

  const selected = scope === "all" ? null : characters.find((c) => c.character_id === scope);
  const label = selected?.character_name ?? t("charAllCharacters");

  return (
    <div ref={wrapRef} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        disabled={scopeBusy}
        aria-haspopup="listbox"
        aria-expanded={open}
        title={t("charSelectCharacter")}
        className="inline-flex h-7 max-w-[200px] items-center gap-1.5 rounded-sm border border-eve-border bg-eve-dark/70 px-2 font-ui text-t-caption text-fg-secondary transition-colors hover:border-eve-accent/50 hover:text-fg disabled:opacity-50"
      >
        {selected ? (
          <img
            src={`https://images.evetech.net/characters/${selected.character_id}/portrait?size=32`}
            alt=""
            className="h-4 w-4 rounded-sm border border-eve-border/50"
          />
        ) : (
          <span className="text-[10px] opacity-80">◉</span>
        )}
        <span className="truncate">{label}</span>
        <span className="text-[9px] opacity-70">▾</span>
      </button>

      {open && (
        <div
          role="listbox"
          className="absolute right-0 top-[calc(100%+4px)] z-50 max-h-72 w-60 overflow-auto rounded-sm border border-eve-border bg-eve-panel p-1 shadow-lg"
        >
          <ScopeOption
            active={scope === "all"}
            label={t("charAllCharacters")}
            onClick={() => {
              void selectScope("all");
              setOpen(false);
            }}
          />
          {characters.map((character) => (
            <ScopeOption
              key={character.character_id}
              active={scope === character.character_id}
              label={character.character_name}
              portraitId={character.character_id}
              onClick={() => {
                void selectScope(character.character_id);
                setOpen(false);
              }}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function ScopeOption({
  active,
  label,
  portraitId,
  onClick,
}: {
  active: boolean;
  label: string;
  portraitId?: number;
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
      ) : (
        <span className="flex h-5 w-5 items-center justify-center text-[10px] opacity-80">◉</span>
      )}
      <span className="truncate">{label}</span>
    </button>
  );
}
