package index

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3" // register sqlite3 driver

	"github.com/rike422/shoka/internal/chunk"
	"github.com/rike422/shoka/internal/gitmeta"
	"github.com/rike422/shoka/internal/limits"
	"github.com/rike422/shoka/internal/root"
	"github.com/rike422/shoka/internal/tokenize"
	"github.com/rike422/shoka/internal/walk"
)

const driverName = "sqlite3"

// Stats summarizes an index build.
type Stats struct {
	Files       int
	Chunks      int
	Added       int
	Updated     int
	Removed     int
	Unchanged   int
	FullRebuild bool
}

// Options controls Build.
type Options struct {
	Force bool // full rebuild
}

// Build creates or updates the index at root/.shoka/index.db.
func Build(projectRoot string, opt Options) (Stats, error) {
	dbPath := root.IndexDB(projectRoot)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return Stats{}, err
	}

	if !opt.Force {
		if st, ok, err := tryIncremental(projectRoot, dbPath); err != nil {
			return Stats{}, err
		} else if ok {
			warnIfNotIgnored(projectRoot)
			return st, nil
		}
	}
	st, err := fullRebuild(projectRoot, dbPath)
	if err != nil {
		return st, err
	}
	warnIfNotIgnored(projectRoot)
	return st, nil
}

func tryIncremental(projectRoot, dbPath string) (Stats, bool, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return Stats{}, false, nil
	}
	db, err := sql.Open(driverName, dbPath+"?_fk=1")
	if err != nil {
		return Stats{}, false, nil
	}
	defer db.Close()

	schema, _ := Meta(db, "schema_version")
	tokver, _ := Meta(db, "tokenizer_version")
	if schema != limits.SchemaVersion || tokver != limits.TokenizerVersion {
		return Stats{}, false, nil
	}
	if !hasTable(db, "files") {
		return Stats{}, false, nil
	}

	st, err := incremental(db, projectRoot)
	if err != nil {
		return Stats{}, false, err
	}
	return st, true, nil
}

func fullRebuild(projectRoot, dbPath string) (Stats, error) {
	var st Stats
	st.FullRebuild = true

	tmp := dbPath + ".tmp"
	cleanupTmp := func() {
		_ = os.Remove(tmp)
		_ = os.Remove(tmp + "-wal")
		_ = os.Remove(tmp + "-shm")
	}
	cleanupTmp()

	db, err := sql.Open(driverName, tmp+"?_fk=1")
	if err != nil {
		return st, err
	}
	ok := false
	defer func() {
		_ = db.Close()
		if !ok {
			cleanupTmp()
		}
	}()

	if err := initSchema(db); err != nil {
		return st, err
	}

	files, err := walk.List(projectRoot)
	if err != nil {
		return st, err
	}

	tx, err := db.Begin()
	if err != nil {
		return st, err
	}
	for _, f := range files {
		n, err := upsertFile(tx, f)
		if err != nil {
			_ = tx.Rollback()
			return st, err
		}
		st.Chunks += n
		st.Added++
	}
	st.Files = len(files)
	if err := writeMeta(tx, projectRoot); err != nil {
		_ = tx.Rollback()
		return st, err
	}
	if err := tx.Commit(); err != nil {
		return st, err
	}
	if err := db.Close(); err != nil {
		return st, err
	}
	if err := os.Rename(tmp, dbPath); err != nil {
		cleanupTmp()
		return st, fmt.Errorf("replace index: %w", err)
	}
	ok = true
	return st, nil
}

type storedFile struct {
	ID    int64
	Hash  string
	Size  int64
	Mtime int64
}

