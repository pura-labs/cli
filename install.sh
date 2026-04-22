#!/usr/bin/env sh
# Pura CLI installer.
#
# Usage:
#   curl -sSL https://get.pura.so/cli | sh
#
# Non-interactive options:
#   PURA_VERSION=v0.2.0          install a specific tag
#   PURA_PREFIX=$HOME/.local/bin install somewhere other than /usr/local/bin
#   PURA_NO_VERIFY=1             skip checksum verification (not recommended)
#
# POSIX sh on purpose — runs on macOS, Linux, Alpine, Docker shells.

set -eu
# Enable pipefail when the shell supports it (bash, zsh, dash >=0.5.4, ash on
# busybox/alpine). Posix sh doesn't guarantee it, so wrap in a subshell probe
# and swallow the error on shells that don't support -o pipefail.
(set -o pipefail 2>/dev/null) && set -o pipefail

# Configuration --------------------------------------------------
GITHUB_OWNER="${PURA_GITHUB_OWNER:-pura-labs}"
GITHUB_REPO="${PURA_GITHUB_REPO:-cli}"
BINARY="pura"
PREFIX="${PURA_PREFIX:-/usr/local/bin}"

# Helpers --------------------------------------------------------
say() { printf '  %s\n' "$*"; }
die() { printf 'pura install: ERROR %s\n' "$*" >&2; exit 1; }

need_cmd() {
	command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed"
}

detect_os() {
	case "$(uname -s)" in
		Darwin*) echo "darwin" ;;
		Linux*)  echo "linux" ;;
		MINGW*|MSYS*|CYGWIN*) echo "windows" ;;
		*) die "unsupported OS: $(uname -s)" ;;
	esac
}

detect_arch() {
	case "$(uname -m)" in
		x86_64|amd64) echo "amd64" ;;
		arm64|aarch64) echo "arm64" ;;
		*) die "unsupported architecture: $(uname -m)" ;;
	esac
}

# Fetch latest release tag via the public GitHub API (no auth).
#
# Two-step resolution so pre-launch / prerelease-only projects still
# DWIM for `curl | sh`:
#
#   1. /releases/latest      ← skips prereleases; returns only `stable` tags
#   2. /releases?per_page=1  ← newest ANY release, prereleases included
#
# A project with nothing but prereleases (our state right now) would 404
# on step 1 and silently fail with "could not resolve latest version".
# Step 2 is the honest fallback: install the newest thing on offer.
#
# Output contract: prints "<tag>" on stdout on success; writes "1" to the
# global `IS_PRERELEASE` file so `main` can annotate the install banner.
# Returns non-zero only when both endpoints fail.
latest_version() {
	latest_url="https://api.github.com/repos/${GITHUB_OWNER}/${GITHUB_REPO}/releases/latest"
	if tag=$(curl -fsSL "$latest_url" 2>/dev/null | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1) && [ -n "$tag" ]; then
		echo "$tag"
		return 0
	fi

	# Fallback: newest release of any kind. The `?per_page=1` trick avoids
	# paging through potentially thousands of historical releases. GitHub's
	# default sort is `published_at` desc — we want that.
	list_url="https://api.github.com/repos/${GITHUB_OWNER}/${GITHUB_REPO}/releases?per_page=1"
	if tag=$(curl -fsSL "$list_url" 2>/dev/null | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1) && [ -n "$tag" ]; then
		printf '1' > "$PRERELEASE_FLAG_FILE" 2>/dev/null || true
		echo "$tag"
		return 0
	fi

	return 1
}

# Main -----------------------------------------------------------
need_cmd curl
need_cmd tar
need_cmd mktemp

OS=$(detect_os)
ARCH=$(detect_arch)

# TMPDIR created first so latest_version can plant the prerelease marker
# into a known path (subshells in command substitution can't mutate
# caller-scope shell vars).
TMPDIR=$(mktemp -d)
PRERELEASE_FLAG_FILE="${TMPDIR}/.prerelease"
trap 'rm -rf "$TMPDIR"' EXIT INT TERM

VERSION="${PURA_VERSION:-}"
if [ -z "$VERSION" ]; then
	say "Resolving latest release…"
	VERSION=$(latest_version || true)
	[ -n "$VERSION" ] || die "could not resolve latest version — set PURA_VERSION"
fi

