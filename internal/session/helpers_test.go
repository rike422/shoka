package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNormalizationAndParsingBranches(t *testing.T) {
	for _, agent := range AllAgents {
		parsed, err := ParseAgent(string(agent))
		if err != nil || parsed != agent {
			t.Fatalf("ParseAgent(%q) = %q, %v", agent, parsed, err)
		}
		if got := AdapterFor(agent); got == nil || got.Agent() != agent {
			t.Fatalf("AdapterFor(%q) = %#v", agent, got)
		}
	}
	if _, err := ParseAgent("other"); err == nil {
		t.Fatal("unknown agent was accepted")
	}
	if AdapterFor("other") != nil {
		t.Fatal("unknown adapter was returned")
	}

	exitOne := 1
	cases := []struct {
		name string
		in   Record
		kind EventKind
	}{
		{name: "status", in: Record{Kind: KindStatus}, kind: KindStatus},
		{name: "file", in: Record{ToolName: "write", Paths: []string{" b.go ", "b.go", `a\\x.go`}}, kind: KindFileOperation},
		{name: "test", in: Record{ToolName: "shell", Command: "go test ./..."}, kind: KindTest},
		{name: "command", in: Record{Command: "go build ./..."}, kind: KindCommand},
		{name: "test failure text", in: Record{Role: RoleTool, Text: "go test failed", ExitCode: &exitOne}, kind: KindTestFailure},
		{name: "error", in: Record{Role: RoleTool, Text: "error: nope", ExitCode: &exitOne}, kind: KindError},
		{name: "error word without outcome", in: Record{Role: RoleTool, Text: "0 errors, 0 failed"}, kind: KindCommandOutput},
		{name: "correction", in: Record{Role: RoleUser, Text: "違う、修正して"}, kind: KindUserCorrection},
		{name: "tool output", in: Record{Role: RoleTool, Text: "ok"}, kind: KindCommandOutput},
		{name: "message", in: Record{Role: RoleAssistant, Text: "done"}, kind: KindMessage},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeRecord(tt.in)
			if got.Kind != tt.kind {
				t.Fatalf("kind = %q, want %q", got.Kind, tt.kind)
			}
		})
	}

	secret := NormalizeRecord(Record{Text: `Authorization: Bearer abcdefghijklmnop API_KEY=secretvalue ghp_abcdefghijklmnop {"password":"json-secret-value","Authorization":"Bearer jsonbearertoken"}`})
	if strings.Contains(secret.Text, "secretvalue") || strings.Contains(secret.Text, "abcdefghijklmnop") || strings.Contains(secret.Text, "json-secret-value") || strings.Contains(secret.Text, "jsonbearertoken") {
		t.Fatalf("secret was not redacted: %q", secret.Text)
	}
	relativePath := NormalizeRecord(Record{Role: RoleAssistant, ToolName: "write", Cwd: "/repo", Paths: []string{"/repo/internal/a.go", "/other/b.go"}})
	if len(relativePath.Paths) != 2 || relativePath.Paths[0] != "/other/b.go" || relativePath.Paths[1] != "internal/a.go" {
		t.Fatalf("repository paths were not normalized: %v", relativePath.Paths)
	}
	if got := NormalizeRecord(Record{Text: strings.Repeat("A", 600)}); got.Text != "" {
		t.Fatal("base64-like noise was retained")
	}
	oversized := NormalizeRecord(Record{Role: RoleAssistant, Kind: KindMessage, Text: strings.Repeat("界", maxMessageBytes)})
	if !oversized.TextTruncated || oversized.TextOriginalBytes <= len(oversized.Text) || oversized.TextSHA256 == "" {
		t.Fatalf("oversized text was not bounded with metadata: %+v", oversized)
	}
	exitZero := 0
	for _, tt := range []struct {
		name   string
		record Record
		limit  int
	}{
		{name: "successful output", record: Record{Role: RoleTool, Kind: KindCommandOutput, ExitCode: &exitZero, Text: strings.Repeat("成功", maxFailureOutput)}, limit: maxSuccessfulOutput},
		{name: "unknown output", record: Record{Role: RoleTool, Kind: KindCommandOutput, Text: strings.Repeat("不明", maxFailureOutput)}, limit: maxUnknownOutput},
		{name: "failure output", record: Record{Role: RoleTool, Kind: KindCommandOutput, ExitCode: &exitOne, Text: strings.Repeat("失敗", maxFailureOutput)}, limit: maxFailureOutput},
	} {
		t.Run(tt.name+" cap", func(t *testing.T) {
			got := NormalizeRecord(tt.record)
			if !got.TextTruncated || len(got.Text) > tt.limit || got.TextOriginalBytes <= len(got.Text) || got.TextSHA256 == "" {
				t.Fatalf("bounded output = %+v", got)
			}
		})
	}
	sharedHead := strings.Repeat("頭", maxMessageBytes)
	sharedTail := strings.Repeat("尾", maxMessageBytes)
	left := NormalizeRecord(Record{Role: RoleAssistant, Kind: KindMessage, Text: sharedHead + "左左" + sharedTail})
	right := NormalizeRecord(Record{Role: RoleAssistant, Kind: KindMessage, Text: sharedHead + "右右" + sharedTail})
	if left.Text != right.Text || left.TextSHA256 == right.TextSHA256 || eventContentHash(left) == eventContentHash(right) {
		t.Fatalf("distinct truncated payloads were deduplicated: text_equal=%v sha_equal=%v content_equal=%v", left.Text == right.Text, left.TextSHA256 == right.TextSHA256, eventContentHash(left) == eventContentHash(right))
	}

	input := parseToolInput(json.RawMessage(`{"input":{"cmd":"go test","path":"a.go"}}`))
	if input.Command != "go test" || len(input.Paths) != 1 || input.Paths[0] != "a.go" {
		t.Fatalf("parseToolInput object = %+v", input)
	}
	input = parseToolInput(json.RawMessage(`"{\"command\":\"pytest\"}"`))
	if input.Command != "pytest" {
		t.Fatalf("nested command = %+v", input)
	}
	patch := "*** Begin Patch\n*** Update File: internal/a.go\n@@\n-old\n+new\n*** Add File: docs/new.md\n+hello\n*** End Patch"
	input = parseToolInput(mustJSON(t, patch))
	if input.Command != "" || len(input.Paths) != 2 || input.Paths[0] != "docs/new.md" || input.Paths[1] != "internal/a.go" || !strings.Contains(input.Diff, "*** Update File") {
		t.Fatalf("patch input = %+v", input)
	}
	input = parseToolInput(json.RawMessage(`{"paths":[{"file_path":"z.go"}],"diff":"*** Begin Patch\n*** Update File: z.go\n@@\n-old\n+new\n*** End Patch"}`))
	if len(input.Paths) != 1 || input.Paths[0] != "z.go" || input.Diff == "" {
		t.Fatalf("nested paths and diff = %+v", input)
	}
	input = parseToolInput(json.RawMessage(`{"code":"const patch = \"*** Begin Patch\\n*** Update File: internal/js.go\\n@@\\n-old\\n+new\\n*** End Patch\"; text(await tools.apply_patch(patch));"}`))
	if input.Command != "" || input.Diff == "" || len(input.Paths) != 1 || input.Paths[0] != "internal/js.go" || strings.Contains(input.Diff, "tools.apply_patch") {
		t.Fatalf("embedded apply_patch = %+v", input)
	}
	input = parseToolInput(json.RawMessage(`{"code":"const r = await tools.exec_command({cmd: \"go test ./...\", workdir: \"/tmp/repo\"});"}`))
	if input.Command != "go test ./..." || input.Diff != "" {
		t.Fatalf("embedded exec command = %+v", input)
	}
	if got := NormalizeRecord(Record{Role: RoleAssistant, ToolName: "exec", Diff: patch, Paths: []string{"a.go"}}); got.Kind != KindFileOperation {
		t.Fatalf("patch through generic exec was classified as %s", got.Kind)
	}
	if input := parseToolInput(json.RawMessage(`not-json`)); input.Command != "" || input.Paths != nil || input.Diff != "" {
		t.Fatalf("invalid command input retained: %+v", input)
	}
	if code := exitCodeFromText("exited with code 17"); code == nil || *code != 17 {
		t.Fatalf("exit code = %v", code)
	}
	if exitCodeFromText("ok") != nil {
		t.Fatal("invented exit code")
	}
}

