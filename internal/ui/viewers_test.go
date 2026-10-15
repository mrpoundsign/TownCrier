package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/secrets"
)

func TestViewersTabViewBuild(t *testing.T) {
	appObj := test.NewApp()
	win := appObj.NewWindow("Test")
	cfg := config.DefaultConfig()
	cfg.Viewers["twitch:chatter1"] = config.ViewerConfig{
		Platform: "twitch",
		UserID:   "chatter1",
		Voice:    "af_sarah",
		Speed:    1.2,
		Nickname: "SarahFan",
	}

	session := app.NewSession(cfg, secrets.NewMemoryStore())

	view := NewViewersTabView(win, session)
	content := view.Build()
	if content == nil {
		t.Fatal("expected non-nil content from ViewersTabView.Build")
	}

	if len(view.tableList.Objects) != 1 {
		t.Errorf("expected 1 row in tableList, got %d", len(view.tableList.Objects))
	}

	// Delete
	view.deleteViewer("twitch:chatter1")
	if len(session.Config().Viewers) != 0 {
		t.Errorf("expected 0 viewers after delete, got %d", len(session.Config().Viewers))
	}
}

func TestViewersMultiPlatformFormattingAndSearch(t *testing.T) {
	vc := config.ViewerConfig{
		Platform: "twitch",
		UserID:   "mrpoundsign",
		Accounts: []config.ViewerAccount{
			{Platform: "twitch", UserID: "mrpoundsign"},
			{Platform: "youtube", UserID: "@MrPoundSignYT"},
		},
		Voice:    "am_adam",
		Speed:    1.1,
		Nickname: "Pound",
	}

	title := formatViewerAccountsTitle(vc)
	expected := `[TWITCH] mrpoundsign  •  [YOUTUBE] @MrPoundSignYT (Spoken as: "Pound")`
	if title != expected {
		t.Errorf("formatViewerAccountsTitle = %q, want %q", title, expected)
	}

	if !viewerMatchesQuery("twitch:mrpoundsign", vc, "mrpoundsignyt") {
		t.Error("expected viewerMatchesQuery to match linked YouTube handle")
	}
	if !viewerMatchesQuery("twitch:mrpoundsign", vc, "youtube") {
		t.Error("expected viewerMatchesQuery to match linked YouTube platform")
	}
}
