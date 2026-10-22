package cmd

import (
	"fmt"
	"net/http/cookiejar"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/IamCoder18/schooltools/internal/authlog"
	"github.com/IamCoder18/schooltools/internal/httpclient"
	"github.com/IamCoder18/schooltools/internal/render"
	"github.com/IamCoder18/schooltools/internal/session"
	"github.com/IamCoder18/schooltools/internal/table"
	"github.com/IamCoder18/schooltools/internal/ua"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose session, ADFS, and D2L reachability",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Print(doctorReport())
		return nil
	},
}

func init() { rootCmd.AddCommand(doctorCmd) }

func doctorReport() string {
	diag := session.Diagnose()
	tw := table.TerminalWidth()
	var b strings.Builder
	b.WriteString(render.Title("Doctor"))

	b.WriteString(render.Section("Session"))
	sessionRows := [][]string{
		{"Session file", existsWord(diag.Exists) + "  ·  " + diag.Path},
		{"Permissions", modeStr(uint32(diag.Mode))},
		{"Cookies", render.Count(diag.CookieCount, "cookie")},
		{"Expired", namesOrNone(diag.ExpiredCookieNames)},
	}
	b.WriteString(checkTable(sessionRows, tw))

	records, _ := session.Load()
	valid := session.HasValidSession(records)
	ex, exErr := session.SessionExpiry()
	tokenRec, _ := session.LoadToken()
	now := time.Now()

	var advisories []string
	validityRows := [][]string{{"Session", validWord(valid)}}
	if !valid {
		advisories = append(advisories, "Saved session is invalid: run `schooltools login`.")
	}
	switch {
	case exErr != nil:
		validityRows = append(validityRows, []string{"SAML assertion", render.Muted("unavailable")})
	case !ex.SAMLAvailable:
		validityRows = append(validityRows, []string{"SAML assertion", render.Muted("none — captured on next login")})
	case ex.SAMLNotAfter.After(now):
		validityRows = append(validityRows, []string{"SAML assertion", render.OK("valid") + "  ·  expires " + render.RelAt(ex.SAMLNotAfter, now)})
	default:
		validityRows = append(validityRows, []string{"SAML assertion", render.Bad("expired") + "  ·  " + render.DateTime(isoOf(ex.SAMLNotAfter))})
		advisories = append(advisories, "SAML assertion expired: re-login required (run `schooltools login`).")
	}
	switch {
	case tokenRec == nil:
		validityRows = append(validityRows, []string{"Brightspace token", render.Muted("none — captured on next login")})
	case tokenRec.ExpiresAt.IsZero():
		validityRows = append(validityRows, []string{"Brightspace token", render.Muted("no expiry")})
	case tokenRec.IsExpired(now):
		validityRows = append(validityRows, []string{"Brightspace token", render.Bad("expired") + "  ·  " + render.DateTime(isoOf(tokenRec.ExpiresAt))})
		if tokenRec.RefreshToken == "" {
			advisories = append(advisories, "Brightspace token expired with no RefreshToken: re-login required (run `schooltools login`).")
		} else {
			advisories = append(advisories, "Brightspace token expired: try `schooltools token --refresh` (only works while the saved session is still valid).")
		}
	default:
		validityRows = append(validityRows, []string{"Brightspace token", render.OK("valid") + "  ·  expires " + render.RelAt(tokenRec.ExpiresAt, now)})
	}
	b.WriteString(render.Section("Validity"))
	b.WriteString(checkTable(validityRows, tw))

	emptyJar, _ := httpclient.NewJar()
	probeRows := [][]string{probeRow("Login endpoint", ua.LoginEndpoint, emptyJar, false)}
	if len(records) > 0 {
		jar, _ := session.LoadJar()
		probeRows = append(probeRows, probeRow("D2L /d2l/home", ua.D2LBase+"/d2l/home", jar, true))
	} else {
		probeRows = append(probeRows, probeRow("D2L /d2l/home", ua.D2LBase+"/d2l/home", emptyJar, false))
	}
	b.WriteString(render.Section("Probes"))
	b.WriteString(checkTable(probeRows, tw))

	b.WriteString(render.Section("Client"))
	clientRows := [][]string{
		{"User-agent", ua.UserAgent},
		{"Auth log", authlog.Path() + "  ·  " + render.StatusWord(boolStr(authlog.Enabled()))},
	}
	b.WriteString(checkTable(clientRows, tw))

	if len(advisories) > 0 {
		b.WriteString(render.Section("Warnings"))
		for _, a := range advisories {
			b.WriteString(render.Note("%s", a))
		}
	}
	b.WriteString(render.Section("Next"))
	if len(advisories) > 0 {
		b.WriteString(render.Hint("schooltools login   to refresh the session"))
	} else {
		b.WriteString(render.Hint("schooltools whoami   to confirm the session end-to-end"))
	}
	return b.String()
}

func checkTable(rows [][]string, tw int) string {
	return table.Render(table.Options{
		Headers: []string{"Check", "Result"},
		Widths:  table.Widths([]table.ColumnSpec{table.Fixed(22), table.Flex(30)}, tw),
		Rows:    rows,
	})
}

func existsWord(b bool) string {
	if b {
		return render.OK("found")
	}
	return render.Bad("missing")
}

func validWord(b bool) string {
	if b {
		return render.OK("valid")
	}
	return render.Bad("invalid")
}

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func modeStr(m uint32) string {
	if m == 0 {
		return "—"
	}
	return fmt.Sprintf("0%o", m)
}

func namesOrNone(names []string) string {
	if len(names) == 0 {
		return render.OK("none")
	}
	return render.Bad(strings.Join(names, ", "))
}

func probeRow(name, rawURL string, jar *cookiejar.Jar, savedCookies bool) []string {
	label := name
	if savedCookies {
		label += " (saved cookies)"
	}
	res, err := httpclient.FollowRedirects(rawURL, jar, httpclient.FetchOptions{})
	if err != nil {
		return []string{label, render.Bad("fail") + "  ·  " + err.Error()}
	}
	final := res.Request.URL.String()
	if res.StatusCode == 200 {
		return []string{label, render.OK("ok") + "  ·  200  ·  " + final}
	}
	return []string{label, render.Warn("unexpected") + "  ·  " + fmt.Sprint(res.StatusCode) + "  ·  " + final}
}
