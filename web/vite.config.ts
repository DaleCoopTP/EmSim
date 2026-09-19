import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  build: {
    // web/embed.go's //go:embed all:dist expects the build here.
    outDir: 'dist',
  },
  server: {
    // "npm run dev" talks to a separately running "emsim api"
    // (make compose-build/up, or "go run ./cmd/emsim api") instead of a
    // second copy of the backend baked into the dev server.
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
})
