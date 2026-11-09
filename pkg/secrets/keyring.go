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

// keyringProvider abstracts the underlying OS credential manager for testability.
type keyringProvider interface {
	Set(service, user, password string) error
	Get(service, user string) (string, error)
	Delete(service, user string) error
}

type osKeyringProvider struct{}

func (osKeyringProvider) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}

func (osKeyringProvider) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (osKeyringProvider) Delete(service, user string) error {
	return keyring.Delete(service, user)
}

// KeyringStore interacts with the OS credential manager with in-memory read caching and fallback.
type KeyringStore struct {
	service   string
	provider  keyringProvider
	fallback  *MemoryStore
	mu        sync.RWMutex
	cache     map[string]string
	notFound  map[string]bool
}

// NewKeyringStore creates a new KeyringStore with an in-memory fallback.
func NewKeyringStore(service string) *KeyringStore {
	return newKeyringStoreWithProvider(service, osKeyringProvider{})
}

func newKeyringStoreWithProvider(service string, provider keyringProvider) *KeyringStore {
	if service == "" {
		service = ServiceName
	}
	if provider == nil {
		provider = osKeyringProvider{}
	}
	return &KeyringStore{
		service:  service,
		provider: provider,
		fallback: NewMemoryStore(),
		cache:    make(map[string]string),
		notFound: make(map[string]bool),
	}
}

// Set saves a secret into the system keyring, falling back to memory on error.
func (k *KeyringStore) Set(key, secret string) error {
	k.mu.Lock()
	k.cache[key] = secret
	delete(k.notFound, key)
	k.mu.Unlock()

	err := k.provider.Set(k.service, key, secret)
	if err != nil {
		// Store in fallback if OS keyring fails
		_ = k.fallback.Set(key, secret)
		return nil
	}
	return nil
}

// Get retrieves a secret from the system keyring or the fallback store.
func (k *KeyringStore) Get(key string) (string, error) {
	k.mu.RLock()
	if val, ok := k.cache[key]; ok {
		k.mu.RUnlock()
		return val, nil
	}
	if k.notFound[key] {
		k.mu.RUnlock()
		return "", ErrNotFound
	}
	k.mu.RUnlock()

	val, err := k.provider.Get(k.service, key)
	if err == nil {
		k.mu.Lock()
		k.cache[key] = val
		delete(k.notFound, key)
		k.mu.Unlock()
		return val, nil
	}

	// If using the default service name and not found, check legacy service name for seamless migration
	if k.service == ServiceName && errors.Is(err, keyring.ErrNotFound) {
		if legacyVal, legErr := k.provider.Get(LegacyServiceName, key); legErr == nil {
			// Migrate secret to new service name
			_ = k.provider.Set(k.service, key, legacyVal)
			k.mu.Lock()
			k.cache[key] = legacyVal
			delete(k.notFound, key)
			k.mu.Unlock()
			return legacyVal, nil
		}
	}

	// Try fallback
	if fallbackVal, fbErr := k.fallback.Get(key); fbErr == nil {
		k.mu.Lock()
		k.cache[key] = fallbackVal
		delete(k.notFound, key)
		k.mu.Unlock()
		return fallbackVal, nil
	}

	if errors.Is(err, keyring.ErrNotFound) {
		k.mu.Lock()
		k.notFound[key] = true
		k.mu.Unlock()
		return "", ErrNotFound
	}
	return "", fmt.Errorf("retrieve secret %q: %w", key, err)
}

// Delete removes a secret from both the system keyring and the fallback store.
func (k *KeyringStore) Delete(key string) error {
	k.mu.Lock()
	delete(k.cache, key)
	k.notFound[key] = true
	k.mu.Unlock()

	_ = k.fallback.Delete(key)
	err := k.provider.Delete(k.service, key)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		// Ignore not found errors on deletion
		return nil
	}
	return nil
}
