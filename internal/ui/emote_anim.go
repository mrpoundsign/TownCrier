package ui

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	_ "image/jpeg" // Register JPEG decoder for static emotes/thumbnails
	"image/png"
	"io"
	"sort"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/riff"
	"golang.org/x/image/webp"
)

const (
	emoteMaxFrameDim       = 64
	emoteMaxAnimFrames     = 120
	emoteMaxCanvasDim      = 2048
	emoteMaxAnimBytes      = int64(32 << 20)
	emoteMinFrameDelay     = 50 * time.Millisecond
	emoteDefaultFrameDelay = 100 * time.Millisecond
)

var (
	fccWebP = riff.FourCC{'W', 'E', 'B', 'P'}
	fccVP8X = riff.FourCC{'V', 'P', '8', 'X'}
	fccANIM = riff.FourCC{'A', 'N', 'I', 'M'}
	fccANMF = riff.FourCC{'A', 'N', 'M', 'F'}
)

// EmoteAsset holds decoded static and animated frame data for a cached emote URL.
type EmoteAsset struct {
	StaticResource fyne.Resource
	Frames         []*image.RGBA
	Delays         []time.Duration
	CumulativeEnds []time.Duration
	TotalDuration  time.Duration
	MemBytes       int64
}

// IsAnimated reports whether this asset contains multiple animation frames.
func (a *EmoteAsset) IsAnimated() bool {
	return a != nil && len(a.Frames) > 1 && a.TotalDuration > 0 && len(a.CumulativeEnds) == len(a.Frames)
}

// FrameAt returns the zero-based frame index for the given monotonic elapsed time
// along with the remaining duration until the next frame transition.
func (a *EmoteAsset) FrameAt(elapsed time.Duration) (int, time.Duration) {
	if !a.IsAnimated() {
		return 0, 0
	}
	if elapsed < 0 {
		elapsed = 0
	}
	pos := elapsed % a.TotalDuration
	idx := sort.Search(len(a.CumulativeEnds), func(i int) bool {
		return pos < a.CumulativeEnds[i]
	})
	if idx >= len(a.Frames) {
		idx = len(a.Frames) - 1
	}
	until := a.CumulativeEnds[idx] - pos
	if until <= 0 {
		until = emoteMinFrameDelay
	}
	return idx, until
}

func decodeEmoteAsset(cacheName string, rawBytes []byte) (*EmoteAsset, string, []byte, error) {
	if len(rawBytes) == 0 {
		return nil, "", nil, io.EOF
	}
	if len(rawBytes) >= 6 && (bytes.HasPrefix(rawBytes, []byte("GIF87a")) || bytes.HasPrefix(rawBytes, []byte("GIF89a"))) {
		return decodeGIFEmoteAsset(cacheName, rawBytes)
	}
	if len(rawBytes) >= 12 && bytes.HasPrefix(rawBytes, []byte("RIFF")) && bytes.Equal(rawBytes[8:12], []byte("WEBP")) {
		return decodeWebPEmoteAsset(cacheName, rawBytes)
	}
	return decodeStaticEmoteAsset(cacheName, rawBytes)
}

func buildStaticEmoteAsset(cacheName string, rgba *image.RGBA, existingPNG []byte) (*EmoteAsset, string, []byte, error) {
	pngBytes := existingPNG
	if len(pngBytes) == 0 {
		var err error
		pngBytes, err = encodePNGBytes(rgba)
		if err != nil {
			return nil, "", nil, err
		}
	}
	res := fyne.NewStaticResource(cacheName+".png", pngBytes)
	asset := &EmoteAsset{
		StaticResource: res,
		Frames:         []*image.RGBA{rgba},
		MemBytes:       int64(len(rgba.Pix)),
	}
	return asset, ".png", pngBytes, nil
}

