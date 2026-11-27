package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/pkg/chat"
)

const (
	emoteInlineSize  float32 = 20
	emoteLineHeight  float32 = 22
	emoteWordSpacing float32 = 4
)

var (
	defaultEmoteCacheOnce sync.Once
	defaultEmoteCacheInst *EmoteCache
)

// DefaultEmoteCache returns the process-wide shared emote image cache.
func DefaultEmoteCache() *EmoteCache {
	defaultEmoteCacheOnce.Do(func() {
		cacheDir := ""
		if base, err := os.UserCacheDir(); err == nil && base != "" {
			cacheDir = filepath.Join(base, "com.github.mrpoundsign.towncrier", "emotes")
		}
		defaultEmoteCacheInst = NewEmoteCache(cacheDir, nil)
	})
	return defaultEmoteCacheInst
}

// EmoteCache caches downloaded platform emote images (static and animated) in memory and on disk.
type EmoteCache struct {
	mu           sync.Mutex
	cacheDir     string
	httpClient   *http.Client
	mem          map[string]*EmoteAsset
	lastUsed     map[string]uint64
	accessSeq    uint64
	animBytes    int64
	maxAnimBytes int64
	inFlight     map[string][]func(*EmoteAsset)
	failedAt     map[string]time.Time
	sem          chan struct{}
	animator     *emoteAnimator
}

// NewEmoteCache creates a new EmoteCache backed by cacheDir and httpClient.
func NewEmoteCache(cacheDir string, client *http.Client) *EmoteCache {
	if client == nil {
		client = &http.Client{Timeout: 6 * time.Second}
	}
	if cacheDir != "" {
		_ = os.MkdirAll(cacheDir, 0o755)
	}
	return &EmoteCache{
		cacheDir:     cacheDir,
		httpClient:   client,
		mem:          make(map[string]*EmoteAsset),
		lastUsed:     make(map[string]uint64),
		maxAnimBytes: emoteMaxAnimBytes,
		inFlight:     make(map[string][]func(*EmoteAsset)),
		failedAt:     make(map[string]time.Time),
		sem:          make(chan struct{}, 6),
		animator:     newEmoteAnimator(),
	}
}

func emoteCacheStem(rawURL string) string {
	norm := chat.NormalizeTwitchEmoteURL(rawURL)
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:16])
}

// SetAnimationEnabled toggles emote playback across all widgets backed by this cache.
func (c *EmoteCache) SetAnimationEnabled(enabled bool) {
	if c == nil || c.animator == nil {
		return
	}
	c.animator.SetEnabled(enabled)
}

// AnimationEnabled reports whether emote animation playback is currently enabled.
func (c *EmoteCache) AnimationEnabled() bool {
	if c == nil || c.animator == nil {
		return false
	}
	return c.animator.IsEnabled()
}

// VisibleAnimatedCount returns the count of animated emote widgets currently marked visible.
func (c *EmoteCache) VisibleAnimatedCount() int {
	if c == nil || c.animator == nil {
		return 0
	}
	return c.animator.VisibleCount()
}

// Close stops the emote animation scheduler for this cache.
func (c *EmoteCache) Close() {
	if c == nil || c.animator == nil {
		return
	}
	c.animator.Close()
}

func (c *EmoteCache) storeMemLocked(stem string, asset *EmoteAsset) {
	if asset == nil {
		return
	}
	if prev, exists := c.mem[stem]; exists && prev != nil && prev.IsAnimated() {
		c.animBytes -= prev.MemBytes
	}
	c.mem[stem] = asset
	c.accessSeq++
	c.lastUsed[stem] = c.accessSeq
	if asset.IsAnimated() {
		c.animBytes += asset.MemBytes
		c.evictAnimatedLRULocked(stem)
	}
}

