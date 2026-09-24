package cmd

import (
	"encoding/json"
	"errors"
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
	rootCmd.SilenceErrors = true
	rootCmd.PersistentFlags().BoolVar(&noAuthLog, "no-auth-log", false, "Disable the JSONL auth log (~/.config/schooltools/auth.log)")
	cobra.OnInitialize(func() {
		httpclient.RegisterTokenRefresher(login.TokenRefresher())
	})
}

var errJSONShown = errors.New("error shown in JSON envelope")

type jsonError struct{ Err error }

func (e *jsonError) Error() string { return e.Err.Error() }
func (e *jsonError) Unwrap() error { return e.Err }

func showJSONError(err error) error {
	if err == nil {
		return nil
	}
	out, _ := json.Marshal(map[string]any{"error": err.Error()})
	fmt.Println(string(out))
	return &jsonError{Err: err}
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		var je *jsonError
		if !errors.As(err, &je) {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}
}