func decodeStaticEmoteAsset(cacheName string, rawBytes []byte) (*EmoteAsset, string, []byte, error) {
	img, format, err := image.Decode(bytes.NewReader(rawBytes))
	if err != nil || img == nil {
		if err == nil {
			err = errors.New("emote decode produced nil image")
		}
		return nil, "", nil, err
	}
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 || b.Dx() > emoteMaxCanvasDim || b.Dy() > emoteMaxCanvasDim {
		return nil, "", nil, errors.New("emote dimensions out of bounds")
	}
	rgba := scaleToRGBA(img, emoteMaxFrameDim)
	var reusePNG []byte
	if format == "png" && rgba.Bounds().Dx() == b.Dx() && rgba.Bounds().Dy() == b.Dy() {
		reusePNG = rawBytes
	}
	return buildStaticEmoteAsset(cacheName, rgba, reusePNG)
}

func decodeGIFEmoteAsset(cacheName string, rawBytes []byte) (*EmoteAsset, string, []byte, error) {
	g, err := gif.DecodeAll(bytes.NewReader(rawBytes))
	if err != nil || len(g.Image) == 0 {
		if err == nil {
			err = errors.New("empty gif frames")
		}
		return nil, "", nil, err
	}

	cw, ch := g.Config.Width, g.Config.Height
	if cw <= 0 || ch <= 0 {
		b0 := g.Image[0].Bounds()
		cw, ch = b0.Dx(), b0.Dy()
	}
	if cw <= 0 || ch <= 0 || cw > emoteMaxCanvasDim || ch > emoteMaxCanvasDim {
		return nil, "", nil, errors.New("gif dimensions out of bounds")
	}

	canvasImg := image.NewRGBA(image.Rect(0, 0, cw, ch))
	if len(g.Image) == 1 {
		draw.Draw(canvasImg, g.Image[0].Bounds(), g.Image[0], g.Image[0].Bounds().Min, draw.Over)
		rgba := scaleToRGBA(canvasImg, emoteMaxFrameDim)
		return buildStaticEmoteAsset(cacheName, rgba, nil)
	}

	limit := min(len(g.Image), emoteMaxAnimFrames)
	frames := make([]*image.RGBA, 0, limit)
	delays := make([]time.Duration, 0, limit)
	cumulative := make([]time.Duration, 0, limit)
	var prevCanvas *image.RGBA
	var total time.Duration
	var memBytes int64

	for i := range limit {
		frame := g.Image[i]
		if frame == nil {
			continue
		}
		disposal := byte(gif.DisposalNone)
		if i < len(g.Disposal) {
			disposal = g.Disposal[i]
		}
		if disposal == gif.DisposalPrevious {
			if prevCanvas == nil {
				prevCanvas = image.NewRGBA(canvasImg.Bounds())
			}
			copy(prevCanvas.Pix, canvasImg.Pix)
		}

		draw.Draw(canvasImg, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)

		scaled := scaleToRGBA(canvasImg, emoteMaxFrameDim)
		d := emoteDefaultFrameDelay
		if i < len(g.Delay) {
			d = normalizeFrameDelay(time.Duration(g.Delay[i]) * 10 * time.Millisecond)
		}
		total += d
		frames = append(frames, scaled)
		delays = append(delays, d)
		cumulative = append(cumulative, total)
		memBytes += int64(len(scaled.Pix))

		switch disposal {
		case gif.DisposalBackground:
			draw.Draw(canvasImg, frame.Bounds(), image.Transparent, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			copy(canvasImg.Pix, prevCanvas.Pix)
		}
	}

	if len(frames) == 0 {
		return nil, "", nil, errors.New("gif contained no usable frames")
	}
	if len(frames) == 1 {
		return buildStaticEmoteAsset(cacheName, frames[0], nil)
	}

	staticPNG, err := encodePNGBytes(frames[0])
	if err != nil {
		return nil, "", nil, err
	}
	asset := &EmoteAsset{
		StaticResource: fyne.NewStaticResource(cacheName+".png", staticPNG),
		Frames:         frames,
		Delays:         delays,
		CumulativeEnds: cumulative,
		TotalDuration:  total,
		MemBytes:       memBytes,
	}
	return asset, ".gif", rawBytes, nil
}

func decodeWebPEmoteAsset(cacheName string, rawBytes []byte) (*EmoteAsset, string, []byte, error) {
	formType, rr, err := riff.NewReader(bytes.NewReader(rawBytes))
	if err != nil || formType != fccWebP {
		if err == nil {
			err = errors.New("invalid webp riff container")
		}
		return nil, "", nil, err
	}

	var (
		hasAnim  bool
		seenANIM bool
		canvasW  int
		canvasH  int
		bgColor  color.NRGBA
		canvas   *image.RGBA
		frames   []*image.RGBA
		delays   []time.Duration
		cumEnds  []time.Duration
		total    time.Duration
		memBytes int64
	)

	for {
		chunkID, chunkLen, chunkData, nextErr := rr.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return nil, "", nil, nextErr
		}

		switch chunkID {
		case fccVP8X:
			if chunkLen != 10 {
				return nil, "", nil, errors.New("invalid webp VP8X chunk")
			}
			var buf [10]byte
			if _, readErr := io.ReadFull(chunkData, buf[:]); readErr != nil {
				return nil, "", nil, readErr
			}
			hasAnim = (buf[0] & (1 << 1)) != 0
			canvasW = int(uint24LE(buf[4:7])) + 1
			canvasH = int(uint24LE(buf[7:10])) + 1
			if canvasW <= 0 || canvasH <= 0 || canvasW > emoteMaxCanvasDim || canvasH > emoteMaxCanvasDim {
				return nil, "", nil, errors.New("webp canvas dimensions out of bounds")
			}
			if !hasAnim {
				return decodeStaticWebP(cacheName, rawBytes)
			}

		case fccANIM:
			if chunkLen < 6 {
				return nil, "", nil, errors.New("invalid webp ANIM chunk")
			}
			var buf [6]byte
			if _, readErr := io.ReadFull(chunkData, buf[:]); readErr != nil {
				return nil, "", nil, readErr
			}
			// WebP ANIM background color is stored in BGRA byte order.
			bgColor = color.NRGBA{R: buf[2], G: buf[1], B: buf[0], A: buf[3]}
			if bgColor.A == 0 {
				bgColor = color.NRGBA{}
			}
			seenANIM = true

		case fccANMF:
			if !hasAnim || !seenANIM || canvasW <= 0 || canvasH <= 0 {
				return nil, "", nil, errors.New("malformed animated webp ANMF chunk")
			}
			if len(frames) >= emoteMaxAnimFrames {
				continue
			}
			if chunkLen < 16 {
				return nil, "", nil, errors.New("truncated webp ANMF chunk")
			}
			var hdr [16]byte
			if _, readErr := io.ReadFull(chunkData, hdr[:]); readErr != nil {
				return nil, "", nil, readErr
			}
			fx := int(uint24LE(hdr[0:3])) * 2
			fy := int(uint24LE(hdr[3:6])) * 2
			fw := int(uint24LE(hdr[6:9])) + 1
			fh := int(uint24LE(hdr[9:12])) + 1
			durMs := uint24LE(hdr[12:15])
			flags := hdr[15]
			disposeToBg := (flags & 0x01) != 0
			noBlend := (flags & 0x02) != 0

			if fw <= 0 || fh <= 0 || fw > emoteMaxCanvasDim || fh > emoteMaxCanvasDim {
				return nil, "", nil, errors.New("webp ANMF frame dimensions out of bounds")
			}

			subChunks, readErr := io.ReadAll(io.LimitReader(chunkData, 4<<20))
			if readErr != nil || len(subChunks) < 8 {
				return nil, "", nil, errors.New("invalid webp ANMF bitstream")
			}
			singleFrame := wrapSingleFrameWebP(subChunks, fw, fh)
			frameImg, decErr := webp.Decode(bytes.NewReader(singleFrame))
			if decErr != nil || frameImg == nil {
				if decErr == nil {
					decErr = errors.New("webp frame decoded nil image")
				}
				return nil, "", nil, decErr
			}

			if canvas == nil {
				canvas = image.NewRGBA(image.Rect(0, 0, canvasW, canvasH))
			}
			frameRect := image.Rect(fx, fy, fx+fw, fy+fh)
			op := draw.Over
			if noBlend {
				op = draw.Src
			}
			draw.Draw(canvas, frameRect, frameImg, frameImg.Bounds().Min, op)

			scaled := scaleToRGBA(canvas, emoteMaxFrameDim)
			d := normalizeFrameDelay(time.Duration(durMs) * time.Millisecond)
			total += d
			frames = append(frames, scaled)
			delays = append(delays, d)
			cumEnds = append(cumEnds, total)
			memBytes += int64(len(scaled.Pix))

			if disposeToBg {
				if bgColor.A == 0 {
					draw.Draw(canvas, frameRect, image.Transparent, image.Point{}, draw.Src)
				} else {
					draw.Draw(canvas, frameRect, image.NewUniform(bgColor), image.Point{}, draw.Src)
				}
			}

		default:
			if !hasAnim && (chunkID == riff.FourCC{'V', 'P', '8', ' '} || chunkID == riff.FourCC{'V', 'P', '8', 'L'}) {
				return decodeStaticWebP(cacheName, rawBytes)
			}
		}
	}

	if len(frames) == 0 {
		return decodeStaticWebP(cacheName, rawBytes)
	}
	if len(frames) == 1 {
		return buildStaticEmoteAsset(cacheName, frames[0], nil)
	}

	staticPNG, err := encodePNGBytes(frames[0])
	if err != nil {
		return nil, "", nil, err
	}
	asset := &EmoteAsset{
		StaticResource: fyne.NewStaticResource(cacheName+".png", staticPNG),
		Frames:         frames,
		Delays:         delays,
		CumulativeEnds: cumEnds,
		TotalDuration:  total,
		MemBytes:       memBytes,
	}
	return asset, ".webp", rawBytes, nil
}

