package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/eventlog"
	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/secrets"
)

func TestHoverCellLabelClickable(t *testing.T) {
	testApp := test.NewApp()
	defer testApp.Quit()

	tapped := false
	tooltip := "User tooltip • Click to open profile in browser: https://www.twitch.tv/coolstreamer"

	label := &hoverCellLabel{
		tooltip: tooltip,
		onTap: func() {
			tapped = true
		},
	}
	label.Text = "coolstreamer"
	label.TextStyle = fyne.TextStyle{Bold: true}
	label.Truncation = fyne.TextTruncateEllipsis
	label.ExtendBaseWidget(label)

	// Verify Cursor returns PointerCursor when onTap is set
	if cur := label.Cursor(); cur != desktop.PointerCursor {
		t.Fatalf("expected desktop.PointerCursor, got %v", cur)
	}

	// Verify Tapped invokes onTap
	label.Tapped(&fyne.PointEvent{})
	if !tapped {
		t.Fatalf("expected tapped callback to be invoked")
	}

	// Verify MouseIn and MouseOut trigger hover and native text underline
	label.MouseIn(&desktop.MouseEvent{})
	if !label.hovered {
		t.Fatalf("expected hovered=true after MouseIn")
	}
	if !label.TextStyle.Underline {
		t.Fatalf("expected TextStyle.Underline=true when hovered with onTap")
	}

	label.MouseOut()
	if label.hovered {
		t.Fatalf("expected hovered=false after MouseOut")
	}
	if label.TextStyle.Underline {
		t.Fatalf("expected TextStyle.Underline=false after MouseOut")
	}

	// Non-clickable label should return DefaultCursor
	nonClickable := &hoverCellLabel{tooltip: "Plain tooltip"}
	nonClickable.Text = "plain"
	nonClickable.ExtendBaseWidget(nonClickable)
	if cur := nonClickable.Cursor(); cur != desktop.DefaultCursor {
		t.Fatalf("expected desktop.DefaultCursor for non-clickable label, got %v", cur)
	}
}

func TestLogsViewUserCellClickableProfile(t *testing.T) {
	testApp := test.NewApp()
	defer testApp.Quit()

	w := testApp.NewWindow("Test")
	defer w.Close()

	v := NewLogsView(w, nil)
	e := eventlog.Entry{
		ID:        "evt-click-1",
		CreatedAt: time.Now(),
		Platform:  "twitch",
		UserName:  "streamer_alice",
		UserID:    "12345",
		RawText:   "Hello world!",
	}

	root, cached := v.buildLogRowWithRef(0, e)
	if root == nil || cached == nil {
		t.Fatalf("buildLogRowWithRef returned nil")
	}

	// Column [2] is userCell
	userCell := cached.rowCols.Objects[2]
	var userLbl *hoverCellLabel
	if lbl, ok := userCell.(*hoverCellLabel); ok {
		userLbl = lbl
	} else if border, ok := userCell.(*fyne.Container); ok {
		userLbl = border.Objects[0].(*hoverCellLabel)
	}

	if userLbl == nil {
		t.Fatalf("could not find hoverCellLabel in userCell")
	}

	if userLbl.onTap == nil {
		t.Fatalf("expected userLbl.onTap to be non-nil for twitch user")
	}
	if userLbl.Cursor() != desktop.PointerCursor {
		t.Fatalf("expected PointerCursor on twitch user cell")
	}
	if userLbl.Text != "streamer_alice" {
		t.Fatalf("expected Text='streamer_alice', got %q", userLbl.Text)
	}
}

func TestDashboardActivityClickableProfile(t *testing.T) {
	testApp := test.NewApp()
	defer testApp.Quit()

	w := testApp.NewWindow("Test Dashboard")
	defer w.Close()

	cfg := config.DefaultConfig()
	session := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))

	dash := NewDashboardView(w, session)
	row := dash.buildActivityRow(0, ActivityEntry{
		ID:        "act-1",
		Timestamp: time.Now(),
		Platform:  "kick",
		UserName:  "kick_streamer",
		UserID:    "k-99",
		Text:      "Hey on Kick!",
	})

	vbox, ok := row.(*fyne.Container)
	if !ok || len(vbox.Objects) < 1 {
		t.Fatalf("expected row to be a container with children")
	}
	topRow, ok := vbox.Objects[0].(*fyne.Container)
	if !ok {
		t.Fatalf("expected topRow to be a container")
	}

	// topRow.Objects[0] is nameBox (HBox: [timeLbl, nameObj, platLbl])
	nameBox, ok := topRow.Objects[0].(*fyne.Container)
	if !ok {
		t.Fatalf("expected nameBox to be a container")
	}
	if len(nameBox.Objects) < 3 {
		t.Fatalf("expected nameBox to have at least 3 objects, got %d", len(nameBox.Objects))
	}

	userLbl, ok := nameBox.Objects[1].(*hoverCellLabel)
	if !ok {
		t.Fatalf("expected nameBox.Objects[1] to be *hoverCellLabel, got %T", nameBox.Objects[1])
	}
	if userLbl.onTap == nil {
		t.Fatalf("expected userLbl.onTap to be non-nil")
	}
	if userLbl.Cursor() != desktop.PointerCursor {
		t.Fatalf("expected PointerCursor for kick activity row")
	}
}
