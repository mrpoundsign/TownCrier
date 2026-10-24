package secrets

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"
)

type fakeKeyringProvider struct {
	mu      sync.Mutex
	entries map[string]map[string]string
	failErr error
}

func newFakeKeyringProvider() *fakeKeyringProvider {
	return &fakeKeyringProvider{
		entries: make(map[string]map[string]string),
	}
}

func (f *fakeKeyringProvider) Set(service, user, password string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failErr != nil {
		return f.failErr
	}
	if f.entries[service] == nil {
		f.entries[service] = make(map[string]string)
	}
	f.entries[service][user] = password
	return nil
}

func (f *fakeKeyringProvider) Get(service, user string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failErr != nil {
		return "", f.failErr
	}
	svc, ok := f.entries[service]
	if !ok {
		return "", keyring.ErrNotFound
	}
	val, ok := svc[user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return val, nil
}

func (f *fakeKeyringProvider) Delete(service, user string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failErr != nil {
		return f.failErr
	}
	svc, ok := f.entries[service]
	if !ok {
		return keyring.ErrNotFound
	}
	if _, exists := svc[user]; !exists {
		return keyring.ErrNotFound
	}
	delete(svc, user)
	return nil
}

func TestMemoryStore(t *testing.T) {
	s := NewMemoryStore()

	// Get non-existent
	_, err := s.Get("token")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// Set and Get
	if err := s.Set("token", "secret123"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	val, err := s.Get("token")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if val != "secret123" {
		t.Errorf("expected 'secret123', got %q", val)
	}

	// Delete
	if err := s.Delete("token"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	_, err = s.Get("token")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestKeyringStoreWithFallback(t *testing.T) {
	fake := newFakeKeyringProvider()
	k := newKeyringStoreWithProvider("TownCrierTest", fake)

	if err := k.Set("oauth_token", "test_value_abc"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	val, err := k.Get("oauth_token")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if val != "test_value_abc" {
		t.Errorf("expected 'test_value_abc', got %q", val)
	}

	if err := k.Delete("oauth_token"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if _, err := k.Get("oauth_token"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}

	// Simulate OS keyring failure and verify fallback to MemoryStore
	fake.failErr = errors.New("credential manager unavailable")
	if err := k.Set("fallback_key", "fallback_secret"); err != nil {
		t.Fatalf("Set with fallback failed: %v", err)
	}
	fbVal, err := k.Get("fallback_key")
	if err != nil || fbVal != "fallback_secret" {
		t.Fatalf("expected fallback_secret, got %q (err=%v)", fbVal, err)
	}
}

func TestStoreConcurrency(t *testing.T) {
	s := NewMemoryStore()
	var wg sync.WaitGroup

	for i := range 20 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := fmt.Sprintf("key_%d", idx)
			val := fmt.Sprintf("val_%d", idx)
			_ = s.Set(key, val)
			readVal, err := s.Get(key)
			if err != nil || readVal != val {
				t.Errorf("concurrent read/write failed for %s", key)
			}
		}(i)
	}
	wg.Wait()
}
