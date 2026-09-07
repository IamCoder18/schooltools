package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/session"
)

var (
	tokenJSON    bool
	tokenPath    string
)

var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Show the captured Brightspace OAuth token (formerly part of `session`)",
	Long: "Read the captured Brightspace access token from " + session.TokenPath() + "\n" +
		"and print its expiry, scope, and (when available) the decoded JWT claims.\n\n" +
		"Reserved future subcommands: `token refresh`, `token clear`. Add them\n" +
		"when a use case appears (out of scope for the 0.2.0 redesign).",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
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

func init() {
	tokenCmd.Flags().BoolVar(&tokenJSON, "json", false, "Emit the raw token record JSON")
	tokenCmd.Flags().StringVar(&tokenPath, "path", "", "Override the token file path (default: ~/.config/schooltools/token.json)")
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
