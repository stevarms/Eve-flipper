import { useCallback, useMemo } from "react";
import { openContractInGame, openMarketInGame, setWaypointInGame } from "./api";
import { handleEveUIError } from "./handleEveUIError";
import { useEveUiLoggedIn } from "./eveUiContext";
import { useI18n } from "./i18n";
import { useOptionalToast } from "../components/Toast";

/**
 * The three ESI in-game UI actions, with one correct error path.
 *
 * These `try { await …InGame(x) } catch { handleEveUIError }` blocks were
 * copy-pasted into six places with three different behaviours, and two of the
 * copies were wrong: StationTrading and ScanResultsTable called
 * `addToast(t(messageKey))` where `messageKey` can be `actionFailed`, whose
 * English is "Action failed: {error}". `i18n.tsx` only substitutes when
 * `params` is passed, so those two shipped a literal "{error}" to the user.
 * PositionsTab and Orders passed the param. Now there is one implementation.
 *
 * Not logged in is handled here rather than at the ESI call: a doomed request
 * comes back looking like a 401, which `handleEveUIError` classifies as
 * `reloginRequired` ("New permissions added! Please logout and login again") —
 * actively misleading for someone who simply never logged in. `loginRequired`
 * is the honest message and costs no round-trip.
 */
export function useEveUiActions() {
  const { t } = useI18n();
  const { addToast } = useOptionalToast();
  const loggedIn = useEveUiLoggedIn();

  const run = useCallback(
    async (fn: () => Promise<void>): Promise<boolean> => {
      if (!loggedIn) {
        addToast(t("loginRequired"), "error", 3000);
        return false;
      }
      try {
        await fn();
        return true;
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : String(err);
        const { messageKey, duration } = handleEveUIError({ message });
        addToast(
          messageKey === "actionFailed"
            ? t(messageKey, { error: message || "Unknown error" })
            : t(messageKey),
          "error",
          duration,
        );
        return false;
      }
    },
    [loggedIn, addToast, t],
  );

  return useMemo(
    () => ({
      /** Resolves true when the window was requested, false when it failed. */
      openMarket: (typeID: number) => run(() => openMarketInGame(typeID)),
      setDestination: (solarSystemID: number, clearOther = true, addToBeginning = false) =>
        run(() => setWaypointInGame(solarSystemID, clearOther, addToBeginning)),
      openContract: (contractID: number) => run(() => openContractInGame(contractID)),
      /** Escape hatch for callers that already hold a bespoke ESI UI promise. */
      run,
    }),
    [run],
  );
}
