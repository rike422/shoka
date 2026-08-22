package session

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveStateDirPrecedence(t *testing.T) {
	home := t.TempDir()
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "shoka override", env: map[string]string{"SHOKA_STATE_DIR": "/tmp/custom-shoka"}, want: "/tmp/custom-shoka"},
		{name: "xdg", env: map[string]string{"XDG_STATE_HOME": "/tmp/xdg-state"}, want: "/tmp/xdg-state/shoka"},
		{name: "home fallback", env: map[string]string{}, want: filepath.Join(home, ".local", "state", "shoka")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := Environment{Home: home, LookupEnv: mapLookup(tt.env)}
			got, err := ResolveStateDir(env)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestEnsureStateDirIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := EnsureStateDir(dir); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits are not enforced on Windows")
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("state dir mode = %o, want 700", got)
	}
}

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
