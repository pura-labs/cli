package commands

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func makePuraTarGz(t *testing.T, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "pura", Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// upgradeServer serves the GitHub release JSON + archive + checksums.txt for the
// current OS/arch. badChecksum corrupts the published sha to test verification.
func upgradeServer(t *testing.T, tag string, tgz []byte, badChecksum bool) *httptest.Server {
	t.Helper()
	archive := fmt.Sprintf("pura_%s_%s_%s.tar.gz", normalizeVer(tag), runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(tgz)
	hexsum := hex.EncodeToString(sum[:])
	if badChecksum {
		hexsum = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/pura-labs/cli/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"tag_name":%q}`, tag)
	})
	dl := fmt.Sprintf("/pura-labs/cli/releases/download/%s/", tag)
	mux.HandleFunc(dl+archive, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(tgz) })
	mux.HandleFunc(dl+"checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", hexsum, archive)
	})
	return httptest.NewServer(mux)
}

func pointUpgradeAt(t *testing.T, srv *httptest.Server) {
	t.Helper()
	oa, od := githubAPIBase, githubDownloadBase
	githubAPIBase, githubDownloadBase = srv.URL, srv.URL
	t.Cleanup(func() { githubAPIBase, githubDownloadBase = oa, od })
}

func writeTarget(t *testing.T) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "pura")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	return target
}

func TestUpgrade_InstallsBinary(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	newBin := []byte("NEW-PURA-BINARY-v9.9.9")
	srv := upgradeServer(t, "v9.9.9", makePuraTarGz(t, newBin), false)
	defer srv.Close()
	pointUpgradeAt(t, srv)

	target := writeTarget(t)
	cmd := rootCmd
	cmd.SetArgs([]string{"upgrade", "--binary-path", target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade: %v", err)
	}

	got, _ := os.ReadFile(target)
	if string(got) != string(newBin) {
		t.Fatalf("binary not replaced: %q", got)
	}
	fi, _ := os.Stat(target)
	if fi.Mode()&0o111 == 0 {
		t.Fatal("replaced binary is not executable")
	}
}

func TestUpgrade_CheckDoesNotInstall(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	srv := upgradeServer(t, "v9.9.9", makePuraTarGz(t, []byte("x")), false)
	defer srv.Close()
	pointUpgradeAt(t, srv)

	target := writeTarget(t)
	cmd := rootCmd
	cmd.SetArgs([]string{"upgrade", "--check", "--binary-path", target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("check: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "OLD" {
		t.Fatal("--check must not replace the binary")
	}
}

func TestUpgrade_AlreadyLatestNoOp(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	old := versionStr
	versionStr = "9.9.9"
	defer func() { versionStr = old }()

	srv := upgradeServer(t, "v9.9.9", makePuraTarGz(t, []byte("x")), false)
	defer srv.Close()
	pointUpgradeAt(t, srv)

	target := writeTarget(t)
	cmd := rootCmd
	cmd.SetArgs([]string{"upgrade", "--binary-path", target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "OLD" {
		t.Fatal("already-latest must be a no-op without --force")
	}
}

func TestUpgrade_ForceReinstallsWhenLatest(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	old := versionStr
	versionStr = "9.9.9"
	defer func() { versionStr = old }()

	newBin := []byte("REINSTALLED")
	srv := upgradeServer(t, "v9.9.9", makePuraTarGz(t, newBin), false)
	defer srv.Close()
	pointUpgradeAt(t, srv)

	target := writeTarget(t)
	cmd := rootCmd
	cmd.SetArgs([]string{"upgrade", "--force", "--binary-path", target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade --force: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != string(newBin) {
		t.Fatal("--force must reinstall even when already latest")
	}
}

func TestUpgrade_ChecksumMismatchAborts(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	srv := upgradeServer(t, "v9.9.9", makePuraTarGz(t, []byte("x")), true)
	defer srv.Close()
	pointUpgradeAt(t, srv)

	target := writeTarget(t)
	cmd := rootCmd
	cmd.SetArgs([]string{"upgrade", "--binary-path", target})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected a checksum-mismatch error")
	}
	if got, _ := os.ReadFile(target); string(got) != "OLD" {
		t.Fatal("binary must NOT be replaced on checksum mismatch")
	}
}

func TestUpgrade_SpecificVersion(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	newBin := []byte("v0.1.5-rollback")
	srv := upgradeServer(t, "v0.1.5", makePuraTarGz(t, newBin), false)
	defer srv.Close()
	pointUpgradeAt(t, srv)

	target := writeTarget(t)
	cmd := rootCmd
	cmd.SetArgs([]string{"upgrade", "--version", "v0.1.5", "--binary-path", target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade --version: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != string(newBin) {
		t.Fatalf("specific version not installed: %q", got)
	}
}
