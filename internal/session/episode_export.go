package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BuildTranscriptEpisode resolves one stable episode id without syncing or
// loading unrelated sessions into the exported evidence.
func BuildTranscriptEpisode(ctx context.Context, env Environment, id string, options EpisodeOptions) (TranscriptEpisode, error) {
	parts := strings.Split(id, ":")
	if len(parts) < 3 {
		return TranscriptEpisode{}, fmt.Errorf("invalid episode id %q", id)
	}
	eventIDPart := parts[len(parts)-2]
	var eventID int64
	if _, err := fmt.Sscan(eventIDPart, &eventID); err != nil || eventID <= 0 {
		return TranscriptEpisode{}, fmt.Errorf("invalid episode event id in %q", id)
	}
	sessionID := strings.Join(parts[:len(parts)-2], ":")
	detail, err := ShowSession(ctx, env, sessionID)
	if err != nil {
		return TranscriptEpisode{}, err
	}
	events := canonicalEvents(detail.Events)
	for index, event := range events {
		if event.ID != eventID {
			continue
		}
		if classifyAnchor(events, index) == "" {
			return TranscriptEpisode{}, fmt.Errorf("event %d is not an episode anchor", eventID)
		}
		episode, err := BuildTranscriptEpisodeFromEvents(TranscriptBuildInput{Session: detail.SessionInfo, Events: events, AnchorIndex: index, Options: options})
		if err != nil {
			return TranscriptEpisode{}, err
		}
		if episode.EpisodeID != id {
			return TranscriptEpisode{}, fmt.Errorf("episode id %q does not match stored anchor", id)
		}
		return episode, nil
	}
	return TranscriptEpisode{}, fmt.Errorf("episode %q not found", id)
}

// WriteTranscriptEpisodeEvidence writes a private, atomically replaced episode
// document. The stored event text is already redacted and reasoning-free.
func WriteTranscriptEpisodeEvidence(ctx context.Context, env Environment, id, output string, options EpisodeOptions) error {
	if strings.TrimSpace(output) == "" {
		return fmt.Errorf("episode output path is required")
	}
	episode, err := BuildTranscriptEpisode(ctx, env, id, options)
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
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".transcript-episode-*.tmp")
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
	if err := encoder.Encode(episode); err != nil {
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
