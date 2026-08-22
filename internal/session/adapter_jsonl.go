package session

import (
	"encoding/json"
	"fmt"
	"strings"
)

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Name      string          `json:"name"`
	ID        string          `json:"id"`
	ToolUseID string          `json:"tool_use_id"`
	Input     json.RawMessage `json:"input"`
	Arguments json.RawMessage `json:"arguments"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

func decodeCodex(source Source, line []byte) (decodedLine, error) {
	var envelope struct {
		Timestamp string `json:"timestamp"`
		Type      string `json:"type"`
		Payload   struct {
			Type      string          `json:"type"`
			Role      string          `json:"role"`
			Content   []contentBlock  `json:"content"`
			Name      string          `json:"name"`
			CallID    string          `json:"call_id"`
			Input     json.RawMessage `json:"input"`
			Arguments json.RawMessage `json:"arguments"`
			Output    json.RawMessage `json:"output"`
			Message   string          `json:"message"`
			Text      string          `json:"text"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return decodedLine{}, err
	}
	result := decodedLine{}
	timestamp := parseTimestamp(envelope.Timestamp)
	base := Record{NativeSessionID: source.NativeSessionID, Timestamp: timestamp, Cwd: source.Cwd}
	switch envelope.Type {
	case "session_meta", "turn_context", "world_state", "compacted":
		result.SkippedNoise++
		return result, nil
	case "response_item":
		switch envelope.Payload.Type {
		case "reasoning", "encrypted_reasoning":
			result.SkippedReasoning++
		case "message":
			role, ok := messageRole(envelope.Payload.Role)
			if !ok || (role != RoleUser && role != RoleAssistant) {
				result.SkippedUnknown++
				return result, nil
			}
			for index, block := range envelope.Payload.Content {
				if block.Type == "reasoning" || block.Type == "thinking" {
					result.SkippedReasoning++
					continue
				}
				if block.Type != "input_text" && block.Type != "output_text" && block.Type != "text" {
					result.SkippedUnknown++
					continue
				}
				record := base
				record.PartOrdinal = int64(index + 1)
				record.Role = role
				record.Kind = KindMessage
				record.Text = block.Text
				result.Records = append(result.Records, record)
			}
		case "function_call", "custom_tool_call":
			record := base
			record.Role = RoleAssistant
			record.Kind = KindTool
			record.ToolName = envelope.Payload.Name
			record.CallID = envelope.Payload.CallID
			raw := envelope.Payload.Input
			if len(raw) == 0 {
				raw = envelope.Payload.Arguments
			}
			input := parseToolInput(raw)
			record.Command, record.Paths, record.Diff = input.Command, input.Paths, input.Diff
			if record.Command == "" && record.Diff == "" && len(raw) > 0 {
				record.Text = compactJSON(raw)
			}
			result.Records = append(result.Records, record)
		case "function_call_output", "custom_tool_call_output":
			record := base
			record.Role = RoleTool
			record.Kind = KindCommandOutput
			record.CallID = envelope.Payload.CallID
			record.Text = rawText(envelope.Payload.Output)
			record.ExitCode = exitCodeFromText(record.Text)
			result.Records = append(result.Records, record)
		case "web_search_call":
			result.SkippedNoise++
		default:
			result.SkippedUnknown++
		}
	case "event_msg":
		switch envelope.Payload.Type {
		case "agent_reasoning":
			result.SkippedReasoning++
		case "user_message", "agent_message":
			record := base
			if envelope.Payload.Type == "user_message" {
				record.Role = RoleUser
			} else {
				record.Role = RoleAssistant
			}
			record.Kind = KindMessage
			record.Text = envelope.Payload.Message
			if record.Text == "" {
				record.Text = envelope.Payload.Text
			}
			if record.Text != "" {
				result.Records = append(result.Records, record)
			}
		case "task_complete":
			record := base
			record.Role = RoleSystem
			record.Kind = KindStatus
			record.Text = "complete"
			result.Records = append(result.Records, record)
		default:
			result.SkippedNoise++
		}
	default:
		result.SkippedUnknown++
	}
	return result, nil
}

