import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// small, modern build: no legacy polyfills, hashed file names for long caching
export default defineConfig({
  plugins: [react()],
  build: {
    target: "es2020",
    modulePreload: { polyfill: false },
    reportCompressedSize: true,
  },
  // during "npm run dev", send api calls to the nginx gateway from docker compose
  server: { proxy: { "/api": "http://localhost:8080" } },
});
