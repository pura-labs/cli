package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pura-labs/cli/internal/api"
	"github.com/pura-labs/cli/internal/config"
	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

// newImageCmd is the `pura image` group — manage the per-user image library
// (图床). Uploading is `pura push <image>`; this group lists / inspects /
// deletes what's been uploaded.
func newImageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "image",
		Short: "Manage your image library (图床)",
		Long:  "List, inspect, and delete images in your per-user image host. Upload with `pura push <image>`.",
	}
	cmd.AddCommand(newImageLsCmd(), newImageRmCmd(), newImageGetCmd())
	return cmd
}

// callTool POSTs args to /api/tool/<name> and returns the raw `result`.
// Reuses pushToolEnvelope / toolEnvelopeError from push.go (same package).
func callTool(cmd *cobra.Command, cfg *config.Config, name string, args map[string]any) (json.RawMessage, error) {
	toolURL := strings.TrimRight(cfg.APIURL, "/") + "/api/tool/" + name
	payload, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal tool args: %w", err)
	}
	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodPost, toolURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("creating tool request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
	req.Header.Set("X-Pura-Agent", fmt.Sprintf("pura-cli/%s (session:%d)", versionStr, os.Getpid()))

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	var envelope pushToolEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	if !envelope.OK {
		return nil, toolEnvelopeError(resp.StatusCode, envelope.Error)
	}
	return envelope.Result, nil
}

func surfaceToolError(w *output.Writer, err error) {
	if ae, ok := err.(*api.Error); ok {
		w.Error(ae.Code, ae.Message, ae.Hint)
	} else {
		w.Error("api_error", err.Error(), "")
	}
}

// resolveOwnHandle figures out the caller's handle for building @handle/slug
// refs: --handle flag, then the stored config, then a /api/auth/me lookup
// (covers api_key users who authenticated via --token and never pushed, so no
// handle is cached locally). Falling back to the anonymous "_" would wrongly
// look up anon-namespace items, so we resolve it properly instead.
func resolveOwnHandle(cmd *cobra.Command, cfg *config.Config) (string, error) {
	if flagHandle != "" {
		return strings.TrimPrefix(flagHandle, "@"), nil
	}
	// Prefer the server's view of the authenticated user. The locally cached
	// cfg.Handle can be stale (a wrong handle builds @wrong/slug → not_found),
	// so trust /api/auth/me when we have a token; fall back to the cache only
	// when the lookup fails (e.g. offline).
	if cfg.Token != "" {
		if me, err := newClient(cmd, cfg).Me(); err == nil && me.Handle != "" {
			return me.Handle, nil
		}
	}
	if cfg.Handle != "" {
		return cfg.Handle, nil
	}
	return "", &api.Error{
		Status:  400,
		Code:    "validation",
		Message: "your account has no handle yet",
		Hint:    "Publish something first (pura push …) so a handle is assigned.",
	}
}

