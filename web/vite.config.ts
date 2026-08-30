import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// The build writes straight into the Go package that embeds it (ADR-0003), so there
// is no copy step between `vite build` and `go build`.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: '../api/static',
    // The Go build needs api/static to exist even before the web app is built, so a
    // committed .gitkeep lives there. Emptying the directory would delete it; the
    // build script clears stale hashed assets instead.
    emptyOutDir: false,
  },
  server: {
    // In development the Go API runs separately; proxy the API surface to it so the
    // frontend uses the same relative paths it will use in production.
    proxy: {
      '/healthz': 'http://localhost:8080',
      '/api': 'http://localhost:8080',
    },
  },
})