func incremental(db *sql.DB, projectRoot string) (Stats, error) {
	var st Stats
	metas, err := walk.ListMeta(projectRoot)
	if err != nil {
		return st, err
	}

	existing := map[string]storedFile{}
	rows, err := db.Query(`SELECT id, path, content_hash, size, mtime FROM files`)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var sf storedFile
		var path string
		if err := rows.Scan(&sf.ID, &path, &sf.Hash, &sf.Size, &sf.Mtime); err != nil {
			rows.Close()
			return st, err
		}
		existing[path] = sf
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}

	seen := map[string]struct{}{}
	tx, err := db.Begin()
	if err != nil {
		return st, err
	}

	for _, m := range metas {
		seen[m.RelPath] = struct{}{}
		sf, ok := existing[m.RelPath]
		f, readable, err := walk.Read(projectRoot, m)
		if err != nil {
			continue
		}
		if !readable {
			// Was indexed as text but is no longer searchable — drop stale chunks.
			if ok {
				if err := deleteFileChunks(tx, sf.ID); err != nil {
					_ = tx.Rollback()
					return st, err
				}
				if _, err := tx.Exec(`DELETE FROM files WHERE id=?`, sf.ID); err != nil {
					_ = tx.Rollback()
					return st, err
				}
				st.Removed++
			}
			continue
		}
		if ok && sf.Hash == f.Hash {
			// content identical; refresh mtime/size only
			if _, err := tx.Exec(`UPDATE files SET mtime=?, size=? WHERE id=?`, f.Mtime, f.Size, sf.ID); err != nil {
				_ = tx.Rollback()
				return st, err
			}
			st.Unchanged++
			continue
		}
		if ok {
			if err := deleteFileChunks(tx, sf.ID); err != nil {
				_ = tx.Rollback()
				return st, err
			}
			if _, err := tx.Exec(`DELETE FROM files WHERE id=?`, sf.ID); err != nil {
				_ = tx.Rollback()
				return st, err
			}
			st.Updated++
		} else {
			st.Added++
		}
		n, err := upsertFile(tx, f)
		if err != nil {
			_ = tx.Rollback()
			return st, err
		}
		st.Chunks += n
	}

	for path, sf := range existing {
		if _, ok := seen[path]; ok {
			continue
		}
		if err := deleteFileChunks(tx, sf.ID); err != nil {
			_ = tx.Rollback()
			return st, err
		}
		if _, err := tx.Exec(`DELETE FROM files WHERE id=?`, sf.ID); err != nil {
			_ = tx.Rollback()
			return st, err
		}
		st.Removed++
	}

	var fileCount, chunkCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&fileCount); err != nil {
		_ = tx.Rollback()
		return st, err
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&chunkCount); err != nil {
		_ = tx.Rollback()
		return st, err
	}
	st.Files = fileCount
	st.Chunks = chunkCount

	if err := writeMeta(tx, projectRoot); err != nil {
		_ = tx.Rollback()
		return st, err
	}
	if err := tx.Commit(); err != nil {
		return st, err
	}
	return st, nil
}

