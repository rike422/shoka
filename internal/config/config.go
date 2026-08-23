package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/rike422/shoka/internal/treesitter"
)

// FileName is the optional project config at the repo root.
const FileName = ".shoka.toml"

// Config is the subset of .shoka.toml shoka understands.
type Config struct {
	Treesitter TreesitterConfig `toml:"treesitter"`
	Projects   ProjectsConfig   `toml:"projects"`
}

// ProjectsConfig contains explicit identity aliases for repositories whose
// remote metadata cannot prove that two workspaces are the same project.
type ProjectsConfig struct {
	Aliases map[string]string `toml:"aliases"`
}

// TreesitterConfig selects bundled languages for symbol extraction.
type TreesitterConfig struct {
	// Languages is nil when the key is omitted (→ all bundled).
	// Empty slice disables symbol extraction.
	Languages *[]string `toml:"languages"`
}

// Load reads root/.shoka.toml if present. Missing file is OK.
func Load(projectRoot string) (Config, error) {
	path := filepath.Join(projectRoot, FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, err
	}
	var cfg Config
	meta, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", FileName, err)
	}
	if undec := meta.Undecoded(); len(undec) > 0 {
		return Config{}, fmt.Errorf("%s: unknown keys: %v", FileName, undec)
	}
	if cfg.Treesitter.Languages != nil {
		if _, err := treesitter.NormalizeEnabled(*cfg.Treesitter.Languages); err != nil {
			return Config{}, fmt.Errorf("%s: %w", FileName, err)
		}
	}
	return cfg, nil
}

// EnabledLanguages returns the language list for NewExtractor.
// nil → all bundled; empty → disabled.
func (c Config) EnabledLanguages() []string {
	if c.Treesitter.Languages == nil {
		return nil
	}
	return *c.Treesitter.Languages
}
