package session

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveStateDirRequiresExplicitSelection(t *testing.T) {
	home := t.TempDir()
	tests := []struct {
		name     string
		stateDir string
		want     string
	}{
		{name: "explicit", stateDir: "/tmp/custom-shoka", want: "/tmp/custom-shoka"},
		{name: "missing", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := Environment{Home: home, StateDir: tt.stateDir, LookupEnv: mapLookup(nil)}
			got, err := ResolveStateDir(env)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("missing state directory resolved to %q", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestResolveConfiguredStateDirPrecedence(t *testing.T) {
	home := t.TempDir()
	tests := []struct {
		name   string
		values map[string]string
		want   string
	}{
		{
			name:   "shoka override",
			values: map[string]string{"SHOKA_STATE_DIR": "/tmp/custom-shoka", "XDG_STATE_HOME": "/tmp/xdg-state"},
			want:   "/tmp/custom-shoka",
		},
		{name: "xdg", values: map[string]string{"XDG_STATE_HOME": "/tmp/xdg-state"}, want: "/tmp/xdg-state/shoka"},
		{name: "home fallback", values: map[string]string{}, want: filepath.Join(home, ".local", "state", "shoka")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveConfiguredStateDir(home, mapLookup(tt.values))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestCurrentEnvironmentResolvesStateDirectory(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	t.Setenv("SHOKA_STATE_DIR", stateDir)
	env, err := CurrentEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if env.StateDir != stateDir {
		t.Fatalf("state directory = %q, want %q", env.StateDir, stateDir)
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

func testEnvironment(home string) Environment {
	return Environment{
		Home:      home,
		StateDir:  filepath.Join(home, ".shoka-test-state"),
		LookupEnv: mapLookup(nil),
	}
}
