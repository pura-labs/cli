---
name: pura
description: Ship Docs, Sheets, Slides, Canvases, and Pages on Pura — the primitive layer agents work in. One identity, one audit log, one publishing surface. Every write is a proposal the user can accept or reject.
triggers:
  - pura
  - publish this
  - share a note
  - upload markdown
  - ai edit this doc
  - revert this doc
  - rollback version
  - pura.so
invocable: true
argument-hint: "<natural language: publish / edit / rollback / share / convert to sheet/slides/page/canvas>"
---

# Pura — Agent Skill

You are driving the `pura` CLI (one Go binary, Cobra-based) against the
Pura API at `https://pura.so`. Pura is **the primitive layer agents work
in** — five Office-familiar primitives on a web-native substrate,
tool-first all the way down:

- **Doc** · markdown prose you can ship as a permanent URL
- **Sheet** · typed rows (csv/json) with schemas and optional public forms
- **Slides** · markdown decks with `---` slide separators
- **Canvas** · SVG / canvas JS / ASCII — diffable visuals
- **Page** · single-file HTML served from its own subdomain

The CLI also supports three non-bootstrap kinds:

- **Image** · uploaded raster assets (`kind=image`, `substrate=image`)
- **File** · uploaded arbitrary files (`kind=file`, `substrate=file`)
- **Book** · ordered refs to existing items (`kind=book`, `substrate=refs`)

For slides authoring rules, per-slide tools, and the curated slides-only
theme set, defer to `pura-slides/SKILL.md`. This file owns kind
selection and CLI usage; the slides companion skill owns deck format.

Use this file as your source of truth. The command surface is stable;
error codes are typed; exit codes are scriptable.

**Tool-first:** every mutation (CLI, MCP, web button, chat LLM) flows
through the same dispatcher at `POST /api/tool/:name`. The CLI is one
client; MCP at `POST /mcp` is another. Everything you read or write here
ends up in the same audit log under one identity.

**Propose-gate:** every agent write lands as a proposal in the owner's
`/inbox` on pura.so. The CLI accepts or rejects on the user's behalf so
scripted flows still read like "one command → doc mutated". Use
`--dry-run` to preview without touching version history.

---

## Kinds — pick the right kind first

Before you run any command, decide the kind. "Kind" is what the user
sees; "substrate" is how it's stored. Default mapping:

| User intent | Kind | Substrate | Notes |
|---|---|---|---|
| Prose, spec, article, README | `doc` | markdown | Default for plain text. Agents patch prose line-by-line. |
| Form, table, leaderboard, tracker | `sheet` | csv or json | Schema-backed; `form.submitted` webhook available. |
| Slide deck, pitch, kickoff | `slides` | markdown | Slides use YAML frontmatter + `---` separators. See `pura-slides/SKILL.md`. |
| Diagram, logo, visualisation | `canvas` | svg / canvas / ascii | SVG default; `ascii` for terminal art. |
| Landing page, pricing page, micro-app | `page` | html | Served from `<slug>--<handle>.pura.so`. |
| Uploaded image, screenshot, photo | `image` | image | Upload-driven; not describe-driven. |
| Uploaded PDF, ZIP, binary blob | `file` | file | Upload-driven; read view is a download card. |
| Manual, anthology, ordered collection | `book` | refs | Curates existing items; use `pura book create`. |

When the intent is ambiguous, read the describe text: "form" / "signup"
/ "guestbook" → sheet; "deck" / "slides" / "pitch" → slides; "landing"
/ "pricing" / "site" → page; "logo" / "svg" / "diagram" → canvas;
"image" / "screenshot" / "photo" → image; "pdf" / "zip" / "binary" →
file; "manual" / "anthology" / "playbook" → book; else doc. `pura new
--describe …` runs the bootstrap subset server-side.

---

## Invariants

**MUST:**

- `pura auth login` before any command that mutates state. If the CLI
  returns exit code **2** (`unauthorized`), re-auth and retry.
- Read stderr for human-readable progress and the "Next:" hints. Read
  **stdout** for the JSON envelope when `--json` is on.
- Treat every agent write as a proposal — it lands in `/inbox` for the
  owner to accept. The CLI auto-accepts interactive writes in TTY mode
  and auto-rejects under `--dry-run`.
- Use `--dry-run` on `pura chat` when the user says "preview" / "试试"
  / "what if". The proposal is auto-rejected so version history stays
  pristine (no revert row to clean up).
