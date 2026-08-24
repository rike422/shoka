package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListTranscriptEpisodesFiltersAnchorTypeAndTriggerDate(t *testing.T) {
	env := syncCodexDesignFixture(t)
	from := time.Date(2026, 8, 20, 1, 0, 6, 0, time.UTC)
	to := from
	episodes, err := ListTranscriptEpisodes(context.Background(), env, TranscriptEpisodeFilter{
		Filter:     Filter{Agent: AgentCodex, From: from, To: to},
		AnchorType: AnchorDesignDirection,
	}, EpisodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(episodes) != 1 {
		t.Fatalf("episodes = %+v", episodes)
	}
	if episodes[0].Trigger.Type != string(AnchorDesignDirection) {
		t.Fatalf("trigger type = %q", episodes[0].Trigger.Type)
	}
	if episodes[0].Trigger.Timestamp != "2026-08-20T01:00:06Z" {
		t.Fatalf("trigger timestamp = %q", episodes[0].Trigger.Timestamp)
	}

	outside, err := ListTranscriptEpisodes(context.Background(), env, TranscriptEpisodeFilter{
		Filter:     Filter{Agent: AgentCodex, From: from.Add(time.Second), To: to.Add(time.Second)},
		AnchorType: AnchorDesignDirection,
	}, EpisodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(outside) != 0 {
		t.Fatalf("out-of-range episodes = %+v", outside)
	}
}

func TestListTranscriptEpisodeSummaries(t *testing.T) {
	env := syncCodexDesignFixture(t)
	from := time.Date(2026, 8, 20, 1, 0, 6, 0, time.UTC)
	summaries, err := ListTranscriptEpisodeSummaries(context.Background(), env, TranscriptEpisodeFilter{
		Filter:     Filter{Agent: AgentCodex, From: from, To: from},
		AnchorType: AnchorDesignDirection,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 {
		t.Fatalf("summaries = %+v", summaries)
	}
	if summaries[0].EpisodeID == "" || summaries[0].Trigger.Type != string(AnchorDesignDirection) || summaries[0].Trigger.Timestamp != "2026-08-20T01:00:06Z" {
		t.Fatalf("unexpected summary = %+v", summaries[0])
	}
}

func TestNormalizeTranscriptListOptionsBoundsUnspecifiedContext(t *testing.T) {
	got := normalizeTranscriptListOptions(EpisodeOptions{})
	if got.Before != 3 || got.After != 8 || got.ByteBudget != 64*1024 || got.TokenBudget != 0 {
		t.Fatalf("default list options = %+v", got)
	}
	explicit := EpisodeOptions{Before: 1, After: 2}
	if got := normalizeTranscriptListOptions(explicit); got != explicit {
		t.Fatalf("explicit list options changed: got=%+v want=%+v", got, explicit)
	}
}

func TestSearchTranscriptEpisodesReturnsEvidenceCountsAndReason(t *testing.T) {
	env := syncCodexFixture(t)
	result, err := SearchTranscriptEpisodes(context.Background(), env, "migration test failed", Filter{Agent: AgentCodex}, EpisodeOptions{Before: 1, After: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Episodes) != 1 {
		t.Fatalf("episodes = %+v", result)
	}
	if result.Summary.RawEvents != 1 || result.Summary.Sessions != 1 || result.Summary.TaskLineages != 1 || result.Summary.Projects != 1 {
		t.Fatalf("summary = %+v", result.Summary)
	}
	if len(result.Episodes[0].SearchReasons) == 0 || !strings.Contains(result.Episodes[0].SearchReasons[0], "query") {
		t.Fatalf("search reason missing: %+v", result.Episodes[0])
	}
	if result.Episodes[0].Trigger.Type != string(AnchorTestFailure) || result.Episodes[0].TaskStatus != "unknown" {
		t.Fatalf("unexpected episode: %+v", result.Episodes[0])
	}
}

func TestBuildAndWriteTranscriptEpisode(t *testing.T) {
	env := syncCodexFixture(t)
	episodes, err := BuildTranscriptEpisodes(context.Background(), env, Filter{Agent: AgentCodex}, EpisodeOptions{Before: 1, After: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(episodes) == 0 {
		t.Fatal("no episode was built")
	}
	found, err := BuildTranscriptEpisode(context.Background(), env, episodes[0].EpisodeID, EpisodeOptions{Before: 0, After: 1})
	if err != nil {
		t.Fatal(err)
	}
	if found.EpisodeID != episodes[0].EpisodeID {
		t.Fatalf("episode id changed: %q != %q", found.EpisodeID, episodes[0].EpisodeID)
	}
	output := t.TempDir() + "/episode.json"
	if err := WriteTranscriptEpisodeEvidence(context.Background(), env, found.EpisodeID, output, EpisodeOptions{Before: 0, After: 1}); err != nil {
		t.Fatal(err)
	}
}

func syncCodexDesignFixture(t *testing.T) Environment {
	t.Helper()
	home := t.TempDir()
	env := Environment{Home: home, LookupEnv: mapLookup(nil)}
	data, err := os.ReadFile(filepath.Join("testdata", "codex.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte(`{"timestamp":"2026-08-20T01:00:06Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Design direction: use the canonical queryless episode list."}]}}
`)...)
	writeFixture(t, filepath.Join(home, ".codex", "sessions", "codex.jsonl"), data)
	if _, err := Sync(context.Background(), SyncOptions{Environment: env, Agents: []Agent{AgentCodex}}); err != nil {
		t.Fatal(err)
	}
	return env
}
