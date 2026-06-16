package commands

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

// pura is a public CLI: upgrade pulls straight from the public GitHub releases
// (same source as install.sh) — no token, no server round-trip. sha256-verified
// against checksums.txt, then the running binary is atomically replaced.
const (
	upgradeOwner   = "pura-labs"
	upgradeRepo    = "cli"
	upgradeBinary  = "pura"
	maxUpgradeSize = 200 * 1024 * 1024
)

// Overridable in tests to point at an httptest server. We resolve the latest
// tag from this base too (via the /releases/latest redirect), so a public
// upgrade never touches api.github.com — that endpoint's 60-req/hr
// unauthenticated rate limit is the usual cause of an HTTP 403 mid-session.
var githubDownloadBase = "https://github.com"

func normalizeVer(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }

// semverParts splits "1.2.3" (or "1.2.3-rc1") into [3]int{1,2,3}; missing or
// non-numeric components read as 0. Good enough to compare release tags.
func semverParts(v string) [3]int {
	var out [3]int
	for i, f := range strings.SplitN(normalizeVer(v), ".", 3) {
		if i > 2 {
			break
		}
		end := 0
		for end < len(f) && f[end] >= '0' && f[end] <= '9' {
			end++
		}
		out[i], _ = strconv.Atoi(f[:end])
	}
	return out
}

// compareSemver returns -1 if a<b, 0 if equal, +1 if a>b.
func compareSemver(a, b string) int {
	pa, pb := semverParts(a), semverParts(b)
	for i := 0; i < 3; i++ {
		switch {
		case pa[i] < pb[i]:
			return -1
		case pa[i] > pb[i]:
			return 1
		}
	}
	return 0
}

func newUpgradeCmd() *cobra.Command {
	var (
		check       bool
		force       bool
		wantVersion string
		binaryPath  string // hidden; overrides the replace target (tests)
	)
	cmd := &cobra.Command{
		Use:     "upgrade",
		Aliases: []string{"update"},
		Short:   "Upgrade the pura CLI to the latest release",
		Long: `Upgrade pura to the latest GitHub release (public; no sign-in needed).
Downloads the release for your OS/arch, verifies its sha256 against
checksums.txt, and atomically replaces the running binary.

  pura upgrade                  # upgrade to latest (no-op if already current)
  pura upgrade --check          # only report whether a newer version exists
  pura upgrade --force          # reinstall even if already latest
  pura upgrade --version v0.2.0 # install a specific version (incl. downgrade)`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := newWriter()

			// An explicit --version pins a tag and may intentionally downgrade;
			// the bare `pura upgrade` resolves "latest" and must never downgrade.
			explicitVersion := false
			tag := strings.TrimSpace(wantVersion)
			if tag == "" || tag == "latest" {
				resolved, err := latestReleaseTag(cmd.Context())
				if err != nil {
					w.Error("api_error", fmt.Sprintf("could not resolve latest version: %v", err),
						"Check your network, or install manually: curl -sSL https://get.pura.so/cli | sh")
					return err
				}
				tag = resolved
			} else {
				explicitVersion = true
			}
			if !strings.HasPrefix(tag, "v") {
				tag = "v" + tag
			}

			current := normalizeVer(versionStr)
			latest := normalizeVer(tag)
			// "dev" builds (no ldflags) have no real version → always behind.
			cmp := -1
			if current != "dev" {
				cmp = compareSemver(current, latest)
			}

			if check {
				// "up to date" for scripting = at or ahead of latest (no upgrade needed).
				w.OK(map[string]any{"current": current, "latest": latest, "up_to_date": cmp >= 0},
					output.WithSummary("current v%s · latest v%s", current, latest))
				w.Print("  current: v%s\n  latest:  v%s\n", current, latest)
				switch {
				case current == "dev":
					w.Print("  → dev build; run `pura upgrade` to install v%s\n", latest)
				case cmp == 0:
					w.Print("  ✓ up to date\n")
				case cmp > 0:
					w.Print("  ✓ ahead of the latest release (local / pre-release build)\n")
				default:
					w.Print("  → run `pura upgrade`\n")
				}
				return nil
			}

			// Default upgrade never downgrades: no-op when already at/ahead of
			// latest. --version (explicit) and --force bypass this.
			if !force && !explicitVersion && cmp >= 0 {
				w.OK(map[string]any{"current": current, "latest": latest, "up_to_date": true},
					output.WithSummary("Already on v%s (latest release is v%s)", current, latest))
				if cmp > 0 {
					w.Print("  ✓ v%s is ahead of the latest release v%s — nothing to do\n", current, latest)
				} else {
					w.Print("  ✓ already latest v%s  (use --force to reinstall)\n", latest)
				}
				return nil
			}

			archive := fmt.Sprintf("%s_%s_%s_%s.tar.gz", upgradeBinary, latest, runtime.GOOS, runtime.GOARCH)
			base := fmt.Sprintf("%s/%s/%s/releases/download/%s", githubDownloadBase, upgradeOwner, upgradeRepo, tag)

			fmt.Fprintf(w.Err, "Downloading %s …\n", archive)
			tarPath, err := downloadAndVerifyUpgrade(cmd.Context(), base, archive)
			if err != nil {
				w.Error("api_error", err.Error(),
					"Install manually if this persists: curl -sSL https://get.pura.so/cli | sh")
				return err
			}
			defer func() { _ = os.Remove(tarPath) }()

			target := binaryPath
			if target == "" {
				target, err = currentExePath()
				if err != nil {
					w.Error("api_error", fmt.Sprintf("resolve current executable: %v", err), "")
					return err
				}
			}

			if err := installUpgradeBinary(tarPath, target); err != nil {
				if errors.Is(err, os.ErrPermission) {
					w.Error("forbidden", fmt.Sprintf("no write permission for %s", target),
						"Re-run with sudo, or reinstall: curl -sSL https://get.pura.so/cli | sh")
					return err
				}
				w.Error("api_error", err.Error(), "")
				return err
			}

			w.OK(map[string]any{"from": current, "to": latest, "path": target},
				output.WithSummary("Upgraded pura v%s → v%s", current, latest),
				output.WithBreadcrumb("verify", "pura version", "Confirm the new version"))
			w.Print("  ✓ pura v%s → v%s\n  %s\n", current, latest, target)
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "Only check for a newer version; don't install")
	cmd.Flags().BoolVar(&force, "force", false, "Reinstall even if already on the latest version")
	cmd.Flags().StringVar(&wantVersion, "version", "", "Target version (vX.Y.Z); defaults to latest")
	cmd.Flags().StringVar(&binaryPath, "binary-path", "", "Override the binary to replace (testing)")
	_ = cmd.Flags().MarkHidden("binary-path")
	return cmd
}