func TestInjectedContextIsExcludedFromUserEvidence(t *testing.T) {
	for _, value := range []string{
		"<recommended_plugins>\n- GitHub\n</recommended_plugins>",
		"# AGENTS.md instructions\n\n<INSTRUCTIONS>\n- 修正して\n</INSTRUCTIONS>",
		"<environment_context>\n<cwd>/tmp/repo</cwd>\n</environment_context>",
		"<app-context>internal app state</app-context><skills_instructions>hidden</skills_instructions>",
	} {
		got := NormalizeRecord(Record{Role: RoleUser, Kind: KindMessage, Text: value})
		if retainRecord(got) {
			t.Fatalf("injected context was retained: %q", got.Text)
		}
	}
	subagent := NormalizeRecord(Record{Role: RoleUser, Kind: KindMessage, Text: `<subagent_notification>{"status":{"completed":"review result"}}</subagent_notification>`})
	if subagent.Role != RoleAssistant || subagent.Kind != KindMessage || subagent.Text != "review result" {
		t.Fatalf("subagent result was not normalized as assistant evidence: %+v", subagent)
	}
	for _, tt := range []struct {
		name, input, want string
	}{
		{name: "pasted file", input: "# Files pasted by the user:\n\n## x: /tmp/x\n\n## My request:\n実装して", want: "実装して"},
		{name: "referenced chat", input: "## Referenced chats with Codex:\n[]\n## My request:\nこれを直して", want: "これを直して"},
		{name: "plain request", input: "通常の依頼", want: "通常の依頼"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeRecord(Record{Role: RoleUser, Kind: KindMessage, Text: tt.input})
			if got.Text != tt.want || got.Kind != KindMessage {
				t.Fatalf("normalized user evidence = %q (%s), want %q", got.Text, got.Kind, tt.want)
			}
		})
	}
}

