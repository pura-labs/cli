package commands

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pura-labs/cli/internal/api"
	"github.com/pura-labs/cli/internal/auth"
	"github.com/pura-labs/cli/internal/config"
	"github.com/pura-labs/cli/internal/detect"
	"github.com/pura-labs/cli/internal/ingest"
	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

// Client-side cap before uploading a local image referenced by a doc. The
// server enforces its own (5 MiB auth) limit; failing fast here is cheaper.
const maxEmbedImageBytes = 10 << 20

func newPushCmd() *cobra.Command {
	var (
		flagTitle     string
		flagSubstrate string
		flagKind      string
		flagTheme     string
		flagStdin     bool
		flagOpen      bool
		flagNoEmbed   bool
	)

	cmd := &cobra.Command{
		Use:   "push [file]",
		Short: "Publish a document",
		Long:  "Create and publish a new document. Reads from file or stdin.",
		Example: `  pura push report.md
  pura push data.csv --title "Q1 Data"
  echo "# Hello" | pura push --stdin
  cat api.json | pura push --stdin --substrate json
  pura push rows.csv --kind sheet`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()

			var content string
			var filename string

			if flagStdin || len(args) == 0 {
				data, err := io.ReadAll(os.Stdin)
				if err != nil {
					w.Error("read_error", "Failed to read stdin", "")
					return err
				}
				content = string(data)
			} else {
				filename = args[0]
				data, err := os.ReadFile(filename)
				if err != nil {
					w.Error("read_error", fmt.Sprintf("Cannot read file: %s", filename), "Check the file path")
					return err
				}
				if assetKind := detectAssetKind(filename, flagKind, flagSubstrate); assetKind != "" {
					return pushAsset(cmd, w, cfg, filename, data, flagTitle, flagOpen, assetKind)
				}
				content = string(data)
			}

			content = strings.TrimSpace(content)
			if len(content) == 0 {
				w.Error("validation", "Content is empty", "Provide non-empty content")
				return fmt.Errorf("empty content")
			}

			// Embed-first: upload local image references to the user's host and
			// rewrite them to host URLs before publishing, so the doc is
			// self-contained. External http(s) URLs are left for the server to
			// rehost. --no-embed opts out.
			if !flagNoEmbed {
				docDir, _ := os.Getwd()
				if filename != "" {
					docDir = filepath.Dir(absPath(filename))
				}
				rewritten, err := embedLocalImages(cmd, w, cfg, docDir, content)
				if err != nil {
					return err
				}
				content = rewritten
			}

			// --substrate wins over auto-detect; --kind is a separate signal
			// the server uses to pick the in-family substrate when
			// --substrate is absent. detect.Type() returns a substrate string
			// by filename+content.
			docSubstrate := flagSubstrate
			if docSubstrate == "" && flagKind == "" {
				docSubstrate = detect.Type(filename, content)
			}

			client := newClient(cmd, cfg)
			resp, err := client.Create(api.CreateRequest{
				Content:   content,
				Kind:      flagKind,
				Substrate: docSubstrate,
				Title:     flagTitle,
				Theme:     flagTheme,
			})
			if err != nil {
				w.Error("api_error", err.Error(), "")
				return err
			}

			publishedURL := resp.URL
			if publishedURL == "" {
				publishedURL = client.DocumentURL(resp.Slug)
			}

			// Auto-save token if not already configured (anon publish flow).
			if cfg.Token == "" {
				if err := auth.NewStore().SetToken(resolvedProfile(cfg), resp.Token); err != nil {
					fmt.Fprintf(w.Err, "Warning: failed to save token: %v\n", err)
				}
			}
			if flagHandle == "" && cfg.Handle == "" {
				if handle, ok := api.HandleFromURL(publishedURL); ok && handle != api.AnonymousHandle {
					if err := config.Set("handle", handle); err != nil {
						fmt.Fprintf(w.Err, "Warning: failed to save handle: %v\n", err)
					}
				}
			}

			// Breadcrumbs: the three most likely next actions for a just-
			// published doc. Agents can pick the intent; humans see a subtle
			// stderr footer.
			w.OK(resp,
				output.WithSummary("Published %s (%s)", publishedURL, kindSubstrateLabel(resp.Kind, resp.Substrate)),
				output.WithBreadcrumb("view", "pura open "+resp.Slug, "Open in browser"),
				output.WithBreadcrumb("edit", fmt.Sprintf("pura chat %s \"<instruction>\"", resp.Slug), "AI-edit this doc"),
				output.WithBreadcrumb("history", "pura versions ls "+resp.Slug, "See version history"),
			)
			w.Print("  Published: %s\n", publishedURL)
			w.Print("  Token:     %s  (save this to edit/delete)\n", resp.Token)
			w.Print("  Kind:      %s\n", resp.Kind)
			if resp.Substrate != "" {
				w.Print("  Substrate: %s\n", resp.Substrate)
			}
			if resp.Title != "" {
				w.Print("  Title:     %s\n", resp.Title)
			}

			if flagOpen {
				openBrowser(publishedURL)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&flagTitle, "title", "t", "", "Document title")
	cmd.Flags().StringVar(&flagSubstrate, "substrate", "", "Wire format (markdown|html|csv|json|svg|canvas|ascii) — auto-detected from content when omitted")
	cmd.Flags().StringVar(&flagKind, "kind", "", "Primitive kind (doc|sheet|page|slides|canvas|image|file|book) — overrides the kind derived from --substrate")
	cmd.Flags().StringVar(&flagTheme, "theme", "", "Theme preset")
	cmd.Flags().BoolVar(&flagStdin, "stdin", false, "Read content from stdin")
	cmd.Flags().BoolVarP(&flagOpen, "open", "o", false, "Open in browser after push")
	cmd.Flags().BoolVar(&flagNoEmbed, "no-embed", false, "Do not upload/rewrite local image references; publish paths as-is")

	return cmd
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	}
	if cmd != nil {
		_ = cmd.Start()
	}
}

