package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	shokasession "github.com/rike422/shoka/internal/session"
)

func TestDefaultTranscriptEpisodeListRange(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	parsed := parsedOptions{values: map[string]string{}, booleans: map[string]bool{}}
	filter := applyDefaultTranscriptEpisodeListRange(shokasession.TranscriptEpisodeFilter{}, parsed, now)
	if !filter.Filter.From.Equal(now.Add(-7*24*time.Hour)) || !filter.Filter.To.Equal(now) {
		t.Fatalf("default range = %s..%s", filter.Filter.From, filter.Filter.To)
	}

	explicit := parsedOptions{values: map[string]string{"--from": "2026-08-01"}, booleans: map[string]bool{}}
	filter = applyDefaultTranscriptEpisodeListRange(shokasession.TranscriptEpisodeFilter{Filter: shokasession.Filter{From: now.Add(-30 * 24 * time.Hour)}}, explicit, now)
	if !filter.Filter.From.Equal(now.Add(-30*24*time.Hour)) || !filter.Filter.To.IsZero() {
		t.Fatalf("explicit range was overwritten = %s..%s", filter.Filter.From, filter.Filter.To)
	}
}

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
	data = append(data, []byte(`{"timestamp":"2026-08-20T01:00:06Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Design direction: use the canonical queryless episode list."}]}}
`)...)
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

	episodeListOutput := captureStdout(t, func() error {
		return cmdTranscript([]string{"episode", "list", "--agent", "codex", "--event-type", "design_direction", "--from", "2026-08-20", "--to", "2026-08-20", "--json"})
	})
	var listedEpisodes []map[string]any
	if err := json.Unmarshal([]byte(episodeListOutput), &listedEpisodes); err != nil {
		t.Fatalf("episode list JSON: %v\n%s", err, episodeListOutput)
	}
	if len(listedEpisodes) != 1 {
		t.Fatalf("unexpected design episodes: %+v", listedEpisodes)
	}
	trigger, ok := listedEpisodes[0]["trigger"].(map[string]any)
	if !ok || trigger["type"] != "design_direction" || trigger["timestamp"] != "2026-08-20T01:00:06Z" {
		t.Fatalf("unexpected design trigger: %+v", listedEpisodes[0]["trigger"])
	}
	plainEpisodeListOutput := captureStdout(t, func() error {
		return cmdTranscript([]string{"episode", "list", "--agent", "codex", "--event-type", "design_direction", "--from", "2026-08-20", "--to", "2026-08-20"})
	})
	if !strings.Contains(plainEpisodeListOutput, "2026-08-20T01:00:06Z") || !strings.Contains(plainEpisodeListOutput, "trigger=design_direction") {
		t.Fatalf("episode list output lacks date or trigger: %s", plainEpisodeListOutput)
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

	episodeSearchOutput := captureStdout(t, func() error {
		return cmdTranscript([]string{"episode", "search", "migration test failed", "--agent", "codex", "--before", "1", "--after", "2", "--json"})
	})
	if !strings.Contains(episodeSearchOutput, `"raw_events": 1`) || !strings.Contains(episodeSearchOutput, `"transcript-episode/v1"`) {
		t.Fatalf("unexpected episode search output: %s", episodeSearchOutput)
	}

	var episodeResult struct {
		Episodes []struct {
			EpisodeID string `json:"episode_id"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal([]byte(episodeSearchOutput), &episodeResult); err != nil {
		t.Fatal(err)
	}
	episodePath := filepath.Join(t.TempDir(), "episode.json")
	if err := cmdTranscript([]string{"episode", "export", episodeResult.Episodes[0].EpisodeID, "--output", episodePath, "--before", "1", "--after", "2"}); err != nil {
		t.Fatal(err)
	}
	episodeEvidence, err := os.ReadFile(episodePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(episodeEvidence), `"transcript-episode/v1"`) || !strings.Contains(string(episodeEvidence), `"transcript_id"`) {
		t.Fatalf("unexpected episode evidence: %s", episodeEvidence)
	}
	assertTranscriptEpisodeShape(t, episodeEvidence)

	reindexOutput := captureStdout(t, func() error {
		return cmdSession([]string{"reindex", "--json"})
	})
	if !strings.Contains(reindexOutput, `"sources_read": 1`) {
		t.Fatalf("unexpected reindex output: %s", reindexOutput)
	}
}

func assertTranscriptEpisodeShape(t *testing.T, data []byte) {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("transcript episode JSON: %v", err)
	}
	for _, field := range []string{"schema_version", "episode_id", "transcript_id", "native_session_id", "task_lineage_id", "project_id", "trigger", "context_before", "corrections", "actions_after", "outcomes", "task_status", "source_event_ids", "omitted"} {
		if _, ok := document[field]; !ok {
			t.Errorf("transcript episode missing required field %q", field)
		}
	}
	for _, field := range []string{"context_before", "corrections", "actions_after"} {
		items, ok := document[field].([]any)
		if !ok {
			t.Errorf("transcript episode field %q is not an array", field)
			continue
		}
		for _, item := range items {
			evidence, ok := item.(map[string]any)
			if !ok {
				t.Errorf("transcript episode %q contains a non-object item", field)
				continue
			}
			assertSourceShape(t, evidence)
		}
	}
	if outcomes, ok := document["outcomes"].([]any); !ok {
		t.Errorf("transcript episode field %q is not an array", "outcomes")
	} else {
		for _, item := range outcomes {
			outcome, ok := item.(map[string]any)
			if !ok {
				t.Errorf("transcript episode outcomes contains a non-object item")
				continue
			}
			assertSourceShape(t, outcome)
		}
	}
	if finalReport, ok := document["final_report"].(map[string]any); ok {
		assertSourceShape(t, finalReport)
	}
}

func assertSourceShape(t *testing.T, evidence map[string]any) {
	t.Helper()
	source, ok := evidence["source"].(map[string]any)
	if !ok {
		t.Errorf("evidence source is missing")
		return
	}
	for _, field := range []string{"path", "hash", "line", "byte_offset", "state"} {
		if _, ok := source[field]; !ok {
			t.Errorf("evidence source missing required field %q", field)
		}
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
