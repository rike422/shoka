package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestListShowSearchAndFilters(t *testing.T) {
	env := syncCodexFixture(t)
	ctx := context.Background()

	sessions, err := ListSessions(ctx, env, Filter{Agent: AgentCodex})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].SessionUID != "codex:codex-session" {
		t.Fatalf("unexpected sessions: %+v", sessions)
	}
	if sessions[0].Repository == "" {
		t.Fatalf("repository was not retained: %+v", sessions[0])
	}
	normalizedRepository, err := ListSessions(ctx, env, Filter{Repository: sessions[0].Repository + string(os.PathSeparator)})
	if err != nil {
		t.Fatal(err)
	}
	if len(normalizedRepository) != 1 {
		t.Fatalf("repository filter was not normalized: %+v", normalizedRepository)
	}

	detail, err := ShowSession(ctx, env, "codex-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Events) < 5 {
		t.Fatalf("too few events: %+v", detail.Events)
	}
	for _, event := range detail.Events {
		if event.Source.Path == "" || event.Source.Hash == "" || event.Source.Line == 0 {
			t.Fatalf("event lacks reverse source reference: %+v", event)
		}
	}

	hits, err := SearchSessions(ctx, env, "go test", Filter{Agent: AgentCodex, Kind: KindTest})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || !strings.Contains(hits[0].Command, "go test") {
		t.Fatalf("command search failed: %+v", hits)
	}
	failures, err := SearchSessions(ctx, env, "migration test failed", Filter{Agent: AgentCodex, Kind: KindTestFailure})
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || failures[0].CallID != "call-1" {
		t.Fatalf("linked test failure search failed: %+v", failures)
	}

	fileHits, err := SearchSessions(ctx, env, "db", Filter{File: "internal/db.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fileHits) != 1 || fileHits[0].Kind != KindFileOperation {
		t.Fatalf("file filter failed: %+v", fileHits)
	}

	from := time.Date(2026, 8, 20, 1, 0, 3, 0, time.UTC)
	to := time.Date(2026, 8, 20, 1, 0, 4, 0, time.UTC)
	dateHits, err := SearchSessions(ctx, env, "go test", Filter{From: from, To: to})
	if err != nil {
		t.Fatal(err)
	}
	if len(dateHits) != 1 {
		t.Fatalf("date filter failed: %+v", dateHits)
	}

	noHits, err := SearchSessions(ctx, env, "migration", Filter{Agent: AgentPi})
	if err != nil {
		t.Fatal(err)
	}
	if len(noHits) != 0 {
		t.Fatalf("agent filter leaked results: %+v", noHits)
	}
}

func TestSubagentsAreHiddenByDefault(t *testing.T) {
	home := t.TempDir()
	env := Environment{Home: home, LookupEnv: mapLookup(nil)}
	rootDir := filepath.Join(home, ".cursor", "projects", "tmp", "agent-transcripts", "root-session")
	writeFixture(t, filepath.Join(rootDir, "root-session.jsonl"), []byte(`{"role":"user","message":{"content":[{"type":"text","text":"root task"}]}}`+"\n"))
	writeFixture(t, filepath.Join(rootDir, "subagents", "child-session.jsonl"), []byte(`{"role":"assistant","message":{"content":[{"type":"text","text":"child evidence"}]}}`+"\n"))
	if _, err := Sync(context.Background(), SyncOptions{Environment: env, Agents: []Agent{AgentCursor}}); err != nil {
		t.Fatal(err)
	}
	rootOnly, err := ListSessions(context.Background(), env, Filter{Agent: AgentCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(rootOnly) != 1 || rootOnly[0].RoleInTree != "root" {
		t.Fatalf("default list should contain one root: %+v", rootOnly)
	}
	all, err := ListSessions(context.Background(), env, Filter{Agent: AgentCursor, IncludeSubagents: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("include-subagents should contain two sessions: %+v", all)
	}
	child, err := ShowSession(context.Background(), env, "child-session")
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentSessionUID != "cursor:root-session" {
		t.Fatalf("child parent = %q", child.ParentSessionUID)
	}
}

func TestSessionEvidenceIsDeterministicBoundedAndTraceable(t *testing.T) {
	env := syncCodexFixture(t)
	ctx := context.Background()
	first, err := BuildSessionEvidence(ctx, env, "codex-session")
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildSessionEvidence(ctx, env, "codex-session")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("evidence changed between runs:\n%+v\n%+v", first, second)
	}
	if first.SchemaVersion != "session-evidence/v1" {
		t.Fatalf("unexpected schema: %q", first.SchemaVersion)
	}
	if first.Task == nil || !strings.Contains(first.Task.Text, "sqlite migration") {
		t.Fatalf("task missing: %+v", first)
	}
	if len(first.UserInputs) == 0 || len(first.Commands) == 0 || len(first.FilesChanged) == 0 || len(first.Errors) == 0 || first.FinalResponse == nil {
		t.Fatalf("evidence fields missing: %+v", first)
	}
	if first.Commands[0].Outcome != "failure" || first.Commands[0].Output == nil {
		t.Fatalf("command outcome was not linked: %+v", first.Commands[0])
	}
	for _, item := range first.UserInputs {
		if item.Source.Path == "" || item.Source.Hash == "" || item.Source.Line == 0 {
			t.Fatalf("user input is not traceable: %+v", item)
		}
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "SHOKA_CANARY_REASONING") || strings.Contains(string(encoded), "SHOKA_CANARY_SECRET_VALUE") {
		t.Fatalf("sensitive data leaked to evidence: %s", encoded)
	}
	if len(encoded) > maxSessionEvidenceBytes {
		t.Fatalf("evidence exceeds cap: %d", len(encoded))
	}
	if strings.Contains(string(encoded), `"events"`) || strings.Contains(string(encoded), `"highlights"`) {
		t.Fatalf("raw events or semantic highlights leaked into evidence: %s", encoded)
	}

	output := filepath.Join(t.TempDir(), "evidence.json")
	if err := WriteSessionEvidence(ctx, env, "codex-session", output); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(output)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("evidence mode = %o, want 600", got)
		}
	}
}

func TestSessionEvidenceSchemaDocumentMatchesProducerVersion(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "session-evidence-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			Const string `json:"const"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Properties["schema_version"].Const != sessionEvidenceVersion {
		t.Fatalf("schema document version drifted: %q != %q", schema.Properties["schema_version"].Const, sessionEvidenceVersion)
	}
}

func syncCodexFixture(t *testing.T) Environment {
	t.Helper()
	home := t.TempDir()
	env := Environment{Home: home, LookupEnv: mapLookup(nil)}
	data, err := os.ReadFile(filepath.Join("testdata", "codex.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(home, ".codex", "sessions", "codex.jsonl"), data)
	if _, err := Sync(context.Background(), SyncOptions{Environment: env, Agents: []Agent{AgentCodex}}); err != nil {
		t.Fatal(err)
	}
	return env
}
