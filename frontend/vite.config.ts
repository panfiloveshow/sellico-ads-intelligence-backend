import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react-swc";
import path from "node:path";

// VITE_API_TARGET picks the backend the dev server proxies to:
//   - https://ads.sellico.ru — production API (default; works without local Go stack)
//   - http://localhost:8080  — when running `go run ./cmd/api` alongside vite
//
// `secure: false` only matters for self-signed local certs; with the Let's Encrypt
// production cert the request already validates fine, the flag is a no-op there.
//
// `changeOrigin: true` rewrites the Host header so nginx in production matches
// its server_name (otherwise it'd see Host: localhost:5173 and 404).
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "VITE_");
  const apiTarget = env.VITE_API_TARGET ?? "https://ads.sellico.ru";

  return {
    plugins: [react()],
    resolve: {
      alias: {
        "@": path.resolve(__dirname, "./src"),
      },
    },
    server: {
      port: 5173,
      host: true,
      proxy: {
        "/api": {
          target: apiTarget,
          changeOrigin: true,
          secure: true,
        },
        "/openapi.yaml": {
          target: apiTarget,
          changeOrigin: true,
          secure: true,
        },
        "/docs": {
          target: apiTarget,
          changeOrigin: true,
          secure: true,
        },
      },
    },
    build: {
      outDir: "dist",
      sourcemap: true,
      target: "es2022",
      // Manual chunks split the bundle into stable, cache-friendly pieces:
      //   - react       → react/react-dom/react-router; rarely changes once installed
      //   - mui         → ~280 KB by itself; isolating it stops every app code
      //                   change from busting the MUI cache hit
      //   - tanstack    → query + devtools, isolated for cache stability
      //   - charts      → Recharts + d3 (loaded only on detail pages later)
      //   - vendor      → catch-all for the rest of node_modules
      //   - <anonymous> → app code (the only thing changing on every push)
      // Targets the 500 KB warning: largest chunk drops well below 300 KB gzipped.
      rollupOptions: {
        output: {
          manualChunks(id) {
            if (!id.includes("node_modules")) return undefined;
            if (
              id.includes("react-router") ||
              /node_modules\/react(-dom)?\//.test(id) ||
              id.includes("/scheduler/") ||
              id.includes("/use-sync-external-store/")
            ) return "react";
            if (id.includes("@mui/") || id.includes("@emotion/")) return "mui";
            if (id.includes("@tanstack/")) return "tanstack";
            if (id.includes("recharts") || id.includes("d3-")) return "charts";
            return "vendor";
          },
        },
      },
    },
    test: {
      environment: "jsdom",
      globals: true,
      setupFiles: ["./src/test-setup.ts"],
    },
  };
});
