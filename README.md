# tonyTheHelper

A Thai-language household stock tracker that runs inside LINE.

A family records what they keep at home, how much of it is left, and who changed it.
The primary user is elderly, which drives most of the design: no passwords, large type,
and no keyboard needed for anything done daily.

> **Status: designed, not yet built.** There is no application code in this repository yet.
> The design is settled and broken into 12 tickets — see [Working on this](#working-on-this).

## What it does

- **Sign in with LINE.** No username, no password, nothing to remember or type.
- **Create a household** and invite family by sending them a link in LINE.
- **Add items** with a name, a free-text unit, and an optional description and photo.
- **Adjust quantities** with large plus and minus buttons, or set a total after shopping.
- **See who changed what**, when, and why — every change is recorded permanently.
- **Flag items running low** against an optional per-item threshold.

Deliberately out of scope: passwords, expiry dates, push notifications, shopping lists,
categories, barcode scanning, and storage locations.

## Stack

| Layer | Choice |
| --- | --- |
| Frontend | Vite + React + TypeScript + Tailwind, running as a LINE LIFF app |
| API | Go |
| Database | PostgreSQL on Supabase |
| Images | Supabase Storage, resized on the phone before upload |
| Hosting | Fly.io, region `sin` |
| CI/CD | GitHub Actions — build web, build Go, deploy |

The web app is built to static files and **embedded into the Go binary** with `go:embed`,
so the whole product is one image, one deployment, one domain, and no CORS. See
[ADR-0003](docs/adr/0003-the-frontend-ships-inside-the-go-binary.md).

## Layout

```
api/                 Go API; embeds and serves the built web app
  internal/config/   environment settings, validated at startup
  internal/line/     LINE ID token verification
  internal/session/  signed session cookies
  internal/server/   HTTP surface: health, sign-in, and the web app
  internal/store/    database access and embedded migrations
  static/            the built web app, written here by `vite build` (gitignored)
web/                 Vite + React LIFF app (Thai UI)
docs/adr/            architecture decision records
docs/agents/         how coding agents work in this repo
CONTEXT.md           domain glossary, English and Thai
Dockerfile           builds web, then the Go binary, into one image
fly.toml             Fly.io deployment, region sin
```

## Configuration

The API reads its settings from the environment and refuses to start if any are
missing. Copy [.env.example](.env.example) and fill it in; in production these are set
with `fly secrets set`, never in `fly.toml`.

| Variable | What it is |
| --- | --- |
| `DATABASE_URL` | Supabase Postgres connection string |
| `LINE_CHANNEL_ID` | LINE Login channel id; ID tokens must be issued for it |
| `LINE_LIFF_ID` | The LIFF app id. Public — served to the frontend at runtime, so changing it needs no rebuild |
| `SESSION_SECRET` | Signs session cookies. At least 32 bytes |
| `SUPABASE_URL` | Supabase project URL, for object storage |
| `SUPABASE_SERVICE_ROLE_KEY` | Signs storage URLs. Bypasses RLS — server only, never sent to the browser |
| `SUPABASE_STORAGE_BUCKET` | Optional, defaults to `item-images`. Must be a **private** bucket |
| `PORT` | Optional, defaults to 8080 |

## Running it locally

```bash
cp .env.example .env                     # then fill it in
cd web && npm install && npm run build   # writes ../api/static
cd ../api && go run .                    # http://localhost:8080
```

A local Postgres is enough for everything except signing in:

```bash
docker run -d --name tth-pg -e POSTGRES_PASSWORD=test -e POSTGRES_DB=tth \
  -p 55432:5432 postgres:17-alpine
```

### Signing in during development

LIFF only runs from an HTTPS endpoint that LINE knows about, so `localhost` alone will
not do. Register a **second, development LIFF app** pointing at a tunnel, and never
point the production LIFF app at your laptop:

```bash
cloudflared tunnel --url http://localhost:8080   # or: ngrok http 8080
```

Put the tunnel URL in the development LIFF app's endpoint, and its id in `LINE_LIFF_ID`.

For frontend work, run the Go API and Vite side by side — Vite proxies `/healthz` and
`/api` through to the Go server, so the frontend uses the same relative paths it will use
in production:

```bash
cd api && go run .        # terminal 1
cd web && npm run dev     # terminal 2
```

Tests and checks:

```bash
cd api && go test ./... -race             # store tests skip without TEST_DATABASE_URL
cd web && npm run lint && npm run build   # build runs tsc -b, so this typechecks too
docker build -t tonythehelper .           # what CI and Fly.io build
```

The store tests run against a real database rather than a fake, because an upsert, a
unique constraint and a uuid cast are exactly what a fake would get wrong:

```bash
TEST_DATABASE_URL='postgres://postgres:test@localhost:55432/tth?sslmode=disable' \
  go test ./internal/store/ -v
```

Deployment is automatic: pushing to `main` runs `flyctl deploy`. It needs a `FLY_API_TOKEN`
repository secret, created with `fly tokens create deploy --app <app-name>`.

## Start here

**[CONTEXT.md](CONTEXT.md)** is the vocabulary: every domain term with its canonical English
name for code and its canonical Thai name for the UI. Use those words. Don't invent synonyms.

**[docs/adr/](docs/adr/)** holds the decisions that are expensive to reverse:

| ADR | Decision |
| --- | --- |
| [0001](docs/adr/0001-quantity-is-a-column-history-is-a-log.md) | Quantity is a mutable column; history is an append-only log |
| [0002](docs/adr/0002-line-identity-is-the-only-identity.md) | LINE identity is the only identity |
| [0003](docs/adr/0003-the-frontend-ships-inside-the-go-binary.md) | The frontend ships inside the Go binary |
| [0004](docs/adr/0004-the-accessibility-floor.md) | The accessibility floor: 20px text, 4.5:1 contrast, 48px targets |

If your change contradicts an ADR, say so explicitly rather than quietly working around it.

## Working on this

Issues are the unit of work. Each one is a vertical slice — schema through API through UI —
that is demoable on a phone on its own, and sized to fit a single agent context.

Blocking edges are recorded as GitHub issue dependencies, so the tracker itself knows what
is startable. To see the frontier:

```bash
gh issue list --label ready-for-agent
```

The long chain is `#1 → #2 → #3 → #4 → #5`. After #5 lands, several tickets unblock at once
and can be picked up in any order.

Agent conventions — the issue tracker, triage labels, and how to read the domain docs — live
in [docs/agents/](docs/agents/) and are summarised for agents in [CLAUDE.md](CLAUDE.md).

## Restoring the agent skills

The skills this repo was designed with are gitignored but pinned:

```bash
npx skills experimental_install
```
