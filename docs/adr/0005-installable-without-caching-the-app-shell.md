# Installable to the home screen, without caching the app shell

The app is opened from LINE, but the shortest path to it for the people who use it daily
is an icon on the phone's home screen: one tap, no opening LINE and finding a chat. That
means being installable as a web app, which means a manifest, real icons, and — on
Android — a service worker.

## The decision

A service worker exists, and it is allowed to cache exactly one thing: files under
`/assets/`, which Vite gives content-hashed names.

**It must never cache `index.html`.** The hashed asset names mean a fresh `index.html` is
the only thing that points at the current build. If the shell is cached, a user can be
pinned to an old version of the app indefinitely, and the person this app is built for
cannot be talked through clearing a browser cache over the phone. Everything that is not
a hashed asset — the HTML, `/api/*`, the icons — goes to the network on every request.

This is a real constraint on future work, not a preference. Dropping a standard
precaching library in and letting it handle "the app shell" would violate it.

## Why a service worker at all

Only because Chrome will not offer to install the app without one that has a fetch
handler. Without it, "add to home screen" on Android produces a bookmark shortcut rather
than an app with its own icon and window. iOS needs no service worker — it installs from
`apple-touch-icon` plus the standalone meta tags.

Caching the JavaScript bundle on repeat opens is a genuine second benefit on mobile data,
but it is not the reason the file is there.

The cache is capped at a small number of entries. Asset names change every deploy, so
without a cap the entries for old builds would accumulate on the phone forever: never
requested again, never removed.

## Icons must be truecolour PNGs

Palette-indexed PNGs are roughly a quarter the size, and the first version of the brand
icons was indexed for exactly that reason. **iOS silently ignores an indexed-palette PNG
for `apple-touch-icon` and substitutes a screenshot of the page instead.** No error, no
warning — the icon is simply wrong, which is how this was found.

The icons are therefore colour type 2 (truecolour RGB). They are still colour-quantised,
which keeps them small because the repetition compresses well, but the format stays RGB.
The in-page images — the header mark and the first-run logo — may stay indexed, because
browsers render those correctly; only the OS-level icon is fussy.

A maskable variant exists separately, with the artwork scaled to 72% and centred, because
Android crops a maskable icon to its own shape and anything outside the centre circle of
80% diameter is cut off.

## Consequences

- **`.webmanifest` needs a MIME registration in Go.** Go's built-in table has no entry for
  it, so the file is content-sniffed and served as `text/plain`, which some browsers refuse
  to parse — quietly making the app non-installable. There is a test for the content type.
- **The document title is the app's name to the operating system**, so it reads "Tony The
  Helper" rather than Thai. The heading inside the app stays Thai: that is the text the
  intended user reads, and ADR-0002's reasoning about who this is for applies to it.
- **Installability is checkable.** `beforeinstallprompt` firing is the signal, and it is
  verified in a browser rather than assumed.
- **The LIFF path still works unchanged.** Opening from LINE and opening from the home
  screen both land on the same origin, and the session cookie is checked before LIFF is
  touched, so an installed shortcut with a valid cookie opens straight into the app.
