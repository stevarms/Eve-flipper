import { useSyncExternalStore } from "react";
import { getStatus } from "./api";
import type { AppStatus } from "./types";

const POLL_INTERVAL_MS = 5000;
const REQUEST_TIMEOUT_MS = 8000;

/** Consecutive bad polls before ESI is declared down… */
const ESI_FAILS_FOR_DOWN = 3;
/** …and the wall-clock floor that must also pass. Both gates apply. */
const ESI_DOWN_MIN_DURATION_MS = 15000;
/** A backend that reported ESI healthy this recently is treated as a flap. */
const ESI_LAST_OK_GRACE_SEC = 60;
/** Consecutive failed requests to our own backend before we say so. */
const BACKEND_FAILS_FOR_DOWN = 3;

export interface AppStatusSnapshot {
  /** Last successful `/api/status` payload, or `null` before the first one. */
  status: AppStatus | null;
  /** `true` = ESI reachable, `false` = down, `null` = not yet known. */
  esiAvailable: boolean | null;
  /** `false` once our own backend has stopped answering `/api/status`. */
  backendReachable: boolean;
}

/**
 * One shared poller for `/api/status`.
 *
 * This exists as a module singleton rather than a hook-local interval for two
 * reasons, both of which produced a spurious full-screen "EVE servers are
 * unavailable" modal on LAN-served (Docker) installs:
 *
 *  - StatusBar and useEsiStatus each ran their own 5s interval, so the app
 *    polled twice as often as the backend's 10s health cache could answer.
 *  - `setInterval` fired regardless of whether the previous request had
 *    finished. Once `/api/status` got slow, requests piled up concurrently and
 *    a single blip rejected the whole pile at once, jumping the failure
 *    counter straight past its threshold. Polls here are self-scheduling:
 *    the next one is queued only after the previous settles.
 */
let snapshot: AppStatusSnapshot = {
  status: null,
  esiAvailable: null,
  backendReachable: true,
};

const listeners = new Set<() => void>();
let timer: ReturnType<typeof setTimeout> | null = null;
let inFlight = false;
let stopped = true;

let esiFailStreak = 0;
let esiFailFirstAt = 0;
let backendFailStreak = 0;

/**
 * Whether two payloads carry the same information.
 *
 * `/api/status` hands back a fresh object every poll, so without this the
 * snapshot identity would change every 5 seconds and re-render every
 * subscriber on a timer, whether or not anything actually moved.
 */
function sameStatus(a: AppStatus | null, b: AppStatus | null): boolean {
  if (a === b) return true;
  if (a === null || b === null) return false;
  return (
    a.sde_loaded === b.sde_loaded &&
    a.sde_systems === b.sde_systems &&
    a.sde_types === b.sde_types &&
    a.esi_ok === b.esi_ok &&
    a.esi_last_ok === b.esi_last_ok &&
    a.esi_error === b.esi_error
  );
}

function emit(next: Partial<AppStatusSnapshot>): void {
  const merged: AppStatusSnapshot = { ...snapshot, ...next };
  if (sameStatus(merged.status, snapshot.status)) {
    merged.status = snapshot.status;
  }
  if (
    merged.status === snapshot.status &&
    merged.esiAvailable === snapshot.esiAvailable &&
    merged.backendReachable === snapshot.backendReachable
  ) {
    return;
  }
  snapshot = merged;
  for (const listener of listeners) listener();
}

function applyStatus(status: AppStatus): void {
  if (status.esi_ok) {
    esiFailStreak = 0;
    esiFailFirstAt = 0;
    emit({ status, esiAvailable: true, backendReachable: true });
    return;
  }

  // ESI answered "not ok" but was healthy moments ago — a flap, not an outage.
  const nowSec = Math.floor(Date.now() / 1000);
  const lastOK = status.esi_last_ok ?? 0;
  if (lastOK > 0 && nowSec - lastOK <= ESI_LAST_OK_GRACE_SEC) {
    esiFailStreak = 0;
    esiFailFirstAt = 0;
    emit({ status, esiAvailable: snapshot.esiAvailable, backendReachable: true });
    return;
  }

  const now = Date.now();
  if (esiFailStreak === 0) esiFailFirstAt = now;
  esiFailStreak += 1;

  // Count alone is not evidence — polls can bunch up after a tab wakes. Only
  // a streak that has also lasted ESI_DOWN_MIN_DURATION_MS counts as an outage.
  const down =
    esiFailStreak >= ESI_FAILS_FOR_DOWN && now - esiFailFirstAt >= ESI_DOWN_MIN_DURATION_MS;

  emit({
    status,
    esiAvailable: down ? false : snapshot.esiAvailable,
    backendReachable: true,
  });
}

async function poll(): Promise<void> {
  if (inFlight || stopped) return;
  inFlight = true;

  const controller = new AbortController();
  const timeoutID = setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);

  try {
    const status = await getStatus(controller.signal);
    backendFailStreak = 0;
    applyStatus(status);
  } catch {
    // A failed request to our OWN backend says nothing about ESI: the LAN
    // dropped, the container restarted, the laptop woke from sleep. Report it
    // as its own condition and leave the last known ESI verdict alone, so a
    // local blip can no longer raise "EVE Online servers are unavailable".
    backendFailStreak += 1;
    if (backendFailStreak >= BACKEND_FAILS_FOR_DOWN) {
      emit({ backendReachable: false });
    }
  } finally {
    clearTimeout(timeoutID);
    inFlight = false;
    schedule();
  }
}

function schedule(): void {
  if (stopped) return;
  if (timer !== null) clearTimeout(timer);
  timer = setTimeout(() => void poll(), POLL_INTERVAL_MS);
}

// A backgrounded tab has its timers throttled, so the first poll after it comes
// back can be reporting on a connection that has since recovered. Re-check
// immediately instead of waiting out the interval.
function onVisibilityChange(): void {
  if (document.visibilityState === "visible") {
    if (timer !== null) {
      clearTimeout(timer);
      timer = null;
    }
    void poll();
  }
}

function start(): void {
  stopped = false;
  if (typeof document !== "undefined") {
    document.addEventListener("visibilitychange", onVisibilityChange);
  }
  void poll();
}

function stop(): void {
  stopped = true;
  if (typeof document !== "undefined") {
    document.removeEventListener("visibilitychange", onVisibilityChange);
  }
  if (timer !== null) {
    clearTimeout(timer);
    timer = null;
  }
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  if (listeners.size === 1) start();
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) stop();
  };
}

function getSnapshot(): AppStatusSnapshot {
  return snapshot;
}

/** Store handle, exported so tests can drive the poller without React. */
export const appStatusStore = { subscribe, getSnapshot };

/** Subscribe to the whole shared `/api/status` snapshot. */
export function useAppStatus(): AppStatusSnapshot {
  return useSyncExternalStore(subscribe, getSnapshot);
}

/**
 * Subscribe to one derived value from the shared status.
 *
 * Prefer this over `useAppStatus` anywhere the component only needs a field.
 * `select` must return a primitive (or an already-stable reference): React
 * compares results with `Object.is` to decide whether to re-render, so
 * returning a fresh object would both defeat the point and spin.
 */
export function useAppStatusSelector<T>(select: (snapshot: AppStatusSnapshot) => T): T {
  return useSyncExternalStore(subscribe, () => select(snapshot));
}
