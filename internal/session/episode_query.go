package session

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"
)

// TranscriptEpisodeFilter selects episodes without requiring a text query.
// Filter carries session metadata and candidate time bounds; AnchorType is the
// observable episode trigger classification, not a stored EventKind.
type TranscriptEpisodeFilter struct {
	Filter     Filter
	AnchorType AnchorType
	Verbose    bool
}

// TranscriptSearchSummary prevents downstream consumers from mistaking session
// count for independent evidence count.
type TranscriptSearchSummary struct {
	RawEvents    int `json:"raw_events"`
	Sessions     int `json:"sessions"`
	TaskLineages int `json:"task_lineages"`
	Projects     int `json:"projects"`
}

type TranscriptSearchResult struct {
	Episodes []TranscriptEpisode     `json:"episodes"`
	Summary  TranscriptSearchSummary `json:"summary"`
}

// TranscriptEpisodeSummary is the metadata-only projection used by list
// output. It deliberately excludes context and evidence payloads.
type TranscriptEpisodeSummary struct {
	EpisodeID       string            `json:"episode_id"`
	TranscriptID    string            `json:"transcript_id"`
	NativeSessionID string            `json:"native_session_id"`
	TaskLineageID   string            `json:"task_lineage_id"`
	ProjectID       string            `json:"project_id"`
	WorkspaceID     string            `json:"workspace_id,omitempty"`
	Trigger         TranscriptTrigger `json:"trigger"`
	TaskStatus      string            `json:"task_status"`
}

// BuildTranscriptEpisodes derives all deterministic episodes in transcripts matching
// the metadata filter. The index is read-only and no sync is performed.
func BuildTranscriptEpisodes(ctx context.Context, env Environment, filter Filter, options EpisodeOptions) ([]TranscriptEpisode, error) {
	return ListTranscriptEpisodes(ctx, env, TranscriptEpisodeFilter{Filter: filter}, options)
}

// ListTranscriptEpisodes derives all observable episodes matching metadata,
// trigger type, and trigger timestamp. It never performs a text search or sync.
func ListTranscriptEpisodes(ctx context.Context, env Environment, episodeFilter TranscriptEpisodeFilter, options EpisodeOptions) ([]TranscriptEpisode, error) {
	options = normalizeTranscriptListOptions(options)
	transcriptVerboseLog(episodeFilter.Verbose, "full list start anchor=%s", episodeFilter.AnchorType)
	var episodes []TranscriptEpisode
	if err := eachTranscriptAnchor(ctx, env, episodeFilter, func(session SessionInfo, events []Event, index int, _ AnchorType) error {
		transcriptVerboseLog(episodeFilter.Verbose, "full episode build session_events=%d", len(events))
		episode, err := BuildTranscriptEpisodeFromEvents(TranscriptBuildInput{Session: session, Events: events, AnchorIndex: index, Options: options})
		if err != nil {
			return err
		}
		episodes = append(episodes, episode)
		return nil
	}); err != nil {
		return nil, err
	}
	transcriptVerboseLog(episodeFilter.Verbose, "full list complete episodes=%d", len(episodes))
	SortTranscriptEpisodes(episodes)
	return episodes, nil
}

// ListTranscriptEpisodeSummaries lists matching anchors without constructing
// the potentially large evidence/context payload for each episode.
func ListTranscriptEpisodeSummaries(ctx context.Context, env Environment, episodeFilter TranscriptEpisodeFilter) ([]TranscriptEpisodeSummary, error) {
	transcriptVerboseLog(episodeFilter.Verbose, "summary list start anchor=%s", episodeFilter.AnchorType)
	filter := episodeFilter.Filter
	filter.IncludeSubagents = true
	sessions, err := listAllSessions(ctx, env, filter)
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		transcriptVerboseLog(episodeFilter.Verbose, "sessions complete count=0")
		return []TranscriptEpisodeSummary{}, nil
	}
	transcriptVerboseLog(episodeFilter.Verbose, "sessions complete count=%d", len(sessions))
	candidates, candidateCount, err := listTranscriptCandidateEvents(ctx, env, sessions, filter, episodeFilter.Verbose)
	if err != nil {
		return nil, err
	}
	transcriptVerboseLog(episodeFilter.Verbose, "candidate query complete events=%d sessions_with_events=%d", candidateCount, len(candidates))
	var summaries []TranscriptEpisodeSummary
	for sessionIndex, session := range sessions {
		events := candidates[session.ID]
		if episodeFilter.Verbose && (sessionIndex == 0 || (sessionIndex+1)%25 == 0) {
			transcriptVerboseLog(true, "classifying session=%d/%d candidate_events=%d", sessionIndex+1, len(sessions), len(events))
		}
		for index, anchorType := range EpisodeAnchorTypes(events) {
			if anchorType == "" || (episodeFilter.AnchorType != "" && anchorType != episodeFilter.AnchorType) {
				continue
			}
			anchor := events[index]
			if !episodeTriggerInRange(anchor.Timestamp, filter.From, filter.To) {
				continue
			}
			summaries = append(summaries, TranscriptEpisodeSummary{
				EpisodeID:       episodeID(session.SessionUID, anchor, anchorType),
				TranscriptID:    session.SessionUID,
				NativeSessionID: session.NativeID,
				TaskLineageID:   session.TaskLineageID,
				ProjectID:       session.ProjectID,
				WorkspaceID:     session.WorkspaceID,
				Trigger:         TranscriptTrigger{Type: string(anchorType), EventID: anchor.ID, Timestamp: anchor.Timestamp},
				TaskStatus:      episodeTaskStatus(session.Status, events),
			})
		}
	}
	transcriptVerboseLog(episodeFilter.Verbose, "summary list complete summaries=%d", len(summaries))
	sortTranscriptEpisodeSummaries(summaries)
	return summaries, nil
}

