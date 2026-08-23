package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectIdentityFallsBackToCanonicalWorkspace(t *testing.T) {
	dir := t.TempDir()
	identity, err := ResolveProjectIdentity(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if identity.ID == "" || identity.WorkspaceID == "" || identity.Canonical == "" {
		t.Fatalf("incomplete identity: %+v", identity)
	}
	if identity.ID != stableProjectID(identity.Canonical) {
		t.Fatalf("project id is not derived from canonical key: %+v", identity)
	}
}

func TestProjectIdentityUsesExplicitAlias(t *testing.T) {
	dir := t.TempDir()
	alias := filepath.Join(dir, "alias.toml")
	if err := os.WriteFile(alias, []byte("[projects.aliases]\n\""+dir+"\" = \"shared-project\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := ResolveProjectIdentity(dir, map[string]string{dir: "shared-project"})
	if err != nil {
		t.Fatal(err)
	}
	if identity.Canonical != "alias:shared-project" || identity.ID != stableProjectID(identity.Canonical) {
		t.Fatalf("alias was not canonicalized: %+v", identity)
	}
}

func TestNormalizeRemoteCanonicalizesCommonGitForms(t *testing.T) {
	for input, want := range map[string]string{
		"git@github.com:Acme/Project.git":      "ssh://github.com/Acme/Project",
		"https://github.com/Acme/Project.git/": "https://github.com/Acme/Project",
	} {
		if got := normalizeRemote(input); got != want {
			t.Fatalf("normalizeRemote(%q) = %q, want %q", input, got, want)
		}
	}
}
