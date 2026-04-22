#!/usr/bin/env sh
# Fails the build if any non-exempt package is below the coverage floor.
#
# Parses one line per Go package from `go test -cover` and compares each
# reported percentage to FLOOR (default 70). Exempt packages are listed
# explicitly — keep the list tiny and justified.
#
# Exempt:
#   cmd/pura  — tiny main, test-by-integration
#   skills    — embed-only; no code paths to cover

set -eu
(set -o pipefail 2>/dev/null) && set -o pipefail

FLOOR="${COVERAGE_FLOOR:-70}"

# Stage two tempfiles under a single cleanup trap: TMP holds the raw
# `go test -cover` output, FAIL_MARKER accumulates packages under the
# floor. FAIL_MARKER has to be a file (not a shell variable) because the
# `while read` loop below runs in a subshell under POSIX sh — variables
# set inside wouldn't survive back to the parent.
TMP=$(mktemp)
FAIL_MARKER=$(mktemp)
trap 'rm -f "$TMP" "$FAIL_MARKER"' EXIT INT TERM
# Truncate (mktemp already created them; this resets to empty explicitly).
: > "$FAIL_MARKER"

go test -cover ./internal/... 2>&1 | tee "$TMP"

grep "coverage:" "$TMP" | while IFS= read -r line; do
	pkg=$(echo "$line" | awk '{print $2}')
	pct=$(echo "$line" | sed -n 's/.*coverage: \([0-9.][0-9.]*\)%.*/\1/p')

	# Exempt list — keep it small and commented.
	case "$pkg" in
		github.com/pura-labs/pura-cli/cmd/pura) continue ;;
		github.com/pura-labs/pura-cli/skills|github.com/pura-labs/pura-cli/skills/*) continue ;;
	esac

	int=$(echo "$pct" | cut -d. -f1)
	if [ "$int" -lt "$FLOOR" ]; then
		printf 'FAIL  %s  %s%% < %s%%\n' "$pkg" "$pct" "$FLOOR"
		echo "$pkg" >> "$FAIL_MARKER"
	fi
done

if [ -s "$FAIL_MARKER" ]; then
	echo ""
	echo "Coverage gate FAILED — at least one package is below $FLOOR%."
	echo "Raise coverage or add the package to the exempt list in scripts/coverage-gate.sh with rationale."
	exit 1
fi
echo "Coverage gate passed (>= $FLOOR% per package, exempting cmd/pura and skills)."
