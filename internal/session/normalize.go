package session

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxMessageBytes     = 32 << 10
	maxSuccessfulOutput = 4 << 10
	maxUnknownOutput    = 8 << 10
	maxFailureOutput    = 16 << 10
	maxToolPayloadBytes = 8 << 10
	maxCommandBytes     = 8 << 10
	maxDiffBytes        = 64 << 10
)

var (
	assignmentSecret = regexp.MustCompile(`(?i)((?:"[A-Z0-9_]*(?:TOKEN|KEY|SECRET|PASSWORD|PASSWD)"|'[A-Z0-9_]*(?:TOKEN|KEY|SECRET|PASSWORD|PASSWD)'|\b[A-Z0-9_]*(?:TOKEN|KEY|SECRET|PASSWORD|PASSWD))\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;}\]]+)`)
	bearerSecret     = regexp.MustCompile(`(?i)((?:"authorization"|'authorization'|\bauthorization)\s*[:=]\s*["']?\s*bearer\s+)[A-Za-z0-9._~+/=-]{8,}`)
	knownSecret      = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{12,}|ghp_[A-Za-z0-9]{12,}|github_pat_[A-Za-z0-9_]{12,}|xox[baprs]-[A-Za-z0-9-]{12,})\b`)
	correctionWords  = regexp.MustCompile(`(?i)(?:\b(?:actually|instead|no,|correction)\b|違(?:う|います)|ではなく|修正して|取り消して)`)
	testCommand      = regexp.MustCompile(`(?i)(?:^|[;&|]\s*|\s)(?:go\s+test|pytest|cargo\s+test|npm\s+(?:run\s+)?test|pnpm\s+(?:run\s+)?test|yarn\s+test|make\s+test)(?:\s|$)`)
	jsAssignment     = regexp.MustCompile(`(?s)\b(?:const|let|var)\s+[A-Za-z_$][A-Za-z0-9_$]*\s*=\s*("(?:\\.|[^"\\])*")`)
	jsCommandField   = regexp.MustCompile(`(?s)(?:\bcmd|["']cmd["'])\s*:\s*("(?:\\.|[^"\\])*")`)
	exitCodePattern  = regexp.MustCompile(`(?i)(?:exit(?:ed)?(?:\s+with)?(?:\s+code)?[=: ]+)(-?\d+)`)
)

// NormalizeRecord applies deterministic classification, noise handling, path
// normalization, and high-confidence secret redaction before persistence.
func NormalizeRecord(record Record) Record {
	record.Text = redact(strings.TrimSpace(strings.ReplaceAll(record.Text, "\x00", "")))
	record.Command = redact(strings.TrimSpace(strings.ReplaceAll(record.Command, "\x00", "")))
	record.Diff = redact(strings.TrimSpace(strings.ReplaceAll(record.Diff, "\x00", "")))
	if record.Role == RoleUser {
		if result, ok := subagentResult(record.Text); ok {
			record.Role = RoleAssistant
			record.Kind = KindMessage
			record.Text = result
		} else {
			record.Text = observableUserText(record.Text)
		}
	}
	if looksLikeBase64(record.Text) {
		record.Text = ""
	}
	record.Paths = relativizePaths(normalizePaths(record.Paths), record.Cwd)

	tool := strings.ToLower(record.ToolName)
	switch {
	case record.Kind == KindStatus:
	case record.Diff != "" || isFileTool(tool):
		record.Kind = KindFileOperation
	case isCommandTool(tool) || record.Command != "":
		record.Kind = KindCommand
		if testCommand.MatchString(" " + record.Command) {
			record.Kind = KindTest
		}
	case record.Role == RoleTool && nonZero(record.ExitCode):
		if testCommand.MatchString(" " + record.Text) {
			record.Kind = KindTestFailure
		} else {
			record.Kind = KindError
		}
	case record.Role == RoleUser && correctionWords.MatchString(record.Text):
		record.Kind = KindUserCorrection
	case record.Role == RoleTool && (record.Kind == "" || record.Kind == KindMessage):
		record.Kind = KindCommandOutput
	case record.Kind == "":
		record.Kind = KindMessage
	}
	record.Text, record.TextTruncated, record.TextOriginalBytes, record.TextSHA256 = boundText(record.Text, textLimit(record))
	record.Command, record.CommandTruncated, record.CommandOriginalBytes, record.CommandSHA256 = boundText(record.Command, maxCommandBytes)
	record.Diff, record.DiffTruncated, record.DiffOriginalBytes, record.DiffSHA256 = boundText(record.Diff, maxDiffBytes)
	return record
}

