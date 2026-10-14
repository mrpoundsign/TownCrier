package secrets

import (
	"errors"
	"fmt"
	"sync"

	"github.com/zalando/go-keyring"
)

const (
	ServiceName       = "TownCrier"
	LegacyServiceName = "OctochorpeBot"
)

// ErrNotFound is returned when a requested secret does not exist.
var ErrNotFound = errors.New("secret not found")

// Store defines the interface for secure credential persistence.
type Store interface {
	Set(key, secret string) error
	Get(key string) (string, error)
	Delete(key string) error
}

// MemoryStore provides a thread-safe in-memory secret store.
type MemoryStore struct {
	mu      sync.RWMutex
	secrets map[string]string
}

// NewMemoryStore creates a new in-memory secret store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		secrets: make(map[string]string),
	}
}

func (m *MemoryStore) Set(key, secret string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secrets[key] = secret
	return nil
}

func (m *MemoryStore) Get(key string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	val, ok := m.secrets[key]
	if !ok {
		return "", ErrNotFound
	}
	return val, nil
}

func (m *MemoryStore) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.secrets, key)
	return nil
}

// KeyringStore interacts with the OS credential manager with memory fallback.
type KeyringStore struct {
	service  string
	fallback *MemoryStore
}

// NewKeyringStore creates a new KeyringStore with an in-memory fallback.
func NewKeyringStore(service string) *KeyringStore {
	if service == "" {
		service = ServiceName
	}
	return &KeyringStore{
		service:  service,
		fallback: NewMemoryStore(),
	}
}

// Set saves a secret into the system keyring, falling back to memory on error.
func (k *KeyringStore) Set(key, secret string) error {
	err := keyring.Set(k.service, key, secret)
	if err != nil {
		// Store in fallback if OS keyring fails
		_ = k.fallback.Set(key, secret)
		return nil
	}
	return nil
}

// Get retrieves a secret from the system keyring or the fallback store.
func (k *KeyringStore) Get(key string) (string, error) {
	val, err := keyring.Get(k.service, key)
	if err == nil {
		return val, nil
	}

	// If using the default service name and not found, check legacy service name for seamless migration
	if k.service == ServiceName && errors.Is(err, keyring.ErrNotFound) {
		if legacyVal, legErr := keyring.Get(LegacyServiceName, key); legErr == nil {
			// Migrate secret to new service name
			_ = keyring.Set(k.service, key, legacyVal)
			return legacyVal, nil
		}
	}

	// Try fallback
	if fallbackVal, fbErr := k.fallback.Get(key); fbErr == nil {
		return fallbackVal, nil
	}

	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return "", fmt.Errorf("retrieve secret %q: %w", key, err)
}

// Delete removes a secret from both the system keyring and the fallback store.
func (k *KeyringStore) Delete(key string) error {
	_ = k.fallback.Delete(key)
	err := keyring.Delete(k.service, key)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		// Ignore not found errors on deletion
		return nil
	}
	return nil
}
