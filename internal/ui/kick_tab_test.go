package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/platform/kick"
	"github.com/mrpoundsign/TownCrier/internal/secrets"
)

func TestKickTabViewBuild(t *testing.T) {
	fyneApp := test.NewApp()
	defer fyneApp.Quit()

	w := fyneApp.NewWindow("Test Kick Tab")
	cfg := config.DefaultConfig()
	session := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
	defer func() { _ = session.Close() }()

	kickView := NewKickTabView(w, session)
	content := kickView.Build()
	if content == nil {
		t.Fatal("expected non-nil Kick tab content")
	}
	if kickView.overrideCheck.Text != "Override Global Defaults" {
		t.Errorf("overrideCheck.Text = %q, want 'Override Global Defaults'", kickView.overrideCheck.Text)
	}
	if kickView.overridesBox.Visible() {
		t.Error("expected overridesBox to be hidden when Override Global Defaults is unchecked")
	}
	kickView.overrideCheck.SetChecked(true)
	if !kickView.overridesBox.Visible() {
		t.Error("expected overridesBox to be visible when Override Global Defaults is checked")
	}

	folChk, ok := kickView.roleChecks["follower"]
	if !ok || folChk == nil {
		t.Fatal("expected 'follower' role checkbox in KickTabView")
	}
	kickView.roleGatedCheck.SetChecked(true)
	folChk.SetChecked(true)

	if kickView.infoLink == nil || kickView.infoLink.URL == nil || kickView.infoLink.URL.String() != kickChannelInfoLookupURL {
		t.Fatalf("expected Kick infoLink to point to %q, got %+v", kickChannelInfoLookupURL, kickView.infoLink)
	}

	kickView.enableCheck.SetChecked(true)
	kickView.channelEntry.SetText("nanaseriously#4014200")
	kickView.UpdateStatus("disconnected", "Channel lookup failed — enter nanaseriously#chatroom_id")
	if kickView.statusBadge.Text != "🔴 Disconnected (Channel lookup failed — enter nanaseriously#chatroom_id)" {
		t.Errorf("unexpected statusBadge on disconnected error: %q", kickView.statusBadge.Text)
	}
	kickView.UpdateStatus("waiting", "nanaseriously")
	if kickView.statusBadge.Text != "🟡 Connected to Chat (Waiting for Stream)..." {
		t.Errorf("unexpected statusBadge on waiting: %q", kickView.statusBadge.Text)
	}

	// Verify OAuth state transitions hide/show channelRow, hintLabel, and infoLink
	if !kickView.channelRow.Visible() {
		t.Error("expected channelRow to be visible when Kick OAuth is not connected")
	}
	_ = session.KickOAuth().SaveToken(&kick.TokenData{
		AccessToken:       "tok_123",
		BroadcasterUserID: 42,
		Slug:              "nanaseriously",
	})
	kickView.refreshOAuthState()
	if kickView.channelRow.Visible() {
		t.Error("expected channelRow to be hidden when Kick OAuth is connected")
	}
	if kickView.hintLabel.Visible() {
		t.Error("expected hintLabel to be hidden when Kick OAuth is connected")
	}
	if kickView.oauthStatusLabel.Text != "🟢 Kick OAuth: Connected (@nanaseriously)" {
		t.Errorf("oauthStatusLabel.Text = %q, want Connected (@nanaseriously)", kickView.oauthStatusLabel.Text)
	}

	// Simulate 401 invalidation
	_, _ = session.KickOAuth().HandleUnauthorized(t.Context())
	kickView.refreshOAuthState()
	if !kickView.channelRow.Visible() {
		t.Error("expected channelRow to be visible again after Kick OAuth is invalidated")
	}
}
