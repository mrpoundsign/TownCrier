package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/secrets"
	"github.com/mrpoundsign/TownCrier/internal/tts"
)

func TestVoiceSpeedFieldAndSummary(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	win := appObj.NewWindow("Test VoiceSpeedField")

	cfg := config.DefaultConfig()
	session := app.NewSession(cfg, secrets.NewMemoryStore())
	defer func() { _ = session.Close() }()

	field := NewVoiceSpeedField(win, session, "Edit Voice", "af_bella", 1.25, nil, nil)
	if field.VoiceID() != "af_bella" {
		t.Errorf("VoiceID() = %q, want %q", field.VoiceID(), "af_bella")
	}
	if field.Speed() != 1.25 {
		t.Errorf("Speed() = %v, want 1.25", field.Speed())
	}
	if !strings.Contains(field.Label.Text, "Bella") || !strings.Contains(field.Label.Text, "1.25x") {
		t.Errorf("unexpected summary label: %q", field.Label.Text)
	}

	field.SetVoiceAndSpeed("supertonic:m2", 1.5)
	if field.VoiceID() != "supertonic:m2" {
		t.Errorf("VoiceID() after update = %q, want %q", field.VoiceID(), "supertonic:m2")
	}
	if field.Speed() != 1.5 {
		t.Errorf("Speed() after update = %v, want 1.5", field.Speed())
	}
	if !strings.Contains(field.Label.Text, "Supertonic M2") || !strings.Contains(field.Label.Text, "1.50x") {
		t.Errorf("unexpected summary label after update: %q", field.Label.Text)
	}

	field.Disable()
	if !field.Disabled() {
		t.Error("expected field to be disabled after Disable()")
	}
	field.Enable()
	if field.Disabled() {
		t.Error("expected field to be enabled after Enable()")
	}
}

func TestBuildVoiceGroupsForModels(t *testing.T) {
	groups := BuildVoiceGroupsForModels([]string{tts.ModelSupertonic, tts.ModelKittenNano}, "")
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups[0].FamilyID != tts.ModelSupertonic || groups[1].FamilyID != tts.ModelKittenNano {
		t.Errorf("unexpected group families: %v, %v", groups[0].FamilyID, groups[1].FamilyID)
	}

	// If currentVoiceID belongs to a disabled family (e.g. kokoro), it should be prepended
	withLegacy := BuildVoiceGroupsForModels([]string{tts.ModelSupertonic}, "af_sarah")
	if len(withLegacy) != 2 {
		t.Fatalf("expected 2 groups when currentVoiceID is from disabled family, got %d", len(withLegacy))
	}
	if withLegacy[0].Voices[0].ID != "af_sarah" {
		t.Errorf("expected prepended voice 'af_sarah', got %q", withLegacy[0].Voices[0].ID)
	}
}

func TestVoicesTabViewBuildAndRefresh(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	win := appObj.NewWindow("Test VoicesTabView")

	cfg := config.DefaultConfig()
	cfg.GlobalChatRules.DefaultVoice = "supertonic:f1"
	cfg.GlobalChatRules.DefaultSpeed = 1.15
	session := app.NewSession(cfg, secrets.NewMemoryStore())
	defer func() { _ = session.Close() }()

	view := NewVoicesTabView(win, session)
	content := view.Build()
	if content == nil {
		t.Fatal("expected non-nil content from VoicesTabView.Build")
	}
	if view.globalVoiceField == nil {
		t.Fatal("expected non-nil globalVoiceField")
	}
	if view.globalVoiceField.VoiceID() != "supertonic:f1" {
		t.Errorf("expected globalVoiceField.VoiceID = 'supertonic:f1', got %q", view.globalVoiceField.VoiceID())
	}
	if view.globalVoiceField.Speed() != 1.15 {
		t.Errorf("expected globalVoiceField.Speed = 1.15, got %v", view.globalVoiceField.Speed())
	}

	view.RefreshVoices()
}
