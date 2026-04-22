// `pura stats <slug>` and `pura events <slug>` — observability helpers.
//
// stats:  view counts (basic public) or a detailed owner-only breakdown
//
//	(requires ownership / docs:read scope).
//
// events: paginated activity stream. --follow polls every few seconds and
//
//	prints new entries as they arrive; use Ctrl-C to stop.
package commands

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pura-labs/cli/internal/api"
	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

var (
	statsDetail   bool
	eventsSince   int
	eventsLimit   int
	eventsFollow  bool
	eventsKinds   string
	eventsPollSec int
)

func resetObservabilityFlags() {
	statsDetail = false
	eventsSince = 0
	eventsLimit = 0
	eventsFollow = false
	eventsKinds = ""
	eventsPollSec = 5
}

// ---------- stats ----------

func newStatsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats <slug>",
		Short: "Show view stats for a document",
		Long: `Public view counter by default. Pass --detail (owner only) for a
country breakdown, view-type split, and bot ratio.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			slug := args[0]
			client := newClient(cmd, cfg)

			if !statsDetail {
				s, err := client.GetStats(slug)
				if err != nil {
					w.Error("api_error", err.Error(), "")
					return err
				}
				w.OK(s,
					output.WithSummary("%s — %d view(s)", slug, s.Views),
					output.WithBreadcrumb("detail", "pura stats "+slug+" --detail", "Full breakdown (owner only)"),
				)
				w.Print("  Views: %d\n", s.Views)
				return nil
			}

			if cfg.Token == "" {
				w.Error("unauthorized",
					"Detailed stats require an authenticated owner",
					"Run `pura auth login` first.",
					output.WithBreadcrumb("retry", "pura auth login", "Sign in"),
				)
				return errors.New("no token")
			}
			d, err := client.GetDetailedStats(slug)
			if err != nil {
				w.Error("api_error", err.Error(), "")
				return err
			}
			w.OK(d,
				output.WithSummary("%s — %d views, %d countries", slug, d.Views, d.UniqueCountries),
				output.WithBreadcrumb("events", "pura events "+slug, "Drill into the activity stream"),
			)
			w.Print("  Views:            %d\n", d.Views)
			w.Print("  Unique countries: %d\n", d.UniqueCountries)
			if len(d.ViewTypes) > 0 {
				w.Print("  By type:\n")
				for k, v := range d.ViewTypes {
					w.Print("    %-8s %d\n", k, v)
				}
			}
			w.Print("  Bot ratio:        %.1f%%\n", d.BotRatio*100)
			return nil
		},
	}
	cmd.Flags().BoolVar(&statsDetail, "detail", false, "Owner-only full breakdown (country, type, bot ratio)")
	return cmd
}

// ---------- events ----------

func newEventsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events <slug>",
		Short: "List activity events for a document",
		Long: `Paginated event stream. Useful for "what happened here in the last hour?"

Flags:
  --since <id>    Return events newer than this cursor (server echoes it back)
  --limit <n>     1..200, default 50
  --kinds <csv>   Filter by public kinds (e.g. doc.updated,comment.added)
  --follow        Long-poll: keep printing new events until Ctrl-C`,
		Example: `  pura events xy12ab --limit 20
  pura events xy12ab --kinds comment.added,version.restored
  pura events xy12ab --follow`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			if cfg.Token == "" {
				w.Error("unauthorized", "No token configured", "Run `pura auth login` first.",
					output.WithBreadcrumb("retry", "pura auth login", "Sign in"))
				return errors.New("no token")
			}
			slug := args[0]
			client := newClient(cmd, cfg)

			opts := api.EventsOptions{
				Since: int64(eventsSince),
				Limit: eventsLimit,
				Kinds: eventsKinds,
			}
			resp, err := client.ListEvents(slug, opts)
			if err != nil {
				w.Error("api_error", err.Error(), "")
				return err
			}

			printEvents(w, resp.Events)

			if !eventsFollow {
				w.OK(resp,
					output.WithSummary("%d event(s), cursor=%d", len(resp.Events), resp.Cursor),
					output.WithBreadcrumb("follow", "pura events "+slug+" --follow", "Stream new events live"),
				)
				return nil
			}

			// Follow loop: poll every eventsPollSec seconds, appending any new
			// events. Ctrl-C breaks out cleanly with a concluding envelope.
			fmt.Fprintln(w.Err, "  ⟳ following — Ctrl-C to stop")
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
			defer signal.Stop(sigCh)

			cursor := resp.Cursor
			total := len(resp.Events)
			interval := time.Duration(eventsPollSec) * time.Second
			if interval < time.Second {
				interval = 5 * time.Second
			}
			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			for {
				select {
				case <-sigCh:
					w.OK(map[string]any{"cursor": cursor, "total": total},
						output.WithSummary("Stopped at cursor=%d after %d event(s)", cursor, total),
					)
					return nil
				case <-ticker.C:
				}
				page, err := client.ListEvents(slug, api.EventsOptions{
					Since: cursor,
					Limit: eventsLimit,
					Kinds: eventsKinds,
				})
				if err != nil {
					// Transient failure — log to stderr, keep polling.
					fmt.Fprintf(w.Err, "  poll error: %v (will retry)\n", err)
					continue
				}
				if len(page.Events) > 0 {
					printEvents(w, page.Events)
					cursor = page.Cursor
					total += len(page.Events)
				}
			}
		},
	}
	cmd.Flags().IntVar(&eventsSince, "since", 0, "Cursor: return events with id > this value")
	cmd.Flags().IntVar(&eventsLimit, "limit", 0, "Max rows (1..200, default 50 server-side)")
	cmd.Flags().StringVar(&eventsKinds, "kinds", "", "Filter by comma-separated public kinds (e.g. doc.updated,comment.added)")
	cmd.Flags().BoolVarP(&eventsFollow, "follow", "f", false, "Poll and stream new events until Ctrl-C")
	cmd.Flags().IntVar(&eventsPollSec, "poll-interval", 5, "Seconds between polls when --follow is set")
	return cmd
}

func printEvents(w *writerLike, events []api.EventRow) {
	for _, e := range events {
		ts := e.CreatedAt
		if len(ts) >= 19 {
			ts = ts[:19]
		}
		propsShort := ""
		if len(e.Props) > 0 {
			var keys []string
			for k, v := range e.Props {
				keys = append(keys, fmt.Sprintf("%s=%v", k, v))
			}
			propsShort = " " + strings.Join(keys, " ")
		}
		w.Print("  %s  %-22s  id=%d%s\n", ts, e.Kind, e.ID, propsShort)
	}
}

// writerLike is the subset of *output.Writer we need, so we can call
// printEvents without importing that package type into this signature.
// Keeps the helper reusable if we ever move it into its own file.
type writerLike = output.Writer
