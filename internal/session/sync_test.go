package session

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSyncIsIdempotentAndHandlesAppendRebuildMissing(t *testing.T) {
	home := t.TempDir()
	env := Environment{Home: home, LookupEnv: mapLookup(nil)}
	logPath := filepath.Join(home, ".codex", "sessions", "2026", "08", "20", "sync-session.jsonl")
	completePrefix := strings.Join([]string{
		`{"timestamp":"2026-08-20T01:00:00Z","type":"session_meta","payload":{"id":"sync-session","cwd":"` + home + `","timestamp":"2026-08-20T01:00:00Z"}}`,
		`{"timestamp":"2026-08-20T01:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"same request"}]}}`,
		`{"timestamp":"2026-08-20T01:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"same request"}]}}`,
		`{malformed}`,
	}, "\n") + "\n"
	incomplete := `{"timestamp":"2026-08-20T01:00:03Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"later`
	writeFixture(t, logPath, []byte(completePrefix+incomplete))

	stats, err := Sync(context.Background(), SyncOptions{Environment: env, Agents: []Agent{AgentCodex}})
	if err != nil {
		t.Fatal(err)
	}
	if stats.SourcesRead != 1 || stats.EventsAdded != 2 || stats.Malformed != 1 {
		t.Fatalf("unexpected initial stats: %+v", stats)
	}
	db := openTestSessionDB(t, env)
	defer db.Close()
	assertCount(t, db, "events", 2)
	assertCount(t, db, "event_contents", 1) // duplicate occurrences share one retained payload
	assertCount(t, db, "events_fts", 1)     // exact-body duplicates remain evidence but index once
	var collapsedOccurrences int
	if err := db.QueryRow(`SELECT count(*) FROM events WHERE duplicate_of IS NOT NULL`).Scan(&collapsedOccurrences); err != nil {
		t.Fatal(err)
	}
	if collapsedOccurrences != 0 {
		t.Fatal("same text at different times was treated as the same occurrence")
	}
	var ftsDDL string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='events_fts'`).Scan(&ftsDDL); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ftsDDL, "content=''") {
		t.Fatalf("FTS is not contentless: %s", ftsDDL)
	}
	var cursor, size int64
	var malformed int
	if err := db.QueryRow(`SELECT cursor_offset, size, malformed_count FROM sources`).Scan(&cursor, &size, &malformed); err != nil {
		t.Fatal(err)
	}
	if cursor != int64(len(completePrefix)) || cursor >= size || malformed != 1 {
		t.Fatalf("cursor=%d size=%d malformed=%d", cursor, size, malformed)
	}
	initialIDs := eventIDs(t, db)
	var initialSourceHash string
	if err := db.QueryRow(`SELECT source_hash FROM events ORDER BY id LIMIT 1`).Scan(&initialSourceHash); err != nil {
		t.Fatal(err)
	}
	if initialSourceHash == "" {
		t.Fatal("initial event has no acquisition source hash")
	}
	initialRaw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("%x", sha256.Sum256(initialRaw)); initialSourceHash != want {
		t.Fatalf("initial source hash = %q, want %q", initialSourceHash, want)
	}
	var hashStateBytes int
	var guardHash, fileID string
	if err := db.QueryRow(`SELECT length(hash_state),guard_hash,file_id FROM sources`).Scan(&hashStateBytes, &guardHash, &fileID); err != nil {
		t.Fatal(err)
	}
	if hashStateBytes == 0 || guardHash == "" || fileID == "" {
		t.Fatalf("incremental hash checkpoint is incomplete: state=%d guard=%q file=%q", hashStateBytes, guardHash, fileID)
	}

	stats, err = Sync(context.Background(), SyncOptions{Environment: env, Agents: []Agent{AgentCodex}})
	if err != nil {
		t.Fatal(err)
	}
	if stats.SourcesUnchanged != 1 || stats.EventsAdded != 0 {
		t.Fatalf("unexpected idempotent stats: %+v", stats)
	}
	if got := eventIDs(t, db); !equalInt64s(got, initialIDs) {
		t.Fatalf("event IDs changed: before=%v after=%v", initialIDs, got)
	}
	var existingContentID int64
	if err := db.QueryRow(`SELECT id FROM event_contents ORDER BY id LIMIT 1`).Scan(&existingContentID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM events_fts WHERE rowid=?`, existingContentID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO events_fts(rowid,body_terms,command_terms,file_terms) VALUES(?,?,?,?)`, existingContentID, "preserve_existing_fts_row", "", ""); err != nil {
		t.Fatal(err)
	}

	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`"}]}}` + "\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	stats, err = Sync(context.Background(), SyncOptions{Environment: env, Agents: []Agent{AgentCodex}})
	if err != nil {
		t.Fatal(err)
	}
	if stats.EventsAdded != 1 || stats.SourcesRebuilt != 0 {
		t.Fatalf("append was not incremental: %+v", stats)
	}
	assertCount(t, db, "events", 3)
	if got := eventIDs(t, db); !equalInt64s(got[:2], initialIDs) {
		t.Fatalf("append changed existing IDs: before=%v after=%v", initialIDs, got)
	}
	var oldHash, appendedHash string
	if err := db.QueryRow(`SELECT source_hash FROM events ORDER BY record_ordinal,part_ordinal LIMIT 1`).Scan(&oldHash); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT source_hash FROM events ORDER BY record_ordinal DESC,part_ordinal DESC LIMIT 1`).Scan(&appendedHash); err != nil {
		t.Fatal(err)
	}
	if oldHash != initialSourceHash || appendedHash == "" || appendedHash == oldHash {
		t.Fatalf("acquisition hashes not preserved: initial=%q old=%q appended=%q", initialSourceHash, oldHash, appendedHash)
	}
	appendedRaw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("%x", sha256.Sum256(appendedRaw)); appendedHash != want {
		t.Fatalf("incremental source hash = %q, want %q", appendedHash, want)
	}
	var preservedFTSRows int
	if err := db.QueryRow(`SELECT count(*) FROM events_fts WHERE events_fts MATCH 'preserve_existing_fts_row'`).Scan(&preservedFTSRows); err != nil {
		t.Fatal(err)
	}
	if preservedFTSRows != 1 {
		t.Fatal("append-only sync rewrote an existing FTS row")
	}

	replacement := strings.Join([]string{
		`{"timestamp":"2026-08-21T01:00:00Z","type":"session_meta","payload":{"id":"sync-session","cwd":"` + home + `","timestamp":"2026-08-21T01:00:00Z"}}`,
		`{"timestamp":"2026-08-21T01:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"replacement request"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(logPath, []byte(replacement), 0o600); err != nil {
		t.Fatal(err)
	}
	stats, err = Sync(context.Background(), SyncOptions{Environment: env, Agents: []Agent{AgentCodex}})
	if err != nil {
		t.Fatal(err)
	}
	if stats.SourcesRebuilt != 1 {
		t.Fatalf("truncate/prefix replacement did not rebuild: %+v", stats)
	}
	assertCount(t, db, "events", 1)
	assertCount(t, db, "events_fts", 1)
	var body string
	if err := db.QueryRow(`SELECT text FROM event_contents`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if body != "replacement request" {
		t.Fatalf("stale event survived rebuild: %q", body)
	}

	if err := os.Remove(logPath); err != nil {
		t.Fatal(err)
	}
	stats, err = Sync(context.Background(), SyncOptions{Environment: env, Agents: []Agent{AgentCodex}})
	if err != nil {
		t.Fatal(err)
	}
	if stats.SourcesMissing != 1 {
		t.Fatalf("missing source not reported: %+v", stats)
	}
	assertCount(t, db, "events", 1)
	var state string
	if err := db.QueryRow(`SELECT state FROM sources`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "missing" {
		t.Fatalf("source state = %q", state)
	}

	writeFixture(t, logPath, []byte(replacement))
	if _, err := Sync(context.Background(), SyncOptions{Environment: env, Agents: []Agent{AgentCodex}}); err != nil {
		t.Fatal(err)
	}
	assertCount(t, db, "events", 1)
}

func TestSyncPersistsOnlyPrivateRedactedText(t *testing.T) {
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
	path, err := StateDBPath(env)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("database mode = %o, want 600", got)
		}
	}
	db := openTestSessionDB(t, env)
	defer db.Close()
	var text string
	if err := db.QueryRow(`SELECT group_concat(text || ' ' || command, ' ') FROM event_contents`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "SHOKA_CANARY_REASONING") || strings.Contains(text, "SHOKA_CANARY_SECRET_VALUE") {
		t.Fatalf("sensitive content persisted: %s", text)
	}
	if !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("redaction marker missing: %s", text)
	}
}

