import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// 5273 rather than the Vite default: 5173 is often already taken, and a fixed
// port keeps the daemon's "open the dashboard here" hint accurate.
const DEV_PORT = 5273;
const DAEMON = process.env.SUCCUBUS_ADDR ?? "127.0.0.1:7801";

export default defineConfig({
  plugins: [react()],
  server: {
    port: DEV_PORT,
    strictPort: true,
    proxy: {
      // Proxying keeps the browser same-origin, so SSE and fetch behave in dev
      // exactly as they will when the SPA is served from the Go binary.
      "/api": {
        target: `http://${DAEMON}`,
        changeOrigin: true,
        configure: (proxy) => {
          proxy.on("proxyRes", (proxyRes) => {
            if (proxyRes.headers["content-type"]?.includes("text/event-stream")) {
              proxyRes.headers["cache-control"] = "no-cache";
            }
          });
        },
      },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
