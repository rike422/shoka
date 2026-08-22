package session

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	maxSourceLineBytes = 8 << 20
	guardSampleBytes   = 16 << 10
)

type decodedLine struct {
	Records          []Record
	SkippedNoise     int
	SkippedUnknown   int
	SkippedReasoning int
}

type lineDecoder func(Source, []byte) (decodedLine, error)

type fileAdapter struct {
	agent   Agent
	roots   func(Environment) []string
	include func(string) bool
	decode  lineDecoder
}

// AdapterFor returns the sole production adapter for an agent.
func AdapterFor(agent Agent) Adapter {
	switch agent {
	case AgentCodex:
		return &fileAdapter{agent: agent, roots: codexRoots, include: allJSONL, decode: decodeCodex}
	case AgentClaude:
		return &fileAdapter{agent: agent, roots: claudeRoots, include: allJSONL, decode: decodeClaude}
	case AgentCursor:
		return &fileAdapter{agent: agent, roots: cursorRoots, include: cursorJSONL, decode: decodeCursor}
	case AgentPi:
		return &fileAdapter{agent: agent, roots: piRoots, include: allJSONL, decode: decodePi}
	case AgentOpenCode:
		return &openCodeAdapter{}
	default:
		return nil
	}
}

func (a *fileAdapter) Agent() Agent { return a.agent }

func (a *fileAdapter) Discover(ctx context.Context, env Environment) ([]Source, error) {
	var sources []Source
	for _, root := range a.roots(env) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, err := os.Stat(root); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() || !a.include(path) {
				return nil
			}
			source, err := inspectJSONLSource(a.agent, path)
			if err != nil {
				// Discovery must not let one malformed source stop the other logs.
				source = fallbackSource(a.agent, path)
			}
			sources = append(sources, source)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].LogicalID < sources[j].LogicalID })
	return sources, nil
}

func (a *fileAdapter) Prepare(_ context.Context, source Source, checkpoint Checkpoint) (ReadPlan, error) {
	info, err := os.Stat(source.Path)
	if err != nil {
		return ReadPlan{}, err
	}
	result := ReadResult{
		Size:            info.Size(),
		MtimeNS:         info.ModTime().UnixNano(),
		FileID:          sourceFileID(info),
		ExternalVersion: source.ExternalVersion,
		ObservedSession: source,
	}
	if checkpoint.Size == result.Size && checkpoint.MtimeNS == result.MtimeNS && checkpoint.ContentHash != "" {
		result.CursorOffset = checkpoint.CursorOffset
		result.CursorOrdinal = checkpoint.CursorOrdinal
		result.ContentHash = checkpoint.ContentHash
		result.HashState = checkpoint.HashState
		result.GuardHash = checkpoint.GuardHash
		result.Unchanged = true
		return ReadPlan{Source: source, Checkpoint: checkpoint, Result: result}, nil
	}

	startOffset := int64(0)
	startOrdinal := int64(0)
	canExtend := checkpoint.Size > 0 && result.Size > checkpoint.Size && checkpoint.ContentHash != "" &&
		len(checkpoint.HashState) > 0 && checkpoint.GuardHash != "" && checkpoint.FileID == result.FileID
	if canExtend {
		guardHash, err := snapshotGuard(source.Path, checkpoint.Size)
		if err != nil {
			return ReadPlan{}, err
		}
		if guardHash == checkpoint.GuardHash {
			result.ContentHash, result.HashState, err = hashSnapshot(source.Path, checkpoint.HashState, checkpoint.Size, result.Size)
			if err != nil {
				return ReadPlan{}, err
			}
			startOffset = checkpoint.CursorOffset
			startOrdinal = checkpoint.CursorOrdinal
		} else {
			result.Rebuild = true
		}
	} else if checkpoint.Size > 0 {
		result.Rebuild = true
	}
	if result.ContentHash == "" {
		result.ContentHash, result.HashState, err = hashSnapshot(source.Path, nil, 0, result.Size)
		if err != nil {
			return ReadPlan{}, err
		}
		if checkpoint.Size == result.Size && checkpoint.ContentHash == result.ContentHash {
			result.Rebuild = false
			startOffset = checkpoint.CursorOffset
			startOrdinal = checkpoint.CursorOrdinal
		}
	}
	result.GuardHash, err = snapshotGuard(source.Path, result.Size)
	if err != nil {
		return ReadPlan{}, err
	}
	return ReadPlan{
		Source:       source,
		Checkpoint:   checkpoint,
		StartOffset:  startOffset,
		StartOrdinal: startOrdinal,
		Result:       result,
	}, nil
}

