package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rike422/shoka/internal/limits"
	"github.com/rike422/shoka/internal/tokenize"
)

// Filter applies exact metadata filters outside FTS ranking.
type Filter struct {
	Agent            Agent
	Session          string
	Repository       string
	ProjectID        string
	WorkspaceID      string
	TaskLineageID    string
	File             string
	Kind             EventKind
	From             time.Time
	To               time.Time
	IncludeSubagents bool
	Limit            int
}

// SessionInfo is one normalized session row.
type SessionInfo struct {
	ID               int64  `json:"id"`
	SessionUID       string `json:"session_id"`
	Agent            Agent  `json:"agent"`
	NativeID         string `json:"native_id"`
	ParentSessionUID string `json:"parent_session_id,omitempty"`
	RoleInTree       string `json:"role_in_tree"`
	Title            string `json:"title,omitempty"`
	Cwd              string `json:"cwd,omitempty"`
	Repository       string `json:"repository,omitempty"`
	ProjectID        string `json:"project_id,omitempty"`
	WorkspaceID      string `json:"workspace_id,omitempty"`
	TaskLineageID    string `json:"task_lineage_id,omitempty"`
	StartedAt        string `json:"started_at,omitempty"`
	EndedAt          string `json:"ended_at,omitempty"`
	Status           string `json:"status"`
	EventCount       int    `json:"event_count"`
	SourceMissing    bool   `json:"source_missing"`
}

// SourceReference points back to the source of an extracted event.
type SourceReference struct {
	Path   string `json:"path"`
	Hash   string `json:"hash"`
	Line   int64  `json:"line"`
	Offset int64  `json:"byte_offset"`
	State  string `json:"state"`
}

// Event is one stored evidence occurrence.
type Event struct {
	ID                   int64           `json:"id"`
	RecordOrdinal        int64           `json:"record_ordinal"`
	PartOrdinal          int64           `json:"part_ordinal"`
	NativeEventID        string          `json:"native_event_id,omitempty"`
	ParentNativeID       string          `json:"parent_native_id,omitempty"`
	Timestamp            string          `json:"timestamp,omitempty"`
	Role                 Role            `json:"role"`
	Kind                 EventKind       `json:"event_type"`
	Text                 string          `json:"text,omitempty"`
	TextTruncated        bool            `json:"text_truncated,omitempty"`
	TextOriginalBytes    int             `json:"text_original_bytes,omitempty"`
	TextSHA256           string          `json:"text_sha256,omitempty"`
	ToolName             string          `json:"tool_name,omitempty"`
	CallID               string          `json:"call_id,omitempty"`
	Command              string          `json:"command,omitempty"`
	CommandTruncated     bool            `json:"command_truncated,omitempty"`
	CommandOriginalBytes int             `json:"command_original_bytes,omitempty"`
	CommandSHA256        string          `json:"command_sha256,omitempty"`
	Diff                 string          `json:"diff,omitempty"`
	DiffTruncated        bool            `json:"diff_truncated,omitempty"`
	DiffOriginalBytes    int             `json:"diff_original_bytes,omitempty"`
	DiffSHA256           string          `json:"diff_sha256,omitempty"`
	ExitCode             *int            `json:"exit_code,omitempty"`
	Files                []string        `json:"files,omitempty"`
	ContentHash          string          `json:"content_hash"`
	DuplicateOf          *int64          `json:"duplicate_of,omitempty"`
	Source               SourceReference `json:"source"`
}

// SessionDetail includes all evidence occurrences for a session.
type SessionDetail struct {
	SessionInfo
	Events []Event `json:"events"`
}

// SearchHit is one BM25-ranked canonical event.
type SearchHit struct {
	SessionUID    string  `json:"session_id"`
	Agent         Agent   `json:"agent"`
	Repository    string  `json:"repository,omitempty"`
	ProjectID     string  `json:"project_id,omitempty"`
	TaskLineageID string  `json:"task_lineage_id,omitempty"`
	Score         float64 `json:"score"`
	RawEventCount int     `json:"-"`
	Event
}

func openExistingStore(env Environment) (*Store, error) {
	path, err := StateDBPath(env)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("session index not found at %s (run: shoka session sync)", path)
		}
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
	db, err := sql.Open("sqlite3", uri+"?mode=ro&_query_only=1&_fk=1&_busy_timeout=2000")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, path: abs}, nil
}

