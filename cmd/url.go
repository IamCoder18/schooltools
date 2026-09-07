package cmd

import (
	"fmt"

	"github.com/aarav/schooltools/internal/authlog"
	"github.com/aarav/schooltools/internal/httpclient"
	"github.com/aarav/schooltools/internal/ua"
	"github.com/spf13/cobra"
)

var urlCmd = &cobra.Command{
	Use:   "url",
	Short: "Print the ADFS SAML login URL that the D2L login redirects to",
	RunE: func(cmd *cobra.Command, args []string) error {
		url, _ := cmd.Flags().GetString("url")
		jar, err := httpclient.NewJar()
		if err != nil {
			return err
		}
		res, err := httpclient.Fetch(url, jar, httpclient.FetchOptions{
			Headers: map[string]string{"User-Agent": ua.UserAgent},
		})
		if err != nil {
			return err
		}
		defer func() { _ = res.Body.Close() }()
		if !httpclient.IsRedirect(res.StatusCode) {
			return fmt.Errorf("expected a redirect, got %d %s", res.StatusCode, res.Status)
		}
		next, err := res.Location()
		if err != nil {
			return fmt.Errorf("no Location header in redirect response: %v", err)
		}
		fmt.Println(next.String())
		_ = authlog.Log("url.printed", map[string]any{"final": next.String()})
		return nil
	},
}

func init() {
	urlCmd.Flags().StringP("url", "u", ua.LoginEndpoint, "Override the D2L login endpoint")
	rootCmd.AddCommand(urlCmd)
}