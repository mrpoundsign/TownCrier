package ui

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/eventlog"
	"github.com/mrpoundsign/TownCrier/pkg/chat"
	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/secrets"
)

func buildTestAnimatedGIF(t *testing.T, w, h int, colors []color.RGBA, delays []int, disposals []byte) []byte {
	t.Helper()
	pal := color.Palette{
		color.RGBA{R: 0, G: 0, B: 0, A: 0},
		color.RGBA{R: 255, G: 0, B: 0, A: 255},
		color.RGBA{R: 0, G: 255, B: 0, A: 255},
		color.RGBA{R: 0, G: 0, B: 255, A: 255},
		color.RGBA{R: 255, G: 255, B: 0, A: 255},
	}
	g := &gif.GIF{
		Delay:    delays,
		Disposal: disposals,
		Config: image.Config{
			ColorModel: pal,
			Width:      w,
			Height:     h,
		},
	}
	for i, c := range colors {
		rect := image.Rect(0, 0, w, h)
		if i == 1 && len(colors) >= 3 && w >= 4 && h >= 4 {
			rect = image.Rect(1, 1, w-1, h-1)
		}
		img := image.NewPaletted(rect, pal)
		idx := uint8(pal.Index(c))
		for y := rect.Min.Y; y < rect.Max.Y; y++ {
			for x := rect.Min.X; x < rect.Max.X; x++ {
				img.SetColorIndex(x, y, idx)
			}
		}
		g.Image = append(g.Image, img)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatalf("gif.EncodeAll: %v", err)
	}
	return buf.Bytes()
}

type lsbBitWriter struct {
	buf   bytes.Buffer
	bits  uint64
	nbits uint
}

func (bw *lsbBitWriter) writeBits(val uint32, n uint) {
	bw.bits |= uint64(val) << bw.nbits
	bw.nbits += n
	for bw.nbits >= 8 {
		bw.buf.WriteByte(byte(bw.bits))
		bw.bits >>= 8
		bw.nbits -= 8
	}
}

func (bw *lsbBitWriter) bytes() []byte {
	if bw.nbits > 0 {
		bw.buf.WriteByte(byte(bw.bits))
		bw.bits = 0
		bw.nbits = 0
	}
	return bw.buf.Bytes()
}

func buildSolidVP8LChunkPayload(w, h int, c color.NRGBA) []byte {
	var bw lsbBitWriter
	bw.writeBits(0x2f, 8) // VP8L signature
	bw.writeBits(uint32(w-1), 14)
	bw.writeBits(uint32(h-1), 14)
	bw.writeBits(1, 1) // alphaIsUsed
	bw.writeBits(0, 3) // version
	bw.writeBits(0, 1) // no transform
	bw.writeBits(0, 1) // no color cache
	bw.writeBits(0, 1) // single huffman group

	// 5 single-symbol Huffman trees: green, red, blue, alpha, distance
	for _, sym := range []uint32{uint32(c.G), uint32(c.R), uint32(c.B), uint32(c.A), 0} {
		bw.writeBits(1, 1)   // useSimple
		bw.writeBits(0, 1)   // nSymbols - 1 = 0 (1 symbol)
		bw.writeBits(1, 1)   // 8-bit symbol
		bw.writeBits(sym, 8) // symbol value
	}
	return bw.bytes()
}

func appendRIFFChunk(dst []byte, fourCC string, payload []byte) []byte {
	dst = append(dst, fourCC[:4]...)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(payload)))
	dst = append(dst, payload...)
	if len(payload)%2 == 1 {
		dst = append(dst, 0)
	}
	return dst
}

type testWebPFrame struct {
	fx, fy      int
	fw, fh      int
	durMs       uint32
	disposeToBg bool
	noBlend     bool
	color       color.NRGBA
}

