package session

import (
	"context"
	"fmt"
	"os"
	"time"
)

// SyncOptions configures one collection pass.
type SyncOptions struct {
	Environment Environment
	Agents      []Agent
	DryRun      bool
}

// SourceError reports one source without aborting other adapters or files.
type SourceError struct {
	Agent Agent  `json:"agent"`
	Path  string `json:"path"`
	Error string `json:"error"`
}

// SyncStats summarizes one collection pass.
type SyncStats struct {
	SourcesFound     int           `json:"sources_found"`
	SourcesRead      int           `json:"sources_read"`
	SourcesUnchanged int           `json:"sources_unchanged"`
	SourcesRebuilt   int           `json:"sources_rebuilt"`
	SourcesMissing   int           `json:"sources_missing"`
	EventsAdded      int           `json:"events_added"`
	Malformed        int           `json:"malformed"`
	SkippedNoise     int           `json:"skipped_noise"`
	SkippedUnknown   int           `json:"skipped_unknown"`
	SkippedReasoning int           `json:"skipped_reasoning"`
	Errors           []SourceError `json:"errors,omitempty"`
}

// Sync discovers and transactionally updates selected agent sources.
func Sync(ctx context.Context, options SyncOptions) (SyncStats, error) {
	if err := validateMutationEnvironment(options.Environment); err != nil {
		return SyncStats{}, err
	}
	agents := options.Agents
	if len(agents) == 0 {
		agents = AllAgents
	}
	if options.DryRun {
		return syncDryRun(ctx, options.Environment, agents)
	}
	release, err := acquireStateLock(options.Environment)
	if err != nil {
		return SyncStats{}, err
	}
	defer release()
	options.Agents = agents
	return syncUnlocked(ctx, options)
}

func syncUnlocked(ctx context.Context, options SyncOptions) (SyncStats, error) {
	agents := options.Agents
	store, err := OpenStore(options.Environment)
	if err != nil {
		return SyncStats{}, err
	}
	defer func() { _ = store.Close() }()
	var stats SyncStats
	for _, agent := range agents {
		adapter := AdapterFor(agent)
		if adapter == nil {
			return stats, fmt.Errorf("no adapter for %s", agent)
		}
		sources, err := adapter.Discover(ctx, options.Environment)
		if err != nil {
			stats.Errors = append(stats.Errors, SourceError{Agent: agent, Error: err.Error()})
			continue
		}
		stats.SourcesFound += len(sources)
		known, err := store.sourcesForAgent(ctx, agent)
		if err != nil {
			return stats, err
		}
		seen := map[string]struct{}{}
		for _, source := range sources {
			seen[source.LogicalID] = struct{}{}
			stored := known[source.LogicalID]
			plan, err := adapter.Prepare(ctx, source, stored.Checkpoint)
			if err != nil {
				stats.Errors = append(stats.Errors, SourceError{Agent: agent, Path: source.Path, Error: err.Error()})
				if markErr := store.markSourceError(ctx, source, err); markErr != nil {
					return stats, markErr
				}
				continue
			}
			if plan.Result.Unchanged {
				stats.SourcesUnchanged++
				if err := store.markSourceOK(ctx, source); err != nil {
					return stats, err
				}
				continue
			}
			if err := store.applySource(ctx, adapter, plan, &stats); err != nil {
				stats.Errors = append(stats.Errors, SourceError{Agent: agent, Path: source.Path, Error: err.Error()})
				if markErr := store.markSourceError(ctx, source, err); markErr != nil {
					return stats, markErr
				}
			}
		}
		for logicalID, source := range known {
			if _, ok := seen[logicalID]; ok {
				continue
			}
			if source.State != "missing" {
				stats.SourcesMissing++
				if _, err := store.db.ExecContext(ctx, `UPDATE sources SET state='missing',last_error='source not found' WHERE id=?`, source.ID); err != nil {
					return stats, err
				}
			}
		}
	}
	return stats, nil
}

