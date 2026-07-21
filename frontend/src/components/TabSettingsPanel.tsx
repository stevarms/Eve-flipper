import { useEffect, useState, type ReactNode } from "react";
import { TabHelp } from "./TabHelp";

interface TabSettingsPanelProps {
  title: string;
  hint?: string;
  icon?: string;
  defaultExpanded?: boolean;
  /** Optional help: step keys and wiki slug for ? button */
  help?: { stepKeys: string[]; wikiSlug: string };
  /** Optional extra content in the header row (e.g. preset picker) */
  headerExtra?: ReactNode;
  /** If set, persist expanded/collapsed state to localStorage under this key */
  persistKey?: string;
  children: ReactNode;
}

const STORAGE_PREFIX = "eve-settings-expanded:";

/**
 * Unified collapsible settings panel for tab-specific parameters.
 * Used to maintain consistent UI across all tabs.
 */
export function TabSettingsPanel({
  title,
  hint,
  icon = "⚙",
  defaultExpanded = false,
  help,
  headerExtra,
  persistKey,
  children,
}: TabSettingsPanelProps) {
  const [expanded, setExpanded] = useState(() => {
    if (persistKey) {
      const stored = localStorage.getItem(STORAGE_PREFIX + persistKey);
      if (stored !== null) return stored === "1";
    }
    return defaultExpanded;
  });

  const toggle = () => {
    setExpanded((prev) => {
      const next = !prev;
      if (persistKey) {
        localStorage.setItem(STORAGE_PREFIX + persistKey, next ? "1" : "0");
      }
      return next;
    });
  };

  return (
    <div className="bg-eve-panel border border-eve-border rounded-sm overflow-visible">
      <div className="flex items-center justify-between px-3 py-2">
        <button
          onClick={toggle}
          className="flex items-center gap-2 text-left hover:bg-eve-accent/5 transition-colors rounded-sm px-1 -ml-1"
        >
          <span className="text-eve-accent text-sm">{icon}</span>
          <span className="text-sm font-medium text-eve-text">{title}</span>
          {hint && <span className="text-xs text-eve-dim hidden sm:inline">— {hint}</span>}
          <span className="text-eve-dim text-xs">{expanded ? "▲" : "▼"}</span>
        </button>
        <div className="flex items-center gap-2" onClick={(e) => e.stopPropagation()}>
          {headerExtra}
          {help && <TabHelp stepKeys={help.stepKeys} wikiSlug={help.wikiSlug} />}
        </div>
      </div>

      {expanded && (
        <div className="px-3 pb-3 border-t border-eve-border/50">
          <div className="pt-3">{children}</div>
        </div>
      )}
    </div>
  );
}

// --- Reusable input components for settings ---

interface FieldProps {
  label: string;
  children: ReactNode;
  /** Optional hover-tooltip explaining the field. Rendered as a small
   *  question-mark badge next to the label; the tooltip appears via the
   *  native title attribute so it works everywhere without extra libs. */
  hint?: string;
}

export function SettingsField({ label, children, hint }: FieldProps) {
  return (
    <div className="flex flex-col gap-1">
      <label className="text-[11px] uppercase tracking-wider text-eve-dim font-medium inline-flex items-center gap-1">
        <span>{label}</span>
        {hint && (
          <span
            className="text-[10px] text-eve-dim/70 hover:text-eve-accent cursor-help border border-eve-border/50 rounded-full w-3.5 h-3.5 inline-flex items-center justify-center leading-none"
            title={hint}
            aria-label={hint}
          >
            ?
          </span>
        )}
      </label>
      {children}
    </div>
  );
}

interface NumberInputProps {
  value: number;
  onChange: (v: number) => void;
  min?: number;
  max?: number;
  step?: number;
  placeholder?: string;
}