func decodeStaticWebP(cacheName string, rawBytes []byte) (*EmoteAsset, string, []byte, error) {
	img, err := webp.Decode(bytes.NewReader(rawBytes))
	if err != nil || img == nil {
		if err == nil {
			err = errors.New("static webp decoded nil image")
		}
		return nil, "", nil, err
	}
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 || b.Dx() > emoteMaxCanvasDim || b.Dy() > emoteMaxCanvasDim {
		return nil, "", nil, errors.New("webp dimensions out of bounds")
	}
	rgba := scaleToRGBA(img, emoteMaxFrameDim)
	return buildStaticEmoteAsset(cacheName, rgba, nil)
}

func wrapSingleFrameWebP(subChunks []byte, fw, fh int) []byte {
	hasALPH := len(subChunks) >= 4 && string(subChunks[:4]) == "ALPH"
	innerLen := len(subChunks)
	if hasALPH {
		innerLen += 18
	}
	out := make([]byte, 0, 12+innerLen)
	out = append(out, 'R', 'I', 'F', 'F')
	out = binary.LittleEndian.AppendUint32(out, uint32(4+innerLen))
	out = append(out, 'W', 'E', 'B', 'P')
	if hasALPH {
		out = append(out, 'V', 'P', '8', 'X')
		out = binary.LittleEndian.AppendUint32(out, 10)
		out = append(out, 1<<4, 0, 0, 0)
		out = appendUint24LE(out, uint32(fw-1))
		out = appendUint24LE(out, uint32(fh-1))
	}
	out = append(out, subChunks...)
	return out
}