func upsertFile(tx *sql.Tx, f walk.File) (int, error) {
	res, err := tx.Exec(
		`INSERT INTO files(path, mtime, size, content_hash) VALUES(?,?,?,?)`,
		f.RelPath, f.Mtime, f.Size, f.Hash,
	)
	if err != nil {
		return 0, err
	}
	fileID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	baseTerms := tokenize.BasenameTerms(f.RelPath)
	pathTerms := tokenize.PathTerms(f.RelPath)
	n := 0
	for _, ch := range chunk.Split(f.Content) {
		cres, err := tx.Exec(
			`INSERT INTO chunks(file_id, path, start_line, end_line, raw_body) VALUES(?,?,?,?,?)`,
			fileID, f.RelPath, ch.StartLine, ch.EndLine, ch.Body,
		)
		if err != nil {
			return n, err
		}
		cid, err := cres.LastInsertId()
		if err != nil {
			return n, err
		}
		searchBody := tokenize.Text(ch.Body)
		if _, err := tx.Exec(
			`INSERT INTO chunks_fts(rowid, basename_terms, path_terms, search_body) VALUES(?,?,?,?)`,
			cid, baseTerms, pathTerms, searchBody,
		); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func deleteFileChunks(tx *sql.Tx, fileID int64) error {
	rows, err := tx.Query(`SELECT id FROM chunks WHERE file_id=?`, fileID)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.Exec(`DELETE FROM chunks_fts WHERE rowid=?`, id); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`DELETE FROM chunks WHERE file_id=?`, fileID)
	return err
}

func writeMeta(tx *sql.Tx, projectRoot string) error {
	meta := map[string]string{
		"schema_version":    limits.SchemaVersion,
		"tokenizer_version": limits.TokenizerVersion,
		"head_commit":       gitmeta.HeadCommit(projectRoot),
		"root":              projectRoot,
		"created_at":        time.Now().UTC().Format(time.RFC3339),
		"chunk_lines":       fmt.Sprintf("%d", limits.ChunkLines),
		"chunk_overlap":     fmt.Sprintf("%d", limits.ChunkOverlap),
	}
	for k, v := range meta {
		if _, err := tx.Exec(
			`INSERT INTO meta(key, value) VALUES(?, ?)
			 ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
			k, v,
		); err != nil {
			return err
		}
	}
	return nil
}

func initSchema(db *sql.DB) error {
	stmts := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE files (
			id INTEGER PRIMARY KEY,
			path TEXT NOT NULL UNIQUE,
			mtime INTEGER NOT NULL,
			size INTEGER NOT NULL,
			content_hash TEXT NOT NULL
		)`,
		`CREATE TABLE chunks (
			id INTEGER PRIMARY KEY,
			file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
			path TEXT NOT NULL,
			start_line INTEGER NOT NULL,
			end_line INTEGER NOT NULL,
			raw_body TEXT NOT NULL
		)`,
		`CREATE INDEX chunks_file_id ON chunks(file_id)`,
		`CREATE VIRTUAL TABLE chunks_fts USING fts5(
			basename_terms,
			path_terms,
			search_body,
			tokenize = 'unicode61'
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("schema: %w\nstmt: %s", err, s)
		}
	}
	return nil
}

func hasTable(db *sql.DB, name string) bool {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name,
	).Scan(&n)
	return err == nil && n > 0
}

func warnIfNotIgnored(projectRoot string) {
	gi := filepath.Join(projectRoot, ".gitignore")
	data, err := os.ReadFile(gi)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: add .shoka/ to .gitignore so the index is not committed")
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == ".shoka/" || line == ".shoka" || line == "**/.shoka/" {
			return
		}
	}
	fmt.Fprintln(os.Stderr, "warning: .shoka/ is not in .gitignore")
}

// Open opens an existing index for read.
func Open(projectRoot string) (*sql.DB, error) {
	dbPath := root.IndexDB(projectRoot)
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("index not found at %s (run: shoka index)", dbPath)
	}
	db, err := sql.Open(driverName, dbPath+"?mode=ro&_fk=1")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// Meta reads a metadata value.
func Meta(db *sql.DB, key string) (string, error) {
	var v string
	err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// EnsureFresh errors if HEAD moved since indexing.
func EnsureFresh(db *sql.DB, projectRoot string) error {
	indexed, err := Meta(db, "head_commit")
	if err != nil {
		return err
	}
	if indexed == "" {
		return nil
	}
	current := gitmeta.HeadCommit(projectRoot)
	if current == "" {
		return nil
	}
	if current != indexed {
		return fmt.Errorf("index stale: HEAD moved (%s -> %s); run: shoka index", short(indexed), short(current))
	}
	return nil
}

// Status is index health info.
type Status struct {
	Root             string `json:"root"`
	IndexPath        string `json:"index_path"`
	Exists           bool   `json:"exists"`
	SchemaVersion    string `json:"schema_version,omitempty"`
	TokenizerVersion string `json:"tokenizer_version,omitempty"`
	HeadCommit       string `json:"head_commit,omitempty"`
	IndexedHead      string `json:"indexed_head,omitempty"`
	Files            int    `json:"files,omitempty"`
	Chunks           int    `json:"chunks,omitempty"`
	Stale            bool   `json:"stale"`
	CreatedAt        string `json:"created_at,omitempty"`
}

// Inspect reports index status for a root.
func Inspect(projectRoot string) (Status, error) {
	st := Status{
		Root:      projectRoot,
		IndexPath: root.IndexDB(projectRoot),
	}
	st.HeadCommit = gitmeta.HeadCommit(projectRoot)
	if _, err := os.Stat(st.IndexPath); err != nil {
		return st, nil
	}
	st.Exists = true
	db, err := Open(projectRoot)
	if err != nil {
		return st, err
	}
	defer db.Close()
	st.SchemaVersion, _ = Meta(db, "schema_version")
	st.TokenizerVersion, _ = Meta(db, "tokenizer_version")
	st.IndexedHead, _ = Meta(db, "head_commit")
	st.CreatedAt, _ = Meta(db, "created_at")
	_ = db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&st.Files)
	_ = db.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&st.Chunks)
	if st.IndexedHead != "" && st.HeadCommit != "" && st.IndexedHead != st.HeadCommit {
		st.Stale = true
	}
	return st, nil
}

func short(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
