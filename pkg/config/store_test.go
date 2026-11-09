package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProductionLocations_RefusesInTests(t *testing.T) {
	loc, err := ProductionLocations()
	if err == nil {
		t.Fatalf("expected ProductionLocations to fail with error during tests, got loc: %+v", loc)
	}
}

func TestTestLocations_ValidatesPath(t *testing.T) {
	tmpDir := t.TempDir()
	loc := TestLocations(tmpDir)
	if loc.IsMemory() {
		t.Error("expected TestLocations to not be memory-based")
	}
	expected := filepath.Join(tmpDir, "config.json")
	if loc.ConfigPath() != expected {
		t.Errorf("expected ConfigPath %q, got %q", expected, loc.ConfigPath())
	}

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when TestLocations is given empty directory")
		}
	}()
	_ = TestLocations("")
}

func TestMemoryStore_Lifecycle(t *testing.T) {
	initial := DefaultConfig()
	initial.GlobalChatRules.DefaultVoice = "af_bella"

	store := NewMemoryStore(initial)
	if store.Location() != "memory" {
		t.Errorf("expected location 'memory', got %q", store.Location())
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.GlobalChatRules.DefaultVoice != "af_bella" {
		t.Errorf("expected 'af_bella', got %q", loaded.GlobalChatRules.DefaultVoice)
	}

	// Mutate and save
	loaded.GlobalChatRules.DefaultVoice = "am_adam"
	if err := store.Save(loaded); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	reloaded, err := store.Load()
	if err != nil {
		t.Fatalf("second Load failed: %v", err)
	}
	if reloaded.GlobalChatRules.DefaultVoice != "am_adam" {
		t.Errorf("expected updated voice 'am_adam', got %q", reloaded.GlobalChatRules.DefaultVoice)
	}

	if err := store.Save(nil); err == nil {
		t.Error("expected error saving nil configuration")
	}
}

func TestFileStore_Lifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	loc := TestLocations(tmpDir)

	store, err := NewFileStore(loc)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	if store.Location() != loc.ConfigPath() {
		t.Errorf("expected Location %q, got %q", loc.ConfigPath(), store.Location())
	}

	// Loading non-existent file returns defaults
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("initial Load failed: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil default config")
	}

	cfg.GlobalChatRules.DefaultVoice = "custom_voice"
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if _, err := os.Stat(loc.ConfigPath()); err != nil {
		t.Fatalf("expected config file to exist at %s: %v", loc.ConfigPath(), err)
	}

	reloaded, err := store.Load()
	if err != nil {
		t.Fatalf("second Load failed: %v", err)
	}
	if reloaded.GlobalChatRules.DefaultVoice != "custom_voice" {
		t.Errorf("expected 'custom_voice', got %q", reloaded.GlobalChatRules.DefaultVoice)
	}
}

func TestFileStore_RejectsInvalidLocations(t *testing.T) {
	if _, err := NewFileStore(nil); err == nil {
		t.Error("expected error with nil Locations")
	}
	if _, err := NewFileStore(MemoryLocations()); err == nil {
		t.Error("expected error with MemoryLocations")
	}
	if _, err := NewFileStore(&Locations{configPath: ""}); err == nil {
		t.Error("expected error with empty config path")
	}
}

func TestLoadAndSave_RejectEmptyPath(t *testing.T) {
	cfg := DefaultConfig()
	if err := Save("", cfg); err == nil {
		t.Error("expected Save(\"\", cfg) to fail with error")
	}
	if _, err := Load(""); err == nil {
		t.Error("expected Load(\"\") to fail with error")
	}
}