// ListSessions lists session metadata, hiding subagents by default.
func ListSessions(ctx context.Context, env Environment, filter Filter) ([]SessionInfo, error) {
	filter = normalizeFilter(filter)
	store, err := openExistingStore(env)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	query := `
		SELECT s.id,s.session_uid,s.agent,s.native_id,s.parent_session_uid,s.role_in_tree,
		       s.title,s.cwd,s.repository,s.project_id,s.workspace_id,s.task_lineage_id,s.started_at,s.ended_at,s.status,s.event_count,
		       EXISTS(
		         SELECT 1 FROM events e JOIN sources src ON src.id=e.source_id
		         WHERE e.session_id=s.id AND src.state='missing'
		       )
		FROM sessions s WHERE 1=1`
	var args []any
	if !filter.IncludeSubagents {
		query += ` AND s.role_in_tree='root'`
	}
	if filter.Agent != "" {
		query += ` AND s.agent=?`
		args = append(args, filter.Agent)
	}
	if filter.Session != "" {
		query += ` AND (s.session_uid=? OR s.native_id=?)`
		args = append(args, filter.Session, filter.Session)
	}
	if filter.Repository != "" {
		query += ` AND s.repository=?`
		args = append(args, filter.Repository)
	}
	if filter.ProjectID != "" {
		query += ` AND s.project_id=?`
		args = append(args, filter.ProjectID)
	}
	if filter.WorkspaceID != "" {
		query += ` AND s.workspace_id=?`
		args = append(args, filter.WorkspaceID)
	}
	if filter.TaskLineageID != "" {
		query += ` AND s.task_lineage_id=?`
		args = append(args, filter.TaskLineageID)
	}
	if !filter.From.IsZero() {
		query += ` AND s.ended_at>=?`
		args = append(args, filter.From.UTC().UnixMilli())
	}
	if !filter.To.IsZero() {
		query += ` AND s.started_at<=?`
		args = append(args, filter.To.UTC().UnixMilli())
	}
	query += ` ORDER BY s.ended_at DESC,s.session_uid ASC LIMIT ?`
	args = append(args, normalizedLimit(filter.Limit, 100))
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []SessionInfo
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

// ShowSession returns a session by canonical or native ID.
func ShowSession(ctx context.Context, env Environment, id string) (SessionDetail, error) {
	store, err := openExistingStore(env)
	if err != nil {
		return SessionDetail{}, err
	}
	defer func() { _ = store.Close() }()
	session, err := store.resolveSession(ctx, id)
	if err != nil {
		return SessionDetail{}, err
	}
	events, err := store.eventsForSession(ctx, session.ID)
	if err != nil {
		return SessionDetail{}, err
	}
	return SessionDetail{SessionInfo: session, Events: events}, nil
}

// SearchSessions runs BM25 over canonical events and exact metadata filters.
func SearchSessions(ctx context.Context, env Environment, queryText string, filter Filter) ([]SearchHit, error) {
	filter = normalizeFilter(filter)
	match, ok := tokenize.FTS5Match(queryText)
	if !ok {
		return nil, fmt.Errorf("empty query after tokenization")
	}
	store, err := openExistingStore(env)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	query := `
		SELECT s.session_uid,s.agent,s.repository,s.project_id,s.task_lineage_id,
		       (SELECT COUNT(*) FROM events e3 WHERE e3.content_id=c.id),
		       e.id,e.record_ordinal,e.part_ordinal,e.native_event_id,e.parent_native_id,e.ts,
		       e.role,e.kind,c.text,c.text_truncated,c.text_original_bytes,c.text_sha256,
		       e.tool_name,e.call_id,c.command,c.command_truncated,c.command_original_bytes,c.command_sha256,
		       c.diff,c.diff_truncated,c.diff_original_bytes,c.diff_sha256,
		       e.exit_code,c.content_hash,e.duplicate_of,
		       src.path,COALESCE(NULLIF(e.source_hash,''),src.content_hash),e.source_line,e.byte_offset,src.state,
		       COALESCE((SELECT group_concat(path,char(10)) FROM event_files f WHERE f.content_id=c.id),''),
		       bm25(events_fts,1.0,5.0,2.0) AS score
		FROM events_fts
		JOIN event_contents c ON c.id=events_fts.rowid
		JOIN events e ON e.content_id=c.id AND e.id=(
			SELECT e2.id FROM events e2 WHERE e2.content_id=c.id
			ORDER BY (e2.duplicate_of IS NOT NULL),e2.ts,e2.source_id,e2.record_ordinal,e2.part_ordinal,e2.id LIMIT 1
		)
		JOIN sessions s ON s.id=e.session_id
		JOIN sources src ON src.id=e.source_id
		WHERE events_fts MATCH ?`
	args := []any{match}
	if filter.Agent != "" {
		query += ` AND s.agent=?`
		args = append(args, filter.Agent)
	}
	if filter.Session != "" {
		query += ` AND (s.session_uid=? OR s.native_id=?)`
		args = append(args, filter.Session, filter.Session)
	}
	if filter.Repository != "" {
		query += ` AND s.repository=?`
		args = append(args, filter.Repository)
	}
	if filter.ProjectID != "" {
		query += ` AND s.project_id=?`
		args = append(args, filter.ProjectID)
	}
	if filter.WorkspaceID != "" {
		query += ` AND s.workspace_id=?`
		args = append(args, filter.WorkspaceID)
	}
	if filter.TaskLineageID != "" {
		query += ` AND s.task_lineage_id=?`
		args = append(args, filter.TaskLineageID)
	}
	if filter.Kind != "" {
		query += ` AND e.kind=?`
		args = append(args, filter.Kind)
	}
	if filter.File != "" {
		query += ` AND EXISTS(SELECT 1 FROM event_files f WHERE f.content_id=c.id AND f.path=?)`
		args = append(args, filter.File)
	}
	if !filter.From.IsZero() {
		query += ` AND e.ts>=?`
		args = append(args, filter.From.UTC().UnixMilli())
	}
	if !filter.To.IsZero() {
		query += ` AND e.ts<=?`
		args = append(args, filter.To.UTC().UnixMilli())
	}
	query += ` ORDER BY score ASC,e.ts ASC,e.id ASC LIMIT ?`
	args = append(args, normalizedLimit(filter.Limit, limits.DefaultTopK))
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("session search: %w", err)
	}
	defer rows.Close()
	var hits []SearchHit
	for rows.Next() {
		var hit SearchHit
		var timestamp int64
		var exitCode, duplicateOf sql.NullInt64
		var files string
		if err := rows.Scan(
			&hit.SessionUID, &hit.Agent, &hit.Repository, &hit.ProjectID, &hit.TaskLineageID, &hit.RawEventCount,
			&hit.ID, &hit.RecordOrdinal, &hit.PartOrdinal, &hit.NativeEventID, &hit.ParentNativeID, &timestamp,
			&hit.Role, &hit.Kind, &hit.Text, &hit.TextTruncated, &hit.TextOriginalBytes, &hit.TextSHA256,
			&hit.ToolName, &hit.CallID, &hit.Command, &hit.CommandTruncated, &hit.CommandOriginalBytes, &hit.CommandSHA256,
			&hit.Diff, &hit.DiffTruncated, &hit.DiffOriginalBytes, &hit.DiffSHA256,
			&exitCode, &hit.ContentHash, &duplicateOf,
			&hit.Source.Path, &hit.Source.Hash, &hit.Source.Line, &hit.Source.Offset, &hit.Source.State,
			&files, &hit.Score,
		); err != nil {
			return nil, err
		}
		finishEventScan(&hit.Event, timestamp, exitCode, duplicateOf, files)
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

func normalizeFilter(filter Filter) Filter {
	if filter.Repository != "" {
		filter.Repository = resolveRepository(filter.Repository)
	}
	return filter
}

func (s *Store) resolveSession(ctx context.Context, id string) (SessionInfo, error) {
	base := `
		SELECT s.id,s.session_uid,s.agent,s.native_id,s.parent_session_uid,s.role_in_tree,
		       s.title,s.cwd,s.repository,s.project_id,s.workspace_id,s.task_lineage_id,s.started_at,s.ended_at,s.status,s.event_count,
		       EXISTS(
		         SELECT 1 FROM events e JOIN sources src ON src.id=e.source_id
		         WHERE e.session_id=s.id AND src.state='missing'
		       )
		FROM sessions s WHERE `
	row := s.db.QueryRowContext(ctx, base+`s.session_uid=?`, id)
	session, err := scanSession(row)
	if err == nil {
		return session, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SessionInfo{}, err
	}
	rows, err := s.db.QueryContext(ctx, base+`s.native_id=? ORDER BY s.session_uid LIMIT 2`, id)
	if err != nil {
		return SessionInfo{}, err
	}
	defer rows.Close()
	var found []SessionInfo
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return SessionInfo{}, err
		}
		found = append(found, session)
	}
	if len(found) == 0 {
		return SessionInfo{}, fmt.Errorf("session %q not found", id)
	}
	if len(found) > 1 {
		return SessionInfo{}, fmt.Errorf("session id %q is ambiguous; use the agent-prefixed session id", id)
	}
	return found[0], nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanSession(scanner rowScanner) (SessionInfo, error) {
	var session SessionInfo
	var started, ended int64
	if err := scanner.Scan(
		&session.ID, &session.SessionUID, &session.Agent, &session.NativeID,
		&session.ParentSessionUID, &session.RoleInTree, &session.Title, &session.Cwd,
		&session.Repository, &session.ProjectID, &session.WorkspaceID, &session.TaskLineageID,
		&started, &ended, &session.Status, &session.EventCount,
		&session.SourceMissing,
	); err != nil {
		return SessionInfo{}, err
	}
	session.StartedAt = formatMillis(started)
	session.EndedAt = formatMillis(ended)
	return session, nil
}

func (s *Store) eventsForSession(ctx context.Context, sessionID int64) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id,e.record_ordinal,e.part_ordinal,e.native_event_id,e.parent_native_id,e.ts,
		       e.role,e.kind,c.text,c.text_truncated,c.text_original_bytes,c.text_sha256,
		       e.tool_name,e.call_id,c.command,c.command_truncated,c.command_original_bytes,c.command_sha256,
		       c.diff,c.diff_truncated,c.diff_original_bytes,c.diff_sha256,
		       e.exit_code,c.content_hash,e.duplicate_of,
		       src.path,COALESCE(NULLIF(e.source_hash,''),src.content_hash),e.source_line,e.byte_offset,src.state,
		       COALESCE((SELECT group_concat(path,char(10)) FROM event_files f WHERE f.content_id=c.id),'')
		FROM events e JOIN event_contents c ON c.id=e.content_id JOIN sources src ON src.id=e.source_id
		WHERE e.session_id=?
		ORDER BY e.ts,e.source_id,e.record_ordinal,e.part_ordinal,e.id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		var event Event
		var timestamp int64
		var exitCode, duplicateOf sql.NullInt64
		var files string
		if err := rows.Scan(
			&event.ID, &event.RecordOrdinal, &event.PartOrdinal, &event.NativeEventID, &event.ParentNativeID, &timestamp,
			&event.Role, &event.Kind, &event.Text, &event.TextTruncated, &event.TextOriginalBytes, &event.TextSHA256,
			&event.ToolName, &event.CallID, &event.Command, &event.CommandTruncated, &event.CommandOriginalBytes, &event.CommandSHA256,
			&event.Diff, &event.DiffTruncated, &event.DiffOriginalBytes, &event.DiffSHA256, &exitCode,
			&event.ContentHash, &duplicateOf, &event.Source.Path, &event.Source.Hash, &event.Source.Line,
			&event.Source.Offset, &event.Source.State, &files,
		); err != nil {
			return nil, err
		}
		finishEventScan(&event, timestamp, exitCode, duplicateOf, files)
		events = append(events, event)
	}
	return events, rows.Err()
}

func finishEventScan(event *Event, timestamp int64, exitCode, duplicateOf sql.NullInt64, files string) {
	event.Timestamp = formatMillis(timestamp)
	if exitCode.Valid {
		value := int(exitCode.Int64)
		event.ExitCode = &value
	}
	if duplicateOf.Valid {
		value := duplicateOf.Int64
		event.DuplicateOf = &value
	}
	if files != "" {
		event.Files = strings.Split(files, "\n")
	}
}

func normalizedLimit(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	if value > limits.MaxTopK {
		return limits.MaxTopK
	}
	return value
}

func formatMillis(value int64) string {
	if value <= 0 {
		return ""
	}
	return time.UnixMilli(value).UTC().Format(time.RFC3339Nano)
}
