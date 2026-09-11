import { useAppStatusSelector } from "./appStatus";

interface UseEsiStatusReturn {
  /** `true` = ESI reachable, `false` = down, `null` = still loading initial check */
  esiAvailable: boolean | null;
  /** Backend's own words on why the last ESI probe failed, when it did. */
  esiError?: string;
}

/**
 * Whether the EVE ESI is reachable, according to the backend's health probe.
 *
 * Backed by the shared poller in `appStatus.ts`. This used to run its own 5s
 * interval alongside StatusBar's — see the note there for why one unguarded
 * interval per consumer was the wrong shape.
 *
 * Subscribes field by field on purpose: App.tsx is the only consumer and is far
 * too large to re-render every time a poll lands.
 */
export function useEsiStatus(): UseEsiStatusReturn {
  const esiAvailable = useAppStatusSelector((snapshot) => snapshot.esiAvailable);
  const esiError = useAppStatusSelector((snapshot) => snapshot.status?.esi_error);
  return { esiAvailable, esiError };
}