func buildTestAnimatedWebP(w, h int, bg color.NRGBA, frames []testWebPFrame) []byte {
	var body []byte

	vp8x := make([]byte, 10)
	vp8x[0] = (1 << 1) | (1 << 4) // animation + alpha
	copy(vp8x[4:7], appendUint24LE(nil, uint32(w-1)))
	copy(vp8x[7:10], appendUint24LE(nil, uint32(h-1)))
	body = appendRIFFChunk(body, "VP8X", vp8x)

	anim := []byte{bg.B, bg.G, bg.R, bg.A, 0, 0}
	body = appendRIFFChunk(body, "ANIM", anim)

	for _, fr := range frames {
		var anmf []byte
		anmf = appendUint24LE(anmf, uint32(fr.fx/2))
		anmf = appendUint24LE(anmf, uint32(fr.fy/2))
		anmf = appendUint24LE(anmf, uint32(fr.fw-1))
		anmf = appendUint24LE(anmf, uint32(fr.fh-1))
		anmf = appendUint24LE(anmf, fr.durMs)
		var flags byte
		if fr.disposeToBg {
			flags |= 0x01
		}
		if fr.noBlend {
			flags |= 0x02
		}
		anmf = append(anmf, flags)
		vp8l := buildSolidVP8LChunkPayload(fr.fw, fr.fh, fr.color)
		anmf = appendRIFFChunk(anmf, "VP8L", vp8l)
		body = appendRIFFChunk(body, "ANMF", anmf)
	}

	out := make([]byte, 0, 12+len(body))
	out = append(out, 'R', 'I', 'F', 'F')
	out = binary.LittleEndian.AppendUint32(out, uint32(4+len(body)))
	out = append(out, 'W', 'E', 'B', 'P')
	out = append(out, body...)
	return out
}

func TestDecodeGIFDisposalAndCaps(t *testing.T) {
	blue := color.RGBA{R: 0, G: 0, B: 255, A: 255}
	red := color.RGBA{R: 255, G: 0, B: 0, A: 255}
	green := color.RGBA{R: 0, G: 255, B: 0, A: 255}

	raw := buildTestAnimatedGIF(t, 4, 4,
		[]color.RGBA{blue, red, green},
		[]int{1, 8, 12}, // 10ms -> normalized to 100ms; 80ms; 120ms
		[]byte{gif.DisposalNone, gif.DisposalBackground, gif.DisposalNone},
	)
	asset, ext, _, err := decodeEmoteAsset("disp", raw)
	if err != nil {
		t.Fatalf("decodeEmoteAsset GIF: %v", err)
	}
	if ext != ".gif" || !asset.IsAnimated() || len(asset.Frames) != 3 {
		t.Fatalf("unexpected decoded GIF asset: ext=%q animated=%v frames=%d", ext, asset.IsAnimated(), len(asset.Frames))
	}
	if asset.Delays[0] != 100*time.Millisecond || asset.Delays[1] != 80*time.Millisecond || asset.Delays[2] != 120*time.Millisecond {
		t.Fatalf("unexpected GIF delays: %v", asset.Delays)
	}
	// Frame 1 should have red at (1,1) and blue at border (0,0)
	if got := asset.Frames[1].RGBAAt(1, 1); got != red {
		t.Errorf("frame 1 center = %v, want %v", got, red)
	}
	if got := asset.Frames[1].RGBAAt(0, 0); got != blue {
		t.Errorf("frame 1 border = %v, want %v", got, blue)
	}
	// Frame 2 overlays full green after DisposalBackground
	if got := asset.Frames[2].RGBAAt(1, 1); got != green {
		t.Errorf("frame 2 center = %v, want %v", got, green)
	}

	// Test DisposalPrevious restores prior frame (blue at (1,1)) before applying partial frame
	prevGIF := &gif.GIF{
		Delay:    []int{5, 5, 5},
		Disposal: []byte{gif.DisposalNone, gif.DisposalPrevious, gif.DisposalNone},
		Config: image.Config{
			ColorModel: color.Palette{color.RGBA{}, blue, red, green},
			Width:      4,
			Height:     4,
		},
	}
	f0 := image.NewPaletted(image.Rect(0, 0, 4, 4), prevGIF.Config.ColorModel.(color.Palette))
	draw.Draw(f0, f0.Bounds(), &image.Uniform{C: blue}, image.Point{}, draw.Src)
	f1 := image.NewPaletted(image.Rect(1, 1, 3, 3), prevGIF.Config.ColorModel.(color.Palette))
	draw.Draw(f1, f1.Bounds(), &image.Uniform{C: red}, image.Point{}, draw.Src)
	f2 := image.NewPaletted(image.Rect(0, 0, 1, 1), prevGIF.Config.ColorModel.(color.Palette))
	draw.Draw(f2, f2.Bounds(), &image.Uniform{C: green}, image.Point{}, draw.Src)
	prevGIF.Image = []*image.Paletted{f0, f1, f2}
	var pbuf bytes.Buffer
	if err := gif.EncodeAll(&pbuf, prevGIF); err != nil {
		t.Fatalf("gif.EncodeAll DisposalPrevious: %v", err)
	}
	prevAsset, _, _, err := decodeEmoteAsset("prev", pbuf.Bytes())
	if err != nil {
		t.Fatalf("decodeEmoteAsset DisposalPrevious: %v", err)
	}
	if got := prevAsset.Frames[2].RGBAAt(1, 1); got != blue {
		t.Errorf("DisposalPrevious failed: frame 2 (1,1) = %v, want %v", got, blue)
	}
	if got := prevAsset.Frames[2].RGBAAt(0, 0); got != green {
		t.Errorf("DisposalPrevious failed: frame 2 (0,0) = %v, want %v", got, green)
	}

	// Downscale cap check: 128x64 -> 64x32
	bigRaw := buildTestAnimatedGIF(t, 128, 64, []color.RGBA{blue, red}, []int{5, 5}, []byte{0, 0})
	bigAsset, _, _, err := decodeEmoteAsset("big", bigRaw)
	if err != nil {
		t.Fatalf("decodeEmoteAsset oversized GIF: %v", err)
	}
	if b := bigAsset.Frames[0].Bounds(); b.Dx() != 64 || b.Dy() != 32 {
		t.Fatalf("expected 128x64 downscaled to 64x32, got %dx%d", b.Dx(), b.Dy())
	}
}

