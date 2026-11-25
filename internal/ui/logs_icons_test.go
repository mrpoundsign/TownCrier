package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

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
		tooltip:   tooltip,
		onTap: func() {
			tapped = true
		},
		baseStyle: fyne.TextStyle{Bold: true},
	}
	label.Text = "coolstreamer"
	label.TextStyle = label.baseStyle
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
	if !label.TextStyle.Bold {
		t.Fatalf("expected TextStyle.Bold=true (preserved from baseStyle)")
	}

	label.MouseOut()
	if label.hovered {
		t.Fatalf("expected hovered=false after MouseOut")
	}
	if label.TextStyle.Underline {
		t.Fatalf("expected TextStyle.Underline=false after MouseOut")
	}
	if !label.TextStyle.Bold {
		t.Fatalf("expected TextStyle.Bold=true after MouseOut (restored to baseStyle)")
	}

	// Verify unbold label does not get stuck bold after hover
	unboldLabel := &hoverCellLabel{
		tooltip:   "Unbold tip",
		onTap:     func() {},
		baseStyle: fyne.TextStyle{},
	}
	unboldLabel.Text = "normaluser"
	unboldLabel.TextStyle = unboldLabel.baseStyle
	unboldLabel.Truncation = fyne.TextTruncateEllipsis
	unboldLabel.ExtendBaseWidget(unboldLabel)

	unboldLabel.MouseIn(&desktop.MouseEvent{})
	if !unboldLabel.TextStyle.Underline {
		t.Fatalf("expected unboldLabel.TextStyle.Underline=true on hover")
	}
	if unboldLabel.TextStyle.Bold {
		t.Fatalf("expected unboldLabel.TextStyle.Bold=false on hover (not forced bold)")
	}
	unboldLabel.MouseOut()
	if unboldLabel.TextStyle.Underline {
		t.Fatalf("expected unboldLabel.TextStyle.Underline=false after MouseOut")
	}
	if unboldLabel.TextStyle.Bold {
		t.Fatalf("expected unboldLabel.TextStyle.Bold=false after MouseOut (not stuck bold)")
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
	if userLbl.TextStyle.Bold {
		t.Fatalf("expected initial userLbl.TextStyle.Bold=false")
	}

	userLbl.MouseIn(&desktop.MouseEvent{})
	if !userLbl.TextStyle.Underline {
		t.Fatalf("expected userLbl.TextStyle.Underline=true on hover")
	}
	userLbl.MouseOut()
	if userLbl.TextStyle.Underline {
		t.Fatalf("expected userLbl.TextStyle.Underline=false after MouseOut")
	}
	if userLbl.TextStyle.Bold {
		t.Fatalf("expected userLbl.TextStyle.Bold=false after MouseOut (not stuck bold)")
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
	defer dash.Close()
	w.SetContent(dash.Build())
	dash.AddActivity(ActivityEntry{
		ID:        "act-1",
		Timestamp: time.Now(),
		Platform:  "kick",
		UserName:  "kick_streamer",
		UserID:    "k-99",
		Text:      "Hey on Kick!",
	})

	dash.logView.mu.Lock()
	cached := dash.logView.rowCache["act-1"]
	dash.logView.mu.Unlock()
	if cached == nil || cached.rowCols == nil || len(cached.rowCols.Objects) < 3 {
		t.Fatalf("expected cached log row with at least 3 columns, got %+v", cached)
	}

	userLbl, ok := cached.rowCols.Objects[2].(*hoverCellLabel)
	if !ok {
		t.Fatalf("expected user cell to be *hoverCellLabel, got %T", cached.rowCols.Objects[2])
	}
	if userLbl.onTap == nil {
		t.Fatalf("expected userLbl.onTap to be non-nil")
	}
	if userLbl.Cursor() != desktop.PointerCursor {
		t.Fatalf("expected PointerCursor for kick activity row")
	}
}

func TestLogTableLayoutSwappedChildAndScaleChange(t *testing.T) {
	testApp := test.NewApp()
	defer testApp.Quit()

	scale := float32(1.0)
	const rowWidth float32 = 1000
	const rowHeight float32 = 36

	layout := &logTableLayout{
		getWidth: func() float32 { return rowWidth },
		getScale: func() float32 { return scale },
	}

	ttsOrig := container.NewCenter(container.NewHBox(widget.NewLabel("A")))
	rowCols := container.New(layout,
		widget.NewLabel("time"),
		widget.NewLabel("svc"),
		widget.NewLabel("user"),
		widget.NewLabel("msg"),
		ttsOrig,
		widget.NewLabel("mod"),
	)
	rowCols.Resize(fyne.NewSize(rowWidth, rowHeight))

	expectedTTSX := func(s float32) float32 {
		msgW := computeMsgColWidth(rowWidth, s)
		return (colWidthTime+colWidthService+colWidthUser+4*colGap)*s + msgW
	}

	wantX := expectedTTSX(1.0)
	if got := rowCols.Objects[4].Position(); got.X != wantX || got.Y != 0 {
		t.Fatalf("initial TTS column pos = %v, want (%v, 0)", got, wantX)
	}
	if got := rowCols.Objects[4].Size(); got.Width != colWidthTTSStatus || got.Height != rowHeight {
		t.Fatalf("initial TTS column size = %v, want (%v, %v)", got, colWidthTTSStatus, rowHeight)
	}

	// Swap Objects[4] without calling layout.Invalidate(), then Refresh().
	ttsSwapped := container.NewCenter(container.NewHBox(widget.NewLabel("B")))
	rowCols.Objects[4] = ttsSwapped
	rowCols.Refresh()

	if got := rowCols.Objects[4].Position(); got.X != wantX || got.Y != 0 {
		t.Fatalf("after swap without Invalidate, TTS column pos = %v, want (%v, 0)", got, wantX)
	}
	if got := rowCols.Objects[4].Size(); got.Width != colWidthTTSStatus || got.Height != rowHeight {
		t.Fatalf("after swap without Invalidate, TTS column size = %v, want (%v, %v)", got, colWidthTTSStatus, rowHeight)
	}

	// Change scale, call MinSize() first, then Layout() with the exact same outer size.
	scale = 1.25
	_ = layout.MinSize(rowCols.Objects)
	layout.Layout(rowCols.Objects, fyne.NewSize(rowWidth, rowHeight))

	wantScaledX := expectedTTSX(1.25)
	wantScaledW := colWidthTTSStatus * 1.25
	if got := rowCols.Objects[4].Position(); got.X != wantScaledX || got.Y != 0 {
		t.Fatalf("after scale change + MinSize, TTS column pos = %v, want (%v, 0)", got, wantScaledX)
	}
	if got := rowCols.Objects[4].Size(); got.Width != wantScaledW || got.Height != rowHeight {
		t.Fatalf("after scale change + MinSize, TTS column size = %v, want (%v, %v)", got, wantScaledW, rowHeight)
	}
}

func TestLogsViewInPlaceTTSStatusUpdateMaintainsColumnPosition(t *testing.T) {
	testApp := test.NewApp()
	defer testApp.Quit()

	w := testApp.NewWindow("Test Logs")
	defer w.Close()

	v := NewLogsView(w, nil)
	w.SetContent(v.listBox)
	w.Resize(fyne.NewSize(1000, 600))

	v.mu.Lock()
	v.entries = []eventlog.Entry{
		{
			ID:        "evt-tts-1",
			CreatedAt: time.Now(),
			Platform:  "twitch",
			UserName:  "alice",
			RawText:   "hello",
			Spoken:    false,
		},
	}
	v.totalCount = 1
	v.mu.Unlock()

	v.renderEntries()

	v.mu.Lock()
	cached := v.rowCache["evt-tts-1"]
	v.mu.Unlock()
	if cached == nil || cached.rowCols == nil || len(cached.rowCols.Objects) <= 4 {
		t.Fatalf("expected cached row with >= 5 columns, got %+v", cached)
	}

	origObj := cached.rowCols.Objects[4]
	origPos := origObj.Position()
	origSize := origObj.Size()
	if origPos.X <= 0 || origSize.Width <= 0 {
		t.Fatalf("expected initial TTS column positioned and sized > 0, got pos=%v size=%v", origPos, origSize)
	}

	// Simulate LiveUpdateOutcome changing Spoken/Outcome and triggering renderEntries without window resize.
	v.mu.Lock()
	v.entries[0].Spoken = true
	v.entries[0].Outcome = "spoken"
	v.mu.Unlock()

	v.renderEntries()

	updatedObj := cached.rowCols.Objects[4]
	if updatedObj == origObj {
		t.Fatalf("expected Objects[4] to be replaced on TTS outcome update")
	}
	if cached.ttsStatusCell != updatedObj {
		t.Fatalf("expected cached.ttsStatusCell to match Objects[4]")
	}
	if got := updatedObj.Position(); got != origPos {
		t.Fatalf("updated TTS column pos = %v, want %v", got, origPos)
	}
	if got := updatedObj.Size(); got != origSize {
		t.Fatalf("updated TTS column size = %v, want %v", got, origSize)
	}
}
