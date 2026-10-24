package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/platform/youtube"
	"github.com/mrpoundsign/TownCrier/internal/secrets"
)

func TestYouTubeTabViewBuild(t *testing.T) {
	fyneApp := test.NewApp()
	defer fyneApp.Quit()

	w := fyneApp.NewWindow("Test YouTube Tab")
	cfg := config.DefaultConfig()
	session := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
	defer func() { _ = session.Close() }()

	ytView := NewYouTubeTabView(w, session)
	content := ytView.Build()
	if content == nil {
		t.Fatal("expected non-nil YouTube tab content")
	}
	if ytView.overrideCheck.Text != "Override Global Defaults" {
		t.Errorf("overrideCheck.Text = %q, want 'Override Global Defaults'", ytView.overrideCheck.Text)
	}
	if ytView.overridesBox.Visible() {
		t.Error("expected overridesBox to be hidden when Override Global Defaults is unchecked")
	}
	ytView.overrideCheck.SetChecked(true)
	if !ytView.overridesBox.Visible() {
		t.Error("expected overridesBox to be visible when Override Global Defaults is checked")
	}

	// Verify YouTube OAuth controls and Zero-Config hint visibility
	if ytView.oauthDisconnectBtn.Visible() {
		t.Error("expected oauthDisconnectBtn to be hidden when no YouTube OAuth token is stored")
	}
	if !ytView.hintLabel.Visible() || !ytView.channelRow.Visible() {
		t.Error("expected Zero-Config hintLabel and channelRow to be visible when no YouTube OAuth token is stored")
	}
	_ = session.YouTubeOAuth().SaveToken(&youtube.TokenData{
		AccessToken:   "yt-test-tok",
		RefreshToken:  "yt-test-ref",
		ChannelID:     "UC_TEST_123",
		ChannelTitle:  "MrPoundSign",
		ChannelHandle: "@mrpoundsign",
	})
	ytView.refreshOAuthState()
	if !ytView.oauthDisconnectBtn.Visible() {
		t.Error("expected oauthDisconnectBtn to be visible when YouTube OAuth token is present")
	}
	if ytView.hintLabel.Visible() || ytView.channelRow.Visible() {
		t.Error("expected Zero-Config hintLabel and channelRow to be hidden when YouTube OAuth is connected")
	}
	if ytView.oauthStatusLabel.Text != "🟢 YouTube OAuth: Connected (@mrpoundsign)" {
		t.Errorf("oauthStatusLabel.Text = %q, want %q", ytView.oauthStatusLabel.Text, "🟢 YouTube OAuth: Connected (@mrpoundsign)")
	}
	if ytView.channelEntry.Text != "mrpoundsign" {
		t.Errorf("expected empty channelEntry to auto-fill 'mrpoundsign', got %q", ytView.channelEntry.Text)
	}

	// Add a second YouTube profile and test switching between profiles via dropdown
	_ = session.YouTubeOAuth().SaveToken(&youtube.TokenData{
		AccessToken:   "yt-test-tok-2",
		RefreshToken:  "yt-test-ref-2",
		ChannelID:     "UC_TEST_456",
		ChannelTitle:  "Alt Channel",
		ChannelHandle: "@altchannel",
	})
	ytView.refreshOAuthState()
	if len(ytView.profileSelect.Options) != 2 {
		t.Fatalf("expected 2 saved profiles in profileSelect.Options, got %v", ytView.profileSelect.Options)
	}
	ytView.profileSelect.SetSelected("Alt Channel (@altchannel)")
	if ytView.channelEntry.Text != "altchannel" {
		t.Errorf("expected channelEntry to switch to 'altchannel', got %q", ytView.channelEntry.Text)
	}
	ytView.profileSelect.SetSelected("@mrpoundsign")
	if ytView.channelEntry.Text != "mrpoundsign" {
		t.Errorf("expected channelEntry to switch back to 'mrpoundsign', got %q", ytView.channelEntry.Text)
	}

	// First disconnect removes @mrpoundsign and falls back to @altchannel
	ytView.oauthDisconnectBtn.OnTapped()
	if !session.YouTubeOAuth().HasToken() {
		t.Fatal("expected second profile to remain active after disconnecting first profile")
	}
	if ytView.channelEntry.Text != "altchannel" {
		t.Errorf("expected channelEntry to fall back to 'altchannel', got %q", ytView.channelEntry.Text)
	}

	// Second disconnect removes the last remaining profile
	ytView.oauthDisconnectBtn.OnTapped()
	if session.YouTubeOAuth().HasToken() {
		t.Error("expected YouTube OAuth token to be cleared after disconnecting last profile")
	}
	if !ytView.hintLabel.Visible() || !ytView.channelRow.Visible() {
		t.Error("expected Zero-Config hintLabel and channelRow to be visible again after disconnecting OAuth")
	}
}