func listTranscriptCandidateEvents(ctx context.Context, env Environment, sessions []SessionInfo, filter Filter, verbose bool) (map[int64][]Event, int, error) {
	transcriptVerboseLog(verbose, "candidate query start sessions=%d", len(sessions))
	store, err := openExistingStore(env)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = store.Close() }()

	candidates := make(map[int64][]Event, len(sessions))
	candidateCount := 0
	const sessionBatchSize = 500
	for start := 0; start < len(sessions); start += sessionBatchSize {
		end := start + sessionBatchSize
		if end > len(sessions) {
			end = len(sessions)
		}
		batch := sessions[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		query := `
			SELECT e.session_id,e.id,e.ts,e.role,e.kind,c.text
			FROM events e
			JOIN event_contents c ON c.id=e.content_id
			WHERE e.session_id IN (` + placeholders + `)
			  AND e.duplicate_of IS NULL
			  AND (
			    e.kind IN (?, ?, ?, ?)
			    OR (e.role=? AND e.kind=?)
			  )`
		args := make([]any, 0, len(batch)+7)
		for _, session := range batch {
			args = append(args, session.ID)
		}
		args = append(args, KindError, KindTestFailure, KindUserCorrection, KindStatus, RoleUser, KindMessage)
		// Classification of a requirement restatement needs user messages from
		// before the output window. The lower bound is therefore applied only
		// after EpisodeAnchorTypes has seen the preceding session history.
		if !filter.To.IsZero() {
			query += ` AND e.ts<=?`
			args = append(args, filter.To.UTC().UnixMilli())
		}
		query += ` ORDER BY e.session_id,e.ts,e.source_id,e.record_ordinal,e.part_ordinal,e.id`
		rows, err := store.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, 0, fmt.Errorf("list transcript episode summaries: %w", err)
		}
		for rows.Next() {
			var sessionID int64
			var event Event
			var timestamp int64
			if err := rows.Scan(&sessionID, &event.ID, &timestamp, &event.Role, &event.Kind, &event.Text); err != nil {
				_ = rows.Close()
				return nil, 0, err
			}
			event.Timestamp = formatMillis(timestamp)
			candidates[sessionID] = append(candidates[sessionID], event)
			candidateCount++
			if verbose && candidateCount%1000 == 0 {
				transcriptVerboseLog(true, "candidate rows scanned=%d", candidateCount)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, 0, err
		}
		if err := rows.Close(); err != nil {
			return nil, 0, err
		}
	}
	return candidates, candidateCount, nil
}

func eachTranscriptAnchor(ctx context.Context, env Environment, episodeFilter TranscriptEpisodeFilter, visit func(SessionInfo, []Event, int, AnchorType) error) error {
	filter := episodeFilter.Filter
	filter.IncludeSubagents = true
	sessions, err := listAllSessions(ctx, env, filter)
	if err != nil {
		return err
	}
	transcriptVerboseLog(episodeFilter.Verbose, "full list sessions complete count=%d", len(sessions))
	for sessionIndex, session := range sessions {
		if episodeFilter.Verbose && (sessionIndex == 0 || (sessionIndex+1)%10 == 0) {
			transcriptVerboseLog(true, "loading full session=%d/%d", sessionIndex+1, len(sessions))
		}
		detail, err := ShowSession(ctx, env, session.SessionUID)
		if err != nil {
			return err
		}
		events := canonicalEvents(detail.Events)
		transcriptVerboseLog(episodeFilter.Verbose, "loaded full session=%d events=%d", sessionIndex+1, len(events))
		for index, anchorType := range EpisodeAnchorTypes(events) {
			if anchorType == "" || (episodeFilter.AnchorType != "" && anchorType != episodeFilter.AnchorType) {
				continue
			}
			if !episodeTriggerInRange(events[index].Timestamp, filter.From, filter.To) {
				continue
			}
			if err := visit(session, events, index, anchorType); err != nil {
				return err
			}
		}
	}
	return nil
}