func TestEvidenceBudgetOutcomeAndSourceHelpers(t *testing.T) {
	items := []string{strings.Repeat("a", 20), strings.Repeat("b", 20), strings.Repeat("c", 20), strings.Repeat("d", 20)}
	selected, omitted := selectBudgeted(items, 50)
	if omitted == 0 || len(selected) >= len(items) || selected[0] != items[0] || selected[len(selected)-1] != items[len(items)-1] {
		t.Fatalf("budget selection = %v omitted=%d", selected, omitted)
	}
	zero, one := 0, 1
	if eventOutcome(Event{}) != "unknown" || eventOutcome(Event{ExitCode: &zero}) != "success" ||
		eventOutcome(Event{ExitCode: &one}) != "failure" || eventOutcome(Event{Kind: KindTestFailure}) != "failure" {
		t.Fatal("event outcome classification drifted")
	}
	sources := uniqueSources([]SourceReference{
		{Path: "b", Line: 2}, {Path: "a", Line: 3}, {Path: "a", Line: 3}, {Path: "a", Line: 1},
	})
	if len(sources) != 3 || sources[0].Path != "a" || sources[0].Line != 1 || sources[2].Path != "b" {
		t.Fatalf("unique sources = %+v", sources)
	}
}

func mustJSON(t *testing.T, value string) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestAdapterUtilityBranches(t *testing.T) {
	if got := rawText(json.RawMessage(`"plain"`)); got != "plain" {
		t.Fatalf("raw string = %q", got)
	}
	blocks := json.RawMessage(`[{"type":"text","text":"a"},{"type":"thinking","text":"secret"},{"type":"output_text","text":"b"}]`)
	if got := rawText(blocks); got != "a\nb" {
		t.Fatalf("raw blocks = %q", got)
	}
	if rawText(nil) != "" || rawText(json.RawMessage(`{bad}`)) != "" {
		t.Fatal("invalid raw text was retained")
	}
	if !parseTimestamp("2026-08-20T01:02:03Z").Equal(time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC)) {
		t.Fatal("timestamp was not parsed")
	}
	if !parseTimestamp("").IsZero() || !parseTimestamp("bad").IsZero() {
		t.Fatal("invalid timestamp was accepted")
	}
	for input, want := range map[string]Role{
		"user": RoleUser, "ASSISTANT": RoleAssistant, "tool_result": RoleTool, "system": RoleSystem,
	} {
		got, ok := messageRole(input)
		if !ok || got != want {
			t.Fatalf("messageRole(%q) = %q, %v", input, got, ok)
		}
	}
	if _, ok := messageRole("other"); ok {
		t.Fatal("unknown role was accepted")
	}
	if got := compactJSON(json.RawMessage(`{"b":2,"a":1}`)); got != `{"a":1,"b":2}` {
		t.Fatalf("compact JSON = %q", got)
	}
	if compactJSON(nil) != "" || compactJSON(json.RawMessage(`bad`)) != "" {
		t.Fatal("invalid JSON was compacted")
	}
}

