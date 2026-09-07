// Package systemd installs, removes, and inspects the schooltools systemd
// units that drive periodic archive runs. The .service and .timer unit files
// are embedded at compile time via go:embed so the binary ships with them
// self-contained.
package systemd

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed units/*.tmpl
var unitTemplates embed.FS

// Unit names are exported so callers can refer to them in messages and in
// documentation without duplicating string literals.
const (
	ServiceName = "schooltools-archive.service"
	TimerName   = "schooltools-archive.timer"
	UserDir     = ".config/systemd/user"
)

// InstallOptions configures an install run. ExecStart is the full ExecStart
// line written into the service unit (typically "/usr/bin/schooltools archive"
// or the resolved path of the current binary).
type InstallOptions struct {
	// ExecStart is the command line to run. If empty, Install tries to
	// resolve the path of the currently running binary via os.Executable().
	ExecStart string

	// Force overwrites existing unit files if true. Default is to fail if
	// the units are already installed, so the user gets a clear message.
	Force bool
}

// Status is the structured result of an install/remove/status run.
type Status struct {
	Installed     bool     // true if the .service file is on disk
	Enabled       bool     // true if systemctl says the timer is enabled
	Active         bool     // true if the timer is currently active
	Next          string   // next scheduled run, formatted by systemctl
	Left          string   // relative time until Next (e.g., "20min")
	Last          string   // last run, if any
	Passed        string   // relative time since Last (e.g., "9min ago")
	Unit          string   // timer unit name from list-timers
	Activates     string   // service unit name from list-timers
	UnitPath      string   // absolute path to the .service file
	UserDir       string   // ~/.config/systemd/user
	TimerOutput   []string // raw `systemctl --user list-timers` lines
	ListTimerExit int      // exit code from systemctl
}

// renderExecStart returns the ExecStart string to bake into the service unit.
// Prefers the caller's override; falls back to the absolute path of the
// currently running binary followed by ` archive`. Returns a non-empty warning
// when the resolved path is in a volatile location like /tmp.
func renderExecStart(override string) (string, []string, error) {
	if strings.TrimSpace(override) != "" {
		return override, nil, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", nil, fmt.Errorf("locate binary: %w", err)
	}
	abs, err := filepath.Abs(exe)
	if err != nil {
		return "", nil, err
	}
	var warnings []string
	if isVolatilePath(abs) {
		warnings = append(warnings,
			fmt.Sprintf("binary lives at %s which may not survive reboot — "+
				"install to /usr/local/bin or ~/.local/bin for a stable unit", abs))
	}
	return abs + " archive", warnings, nil
}

// isVolatilePath returns true for paths that are cleaned on reboot or have
// transient semantics. We err on the side of warning (the cost is just an
// informational print at install time), so the list is intentionally
// over-inclusive.
func isVolatilePath(p string) bool {
	clean := filepath.Clean(p)
	volatiles := []string{"/tmp/", "/var/tmp/", "/dev/shm/", "/run/"}
	for _, v := range volatiles {
		if strings.HasPrefix(clean, v) {
			return true
		}
	}
	return false
}

// unitDir returns ~/.config/systemd/user, expanded. Returns an error if $HOME
// is unset (shouldn't happen in normal systemd-user contexts but be explicit).
func unitDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("$HOME not set: %w", err)
	}
	return filepath.Join(home, UserDir), nil
}

// renderTemplate parses one of the embedded .tmpl files and writes the result
// to out. Used by both Install and (potentially) tests.
func renderTemplate(name string, data any, out string) error {
	body, err := unitTemplates.ReadFile("units/" + name)
	if err != nil {
		return fmt.Errorf("read template %s: %w", name, err)
	}
	t, err := template.New(name).Parse(string(body))
	if err != nil {
		return fmt.Errorf("parse template %s: %w", name, err)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := t.Execute(f, data); err != nil {
		return fmt.Errorf("execute template %s: %w", name, err)
	}
	return nil
}

// Install writes the unit files into ~/.config/systemd/user/, runs
// `systemctl --user daemon-reload`, and enables+starts the timer.
//
// Returns the new Status so callers can print "next run" etc.
func Install(opts InstallOptions) (Status, error) {
	dir, err := unitDir()
	if err != nil {
		return Status{}, err
	}
	svcPath := filepath.Join(dir, ServiceName)
	tmrPath := filepath.Join(dir, TimerName)

	if !opts.Force {
		if _, err := os.Stat(svcPath); err == nil {
			return Status{Installed: true, UnitPath: svcPath, UserDir: dir},
				fmt.Errorf("%s already exists; rerun with --force to overwrite", ServiceName)
		}
		if _, err := os.Stat(tmrPath); err == nil {
			return Status{Installed: true, UnitPath: svcPath, UserDir: dir},
				fmt.Errorf("%s already exists; rerun with --force to overwrite", TimerName)
		}
	}

	execStart, warnings, err := renderExecStart(opts.ExecStart)
	if err != nil {
		return Status{}, err
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	data := map[string]string{"ExecStart": execStart}
	if err := renderTemplate("schooltools-archive.service.tmpl", data, svcPath); err != nil {
		return Status{}, err
	}
	if err := renderTemplate("schooltools-archive.timer.tmpl", nil, tmrPath); err != nil {
		_ = os.Remove(svcPath)
		return Status{}, err
	}

	if out, err := runSystemctl("daemon-reload"); err != nil {
		return Status{}, fmt.Errorf("daemon-reload failed: %w\n%s", err, out)
	}
	if out, err := runSystemctl("enable", "--now", TimerName); err != nil {
		return Status{}, fmt.Errorf("enable --now %s failed: %w\n%s", TimerName, err, out)
	}

	return Query()
}

// Uninstall disables+stops the timer, then removes the unit files. Safe to
// call even if nothing is installed — it's a no-op in that case.
func Uninstall() (Status, error) {
	dir, err := unitDir()
	if err != nil {
		return Status{}, err
	}
	// disable --now is allowed even if the timer isn't active.
	_, _ = runSystemctl("disable", "--now", TimerName)

	svcPath := filepath.Join(dir, ServiceName)
	tmrPath := filepath.Join(dir, TimerName)
	removed := []string{}
	if err := os.Remove(svcPath); err == nil {
		removed = append(removed, svcPath)
	}
	if err := os.Remove(tmrPath); err == nil {
		removed = append(removed, tmrPath)
	}
	if _, err := runSystemctl("daemon-reload"); err != nil {
		return Status{}, fmt.Errorf("daemon-reload after uninstall failed: %w", err)
	}
	if len(removed) == 0 {
		return Status{UserDir: dir}, nil
	}
	return Status{UserDir: dir}, nil
}

// Query inspects the current state: are the units on disk? is the timer
// enabled? what's the next run time?
func Query() (Status, error) {
	dir, err := unitDir()
	if err != nil {
		return Status{}, err
	}
	svcPath := filepath.Join(dir, ServiceName)
	installed := false
	if _, err := os.Stat(svcPath); err == nil {
		installed = true
	}
	st := Status{Installed: installed, UnitPath: svcPath, UserDir: dir}
	if !installed {
		return st, nil
	}

	// is-enabled
	if out, err := runSystemctl("is-enabled", TimerName); err == nil {
		st.Enabled = strings.TrimSpace(string(out)) == "enabled"
	}
	// is-active
	if out, err := runSystemctl("is-active", TimerName); err == nil {
		st.Active = strings.TrimSpace(string(out)) == "active"
	}
	// Parse `list-timers` output. The header line lists columns:
	//   NEXT  LEFT  LAST  PASSED  UNIT  ACTIVATES
	// and each data line is a fixed-width-ish sequence of:
	//   <NEXT datetime, ~4 tokens>  <LEFT, 1 token>
	//   <LAST datetime, ~4 tokens>  <PASSED, 2 tokens like "9min ago">
	//   <UNIT>  <ACTIVATES>
	// We don't try to be clever about column widths — we just locate the
	// header, then for each non-header, non-blank, non-summary line, pull
	// the date/time/tz tokens off the front.
	out, err := runSystemctl("list-timers", "--no-pager", TimerName)
	st.TimerOutput = splitNonEmptyLines(out)
	st.ListTimerExit = 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			st.ListTimerExit = ee.ExitCode()
		}
	}
	for _, line := range st.TimerOutput {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// Header line: first field is "NEXT". Skip.
		if fields[0] == "NEXT" {
			continue
		}
		// Summary lines: "1 timers listed.", "Pass --all ...", "0 loaded ...".
		// Skip anything whose first field doesn't look like a day-of-week.
		if !isDayOfWeek(fields[0]) {
			continue
		}
		// We expect at least: <NEXT: 3 tokens + tz, 1 token for LEFT,
		// 3 tokens + tz for LAST, "N<unit> ago" for PASSED, UNIT, ACTIVATES>.
		// Walk the fields and group into the six semantic columns.
		next, left, last, passed, unit, activates, ok := splitTimerFields(fields)
		if !ok {
			continue
		}
		st.Next = next
		st.Left = left
		st.Last = last
		st.Passed = passed
		st.Unit = unit
		st.Activates = activates
		break
	}
	return st, nil
}

// splitTimerFields groups a token slice from a `list-timers` data row into
// the six semantic columns. Robust to either layout:
//   - NEXT is `-` (timer never fired):  firstDOW is at the LAST position
//   - NEXT is a real datetime:           firstDOW is at position 0
// We anchor on the LAST datetime (second day-of-week token) which is always
// present, then derive everything else relative to it.
func splitTimerFields(f []string) (next, left, last, passed, unit, activates string, ok bool) {
	if len(f) < 4 {
		return
	}
	// Locate the two day-of-week tokens. LAST always has 4 tokens
	// (DayOfWeek YYYY-MM-DD HH:MM:SS TZ), so its DOW marks a clean split.
	firstDOW := -1
	secondDOW := -1
	for i, tok := range f {
		if isDayOfWeek(tok) {
			if firstDOW == -1 {
				firstDOW = i
			} else if secondDOW == -1 {
				secondDOW = i
				break
			}
		}
	}
	if firstDOW == -1 || secondDOW == -1 {
		return
	}
	// last = 4 tokens starting at secondDOW
	lastEnd := secondDOW + 4
	if lastEnd > len(f) {
		return
	}
	last = strings.Join(f[secondDOW:lastEnd], " ")

	// next = tokens before secondDOW. Two sub-cases:
	//   - firstDOW == 0: NEXT is a full datetime at f[0:4], LEFT is f[4]
	//   - firstDOW > 0:  NEXT is f[0:firstDOW] (placeholder or just "-" with
	//     nothing after), LEFT would be the next field after that
	if firstDOW == 0 {
		// Real datetime
		if secondDOW < 4 {
			return // malformed: NEXT datetime would overlap LAST
		}
		next = strings.Join(f[0:4], " ")
		if 4 < secondDOW {
			left = f[4]
		}
	} else {
		// Placeholder (or short). firstDOW > 0 means f[0] is the placeholder.
		next = strings.Join(f[0:firstDOW], " ")
		if firstDOW+1 < secondDOW {
			left = f[firstDOW+1]
		}
	}

	// passed = tokens after LAST, up to and including the "ago" sentinel.
	// `systemctl list-timers` renders relative times in many shapes:
	// "5s", "1min", "2h", "1min 42s", "3 days 2h", etc. The only stable
	// boundary is the trailing "ago".
	passedStart := lastEnd
	passedEnd := -1
	for i := passedStart; i < len(f)-1; i++ {
		if f[i+1] == "ago" {
			passedEnd = i + 2 // include the "ago"
			break
		}
	}
	if passedEnd == -1 {
		// No "ago" sentinel — leave Passed empty.
		passed = ""
		passedEnd = passedStart
	} else {
		passed = strings.Join(f[passedStart:passedEnd], " ")
	}

	// unit, activates at the tail.
	tail := f[passedEnd:]
	if len(tail) < 2 {
		return
	}
	unit = tail[0]
	activates = tail[1]
	ok = true
	return
}

// splitNonEmptyLines trims trailing whitespace and drops empty lines.
func splitNonEmptyLines(s string) []string {
	raw := strings.Split(strings.TrimRight(s, "\n"), "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// isDayOfWeek reports whether s looks like the start of a list-timers data
// row. Used to skip the header line and the trailing summary lines.
func isDayOfWeek(s string) bool {
	switch s {
	case "Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun":
		return true
	}
	return false
}

// runSystemctl invokes `systemctl --user <args...>` and returns the combined
// stdout/stderr. Errors include the command output for diagnostics.
func runSystemctl(args ...string) (string, error) {
	full := append([]string{"--user"}, args...)
	cmd := exec.Command("systemctl", full...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
