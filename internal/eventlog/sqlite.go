package eventlog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite" // Pure-Go SQLite driver
)

const (
	defaultQueryLimit      = 100
	maxQueryLimit          = 1000
	currentSchemaVersion   = 3
	maxDBBackups           = 5
	historicalSessionGapMs = 4 * 60 * 60 * 1000 // 4 hour inactivity gap splits historical stream sessions
)

const sqliteBaseTablesSchema = `
CREATE TABLE IF NOT EXISTS stream_sessions (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	started_at     INTEGER NOT NULL,
	ended_at       INTEGER NOT NULL DEFAULT 0,
	platforms_json TEXT NOT NULL DEFAULT '[]',
	channels_json  TEXT NOT NULL DEFAULT '[]'
);

CREATE TABLE IF NOT EXISTS event_logs (
	id              TEXT PRIMARY KEY,
	session_id      INTEGER NOT NULL DEFAULT 0,
	created_at      INTEGER NOT NULL,
	updated_at      INTEGER NOT NULL,
	platform        TEXT NOT NULL,
	channel         TEXT NOT NULL,
	kind            TEXT NOT NULL,
	is_paid_event   INTEGER NOT NULL DEFAULT 0,
	user_id         TEXT NOT NULL,
	user_name       TEXT NOT NULL,
	display_name    TEXT NOT NULL,
	roles_json      TEXT NOT NULL DEFAULT '[]',
	raw_text        TEXT NOT NULL DEFAULT '',
	emotes_json     TEXT NOT NULL DEFAULT '[]',
	spoken_text     TEXT NOT NULL DEFAULT '',
	voice_id        TEXT NOT NULL DEFAULT '',
	speed           REAL NOT NULL DEFAULT 1.0,
	amount          REAL NOT NULL DEFAULT 0,
	unit_amount     REAL NOT NULL DEFAULT 0,
	currency        TEXT NOT NULL DEFAULT '',
	item_name       TEXT NOT NULL DEFAULT '',
	sub_tier        TEXT NOT NULL DEFAULT '',
	recipient       TEXT NOT NULL DEFAULT '',
	reward_id       TEXT NOT NULL DEFAULT '',
	reward_title    TEXT NOT NULL DEFAULT '',
	months          INTEGER NOT NULL DEFAULT 0,
	count           INTEGER NOT NULL DEFAULT 0,
	passed_filters  INTEGER NOT NULL DEFAULT 0,
	spoken          INTEGER NOT NULL DEFAULT 0,
	outcome         TEXT NOT NULL,
	outcome_detail  TEXT NOT NULL DEFAULT ''
);
`

const sqliteIndexesSchema = `
CREATE INDEX IF NOT EXISTS idx_event_logs_created_at ON event_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_event_logs_paid_time ON event_logs(is_paid_event, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_event_logs_session_id ON event_logs(session_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_event_logs_kind_time ON event_logs(kind, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_event_logs_platform_time ON event_logs(LOWER(TRIM(platform)), created_at DESC);
CREATE INDEX IF NOT EXISTS idx_event_logs_plat_chan_time ON event_logs(platform, channel, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_event_logs_user_id_time ON event_logs(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_event_logs_user_name_time ON event_logs(user_name, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_event_logs_user_name_clean ON event_logs(LOWER(LTRIM(user_name, '@')), created_at DESC);
CREATE INDEX IF NOT EXISTS idx_event_logs_user_id_clean ON event_logs(LOWER(LTRIM(user_id, '@')), created_at DESC);
CREATE INDEX IF NOT EXISTS idx_event_logs_display_name_lower ON event_logs(LOWER(display_name), created_at DESC);
CREATE INDEX IF NOT EXISTS idx_event_logs_outcome_time ON event_logs(outcome, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_event_logs_alert_summary ON event_logs(created_at DESC, kind, platform, currency) WHERE kind NOT IN ('message', 'moderation') AND kind != '';
CREATE INDEX IF NOT EXISTS idx_stream_sessions_started_at ON stream_sessions(started_at DESC);
`

const sqliteFTSSchema = `
CREATE VIRTUAL TABLE IF NOT EXISTS event_logs_fts USING fts5(
	user_id,
	user_name,
	display_name,
	raw_text,
	spoken_text,
	item_name,
	reward_title,
	recipient,
	content='event_logs',
	content_rowid='rowid',
	tokenize='trigram'
);

CREATE TRIGGER IF NOT EXISTS trg_event_logs_ai AFTER INSERT ON event_logs BEGIN
	INSERT INTO event_logs_fts(rowid, user_id, user_name, display_name, raw_text, spoken_text, item_name, reward_title, recipient)
	VALUES (new.rowid, new.user_id, new.user_name, new.display_name, new.raw_text, new.spoken_text, new.item_name, new.reward_title, new.recipient);
END;

CREATE TRIGGER IF NOT EXISTS trg_event_logs_ad AFTER DELETE ON event_logs BEGIN
	INSERT INTO event_logs_fts(event_logs_fts, rowid, user_id, user_name, display_name, raw_text, spoken_text, item_name, reward_title, recipient)
	VALUES ('delete', old.rowid, old.user_id, old.user_name, old.display_name, old.raw_text, old.spoken_text, old.item_name, old.reward_title, old.recipient);
END;

CREATE TRIGGER IF NOT EXISTS trg_event_logs_au AFTER UPDATE OF user_id, user_name, display_name, raw_text, spoken_text, item_name, reward_title, recipient ON event_logs BEGIN
	INSERT INTO event_logs_fts(event_logs_fts, rowid, user_id, user_name, display_name, raw_text, spoken_text, item_name, reward_title, recipient)
	VALUES ('delete', old.rowid, old.user_id, old.user_name, old.display_name, old.raw_text, old.spoken_text, old.item_name, old.reward_title, old.recipient);
	INSERT INTO event_logs_fts(rowid, user_id, user_name, display_name, raw_text, spoken_text, item_name, reward_title, recipient)
	VALUES (new.rowid, new.user_id, new.user_name, new.display_name, new.raw_text, new.spoken_text, new.item_name, new.reward_title, new.recipient);
END;
`

type columnSpec struct {
	name string
	ddl  string
}

