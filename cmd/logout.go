package cmd

import (
	"fmt"
	"os"

	"github.com/aarav/schooltools/internal/authlog"
	"github.com/aarav/schooltools/internal/session"
	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Delete the saved D2L session",
	RunE: func(cmd *cobra.Command, args []string) error {
		existed := false
		if _, err := os.Stat(session.SessionPath()); err == nil {
			existed = true
		}
		if err := session.Clear(); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "Session cleared.")
		_ = authlog.Log("logout", map[string]any{"saved_session_existed": existed})
		return nil
	},
}

func init() { rootCmd.AddCommand(logoutCmd) }