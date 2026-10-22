package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/IamCoder18/schooltools/internal/render"
	"github.com/IamCoder18/schooltools/internal/systemd"
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
		return render.PrintJSON(map[string]any{
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
			"warnings":       st.Warnings,
			"list_timer_raw": st.TimerOutput,
		})
	}

	var b strings.Builder
	b.WriteString(render.Title("systemd · " + action))

	switch action {
	case "installed":
		b.WriteString(render.Section("Unit"))
		b.WriteString(systemdKV(st))
		b.WriteString(warningNotes(st.Warnings))
		b.WriteString(render.Section("Next"))
		b.WriteString(render.Hint("schooltools systemd status   to inspect the timer"))
		b.WriteString(render.Hint("journalctl --user -u schooltools-archive.service   for run logs"))
	case "uninstalled":
		b.WriteString(render.Section("Unit"))
		b.WriteString(render.KV(
			render.Row{Key: "Removed", Value: st.UserDir},
		))
		b.WriteString(render.Bullets("A run currently in flight will finish; subsequent timer ticks are disabled."))
		b.WriteString(warningNotes(st.Warnings))
		b.WriteString(render.Section("Next"))
		b.WriteString(render.Hint("schooltools systemd install   to set the timer back up"))
	case "status":
		if !st.Installed {
			b.WriteString(render.Empty("Not installed."))
			b.WriteString(render.Hint("schooltools systemd install   to set up the timer"))
			fmt.Print(b.String())
			return nil
		}
		b.WriteString(render.Section("Unit"))
		b.WriteString(systemdKV(st))
		if len(st.TimerOutput) > 0 {
			b.WriteString(render.Section("list-timers"))
			for _, line := range st.TimerOutput {
				b.WriteString("  " + render.Muted(line) + "\n")
			}
		}
		b.WriteString(warningNotes(st.Warnings))
		b.WriteString(render.Section("Next"))
		b.WriteString(render.Hint("journalctl --user -u schooltools-archive.service   for run logs"))
	}
	fmt.Print(b.String())
	return nil
}

func systemdKV(st systemd.Status) string {
	rows := []render.Row{
		{Key: "Unit", Value: st.UnitPath},
		{Key: "Enabled", Value: render.StatusWord(boolStr(st.Enabled))},
		{Key: "Active", Value: render.StatusWord(boolStr(st.Active))},
	}
	if st.Next != "" {
		rows = append(rows, render.Row{Key: "Next run", Value: st.Next + "  ·  in " + st.Left})
	}
	if st.Last != "" {
		v := st.Last
		if st.Passed != "" {
			v += "  ·  " + st.Passed
		}
		rows = append(rows, render.Row{Key: "Last run", Value: v})
	}
	return render.KV(rows...)
}

func warningNotes(warnings []string) string {
	if len(warnings) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(render.Section("Warnings"))
	for _, w := range warnings {
		b.WriteString(render.Note("%s", w))
	}
	return b.String()
}
