// Package systemd installs, removes, and inspects the schooltools systemd
// units that drive periodic archive runs. The .service and .timer unit files
// are embedded at compile time via go:embed so the binary ships with them
// self-contained.
package systemd

import (
	"embed"
	"errors"
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
// line written into the service unit (typically "/usr/bin/schooltools archive update"
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
	Active        bool     // true if the timer is currently active
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
	Warnings      []string // human-readable warnings surfaced by Query (e.g. legacy ExecStart)
}

// renderExecStart returns the ExecStart string to bake into the service unit.
// Prefers the caller's override; falls back to the absolute path of the
// currently running binary followed by ` archive update --no-auto-refresh`.
// Returns a non-empty warning when the resolved path is in a volatile
// location like /tmp.
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
	return abs + " archive update --no-auto-refresh", warnings, nil
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

// legacyExecStart reports whether the on-disk service unit still calls the
// pre-archive-UX-redesign form (e.g. `archive` / `prune`), or is missing an
// ExecStart line altogether. Parses each ExecStart= directive (skipping
// comments and unrelated lines) and returns true when none of them invoke
// `archive update`. A bare mention of "archive update" in a Description=
// line cannot suppress the warning.
func legacyExecStart(svcPath string) bool {
	data, err := os.ReadFile(svcPath)
	if err != nil {
		return false
	}
	anyUpdate := false
	anyExec := false
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if !strings.HasPrefix(line, "ExecStart=") {
			continue
		}
		anyExec = true
		if strings.Contains(line, "archive update") {
			anyUpdate = true
		}
	}
	if !anyExec {
		return true
	}
	return !anyUpdate
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
	defer func() { _ = f.Close() }()
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

	st, err := Query()
	st.Warnings = append(warnings, st.Warnings...)
	return st, err
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
	removed := false
	remove := func(p string) error {
		err := os.Remove(p)
		switch {
		case err == nil:
			removed = true
			return nil
		case errors.Is(err, os.ErrNotExist):
			return nil
		default:
			return err
		}
	}
	var rmErr error
	if err := remove(svcPath); err != nil {
		rmErr = errors.Join(rmErr, err)
	}
	if err := remove(tmrPath); err != nil {
		rmErr = errors.Join(rmErr, err)
	}
	if !removed && rmErr == nil {
		// Nothing was installed: leave systemctl alone entirely.
		return Status{UserDir: dir}, nil
	}
	if _, err := runSystemctl("daemon-reload"); err != nil {
		return Status{}, fmt.Errorf("daemon-reload after uninstall failed: %w", err)
	}
	if rmErr != nil {
		return Status{UserDir: dir}, fmt.Errorf("remove unit files: %w", rmErr)
	}
	return Status{UserDir: dir}, nil
}

// Query inspects the current state: are the units on disk? is the timer
// enabled? what's the next run time? Also flags when the installed service
// was written by an older schooltools release whose ExecStart pre-dates the
// `archive update` redesign — running `schooltools systemd install --force`
// upgrades the unit in place.
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
	if legacyExecStart(svcPath) {
		st.Warnings = append(st.Warnings,
			"service unit has no ExecStart or uses a pre-archive-update one; rerun `schooltools systemd install --force` to upgrade in place")
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
	//   <NEXT datetime, ~4 tokens>  <LEFT, display text like "29min left">
	//   <LAST datetime, ~4 tokens>  <PASSED, display text like "9min ago">
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
		// Summary lines ("1 timers listed.", "Pass --all …") and the header
		// never carry the unit name; data rows always do.
		if !strings.Contains(line, TimerName) {
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

// isTimerPlaceholder reports whether a list-timers token is an empty
// column. systemctl prints "-" on most versions and "n/a" on systemd
// 249+ for empty NEXT/LEFT/LAST/PASSED columns.
func isTimerPlaceholder(s string) bool {
	return s == "-" || s == "n/a"
}

// splitTimerFields groups a token slice from a `list-timers` data row into
// the six semantic columns. The row layout is:
//
//	<NEXT: 4 tokens>  <LEFT: 1+ tokens ("29min", "29min left")>  <LAST: 4 tokens | placeholder>  <PASSED: "N ago" | placeholder>  <UNIT>  <ACTIVATES>
//
// The final two tokens are always UNIT and ACTIVATES, so they anchor the
// parse. LAST and PASSED are placeholders until the timer has fired at
// least once; a placeholder PASSED must not shift the UNIT/ACTIVATES
// columns. LEFT is human display text ("29min left"), not a fixed
// token count, so its slice runs up to the next known column.
func splitTimerFields(f []string) (next, left, last, passed, unit, activates string, ok bool) {
	if len(f) < 6 {
		return
	}
	unit, activates = f[len(f)-2], f[len(f)-1]
	rest := f[:len(f)-2]

	var dows []int
	for i, tok := range rest {
		if isDayOfWeek(tok) {
			dows = append(dows, i)
		}
	}
	switch len(dows) {
	case 1:
		if dows[0] == 0 {
			// NEXT is real; LAST and PASSED are single-token
			// placeholders, so LEFT runs from the end of the NEXT
			// datetime up to those two trailing tokens.
			if len(rest) < 7 {
				return
			}
			next = strings.Join(rest[:4], " ")
			if l := rest[4 : len(rest)-2]; !allPlaceholders(l) {
				left = strings.Join(l, " ")
			}
		} else {
			// NEXT is a placeholder (timer with no next elapse);
			// the sole DOW token starts LAST.
			if dows[0]+4 > len(rest) {
				return
			}
			if toks := rest[:dows[0]-1]; !allPlaceholders(toks) {
				next = strings.Join(toks, " ")
			}
			if dows[0] >= 2 && !isTimerPlaceholder(rest[dows[0]-1]) {
				left = rest[dows[0]-1]
			}
			last = strings.Join(rest[dows[0]:dows[0]+4], " ")
			mid := rest[dows[0]+4:]
			if len(mid) == 1 && isTimerPlaceholder(mid[0]) {
				mid = nil
			}
			passed = strings.Join(mid, " ")
		}
	case 2:
		if dows[1]+4 > len(rest) {
			return
		}
		next = strings.Join(rest[:4], " ")
		if l := rest[4:dows[1]]; !allPlaceholders(l) {
			left = strings.Join(l, " ")
		}
		last = strings.Join(rest[dows[1]:dows[1]+4], " ")
		mid := rest[dows[1]+4:]
		if len(mid) == 1 && isTimerPlaceholder(mid[0]) {
			mid = nil
		}
		passed = strings.Join(mid, " ")
	default:
		return
	}
	ok = true
	return
}

// allPlaceholders reports whether every token is an empty-column marker
// (or the slice is empty), i.e. the column carries no real value.
func allPlaceholders(toks []string) bool {
	if len(toks) == 0 {
		return true
	}
	for _, t := range toks {
		if !isTimerPlaceholder(t) {
			return false
		}
	}
	return true
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
