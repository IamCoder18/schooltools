package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/IamCoder18/schooltools/internal/authlog"
	"github.com/IamCoder18/schooltools/internal/login"
	"github.com/IamCoder18/schooltools/internal/render"
	"github.com/IamCoder18/schooltools/internal/session"
)

var (
	tokenJSON    bool
	tokenPath    string
	tokenRefresh bool
)

var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Show the captured Brightspace OAuth token (formerly part of `session`)",
	Long: "Read the captured Brightspace access token from " + session.TokenPath() + "\n" +
		"and print its expiry, scope, and (when available) the decoded JWT claims.\n\n" +
		"Pass `--refresh` to mint a fresh token using the saved session cookies\n" +
		"(KNOWN_ISSUES #14): no full SAML re-login needed when the session\n" +
		"cookies are still alive but the Brightspace bearer has expired.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if tokenRefresh {
			return runTokenRefresh()
		}
		tok, err := session.LoadToken()
		if err != nil {
			return err
		}
		if tok == nil {
			fmt.Print(render.Empty(fmt.Sprintf("No captured token at %s.", session.TokenPath())))
			fmt.Print(render.Hint("schooltools login   captures a Brightspace token on the next login"))
			return nil
		}
		if tokenJSON {
			return render.PrintJSON(redactedToken(*tok))
		}
		fmt.Print(render.Title("Token"))
		fmt.Print(render.KV(render.Row{Key: "File", Value: session.TokenPath()}))
		fmt.Print(tokenKV(*tok, time.Now()))
		fmt.Print(render.Section("Next"))
		if tok.IsExpired(time.Now()) {
			fmt.Print(render.Hint("schooltools token --refresh   mints a fresh token from the saved cookies"))
		} else {
			fmt.Print(render.Hint("schooltools session   for cookie and SAML expiry"))
		}
		return nil
	},
}

// runTokenRefresh mints a new Brightspace OAuth token using the saved session
// cookies. Solves KNOWN_ISSUES #14: no obvious way to refresh just the bearer.
func runTokenRefresh() error {
	rec, err := login.MintTokenFromSavedSession()
	if err != nil {
		_ = authlog.Log("token.refresh.failed", map[string]any{"error": err.Error()})
		return fmt.Errorf("refresh token: %w", err)
	}
	if rec == nil {
		return fmt.Errorf("refresh token: returned no record")
	}
	if err := session.SaveToken(*rec); err != nil {
		return fmt.Errorf("save token: %w", err)
	}
	_ = authlog.Log("token.refresh.ok", map[string]any{
		"path":       session.TokenPath(),
		"expires_at": rec.ExpiresAt,
		"user_id":    rec.UserID,
		"tenant_id":  rec.TenantID,
	})
	if tokenJSON {
		return render.PrintJSON(redactedToken(*rec))
	}
	fmt.Print(render.Title("Token refreshed"))
	fmt.Print(render.KV(render.Row{Key: "File", Value: session.TokenPath()}))
	fmt.Print(tokenKV(*rec, time.Now()))
	if rec.RefreshToken == "" {
		fmt.Print(render.Note("no refresh token was minted; the next `--refresh` still needs the saved session to be alive"))
	}
	return nil
}

func init() {
	tokenCmd.Flags().BoolVar(&tokenJSON, "json", false, "Emit the token record as JSON (access and refresh tokens redacted)")
	tokenCmd.Flags().StringVar(&tokenPath, "path", "", "Override the token file path (default: ~/.config/schooltools/token.json)")
	tokenCmd.Flags().BoolVar(&tokenRefresh, "refresh", false, "Mint a fresh Brightspace token using the saved session cookies")
	rootCmd.AddCommand(tokenCmd)
}

func tokenKV(tok session.TokenRecord, now time.Time) string {
	rows := []render.Row{}
	if tok.TenantID != "" || tok.UserID != "" {
		rows = append(rows, render.Row{Key: "Tenant/user", Value: fmt.Sprintf("%s / %s", tok.TenantID, tok.UserID)})
	}
	if tok.Issuer != "" {
		rows = append(rows, render.Row{Key: "Issuer", Value: tok.Issuer})
	}
	if tok.Scope != "" {
		rows = append(rows, render.Row{Key: "Scope", Value: tok.Scope})
	}
	if !tok.IssuedAt.IsZero() {
		rows = append(rows, render.Row{Key: "Issued", Value: render.When(isoOf(tok.IssuedAt))})
	}
	switch {
	case tok.ExpiresAt.IsZero():
		rows = append(rows, render.Row{Key: "Expires", Value: render.Muted("no expiry recorded")})
	case tok.IsExpired(now):
		rows = append(rows, render.Row{Key: "Expires", Value: expiryPhrase(tok.ExpiresAt, now) + "  ·  re-login to refresh"})
	default:
		rows = append(rows, render.Row{Key: "Expires", Value: expiryPhrase(tok.ExpiresAt, now)})
	}
	return render.KV(rows...)
}

func redactedToken(tok session.TokenRecord) session.TokenRecord {
	tok.AccessToken = "[redacted]"
	if tok.RefreshToken != "" {
		tok.RefreshToken = "[redacted]"
	}
	return tok
}
