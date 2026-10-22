package cmd

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/authlog"
	"github.com/aarav/schooltools/internal/cookies"
	"github.com/aarav/schooltools/internal/dupdata"
	"github.com/aarav/schooltools/internal/env"
	stderrors "github.com/aarav/schooltools/internal/errors"
	"github.com/aarav/schooltools/internal/httpclient"
	"github.com/aarav/schooltools/internal/login"
	"github.com/aarav/schooltools/internal/render"
	"github.com/aarav/schooltools/internal/session"
	"github.com/aarav/schooltools/internal/ua"
)

type whoamiUser struct {
	Identifier        string `json:"Identifier"`
	FirstName         string `json:"FirstName"`
	LastName          string `json:"LastName"`
	Pronouns          string `json:"Pronouns"`
	UniqueName        string `json:"UniqueName"`
	ProfileIdentifier string `json:"ProfileIdentifier"`
}

func (u whoamiUser) displayName() string {
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if name == "" {
		return "(unknown)"
	}
	return name
}

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Check the saved D2L session by fetching /d2l/home",
	RunE: func(cmd *cobra.Command, args []string) error {
		envFile, _ := cmd.Flags().GetString("env-file")
		autoRefresh, _ := cmd.Flags().GetBool("auto-refresh")
		asJSON, _ := cmd.Flags().GetBool("json")

		// First attempt: load + maybe auto-refresh.
		ens, err := session.EnsureSession(session.EnsureOptions{
			EnvFile:     envFile,
			AutoRefresh: autoRefresh,
			KMSI:        true,
			LoginFn:     loginFn,
		})
		if err != nil {
			// Map to the legacy WhoamiError exit codes.
			msg := err.Error()
			if strings.HasPrefix(msg, "no saved session") {
				os.Exit(2)
			}
			if strings.HasPrefix(msg, "saved session cookies are all expired") {
				os.Exit(3)
			}
			return err
		}

		// Fetch home and detect login-page redirect (which means session expired).
		res, err := fetchHome(ens.Jar)
		if err != nil {
			if !autoRefresh {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(3)
			}
			if !refreshAndLog(envFile) {
				os.Exit(3)
			}
			ens.Jar, _ = session.LoadJar()
			res, err = fetchHome(ens.Jar)
			if err != nil {
				return err
			}
		}
		if ua.IsLoginURL(res.Request.URL.String()) {
			_ = session.Clear()
			_ = authlog.Log("session.invalid", map[string]any{"reason": "redirect_to_login"})
			if !autoRefresh {
				fmt.Fprintf(os.Stderr, "Session expired (landed on %s). Run 'schooltools login' again.\n", res.Request.URL.String())
				os.Exit(3)
			}
			if !refreshAndLog(envFile) {
				os.Exit(3)
			}
			ens.Jar, _ = session.LoadJar()
			res, err = fetchHome(ens.Jar)
			if err != nil {
				return err
			}
		}
		landed := res.Request.URL.String()
		_ = res.Body.Close()

		user, uerr := fetchWhoami(ens.Jar)

		if asJSON {
			out := map[string]any{
				"status":             "logged_in",
				"user":               user.displayName(),
				"landed":             landed,
				"identifier":         user.Identifier,
				"unique_name":        user.UniqueName,
				"first_name":         user.FirstName,
				"last_name":          user.LastName,
				"pronouns":           user.Pronouns,
				"profile_identifier": user.ProfileIdentifier,
			}
			return render.PrintJSON(out)
		}

		fmt.Print(render.Title(user.displayName()))
		fmt.Print(render.Section("Identity"))
		rows := []render.Row{
			{Key: "Status", Value: render.OK("logged in")},
			{Key: "Name", Value: user.displayName()},
		}
		if user.Pronouns != "" {
			rows = append(rows, render.Row{Key: "Pronouns", Value: user.Pronouns})
		}
		if user.UniqueName != "" {
			rows = append(rows, render.Row{Key: "Unique name", Value: user.UniqueName})
		}
		if user.Identifier != "" {
			rows = append(rows, render.Row{Key: "User ID", Value: user.Identifier})
		}
		if user.ProfileIdentifier != "" {
			rows = append(rows, render.Row{Key: "Profile ID", Value: user.ProfileIdentifier})
		}
		fmt.Print(render.KV(rows...))
		if uerr != nil {
			fmt.Print(render.Note("identity is incomplete: %s", uerr))
		}
		fmt.Print(render.Section("Session"))
		fmt.Print(render.KV(
			render.Row{Key: "Landed on", Value: landed},
		))
		fmt.Print(render.Section("Next"))
		fmt.Print(render.Hint("schooltools session   for cookie, SAML, and token expiry"))
		return nil
	},
}

func init() {
	whoamiCmd.Flags().StringP("env-file", "e", ".env", "Path to .env for auto-refresh")
	whoamiCmd.Flags().Bool("auto-refresh", false, "If the session is expired, re-run 'login' automatically")
	whoamiCmd.Flags().Bool("json", false, "Emit the parsed session info as JSON instead of a table")
	rootCmd.AddCommand(whoamiCmd)
}

func fetchHome(jar *cookiejar.Jar) (*http.Response, error) {
	res, err := httpclient.FollowRedirects(ua.D2LBase+"/d2l/home", jar, httpclient.FetchOptions{})
	if err != nil {
		return nil, err
	}
	if ua.IsLoginURL(res.Request.URL.String()) {
		return res, fmt.Errorf("landed on %s", res.Request.URL.String())
	}
	return res, nil
}

func fetchWhoami(jar *cookiejar.Jar) (whoamiUser, error) {
	var u whoamiUser
	raw := fmt.Sprintf("%s/d2l/api/lp/%s/users/whoami", ua.D2LBase, dupdata.LPVersion())
	err := dupdata.FetchJSON(raw, jar, &u)
	return u, err
}

// loginFn is the dependency-injected login runner for session.EnsureSession.
// It lives in cmd so we don't import internal/login from internal/session.
func loginFn(envFile string, kmsi bool) ([]cookies.Record, error) {
	if err := env.LoadEnv(envFile); err != nil {
		return nil, err
	}
	res, err := login.Run(login.Options{EnvFile: envFile, Verbose: false, KMSI: kmsi})
	if err != nil {
		return nil, err
	}
	return res.Cookies, nil
}

func refreshAndLog(envFile string) bool {
	if err := env.LoadEnv(envFile); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return false
	}
	_ = authlog.Log("reauth.start", map[string]any{"reason": "session_invalid", "kmsi": true})
	res, err := login.Run(login.Options{EnvFile: envFile, Verbose: false, KMSI: true})
	if err != nil {
		// Retry once on SamlConsumedError.
		if _, ok := err.(*stderrors.SamlConsumedError); ok {
			fmt.Fprintln(os.Stderr, "Auto-refresh: SAML consumed; retrying once.")
			res, err = login.Run(login.Options{EnvFile: envFile, Verbose: false, KMSI: true})
		}
		if err != nil {
			code := "LOGIN_ERROR"
			type coder interface{ AuthCode() string }
			if c, ok := err.(coder); ok {
				code = c.AuthCode()
			}
			_ = authlog.Log("reauth.failure", map[string]any{"code": code, "message": err.Error()})
			fmt.Fprintln(os.Stderr, err)
			return false
		}
	}
	if err := session.Save(res.Cookies); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return false
	}
	_ = authlog.Log("reauth.success", map[string]any{})
	return true
}
