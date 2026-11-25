package bot

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite" // Register pure-Go SQLite driver
)

// VarEntry represents a persisted or ephemeral global bot variable.
type VarEntry struct {
	Key       string `json:"key"`
	Kind      string `json:"kind"` // "string", "int", "float", "bool"
	Value     string `json:"value"`
	UpdatedAt int64  `json:"updated_at"`
}

// UserVarEntry represents a per-user persisted bot variable.
type UserVarEntry struct {
	Platform  string `json:"platform"`
	UserID    string `json:"user_id"`
	Key       string `json:"key"`
	Kind      string `json:"kind"`
	Value     string `json:"value"`
	UpdatedAt int64  `json:"updated_at"`
}

// GroupMember represents a viewer belonging to a named bot permission group.
type GroupMember struct {
	GroupName string `json:"group_name"`
	Platform  string `json:"platform"`
	UserID    string `json:"user_id"`
	UserName  string `json:"user_name"`
}

// Store persists bot global variables, user variables, and user permission groups in SQLite,
// alongside in-memory ephemeral global variables.
type Store struct {
	mu     sync.RWMutex
	db     *sql.DB
	ephem  map[string]VarEntry
	closed bool
}

// OpenStore opens (or creates) bot_state.db (or ":memory:" for in-memory/test operation).
func OpenStore(dbPath string) (*Store, error) {
	path := strings.TrimSpace(dbPath)
	if path == "" {
		return nil, errors.New("bot store dbPath cannot be empty")
	}
	if testing.Testing() && path != ":memory:" {
		tempDir := filepath.Clean(os.TempDir())
		cleanPath := filepath.Clean(path)
		rel, err := filepath.Rel(tempDir, cleanPath)
		if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			return nil, fmt.Errorf("refusing to open bot_state db outside temp dir during unit tests: %s", path)
		}
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create bot store dir: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open bot sqlite db: %w", err)
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}

	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set busy_timeout: %w", err)
	}

	schema := `
CREATE TABLE IF NOT EXISTS global_vars (
	key TEXT PRIMARY KEY,
	kind TEXT NOT NULL DEFAULT 'string',
	value TEXT NOT NULL DEFAULT '',
	updated_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS user_vars (
	platform TEXT NOT NULL,
	user_id TEXT NOT NULL,
	key TEXT NOT NULL,
	kind TEXT NOT NULL DEFAULT 'string',
	value TEXT NOT NULL DEFAULT '',
	updated_at INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (platform, user_id, key)
);
CREATE TABLE IF NOT EXISTS user_groups (
	group_name TEXT NOT NULL,
	platform TEXT NOT NULL,
	user_id TEXT NOT NULL,
	user_name TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (group_name, platform, user_id)
);`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create bot_state schema: %w", err)
	}

	return &Store{
		db:    db,
		ephem: make(map[string]VarEntry),
	}, nil
}

// Close closes the underlying SQLite handle.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	db := s.db
	s.db = nil
	s.mu.Unlock()
	if db != nil {
		return db.Close()
	}
	return nil
}

func normalizeKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "int", "integer":
		return "int"
	case "float", "double", "number":
		return "float"
	case "bool", "boolean":
		return "bool"
	default:
		return "string"
	}
}

// GetGlobalVar retrieves a persistent or ephemeral global variable.
func (s *Store) GetGlobalVar(key string, ephemeral bool) (VarEntry, bool) {
	clean := strings.TrimSpace(key)
	if clean == "" {
		return VarEntry{}, false
	}
	if ephemeral {
		s.mu.RLock()
		v, ok := s.ephem[clean]
		s.mu.RUnlock()
		return v, ok
	}

	s.mu.RLock()
	db := s.db
	s.mu.RUnlock()
	if db == nil {
		return VarEntry{}, false
	}

	var out VarEntry
	err := db.QueryRow(`SELECT key, kind, value, updated_at FROM global_vars WHERE key = ?`, clean).
		Scan(&out.Key, &out.Kind, &out.Value, &out.UpdatedAt)
	if err != nil {
		return VarEntry{}, false
	}
	return out, true
}