func uint24LE(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16
}

func appendUint24LE(dst []byte, v uint32) []byte {
	return append(dst, byte(v), byte(v>>8), byte(v>>16))
}

func normalizeFrameDelay(d time.Duration) time.Duration {
	if d <= 10*time.Millisecond {
		return emoteDefaultFrameDelay
	}
	return d
}

func scaleToRGBA(src image.Image, maxDim int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	dstW, dstH := w, h
	if maxDim > 0 && (w > maxDim || h > maxDim) {
		if w >= h {
			dstW = maxDim
			dstH = max(1, (h*maxDim+w/2)/w)
		} else {
			dstH = maxDim
			dstW = max(1, (w*maxDim+h/2)/h)
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	if dstW != w || dstH != h {
		xdraw.BiLinear.Scale(dst, dst.Bounds(), src, b, xdraw.Over, nil)
	} else {
		draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	}
	return dst
}

func encodePNGBytes(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type animWidgetState struct {
	asset     *EmoteAsset
	lastFrame int
}

type frameUpdate struct {
	w        *emoteInlineWidget
	frameIdx int
	frame    *image.RGBA
}

// emoteAnimator coordinates animation playback across all visible animated emote widgets
// using a single shared timer, lockstep timestamps, and a 20 FPS maximum update rate.
// Lock order: uiRenderMu -> view mu -> emoteInlineWidget.mu -> emoteAnimator.mu.
// emoteAnimator.mu must never be held across fyne.Do calls.
type emoteAnimator struct {
	mu         sync.Mutex
	enabled    bool
	closed     bool
	inFlight   bool
	epoch      time.Time
	visible    map[*emoteInlineWidget]*animWidgetState
	timer      *time.Timer
	nextFireAt time.Time
	tickSeq    uint64
	tickCount  uint64
}

func newEmoteAnimator() *emoteAnimator {
	return &emoteAnimator{
		enabled: true,
		epoch:   time.Now(),
		visible: make(map[*emoteInlineWidget]*animWidgetState),
	}
}

func (a *emoteAnimator) CurrentElapsed() time.Duration {
	if a == nil {
		return 0
	}
	if d := time.Since(a.epoch); d > 0 {
		return d
	}
	return 0
}

func (a *emoteAnimator) SetVisible(w *emoteInlineWidget, asset *EmoteAsset, visible bool, shownFrame int) {
	if a == nil || w == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	if visible && asset.IsAnimated() {
		a.visible[w] = &animWidgetState{asset: asset, lastFrame: shownFrame}
		if a.enabled && !a.inFlight {
			now := time.Now()
			delay := a.nextDelayLocked(now)
			wantAt := now.Add(delay)
			if a.timer == nil || wantAt.Before(a.nextFireAt) {
				a.armTimerLocked(now, delay)
			}
		}
		return
	}

	delete(a.visible, w)
	if len(a.visible) == 0 && a.timer != nil {
		a.timer.Stop()
		a.timer = nil
		a.nextFireAt = time.Time{}
		a.tickSeq++
	}
}

func (a *emoteAnimator) SetEnabled(enabled bool) {
	if a == nil {
		return
	}
	var resets []frameUpdate

	a.mu.Lock()
	if a.enabled == enabled {
		a.mu.Unlock()
		return
	}
	a.enabled = enabled
	if !enabled {
		if a.timer != nil {
			a.timer.Stop()
			a.timer = nil
		}
		a.nextFireAt = time.Time{}
		a.tickSeq++
		for w, st := range a.visible {
			if st.lastFrame != 0 && st.asset != nil && len(st.asset.Frames) > 0 {
				st.lastFrame = 0
				resets = append(resets, frameUpdate{
					w:        w,
					frameIdx: 0,
					frame:    st.asset.Frames[0],
				})
			}
		}
		a.mu.Unlock()
		if len(resets) > 0 {
			fyne.Do(func() {
				uiRenderMu.Lock()
				for _, u := range resets {
					u.w.applyAnimationFrame(u.frameIdx, u.frame)
				}
				uiRenderMu.Unlock()
			})
		}
		return
	}

	if !a.closed && len(a.visible) > 0 && a.timer == nil && !a.inFlight {
		now := time.Now()
		a.armTimerLocked(now, a.nextDelayLocked(now))
	}
	a.mu.Unlock()
}

func (a *emoteAnimator) IsEnabled() bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.enabled
}

func (a *emoteAnimator) VisibleCount() int {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.visible)
}

func (a *emoteAnimator) TickCount() uint64 {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tickCount
}

func (a *emoteAnimator) Close() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.closed = true
	if a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
	a.nextFireAt = time.Time{}
	a.tickSeq++
	clear(a.visible)
	a.mu.Unlock()
}

