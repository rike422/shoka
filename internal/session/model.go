package session

import (
	"context"
	"fmt"
	"os"
	"time"
)

// Agent identifies a supported coding agent.
type Agent string

const (
	AgentCodex    Agent = "codex"
	AgentClaude   Agent = "claude"
	AgentOpenCode Agent = "opencode"
	AgentCursor   Agent = "cursor"
	AgentPi       Agent = "pi"
)

// AllAgents is the stable CLI and sync order.
var AllAgents = []Agent{AgentCodex, AgentClaude, AgentOpenCode, AgentCursor, AgentPi}

// ParseAgent validates an agent name.
func ParseAgent(value string) (Agent, error) {
	for _, agent := range AllAgents {
		if string(agent) == value {
			return agent, nil
		}
	}
	return "", fmt.Errorf("unknown agent %q", value)
}

// SourceKind describes how an adapter reads a source.
type SourceKind string

const (
	SourceJSONL         SourceKind = "jsonl"
	SourceSQLiteLogical SourceKind = "sqlite_logical"
)

// Environment explicitly identifies the raw session inputs and derived state.
type Environment struct {
	Home      string
	StateDir  string
	LookupEnv func(string) (string, bool)
}

// CurrentEnvironment resolves the current user's home and environment.
func CurrentEnvironment() (Environment, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Environment{}, err
	}
	stateDir, err := resolveConfiguredStateDir(home, os.LookupEnv)
	if err != nil {
		return Environment{}, err
	}
	return Environment{Home: home, StateDir: stateDir, LookupEnv: os.LookupEnv}, nil
}

func (e Environment) lookup(key string) (string, bool) {
	if e.LookupEnv == nil {
		return "", false
	}
	return e.LookupEnv(key)
}

// Source is an adapter-owned source of one logical session stream.
type Source struct {
	Agent                 Agent
	Kind                  SourceKind
	LogicalID             string
	Path                  string
	NativeSessionID       string
	ParentNativeSessionID string
	TaskLineageID         string
	ProjectID             string
	WorkspaceID           string
	Cwd                   string
	Title                 string
	StartedAt             time.Time
	ExternalVersion       string
}

// Checkpoint records the last committed source boundary.
type Checkpoint struct {
	Size            int64
	MtimeNS         int64
	CursorOffset    int64
	CursorOrdinal   int64
	ContentHash     string
	HashState       []byte
	GuardHash       string
	FileID          string
	ExternalVersion string
}

// ReadPlan fixes the source snapshot and rebuild decision before any record is
// emitted, allowing the store to stream records inside one transaction.
type ReadPlan struct {
	Source       Source
	Checkpoint   Checkpoint
	StartOffset  int64
	StartOrdinal int64
	Result       ReadResult
}

// Role is the non-sensitive actor classification retained by Shoka.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
	RoleSystem    Role = "system"
)

// EventKind is the normalized searchable event classification.
type EventKind string

const (
	KindMessage        EventKind = "message"
	KindCommand        EventKind = "command"
	KindCommandOutput  EventKind = "command_output"
	KindTest           EventKind = "test"
	KindTestFailure    EventKind = "test_failure"
	KindFileOperation  EventKind = "file_operation"
	KindError          EventKind = "error"
	KindUserCorrection EventKind = "user_correction"
	KindTool           EventKind = "tool"
	KindStatus         EventKind = "status"
)

// Record is the only data an adapter may emit. Raw payloads are intentionally
// absent so unknown fields and hidden reasoning cannot reach persistence.
type Record struct {
	NativeSessionID      string
	NativeEventID        string
	ParentNativeID       string
	RecordOrdinal        int64
	PartOrdinal          int64
	ByteOffset           int64
	SourceLine           int64
	Timestamp            time.Time
	Role                 Role
	Kind                 EventKind
	Text                 string
	TextTruncated        bool
	TextOriginalBytes    int
	TextSHA256           string
	ToolName             string
	CallID               string
	Command              string
	CommandTruncated     bool
	CommandOriginalBytes int
	CommandSHA256        string
	Diff                 string
	DiffTruncated        bool
	DiffOriginalBytes    int
	DiffSHA256           string
	ExitCode             *int
	Paths                []string
	Cwd                  string
}

// ReadResult describes a source snapshot and the safely consumed boundary.
type ReadResult struct {
	Size             int64
	MtimeNS          int64
	CursorOffset     int64
	CursorOrdinal    int64
	ContentHash      string
	HashState        []byte
	GuardHash        string
	FileID           string
	ExternalVersion  string
	Rebuild          bool
	Unchanged        bool
	Malformed        int
	SkippedNoise     int
	SkippedUnknown   int
	SkippedReasoning int
	ObservedSession  Source
}

// EmitRecord receives one allowlisted record.
type EmitRecord func(Record) error

// Adapter is the single production contract for discovery and source reading.
type Adapter interface {
	Agent() Agent
	Discover(context.Context, Environment) ([]Source, error)
	Prepare(context.Context, Source, Checkpoint) (ReadPlan, error)
	Read(context.Context, ReadPlan, EmitRecord) (ReadResult, error)
}
