package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	shokasession "github.com/rike422/shoka/internal/session"
)

func TestRunCreatesAnIsolatedSyncedSQLiteDatabase(t *testing.T) {
	root := filepath.Join(t.TempDir(), "episode-pipeline")
	var output bytes.Buffer
	if err := run([]string{"--output", root, "--sessions", "64"}, &output); err != nil {
		t.Fatal(err)
	}
	var result fixtureResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("fixture result JSON: %v\n%s", err, output.String())
	}
	if result.Corpus.Sessions != 64 || result.Sync.SourcesRead != 64 {
		t.Fatalf("unexpected fixture result: %+v", result)
	}
	if result.Database != filepath.Join(root, "state", "sessions.db") {
		t.Fatalf("database = %q", result.Database)
	}
	if info, err := os.Stat(result.Database); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("fixture database was not created: info=%v err=%v", info, err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("fixture database mode = %o, want 600", info.Mode().Perm())
	}
	if info, err := os.Stat(result.Corpus.StateDir); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o700 {
		t.Fatalf("fixture state mode = %o, want 700", info.Mode().Perm())
	}
	env := shokasession.Environment{Home: result.Corpus.Home, StateDir: result.Corpus.StateDir}
	summary, err := shokasession.SummarizeTranscriptEpisodes(context.Background(), env, shokasession.TranscriptEpisodeFilter{Filter: shokasession.Filter{Agent: shokasession.AgentCodex}})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Episodes != 69 {
		t.Fatalf("episodes = %d, want 69", summary.Episodes)
	}
}

func TestRunRequiresANewDestination(t *testing.T) {
	if err := run(nil, &bytes.Buffer{}); err == nil {
		t.Fatal("missing output was accepted")
	}
	if err := run([]string{"--unknown"}, &bytes.Buffer{}); err == nil {
		t.Fatal("unknown flag was accepted")
	}
	if err := run([]string{"--output", filepath.Join(t.TempDir(), "fixture"), "extra"}, &bytes.Buffer{}); err == nil {
		t.Fatal("positional argument was accepted")
	}
	if err := run([]string{"--output", filepath.Join(t.TempDir(), "fixture"), "--sessions", "0"}, &bytes.Buffer{}); err == nil {
		t.Fatal("zero sessions were accepted")
	}
	root := filepath.Join(t.TempDir(), "occupied")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--output", root}, &bytes.Buffer{}); err == nil {
		t.Fatal("occupied output was overwritten")
	}
}
