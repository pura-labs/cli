package commands

import (
	"os/exec"
	"runtime"
	"testing"
)

func TestOpenCmd_Exists(t *testing.T) {
	cmd := newOpenCmd()
	if cmd.Use != "open <slug>" {
		t.Errorf("expected Use to be 'open <slug>', got %q", cmd.Use)
	}
}

func TestOpenCmd_RequiresArg(t *testing.T) {
	cmd := newOpenCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when no slug provided")
	}
}

func TestOpenCmd_AcceptsSlug(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	// Override execCommand so we don't actually open a browser
	origExec := execCommand
	defer func() { execCommand = origExec }()

	var gotName string
	var gotArgs []string
	execCommand = func(name string, args ...string) *exec.Cmd {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return exec.Command("echo", "noop")
	}

	cmd := rootCmd
	cmd.SetArgs([]string{"open", "test-slug", "--api-url", "http://localhost:9999/api", "--handle", "@alice"})
	// The command may produce output but should accept the slug argument
	_ = cmd.Execute()

	if gotName == "" || len(gotArgs) == 0 {
		t.Fatal("expected open command to invoke browser launcher")
	}
	if got := gotArgs[len(gotArgs)-1]; got != "http://localhost:9999/@alice/test-slug" {
		t.Fatalf("url = %q, want http://localhost:9999/@alice/test-slug", got)
	}
	if runtime.GOOS == "darwin" && gotName != "open" {
		t.Fatalf("launcher = %q, want open", gotName)
	}
}
