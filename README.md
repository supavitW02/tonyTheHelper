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
web/                 Vite + React LIFF app (Thai UI)
docs/adr/            architecture decision records
docs/agents/         how coding agents work in this repo
CONTEXT.md           domain glossary, English and Thai
```

`api/` and `web/` do not exist yet — issue #1 creates them.

## Start here

**[CONTEXT.md](CONTEXT.md)** is the vocabulary: every domain term with its canonical English
name for code and its canonical Thai name for the UI. Use those words. Don't invent synonyms.

**[docs/adr/](docs/adr/)** holds the decisions that are expensive to reverse:

| ADR | Decision |
| --- | --- |
| [0001](docs/adr/0001-quantity-is-a-column-history-is-a-log.md) | Quantity is a mutable column; history is an append-only log |
| [0002](docs/adr/0002-line-identity-is-the-only-identity.md) | LINE identity is the only identity |
| [0003](docs/adr/0003-the-frontend-ships-inside-the-go-binary.md) | The frontend ships inside the Go binary |

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
