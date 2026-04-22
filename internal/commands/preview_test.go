package commands

import (
	"os"
	"testing"
)

func TestPreviewCmd_Exists(t *testing.T) {
	cmd := newPreviewCmd()
	if cmd.Use != "preview <file>" {
		t.Errorf("expected Use to be 'preview <file>', got %q", cmd.Use)
	}
}

func TestPreviewCmd_RequiresArg(t *testing.T) {
	cmd := newPreviewCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when no file provided")
	}
}

func TestPreviewCmd_ShowsFileInfo(t *testing.T) {
	tmpFile := t.TempDir() + "/test.md"
	if err := os.WriteFile(tmpFile, []byte("# Hello\n\nSome content here.\n"), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	cmd := rootCmd
	cmd.SetArgs([]string{"preview", tmpFile})
	if err := cmd.Execute(); err != nil {
		t.Errorf("preview command failed: %v", err)
	}
}

func TestPreviewCmd_EmptyFile(t *testing.T) {
	tmpFile := t.TempDir() + "/empty.md"
	if err := os.WriteFile(tmpFile, []byte(""), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	cmd := rootCmd
	cmd.SetArgs([]string{"preview", tmpFile})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error for empty file")
	}
}

func TestPreviewCmd_FileNotFound(t *testing.T) {
	cmd := rootCmd
	cmd.SetArgs([]string{"preview", "/nonexistent/file.md"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error for missing file")
	}
}
