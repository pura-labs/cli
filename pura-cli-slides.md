<style>
:root {
  --bg: oklch(0.10 0.02 260);
  --bg-subtle: oklch(0.14 0.02 260);
  --surface: oklch(0.18 0.02 260);
  --border: oklch(0.25 0.02 260);
  --text: oklch(0.95 0.00 0);
  --text-muted: oklch(0.60 0.00 0);
  --accent: oklch(0.75 0.15 330);
  --success: oklch(0.75 0.15 145);
  --warning: oklch(0.80 0.15 85);
  --error: oklch(0.70 0.20 25);
  --info: oklch(0.72 0.13 250);
}
</style>

# Pura CLI

<p class="subtitle">The Primitive Layer Agents Work In</p>

One Go binary. Five primitives. Infinite workflows.

---

## What is Pura?

- **Doc** · Markdown prose shipped as a permanent URL
- **Sheet** · Typed rows (CSV/JSON) with schemas & forms
- **Slides** · One HTML deck, each slide a `<section>`
- **Canvas** · SVG / Canvas JS / ASCII — diffable visuals
- **Page** · Single-file HTML served from its own subdomain

Everything flows through `POST /api/tool/:name`. One audit log. One identity.

---

## Install & Auth

```bash
# One-line install
curl -sSL https://get.pura.so/cli | sh

# Sign in (device flow; opens browser)
pura auth login

# Or CI bypass
pura auth login --token sk_pura_...

# Verify
pura auth status --verify
```

---

## The Core Loop

1. **Publish** — `pura push <file>`
2. **Read / Share** — `pura get <slug>` or `pura open <slug>`
3. **Iterate** — `pura chat <slug> "..."`

Every mutation is a **proposal** in your inbox. Auto-accepted in TTY. Preview with `--dry-run`.

---

## Scenario 1: Publish a Report

```bash
# From file
pura push report.md --title "Q4 Review"

# From stdin
cat report.md | pura push --stdin --substrate markdown --title "Q4 Review"

# From a snippet
echo "# Hello World" | pura push --stdin --substrate markdown

# Get the URL
pura push report.md --json --jq .data.url | tr -d '"'
```

---

## Scenario 2: AI Edit

```bash
# Make it friendlier
pura chat ax12cd "make the intro friendlier; keep it under 80 words"

# Preview without touching history
pura chat ax12cd "rewrite as bullet points" --dry-run

# Scope to a selection
pura chat ax12cd "simplify this" --selection "complex paragraph here"

# Interactive diff + confirm
pura chat ax12cd "update tone" --interactive
```

---

## Scenario 3: Version Control

```bash
# See history
pura versions ls ax12cd

# View a specific version
pura versions show ax12cd 2

# Diff two versions
pura versions diff ax12cd 2 latest

# Rollback
pura versions restore ax12cd 2 --yes
```

Versions are 1-indexed, monotonic, never reused. Restores create new versions.

---

## Scenario 4: CI / Bot Integration

```bash
# Create a scoped key
pura keys create --name "ci:github-actions" \
  --scope docs:read --scope docs:write \
  --json --jq .data.token | tr -d '"'

# Use in CI
PURA_TOKEN=$PURA_TOKEN pura push CHANGELOG.md \
  --title "$GITHUB_SHA"
```

Never commit credentials. `~/.config/pura/credentials.json` is mode 0600.

---

## Scenario 5: Anonymous → Claim

```bash
# Publish anonymously (before login)
TOKEN=$(pura push rough.md --json --jq '.data.token' | tr -d '"')

# Later, sign up and claim
pura auth login
pura claim "$TOKEN"
pura ls  # now under @you
```

---

## Global Flags & Output

```
--json          # Force JSON envelope
--jq "<expr>"   # Filter with built-in gojq
--quiet         # Raw data only
--profile <n>   # Switch account (work vs personal)
--token <t>     # One-shot override
--verbose       # HTTP trace to stderr
--api-url <u>   # Point at a different Pura instance
```

Every command emits an envelope with `ok`, `data`, `summary`, and `breadcrumbs`.

---

## Exit Codes (Scriptable)

| Code | Meaning | Fix |
|:---:|:---|:---|
| 0 | OK | — |
| 2 | Auth (401) | `pura auth login` |
| 3 | Forbidden (403) | Check scope |
| 4 | NotFound (404) | `pura ls` |
| 5 | Invalid (400) | Read the hint |
| 6 | Conflict (409) | Retry / new slug |
| 7 | RateLimit (429) | Wait retry_after |
| 8 | API (5xx) | Retry, then `pura doctor` |

---

## Decision Tree

```
File on disk?    → pura push <file>
Piped stdin?     → ... | pura push --stdin --substrate <m>
Describe idea?   → pura new --describe "..."
Full rewrite?    → pura edit <slug> --file <new.md>
Small AI tweak?  → pura chat <slug> "..."
Preview only?    → pura chat ... --dry-run
See history?     → pura versions ls <slug>
Go back?         → pura versions restore <slug> <N> --yes
Stats?           → pura stats <slug> --detail
Live tail?       → pura events <slug> --follow
```

---

## Quick Start

```bash
# 1. Sign in
pura auth login

# 2. Publish
pura push README.md

# 3. Iterate
pura chat <slug> "make it shine"

# 4. Share
pura open <slug>
```

That's the entire loop. Ship docs, sheets, slides, canvas, and pages — all from the terminal.
