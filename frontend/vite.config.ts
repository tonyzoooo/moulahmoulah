import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    // Bind all interfaces (dual-stack) so both localhost and 127.0.0.1 work.
    host: true,
    proxy: {
      "/api": "http://localhost:3000",
    },
  },
});
