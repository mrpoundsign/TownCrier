package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/stretchr/testify/require"

	"github.com/mrpoundsign/TownCrier/pkg/pluginsdk/ui"
	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

func TestPluginUIReconcilerSnapshotPatchAndPreservation(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("ReconcilerTest")
	defer w.Close()

	var events []*pluginv1.UIEvent
	var resyncCalls atomic.Int32
	var toasts []string

	rec := NewPluginUIReconciler(w, "main", NewPluginImageCache(nil), ReconcilerCallbacks{
		OnUIEvent: func(ev *pluginv1.UIEvent) {
			events = append(events, ev)
		},
		OnResync: func() {
			resyncCalls.Add(1)
		},
		OnToast: func(msg string, _ pluginv1.SemanticTone) {
			toasts = append(toasts, msg)
		},
	})

	snap := ui.Snapshot("main", 1, ui.VBox("root",
		ui.Text("title", "Initial Title", ui.WithBold()),
		ui.Button("play", "Play", ui.WithIcon("play")),
		ui.TextInput("search", ui.WithPlaceholder("Type...")),
		ui.Slider("seek", 0, 100, 1, 10),
		ui.Progress("prog", 0.1),
		ui.Checkbox("chk", "Approve", false),
		ui.Select("sel", []string{"One", "Two"}, "One"),
		ui.VirtualList("vlist", []*pluginv1.VirtualListRow{
			ui.ListRow("k1", ui.HBox("r1", ui.Text("r1-lbl", "Song A"), ui.Button("r1-btn", "Skip"))),
		}, ui.WithListMinHeight(120)),
	))

	rec.ApplySnapshot(snap)
	w.SetContent(rec.Root())

	// Tap Button -> emits CLICK without crashing
	btn := rec.nodes["play"].btn
	require.NotNil(t, btn)
	test.Tap(btn)
	require.Len(t, events, 1)
	require.Equal(t, "play", events[0].GetNodeId())
	require.Equal(t, pluginv1.UIEventKind_UI_EVENT_KIND_CLICK, events[0].GetKind())

	// Focused TextInput suppresses programmatic SetValue unless force_value=true
	entry := rec.nodes["search"].entry
	require.NotNil(t, entry)
	entry.FocusGained()
	test.Type(entry, "beethoven")
	require.Equal(t, "beethoven", entry.Text)

	ok := rec.ApplyPatch(ui.PatchBatch("main", 1, 2,
		ui.SetText("title", "Patched Title"),
		ui.SetValue("search", "server-echo"),
		ui.SetProgress("prog", 0.5),
		ui.SetChecked("chk", true),
		ui.Toast("Now Playing", pluginv1.SemanticTone_TONE_SUCCESS),
	))
	require.True(t, ok)
	require.Equal(t, "Patched Title", rec.nodes["title"].richText.Segments[0].(*widget.TextSegment).Text)
	require.Equal(t, "beethoven", entry.Text, "focused TextInput must ignore non-forced SetValue")
	require.Equal(t, 0.5, rec.nodes["prog"].progressBar.Value)
	require.True(t, rec.nodes["chk"].check.Checked)
	require.Equal(t, []string{"Now Playing"}, toasts)

	// ForceValue overrides even while focused
	require.True(t, rec.ApplyPatch(ui.PatchBatch("main", 2, 3, ui.SetForceValue("search", ""))))
	require.Equal(t, "", entry.Text)

	// Slider active drag suppresses programmatic 1Hz SetValue updates until OnChangeEnded
	slider := rec.nodes["seek"].slider
	slider.Value = 33
	slider.OnChanged(33)
	require.True(t, rec.nodes["seek"].dragging)
	require.True(t, rec.ApplyPatch(ui.PatchBatch("main", 3, 4, ui.SetSlider("seek", 99))))
	require.Equal(t, float64(33), slider.Value, "active drag must suppress programmatic SetSlider")
	slider.OnChangeEnded(33)
	require.False(t, rec.nodes["seek"].dragging)
	require.True(t, rec.ApplyPatch(ui.PatchBatch("main", 4, 5, ui.SetSlider("seek", 77))))
	require.Equal(t, float64(77), slider.Value)

	// InsertChild, ReplaceSubtree, and RemoveChild
	require.True(t, rec.ApplyPatch(ui.PatchBatch("main", 5, 6,
		ui.InsertChild("root", 1, ui.Text("extra", "Inserted")),
		ui.ReplaceNode("extra", ui.Text("extra", "Replaced")),
	)))
	require.Equal(t, "Replaced", rec.nodes["extra"].richText.Segments[0].(*widget.TextSegment).Text)
	require.True(t, rec.ApplyPatch(ui.PatchBatch("main", 6, 7, ui.RemoveChild("extra"))))
	require.Nil(t, rec.nodes["extra"])

	// Base revision mismatch triggers resync callback
	mismatchOK := rec.ApplyPatch(ui.PatchBatch("main", 999, 1000, ui.SetText("title", "ShouldSkip")))
	require.False(t, mismatchOK)
	require.Equal(t, int32(1), resyncCalls.Load())
}

func TestPluginImageCacheFetchAndAllowlist(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.RGBA{R: 56, G: 189, B: 248, A: 255})
	require.NoError(t, png.Encode(&buf, img))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	cache := NewPluginImageCache(srv.Client())
	cache.allowHTTP = true

	// Disallowed host fails fast
	var errGot error
	cache.Fetch(srv.URL+"/thumb.png", []string{"i.ytimg.com"}, true, func(_ image.Image, err error) {
		errGot = err
	})
	require.Error(t, errGot)

	// Allowed host fetches asynchronously and hits LRU on second call
	done := make(chan image.Image, 1)
	cache.Fetch(srv.URL+"/thumb.png", []string{"127.0.0.1:*"}, true, func(res image.Image, err error) {
		require.NoError(t, err)
		done <- res
	})
	select {
	case got := <-done:
		require.NotNil(t, got)
		require.Equal(t, 4, got.Bounds().Dx())
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for cached image fetch")
	}

	// Second fetch serves synchronously from LRU
	var syncHit image.Image
	cache.Fetch(srv.URL+"/thumb.png", []string{"127.0.0.1:*"}, true, func(res image.Image, err error) {
		require.NoError(t, err)
		syncHit = res
	})
	require.NotNil(t, syncHit)
	_ = fyne.NewSize(4, 4)
}