// Reindex deletes the derived session database and rebuilds it from raw agent
// sources. Discovery is completed before deletion so an unavailable source
// layout cannot erase the current index.
func Reindex(ctx context.Context, env Environment) (SyncStats, error) {
	if err := validateMutationEnvironment(env); err != nil {
		return SyncStats{}, err
	}
	release, err := acquireStateLock(env)
	if err != nil {
		return SyncStats{}, err
	}
	defer release()
	found := 0
	for _, agent := range AllAgents {
		adapter := AdapterFor(agent)
		if adapter == nil {
			return SyncStats{}, fmt.Errorf("no adapter for %s", agent)
		}
		sources, err := adapter.Discover(ctx, env)
		if err != nil {
			return SyncStats{}, fmt.Errorf("reindex preflight %s: %w", agent, err)
		}
		found += len(sources)
	}
	if found == 0 {
		return SyncStats{}, fmt.Errorf("reindex found no raw session sources; existing index was preserved")
	}
	path, err := StateDBPath(env)
	if err != nil {
		return SyncStats{}, err
	}
	for _, target := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return SyncStats{}, fmt.Errorf("remove derived session state %s: %w", target, err)
		}
	}
	stats, err := syncUnlocked(ctx, SyncOptions{Environment: env, Agents: AllAgents})
	if err != nil {
		return stats, err
	}
	if len(stats.Errors) > 0 {
		return stats, fmt.Errorf("reindex completed with %d source errors; rerun after correcting them", len(stats.Errors))
	}
	return stats, nil
}

func validateMutationEnvironment(env Environment) error {
	if env.Home == "" {
		return fmt.Errorf("session environment requires an explicit home directory")
	}
	if env.StateDir == "" {
		return fmt.Errorf("session environment requires an explicit state directory")
	}
	return nil
}

func syncDryRun(ctx context.Context, env Environment, agents []Agent) (SyncStats, error) {
	var stats SyncStats
	for _, agent := range agents {
		adapter := AdapterFor(agent)
		if adapter == nil {
			return stats, fmt.Errorf("no adapter for %s", agent)
		}
		sources, err := adapter.Discover(ctx, env)
		if err != nil {
			stats.Errors = append(stats.Errors, SourceError{Agent: agent, Error: err.Error()})
			continue
		}
		stats.SourcesFound += len(sources)
		for _, source := range sources {
			plan, err := adapter.Prepare(ctx, source, Checkpoint{})
			if err != nil {
				stats.Errors = append(stats.Errors, SourceError{Agent: agent, Path: source.Path, Error: err.Error()})
				continue
			}
			result, err := adapter.Read(ctx, plan, func(record Record) error {
				record = NormalizeRecord(record)
				if retainRecord(record) {
					stats.EventsAdded++
				}
				return nil
			})
			if err != nil {
				stats.Errors = append(stats.Errors, SourceError{Agent: agent, Path: source.Path, Error: err.Error()})
				continue
			}
			stats.SourcesRead++
			mergeReadStats(&stats, result)
		}
	}
	return stats, nil
}

