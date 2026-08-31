import { execSync } from 'node:child_process'

import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig, type Plugin } from 'vite'

/**
 * An identifier for this build, used to notice that a newer one has been deployed.
 *
 * It only has to differ between builds; what it actually says is a convenience. The
 * commit is preferred because it makes a deployed version identifiable from the outside,
 * and the deploy workflow passes it in as BUILD_ID because the Docker build context does
 * not include `.git`. The timestamp is the last resort, and is still a distinct id per
 * build, which is all this needs to be.
 */
function resolveBuildId(): string {
  const fromEnv = process.env.BUILD_ID ?? process.env.GITHUB_SHA
  if (fromEnv) return fromEnv.slice(0, 12)
  try {
    return execSync('git rev-parse --short HEAD', { stdio: ['ignore', 'pipe', 'ignore'] })
      .toString()
      .trim()
  } catch {
    return `t${Date.now().toString(36)}`
  }
}

/**
 * Writes the build id into the output as `version.json`, next to the bundle it describes.
 *
 * Emitted from the same value that is compiled into the bundle, so the file on the server
 * and the code running in the browser can never disagree about which build they are.
 */
function versionFile(buildId: string): Plugin {
  return {
    name: 'tth-version-file',
    generateBundle() {
      this.emitFile({
        type: 'asset',
        fileName: 'version.json',
        source: `${JSON.stringify({ build: buildId })}\n`,
      })
    },
  }
}

const BUILD_ID = resolveBuildId()

// The build writes straight into the Go package that embeds it (ADR-0003), so there
// is no copy step between `vite build` and `go build`.
export default defineConfig({
  plugins: [react(), tailwindcss(), versionFile(BUILD_ID)],
  define: {
    __BUILD_ID__: JSON.stringify(BUILD_ID),
  },
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
