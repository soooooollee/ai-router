import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../internal/admin/webdist",
    emptyOutDir: true,
    assetsInlineLimit: 4096,
    sourcemap: false,
  },
  server: {
    proxy: { "/api": "http://127.0.0.1:8081" },
  },
});
