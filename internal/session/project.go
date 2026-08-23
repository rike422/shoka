package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rike422/shoka/internal/config"
	"github.com/rike422/shoka/internal/root"
)

// ProjectIdentity is the stable identity derived from observable workspace
// metadata. The canonical key is retained for diagnostics, while ID is the
// opaque value stored in the session index and episode evidence.
type ProjectIdentity struct {
	ID             string
	Canonical      string
	RepositoryRoot string
	WorkspaceID    string
}

// ResolveProjectIdentity derives a project identity without making semantic
// guesses about the task. aliases are explicit operator-provided overrides.
func ResolveProjectIdentity(cwd string, aliases map[string]string) (ProjectIdentity, error) {
	workspace, err := normalizedPath(cwd)
	if err != nil {
		return ProjectIdentity{}, err
	}
	repositoryRoot := workspace
	if resolved, resolveErr := root.Resolve(workspace, false); resolveErr == nil {
		repositoryRoot = resolved
	}
	if alias := matchingAlias(aliases, workspace, repositoryRoot); alias != "" {
		canonical := "alias:" + alias
		return newProjectIdentity(canonical, repositoryRoot, workspace), nil
	}
	if remote := gitRemote(repositoryRoot); remote != "" {
		canonical := "remote:" + remote
		return newProjectIdentity(canonical, repositoryRoot, workspace), nil
	}
	if common := gitCommonDirectory(repositoryRoot); common != "" {
		canonical := "git-common:" + common
		return newProjectIdentity(canonical, repositoryRoot, workspace), nil
	}
	return newProjectIdentity("repo:"+repositoryRoot, repositoryRoot, workspace), nil
}

func newProjectIdentity(canonical, repositoryRoot, workspace string) ProjectIdentity {
	return ProjectIdentity{
		ID:             stableProjectID(canonical),
		Canonical:      canonical,
		RepositoryRoot: repositoryRoot,
		WorkspaceID:    stableWorkspaceID(workspace),
	}
}

func enrichSourceIdentity(source Source) Source {
	if source.Cwd == "" || (source.ProjectID != "" && source.WorkspaceID != "") {
		return source
	}
	identity, err := ResolveProjectIdentity(source.Cwd, loadProjectAliases(source.Cwd))
	if err != nil {
		return source
	}
	if source.ProjectID == "" {
		source.ProjectID = identity.ID
	}
	if source.WorkspaceID == "" {
		source.WorkspaceID = identity.WorkspaceID
	}
	return source
}

func loadProjectAliases(cwd string) map[string]string {
	rootPath, err := root.Resolve(cwd, false)
	if err != nil {
		return nil
	}
	cfg, err := config.Load(rootPath)
	if err != nil {
		return nil
	}
	aliases := make(map[string]string, len(cfg.Projects.Aliases))
	for path, alias := range cfg.Projects.Aliases {
		if normalized, err := normalizedPath(path); err == nil {
			aliases[normalized] = strings.TrimSpace(alias)
		}
	}
	return aliases
}

func matchingAlias(aliases map[string]string, workspace, repositoryRoot string) string {
	paths := []string{workspace, repositoryRoot}
	keys := make([]string, 0, len(aliases))
	for key := range aliases {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		normalizedKey, err := normalizedPath(key)
		if err != nil {
			continue
		}
		for _, path := range paths {
			if normalizedKey == path && strings.TrimSpace(aliases[key]) != "" {
				return strings.TrimSpace(aliases[key])
			}
		}
	}
	return ""
}

func normalizedPath(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("path is required")
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = filepath.Clean(resolved)
	}
	return abs, nil
}

func stableProjectID(canonical string) string {
	digest := sha256.Sum256([]byte(canonical))
	return "project:" + hex.EncodeToString(digest[:])
}

func stableWorkspaceID(workspace string) string {
	digest := sha256.Sum256([]byte(workspace))
	return "workspace:" + hex.EncodeToString(digest[:])
}

func stableTaskLineageID(projectID, seed string) string {
	digest := sha256.Sum256([]byte(projectID + "\x00" + seed))
	return "lineage:" + hex.EncodeToString(digest[:])
}

func gitRemote(repositoryRoot string) string {
	for _, name := range []string{"origin", "upstream"} {
		if value := gitOutput(repositoryRoot, "remote", "get-url", name); value != "" {
			return normalizeRemote(value)
		}
	}
	return ""
}

func gitCommonDirectory(repositoryRoot string) string {
	value := gitOutput(repositoryRoot, "rev-parse", "--git-common-dir")
	if value == "" {
		return ""
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(repositoryRoot, value)
	}
	return filepath.Clean(value)
}

func gitOutput(repositoryRoot string, args ...string) string {
	commandArgs := append([]string{"-C", repositoryRoot}, args...)
	output, err := exec.Command("git", commandArgs...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func normalizeRemote(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, ":") && !strings.Contains(value, "://") && strings.Contains(value, "@") {
		parts := strings.SplitN(value, ":", 2)
		value = "ssh://" + parts[0] + "/" + strings.TrimPrefix(parts[1], "/")
	}
	parsed, err := url.Parse(value)
	if err == nil && parsed.Host != "" {
		parsed.User = nil
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		parsed.Path = strings.TrimRight(filepath.ToSlash(parsed.Path), "/")
		parsed.Path = strings.TrimSuffix(parsed.Path, ".git")
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return parsed.String()
	}
	return strings.TrimSuffix(strings.TrimRight(strings.ToLower(value), "/"), ".git")
}