func subagentResult(value string) (string, bool) {
	value = strings.TrimSpace(value)
	const open = "<subagent_notification>"
	const close = "</subagent_notification>"
	if !strings.HasPrefix(value, open) || !strings.HasSuffix(value, close) {
		return "", false
	}
	payload := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, open), close))
	var notification struct {
		Status struct {
			Completed string `json:"completed"`
		} `json:"status"`
	}
	if json.Unmarshal([]byte(payload), &notification) != nil || strings.TrimSpace(notification.Status.Completed) == "" {
		return "", false
	}
	return strings.TrimSpace(notification.Status.Completed), true
}

func observableUserText(value string) string {
	value = strings.TrimSpace(value)
	for _, wrapper := range []string{"# Files pasted by the user:", "# Files attached by the user:", "## Referenced chats with Codex:"} {
		if strings.HasPrefix(value, wrapper) {
			if index := strings.LastIndex(value, "## My request:"); index >= 0 {
				return strings.TrimSpace(value[index+len("## My request:"):])
			}
		}
	}
	lower := strings.ToLower(value)
	for _, prefix := range []string{
		"<recommended_plugins>", "# agents.md instructions", "<environment_context>",
		"<app-context>", "<skills_instructions>", "<permissions instructions>",
		"<collaboration_mode>", "<apps_instructions>", "<plugins_instructions>",
		"<subagent_notification>", "<team_message>", "<system-reminder>", "<turn_aborted>",
	} {
		if strings.HasPrefix(lower, prefix) {
			return ""
		}
	}
	return value
}

func textLimit(record Record) int {
	if record.Kind == KindError || record.Kind == KindTestFailure || nonZero(record.ExitCode) {
		return maxFailureOutput
	}
	if record.Kind == KindCommandOutput && record.ExitCode != nil && *record.ExitCode == 0 {
		return maxSuccessfulOutput
	}
	if record.Kind == KindCommandOutput || record.Role == RoleTool {
		return maxUnknownOutput
	}
	if record.Kind == KindTool || record.Kind == KindFileOperation {
		return maxToolPayloadBytes
	}
	return maxMessageBytes
}

func boundText(value string, limit int) (string, bool, int, string) {
	originalBytes := len(value)
	if value == "" {
		return "", false, 0, ""
	}
	if originalBytes <= limit {
		return value, false, originalBytes, ""
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
	marker := []byte("\n…[truncated]…\n")
	budget := limit - len(marker)
	if budget <= 0 {
		return "", true, originalBytes, digest
	}
	headBytes := budget / 2
	for headBytes > 0 && !utf8.ValidString(value[:headBytes]) {
		headBytes--
	}
	tailBytes := budget - headBytes
	tailStart := len(value) - tailBytes
	for tailStart < len(value) && !utf8.ValidString(value[tailStart:]) {
		tailStart++
	}
	return value[:headBytes] + string(marker) + value[tailStart:], true, originalBytes, digest
}

func redact(value string) string {
	value = assignmentSecret.ReplaceAllString(value, `${1}[REDACTED]`)
	value = bearerSecret.ReplaceAllString(value, `${1}[REDACTED]`)
	return knownSecret.ReplaceAllString(value, "[REDACTED]")
}

func looksLikeBase64(value string) bool {
	compact := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, value)
	if len(compact) < 512 {
		return false
	}
	valid := 0
	for _, r := range compact {
		if r <= unicode.MaxASCII && ((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || strings.ContainsRune("+/=_-", r)) {
			valid++
		}
	}
	return valid*100/len([]rune(compact)) >= 95
}

func isCommandTool(tool string) bool {
	return tool == "bash" || tool == "shell" || tool == "exec" || tool == "exec_command" || tool == "run_command"
}

func isFileTool(tool string) bool {
	return tool == "edit" || tool == "write" || tool == "apply_patch" || tool == "create_file" || tool == "delete_file"
}

func nonZero(value *int) bool {
	return value != nil && *value != 0
}

func normalizePaths(paths []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, path := range paths {
		path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func relativizePaths(paths []string, cwd string) []string {
	if cwd == "" {
		return paths
	}
	var values []string
	for _, path := range paths {
		if filepath.IsAbs(path) {
			relative, err := filepath.Rel(cwd, path)
			if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				path = filepath.ToSlash(relative)
			}
		}
		values = append(values, path)
	}
	return normalizePaths(values)
}

type toolInput struct {
	Command string
	Paths   []string
	Diff    string
}

func parseToolInput(raw json.RawMessage) toolInput {
	if len(raw) == 0 || string(raw) == "null" {
		return toolInput{}
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return toolInput{}
	}
	var result toolInput
	walkToolInput(value, &result, false)
	result.Paths = normalizePaths(append(result.Paths, patchPaths(result.Diff)...))
	result.Diff = strings.TrimSpace(result.Diff)
	return result
}

func walkToolInput(value any, result *toolInput, commandContext bool) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := typed[key]
			lower := strings.ToLower(key)
			switch lower {
			case "cmd", "command", "script":
				walkToolInput(child, result, true)
			case "path", "file_path", "file", "target_file":
				collectPaths(child, &result.Paths)
			case "patch", "diff":
				collectDiff(child, result)
			default:
				walkToolInput(child, result, false)
			}
		}
	case []any:
		for _, child := range typed {
			walkToolInput(child, result, commandContext)
		}
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return
		}
		if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
			var nested any
			if json.Unmarshal([]byte(text), &nested) == nil {
				walkToolInput(nested, result, commandContext)
				return
			}
		}
		if patch := embeddedPatchText(text); patch != "" {
			appendDiff(result, patch)
			return
		}
		if command := embeddedCommandText(text); command != "" && result.Command == "" {
			result.Command = command
			return
		}
		if looksLikePatch(text) {
			appendDiff(result, text)
			return
		}
		if commandContext && result.Command == "" {
			result.Command = text
		} else if result.Command == "" && !strings.ContainsAny(text, "\n\r") {
			result.Command = text
		}
	}
}

