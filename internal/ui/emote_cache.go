package ui

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	_ "image/gif"  // Register GIF decoder for animated/static emotes
	_ "image/jpeg" // Register JPEG decoder for custom thumbnails
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
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
	_ "golang.org/x/image/webp" // Register WebP decoder for Kick/YouTube emotes

	"github.com/mrpoundsign/TownCrier/internal/chat"
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

// EmoteCache caches downloaded platform emote images in memory and on disk as normalized PNGs.
type EmoteCache struct {
	mu         sync.RWMutex
	cacheDir   string
	httpClient *http.Client
	mem        map[string]fyne.Resource
	inFlight   map[string][]func(fyne.Resource)
	failedAt   map[string]time.Time
	sem        chan struct{}
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
		cacheDir:   cacheDir,
		httpClient: client,
		mem:        make(map[string]fyne.Resource),
		inFlight:   make(map[string][]func(fyne.Resource)),
		failedAt:   make(map[string]time.Time),
		sem:        make(chan struct{}, 6),
	}
}

func emoteCacheKey(rawURL string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(rawURL)))
	return hex.EncodeToString(sum[:16]) + ".png"
}

// Put stores a pre-loaded emote resource for a URL (useful in unit tests).
func (c *EmoteCache) Put(rawURL string, res fyne.Resource) {
	if c == nil || rawURL == "" || res == nil {
		return
	}
	key := emoteCacheKey(rawURL)
	c.mu.Lock()
	c.mem[key] = res
	c.mu.Unlock()
}

// GetOrFetch returns the cached emote resource immediately if available in memory or on disk.
// Otherwise it initiates a deduplicated asynchronous HTTP fetch and invokes onLoaded on the Fyne UI thread.
func (c *EmoteCache) GetOrFetch(rawURL string, onLoaded func(fyne.Resource)) fyne.Resource {
	u := strings.TrimSpace(rawURL)
	if c == nil || u == "" {
		return nil
	}
	key := emoteCacheKey(u)

	c.mu.RLock()
	if res, ok := c.mem[key]; ok {
		c.mu.RUnlock()
		return res
	}
	c.mu.RUnlock()

	// Check on-disk cache
	if c.cacheDir != "" {
		diskPath := filepath.Join(c.cacheDir, key)
		if data, err := os.ReadFile(diskPath); err == nil && len(data) > 0 {
			res := fyne.NewStaticResource(key, data)
			c.mu.Lock()
			c.mem[key] = res
			c.mu.Unlock()
			return res
		}
	}

	c.mu.Lock()
	if res, ok := c.mem[key]; ok {
		c.mu.Unlock()
		return res
	}
	if failTime, failed := c.failedAt[key]; failed && time.Since(failTime) < 2*time.Minute {
		c.mu.Unlock()
		return nil
	}
	if cbs, busy := c.inFlight[key]; busy {
		if onLoaded != nil {
			c.inFlight[key] = append(cbs, onLoaded)
		}
		c.mu.Unlock()
		return nil
	}
	if onLoaded != nil {
		c.inFlight[key] = []func(fyne.Resource){onLoaded}
	} else {
		c.inFlight[key] = nil
	}
	c.mu.Unlock()

	go c.fetchAsync(u, key)
	return nil
}

func (c *EmoteCache) fetchAsync(rawURL, key string) {
	c.sem <- struct{}{}
	defer func() { <-c.sem }()

	pngData, err := c.downloadAndNormalizePNG(rawURL)

	c.mu.Lock()
	callbacks := c.inFlight[key]
	delete(c.inFlight, key)

	if err != nil || len(pngData) == 0 {
		c.failedAt[key] = time.Now()
		c.mu.Unlock()
		return
	}

	res := fyne.NewStaticResource(key, pngData)
	c.mem[key] = res
	delete(c.failedAt, key)
	c.mu.Unlock()

	if c.cacheDir != "" {
		diskPath := filepath.Join(c.cacheDir, key)
		tmpPath := diskPath + ".tmp"
		if writeErr := os.WriteFile(tmpPath, pngData, 0o644); writeErr == nil {
			_ = os.Rename(tmpPath, diskPath)
		}
	}

	if len(callbacks) > 0 {
		fyne.Do(func() {
			for _, cb := range callbacks {
				if cb != nil {
					cb(res)
				}
			}
		})
	}
}