var requiredEventLogColumns = []columnSpec{
	{"session_id", "INTEGER NOT NULL DEFAULT 0"},
	{"emotes_json", "TEXT NOT NULL DEFAULT '[]'"},
	{"is_paid_event", "INTEGER NOT NULL DEFAULT 0"},
	{"amount", "REAL NOT NULL DEFAULT 0"},
	{"unit_amount", "REAL NOT NULL DEFAULT 0"},
	{"currency", "TEXT NOT NULL DEFAULT ''"},
	{"item_name", "TEXT NOT NULL DEFAULT ''"},
	{"sub_tier", "TEXT NOT NULL DEFAULT ''"},
	{"recipient", "TEXT NOT NULL DEFAULT ''"},
	{"reward_id", "TEXT NOT NULL DEFAULT ''"},
	{"reward_title", "TEXT NOT NULL DEFAULT ''"},
	{"months", "INTEGER NOT NULL DEFAULT 0"},
	{"count", "INTEGER NOT NULL DEFAULT 0"},
}

var requiredStreamSessionColumns = []columnSpec{
	{"platforms_json", "TEXT NOT NULL DEFAULT '[]'"},
	{"channels_json", "TEXT NOT NULL DEFAULT '[]'"},
}

// SQLiteBackend implements Backend using pure-Go SQLite (modernc.org/sqlite) with WAL mode
// and dynamic created_at-based retention pruning.
type SQLiteBackend struct {
	mu     sync.Mutex
	db     *sql.DB
	dbPath string
}

// NewSQLiteBackend opens or initializes the SQLite event log database at dbPath.
// If dbPath is empty, DefaultDBPath() is used. Use ":memory:" for an in-memory database.
func NewSQLiteBackend(dbPath string) (*SQLiteBackend, error) {
	if strings.TrimSpace(dbPath) == "" {
		p, err := DefaultDBPath()
		if err != nil {
			return nil, err
		}
		dbPath = p
	}

	cleanFile := cleanDBFilePath(dbPath)
	if !isMemoryPath(dbPath) {
		if err := os.MkdirAll(filepath.Dir(cleanFile), 0o755); err != nil {
			return nil, fmt.Errorf("create event log directory: %w", err)
		}
	}

	dsn := dbPath
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-4000)&_pragma=foreign_keys(ON)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite event log: %w", err)
	}

	// Single writer connection avoids SQLITE_BUSY contention while keeping RAM minimal (~4 MB).
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if _, err := db.Exec(`PRAGMA auto_vacuum = INCREMENTAL;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure sqlite auto_vacuum: %w", err)
	}

	if err := migrateAndRecover(db, cleanFile); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &SQLiteBackend{
		db:     db,
		dbPath: cleanFile,
	}, nil
}

func isMemoryPath(p string) bool {
	trimmed := strings.TrimSpace(p)
	return trimmed == ":memory:" || strings.HasPrefix(trimmed, "file::memory:") || strings.Contains(trimmed, "mode=memory")
}

func cleanDBFilePath(p string) string {
	if before, _, ok := strings.Cut(p, "?"); ok {
		return before
	}
	return p
}

func migrateAndRecover(db *sql.DB, cleanFile string) error {
	existingEventLogs := tableExists(db, "event_logs")
	existingFTS := tableExists(db, "event_logs_fts")
	userVersion := getUserVersion(db)

	// 1. Pre-migration safety backup for existing on-disk databases before any schema/data migration runs
	if !isMemoryPath(cleanFile) && existingEventLogs {
		if needsMigration(db, userVersion) {
			if err := createPreMigrationBackup(db, cleanFile, userVersion); err != nil {
				return fmt.Errorf("backup event log database before migration: %w", err)
			}
		}
	}

	// 2. Ensure base tables exist
	if _, err := db.Exec(sqliteBaseTablesSchema); err != nil {
		return fmt.Errorf("initialize sqlite base tables: %w", err)
	}

	// 3. Add any missing columns via PRAGMA table_info inspection BEFORE creating indexes
	if err := ensureColumns(db, "event_logs", requiredEventLogColumns); err != nil {
		return err
	}
	if err := ensureColumns(db, "stream_sessions", requiredStreamSessionColumns); err != nil {
		return err
	}

	// 4. Create base B-tree indexes now that all target columns exist
	if _, err := db.Exec(sqliteIndexesSchema); err != nil {
		return fmt.Errorf("initialize sqlite indexes: %w", err)
	}

	// 5. Run v1 data migration before attaching FTS5 triggers
	if userVersion < 1 {
		if err := runVersion1DataMigration(db); err != nil {
			return fmt.Errorf("migrate sqlite data to v1: %w", err)
		}
	}

	// 6. Create FTS5 virtual table and sync triggers, then rebuild FTS5 index when upgrading to v2
	if _, err := db.Exec(sqliteFTSSchema); err != nil {
		return fmt.Errorf("initialize sqlite fts5 schema: %w", err)
	}
	if userVersion < 2 || (existingEventLogs && !existingFTS) {
		if err := runVersion2DataMigration(db); err != nil {
			return fmt.Errorf("migrate sqlite data to v2: %w", err)
		}
	}

	// 7. Cluster historical unassigned events into stream sessions with 4-hour gap rule when upgrading to v3
	if userVersion < 3 {
		if err := runVersion3DataMigration(db); err != nil {
			return fmt.Errorf("migrate sqlite data to v3: %w", err)
		}
	}

	if userVersion < currentSchemaVersion {
		if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d;", currentSchemaVersion)); err != nil {
			return fmt.Errorf("set sqlite user_version: %w", err)
		}
	}

	// 8. Auto-close any open stream_sessions left over from a previous run/crash
	if err := recoverOpenSessions(db); err != nil {
		return fmt.Errorf("recover open stream sessions: %w", err)
	}

	return nil
}

func tableExists(db *sql.DB, tableName string) bool {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?;`, tableName).Scan(&count)
	return err == nil && count > 0
}

func getUserVersion(db *sql.DB) int {
	var ver int
	_ = db.QueryRow(`PRAGMA user_version;`).Scan(&ver)
	return ver
}

func tableColumns(db *sql.DB, tableName string) (map[string]bool, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s);", tableName))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	cols := make(map[string]bool)
	for rows.Next() {
		var (
			cid       int
			name      string
			colType   string
			notNull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		cols[strings.ToLower(strings.TrimSpace(name))] = true
	}
	return cols, rows.Err()
}

