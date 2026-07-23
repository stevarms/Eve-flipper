interface IndustrySummaryCardProps {
  label: string;
  value: string;
  subtext?: string;
  color?: string;
  /** Multi-line breakdown shown on hover. Rendered via the native title
   *  attribute — newlines are honored. When present, the card grows a
   *  `cursor-help` cue so the tooltip is discoverable. */
  tooltip?: string;
}

export function IndustrySummaryCard({
  label,
  value,
  subtext,
  color = "text-eve-accent",
  tooltip,
}: IndustrySummaryCardProps) {
  return (
    <div
      className={`bg-eve-panel border border-eve-border rounded-sm p-3 ${tooltip ? "cursor-help" : ""}`}
      title={tooltip}
    >
      <div className="text-[10px] uppercase tracking-wider text-eve-dim mb-1">{label}</div>
      <div className={`text-lg font-mono font-semibold ${color}`}>{value}</div>
      {subtext && <div className="text-xs text-eve-dim">{subtext}</div>}
    </div>
  );
}
