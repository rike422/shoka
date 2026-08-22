package session

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestJSONLAdaptersReadFixturesWithoutReasoning(t *testing.T) {
	tests := []struct {
		agent Agent
		rel   string
		root  string
	}{
		{agent: AgentCodex, rel: filepath.Join(".codex", "sessions", "2026", "08", "20", "codex-session.jsonl")},
		{agent: AgentClaude, rel: filepath.Join(".claude", "projects", "-tmp-example", "claude-session.jsonl")},
		{agent: AgentCursor, rel: filepath.Join(".cursor", "projects", "tmp-example", "agent-transcripts", "cursor-session", "cursor-session.jsonl")},
		{agent: AgentPi, rel: filepath.Join(".pi", "agent", "sessions", "--tmp-example--", "pi-session.jsonl")},
	}

	for _, tt := range tests {
		t.Run(string(tt.agent), func(t *testing.T) {
			home := t.TempDir()
			data, err := os.ReadFile(filepath.Join("testdata", string(tt.agent)+".jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, tt.rel)
			writeFixture(t, path, data)

			adapter := AdapterFor(tt.agent)
			if adapter == nil {
				t.Fatalf("missing adapter for %s", tt.agent)
			}
			sources, err := adapter.Discover(context.Background(), Environment{Home: home, LookupEnv: mapLookup(nil)})
			if err != nil {
				t.Fatal(err)
			}
			if len(sources) != 1 {
				t.Fatalf("sources = %d, want 1: %+v", len(sources), sources)
			}
			var records []Record
			plan, err := adapter.Prepare(context.Background(), sources[0], Checkpoint{})
			if err != nil {
				t.Fatal(err)
			}
			result, err := adapter.Read(context.Background(), plan, func(record Record) error {
				records = append(records, NormalizeRecord(record))
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(records) < 3 {
				t.Fatalf("records = %d, want >= 3: %+v", len(records), records)
			}
			joined := recordText(records)
			if strings.Contains(joined, "SHOKA_CANARY_REASONING") {
				t.Fatalf("reasoning leaked from %s: %s", tt.agent, joined)
			}
			if strings.Contains(joined, "SHOKA_CANARY_SECRET_VALUE") {
				t.Fatalf("secret leaked from %s: %s", tt.agent, joined)
			}
			if !strings.Contains(joined, "[REDACTED]") {
				t.Fatalf("secret was not deterministically redacted for %s: %s", tt.agent, joined)
			}
			if result.SkippedReasoning == 0 {
				t.Fatalf("%s did not report skipped reasoning", tt.agent)
			}
			seen := map[[2]int64]bool{}
			for _, record := range records {
				key := [2]int64{record.RecordOrdinal, record.PartOrdinal}
				if seen[key] {
					t.Fatalf("duplicate source coordinate %v", key)
				}
				seen[key] = true
			}
		})
	}
}

func TestOpenCodeAdapterUsesAllowlistedTables(t *testing.T) {
	home := t.TempDir()
	dbPath := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`CREATE TABLE credential(id TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO credential(id,value) VALUES('secret','SHOKA_CANARY_REASONING')`,
		`CREATE TABLE project(id TEXT PRIMARY KEY, worktree TEXT NOT NULL, name TEXT, time_updated INTEGER NOT NULL)`,
		`CREATE TABLE session(id TEXT PRIMARY KEY, project_id TEXT NOT NULL, parent_id TEXT, directory TEXT NOT NULL, title TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL)`,
		`CREATE TABLE message(id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL)`,
		`CREATE TABLE part(id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL)`,
		`INSERT INTO project VALUES('project-1','/tmp/example','example',1)`,
		`INSERT INTO session VALUES('opencode-session','project-1',NULL,'/tmp/example','Migration fix',1000,4000)`,
		`INSERT INTO message VALUES('m1','opencode-session',1000,1000,'{"role":"user"}')`,
		`INSERT INTO part VALUES('p1','m1','opencode-session',1000,1000,'{"type":"text","text":"Fix migration. API_TOKEN=SHOKA_CANARY_SECRET_VALUE"}')`,
		`INSERT INTO message VALUES('m2','opencode-session',2000,2000,'{"role":"assistant"}')`,
		`INSERT INTO part VALUES('p2','m2','opencode-session',2000,2000,'{"type":"reasoning","text":"SHOKA_CANARY_REASONING"}')`,
		`INSERT INTO part VALUES('p3','m2','opencode-session',3000,3000,'{"type":"tool","callID":"call-1","tool":"bash","state":{"status":"completed","input":{"command":"go test ./..."},"output":"ok"}}')`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	adapter := AdapterFor(AgentOpenCode)
	sources, err := adapter.Discover(context.Background(), Environment{Home: home, LookupEnv: mapLookup(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 {
		t.Fatalf("sources = %d, want 1: %+v", len(sources), sources)
	}
	var records []Record
	plan, err := adapter.Prepare(context.Background(), sources[0], Checkpoint{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Read(context.Background(), plan, func(record Record) error {
		records = append(records, NormalizeRecord(record))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := recordText(records)
	if strings.Contains(joined, "SHOKA_CANARY_REASONING") || strings.Contains(joined, "SHOKA_CANARY_SECRET_VALUE") {
		t.Fatalf("sensitive data leaked: %s", joined)
	}
	if result.SkippedReasoning != 1 {
		t.Fatalf("skipped reasoning = %d, want 1", result.SkippedReasoning)
	}
	if !strings.Contains(joined, "go test ./...") {
		t.Fatalf("tool command missing: %s", joined)
	}
}

func TestPiToolResultKeepsCallIDAndFailure(t *testing.T) {
	line := []byte(`{"type":"message","id":"pi-result","timestamp":"2026-08-20T01:00:03Z","message":{"role":"toolResult","toolCallId":"pi-call","isError":true,"content":[{"type":"text","text":"tests failed"}]}}`)
	decoded, err := decodePi(Source{Agent: AgentPi, NativeSessionID: "pi-session"}, line)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Records) != 1 {
		t.Fatalf("records = %d, want 1", len(decoded.Records))
	}
	record := NormalizeRecord(decoded.Records[0])
	if record.CallID != "pi-call" || record.ExitCode == nil || *record.ExitCode == 0 || record.Kind != KindError {
		t.Fatalf("Pi tool failure metadata was lost: %+v", record)
	}
}

func recordText(records []Record) string {
	var values []string
	for _, record := range records {
		values = append(values, record.Text, record.Command)
	}
	return strings.Join(values, "\n")
}

func writeFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
