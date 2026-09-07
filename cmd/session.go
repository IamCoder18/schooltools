package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/session"
)

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Show session cookie expiry, captured SAML window, and Brightspace token",
	RunE: func(cmd *cobra.Command, args []string) error {
		ex, err := session.SessionExpiry()
		if err != nil {
			return err
		}
		printExpiry(ex)
		printToken()
		return nil
	},
}

func init() { rootCmd.AddCommand(sessionCmd) }

func printExpiry(ex session.Expiry) {
	fmt.Fprintf(os.Stdout, "Session cookies (%d):\n", len(ex.PerCookie))
	for _, c := range ex.PerCookie {
		fmt.Fprintf(os.Stdout, "  %-22s domain=%s\n", c.Name, c.Domain)
		if c.IsSession {
			fmt.Fprintf(os.Stdout, "      session-only (no Expires; cleared when browser closes)\n")
			continue
		}
		if c.IsExpired {
			fmt.Fprintf(os.Stdout, "      EXPIRED at %s\n", c.Expires.UTC().Format(time.RFC3339))
			continue
		}
		fmt.Fprintf(os.Stdout, "      expires %s   (in %s)\n",
			c.Expires.UTC().Format(time.RFC3339), humanize(c.Remaining))
	}

	switch {
	case ex.Soonest.IsZero() && !ex.HasSession:
		fmt.Fprintln(os.Stdout, "\nNo session cookies found. Run 'schooltools login'.")
	case ex.Soonest.IsZero():
		fmt.Fprintln(os.Stdout, "\nActual session expiry: (no Expires on any cookie; browser-session only)")
	default:
		fmt.Fprintf(os.Stdout, "\nActual session expires: %s (in %s, driven by %s)\n",
			ex.Soonest.UTC().Format(time.RFC3339), humanize(ex.Soonest.Sub(ex.Now)), ex.SoonestName)
	}

	if ex.SAMLAvailable {
		remaining := ex.SAMLNotAfter.Sub(ex.Now)
		if remaining < 0 {
			fmt.Fprintf(os.Stdout, "SAML assertion: EXPIRED at %s (from saml-last.json)\n",
				ex.SAMLNotAfter.UTC().Format(time.RFC3339))
		} else {
			fmt.Fprintf(os.Stdout, "SAML assertion valid until: %s (in %s, from saml-last.json)\n",
				ex.SAMLNotAfter.UTC().Format(time.RFC3339), humanize(remaining))
		}
	} else {
		fmt.Fprintln(os.Stdout, "SAML assertion: (no saml-last.json; will be captured on next login)")
	}
}

// printToken shows the captured Brightspace OAuth token expiry and claims.
// Reads token.json directly; the token is also exposed via the
// `schooltools token show` command for scripting.
func printToken() {
	tok, err := session.LoadToken()
	if err != nil {
		fmt.Fprintf(os.Stdout, "\nBrightspace token: error reading %s: %v\n", session.TokenPath(), err)
		return
	}
	if tok == nil {
		fmt.Fprintln(os.Stdout, "\nBrightspace token: (none — will be captured on next login)")
		return
	}
	fmt.Fprintln(os.Stdout, "\nBrightspace token:")
	if tok.TenantID != "" || tok.UserID != "" {
		fmt.Fprintf(os.Stdout, "  tenant/user: %s / %s\n", tok.TenantID, tok.UserID)
	}
	if tok.Issuer != "" {
		fmt.Fprintf(os.Stdout, "  issuer:      %s\n", tok.Issuer)
	}
	if tok.Scope != "" {
		fmt.Fprintf(os.Stdout, "  scope:       %s\n", tok.Scope)
	}
	if tok.IsExpired(time.Now()) {
		fmt.Fprintf(os.Stdout, "  EXPIRED at %s (re-login to refresh)\n",
			tok.ExpiresAt.UTC().Format(time.RFC3339))
	} else {
		fmt.Fprintf(os.Stdout, "  expires:     %s   (in %s)\n",
			tok.ExpiresAt.UTC().Format(time.RFC3339), humanize(tok.Remaining(time.Now())))
	}
}

func humanize(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	days := int(d / (24 * time.Hour))
	hours := int(d/time.Hour) % 24
	return fmt.Sprintf("%dd %dh", days, hours)
}