func (c *EmoteCache) evictAnimatedLRULocked(keepStem string) {
	if c.maxAnimBytes <= 0 {
		return
	}
	for c.animBytes > c.maxAnimBytes {
		var oldestStem string
		var oldestSeq uint64
		for stem, asset := range c.mem {
			if stem == keepStem || asset == nil || !asset.IsAnimated() {
				continue
			}
			seq := c.lastUsed[stem]
			if oldestStem == "" || seq < oldestSeq {
				oldestStem = stem
				oldestSeq = seq
			}
		}
		if oldestStem == "" {
			break
		}
		if victim := c.mem[oldestStem]; victim != nil {
			c.animBytes -= victim.MemBytes
		}
		delete(c.mem, oldestStem)
		delete(c.lastUsed, oldestStem)
	}
}

// Put stores a pre-loaded emote resource for a URL (useful in unit tests).
func (c *EmoteCache) Put(rawURL string, res fyne.Resource) {
	if c == nil || rawURL == "" || res == nil {
		return
	}
	stem := emoteCacheStem(rawURL)
	var asset *EmoteAsset
	if content := res.Content(); len(content) > 0 {
		if decoded, _, _, err := decodeEmoteAsset(stem, content); err == nil && decoded != nil {
			asset = decoded
		}
	}
	if asset == nil {
		asset = &EmoteAsset{StaticResource: res}
	}
	c.mu.Lock()
	c.storeMemLocked(stem, asset)
	c.mu.Unlock()
}

// GetOrFetch returns the static cached emote resource immediately if available in memory or on disk.
// Otherwise it initiates a deduplicated asynchronous HTTP fetch and invokes onLoaded on the Fyne UI thread.
func (c *EmoteCache) GetOrFetch(rawURL string, onLoaded func(fyne.Resource)) fyne.Resource {
	var cb func(*EmoteAsset)
	if onLoaded != nil {
		cb = func(a *EmoteAsset) {
			if a != nil && a.StaticResource != nil {
				onLoaded(a.StaticResource)
			}
		}
	}
	asset := c.GetOrFetchAsset(rawURL, cb)
	if asset == nil {
		return nil
	}
	return asset.StaticResource
}

// GetOrFetchAsset returns the cached emote asset (static or animated) immediately if available in memory or on disk.
// Otherwise it initiates a deduplicated asynchronous HTTP fetch and invokes onLoaded on the Fyne UI thread.
func (c *EmoteCache) GetOrFetchAsset(rawURL string, onLoaded func(*EmoteAsset)) *EmoteAsset {
	u := chat.NormalizeTwitchEmoteURL(rawURL)
	if c == nil || u == "" {
		return nil
	}
	stem := emoteCacheStem(u)

	c.mu.Lock()
	if asset, ok := c.mem[stem]; ok && asset != nil {
		c.accessSeq++
		c.lastUsed[stem] = c.accessSeq
		c.mu.Unlock()
		return asset
	}
	cacheDir := c.cacheDir
	c.mu.Unlock()

	if cacheDir != "" {
		for _, suffix := range []string{"_v2.gif", "_v2.webp", "_v2.png"} {
			diskPath := filepath.Join(cacheDir, stem+suffix)
			data, err := os.ReadFile(diskPath)
			if err != nil || len(data) == 0 {
				continue
			}
			if asset, _, _, decErr := decodeEmoteAsset(stem, data); decErr == nil && asset != nil {
				c.mu.Lock()
				if existing, ok := c.mem[stem]; ok && existing != nil {
					c.accessSeq++
					c.lastUsed[stem] = c.accessSeq
					c.mu.Unlock()
					return existing
				}
				c.storeMemLocked(stem, asset)
				c.mu.Unlock()
				return asset
			}
		}
	}

	c.mu.Lock()
	if asset, ok := c.mem[stem]; ok && asset != nil {
		c.accessSeq++
		c.lastUsed[stem] = c.accessSeq
		c.mu.Unlock()
		return asset
	}
	if failTime, failed := c.failedAt[stem]; failed && time.Since(failTime) < 2*time.Minute {
		c.mu.Unlock()
		return nil
	}
	if cbs, busy := c.inFlight[stem]; busy {
		if onLoaded != nil {
			c.inFlight[stem] = append(cbs, onLoaded)
		}
		c.mu.Unlock()
		return nil
	}
	if onLoaded != nil {
		c.inFlight[stem] = []func(*EmoteAsset){onLoaded}
	} else {
		c.inFlight[stem] = nil
	}
	c.mu.Unlock()

	go c.fetchAsync(u, stem)
	return nil
}

