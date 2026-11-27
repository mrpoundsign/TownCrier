package pluginhost

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // Pure-Go SQLite driver

	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

const (
	maxKVKeyBytes      = 256
	maxKVValueBytes    = 1024 * 1024
	maxKVKeysPerPlugin = 10000
	defaultKVListLimit = 256
	maxKVListLimit     = 2000
)

const kvSchemaSQL = `
CREATE TABLE IF NOT EXISTS plugin_kv (
	plugin_uuid TEXT NOT NULL,
	key         TEXT NOT NULL,
	value       BLOB NOT NULL,
	updated_at  INTEGER NOT NULL,
	PRIMARY KEY (plugin_uuid, key)
);
CREATE INDEX IF NOT EXISTS idx_plugin_kv_prefix ON plugin_kv(plugin_uuid, key);
`

// KVStore provides plugin-namespaced persistent key-value storage backed by SQLite.
type KVStore struct {
	mu     sync.Mutex
	dbPath string
	db     *sql.DB
}

// NewKVStore creates a lazy-opening KVStore for dbPath (pass ":memory:" in tests).
func NewKVStore(dbPath string) *KVStore {
	return &KVStore{dbPath: strings.TrimSpace(dbPath)}
}

func (s *KVStore) ensureOpen() (*sql.DB, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		return s.db, nil
	}
	dsn := s.dbPath
	if dsn == "" {
		dsn = ":memory:"
	}
	if dsn != ":memory:" && !strings.HasPrefix(dsn, "file:") {
		if err := os.MkdirAll(filepath.Dir(dsn), 0o755); err != nil {
			return nil, fmt.Errorf("create kv directory: %w", err)
		}
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open plugin kv db: %w", err)
	}
	if dsn == ":memory:" {
		db.SetMaxOpenConns(1)
	} else {
		_, _ = db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;")
	}
	if _, err := db.Exec(kvSchemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init plugin kv schema: %w", err)
	}
	s.db = db
	return db, nil
}

// Close closes the underlying SQLite connection if opened.
func (s *KVStore) Close() error {
	s.mu.Lock()
	db := s.db
	s.db = nil
	s.mu.Unlock()
	if db != nil {
		return db.Close()
	}
	return nil
}

// Get fetches a stored value for pluginUUID and key.
func (s *KVStore) Get(ctx context.Context, pluginUUID, key string) ([]byte, bool, *pluginv1.APIError) {
	if err := validateKVKey(pluginUUID, key); err != nil {
		return nil, false, err
	}
	db, openErr := s.ensureOpen()
	if openErr != nil {
		return nil, false, &pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INTERNAL,
			Message: "failed opening plugin KV storage",
			Detail:  openErr.Error(),
		}
	}
	var val []byte
	err := db.QueryRowContext(ctx, `SELECT value FROM plugin_kv WHERE plugin_uuid = ? AND key = ?`, pluginUUID, key).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, &pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INTERNAL,
			Message: "failed reading plugin KV key",
			Detail:  err.Error(),
		}
	}
	return val, true, nil
}

// Set writes or updates a value for pluginUUID and key.
func (s *KVStore) Set(ctx context.Context, pluginUUID, key string, value []byte) *pluginv1.APIError {
	if err := validateKVKey(pluginUUID, key); err != nil {
		return err
	}
	if len(value) > maxKVValueBytes {
		return &pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			Message: fmt.Sprintf("KV value exceeds maximum size (%d > %d bytes)", len(value), maxKVValueBytes),
		}
	}
	db, openErr := s.ensureOpen()
	if openErr != nil {
		return &pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INTERNAL,
			Message: "failed opening plugin KV storage",
			Detail:  openErr.Error(),
		}
	}

	var exists int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM plugin_kv WHERE plugin_uuid = ? AND key = ?`, pluginUUID, key).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		var count int
		if cntErr := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_kv WHERE plugin_uuid = ?`, pluginUUID).Scan(&count); cntErr == nil && count >= maxKVKeysPerPlugin {
			return &pluginv1.APIError{
				Code:    pluginv1.ErrorCode_ERROR_CODE_RATE_LIMITED,
				Message: fmt.Sprintf("plugin KV key limit (%d) reached", maxKVKeysPerPlugin),
			}
		}
	}

	if value == nil {
		value = []byte{}
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO plugin_kv (plugin_uuid, key, value, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(plugin_uuid, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, pluginUUID, key, value, time.Now().UTC().UnixMilli()); err != nil {
		return &pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INTERNAL,
			Message: "failed writing plugin KV key",
			Detail:  err.Error(),
		}
	}
	return nil
}

// Delete removes key for pluginUUID.
func (s *KVStore) Delete(ctx context.Context, pluginUUID, key string) *pluginv1.APIError {
	if err := validateKVKey(pluginUUID, key); err != nil {
		return err
	}
	db, openErr := s.ensureOpen()
	if openErr != nil {
		return &pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INTERNAL,
			Message: "failed opening plugin KV storage",
			Detail:  openErr.Error(),
		}
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM plugin_kv WHERE plugin_uuid = ? AND key = ?`, pluginUUID, key); err != nil {
		return &pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INTERNAL,
			Message: "failed deleting plugin KV key",
			Detail:  err.Error(),
		}
	}
	return nil
}

// List returns sorted keys for pluginUUID matching prefix, up to limit.
func (s *KVStore) List(ctx context.Context, pluginUUID, prefix string, limit uint32) ([]string, *pluginv1.APIError) {
	if strings.TrimSpace(pluginUUID) == "" {
		return nil, &pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			Message: "plugin UUID is required for KV access",
		}
	}
	db, openErr := s.ensureOpen()
	if openErr != nil {
		return nil, &pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INTERNAL,
			Message: "failed opening plugin KV storage",
			Detail:  openErr.Error(),
		}
	}
	lim := int(limit)
	if lim <= 0 {
		lim = defaultKVListLimit
	} else if lim > maxKVListLimit {
		lim = maxKVListLimit
	}

	var (
		rows *sql.Rows
		err  error
	)
	if prefix == "" {
		rows, err = db.QueryContext(ctx, `SELECT key FROM plugin_kv WHERE plugin_uuid = ? ORDER BY key ASC LIMIT ?`, pluginUUID, lim)
	} else {
		rows, err = db.QueryContext(ctx, `SELECT key FROM plugin_kv WHERE plugin_uuid = ? AND key >= ? AND key < ? ORDER BY key ASC LIMIT ?`, pluginUUID, prefix, prefix+"\xff", lim)
	}
	if err != nil {
		return nil, &pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INTERNAL,
			Message: "failed listing plugin KV keys",
			Detail:  err.Error(),
		}
	}
	defer func() { _ = rows.Close() }()

	var keys []string
	for rows.Next() {
		var k string
		if scanErr := rows.Scan(&k); scanErr != nil {
			return nil, &pluginv1.APIError{
				Code:    pluginv1.ErrorCode_ERROR_CODE_INTERNAL,
				Message: "failed scanning plugin KV key",
				Detail:  scanErr.Error(),
			}
		}
		keys = append(keys, k)
	}
	return keys, nil
}

func validateKVKey(pluginUUID, key string) *pluginv1.APIError {
	if strings.TrimSpace(pluginUUID) == "" {
		return &pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			Message: "plugin UUID is required for KV access",
		}
	}
	if key == "" {
		return &pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			Message: "KV key cannot be empty",
		}
	}
	if len(key) > maxKVKeyBytes {
		return &pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			Message: fmt.Sprintf("KV key exceeds maximum length (%d > %d bytes)", len(key), maxKVKeyBytes),
		}
	}
	return nil
}
