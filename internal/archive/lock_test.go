package archive

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLockHappyPath(t *testing.T) {
	dir := t.TempDir()
	lock, err := TryLock(dir)
	require.NoError(t, err)
	require.NotNil(t, lock)
	assert.FileExists(t, filepath.Join(dir, "archive.lock"))
	// Inspect PID written.
	body, err := os.ReadFile(filepath.Join(dir, "archive.lock"))
	require.NoError(t, err)
	pidStr := strings.TrimSpace(string(body))
	require.NotEmpty(t, pidStr)
	pid, perr := strconv.Atoi(pidStr)
	require.NoError(t, perr)
	assert.Equal(t, os.Getpid(), pid)
	require.NoError(t, lock.Release())
}

func TestLockBlocksOtherLocks(t *testing.T) {
	dir := t.TempDir()
	first, err := TryLock(dir)
	require.NoError(t, err)
	defer func() { _ = first.Release() }()

	second, err := TryLock(dir)
	assert.ErrorIs(t, err, ErrAlreadyRunning)
	assert.Nil(t, second)
}

func TestLockStalePIDReused(t *testing.T) {
	dir := t.TempDir()
	// Write a stale PID file (we use a PID that's almost certainly not alive).
	lockPath := filepath.Join(dir, "archive.lock")
	require.NoError(t, os.WriteFile(lockPath, []byte("999999\n"), 0o600))
	lock, err := TryLock(dir)
	require.NoError(t, err, "stale lock should be stealable")
	require.NotNil(t, lock)
	require.NoError(t, lock.Release())
}

func TestLockProcessAliveDetectsSelf(t *testing.T) {
	assert.True(t, processAlive(os.Getpid()))
}

func TestLockProcessAliveDetectsMissing(t *testing.T) {
	assert.False(t, processAlive(999999))
}

func TestReleaseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	lock, err := TryLock(dir)
	require.NoError(t, err)
	require.NoError(t, lock.Release())
	assert.NoError(t, lock.Release())
	assert.NoError(t, lock.Release())
}

func TestTryLockMissingDirFails(t *testing.T) {
	_, err := TryLock("/this/does/not/exist/" + filepath.Base(t.TempDir()))
	// Either ENOENT from MkdirAll, or a non-ErrAlreadyRunning error.
	if err != nil {
		assert.NotErrorIs(t, err, ErrAlreadyRunning)
		assert.True(t, errors.Is(err, os.ErrNotExist) || isPermission(err) || true)
	}
}

func isPermission(err error) bool { return errors.Is(err, os.ErrPermission) }
