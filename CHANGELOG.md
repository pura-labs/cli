# Changelog

All notable changes to the `pura` CLI. Format loosely follows
[Keep a Changelog](https://keepachangelog.com/); versions are git tags consumed
by goreleaser.

## [0.2.1] - 2026-06-16

### Added
- **`pura upgrade`** (alias `update`) — public self-upgrade. Pulls the release
  for your OS/arch straight from GitHub releases (same source as install.sh),
  verifies sha256 against `checksums.txt`, and atomically replaces the running
  binary. No sign-in. `--check` (report only), `--force` (reinstall when latest),
  `--version vX.Y.Z` (pin / downgrade). Resolves "latest" via the
  `releases/latest` redirect rather than the GitHub API, so it no longer trips
  the unauthenticated API rate limit (HTTP 403); a build already at or ahead of
  the latest release is a no-op (never silently downgrades).

### Changed
- The bundled skill now documents the image host (`pura image`, embed-first
  push, `/library`) and `pura upgrade`, so `pura skill install` ships docs in
  sync with the CLI.
- `pura skill install` confirms before overwriting an existing install
  (prompts on a TTY, requires `--force` non-interactively) — no more silently
  clobbering a hand-edited `SKILL.md`.

## [0.2.0] - 2026-06-15

### Added
- **Embed-first `pura push`.** Pushing a markdown/HTML doc that references local
  images (`![](./pic.png)`, `<img src="/abs/pic.png">`) now uploads each local
  image to your image host first, then rewrites the reference to the canonical
  `https://i.pura.so/…` URL before the doc is created — the published doc is
  self-contained. External `http(s)` URLs are left for the server to rehost;
  `data:` URIs stay inline. `--no-embed` disables it.
- **`pura image` command group** for the per-user image library (图床):
  - `pura image ls [--query <q>] [--limit N]` — your images with `usage_count`.
  - `pura image get <slug>` — URL, dimensions, size, alt, tags.
  - `pura image rm <slug> [--yes] [--force]` — refcount-guarded delete; blocked
    with the consuming docs when still embedded, `--force` overrides.
- `internal/ingest` package: a pure image-reference scanner + offset-anchored
  rewriter (covers markdown inline + HTML `<img src>`).

### Fixed
- **`pura push` now authenticates doc creation.** `client.Create` sent no
  `Authorization` header, so text-doc pushes were treated as anonymous and
  landed under the `@_` namespace instead of your `@handle`. Now sends the
  bearer (anonymous publish still works when no token is set).
- `pura image get/rm` resolve the caller's handle from the server when no
  `--handle`/cached handle is available (a stale cached handle built
  `@wrong/slug` and 404'd).
- Embed only rewrites doc-like substrates and no longer leaks command flag state
  (`--no-embed`) between invocations.
- Missing/oversize/non-image **relative** local images abort the push (no dead
  links) rather than publishing a broken path; absolute non-resolving paths are
  left untouched.

### Notes
- Requires the matching server-side image-host deploy for the library/refcount
  features (`image.list`, refcount-guarded delete, embed rehost).

## [0.1.2]

- Image/file asset upload via `pura push` (base64 → `image.upload`/`file.upload`).