STRIPPED_VERSION=${VERSION#v}
ARCHIVE_EXT="tar.gz"
[ "$OS" = "windows" ] && ARCHIVE_EXT="zip"
ARCHIVE="${BINARY}_${STRIPPED_VERSION}_${OS}_${ARCH}.${ARCHIVE_EXT}"
BASE_URL="https://github.com/${GITHUB_OWNER}/${GITHUB_REPO}/releases/download/${VERSION}"

# Surface prerelease status honestly — users `curl | sh`ing on a pre-launch
# project deserve to know they're on the bleeding edge, not silently
# assume they got a stable build.
if [ -s "$PRERELEASE_FLAG_FILE" ]; then
	say "Installing ${BINARY} ${VERSION} (pre-release) for ${OS}/${ARCH}"
else
	say "Installing ${BINARY} ${VERSION} for ${OS}/${ARCH}"
fi

cd "$TMPDIR"

say "Downloading ${ARCHIVE}…"
curl -fsSL -o "$ARCHIVE" "${BASE_URL}/${ARCHIVE}" \
	|| die "download failed: ${BASE_URL}/${ARCHIVE}"

# Checksum + (optional) cosign signature verification ----------
if [ "${PURA_NO_VERIFY:-0}" != "1" ]; then
	say "Verifying checksum…"
	curl -fsSL -o checksums.txt "${BASE_URL}/checksums.txt" \
		|| die "could not fetch checksums.txt — pass PURA_NO_VERIFY=1 to skip"

	expected=$(grep "  ${ARCHIVE}$" checksums.txt | awk '{print $1}')
	[ -n "$expected" ] || die "checksum missing for ${ARCHIVE}"

	if command -v shasum >/dev/null 2>&1; then
		actual=$(shasum -a 256 "$ARCHIVE" | awk '{print $1}')
	elif command -v sha256sum >/dev/null 2>&1; then
		actual=$(sha256sum "$ARCHIVE" | awk '{print $1}')
	else
		die "need shasum or sha256sum — install one, or pass PURA_NO_VERIFY=1"
	fi

	[ "$expected" = "$actual" ] || die "checksum mismatch: expected $expected, got $actual"

	# Cosign signature — best-effort. Signing checksums.txt transitively
	# covers every archive; verifying the checksum file is what unlocks
	# supply-chain trust for the whole release.
	#
	# Skip paths (intentional):
	#   - cosign not installed              → not everyone has it; note + skip
	#   - PURA_NO_COSIGN=1                  → explicit opt-out (air-gapped, old Go)
	#   - .sig or .pem missing from release → older release, predates signing
	if [ "${PURA_NO_COSIGN:-0}" != "1" ] && command -v cosign >/dev/null 2>&1; then
		if curl -fsSL -o checksums.txt.sig "${BASE_URL}/checksums.txt.sig" 2>/dev/null \
		   && curl -fsSL -o checksums.txt.pem "${BASE_URL}/checksums.txt.pem" 2>/dev/null; then
			say "Verifying cosign signature…"
			cosign verify-blob \
				--certificate checksums.txt.pem \
				--signature checksums.txt.sig \
				--certificate-identity-regexp "https://github.com/${GITHUB_OWNER}/${GITHUB_REPO}/.*" \
				--certificate-oidc-issuer https://token.actions.githubusercontent.com \
				checksums.txt >/dev/null 2>&1 \
				|| die "cosign signature verification failed — aborting install"
		else
			say "(cosign present but release has no signature yet — skipping)"
		fi
	fi
fi

# Unpack --------------------------------------------------------
say "Unpacking…"
if [ "$ARCHIVE_EXT" = "zip" ]; then
	need_cmd unzip
	unzip -q "$ARCHIVE"
else
	tar -xzf "$ARCHIVE"
fi

BIN_NAME="$BINARY"
[ -f "${BINARY}.exe" ] && BIN_NAME="${BINARY}.exe"

[ -x "$BIN_NAME" ] || die "binary not found after unpack — archive layout may have changed"

# Permission check: ask for sudo once instead of failing silently.
if [ -w "$PREFIX" ]; then
	mv "$BIN_NAME" "$PREFIX/$BIN_NAME"
else
	say "Installing to $PREFIX requires sudo…"
	sudo mv "$BIN_NAME" "$PREFIX/$BIN_NAME"
fi
chmod +x "$PREFIX/$BIN_NAME"

# Verify + onboarding -------------------------------------------
say "Verifying install…"
"$PREFIX/$BIN_NAME" version >/dev/null 2>&1 || say "(binary ran but 'pura version' did not — continue anyway)"

cat <<EOF

  ✓ ${BINARY} installed to ${PREFIX}/${BIN_NAME}

  Try:
    ${BINARY} auth login
    ${BINARY} push <file>
    ${BINARY} --help

  Install the agent skill:
    ${BINARY} skill

EOF
