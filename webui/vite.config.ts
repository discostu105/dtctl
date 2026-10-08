import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// `npm run dev` serves the UI with HMR and proxies /api to a running
// `dtctl serve web --no-open` (default port 7878).
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: '../pkg/webui/dist',
    emptyOutDir: true,
    target: 'es2022',
    chunkSizeWarningLimit: 800,
  },
  server: {
    proxy: { '/api': { target: 'http://localhost:7878', headers: { host: 'localhost:7878' } } },
  },
})
