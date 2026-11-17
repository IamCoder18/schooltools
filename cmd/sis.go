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

func loadEnvOptional(envFile string) error {
	if err := env.LoadEnv(envFile); err != nil && !goerrors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

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

func logSISLoginFailure(err error) {
	code := "PS_LOGIN_ERROR"
	var coder interface{ AuthCode() string }
	if goerrors.As(err, &coder) {
		code = coder.AuthCode()
	}
	_ = authlog.Log("sis.login.failure", map[string]any{"code": code, "message": err.Error()})
}