func (c *EmoteCache) fetchAsync(rawURL, stem string) {
	c.sem <- struct{}{}
	defer func() { <-c.sem }()

	rawBytes, err := c.downloadEmoteBytes(rawURL)
	var asset *EmoteAsset
	var diskExt string
	var diskBytes []byte
	if err == nil && len(rawBytes) > 0 {
		asset, diskExt, diskBytes, err = decodeEmoteAsset(stem, rawBytes)
	}

	c.mu.Lock()
	callbacks := c.inFlight[stem]
	delete(c.inFlight, stem)

	if err != nil || asset == nil {
		c.failedAt[stem] = time.Now()
		c.mu.Unlock()
		return
	}

	c.storeMemLocked(stem, asset)
	delete(c.failedAt, stem)
	cacheDir := c.cacheDir
	c.mu.Unlock()

	if cacheDir != "" && diskExt != "" && len(diskBytes) > 0 {
		diskPath := filepath.Join(cacheDir, stem+"_v2"+diskExt)
		tmpPath := diskPath + ".tmp"
		if writeErr := os.WriteFile(tmpPath, diskBytes, 0o644); writeErr == nil {
			_ = os.Rename(tmpPath, diskPath)
		}
	}

	if len(callbacks) > 0 {
		fyne.Do(func() {
			for _, cb := range callbacks {
				if cb != nil {
					cb(asset)
				}
			}
		})
	}
}

func (c *EmoteCache) downloadEmoteBytes(rawURL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "TownCrier/1.0")
	req.Header.Set("Accept", "image/png,image/webp,image/gif,image/jpeg,*/*;q=0.8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, io.EOF
	}

	rawBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil || len(rawBytes) == 0 {
		return nil, io.EOF
	}
	return rawBytes, nil
}

// emoteInlineWidget renders a scaled inline emote image (or a compact :name: fallback while loading)
// with a mouse-over tooltip displaying the emote code.
type emoteInlineWidget struct {
	widget.BaseWidget
	mu sync.RWMutex

	cache    *EmoteCache
	name     string
	imageURL string
	scale    float32
	asset    *EmoteAsset
	frameIdx int
	onScreen bool
	renderer *emoteInlineRenderer
	onHover  func(obj fyne.CanvasObject, tip string, hovered bool)
}

var _ desktop.Hoverable = (*emoteInlineWidget)(nil)

func newScaledEmoteInlineWidget(cache *EmoteCache, name, imageURL string, scale float32, onHover func(obj fyne.CanvasObject, tip string, hovered bool)) *emoteInlineWidget {
	if cache == nil {
		cache = DefaultEmoteCache()
	}
	if scale <= 0 {
		scale = 1.0
	}
	w := &emoteInlineWidget{
		cache:    cache,
		name:     name,
		imageURL: imageURL,
		scale:    scale,
		onHover:  onHover,
	}
	w.ExtendBaseWidget(w)
	if imageURL != "" {
		initialAsset := cache.GetOrFetchAsset(imageURL, func(loaded *EmoteAsset) {
			if loaded == nil {
				return
			}
			w.mu.Lock()
			w.asset = loaded
			w.frameIdx = 0
			w.mu.Unlock()

			w.Refresh()

			w.mu.Lock()
			if w.onScreen && loaded.IsAnimated() && w.cache != nil && w.cache.animator != nil {
				w.cache.animator.SetVisible(w, loaded, true, w.frameIdx)
			}
			w.mu.Unlock()
		})
		w.mu.Lock()
		w.asset = initialAsset
		w.mu.Unlock()
	}
	return w
}

func (w *emoteInlineWidget) hasAsset() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.asset != nil && (len(w.asset.Frames) > 0 || w.asset.StaticResource != nil)
}

func (w *emoteInlineWidget) renderSnapshot() (asset *EmoteAsset, frameIdx int, animEnabled bool) {
	w.mu.RLock()
	asset = w.asset
	frameIdx = w.frameIdx
	cache := w.cache
	w.mu.RUnlock()
	return asset, frameIdx, cache != nil && cache.AnimationEnabled()
}

