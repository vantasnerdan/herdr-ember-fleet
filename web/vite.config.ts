// The fleet page. `npm run dev` proxies /api (and its WebSockets) to the hub on
// 127.0.0.1:4790; `npm run dev:mock` serves the whole API from synthetic data instead
// (mock/), so the page can be built and looked at without a hub.
//
// The build lands in internal/webui/dist, which the Go hub embeds (go:embed all:dist).

import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import { mockApi } from "./mock/plugin.ts";

const HUB = "http://127.0.0.1:4790";

export default defineConfig(({ mode }) => {
  const mock = mode === "mock";
  return {
    root: fileURLToPath(new URL(".", import.meta.url)),
    base: "/",
    plugins: [vue(), ...(mock ? [mockApi()] : [])],
    server: {
      host: "127.0.0.1",
      port: 5173,
      strictPort: true,
      proxy: mock
        ? undefined
        : {
            "/api": {
              target: HUB,
              ws: true,
              changeOrigin: true,
              // The hub checks Origin against its own address on every request and
              // WebSocket. Through this proxy the browser's origin is the dev server,
              // so present the hub's own origin instead.
              configure(proxy) {
                proxy.on("proxyReq", (req) => {
                  if (req.getHeader("origin")) req.setHeader("origin", HUB);
                });
                proxy.on("proxyReqWs", (req) => {
                  if (req.getHeader("origin")) req.setHeader("origin", HUB);
                });
              },
            },
          },
    },
    build: {
      outDir: fileURLToPath(new URL("../internal/webui/dist", import.meta.url)),
      emptyOutDir: true,
      target: "es2022",
      chunkSizeWarningLimit: 900,
    },
  };
});