export function SettingsNumberInput({
  value,
  onChange,
  min = 0,
  max = 999999999,
  step = 1,
  placeholder,
}: NumberInputProps) {
  const [draft, setDraft] = useState(String(value));
  const [focused, setFocused] = useState(false);

  useEffect(() => {
    if (!focused) {
      setDraft(String(value));
    }
  }, [focused, value]);

  const commit = (raw: string) => {
    const trimmed = raw.trim();
    if (trimmed === "" || trimmed === "-" || trimmed === "." || trimmed === "-.") {
      setDraft(String(value));
      return;
    }
    const parsed = parseFloat(trimmed);
    if (!Number.isFinite(parsed)) {
      setDraft(String(value));
      return;
    }
    const clamped = Math.min(max, Math.max(min, parsed));
    setDraft(String(clamped));
    onChange(clamped);
  };

  return (
    <input
      type="number"
      value={draft}
      onChange={(e) => {
        const raw = e.target.value;
        setDraft(raw);
        if (raw.trim() === "" || raw === "-" || raw === "." || raw === "-.") return;
        const v = parseFloat(raw);
        if (Number.isFinite(v) && v >= min && v <= max) onChange(v);
      }}
      onFocus={() => setFocused(true)}
      onBlur={(e) => {
        setFocused(false);
        commit(e.target.value);
      }}
      min={min}
      max={max}
      step={step}
      placeholder={placeholder}
      className="w-full px-3 py-1.5 bg-eve-input border border-eve-border rounded-sm text-eve-text text-sm font-mono
                 focus:outline-none focus:border-eve-accent focus:ring-1 focus:ring-eve-accent/30
                 transition-colors
                 [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
    />
  );
}

interface SelectInputProps {
  value: string | number;
  onChange: (v: string) => void;
  options: { value: string | number; label: string }[];
}

export function SettingsSelect({ value, onChange, options }: SelectInputProps) {
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className="w-full px-3 py-1.5 bg-eve-input border border-eve-border rounded-sm text-eve-text text-sm font-mono
                 focus:outline-none focus:border-eve-accent focus:ring-1 focus:ring-eve-accent/30
                 transition-colors"
    >
      {options.map((opt) => (
        <option key={opt.value} value={opt.value}>
          {opt.label}
        </option>
      ))}
    </select>
  );
}

interface CheckboxInputProps {
  checked: boolean;
  onChange: (v: boolean) => void;
  label?: string;
  /** When true, ignores clicks and grey-styles the control. The underlying
   *  `checked` value is still displayed so a user re-enabling the field
   *  gets their preference back — no state loss. */
  disabled?: boolean;
  /** Hover tooltip for the disabled state (or the whole control). */
  title?: string;
}

export function SettingsCheckbox({ checked, onChange, label, disabled, title }: CheckboxInputProps) {
  return (
    <div
      className={`flex items-center h-[34px] ${disabled ? "opacity-50" : ""}`}
      title={title}
    >
      <label className={`relative inline-flex items-center gap-2 ${disabled ? "cursor-not-allowed" : "cursor-pointer"}`}>
        <input
          type="checkbox"
          checked={checked}
          disabled={disabled}
          onChange={(e) => {
            if (disabled) return;
            onChange(e.target.checked);
          }}
          className="sr-only peer"
        />
        <div className="w-9 h-5 bg-eve-input border border-eve-border rounded-full peer
                      peer-checked:bg-eve-accent/30 peer-checked:border-eve-accent
                      after:content-[''] after:absolute after:top-[2px] after:left-[2px]
                      after:bg-eve-dim after:rounded-full after:h-4 after:w-4
                      after:transition-all peer-checked:after:translate-x-4 peer-checked:after:bg-eve-accent" />
        {label && <span className="text-xs text-eve-dim">{label}</span>}
      </label>
    </div>
  );
}

// Grid wrapper for settings fields
interface SettingsGridProps {
  children: ReactNode;
  cols?: number;
}

export function SettingsGrid({ children, cols = 4 }: SettingsGridProps) {
  const colsClass = {
    2: "grid-cols-2",
    3: "grid-cols-2 sm:grid-cols-3",
    4: "grid-cols-2 sm:grid-cols-3 md:grid-cols-4",
    5: "grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5",
  }[cols] ?? "grid-cols-2 sm:grid-cols-4";

  return (
    <div className={`grid ${colsClass} gap-x-3 gap-y-3 items-end`}>
      {children}
    </div>
  );
}

// Hints section at the bottom of settings
interface SettingsHintsProps {
  hints: string[];
}

export function SettingsHints({ hints }: SettingsHintsProps) {
  if (hints.length === 0) return null;
  return (
    <div className="mt-3 pt-2 border-t border-eve-border/30 text-xs text-eve-dim space-y-1">
      {hints.map((hint, i) => (
        <p key={i}>• {hint}</p>
      ))}
    </div>
  );
}