// SetOnScreen updates whether this emote widget is currently visible inside an active scroll viewport
// and registers/unregisters it with the EmoteCache's shared animation coordinator.
func (w *emoteInlineWidget) SetOnScreen(visible bool) {
	if w == nil {
		return
	}
	w.mu.Lock()
	if w.onScreen == visible {
		w.mu.Unlock()
		return
	}
	w.onScreen = visible
	cache := w.cache
	asset := w.asset
	animEnabled := cache != nil && cache.AnimationEnabled()
	var resetFrame *image.RGBA
	if visible && !animEnabled && w.frameIdx != 0 && asset != nil && len(asset.Frames) > 0 {
		w.frameIdx = 0
		resetFrame = asset.Frames[0]
	}
	if cache != nil && cache.animator != nil && asset.IsAnimated() {
		cache.animator.SetVisible(w, asset, visible, w.frameIdx)
	}
	w.mu.Unlock()

	if resetFrame != nil {
		go func(f *image.RGBA) {
			fyne.Do(func() {
				uiRenderMu.Lock()
				w.applyAnimationFrame(0, f)
				uiRenderMu.Unlock()
			})
		}(resetFrame)
	}
}

func (w *emoteInlineWidget) effectiveScale() float32 {
	if w.scale <= 0 {
		return 1.0
	}
	return w.scale
}

func (w *emoteInlineWidget) tooltipText() string {
	n := strings.TrimSpace(w.name)
	if n == "" {
		return "Emote"
	}
	if strings.HasPrefix(n, ":") && strings.HasSuffix(n, ":") {
		return "Emote: " + n
	}
	return "Emote: :" + n + ":"
}

func (w *emoteInlineWidget) MouseIn(*desktop.MouseEvent) {
	if w.onHover != nil {
		w.onHover(w, w.tooltipText(), true)
	}
}

func (w *emoteInlineWidget) MouseMoved(*desktop.MouseEvent) {}

func (w *emoteInlineWidget) MouseOut() {
	if w.onHover != nil {
		w.onHover(w, "", false)
	}
}

func (w *emoteInlineWidget) fallbackLabelText() string {
	n := strings.TrimSpace(w.name)
	if n == "" {
		return ":emote:"
	}
	if strings.HasPrefix(n, ":") && strings.HasSuffix(n, ":") {
		return n
	}
	return ":" + n + ":"
}

func (w *emoteInlineWidget) CreateRenderer() fyne.WidgetRenderer {
	s := w.effectiveScale()
	sz := emoteInlineSize * s
	img := &canvas.Image{
		FillMode:  canvas.ImageFillContain,
		ScaleMode: canvas.ImageScaleFastest,
	}
	img.SetMinSize(fyne.NewSize(sz, sz))

	fallback := canvas.NewText(w.fallbackLabelText(), colorSkyBlue)
	fallback.TextSize = (theme.TextSize() - 1) * s
	fallback.TextStyle = fyne.TextStyle{Bold: true}

	r := &emoteInlineRenderer{
		w:        w,
		img:      img,
		fallback: fallback,
	}
	w.mu.Lock()
	w.renderer = r
	w.mu.Unlock()

	r.Refresh()
	return r
}

type emoteInlineRenderer struct {
	w        *emoteInlineWidget
	img      *canvas.Image
	fallback *canvas.Text
}

func (r *emoteInlineRenderer) Layout(size fyne.Size) {
	sz := emoteInlineSize * r.w.effectiveScale()
	if r.w.hasAsset() {
		r.img.Resize(fyne.NewSize(sz, sz))
		r.img.Move(fyne.NewPos(
			(size.Width-sz)/2,
			(size.Height-sz)/2,
		))
	} else {
		fbMin := r.fallback.MinSize()
		r.fallback.Resize(fbMin)
		r.fallback.Move(fyne.NewPos(0, (size.Height-fbMin.Height)/2))
	}
}

