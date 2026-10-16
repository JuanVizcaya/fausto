import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The Go server embeds the build output, so `npm run build` writes it next
// to the server package. During `npm run dev`, API calls go to a running
// `fausto serve`.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../internal/server/dist",
    emptyOutDir: true,
  },
  server: {
    proxy: { "/api": "http://127.0.0.1:7070" },
  },
});