func (c *EmoteCache) downloadAndNormalizePNG(rawURL string) ([]byte, error) {
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

	rawBytes, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil || len(rawBytes) == 0 {
		return nil, io.EOF
	}

	// Decode any supported image format (PNG, GIF first frame, JPEG, WebP) and re-encode to PNG
	img, format, decErr := image.Decode(bytes.NewReader(rawBytes))
	if decErr != nil || img == nil {
		return nil, decErr
	}
	if format == "png" {
		return rawBytes, nil
	}

	var buf bytes.Buffer
	if encErr := png.Encode(&buf, img); encErr != nil {
		return nil, encErr
	}
	return buf.Bytes(), nil
}

// emoteInlineWidget renders a scaled inline emote image (or a compact :name: fallback while loading)
// with a mouse-over tooltip displaying the emote code.
type emoteInlineWidget struct {
	widget.BaseWidget
	mu sync.RWMutex

	name     string
	imageURL string
	scale    float32
	res      fyne.Resource
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
		name:     name,
		imageURL: imageURL,
		scale:    scale,
		onHover:  onHover,
	}
	w.ExtendBaseWidget(w)
	if imageURL != "" {
		initialRes := cache.GetOrFetch(imageURL, func(loaded fyne.Resource) {
			w.setResource(loaded)
			w.Refresh()
		})
		w.setResource(initialRes)
	}
	return w
}

func (w *emoteInlineWidget) getResource() fyne.Resource {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.res
}

func (w *emoteInlineWidget) setResource(res fyne.Resource) {
	w.mu.Lock()
	w.res = res
	w.mu.Unlock()
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
	img := canvas.NewImageFromResource(w.getResource())
	img.FillMode = canvas.ImageFillContain
	img.ScaleMode = canvas.ImageScaleSmooth
	img.SetMinSize(fyne.NewSize(sz, sz))

	fallback := canvas.NewText(w.fallbackLabelText(), colorSkyBlue)
	fallback.TextSize = (theme.TextSize() - 1) * s
	fallback.TextStyle = fyne.TextStyle{Bold: true}

	r := &emoteInlineRenderer{
		w:        w,
		img:      img,
		fallback: fallback,
	}
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
	if r.w.getResource() != nil {
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
	if r.w.getResource() != nil {
		return fyne.NewSize(sz, sz)
	}
	fbMin := r.fallback.MinSize()
	return fyne.NewSize(max(fbMin.Width, sz), max(fbMin.Height, sz))
}

func (r *emoteInlineRenderer) Refresh() {
	if res := r.w.getResource(); res != nil {
		r.img.Resource = res
		r.img.Show()
		r.fallback.Hide()
		r.img.Refresh()
	} else {
		r.fallback.Text = r.w.fallbackLabelText()
		r.fallback.Show()
		r.img.Hide()
		r.fallback.Refresh()
	}
}

func (r *emoteInlineRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.img, r.fallback}
}

func (r *emoteInlineRenderer) Destroy() {}

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

	cache      *EmoteCache
	scale      float32
	onHover    func(obj fyne.CanvasObject, tip string, hovered bool)
	plainLabel *wrappedCellLabel
	flowLayout *inlineFlowLayout
	flowBox    *fyne.Container
	subLabel   *wrappedCellLabel
	rootBox    *fyne.Container
}

func newEmoteMessageCell(cache *EmoteCache, onHover func(obj fyne.CanvasObject, tip string, hovered bool)) *emoteMessageCell {
	return newScaledEmoteMessageCell(cache, 1.0, onHover)
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

// SetContent updates the cell to display rawText with inline emotes and an optional tooltip/subtext.
func (c *emoteMessageCell) SetContent(platform, rawText string, emotes []chat.EmoteSpan, tooltip string, subText string) {
	s := c.effectiveScale()
	c.flowLayout.scale = s
	c.plainLabel.tooltip = tooltip
	if len(emotes) == 0 {
		c.flowBox.Objects = nil
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
			objs = append(objs, newScaledEmoteInlineWidget(c.cache, tok.name, tok.imageURL, s, c.onHover))
		} else {
			lbl := canvas.NewText(tok.text, colorChromeWhite)
			lbl.TextSize = theme.TextSize() * s
			objs = append(objs, lbl)
		}
	}
	c.flowBox.Objects = objs
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
