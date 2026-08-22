package session

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3" // register sqlite3 driver

	"github.com/rike422/shoka/internal/root"
	"github.com/rike422/shoka/internal/tokenize"
)

const sessionSchemaVersion = "3"

// Store owns the user-level session index.
type Store struct {
	db   *sql.DB
	path string
}

// OpenStore opens or creates the private session index.
func OpenStore(env Environment) (*Store, error) {
	path, err := StateDBPath(env)
	if err != nil {
		return nil, err
	}
	if err := EnsureStateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
	file, err := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		if closeErr := file.Close(); closeErr != nil {
			return nil, closeErr
		}
	} else if !os.IsExist(err) {
		return nil, err
	}
	db, err := sql.Open("sqlite3", uri+"?_fk=1&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	store := &Store{db: db, path: abs}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := os.Chmod(abs, 0o600); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// Close closes the session index.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) init() error {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='meta'`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		var version string
		if err := s.db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&version); err != nil {
			return err
		}
		if version != sessionSchemaVersion {
			return fmt.Errorf("unsupported session schema %q (expected %s; run: shoka session reindex)", version, sessionSchemaVersion)
		}
		return nil
	}
	if _, err := s.db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		return err
	}
	statements := []string{
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE sources (
			id INTEGER PRIMARY KEY,
			agent TEXT NOT NULL,
			kind TEXT NOT NULL,
			logical_id TEXT NOT NULL,
			path TEXT NOT NULL,
			native_session_id TEXT NOT NULL DEFAULT '',
			external_version TEXT NOT NULL DEFAULT '',
			size INTEGER NOT NULL DEFAULT 0,
			mtime_ns INTEGER NOT NULL DEFAULT 0,
			cursor_offset INTEGER NOT NULL DEFAULT 0,
			cursor_ordinal INTEGER NOT NULL DEFAULT 0,
			content_hash TEXT NOT NULL DEFAULT '',
			hash_state BLOB NOT NULL DEFAULT X'',
			guard_hash TEXT NOT NULL DEFAULT '',
			file_id TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL DEFAULT 'ok',
			malformed_count INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			last_synced_at INTEGER NOT NULL DEFAULT 0,
			UNIQUE(agent, logical_id)
		)`,
		`CREATE INDEX sources_agent_state ON sources(agent, state)`,
		`CREATE TABLE sessions (
			id INTEGER PRIMARY KEY,
			session_uid TEXT NOT NULL UNIQUE,
			agent TEXT NOT NULL,
			native_id TEXT NOT NULL,
			parent_session_uid TEXT NOT NULL DEFAULT '',
			role_in_tree TEXT NOT NULL DEFAULT 'root',
			title TEXT NOT NULL DEFAULT '',
			cwd TEXT NOT NULL DEFAULT '',
			repository TEXT NOT NULL DEFAULT '',
			started_at INTEGER NOT NULL DEFAULT 0,
			ended_at INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'unknown',
			event_count INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX sessions_agent_time ON sessions(agent, started_at DESC)`,
		`CREATE INDEX sessions_repo_time ON sessions(repository, started_at DESC)`,
		`CREATE INDEX sessions_parent ON sessions(parent_session_uid)`,
		`CREATE TABLE event_contents (
			id INTEGER PRIMARY KEY,
			session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			content_hash TEXT NOT NULL,
			text TEXT NOT NULL DEFAULT '',
			text_truncated INTEGER NOT NULL DEFAULT 0,
			text_original_bytes INTEGER NOT NULL DEFAULT 0,
			text_sha256 TEXT NOT NULL DEFAULT '',
			command TEXT NOT NULL DEFAULT '',
			command_truncated INTEGER NOT NULL DEFAULT 0,
			command_original_bytes INTEGER NOT NULL DEFAULT 0,
			command_sha256 TEXT NOT NULL DEFAULT '',
			diff TEXT NOT NULL DEFAULT '',
			diff_truncated INTEGER NOT NULL DEFAULT 0,
			diff_original_bytes INTEGER NOT NULL DEFAULT 0,
			diff_sha256 TEXT NOT NULL DEFAULT '',
			UNIQUE(session_id, content_hash)
		)`,
		`CREATE INDEX event_contents_session ON event_contents(session_id)`,
		`CREATE TABLE events (
			id INTEGER PRIMARY KEY,
			session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			content_id INTEGER NOT NULL REFERENCES event_contents(id) ON DELETE RESTRICT,
			source_id INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
			record_ordinal INTEGER NOT NULL,
			part_ordinal INTEGER NOT NULL,
			byte_offset INTEGER NOT NULL DEFAULT 0,
			source_line INTEGER NOT NULL DEFAULT 0,
			native_event_id TEXT NOT NULL DEFAULT '',
			parent_native_id TEXT NOT NULL DEFAULT '',
			ts INTEGER NOT NULL DEFAULT 0,
			role TEXT NOT NULL,
			kind TEXT NOT NULL,
			tool_name TEXT NOT NULL DEFAULT '',
			call_id TEXT NOT NULL DEFAULT '',
			exit_code INTEGER,
			source_hash TEXT NOT NULL DEFAULT '',
			duplicate_of INTEGER REFERENCES events(id) ON DELETE SET NULL,
			UNIQUE(source_id, record_ordinal, part_ordinal)
		)`,
		`CREATE INDEX events_session_order ON events(session_id, ts, source_id, record_ordinal, part_ordinal)`,
		`CREATE INDEX events_kind_time ON events(kind, ts DESC)`,
		`CREATE INDEX events_content ON events(content_id)`,
		`CREATE TABLE event_files (
			content_id INTEGER NOT NULL REFERENCES event_contents(id) ON DELETE CASCADE,
			path TEXT NOT NULL,
			PRIMARY KEY(content_id, path)
		)`,
		`CREATE INDEX event_files_path ON event_files(path)`,
		`CREATE VIRTUAL TABLE events_fts USING fts5(
			body_terms,
			command_terms,
			file_terms,
			content='',
			contentless_delete=1,
			tokenize = 'unicode61'
		)`,
		`INSERT INTO meta(key,value) VALUES('schema_version','` + sessionSchemaVersion + `')`,
		`INSERT INTO meta(key,value) VALUES('derive_version','session-evidence-v1')`,
		`INSERT INTO meta(key,value) VALUES('redaction_version','1')`,
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("session schema: %w\nstmt: %s", err, statement)
		}
	}
	return tx.Commit()
}

type storedSource struct {
	ID         int64
	LogicalID  string
	Checkpoint Checkpoint
	State      string
}

func (s *Store) sourcesForAgent(ctx context.Context, agent Agent) (map[string]storedSource, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, logical_id, size, mtime_ns, cursor_offset, cursor_ordinal,
		       content_hash, hash_state, guard_hash, file_id, external_version, state
		FROM sources WHERE agent=?`, agent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := map[string]storedSource{}
	for rows.Next() {
		var source storedSource
		if err := rows.Scan(
			&source.ID,
			&source.LogicalID,
			&source.Checkpoint.Size,
			&source.Checkpoint.MtimeNS,
			&source.Checkpoint.CursorOffset,
			&source.Checkpoint.CursorOrdinal,
			&source.Checkpoint.ContentHash,
			&source.Checkpoint.HashState,
			&source.Checkpoint.GuardHash,
			&source.Checkpoint.FileID,
			&source.Checkpoint.ExternalVersion,
			&source.State,
		); err != nil {
			return nil, err
		}
		values[source.LogicalID] = source
	}
	return values, rows.Err()
}

func ensureSource(tx *sql.Tx, source Source) (int64, error) {
	_, err := tx.Exec(`
		INSERT INTO sources(agent,kind,logical_id,path,native_session_id,state)
		VALUES(?,?,?,?,?,'ok')
		ON CONFLICT(agent,logical_id) DO UPDATE SET
			kind=excluded.kind,
			path=excluded.path,
			native_session_id=excluded.native_session_id`,
		source.Agent, source.Kind, source.LogicalID, source.Path, source.NativeSessionID)
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRow(`SELECT id FROM sources WHERE agent=? AND logical_id=?`, source.Agent, source.LogicalID).Scan(&id)
	return id, err
}

func ensureSession(tx *sql.Tx, source Source, record Record, fallback time.Time) (int64, error) {
	nativeID := record.NativeSessionID
	if nativeID == "" {
		nativeID = source.NativeSessionID
	}
	if nativeID == "" {
		return 0, fmt.Errorf("source %s emitted a record without a session id", source.Path)
	}
	uid := sessionUID(source.Agent, nativeID)
	parentNative := source.ParentNativeSessionID
	parentUID := ""
	roleInTree := "root"
	if parentNative != "" {
		parentUID = sessionUID(source.Agent, parentNative)
		roleInTree = "subagent"
	}
	cwd := record.Cwd
	if cwd == "" {
		cwd = source.Cwd
	}
	repository := resolveRepository(cwd)
	timestamp := record.Timestamp
	if timestamp.IsZero() {
		timestamp = source.StartedAt
	}
	if timestamp.IsZero() {
		timestamp = fallback
	}
	millis := timestamp.UnixMilli()
	_, err := tx.Exec(`
		INSERT INTO sessions(
			session_uid,agent,native_id,parent_session_uid,role_in_tree,title,cwd,repository,started_at,ended_at
		) VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(session_uid) DO UPDATE SET
			parent_session_uid=CASE WHEN excluded.parent_session_uid<>'' THEN excluded.parent_session_uid ELSE sessions.parent_session_uid END,
			role_in_tree=CASE WHEN excluded.role_in_tree='subagent' THEN 'subagent' ELSE sessions.role_in_tree END,
			title=CASE WHEN excluded.title<>'' THEN excluded.title ELSE sessions.title END,
			cwd=CASE WHEN sessions.cwd='' THEN excluded.cwd ELSE sessions.cwd END,
			repository=CASE WHEN sessions.repository='' THEN excluded.repository ELSE sessions.repository END,
			started_at=CASE WHEN sessions.started_at=0 OR excluded.started_at<sessions.started_at THEN excluded.started_at ELSE sessions.started_at END,
			ended_at=CASE WHEN excluded.ended_at>sessions.ended_at THEN excluded.ended_at ELSE sessions.ended_at END`,
		uid, source.Agent, nativeID, parentUID, roleInTree, source.Title, cwd, repository, millis, millis)
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRow(`SELECT id FROM sessions WHERE session_uid=?`, uid).Scan(&id)
	return id, err
}

type insertedEvent struct {
	ID           int64
	ContentID    int64
	Added        bool
	ContentAdded bool
}

func insertEvent(tx *sql.Tx, sourceID, sessionID int64, sourceHash string, record Record) (insertedEvent, error) {
	contentHash := eventContentHash(record)
	contentResult, err := tx.Exec(`
		INSERT OR IGNORE INTO event_contents(
			session_id,content_hash,text,text_truncated,text_original_bytes,text_sha256,
			command,command_truncated,command_original_bytes,command_sha256,
			diff,diff_truncated,diff_original_bytes,diff_sha256
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sessionID, contentHash, record.Text, record.TextTruncated, record.TextOriginalBytes, record.TextSHA256,
		record.Command, record.CommandTruncated, record.CommandOriginalBytes, record.CommandSHA256,
		record.Diff, record.DiffTruncated, record.DiffOriginalBytes, record.DiffSHA256)
	if err != nil {
		return insertedEvent{}, err
	}
	contentRows, err := contentResult.RowsAffected()
	if err != nil {
		return insertedEvent{}, err
	}
	var contentID int64
	if err := tx.QueryRow(`SELECT id FROM event_contents WHERE session_id=? AND content_hash=?`, sessionID, contentHash).Scan(&contentID); err != nil {
		return insertedEvent{}, err
	}
	for _, path := range record.Paths {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO event_files(content_id,path) VALUES(?,?)`, contentID, path); err != nil {
			return insertedEvent{}, err
		}
	}
	result, err := tx.Exec(`
		INSERT OR IGNORE INTO events(
			session_id,content_id,source_id,record_ordinal,part_ordinal,byte_offset,source_line,
			native_event_id,parent_native_id,ts,role,kind,tool_name,call_id,exit_code,source_hash
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sessionID, contentID, sourceID, record.RecordOrdinal, record.PartOrdinal, record.ByteOffset, record.SourceLine,
		record.NativeEventID, record.ParentNativeID, record.Timestamp.UnixMilli(), record.Role, record.Kind,
		record.ToolName, record.CallID, nullableInt(record.ExitCode), sourceHash)
	if err != nil {
		return insertedEvent{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return insertedEvent{}, err
	}
	var id int64
	if err := tx.QueryRow(`
		SELECT id FROM events WHERE source_id=? AND record_ordinal=? AND part_ordinal=?`,
		sourceID, record.RecordOrdinal, record.PartOrdinal).Scan(&id); err != nil {
		return insertedEvent{}, err
	}
	return insertedEvent{ID: id, ContentID: contentID, Added: rows > 0, ContentAdded: contentRows > 0}, nil
}

func classifyLinkedToolResult(tx *sql.Tx, sessionID int64, record *Record) error {
	if record.Role != RoleTool || record.CallID == "" || record.Kind != KindError {
		return nil
	}
	var parentKind EventKind
	err := tx.QueryRow(`
		SELECT kind FROM events
		WHERE session_id=? AND call_id=?
		ORDER BY ts DESC,id DESC LIMIT 1`, sessionID, record.CallID).Scan(&parentKind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if parentKind == KindTest {
		record.Kind = KindTestFailure
	}
	return nil
}

func deleteSourceEvents(tx *sql.Tx, sourceID int64) (map[int64]struct{}, error) {
	rows, err := tx.Query(`SELECT DISTINCT session_id FROM events WHERE source_id=?`, sourceID)
	if err != nil {
		return nil, err
	}
	sessions := map[int64]struct{}{}
	for rows.Next() {
		var sessionID int64
		if err := rows.Scan(&sessionID); err != nil {
			rows.Close()
			return nil, err
		}
		sessions[sessionID] = struct{}{}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM events WHERE source_id=?`, sourceID); err != nil {
		return nil, err
	}
	return sessions, nil
}

