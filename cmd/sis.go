package cmd

import (
	goerrors "errors"
	"fmt"
	"io/fs"
	"net/http/cookiejar"

	"github.com/spf13/cobra"

	"github.com/IamCoder18/schooltools/internal/authlog"
	"github.com/IamCoder18/schooltools/internal/cookies"
	"github.com/IamCoder18/schooltools/internal/env"
	"github.com/IamCoder18/schooltools/internal/ps"
)

// sisCmd is the parent for every PowerSchool MyCBE subcommand. All
// subcommands share the same on-disk SIS session at
// ~/.config/schooltools/sis-session.json, which is independent of the
// D2L session.json — the two systems use different cookie sets and
// expiries.
var sisCmd = &cobra.Command{
	Use:   "sis",
	Short: "PowerSchool MyCBE parent/student portal (sis.cbe.ab.ca)",
	Long: "Access the Calgary Board of Education's PowerSchool SIS parent\n" +
		"and student portal (https://sis.cbe.ab.ca). The portal is an Angular\n" +
		"SPA on top of PowerSchool 21.06 / PDS 31.0.3 with internal REST\n" +
		"endpoints under /ws/xte/....\n\n" +
		"Login uses the student number (NOT email) + password. CBE customised\n" +
		"PowerSchool's doPCASLogin to send plaintext passwords, so no HMAC\n" +
		"or pskey is required.\n\n" +
		"Use SIS_STUDENT_NUMBER + SIS_PASSWORD in .env (falls back to\n" +
		"CBE_EMAIL + CBE_PASSWORD).",
	Args: cobra.NoArgs,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		envFile, _ := cmd.Flags().GetString("env-file")
		return loadEnvOptional(envFile)
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	sisCmd.PersistentFlags().StringP("env-file", "e", ".env", "Path to .env (read for SIS_STUDENT_NUMBER / SIS_PASSWORD)")
	sisCmd.PersistentFlags().Bool("auto-refresh", true, "Re-run 'sis login' automatically when the saved session is missing or the portal rejects it")
	sisCmd.PersistentFlags().Bool("json", false, "Emit machine-readable JSON instead of a table")
	rootCmd.AddCommand(sisCmd)
}

// loadEnvOptional loads the .env file if it exists. A missing file is
// not fatal: every read-only `sis` subcommand works from the saved
// session, and only `sis login` (and the 401/403 re-auth path) actually
// needs credentials. Any other load error is returned.
func loadEnvOptional(envFile string) error {
	if err := env.LoadEnv(envFile); err != nil && !goerrors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// sisClient builds a PowerSchool client for a subcommand, honouring
// --auto-refresh and wiring the 401/403 re-auth hook.
//
// Every `sis` subcommand goes through here so the session policy is
// defined in exactly one place. On the valid-session path no login
// runs; when the portal later answers 401/403 the hook re-authenticates
// once and retries, which is the only way to recover a session that
// PowerSchool revoked server-side (psaid carries no Expires, so the
// local check never notices).
func sisClient(cmd *cobra.Command) (*ps.Client, error) {
	envFile, _ := cmd.Flags().GetString("env-file")
	autoRefresh, _ := cmd.Flags().GetBool("auto-refresh")

	loginFn := func() ([]cookies.Record, error) { return sisLoginRunner(envFile) }
	if !autoRefresh {
		loginFn = nil
	}

	jar, err := ps.EnsureSession(ps.EnsureSessionOptions{
		AutoRefresh: autoRefresh,
		LoginFn:     loginFn,
	})
	if err != nil {
		return nil, err
	}

	client := ps.NewClient(jar)
	if autoRefresh {
		client.SetReauth(func() (*cookiejar.Jar, error) {
			_ = authlog.Log("sis.reauth", map[string]any{"reason": "portal returned 401/403"})
			return ps.Reauthenticate(loginFn)
		})
	}
	return client, nil
}

// resolveYearID returns the school year to query. A non-zero override
// wins; otherwise the newest year from the portal's year picker is
// used. Every `sis` subcommand funnels through this so they cannot
// disagree about which year they are reporting.
func resolveYearID(client *ps.Client, dcid int64, override int) (int, error) {
	if override != 0 {
		return override, nil
	}
	years, err := client.GetYears(dcid)
	if err != nil {
		return 0, err
	}
	if len(years) == 0 {
		return 0, fmt.Errorf("year picker returned no entries for this student; pass --year <id> to select one explicitly")
	}
	latest := years[0].YearID
	for _, y := range years[1:] {
		if y.YearID > latest {
			latest = y.YearID
		}
	}
	return latest, nil
}

// sisLoginRunner is the dependency-injected login function used by
// every authenticated `sis` subcommand, and by `sis login` itself. It
// lives in cmd so internal/ps doesn't import internal/env /
// internal/authlog.
//
// It logs the attempt and its outcome; persisting the resulting session
// is the caller's job, because the auto-refresh path persists through
// ps.Reauthenticate.
func sisLoginRunner(envFile string) ([]cookies.Record, error) {
	if err := env.LoadEnv(envFile); err != nil {
		return nil, err
	}
	creds, err := env.GetSISCreds()
	if err != nil {
		return nil, err
	}
	_ = authlog.Log("sis.login.start", map[string]any{"account": creds.Account})
	records, err := ps.Login(ps.LoginOptions{
		Account:  creds.Account,
		Password: creds.Password,
	})
	if err != nil {
		logSISLoginFailure(err)
		return nil, err
	}
	_ = authlog.Log("sis.login.success", map[string]any{"cookie_count": len(records)})
	return records, nil
}

// logSISLoginFailure records a failed PowerSchool login using the
// error's machine-readable code. *errors.AuthError carries a Code that
// nothing but this assertion exposes, so it must unwrap rather than
// type-assert directly.
func logSISLoginFailure(err error) {
	code := "PS_LOGIN_ERROR"
	var coder interface{ AuthCode() string }
	if goerrors.As(err, &coder) {
		code = coder.AuthCode()
	}
	_ = authlog.Log("sis.login.failure", map[string]any{"code": code, "message": err.Error()})
}