func embeddedPatchText(value string) string {
	if !strings.Contains(value, "tools.apply_patch(") {
		return ""
	}
	for _, match := range jsAssignment.FindAllStringSubmatch(value, -1) {
		if len(match) != 2 {
			continue
		}
		decoded, err := strconv.Unquote(match[1])
		if err == nil && looksLikePatch(decoded) {
			return decoded
		}
	}
	return ""
}

func embeddedCommandText(value string) string {
	if !strings.Contains(value, "tools.exec_command(") {
		return ""
	}
	match := jsCommandField.FindStringSubmatch(value)
	if len(match) != 2 {
		return ""
	}
	decoded, err := strconv.Unquote(match[1])
	if err != nil {
		return ""
	}
	return strings.TrimSpace(decoded)
}

func collectPaths(value any, paths *[]string) {
	switch typed := value.(type) {
	case string:
		*paths = append(*paths, typed)
	case []any:
		for _, item := range typed {
			collectPaths(item, paths)
		}
	}
}

func collectDiff(value any, result *toolInput) {
	if text, ok := value.(string); ok && looksLikePatch(text) {
		appendDiff(result, text)
	}
}

func appendDiff(result *toolInput, value string) {
	if result.Diff != "" {
		result.Diff += "\n"
	}
	result.Diff += value
}

func looksLikePatch(value string) bool {
	return strings.Contains(value, "*** Begin Patch") || strings.Contains(value, "*** Update File:") ||
		strings.Contains(value, "diff --git ") || (strings.Contains(value, "\n--- ") && strings.Contains(value, "\n+++ "))
}

func patchPaths(value string) []string {
	var paths []string
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "} {
			if strings.HasPrefix(line, prefix) {
				paths = append(paths, strings.TrimSpace(strings.TrimPrefix(line, prefix)))
			}
		}
		if strings.HasPrefix(line, "diff --git a/") {
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				paths = append(paths, strings.TrimPrefix(parts[3], "b/"))
			}
		} else if strings.HasPrefix(line, "+++ b/") {
			paths = append(paths, strings.TrimPrefix(line, "+++ b/"))
		}
	}
	return paths
}

func exitCodeFromText(value string) *int {
	match := exitCodePattern.FindStringSubmatch(value)
	if len(match) != 2 {
		return nil
	}
	code, err := strconv.Atoi(match[1])
	if err != nil {
		return nil
	}
	return &code
}