func TestDecodeAnimatedWebP(t *testing.T) {
	red := color.NRGBA{R: 255, G: 0, B: 0, A: 255}
	semiGreen := color.NRGBA{R: 0, G: 255, B: 0, A: 255}

	raw := buildTestAnimatedWebP(4, 4, color.NRGBA{A: 0}, []testWebPFrame{
		{fx: 0, fy: 0, fw: 4, fh: 4, durMs: 60, disposeToBg: false, noBlend: true, color: red},
		{fx: 2, fy: 2, fw: 2, fh: 2, durMs: 5, disposeToBg: true, noBlend: false, color: semiGreen},
		{fx: 0, fy: 0, fw: 2, fh: 2, durMs: 120, disposeToBg: false, noBlend: true, color: color.NRGBA{B: 255, A: 255}},
	})

	asset, ext, _, err := decodeEmoteAsset("animwebp", raw)
	if err != nil {
		t.Fatalf("decodeEmoteAsset WebP: %v", err)
	}
	if ext != ".webp" || !asset.IsAnimated() || len(asset.Frames) != 3 {
		t.Fatalf("unexpected decoded WebP: ext=%q animated=%v frames=%d", ext, asset.IsAnimated(), len(asset.Frames))
	}
	if asset.Delays[0] != 60*time.Millisecond || asset.Delays[1] != 100*time.Millisecond || asset.Delays[2] != 120*time.Millisecond {
		t.Fatalf("unexpected WebP delays: %v", asset.Delays)
	}
	if got := asset.Frames[0].RGBAAt(1, 1); got != (color.RGBA{R: 255, A: 255}) {
		t.Errorf("frame 0 pixel = %v, want red", got)
	}
	if got := asset.Frames[1].RGBAAt(2, 2); got != (color.RGBA{G: 255, A: 255}) {
		t.Errorf("frame 1 (2,2) = %v, want green", got)
	}
	// Because frame 1 had disposeToBg=true (transparent bg), (2,2) in frame 2 should be cleared to transparent
	if got := asset.Frames[2].RGBAAt(2, 2); got != (color.RGBA{}) {
		t.Errorf("frame 2 (2,2) after dispose = %v, want transparent", got)
	}
	if got := asset.Frames[2].RGBAAt(0, 0); got != (color.RGBA{B: 255, A: 255}) {
		t.Errorf("frame 2 (0,0) = %v, want blue", got)
	}
}

