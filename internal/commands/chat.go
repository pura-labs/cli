// `pura chat <slug> "<instruction>"` — AI-edit a document.
//
// Phase 4 (propose-gate) semantics:
//
//   Default    → proposal streams in → we automatically POST /accept so the
//                doc-mutating behaviour from a scripted user's point of view
//                is preserved.
//   --dry-run  → we automatically POST /reject so the stream leaves zero
//                trace on the version history. Exit code 0, but the summary
//                says "not applied".
//   --interactive (TTY) → show the diff summary + destructive flag, prompt
//                y/N, then accept or reject.
//   --resolve=accept|reject → when the server returns 409 `pending_exists`
//                for a previously-abandoned proposal, resolve it first then
//                retry the current turn. Without this flag, 409 is a hard
//                error with a hint.
//
// The stream prints tokens to stderr so stdout stays clean for the final
// JSON envelope.

package commands

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pura-labs/cli/internal/api"
	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

var (
	chatDryRun      bool
	chatSelection   string
	chatModel       string
	chatNoStream    bool
	chatInteractive bool
	chatResolve     string
	chatYes         bool
)

func resetChatFlags() {
	chatDryRun = false
	chatSelection = ""
	chatModel = ""
	chatNoStream = false
	chatInteractive = false
	chatResolve = ""
	chatYes = false
}

func newChatCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chat <slug> \"<instruction>\"",
		Short: "AI-edit a document",
		Long: `Propose an AI edit to the document. Default behaviour auto-accepts the
proposal; --dry-run auto-rejects; --interactive shows the diff and asks.

Examples:
  pura chat xy12ab "tighten the intro"
  pura chat xy12ab "summarize in 3 bullets" --dry-run
  pura chat xy12ab "add an email column" --interactive
  pura chat xy12ab "retry" --resolve=reject`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			if cfg.Token == "" {
				w.Error("unauthorized", "No token configured",
					"Run `pura auth login` first.",
					output.WithBreadcrumb("retry", "pura auth login", "Sign in"),
				)
				return errors.New("no token")
			}
			slug, instruction := args[0], args[1]
			if strings.TrimSpace(instruction) == "" {
				w.Error("validation", "Instruction is required", "Pass a non-empty natural-language edit request.")
				return errors.New("empty instruction")
			}
			if chatResolve != "" && chatResolve != "accept" && chatResolve != "reject" {
				w.Error("validation", "--resolve must be 'accept' or 'reject'", "")
				return errors.New("invalid --resolve")
			}

			client := newClient(cmd, cfg)
			return runChatTurn(w, client, slug, instruction, false)
		},
	}
	cmd.Flags().BoolVar(&chatDryRun, "dry-run", false, "Auto-reject the proposal so nothing lands; use to preview")
	cmd.Flags().StringVar(&chatSelection, "selection", "", "Scope the edit to this substring of the doc")
	cmd.Flags().StringVar(&chatModel, "model", "", "Model id; when omitted the server picks the default")
	cmd.Flags().BoolVar(&chatNoStream, "no-stream", false, "Suppress the token-by-token stderr stream")
	cmd.Flags().BoolVar(&chatInteractive, "interactive", false, "On TTY, show the diff and prompt before applying")
	cmd.Flags().StringVar(&chatResolve, "resolve", "", "When a pending proposal blocks this turn, resolve it first: accept|reject")
	cmd.Flags().BoolVar(&chatYes, "yes", false, "Skip the interactive prompt (forces auto-accept)")
	return cmd
}

