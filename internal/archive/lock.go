package archive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Lock represents an exclusive archive-run lock held for the duration of a
// single `schooltools archive` invocation. It guards against two archive
// runs racing — most commonly the 30-min systemd timer colliding with a
// manual run the user just kicked off.
type Lock struct {
	path string
	f    *os.File
}

// ErrAlreadyRunning is returned by TryLock when another archive run is
// already holding the lock and its PID is still alive. Callers should treat
// this as a clean skip, not an error.
var ErrAlreadyRunning = errors.New("another archive run is in progress")

// LockPath returns the on-disk path of the lock file. Exposed for tests.
func LockPath(root string) string {
	return filepath.Join(root, "archive.lock")
}

// TryLock attempts to claim the archive lock. Returns:
//   - (*Lock, nil) on success — caller MUST defer Release
//   - (nil, ErrAlreadyRunning) when another live process holds it
//   - (nil, otherErr) for I/O failures
//
// Stale locks (file exists but PID is dead) are silently claimed.
func TryLock(root string) (*Lock, error) {
	path := LockPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrAlreadyRunning
		}
		return nil, err
	}
	// Truncate + write our PID. Reading and re-checking the previous PID
	// before truncating gives us "stale lock detection" for the case where
	// the previous process crashed without releasing the flock.
	if _, err := f.Seek(0, 0); err != nil {
		_ = f.Close()
		return nil, err
	}
	prevPIDBytes := make([]byte, 32)
	n, _ := f.Read(prevPIDBytes)
	if n > 0 {
		prevPID, perr := strconv.Atoi(strings.TrimSpace(string(prevPIDBytes[:n])))
		if perr == nil && prevPID != os.Getpid() && processAlive(prevPID) {
			_ = f.Close()
			return nil, ErrAlreadyRunning
		}
	}
	if err := f.Truncate(0); err != nil {
		_ = f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, 0); err != nil {
		_ = f.Close()
		return nil, err
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Lock{path: path, f: f}, nil
}

// Release drops the flock and closes the file. Best-effort: errors are
// returned for the caller to log but shouldn't fail the archive run.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	if cerr := l.f.Close(); cerr != nil && err == nil {
		err = cerr
	}
	l.f = nil
	return err
}

// processAlive returns true if pid is currently a running process. Uses
// kill(pid, 0) which succeeds for any process we have permission to signal,
// including our own. Permission errors are treated as "alive" (conservative:
// we won't steal a lock from a process we just can't see).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.ESRCH) {
		return false // no such process
	}
	// EPERM means the process exists but we can't signal it. Treat as alive.
	return true
}
