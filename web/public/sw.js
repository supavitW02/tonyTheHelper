/*
  The service worker exists for two reasons, in this order:

  1. Chrome will not treat the app as installable without one that has a fetch handler.
     Without it, "add to home screen" on Android produces a bookmark shortcut rather than
     an app with its own icon and window.
  2. Repeat opens skip re-downloading the JavaScript bundle, which matters on mobile data.

  What it deliberately does NOT do is cache HTML. The app shell is the one file that must
  always come from the network: the built asset filenames contain a content hash, so a
  fresh index.html is what points at the current build. Caching the shell would risk
  pinning someone to an old version of the app indefinitely, and the person using this
  cannot be talked through clearing a cache over the phone.
*/

const CACHE = 'tth-assets-v1'

// Asset filenames change with every deploy, so entries for old builds would otherwise
// pile up forever on someone's phone — they are never requested again, but never removed
// either. Keeping the most recent few is enough: a build is one JS file and one CSS file.
const MAX_ENTRIES = 8

// Only content-hashed build output is cacheable. A file at /assets/index-A1b2C3.js can
// never change meaning: if the contents change, so does the name. Anything else — the
// HTML, the API, the icons — goes to the network every time.
const isImmutableAsset = (url) => url.origin === self.location.origin && url.pathname.startsWith('/assets/')

self.addEventListener('install', () => {
  // Take over straight away rather than waiting for every tab to close, so a deployed
  // fix reaches the user on their next open.
  self.skipWaiting()
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    (async () => {
      // Drop caches from older versions of this worker.
      const names = await caches.keys()
      await Promise.all(names.filter((n) => n !== CACHE).map((n) => caches.delete(n)))
      await self.clients.claim()
    })(),
  )
})

self.addEventListener('fetch', (event) => {
  const { request } = event
  if (request.method !== 'GET') return

  const url = new URL(request.url)
  if (!isImmutableAsset(url)) return // straight to the network, unintercepted

  event.respondWith(
    (async () => {
      const cache = await caches.open(CACHE)
      const hit = await cache.match(request)
      if (hit) return hit

      const response = await fetch(request)
      // Only store a clean, complete response. Caching an error or an opaque redirect
      // would serve that failure back on every later load.
      if (response.ok && response.type === 'basic') {
        cache
          .put(request, response.clone())
          .then(() => trim(cache))
          .catch(() => {})
      }
      return response
    })(),
  )
})

/** Drops the oldest entries once the cache grows past MAX_ENTRIES. Cache API keys come
 *  back in insertion order, so the front of the list is the least recently added. */
async function trim(cache) {
  const keys = await cache.keys()
  for (const stale of keys.slice(0, Math.max(0, keys.length - MAX_ENTRIES))) {
    await cache.delete(stale)
  }
}
