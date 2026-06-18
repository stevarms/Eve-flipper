import { useState } from "react";
import type { HostedAccessPlanOffer, HostedAccessStatus } from "../../lib/types";
import { trackClientTelemetry } from "../../lib/telemetry";

interface HostedAccessTabProps {
  access: HostedAccessStatus | null;
  loading: boolean;
  error: string | null;
  lastCheckedAt: Date | null;
  onReload: () => void;
  onRequestPayment: (planId: string) => Promise<void>;
  formatIsk: (value: number) => string;
}

type HostedPaymentHistoryRow = NonNullable<HostedAccessStatus["payment_history"]>[number];
type HostedAccessPayment = NonNullable<HostedAccessStatus["payment"]>;

function formatDate(value?: string | Date | null) {
  if (!value) return "N/A";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return date.toLocaleDateString() + " " + date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function statusTone(status?: string) {
  const normalized = (status ?? "").toLowerCase();
  if (normalized === "active" || normalized === "trial" || normalized === "grace") return "text-eve-success border-eve-success/40 bg-eve-success/10";
  if (normalized === "expired" || normalized === "blocked") return "text-eve-error border-eve-error/40 bg-eve-error/10";
  return "text-eve-accent border-eve-accent/40 bg-eve-accent/10";
}

function paymentStatusTone(status?: string) {
  const normalized = (status ?? "").toLowerCase();
  if (normalized === "matched") return "text-eve-success border-eve-success/40 bg-eve-success/10";
  if (
    normalized === "underpaid" ||
    normalized === "expired" ||
    normalized === "unmatched" ||
    normalized === "sender_mismatch" ||
    normalized === "receiver_mismatch"
  ) {
    return "text-eve-error border-eve-error/40 bg-eve-error/10";
  }
  if (normalized === "duplicate") return "text-eve-dim border-eve-border bg-eve-dark/60";
  return "text-eve-accent border-eve-accent/40 bg-eve-accent/10";
}

function receiverDisplay(payment: HostedAccessPayment) {
  const name = payment.receiver_name || "Configured receiver";
  if (payment.receiver_character_id) return `${name} (character ${payment.receiver_character_id})`;
  if (payment.receiver_corporation_id) return `${name} (corporation ${payment.receiver_corporation_id})`;
  return name;
}

function receiverInstructionLines(payment: HostedAccessPayment) {
  const lines = [`Receiver: ${payment.receiver_name || "Configured receiver"}`];
  if (payment.receiver_character_id) lines.push(`Receiver character ID: ${payment.receiver_character_id}`);
  if (payment.receiver_corporation_id) lines.push(`Receiver corporation ID: ${payment.receiver_corporation_id}`);
  return lines;
}

function formatStatus(value?: string) {
  return (value || "unknown").replace(/_/g, " ");
}

function planLimitSummary(plan: HostedAccessPlanOffer) {
  const parts = [`${plan.period_days} days`];
  parts.push(plan.scan_limit_per_day != null ? `${plan.scan_limit_per_day} scans/day` : "unlimited scans");
  if (plan.features?.includes("station_ai") || plan.station_ai_limit_per_day != null) {
    parts.push(plan.station_ai_limit_per_day != null ? `${plan.station_ai_limit_per_day} AI/day` : "unlimited AI");
  }
  return parts.join(" - ");
}

function paymentDelta(row: HostedPaymentHistoryRow) {
  if (!row.matched_amount_isk) return null;
  return row.matched_amount_isk - row.amount_isk;
}

function latestPaymentStatus(history: HostedAccessStatus["payment_history"]) {
  return history && history.length > 0 ? history[0]?.status?.toLowerCase() ?? "" : "";
}

function paymentHistoryHint(status?: string) {
  switch ((status ?? "").toLowerCase()) {
    case "pending":
      return "Waiting for wallet journal polling.";
    case "matched":
      return "Activated automatically.";
    case "underpaid":
      return "Amount is lower than required. Owner review is needed.";
    case "overpaid":
      return "Access activated; extra credit is manual-only.";
    case "expired":
      return "Transfer was after request expiry. Create a fresh request.";
    case "duplicate":
      return "This wallet reference was already processed.";
    case "sender_mismatch":
      return "Sender does not match the character that created the request.";
    case "receiver_mismatch":
      return "Transfer went to a different receiver wallet.";
    case "unmatched":
      return "Reason code was not recognized.";
    default:
      return "";
  }
}

function paymentState(access: HostedAccessStatus | null, paymentHistory: HostedAccessStatus["payment_history"]) {
  const status = (access?.status ?? "").toLowerCase();
  const latest = latestPaymentStatus(paymentHistory);
  if (access?.payment) {
    return {
      tone: "text-eve-accent border-eve-accent/40 bg-eve-accent/10",
      title: "Waiting for payment",
      body: "Send ISK with the exact reason code. Wallet polling will activate access automatically after the transfer is visible.",
    };
  }
  if (status === "active" || status === "trial" || status === "grace") {
    return {
      tone: "text-eve-success border-eve-success/40 bg-eve-success/10",
      title: status === "grace" ? "Subscription in grace period" : "Subscription active",
      body: "Paid access is enabled. Usage counters below show the current billing window.",
    };
  }
  if (latest === "matched") {
    return {
      tone: "text-eve-success border-eve-success/40 bg-eve-success/10",
      title: "Payment found",
      body: "The payment was matched. Refresh status if the subscription state has not updated yet.",
    };
  }
  if (latest === "underpaid" || latest === "expired" || latest === "unmatched" || latest === "sender_mismatch" || latest === "receiver_mismatch") {
    return {
      tone: "text-eve-error border-eve-error/40 bg-eve-error/10",
      title: "Payment needs review",
      body: paymentHistoryHint(latest) || "The latest payment attempt could not be activated automatically. Check payment history or contact support.",
    };
  }
  return {
    tone: "text-eve-dim border-eve-border bg-eve-dark/55",
    title: "Choose a plan",
    body: "Create a payment request, send ISK with the generated reason code, then refresh status.",
  };
}

export function HostedAccessTab({ access, loading, error, lastCheckedAt, onReload, onRequestPayment, formatIsk }: HostedAccessTabProps) {
  const [requestingPlan, setRequestingPlan] = useState<string | null>(null);
  const [paymentError, setPaymentError] = useState<string | null>(null);
  const usageEntries = Object.entries(access?.usage ?? {});
  const featureEntries = Object.entries(access?.features ?? {}).filter(([, enabled]) => enabled);
  const planOffers = access?.available_plans ?? [];
  const paymentHistory = access?.payment_history ?? [];
  const state = paymentState(access, paymentHistory);
  const refreshLabel = loading ? "Checking..." : "Refresh";
  const refreshStatusLabel = loading ? "Checking..." : "Refresh status";

  const copyPaymentCode = async () => {
    const payment = access?.payment;
    if (!access || !payment?.reason_code) return;
    try {
      await navigator.clipboard.writeText(payment.reason_code);
      trackClientTelemetry({
        event_type: "payment_instructions_copied",
        module: "hosted",
        properties: {
          plan: access.plan.id,
          amount_isk: payment.amount_isk,
        },
      });
    } catch {
      // Clipboard support is best-effort in desktop/web shells.
    }
  };

  const copyPaymentInstructions = async () => {
    const payment = access?.payment;
    if (!access || !payment?.reason_code) return;
    const lines = [
      "EVE Flipper hosted access payment",
      ...receiverInstructionLines(payment),
      `Amount: ${formatIsk(payment.amount_isk)} ISK`,
      `Reason: ${payment.reason_code}`,
      `Valid until: ${formatDate(payment.expires_at)}`,
    ];
    try {
      await navigator.clipboard.writeText(lines.join("\n"));
      trackClientTelemetry({
        event_type: "payment_instructions_copied",
        module: "hosted",
        properties: {
          plan: access.plan.id,
          amount_isk: payment.amount_isk,
          copy_mode: "full",
        },
      });
    } catch {
      // Clipboard support is best-effort in desktop/web shells.
    }
  };

  const createPaymentRequest = async (planId: string) => {
    if (!access) return;
    setRequestingPlan(planId);
    setPaymentError(null);
    const offer = planOffers.find((plan) => plan.id === planId);
    trackClientTelemetry({
      event_type: "plan_selected",
      module: "hosted",
      properties: {
        plan: planId,
        amount_isk: offer?.price_isk,
        subscription_status: access.status,
      },
    });
    try {
      await onRequestPayment(planId);
    } catch (e: any) {
      setPaymentError(e?.message || "Payment request failed");
    } finally {
      setRequestingPlan(null);
    }
  };

  if (loading && !access) {
    return <div className="flex h-full items-center justify-center text-eve-dim">Loading access...</div>;
  }

  return (
    <div className="space-y-4 text-sm">
      <div className="grid gap-3 md:grid-cols-[1.2fr_0.8fr]">
        <section className="border border-eve-border bg-eve-panel/65 p-4">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <div className="text-[10px] uppercase tracking-[0.2em] text-eve-dim">Current access</div>
              <div className="mt-2 flex flex-wrap items-center gap-2">
                <span className="text-2xl font-semibold text-eve-text">{access?.plan.name ?? "Unknown"}</span>
                <span className={`rounded-sm border px-2 py-0.5 text-[10px] uppercase tracking-[0.18em] ${statusTone(access?.status)}`}>
                  {access?.status ?? "unknown"}
                </span>
                {!access?.hosted && (
                  <span className="rounded-sm border border-eve-border bg-eve-dark px-2 py-0.5 text-[10px] uppercase tracking-[0.18em] text-eve-dim">
                    local
                  </span>
                )}
              </div>
            </div>
            <button
              type="button"
              onClick={onReload}
              disabled={loading}
              className="border border-eve-border bg-eve-dark px-3 py-1.5 text-[11px] uppercase tracking-[0.14em] text-eve-dim hover:border-eve-accent/60 hover:text-eve-accent disabled:opacity-50"
            >
              {refreshLabel}
            </button>
          </div>

          {error && <div className="mt-3 border border-eve-error/40 bg-eve-error/10 px-3 py-2 text-eve-error">{error}</div>}
          {access?.message && <div className="mt-3 text-eve-dim">{access.message}</div>}
          {lastCheckedAt && <div className="mt-3 text-xs text-eve-dim">Last checked {formatDate(lastCheckedAt)}</div>}
          <div className={`mt-4 border px-3 py-2 ${state.tone}`}>
            <div className="text-xs font-semibold uppercase tracking-[0.16em]">{state.title}</div>
            <div className="mt-1 text-xs opacity-85">{state.body}</div>
          </div>

          <div className="mt-4 grid gap-3 sm:grid-cols-2">
            <div className="border border-eve-border/70 bg-eve-dark/45 p-3">
              <div className="text-[10px] uppercase tracking-[0.18em] text-eve-dim">Expires</div>
              <div className="mt-1 text-eve-text">{formatDate(access?.plan.expires_at)}</div>
            </div>
            <div className="border border-eve-border/70 bg-eve-dark/45 p-3">
              <div className="text-[10px] uppercase tracking-[0.18em] text-eve-dim">Renews</div>
              <div className="mt-1 text-eve-text">{formatDate(access?.plan.renews_at)}</div>
            </div>
          </div>
        </section>

        <section className="border border-eve-border bg-eve-panel/65 p-4">
          <div className="flex items-center justify-between gap-3">
            <div className="text-[10px] uppercase tracking-[0.2em] text-eve-dim">Payment</div>
            <button
              type="button"
              onClick={onReload}
              disabled={loading}
              className="border border-eve-border bg-eve-dark px-2.5 py-1 text-[10px] uppercase tracking-[0.14em] text-eve-dim hover:border-eve-accent/60 hover:text-eve-accent disabled:opacity-50"
            >
              {refreshStatusLabel}
            </button>
          </div>
          {access?.payment ? (
            <div className="mt-3 space-y-3">
              <div className="border border-eve-accent/35 bg-eve-accent/10 px-3 py-2 text-xs text-eve-accent">
                Waiting for ISK transfer. Send the exact amount with this reason code, then refresh status.
              </div>
              <div>
                <div className="text-eve-dim">Amount</div>
                <div className="text-xl font-semibold text-eve-accent">{formatIsk(access.payment.amount_isk)} ISK</div>
              </div>
              <div>
                <div className="text-eve-dim">Receiver</div>
                <div className="text-eve-text">{receiverDisplay(access.payment)}</div>
              </div>
              <button
                type="button"
                onClick={copyPaymentCode}
                className="w-full border border-eve-accent/50 bg-eve-accent/10 px-3 py-2 text-left font-mono text-eve-accent hover:bg-eve-accent/15"
              >
                {access.payment.reason_code}
              </button>
              <button
                type="button"
                onClick={copyPaymentInstructions}
                className="w-full border border-eve-border bg-eve-dark/70 px-3 py-2 text-left text-xs uppercase tracking-[0.12em] text-eve-dim hover:border-eve-accent/50 hover:text-eve-accent"
              >
                Copy full payment instructions
              </button>
              <div className="text-xs text-eve-dim">Valid until {formatDate(access.payment.expires_at)}</div>
            </div>
          ) : (access?.status === "active" || access?.status === "trial" || access?.status === "grace") ? (
            <div className="mt-3 space-y-3">
              <div className="border border-eve-success/35 bg-eve-success/10 px-3 py-2 text-xs text-eve-success">
                No pending payment. Current hosted access is active.
              </div>
              {planOffers.length > 0 && (
                <div className="space-y-2">
                  <div className="text-[10px] uppercase tracking-[0.18em] text-eve-dim">Extend or change plan</div>
                  {planOffers.map((plan) => (
                    <button
                      key={plan.id}
                      type="button"
                      onClick={() => { void createPaymentRequest(plan.id); }}
                      disabled={requestingPlan !== null}
                      className="w-full border border-eve-border bg-eve-dark/65 px-3 py-2 text-left hover:border-eve-accent/60 hover:bg-eve-accent/10 disabled:opacity-50"
                    >
                      <div className="flex items-center justify-between gap-3">
                        <span className="font-semibold text-eve-text">{plan.name}</span>
                        <span className="font-mono text-eve-accent">{formatIsk(plan.price_isk)} ISK</span>
                      </div>
                      <div className="mt-1 text-xs text-eve-dim">{planLimitSummary(plan)}</div>
                    </button>
                  ))}
                </div>
              )}
              {paymentError && <div className="border border-eve-error/40 bg-eve-error/10 px-3 py-2 text-xs text-eve-error">{paymentError}</div>}
            </div>
          ) : planOffers.length > 0 ? (
            <div className="mt-3 space-y-2">
              {planOffers.map((plan) => (
                <button
                  key={plan.id}
                  type="button"
                  onClick={() => { void createPaymentRequest(plan.id); }}
                  disabled={requestingPlan !== null}
                  className="w-full border border-eve-border bg-eve-dark/65 px-3 py-2 text-left hover:border-eve-accent/60 hover:bg-eve-accent/10 disabled:opacity-50"
                >
                  <div className="flex items-center justify-between gap-3">
                    <span className="font-semibold text-eve-text">{plan.name}</span>
                    <span className="font-mono text-eve-accent">{formatIsk(plan.price_isk)} ISK</span>
                  </div>
                  <div className="mt-1 text-xs text-eve-dim">{planLimitSummary(plan)}</div>
                </button>
              ))}
              {paymentError && <div className="border border-eve-error/40 bg-eve-error/10 px-3 py-2 text-xs text-eve-error">{paymentError}</div>}
            </div>
          ) : (
            <div className="mt-3 text-eve-dim">No pending payment request.</div>
          )}
        </section>
      </div>

      <section className="border border-eve-border bg-eve-panel/65 p-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="text-[10px] uppercase tracking-[0.2em] text-eve-dim">Payment history</div>
          {paymentHistory.length > 0 && <div className="text-xs text-eve-dim">latest {paymentHistory.length}</div>}
        </div>
        {paymentHistory.length === 0 ? (
          <div className="mt-3 text-eve-dim">No payment attempts yet.</div>
        ) : (
          <div className="mt-3 overflow-x-auto">
            <table className="min-w-full text-left text-xs">
              <thead className="border-b border-eve-border text-[10px] uppercase tracking-[0.16em] text-eve-dim">
                <tr>
                  <th className="py-2 pr-3 font-medium">Status</th>
                  <th className="py-2 pr-3 font-medium">Plan</th>
                  <th className="py-2 pr-3 font-medium">Required</th>
                  <th className="py-2 pr-3 font-medium">Paid</th>
                  <th className="py-2 pr-3 font-medium">Code</th>
                  <th className="py-2 pr-3 font-medium">Created</th>
                  <th className="py-2 pr-3 font-medium">Matched</th>
                  <th className="py-2 pr-3 font-medium">Note</th>
                </tr>
              </thead>
              <tbody>
                {paymentHistory.map((row) => {
                  const delta = paymentDelta(row);
                  return (
                    <tr key={`${row.code}-${row.created_at}`} className="border-b border-eve-border/55">
                      <td className="py-2 pr-3">
                        <span className={`inline-flex rounded-sm border px-2 py-0.5 uppercase tracking-[0.12em] ${paymentStatusTone(row.status)}`}>
                          {formatStatus(row.status)}
                        </span>
                      </td>
                      <td className="py-2 pr-3 font-semibold text-eve-text">{row.plan_id}</td>
                      <td className="py-2 pr-3 font-mono text-eve-accent">{formatIsk(row.amount_isk)} ISK</td>
                      <td className="py-2 pr-3 font-mono">
                        {row.matched_amount_isk ? (
                          <div className="space-y-0.5">
                            <div className="text-eve-text">{formatIsk(row.matched_amount_isk)} ISK</div>
                            {delta !== 0 && (
                              <div className={delta && delta > 0 ? "text-eve-success" : "text-eve-error"}>
                                {delta && delta > 0 ? "+" : ""}{formatIsk(delta ?? 0)} ISK
                              </div>
                            )}
                          </div>
                        ) : (
                          <span className="text-eve-dim">-</span>
                        )}
                      </td>
                      <td className="py-2 pr-3 font-mono text-eve-text">{row.code}</td>
                      <td className="py-2 pr-3 text-eve-dim">{formatDate(row.created_at)}</td>
                      <td className="py-2 pr-3 text-eve-dim">{row.matched_at ? formatDate(row.matched_at) : "-"}</td>
                      <td className="py-2 pr-3 text-eve-dim">{row.note || paymentHistoryHint(row.status) || "-"}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <div className="grid gap-3 lg:grid-cols-[1fr_1fr]">
        <section className="border border-eve-border bg-eve-panel/65 p-4">
          <div className="text-[10px] uppercase tracking-[0.2em] text-eve-dim">Usage</div>
          <div className="mt-3 space-y-3">
            {usageEntries.length === 0 && <div className="text-eve-dim">No usage counters.</div>}
            {usageEntries.map(([key, usage]) => {
              const limit = usage.limit ?? null;
              const remaining = usage.remaining ?? null;
              const pct = limit && limit > 0 ? Math.min(100, Math.max(0, (usage.used / limit) * 100)) : 0;
              return (
                <div key={key} className="border border-eve-border/70 bg-eve-dark/45 p-3">
                  <div className="flex items-center justify-between gap-3">
                    <span className="uppercase tracking-[0.16em] text-eve-dim">{key}</span>
                    <span className="text-eve-text">
                      {usage.used} / {limit ?? "unlimited"}
                    </span>
                  </div>
                  <div className="mt-2 h-1.5 bg-eve-dark">
                    <div className="h-full bg-eve-accent" style={{ width: `${pct}%` }} />
                  </div>
                  <div className="mt-2 flex justify-between text-xs text-eve-dim">
                    <span>{usage.window}</span>
                    <span>{remaining == null ? "unlimited" : `${remaining} left`}</span>
                  </div>
                </div>
              );
            })}
          </div>
        </section>

        <section className="border border-eve-border bg-eve-panel/65 p-4">
          <div className="text-[10px] uppercase tracking-[0.2em] text-eve-dim">Enabled features</div>
          <div className="mt-3 flex flex-wrap gap-2">
            {featureEntries.length === 0 && <div className="text-eve-dim">No feature flags.</div>}
            {featureEntries.map(([feature]) => (
              <span key={feature} className="border border-eve-border bg-eve-dark px-2 py-1 text-xs text-eve-text">
                {feature.replace(/_/g, " ")}
              </span>
            ))}
          </div>
        </section>
      </div>
    </div>
  );
}
