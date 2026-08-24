package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	shokasession "github.com/rike422/shoka/internal/session"
)

type parsedOptions struct {
	values      map[string]string
	booleans    map[string]bool
	positionals []string
}

func cmdSession(env shokasession.Environment, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: shoka session sync|list|show|search|export|reindex")
	}
	switch args[0] {
	case "sync":
		return cmdSessionSync(env, args[1:])
	case "list":
		return cmdSessionList(env, args[1:])
	case "show":
		return cmdSessionShow(env, args[1:])
	case "search":
		return cmdSessionSearch(env, args[1:])
	case "export":
		return cmdSessionExport(env, args[1:])
	case "reindex":
		return cmdSessionReindex(env, args[1:])
	default:
		return fmt.Errorf("unknown session subcommand: %s", args[0])
	}
}

func cmdTranscript(env shokasession.Environment, args []string) error {
	if len(args) < 2 || args[0] != "episode" {
		return fmt.Errorf("usage: shoka transcript episode list|summary|search|export")
	}
	args = args[1:]
	switch args[0] {
	case "list":
		return cmdTranscriptEpisodeList(env, args[1:])
	case "summary":
		return cmdTranscriptEpisodeSummary(env, args[1:])
	case "search":
		return cmdTranscriptEpisodeSearch(env, args[1:])
	case "export":
		return cmdTranscriptEpisodeExport(env, args[1:])
	default:
		return fmt.Errorf("unknown transcript episode subcommand: %s", args[0])
	}
}

func cmdTranscriptEpisodeList(env shokasession.Environment, args []string) error {
	if hasCLIFlag(args, "--verbose") {
		fmt.Fprintf(os.Stderr, "[transcript verbose] episode list entry args_count=%d\n", len(args))
	}
	spec := filterOptionSpec(false)
	delete(spec, "--top")
	spec["--verbose"] = false
	parsed, err := parseCommandOptions(args, spec)
	if err != nil {
		return err
	}
	if len(parsed.positionals) != 0 {
		return fmt.Errorf("transcript episode list does not accept positional arguments")
	}
	filter, err := transcriptEpisodeFilterFromOptions(parsed)
	if err != nil {
		return err
	}
	filter = applyDefaultTranscriptEpisodeRange(filter, parsed, time.Now().UTC())
	filter.Verbose = parsed.booleans["--verbose"]
	summaries, err := shokasession.ListTranscriptEpisodeSummaries(context.Background(), env, filter)
	if err != nil {
		return err
	}
	if parsed.booleans["--json"] {
		return encodeJSON(summaries)
	}
	for _, summary := range summaries {
		fmt.Printf("%s  %s  trigger=%s event=%d status=%s\n", summary.Trigger.Timestamp, summary.EpisodeID, summary.Trigger.Type, summary.Trigger.EventID, summary.TaskStatus)
	}
	return nil
}

func cmdTranscriptEpisodeSummary(env shokasession.Environment, args []string) error {
	spec := filterOptionSpec(false)
	delete(spec, "--top")
	spec["--verbose"] = false
	parsed, err := parseCommandOptions(args, spec)
	if err != nil {
		return err
	}
	if len(parsed.positionals) != 0 {
		return fmt.Errorf("transcript episode summary does not accept positional arguments")
	}
	filter, err := transcriptEpisodeFilterFromOptions(parsed)
	if err != nil {
		return err
	}
	filter = applyDefaultTranscriptEpisodeRange(filter, parsed, time.Now().UTC())
	filter.Verbose = parsed.booleans["--verbose"]
	summary, err := shokasession.SummarizeTranscriptEpisodes(context.Background(), env, filter)
	if err != nil {
		return err
	}
	if parsed.booleans["--json"] {
		return encodeJSON(summary)
	}
	fmt.Printf("episodes=%d sessions=%d task_lineages=%d projects=%d\n", summary.Episodes, summary.Sessions, summary.TaskLineages, summary.Projects)
	for _, trigger := range sortedCountKeys(summary.ByTrigger) {
		fmt.Printf("trigger=%s count=%d\n", trigger, summary.ByTrigger[trigger])
	}
	for _, status := range sortedCountKeys(summary.ByStatus) {
		fmt.Printf("status=%s count=%d\n", status, summary.ByStatus[status])
	}
	return nil
}

