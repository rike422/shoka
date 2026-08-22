package session

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	sessionEvidenceVersion  = "session-evidence/v1"
	maxSessionEvidenceBytes = 1 << 20
	maxUserEvidenceBytes    = 160 << 10
	maxCommandEvidenceBytes = 320 << 10
	maxFileEvidenceBytes    = 64 << 10
	maxErrorEvidenceBytes   = 192 << 10
)

// EvidenceText is bounded observable text. SHA256 identifies the complete
// redacted value even when only a head/tail snippet is retained.
type EvidenceText struct {
	Text          string `json:"text"`
	Truncated     bool   `json:"truncated"`
	OriginalBytes int    `json:"original_bytes"`
	SHA256        string `json:"sha256"`
}

// ObservedText ties bounded text to its source occurrence.
type ObservedText struct {
	EvidenceText
	Kind      string          `json:"kind,omitempty"`
	Timestamp string          `json:"timestamp,omitempty"`
	Source    SourceReference `json:"source"`
}

// CommandEvidence records an observed command and its linked outcome.
type CommandEvidence struct {
	Command      EvidenceText     `json:"command"`
	Kind         EventKind        `json:"kind"`
	Outcome      string           `json:"outcome"`
	ExitCode     *int             `json:"exit_code,omitempty"`
	Output       *EvidenceText    `json:"output,omitempty"`
	OutputSource *SourceReference `json:"output_source,omitempty"`
	Timestamp    string           `json:"timestamp,omitempty"`
	Source       SourceReference  `json:"source"`
}

// FileEvidence records a changed path without inferring change semantics that
// were not present in the native log.
type FileEvidence struct {
	Path      string          `json:"path"`
	Timestamp string          `json:"timestamp,omitempty"`
	Source    SourceReference `json:"source"`
}

// ErrorEvidence records only explicit failures and non-zero outcomes.
type ErrorEvidence struct {
	Kind      EventKind       `json:"kind"`
	Text      EvidenceText    `json:"text"`
	Timestamp string          `json:"timestamp,omitempty"`
	Source    SourceReference `json:"source"`
}

// DiffEvidence contains only patch text observed in the native session log.
type DiffEvidence struct {
	EvidenceText
	Sources []SourceReference `json:"sources"`
}

// EvidenceOmissions makes deterministic size reduction explicit.
type EvidenceOmissions struct {
	UserInputs      int `json:"user_inputs"`
	Commands        int `json:"commands"`
	FilesChanged    int `json:"files_changed"`
	Errors          int `json:"errors"`
	DiffSources     int `json:"diff_sources"`
	DuplicateEvents int `json:"duplicate_events"`
}