func TestEmoteCacheDiskV2AndHTTPUpgradeAndLRUEviction(t *testing.T) {
	gifBytes := buildTestAnimatedGIF(t, 4, 4,
		[]color.RGBA{{R: 255, A: 255}, {G: 255, A: 255}},
		[]int{6, 6},
		[]byte{gif.DisposalNone, gif.DisposalNone},
	)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/gif")
		_, _ = w.Write(gifBytes)
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	urlStr := srv.URL + "/emotes/test-anim"
	// Place a stale v1 <stem>.png on disk; v2 cache must ignore it and fetch animated GIF
	stem := emoteCacheStem(urlStr)
	if err := os.WriteFile(filepath.Join(cacheDir, stem+".png"), []byte("stale-v1"), 0o644); err != nil {
		t.Fatalf("write stale v1 file: %v", err)
	}

	cache := NewEmoteCache(cacheDir, srv.Client())
	defer cache.Close()

	done := make(chan *EmoteAsset, 1)
	if asset := cache.GetOrFetchAsset(urlStr, func(a *EmoteAsset) { done <- a }); asset != nil {
		t.Fatalf("expected initial miss due to v1 ignore, got cached asset")
	}

	var fetched *EmoteAsset
	select {
	case fetched = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for async emote fetch")
	}
	if !fetched.IsAnimated() {
		t.Fatal("expected fetched GIF asset to be animated")
	}
	if _, err := os.Stat(filepath.Join(cacheDir, stem+"_v2.gif")); err != nil {
		t.Fatalf("expected _v2.gif persisted on disk: %v", err)
	}

	// Re-open fresh cache pointing to same disk dir -> should load animated GIF from disk without HTTP hit
	hitsBefore := hits.Load()
	cache2 := NewEmoteCache(cacheDir, srv.Client())
	defer cache2.Close()
	fromDisk := cache2.GetOrFetchAsset(urlStr, nil)
	if fromDisk == nil || !fromDisk.IsAnimated() {
		t.Fatalf("expected animated asset loaded from _v2.gif disk cache, got %+v", fromDisk)
	}
	if hits.Load() != hitsBefore {
		t.Fatalf("expected 0 HTTP requests on disk cache hit, got %d", hits.Load()-hitsBefore)
	}

	// LRU eviction test: 2 frames of 4x4 RGBA = 2*64 = 128 bytes each. Budget = 260 bytes (holds 2, evicts 3rd).
	lruCache := NewEmoteCache("", nil)
	defer lruCache.Close()
	lruCache.maxAnimBytes = 260
	resGIF := fyne.NewStaticResource("anim.gif", gifBytes)
	lruCache.Put("https://example.com/a.gif", resGIF)
	lruCache.Put("https://example.com/b.gif", resGIF)
	// Access A so B becomes least-recently-used
	if lruCache.GetOrFetchAsset("https://example.com/a.gif", nil) == nil {
		t.Fatal("expected A in cache")
	}
	lruCache.Put("https://example.com/c.gif", resGIF)

	lruCache.mu.Lock()
	_, hasA := lruCache.mem[emoteCacheStem("https://example.com/a.gif")]
	_, hasB := lruCache.mem[emoteCacheStem("https://example.com/b.gif")]
	_, hasC := lruCache.mem[emoteCacheStem("https://example.com/c.gif")]
	lruCache.mu.Unlock()
	if !hasA || hasB || !hasC {
		t.Fatalf("unexpected LRU eviction state: hasA=%v hasB=%v hasC=%v (want true/false/true)", hasA, hasB, hasC)
	}
}

func TestEmoteAnimatorLockstepAndThrottle(t *testing.T) {
	asset := &EmoteAsset{
		Delays:         []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 50 * time.Millisecond},
		CumulativeEnds: []time.Duration{50 * time.Millisecond, 150 * time.Millisecond, 200 * time.Millisecond},
		TotalDuration:  200 * time.Millisecond,
		Frames: []*image.RGBA{
			image.NewRGBA(image.Rect(0, 0, 2, 2)),
			image.NewRGBA(image.Rect(0, 0, 2, 2)),
			image.NewRGBA(image.Rect(0, 0, 2, 2)),
		},
	}

	if idx, until := asset.FrameAt(0); idx != 0 || until != 50*time.Millisecond {
		t.Fatalf("FrameAt(0) = (%d, %v), want (0, 50ms)", idx, until)
	}
	if idx, until := asset.FrameAt(60 * time.Millisecond); idx != 1 || until != 90*time.Millisecond {
		t.Fatalf("FrameAt(60ms) = (%d, %v), want (1, 90ms)", idx, until)
	}
	if idx, until := asset.FrameAt(360 * time.Millisecond); idx != 2 || until != 40*time.Millisecond {
		t.Fatalf("FrameAt(360ms) = (%d, %v), want (2, 40ms)", idx, until)
	}

	// Verify idle animator schedules 0 ticks when nothing is visible
	appObj := test.NewApp()
	defer appObj.Quit()
	cache := NewEmoteCache(t.TempDir(), nil)
	defer cache.Close()
	if cache.animator.TickCount() != 0 {
		t.Fatalf("expected 0 initial ticks, got %d", cache.animator.TickCount())
	}
	time.Sleep(80 * time.Millisecond)
	if cache.animator.TickCount() != 0 {
		t.Fatalf("expected 0 ticks when no animated widget is visible, got %d", cache.animator.TickCount())
	}
}

