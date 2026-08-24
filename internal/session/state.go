package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const stateDBName = "sessions.db"
const stateLockName = "sessions.lock"

// ResolveStateDir returns the explicitly selected Shoka state directory.
func ResolveStateDir(env Environment) (string, error) {
	if env.StateDir == "" {
		return "", fmt.Errorf("session environment requires an explicit state directory")
	}
	return filepath.Abs(env.StateDir)
}

func resolveConfiguredStateDir(home string, lookup func(string) (string, bool)) (string, error) {
	if lookup != nil {
		if value, ok := lookup("SHOKA_STATE_DIR"); ok && value != "" {
			return filepath.Abs(value)
		}
		if value, ok := lookup("XDG_STATE_HOME"); ok && value != "" {
			return filepath.Abs(filepath.Join(value, "shoka"))
		}
	}
	if home == "" {
		return "", fmt.Errorf("cannot resolve state directory without a home directory")
	}
	return filepath.Abs(filepath.Join(home, ".local", "state", "shoka"))
}

// EnsureStateDir creates or tightens a private state directory.
func EnsureStateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

// StateDBPath returns the session database path.
func StateDBPath(env Environment) (string, error) {
	dir, err := ResolveStateDir(env)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, stateDBName), nil
}

func acquireStateLock(env Environment) (func(), error) {
	dir, err := ResolveStateDir(env)
	if err != nil {
		return nil, err
	}
	if err := EnsureStateDir(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, stateLockName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("session state is locked by another sync or reindex: %s", path)
		}
		return nil, err
	}
	if err := file.Truncate(0); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, err
	}
	if _, err := file.Seek(0, 0); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, err
	}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, err
	}
	released := false
	return func() {
		if released {
			return
		}
		released = true
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}
