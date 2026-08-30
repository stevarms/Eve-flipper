import type { Config } from "tailwindcss";
import animate from "tailwindcss-animate";

/** Helper: creates a Tailwind color value from a CSS RGB-triplet variable
 *  that supports opacity modifiers like bg-eve-accent/10 */
function v(name: string) {
  return `rgb(var(--eve-${name}) / <alpha-value>)`;
}

/** Same, for the semantic token layer defined in index.css (phase 0).
 *  These are palette-independent: green always means profit, etc. */
function s(name: string) {
  return `rgb(var(--${name}) / <alpha-value>)`;
}

const config: Config = {
  darkMode: "class",
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        eve: {
          dark: v("dark"),
          panel: v("panel"),
          "panel-hover": v("panel-hover"),
          input: v("input"),
          accent: v("accent"),
          "accent-hover": v("accent-hover"),
          "accent-dim": v("accent-dim"),
          text: v("text"),
          dim: v("dim"),
          success: v("success"),
          error: v("error"),
          warning: v("warning"),
          border: v("border"),
          "border-light": v("border-light"),
          glow: "var(--eve-glow)",
        },

        /* Semantic layer — use these for anything that carries meaning.
           `text-profit`, `bg-loss/10`, `border-warn`, … */
        profit: { DEFAULT: s("sem-profit"), dim: s("sem-profit-dim") },
        loss: { DEFAULT: s("sem-loss"), dim: s("sem-loss-dim") },
        warn: { DEFAULT: s("sem-warn"), dim: s("sem-warn-dim") },
        info: { DEFAULT: s("sem-info"), dim: s("sem-info-dim") },
        /* named `muted`, not `neutral` — `neutral` is a built-in Tailwind
           scale and shadowing it would break neutral-50…950 */
        muted: s("sem-neutral"),

        /* Text ramp — `text-fg`, `text-fg-secondary`, `text-fg-tertiary` */
        fg: {
          DEFAULT: s("text-primary"),
          secondary: s("text-secondary"),
          tertiary: s("text-tertiary"),
        },

        /* Surface ramp — follows the active faction palette */
        surface: {
          0: s("surface-0"),
          1: s("surface-1"),
          2: s("surface-2"),
          3: s("surface-3"),
        },

        /* EVE canonical security-status ramp */
        sec: {
          high: s("sec-high"),
          mid: s("sec-mid"),
          low: s("sec-low"),
          null: s("sec-null"),
        },
      },
      fontFamily: {
        eve: ['"JetBrains Mono"', "ui-monospace", "SFMono-Regular", "Consolas", "Monaco", "monospace"],
        mono: ['"JetBrains Mono"', "ui-monospace", "SFMono-Regular", "Consolas", "Monaco", "monospace"],
        /* Proportional UI face — labels, nav, headings, prose */
        ui: "var(--font-ui)",
        /* Tabular-figure face — numbers only */
        num: "var(--font-num)",
      },
      fontSize: {
        /* Additive, `t-` prefixed so Tailwind's default xs/sm/base scale is
           left alone — 87k LOC of existing markup depends on it. New and
           migrated surfaces use these; see docs/UI_DESIGN_SYSTEM.md.
           Weight carries hierarchy alongside size, not size alone. */
        "t-caption": ["0.6875rem", { lineHeight: "1rem" }],   // 11 — captions, badges
        "t-cell": ["0.75rem", { lineHeight: "1.125rem" }],    // 12 — table cells
        "t-body": ["0.8125rem", { lineHeight: "1.25rem" }],   // 13 — body / controls
        "t-emphasis": ["0.9375rem", { lineHeight: "1.375rem" }], // 15 — row identity
        "t-title": ["1.125rem", { lineHeight: "1.5rem" }],    // 18 — section titles
        "t-display": ["1.5rem", { lineHeight: "1.875rem" }],  // 24 — page titles / KPIs
      },
      spacing: {
        row: "var(--row-h)",
      },
      boxShadow: {
        "eve-glow": "0 0 8px var(--eve-glow)",
        "eve-glow-strong": "0 0 16px var(--eve-glow)",
        "eve-inset": "inset 0 1px 3px rgba(0, 0, 0, 0.5)",
      },
    },
  },
  plugins: [animate],
};
export default config;