func decodeClaude(source Source, line []byte) (decodedLine, error) {
	var envelope struct {
		Type        string `json:"type"`
		SessionID   string `json:"sessionId"`
		Cwd         string `json:"cwd"`
		Timestamp   string `json:"timestamp"`
		UUID        string `json:"uuid"`
		ParentUUID  string `json:"parentUuid"`
		IsSidechain bool   `json:"isSidechain"`
		Message     struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return decodedLine{}, err
	}
	result := decodedLine{}
	if envelope.Type != "user" && envelope.Type != "assistant" {
		result.SkippedNoise++
		return result, nil
	}
	role, ok := messageRole(envelope.Message.Role)
	if !ok {
		result.SkippedUnknown++
		return result, nil
	}
	base := Record{
		NativeSessionID: envelope.SessionID,
		NativeEventID:   envelope.UUID,
		ParentNativeID:  envelope.ParentUUID,
		Timestamp:       parseTimestamp(envelope.Timestamp),
		Role:            role,
		Cwd:             envelope.Cwd,
	}
	var directText string
	if json.Unmarshal(envelope.Message.Content, &directText) == nil {
		base.Kind = KindMessage
		base.Text = directText
		result.Records = append(result.Records, base)
		return result, nil
	}
	var blocks []contentBlock
	if err := json.Unmarshal(envelope.Message.Content, &blocks); err != nil {
		return decodedLine{}, fmt.Errorf("claude content: %w", err)
	}
	for index, block := range blocks {
		record := base
		record.PartOrdinal = int64(index + 1)
		switch block.Type {
		case "thinking", "redacted_thinking", "reasoning":
			result.SkippedReasoning++
		case "text":
			if role == RoleTool {
				record.Kind = KindCommandOutput
			} else {
				record.Kind = KindMessage
			}
			record.Text = block.Text
			result.Records = append(result.Records, record)
		case "tool_use":
			record.Kind = KindTool
			record.Role = RoleAssistant
			record.NativeEventID = block.ID
			record.CallID = block.ID
			record.ToolName = block.Name
			input := parseToolInput(block.Input)
			record.Command, record.Paths, record.Diff = input.Command, input.Paths, input.Diff
			if record.Command == "" && record.Diff == "" {
				record.Text = compactJSON(block.Input)
			}
			result.Records = append(result.Records, record)
		case "tool_result":
			record.Kind = KindCommandOutput
			record.Role = RoleTool
			record.CallID = block.ToolUseID
			record.Text = rawText(block.Content)
			if block.IsError {
				code := 1
				record.ExitCode = &code
			} else {
				record.ExitCode = exitCodeFromText(record.Text)
			}
			result.Records = append(result.Records, record)
		default:
			result.SkippedUnknown++
		}
	}
	return result, nil
}