func needsMigration(db *sql.DB, userVersion int) bool {
	if userVersion < currentSchemaVersion {
		return true
	}
	if !tableExists(db, "stream_sessions") || !tableExists(db, "event_logs_fts") {
		return true
	}
	cols, err := tableColumns(db, "event_logs")
	if err != nil {
		return true
	}
	for _, spec := range requiredEventLogColumns {
		if !cols[spec.name] {
			return true
		}
	}
	sessCols, err := tableColumns(db, "stream_sessions")
	if err != nil {
		return true
	}
	for _, spec := range requiredStreamSessionColumns {
		if !sessCols[spec.name] {
			return true
		}
	}
	return false
}

func ensureColumns(db *sql.DB, tableName string, specs []columnSpec) error {
	existing, err := tableColumns(db, tableName)
	if err != nil {
		return fmt.Errorf("inspect columns of %s: %w", tableName, err)
	}
	for _, spec := range specs {
		if existing[spec.name] {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s;", tableName, spec.name, spec.ddl)
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("add column %s.%s: %w", tableName, spec.name, err)
		}
	}
	return nil
}

// createPreMigrationBackup flushes any pending WAL frames into cleanFile, copies cleanFile to a
// versioned backup file in the same directory, and rotates backups so at most maxDBBackups (5) remain.
func createPreMigrationBackup(db *sql.DB, cleanFile string, oldVersion int) error {
	info, err := os.Stat(cleanFile)
	if err != nil || info.Size() == 0 {
		return nil
	}

	// Flush WAL into the main DB file so the copied file is 100% self-contained.
	_, _ = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE);`)

	dir := filepath.Dir(cleanFile)
	base := filepath.Base(cleanFile)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if stem == "" {
		stem = base
	}

	ts := time.Now().UTC().Format("20060102-150405.000")
	ts = strings.ReplaceAll(ts, ".", "")
	backupName := fmt.Sprintf("%s.v%d.%s.bak.db", stem, oldVersion, ts)
	backupPath := filepath.Join(dir, backupName)

	// Avoid collision if called multiple times within the same millisecond in tests
	for seq := 1; ; seq++ {
		if _, statErr := os.Stat(backupPath); os.IsNotExist(statErr) {
			break
		}
		backupName = fmt.Sprintf("%s.v%d.%s-%d.bak.db", stem, oldVersion, ts, seq)
		backupPath = filepath.Join(dir, backupName)
	}

	if err := copyFileContents(cleanFile, backupPath); err != nil {
		return err
	}

	return pruneOldBackups(dir, stem, maxDBBackups)
}

func copyFileContents(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// ListBackups returns all backup database file paths for dbPath sorted oldest-first.
func ListBackups(dbPath string) ([]string, error) {
	cleanFile := cleanDBFilePath(dbPath)
	dir := filepath.Dir(cleanFile)
	base := filepath.Base(cleanFile)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		stem = base
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	prefix := stem + ".v"
	suffix := ".bak.db"
	var matches []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix) {
			matches = append(matches, filepath.Join(dir, name))
		}
	}

	sort.Strings(matches)
	return matches, nil
}

func pruneOldBackups(dir, stem string, keep int) error {
	if keep <= 0 {
		keep = maxDBBackups
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	prefix := stem + ".v"
	suffix := ".bak.db"
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix) {
			names = append(names, name)
		}
	}

	sort.Strings(names)
	if len(names) <= keep {
		return nil
	}

	toDelete := names[:len(names)-keep]
	for _, name := range toDelete {
		_ = os.Remove(filepath.Join(dir, name))
	}
	return nil
}

func runVersion1DataMigration(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// 1. Normalize legacy kind='chat' to 'message' and ensure is_paid_event is set on alert/paid rows
	if _, err := tx.Exec(`UPDATE event_logs SET kind = 'message' WHERE kind = 'chat';`); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE event_logs SET is_paid_event = 1 WHERE (kind NOT IN ('message', 'moderation') AND kind != '') OR amount > 0;`); err != nil {
		return err
	}

	// 2. Backfill item_name and unit_amount on existing rows
	if _, err := tx.Exec(`
		UPDATE event_logs
		SET item_name = CASE
			WHEN reward_title != '' THEN reward_title
			WHEN raw_text != '' AND raw_text NOT LIKE 'Sent %' THEN raw_text
			ELSE 'Gift'
		END
		WHERE kind = 'gift' AND item_name = '';
	`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		UPDATE event_logs
		SET unit_amount = CASE
			WHEN count > 1 THEN amount / CAST(count AS REAL)
			ELSE amount
		END
		WHERE unit_amount = 0 AND amount > 0;
	`); err != nil {
		return err
	}

	return tx.Commit()
}

func clusterAndAssignHistoricalSessions(tx *sql.Tx, gapMs int64) error {
	type histRow struct {
		id        string
		createdAt int64
		platform  string
		channel   string
	}
	rows, err := tx.Query(`SELECT id, created_at, platform, channel FROM event_logs WHERE session_id = 0 ORDER BY created_at ASC, id ASC;`)
	if err != nil {
		return err
	}
	var allRows []histRow
	for rows.Next() {
		var r histRow
		if err := rows.Scan(&r.id, &r.createdAt, &r.platform, &r.channel); err != nil {
			_ = rows.Close()
			return err
		}
		allRows = append(allRows, r)
	}
	_ = rows.Close()

	if len(allRows) == 0 {
		return nil
	}

	type sessionBucket struct {
		startMs   int64
		endMs     int64
		platforms []string
		channels  []string
	}
	var buckets []sessionBucket
	for _, r := range allRows {
		if len(buckets) == 0 || r.createdAt-buckets[len(buckets)-1].endMs > gapMs {
			var plats, chans []string
			if p := strings.ToLower(strings.TrimSpace(r.platform)); p != "" {
				plats = append(plats, p)
			}
			if ch := strings.TrimSpace(r.channel); ch != "" {
				chans = append(chans, ch)
			}
			buckets = append(buckets, sessionBucket{
				startMs:   r.createdAt,
				endMs:     r.createdAt,
				platforms: plats,
				channels:  chans,
			})
			continue
		}
		b := &buckets[len(buckets)-1]
		if r.createdAt > b.endMs {
			b.endMs = r.createdAt
		}
		if p := strings.ToLower(strings.TrimSpace(r.platform)); p != "" && !slices.Contains(b.platforms, p) {
			b.platforms = append(b.platforms, p)
		}
		if ch := strings.TrimSpace(r.channel); ch != "" && !slices.Contains(b.channels, ch) {
			b.channels = append(b.channels, ch)
		}
	}

	for _, b := range buckets {
		platsJSON, _ := json.Marshal(b.platforms)
		chansJSON, _ := json.Marshal(b.channels)
		res, err := tx.Exec(
			`INSERT INTO stream_sessions (started_at, ended_at, platforms_json, channels_json) VALUES (?, ?, ?, ?);`,
			b.startMs, b.endMs, string(platsJSON), string(chansJSON),
		)
		if err != nil {
			return err
		}
		sessID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`UPDATE event_logs SET session_id = ? WHERE created_at >= ? AND created_at <= ? AND session_id = 0;`,
			sessID, b.startMs, b.endMs,
		); err != nil {
			return err
		}
	}
	return nil
}

func runVersion2DataMigration(db *sql.DB) error {
	_, err := db.Exec(`INSERT INTO event_logs_fts(event_logs_fts) VALUES('rebuild');`)
	return err
}

func runVersion3DataMigration(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := clusterAndAssignHistoricalSessions(tx, historicalSessionGapMs); err != nil {
		return err
	}

	return tx.Commit()
}

// recoverOpenSessions inspects stream_sessions for any rows left open (ended_at = 0)
// from a previous run that exited before the stream ended, closing them at MAX(event_logs.created_at)
// (or started_at if no events were recorded).
func recoverOpenSessions(db *sql.DB) error {
	type openSess struct {
		id        int64
		startedAt int64
	}
	rows, err := db.Query(`SELECT id, started_at FROM stream_sessions WHERE ended_at = 0 ORDER BY id ASC;`)
	if err != nil {
		return err
	}
	var openList []openSess
	for rows.Next() {
		var os openSess
		if err := rows.Scan(&os.id, &os.startedAt); err != nil {
			_ = rows.Close()
			return err
		}
		openList = append(openList, os)
	}
	_ = rows.Close()

	for _, os := range openList {
		var maxCreated int64
		err := db.QueryRow(
			`SELECT COALESCE(MAX(created_at), 0) FROM event_logs WHERE session_id = ? OR created_at >= ?;`,
			os.id, os.startedAt,
		).Scan(&maxCreated)
		if err != nil {
			return err
		}
		endMs := max(maxCreated, os.startedAt)
		if endMs <= 0 {
			endMs = time.Now().UTC().UnixMilli()
		}
		if _, err := db.Exec(`UPDATE stream_sessions SET ended_at = ? WHERE id = ?;`, endMs, os.id); err != nil {
			return err
		}
	}
	return nil
}

// Name returns the backend identifier.
func (s *SQLiteBackend) Name() string {
	return "sqlite"
}

// Path returns the active database file path.
func (s *SQLiteBackend) Path() string {
	return s.dbPath
}

// StartSession creates a new open StreamSession row (ended_at = 0) and returns it with its assigned ID.
func (s *SQLiteBackend) StartSession(ctx context.Context, startedAt time.Time, platform, channel string) (*StreamSession, error) {
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	} else {
		startedAt = startedAt.UTC()
	}

	var plats, chans []string
	if p := strings.ToLower(strings.TrimSpace(platform)); p != "" {
		plats = append(plats, p)
	}
	if ch := strings.TrimSpace(channel); ch != "" {
		chans = append(chans, ch)
	}
	platsBytes, _ := json.Marshal(plats)
	chansBytes, _ := json.Marshal(chans)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil, errors.New("sqlite backend is closed")
	}

	res, err := s.db.ExecContext(
		ctx,
		`INSERT INTO stream_sessions (started_at, ended_at, platforms_json, channels_json) VALUES (?, 0, ?, ?);`,
		startedAt.UnixMilli(),
		string(platsBytes),
		string(chansBytes),
	)
	if err != nil {
		return nil, fmt.Errorf("insert stream session: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("stream session last insert id: %w", err)
	}

	return &StreamSession{
		ID:        id,
		StartedAt: startedAt,
		Platforms: plats,
		Channels:  chans,
	}, nil
}

// AddSessionPlatform merges a newly live platform and channel into an existing StreamSession.
func (s *SQLiteBackend) AddSessionPlatform(ctx context.Context, sessionID int64, platform, channel string) error {
	if sessionID <= 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return errors.New("sqlite backend is closed")
	}

	var platsJSON, chansJSON string
	err := s.db.QueryRowContext(ctx, `SELECT platforms_json, channels_json FROM stream_sessions WHERE id = ?;`, sessionID).Scan(&platsJSON, &chansJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}

	var plats, chans []string
	_ = json.Unmarshal([]byte(platsJSON), &plats)
	_ = json.Unmarshal([]byte(chansJSON), &chans)

	if p := strings.ToLower(strings.TrimSpace(platform)); p != "" && !slices.Contains(plats, p) {
		plats = append(plats, p)
	}
	if ch := strings.TrimSpace(channel); ch != "" && !slices.Contains(chans, ch) {
		chans = append(chans, ch)
	}

	newPlatsBytes, _ := json.Marshal(plats)
	newChansBytes, _ := json.Marshal(chans)

	_, err = s.db.ExecContext(
		ctx,
		`UPDATE stream_sessions SET platforms_json = ?, channels_json = ? WHERE id = ?;`,
		string(newPlatsBytes),
		string(newChansBytes),
		sessionID,
	)
	return err
}

// EndSession marks a StreamSession as ended at endedAt.
func (s *SQLiteBackend) EndSession(ctx context.Context, sessionID int64, endedAt time.Time) error {
	if sessionID <= 0 {
		return nil
	}
	if endedAt.IsZero() {
		endedAt = time.Now().UTC()
	} else {
		endedAt = endedAt.UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return errors.New("sqlite backend is closed")
	}

	var startedMs int64
	if err := s.db.QueryRowContext(ctx, `SELECT started_at FROM stream_sessions WHERE id = ?;`, sessionID).Scan(&startedMs); err == nil {
		endMs := max(endedAt.UnixMilli(), startedMs)
		_, err = s.db.ExecContext(ctx, `UPDATE stream_sessions SET ended_at = ? WHERE id = ?;`, endMs, sessionID)
		return err
	}

	_, err := s.db.ExecContext(ctx, `UPDATE stream_sessions SET ended_at = ? WHERE id = ?;`, endedAt.UnixMilli(), sessionID)
	return err
}

// ListSessions returns the most recent stream sessions ordered newest-first.
func (s *SQLiteBackend) ListSessions(ctx context.Context, limit int) ([]StreamSession, error) {
	if limit <= 0 {
		limit = 50
	} else if limit > 500 {
		limit = 500
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil, errors.New("sqlite backend is closed")
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, started_at, ended_at, platforms_json, channels_json
		 FROM stream_sessions
		 ORDER BY started_at DESC, id DESC
		 LIMIT ?;`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list stream sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []StreamSession
	for rows.Next() {
		var (
			sess      StreamSession
			startMs   int64
			endMs     int64
			platsJSON string
			chansJSON string
		)
		if err := rows.Scan(&sess.ID, &startMs, &endMs, &platsJSON, &chansJSON); err != nil {
			return nil, fmt.Errorf("scan stream session: %w", err)
		}
		sess.StartedAt = time.UnixMilli(startMs).UTC()
		if endMs > 0 {
			sess.EndedAt = time.UnixMilli(endMs).UTC()
		}
		if platsJSON != "" && platsJSON != "[]" {
			_ = json.Unmarshal([]byte(platsJSON), &sess.Platforms)
		}
		if chansJSON != "" && chansJSON != "[]" {
			_ = json.Unmarshal([]byte(chansJSON), &sess.Channels)
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// Append inserts or replaces an Entry in the SQLite event_logs table.
func (s *SQLiteBackend) Append(ctx context.Context, entry *Entry) error {
	if entry == nil {
		return errors.New("cannot append nil entry")
	}
	if strings.TrimSpace(entry.ID) == "" {
		return errors.New("entry ID is required")
	}

	now := time.Now().UTC()
	createdAt := entry.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := entry.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}

	rolesBytes, err := json.Marshal(entry.Roles)
	if err != nil || len(entry.Roles) == 0 {
		rolesBytes = []byte("[]")
	}

	emotesBytes, err := json.Marshal(entry.Emotes)
	if err != nil || len(entry.Emotes) == 0 {
		emotesBytes = []byte("[]")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return errors.New("sqlite backend is closed")
	}

	const q = `
INSERT INTO event_logs (
	id, session_id, created_at, updated_at, platform, channel, kind, is_paid_event,
	user_id, user_name, display_name, roles_json,
	raw_text, emotes_json, spoken_text, voice_id, speed,
	amount, unit_amount, currency, item_name, sub_tier, recipient, reward_id, reward_title,
	months, count,
	passed_filters, spoken, outcome, outcome_detail
) VALUES (
	?, ?, ?, ?, ?, ?, ?, ?,
	?, ?, ?, ?,
	?, ?, ?, ?, ?,
	?, ?, ?, ?, ?, ?, ?, ?,
	?, ?,
	?, ?, ?, ?
)
ON CONFLICT(id) DO UPDATE SET
	session_id     = CASE WHEN excluded.session_id > 0 THEN excluded.session_id ELSE event_logs.session_id END,
	updated_at     = excluded.updated_at,
	emotes_json    = CASE WHEN excluded.emotes_json != '[]' THEN excluded.emotes_json ELSE event_logs.emotes_json END,
	spoken_text    = CASE WHEN excluded.spoken_text != '' THEN excluded.spoken_text ELSE event_logs.spoken_text END,
	voice_id       = CASE WHEN excluded.voice_id != '' THEN excluded.voice_id ELSE event_logs.voice_id END,
	speed          = CASE WHEN excluded.speed > 0 THEN excluded.speed ELSE event_logs.speed END,
	passed_filters = excluded.passed_filters,
	spoken         = excluded.spoken,
	outcome        = excluded.outcome,
	outcome_detail = excluded.outcome_detail;
`

	_, err = s.db.ExecContext(
		ctx,
		q,
		entry.ID,
		entry.SessionID,
		createdAt.UTC().UnixMilli(),
		updatedAt.UTC().UnixMilli(),
		strings.ToLower(strings.TrimSpace(entry.Platform)),
		strings.TrimSpace(entry.Channel),
		strings.TrimSpace(entry.Kind),
		boolToInt(entry.IsPaidOrEvent),
		strings.TrimSpace(entry.UserID),
		strings.TrimSpace(entry.UserName),
		strings.TrimSpace(entry.DisplayName),
		string(rolesBytes),
		entry.RawText,
		string(emotesBytes),
		entry.SpokenText,
		entry.VoiceID,
		entry.Speed,
		entry.Amount,
		entry.UnitAmount,
		entry.Currency,
		entry.ItemName,
		entry.SubTier,
		entry.Recipient,
		entry.RewardID,
		entry.RewardTitle,
		entry.Months,
		entry.Count,
		boolToInt(entry.PassedFilters),
		boolToInt(entry.Spoken),
		string(entry.Outcome),
		entry.OutcomeDetail,
	)
	if err != nil {
		return fmt.Errorf("insert event log: %w", err)
	}
	return nil
}

// UpdateOutcome updates the spoken state, outcome, detail, and effective playback speed for an event.
func (s *SQLiteBackend) UpdateOutcome(ctx context.Context, id string, update OutcomeUpdate) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	updatedAt := update.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return errors.New("sqlite backend is closed")
	}

	if update.Speed > 0 {
		const q = `
UPDATE event_logs
SET spoken = ?, outcome = ?, outcome_detail = ?, speed = ?, updated_at = ?
WHERE id = ?;
`
		_, err := s.db.ExecContext(ctx, q, boolToInt(update.Spoken), string(update.Outcome), update.OutcomeDetail, update.Speed, updatedAt.UTC().UnixMilli(), id)
		return err
	}

	const q = `
UPDATE event_logs
SET spoken = ?, outcome = ?, outcome_detail = ?, updated_at = ?
WHERE id = ?;
`
	_, err := s.db.ExecContext(ctx, q, boolToInt(update.Spoken), string(update.Outcome), update.OutcomeDetail, updatedAt.UTC().UnixMilli(), id)
	return err
}