func (r *emoteInlineRenderer) MinSize() fyne.Size {
	sz := emoteInlineSize * r.w.effectiveScale()
	if r.w.hasAsset() {
		return fyne.NewSize(sz, sz)
	}
	fbMin := r.fallback.MinSize()
	return fyne.NewSize(max(fbMin.Width, sz), max(fbMin.Height, sz))
}

func (r *emoteInlineRenderer) Refresh() {
	asset, idx, animEnabled := r.w.renderSnapshot()
	switch {
	case asset != nil && len(asset.Frames) > 0:
		if !animEnabled || idx < 0 || idx >= len(asset.Frames) {
			idx = 0
		}
		r.img.Resource = nil
		r.img.Image = asset.Frames[idx]
		r.img.ScaleMode = canvas.ImageScaleFastest
		r.img.Show()
		r.fallback.Hide()
		r.img.Refresh()
	case asset != nil && asset.StaticResource != nil:
		r.img.Image = nil
		r.img.Resource = asset.StaticResource
		r.img.ScaleMode = canvas.ImageScaleSmooth
		r.img.Show()
		r.fallback.Hide()
		r.img.Refresh()
	default:
		r.fallback.Text = r.w.fallbackLabelText()
		r.fallback.Show()
		r.img.Hide()
		r.fallback.Refresh()
	}
}

func (r *emoteInlineRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.img, r.fallback}
}

func (r *emoteInlineRenderer) Destroy() {
	r.w.mu.Lock()
	if r.w.renderer == r {
		r.w.renderer = nil
	}
	r.w.mu.Unlock()
	r.w.SetOnScreen(false)
}

// inlineFlowLayout arranges word labels and inline emote widgets left-to-right with automatic word wrapping.
// Its MinSize().Width is always clamped to 80px so long messages never expand the OS window width.
type inlineFlowLayout struct {
	wrapWidth float32
	scale     float32
}

func (l *inlineFlowLayout) effectiveScale() float32 {
	if l.scale <= 0 {
		return 1.0
	}
	return l.scale
}

func (l *inlineFlowLayout) effectiveWidth(currentW float32) float32 {
	if currentW > 40 {
		return currentW
	}
	if l.wrapWidth > 40 {
		return l.wrapWidth
	}
	return 360
}

func (l *inlineFlowLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	l.wrapWidth = size.Width
	maxW := l.effectiveWidth(size.Width)
	s := l.effectiveScale()
	var x, y float32
	lineH := emoteLineHeight * s
	spacing := emoteWordSpacing * s

	for _, obj := range objects {
		if obj == nil || !obj.Visible() {
			continue
		}
		sz := obj.MinSize()
		if sz.Width > maxW {
			sz.Width = maxW
		}
		if x > 0 && x+sz.Width > maxW {
			x = 0
			y += lineH + 2*s
		}
		yOffset := (lineH - sz.Height) / 2
		obj.Move(fyne.NewPos(x, y+yOffset))
		obj.Resize(sz)
		x += sz.Width + spacing
	}
}

func (l *inlineFlowLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	maxW := l.effectiveWidth(l.wrapWidth)
	s := l.effectiveScale()
	var x, y float32
	lineH := emoteLineHeight * s
	spacing := emoteWordSpacing * s
	hasVisible := false

	for _, obj := range objects {
		if obj == nil || !obj.Visible() {
			continue
		}
		hasVisible = true
		sz := obj.MinSize()
		if sz.Width > maxW {
			sz.Width = maxW
		}
		if x > 0 && x+sz.Width > maxW {
			x = 0
			y += lineH + 2*s
		}
		x += sz.Width + spacing
	}

	if !hasVisible {
		return fyne.NewSize(80, lineH)
	}
	return fyne.NewSize(80, y+lineH)
}

// inlineToken represents either a plain text word or an emote span within a message.
type inlineToken struct {
	isEmote  bool
	text     string
	name     string
	imageURL string
	bits     int
	colorHex string
}