func TestOpenCodePartVariants(t *testing.T) {
	source := Source{Agent: AgentOpenCode, NativeSessionID: "s1", Cwd: "/tmp/repo"}
	message := json.RawMessage(`{"role":"assistant"}`)
	tests := []struct {
		name      string
		part      string
		records   int
		noise     int
		unknown   int
		reasoning int
	}{
		{name: "text", part: `{"type":"text","text":"done"}`, records: 1},
		{name: "reasoning", part: `{"type":"reasoning","text":"hidden"}`, reasoning: 1},
		{name: "tool error", part: `{"type":"tool","callID":"c1","tool":"bash","state":{"status":"failed","input":{"value":1},"error":"failed"}}`, records: 2},
		{name: "patch strings", part: `{"type":"patch","files":["b.go","a.go"]}`, records: 1},
		{name: "patch objects", part: `{"type":"patch","files":[{"path":"c.go"},{"file":"d.go"}]}`, records: 1},
		{name: "step", part: `{"type":"step-start"}`, noise: 1},
		{name: "unknown", part: `{"type":"snapshot"}`, unknown: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeOpenCodePart(source, "p1", "m1", 1000, message, json.RawMessage(tt.part))
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Records) != tt.records || got.SkippedNoise != tt.noise || got.SkippedUnknown != tt.unknown || got.SkippedReasoning != tt.reasoning {
				t.Fatalf("decoded = %+v", got)
			}
		})
	}
	if _, err := decodeOpenCodePart(source, "p", "m", 0, json.RawMessage(`bad`), json.RawMessage(`{}`)); err == nil {
		t.Fatal("invalid message JSON was accepted")
	}
	if _, err := decodeOpenCodePart(source, "p", "m", 0, json.RawMessage(`{"role":"assistant"}`), json.RawMessage(`bad`)); err == nil {
		t.Fatal("invalid part JSON was accepted")
	}
	unknownRole, err := decodeOpenCodePart(source, "p", "m", 0, json.RawMessage(`{"role":"other"}`), json.RawMessage(`{}`))
	if err != nil || unknownRole.SkippedUnknown != 1 {
		t.Fatalf("unknown role = %+v, %v", unknownRole, err)
	}
	if got := openCodeFiles(json.RawMessage(`bad`)); got != nil {
		t.Fatalf("invalid files = %v", got)
	}
	if !unixMillis(0).IsZero() || unixMillis(1000).UnixMilli() != 1000 {
		t.Fatal("unixMillis conversion failed")
	}
}

