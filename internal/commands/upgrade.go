package commands

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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

// Overridable in tests to point at an httptest server.
var (
	githubAPIBase      = "https://api.github.com"
	githubDownloadBase = "https://github.com"
)

func normalizeVer(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }

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

			tag := strings.TrimSpace(wantVersion)
			if tag == "" || tag == "latest" {
				resolved, err := latestReleaseTag(cmd.Context())
				if err != nil {
					w.Error("api_error", fmt.Sprintf("could not resolve latest version: %v", err),
						"Check your network, or install manually: curl -sSL https://get.pura.so/cli | sh")
					return err
				}
				tag = resolved
			}
			if !strings.HasPrefix(tag, "v") {
				tag = "v" + tag
			}

			current := normalizeVer(versionStr)
			latest := normalizeVer(tag)

			if check {
				w.OK(map[string]any{"current": current, "latest": latest, "up_to_date": current == latest},
					output.WithSummary("current v%s · latest v%s", current, latest))
				w.Print("  current: v%s\n  latest:  v%s\n", current, latest)
				if current == latest {
					w.Print("  ✓ up to date\n")
				} else {
					w.Print("  → run `pura upgrade`\n")
				}
				return nil
			}

			// "dev" builds (no ldflags) always upgrade — they have no real version.
			if !force && current != "dev" && current == latest {
				w.OK(map[string]any{"current": current, "latest": latest, "up_to_date": true},
					output.WithSummary("Already on the latest version (v%s)", latest))
				w.Print("  ✓ already latest v%s  (use --force to reinstall)\n", latest)
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

// latestReleaseTag asks the public GitHub API for the newest stable release tag.
func latestReleaseTag(ctx context.Context) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", githubAPIBase, upgradeOwner, upgradeRepo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API HTTP %d", resp.StatusCode)
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("no tag_name in release response")
	}
	return rel.TagName, nil
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
