package session

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const transcriptEpisodeVersion = "transcript-episode/v1"

// AnchorType is a deterministic observable reason for starting an episode.
// It is deliberately not a root-cause or Skill classification.
type AnchorType string

const (
	AnchorUserCorrection         AnchorType = "user_correction"
	AnchorError                  AnchorType = "error"
	AnchorTestFailure            AnchorType = "test_failure"
	AnchorRequirementRestatement AnchorType = "requirement_restatement"
	AnchorScopeRevision          AnchorType = "scope_revision"
	AnchorDesignDirection        AnchorType = "design_direction"
)

// EpisodeOptions controls deterministic context and evidence bounds.
type EpisodeOptions struct {
	Before      int
	After       int
	ByteBudget  int
	TokenBudget int
}

// TranscriptTrigger identifies the observable anchor event.
type TranscriptTrigger struct {
	Type      string `json:"type"`
	EventID   int64  `json:"event_id"`
	Timestamp string `json:"timestamp,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// TranscriptEvidence is an allowlisted, source-linked event projection.
type TranscriptEvidence struct {
	EventID     int64           `json:"event_id"`
	EventType   EventKind       `json:"event_type"`
	Role        Role            `json:"role"`
	Timestamp   string          `json:"timestamp,omitempty"`
	Text        string          `json:"text,omitempty"`
	Command     string          `json:"command,omitempty"`
	Diff        string          `json:"diff,omitempty"`
	Files       []string        `json:"files,omitempty"`
	ExitCode    *int            `json:"exit_code,omitempty"`
	Outcome     string          `json:"outcome,omitempty"`
	ContentHash string          `json:"content_hash,omitempty"`
	Source      SourceReference `json:"source"`
}

// TranscriptOutcome records an observed result without inferring success from
// the mere presence of a later action or assistant message.
type TranscriptOutcome struct {
	EventID int64           `json:"event_id"`
	Type    string          `json:"type"`
	Outcome string          `json:"outcome"`
	Text    string          `json:"text,omitempty"`
	Source  SourceReference `json:"source"`
}

type TranscriptOmissions struct {
	Events   int `json:"events"`
	Bytes    int `json:"bytes"`
	Tokens   int `json:"tokens"`
	Context  int `json:"context_before"`
	Actions  int `json:"actions_after"`
	Outcomes int `json:"outcomes"`
}

// TranscriptEpisode is Shoka's deterministic correction/result evidence unit.
type TranscriptEpisode struct {
	SchemaVersion         string               `json:"schema_version"`
	EpisodeID             string               `json:"episode_id"`
	TranscriptID          string               `json:"transcript_id"`
	NativeSessionID       string               `json:"native_session_id"`
	TaskLineageID         string               `json:"task_lineage_id"`
	ProjectID             string               `json:"project_id"`
	WorkspaceID           string               `json:"workspace_id,omitempty"`
	Trigger               TranscriptTrigger    `json:"trigger"`
	ContextBefore         []TranscriptEvidence `json:"context_before"`
	Corrections           []TranscriptEvidence `json:"corrections"`
	ActionsAfter          []TranscriptEvidence `json:"actions_after"`
	Outcomes              []TranscriptOutcome  `json:"outcomes"`
	FinalReport           *TranscriptEvidence  `json:"final_report,omitempty"`
	AdditionalCorrections []TranscriptEvidence `json:"additional_corrections,omitempty"`
	TaskStatus            string               `json:"task_status"`
	SourceEventIDs        []int64              `json:"source_event_ids"`
	SearchReasons         []string             `json:"search_reasons,omitempty"`
	Omitted               TranscriptOmissions  `json:"omitted"`
}

type TranscriptBuildInput struct {
	Session     SessionInfo
	Events      []Event
	AnchorIndex int
	Options     EpisodeOptions
}

var (
	scopeAnchorWords  = regexp.MustCompile(`(?i)(?:\bscope\b|\bscoped\b|\bonly\b|\bin scope\b|範囲|対象|含めない|除外)`)
	designAnchorWords = regexp.MustCompile(`(?i)(?:\bdesign\b|\barchitecture\b|\bpolicy\b|\buse [a-z0-9_./-]+\b|方針|設計|規約|契約|〜にする|にする)`)
)

// EpisodeAnchorTypes returns one anchor classification per event. Empty
// values are ordinary events and never become episodes.
func EpisodeAnchorTypes(events []Event) []AnchorType {
	types := make([]AnchorType, len(events))
	for index := range events {
		types[index] = classifyAnchor(events, index)
	}
	return types
}

func classifyAnchor(events []Event, index int) AnchorType {
	event := events[index]
	switch event.Kind {
	case KindUserCorrection:
		return AnchorUserCorrection
	case KindError:
		return AnchorError
	case KindTestFailure:
		return AnchorTestFailure
	}
	if event.Role != RoleUser || event.Kind != KindMessage || strings.TrimSpace(event.Text) == "" {
		return ""
	}
	if scopeAnchorWords.MatchString(event.Text) {
		return AnchorScopeRevision
	}
	if designAnchorWords.MatchString(event.Text) {
		return AnchorDesignDirection
	}
	if repeatedRequirement(events, index) {
		return AnchorRequirementRestatement
	}
	return ""
}

func repeatedRequirement(events []Event, index int) bool {
	current := normalizedRequirement(events[index].Text)
	if current == "" {
		return false
	}
	for previous := 0; previous < index; previous++ {
		candidate := events[previous]
		if candidate.Role != RoleUser || (candidate.Kind != KindMessage && candidate.Kind != KindUserCorrection) {
			continue
		}
		prior := normalizedRequirement(candidate.Text)
		if prior == current || requirementSimilarity(prior, current) >= 0.8 {
			return true
		}
	}
	return false
}

func normalizedRequirement(value string) string {
	var builder strings.Builder
	for _, char := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

func requirementSimilarity(left, right string) float64 {
	if left == "" || right == "" {
		return 0
	}
	leftRunes := []rune(left)
	rightRunes := []rune(right)
	common := 0
	for _, char := range leftRunes {
		if strings.ContainsRune(right, char) {
			common++
		}
	}
	denominator := len(leftRunes)
	if len(rightRunes) > denominator {
		denominator = len(rightRunes)
	}
	return float64(common) / float64(denominator)
}

// BuildTranscriptEpisodeFromEvents builds one episode from canonical, chronologically ordered
// events. Duplicate occurrences should be removed by the caller first.
func BuildTranscriptEpisodeFromEvents(input TranscriptBuildInput) (TranscriptEpisode, error) {
	if input.AnchorIndex < 0 || input.AnchorIndex >= len(input.Events) {
		return TranscriptEpisode{}, fmt.Errorf("episode anchor index %d is out of range", input.AnchorIndex)
	}
	anchorType := classifyAnchor(input.Events, input.AnchorIndex)
	if anchorType == "" {
		return TranscriptEpisode{}, fmt.Errorf("event %d is not an episode anchor", input.Events[input.AnchorIndex].ID)
	}
	options := input.Options
	if options.Before < 0 || options.After < 0 || options.ByteBudget < 0 || options.TokenBudget < 0 {
		return TranscriptEpisode{}, fmt.Errorf("episode bounds must not be negative")
	}
	if options.After == 0 {
		options.After = len(input.Events) - input.AnchorIndex - 1
	}
	start := input.AnchorIndex - options.Before
	if start < 0 {
		start = 0
	}
	end := input.AnchorIndex + options.After + 1
	if end > len(input.Events) {
		end = len(input.Events)
	}
	selected := input.Events[start:end]
	selected, omitted := boundEpisodeEvents(selected, input.Events[input.AnchorIndex].ID, options)
	for _, event := range selected {
		if err := validateEpisodeSource(event); err != nil {
			return TranscriptEpisode{}, err
		}
	}

	episode := TranscriptEpisode{
		SchemaVersion:   transcriptEpisodeVersion,
		EpisodeID:       episodeID(input.Session.SessionUID, input.Events[input.AnchorIndex], anchorType),
		TranscriptID:    input.Session.SessionUID,
		NativeSessionID: input.Session.NativeID,
		TaskLineageID:   input.Session.TaskLineageID,
		ProjectID:       input.Session.ProjectID,
		WorkspaceID:     input.Session.WorkspaceID,
		Trigger:         TranscriptTrigger{Type: string(anchorType), EventID: input.Events[input.AnchorIndex].ID, Timestamp: input.Events[input.AnchorIndex].Timestamp},
		ContextBefore:   []TranscriptEvidence{},
		Corrections:     []TranscriptEvidence{},
		ActionsAfter:    []TranscriptEvidence{},
		Outcomes:        []TranscriptOutcome{},
		SourceEventIDs:  []int64{},
		Omitted:         omitted,
		TaskStatus:      episodeTaskStatus(input.Session.Status, selected),
	}

	for _, event := range selected {
		if !containsEventID(episode.SourceEventIDs, event.ID) {
			episode.SourceEventIDs = append(episode.SourceEventIDs, event.ID)
		}
		item := episodeEvidence(event)
		eventIndex := indexOfEvent(input.Events, event.ID)
		if eventIndex < input.AnchorIndex {
			episode.ContextBefore = append(episode.ContextBefore, item)
		}
		if event.ID == input.Events[input.AnchorIndex].ID || (eventIndex > input.AnchorIndex && isCorrectionEvent(input.Events, eventIndex)) {
			if event.Role == RoleUser || event.ID == input.Events[input.AnchorIndex].ID {
				episode.Corrections = append(episode.Corrections, item)
			}
		}
		if eventIndex > input.AnchorIndex {
			episode.ActionsAfter = append(episode.ActionsAfter, item)
		}
		if outcome := episodeOutcome(event, input.Events); outcome != nil {
			episode.Outcomes = append(episode.Outcomes, *outcome)
		}
		if event.Role == RoleAssistant && event.Kind == KindMessage && eventIndex > input.AnchorIndex {
			copy := item
			episode.FinalReport = &copy
		}
	}
	for _, item := range episode.Corrections {
		if item.EventID != episode.Trigger.EventID {
			episode.AdditionalCorrections = append(episode.AdditionalCorrections, item)
		}
	}
	if episode.Omitted.Context > 0 || episode.Omitted.Actions > 0 || episode.Omitted.Outcomes > 0 {
		episode.Omitted.Events = episode.Omitted.Context + episode.Omitted.Actions + episode.Omitted.Outcomes
	}
	return episode, nil
}

func isCorrectionEvent(events []Event, index int) bool {
	if index < 0 || index >= len(events) || events[index].Role != RoleUser {
		return false
	}
	switch classifyAnchor(events, index) {
	case AnchorUserCorrection, AnchorRequirementRestatement, AnchorScopeRevision, AnchorDesignDirection:
		return true
	default:
		return false
	}
}

func episodeEvidence(event Event) TranscriptEvidence {
	return TranscriptEvidence{EventID: event.ID, EventType: event.Kind, Role: event.Role, Timestamp: event.Timestamp, Text: event.Text, Command: event.Command, Diff: event.Diff, Files: append([]string{}, event.Files...), ExitCode: event.ExitCode, ContentHash: event.ContentHash, Source: event.Source}
}

func validateEpisodeSource(event Event) error {
	if strings.TrimSpace(event.Source.Path) == "" || strings.TrimSpace(event.Source.Hash) == "" {
		return fmt.Errorf("event %d has incomplete source reference", event.ID)
	}
	return nil
}

// episodeOutcome includes only event kinds that carry an observable command,
// test, error, or task-status result. Ordinary messages and file operations
// remain actions, not inferred outcomes.
func episodeOutcome(event Event, events []Event) *TranscriptOutcome {
	if event.Kind != KindCommand && event.Kind != KindTest && event.Kind != KindCommandOutput && event.Kind != KindTestFailure && event.Kind != KindError && event.Kind != KindStatus {
		return nil
	}
	outcome := "unknown"
	if event.Kind == KindError || event.Kind == KindTestFailure || (event.ExitCode != nil && *event.ExitCode != 0) {
		outcome = "failure"
	} else if event.ExitCode != nil && *event.ExitCode == 0 {
		outcome = "success"
	} else if event.Kind == KindStatus {
		switch strings.ToLower(strings.TrimSpace(event.Text)) {
		case "complete", "completed", "success", "succeeded":
			outcome = "success"
		case "failed", "failure":
			outcome = "failure"
		}
	} else if event.Kind == KindCommand || event.Kind == KindTest {
		for _, candidate := range events {
			if candidate.Role == RoleTool && candidate.CallID == event.CallID && candidate.ID > event.ID {
				if candidate.ExitCode != nil && *candidate.ExitCode == 0 {
					outcome = "success"
				} else if candidate.Kind == KindError || candidate.Kind == KindTestFailure || candidate.ExitCode != nil {
					outcome = "failure"
				}
				break
			}
		}
	}
	return &TranscriptOutcome{EventID: event.ID, Type: string(event.Kind), Outcome: outcome, Text: event.Text, Source: event.Source}
}

func episodeTaskStatus(sessionStatus string, events []Event) string {
	if strings.EqualFold(sessionStatus, "complete") || strings.EqualFold(sessionStatus, "completed") {
		return "complete"
	}
	if strings.EqualFold(sessionStatus, "failed") {
		return "failed"
	}
	for _, event := range events {
		if event.Kind == KindStatus {
			if strings.EqualFold(event.Text, "complete") || strings.EqualFold(event.Text, "completed") {
				return "complete"
			}
			if strings.EqualFold(event.Text, "failed") {
				return "failed"
			}
		}
	}
	return "unknown"
}

func boundEpisodeEvents(events []Event, anchorID int64, options EpisodeOptions) ([]Event, TranscriptOmissions) {
	omitted := TranscriptOmissions{}
	if options.ByteBudget == 0 && options.TokenBudget == 0 {
		return events, omitted
	}
	budget := options.ByteBudget
	if options.TokenBudget > 0 {
		tokenBytes := options.TokenBudget * 4
		if budget == 0 || tokenBytes < budget {
			budget = tokenBytes
		}
	}
	selected := make([]Event, 0, len(events))
	used := 0
	anchorIndex := -1
	for index, event := range events {
		if event.ID == anchorID {
			anchorIndex = index
			break
		}
	}
	for index, event := range events {
		encoded, _ := json.Marshal(episodeEvidence(event))
		size := len(encoded)
		if event.ID == anchorID || used+size <= budget || len(selected) == 0 {
			selected = append(selected, event)
			used += size
			continue
		}
		omitted.Events++
		omitted.Bytes += size
		omitted.Tokens += (size + 3) / 4
		if anchorIndex >= 0 && index < anchorIndex {
			omitted.Context++
		} else {
			omitted.Actions++
		}
	}
	return selected, omitted
}

func episodeID(sessionID string, anchor Event, anchorType AnchorType) string {
	return fmt.Sprintf("%s:%d:%s", sessionID, anchor.ID, anchorType)
}

func containsEventID(ids []int64, id int64) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

func indexOfEvent(events []Event, id int64) int {
	for index, event := range events {
		if event.ID == id {
			return index
		}
	}
	return -1
}

// SortTranscriptEpisodes makes export ordering independent of map or SQL iteration.
func SortTranscriptEpisodes(episodes []TranscriptEpisode) {
	sort.Slice(episodes, func(i, j int) bool {
		if episodes[i].TranscriptID != episodes[j].TranscriptID {
			return episodes[i].TranscriptID < episodes[j].TranscriptID
		}
		return episodes[i].Trigger.EventID < episodes[j].Trigger.EventID
	})
}
