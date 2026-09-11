import { useAppStatus } from "@/lib/appStatus";
import { useI18n } from "@/lib/i18n";

function formatTimeAgo(timestamp: number): string {
  const seconds = Math.floor(Date.now() / 1000 - timestamp);
  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

export function StatusBar() {
  const { t } = useI18n();
  const { status, backendReachable } = useAppStatus();

  // Build ESI label with more info when unavailable
  const getEsiLabel = () => {
    // Our own backend going quiet is a different fault from ESI going quiet,
    // and saying so beats blaming CCP for a dropped LAN connection.
    if (!backendReachable) return t("backendUnreachable");
    if (status === null) return t("esiApi");
    if (status.esi_ok) return t("esiApi");

    // ESI is down - show when it was last working
    if (status.esi_last_ok) {
      return `${t("esiUnavailable")} (${formatTimeAgo(status.esi_last_ok)})`;
    }
    return t("esiUnavailable");
  };

  const sdeOk =
    (status?.sde_loaded ?? false) ||
    ((status?.sde_systems ?? 0) > 0 && (status?.sde_types ?? 0) > 0);

  const esiOk = backendReachable && (status?.esi_ok ?? false);
  const esiDetail = status?.esi_error ? `${t("esiLastError")}: ${status.esi_error}` : undefined;

  return (
    <div className="eve-header-status flex min-w-0 items-center gap-2 h-[34px] px-2 bg-eve-panel border border-eve-border rounded-sm">
      <StatusDot
        ok={sdeOk}
        loading={status === null && backendReachable}
        label={
          sdeOk
            ? `SDE: ${status?.sde_systems ?? 0} ${t("sdeSystems")}, ${status?.sde_types ?? 0} ${t("sdeTypes")}`
            : t("sdeLoading")
        }
      />
      <div className="w-px h-4 bg-eve-border" />
      <StatusDot
        ok={esiOk}
        loading={status === null && backendReachable}
        label={getEsiLabel()}
        warning={!esiOk && (status !== null || !backendReachable)}
        detail={esiDetail}
      />
    </div>
  );
}

function StatusDot({
  ok,
  loading,
  label,
  warning,
  detail,
}: {
  ok: boolean;
  loading: boolean;
  label: string;
  warning?: boolean;
  detail?: string;
}) {
  return (
    <div className="flex min-w-0 items-center gap-2 text-xs" title={detail}>
      <div
        className={`w-2 h-2 rounded-full ${
          loading
            ? "bg-eve-accent animate-pulse"
            : ok
              ? "bg-eve-success"
              : warning
                ? "bg-eve-error animate-pulse"
                : "bg-eve-error"
        }`}
      />
      <span className={`${ok ? "text-eve-text" : warning ? "text-eve-error" : "text-eve-dim"} min-w-0 truncate`}>{label}</span>
    </div>
  );
}
