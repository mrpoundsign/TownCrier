package secrets

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

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
	// Testing with a custom test service name
	k := NewKeyringStore("TownCrierTest")

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
