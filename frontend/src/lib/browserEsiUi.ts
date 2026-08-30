// Browser-side ESI-UI helper.
//
// CCP's esi-ui.* endpoints (open-market, set-waypoint, open-contract) only
// deliver the command to the running game client when the network request
// originates from the same public IP as the client. For users running
// eve-flipper on a remote Docker host, server-side calls return 204 but
// silently drop delivery. Calling ESI from the browser (same IP as the
// game) sidesteps that constraint.
//
// The tradeoff: the ESI access token has to leave the local DB and land in
// browser memory for the duration of the click. The refresh token stays
// server-side, so the exposure window is bounded to the token's ~20 minute
// lifetime. Users who need to close this hole can opt out via the
// SecurityVaultModal — the frontend then falls back to the server-side
// POST path (which works locally but not for remote Docker deployments).

const BASE = import.meta.env.VITE_API_URL || "";
const OPT_OUT_KEY = "esi.browser_ui_calls_disabled";
const TOKEN_REFRESH_SLACK_MS = 30_000; // refetch when < 30s left
// Cap cache lifetime independently of token expiry: if the user switches
// active character in another tab, we want to notice within a minute
// rather than serve a stale character's token for the full 20-min token
// lifetime.
const CACHE_MAX_AGE_MS = 60_000;

export function browserEsiUiDisabled(): boolean {
  try {
    return window.localStorage.getItem(OPT_OUT_KEY) === "1";
  } catch {
    return false;
  }
}

export function setBrowserEsiUiDisabled(disabled: boolean): void {
  try {
    if (disabled) {
      window.localStorage.setItem(OPT_OUT_KEY, "1");
    } else {
      window.localStorage.removeItem(OPT_OUT_KEY);
    }
  } catch {
    // ignore storage errors — the fallback path still works.
  }
}

interface CachedToken {
  accessToken: string;
  expiresAtMs: number;
  characterID: number;
  fetchedAtMs: number;
}

// Module-private cache so repeated clicks in a short window don't re-hit
// the backend token endpoint.
let cached: CachedToken | null = null;
let inFlight: Promise<CachedToken> | null = null;

async function fetchTokenFromBackend(): Promise<CachedToken> {
  const res = await fetch(`${BASE}/api/auth/esi-token`, {
    credentials: "include",
    headers: { Accept: "application/json" },
  });
  if (!res.ok) {
    // Surface the backend's error code so the caller's toast has something
    // more useful than "Failed to fetch".
    let body: { error?: string } | null = null;
    try {
      body = (await res.json()) as { error?: string };
    } catch {
      body = null;
    }
    throw new Error(body?.error || `token_fetch_failed_${res.status}`);
  }
  const body = (await res.json()) as {
    access_token?: string;
    expires_at?: string;
    character_id?: number;
  };
  if (!body.access_token || !body.expires_at || !body.character_id) {
    throw new Error("token_fetch_malformed");
  }
  return {
    accessToken: body.access_token,
    expiresAtMs: Date.parse(body.expires_at),
    characterID: body.character_id,
    fetchedAtMs: Date.now(),
  };
}

async function getToken(forceRefresh = false): Promise<CachedToken> {
  const now = Date.now();
  if (
    !forceRefresh &&
    cached &&
    cached.expiresAtMs - now > TOKEN_REFRESH_SLACK_MS &&
    now - cached.fetchedAtMs < CACHE_MAX_AGE_MS
  ) {
    return cached;
  }
  if (inFlight) return inFlight;
  inFlight = fetchTokenFromBackend()
    .then((tok) => {
      cached = tok;
      return tok;
    })
    .finally(() => {
      inFlight = null;
    });
  return inFlight;
}

// Wipe the cache — used when the app signals a character switch or logout.
export function clearBrowserEsiTokenCache(): void {
  cached = null;
}

type EsiCall = (accessToken: string) => Promise<Response>;

async function callEsiFromBrowser(call: EsiCall): Promise<void> {
  const tok = await getToken();
  let res = await call(tok.accessToken);
  // On 401, refresh once and retry (token might have been invalidated
  // out-of-band, e.g. scope change on the CCP side).
  if (res.status === 401) {
    const fresh = await getToken(true);
    res = await call(fresh.accessToken);
  }
  if (!res.ok && res.status !== 204) {
    let msg = `esi_${res.status}`;
    try {
      const body = (await res.json()) as { error?: string; error_description?: string };
      if (body?.error_description) msg = body.error_description;
      else if (body?.error) msg = body.error;
    } catch {
      // response wasn't JSON — leave msg as the status code.
    }
    throw new Error(msg);
  }
}

const ESI_ROOT = "https://esi.evetech.net/latest";

export async function openMarketViaBrowser(typeID: number): Promise<void> {
  await callEsiFromBrowser((token) =>
    fetch(`${ESI_ROOT}/ui/openwindow/marketdetails/?type_id=${typeID}`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${token}`,
      },
    }),
  );
}

export async function setWaypointViaBrowser(
  solarSystemID: number,
  clearOther: boolean,
  addToBeginning: boolean,
): Promise<void> {
  const params = new URLSearchParams({
    add_to_beginning: String(addToBeginning),
    clear_other_waypoints: String(clearOther),
    destination_id: String(solarSystemID),
  });
  await callEsiFromBrowser((token) =>
    fetch(`${ESI_ROOT}/ui/autopilot/waypoint/?${params.toString()}`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${token}`,
      },
    }),
  );
}

export async function openContractViaBrowser(contractID: number): Promise<void> {
  await callEsiFromBrowser((token) =>
    fetch(`${ESI_ROOT}/ui/openwindow/contract/?contract_id=${contractID}`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${token}`,
      },
    }),
  );
}
