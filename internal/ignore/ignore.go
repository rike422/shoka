package ignore

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Set is a root-level .shokaignore matcher (gitignore-like subset).
type Set struct {
	rules []rule
}

type rule struct {
	negate  bool
	dirOnly bool
	rooted  bool
	raw     string
}

// Load reads root/.shokaignore. Missing file is an empty set.
func Load(root string) (*Set, error) {
	path := filepath.Join(root, ".shokaignore")
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return &Set{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var s Set
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r := rule{}
		if strings.HasPrefix(line, "!") {
			r.negate = true
			line = strings.TrimSpace(strings.TrimPrefix(line, "!"))
			if line == "" {
				continue
			}
		}
		if strings.HasSuffix(line, "/") {
			r.dirOnly = true
			line = strings.TrimSuffix(line, "/")
		}
		if strings.HasPrefix(line, "/") {
			r.rooted = true
			line = strings.TrimPrefix(line, "/")
		}
		line = filepath.ToSlash(line)
		if line == "" {
			continue
		}
		r.raw = line
		s.rules = append(s.rules, r)
	}
	return &s, sc.Err()
}

// Match reports whether rel (slash-separated, relative) is ignored.
func (s *Set) Match(rel string) bool {
	if s == nil || len(s.rules) == 0 {
		return false
	}
	rel = filepath.ToSlash(rel)
	ignored := false
	for _, r := range s.rules {
		if matchRule(r, rel) {
			ignored = !r.negate
		}
	}
	return ignored
}

func matchRule(r rule, path string) bool {
	pat := r.raw
	if r.dirOnly {
		// Directory patterns match the dir itself and anything under it.
		if path == pat || strings.HasPrefix(path, pat+"/") {
			return true
		}
		if !r.rooted && !strings.Contains(pat, "/") {
			for _, part := range strings.Split(path, "/") {
				if part == pat {
					return true
				}
			}
		}
		return false
	}

	if r.rooted || strings.Contains(pat, "/") {
		return pathGlob(pat, path)
	}

	// Pattern without slash: match any path segment (gitignore semantics).
	base := filepath.Base(path)
	if ok, _ := filepath.Match(pat, base); ok {
		return true
	}
	for _, part := range strings.Split(path, "/") {
		if ok, _ := filepath.Match(pat, part); ok {
			return true
		}
	}
	return false
}

func pathGlob(pat, path string) bool {
	if ok, _ := filepath.Match(pat, path); ok {
		return true
	}
	// Prefix directory match: docs/** style via trailing /**
	if strings.HasSuffix(pat, "/**") {
		prefix := strings.TrimSuffix(pat, "/**")
		return path == prefix || strings.HasPrefix(path, prefix+"/")
	}
	if strings.Contains(pat, "**") {
		// Minimal ** support: replace with * for filepath.Match after normalizing.
		simple := strings.ReplaceAll(pat, "**/", "")
		simple = strings.ReplaceAll(simple, "**", "*")
		if ok, _ := filepath.Match(simple, path); ok {
			return true
		}
		if strings.HasSuffix(path, "/"+filepath.Base(pat)) {
			basePat := filepath.Base(pat)
			if ok, _ := filepath.Match(basePat, filepath.Base(path)); ok {
				return true
			}
		}
	}
	return false
}