func buildWhereClause(filter QueryFilter) (string, []any) {
	var whereClauses []string
	var args []any

	// 1. Dynamic created_at retention enforcement on read
	chatCutoff, eventCutoff := filter.Retention.Cutoffs()
	switch {
	case !chatCutoff.IsZero() && !eventCutoff.IsZero():
		if filter.Retention.ChatRetentionDays == filter.Retention.EventRetentionDays {
			whereClauses = append(whereClauses, "created_at >= ?")
			args = append(args, chatCutoff.UnixMilli())
		} else {
			whereClauses = append(whereClauses, "created_at >= ?")
			args = append(args, min(chatCutoff.UnixMilli(), eventCutoff.UnixMilli()))
			whereClauses = append(whereClauses, "((is_paid_event = 0 AND created_at >= ?) OR (is_paid_event = 1 AND created_at >= ?))")
			args = append(args, chatCutoff.UnixMilli(), eventCutoff.UnixMilli())
		}
	case !chatCutoff.IsZero():
		whereClauses = append(whereClauses, "(is_paid_event = 1 OR created_at >= ?)")
		args = append(args, chatCutoff.UnixMilli())
	case !eventCutoff.IsZero():
		whereClauses = append(whereClauses, "(is_paid_event = 0 OR created_at >= ?)")
		args = append(args, eventCutoff.UnixMilli())
	}

	// 2. Stream session filter (supports single SessionID or multiple SessionIDs)
	var sessionIDs []int64
	if filter.SessionID > 0 {
		sessionIDs = append(sessionIDs, filter.SessionID)
	}
	for _, sid := range filter.SessionIDs {
		if sid > 0 && !slices.Contains(sessionIDs, sid) {
			sessionIDs = append(sessionIDs, sid)
		}
	}
	if len(sessionIDs) == 1 {
		whereClauses = append(whereClauses, "session_id = ?")
		args = append(args, sessionIDs[0])
	} else if len(sessionIDs) > 1 {
		placeholders := make([]string, len(sessionIDs))
		for i, sid := range sessionIDs {
			placeholders[i] = "?"
			args = append(args, sid)
		}
		whereClauses = append(whereClauses, fmt.Sprintf("session_id IN (%s)", strings.Join(placeholders, ",")))
	}

	// 3. Date range filter
	if filter.From != nil && !filter.From.IsZero() {
		whereClauses = append(whereClauses, "created_at >= ?")
		args = append(args, filter.From.UTC().UnixMilli())
	}
	if filter.To != nil && !filter.To.IsZero() {
		whereClauses = append(whereClauses, "created_at <= ?")
		args = append(args, filter.To.UTC().UnixMilli())
	}

	// 4. Platform & Channel filters
	if plat := strings.ToLower(strings.TrimSpace(filter.Platform)); plat != "" && plat != "all" && plat != "all platforms" {
		whereClauses = append(whereClauses, "LOWER(TRIM(platform)) = ?")
		args = append(args, plat)
	}
	if ch := strings.TrimSpace(filter.Channel); ch != "" && !strings.EqualFold(ch, "all") {
		whereClauses = append(whereClauses, "LOWER(channel) = LOWER(?)")
		args = append(args, ch)
	}

	// 5. Event Kind filter
	switch strings.ToLower(strings.TrimSpace(filter.KindFilter)) {
	case "", "all":
		// no filter
	case "chat", "message":
		whereClauses = append(whereClauses, "kind = 'message'")
	case "alerts":
		whereClauses = append(whereClauses, "kind NOT IN ('message', 'moderation')")
	case "follow":
		whereClauses = append(whereClauses, "kind = 'follow'")
	case "sub":
		whereClauses = append(whereClauses, "kind IN ('sub', 'resub', 'gift_sub', 'gift_redemption', 'sub_upgrade', 'membership', 'gift_membership')")
	case "cheer":
		whereClauses = append(whereClauses, "kind IN ('cheer', 'bits_badge_tier', 'gift', 'super_chat', 'super_sticker', 'superchat', 'supersticker', 'charity_donation')")
	case "raid":
		whereClauses = append(whereClauses, "kind = 'raid'")
	case "redemption":
		whereClauses = append(whereClauses, "kind = 'redemption'")
	case "moderation":
		whereClauses = append(whereClauses, "kind = 'moderation'")
	default:
		whereClauses = append(whereClauses, "LOWER(kind) = LOWER(?)")
		args = append(args, strings.TrimSpace(filter.KindFilter))
	}

	// 6 & 7. User / UserID and Message text substring filters (accelerated via FTS5 trigram index for >= 3 runes)
	uq := strings.TrimSpace(filter.UserQuery)
	tq := strings.TrimSpace(filter.TextQuery)
	userFTS, hasUserFTS := ftsTrigramColumnExpr("user_id user_name display_name", uq)
	textFTS, hasTextFTS := ftsTrigramColumnExpr("raw_text spoken_text item_name reward_title recipient", tq)
	switch {
	case hasUserFTS && hasTextFTS:
		whereClauses = append(whereClauses, "rowid IN (SELECT rowid FROM event_logs_fts WHERE event_logs_fts MATCH ?)")
		args = append(args, fmt.Sprintf("(%s) AND (%s)", userFTS, textFTS))
	case hasUserFTS:
		whereClauses = append(whereClauses, "rowid IN (SELECT rowid FROM event_logs_fts WHERE event_logs_fts MATCH ?)")
		args = append(args, userFTS)
	case hasTextFTS:
		whereClauses = append(whereClauses, "rowid IN (SELECT rowid FROM event_logs_fts WHERE event_logs_fts MATCH ?)")
		args = append(args, textFTS)
	}

	if uq != "" && !hasUserFTS {
		likePat := "%" + strings.ToLower(uq) + "%"
		whereClauses = append(whereClauses, "(LOWER(user_id) LIKE ? OR LOWER(user_name) LIKE ? OR LOWER(display_name) LIKE ?)")
		args = append(args, likePat, likePat, likePat)
	}

	if tq != "" && !hasTextFTS {
		likePat := "%" + strings.ToLower(tq) + "%"
		whereClauses = append(whereClauses, "(LOWER(raw_text) LIKE ? OR LOWER(spoken_text) LIKE ? OR LOWER(item_name) LIKE ? OR LOWER(reward_title) LIKE ? OR LOWER(recipient) LIKE ?)")
		args = append(args, likePat, likePat, likePat, likePat, likePat)
	}

	// 8. Outcome / Spoken mode filter
	if filter.Outcome != "" {
		whereClauses = append(whereClauses, "outcome = ?")
		args = append(args, string(filter.Outcome))
	} else {
		switch filter.OutcomeMode {
		case FilterOutcomeSpokenOnly:
			whereClauses = append(whereClauses, "spoken = 1")
		case FilterOutcomePassedFilters:
			whereClauses = append(whereClauses, "passed_filters = 1")
		case FilterOutcomeDroppedQueue:
			whereClauses = append(whereClauses, "(passed_filters = 1 AND spoken = 0 AND outcome != ?)")
			args = append(args, string(OutcomeQueued))
		case FilterOutcomeFilteredOut:
			whereClauses = append(whereClauses, "(passed_filters = 0 AND outcome != ?)")
			args = append(args, string(OutcomeModerationEvent))
		case FilterOutcomeModeration:
			whereClauses = append(whereClauses, "(outcome = ? OR outcome = ?)")
			args = append(args, string(OutcomeModerationEvent), string(OutcomeDroppedModeration))
		}
	}

	// 9. Hide Bots filter (excludes dropped_bot outcome, bot role tag, and configured known/custom/ignored bots)
	if filter.HideBots && filter.Outcome != OutcomeDroppedBot {
		whereClauses = append(whereClauses, "(outcome != ? AND LOWER(roles_json) NOT LIKE ?)")
		args = append(args, string(OutcomeDroppedBot), `%"bot"%`)

		if len(filter.ExcludedUsers) > 0 {
			var placeholders []string
			var userArgs []any
			for _, u := range filter.ExcludedUsers {
				uClean := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(u), "@"))
				if uClean != "" {
					placeholders = append(placeholders, "?")
					userArgs = append(userArgs, uClean)
				}
			}
			if len(placeholders) > 0 {
				inList := strings.Join(placeholders, ",")
				whereClauses = append(whereClauses, fmt.Sprintf("(LOWER(LTRIM(user_name, '@')) NOT IN (%s) AND LOWER(LTRIM(user_id, '@')) NOT IN (%s))", inList, inList))
				args = append(args, userArgs...)
				args = append(args, userArgs...)
			}
		}
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = " WHERE " + strings.Join(whereClauses, " AND ")
	}
	return whereSQL, args
}

