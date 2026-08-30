# The frontend ships inside the Go binary

The LIFF web app (Vite + React + TypeScript) is built to static files and embedded into the
Go server with `//go:embed`, which serves them alongside the API. One repository, one
container image, one Fly.io app, one domain. There is no separate static host.

We chose this over deploying the frontend to a CDN because it removes CORS configuration, a
second deployment pipeline, a second domain, and the class of bug where the frontend and API
versions disagree. The cost is no CDN caching and a full image rebuild for a CSS change,
neither of which matters for a handful of users in Thailand hitting a Singapore region.

## Consequences

The GitHub Actions workflow must build `web/` before building `api/`, because the Go build
needs `web/dist` present in its context. A frontend-only change still redeploys the whole
binary. If the app ever grows a genuinely public audience, this is the first decision to
revisit.
