package session

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEpisodeAnchorCandidatesAreDeterministic(t *testing.T) {
	events := []Event{
		{ID: 1, Role: RoleUser, Kind: KindMessage, Text: "Implement the sqlite migration."},
		{ID: 2, Role: RoleAssistant, Kind: KindMessage, Text: "I will update the schema."},
		{ID: 3, Role: RoleUser, Kind: KindMessage, Text: "Implement the sqlite migration."},
		{ID: 4, Role: RoleUser, Kind: KindMessage, Text: "Scope: only change the session package."},
		{ID: 5, Role: RoleUser, Kind: KindMessage, Text: "設計方針は既存の契約を維持すること。"},
		{ID: 6, Role: RoleTool, Kind: KindTestFailure, Text: "go test failed"},
	}
	want := []AnchorType{"", "", AnchorRequirementRestatement, AnchorScopeRevision, AnchorDesignDirection, AnchorTestFailure}
	got := EpisodeAnchorTypes(events)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("anchor types = %v, want %v", got, want)
	}
	if gotAgain := EpisodeAnchorTypes(events); !reflect.DeepEqual(got, gotAgain) {
		t.Fatalf("anchor detection was not deterministic: %v != %v", got, gotAgain)
	}
}

func TestBuildTranscriptEpisodeRetainsTraceableEvidenceAndUnknownOutcome(t *testing.T) {
	zero := 0
	events := []Event{
		{ID: 10, Role: RoleUser, Kind: KindMessage, Text: "Original request", Timestamp: "2026-08-22T00:00:00Z", Source: testSource("before", 10)},
		{ID: 11, Role: RoleUser, Kind: KindUserCorrection, Text: "違う、修正して。use the existing contract", Timestamp: "2026-08-22T00:00:01Z", Source: testSource("anchor", 11)},
		{ID: 12, Role: RoleAssistant, Kind: KindFileOperation, Files: []string{"internal/session/episode.go"}, Diff: "-old\n+new", Timestamp: "2026-08-22T00:00:02Z", Source: testSource("file", 12)},
		{ID: 13, Role: RoleAssistant, Kind: KindTest, Command: "go test ./internal/session", CallID: "call-13", Timestamp: "2026-08-22T00:00:03Z", Source: testSource("command", 13)},
		{ID: 14, Role: RoleTool, Kind: KindCommandOutput, CallID: "other-call", Text: "ok", ExitCode: &zero, Timestamp: "2026-08-22T00:00:04Z", Source: testSource("output", 14)},
		{ID: 15, Role: RoleAssistant, Kind: KindMessage, Text: "I updated the file; result is not confirmed.", Timestamp: "2026-08-22T00:00:05Z", Source: testSource("report", 15)},
	}
	episode, err := BuildTranscriptEpisodeFromEvents(TranscriptBuildInput{
		Session: SessionInfo{SessionUID: "codex:s1", Agent: AgentCodex, NativeID: "s1", ProjectID: "project:p1", WorkspaceID: "workspace:w1", TaskLineageID: "lineage:l1", Status: "active"},
		Events:  events, AnchorIndex: 1, Options: EpisodeOptions{Before: 1, After: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	if episode.SchemaVersion != "transcript-episode/v1" || episode.Trigger.Type != string(KindUserCorrection) || episode.Trigger.EventID != 11 || episode.Trigger.Timestamp != "2026-08-22T00:00:01Z" {
		t.Fatalf("unexpected episode identity: %+v", episode)
	}
	if episode.TranscriptID != "codex:s1" {
		t.Fatalf("transcript id = %q", episode.TranscriptID)
	}
	if episode.NativeSessionID != "s1" {
		t.Fatalf("native session id = %q", episode.NativeSessionID)
	}
	if episode.ProjectID != "project:p1" || episode.WorkspaceID != "workspace:w1" || episode.TaskLineageID != "lineage:l1" {
		t.Fatalf("identity was not retained: %+v", episode)
	}
	if len(episode.ContextBefore) != 1 || episode.ContextBefore[0].EventID != 10 {
		t.Fatalf("context_before = %+v", episode.ContextBefore)
	}
	if len(episode.Corrections) != 1 || episode.Corrections[0].EventID != 11 {
		t.Fatalf("corrections = %+v", episode.Corrections)
	}
	if len(episode.ActionsAfter) != 4 || episode.ActionsAfter[0].EventID != 12 {
		t.Fatalf("actions_after = %+v", episode.ActionsAfter)
	}
	if episode.Outcomes[0].Outcome != "unknown" || episode.TaskStatus != "unknown" {
		t.Fatalf("unknown result was asserted: outcomes=%+v status=%q", episode.Outcomes, episode.TaskStatus)
	}
	if len(episode.SourceEventIDs) != 6 || episode.SourceEventIDs[0] != 10 || episode.SourceEventIDs[5] != 15 {
		t.Fatalf("source event ids = %v", episode.SourceEventIDs)
	}
	for _, item := range append(append(append([]TranscriptEvidence{}, episode.ContextBefore...), episode.Corrections...), episode.ActionsAfter...) {
		if item.Source.Hash == "" || item.EventID == 0 {
			t.Fatalf("evidence lacks source identity: %+v", item)
		}
	}
}

func TestEpisodeBudgetRecordsOmissions(t *testing.T) {
	var events []Event
	for i := 1; i <= 8; i++ {
		events = append(events, Event{ID: int64(i), Role: RoleAssistant, Kind: KindMessage, Text: strings.Repeat("x", 40), Timestamp: time.Unix(int64(i), 0).UTC().Format(time.RFC3339), Source: testSource("event", int64(i))})
	}
	events[0].Role = RoleUser
	events[0].Kind = KindUserCorrection
	episode, err := BuildTranscriptEpisodeFromEvents(TranscriptBuildInput{Session: SessionInfo{SessionUID: "s", ProjectID: "p", TaskLineageID: "l"}, Events: events, AnchorIndex: 0, Options: EpisodeOptions{Before: 0, After: 1, ByteBudget: 300}})
	if err != nil {
		t.Fatal(err)
	}
	if episode.Omitted.Events <= 0 || len(episode.SourceEventIDs) >= len(events) {
		t.Fatalf("budget omission was not recorded: omitted=%+v ids=%v", episode.Omitted, episode.SourceEventIDs)
	}
}

func TestBuildTranscriptEpisodeRejectsUntraceableEvidence(t *testing.T) {
	_, err := BuildTranscriptEpisodeFromEvents(TranscriptBuildInput{
		Session:     SessionInfo{SessionUID: "s", ProjectID: "p", TaskLineageID: "l"},
		Events:      []Event{{ID: 1, Role: RoleUser, Kind: KindUserCorrection, Text: "correct this"}},
		AnchorIndex: 0,
	})
	if err == nil || !strings.Contains(err.Error(), "source") {
		t.Fatalf("missing source was accepted: %v", err)
	}
}

func TestTranscriptEpisodeOmissionsUseOutcomeField(t *testing.T) {
	episode := TranscriptEpisode{Omitted: TranscriptOmissions{Outcomes: 1}}
	encoded, err := json.Marshal(episode)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"outcomes":1`) || strings.Contains(string(encoded), `"results"`) {
		t.Fatalf("unexpected omission fields: %s", encoded)
	}
}

func TestEpisodeOutcomeAndStatusRules(t *testing.T) {
	zero, one := 0, 1
	events := []Event{
		{ID: 1, Role: RoleUser, Kind: KindUserCorrection, Text: "correct this", Source: testSource("anchor", 1)},
		{ID: 2, Role: RoleAssistant, Kind: KindTest, Command: "go test", CallID: "ok", Source: testSource("test", 2)},
		{ID: 3, Role: RoleTool, Kind: KindCommandOutput, CallID: "ok", ExitCode: &zero, Source: testSource("ok", 3)},
		{ID: 4, Role: RoleAssistant, Kind: KindTest, Command: "go test", CallID: "bad", Source: testSource("test2", 4)},
		{ID: 5, Role: RoleTool, Kind: KindTestFailure, CallID: "bad", ExitCode: &one, Source: testSource("bad", 5)},
		{ID: 6, Role: RoleSystem, Kind: KindStatus, Text: "complete", Source: testSource("status", 6)},
	}
	episode, err := BuildTranscriptEpisodeFromEvents(TranscriptBuildInput{Session: SessionInfo{SessionUID: "s", ProjectID: "p", TaskLineageID: "l", Status: "active"}, Events: events, AnchorIndex: 0, Options: EpisodeOptions{After: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if episode.TaskStatus != "complete" || len(episode.Outcomes) != 5 {
		t.Fatalf("status/outcomes = %q/%+v", episode.TaskStatus, episode.Outcomes)
	}
	if episode.Outcomes[0].Outcome != "success" || episode.Outcomes[1].Outcome != "success" || episode.Outcomes[2].Outcome != "failure" || episode.Outcomes[3].Outcome != "failure" || episode.Outcomes[4].Outcome != "success" {
		t.Fatalf("outcome order = %+v", episode.Outcomes)
	}
}

func TestSortTranscriptEpisodesAndRequirementSimilarity(t *testing.T) {
	events := []Event{
		{ID: 1, Role: RoleUser, Kind: KindMessage, Text: "Fix the database migration now"},
		{ID: 2, Role: RoleUser, Kind: KindMessage, Text: "Fix database migration now"},
	}
	if got := EpisodeAnchorTypes(events); got[1] != AnchorRequirementRestatement {
		t.Fatalf("similar requirement was not detected: %v", got)
	}
	episodes := []TranscriptEpisode{{TranscriptID: "b", Trigger: TranscriptTrigger{EventID: 2}}, {TranscriptID: "a", Trigger: TranscriptTrigger{EventID: 3}}, {TranscriptID: "a", Trigger: TranscriptTrigger{EventID: 1}}}
	SortTranscriptEpisodes(episodes)
	if episodes[0].TranscriptID != "a" || episodes[0].Trigger.EventID != 1 || episodes[2].TranscriptID != "b" {
		t.Fatalf("episodes were not sorted: %+v", episodes)
	}
}

func testSource(name string, id int64) SourceReference {
	return SourceReference{Path: name + ".jsonl", Hash: "hash-" + name, Line: id, Offset: id * 10, State: "ok"}
}