func TestJSONLDecoderVariants(t *testing.T) {
	source := Source{NativeSessionID: "s1", Cwd: "/tmp/repo"}
	if _, err := decodeCodex(source, []byte(`bad`)); err == nil {
		t.Fatal("invalid Codex JSON was accepted")
	}
	codexLines := []string{
		`{"type":"turn_context"}`,
		`{"type":"response_item","payload":{"type":"message","role":"tool","content":[]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"thinking","text":"hidden"},{"type":"image","text":"ignored"},{"type":"text","text":"shown"}]}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","name":"custom","call_id":"c1","input":{"value":1}}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c1","output":"exited with code 2"}}`,
		`{"type":"response_item","payload":{"type":"web_search_call"}}`,
		`{"type":"event_msg","payload":{"type":"agent_reasoning"}}`,
		`{"type":"event_msg","payload":{"type":"agent_message","text":"fallback"}}`,
		`{"type":"event_msg","payload":{"type":"task_complete"}}`,
		`{"type":"event_msg","payload":{"type":"other"}}`,
		`{"type":"other"}`,
	}
	var codexRecords, codexSkipped int
	for _, line := range codexLines {
		decoded, err := decodeCodex(source, []byte(line))
		if err != nil {
			t.Fatal(err)
		}
		codexRecords += len(decoded.Records)
		codexSkipped += decoded.SkippedNoise + decoded.SkippedUnknown + decoded.SkippedReasoning
	}
	if codexRecords < 5 || codexSkipped < 6 {
		t.Fatalf("Codex variants records=%d skipped=%d", codexRecords, codexSkipped)
	}
	patchLine := `{"type":"response_item","payload":{"type":"custom_tool_call","name":"exec","call_id":"p1","input":{"code":"const patch = \"*** Begin Patch\\n*** Update File: internal/a.go\\n@@\\n-old\\n+new\\n*** End Patch\"; text(await tools.apply_patch(patch));"}}}`
	decodedPatch, err := decodeCodex(source, []byte(patchLine))
	if err != nil || len(decodedPatch.Records) != 1 {
		t.Fatalf("Codex patch decode = %+v, %v", decodedPatch, err)
	}
	patchRecord := NormalizeRecord(decodedPatch.Records[0])
	if patchRecord.Kind != KindFileOperation || patchRecord.Diff == "" || patchRecord.Text != "" || len(patchRecord.Paths) != 1 {
		t.Fatalf("Codex patch record retained wrapper noise: %+v", patchRecord)
	}

	if _, err := decodeClaude(source, []byte(`bad`)); err == nil {
		t.Fatal("invalid Claude JSON was accepted")
	}
	claudeLines := []string{
		`{"type":"summary"}`,
		`{"type":"assistant","message":{"role":"other","content":"x"}}`,
		`{"type":"user","sessionId":"s1","message":{"role":"user","content":"direct"}}`,
		`{"type":"assistant","sessionId":"s1","message":{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"custom","input":{"value":1}},{"type":"tool_result","tool_use_id":"c1","content":"ok"},{"type":"other"}]}}`,
	}
	for _, line := range claudeLines {
		if _, err := decodeClaude(source, []byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := decodeClaude(source, []byte(`{"type":"user","message":{"role":"user","content":{}}}`)); err == nil {
		t.Fatal("invalid Claude content was accepted")
	}

	if _, err := decodeCursor(source, []byte(`bad`)); err == nil {
		t.Fatal("invalid Cursor JSON was accepted")
	}
	cursorLines := []string{
		`{"type":"turn_ended","status":"complete"}`,
		`{"role":"other"}`,
		`{"role":"assistant","message":{"content":[{"type":"text","text":"done"},{"type":"tool_use","id":"c1","name":"custom","input":{"value":1}},{"type":"other"}]}}`,
	}
	for _, line := range cursorLines {
		if _, err := decodeCursor(source, []byte(line)); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := decodePi(source, []byte(`bad`)); err == nil {
		t.Fatal("invalid Pi JSON was accepted")
	}
	piLines := []string{
		`{"type":"session"}`,
		`{"type":"model_change"}`,
		`{"type":"other"}`,
		`{"type":"message","message":{"role":"other","content":"x"}}`,
		`{"type":"message","id":"m1","message":{"role":"user","content":"direct"}}`,
		`{"type":"message","id":"m2","message":{"role":"assistant","content":[{"type":"tool_call","id":"c1","name":"custom","input":{"value":1}},{"type":"other"}]}}`,
		`{"type":"message","id":"m3","message":{"role":"tool","toolCallId":"c1","isError":true,"content":[{"type":"other"}]}}`,
	}
	for _, line := range piLines {
		if _, err := decodePi(source, []byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := decodePi(source, []byte(`{"type":"message","message":{"role":"user","content":{}}}`)); err == nil {
		t.Fatal("invalid Pi content was accepted")
	}
}

func TestQueryFormattingBranches(t *testing.T) {
	if normalizedLimit(-1, 7) != 7 || normalizedLimit(2, 7) != 2 {
		t.Fatal("limit normalization failed")
	}
	if formatMillis(0) != "" || formatMillis(1000) == "" {
		t.Fatal("millisecond formatting failed")
	}
}