func absPath(name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	wd, _ := os.Getwd()
	return filepath.Join(wd, name)
}

var imageExtToMIME = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".gif":  "image/gif",
}

var fileExtToMIME = map[string]string{
	".pdf":  "application/pdf",
	".txt":  "text/plain",
	".md":   "text/markdown",
	".csv":  "text/csv",
	".json": "application/json",
	".yaml": "application/yaml",
	".yml":  "application/yaml",
	".toml": "application/toml",
	".zip":  "application/zip",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".xls":  "application/vnd.ms-excel",
	".doc":  "application/msword",
}

var autoFileAssetExts = map[string]struct{}{
	".pdf":  {},
	".zip":  {},
	".xlsx": {},
	".docx": {},
	".pptx": {},
	".xls":  {},
	".doc":  {},
}

type pushToolEnvelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code       string `json:"code"`
		Field      string `json:"field,omitempty"`
		Suggestion string `json:"suggestion,omitempty"`
	} `json:"error,omitempty"`
}

type imageUploadResult struct {
	ImageRef  string `json:"image_ref"`
	URL       string `json:"url"`
	R2Key     string `json:"r2_key"`
	R2Deduped bool   `json:"r2_deduped"`
	Deduped   bool   `json:"deduped"`
	Slug      string `json:"slug"`
}

type fileUploadResult struct {
	FileRef   string `json:"file_ref"`
	URL       string `json:"url"`
	R2Key     string `json:"r2_key"`
	R2Deduped bool   `json:"r2_deduped"`
	Deduped   bool   `json:"deduped"`
	Slug      string `json:"slug"`
}

// assetUploadResult is the normalized output of uploadAssetCore — the fields
// both pushAsset (terminal publish) and embedLocalImages (internal) need.
type assetUploadResult struct {
	URL     string
	Slug    string
	Ref     string
	Deduped bool
}

func detectAssetKind(filename, flagKind, flagSubstrate string) string {
	switch {
	case flagSubstrate == "image" || flagKind == "image":
		return "image"
	case flagSubstrate == "file" || flagKind == "file":
		return "file"
	}

	ext := strings.ToLower(filepath.Ext(filename))
	if _, ok := imageExtToMIME[ext]; ok {
		return "image"
	}
	if _, ok := autoFileAssetExts[ext]; ok {
		return "file"
	}
	return ""
}

func pushAsset(
	cmd *cobra.Command,
	w *output.Writer,
	cfg *config.Config,
	filename string,
	data []byte,
	flagTitle string,
	flagOpen bool,
	assetKind string,
) error {
	if len(data) == 0 {
		w.Error("validation", "Content is empty", "Provide non-empty content")
		return fmt.Errorf("empty content")
	}

	mimeType, err := detectAssetMIME(filename, data, assetKind)
	if err != nil {
		w.Error("validation", err.Error(), "Use a supported file extension or pass a matching asset file")
		return err
	}

	if cfg.Token == "" {
		err := fmt.Errorf("asset uploads require authentication")
		w.Error("unauthorized", err.Error(), "Run `pura auth login` or pass `--token` with a docs:write API key")
		return err
	}

	res, err := uploadAssetCore(cmd, cfg, assetKind, mimeType, filename, data, flagTitle)
	if err != nil {
		if ae, ok := err.(*api.Error); ok {
			w.Error(ae.Code, ae.Message, ae.Hint)
		} else {
			w.Error("api_error", err.Error(), "")
		}
		return err
	}
	return finishAssetPush(w, cfg, res.URL, res.Slug, res.Ref, mimeType, assetKind, flagTitle, flagOpen)
}