func (a *emoteAnimator) nextDelayLocked(now time.Time) time.Duration {
	elapsed := max(now.Sub(a.epoch), 0)
	minNext := 24 * time.Hour
	for _, st := range a.visible {
		if !st.asset.IsAnimated() {
			continue
		}
		idx, until := st.asset.FrameAt(elapsed)
		wait := until
		if idx != st.lastFrame {
			wait = emoteMinFrameDelay
		} else if wait < emoteMinFrameDelay {
			wait = emoteMinFrameDelay
		}
		if wait < minNext {
			minNext = wait
		}
	}
	if minNext < emoteMinFrameDelay {
		return emoteMinFrameDelay
	}
	return minNext
}

func (a *emoteAnimator) armTimerLocked(now time.Time, delay time.Duration) {
	if a.timer != nil {
		a.timer.Stop()
	}
	a.tickSeq++
	seq := a.tickSeq
	a.nextFireAt = now.Add(delay)
	a.timer = time.AfterFunc(delay, func() {
		a.onTick(seq)
	})
}

func (a *emoteAnimator) onTick(seq uint64) {
	a.mu.Lock()
	if a.closed || !a.enabled || a.tickSeq != seq || len(a.visible) == 0 {
		if a.tickSeq == seq {
			a.timer = nil
			a.nextFireAt = time.Time{}
		}
		a.mu.Unlock()
		return
	}

	now := time.Now()
	elapsed := max(now.Sub(a.epoch), 0)

	var updates []frameUpdate
	for w, st := range a.visible {
		if !st.asset.IsAnimated() {
			continue
		}
		idx, _ := st.asset.FrameAt(elapsed)
		if idx != st.lastFrame && idx >= 0 && idx < len(st.asset.Frames) {
			st.lastFrame = idx
			updates = append(updates, frameUpdate{
				w:        w,
				frameIdx: idx,
				frame:    st.asset.Frames[idx],
			})
		}
	}

	a.timer = nil
	a.nextFireAt = time.Time{}
	a.inFlight = true
	a.tickCount++
	a.mu.Unlock()

	fyne.Do(func() {
		if len(updates) > 0 {
			uiRenderMu.Lock()
			for _, u := range updates {
				u.w.applyAnimationFrame(u.frameIdx, u.frame)
			}
			uiRenderMu.Unlock()
		}

		a.mu.Lock()
		a.inFlight = false
		if !a.closed && a.enabled && len(a.visible) > 0 {
			fireNow := time.Now()
			a.armTimerLocked(fireNow, a.nextDelayLocked(fireNow))
		}
		a.mu.Unlock()
	})
}

// applyAnimationFrame updates only the underlying canvas.Image frame (never re-laying out the row).
func (w *emoteInlineWidget) applyAnimationFrame(idx int, frame *image.RGBA) {
	if w == nil || frame == nil {
		return
	}
	w.mu.Lock()
	r := w.renderer
	onScreen := w.onScreen
	asset := w.asset
	if r == nil || !onScreen || asset == nil {
		w.mu.Unlock()
		return
	}
	w.frameIdx = idx
	img := r.img
	w.mu.Unlock()

	if img != nil {
		img.Image = frame
		img.Resource = nil
		img.ScaleMode = canvas.ImageScaleFastest
		img.Refresh()
	}
}