func transcriptVerboseLog(enabled bool, format string, args ...any) {
	if !enabled {
		return
	}
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	message := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "[transcript verbose] %s heap_alloc_mb=%d heap_sys_mb=%d num_gc=%d\n",
		message, stats.HeapAlloc/(1024*1024), stats.HeapSys/(1024*1024), stats.NumGC)
}

func episodeTriggerInRange(timestamp string, from, to time.Time) bool {
	if from.IsZero() && to.IsZero() {
		return true
	}
	triggeredAt, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return false
	}
	triggeredAt = triggeredAt.UTC()
	if !from.IsZero() && triggeredAt.Before(from.UTC()) {
		return false
	}
	if !to.IsZero() && triggeredAt.After(to.UTC()) {
		return false
	}
	return true
}

func normalizeTranscriptListOptions(options EpisodeOptions) EpisodeOptions {
	if options.Before == 0 && options.After == 0 && options.ByteBudget == 0 && options.TokenBudget == 0 {
		options.Before = 3
		options.After = 8
		options.ByteBudget = 64 * 1024
	}
	return options
}

func sortTranscriptEpisodeSummaries(summaries []TranscriptEpisodeSummary) {
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].TranscriptID != summaries[j].TranscriptID {
			return summaries[i].TranscriptID < summaries[j].TranscriptID
		}
		return summaries[i].Trigger.EventID < summaries[j].Trigger.EventID
	})
}

// SearchTranscriptEpisodes returns only episodes containing a matched canonical event.
func SearchTranscriptEpisodes(ctx context.Context, env Environment, queryText string, filter Filter, options EpisodeOptions) (TranscriptSearchResult, error) {
	hits, err := SearchSessions(ctx, env, queryText, filter)
	if err != nil {
		return TranscriptSearchResult{}, err
	}
	result := TranscriptSearchResult{Episodes: []TranscriptEpisode{}}
	if len(hits) == 0 {
		return result, nil
	}
	sessionIDs := map[string]struct{}{}
	lineages := map[string]struct{}{}
	projects := map[string]struct{}{}
	matchedBySession := map[string][]SearchHit{}
	for _, hit := range hits {
		if hit.RawEventCount > 0 {
			result.Summary.RawEvents += hit.RawEventCount
		} else {
			result.Summary.RawEvents++
		}
		sessionIDs[hit.SessionUID] = struct{}{}
		if hit.TaskLineageID != "" {
			lineages[hit.TaskLineageID] = struct{}{}
		}
		if hit.ProjectID != "" {
			projects[hit.ProjectID] = struct{}{}
		}
		matchedBySession[hit.SessionUID] = append(matchedBySession[hit.SessionUID], hit)
	}
	result.Summary.Sessions = len(sessionIDs)
	result.Summary.TaskLineages = len(lineages)
	result.Summary.Projects = len(projects)
	for sessionID, sessionHits := range matchedBySession {
		session, err := sessionByUID(ctx, env, sessionID)
		if err != nil {
			return TranscriptSearchResult{}, err
		}
		detail, err := ShowSession(ctx, env, sessionID)
		if err != nil {
			return TranscriptSearchResult{}, err
		}
		events := canonicalEvents(detail.Events)
		matchedIDs := map[int64]struct{}{}
		for _, hit := range sessionHits {
			matchedIDs[hit.ID] = struct{}{}
		}
		for index, anchor := range EpisodeAnchorTypes(events) {
			if anchor == "" {
				continue
			}
			episode, err := BuildTranscriptEpisodeFromEvents(TranscriptBuildInput{Session: session, Events: events, AnchorIndex: index, Options: options})
			if err != nil {
				return TranscriptSearchResult{}, err
			}
			for _, eventID := range episode.SourceEventIDs {
				if _, ok := matchedIDs[eventID]; !ok {
					continue
				}
				episode.SearchReasons = append(episode.SearchReasons, fmt.Sprintf("query matched event %d", eventID))
			}
			if len(episode.SearchReasons) > 0 {
				result.Episodes = append(result.Episodes, episode)
			}
		}
	}
	SortTranscriptEpisodes(result.Episodes)
	return result, nil
}

func canonicalEvents(events []Event) []Event {
	canonical := make([]Event, 0, len(events))
	for _, event := range events {
		if event.DuplicateOf == nil {
			canonical = append(canonical, event)
		}
	}
	return canonical
}

func sessionByUID(ctx context.Context, env Environment, uid string) (SessionInfo, error) {
	detail, err := ShowSession(ctx, env, uid)
	if err != nil {
		return SessionInfo{}, err
	}
	return detail.SessionInfo, nil
}
