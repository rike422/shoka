package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionCLIWorkflow(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	t.Setenv("HOME", home)
	t.Setenv("SHOKA_STATE_DIR", stateDir)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "session", "testdata", "codex.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(home, ".codex", "sessions", "codex.jsonl")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	dryRun := captureStdout(t, func() error {
		return cmdSession([]string{"sync", "--agent", "codex", "--dry-run", "--json"})
	})
	if !strings.Contains(dryRun, `"sources_read": 1`) {
		t.Fatalf("unexpected dry-run output: %s", dryRun)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "sessions.db")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created a database: %v", err)
	}

	syncOutput := captureStdout(t, func() error {
		return cmdSession([]string{"sync", "--agent", "codex", "--json"})
	})
	if !strings.Contains(syncOutput, `"events_added"`) {
		t.Fatalf("unexpected sync output: %s", syncOutput)
	}

	listOutput := captureStdout(t, func() error {
		return cmdSession([]string{"list", "--agent", "codex", "--json"})
	})
	var listed []map[string]any
	if err := json.Unmarshal([]byte(listOutput), &listed); err != nil {
		t.Fatalf("list JSON: %v\n%s", err, listOutput)
	}
	if len(listed) != 1 || listed[0]["session_id"] != "codex:codex-session" {
		t.Fatalf("unexpected list: %+v", listed)
	}

	searchOutput := captureStdout(t, func() error {
		return cmdSession([]string{"search", "go test", "--agent", "codex", "--event-type", "test", "--json"})
	})
	if !strings.Contains(searchOutput, "go test ./...") {
		t.Fatalf("unexpected search output: %s", searchOutput)
	}

	showOutput := captureStdout(t, func() error {
		return cmdSession([]string{"show", "codex-session", "--json"})
	})
	if !strings.Contains(showOutput, `"source"`) || !strings.Contains(showOutput, `"hash"`) {
		t.Fatalf("show lacks source references: %s", showOutput)
	}

	evidencePath := filepath.Join(t.TempDir(), "evidence.json")
	if err := cmdSession([]string{"export", "codex-session", "--output", evidencePath}); err != nil {
		t.Fatal(err)
	}
	evidence, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(evidence), `"session-evidence/v1"`) {
		t.Fatalf("unexpected evidence: %s", evidence)
	}

	reindexOutput := captureStdout(t, func() error {
		return cmdSession([]string{"reindex", "--json"})
	})
	if !strings.Contains(reindexOutput, `"sources_read": 1`) {
		t.Fatalf("unexpected reindex output: %s", reindexOutput)
	}
}

func TestSessionCLIRejectsUnknownFlags(t *testing.T) {
	err := cmdSession([]string{"list", "--agnet", "claude"})
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func captureStdout(t *testing.T, run func() error) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = writer
	err = run()
	_ = writer.Close()
	os.Stdout = previous
	if err != nil {
		_ = reader.Close()
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(data)
}
