package cmd

import (
	"fmt"
	"net/http/cookiejar"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/authlog"
	"github.com/aarav/schooltools/internal/httpclient"
	"github.com/aarav/schooltools/internal/session"
	"github.com/aarav/schooltools/internal/table"
	"github.com/aarav/schooltools/internal/ua"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose session, ADFS, and D2L reachability",
	RunE: func(cmd *cobra.Command, args []string) error {
		diag := session.Diagnose()
		tw := table.TerminalWidth()

		// Session table.
		sessionRows := [][]string{
			{"Path", diag.Path},
			{"Exists", boolStr(diag.Exists)},
			{"Mode", modeStr(uint32(diag.Mode))},
			{"Cookies", intStr(diag.CookieCount)},
			{"Names", joinNames(diag.CookieNames)},
			{"Expired", joinNames(diag.ExpiredCookieNames)},
		}
		fmt.Println("Session")
		fmt.Print(table.Render(table.Options{
			Headers: []string{"Field", "Value"},
			Widths:  table.Widths([]table.ColumnSpec{table.Fixed(18), table.Flex(24)}, tw),
			Rows:    sessionRows,
		}))

		// Validity.
		records, _ := session.Load()
		valid := session.HasValidSession(records)
		fmt.Println("\nValidity")
		fmt.Print(table.Render(table.Options{
			Headers: []string{"Check", "Result"},
			Widths:  table.Widths([]table.ColumnSpec{table.Fixed(22), table.Flex(14)}, tw),
			Rows:    [][]string{{"hasValidSession()", boolResult(valid, "valid", "invalid")}},
		}))

		// Probes.
		probeRows := [][]string{}
		emptyJar, _ := httpclient.NewJar()
		probeRows = append(probeRows, probeRow("Login endpoint", ua.LoginEndpoint, emptyJar, nil))
		if len(records) > 0 {
			jar, _ := session.LoadJar()
			probeRows = append(probeRows, probeRow("D2L /d2l/home", ua.D2LBase+"/d2l/home", jar, []byte("saved cookies")))
		} else {
			probeRows = append(probeRows, probeRow("D2L /d2l/home", ua.D2LBase+"/d2l/home", emptyJar, nil))
		}
		fmt.Println("\nProbes")
		fmt.Print(table.Render(table.Options{
			Headers: []string{"Probe", "Status", "Final URL / Error"},
			Widths:  table.Widths([]table.ColumnSpec{table.Fixed(22), table.Fixed(8), table.Flex(32)}, tw),
			Rows:    probeRows,
		}))

		// Client table + auth log row.
		clientRows := [][]string{
			{"User-Agent", ua.UserAgent},
			{"Auth log", fmt.Sprintf("%s (enabled: %s)", authlog.Path(), boolStr(authlog.Enabled()))},
		}
		fmt.Println("\nClient")
		fmt.Print(table.Render(table.Options{
			Headers: []string{"Field", "Value"},
			Widths:  table.Widths([]table.ColumnSpec{table.Fixed(14), table.Flex(32)}, tw),
			Rows:    clientRows,
		}))
		return nil
	},
}

func init() { rootCmd.AddCommand(doctorCmd) }

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func boolResult(ok bool, yes, no string) string {
	if ok {
		return "✓ " + yes
	}
	return "✗ " + no
}

func modeStr(m uint32) string {
	if m == 0 {
		return "—"
	}
	return fmt.Sprintf("0%o", m)
}

func intStr(n int) string { return fmt.Sprintf("%d", n) }

func joinNames(names []string) string {
	if len(names) == 0 {
		return "—"
	}
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}

func probeRow(name, url string, jar *cookiejar.Jar, note []byte) []string {
	res, err := httpclient.FollowRedirects(url, jar, httpclient.FetchOptions{})
	if err != nil {
		noteStr := ""
		if note != nil {
			noteStr = string(note)
		}
		label := name
		if noteStr != "" {
			label += " (" + noteStr + ")"
		}
		return []string{label, "ERR", err.Error()}
	}
	final := res.Request.URL.String()
	status := fmt.Sprintf("%d", res.StatusCode)
	noteStr := ""
	if note != nil {
		noteStr = string(note)
	}
	label := name
	if noteStr != "" {
		label += " (" + noteStr + ")"
	}
	return []string{label, status, final}
}