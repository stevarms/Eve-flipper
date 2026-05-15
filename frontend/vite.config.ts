import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "path";

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  clearScreen: false,
  server: {
    port: 1420,
    strictPort: true,
    proxy: {
      "/api": "http://127.0.0.1:13370",
      "/auth": "http://127.0.0.1:13370",
    },
  },
  envPrefix: ["VITE_"],
  build: {
    target: "esnext",
    minify: "esbuild",
    sourcemap: false,
    outDir: "dist",
    rollupOptions: {
      output: {
        manualChunks(id) {
          const normalizedId = id.replace(/\\/g, "/");

          if (normalizedId.includes("/src/components/industry/") || normalizedId.endsWith("/src/components/IndustryTab.tsx")) {
            return "feature-industry";
          }
          if (
            normalizedId.endsWith("/src/components/StationTrading.tsx") ||
            normalizedId.endsWith("/src/components/StationTradingExecutionCalculator.tsx") ||
            normalizedId.includes("/src/components/character-popup/")
          ) {
            return "feature-station";
          }
          if (normalizedId.includes("/src/components/plex-tab/") || normalizedId.endsWith("/src/components/PlexTab.tsx")) {
            return "feature-plex";
          }
          if (
            normalizedId.endsWith("/src/components/ContractResultsTable.tsx") ||
            normalizedId.endsWith("/src/components/ContractParametersPanel.tsx") ||
            normalizedId.endsWith("/src/components/ContractDetailsPopup.tsx")
          ) {
            return "feature-contracts";
          }
          if (normalizedId.endsWith("/src/components/RouteBuilder.tsx")) {
            return "feature-route";
          }

          if (!normalizedId.includes("/node_modules/")) return;
          if (/[\\/]node_modules[\\/](react|react-dom|scheduler)[\\/]/.test(normalizedId)) {
            return "vendor-react";
          }
          if (/[\\/]node_modules[\\/](@tanstack|react-window|lightweight-charts)[\\/]/.test(normalizedId)) {
            return "vendor-data";
          }
          if (/[\\/]node_modules[\\/](@radix-ui|lucide-react|cmdk)[\\/]/.test(normalizedId)) {
            return "vendor-ui";
          }
          return "vendor-misc";
        },
      },
    },
  },
});
