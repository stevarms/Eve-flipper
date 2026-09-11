/* @vitest-environment jsdom */

import { afterEach, describe, expect, it, vi } from "vitest";
import { getStatus } from "./api";

function respondWith(body: string, url: string, status = 200): Response {
  const res = new Response(body, {
    status,
    headers: { "Content-Type": "application/json" },
  });
  // `new Response()` leaves url empty; a real fetch would have set it.
  Object.defineProperty(res, "url", { value: url });
  return res;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

/**
 * A 2xx whose body will not parse is a server bug. It used to reach the UI as a
 * bare browser SyntaxError ("... is not valid JSON") naming no endpoint, which
 * is unactionable when several calls are in flight at once.
 */
describe("handleResponse on a malformed success body", () => {
  it("names the route when the body is empty", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(respondWith("", "http://box:13370/api/status")),
    );

    await expect(getStatus()).rejects.toThrow(/\/api\/status/);
  });

  it("reports the status code alongside the route", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(respondWith("<!doctype html>", "http://box:13370/api/status")),
    );

    await expect(getStatus()).rejects.toThrow(/HTTP 200/);
  });

  it("still returns parsed JSON when the body is good", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        respondWith(
          JSON.stringify({ sde_loaded: true, sde_systems: 1, sde_types: 2, esi_ok: true }),
          "http://box:13370/api/status",
        ),
      ),
    );

    await expect(getStatus()).resolves.toMatchObject({ esi_ok: true });
  });

  it("leaves genuine HTTP errors reporting their own message", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        respondWith(JSON.stringify({ error: "invalid json" }), "http://box:13370/api/status", 400),
      ),
    );

    await expect(getStatus()).rejects.toThrow("invalid json");
  });
});
