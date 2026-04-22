package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pura-labs/cli/internal/detect"
	"github.com/spf13/cobra"
)

func newPreviewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "preview <file>",
		Short: "Preview a file before publishing",
		Long: `Preview a file locally to check its content before publishing.

Reads the file, detects its type, and shows a summary.
For full rendered preview, use: pura push <file> --open`,
		Example: `  pura preview report.md
  pura preview data.csv
  pura preview chart.svg`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			filename := args[0]

			data, err := os.ReadFile(filename)
			if err != nil {
				w.Error("read_error", fmt.Sprintf("Cannot read file: %s", filename), "Check the file path")
				return err
			}

			content := string(data)
			if len(content) == 0 {
				w.Error("validation", "File is empty", "Provide a non-empty file")
				return fmt.Errorf("empty file")
			}

			docType := detect.Type(filename, content)
			absPath := filename
			if !filepath.IsAbs(filename) {
				wd, _ := os.Getwd()
				absPath = filepath.Join(wd, filename)
			}

			info, err := os.Stat(filename)
			if err != nil {
				w.Error("read_error", err.Error(), "")
				return err
			}

			lines := 1
			for _, ch := range content {
				if ch == '\n' {
					lines++
				}
			}

			result := map[string]any{
				"file":  absPath,
				"type":  docType,
				"size":  info.Size(),
				"lines": lines,
			}

			w.OK(result)
			w.Print("  Preview\n")
			w.Print("  ───────\n\n")
			w.Print("  File:      %s\n", absPath)
			w.Print("  Substrate: %s\n", docType)
			w.Print("  Size:    %d bytes\n", info.Size())
			w.Print("  Lines:   %d\n", lines)
			w.Print("\n")

			// Show first few lines as a preview snippet
			previewLines := 10
			shown := 0
			start := 0
			for i, ch := range content {
				if ch == '\n' {
					if shown < previewLines {
						w.Print("  | %s\n", content[start:i])
						shown++
					}
					start = i + 1
				}
			}
			// Handle last line without trailing newline
			if start < len(content) && shown < previewLines {
				w.Print("  | %s\n", content[start:])
				shown++
			}

			if lines > previewLines {
				w.Print("  | ... (%d more lines)\n", lines-previewLines)
			}

			w.Print("\n")
			w.Print("  To publish: pura push %s\n", filename)
			w.Print("  To publish and open: pura push %s --open\n", filename)

			return nil
		},
	}
}
