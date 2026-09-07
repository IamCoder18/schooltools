package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/systemd"
)

var (
	systemdExecStart  string
	systemdForce      bool
	systemdJSONOutput bool
)

var systemdCmd = &cobra.Command{
	Use:   "systemd",
	Short: "Install, remove, or inspect the schooltools systemd units (opt-in)",
	Long: "Manage the user-level systemd units that drive periodic `schooltools archive`\n" +
		"runs. This is purely opt-in — running `schooltools systemd install` is the\n" +
		"only way the units land in ~/.config/systemd/user/, and nothing happens on\n" +
		"`schooltools archive` invocations unless you also enable the timer.\n\n" +
		"Sub-commands:\n" +
		"  install    write units, daemon-reload, enable --now the timer\n" +
		"  uninstall  disable --now the timer, remove unit files\n" +
		"  status     show whether the timer is installed/enabled/active and when it next fires",
}

var systemdInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install and enable the systemd timer (opt-in)",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := systemd.Install(systemd.InstallOptions{
			ExecStart: systemdExecStart,
			Force:     systemdForce,
		})
		if err != nil {
			return err
		}
		return printSystemdStatus("installed", st)
	},
}

var systemdUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Disable the timer and remove the unit files",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := systemd.Uninstall()
		if err != nil {
			return err
		}
		return printSystemdStatus("uninstalled", st)
	},
}

var systemdStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show whether the timer is installed, enabled, and active",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := systemd.Query()
		if err != nil {
			return err
		}
		return printSystemdStatus("status", st)
	},
}

func init() {
	systemdCmd.PersistentFlags().StringVar(&systemdExecStart, "exec-start", "",
		"Override ExecStart in the service unit (default: <current binary> archive)")
	systemdCmd.PersistentFlags().BoolVar(&systemdForce, "force", false,
		"Overwrite existing unit files when installing")
	systemdCmd.PersistentFlags().BoolVar(&systemdJSONOutput, "json", false,
		"Emit JSON instead of a human-readable status block")

	systemdCmd.AddCommand(systemdInstallCmd)
	systemdCmd.AddCommand(systemdUninstallCmd)
	systemdCmd.AddCommand(systemdStatusCmd)

	rootCmd.AddCommand(systemdCmd)
}

// printSystemdStatus renders the result of an install/uninstall/status run.
// JSON output goes to stdout; human output goes to stdout too (the parent
// `schooltools` process writes status to stderr only for errors).
func printSystemdStatus(action string, st systemd.Status) error {
	if systemdJSONOutput {
		out, err := json.MarshalIndent(map[string]any{
			"action":         action,
			"installed":      st.Installed,
			"enabled":        st.Enabled,
			"active":         st.Active,
			"next":           st.Next,
			"left":           st.Left,
			"last":           st.Last,
			"passed":         st.Passed,
			"unit":           st.Unit,
			"activates":      st.Activates,
			"unit_path":      st.UnitPath,
			"user_dir":       st.UserDir,
			"list_timer_raw": st.TimerOutput,
		}, "", "  ")
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(os.Stdout, string(out))
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer func() { _ = tw.Flush() }()

	switch action {
	case "installed":
		_, _ = fmt.Fprintf(tw, "Installed:\t%s\n", st.UnitPath)
		_, _ = fmt.Fprintf(tw, "Enabled:\t%v\n", st.Enabled)
		_, _ = fmt.Fprintf(tw, "Active:\t%v\n", st.Active)
		if st.Next != "" {
			_, _ = fmt.Fprintf(tw, "Next run:\t%s (in %s)\n", st.Next, st.Left)
		}
		if st.Last != "" {
			_, _ = fmt.Fprintf(tw, "Last run:\t%s (%s)\n", st.Last, st.Passed)
		}
		_, _ = fmt.Fprintln(tw, "")
		_, _ = fmt.Fprintln(tw, "Run `schooltools systemd status` any time to inspect the timer.")
		_, _ = fmt.Fprintln(tw, "Inspect logs with: journalctl --user -u schooltools-archive.service")
	case "uninstalled":
		_, _ = fmt.Fprintln(tw, "Uninstalled. Unit files removed from "+st.UserDir+".")
		_, _ = fmt.Fprintln(tw, "If a run is currently in-flight it will finish; subsequent timer ticks are disabled.")
	case "status":
		if !st.Installed {
			_, _ = fmt.Fprintln(tw, "Not installed. Run `schooltools systemd install` to set up the timer.")
			return nil
		}
		_, _ = fmt.Fprintf(tw, "Unit path:\t%s\n", st.UnitPath)
		_, _ = fmt.Fprintf(tw, "Enabled:\t%v\n", st.Enabled)
		_, _ = fmt.Fprintf(tw, "Active:\t%v\n", st.Active)
		if st.Next != "" {
			_, _ = fmt.Fprintf(tw, "Next run:\t%s (in %s)\n", st.Next, st.Left)
		}
		if st.Last != "" {
			_, _ = fmt.Fprintf(tw, "Last run:\t%s (%s)\n", st.Last, st.Passed)
		}
		if len(st.TimerOutput) > 0 {
			_, _ = fmt.Fprintln(tw, "")
			_, _ = fmt.Fprintln(tw, "Raw `systemctl --user list-timers` output:")
			for _, line := range st.TimerOutput {
				_, _ = fmt.Fprintln(tw, "  "+line)
			}
		}
	}
	return nil
}

// ensure time is used (kept here so go vet doesn't complain if all imports
// become unused in some future refactor).
var _ = time.Now
