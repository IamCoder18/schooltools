package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/IamCoder18/schooltools/internal/authlog"
	"github.com/IamCoder18/schooltools/internal/authstats"
	"github.com/IamCoder18/schooltools/internal/render"
	"github.com/IamCoder18/schooltools/internal/table"
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
		fmt.Print(authSummaryReport(s, ttl))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(authlogCmd)
}

func authSummaryReport(s authstats.Summary, ttl authstats.TTLResult) string {
	tw := table.TerminalWidth()
	var b strings.Builder
	b.WriteString(render.Title("Auth log"))

	b.WriteString(render.Section("Summary"))
	rows := []render.Row{
		{Key: "Path", Value: authlog.Path()},
		{Key: "Generated", Value: whenOf(s.GeneratedAt)},
		{Key: "Events", Value: render.Count(s.TotalEvents, "event")},
	}
	if !s.FirstEvent.IsZero() {
		rows = append(rows, render.Row{Key: "Span", Value: fmt.Sprintf("%s → %s  ·  %s",
			render.DateTime(isoOf(s.FirstEvent)),
			render.DateTime(isoOf(s.LastEvent)),
			render.Duration(s.LastEvent.Sub(s.FirstEvent)))})
	}
	b.WriteString(render.KV(rows...))

	b.WriteString(render.Section("Event counts"))
	type pair struct {
		name string
		n    int
	}
	pairs := make([]pair, 0, len(s.Counts))
	for name, n := range s.Counts {
		pairs = append(pairs, pair{name, n})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].n != pairs[j].n {
			return pairs[i].n > pairs[j].n
		}
		return pairs[i].name < pairs[j].name
	})
	if len(pairs) == 0 {
		b.WriteString(render.Empty("No events recorded yet."))
	} else {
		crows := make([][]string, 0, len(pairs))
		for _, p := range pairs {
			crows = append(crows, []string{p.name, fmt.Sprint(p.n)})
		}
		b.WriteString(table.Render(table.Options{
			Headers: []string{"Event", "Count"},
			Widths:  table.Widths([]table.ColumnSpec{table.Flex(30), table.Fixed(10)}, tw),
			Rows:    crows,
		}))
	}

	b.WriteString(render.Section("Session lifetime"))
	if ttl.Samples == 0 {
		b.WriteString(render.Empty("Need at least 2 successful logins to compute a gap."))
	} else {
		b.WriteString(render.KV(
			render.Row{Key: "Samples", Value: render.Count(ttl.Samples, "gap") + "  ·  login.success → login.success"},
			render.Row{Key: "Shortest", Value: render.Duration(ttl.Min) + "  ·  lower bound on session life"},
			render.Row{Key: "Longest", Value: render.Duration(ttl.Max) + "  ·  upper bound"},
			render.Row{Key: "Mean", Value: render.Duration(ttl.Mean)},
			render.Row{Key: "Median", Value: render.Duration(ttl.Median)},
			render.Row{Key: "Latest gap", Value: render.Duration(ttl.LatestGap) + "  ·  before the most recent login"},
			render.Row{Key: "Logged in", Value: render.Duration(ttl.HoursOfUptime) + "  ·  total across all gaps"},
		))
	}

	if len(s.LastFailures) > 0 {
		b.WriteString(render.Section("Recent failures"))
		frows := make([][]string, 0, len(s.LastFailures))
		for _, f := range s.LastFailures {
			detail := ""
			switch f.Name {
			case "login.failure":
				detail = "code=" + f.Code + "  ·  msg=" + render.Truncate(f.Message, 60)
			case "session.invalid":
				detail = "reason=" + f.Reason
			case "session.heartbeat.failed":
				detail = "msg=" + render.Truncate(f.Message, 60)
			}
			frows = append(frows, []string{whenOf(f.Time), render.Bad(f.Name), detail})
		}
		b.WriteString(table.Render(table.Options{
			Headers: []string{"When", "Event", "Detail"},
			Widths:  table.Widths([]table.ColumnSpec{table.Fixed(32), table.Fixed(26), table.Flex(24)}, tw),
			Rows:    frows,
		}))
	}

	b.WriteString(render.Section("Next"))
	b.WriteString(render.Hint("schooltools session ttl   for the full session-lifetime breakdown"))
	return b.String()
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
