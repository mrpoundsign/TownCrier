package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/bot"
	"github.com/mrpoundsign/TownCrier/pkg/config"
)

func TestEventTemplateVarsCoverage(t *testing.T) {
	platforms := []string{"twitch", "youtube", "tiktok", "kick"}
	for _, p := range platforms {
		vars := EventTemplateVars(p, "sub")
		if len(vars) == 0 {
			t.Errorf("expected template vars for %s sub", p)
		}
		foundUser := false
		for _, v := range vars {
			if v.Var == "{user}" {
				foundUser = true
				break
			}
		}
		if !foundUser {
			t.Errorf("expected {user} in %s sub template vars", p)
		}
	}

	// Specific Twitch watch_streak vars
	wsVars := EventTemplateVars("twitch", "watch_streak")
	if len(wsVars) < 3 {
		t.Fatalf("expected at least 3 vars for twitch watch_streak, got %d", len(wsVars))
	}
	hasStreak := false
	hasAmount := false
	for _, v := range wsVars {
		if v.Var == "{count}" {
			hasStreak = true
		}
		if v.Var == "{amount}" {
			hasAmount = true
		}
	}
	if !hasStreak || !hasAmount {
		t.Errorf("expected {count} and {amount} in watch_streak vars, got %+v", wsVars)
	}
}

func TestTemplateHelpMarkerLifecycle(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	win := app.NewWindow("Test")
	defer win.Close()

	var statusMsg string
	vars := []TemplateVar{
		{Var: "{user}", Desc: "Username"},
		{Var: "{count}", Desc: "Count"},
	}
	entry := widget.NewEntry()
	marker := newTemplateHelpMarker(win, "test", "Test Alert", vars, func(msg string) {
		statusMsg = msg
	}, entry)

	if marker.minSize != fyne.NewSize(20, 20) {
		t.Errorf("unexpected minSize: %v", marker.minSize)
	}

	// MouseIn triggers status
	marker.MouseIn(nil)
	if statusMsg == "" {
		t.Error("expected onStatus to be called on MouseIn")
	}

	// Tap triggers modal dialog without panic
	marker.Tapped(nil)

	// Test insertVariable helper
	entry.SetText("Hello")
	entry.CursorColumn = 5
	insertVariable(entry, "{user}")
	if entry.Text != "Hello {user}" {
		t.Errorf("expected 'Hello {user}', got '%s'", entry.Text)
	}

	// Insert into empty entry
	emptyEntry := widget.NewEntry()
	insertVariable(emptyEntry, "{user}")
	if emptyEntry.Text != "{user}" {
		t.Errorf("expected '{user}', got '%s'", emptyEntry.Text)
	}
}

func TestBotTemplateVarsCoverage(t *testing.T) {
	vars := BotTemplateVars()
	if len(vars) < 10 {
		t.Fatalf("expected comprehensive bot template vars, got %d", len(vars))
	}
	want := map[string]bool{
		"%obs.scene%":         false,
		"%obs.previousScene%": false,
		"%obs.previewScene%":  false,
		"%obs.connected%":     false,
		"%user%":              false,
		"%arg0%":              false,
		"%global.<key>%":      false,
	}
	for _, v := range vars {
		if _, ok := want[v.Var]; ok {
			want[v.Var] = true
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("expected %s in BotTemplateVars()", k)
		}
	}
}

func TestBotTemplateVarsWithDeclared(t *testing.T) {
	st, err := bot.OpenStore(":memory:")
	requireNoErr := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	requireNoErr(err)
	defer func() { _ = st.Close() }()
	requireNoErr(st.SetGlobalVar("total", "int", "12", false))
	requireNoErr(st.SetGlobalVar("deaths", "int", "3", true))

	botCfg := config.BotAutomationConfig{
		Variables: []config.BotVariableDef{
			{Name: "total", Scope: "global", Kind: "int", DefaultValue: "0"},
			{Name: "deaths", Scope: "session", Kind: "int", DefaultValue: "0"},
		},
	}
	act := &config.BotActionDef{
		ID:   "a1",
		Name: "Act",
		Variables: []config.BotVariableDef{
			{Name: "tmp", Scope: "action", Kind: "string", DefaultValue: "abc"},
		},
	}

	vars := BotTemplateVarsWithDeclared(botCfg, act, st)
	if len(vars) < 3 {
		t.Fatalf("expected declared + base vars, got %d", len(vars))
	}
	if vars[0].Var != "%global.total%" || !strings.Contains(vars[0].Desc, `current="12"`) {
		t.Fatalf("unexpected first declared var: %+v", vars[0])
	}
	if vars[1].Var != "%session.deaths%" || !strings.Contains(vars[1].Desc, `current="3"`) {
		t.Fatalf("unexpected second declared var: %+v", vars[1])
	}
	if vars[2].Var != "%action.tmp%" || !strings.Contains(vars[2].Desc, `default="abc"`) {
		t.Fatalf("unexpected third declared var: %+v", vars[2])
	}
}
