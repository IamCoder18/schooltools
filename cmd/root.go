package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/httpclient"
	"github.com/aarav/schooltools/internal/login"
)

var (
	noAuthLog bool
)

var rootCmd = &cobra.Command{
	Use:     "schooltools",
	Short:   "CLI tools for CBE / D2L / Brightspace",
	Version: "0.2.0",
}

func init() {
	rootCmd.SetVersionTemplate("schooltools version {{.Version}}\n")
	rootCmd.SilenceUsage = true
	rootCmd.PersistentFlags().BoolVar(&noAuthLog, "no-auth-log", false, "Disable the JSONL auth log (~/.config/schooltools/auth.log)")
	cobra.OnInitialize(func() {
		// Wire the Bearer-token auto-refresh path: any 401 from a Brightspace
		// API call re-mints via /d2l/lp/auth/oauth2/token using the saved LMS
		// session cookies (no full SAML re-login) as long as those cookies
		// are still valid.
		httpclient.RegisterTokenRefresher(login.TokenRefresher())
	})
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}