// SetGlobalVar stores a persistent or ephemeral global variable.
func (s *Store) SetGlobalVar(key, kind, val string, ephemeral bool) error {
	clean := strings.TrimSpace(key)
	if clean == "" {
		return errors.New("global variable key cannot be empty")
	}
	k := normalizeKind(kind)
	now := time.Now().UTC().Unix()

	if ephemeral {
		s.mu.Lock()
		s.ephem[clean] = VarEntry{Key: clean, Kind: k, Value: val, UpdatedAt: now}
		s.mu.Unlock()
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("bot store is closed")
	}
	_, err := s.db.Exec(
		`INSERT INTO global_vars(key, kind, value, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET kind=excluded.kind, value=excluded.value, updated_at=excluded.updated_at`,
		clean, k, val, now,
	)
	return err
}

// DeleteGlobalVar removes a persistent or ephemeral global variable.
func (s *Store) DeleteGlobalVar(key string, ephemeral bool) error {
	clean := strings.TrimSpace(key)
	if clean == "" {
		return nil
	}
	if ephemeral {
		s.mu.Lock()
		delete(s.ephem, clean)
		s.mu.Unlock()
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM global_vars WHERE key = ?`, clean)
	return err
}

// IncrementGlobalVar atomically adds delta to a persistent or ephemeral global variable and returns the new numeric value.
func (s *Store) IncrementGlobalVar(key string, delta float64, ephemeral bool) (float64, error) {
	clean := strings.TrimSpace(key)
	if clean == "" {
		return 0, errors.New("global variable key cannot be empty")
	}
	now := time.Now().UTC().Unix()

	s.mu.Lock()
	defer s.mu.Unlock()

	if ephemeral {
		cur := s.ephem[clean]
		val := parseNumOrZero(cur.Value) + delta
		kind, formatted := formatIncNumber(val, cur.Kind, delta)
		s.ephem[clean] = VarEntry{Key: clean, Kind: kind, Value: formatted, UpdatedAt: now}
		return val, nil
	}
	if s.db == nil {
		return 0, errors.New("bot store is closed")
	}

	var rawVal, rawKind string
	err := s.db.QueryRow(`SELECT value, kind FROM global_vars WHERE key = ?`, clean).Scan(&rawVal, &rawKind)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	val := parseNumOrZero(rawVal) + delta
	kind, formatted := formatIncNumber(val, rawKind, delta)
	if _, err := s.db.Exec(
		`INSERT INTO global_vars(key, kind, value, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET kind=excluded.kind, value=excluded.value, updated_at=excluded.updated_at`,
		clean, kind, formatted, now,
	); err != nil {
		return 0, err
	}
	return val, nil
}

// ListGlobalVars returns all persistent or ephemeral global variables sorted by key.
func (s *Store) ListGlobalVars(ephemeral bool) []VarEntry {
	if ephemeral {
		s.mu.RLock()
		out := make([]VarEntry, 0, len(s.ephem))
		for _, v := range s.ephem {
			out = append(out, v)
		}
		s.mu.RUnlock()
		slices.SortFunc(out, func(a, b VarEntry) int {
			return strings.Compare(a.Key, b.Key)
		})
		return out
	}

	s.mu.RLock()
	db := s.db
	s.mu.RUnlock()
	if db == nil {
		return nil
	}
	rows, err := db.Query(`SELECT key, kind, value, updated_at FROM global_vars ORDER BY key ASC`)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var out []VarEntry
	for rows.Next() {
		var e VarEntry
		if scanErr := rows.Scan(&e.Key, &e.Kind, &e.Value, &e.UpdatedAt); scanErr == nil {
			out = append(out, e)
		}
	}
	return out
}

// GetUserVar retrieves a persisted per-user variable.
func (s *Store) GetUserVar(platform, userID, key string) (UserVarEntry, bool) {
	p := strings.ToLower(strings.TrimSpace(platform))
	uid := strings.TrimSpace(userID)
	k := strings.TrimSpace(key)
	if p == "" || uid == "" || k == "" {
		return UserVarEntry{}, false
	}

	s.mu.RLock()
	db := s.db
	s.mu.RUnlock()
	if db == nil {
		return UserVarEntry{}, false
	}

	var out UserVarEntry
	err := db.QueryRow(
		`SELECT platform, user_id, key, kind, value, updated_at FROM user_vars WHERE platform = ? AND user_id = ? AND key = ?`,
		p, uid, k,
	).Scan(&out.Platform, &out.UserID, &out.Key, &out.Kind, &out.Value, &out.UpdatedAt)
	if err != nil {
		return UserVarEntry{}, false
	}
	return out, true
}

// SetUserVar persists a per-user variable.
func (s *Store) SetUserVar(platform, userID, key, kind, val string) error {
	p := strings.ToLower(strings.TrimSpace(platform))
	uid := strings.TrimSpace(userID)
	k := strings.TrimSpace(key)
	if p == "" || uid == "" || k == "" {
		return errors.New("platform, userID, and key are required for user variables")
	}
	now := time.Now().UTC().Unix()
	normKind := normalizeKind(kind)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("bot store is closed")
	}
	_, err := s.db.Exec(
		`INSERT INTO user_vars(platform, user_id, key, kind, value, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(platform, user_id, key) DO UPDATE SET kind=excluded.kind, value=excluded.value, updated_at=excluded.updated_at`,
		p, uid, k, normKind, val, now,
	)
	return err
}

// DeleteUserVar removes a persisted per-user variable.
func (s *Store) DeleteUserVar(platform, userID, key string) error {
	p := strings.ToLower(strings.TrimSpace(platform))
	uid := strings.TrimSpace(userID)
	k := strings.TrimSpace(key)
	if p == "" || uid == "" || k == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM user_vars WHERE platform = ? AND user_id = ? AND key = ?`, p, uid, k)
	return err
}

// IncrementUserVar atomically adds delta to a per-user variable and returns the updated value.
func (s *Store) IncrementUserVar(platform, userID, key string, delta float64) (float64, error) {
	p := strings.ToLower(strings.TrimSpace(platform))
	uid := strings.TrimSpace(userID)
	k := strings.TrimSpace(key)
	if p == "" || uid == "" || k == "" {
		return 0, errors.New("platform, userID, and key are required for user variables")
	}
	now := time.Now().UTC().Unix()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return 0, errors.New("bot store is closed")
	}
	var rawVal, rawKind string
	err := s.db.QueryRow(
		`SELECT value, kind FROM user_vars WHERE platform = ? AND user_id = ? AND key = ?`, p, uid, k,
	).Scan(&rawVal, &rawKind)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	val := parseNumOrZero(rawVal) + delta
	kind, formatted := formatIncNumber(val, rawKind, delta)
	if _, err := s.db.Exec(
		`INSERT INTO user_vars(platform, user_id, key, kind, value, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(platform, user_id, key) DO UPDATE SET kind=excluded.kind, value=excluded.value, updated_at=excluded.updated_at`,
		p, uid, k, kind, formatted, now,
	); err != nil {
		return 0, err
	}
	return val, nil
}

// ListUserVars returns user variables optionally filtered by platform and/or userID.
func (s *Store) ListUserVars(platform, userID string) []UserVarEntry {
	s.mu.RLock()
	db := s.db
	s.mu.RUnlock()
	if db == nil {
		return nil
	}
	p := strings.ToLower(strings.TrimSpace(platform))
	uid := strings.TrimSpace(userID)

	var (
		rows *sql.Rows
		err  error
	)
	switch {
	case p != "" && uid != "":
		rows, err = db.Query(`SELECT platform, user_id, key, kind, value, updated_at FROM user_vars WHERE platform = ? AND user_id = ? ORDER BY key ASC`, p, uid)
	default:
		rows, err = db.Query(`SELECT platform, user_id, key, kind, value, updated_at FROM user_vars ORDER BY platform ASC, user_id ASC, key ASC`)
	}
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var out []UserVarEntry
	for rows.Next() {
		var e UserVarEntry
		if scanErr := rows.Scan(&e.Platform, &e.UserID, &e.Key, &e.Kind, &e.Value, &e.UpdatedAt); scanErr == nil {
			out = append(out, e)
		}
	}
	return out
}

// AddUserToGroup adds (platform, userID) to a named permission group.
func (s *Store) AddUserToGroup(groupName, platform, userID, userName string) error {
	g := strings.TrimSpace(groupName)
	p := strings.ToLower(strings.TrimSpace(platform))
	uid := strings.TrimSpace(userID)
	if g == "" || p == "" || uid == "" {
		return errors.New("groupName, platform, and userID are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("bot store is closed")
	}
	_, err := s.db.Exec(
		`INSERT INTO user_groups(group_name, platform, user_id, user_name) VALUES (?, ?, ?, ?)
		 ON CONFLICT(group_name, platform, user_id) DO UPDATE SET user_name=excluded.user_name`,
		g, p, uid, strings.TrimSpace(userName),
	)
	return err
}

// RemoveUserFromGroup removes (platform, userID) from groupName.
func (s *Store) RemoveUserFromGroup(groupName, platform, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	_, err := s.db.Exec(
		`DELETE FROM user_groups WHERE LOWER(group_name) = LOWER(?) AND platform = ? AND user_id = ?`,
		strings.TrimSpace(groupName), strings.ToLower(strings.TrimSpace(platform)), strings.TrimSpace(userID),
	)
	return err
}

// IsUserInGroup reports whether (platform, userID) belongs to groupName (case-insensitive).
func (s *Store) IsUserInGroup(groupName, platform, userID string) bool {
	s.mu.RLock()
	db := s.db
	s.mu.RUnlock()
	if db == nil {
		return false
	}
	var count int
	err := db.QueryRow(
		`SELECT COUNT(1) FROM user_groups WHERE LOWER(group_name) = LOWER(?) AND platform = ? AND user_id = ?`,
		strings.TrimSpace(groupName), strings.ToLower(strings.TrimSpace(platform)), strings.TrimSpace(userID),
	).Scan(&count)
	return err == nil && count > 0
}

// UserGroups returns all group names that (platform, userID) belongs to.
func (s *Store) UserGroups(platform, userID string) []string {
	s.mu.RLock()
	db := s.db
	s.mu.RUnlock()
	if db == nil {
		return nil
	}
	rows, err := db.Query(
		`SELECT group_name FROM user_groups WHERE platform = ? AND user_id = ? ORDER BY group_name ASC`,
		strings.ToLower(strings.TrimSpace(platform)), strings.TrimSpace(userID),
	)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var g string
		if rows.Scan(&g) == nil {
			out = append(out, g)
		}
	}
	return out
}

// ListGroupMembers lists members of groupName (or all groups if groupName is empty).
func (s *Store) ListGroupMembers(groupName string) []GroupMember {
	s.mu.RLock()
	db := s.db
	s.mu.RUnlock()
	if db == nil {
		return nil
	}
	var (
		rows *sql.Rows
		err  error
	)
	if g := strings.TrimSpace(groupName); g != "" {
		rows, err = db.Query(`SELECT group_name, platform, user_id, user_name FROM user_groups WHERE LOWER(group_name) = LOWER(?) ORDER BY user_name ASC`, g)
	} else {
		rows, err = db.Query(`SELECT group_name, platform, user_id, user_name FROM user_groups ORDER BY group_name ASC, user_name ASC`)
	}
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []GroupMember
	for rows.Next() {
		var m GroupMember
		if rows.Scan(&m.GroupName, &m.Platform, &m.UserID, &m.UserName) == nil {
			out = append(out, m)
		}
	}
	return out
}

// ListGroups returns distinct group names ordered alphabetically.
func (s *Store) ListGroups() []string {
	s.mu.RLock()
	db := s.db
	s.mu.RUnlock()
	if db == nil {
		return nil
	}
	rows, err := db.Query(`SELECT DISTINCT group_name FROM user_groups ORDER BY group_name ASC`)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var g string
		if rows.Scan(&g) == nil {
			out = append(out, g)
		}
	}
	return out
}

func parseNumOrZero(raw string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0
	}
	return v
}

func formatIncNumber(val float64, prevKind string, delta float64) (kind string, formatted string) {
	if (prevKind == "" || prevKind == "int") && delta == float64(int64(delta)) && val == float64(int64(val)) {
		return "int", strconv.FormatInt(int64(val), 10)
	}
	return "float", strconv.FormatFloat(val, 'f', -1, 64)
}
