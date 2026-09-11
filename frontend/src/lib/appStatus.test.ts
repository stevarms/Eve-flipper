/* @vitest-environment jsdom */

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AppStatus } from "./types";

const getStatus = vi.fn();
vi.mock("./api", () => ({
  getStatus: (signal?: AbortSignal) => getStatus(signal),
}));

const HEALTHY: AppStatus = {
  sde_loaded: true,
  sde_systems: 8490,
  sde_types: 19422,
  esi_ok: true,
};

async function loadStore() {
  vi.resetModules();
  const mod = await import("./appStatus");
  return mod.appStatusStore;
}

beforeEach(() => {
  vi.useFakeTimers();
  getStatus.mockReset();
});

afterEach(() => {
  vi.useRealTimers();
});

/**
 * These pin the fix for a spurious full-screen "EVE Online servers are
 * unavailable" modal on LAN-served (Docker) installs.
 *
 * The old hook counted *any* failed `/api/status` request toward an ESI-down
 * verdict, with no time window, no in-flight guard, and a 5s `setInterval` that
 * fired whether or not the previous request had finished. Once the status
 * endpoint got slow, requests piled up and one blip rejected the whole pile,
 * jumping the counter past its threshold instantly.
 */
describe("appStatus poller", () => {
  it("reports ESI available once a healthy status arrives", async () => {
    getStatus.mockResolvedValue(HEALTHY);
    const store = await loadStore();
    const unsubscribe = store.subscribe(() => {});

    await vi.advanceTimersByTimeAsync(0);

    expect(store.getSnapshot().esiAvailable).toBe(true);
    expect(store.getSnapshot().backendReachable).toBe(true);
    unsubscribe();
  });

  it("never blames ESI when it is our own backend that stopped answering", async () => {
    getStatus.mockRejectedValue(new TypeError("Failed to fetch"));
    const store = await loadStore();
    const unsubscribe = store.subscribe(() => {});

    await vi.advanceTimersByTimeAsync(60_000);

    const snapshot = store.getSnapshot();
    // This is the regression: a dropped LAN connection used to raise the modal.
    expect(snapshot.esiAvailable).toBeNull();
    expect(snapshot.backendReachable).toBe(false);
    unsubscribe();
  });

  it("treats a recently-healthy ESI as a flap, not an outage", async () => {
    getStatus.mockImplementation(async () => ({
      ...HEALTHY,
      esi_ok: false,
      esi_last_ok: Math.floor(Date.now() / 1000),
    }));
    const store = await loadStore();
    const unsubscribe = store.subscribe(() => {});

    await vi.advanceTimersByTimeAsync(60_000);

    expect(store.getSnapshot().esiAvailable).not.toBe(false);
    unsubscribe();
  });

  it("requires both a failure streak and elapsed time before declaring ESI down", async () => {
    getStatus.mockResolvedValue({ ...HEALTHY, esi_ok: false });
    const store = await loadStore();
    const unsubscribe = store.subscribe(() => {});

    // Three failures have landed by now (t=0, 5s, 10s) but only 10s has passed,
    // so the count gate alone must not be enough.
    await vi.advanceTimersByTimeAsync(10_000);
    expect(store.getSnapshot().esiAvailable).not.toBe(false);

    await vi.advanceTimersByTimeAsync(10_000);
    expect(store.getSnapshot().esiAvailable).toBe(false);
    unsubscribe();
  });

  it("recovers as soon as one healthy poll lands", async () => {
    getStatus.mockResolvedValue({ ...HEALTHY, esi_ok: false });
    const store = await loadStore();
    const unsubscribe = store.subscribe(() => {});

    await vi.advanceTimersByTimeAsync(20_000);
    expect(store.getSnapshot().esiAvailable).toBe(false);

    getStatus.mockResolvedValue(HEALTHY);
    await vi.advanceTimersByTimeAsync(5_000);

    expect(store.getSnapshot().esiAvailable).toBe(true);
    unsubscribe();
  });

  it("keeps only one request in flight, however slow the endpoint is", async () => {
    // Never settles, standing in for a status endpoint blocked behind a slow
    // ESI round trip. (Real fetch would reject on the abort timeout; the point
    // here is that no second request is issued while one is outstanding.)
    getStatus.mockImplementation(() => new Promise(() => {}));
    const store = await loadStore();
    const unsubscribe = store.subscribe(() => {});

    await vi.advanceTimersByTimeAsync(30_000);

    expect(getStatus).toHaveBeenCalledTimes(1);
    unsubscribe();
  });

  it("passes an abort signal so a hung request cannot outlive its timeout", async () => {
    getStatus.mockResolvedValue(HEALTHY);
    const store = await loadStore();
    const unsubscribe = store.subscribe(() => {});

    await vi.advanceTimersByTimeAsync(0);

    expect(getStatus.mock.calls[0][0]).toBeInstanceOf(AbortSignal);
    unsubscribe();
  });

  it("stops polling when the last subscriber goes away", async () => {
    getStatus.mockResolvedValue(HEALTHY);
    const store = await loadStore();
    const unsubscribe = store.subscribe(() => {});

    await vi.advanceTimersByTimeAsync(0);
    const callsWhileSubscribed = getStatus.mock.calls.length;
    unsubscribe();

    await vi.advanceTimersByTimeAsync(60_000);
    expect(getStatus.mock.calls.length).toBe(callsWhileSubscribed);
  });
});

describe("appStatus snapshot stability", () => {
  it("keeps the same snapshot across polls that change nothing", async () => {
    getStatus.mockResolvedValue(HEALTHY);
    const store = await loadStore();
    const unsubscribe = store.subscribe(() => {});

    await vi.advanceTimersByTimeAsync(0);
    const first = store.getSnapshot();

    await vi.advanceTimersByTimeAsync(20_000);

    // `/api/status` returns a fresh object every poll. If that reached
    // subscribers, App.tsx would re-render on a 5s timer for no reason.
    expect(getStatus.mock.calls.length).toBeGreaterThan(1);
    expect(store.getSnapshot()).toBe(first);
    unsubscribe();
  });

  it("publishes a new snapshot when the payload actually changes", async () => {
    getStatus.mockResolvedValue(HEALTHY);
    const store = await loadStore();
    const unsubscribe = store.subscribe(() => {});

    await vi.advanceTimersByTimeAsync(0);
    const first = store.getSnapshot();

    getStatus.mockResolvedValue({ ...HEALTHY, esi_error: "reachable but rate limited (HTTP 429)" });
    await vi.advanceTimersByTimeAsync(5_000);

    expect(store.getSnapshot()).not.toBe(first);
    expect(store.getSnapshot().status?.esi_error).toBe("reachable but rate limited (HTTP 429)");
    unsubscribe();
  });
});
