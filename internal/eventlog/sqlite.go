package eventlog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // Pure-Go SQLite driver
)

const defaultQueryLimit = 100
const maxQueryLimit = 1000

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS event_logs (
	id              TEXT PRIMARY KEY,
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
	currency        TEXT NOT NULL DEFAULT '',
	months          INTEGER NOT NULL DEFAULT 0,
	count           INTEGER NOT NULL DEFAULT 0,
	passed_filters  INTEGER NOT NULL DEFAULT 0,
	spoken          INTEGER NOT NULL DEFAULT 0,
	outcome         TEXT NOT NULL,
	outcome_detail  TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_event_logs_created_at ON event_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_event_logs_plat_chan_time ON event_logs(platform, channel, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_event_logs_user_id_time ON event_logs(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_event_logs_user_name_time ON event_logs(user_name, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_event_logs_outcome_time ON event_logs(outcome, created_at DESC);
`

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

	if dbPath != ":memory:" && !strings.HasPrefix(dbPath, "file::memory:") {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
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

	if _, err := db.Exec(sqliteSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize sqlite event_logs schema: %w", err)
	}

	// Idempotent migration for existing event_logs tables created before emotes_json was added.
	_, _ = db.Exec(`ALTER TABLE event_logs ADD COLUMN emotes_json TEXT NOT NULL DEFAULT '[]';`)

	return &SQLiteBackend{
		db:     db,
		dbPath: dbPath,
	}, nil
}

// Name returns the backend identifier.
func (s *SQLiteBackend) Name() string {
	return "sqlite"
}

// Path returns the active database file path.
func (s *SQLiteBackend) Path() string {
	return s.dbPath
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
	id, created_at, updated_at, platform, channel, kind, is_paid_event,
	user_id, user_name, display_name, roles_json,
	raw_text, emotes_json, spoken_text, voice_id, speed,
	amount, currency, months, count,
	passed_filters, spoken, outcome, outcome_detail
) VALUES (
	?, ?, ?, ?, ?, ?, ?,
	?, ?, ?, ?,
	?, ?, ?, ?, ?,
	?, ?, ?, ?,
	?, ?, ?, ?
)
ON CONFLICT(id) DO UPDATE SET
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
		entry.Currency,
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

// Query searches event_logs with date range, platform, channel, user, text, outcome, and dynamic retention filters.
func (s *SQLiteBackend) Query(ctx context.Context, filter QueryFilter) (*QueryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil, errors.New("sqlite backend is closed")
	}

	var whereClauses []string
	var args []any

	// 1. Dynamic created_at retention enforcement on read
	chatCutoff, eventCutoff := filter.Retention.Cutoffs()
	switch {
	case !chatCutoff.IsZero() && !eventCutoff.IsZero():
		whereClauses = append(whereClauses, "((is_paid_event = 0 AND created_at >= ?) OR (is_paid_event = 1 AND created_at >= ?))")
		args = append(args, chatCutoff.UnixMilli(), eventCutoff.UnixMilli())
	case !chatCutoff.IsZero():
		whereClauses = append(whereClauses, "(is_paid_event = 1 OR created_at >= ?)")
		args = append(args, chatCutoff.UnixMilli())
	case !eventCutoff.IsZero():
		whereClauses = append(whereClauses, "(is_paid_event = 0 OR created_at >= ?)")
		args = append(args, eventCutoff.UnixMilli())
	}

	// 2. Date range filter
	if filter.From != nil && !filter.From.IsZero() {
		whereClauses = append(whereClauses, "created_at >= ?")
		args = append(args, filter.From.UTC().UnixMilli())
	}
	if filter.To != nil && !filter.To.IsZero() {
		whereClauses = append(whereClauses, "created_at <= ?")
		args = append(args, filter.To.UTC().UnixMilli())
	}

	// 3. Platform & Channel filters
	if plat := strings.ToLower(strings.TrimSpace(filter.Platform)); plat != "" && plat != "all" {
		whereClauses = append(whereClauses, "platform = ?")
		args = append(args, plat)
	}
	if ch := strings.TrimSpace(filter.Channel); ch != "" && !strings.EqualFold(ch, "all") {
		whereClauses = append(whereClauses, "LOWER(channel) = LOWER(?)")
		args = append(args, ch)
	}

	// 3b. Event Kind filter
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
		whereClauses = append(whereClauses, "kind IN ('sub', 'resub', 'gift_sub', 'membership', 'gift_membership')")
	case "cheer":
		whereClauses = append(whereClauses, "kind IN ('cheer', 'gift', 'superchat', 'supersticker')")
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

	// 4. User / UserID filter
	if uq := strings.TrimSpace(filter.UserQuery); uq != "" {
		likePat := "%" + strings.ToLower(uq) + "%"
		whereClauses = append(whereClauses, "(LOWER(user_id) LIKE ? OR LOWER(user_name) LIKE ? OR LOWER(display_name) LIKE ?)")
		args = append(args, likePat, likePat, likePat)
	}

	// 5. Message text substring filter
	if tq := strings.TrimSpace(filter.TextQuery); tq != "" {
		likePat := "%" + strings.ToLower(tq) + "%"
		whereClauses = append(whereClauses, "(LOWER(raw_text) LIKE ? OR LOWER(spoken_text) LIKE ?)")
		args = append(args, likePat, likePat)
	}

	// 6. Outcome / Spoken mode filter
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

	// 7. Hide Bots filter (excludes dropped_bot outcome, bot role tag, and configured known/custom/ignored bots)
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

	// Count total matching rows
	var totalCount int
	countSQL := "SELECT COUNT(*) FROM event_logs" + whereSQL
	if err := s.db.QueryRowContext(ctx, countSQL, args...).Scan(&totalCount); err != nil {
		return nil, fmt.Errorf("count event logs: %w", err)
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = defaultQueryLimit
	} else if limit > maxQueryLimit {
		limit = maxQueryLimit
	}
	offset := max(filter.Offset, 0)

	querySQL := `
SELECT
	id, created_at, updated_at, platform, channel, kind, is_paid_event,
	user_id, user_name, display_name, roles_json,
	raw_text, emotes_json, spoken_text, voice_id, speed,
	amount, currency, months, count,
	passed_filters, spoken, outcome, outcome_detail
FROM event_logs` + whereSQL + `
ORDER BY created_at DESC, id DESC
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
			&e.Currency,
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate event log rows: %w", err)
	}

	return &QueryResult{
		Entries:    entries,
		TotalCount: totalCount,
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
	if plat != "" && plat != "all" {
		rows, err = s.db.QueryContext(ctx, `SELECT DISTINCT channel FROM event_logs WHERE channel != '' AND platform = ? ORDER BY channel ASC;`, plat)
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
