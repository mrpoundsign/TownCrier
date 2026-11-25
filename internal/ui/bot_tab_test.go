package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"github.com/stretchr/testify/require"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/secrets"
)

func TestBotTabViewBuildTestRunAndOBSSecret(t *testing.T) {
	fyneApp := test.NewApp()
	defer fyneApp.Quit()

	cfg := config.DefaultConfig()
	cfg.Bot.Enabled = true
	cfg.Bot.Commands = []config.BotCommandDef{
		{
			ID:        "cmd-1",
			Name:      "Hello",
			Enabled:   true,
			Trigger:   "!hello",
			MatchMode: "prefix",
			ActionID:  "act-1",
		},
	}
	cfg.Bot.Actions = []config.BotActionDef{
		{
			ID:      "act-1",
			Name:    "Set Welcome Var",
			Enabled: true,
			SubActions: []config.BotSubActionDef{
				{
					ID:      "sa-1",
					Kind:    "set_var",
					Enabled: true,
					Scope:   "global_persistent",
					Key:     "welcome_test",
					VarKind: "string",
					Value:   "ok-%user%",
				},
			},
		},
	}

	secStore := secrets.NewMemoryStore()
	sess := app.NewSession(cfg, secStore, config.NewMemoryStore(cfg))
	defer func() { _ = sess.Close() }()
	require.NoError(t, sess.Start())

	win := fyneApp.NewWindow("Bot Tab Test")
	defer win.Close()

	var lastStatus string
	view := NewBotTabView(win, sess, func(msg string) {
		lastStatus = msg
	})
	obj := view.Build()
	tabs, ok := obj.(*container.AppTabs)
	require.True(t, ok, "expected *container.AppTabs from BotTabView.Build()")
	require.Len(t, tabs.Items, 5)
	require.Equal(t, "Commands", tabs.Items[0].Text)
	require.Equal(t, "Actions & Sub-Actions", tabs.Items[1].Text)
	require.Equal(t, "Timers & Voice", tabs.Items[2].Text)
	require.Equal(t, "Queues & Variables", tabs.Items[3].Text)
	require.Equal(t, "OBS Studio", tabs.Items[4].Text)

	// Verify Test Run executes the selected action via BotEngine and writes to Store
	view.selectedActionID = "act-1"
	view.runSelectedActionTest()
	require.Contains(t, lastStatus, "Executed Test Run")
	require.Eventually(t, func() bool {
		v, found := sess.BotEngine().Store().GetGlobalVar("welcome_test", false)
		return found && v.Value == "ok-tester"
	}, 2*time.Second, 20*time.Millisecond)

	// Verify OBS credentials save host/port to Config and password to OS keyring secret
	view.obsHostEntry.SetText("192.168.1.50")
	view.obsPortEntry.SetText("4466")
	view.obsPasswordEntry.SetText("supersecret")
	view.obsAutoReconnChk.SetChecked(true)
	_, _, _ = view.saveOBSSettings()

	gotCfg := sess.Config()
	require.True(t, gotCfg.Bot.OBS.Enabled)
	require.Equal(t, "192.168.1.50", gotCfg.Bot.OBS.Host)
	require.Equal(t, 4466, gotCfg.Bot.OBS.Port)

	pw, err := secStore.Get(config.SecretOBSPassword)
	require.NoError(t, err)
	require.Equal(t, "supersecret", pw)
}
