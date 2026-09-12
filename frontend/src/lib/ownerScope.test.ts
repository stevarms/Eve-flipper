import { describe, expect, it } from "vitest";
import { characterScopeFromOwner, ownerScopeKey, ownerScopeParam, type OwnerScope } from "./api";
import { readStoredScope } from "../components/character/CharacterScopeProvider";

describe("characterScopeFromOwner", () => {
  it("keeps a single character", () => {
    expect(characterScopeFromOwner({ kind: "character", characterId: 42 })).toBe(42);
  });

  // Every corporation form has to widen to "all" rather than narrow to one
  // character: the ten tools that only speak CharacterScope would otherwise
  // silently show one character's data while the pill named a corporation.
  it("collapses every corporation form to all", () => {
    const corpForms: OwnerScope[] = [
      { kind: "all-characters" },
      { kind: "corporation", corporationId: 98000001 },
      { kind: "all-corporations" },
      { kind: "everything" },
    ];
    for (const owner of corpForms) {
      expect(characterScopeFromOwner(owner)).toBe("all");
    }
  });
});

describe("ownerScopeParam", () => {
  it("matches the wire form the server parses", () => {
    expect(ownerScopeParam({ kind: "character", characterId: 7 })).toBe("char:7");
    expect(ownerScopeParam({ kind: "all-characters" })).toBe("characters");
    expect(ownerScopeParam({ kind: "corporation", corporationId: 98000001 })).toBe("corp:98000001");
    expect(ownerScopeParam({ kind: "all-corporations" })).toBe("corps");
    expect(ownerScopeParam({ kind: "everything" })).toBe("all");
  });
});

describe("readStoredScope", () => {
  // Upgrading must not change which owner is selected. Earlier builds stored
  // a bare "all" or a bare character id under the same key.
  it("migrates legacy values", () => {
    expect(readStoredScope("all")).toEqual({ kind: "all-characters" });
    expect(readStoredScope("12345")).toEqual({ kind: "character", characterId: 12345 });
  });

  // Storage uses ownerScopeKey, not the wire form: "all" on the wire means
  // "everything", but a *stored* "all" is the legacy value for all characters.
  it("round-trips the new forms", () => {
    const forms: OwnerScope[] = [
      { kind: "character", characterId: 9 },
      { kind: "all-characters" },
      { kind: "corporation", corporationId: 98000001 },
      { kind: "all-corporations" },
      { kind: "everything" },
    ];
    for (const owner of forms) {
      expect(readStoredScope(ownerScopeKey(owner))).toEqual(owner);
    }
  });

  it("keeps the legacy meaning of a stored \"all\" distinct from everything", () => {
    expect(ownerScopeKey({ kind: "everything" })).toBe("everything");
    expect(ownerScopeParam({ kind: "everything" })).toBe("all");
  });

  it("rejects junk rather than inventing an owner", () => {
    expect(readStoredScope(null)).toBeNull();
    expect(readStoredScope("")).toBeNull();
    expect(readStoredScope("char:0")).toBeNull();
    expect(readStoredScope("corp:nope")).toBeNull();
  });
});