// uploadAssetCore POSTs base64-encoded bytes to <kind>.upload and returns the
// parsed result. Shared by pushAsset (terminal publish) and embedLocalImages
// (internal embed-first rewrite). Does not print — callers render output.
func uploadAssetCore(
	cmd *cobra.Command,
	cfg *config.Config,
	assetKind, mimeType, filename string,
	data []byte,
	title string,
) (assetUploadResult, error) {
	toolName := assetKind + ".upload"
	toolURL := strings.TrimRight(cfg.APIURL, "/") + "/api/tool/" + toolName
	toolArgs := map[string]any{
		"content_base64": base64.StdEncoding.EncodeToString(data),
		"mime":           mimeType,
		"filename":       filepath.Base(filename),
	}
	if title != "" {
		toolArgs["title"] = title
	}
	payload, err := json.Marshal(toolArgs)
	if err != nil {
		return assetUploadResult{}, fmt.Errorf("marshal asset upload args: %w", err)
	}

	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodPost, toolURL, bytes.NewReader(payload))
	if err != nil {
		return assetUploadResult{}, fmt.Errorf("creating asset upload request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("X-Pura-Agent", fmt.Sprintf("pura-cli/%s (session:%d)", versionStr, os.Getpid()))

	httpC := &http.Client{Timeout: 60 * time.Second}
	start := time.Now()
	if flagVerbose {
		fmt.Fprintf(os.Stderr, "> POST %s\n", req.URL.Redacted())
	}
	resp, err := httpC.Do(req)
	if err != nil {
		return assetUploadResult{}, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return assetUploadResult{}, fmt.Errorf("reading response: %w", err)
	}
	if flagVerbose {
		fmt.Fprintf(
			os.Stderr,
			"< %d %s (%dB, %s)\n",
			resp.StatusCode,
			http.StatusText(resp.StatusCode),
			len(raw),
			time.Since(start).Round(time.Millisecond),
		)
	}

	var envelope pushToolEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return assetUploadResult{}, fmt.Errorf("decoding response: %w", err)
	}
	if !envelope.OK {
		return assetUploadResult{}, toolEnvelopeError(resp.StatusCode, envelope.Error)
	}

	switch assetKind {
	case "image":
		var result imageUploadResult
		if err := json.Unmarshal(envelope.Result, &result); err != nil {
			return assetUploadResult{}, fmt.Errorf("decoding image upload result: %w", err)
		}
		return assetUploadResult{URL: result.URL, Slug: result.Slug, Ref: result.ImageRef, Deduped: result.Deduped}, nil
	case "file":
		var result fileUploadResult
		if err := json.Unmarshal(envelope.Result, &result); err != nil {
			return assetUploadResult{}, fmt.Errorf("decoding file upload result: %w", err)
		}
		return assetUploadResult{URL: result.URL, Slug: result.Slug, Ref: result.FileRef, Deduped: result.Deduped}, nil
	default:
		return assetUploadResult{}, fmt.Errorf("unsupported asset kind: %s", assetKind)
	}
}