func (s *Store) applySource(ctx context.Context, adapter Adapter, plan ReadPlan, stats *SyncStats) error {
	plan.Source = enrichSourceIdentity(plan.Source)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	sourceID, err := ensureSource(tx, plan.Source)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	affected := map[int64]struct{}{}
	rebuilt := false
	if plan.Result.Rebuild {
		removed, err := deleteSourceEvents(tx, sourceID)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		for sessionID := range removed {
			affected[sessionID] = struct{}{}
		}
		rebuilt = true
	}
	eventsAdded := 0
	var inserted []insertedEvent
	fallback := time.Unix(0, plan.Result.MtimeNS).UTC()
	result, err := adapter.Read(ctx, plan, func(record Record) error {
		record = NormalizeRecord(record)
		if !retainRecord(record) {
			return nil
		}
		if record.Timestamp.IsZero() {
			record.Timestamp = plan.Source.StartedAt
		}
		if record.Timestamp.IsZero() {
			record.Timestamp = fallback
		}
		sessionID, err := ensureSession(tx, plan.Source, record, fallback)
		if err != nil {
			return err
		}
		if err := classifyLinkedToolResult(tx, sessionID, &record); err != nil {
			return err
		}
		affected[sessionID] = struct{}{}
		event, err := insertEvent(tx, sourceID, sessionID, plan.Result.ContentHash, record)
		if err != nil {
			return err
		}
		if event.Added {
			eventsAdded++
			inserted = append(inserted, event)
		}
		return nil
	})
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if result.Rebuild && !plan.Result.Rebuild {
		// A streaming adapter may only discover that an existing logical source
		// changed after hashing its snapshot. Roll back the tentative pass and
		// replay it through the normal source-rebuild path.
		_ = tx.Rollback()
		plan.Result.Rebuild = true
		return s.applySource(ctx, adapter, plan, stats)
	}
	// Some adapters (notably a logical SQLite snapshot) can only calculate the
	// acquisition hash while streaming. Fill only newly inserted rows so older
	// append-era evidence retains the hash observed when it was collected.
	if result.ContentHash != "" {
		if _, err := tx.Exec(`UPDATE events SET source_hash=? WHERE source_id=? AND source_hash=''`, result.ContentHash, sourceID); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if plan.Result.Rebuild {
		for sessionID := range affected {
			if err := rebuildSessionDerived(tx, sessionID); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
	} else {
		for _, event := range inserted {
			if err := deriveInsertedEvent(tx, event); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
		for sessionID := range affected {
			if err := updateSessionSummary(tx, sessionID); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
	}
	if err := deleteOrphanSessions(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	_, err = tx.Exec(`
		UPDATE sources SET
			kind=?,path=?,native_session_id=?,external_version=?,size=?,mtime_ns=?,
			cursor_offset=?,cursor_ordinal=?,content_hash=?,hash_state=?,guard_hash=?,file_id=?,state='ok',
			malformed_count=malformed_count+?,last_error='',last_synced_at=?
		WHERE id=?`,
		plan.Source.Kind, plan.Source.Path, plan.Source.NativeSessionID, result.ExternalVersion,
		result.Size, result.MtimeNS, result.CursorOffset, result.CursorOrdinal, result.ContentHash,
		nonNilBytes(result.HashState), result.GuardHash, result.FileID,
		result.Malformed, time.Now().UTC().UnixMilli(), sourceID)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if rebuilt {
		stats.SourcesRebuilt++
	}
	stats.EventsAdded += eventsAdded
	stats.SourcesRead++
	mergeReadStats(stats, result)
	return nil
}

func nonNilBytes(value []byte) []byte {
	if value == nil {
		return []byte{}
	}
	return value
}

func (s *Store) markSourceError(ctx context.Context, source Source, sourceErr error) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sources(agent,kind,logical_id,path,native_session_id,state,last_error,last_synced_at)
		VALUES(?,?,?,?,?,'error',?,?)
		ON CONFLICT(agent,logical_id) DO UPDATE SET
			path=excluded.path,state='error',last_error=excluded.last_error,last_synced_at=excluded.last_synced_at`,
		source.Agent, source.Kind, source.LogicalID, source.Path, source.NativeSessionID,
		sourceErr.Error(), time.Now().UTC().UnixMilli())
	return err
}

func (s *Store) markSourceOK(ctx context.Context, source Source) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE sources SET state='ok',last_error='',path=?
		WHERE agent=? AND logical_id=? AND (state<>'ok' OR last_error<>'' OR path<>?)`,
		source.Path, source.Agent, source.LogicalID, source.Path)
	return err
}

func retainRecord(record Record) bool {
	return record.Text != "" || record.Command != "" || len(record.Paths) > 0 || record.Kind == KindStatus
}

func mergeReadStats(stats *SyncStats, result ReadResult) {
	stats.Malformed += result.Malformed
	stats.SkippedNoise += result.SkippedNoise
	stats.SkippedUnknown += result.SkippedUnknown
	stats.SkippedReasoning += result.SkippedReasoning
}