func (a *fileAdapter) Read(ctx context.Context, plan ReadPlan, emit EmitRecord) (ReadResult, error) {
	result := plan.Result
	if result.Unchanged {
		return result, nil
	}
	source := plan.Source
	startOffset := plan.StartOffset
	startOrdinal := plan.StartOrdinal

	file, err := os.Open(source.Path)
	if err != nil {
		return ReadResult{}, err
	}
	defer file.Close()
	if _, err := file.Seek(startOffset, io.SeekStart); err != nil {
		return ReadResult{}, err
	}

	// Prepare captured the source size and content hash. Limit the stream to
	// that boundary so bytes appended during this sync belong to the next pass.
	remaining := result.Size - startOffset
	if remaining < 0 {
		return ReadResult{}, fmt.Errorf("source shrank after prepare: %s", source.Path)
	}
	reader := bufio.NewReaderSize(io.LimitReader(file, remaining), 64<<10)
	offset := startOffset
	ordinal := startOrdinal
	result.CursorOffset = startOffset
	result.CursorOrdinal = startOrdinal
	for {
		if err := ctx.Err(); err != nil {
			return ReadResult{}, err
		}
		lineStart := offset
		line, readErr := reader.ReadString('\n')
		offset += int64(len(line))
		complete := strings.HasSuffix(line, "\n")
		if !complete {
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return ReadResult{}, readErr
			}
			break
		}
		ordinal++
		result.CursorOffset = offset
		result.CursorOrdinal = ordinal
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		if len(line) == 0 {
			result.SkippedNoise++
			if readErr != nil {
				break
			}
			continue
		}
		if len(line) > maxSourceLineBytes {
			result.SkippedNoise++
			if readErr != nil {
				break
			}
			continue
		}
		decoded, err := a.decode(source, []byte(line))
		if err != nil {
			result.Malformed++
			if readErr != nil {
				break
			}
			continue
		}
		result.SkippedNoise += decoded.SkippedNoise
		result.SkippedUnknown += decoded.SkippedUnknown
		result.SkippedReasoning += decoded.SkippedReasoning
		for index, record := range decoded.Records {
			if record.NativeSessionID == "" {
				record.NativeSessionID = source.NativeSessionID
			}
			record.RecordOrdinal = ordinal
			if record.PartOrdinal == 0 {
				record.PartOrdinal = int64(index + 1)
			}
			record.ByteOffset = lineStart
			record.SourceLine = ordinal
			if record.Cwd == "" {
				record.Cwd = source.Cwd
			}
			if err := emit(record); err != nil {
				return ReadResult{}, err
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return ReadResult{}, readErr
		}
	}
	return result, nil
}

func allJSONL(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".jsonl")
}

func cursorJSONL(path string) bool {
	return allJSONL(path) && strings.Contains(filepath.ToSlash(path), "/agent-transcripts/")
}

func codexRoots(env Environment) []string {
	base := filepath.Join(env.Home, ".codex")
	if value, ok := env.lookup("CODEX_HOME"); ok && value != "" {
		base = value
	}
	return []string{filepath.Join(base, "sessions"), filepath.Join(base, "archived_sessions")}
}

func claudeRoots(env Environment) []string {
	base := filepath.Join(env.Home, ".claude")
	if value, ok := env.lookup("CLAUDE_CONFIG_DIR"); ok && value != "" {
		base = value
	}
	return []string{filepath.Join(base, "projects")}
}

func cursorRoots(env Environment) []string {
	return []string{filepath.Join(env.Home, ".cursor", "projects")}
}

func piRoots(env Environment) []string {
	if value, ok := env.lookup("PI_CODING_AGENT_SESSION_DIR"); ok && value != "" {
		return []string{value}
	}
	return []string{filepath.Join(env.Home, ".pi", "agent", "sessions")}
}

func fallbackSource(agent Agent, path string) Source {
	abs, _ := filepath.Abs(path)
	nativeID := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return Source{
		Agent:                 agent,
		Kind:                  SourceJSONL,
		LogicalID:             abs,
		Path:                  abs,
		NativeSessionID:       nativeID,
		ParentNativeSessionID: parentSessionFromPath(path),
	}
}

