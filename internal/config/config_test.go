package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rike422/shoka/internal/config"
)

func TestLoadMissing(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EnabledLanguages() != nil {
		t.Fatalf("want nil (all bundled), got %v", cfg.EnabledLanguages())
	}
}

func TestLoadEmptyDisables(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "[treesitter]\nlanguages = []\n")
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	langs := cfg.EnabledLanguages()
	if langs == nil || len(langs) != 0 {
		t.Fatalf("want empty slice, got %#v", langs)
	}
}

func TestLoadSubset(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "[treesitter]\nlanguages = [\"gdscript\", \"go\"]\n")
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	langs := cfg.EnabledLanguages()
	if len(langs) != 2 {
		t.Fatalf("got %v", langs)
	}
}

func TestLoadUnknownLang(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "[treesitter]\nlanguages = [\"cobol\"]\n")
	if _, err := config.Load(dir); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadRubyOK(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "[treesitter]\nlanguages = [\"ruby\"]\n")
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	langs := cfg.EnabledLanguages()
	if len(langs) != 1 || langs[0] != "ruby" {
		t.Fatalf("got %v", langs)
	}
}

func TestLoadUnknownKey(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "[treesitter]\nfoo = 1\n")
	if _, err := config.Load(dir); err == nil {
		t.Fatal("expected error")
	}
}

func write(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