// tokenizeMessageWithEmotes splits rawText and rune-indexed EmoteSpans into word and emote tokens.
func tokenizeMessageWithEmotes(platform, rawText string, emotes []chat.EmoteSpan) []inlineToken {
	if len(emotes) == 0 {
		return splitWordsForFlow(rawText)
	}

	sorted := make([]chat.EmoteSpan, len(emotes))
	copy(sorted, emotes)
	slices.SortFunc(sorted, func(a, b chat.EmoteSpan) int {
		return a.Start - b.Start
	})

	runes := []rune(rawText)
	var tokens []inlineToken
	pos := 0

	for _, sp := range sorted {
		if sp.Start < pos || sp.Start >= len(runes) || sp.End <= sp.Start {
			continue
		}
		end := min(sp.End, len(runes))
		if sp.Start > pos {
			prefix := string(runes[pos:sp.Start])
			tokens = append(tokens, splitWordsForFlow(prefix)...)
		}
		name := sp.Name
		if name == "" {
			name = string(runes[sp.Start:end])
		}
		tokens = append(tokens, inlineToken{
			isEmote:  true,
			name:     name,
			imageURL: sp.ImageURL(platform),
			bits:     sp.Bits,
			colorHex: sp.Color,
		})
		pos = end
	}

	if pos < len(runes) {
		suffix := string(runes[pos:])
		tokens = append(tokens, splitWordsForFlow(suffix)...)
	}

	return tokens
}

func splitWordsForFlow(s string) []inlineToken {
	fields := strings.Fields(s)
	var out []inlineToken
	const maxWordRunes = 28
	for _, f := range fields {
		// Break extremely long unbroken strings (e.g., long URLs or character spam) into chunks so they wrap
		for utf8.RuneCountInString(f) > maxWordRunes {
			r := []rune(f)
			out = append(out, inlineToken{text: string(r[:maxWordRunes])})
			f = string(r[maxWordRunes:])
		}
		if f != "" {
			out = append(out, inlineToken{text: f})
		}
	}
	return out
}

// emoteMessageCell renders a chat message with inline emote images (when EmoteSpans are present)
// or a fast wrappedCellLabel (when plain text), plus an optional muted secondary note line.
type emoteMessageCell struct {
	widget.BaseWidget

	mu         sync.Mutex
	onScreen   bool
	cache      *EmoteCache
	scale      float32
	onHover    func(obj fyne.CanvasObject, tip string, hovered bool)
	plainLabel *wrappedCellLabel
	flowLayout *inlineFlowLayout
	flowBox    *fyne.Container
	subLabel   *wrappedCellLabel
	rootBox    *fyne.Container
}

func newScaledEmoteMessageCell(cache *EmoteCache, scale float32, onHover func(obj fyne.CanvasObject, tip string, hovered bool)) *emoteMessageCell {
	if cache == nil {
		cache = DefaultEmoteCache()
	}
	if scale <= 0 {
		scale = 1.0
	}
	plain := newClampedWrapLabel("", fyne.TextStyle{})
	plain.onHover = onHover
	fl := &inlineFlowLayout{wrapWidth: 360, scale: scale}
	flow := container.New(fl)
	flow.Hide()
	sub := newClampedWrapLabel("", fyne.TextStyle{Italic: true})
	sub.Hide()

	c := &emoteMessageCell{
		cache:      cache,
		scale:      scale,
		onHover:    onHover,
		plainLabel: plain,
		flowLayout: fl,
		flowBox:    flow,
		subLabel:   sub,
		rootBox:    container.NewVBox(plain, flow, sub),
	}
	c.ExtendBaseWidget(c)
	return c
}

func (c *emoteMessageCell) effectiveScale() float32 {
	if c.scale <= 0 {
		return 1.0
	}
	return c.scale
}

func setFlowObjectsOnScreen(objs []fyne.CanvasObject, visible bool) {
	for _, obj := range objs {
		switch x := obj.(type) {
		case *emoteInlineWidget:
			x.SetOnScreen(visible)
		case *fyne.Container:
			setFlowObjectsOnScreen(x.Objects, visible)
		}
	}
}

func parseEmoteHexColor(hexStr string, fallback color.NRGBA) color.NRGBA {
	s := strings.TrimPrefix(strings.TrimSpace(hexStr), "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 && len(s) != 8 {
		return fallback
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return fallback
	}
	if len(s) == 6 {
		return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}
	}
	return color.NRGBA{R: uint8(v >> 24), G: uint8(v >> 16), B: uint8(v >> 8), A: uint8(v)}
}

