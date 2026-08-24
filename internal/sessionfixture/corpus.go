// Package sessionfixture creates small native agent logs for Shoka tests and
// local development. It never writes Shoka's SQLite schema directly; callers
// must run the production session sync path against the returned Home.
package sessionfixture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Corpus describes an isolated raw-source home and its derived-state target.
type Corpus struct {
	Root     string `json:"root"`
	Home     string `json:"home"`
	StateDir string `json:"state_dir"`
	Sessions int    `json:"sessions"`
}

// WriteEpisodeCorpus creates native Codex JSONL sources with deterministic
// episode anchors. The destination must not already exist.
func WriteEpisodeCorpus(root string, sessionCount int) (Corpus, error) {
	if sessionCount <= 0 {
		return Corpus{}, fmt.Errorf("fixture session count must be positive")
	}
	if root == "" {
		return Corpus{}, fmt.Errorf("fixture root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return Corpus{}, err
	}
	if _, err := os.Stat(abs); err == nil {
		return Corpus{}, fmt.Errorf("fixture destination already exists: %s", abs)
	} else if !os.IsNotExist(err) {
		return Corpus{}, err
	}
	parent := filepath.Dir(abs)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return Corpus{}, err
	}
	temporary, err := os.MkdirTemp(parent, ".shoka-session-fixture-*")
	if err != nil {
		return Corpus{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(temporary)
		}
	}()
	if err := os.Chmod(temporary, 0o700); err != nil {
		return Corpus{}, err
	}
	sessionsDir := filepath.Join(temporary, "home", ".codex", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		return Corpus{}, err
	}
	for index := 0; index < sessionCount; index++ {
		data, err := codexSession(index)
		if err != nil {
			return Corpus{}, err
		}
		path := filepath.Join(sessionsDir, fmt.Sprintf("episode-session-%03d.jsonl", index))
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return Corpus{}, err
		}
	}
	if err := os.Rename(temporary, abs); err != nil {
		return Corpus{}, err
	}
	committed = true
	return Corpus{
		Root:     abs,
		Home:     filepath.Join(abs, "home"),
		StateDir: filepath.Join(abs, "state"),
		Sessions: sessionCount,
	}, nil
}

func codexSession(index int) ([]byte, error) {
	sessionID := fmt.Sprintf("episode-session-%03d", index)
	if index == 0 {
		return encodeJSONLines([]any{
			sessionMeta(sessionID, "/tmp/episode-project-a", timestamp(2026, 8, 19, 23, 59, 0)),
			message(timestamp(2026, 8, 19, 23, 59, 30), "user", "Keep the export manifest stable"),
			message(timestamp(2026, 8, 19, 23, 59, 40), "assistant", "I will replace the manifest format"),
			message(timestamp(2026, 8, 20, 0, 0, 30), "user", "Keep the export manifest stable"),
			message(timestamp(2026, 8, 20, 0, 0, 40), "user", "違う、既存のexport contractを修正して"),
			message(timestamp(2026, 8, 20, 0, 0, 50), "user", "Only change the episode manifest scope"),
			message(timestamp(2026, 8, 20, 0, 1, 0), "user", "Design direction: use one canonical manifest export path"),
			functionCall(timestamp(2026, 8, 20, 0, 1, 10), "test-call", "go test ./..."),
			functionOutput(timestamp(2026, 8, 20, 0, 1, 11), "test-call", "exit code 1: go test ./... failed"),
			functionCall(timestamp(2026, 8, 20, 0, 1, 20), "build-call", "go build ./..."),
			functionOutput(timestamp(2026, 8, 20, 0, 1, 21), "build-call", "exit code 1: compiler error"),
			taskComplete(timestamp(2026, 8, 20, 0, 1, 30)),
		})
	}
	base := time.Date(2026, 8, 20, 2, 0, 0, 0, time.UTC).Add(time.Duration(index) * time.Minute)
	return encodeJSONLines([]any{
		sessionMeta(sessionID, fmt.Sprintf("/tmp/episode-project-%d", index%3), base),
		message(base.Add(10*time.Second), "user", fmt.Sprintf("Implement fixture session %03d", index)),
		message(base.Add(20*time.Second), "assistant", "I will add a parallel compatibility path"),
		message(base.Add(30*time.Second), "user", fmt.Sprintf("Design direction: use canonical fixture path %03d", index)),
		taskComplete(base.Add(40 * time.Second)),
	})
}

func timestamp(year int, month time.Month, day, hour, minute, second int) time.Time {
	return time.Date(year, month, day, hour, minute, second, 0, time.UTC)
}

func sessionMeta(id, cwd string, at time.Time) map[string]any {
	return map[string]any{
		"timestamp": at.Format(time.RFC3339Nano),
		"type":      "session_meta",
		"payload": map[string]any{
			"id": id, "cwd": cwd, "timestamp": at.Format(time.RFC3339Nano),
		},
	}
}

func message(at time.Time, role, text string) map[string]any {
	contentType := "output_text"
	if role == "user" {
		contentType = "input_text"
	}
	return map[string]any{
		"timestamp": at.Format(time.RFC3339Nano),
		"type":      "response_item",
		"payload": map[string]any{
			"type": "message", "role": role,
			"content": []map[string]any{{"type": contentType, "text": text}},
		},
	}
}

func functionCall(at time.Time, callID, command string) map[string]any {
	return map[string]any{
		"timestamp": at.Format(time.RFC3339Nano),
		"type":      "response_item",
		"payload": map[string]any{
			"type": "function_call", "name": "exec_command", "call_id": callID,
			"arguments": map[string]any{"cmd": command},
		},
	}
}

func functionOutput(at time.Time, callID, output string) map[string]any {
	return map[string]any{
		"timestamp": at.Format(time.RFC3339Nano),
		"type":      "response_item",
		"payload": map[string]any{
			"type": "function_call_output", "call_id": callID, "output": output,
		},
	}
}

func taskComplete(at time.Time) map[string]any {
	return map[string]any{
		"timestamp": at.Format(time.RFC3339Nano),
		"type":      "event_msg",
		"payload":   map[string]any{"type": "task_complete"},
	}
}

func encodeJSONLines(values []any) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	for _, value := range values {
		if err := encoder.Encode(value); err != nil {
			return nil, err
		}
	}
	return output.Bytes(), nil
}
