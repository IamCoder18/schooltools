package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/IamCoder18/schooltools/internal/authstats"
	"github.com/IamCoder18/schooltools/internal/render"
	"github.com/IamCoder18/schooltools/internal/table"
)

var sessionTtlCmd = &cobra.Command{
	Use:   "ttl",
	Short: "Compute observed D2L server-side session lifetime from the auth log",
	Long: "Each gap between two successive `login.success` events in auth.log\n" +
		"is an upper bound on how long the saved LMS session actually lived — the\n" +
		"session had to be alive at the start of each gap and dead before the\n" +
		"next login succeeded. Useful for picking a sensible heartbeat interval.",
	RunE: func(cmd *cobra.Command, args []string) error {
		events, err := authstats.Load()
		if err != nil {
			return err
		}
		ttl := authstats.TTL(events)
		printSessionTTL(ttl)
		return nil
	},
}

func init() {
	sessionCmd.AddCommand(sessionTtlCmd)
}

func printSessionTTL(ttl authstats.TTLResult) {
	var b strings.Builder
	b.WriteString(render.Title("Session lifetime"))

	if ttl.Samples == 0 {
		b.WriteString(render.Empty("Need at least 2 successful logins in auth.log to compute a gap."))
		b.WriteString(render.Bullets(
			"Each gap between two successive `login.success` events bounds how long the saved session lived.",
			"Run `schooltools login` at least twice, with the session actually dying in between, to start collecting samples.",
		))
		b.WriteString(render.Hint("schooltools login"))
		fmt.Print(b.String())
		return
	}

	b.WriteString(render.Section("Summary"))
	b.WriteString(render.KV(
		render.Row{Key: "Generated", Value: whenOf(ttl.GeneratedAt)},
		render.Row{Key: "Samples", Value: render.Count(ttl.Samples, "gap") + "  ·  login.success → login.success"},
		render.Row{Key: "Shortest", Value: render.Duration(ttl.Min)},
		render.Row{Key: "Longest", Value: render.Duration(ttl.Max)},
		render.Row{Key: "Mean", Value: render.Duration(ttl.Mean)},
		render.Row{Key: "Median", Value: render.Duration(ttl.Median)},
		render.Row{Key: "Latest gap", Value: render.Duration(ttl.LatestGap)},
		render.Row{Key: "Total uptime", Value: render.Duration(ttl.HoursOfUptime)},
	))

	b.WriteString(render.Section("Recent logins"))
	start := len(ttl.LoginTimestamps) - 5
	if start < 0 {
		start = 0
	}
	rows := make([][]string, 0, 5)
	for i := len(ttl.LoginTimestamps) - 1; i >= start; i-- {
		gap := "—"
		if i > 0 && ttl.LoginTimestamps[i].After(ttl.LoginTimestamps[i-1]) {
			gap = render.Duration(ttl.LoginTimestamps[i].Sub(ttl.LoginTimestamps[i-1]))
		}
		rows = append(rows, []string{whenOf(ttl.LoginTimestamps[i]), gap})
	}
	b.WriteString(table.Render(table.Options{
		Headers: []string{"Logged in", "Gap before"},
		Widths:  table.Widths([]table.ColumnSpec{table.Fixed(38), table.Flex(16)}, table.TerminalWidth()),
		Rows:    rows,
	}))

	b.WriteString(render.Section("Interpretation"))
	b.WriteString(render.Bullets(
		"The shortest gap is the tightest lower bound on session lifetime.",
		"The longest gap is an upper bound (the session may have died earlier).",
		"The actual lifetime is somewhere between the shortest and the longest.",
		"Pick a heartbeat interval comfortably below the median to avoid forced re-logins.",
	))
	b.WriteString(render.Section("Next"))
	b.WriteString(render.Hint("schooltools session   to inspect the current session"))
	fmt.Print(b.String())
}
