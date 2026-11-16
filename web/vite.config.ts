import { defineConfig } from "vite";

// Surcharge locale pour les processus de test sur des ports isoles.
const target = process.env.API_PROXY_TARGET ?? "http://127.0.0.1:8080";
export default defineConfig({
  server: { proxy: { "/api": target } },
  preview: { proxy: { "/api": target } },
});