func inspectJSONLSource(agent Agent, path string) (Source, error) {
	source := fallbackSource(agent, path)
	file, err := os.Open(path)
	if err != nil {
		return source, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for line := 0; line < 64 && scanner.Scan(); line++ {
		var envelope struct {
			Type      string          `json:"type"`
			Timestamp string          `json:"timestamp"`
			SessionID string          `json:"sessionId"`
			Cwd       string          `json:"cwd"`
			Payload   json.RawMessage `json:"payload"`
			ID        string          `json:"id"`
			Title     string          `json:"title"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			continue
		}
		if envelope.SessionID != "" {
			source.NativeSessionID = envelope.SessionID
		}
		if envelope.ID != "" && envelope.Type == "session" {
			source.NativeSessionID = envelope.ID
		}
		if envelope.Cwd != "" {
			source.Cwd = envelope.Cwd
		}
		if envelope.Title != "" {
			source.Title = envelope.Title
		}
		if parsed := parseTimestamp(envelope.Timestamp); !parsed.IsZero() {
			source.StartedAt = parsed
		}
		if envelope.Type == "session_meta" {
			var payload struct {
				ID        string `json:"id"`
				SessionID string `json:"session_id"`
				Cwd       string `json:"cwd"`
				Timestamp string `json:"timestamp"`
			}
			if json.Unmarshal(envelope.Payload, &payload) == nil {
				if payload.SessionID != "" {
					source.NativeSessionID = payload.SessionID
				} else if payload.ID != "" {
					source.NativeSessionID = payload.ID
				}
				if payload.Cwd != "" {
					source.Cwd = payload.Cwd
				}
				if parsed := parseTimestamp(payload.Timestamp); !parsed.IsZero() {
					source.StartedAt = parsed
				}
			}
		}
		if source.Cwd != "" && source.NativeSessionID != "" {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return source, err
	}
	return source, nil
}

func parentSessionFromPath(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for index, part := range parts {
		if part == "subagents" && index > 0 {
			return parts[index-1]
		}
	}
	return ""
}

func hashSnapshot(path string, priorState []byte, start, size int64) (string, []byte, error) {
	if start < 0 || size < start {
		return "", nil, fmt.Errorf("invalid hash snapshot range: start=%d size=%d", start, size)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	hash := sha256.New()
	if len(priorState) > 0 {
		unmarshaler, ok := hash.(encoding.BinaryUnmarshaler)
		if !ok {
			return "", nil, fmt.Errorf("sha256 does not support restoring hash state")
		}
		if err := unmarshaler.UnmarshalBinary(priorState); err != nil {
			return "", nil, fmt.Errorf("restore source hash state: %w", err)
		}
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return "", nil, err
	}
	if _, err := io.CopyN(hash, file, size-start); err != nil {
		return "", nil, err
	}
	marshaler, ok := hash.(encoding.BinaryMarshaler)
	if !ok {
		return "", nil, fmt.Errorf("sha256 does not support saving hash state")
	}
	state, err := marshaler.MarshalBinary()
	if err != nil {
		return "", nil, fmt.Errorf("save source hash state: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), state, nil
}

func snapshotGuard(path string, size int64) (string, error) {
	if size < 0 {
		return "", fmt.Errorf("invalid guard snapshot size: %d", size)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if err := binary.Write(hash, binary.LittleEndian, uint64(size)); err != nil {
		return "", err
	}
	window := int64(guardSampleBytes)
	if size < window {
		window = size
	}
	offsets := []int64{0}
	if size > window {
		offsets = append(offsets, size/4, size/2, size*3/4, size-window)
	}
	seen := map[int64]struct{}{}
	for _, offset := range offsets {
		if offset+window > size {
			offset = size - window
		}
		if _, ok := seen[offset]; ok {
			continue
		}
		seen[offset] = struct{}{}
		if err := binary.Write(hash, binary.LittleEndian, uint64(offset)); err != nil {
			return "", err
		}
		if _, err := io.CopyN(hash, io.NewSectionReader(file, offset, window), window); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func sourceFileID(info os.FileInfo) string {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d:%d", uint64(stat.Dev), stat.Ino)
}

func parseTimestamp(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func rawText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var values []string
		for _, block := range blocks {
			if block.Type == "text" || block.Type == "input_text" || block.Type == "output_text" {
				values = append(values, block.Text)
			}
		}
		return strings.Join(values, "\n")
	}
	return ""
}