// runChatTurn is extracted so a retry after a /resolve can re-enter it
// without duplicating the stream-event wiring.
func runChatTurn(w *output.Writer, client *api.Client, slug, instruction string, isRetry bool) error {
	req := api.ChatRequest{
		Instruction:  instruction,
		SelectedText: chatSelection,
		Model:        chatModel,
	}

	var (
		beforeVersion int
		messageID     string
		diffSummary   string
		destructive   bool
		noopMessage   string
		streamErr     *api.ChatSSEEvent
		accumulated   strings.Builder
		model         string
		promptTokens  int
		completion    int
		gotProposal   bool
		gotNoop       bool
	)

	streamTokens := !chatNoStream && w.IsTTY && !flagJSON && flagJQ == ""

	onEvent := func(e api.ChatSSEEvent) {
		switch e.Type {
		case "message":
			beforeVersion = e.BeforeVersion
			messageID = e.MessageID
		case "token":
			accumulated.WriteString(e.Content)
			if streamTokens {
				fmt.Fprint(w.Err, e.Content)
			}
		case "tool_call":
			if streamTokens {
				fmt.Fprintf(w.Err, "\n  ▸ tool_call %s %v", e.ToolName, e.ToolArgs)
			}
		case "proposal":
			gotProposal = true
			diffSummary = e.DiffSummary
			destructive = e.Destructive
			if messageID == "" {
				messageID = e.MessageID
			}
		case "noop":
			gotNoop = true
			noopMessage = e.Message
			if messageID == "" {
				messageID = e.MessageID
			}
		case "usage":
			model = e.Model
			promptTokens = e.PromptTokens
			completion = e.CompletionTokens
		case "error":
			copy := e
			streamErr = &copy
		}
	}

	if streamTokens {
		fmt.Fprintln(w.Err, "")
	}

	err := client.Chat(slug, req, onEvent)
	if err != nil && !isRetry {
		// 409 pending_exists handling — the server refuses the turn because
		// a previous proposal is still pending. --resolve unblocks.
		if apiErr, ok := err.(*api.Error); ok && apiErr.Code == "pending_exists" {
			if chatResolve == "" {
				w.Error("pending_exists", apiErr.Message,
					"Pass --resolve=accept or --resolve=reject to clear the pending proposal, then this turn retries automatically.",
				)
				return err
			}
			if err := resolveExistingPending(client, slug, chatResolve); err != nil {
				w.Error("resolve_failed", fmt.Sprintf("Could not %s prior pending proposal: %v", chatResolve, err), "")
				return err
			}
			// Retry the turn now that the slot is clear.
			return runChatTurn(w, client, slug, instruction, true)
		}
		w.Error("api_error", err.Error(), "")
		return err
	}
	if err != nil {
		w.Error("api_error", err.Error(), "")
		return err
	}
	if streamTokens {
		fmt.Fprintln(w.Err, "")
	}

	if streamErr != nil {
		code := streamErr.ErrorCode
		if code == "" {
			code = "model_error"
		}
		w.Error(code,
			firstNonEmpty(streamErr.Message, "Model returned an error"),
			"Re-run without --selection to broaden context, or pick a different --model.",
		)
		return errors.New(streamErr.Message)
	}

	// Noop — model decided nothing should change. Report + exit clean.
	if gotNoop {
		w.OK(map[string]any{
			"slug":           slug,
			"message_id":     messageID,
			"noop_message":   noopMessage,
			"applied":        false,
			"before_version": beforeVersion,
		}, output.WithSummary("%s · no change proposed", slug))
		return nil
	}

	if !gotProposal {
		w.Error("incomplete_stream",
			"Stream ended before a proposal or noop was emitted",
			"Retry — the network or model may have dropped mid-turn.",
		)
		return errors.New("no proposal event")
	}

	// Decide Accept / Reject based on flags + TTY.
	action := decideAction(diffSummary, destructive, w.IsTTY)
	var appliedVersion int
	switch action {
	case "accept":
		res, err := client.AcceptProposal(slug, messageID)
		if err != nil {
			w.Error("accept_failed", err.Error(),
				"If the error was `stale`, the doc changed since the proposal was drafted — re-run the chat to propose on the latest version.",
			)
			return err
		}
		appliedVersion = res.AfterVersion
	case "reject":
		if _, err := client.RejectProposal(slug, messageID, "reject"); err != nil {
			w.Error("reject_failed", err.Error(), "")
			return err
		}
	}

	data := map[string]any{
		"slug":              slug,
		"message_id":        messageID,
		"before_version":    beforeVersion,
		"applied":           action == "accept",
		"after_version":     appliedVersion,
		"diff_summary":      diffSummary,
		"destructive":       destructive,
		"dry_run":           chatDryRun,
		"model":             model,
		"prompt_tokens":     promptTokens,
		"completion_tokens": completion,
		"content":           accumulated.String(),
	}

	var summary string
	switch action {
	case "accept":
		summary = fmt.Sprintf("Edited %s → v%d · %s", slug, appliedVersion, diffSummary)
	case "reject":
		if chatDryRun {
			summary = fmt.Sprintf("(dry-run) %s · proposal rejected — %s", slug, diffSummary)
		} else {
			summary = fmt.Sprintf("Rejected on %s · %s", slug, diffSummary)
		}
	}
	w.OK(data, output.WithSummary("%s", summary))
	return nil
}

// decideAction resolves accept vs reject based on flags + TTY. Called after
// a proposal event is received.
func decideAction(diffSummary string, destructive, isTTY bool) string {
	if chatDryRun {
		return "reject"
	}
	if chatYes {
		return "accept"
	}
	if chatInteractive && isTTY {
		warning := ""
		if destructive {
			warning = " (destructive)"
		}
		fmt.Printf("  ▸ %s%s\n  Apply? [y/N] ", diffSummary, warning)
		var answer string
		_, _ = fmt.Scanln(&answer)
		a := strings.ToLower(strings.TrimSpace(answer))
		if a == "y" || a == "yes" {
			return "accept"
		}
		return "reject"
	}
	return "accept"
}

// resolveExistingPending finds the prior pending message for this doc and
// applies the requested resolution (accept or reject). Uses /messages as
// the lookup since the server's 409 body lacks the id.
func resolveExistingPending(client *api.Client, slug, kind string) error {
	msgs, err := client.ListMessages(slug, 10)
	if err != nil {
		return fmt.Errorf("listing messages: %w", err)
	}
	var pendingID string
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].ProposalStatus == "pending" {
			pendingID = msgs[i].ID
			break
		}
	}
	if pendingID == "" {
		return fmt.Errorf("server returned pending_exists but no pending message was found in /messages")
	}
	if kind == "accept" {
		_, err := client.AcceptProposal(slug, pendingID)
		return err
	}
	_, err = client.RejectProposal(slug, pendingID, "reject")
	return err
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
