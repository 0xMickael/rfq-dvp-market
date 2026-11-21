import { defineConfig } from "vite";

// Override the API target for isolated integration processes.
const target = process.env.API_PROXY_TARGET ?? "http://127.0.0.1:8080";
export default defineConfig({
  server: { proxy: { "/api": target } },
  preview: { proxy: { "/api": target } },
});