func TestLogsViewOnScreenAnimatedEmoteGatingAndPopOut(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	w := appObj.NewWindow("LogsAnimTest")

	tmpDir := t.TempDir()
	backend, err := eventlog.NewSQLiteBackend(filepath.Join(tmpDir, "events.db"))
	if err != nil {
		t.Fatalf("NewSQLiteBackend: %v", err)
	}
	mgr := eventlog.NewManager(eventlog.RetentionPolicy{ChatRetentionDays: 30, EventRetentionDays: 90}, backend)
	defer func() { _ = mgr.Close() }()

	cfg := config.DefaultConfig()
	sess := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
	sess.SetEventLogger(mgr)
	defer func() { _ = sess.Close() }()

	gifBytes := buildTestAnimatedGIF(t, 8, 8,
		[]color.RGBA{{R: 255, A: 255}, {G: 255, A: 255}, {B: 255, A: 255}},
		[]int{5, 5, 5},
		[]byte{gif.DisposalNone, gif.DisposalNone, gif.DisposalNone},
	)
	const emoteURL = "https://static-cdn.jtvnw.net/emoticons/v2/anim1/default/dark/1.0"
	cache := NewEmoteCache(t.TempDir(), nil)
	defer cache.Close()
	cache.Put(emoteURL, fyne.NewStaticResource("anim1.gif", gifBytes))

	ctx := context.Background()
	now := time.Now().UTC()
	for i := range 30 {
		if err := mgr.Append(ctx, &eventlog.Entry{
			ID:        fmt.Sprintf("ev-%02d", i),
			CreatedAt: now.Add(time.Duration(i) * time.Second),
			Platform:  "twitch",
			Kind:      "message",
			UserName:  fmt.Sprintf("user_%02d", i),
			RawText:   "Hello Anim!",
			Emotes: []chat.EmoteSpan{
				{Start: 6, End: 10, ID: "anim1", Name: "Anim", URL: emoteURL},
			},
			PassedFilters: true,
			Spoken:        true,
			Outcome:       eventlog.OutcomeSpoken,
		}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	lv := NewLogsView(w, sess)
	lv.SetEmoteCache(cache)
	defer lv.Close()
	w.SetContent(lv.Build())
	w.Resize(fyne.NewSize(960, 460))

	// 1. SubTabs starts on "Live Logs" (index 0) -> Search History animations must be paused
	if cnt := cache.VisibleAnimatedCount(); cnt != 0 {
		t.Fatalf("expected 0 visible animated emotes while on Live Logs sub-tab, got %d", cnt)
	}

	// 2. Switch to Search History (index 1) -> only on-screen subset should animate (strictly >0 and <30)
	lv.subTabs.SelectIndex(1)
	onScreenCount := cache.VisibleAnimatedCount()
	if onScreenCount <= 0 || onScreenCount >= 30 {
		t.Fatalf("expected partial viewport gating (0 < visible < 30), got %d", onScreenCount)
	}

	// Verify bottom-most row (ev-00, oldest) is currently off-screen while top-most (ev-29, newest) is on-screen
	lv.mu.Lock()
	newestRow := lv.rowCache["ev-29"]
	oldestRow := lv.rowCache["ev-00"]
	lv.mu.Unlock()
	if newestRow == nil || newestRow.emoteCell == nil || !newestRow.emoteCell.onScreen {
		t.Fatal("expected newest top row ev-29 to be marked on-screen")
	}
	if oldestRow == nil || oldestRow.emoteCell == nil || oldestRow.emoteCell.onScreen {
		t.Fatal("expected oldest bottom row ev-00 to be marked off-screen initially")
	}

	// 3. Scroll all the way down -> top row leaves screen, bottom row enters screen
	lv.listScroll.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DY: -5000}})
	if newestRow.emoteCell.onScreen {
		t.Fatal("expected newest top row ev-29 to be off-screen after scrolling down")
	}
	if !oldestRow.emoteCell.onScreen {
		t.Fatal("expected oldest bottom row ev-00 to be on-screen after scrolling down")
	}

	// 4. Advance ticks while visible, then disable animation -> resets to frame 0 and stops advancing
	time.Sleep(130 * time.Millisecond)
	if cache.animator.TickCount() == 0 {
		t.Fatal("expected animator to tick while widgets are on screen")
	}
	cache.SetAnimationEnabled(false)
	for _, obj := range oldestRow.emoteCell.flowBox.Objects {
		if ew, ok := obj.(*emoteInlineWidget); ok {
			ew.mu.RLock()
			fidx := ew.frameIdx
			ew.mu.RUnlock()
			if fidx != 0 {
				t.Fatalf("expected frameIdx reset to 0 after SetAnimationEnabled(false), got %d", fidx)
			}
		}
	}
	cache.SetAnimationEnabled(true)

	// 5. Switch main Logs tab inactive -> drops visible count to 0
	lv.SetTabActive(false)
	if cnt := cache.VisibleAnimatedCount(); cnt != 0 {
		t.Fatalf("expected 0 visible animated emotes when Logs tab inactive, got %d", cnt)
	}

	// 6. Pop-out Live Log window animates independently even while main Logs tab is inactive
	preset := config.DefaultLiveLogPresets()[0]
	popWin := lv.OpenLiveLogWindow(preset)
	if popWin == nil {
		t.Fatal("expected non-nil pop-out live log window")
	}
	popWin.Resize(fyne.NewSize(920, 420))
	if cnt := cache.VisibleAnimatedCount(); cnt <= 0 || cnt >= 30 {
		t.Fatalf("expected pop-out window viewport gating (0 < visible < 30), got %d", cnt)
	}
	popWin.Close()
	if cnt := cache.VisibleAnimatedCount(); cnt != 0 {
		t.Fatalf("expected 0 visible animated emotes after closing pop-out window, got %d", cnt)
	}
}