func sortedCountKeys(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func applyDefaultTranscriptEpisodeRange(filter shokasession.TranscriptEpisodeFilter, parsed parsedOptions, now time.Time) shokasession.TranscriptEpisodeFilter {
	if parsed.values["--from"] == "" && parsed.values["--to"] == "" {
		filter.Filter.From = now.Add(-7 * 24 * time.Hour)
		filter.Filter.To = now
	}
	return filter
}

func transcriptEpisodeFilterFromOptions(parsed parsedOptions) (shokasession.TranscriptEpisodeFilter, error) {
	values := make(map[string]string, len(parsed.values))
	for key, value := range parsed.values {
		values[key] = value
	}
	eventType := values["--event-type"]
	values["--event-type"] = ""
	baseFilter, err := filterFromOptions(parsedOptions{values: values, booleans: parsed.booleans, positionals: parsed.positionals})
	if err != nil {
		return shokasession.TranscriptEpisodeFilter{}, err
	}
	filter := shokasession.TranscriptEpisodeFilter{Filter: baseFilter}
	if eventType == "" {
		return filter, nil
	}
	anchorType, err := parseAnchorType(eventType)
	if err != nil {
		return shokasession.TranscriptEpisodeFilter{}, err
	}
	filter.AnchorType = anchorType
	return filter, nil
}

func cmdTranscriptEpisodeSearch(env shokasession.Environment, args []string) error {
	spec := filterOptionSpec(false)
	addEpisodeOptionSpec(spec)
	parsed, err := parseCommandOptions(args, spec)
	if err != nil {
		return err
	}
	if len(parsed.positionals) == 0 {
		return fmt.Errorf("transcript episode search requires a QUERY")
	}
	filter, err := filterFromOptions(parsed)
	if err != nil {
		return err
	}
	options, err := episodeOptionsFromParsed(parsed)
	if err != nil {
		return err
	}
	result, err := shokasession.SearchTranscriptEpisodes(context.Background(), env, strings.Join(parsed.positionals, " "), filter, options)
	if err != nil {
		return err
	}
	if parsed.booleans["--json"] {
		return encodeJSON(result)
	}
	fmt.Printf("raw events: %d  sessions: %d  task lineages: %d  projects: %d  episodes: %d\n",
		result.Summary.RawEvents, result.Summary.Sessions, result.Summary.TaskLineages, result.Summary.Projects, len(result.Episodes))
	for _, episode := range result.Episodes {
		fmt.Printf("%s  trigger=%s event=%d status=%s\n", episode.EpisodeID, episode.Trigger.Type, episode.Trigger.EventID, episode.TaskStatus)
		for _, reason := range episode.SearchReasons {
			fmt.Printf("  reason: %s\n", reason)
		}
	}
	return nil
}

func cmdTranscriptEpisodeExport(env shokasession.Environment, args []string) error {
	spec := map[string]bool{"--output": true}
	addEpisodeOptionSpec(spec)
	parsed, err := parseCommandOptions(args, spec)
	if err != nil {
		return err
	}
	if len(parsed.positionals) != 1 || parsed.values["--output"] == "" {
		return fmt.Errorf("usage: shoka transcript episode export EPISODE_ID --output PATH")
	}
	options, err := episodeOptionsFromParsed(parsed)
	if err != nil {
		return err
	}
	if err := shokasession.WriteTranscriptEpisodeEvidence(context.Background(), env, parsed.positionals[0], parsed.values["--output"], options); err != nil {
		return err
	}
	fmt.Printf("exported episode evidence: %s\n", parsed.values["--output"])
	return nil
}

func addEpisodeOptionSpec(spec map[string]bool) {
	spec["--before"] = true
	spec["--after"] = true
	spec["--byte-budget"] = true
	spec["--token-budget"] = true
}

func parseAnchorType(value string) (shokasession.AnchorType, error) {
	valid := []shokasession.AnchorType{
		shokasession.AnchorUserCorrection,
		shokasession.AnchorError,
		shokasession.AnchorTestFailure,
		shokasession.AnchorRequirementRestatement,
		shokasession.AnchorScopeRevision,
		shokasession.AnchorDesignDirection,
	}
	for _, anchorType := range valid {
		if string(anchorType) == value {
			return anchorType, nil
		}
	}
	return "", fmt.Errorf("unknown transcript episode event type %q", value)
}

func episodeOptionsFromParsed(parsed parsedOptions) (shokasession.EpisodeOptions, error) {
	options := shokasession.EpisodeOptions{}
	for flag, target := range map[string]*int{
		"--before": &options.Before, "--after": &options.After,
		"--byte-budget": &options.ByteBudget, "--token-budget": &options.TokenBudget,
	} {
		value := parsed.values[flag]
		if value == "" {
			continue
		}
		parsedValue, err := strconv.Atoi(value)
		if err != nil || parsedValue < 0 {
			return options, fmt.Errorf("invalid %s: %s", flag, value)
		}
		*target = parsedValue
	}
	return options, nil
}

func cmdSessionSync(env shokasession.Environment, args []string) error {
	parsed, err := parseCommandOptions(args, map[string]bool{"--agent": true, "--dry-run": false, "--json": false})
	if err != nil {
		return err
	}
	if len(parsed.positionals) != 0 {
		return fmt.Errorf("session sync does not accept positional arguments")
	}
	var agents []shokasession.Agent
	if value := parsed.values["--agent"]; value != "" && value != "all" {
		agent, err := shokasession.ParseAgent(value)
		if err != nil {
			return err
		}
		agents = []shokasession.Agent{agent}
	}
	stats, err := shokasession.Sync(context.Background(), shokasession.SyncOptions{
		Environment: env,
		Agents:      agents,
		DryRun:      parsed.booleans["--dry-run"],
	})
	if err != nil {
		return err
	}
	if parsed.booleans["--json"] {
		if err := encodeJSON(stats); err != nil {
			return err
		}
	} else {
		fmt.Printf("sources=%d read=%d unchanged=%d rebuilt=%d missing=%d events_added=%d malformed=%d reasoning_skipped=%d\n",
			stats.SourcesFound, stats.SourcesRead, stats.SourcesUnchanged, stats.SourcesRebuilt,
			stats.SourcesMissing, stats.EventsAdded, stats.Malformed, stats.SkippedReasoning)
		for _, sourceErr := range stats.Errors {
			fmt.Fprintf(os.Stderr, "warning: %s %s: %s\n", sourceErr.Agent, sourceErr.Path, sourceErr.Error)
		}
	}
	if len(stats.Errors) > 0 {
		return fmt.Errorf("session sync completed with %d source errors", len(stats.Errors))
	}
	return nil
}

func cmdSessionList(env shokasession.Environment, args []string) error {
	parsed, err := parseCommandOptions(args, filterOptionSpec(true))
	if err != nil {
		return err
	}
	if len(parsed.positionals) != 0 {
		return fmt.Errorf("session list does not accept positional arguments")
	}
	filter, err := filterFromOptions(parsed)
	if err != nil {
		return err
	}
	sessions, err := shokasession.ListSessions(context.Background(), env, filter)
	if err != nil {
		return err
	}
	if parsed.booleans["--json"] {
		return encodeJSON(sessions)
	}
	for _, session := range sessions {
		fmt.Printf("%s  %s  %s  events=%d  %s\n", session.SessionUID, session.Status, session.EndedAt, session.EventCount, session.Repository)
	}
	return nil
}

func cmdSessionShow(env shokasession.Environment, args []string) error {
	parsed, err := parseCommandOptions(args, map[string]bool{"--json": false})
	if err != nil {
		return err
	}
	if len(parsed.positionals) != 1 {
		return fmt.Errorf("usage: shoka session show SESSION_ID [--json]")
	}
	detail, err := shokasession.ShowSession(context.Background(), env, parsed.positionals[0])
	if err != nil {
		return err
	}
	if parsed.booleans["--json"] {
		return encodeJSON(detail)
	}
	fmt.Printf("session: %s\nagent: %s\nrepository: %s\nstatus: %s\n", detail.SessionUID, detail.Agent, detail.Repository, detail.Status)
	for _, event := range detail.Events {
		text := event.Text
		if event.Command != "" {
			text = event.Command
		}
		fmt.Printf("%s  %-16s %s:%d  %s\n", event.Timestamp, event.Kind, event.Source.Path, event.Source.Line, oneLine(text))
	}
	return nil
}

func cmdSessionSearch(env shokasession.Environment, args []string) error {
	parsed, err := parseCommandOptions(args, filterOptionSpec(false))
	if err != nil {
		return err
	}
	if len(parsed.positionals) == 0 {
		return fmt.Errorf("session search requires a QUERY")
	}
	filter, err := filterFromOptions(parsed)
	if err != nil {
		return err
	}
	hits, err := shokasession.SearchSessions(context.Background(), env, strings.Join(parsed.positionals, " "), filter)
	if err != nil {
		return err
	}
	if parsed.booleans["--json"] {
		return encodeJSON(hits)
	}
	for _, hit := range hits {
		text := hit.Text
		if hit.Command != "" {
			text = hit.Command
		}
		fmt.Printf("%s  %s:%d  %-16s score=%.4f\n  %s\n", hit.SessionUID, hit.Source.Path, hit.Source.Line, hit.Kind, hit.Score, oneLine(text))
	}
	return nil
}

func cmdSessionExport(env shokasession.Environment, args []string) error {
	parsed, err := parseCommandOptions(args, map[string]bool{"--output": true})
	if err != nil {
		return err
	}
	if len(parsed.positionals) != 1 || parsed.values["--output"] == "" {
		return fmt.Errorf("usage: shoka session export SESSION_ID --output PATH")
	}
	if err := shokasession.WriteSessionEvidence(context.Background(), env, parsed.positionals[0], parsed.values["--output"]); err != nil {
		return err
	}
	fmt.Printf("exported evidence: %s\n", parsed.values["--output"])
	return nil
}

func cmdSessionReindex(env shokasession.Environment, args []string) error {
	parsed, err := parseCommandOptions(args, map[string]bool{"--json": false})
	if err != nil {
		return err
	}
	if len(parsed.positionals) != 0 {
		return fmt.Errorf("session reindex does not accept positional arguments")
	}
	stats, err := shokasession.Reindex(context.Background(), env)
	if err != nil {
		return err
	}
	if parsed.booleans["--json"] {
		return encodeJSON(stats)
	}
	fmt.Printf("reindexed sources=%d events=%d\n", stats.SourcesRead, stats.EventsAdded)
	return nil
}

func filterOptionSpec(includeSubagents bool) map[string]bool {
	spec := map[string]bool{
		"--agent":           true,
		"--session":         true,
		"--repository":      true,
		"--project-id":      true,
		"--workspace-id":    true,
		"--task-lineage-id": true,
		"--file":            true,
		"--event-type":      true,
		"--from":            true,
		"--to":              true,
		"--top":             true,
		"--json":            false,
	}
	if includeSubagents {
		spec["--include-subagents"] = false
	}
	return spec
}

func filterFromOptions(parsed parsedOptions) (shokasession.Filter, error) {
	var filter shokasession.Filter
	if value := parsed.values["--agent"]; value != "" {
		agent, err := shokasession.ParseAgent(value)
		if err != nil {
			return filter, err
		}
		filter.Agent = agent
	}
	filter.Session = parsed.values["--session"]
	filter.Repository = parsed.values["--repository"]
	filter.ProjectID = parsed.values["--project-id"]
	filter.WorkspaceID = parsed.values["--workspace-id"]
	filter.TaskLineageID = parsed.values["--task-lineage-id"]
	filter.File = parsed.values["--file"]
	if value := parsed.values["--event-type"]; value != "" {
		kind, err := parseEventKind(value)
		if err != nil {
			return filter, err
		}
		filter.Kind = kind
	}
	if value := parsed.values["--from"]; value != "" {
		parsedTime, err := parseCLITime(value, false)
		if err != nil {
			return filter, fmt.Errorf("invalid --from: %w", err)
		}
		filter.From = parsedTime
	}
	if value := parsed.values["--to"]; value != "" {
		parsedTime, err := parseCLITime(value, true)
		if err != nil {
			return filter, fmt.Errorf("invalid --to: %w", err)
		}
		filter.To = parsedTime
	}
	if value := parsed.values["--top"]; value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit <= 0 {
			return filter, fmt.Errorf("invalid --top: %s", value)
		}
		filter.Limit = limit
	}
	filter.IncludeSubagents = parsed.booleans["--include-subagents"]
	return filter, nil
}

