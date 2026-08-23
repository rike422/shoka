package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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

func cmdSession(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: shoka session sync|list|show|search|export|reindex")
	}
	switch args[0] {
	case "sync":
		return cmdSessionSync(args[1:])
	case "list":
		return cmdSessionList(args[1:])
	case "show":
		return cmdSessionShow(args[1:])
	case "search":
		return cmdSessionSearch(args[1:])
	case "export":
		return cmdSessionExport(args[1:])
	case "reindex":
		return cmdSessionReindex(args[1:])
	default:
		return fmt.Errorf("unknown session subcommand: %s", args[0])
	}
}

func cmdTranscript(args []string) error {
	if len(args) < 2 || args[0] != "episode" {
		return fmt.Errorf("usage: shoka transcript episode list|search|export")
	}
	args = args[1:]
	switch args[0] {
	case "list":
		return cmdTranscriptEpisodeList(args[1:])
	case "search":
		return cmdTranscriptEpisodeSearch(args[1:])
	case "export":
		return cmdTranscriptEpisodeExport(args[1:])
	default:
		return fmt.Errorf("unknown transcript episode subcommand: %s", args[0])
	}
}

func cmdTranscriptEpisodeList(args []string) error {
	spec := filterOptionSpec(false)
	addEpisodeOptionSpec(spec)
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
	options, err := episodeOptionsFromParsed(parsed)
	if err != nil {
		return err
	}
	env, err := shokasession.CurrentEnvironment()
	if err != nil {
		return err
	}
	episodes, err := shokasession.ListTranscriptEpisodes(context.Background(), env, filter, options)
	if err != nil {
		return err
	}
	if parsed.booleans["--json"] {
		return encodeJSON(episodes)
	}
	for _, episode := range episodes {
		fmt.Printf("%s  %s  trigger=%s event=%d status=%s\n", episode.Trigger.Timestamp, episode.EpisodeID, episode.Trigger.Type, episode.Trigger.EventID, episode.TaskStatus)
	}
	return nil
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

func cmdTranscriptEpisodeSearch(args []string) error {
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
	env, err := shokasession.CurrentEnvironment()
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

func cmdTranscriptEpisodeExport(args []string) error {
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
	env, err := shokasession.CurrentEnvironment()
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

func cmdSessionSync(args []string) error {
	parsed, err := parseCommandOptions(args, map[string]bool{"--agent": true, "--dry-run": false, "--json": false})
	if err != nil {
		return err
	}
	if len(parsed.positionals) != 0 {
		return fmt.Errorf("session sync does not accept positional arguments")
	}
	env, err := shokasession.CurrentEnvironment()
	if err != nil {
		return err
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

func cmdSessionList(args []string) error {
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
	env, err := shokasession.CurrentEnvironment()
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

func cmdSessionShow(args []string) error {
	parsed, err := parseCommandOptions(args, map[string]bool{"--json": false})
	if err != nil {
		return err
	}
	if len(parsed.positionals) != 1 {
		return fmt.Errorf("usage: shoka session show SESSION_ID [--json]")
	}
	env, err := shokasession.CurrentEnvironment()
	if err != nil {
		return err
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

func cmdSessionSearch(args []string) error {
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
	env, err := shokasession.CurrentEnvironment()
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

func cmdSessionExport(args []string) error {
	parsed, err := parseCommandOptions(args, map[string]bool{"--output": true})
	if err != nil {
		return err
	}
	if len(parsed.positionals) != 1 || parsed.values["--output"] == "" {
		return fmt.Errorf("usage: shoka session export SESSION_ID --output PATH")
	}
	env, err := shokasession.CurrentEnvironment()
	if err != nil {
		return err
	}
	if err := shokasession.WriteSessionEvidence(context.Background(), env, parsed.positionals[0], parsed.values["--output"]); err != nil {
		return err
	}
	fmt.Printf("exported evidence: %s\n", parsed.values["--output"])
	return nil
}

func cmdSessionReindex(args []string) error {
	parsed, err := parseCommandOptions(args, map[string]bool{"--json": false})
	if err != nil {
		return err
	}
	if len(parsed.positionals) != 0 {
		return fmt.Errorf("session reindex does not accept positional arguments")
	}
	env, err := shokasession.CurrentEnvironment()
	if err != nil {
		return err
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
