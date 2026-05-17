import { useCallback, useEffect, useMemo, useState } from "react";
import { RefreshCw } from "lucide-react";
import { getPIPlanets, type CharacterScope } from "../../lib/api";
import type { PIPlanetRow } from "../../lib/types";

interface PIPlanetsTabProps {
  characterScope: CharacterScope;
  formatIsk: (value: number) => string;
}

function statusClass(status: string): string {
  switch (status) {
    case "running":
      return "border-green-500/35 text-green-400 bg-green-500/10";
    case "expiring":
      return "border-eve-warning/40 text-eve-warning bg-eve-warning/10";
    case "expired":
    case "needs_setup":
      return "border-eve-error/40 text-eve-error bg-eve-error/10";
    case "detail_unavailable":
      return "border-eve-border text-eve-dim bg-eve-panel";
    default:
      return "border-eve-border text-eve-text bg-eve-panel";
  }
}

function formatTime(value: string): string {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString(undefined, {
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function formatPlanetType(value: string): string {
  return value ? value.replace(/^temperate$/i, "Temperate") : "-";
}

export function PIPlanetsTab({ characterScope, formatIsk }: PIPlanetsTabProps) {
  const [planets, setPlanets] = useState<PIPlanetRow[]>([]);
  const [warnings, setWarnings] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const load = useCallback(
    (signal?: AbortSignal) => {
      setLoading(true);
      setError("");
      getPIPlanets(characterScope, signal)
        .then((resp) => {
          setPlanets(resp.planets);
          setWarnings(resp.warnings ?? []);
        })
        .catch((err) => {
          if (signal?.aborted) return;
          setError(
            err instanceof Error ? err.message : "Failed to fetch PI planets",
          );
          setPlanets([]);
          setWarnings([]);
        })
        .finally(() => {
          if (!signal?.aborted) setLoading(false);
        });
    },
    [characterScope],
  );

  useEffect(() => {
    const controller = new AbortController();
    load(controller.signal);
    return () => controller.abort();
  }, [load]);

  const totals = useMemo(() => {
    return planets.reduce(
      (acc, p) => {
        acc.value += p.stored_value_isk || 0;
        acc.daily += p.estimated_daily_value_isk || 0;
        acc.monthly += p.estimated_monthly_value_isk || 0;
        acc.gross += p.gross_isk_per_day || 0;
        acc.net += p.net_isk_per_day || 0;
        acc.factoryNet += p.factory_net_isk_per_day || 0;
        acc.input += p.factory_input_isk_per_day || 0;
        acc.output += p.factory_output_isk_per_day || 0;
        acc.extractors += p.extractor_pins || 0;
        acc.factories += p.factory_pins || 0;
        acc.idleFactories += p.idle_factory_pins || 0;
        acc.expiredExtractors += p.expired_extractor_pins || 0;
        acc.extractorDaily += p.extractor_value_isk_per_day || 0;
        return acc;
      },
      {
        value: 0,
        daily: 0,
        monthly: 0,
        gross: 0,
        net: 0,
        factoryNet: 0,
        input: 0,
        output: 0,
        extractors: 0,
        factories: 0,
        idleFactories: 0,
        expiredExtractors: 0,
        extractorDaily: 0,
      },
    );
  }, [planets]);

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <div className="text-xs uppercase tracking-[0.16em] text-eve-accent">
            PI planets
          </div>
          <div className="mt-1 text-[11px] text-eve-dim">
            Colony status, routed production chains and PI gross/net estimates
            from ESI + SDE schematics.
          </div>
        </div>
        <button
          type="button"
          onClick={() => load()}
          disabled={loading}
          className="inline-flex items-center gap-2 border border-eve-border bg-eve-panel px-2.5 py-1.5 text-xs text-eve-dim hover:text-eve-accent disabled:opacity-50"
        >
          <RefreshCw
            className={`h-3.5 w-3.5 ${loading ? "animate-spin" : ""}`}
            aria-hidden="true"
          />
          Refresh
        </button>
      </div>

      <div className="grid grid-cols-2 gap-2 lg:grid-cols-6">
        <Stat label="Planets" value={planets.length.toLocaleString()} />
        <Stat label="Extractors" value={totals.extractors.toLocaleString()} />
        <Stat label="Factories" value={totals.factories.toLocaleString()} />
        <Stat label="Stored value" value={`${formatIsk(totals.value)} ISK`} />
        <Stat label="Gross / day" value={`${formatIsk(totals.gross)} ISK`} />
        <Stat label="Net / day" value={`${formatIsk(totals.net)} ISK`} />
        <Stat
          label="Factory net"
          value={`${formatIsk(totals.factoryNet)} ISK`}
          tone={totals.factoryNet >= 0 ? "text-green-400" : "text-eve-error"}
        />
        <Stat label="Factory input" value={`${formatIsk(totals.input)} ISK`} />
        <Stat
          label="Factory output"
          value={`${formatIsk(totals.output)} ISK`}
        />
        <Stat
          label="Extractor / day"
          value={`${formatIsk(totals.extractorDaily)} ISK`}
        />
      </div>
      {(totals.idleFactories > 0 || totals.expiredExtractors > 0) && (
        <div className="grid grid-cols-1 gap-2 md:grid-cols-2">
          {totals.expiredExtractors > 0 && (
            <div className="border border-eve-error/40 bg-eve-error/10 px-3 py-2 text-xs text-eve-error">
              {totals.expiredExtractors.toLocaleString()} extractor program(s)
              look expired.
            </div>
          )}
          {totals.idleFactories > 0 && (
            <div className="border border-eve-warning/40 bg-eve-warning/10 px-3 py-2 text-xs text-eve-warning">
              {totals.idleFactories.toLocaleString()} factory pin(s) have no
              schematic.
            </div>
          )}
        </div>
      )}

      {error && (
        <div className="border border-eve-error/40 bg-eve-error/10 px-3 py-2 text-xs text-eve-error">
          {error}
          {error.includes("403") || error.includes("401") ? (
            <div className="mt-1 text-eve-dim">
              PI requires the `esi-planets.manage_planets.v1` scope. Re-login
              after updating scopes.
            </div>
          ) : null}
        </div>
      )}

      {warnings.length > 0 && (
        <div className="border border-eve-warning/40 bg-eve-warning/10 px-3 py-2 text-xs text-eve-warning">
          {warnings.slice(0, 4).map((warning, index) => (
            <div key={`${warning}-${index}`}>{warning}</div>
          ))}
        </div>
      )}

      {!loading && !error && planets.length === 0 && (
        <div className="flex h-52 items-center justify-center border border-eve-border bg-eve-panel/35 text-sm text-eve-dim">
          No PI planets visible for this scope.
        </div>
      )}

      {planets.length > 0 && (
        <div className="overflow-auto border border-eve-border">
          <table className="w-full text-xs">
            <thead className="sticky top-0 bg-eve-panel text-[10px] uppercase tracking-[0.12em] text-eve-dim">
              <tr>
                <th className="px-3 py-2 text-left font-medium">Planet</th>
                <th className="px-3 py-2 text-left font-medium">Character</th>
                <th className="px-3 py-2 text-right font-medium">Pins</th>
                <th className="px-3 py-2 text-right font-medium">Stored</th>
                <th className="px-3 py-2 text-right font-medium">Net/day</th>
                <th className="px-3 py-2 text-right font-medium">Gross/day</th>
                <th className="px-3 py-2 text-left font-medium">Products</th>
                <th className="px-3 py-2 text-right font-medium">Routes</th>
                <th className="px-3 py-2 text-left font-medium">Status</th>
                <th className="px-3 py-2 text-left font-medium">Next expiry</th>
              </tr>
            </thead>
            <tbody>
              {planets.map((planet) => (
                <tr
                  key={`${planet.character_id}-${planet.planet_id}`}
                  className="border-t border-eve-border/50 hover:bg-eve-panel-hover"
                >
                  <td className="px-3 py-2">
                    <div className="font-semibold text-eve-text">
                      {planet.solar_system_name ||
                        `System ${planet.solar_system_id}`}
                    </div>
                    <div className="text-[10px] text-eve-dim">
                      {formatPlanetType(planet.planet_type)} | upgrade{" "}
                      {planet.upgrade_level}
                    </div>
                  </td>
                  <td className="px-3 py-2 text-eve-dim">
                    {planet.character_name}
                  </td>
                  <td className="px-3 py-2 text-right font-mono text-eve-text">
                    {planet.num_pins}
                    <div className="text-[10px] text-eve-dim">
                      E{planet.extractor_pins} / F{planet.factory_pins} / S
                      {planet.storage_pins}
                    </div>
                  </td>
                  <td className="px-3 py-2 text-right font-mono text-eve-accent">
                    {formatIsk(planet.stored_value_isk)}
                    <div className="text-[10px] text-eve-dim">
                      {planet.stored_quantity.toLocaleString()} units
                    </div>
                  </td>
                  <td className="px-3 py-2 text-right font-mono text-eve-text">
                    <span
                      className={
                        (planet.net_isk_per_day ||
                          planet.estimated_daily_value_isk) >= 0
                          ? "text-green-400"
                          : "text-eve-error"
                      }
                    >
                      {formatIsk(
                        planet.net_isk_per_day ||
                          planet.estimated_daily_value_isk,
                      )}
                    </span>
                    <div className="text-[10px] text-eve-dim">
                      {formatIsk(planet.estimated_monthly_value_isk)} / mo
                    </div>
                  </td>
                  <td className="px-3 py-2 text-right font-mono text-eve-text">
                    {formatIsk(
                      planet.gross_isk_per_day ||
                        planet.extractor_value_isk_per_day,
                    )}
                    <div className="text-[10px] text-eve-dim">
                      in {formatIsk(planet.factory_input_isk_per_day || 0)}
                    </div>
                  </td>
                  <td className="px-3 py-2 text-eve-dim">
                    {(planet.product_flows ?? []).slice(0, 2).map((flow) => (
                      <div
                        key={`${flow.direction}-${flow.source}-${flow.type_id}`}
                        className="max-w-[220px] truncate"
                        title={`${flow.direction} ${flow.source}: ${flow.units_per_day.toLocaleString(undefined, { maximumFractionDigits: 1 })}/day`}
                      >
                        <span
                          className={
                            flow.direction === "output"
                              ? "text-green-400"
                              : "text-eve-warning"
                          }
                        >
                          {flow.direction === "output" ? "+" : "-"}
                        </span>{" "}
                        {flow.type_name || `Type ${flow.type_id}`} -{" "}
                        {formatIsk(flow.value_isk_per_day)}
                      </div>
                    ))}
                    {(planet.product_flows?.length ?? 0) === 0 && (
                      <span>-</span>
                    )}
                  </td>
                  <td className="px-3 py-2 text-right font-mono text-eve-text">
                    {planet.routed_pins}
                    <div className="text-[10px] text-eve-dim">
                      {planet.routed_quantity.toLocaleString()} units -{" "}
                      {Math.round(planet.cycle_health_score || 0)}%
                    </div>
                  </td>
                  <td className="px-3 py-2">
                    <span
                      className={`inline-flex px-2 py-0.5 border text-[10px] uppercase tracking-wide ${statusClass(planet.status)}`}
                    >
                      {planet.status.replace(/_/g, " ")}
                    </span>
                  </td>
                  <td className="px-3 py-2 text-eve-dim">
                    {formatTime(planet.next_expiry)}
                    {planet.warnings && planet.warnings.length > 0 && (
                      <div
                        className="mt-1 text-[10px] text-eve-warning"
                        title={planet.estimate_basis}
                      >
                        {planet.warnings.slice(0, 2).join("; ")}
                      </div>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className="text-[10px] leading-relaxed text-eve-dim">
        Estimate note: net/day uses extractor cycle output plus factory
        schematic output minus schematic inputs when routes/contents are
        visible. ESI still does not expose a historical PI ledger, so
        import/export taxes and missed cycles must be treated as diagnostics
        rather than guaranteed accounting.
      </div>
    </div>
  );
}

function Stat({
  label,
  value,
  tone = "text-eve-text",
}: {
  label: string;
  value: string;
  tone?: string;
}) {
  return (
    <div className="border border-eve-border bg-eve-panel/40 px-3 py-2">
      <div className="text-[10px] uppercase tracking-[0.14em] text-eve-dim">
        {label}
      </div>
      <div className={`mt-1 font-mono text-sm ${tone}`}>{value}</div>
    </div>
  );
}
