import { defineConfig } from "vite";
import react from "@vitejs/plugin-react-swc";
import path from "node:path";

// During dev, requests to /api and /openapi.yaml are proxied to the local
// Go backend so we don't fight CORS. In production, nginx serves the static
// build under the same origin as the API, so no proxy is needed.
export default defineConfig({
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
      "/api": "http://localhost:8080",
      "/openapi.yaml": "http://localhost:8080",
      "/docs": "http://localhost:8080",
    },
  },
  build: {
    outDir: "dist",
    sourcemap: true,
    target: "es2022",
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test-setup.ts"],
  },
});
