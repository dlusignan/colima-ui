import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv } from 'vite'

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  // Go backend address for the dev proxy. Override with COLIMA_UI_API.
  const apiTarget =
    env.COLIMA_UI_API || process.env.COLIMA_UI_API || 'http://127.0.0.1:8787'

  return {
    plugins: [react(), tailwindcss()],
    resolve: {
      alias: { '@': path.resolve(import.meta.dirname, './src') },
    },
    server: {
      proxy: {
        // changeOrigin rewrites Host but not Origin. Browsers send Origin on
        // same-origin POST and PUT, and the Go guard rejects the dev page's
        // origin, so the proxy sets the backend's own origin.
        '/api': {
          target: apiTarget,
          changeOrigin: true,
          headers: { Origin: apiTarget },
        },
      },
    },
  }
})
