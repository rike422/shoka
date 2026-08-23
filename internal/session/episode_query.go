package session

import (
	"context"
	"fmt"
	"time"
)

// TranscriptEpisodeFilter selects episodes without requiring a text query.
// Filter carries session metadata and candidate time bounds; AnchorType is the
// observable episode trigger classification, not a stored EventKind.
type TranscriptEpisodeFilter struct {
	Filter     Filter
	AnchorType AnchorType
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

// BuildTranscriptEpisodes derives all deterministic episodes in transcripts matching
// the metadata filter. The index is read-only and no sync is performed.
func BuildTranscriptEpisodes(ctx context.Context, env Environment, filter Filter, options EpisodeOptions) ([]TranscriptEpisode, error) {
	return ListTranscriptEpisodes(ctx, env, TranscriptEpisodeFilter{Filter: filter}, options)
}

// ListTranscriptEpisodes derives all observable episodes matching metadata,
// trigger type, and trigger timestamp. It never performs a text search or sync.
func ListTranscriptEpisodes(ctx context.Context, env Environment, episodeFilter TranscriptEpisodeFilter, options EpisodeOptions) ([]TranscriptEpisode, error) {
	filter := episodeFilter.Filter
	filter.IncludeSubagents = true
	if filter.Limit == 0 {
		filter.Limit = 1000
	}
	sessions, err := ListSessions(ctx, env, filter)
	if err != nil {
		return nil, err
	}
	var episodes []TranscriptEpisode
	for _, session := range sessions {
		detail, err := ShowSession(ctx, env, session.SessionUID)
		if err != nil {
			return nil, err
		}
		events := canonicalEvents(detail.Events)
		for index, anchor := range EpisodeAnchorTypes(events) {
			if anchor == "" {
				continue
			}
			episode, err := BuildTranscriptEpisodeFromEvents(TranscriptBuildInput{Session: session, Events: events, AnchorIndex: index, Options: options})
			if err != nil {
				return nil, err
			}
			if episodeFilter.AnchorType != "" && AnchorType(episode.Trigger.Type) != episodeFilter.AnchorType {
				continue
			}
			if !episodeTriggerInRange(episode, filter.From, filter.To) {
				continue
			}
			episodes = append(episodes, episode)
		}
	}
	SortTranscriptEpisodes(episodes)
	return episodes, nil
}

func episodeTriggerInRange(episode TranscriptEpisode, from, to time.Time) bool {
	if from.IsZero() && to.IsZero() {
		return true
	}
	triggeredAt, err := time.Parse(time.RFC3339Nano, episode.Trigger.Timestamp)
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