- Use `pura new --describe "<prompt>"` for describe-driven create ("help
  me start a…", "draft a…"). Skip if the user already has content in a
  file — `pura push <file>` is the shorter path.
- Confirm destructive operations (`rm`, `versions restore`, `keys rm`)
  with `--yes` in non-interactive contexts or you'll hang on a prompt.

**NEVER:**

- Expose the raw token on stdout unless the user explicitly asked
  (`pura auth token --yes`). The envelope already carries a safe prefix.
- Commit credentials to git. `~/.config/pura/credentials.json` is mode
  0600 for a reason.
- Bypass `pura auth login` by pasting tokens unless the user is in CI.
  The device flow is two commands; it's rarely worth skipping.
- Invent slugs. Always read them back from `pura push` output or
  `pura ls`.

---

## Decision Tree

```
user wants to …
├── publish content
│   ├── file on disk?       → pura push <file> [--title "…"]
│   ├── piped stdin?        → <producer> | pura push --stdin --substrate <m> --title "…"
│   └── from a snippet?     → echo "…" | pura push --stdin --substrate markdown
│
├── read / share
│   ├── metadata?           → pura get <slug>
│   ├── raw body?           → pura get <slug> -f raw
│   ├── AI context JSON?    → pura get <slug> -f ctx
│   ├── browser link?       → pura open <slug>
│   └── pipe to another tool? → pura get <slug> -f raw | pandoc …
│
├── create (chat-first)
│   └── describe it?        → pura new --describe "<prompt>" [--yes]
│
├── edit
│   ├── full rewrite?       → pura edit <slug> --file <new.md>
│   ├── small AI tweak?     → pura chat <slug> "<instruction>"
│   ├── AI, preview only?   → pura chat <slug> "<instr>" --dry-run
│   ├── AI, TTY confirm?    → pura chat <slug> "<instr>" --interactive
│   ├── 409 pending stuck?  → pura chat <slug> "<instr>" --resolve=reject
│   └── scoped to a span?   → pura chat <slug> "<instr>" --selection "<text>"
│
├── rollback
│   ├── see history?        → pura versions ls <slug>
│   ├── view one?           → pura versions show <slug> <N>
│   ├── see the diff?       → pura versions diff <slug> <A> [<B>]
│   └── go back?            → pura versions restore <slug> <N> --yes
│
├── manage
│   ├── list own docs?      → pura ls
│   ├── delete?             → pura rm <slug> --yes
│   ├── claim anon docs?    → pura claim <edit_token>
│   ├── stats?              → pura stats <slug>  (--detail for owner)
│   └── activity stream?    → pura events <slug>  (--follow to tail)
│
├── setup / debug
│   ├── sign in?            → pura auth login  (or --token <t> for CI)
│   ├── verify token?       → pura auth status --verify
│   ├── sign out?           → pura auth logout
│   ├── mint CI key?        → pura keys create --name "ci:…" [--scope docs:write]
│   ├── revoke key?         → pura keys rm <id|prefix> --yes
│   └── why is it broken?   → pura doctor
│
└── switch profile (e.g. personal ↔ work)
                            → pass --profile <name> to ANY command
```

---

## Commands (condensed)

Every command accepts these global flags:

| Flag | Purpose |
|---|---|
| `--json` | Force JSON envelope (default on non-TTY) |
| `--jq <expr>` | Filter JSON output through built-in gojq (no external jq) |
| `--quiet` | Print raw data only (no envelope) |
| `--profile <name>` | Use a named credential profile |
| `--token <t>` | One-shot bearer token override |
| `--api-url <u>` | Point at a different Pura instance |
| `--verbose` / `-v` | HTTP trace to stderr |
| `--no-color` | Strip ANSI |

### Auth

```
pura auth login                   # device flow; opens browser
pura auth login --token sk_pura_…  # CI bypass
pura auth logout
pura auth status [--verify]       # --verify hits /api/auth/me
pura auth token [--yes]           # print raw; TTY guard, --yes to confirm
```

### Content

```
pura push <file> [--title "…"] [--substrate <m>] [--kind <k>] [--theme <p>] [--open]
pura push --stdin --substrate <m> --title "…"      # from pipe
pura get <slug> [-f raw|ctx|meta] [-o <file>]
pura edit <slug> --file <new.md>   | cat … | pura edit <slug> --stdin
pura rm  <slug> [--yes]
pura ls
pura open <slug>
pura preview <file>                # dry-run: what substrate would it be?
```

### AI editing (propose-gate)

Every chat turn produces a **proposal** on the server (`proposals` table
since 5a/5b, addressable by `proposal_id`). The CLI then accepts or
rejects on the user's behalf so scripted flows still look like "one
command → doc mutated". The same proposal pipeline backs MCP-driven
writes from other agents; the user sees one unified `/inbox`.

```
pura chat <slug> "<instruction>"
                  [--selection "<text>"]        # scope the edit
                  [--model <id>]                # from allow-list
                  [--dry-run]                   # auto-reject; leaves zero version noise
                  [--interactive]               # TTY: show diff, prompt y/N
                  [--yes]                       # with --interactive: skip prompt
                  [--resolve accept|reject]     # unblock 409 pending_exists
                  [--no-stream]                 # suppress stderr token stream
```

`--dry-run` auto-rejects the proposal server-side — no "apply then
restore" churn, no version rows. If the server returns 409
`pending_exists` (a previous proposal is still pending for this
owner+doc), pass `--resolve=accept|reject` to clear it first and
retry automatically.

### AI create

```
pura new --describe "<what you want>"
                  [--starter blog|form|landing|table|slides|canvas]
                  [--model <id>]
                  [--yes]       # auto-publish without the TTY confirmation
                  [--open]      # open the published doc in the browser
                  [--json]      # machine-readable envelope
```

Streams `/api/p/bootstrap` → infers kind/substrate/title/slug/schema →
on accept, publishes with `bootstrap_thread` so `/edit` opens with the
origin chat already in-thread.

Bootstrap supports the five core primitives (doc · sheet · slides ·
canvas · page). `image` and `file` are **upload-driven, not
describe-driven**; `book` is **collection-driven** (`pura book create`
then `pura book add`). Use `pura push <file.png>` / `pura push
<file.pdf>` for assets.

### Versions

```
pura versions ls      <slug>
pura versions show    <slug> <N>
pura versions diff    <slug> <A> [<B>]   [--color auto|always|never]
pura versions restore <slug> <N> [--yes]
```

### Keys (programmatic auth)

```
pura keys ls
pura keys create --name "<label>" [--scope docs:read --scope docs:write]
pura keys rm <id|prefix> [--yes]
```

### Claim, stats, events

```
pura claim <edit_token>                  # attach anon docs to your account
pura stats  <slug> [--detail]            # --detail requires ownership
pura events <slug> [--since <id>]
                   [--limit 1..200]
                   [--kinds doc.updated,comment.added]
                   [--follow]            # live tail
```

### Diagnostics

```
pura doctor                              # config / network / auth / profile checks
pura version                             # CLI version, commit, date
pura completion bash|zsh|fish
```

---

## Output envelope

Every command (other than `--quiet` raw payloads) emits:

```json
{
  "ok": true,
  "data": { ... },
  "summary": "Published https://pura.so/@you/ax12 (markdown, 1.2 KB)",
  "breadcrumbs": [
    {"action":"view",    "cmd":"pura open ax12",          "description":"Open in browser"},
    {"action":"edit",    "cmd":"pura chat ax12 \"...\"",  "description":"AI-edit"},
    {"action":"history", "cmd":"pura versions ls ax12",   "description":"See history"}
  ]
}
```

When `ok` is false, `data` is omitted and `error` is populated:

```json
{
  "ok": false,
  "error": { "code": "unauthorized", "message": "Token expired or missing.", "hint": "Run `pura auth login`." },
  "breadcrumbs": [ {"action":"retry","cmd":"pura auth login","description":"Sign in"} ]
}
```

**Agents: prefer the `breadcrumbs` field.** It's the explicit next-step
hint. If you're confused about what to run next, `.breadcrumbs[].cmd` is
the answer.

---

## Exit codes

Scripts branch on these. Values are contractual.

| Exit | Meaning | Typical cause |
|---:|---|---|
| 0 | OK | success |
| 1 | Generic | CLI error we don't route (flag parse, file I/O) |
| 2 | Auth | 401 — token missing or rejected; re-login |
| 3 | Forbidden | 403 — scope missing or not the owner |
| 4 | NotFound | 404 — slug / key / version doesn't exist |
| 5 | Invalid | 400 — bad input; read the `hint` |
| 6 | Conflict | 409 — slug taken, handle collision, race |
| 7 | RateLimit | 429 — wait `retry_after` then retry |
| 8 | API | 5xx, network, timeout, malformed response |

---

## Scenario recipes

### 1. Publish a report from a file

```bash
pura push report.md --title "Q4 Review" --json --jq .url
# → "https://pura.so/@you/ax12cd"
```

### 2. "Make the intro friendlier"

```bash
pura chat ax12cd "make the intro friendlier; keep it under 80 words"
# Streams tokens to stderr, prints envelope on stdout when done.
# The doc now has a new version; `pura versions ls ax12cd` to audit.
```

### 3. Preview an AI edit without keeping it

```bash
pura chat ax12cd "rewrite as bullet points" --dry-run --json --jq .data.content
# Writes the AI output then reverts. Envelope has dry_run=true.
```

### 4. "I liked v2 better — go back"

```bash
pura versions ls ax12cd                 # find the version you want
pura versions diff ax12cd 2 latest      # (latest is implicit when B omitted)
pura versions restore ax12cd 2 --yes
```

### 5. Anonymous publish → sign up → claim

```bash
# Before login, push anon:
TOKEN=$(pura push rough.md --json --jq -r '.data.token')

# Later, once authenticated:
pura auth login
pura claim "$TOKEN"
pura ls      # the anon doc is now under @you
```

### 6. CI / bot credentials

```bash
# One-time, at a workstation:
pura keys create --name "ci:github-actions" --scope docs:read --scope docs:write --json --jq -r .data.token
# → copy into the CI secret store as PURA_TOKEN

# In the CI job:
PURA_TOKEN=$PURA_TOKEN pura push CHANGELOG.md --title "$GITHUB_SHA"
```

### 7. Tail activity on a shared doc

```bash
pura events ax12cd --follow --kinds comment.added,version.restored
```

### 8. Observability before a shipping window

```bash
pura stats ax12cd --detail --json
# {"views":423,"unique_countries":88,"view_types":{"page":…},"bot_ratio":0.04}
```

### 9. Health check before a long run

```bash
pura doctor --json
# Exits 0 only if no hard failures.
```

### 10. Multi-account: work vs personal

```bash
pura --profile work auth login
pura --profile personal auth login

pura --profile work ls         # returns work docs
pura --profile personal ls     # returns personal docs
```

---

## Error triage

| `error.code` | What it means | First thing to try |
|---|---|---|
| `unauthorized` | Token missing / expired | `pura auth login` |
| `forbidden` | Signed in but no permission | Check scope; for keys, you may need `docs:write` |
| `not_found` | Slug / key / version doesn't exist | `pura ls` or `pura keys ls` to list what exists |
| `validation` | Bad input — read `hint` | Re-run with fixed flags |
| `rate_limit` | Throttled | Wait `retry_after` seconds, retry |
| `conflict` | Slug taken, race, etc. | Pick a new slug / retry |
| `server_error` | Transient 5xx | Retry with backoff; then `pura doctor` |
| `model_error` | AI returned an error mid-stream | Try different `--model`, broader context |
| `authorization_pending` | Device flow still waiting for user | Keep polling (the CLI does this itself) |
| `slow_down` | Poll faster than `interval` | CLI auto-backoffs; don't override |
| `expired` | Device code / poll window gone | `pura auth login` again |
| `access_denied` | User rejected the device grant | Ask the user to confirm |

---

## Assumptions agents can make

- **Slugs are 8-char opaque strings** like `ax12cd34`. They are globally
  unique within a handle namespace (`@you/ax12cd34`).
- **Tokens always start with `sk_pura_`.** The first 16 chars are a
  safe-to-log prefix; the rest are secret.
- **Anonymous docs live under `@_`.** They get a long edit-token that
  acts as a per-doc password.
- **Version numbers are 1-indexed, monotonic, never reused.** Restores
  create new versions that mirror older ones; nothing is deleted.
- **The server always returns a JSON envelope** — even error pages from
  CF gateways are normalized (status codes still meaningful for exit
  code routing).
- **SSE responses use `data: <json>\n\n`** terminated by `data: [DONE]`.
  The CLI handles heartbeats (`:keep-alive`) and malformed frames
  transparently.

---

## Quick start for a fresh agent

```bash
# 1. Sign in (opens browser; pass --token for CI)
pura auth login

# 2. Publish something
pura push README.md

# 3. The envelope shows the next best moves:
#      pura open <slug>
#      pura chat <slug> "<instruction>"
#      pura versions ls <slug>
```

That's the entire loop. Every other command extends one of these three
verbs (publish, read, iterate) with scope, filter, or observability.

---

*This skill is the canonical reference. If something on the CLI
disagrees with this doc, the CLI is right — and please file a diff.*
