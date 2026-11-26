package ui

import (
	"bytes"
	"container/list"
	"context"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	_ "golang.org/x/image/webp"

	"github.com/mrpoundsign/TownCrier/internal/pluginhost"
)

const (
	pluginImageCacheMaxEntries = 128
	pluginImageCacheMaxBytes   = 32 * 1024 * 1024
	pluginImageMaxDownloadSize = 4 * 1024 * 1024
	pluginImageFetchTimeout    = 8 * time.Second
)

type cachedPluginImage struct {
	key  string
	img  image.Image
	size int
}

// PluginImageCache asynchronously fetches, validates, decodes, and LRU-caches remote HTTPS images
// for plugin declarative UI ImageNode elements.
type PluginImageCache struct {
	mu         sync.Mutex
	client     *http.Client
	allowHTTP  bool // reserved for loopback httptest verification
	items      map[string]*list.Element
	lru        *list.List
	totalBytes int
	inflight   map[string][]func(image.Image, error)
}

// NewPluginImageCache creates a bounded image cache for declarative plugin UI thumbnails.
func NewPluginImageCache(client *http.Client) *PluginImageCache {
	if client == nil {
		client = &http.Client{Timeout: pluginImageFetchTimeout}
	}
	return &PluginImageCache{
		client:   client,
		items:    make(map[string]*list.Element),
		lru:      list.New(),
		inflight: make(map[string][]func(image.Image, error)),
	}
}

// Fetch resolves rawURL (synchronously from LRU if cached, otherwise off-thread) and invokes cb.
func (c *PluginImageCache) Fetch(rawURL string, allowedHosts []string, enforceHosts bool, cb func(image.Image, error)) {
	cleanURL := strings.TrimSpace(rawURL)
	if cleanURL == "" {
		if cb != nil {
			cb(nil, errors.New("empty image URL"))
		}
		return
	}
	u, err := url.Parse(cleanURL)
	if err != nil || u == nil {
		if cb != nil {
			cb(nil, errors.New("invalid image URL"))
		}
		return
	}
	scheme := strings.ToLower(u.Scheme)
	allowedLocalHTTP := c.allowHTTP && scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")
	if scheme != "https" && !allowedLocalHTTP {
		if cb != nil {
			cb(nil, errors.New("plugin image URLs must use https://"))
		}
		return
	}
	if u.User != nil {
		if cb != nil {
			cb(nil, errors.New("embedded credentials in image URLs are disallowed"))
		}
		return
	}
	if enforceHosts && !pluginhost.IsHostAllowed(cleanURL, allowedHosts) {
		if cb != nil {
			cb(nil, errors.New("image host blocked by plugin allowed_hosts"))
		}
		return
	}

	c.mu.Lock()
	if el, ok := c.items[cleanURL]; ok {
		c.lru.MoveToFront(el)
		entry := el.Value.(*cachedPluginImage)
		img := entry.img
		c.mu.Unlock()
		if cb != nil {
			cb(img, nil)
		}
		return
	}
	if _, pending := c.inflight[cleanURL]; pending {
		if cb != nil {
			c.inflight[cleanURL] = append(c.inflight[cleanURL], cb)
		}
		c.mu.Unlock()
		return
	}
	var cbs []func(image.Image, error)
	if cb != nil {
		cbs = []func(image.Image, error){cb}
	}
	c.inflight[cleanURL] = cbs
	c.mu.Unlock()

	go func() {
		img, byteSize, fetchErr := c.downloadAndDecode(cleanURL)

		c.mu.Lock()
		waiters := c.inflight[cleanURL]
		delete(c.inflight, cleanURL)
		if fetchErr == nil && img != nil {
			c.insertLocked(cleanURL, img, byteSize)
		}
		c.mu.Unlock()

		for _, w := range waiters {
			if w != nil {
				w(img, fetchErr)
			}
		}
	}()
}