func parseCommandOptions(args []string, spec map[string]bool) (parsedOptions, error) {
	parsed := parsedOptions{values: map[string]string{}, booleans: map[string]bool{}}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		requiresValue, known := spec[arg]
		if strings.HasPrefix(arg, "--") {
			if !known {
				return parsedOptions{}, fmt.Errorf("unknown flag: %s", arg)
			}
			if requiresValue {
				index++
				if index >= len(args) {
					return parsedOptions{}, fmt.Errorf("%s requires a value", arg)
				}
				parsed.values[arg] = args[index]
			} else {
				parsed.booleans[arg] = true
			}
			continue
		}
		parsed.positionals = append(parsed.positionals, arg)
	}
	return parsed, nil
}

func parseEventKind(value string) (shokasession.EventKind, error) {
	valid := []shokasession.EventKind{
		shokasession.KindMessage, shokasession.KindCommand, shokasession.KindCommandOutput,
		shokasession.KindTest, shokasession.KindTestFailure, shokasession.KindFileOperation,
		shokasession.KindError, shokasession.KindUserCorrection, shokasession.KindTool, shokasession.KindStatus,
	}
	for _, kind := range valid {
		if string(kind) == value {
			return kind, nil
		}
	}
	return "", fmt.Errorf("unknown event type %q", value)
}

func parseCLITime(value string, endOfDay bool) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC(), nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, fmt.Errorf("expected RFC3339 or YYYY-MM-DD")
	}
	if endOfDay {
		parsed = parsed.Add(24*time.Hour - time.Nanosecond)
	}
	return parsed.UTC(), nil
}

func encodeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func oneLine(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len([]rune(value)) <= 200 {
		return value
	}
	return string([]rune(value)[:200]) + "…"
}
