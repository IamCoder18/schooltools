package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/authlog"
	"github.com/aarav/schooltools/internal/ps"
)

var sisLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Delete the saved PowerSchool session",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		existed := false
		if _, err := os.Stat(ps.SessionPath()); err == nil {
			existed = true
		}
		if err := ps.Clear(); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "PowerSchool session cleared.")
		_ = authlog.Log("sis.logout", map[string]any{"saved_session_existed": existed})
		return nil
	},
}

func init() { sisCmd.AddCommand(sisLogoutCmd) }