func newImageLsCmd() *cobra.Command {
	var (
		flagLimit int
		flagQuery string
	)
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List your image library",
		Long:  "List images you've uploaded, with how many documents embed each (usage_count).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := newWriter()
			cfg := loadConfig()
			if cfg.Token == "" {
				w.Error("unauthorized", "Listing images requires authentication", "Run `pura auth login`",
					output.WithBreadcrumb("retry", "pura auth login", "Sign in"))
				return fmt.Errorf("no token")
			}

			args := map[string]any{}
			if flagLimit > 0 {
				args["limit"] = flagLimit
			}
			if flagQuery != "" {
				args["q"] = flagQuery
			}
			raw, err := callTool(cmd, cfg, "image.list", args)
			if err != nil {
				surfaceToolError(w, err)
				return err
			}
			var result struct {
				Images []api.ImageListItem `json:"images"`
				Total  int                 `json:"total"`
			}
			if err := json.Unmarshal(raw, &result); err != nil {
				return fmt.Errorf("decoding image.list result: %w", err)
			}

			if len(result.Images) == 0 {
				w.OK(result,
					output.WithSummary("No images yet"),
					output.WithBreadcrumb("upload", "pura push <image>", "Upload an image"))
				w.Print("  No images yet. Upload one with `pura push <image>`.\n")
				return nil
			}

			w.OK(result,
				output.WithSummary("%d image(s)", result.Total),
				output.WithBreadcrumb("get", "pura image get <slug>", "Inspect an image"),
				output.WithBreadcrumb("rm", "pura image rm <slug>", "Delete an image"),
				output.WithBreadcrumb("upload", "pura push <image>", "Upload another"),
			)
			w.Print("  %-12s %-24s %-11s %-9s %-5s %s\n", "SLUG", "TITLE", "DIMS", "SIZE", "USED", "UPDATED")
			for _, img := range result.Images {
				name := img.Title
				if name == "" {
					name = img.Filename
				}
				dims := ""
				if img.Width > 0 && img.Height > 0 {
					dims = fmt.Sprintf("%dx%d", img.Width, img.Height)
				}
				w.Print("  %-12s %-24s %-11s %-9s %-5d %s\n",
					truncate(img.Slug, 12), truncate(name, 24), dims, humanBytes(img.Bytes), img.UsageCount, dateOnly(img.UpdatedAt))
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&flagLimit, "limit", 0, "Max images to list (default 50, max 200)")
	cmd.Flags().StringVarP(&flagQuery, "query", "q", "", "Filter by title / filename / alt / tags")
	return cmd
}

func newImageRmCmd() *cobra.Command {
	var (
		flagYes   bool
		flagForce bool
	)
	cmd := &cobra.Command{
		Use:   "rm <slug>",
		Short: "Delete an image from your library",
		Long:  "Delete an image. Blocked (409 in_use) when documents still embed it; pass --force to delete anyway.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			slug := args[0]
			if cfg.Token == "" {
				w.Error("unauthorized", "Deleting images requires authentication", "Run `pura auth login`")
				return fmt.Errorf("no token")
			}
			handle, err := resolveOwnHandle(cmd, cfg)
			if err != nil {
				surfaceToolError(w, err)
				return err
			}

			// --yes OR --force skip the prompt; --force additionally overrides
			// the refcount block.
			ok, err := confirmMutation(w, flagYes || flagForce, "--yes",
				fmt.Sprintf("Delete image %s?", slug), "Removes it from your library.", "Delete")
			if err != nil {
				w.Error("confirmation_required", "Refusing to delete without confirmation",
					"Re-run with --yes in non-interactive mode.")
				return err
			}
			if !ok {
				w.Print("  Cancelled.\n")
				return nil
			}

			status, body, err := deleteImageRequest(cmd, cfg, handle, slug, flagForce)
			if err != nil {
				w.Error("api_error", err.Error(), "")
				return err
			}
			if status == 204 || (body != nil && body.OK) {
				w.OK(map[string]string{"slug": slug, "status": "deleted"},
					output.WithSummary("Deleted image %s", slug),
					output.WithBreadcrumb("list", "pura image ls", "See remaining images"))
				w.Print("  Deleted: %s\n", slug)
				return nil
			}

			// in_use: surface the consuming docs + how to override.
			if status == 409 && body != nil && body.Error != nil && body.Error.Code == "in_use" {
				w.Error("in_use", body.Error.Message, "Re-run with --force to delete anyway.")
				for _, c := range body.Error.Consumers {
					label := c.Slug
					if c.Title != nil && *c.Title != "" {
						label = *c.Title
					}
					w.Print("  used by: @%s/%s  (%s)\n", c.Handle, c.Slug, label)
				}
				return &api.Error{Status: 409, Code: "in_use", Message: body.Error.Message}
			}

			code, msg, hint := "api_error", fmt.Sprintf("delete failed (%d)", status), ""
			if body != nil && body.Error != nil {
				code, msg, hint = body.Error.Code, body.Error.Message, body.Error.Hint
			}
			w.Error(code, msg, hint)
			return &api.Error{Status: status, Code: code, Message: msg, Hint: hint}
		},
	}
	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Skip confirmation")
	cmd.Flags().BoolVar(&flagForce, "force", false, "Delete even if documents still embed the image")
	return cmd
}

