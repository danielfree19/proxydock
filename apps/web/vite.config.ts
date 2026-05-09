import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// During `npm run dev`, the Vite dev server proxies API requests to the
// manager-api on host port 8090 so we don't need to deal with CORS.
// In the demo deployment, the manager-api serves the built bundle
// itself and same-origin handles routing.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      "/api": "http://localhost:8090",
      "/healthz": "http://localhost:8090",
    },
  },
});