// EvidenceSession is the stable, non-semantic session identity.
type EvidenceSession struct {
	ID              string `json:"id"`
	NativeID        string `json:"native_id"`
	Agent           Agent  `json:"agent"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	RoleInTree      string `json:"role_in_tree"`
	Repository      string `json:"repository,omitempty"`
	StartedAt       string `json:"started_at,omitempty"`
	EndedAt         string `json:"ended_at,omitempty"`
	Status          string `json:"status"`
	EventCount      int    `json:"event_count"`
}

// SessionEvidence is Shoka's canonical observable-evidence contract. It does
// not rank, classify, or infer reusable lessons.
type SessionEvidence struct {
	SchemaVersion string            `json:"schema_version"`
	Session       EvidenceSession   `json:"session"`
	Task          *ObservedText     `json:"task,omitempty"`
	UserInputs    []ObservedText    `json:"user_inputs"`
	Commands      []CommandEvidence `json:"commands"`
	FilesChanged  []FileEvidence    `json:"files_changed"`
	Errors        []ErrorEvidence   `json:"errors"`
	Diff          DiffEvidence      `json:"diff"`
	FinalResponse *ObservedText     `json:"final_response,omitempty"`
	Omitted       EvidenceOmissions `json:"omitted"`
}

// BuildSessionEvidence creates a deterministic, bounded evidence document.
func BuildSessionEvidence(ctx context.Context, env Environment, id string) (SessionEvidence, error) {
	detail, err := ShowSession(ctx, env, id)
	if err != nil {
		return SessionEvidence{}, err
	}
	evidence := SessionEvidence{
		SchemaVersion: sessionEvidenceVersion,
		Session: EvidenceSession{
			ID: detail.SessionUID, NativeID: detail.NativeID, Agent: detail.Agent,
			ParentSessionID: detail.ParentSessionUID, RoleInTree: detail.RoleInTree,
			Repository: detail.Repository, StartedAt: detail.StartedAt, EndedAt: detail.EndedAt,
			Status: detail.Status, EventCount: detail.EventCount,
		},
		UserInputs: []ObservedText{}, Commands: []CommandEvidence{},
		FilesChanged: []FileEvidence{}, Errors: []ErrorEvidence{},
		Diff: DiffEvidence{Sources: []SourceReference{}},
	}

	canonical := make([]Event, 0, len(detail.Events))
	for _, event := range detail.Events {
		if event.DuplicateOf != nil {
			evidence.Omitted.DuplicateEvents++
			continue
		}
		canonical = append(canonical, event)
	}
	outputs := map[string]Event{}
	for _, event := range canonical {
		if event.Role == RoleTool && event.CallID != "" && (event.Kind == KindCommandOutput || event.Kind == KindError || event.Kind == KindTestFailure) {
			outputs[event.CallID] = event
		}
	}

	fileSeen := map[string]bool{}
	var diffParts []string
	var diffSources []SourceReference
	for _, event := range canonical {
		if event.Role == RoleUser && (event.Kind == KindMessage || event.Kind == KindUserCorrection) && event.Text != "" {
			item := observedText(event, event.Text, event.TextTruncated, event.TextOriginalBytes, event.TextSHA256)
			if event.Kind == KindUserCorrection {
				item.Kind = "correction"
			} else {
				item.Kind = "request"
			}
			if evidence.Task == nil {
				task := item
				evidence.Task = &task
			}
			evidence.UserInputs = append(evidence.UserInputs, item)
		}
		if event.Role == RoleAssistant && event.Kind == KindMessage && event.Text != "" {
			item := observedText(event, event.Text, event.TextTruncated, event.TextOriginalBytes, event.TextSHA256)
			evidence.FinalResponse = &item
		}
		if (event.Kind == KindCommand || event.Kind == KindTest) && event.Command != "" {
			command := CommandEvidence{
				Command: evidenceText(event.Command, event.CommandTruncated, event.CommandOriginalBytes, event.CommandSHA256),
				Kind:    event.Kind, Outcome: "unknown", Timestamp: event.Timestamp, Source: event.Source,
			}
			if output, ok := outputs[event.CallID]; ok {
				command.ExitCode = output.ExitCode
				command.Outcome = eventOutcome(output)
				if output.Text != "" {
					value := evidenceText(output.Text, output.TextTruncated, output.TextOriginalBytes, output.TextSHA256)
					command.Output = &value
					source := output.Source
					command.OutputSource = &source
				}
			}
			evidence.Commands = append(evidence.Commands, command)
		}
		if event.Kind == KindError || event.Kind == KindTestFailure {
			evidence.Errors = append(evidence.Errors, ErrorEvidence{
				Kind: event.Kind, Text: evidenceText(event.Text, event.TextTruncated, event.TextOriginalBytes, event.TextSHA256),
				Timestamp: event.Timestamp, Source: event.Source,
			})
		}
		if event.Kind == KindFileOperation {
			for _, path := range event.Files {
				if fileSeen[path] {
					continue
				}
				fileSeen[path] = true
				evidence.FilesChanged = append(evidence.FilesChanged, FileEvidence{Path: path, Timestamp: event.Timestamp, Source: event.Source})
			}
			if event.Diff != "" {
				diffParts = append(diffParts, event.Diff)
				diffSources = append(diffSources, event.Source)
			}
		}
	}

	evidence.UserInputs, evidence.Omitted.UserInputs = selectBudgeted(evidence.UserInputs, maxUserEvidenceBytes)
	evidence.Commands, evidence.Omitted.Commands = selectBudgeted(evidence.Commands, maxCommandEvidenceBytes)
	evidence.FilesChanged, evidence.Omitted.FilesChanged = selectBudgeted(evidence.FilesChanged, maxFileEvidenceBytes)
	evidence.Errors, evidence.Omitted.Errors = selectBudgeted(evidence.Errors, maxErrorEvidenceBytes)

	joinedDiff := strings.Join(diffParts, "\n")
	bounded, truncated, originalBytes, digest := boundText(joinedDiff, maxDiffBytes)
	evidence.Diff.EvidenceText = evidenceText(bounded, truncated, originalBytes, digest)
	if len(diffSources) > 32 {
		evidence.Omitted.DiffSources = len(diffSources) - 32
		diffSources = append(append([]SourceReference{}, diffSources[:16]...), diffSources[len(diffSources)-16:]...)
	}
	evidence.Diff.Sources = uniqueSources(diffSources)

	encoded, err := json.Marshal(evidence)
	if err != nil {
		return SessionEvidence{}, err
	}
	if len(encoded) > maxSessionEvidenceBytes {
		return SessionEvidence{}, fmt.Errorf("session evidence exceeds %d bytes after deterministic limits", maxSessionEvidenceBytes)
	}
	return evidence, nil
}

func eventOutcome(event Event) string {
	if event.Kind == KindError || event.Kind == KindTestFailure || (event.ExitCode != nil && *event.ExitCode != 0) {
		return "failure"
	}
	if event.ExitCode != nil && *event.ExitCode == 0 {
		return "success"
	}
	return "unknown"
}

func observedText(event Event, value string, truncated bool, originalBytes int, digest string) ObservedText {
	return ObservedText{EvidenceText: evidenceText(value, truncated, originalBytes, digest), Timestamp: event.Timestamp, Source: event.Source}
}

func evidenceText(value string, truncated bool, originalBytes int, digest string) EvidenceText {
	if originalBytes == 0 {
		originalBytes = len(value)
	}
	if digest == "" {
		digest = fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
	}
	return EvidenceText{Text: value, Truncated: truncated, OriginalBytes: originalBytes, SHA256: digest}
}

func selectBudgeted[T any](items []T, budget int) ([]T, int) {
	if len(items) == 0 {
		return []T{}, 0
	}
	sizes := make([]int, len(items))
	total := 0
	for index, item := range items {
		encoded, _ := json.Marshal(item)
		sizes[index] = len(encoded) + 1
		total += sizes[index]
	}
	if total <= budget {
		return items, 0
	}
	frontBudget := budget / 2
	used := 0
	frontEnd := 0
	for frontEnd < len(items) && (used+sizes[frontEnd] <= frontBudget || frontEnd == 0) {
		used += sizes[frontEnd]
		frontEnd++
	}
	backStart := len(items)
	for backStart > frontEnd && used+sizes[backStart-1] <= budget {
		backStart--
		used += sizes[backStart]
	}
	selected := append([]T{}, items[:frontEnd]...)
	selected = append(selected, items[backStart:]...)
	return selected, len(items) - len(selected)
}

func uniqueSources(sources []SourceReference) []SourceReference {
	seen := map[string]SourceReference{}
	for _, source := range sources {
		key := fmt.Sprintf("%s\x00%s\x00%d\x00%d", source.Path, source.Hash, source.Line, source.Offset)
		seen[key] = source
	}
	values := make([]SourceReference, 0, len(seen))
	for _, source := range seen {
		values = append(values, source)
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Path != values[j].Path {
			return values[i].Path < values[j].Path
		}
		if values[i].Line != values[j].Line {
			return values[i].Line < values[j].Line
		}
		return values[i].Offset < values[j].Offset
	})
	return values
}

// WriteSessionEvidence atomically writes a private evidence file.
func WriteSessionEvidence(ctx context.Context, env Environment, id, output string) error {
	if output == "" {
		return fmt.Errorf("evidence output path is required")
	}
	evidence, err := BuildSessionEvidence(ctx, env, id)
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".session-evidence-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(evidence); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, abs); err != nil {
		return err
	}
	if err := os.Chmod(abs, 0o600); err != nil {
		return err
	}
	ok = true
	return nil
}