// deleteImageResponse mirrors the DELETE /api/p envelope, including the
// image-host `in_use` consumers list.
type deleteImageResponse struct {
	OK    bool `json:"ok"`
	Error *struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Hint      string `json:"hint"`
		Consumers []struct {
			Handle string  `json:"handle"`
			Slug   string  `json:"slug"`
			Title  *string `json:"title"`
		} `json:"consumers"`
	} `json:"error"`
}

func deleteImageRequest(cmd *cobra.Command, cfg *config.Config, handle, slug string, force bool) (int, *deleteImageResponse, error) {
	url := strings.TrimRight(cfg.APIURL, "/") + "/api/p/@" + handle + "/" + slug
	if force {
		url += "?force=1"
	}
	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodDelete, url, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("creating delete request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("X-Pura-Agent", fmt.Sprintf("pura-cli/%s (session:%d)", versionStr, os.Getpid()))

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 204 {
		return 204, nil, nil
	}
	raw, _ := io.ReadAll(resp.Body)
	var body deleteImageResponse
	_ = json.Unmarshal(raw, &body)
	return resp.StatusCode, &body, nil
}

func newImageGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <slug>",
		Short: "Show an image's URL and metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			slug := args[0]
			if cfg.Token == "" {
				w.Error("unauthorized", "Reading images requires authentication", "Run `pura auth login`")
				return fmt.Errorf("no token")
			}
			handle, err := resolveOwnHandle(cmd, cfg)
			if err != nil {
				surfaceToolError(w, err)
				return err
			}

			raw, err := callTool(cmd, cfg, "image.read", map[string]any{"image_ref": "@" + handle + "/" + slug})
			if err != nil {
				surfaceToolError(w, err)
				return err
			}
			var img struct {
				URL      string   `json:"url"`
				R2Key    string   `json:"r2_key"`
				Mime     string   `json:"mime"`
				Bytes    int64    `json:"bytes"`
				Width    int      `json:"width"`
				Height   int      `json:"height"`
				Alt      string   `json:"alt"`
				Tags     []string `json:"tags"`
				Filename string   `json:"filename"`
				Title    string   `json:"title"`
			}
			if err := json.Unmarshal(raw, &img); err != nil {
				return fmt.Errorf("decoding image.read result: %w", err)
			}

			w.OK(img,
				output.WithSummary("%s (%s)", slug, humanBytes(img.Bytes)),
				output.WithBreadcrumb("open", fmt.Sprintf("pura open @%s/%s", handle, slug), "Open in browser"),
				output.WithBreadcrumb("rm", "pura image rm "+slug, "Delete"))
			w.Print("  URL:      %s\n", img.URL)
			if img.Width > 0 && img.Height > 0 {
				w.Print("  Dims:     %dx%d\n", img.Width, img.Height)
			}
			w.Print("  Size:     %s\n", humanBytes(img.Bytes))
			w.Print("  MIME:     %s\n", img.Mime)
			if img.Filename != "" {
				w.Print("  Filename: %s\n", img.Filename)
			}
			if img.Alt != "" {
				w.Print("  Alt:      %s\n", img.Alt)
			}
			if len(img.Tags) > 0 {
				w.Print("  Tags:     %s\n", strings.Join(img.Tags, ", "))
			}
			return nil
		},
	}
	return cmd
}

func humanBytes(n int64) string {
	switch {
	case n <= 0:
		return "0 B"
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

func dateOnly(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}
