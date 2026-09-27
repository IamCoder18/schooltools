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

// WaitLock blocks until the archive lock can be acquired. Used by --wait.
// Returns the lock or an I/O error (never ErrAlreadyRunning).
func WaitLock(root string) (*Lock, error) {
	return lockWith(root, false)
}

// TryLock attempts to claim the archive lock. Returns:
//   - (*Lock, nil) on success — caller MUST defer Release
//   - (nil, ErrAlreadyRunning) when another live process holds it
//   - (nil, otherErr) for I/O failures
//
// Stale locks (file exists but PID is dead) are silently claimed.
func TryLock(root string) (*Lock, error) {
	return lockWith(root, true)
}

// lockHeldByOther reports whether another live process is currently
// holding the archive lock. It only inspects an existing lock file —
// never creates one or its parent directory. Used by status queries
// (`archive`, `archive verify`) which must report lock state without
// side effects.
func lockHeldByOther(root string) bool {
	data, err := os.ReadFile(LockPath(root))
	if err != nil {
		return false
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(string(data)))
	if perr != nil || pid <= 0 || pid == os.Getpid() {
		return false
	}
	return processAlive(pid)
}

func lockWith(root string, nonblock bool) (*Lock, error) {
	path := LockPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	flags := syscall.LOCK_EX
	if nonblock {
		flags |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), flags); err != nil {
		_ = f.Close()
		if nonblock && errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrAlreadyRunning
		}
		return nil, err
	}
	if _, err := f.Seek(0, 0); err != nil {
		_ = f.Close()
		return nil, err
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
	if terr := l.f.Truncate(0); terr != nil {
		// Best-effort: a future caller will overwrite the PID anyway, but
		// skipping the truncate leaves a stale PID in the file which can
		// confuse the next holder if its PID happens to be alive (PID
		// reuse). Swallow only if close also fails to keep behaviour
		// predictable.
		_ = terr
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