func TestSyncDryRunDoesNotCreateState(t *testing.T) {
	home := t.TempDir()
	env := Environment{Home: home, LookupEnv: mapLookup(nil)}
	data, err := os.ReadFile(filepath.Join("testdata", "pi.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(home, ".pi", "agent", "sessions", "--tmp--", "pi.jsonl"), data)
	stats, err := Sync(context.Background(), SyncOptions{Environment: env, Agents: []Agent{AgentPi}, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if stats.SourcesRead != 1 || stats.EventsAdded == 0 {
		t.Fatalf("unexpected dry-run stats: %+v", stats)
	}
	path, err := StateDBPath(env)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("dry-run created state database: %v", err)
	}
}

func TestSyncRebuildsChangedOpenCodePart(t *testing.T) {
	home := t.TempDir()
	env := Environment{Home: home, LookupEnv: mapLookup(nil)}
	dbPath := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	sourceDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`CREATE TABLE session(id TEXT PRIMARY KEY, project_id TEXT NOT NULL, parent_id TEXT, directory TEXT NOT NULL, title TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL)`,
		`CREATE TABLE message(id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL)`,
		`CREATE TABLE part(id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL)`,
		`INSERT INTO session VALUES('session-1','project-1',NULL,'/tmp/example','Task',1000,1000)`,
		`INSERT INTO message VALUES('message-1','session-1',1000,1000,'{"role":"assistant"}')`,
		`INSERT INTO part VALUES('part-1','message-1','session-1',1000,1000,'{"type":"text","text":"before"}')`,
	}
	for _, statement := range statements {
		if _, err := sourceDB.Exec(statement); err != nil {
			_ = sourceDB.Close()
			t.Fatal(err)
		}
	}
	if err := sourceDB.Close(); err != nil {
		t.Fatal(err)
	}

	initialStats, err := Sync(context.Background(), SyncOptions{Environment: env, Agents: []Agent{AgentOpenCode}})
	if err != nil {
		t.Fatal(err)
	}
	if len(initialStats.Errors) > 0 {
		t.Fatalf("initial OpenCode sync errors: %+v", initialStats.Errors)
	}
	indexDB := openTestSessionDB(t, env)
	defer func() { _ = indexDB.Close() }()
	var beforeHash string
	if err := indexDB.QueryRow(`SELECT e.source_hash FROM events e JOIN event_contents c ON c.id=e.content_id WHERE c.text='before'`).Scan(&beforeHash); err != nil {
		t.Fatal(err)
	}

	sourceDB, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sourceDB.Exec(`UPDATE part SET time_updated=2000,data='{"type":"text","text":"after"}' WHERE id='part-1'`); err != nil {
		_ = sourceDB.Close()
		t.Fatal(err)
	}
	if err := sourceDB.Close(); err != nil {
		t.Fatal(err)
	}

	stats, err := Sync(context.Background(), SyncOptions{Environment: env, Agents: []Agent{AgentOpenCode}})
	if err != nil {
		t.Fatal(err)
	}
	if stats.SourcesRebuilt != 1 || stats.EventsAdded != 1 {
		t.Fatalf("changed OpenCode source was not rebuilt: %+v", stats)
	}
	var text, afterHash string
	if err := indexDB.QueryRow(`SELECT c.text,e.source_hash FROM events e JOIN event_contents c ON c.id=e.content_id`).Scan(&text, &afterHash); err != nil {
		t.Fatal(err)
	}
	if text != "after" || afterHash == "" || afterHash == beforeHash {
		t.Fatalf("stale OpenCode event survived: text=%q before_hash=%q after_hash=%q", text, beforeHash, afterHash)
	}
}

func TestReindexDeletesDerivedStateAndRebuildsFromRawSources(t *testing.T) {
	home := t.TempDir()
	env := Environment{Home: home, LookupEnv: mapLookup(nil)}
	logPath := filepath.Join(home, ".codex", "sessions", "reindex.jsonl")
	writeFixture(t, logPath, []byte(strings.Join([]string{
		`{"timestamp":"2026-08-20T01:00:00Z","type":"session_meta","payload":{"id":"reindex-session","cwd":"/tmp/example"}}`,
		`{"timestamp":"2026-08-20T01:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"original evidence"}]}}`,
	}, "\n")+"\n"))
	if _, err := Sync(context.Background(), SyncOptions{Environment: env, Agents: []Agent{AgentCodex}}); err != nil {
		t.Fatal(err)
	}
	db := openTestSessionDB(t, env)
	if _, err := db.Exec(`UPDATE event_contents SET text='stale derived value'`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE stale_marker(value TEXT)`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	stats, err := Reindex(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if stats.SourcesRead != 1 || stats.EventsAdded != 1 {
		t.Fatalf("unexpected reindex stats: %+v", stats)
	}
	db = openTestSessionDB(t, env)
	defer db.Close()
	var text string
	if err := db.QueryRow(`SELECT text FROM event_contents`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if text != "original evidence" {
		t.Fatalf("reindex did not rebuild raw evidence: %q", text)
	}
	var staleTables int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='stale_marker'`).Scan(&staleTables); err != nil {
		t.Fatal(err)
	}
	if staleTables != 0 {
		t.Fatal("old derived database survived reindex")
	}
}

func TestReindexPreflightFailurePreservesExistingDatabase(t *testing.T) {
	home := t.TempDir()
	env := Environment{Home: home, LookupEnv: mapLookup(nil)}
	store, err := OpenStore(env)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TABLE keep_me(value TEXT)`); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Reindex(context.Background(), env); err == nil {
		t.Fatal("reindex without raw sources succeeded")
	}
	db := openTestSessionDB(t, env)
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='keep_me'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("preflight failure deleted the existing database")
	}
}

func TestStateLockExcludesConcurrentMutation(t *testing.T) {
	env := Environment{Home: t.TempDir(), LookupEnv: mapLookup(nil)}
	release, err := acquireStateLock(env)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(context.Background(), SyncOptions{Environment: env, Agents: []Agent{AgentCodex}}); err == nil || !strings.Contains(err.Error(), "session state is locked") {
		t.Fatalf("concurrent sync was not rejected: %v", err)
	}
	release()
	dir, err := ResolveStateDir(env)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, stateLockName)); err != nil {
		t.Fatalf("kernel-backed lock file should remain reusable: %v", err)
	}
	releaseAgain, err := acquireStateLock(env)
	if err != nil {
		t.Fatalf("released lock could not be reacquired: %v", err)
	}
	releaseAgain()
}

func openTestSessionDB(t *testing.T, env Environment) *sql.DB {
	t.Helper()
	path, err := StateDBPath(env)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func assertCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}

func eventIDs(t *testing.T, db *sql.DB) []int64 {
	t.Helper()
	rows, err := db.Query(`SELECT id FROM events ORDER BY record_ordinal, part_ordinal`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var values []int64
	for rows.Next() {
		var value int64
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}

func equalInt64s(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