// ftsTrigramColumnExpr formats an FTS5 column-filter phrase query for the trigram tokenizer
// when rawQuery contains at least 3 runes.
func ftsTrigramColumnExpr(cols, rawQuery string) (string, bool) {
	if utf8.RuneCountInString(rawQuery) < 3 {
		return "", false
	}
	escaped := strings.ReplaceAll(rawQuery, `"`, `""`)
	return fmt.Sprintf(`{%s} : "%s"`, cols, escaped), true
}

func isSummaryEmptyForKindFilter(kindFilter string) bool {
	switch strings.ToLower(strings.TrimSpace(kindFilter)) {
	case "chat", "message", "moderation":
		return true
	default:
		return false
	}
}

// Summarize computes aggregated stream event and monetization metrics across all rows matching filter.
func (s *SQLiteBackend) Summarize(ctx context.Context, filter QueryFilter) (*QuerySummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil, errors.New("sqlite backend is closed")
	}

	if isSummaryEmptyForKindFilter(filter.KindFilter) {
		return &QuerySummary{}, nil
	}

	whereSQL, args := buildWhereClause(filter)
	sum, err := s.computeSummaryLocked(ctx, whereSQL, args)
	if err != nil {
		return nil, err
	}
	return &sum, nil
}

func (s *SQLiteBackend) computeSummaryLocked(ctx context.Context, whereSQL string, args []any) (QuerySummary, error) {
	var summary QuerySummary

	extraCond := "kind NOT IN ('message', 'moderation') AND kind != ''"
	fullWhere := " WHERE " + extraCond
	if whereSQL != "" {
		fullWhere = whereSQL + " AND " + extraCond
	}

	q := `
SELECT
	LOWER(kind),
	LOWER(platform),
	UPPER(TRIM(currency)),
	COUNT(*),
	COALESCE(SUM(amount), 0),
	COALESCE(SUM(CASE WHEN count > 0 THEN count ELSE 1 END), 0)
FROM event_logs` + fullWhere + `
GROUP BY LOWER(kind), LOWER(platform), UPPER(TRIM(currency));`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return summary, fmt.Errorf("summarize event logs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			kind      string
			platform  string
			currency  string
			rowCount  int
			sumAmount float64
			sumCount  int
		)
		if err := rows.Scan(&kind, &platform, &currency, &rowCount, &sumAmount, &sumCount); err != nil {
			return summary, fmt.Errorf("scan summary row: %w", err)
		}

		accumulateSummaryRow(&summary, kind, platform, currency, rowCount, sumAmount, sumCount)
	}
	return summary, rows.Err()
}

