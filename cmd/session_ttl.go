package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/authstats"
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
		return printSessionTTL(ttl)
	},
}

func init() {
	sessionCmd.AddCommand(sessionTtlCmd)
}

func printSessionTTL(ttl authstats.TTLResult) error {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer tw.Flush()

	if ttl.Samples == 0 {
		fmt.Fprintln(tw, "Need at least 2 successful logins in auth.log to compute a gap.")
		fmt.Fprintln(tw, "Run `schooltools login` at least twice (with the session actually dying in between)")
		fmt.Fprintln(tw, "to start collecting samples.")
		return nil
	}

	fmt.Fprintf(tw, "Generated:\t%s\n", ttl.GeneratedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(tw, "Samples:\t%d (login.success → login.success gaps)\n", ttl.Samples)
	fmt.Fprintf(tw, "Shortest:\t%s\n", humanGap(ttl.Min))
	fmt.Fprintf(tw, "Longest:\t%s\n", humanGap(ttl.Max))
	fmt.Fprintf(tw, "Mean:\t%s\n", humanGap(ttl.Mean))
	fmt.Fprintf(tw, "Median:\t%s\n", humanGap(ttl.Median))
	fmt.Fprintf(tw, "Latest gap:\t%s\n", humanGap(ttl.LatestGap))
	fmt.Fprintf(tw, "Total uptime:\t%s\n", humanGap(ttl.HoursOfUptime))
	fmt.Fprintln(tw, "")
	fmt.Fprintln(tw, "Recent login timestamps:")
	for i := len(ttl.LoginTimestamps) - 1; i >= 0 && i >= len(ttl.LoginTimestamps)-5; i-- {
		if i < 0 {
			break
		}
		fmt.Fprintf(tw, "  %s\n", ttl.LoginTimestamps[i].UTC().Format(time.RFC3339))
	}
	fmt.Fprintln(tw, "")
	fmt.Fprintln(tw, "Interpretation:")
	fmt.Fprintln(tw, "  - The shortest gap is the tightest lower bound on session lifetime.")
	fmt.Fprintln(tw, "  - The longest gap is an upper bound (the session may have died earlier).")
	fmt.Fprintln(tw, "  - The actual lifetime is somewhere between the shortest and the longest.")
	fmt.Fprintln(tw, "  - Pick a heartbeat interval comfortably below the median to avoid forced re-logins.")
	return nil
}
