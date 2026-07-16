import { useCallback, useState } from "react";
import { getCharacterMarketFees, type CharacterMarketFees } from "./api";
import { useI18n } from "./i18n";
import { useGlobalToast } from "../components/Toast";

interface UseEsiFeeImportOptions {
  /**
   * Set to false to suppress the built-in success toast (rare — most callers
   * want it). Error toasts always fire so the user knows why nothing happened.
   */
  successToast?: boolean;
}

/**
 * Wraps the "pull sales tax + broker fee from ESI skills" flow that would
 * otherwise be duplicated in every settings panel that has broker/tax fields
 * (Station Trading tax editor, Industry scanner, PI Factory).
 *
 * The caller owns the field mapping — its `apply(fees)` callback decides
 * which state to write to, since each caller stores fees in a slightly
 * different shape (StationTrading uses a full split-fees profile, others
 * store two flat numbers).
 */
export function useEsiFeeImport() {
  const { t } = useI18n();
  const { addToast } = useGlobalToast();
  const [loading, setLoading] = useState(false);

  const importFees = useCallback(
    async (
      apply: (fees: CharacterMarketFees) => void,
      opts: UseEsiFeeImportOptions = {},
    ) => {
      if (loading) return;
      setLoading(true);
      try {
        const fees = await getCharacterMarketFees();
        apply(fees);
        if (opts.successToast !== false) {
          addToast(
            t("esiFeeSyncSuccess", {
              tax: fees.suggested_sales_tax_percent.toFixed(2),
              fee: fees.suggested_broker_fee_percent.toFixed(2),
              acc: String(fees.accounting_level),
              br: String(fees.broker_relations_level),
            }),
            "success",
            2800,
          );
        }
      } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : t("esiFeeSyncFailed");
        addToast(msg, "error", 3000);
      } finally {
        setLoading(false);
      }
    },
    [addToast, loading, t],
  );

  return { importFees, loading };
}
