# Pura CLI

Project guide for `github.com/pura-labs/cli`, the open-source Go CLI for
[pura.so](https://pura.so).

## What This Repo Ships

- One binary: `pura`
- Cobra command tree under `internal/commands`
- HTTP + SSE client under `internal/api`
- Release packaging via `.goreleaser.yaml` and `.github/workflows/`
- Shell completions under `completions/`
- Agent-facing usage guide under `skills/pura/SKILL.md`

## Build And Verify

```bash
go build -o pura ./cmd/pura
go test ./...
make check
```

Useful extra targets:

```bash
make test-race
make surface
make release-snapshot
./scripts/gen-completions.sh
UPDATE_GOLDEN=1 go test ./internal/commands/... -run TestHelpGolden
UPDATE_CONTRACTS=1 go test -tags=contract ./internal/api/...
```

## High-Signal Files

- `cmd/pura/main.go`: CLI entrypoint and version wiring
- `internal/commands/`: user-facing command implementations
- `internal/api/`: API client, SSE parsing, wire types
- `internal/output/`: envelopes, exit-code mapping, terminal output
- `internal/auth/`: local credential store
- `SURFACE.txt`: checked-in command/flag surface snapshot
- `install.sh`: curl installer for release archives
- `.goreleaser.yaml`: archive, checksum, signing, SBOM, GitHub release config
- `.github/workflows/ci.yml`: CI gate
- `.github/workflows/release.yml`: tagged release flow

## Repo Conventions

- Keep the module path and repo name aligned with `github.com/pura-labs/cli`.
- If commands or flags change, update all three:
  - `SURFACE.txt`
  - shell completions in `completions/`
  - help goldens in `internal/commands/testdata/golden/help/`
- If release behavior changes, review the same change across:
  - `.goreleaser.yaml`
  - `.github/workflows/release.yml`
  - `install.sh`
  - `README.md`
- `package.json` is only lightweight tooling support. `node_modules/` and
  `package-lock.json` are local-only in this repo.

## Open-Source Hygiene

- Do not commit local binaries, `dist/`, coverage output, editor state, or
  ad-hoc temp files.
- Do not commit real credentials, API keys, `.env*`, or a copied
  `~/.config/pura/credentials.json`.
- E2E and contract tests rely on env vars such as `PURA_E2E_URL`,
  `PURA_E2E_TOKEN`, and `UPDATE_CONTRACTS`; keep those values out of tracked
  files.
- Test fixtures may use obviously fake `sk_pura_...` examples. Never replace
  them with real tokens.

## Before You Cut A Release

- Run `make check`
- Run `make release-snapshot`
- Confirm `SURFACE.txt` and completions are current
- Confirm install path still matches the GitHub repo and release asset names
- Confirm no ignored local artifacts were force-added to git
