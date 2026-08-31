/**
 * Registers the service worker, which is what makes the app installable to a home screen.
 *
 * Production only. In development Vite serves modules from paths the worker does not
 * touch, so it would cache nothing and only get in the way of hot reloading.
 *
 * Failure is deliberately silent: a browser that refuses to register a worker — private
 * browsing, an old WebView, a user who has disabled it — still gets a fully working app,
 * just without the home-screen install. That is not worth an error message in Thai to
 * someone who cannot act on it.
 */
export function registerServiceWorker(): void {
  if (!import.meta.env.PROD) return
  if (!('serviceWorker' in navigator)) return

  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/sw.js').catch((err: unknown) => {
      console.warn('service worker registration failed', err)
    })
  })
}
