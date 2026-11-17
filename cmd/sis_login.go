package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/ps"
)

var sisLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate against PowerSchool MyCBE and persist the SIS session",
	Long: "Sign in to sis.cbe.ab.ca using SIS_STUDENT_NUMBER (or CBE_EMAIL) +\n" +
		"SIS_PASSWORD (or CBE_PASSWORD) from .env and persist the four\n" +
		"session cookies (JSESSIONID, psaid, currentSchool, current_locale)\n" +
		"to ~/.config/schooltools/sis-session.json.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		envFile, _ := cmd.Flags().GetString("env-file")
		records, err := sisLoginRunner(envFile)
		if err != nil {
			return err
		}
		if err := ps.Save(records); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Saved %d PowerSchool cookie(s) to %s\n", len(records), ps.SessionPath())
		return nil
	},
}

func init() { sisCmd.AddCommand(sisLoginCmd) }