func rebuildSessionDerived(tx *sql.Tx, sessionID int64) error {
	rows, err := tx.Query(`SELECT id FROM event_contents WHERE session_id=?`, sessionID)
	if err != nil {
		return err
	}
	var contentIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		contentIDs = append(contentIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range contentIDs {
		if _, err := tx.Exec(`DELETE FROM events_fts WHERE rowid=?`, id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM event_contents WHERE session_id=? AND NOT EXISTS (SELECT 1 FROM events e WHERE e.content_id=event_contents.id)`, sessionID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE events SET duplicate_of=NULL WHERE session_id=?`, sessionID); err != nil {
		return err
	}

	eventRows, err := tx.Query(`
		SELECT e.id,e.content_id,e.native_event_id,e.parent_native_id,e.ts,e.call_id
		FROM events e
		WHERE e.session_id=?
		ORDER BY e.ts,e.source_id,e.record_ordinal,e.part_ordinal,e.id`, sessionID)
	if err != nil {
		return err
	}
	type derivedEvent struct {
		id, contentID, timestamp   int64
		canonical                  int64
		nativeID, parentID, callID string
	}
	var events []derivedEvent
	canonical := map[string]int64{}
	for eventRows.Next() {
		var event derivedEvent
		if err := eventRows.Scan(&event.id, &event.contentID, &event.nativeID, &event.parentID, &event.timestamp, &event.callID); err != nil {
			eventRows.Close()
			return err
		}
		key := duplicateOccurrenceKey(event.contentID, event.nativeID, event.parentID, event.callID, event.timestamp)
		if first, ok := canonical[key]; ok {
			event.canonical = first
		} else {
			canonical[key] = event.id
		}
		events = append(events, event)
	}
	eventRows.Close()
	if err := eventRows.Err(); err != nil {
		return err
	}
	for _, event := range events {
		if event.canonical != 0 {
			if _, err := tx.Exec(`UPDATE events SET duplicate_of=? WHERE id=?`, event.canonical, event.id); err != nil {
				return err
			}
		}
	}

	contentRows, err := tx.Query(`
		SELECT c.id FROM event_contents c
		WHERE c.session_id=? AND EXISTS(SELECT 1 FROM events e WHERE e.content_id=c.id)
		ORDER BY c.id`, sessionID)
	if err != nil {
		return err
	}
	var indexedContentIDs []int64
	for contentRows.Next() {
		var id int64
		if err := contentRows.Scan(&id); err != nil {
			contentRows.Close()
			return err
		}
		indexedContentIDs = append(indexedContentIDs, id)
	}
	contentRows.Close()
	if err := contentRows.Err(); err != nil {
		return err
	}
	for _, contentID := range indexedContentIDs {
		if err := indexContent(tx, contentID); err != nil {
			return err
		}
	}

	return updateSessionSummary(tx, sessionID)
}

func deriveInsertedEvent(tx *sql.Tx, event insertedEvent) error {
	if !event.Added {
		return nil
	}
	if event.ContentAdded {
		if err := indexContent(tx, event.ContentID); err != nil {
			return err
		}
	}

	var nativeID, parentID, callID string
	var timestamp int64
	if err := tx.QueryRow(`SELECT native_event_id,parent_native_id,call_id,ts FROM events WHERE id=?`, event.ID).
		Scan(&nativeID, &parentID, &callID, &timestamp); err != nil {
		return err
	}
	query := `SELECT id FROM events WHERE content_id=? AND call_id=? ORDER BY ts,source_id,record_ordinal,part_ordinal,id`
	args := []any{event.ContentID, callID}
	if callID == "" && nativeID != "" {
		query = `SELECT id FROM events WHERE content_id=? AND call_id='' AND native_event_id=? ORDER BY ts,source_id,record_ordinal,part_ordinal,id`
		args = []any{event.ContentID, nativeID}
	} else if callID == "" {
		query = `SELECT id FROM events WHERE content_id=? AND call_id='' AND native_event_id='' AND ts=? AND parent_native_id=? ORDER BY ts,source_id,record_ordinal,part_ordinal,id`
		args = []any{event.ContentID, timestamp, parentID}
	}
	rows, err := tx.Query(query, args...)
	if err != nil {
		return err
	}
	var occurrenceIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		occurrenceIDs = append(occurrenceIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(occurrenceIDs) == 0 {
		return fmt.Errorf("inserted event %d was not found while deriving duplicates", event.ID)
	}
	canonicalID := occurrenceIDs[0]
	for i, id := range occurrenceIDs {
		var duplicateOf any
		if i > 0 {
			duplicateOf = canonicalID
		}
		if _, err := tx.Exec(`UPDATE events SET duplicate_of=? WHERE id=?`, duplicateOf, id); err != nil {
			return err
		}
	}
	return nil
}

func indexContent(tx *sql.Tx, contentID int64) error {
	var text, command, diff, files string
	if err := tx.QueryRow(`
		SELECT c.text,c.command,c.diff,
		       COALESCE((SELECT group_concat(path,' ') FROM event_files f WHERE f.content_id=c.id),'')
		FROM event_contents c WHERE c.id=?`, contentID).Scan(&text, &command, &diff, &files); err != nil {
		return err
	}
	bodyTerms := tokenize.Text(strings.Join([]string{text, diff}, "\n"))
	commandTerms := tokenize.Text(command)
	fileTerms := tokenize.PathTerms(files)
	if bodyTerms == "" && commandTerms == "" && fileTerms == "" {
		return nil
	}
	_, err := tx.Exec(`INSERT INTO events_fts(rowid,body_terms,command_terms,file_terms) VALUES(?,?,?,?)`, contentID, bodyTerms, commandTerms, fileTerms)
	return err
}

func updateSessionSummary(tx *sql.Tx, sessionID int64) error {
	var count int
	var started, ended int64
	if err := tx.QueryRow(`
		SELECT COUNT(*),COALESCE(MIN(ts),0),COALESCE(MAX(ts),0) FROM events WHERE session_id=?`, sessionID).
		Scan(&count, &started, &ended); err != nil {
		return err
	}
	status := "unknown"
	var lastKind, lastText string
	err := tx.QueryRow(`SELECT e.kind,c.text FROM events e JOIN event_contents c ON c.id=e.content_id WHERE e.session_id=? ORDER BY e.ts DESC,e.source_id DESC,e.record_ordinal DESC,e.part_ordinal DESC LIMIT 1`, sessionID).
		Scan(&lastKind, &lastText)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if lastKind == string(KindError) || lastKind == string(KindTestFailure) {
		status = "failed"
	} else if lastKind == string(KindStatus) && (strings.EqualFold(lastText, "complete") || strings.EqualFold(lastText, "completed")) {
		status = "complete"
	} else if count > 0 {
		status = "active"
	}
	_, err = tx.Exec(`UPDATE sessions SET event_count=?,started_at=?,ended_at=?,status=? WHERE id=?`, count, started, ended, status, sessionID)
	return err
}

func duplicateOccurrenceKey(contentID int64, nativeID, parentID, callID string, timestamp int64) string {
	switch {
	case callID != "":
		return fmt.Sprintf("%d\x00call\x00%s", contentID, callID)
	case nativeID != "":
		return fmt.Sprintf("%d\x00native\x00%s", contentID, nativeID)
	default:
		return fmt.Sprintf("%d\x00time\x00%d\x00%s", contentID, timestamp, parentID)
	}
}

func deleteOrphanSessions(tx *sql.Tx) error {
	_, err := tx.Exec(`DELETE FROM sessions WHERE NOT EXISTS (SELECT 1 FROM events WHERE events.session_id=sessions.id)`)
	return err
}

func eventContentHash(record Record) string {
	paths := append([]string(nil), record.Paths...)
	sort.Strings(paths)
	hash := sha256.New()
	for _, value := range []string{
		string(record.Role), string(record.Kind), record.Text, record.TextSHA256,
		record.Command, record.CommandSHA256, record.Diff, record.DiffSHA256, strings.Join(paths, "\n"),
	} {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func sessionUID(agent Agent, nativeID string) string {
	return string(agent) + ":" + nativeID
}

func resolveRepository(cwd string) string {
	if cwd == "" {
		return ""
	}
	resolved, err := root.Resolve(cwd, false)
	if err == nil {
		return resolved
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(cwd)
}
