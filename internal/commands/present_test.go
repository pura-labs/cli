package commands

import (
	"os/exec"
	"strings"
	"testing"
)

func TestPresentURL(t *testing.T) {
	// Intercept openInBrowser so the test doesn't actually open a browser.
	var captured string
	orig := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		// Find the URL arg — it's the one that contains "?present=1".
		for _, a := range args {
			if strings.Contains(a, "?present=1") {
				captured = a
			}
		}
		return exec.Command("true")
	}
	defer func() { execCommand = orig }()

	cmd := newPresentCmd()
	cmd.SetArgs([]string{"test-deck"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.HasSuffix(captured, "/test-deck?present=1") && !strings.Contains(captured, "/test-deck?present=1") {
		t.Errorf("expected URL to end with /test-deck?present=1, got %q", captured)
	}
}