func (c *PluginImageCache) downloadAndDecode(rawURL string) (image.Image, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pluginImageFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, 0, errors.New("image HTTP error: " + resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, pluginImageMaxDownloadSize+1))
	if err != nil {
		return nil, 0, err
	}
	if len(data) > pluginImageMaxDownloadSize {
		return nil, 0, errors.New("image exceeds 4 MiB limit")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, err
	}
	bounds := img.Bounds()
	approxBytes := max(bounds.Dx()*bounds.Dy()*4, len(data))
	return img, approxBytes, nil
}

func (c *PluginImageCache) insertLocked(key string, img image.Image, size int) {
	if existing, ok := c.items[key]; ok {
		c.lru.Remove(existing)
		c.totalBytes -= existing.Value.(*cachedPluginImage).size
		delete(c.items, key)
	}
	el := c.lru.PushFront(&cachedPluginImage{
		key:  key,
		img:  img,
		size: size,
	})
	c.items[key] = el
	c.totalBytes += size

	for len(c.items) > pluginImageCacheMaxEntries || c.totalBytes > pluginImageCacheMaxBytes {
		back := c.lru.Back()
		if back == nil {
			break
		}
		evicted := c.lru.Remove(back).(*cachedPluginImage)
		delete(c.items, evicted.key)
		c.totalBytes -= evicted.size
	}
}

func resolvePluginIcon(name string) fyne.Resource {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "none":
		return nil
	case "play", "media_play":
		return theme.MediaPlayIcon()
	case "pause", "media_pause":
		return theme.MediaPauseIcon()
	case "stop", "media_stop":
		return theme.MediaStopIcon()
	case "skip", "next", "fast_forward", "media_fast_forward":
		return theme.MediaFastForwardIcon()
	case "prev", "rewind", "media_rewind", "media_fast_rewind":
		return theme.MediaFastRewindIcon()
	case "music", "audio", "song":
		return theme.MediaMusicIcon()
	case "video":
		return theme.MediaVideoIcon()
	case "record":
		return theme.MediaRecordIcon()
	case "volume", "volume_up":
		return theme.VolumeUpIcon()
	case "volume_down":
		return theme.VolumeDownIcon()
	case "mute", "volume_mute":
		return theme.VolumeMuteIcon()
	case "search":
		return theme.SearchIcon()
	case "add", "plus", "content_add":
		return theme.ContentAddIcon()
	case "remove", "minus":
		return theme.ContentRemoveIcon()
	case "delete", "trash":
		return theme.DeleteIcon()
	case "refresh", "reload":
		return theme.ViewRefreshIcon()
	case "settings", "gear":
		return theme.SettingsIcon()
	case "info":
		return theme.InfoIcon()
	case "warning":
		return theme.WarningIcon()
	case "error":
		return theme.ErrorIcon()
	case "check", "confirm", "approve":
		return theme.ConfirmIcon()
	case "close", "cancel":
		return theme.CancelIcon()
	case "folder", "folder_open":
		return theme.FolderOpenIcon()
	case "save", "document_save":
		return theme.DocumentSaveIcon()
	case "document", "logs":
		return theme.DocumentIcon()
	case "history":
		return theme.HistoryIcon()
	case "list", "queue":
		return theme.ListIcon()
	case "visibility", "overlay", "eye":
		return theme.VisibilityIcon()
	case "account", "user":
		return theme.AccountIcon()
	case "computer", "monitor":
		return theme.ComputerIcon()
	case "home":
		return theme.HomeIcon()
	case "help":
		return theme.HelpIcon()
	case "download":
		return theme.DownloadIcon()
	case "upload":
		return theme.UploadIcon()
	case "up", "arrow_up", "bump":
		return theme.MoveUpIcon()
	case "down", "arrow_down":
		return theme.MoveDownIcon()
	case "clear", "content_clear":
		return theme.ContentClearIcon()
	case "storage":
		return theme.StorageIcon()
	default:
		return theme.GridIcon()
	}
}