func accumulateSummaryRow(summary *QuerySummary, kind, platform, currency string, rowCount int, sumAmount float64, sumCount int) {
	if summary != nil {
		summary.AccumulateRow(kind, platform, currency, rowCount, sumAmount, sumCount)
	}
}

func summarizeEntriesInMemory(entries []Entry) QuerySummary {
	var summary QuerySummary
	for i := range entries {
		summary.AddEntry(entries[i])
	}
	return summary
}

func hasFTSFilter(filter QueryFilter) bool {
	_, uOK := ftsTrigramColumnExpr("user_id user_name display_name", strings.TrimSpace(filter.UserQuery))
	if uOK {
		return true
	}
	_, tOK := ftsTrigramColumnExpr("raw_text spoken_text item_name reward_title recipient", strings.TrimSpace(filter.TextQuery))
	return tOK
}

// Query searches event_logs with session, date range, platform, channel, user, text, outcome, and dynamic retention filters.
func (s *SQLiteBackend) Query(ctx context.Context, filter QueryFilter) (*QueryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil, errors.New("sqlite backend is closed")
	}

	whereSQL, args := buildWhereClause(filter)

	limit := filter.Limit
	if limit <= 0 {
		limit = defaultQueryLimit
	} else if limit > maxQueryLimit {
		limit = maxQueryLimit
	}
	offset := max(filter.Offset, 0)

	// When FTS5 is active, use +created_at DESC so SQLite's query planner drives the lookup
	// from the FTS5 rowid set instead of scanning idx_event_logs_created_at across the whole table.
	orderByClause := "ORDER BY created_at DESC, id DESC"
	if hasFTSFilter(filter) {
		orderByClause = "ORDER BY +created_at DESC, id DESC"
	}

	querySQL := `
SELECT
	id, session_id, created_at, updated_at, platform, channel, kind, is_paid_event,
	user_id, user_name, display_name, roles_json,
	raw_text, emotes_json, spoken_text, voice_id, speed,
	amount, unit_amount, currency, item_name, sub_tier, recipient, reward_id, reward_title,
	months, count,
	passed_filters, spoken, outcome, outcome_detail
FROM event_logs` + whereSQL + "\n" + orderByClause + `
LIMIT ? OFFSET ?;`

	queryArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.QueryContext(ctx, querySQL, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("query event logs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	entries := make([]Entry, 0)
	for rows.Next() {
		var (
			e          Entry
			createdMs  int64
			updatedMs  int64
			isPaidInt  int
			rolesJSON  string
			emotesJSON string
			passedInt  int
			spokenInt  int
			outcomeStr string
			speedFloat float64
		)
		if err := rows.Scan(
			&e.ID,
			&e.SessionID,
			&createdMs,
			&updatedMs,
			&e.Platform,
			&e.Channel,
			&e.Kind,
			&isPaidInt,
			&e.UserID,
			&e.UserName,
			&e.DisplayName,
			&rolesJSON,
			&e.RawText,
			&emotesJSON,
			&e.SpokenText,
			&e.VoiceID,
			&speedFloat,
			&e.Amount,
			&e.UnitAmount,
			&e.Currency,
			&e.ItemName,
			&e.SubTier,
			&e.Recipient,
			&e.RewardID,
			&e.RewardTitle,
			&e.Months,
			&e.Count,
			&passedInt,
			&spokenInt,
			&outcomeStr,
			&e.OutcomeDetail,
		); err != nil {
			return nil, fmt.Errorf("scan event log row: %w", err)
		}

		e.CreatedAt = time.UnixMilli(createdMs).UTC()
		e.UpdatedAt = time.UnixMilli(updatedMs).UTC()
		e.IsPaidOrEvent = isPaidInt != 0
		e.PassedFilters = passedInt != 0
		e.Spoken = spokenInt != 0
		e.Speed = float32(speedFloat)
		e.Outcome = EventOutcome(outcomeStr)
		if rolesJSON != "" && rolesJSON != "[]" {
			_ = json.Unmarshal([]byte(rolesJSON), &e.Roles)
		}
		if emotesJSON != "" && emotesJSON != "[]" {
			_ = json.Unmarshal([]byte(emotesJSON), &e.Emotes)
		}
		entries = append(entries, e)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate event log rows: %w", err)
	}

	var totalCount int
	var summary QuerySummary

	switch {
	case offset == 0 && len(entries) < limit:
		// All matching rows fit in this single page: no extra COUNT(*) or GROUP BY SQL query needed.
		totalCount = len(entries)
		if !filter.SkipSummary && !isSummaryEmptyForKindFilter(filter.KindFilter) && totalCount > 0 {
			summary = summarizeEntriesInMemory(entries)
		}
	case offset > 0 && len(entries) > 0 && len(entries) < limit:
		// Partial final page: exact total count is known without a COUNT(*) query.
		totalCount = offset + len(entries)
		if !filter.SkipSummary && !isSummaryEmptyForKindFilter(filter.KindFilter) {
			sum, err := s.computeSummaryLocked(ctx, whereSQL, args)
			if err != nil {
				return nil, err
			}
			summary = sum
		}
	default:
		countSQL := "SELECT COUNT(*) FROM event_logs" + whereSQL
		if err := s.db.QueryRowContext(ctx, countSQL, args...).Scan(&totalCount); err != nil {
			return nil, fmt.Errorf("count event logs: %w", err)
		}
		if !filter.SkipSummary && !isSummaryEmptyForKindFilter(filter.KindFilter) && totalCount > 0 {
			sum, err := s.computeSummaryLocked(ctx, whereSQL, args)
			if err != nil {
				return nil, err
			}
			summary = sum
		}
	}

	return &QueryResult{
		Entries:    entries,
		TotalCount: totalCount,
		Summary:    summary,
	}, nil
}

// DistinctChannels returns sorted non-empty channel names stored in event_logs, optionally scoped by platform.
func (s *SQLiteBackend) DistinctChannels(ctx context.Context, platform string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil, errors.New("sqlite backend is closed")
	}

	plat := strings.ToLower(strings.TrimSpace(platform))
	var rows *sql.Rows
	var err error
	if plat != "" && plat != "all" && plat != "all platforms" {
		rows, err = s.db.QueryContext(ctx, `SELECT DISTINCT channel FROM event_logs WHERE channel != '' AND LOWER(TRIM(platform)) = ? ORDER BY channel ASC;`, plat)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT DISTINCT channel FROM event_logs WHERE channel != '' ORDER BY channel ASC;`)
	}
	if err != nil {
		return nil, fmt.Errorf("query distinct channels: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var channels []string
	for rows.Next() {
		var ch string
		if err := rows.Scan(&ch); err != nil {
			return nil, err
		}
		if ch != "" {
			channels = append(channels, ch)
		}
	}
	return channels, rows.Err()
}

// Prune deletes rows older than the dynamic created_at cutoffs defined by policy
// and reclaims free database pages via PRAGMA incremental_vacuum.
func (s *SQLiteBackend) Prune(ctx context.Context, policy RetentionPolicy) (int64, error) {
	chatCutoff, eventCutoff := policy.Cutoffs()
	if chatCutoff.IsZero() && eventCutoff.IsZero() {
		return 0, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return 0, errors.New("sqlite backend is closed")
	}

	var totalDeleted int64

	if !chatCutoff.IsZero() {
		res, err := s.db.ExecContext(ctx, `DELETE FROM event_logs WHERE is_paid_event = 0 AND created_at < ?;`, chatCutoff.UnixMilli())
		if err != nil {
			return totalDeleted, fmt.Errorf("prune expired chat logs: %w", err)
		}
		n, _ := res.RowsAffected()
		totalDeleted += n
	}

	if !eventCutoff.IsZero() {
		res, err := s.db.ExecContext(ctx, `DELETE FROM event_logs WHERE is_paid_event = 1 AND created_at < ?;`, eventCutoff.UnixMilli())
		if err != nil {
			return totalDeleted, fmt.Errorf("prune expired event logs: %w", err)
		}
		n, _ := res.RowsAffected()
		totalDeleted += n
	}

	if totalDeleted > 0 {
		_, _ = s.db.ExecContext(ctx, `PRAGMA incremental_vacuum(500);`)
	}

	return totalDeleted, nil
}

// Close closes the underlying SQLite database connection.
func (s *SQLiteBackend) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