func TestDashboardViewOnScreenAnimatedEmoteGating(t *testing.T) {
	appObj := test.NewApp()
	defer appObj.Quit()
	w := appObj.NewWindow("DashAnimTest")

	cfg := config.DefaultConfig()
	sess := app.NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
	defer func() { _ = sess.Close() }()

	gifBytes := buildTestAnimatedGIF(t, 8, 8,
		[]color.RGBA{{R: 255, A: 255}, {G: 255, A: 255}},
		[]int{5, 5},
		[]byte{gif.DisposalNone, gif.DisposalNone},
	)
	const emoteURL = "https://static-cdn.jtvnw.net/emoticons/v2/dashanim/default/dark/1.0"
	cache := NewEmoteCache(t.TempDir(), nil)
	defer cache.Close()
	cache.Put(emoteURL, fyne.NewStaticResource("dashanim.gif", gifBytes))

	dash := NewDashboardView(w, sess)
	dash.SetEmoteCache(cache)
	defer dash.Close()
	w.SetContent(dash.Build())
	w.Resize(fyne.NewSize(900, 520))

	for i := range 20 {
		dash.AddActivity(ActivityEntry{
			ID:        fmt.Sprintf("d-%02d", i),
			Timestamp: time.Now(),
			Platform:  "twitch",
			UserName:  fmt.Sprintf("chatter_%02d", i),
			Text:      "Hi Anim!",
			Emotes:    []chat.EmoteSpan{{Start: 3, End: 7, ID: "dashanim", Name: "Anim", URL: emoteURL}},
			Kind:      "message",
		})
	}

	vis := cache.VisibleAnimatedCount()
	if vis <= 0 || vis >= 20 {
		t.Fatalf("expected partial dashboard viewport gating (0 < visible < 20), got %d", vis)
	}

	dash.SetTabActive(false)
	if cnt := cache.VisibleAnimatedCount(); cnt != 0 {
		t.Fatalf("expected 0 visible animated emotes when Dashboard tab inactive, got %d", cnt)
	}

	dash.SetTabActive(true)
	if cnt := cache.VisibleAnimatedCount(); cnt <= 0 {
		t.Fatalf("expected dashboard animated emotes to resume on SetTabActive(true), got %d", cnt)
	}

	dash.Close()
	if cnt := cache.VisibleAnimatedCount(); cnt != 0 {
		t.Fatalf("expected 0 visible animated emotes after DashboardView.Close(), got %d", cnt)
	}
}
