export type InterfacePage =
  | "overview"
  | "presets"
  | "adaptive"
  | "roles"
  | "context"
  | "templates"
  | "gallery"
  | "navigation"
  | "layout"
  | "density"
  | "panels"
  | "columns"
  | "filters"
  | "startup"
  | "scanner"
  | "regional"
  | "station"
  | "route"
  | "industry"
  | "ledger"
  | "mission"
  | "plex"
  | "share";

export const cockpitInterfacePages: { id: InterfacePage; label: string }[] = [
  { id: "overview", label: "Overview" },
  { id: "presets", label: "Profile Presets" },
  { id: "adaptive", label: "Adaptive Cockpit" },
  { id: "roles", label: "Role Aware" },
  { id: "context", label: "Context Cockpit" },
  { id: "templates", label: "Workspace Templates" },
  { id: "gallery", label: "Community Gallery" },
  { id: "navigation", label: "Main Navigation" },
  { id: "layout", label: "Per-tab Layout" },
  { id: "density", label: "Density" },
  { id: "panels", label: "Panels" },
  { id: "columns", label: "Column Presets" },
  { id: "filters", label: "Filter Presets" },
  { id: "startup", label: "Startup / Actions" },
  { id: "scanner", label: "Scanner / Flipper" },
  { id: "regional", label: "Regional Trade" },
  { id: "station", label: "Station Trading" },
  { id: "route", label: "Route Builder" },
  { id: "industry", label: "Industry" },
  { id: "ledger", label: "Ledger" },
  { id: "mission", label: "Mission Control" },
  { id: "plex", label: "PLEX+" },
  { id: "share", label: "Import / Export" },
];
