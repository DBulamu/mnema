import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Backend listens on :8080 in local-stack; the proxy keeps the frontend
// origin-clean (no CORS in dev) and lets prod stay on a single host.
const BACKEND_URL = process.env.MNEMA_BACKEND_URL ?? 'http://localhost:8080';

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/v1': { target: BACKEND_URL, changeOrigin: true },
      '/healthz': { target: BACKEND_URL, changeOrigin: true },
    },
  },
});
