# pura — CLI for Pura

Publish, read, AI-edit, version, and share content on [pura.so](https://pura.so).
One Go binary. Agent-first. `make check` gates every change.

```
$ pura auth login
$ pura push report.md
$ pura chat <slug> "make the intro friendlier"
$ pura versions ls <slug>
$ pura versions restore <slug> 2 --yes
```

## Install

```bash
curl -sSL https://get.pura.so/cli | sh
```

Or build from source:

```bash
cd cli
go build -o pura ./cmd/pura
./pura version
```

## Documentation

- **Agent guide**: [`skills/pura/SKILL.md`](skills/pura/SKILL.md) — canonical
  reference for AI agents driving the CLI.
- **Architectural plan**: [`../PLAN-CLI.md`](../PLAN-CLI.md) — the living
  spec behind every design decision.
- **Repo agent guide**: [`../AGENTS.md`](../AGENTS.md) — what a new contributor
  (human or AI) should read first.

## Development

```bash
make check           # fmt + vet + test + coverage gate + surface-check
make test-race       # race detector
make e2e             # optional; skipped when wrangler dev isn't running
make surface         # regenerate SURFACE.txt after UX changes
make release-snapshot  # goreleaser dry run → dist/
```

## Exit codes

Scripts should branch on these. Values are contractual and shared with
`SKILL.md`.

| Exit | Meaning |
|---:|---|
| 0 | OK |
| 1 | Generic |
| 2 | Auth (401) |
| 3 | Forbidden (403) |
| 4 | NotFound (404) |
| 5 | Invalid (400) |
| 6 | Conflict (409) |
| 7 | RateLimit (429) |
| 8 | API (5xx, network, parse) |

## License

[MIT](./LICENSE) — see file for the full text.