// SetOnScreen propagates viewport visibility to all inline emote widgets in this message cell.
func (c *emoteMessageCell) SetOnScreen(visible bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.onScreen = visible
	objs := slices.Clone(c.flowBox.Objects)
	c.mu.Unlock()

	setFlowObjectsOnScreen(objs, visible)
}

// SetContent updates the cell to display rawText with inline emotes and an optional tooltip/subtext.
func (c *emoteMessageCell) SetContent(platform, rawText string, emotes []chat.EmoteSpan, tooltip string, subText string) {
	s := c.effectiveScale()
	c.flowLayout.scale = s
	c.plainLabel.tooltip = tooltip

	c.mu.Lock()
	prevObjs := slices.Clone(c.flowBox.Objects)
	c.mu.Unlock()
	setFlowObjectsOnScreen(prevObjs, false)

	if len(emotes) == 0 {
		c.mu.Lock()
		c.flowBox.Objects = nil
		c.mu.Unlock()
		c.flowBox.Hide()
		full := rawText
		if subText != "" {
			if full != "" {
				full += "\n" + subText
			} else {
				full = subText
			}
		}
		c.plainLabel.SetText(full)
		c.plainLabel.Show()
		c.subLabel.Hide()
		c.rootBox.Refresh()
		return
	}

	c.plainLabel.Hide()
	tokens := tokenizeMessageWithEmotes(platform, rawText, emotes)
	objs := make([]fyne.CanvasObject, 0, len(tokens))
	for _, tok := range tokens {
		if tok.isEmote {
			ew := newScaledEmoteInlineWidget(c.cache, tok.name, tok.imageURL, s, c.onHover)
			if tok.bits > 0 {
				bitsLbl := canvas.NewText(strconv.Itoa(tok.bits), parseEmoteHexColor(tok.colorHex, colorSkyBlue))
				bitsLbl.TextSize = (theme.TextSize() - 1) * s
				bitsLbl.TextStyle = fyne.TextStyle{Bold: true}
				objs = append(objs, container.NewHBox(ew, bitsLbl))
			} else {
				objs = append(objs, ew)
			}
		} else {
			lbl := canvas.NewText(tok.text, colorChromeWhite)
			lbl.TextSize = theme.TextSize() * s
			objs = append(objs, lbl)
		}
	}
	c.mu.Lock()
	onScreen := c.onScreen
	c.flowBox.Objects = objs
	c.mu.Unlock()
	if onScreen {
		setFlowObjectsOnScreen(objs, true)
	}
	c.flowBox.Show()
	c.flowBox.Refresh()

	if strings.TrimSpace(subText) != "" {
		c.subLabel.SetText(subText)
		c.subLabel.Show()
	} else {
		c.subLabel.SetText("")
		c.subLabel.Hide()
	}
	c.rootBox.Refresh()
}

func (c *emoteMessageCell) Resize(size fyne.Size) {
	if size.Width > 40 {
		c.flowLayout.wrapWidth = size.Width
		c.plainLabel.Resize(fyne.NewSize(size.Width, c.plainLabel.Size().Height))
		c.subLabel.Resize(fyne.NewSize(size.Width, c.subLabel.Size().Height))
	}
	c.BaseWidget.Resize(size)
	c.rootBox.Resize(size)
}

func (c *emoteMessageCell) MinSize() fyne.Size {
	s := c.effectiveScale()
	var h float32
	if c.plainLabel.Visible() {
		h += c.plainLabel.MinSize().Height
	}
	if c.flowBox.Visible() {
		h += c.flowLayout.MinSize(c.flowBox.Objects).Height
	}
	if c.subLabel.Visible() {
		if h > 0 {
			h += 2 * s
		}
		h += c.subLabel.MinSize().Height
	}
	minH := emoteLineHeight * s
	if h < minH {
		h = minH
	}
	return fyne.NewSize(80, h)
}

func (c *emoteMessageCell) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.rootBox)
}