// latestReleaseTag resolves the newest release tag WITHOUT hitting the GitHub
// API. `github.com/<owner>/<repo>/releases/latest` answers any unauthenticated
// request with a 302 → `.../releases/tag/<tag>`; reading that Location avoids
// the api.github.com 60/hr rate limit that surfaces as HTTP 403.
func latestReleaseTag(ctx context.Context) (string, error) {
	url := fmt.Sprintf("%s/%s/%s/releases/latest", githubDownloadBase, upgradeOwner, upgradeRepo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{
		Timeout: 15 * time.Second,
		// Don't follow the redirect — we only want the Location header.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("no release redirect (HTTP %d) — repo may have no releases yet", resp.StatusCode)
	}
	const marker = "/releases/tag/"
	idx := strings.LastIndex(loc, marker)
	if idx < 0 {
		return "", fmt.Errorf("unexpected redirect target %q", loc)
	}
	tag := strings.Trim(loc[idx+len(marker):], "/")
	if tag == "" {
		return "", fmt.Errorf("empty tag in redirect %q", loc)
	}
	return tag, nil
}

// downloadAndVerifyUpgrade fetches the archive + checksums.txt and verifies sha256.
func downloadAndVerifyUpgrade(ctx context.Context, base, archive string) (string, error) {
	want, err := fetchChecksum(ctx, base+"/checksums.txt", archive)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/"+archive, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download HTTP %d for %s", resp.StatusCode, archive)
	}

	tmp, err := os.CreateTemp("", "pura-upgrade-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer tmp.Close()

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hasher), io.LimitReader(resp.Body, maxUpgradeSize)); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	if got != want {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("sha256 mismatch for %s: expected %s got %s", archive, want, got)
	}
	return filepath.Clean(tmp.Name()), nil
}

func fetchChecksum(ctx context.Context, url, archive string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("could not fetch checksums.txt (HTTP %d)", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	// Lines are "<sha256>  <archive>" (two spaces, goreleaser default).
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == archive {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksum missing for %s", archive)
}

func currentExePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe = filepath.Clean(exe)
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return filepath.Clean(resolved), nil
	}
	return exe, nil
}

// installUpgradeBinary extracts the pura binary from the archive into a temp file
// in the target's directory, then atomically renames it over the target.
func installUpgradeBinary(archivePath, targetPath string) error {
	targetPath = filepath.Clean(targetPath)
	tmp, err := os.CreateTemp(filepath.Dir(targetPath), ".pura-upgrade-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := extractUpgradeBinary(archivePath, tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func extractUpgradeBinary(archivePath string, dst *os.File) error {
	archive, err := os.Open(archivePath) // #nosec G304 -- verified temp download
	if err != nil {
		return err
	}
	defer archive.Close()

	gz, err := gzip.NewReader(archive)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("archive does not contain the %s binary", upgradeBinary)
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(filepath.Clean(hdr.Name)) != upgradeBinary {
			continue
		}
		if hdr.Size <= 0 || hdr.Size > maxUpgradeSize {
			return fmt.Errorf("invalid %s binary size: %d", upgradeBinary, hdr.Size)
		}
		if _, err := io.CopyN(dst, tr, hdr.Size); err != nil {
			return err
		}
		return nil
	}
}
