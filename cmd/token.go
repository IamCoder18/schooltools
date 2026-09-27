package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/authlog"
	"github.com/aarav/schooltools/internal/login"
	"github.com/aarav/schooltools/internal/session"
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
			fmt.Printf("No captured token at %s. Run `schooltools login` to capture one.\n", session.TokenPath())
			return nil
		}
		if tokenJSON {
			b, _ := json.MarshalIndent(tok, "", "  ")
			fmt.Println(string(b))
			return nil
		}
		printTokenRecord(*tok, time.Now())
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
		b, _ := json.MarshalIndent(rec, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if !rec.ExpiresAt.IsZero() {
		_, _ = fmt.Fprintf(os.Stdout, "Refreshed Brightspace token (valid until %s, user %s, tenant %s) -> %s\n",
			rec.ExpiresAt.Format(time.RFC3339), rec.UserID, rec.TenantID, session.TokenPath())
	} else {
		_, _ = fmt.Fprintf(os.Stdout, "Refreshed Brightspace token -> %s\n", session.TokenPath())
	}
	printTokenRecord(*rec, time.Now())
	return nil
}

func init() {
	tokenCmd.Flags().BoolVar(&tokenJSON, "json", false, "Emit the raw token record JSON")
	tokenCmd.Flags().StringVar(&tokenPath, "path", "", "Override the token file path (default: ~/.config/schooltools/token.json)")
	tokenCmd.Flags().BoolVar(&tokenRefresh, "refresh", false, "Mint a fresh Brightspace token using the saved session cookies")
	rootCmd.AddCommand(tokenCmd)
}

func printTokenRecord(tok session.TokenRecord, now time.Time) {
	fmt.Printf("Token file: %s\n", session.TokenPath())
	if tok.TenantID != "" || tok.UserID != "" {
		fmt.Printf("  tenant/user: %s / %s\n", tok.TenantID, tok.UserID)
	}
	if tok.Issuer != "" {
		fmt.Printf("  issuer:      %s\n", tok.Issuer)
	}
	if tok.Scope != "" {
		fmt.Printf("  scope:       %s\n", tok.Scope)
	}
	if !tok.IssuedAt.IsZero() {
		fmt.Printf("  issued:      %s\n", tok.IssuedAt.UTC().Format(time.RFC3339))
	}
	if tok.IsExpired(now) {
		_, _ = fmt.Fprintf(os.Stdout, "  EXPIRED at %s (re-login to refresh)\n",
			tok.ExpiresAt.UTC().Format(time.RFC3339))
	} else if !tok.ExpiresAt.IsZero() {
		_, _ = fmt.Fprintf(os.Stdout, "  expires:     %s   (in %s)\n",
			tok.ExpiresAt.UTC().Format(time.RFC3339), humanize(tok.Remaining(now)))
	}
}
