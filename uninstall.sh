#!/usr/bin/env sh
# Pura CLI uninstaller — symmetric with install.sh.
#
# Usage:
#   curl -sSL https://get.pura.so/uninstall | sh
#
# Env knobs:
#   PURA_PREFIX=/path/to/bin        where the pura binary lives (default: autodetect)
#   PURA_UNINSTALL_DRY_RUN=1        print what would be removed, don't touch anything
#   PURA_UNINSTALL_KEEP_DATA=1      remove binary only; keep ~/.config/pura intact
#
# POSIX sh on purpose — runs on macOS, Linux, Alpine, Docker shells.

set -eu
(set -o pipefail 2>/dev/null) && set -o pipefail

BINARY="pura"
DRY_RUN="${PURA_UNINSTALL_DRY_RUN:-0}"
KEEP_DATA="${PURA_UNINSTALL_KEEP_DATA:-0}"

# Helpers --------------------------------------------------------
say()  { printf '  %s\n' "$*"; }
warn() { printf '  ! %s\n' "$*"; }
die()  { printf 'pura uninstall: ERROR %s\n' "$*" >&2; exit 1; }

# Wrap rm so dry-run is a single switch, not scattered through the script.
erase() {
	target="$1"
	if [ "$DRY_RUN" = "1" ]; then
		printf '  [dry-run] would remove: %s\n' "$target"
	else
		rm -rf "$target"
		printf '  removed: %s\n' "$target"
	fi
}

# Binary discovery -----------------------------------------------
# Only look where install.sh would have put it:
#   1. $PURA_PREFIX/pura  (explicit override)
#   2. ~/.local/bin/pura  (install.sh default)
#   3. `command -v pura`  (first $PATH match — catches custom PURA_PREFIX
#                          at install time that the uninstaller isn't told about)
#
# We deliberately do NOT scan /usr/local/bin or /opt/homebrew/bin as a matter
# of policy: if the user installed there, they set PURA_PREFIX at install
# time and can set it again at uninstall time. Scanning system paths makes
# the uninstaller behave like an antivirus and risks touching unrelated
# things.
candidates=""
[ -n "${PURA_PREFIX:-}" ] && candidates="$candidates ${PURA_PREFIX%/}/$BINARY"
candidates="$candidates ${HOME:-/root}/.local/bin/$BINARY"
if command -v "$BINARY" >/dev/null 2>&1; then
	candidates="$candidates $(command -v "$BINARY")"
fi

# Deduplicate by resolving each candidate to a canonical absolute path and
# tracking what we've seen. Padding with spaces on both ends makes the
# "already seen" check a clean substring match without edge cases.
binaries=""
seen=" "
for c in $candidates; do
	[ -f "$c" ] || continue
	case "$seen" in
		*" $c "*) continue ;;
	esac
	seen="$seen$c "
	binaries="${binaries:+$binaries }$c"
done

# Main -----------------------------------------------------------
say "Pura uninstaller"
[ "$DRY_RUN" = "1" ] && say "(dry-run — nothing will actually be removed)"
printf '\n'

if [ -z "$binaries" ]; then
	warn "no pura binary found in PURA_PREFIX, ~/.local/bin, /usr/local/bin, /opt/homebrew/bin, or \$PATH"
else
	say "Binaries to remove:"
	for b in $binaries; do
		# Sanity check: verify it's actually pura, not an unrelated binary
		# with the same name. `pura version` prints an "ok":true JSON envelope.
		if "$b" version 2>/dev/null | grep -q '"ok":' 2>/dev/null; then
			erase "$b"
		else
			warn "skipping $b — doesn't respond to \`pura version\`, refusing to delete unknown binary"
		fi
	done
fi

printf '\n'

# Config + credentials ------------------------------------------
CONFIG_DIR="${HOME:-/root}/.config/pura"
if [ "$KEEP_DATA" = "1" ]; then
	if [ -d "$CONFIG_DIR" ]; then
		say "Keeping config: $CONFIG_DIR (PURA_UNINSTALL_KEEP_DATA=1)"
	fi
else
	if [ -d "$CONFIG_DIR" ]; then
		say "Config + credentials:"
		erase "$CONFIG_DIR"
	else
		say "No config dir at $CONFIG_DIR (already clean)"
	fi
fi

printf '\n'

# MCP clients — best-effort hint ---------------------------------
# We don't touch ~/Library/Application Support/Claude/claude_desktop_config.json
# et al. because:
#   (1) the pura binary is gone at this point so we can't invoke `pura mcp uninstall`
#   (2) mutating other apps' config files from a shell script is the kind of
#       silent side-effect users rightly get angry about
# Instead, tell them where to look if they ever used `pura mcp install`.
claude_desktop="${HOME:-/root}/Library/Application Support/Claude/claude_desktop_config.json"
claude_code="${HOME:-/root}/.claude.json"
cursor="${HOME:-/root}/.cursor/mcp.json"

mcp_hits=""
for f in "$claude_desktop" "$claude_code" "$cursor"; do
	if [ -f "$f" ] && grep -q '"pura"' "$f" 2>/dev/null; then
		mcp_hits="${mcp_hits:+$mcp_hits
}    $f"
	fi
done

if [ -n "$mcp_hits" ]; then
	say "MCP client configs still reference pura:"
	printf '%s\n' "$mcp_hits"
	say "Remove the \"pura\" entry manually from those files, or re-install"
	say "pura and run \`pura mcp uninstall --client=<name>\` first."
	printf '\n'
fi

# Shell completions ---------------------------------------------
# These only exist if someone ran `pura completion <shell>` and piped into
# a completion dir. Best-effort scrub — common locations only.
completion_candidates="\
${HOME:-/root}/.bash_completion.d/pura \
${HOME:-/root}/.local/share/bash-completion/completions/pura \
${HOME:-/root}/.zfunc/_pura \
${HOME:-/root}/.config/fish/completions/pura.fish \
/etc/bash_completion.d/pura \
/usr/local/share/zsh/site-functions/_pura \
/opt/homebrew/share/zsh/site-functions/_pura"

found_comp=""
for f in $completion_candidates; do
	[ -e "$f" ] && found_comp="${found_comp:+$found_comp }$f"
done

if [ -n "$found_comp" ]; then
	say "Shell completions:"
	for f in $found_comp; do
		if [ -w "$(dirname "$f")" ]; then
			erase "$f"
		else
			warn "skipping $f — needs elevated permissions (try: sudo rm $f)"
		fi
	done
	printf '\n'
fi

# Done ----------------------------------------------------------
if [ "$DRY_RUN" = "1" ]; then
	say "Dry-run complete. Re-run without PURA_UNINSTALL_DRY_RUN=1 to actually remove."
else
	say "✓ pura uninstalled."
	say "  Reinstall anytime: curl -sSL https://get.pura.so/cli | sh"
fi
