/**
 * Reloads the app when a newer build has been deployed.
 *
 * The problem this exists for is the home-screen shortcut. Its start URL is fixed, so a
 * new deploy cannot be signalled by changing the URL, and the app is *suspended and
 * resumed* rather than closed and reopened. A resumed page keeps running the JavaScript it
 * already has, without re-requesting `index.html`, so it can sit on a build from days ago
 * while the server has moved on. Serving the shell uncached is necessary but not
 * sufficient: it only helps on a load that actually happens.
 *
 * So the app asks. `version.json` is emitted by the same build that produced this bundle,
 * and is served uncached, so a difference between the two means a newer build exists.
 *
 * Checks are triggered by the things that actually coincide with a user coming back:
 * becoming visible again, being restored from the back/forward cache — which is what an
 * iOS webclip resume looks like — regaining network, and a slow interval as a backstop for
 * an app left open and visible for hours.
 */

/** How recently a reload must have happened for another to be suppressed. */
const RELOAD_GUARD_MS = 60_000
const RELOAD_GUARD_KEY = 'tth:last-reload'

/** Backstop for a session that stays open and visible without any other trigger. */
const POLL_MS = 5 * 60_000

export function startVersionCheck(): void {
  // In development the file is not emitted and the running code is whatever Vite just
  // served, so there is nothing to compare.
  if (!import.meta.env.PROD) return

  let checking = false

  const check = async (): Promise<void> => {
    if (checking || !navigator.onLine) return
    checking = true
    try {
      // Uncached three ways over: the server sends no-cache, the request says no-store,
      // and the URL is unique. Any one of them being honoured is enough.
      const res = await fetch(`/version.json?t=${Date.now()}`, { cache: 'no-store' })
      if (!res.ok) return

      const { build } = (await res.json()) as { build?: string }
      if (build && build !== __BUILD_ID__) reload()
    } catch {
      // Offline, or a transient failure. Another trigger will come along; a failed
      // version check must never be something the user hears about.
    } finally {
      checking = false
    }
  }

  const reload = (): void => {
    // Never reload out from under someone who is typing. Every dialog in this app holds
    // unsaved input — a half-entered item, a new house name — and losing it to a silent
    // reload would be worse than running yesterday's build for another few minutes. The
    // next trigger will find the dialog closed.
    if (document.querySelector('dialog[open]')) return

    // A stale shell served alongside a fresh version.json would otherwise reload in a
    // tight loop on someone's phone. There is no such edge cache in front of this app
    // today, but the cost of being wrong about that is high and the guard is three lines.
    const last = Number(sessionStorage.getItem(RELOAD_GUARD_KEY) ?? 0)
    if (Date.now() - last < RELOAD_GUARD_MS) {
      console.warn('version check: reload suppressed, one happened less than a minute ago')
      return
    }
    sessionStorage.setItem(RELOAD_GUARD_KEY, String(Date.now()))
    window.location.reload()
  }

  const onVisible = () => {
    if (document.visibilityState === 'visible') void check()
  }

  // `persisted` means the page came back from the back/forward cache rather than being
  // loaded — the resume case this whole module is for.
  const onPageShow = (event: PageTransitionEvent) => {
    if (event.persisted) void check()
  }

  document.addEventListener('visibilitychange', onVisible)
  window.addEventListener('pageshow', onPageShow)
  window.addEventListener('online', () => void check())
  setInterval(() => void check(), POLL_MS)

  void check()
}