func decodeCursor(source Source, line []byte) (decodedLine, error) {
	var envelope struct {
		Type    string `json:"type"`
		Status  string `json:"status"`
		Role    string `json:"role"`
		Message struct {
			Content []contentBlock `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return decodedLine{}, err
	}
	result := decodedLine{}
	if envelope.Type == "turn_ended" {
		result.Records = append(result.Records, Record{
			NativeSessionID: source.NativeSessionID,
			Role:            RoleSystem,
			Kind:            KindStatus,
			Text:            envelope.Status,
		})
		return result, nil
	}
	role, ok := messageRole(envelope.Role)
	if !ok {
		result.SkippedUnknown++
		return result, nil
	}
	for index, block := range envelope.Message.Content {
		record := Record{NativeSessionID: source.NativeSessionID, Role: role, PartOrdinal: int64(index + 1)}
		switch block.Type {
		case "thinking", "reasoning":
			result.SkippedReasoning++
		case "text":
			record.Kind = KindMessage
			record.Text = block.Text
			result.Records = append(result.Records, record)
		case "tool_use":
			record.Kind = KindTool
			record.ToolName = block.Name
			record.CallID = block.ID
			input := parseToolInput(block.Input)
			record.Command, record.Paths, record.Diff = input.Command, input.Paths, input.Diff
			if record.Command == "" && record.Diff == "" {
				record.Text = compactJSON(block.Input)
			}
			result.Records = append(result.Records, record)
		default:
			result.SkippedUnknown++
		}
	}
	return result, nil
}

func decodePi(source Source, line []byte) (decodedLine, error) {
	var envelope struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		ParentID  string `json:"parentId"`
		Timestamp string `json:"timestamp"`
		Message   struct {
			Role       string          `json:"role"`
			Content    json.RawMessage `json:"content"`
			ToolCallID string          `json:"toolCallId"`
			IsError    bool            `json:"isError"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return decodedLine{}, err
	}
	result := decodedLine{}
	if envelope.Type == "session" {
		result.SkippedNoise++
		return result, nil
	}
	if envelope.Type != "message" {
		if envelope.Type == "thinking_level_change" || envelope.Type == "model_change" || envelope.Type == "session_info" {
			result.SkippedNoise++
		} else {
			result.SkippedUnknown++
		}
		return result, nil
	}
	role, ok := messageRole(envelope.Message.Role)
	if !ok {
		result.SkippedUnknown++
		return result, nil
	}
	base := Record{
		NativeSessionID: source.NativeSessionID,
		NativeEventID:   envelope.ID,
		ParentNativeID:  envelope.ParentID,
		Timestamp:       parseTimestamp(envelope.Timestamp),
		Role:            role,
		Cwd:             source.Cwd,
	}
	if role == RoleTool {
		base.CallID = envelope.Message.ToolCallID
		if envelope.Message.IsError {
			code := 1
			base.ExitCode = &code
		}
	}
	var directText string
	if json.Unmarshal(envelope.Message.Content, &directText) == nil {
		base.Kind = KindMessage
		base.Text = directText
		result.Records = append(result.Records, base)
		return result, nil
	}
	var blocks []contentBlock
	if err := json.Unmarshal(envelope.Message.Content, &blocks); err != nil {
		return decodedLine{}, fmt.Errorf("pi content: %w", err)
	}
	for index, block := range blocks {
		record := base
		record.PartOrdinal = int64(index + 1)
		switch strings.ToLower(block.Type) {
		case "thinking", "reasoning":
			result.SkippedReasoning++
		case "text":
			if role == RoleTool {
				record.Kind = KindCommandOutput
			} else {
				record.Kind = KindMessage
			}
			record.Text = block.Text
			result.Records = append(result.Records, record)
		case "toolcall", "tool_call", "tool_use":
			record.Kind = KindTool
			record.Role = RoleAssistant
			record.NativeEventID = block.ID
			record.CallID = block.ID
			record.ToolName = block.Name
			raw := block.Arguments
			if len(raw) == 0 {
				raw = block.Input
			}
			input := parseToolInput(raw)
			record.Command, record.Paths, record.Diff = input.Command, input.Paths, input.Diff
			if record.Command == "" && record.Diff == "" {
				record.Text = compactJSON(raw)
			}
			result.Records = append(result.Records, record)
		default:
			result.SkippedUnknown++
		}
	}
	if role == RoleTool && len(result.Records) == 0 {
		base.Kind = KindCommandOutput
		base.Text = rawText(envelope.Message.Content)
		result.Records = append(result.Records, base)
	}
	return result, nil
}

func messageRole(value string) (Role, bool) {
	switch strings.ToLower(value) {
	case "user":
		return RoleUser, true
	case "assistant":
		return RoleAssistant, true
	case "tool", "toolresult", "tool_result":
		return RoleTool, true
	case "system":
		return RoleSystem, true
	default:
		return "", false
	}
}

func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}
