package config

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

// Store defines an abstraction for loading and persisting application configuration.
type Store interface {
	Load() (*Config, error)
	Save(*Config) error
	Location() string
}

// Locations specifies physical filesystem paths and persistence modes for configuration.
type Locations struct {
	configPath string
	isMemory   bool
}

// ProductionLocations resolves the host user's standard configuration path (%APPDATA% / ~/.config).
// In test environments, it returns an error to prevent tests from touching real user configs.
func ProductionLocations() (*Locations, error) {
	if testing.Testing() {
		return nil, errors.New("ProductionLocations() cannot be used in unit tests; use config.TestLocations() or config.NewMemoryStore()")
	}
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	return &Locations{configPath: path}, nil
}

// TestLocations creates an isolated configuration location inside an explicit test directory.
func TestLocations(dir string) *Locations {
	if dir == "" {
		panic("TestLocations requires a non-empty directory")
	}
	return &Locations{
		configPath: filepath.Join(dir, "config.json"),
	}
}

// MemoryLocations creates a non-persisting in-memory location for tests.
func MemoryLocations() *Locations {
	return &Locations{
		isMemory: true,
	}
}

// ConfigPath returns the configured filesystem path, or an empty string if in-memory.
func (l *Locations) ConfigPath() string {
	if l == nil || l.isMemory {
		return ""
	}
	return l.configPath
}

// IsMemory returns true if this location does not persist to disk.
func (l *Locations) IsMemory() bool {
	return l != nil && l.isMemory
}

// FileStore implements Store backed by a validated disk location.
type FileStore struct {
	loc *Locations
}

// NewFileStore creates a Store that reads and writes from the given Locations instance.
func NewFileStore(loc *Locations) (*FileStore, error) {
	if loc == nil {
		return nil, errors.New("cannot create FileStore with nil Locations")
	}
	if loc.isMemory {
		return nil, errors.New("cannot create FileStore with MemoryLocations; use NewMemoryStore instead")
	}
	if loc.configPath == "" {
		return nil, errors.New("cannot create FileStore without an explicit config path")
	}
	return &FileStore{loc: loc}, nil
}

// Load reads and parses configuration from the configured file path.
func (s *FileStore) Load() (*Config, error) {
	return Load(s.loc.configPath)
}

// Save atomically writes configuration to the configured file path.
func (s *FileStore) Save(cfg *Config) error {
	return Save(s.loc.configPath, cfg)
}

// Location returns the filesystem path of the configuration file.
func (s *FileStore) Location() string {
	return s.loc.configPath
}

// MemoryStore implements Store completely in-memory with zero disk interaction.
type MemoryStore struct {
	mu  sync.RWMutex
	cfg *Config
}

// NewMemoryStore creates a Store that manages configuration entirely in memory.
func NewMemoryStore(initial *Config) *MemoryStore {
	if initial == nil {
		initial = DefaultConfig()
	}
	return &MemoryStore{cfg: initial.Clone()}
}

// Load returns a cloned copy of the in-memory configuration.
func (s *MemoryStore) Load() (*Config, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Clone(), nil
}

// Save updates the in-memory configuration without touching disk.
func (s *MemoryStore) Save(cfg *Config) error {
	if cfg == nil {
		return errors.New("cannot save nil configuration")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg.Clone()
	return nil
}

// Location returns the constant string "memory".
func (s *MemoryStore) Location() string {
	return "memory"
}
