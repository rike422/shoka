// Command shoka-testfixture creates an isolated development session database
// from deterministic native Codex logs. It never reads or replaces user state.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	shokasession "github.com/rike422/shoka/internal/session"
	"github.com/rike422/shoka/internal/sessionfixture"
)

type fixtureResult struct {
	Corpus   sessionfixture.Corpus  `json:"corpus"`
	Database string                 `json:"database"`
	Sync     shokasession.SyncStats `json:"sync"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("shoka-testfixture", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	destination := flags.String("output", "", "new fixture directory")
	sessions := flags.Int("sessions", 64, "number of synthetic Codex sessions")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("shoka-testfixture does not accept positional arguments")
	}
	if strings.TrimSpace(*destination) == "" {
		return fmt.Errorf("usage: shoka-testfixture --output DIR [--sessions N]")
	}
	corpus, err := sessionfixture.WriteEpisodeCorpus(*destination, *sessions)
	if err != nil {
		return err
	}
	env := shokasession.Environment{Home: corpus.Home, StateDir: corpus.StateDir}
	stats, err := shokasession.Sync(context.Background(), shokasession.SyncOptions{
		Environment: env,
		Agents:      []shokasession.Agent{shokasession.AgentCodex},
	})
	if err != nil {
		return err
	}
	database, err := shokasession.StateDBPath(env)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(fixtureResult{Corpus: corpus, Database: database, Sync: stats})
}
