package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/IamCoder18/schooltools/internal/render"
	"github.com/IamCoder18/schooltools/internal/session"
	"github.com/IamCoder18/schooltools/internal/table"
)

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Show session cookie expiry, captured SAML window, and Brightspace token",
	RunE: func(cmd *cobra.Command, args []string) error {
		ex, err := session.SessionExpiry()
		if err != nil {
			return err
		}
		fmt.Print(sessionReport(ex))
		return nil
	},
}

func init() { rootCmd.AddCommand(sessionCmd) }

func isoOf(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func whenOf(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return render.When(isoOf(t))
}

func expiryPhrase(t time.Time, now time.Time) string {
	if t.Before(now) {
		return render.Bad("expired "+render.RelAt(t, now)) + "  ·  " + render.DateTime(isoOf(t))
	}
	return "expires " + render.RelAt(t, now) + "  ·  " + render.DateTime(isoOf(t))
}

func sessionReport(ex session.Expiry) string {
	now := ex.Now
	tok, tokErr := session.LoadToken()

	var b strings.Builder
	b.WriteString(render.Title("Session"))

	b.WriteString(render.Section("Summary"))
	rows := []render.Row{}
	switch {
	case ex.Soonest.IsZero() && !ex.HasSession:
		rows = append(rows, render.Row{Key: "Expires", Value: render.Bad("no session cookies found")})
	case ex.Soonest.IsZero():
		rows = append(rows, render.Row{Key: "Expires", Value: render.Muted("browser-session only (no Expires; cleared when the browser closes)")})
	default:
		rows = append(rows, render.Row{Key: "Expires", Value: expiryPhrase(ex.Soonest, now) + "  ·  driven by " + ex.SoonestName})
	}
	rows = append(rows, render.Row{Key: "Cookies", Value: render.Count(len(ex.PerCookie), "cookie")})
	if ex.SAMLAvailable {
		rows = append(rows, render.Row{Key: "SAML", Value: expiryPhrase(ex.SAMLNotAfter, now) + "  ·  from saml-last.json"})
	} else {
		rows = append(rows, render.Row{Key: "SAML", Value: render.Muted("none — captured on the next login")})
	}
	switch {
	case tokErr != nil:
		rows = append(rows, render.Row{Key: "Token", Value: render.Bad("error reading " + session.TokenPath())})
	case tok == nil:
		rows = append(rows, render.Row{Key: "Token", Value: render.Muted("none — captured on the next login")})
	case tok.IsExpired(now):
		rows = append(rows, render.Row{Key: "Token", Value: expiryPhrase(tok.ExpiresAt, now) + "  ·  re-login to refresh"})
	default:
		rows = append(rows, render.Row{Key: "Token", Value: expiryPhrase(tok.ExpiresAt, now)})
	}
	b.WriteString(render.KV(rows...))

	b.WriteString(render.Section("Cookies"))
	if len(ex.PerCookie) == 0 {
		b.WriteString(render.Empty("No session cookies found."))
	} else {
		crows := make([][]string, 0, len(ex.PerCookie))
		for _, c := range ex.PerCookie {
			expires, status := "—", render.Muted("session-only")
			if !c.IsSession {
				expires = render.DateTime(isoOf(c.Expires))
				if c.IsExpired {
					status = render.Bad("expired") + "  ·  " + render.RelAt(c.Expires, now)
				} else {
					status = render.OK("valid") + "  ·  expires " + render.RelAt(c.Expires, now)
				}
			}
			crows = append(crows, []string{c.Name, c.Domain, expires, status})
		}
		b.WriteString(table.Render(table.Options{
			Headers: []string{"Cookie", "Domain", "Expires", "Status"},
			Widths:  table.Widths([]table.ColumnSpec{table.Fixed(24), table.Fixed(16), table.Fixed(24), table.Flex(20)}, table.TerminalWidth()),
			Rows:    crows,
		}))
	}

	b.WriteString(render.Section("Brightspace token"))
	switch {
	case tokErr != nil:
		b.WriteString(render.Empty("Could not read " + session.TokenPath() + "."))
	case tok == nil:
		b.WriteString(render.Empty("No captured token — it will be captured on the next login."))
	default:
		b.WriteString(tokenKV(*tok, now))
	}

	b.WriteString(render.Section("Next"))
	switch {
	case ex.Soonest.IsZero() && !ex.HasSession:
		b.WriteString(render.Hint("schooltools login   to start a fresh session"))
	case tok != nil && tok.IsExpired(now) && tok.RefreshToken == "":
		b.WriteString(render.Hint("schooltools login   to capture a fresh Brightspace token"))
	case tok != nil && tok.IsExpired(now):
		b.WriteString(render.Hint("schooltools token --refresh   mints a fresh token from the saved cookies"))
	default:
		b.WriteString(render.Hint("schooltools doctor   to probe session and network health"))
	}
	return b.String()
}
