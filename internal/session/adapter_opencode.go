package session

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3" // register sqlite3 driver
)

type openCodeAdapter struct{}

func (a *openCodeAdapter) Agent() Agent { return AgentOpenCode }

func (a *openCodeAdapter) Discover(ctx context.Context, env Environment) ([]Source, error) {
	path := openCodeDBPath(env)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	db, err := openSQLiteReadOnly(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := validateOpenCodeSchema(ctx, db); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT s.id, COALESCE(s.parent_id, ''), s.directory, s.title,
		       s.time_created,
		       max(s.time_updated,
		           COALESCE((SELECT max(m.time_updated) FROM message m WHERE m.session_id=s.id), 0),
		           COALESCE((SELECT max(p.time_updated) FROM part p WHERE p.session_id=s.id), 0)) AS source_version
		FROM session s
		ORDER BY s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	var sources []Source
	for rows.Next() {
		var id, parentID, directory, title string
		var created, version int64
		if err := rows.Scan(&id, &parentID, &directory, &title, &created, &version); err != nil {
			return nil, err
		}
		sources = append(sources, Source{
			Agent:                 AgentOpenCode,
			Kind:                  SourceSQLiteLogical,
			LogicalID:             abs + "#session=" + id,
			Path:                  abs,
			NativeSessionID:       id,
			ParentNativeSessionID: parentID,
			Cwd:                   directory,
			Title:                 title,
			StartedAt:             unixMillis(created),
			ExternalVersion:       strconv.FormatInt(version, 10),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sources, nil
}

func (a *openCodeAdapter) Prepare(_ context.Context, source Source, checkpoint Checkpoint) (ReadPlan, error) {
	info, err := os.Stat(source.Path)
	if err != nil {
		return ReadPlan{}, err
	}
	result := ReadResult{
		Size:            info.Size(),
		MtimeNS:         info.ModTime().UnixNano(),
		ExternalVersion: source.ExternalVersion,
		ObservedSession: source,
	}
	if checkpoint.ExternalVersion != "" && checkpoint.ExternalVersion == source.ExternalVersion {
		result.Unchanged = true
		result.ContentHash = checkpoint.ContentHash
		result.CursorOffset = checkpoint.CursorOffset
		result.CursorOrdinal = checkpoint.CursorOrdinal
		return ReadPlan{Source: source, Checkpoint: checkpoint, Result: result}, nil
	}
	return ReadPlan{
		Source:       source,
		Checkpoint:   checkpoint,
		StartOrdinal: checkpoint.CursorOrdinal,
		Result:       result,
	}, nil
}

func (a *openCodeAdapter) Read(ctx context.Context, plan ReadPlan, emit EmitRecord) (ReadResult, error) {
	result := plan.Result
	if result.Unchanged {
		return result, nil
	}
	source := plan.Source
	checkpoint := plan.Checkpoint
	db, err := openSQLiteReadOnly(source.Path)
	if err != nil {
		return ReadResult{}, err
	}
	defer db.Close()
	if err := validateOpenCodeSchema(ctx, db); err != nil {
		return ReadResult{}, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ReadResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `
		SELECT p.id, p.message_id, p.time_created, p.time_updated, m.data, p.data
		FROM part p
		JOIN message m ON m.id=p.message_id AND m.session_id=p.session_id
		WHERE p.session_id=?
		ORDER BY p.time_created, p.id`, source.NativeSessionID)
	if err != nil {
		return ReadResult{}, err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(source.NativeSessionID))
	ordinal := int64(0)
	for rows.Next() {
		ordinal++
		var partID, messageID string
		var created, updated int64
		var messageData, partData []byte
		if err := rows.Scan(&partID, &messageID, &created, &updated, &messageData, &partData); err != nil {
			rows.Close()
			return ReadResult{}, err
		}
		_, _ = hash.Write([]byte(partID))
		_, _ = hash.Write([]byte(messageID))
		_, _ = hash.Write(messageData)
		_, _ = hash.Write(partData)
		decoded, err := decodeOpenCodePart(source, partID, messageID, created, messageData, partData)
		if err != nil {
			result.Malformed++
			continue
		}
		result.SkippedNoise += decoded.SkippedNoise
		result.SkippedUnknown += decoded.SkippedUnknown
		result.SkippedReasoning += decoded.SkippedReasoning
		for index, record := range decoded.Records {
			record.RecordOrdinal = ordinal
			if record.PartOrdinal == 0 {
				record.PartOrdinal = int64(index + 1)
			}
			record.SourceLine = ordinal
			if err := emit(record); err != nil {
				rows.Close()
				return ReadResult{}, err
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ReadResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ReadResult{}, err
	}
	result.CursorOrdinal = ordinal
	result.ContentHash = hex.EncodeToString(hash.Sum(nil))
	result.Rebuild = checkpoint.ContentHash != "" && checkpoint.ContentHash != result.ContentHash
	return result, nil
}

func openCodeDBPath(env Environment) string {
	if value, ok := env.lookup("OPENCODE_DB"); ok && value != "" {
		return value
	}
	dataHome := filepath.Join(env.Home, ".local", "share")
	if value, ok := env.lookup("XDG_DATA_HOME"); ok && value != "" {
		dataHome = value
	}
	return filepath.Join(dataHome, "opencode", "opencode.db")
}

func openSQLiteReadOnly(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
	db, err := sql.Open("sqlite3", uri+"?mode=ro&_query_only=1&_busy_timeout=2000")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func validateOpenCodeSchema(ctx context.Context, db *sql.DB) error {
	required := map[string][]string{
		"session": {"id", "project_id", "parent_id", "directory", "title", "time_created", "time_updated"},
		"message": {"id", "session_id", "time_created", "time_updated", "data"},
		"part":    {"id", "message_id", "session_id", "time_created", "time_updated", "data"},
	}
	for table, columns := range required {
		rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, columnType string
			var defaultValue any
			if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				rows.Close()
				return err
			}
			seen[name] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, column := range columns {
			if !seen[column] {
				return fmt.Errorf("unsupported opencode schema: %s.%s is missing", table, column)
			}
		}
	}
	return nil
}

func decodeOpenCodePart(source Source, partID, messageID string, created int64, messageData, partData []byte) (decodedLine, error) {
	var message struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(messageData, &message); err != nil {
		return decodedLine{}, err
	}
	role, ok := messageRole(message.Role)
	if !ok {
		return decodedLine{SkippedUnknown: 1}, nil
	}
	var part struct {
		Type   string          `json:"type"`
		Text   string          `json:"text"`
		CallID string          `json:"callID"`
		Tool   string          `json:"tool"`
		Files  json.RawMessage `json:"files"`
		State  struct {
			Status string          `json:"status"`
			Input  json.RawMessage `json:"input"`
			Output json.RawMessage `json:"output"`
			Error  string          `json:"error"`
		} `json:"state"`
	}
	if err := json.Unmarshal(partData, &part); err != nil {
		return decodedLine{}, err
	}
	base := Record{
		NativeSessionID: source.NativeSessionID,
		NativeEventID:   partID,
		ParentNativeID:  messageID,
		Timestamp:       unixMillis(created),
		Role:            role,
		Cwd:             source.Cwd,
	}
	result := decodedLine{}
	switch part.Type {
	case "reasoning":
		result.SkippedReasoning++
	case "text":
		base.Kind = KindMessage
		base.Text = part.Text
		result.Records = append(result.Records, base)
	case "tool":
		call := base
		call.PartOrdinal = 1
		call.Role = RoleAssistant
		call.Kind = KindTool
		call.CallID = part.CallID
		call.ToolName = part.Tool
		input := parseToolInput(part.State.Input)
		call.Command, call.Paths, call.Diff = input.Command, input.Paths, input.Diff
		if call.Command == "" && call.Diff == "" {
			call.Text = compactJSON(part.State.Input)
		}
		result.Records = append(result.Records, call)
		output := rawText(part.State.Output)
		if output == "" {
			output = part.State.Error
		}
		if output != "" {
			response := base
			response.PartOrdinal = 2
			response.Role = RoleTool
			response.Kind = KindCommandOutput
			response.CallID = part.CallID
			response.ToolName = part.Tool
			response.Text = output
			if part.State.Error != "" || strings.EqualFold(part.State.Status, "error") || strings.EqualFold(part.State.Status, "failed") {
				code := 1
				response.ExitCode = &code
			}
			result.Records = append(result.Records, response)
		}
	case "patch":
		base.Kind = KindFileOperation
		base.Role = RoleTool
		base.ToolName = "patch"
		base.Paths = openCodeFiles(part.Files)
		result.Records = append(result.Records, base)
	case "step-start", "step-finish", "compaction":
		result.SkippedNoise++
	default:
		result.SkippedUnknown++
	}
	return result, nil
}

func openCodeFiles(raw json.RawMessage) []string {
	var stringsOnly []string
	if json.Unmarshal(raw, &stringsOnly) == nil {
		sort.Strings(stringsOnly)
		return stringsOnly
	}
	var objects []struct {
		File string `json:"file"`
		Path string `json:"path"`
	}
	if json.Unmarshal(raw, &objects) != nil {
		return nil
	}
	var paths []string
	for _, object := range objects {
		if object.Path != "" {
			paths = append(paths, object.Path)
		} else if object.File != "" {
			paths = append(paths, object.File)
		}
	}
	return paths
}

func unixMillis(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(value).UTC()
}
