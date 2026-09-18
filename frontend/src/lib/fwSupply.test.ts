/* @vitest-environment jsdom */

import { afterEach, describe, expect, it, vi } from "vitest";
import { deleteFWCampaign, updateFWCampaign } from "./api";

afterEach(() => {
  vi.unstubAllGlobals();
});

/**
 * The server refuses an unconfirmed campaign delete, and it is right to: the
 * lots are the only record of what was bought and what it cost. The client used
 * to omit ?confirm=1 entirely, so the guard's own explanatory sentence arrived
 * in the UI as an error with no way to satisfy it.
 *
 * The fix is to send the confirmation, not to relax the guard -- which means the
 * caller has to have asked first. That part is the dialog in FWSupply; this is
 * the wire format it depends on.
 */
describe("deleteFWCampaign", () => {
  it("confirms the delete, because the server will not do it otherwise", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    await deleteFWCampaign(42);

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toContain("/api/auth/fw/campaigns/42");
    expect(url).toContain("confirm=1");
    expect(init.method).toBe("DELETE");
  });
});

/**
 * The per-item override lists are how a user's own judgment overrides the
 * model's, so a PATCH has to send the whole list the toggle handler computed --
 * not a partial update the server would have to diff against its own copy to
 * apply correctly. json.Unmarshal on the Go side replaces the slice wholesale,
 * so "the whole list" is also the only reading the server can act on.
 */
describe("updateFWCampaign / type overrides", () => {
  it("sends the full included_types and excluded_types lists, not a delta", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ campaign_id: 7 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    await updateFWCampaign(7, {
      included_types: [{ type_id: 12345, type_name: "5MN Y-T8 Compact Microwarpdrive" }],
      excluded_types: [],
    });

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toContain("/api/auth/fw/campaigns/7");
    expect(init.method).toBe("PATCH");
    const body = JSON.parse(init.body as string);
    expect(body.included_types).toEqual([{ type_id: 12345, type_name: "5MN Y-T8 Compact Microwarpdrive" }]);
    expect(body.excluded_types).toEqual([]);
  });
});

/**
 * Choosing "no character" for the seller has to clear both fields the fee
 * resolver keys on -- seller_owner_kind and seller_owner_id -- not just zero
 * the ID and leave "character" behind, which would still send a character ID
 * of 0 to ESI's skill lookup on the next plan generation.
 */
describe("updateFWCampaign / seller character", () => {
  it("clears seller_owner_kind and seller_owner_id together when 'none' is chosen", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ campaign_id: 7 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    await updateFWCampaign(7, { seller_owner_kind: "", seller_owner_id: 0 });

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    const body = JSON.parse(init.body as string);
    expect(body.seller_owner_kind).toBe("");
    expect(body.seller_owner_id).toBe(0);
  });

  it("sends the pair together when a character is chosen", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ campaign_id: 7 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    await updateFWCampaign(7, { seller_owner_kind: "character", seller_owner_id: 98765 });

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    const body = JSON.parse(init.body as string);
    expect(body.seller_owner_kind).toBe("character");
    expect(body.seller_owner_id).toBe(98765);
  });
});
