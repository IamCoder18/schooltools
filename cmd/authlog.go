package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/authstats"
	"github.com/aarav/schooltools/internal/authlog"
)

var authlogCmd = &cobra.Command{
	Use:   "auth log",
	Short: "Summarise the schooltools auth log (~/.config/schooltools/auth.log)",
	Long: "Read the JSON-line auth log and surface:\n" +
		"  - event-type counts over the log's lifetime\n" +
		"  - the most recent login.success gap (a proxy for server-side session lifetime)\n" +
		"  - the last 5 login.failure / session.invalid / session.heartbeat.failed events\n\n" +
		"Use --json for scriptable output.",
	RunE: func(cmd *cobra.Command, args []string) error {
		events, err := authstats.Load()
		if err != nil {
			return err
		}
		s := authstats.Summarise(events)
		ttl := authstats.TTL(events)
		return printAuthSummary(s, ttl)
	},
}

func init() {
	rootCmd.AddCommand(authlogCmd)
}

func printAuthSummary(s authstats.Summary, ttl authstats.TTLResult) error {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer tw.Flush()

	fmt.Fprintf(tw, "Auth log:\t%s\n", authlog.Path())
	fmt.Fprintf(tw, "Generated:\t%s\n", s.GeneratedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(tw, "Total events:\t%d\n", s.TotalEvents)
	if !s.FirstEvent.IsZero() {
		fmt.Fprintf(tw, "Span:\t%s → %s (%s)\n",
			s.FirstEvent.UTC().Format(time.RFC3339),
			s.LastEvent.UTC().Format(time.RFC3339),
			s.LastEvent.Sub(s.FirstEvent).Truncate(time.Second))
	}

	fmt.Fprintln(tw, "")
	fmt.Fprintln(tw, "Event counts:")
	// Sort counts descending for stable output.
	type kv struct {
		name string
		n    int
	}
	pairs := make([]kv, 0, len(s.Counts))
	for name, n := range s.Counts {
		pairs = append(pairs, kv{name, n})
	}
	for i := 0; i < len(pairs); i++ {
		for j := i + 1; j < len(pairs); j++ {
			if pairs[j].n > pairs[i].n {
				pairs[i], pairs[j] = pairs[j], pairs[i]
			}
		}
	}
	for _, p := range pairs {
		fmt.Fprintf(tw, "  %-32s\t%d\n", p.name, p.n)
	}

	fmt.Fprintln(tw, "")
	fmt.Fprintln(tw, "Observed session lifetime (gap between successive login.success):")
	if ttl.Samples == 0 {
		fmt.Fprintln(tw, "  (need at least 2 successful logins to compute a gap)")
	} else {
		fmt.Fprintf(tw, "  samples:\t%d\n", ttl.Samples)
		fmt.Fprintf(tw, "  shortest:\t%s (lower bound on session life)\n", humanGap(ttl.Min))
		fmt.Fprintf(tw, "  longest:\t%s\n", humanGap(ttl.Max))
		fmt.Fprintf(tw, "  mean:\t%s\n", humanGap(ttl.Mean))
		fmt.Fprintf(tw, "  median:\t%s\n", humanGap(ttl.Median))
		fmt.Fprintf(tw, "  latest gap:\t%s (before the most recent login)\n", humanGap(ttl.LatestGap))
		fmt.Fprintf(tw, "  total logged-in time:\t%s\n", humanGap(ttl.HoursOfUptime))
	}

	if len(s.LastFailures) > 0 {
		fmt.Fprintln(tw, "")
		fmt.Fprintln(tw, "Recent failures (last 5):")
		for _, f := range s.LastFailures {
			ts := f.Time.UTC().Format(time.RFC3339)
			switch f.Name {
			case "login.failure":
				fmt.Fprintf(tw, "  %s  login.failure  code=%s  msg=%s\n", ts, f.Code, truncate(f.Message, 80))
			case "session.invalid":
				fmt.Fprintf(tw, "  %s  session.invalid  reason=%s\n", ts, f.Reason)
			case "session.heartbeat.failed":
				fmt.Fprintf(tw, "  %s  session.heartbeat.failed  msg=%s\n", ts, truncate(f.Message, 80))
			}
		}
	}

	return nil
}

// humanGap renders a duration in compact form.
func humanGap(d time.Duration) string {
	if d <= 0 {
		return "0"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d/(24*time.Hour)), int(d/time.Hour)%24)
}

// truncate cuts s to n chars with an ellipsis if shortened.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	return s[:n-1] + "…"
}