// embedLocalImages uploads every LOCAL image reference that resolves to a real
// file (relative to docDir, or absolute) to the user's host and rewrites the
// reference to the host URL. External / data / unresolvable refs are left
// untouched (the server rehosts external URLs; an unresolvable relative path is
// likely a web-relative reference we must not clobber). A genuine upload failure
// (network / auth / server error) aborts the push.
func embedLocalImages(
	cmd *cobra.Command,
	w *output.Writer,
	cfg *config.Config,
	docDir, content string,
) (string, error) {
	refs := ingest.ScanImageRefs(content)
	if len(refs) == 0 {
		return content, nil
	}

	replace := map[string]string{}
	seen := map[string]string{} // resolved abs path → host URL (client-side dedup)
	embedded := 0

	for _, ref := range refs {
		if ref.Kind != ingest.LocalRelative && ref.Kind != ingest.LocalAbsolute {
			continue
		}
		abs := ref.URL
		if ref.Kind == ingest.LocalRelative {
			abs = filepath.Join(docDir, ref.URL)
		}
		abs = filepath.Clean(abs)

		if url, ok := seen[abs]; ok {
			replace[ref.URL] = url
			continue
		}

		info, statErr := os.Stat(abs)
		if statErr != nil || info.IsDir() {
			continue // not a local file — leave as-is (likely a web path)
		}
		data, readErr := os.ReadFile(abs)
		if readErr != nil {
			if flagVerbose {
				fmt.Fprintf(w.Err, "  embed skip (unreadable): %s\n", ref.URL)
			}
			continue
		}
		if int64(len(data)) > maxEmbedImageBytes {
			if flagVerbose {
				fmt.Fprintf(w.Err, "  embed skip (>%dMB): %s\n", maxEmbedImageBytes>>20, ref.URL)
			}
			continue
		}
		mimeType, mErr := detectAssetMIME(abs, data, "image")
		if mErr != nil {
			if flagVerbose {
				fmt.Fprintf(w.Err, "  embed skip (not an image): %s\n", ref.URL)
			}
			continue
		}
		if cfg.Token == "" {
			if flagVerbose {
				fmt.Fprintf(w.Err, "  embed skip (login required to host local images): %s\n", ref.URL)
			}
			continue
		}

		res, err := uploadAssetCore(cmd, cfg, "image", mimeType, abs, data, "")
		if err != nil {
			if ae, ok := err.(*api.Error); ok {
				w.Error(ae.Code, ae.Message, ae.Hint)
			} else {
				w.Error("api_error", fmt.Sprintf("embedding %s: %v", ref.URL, err), "")
			}
			return "", err
		}
		seen[abs] = res.URL
		replace[ref.URL] = res.URL
		embedded++
	}

	if embedded > 0 && flagVerbose {
		fmt.Fprintf(w.Err, "  embedded %d local image(s)\n", embedded)
	}
	return ingest.RewriteRefs(content, refs, replace), nil
}

func detectAssetMIME(filename string, data []byte, assetKind string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	if assetKind == "image" {
		if mimeType, ok := imageExtToMIME[ext]; ok {
			return mimeType, nil
		}
		sniffed := normalizeDetectedMIME(http.DetectContentType(data))
		if strings.HasPrefix(sniffed, "image/") {
			return sniffed, nil
		}
		return "", fmt.Errorf("unsupported image type: %s", sniffed)
	}

	if mimeType, ok := fileExtToMIME[ext]; ok {
		return mimeType, nil
	}
	sniffed := normalizeDetectedMIME(http.DetectContentType(data))
	if isSupportedFileMIME(sniffed) {
		return sniffed, nil
	}
	return "", fmt.Errorf("unsupported file type: %s", sniffed)
}

func normalizeDetectedMIME(v string) string {
	return strings.TrimSpace(strings.Split(v, ";")[0])
}

func isSupportedFileMIME(v string) bool {
	for _, mimeType := range fileExtToMIME {
		if v == mimeType {
			return true
		}
	}
	return false
}

func toolEnvelopeError(status int, detail *struct {
	Code       string `json:"code"`
	Field      string `json:"field,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}) error {
	if detail == nil {
		return &api.Error{Status: status, Message: fmt.Sprintf("unexpected status %d", status)}
	}
	msg := detail.Code
	if detail.Field != "" {
		msg += " (field=" + detail.Field + ")"
	}
	return &api.Error{
		Status:  status,
		Code:    detail.Code,
		Message: msg,
		Hint:    detail.Suggestion,
	}
}

func finishAssetPush(
	w *output.Writer,
	cfg *config.Config,
	publishedURL string,
	slug string,
	ref string,
	mimeType string,
	kind string,
	title string,
	flagOpen bool,
) error {
	if flagHandle == "" && cfg.Handle == "" {
		if handle, ok := api.HandleFromURL(publishedURL); ok && handle != api.AnonymousHandle {
			if err := config.Set("handle", handle); err != nil {
				fmt.Fprintf(w.Err, "Warning: failed to save handle: %v\n", err)
			}
		}
	}

	w.OK(
		map[string]any{
			"url":   publishedURL,
			"slug":  slug,
			"ref":   ref,
			"kind":  kind,
			"mime":  mimeType,
			"title": title,
		},
		output.WithSummary("Published %s (%s)", publishedURL, kind),
		output.WithBreadcrumb("view", "pura open "+slug, "Open in browser"),
		output.WithBreadcrumb("history", "pura versions ls "+slug, "See version history"),
	)
	w.Print("  Published: %s\n", publishedURL)
	w.Print("  Ref:       %s\n", ref)
	w.Print("  Kind:      %s\n", kind)
	w.Print("  MIME:      %s\n", mimeType)
	if title != "" {
		w.Print("  Title:     %s\n", title)
	}

	if flagOpen {
		openBrowser(publishedURL)
	}
	return nil
}
