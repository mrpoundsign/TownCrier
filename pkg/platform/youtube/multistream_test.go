package youtube

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrpoundsign/TownCrier/pkg/chat"
	"github.com/mrpoundsign/TownCrier/pkg/secrets"
)

func TestExtractActiveLiveVideoIDs(t *testing.T) {
	html := `<html><script>var ytInitialData = {
		"contents": {
			"items": [
				{"richItemRenderer": {"content": {"videoRenderer": {
					"videoId": "HORIZ_VID01",
					"thumbnailOverlays": [{"thumbnailOverlayTimeStatusRenderer": {"style": "LIVE", "text": {"simpleText": "LIVE"}}}]
				}}}},
				{"richItemRenderer": {"content": {"videoRenderer": {
					"videoId": "VERT_VID002",
					"badges": [{"metadataBadgeRenderer": {"style": "BADGE_STYLE_TYPE_LIVE_NOW", "label": "LIVE"}}]
				}}}},
				{"richItemRenderer": {"content": {"videoRenderer": {
					"videoId": "UPCOMING003",
					"upcomingEventData": {"startTime": "1999999999"},
					"badges": [{"metadataBadgeRenderer": {"style": "BADGE_STYLE_TYPE_LIVE_NOW"}}]
				}}}},
				{"richItemRenderer": {"content": {"videoRenderer": {
					"videoId": "PAST_VOD004",
					"thumbnailOverlays": [{"thumbnailOverlayTimeStatusRenderer": {"style": "DEFAULT", "text": {"simpleText": "1:12:00"}}}]
				}}}},
				{"reelItemRenderer": {
					"videoId": "VERT_VID002",
					"badge": {"iconType": "LIVE"}
				}}
			]
		}
	};</script></html>`

	ids := ExtractActiveLiveVideoIDs(html)
	if len(ids) != 2 {
		t.Fatalf("expected 2 active live video IDs, got %d: %v", len(ids), ids)
	}
	if ids[0] != "HORIZ_VID01" || ids[1] != "VERT_VID002" {
		t.Errorf("unexpected video IDs: %v", ids)
	}
}

func TestExtractWatchPageMetadata_Orientation(t *testing.T) {
	t.Run("horizontal via streamingData", func(t *testing.T) {
		html := `{"videoDetails":{"videoId":"HORIZ123456","title":"Main 16:9 Stream","isLive":true},"streamingData":{"adaptiveFormats":[{"width":1920,"height":1080}]}}`
		info, live, found := ExtractWatchPageMetadata(html)
		if !found || !live {
			t.Fatalf("expected live video, got found=%v live=%v", found, live)
		}
		if info.VideoID != "HORIZ123456" || info.Title != "Main 16:9 Stream" || info.Orientation != OrientationHorizontal {
			t.Errorf("unexpected metadata: %+v", info)
		}
	})

	t.Run("vertical via streamingData", func(t *testing.T) {
		html := `{"videoDetails":{"videoId":"VERT1234567","title":"Vertical Shorts Live","isLive":true},"streamingData":{"adaptiveFormats":[{"width":1080,"height":1920}]}}`
		info, live, found := ExtractWatchPageMetadata(html)
		if !found || !live {
			t.Fatalf("expected live video, got found=%v live=%v", found, live)
		}
		if info.VideoID != "VERT1234567" || info.Title != "Vertical Shorts Live" || info.Orientation != OrientationVertical {
			t.Errorf("unexpected metadata: %+v", info)
		}
	})

	t.Run("vertical via microformat embed fallback", func(t *testing.T) {
		html := `{"videoDetails":{"videoId":"VERT_MF_001","title":"Shorts Stream","isLive":true},"microformat":{"playerMicroformatRenderer":{"embed":{"width":720,"height":1280}}}}`
		info, live, found := ExtractWatchPageMetadata(html)
		if !found || !live {
			t.Fatalf("expected live video, got found=%v live=%v", found, live)
		}
		if info.Orientation != OrientationVertical {
			t.Errorf("expected vertical orientation from microformat fallback, got %q", info.Orientation)
		}
	})
}

func TestSelectActiveBroadcasts_MultiAndDualFormatDedup(t *testing.T) {
	items := []liveBroadcastItem{
		{
			ID: "VID_HORIZ",
			Snippet: &struct {
				Title              string `json:"title"`
				LiveChatID         string `json:"liveChatId"`
				ActualStartTime    string `json:"actualStartTime"`
				ActualEndTime      string `json:"actualEndTime"`
				IsDefaultBroadcast bool   `json:"isDefaultBroadcast"`
			}{Title: "Horizontal Main", LiveChatID: "CHAT_H"},
			Status: &struct {
				LifeCycleStatus string `json:"lifeCycleStatus"`
				PrivacyStatus   string `json:"privacyStatus"`
			}{LifeCycleStatus: "live"},
		},
		{
			ID: "VID_VERT",
			Snippet: &struct {
				Title              string `json:"title"`
				LiveChatID         string `json:"liveChatId"`
				ActualStartTime    string `json:"actualStartTime"`
				ActualEndTime      string `json:"actualEndTime"`
				IsDefaultBroadcast bool   `json:"isDefaultBroadcast"`
			}{Title: "Vertical Shorts", LiveChatID: "CHAT_V"},
			Status: &struct {
				LifeCycleStatus string `json:"lifeCycleStatus"`
				PrivacyStatus   string `json:"privacyStatus"`
			}{LifeCycleStatus: "live"},
		},
		{
			// Dual-format alias sharing CHAT_H must be deduplicated
			ID: "VID_DUAL_DUP",
			Snippet: &struct {
				Title              string `json:"title"`
				LiveChatID         string `json:"liveChatId"`
				ActualStartTime    string `json:"actualStartTime"`
				ActualEndTime      string `json:"actualEndTime"`
				IsDefaultBroadcast bool   `json:"isDefaultBroadcast"`
			}{Title: "Duplicate Dual", LiveChatID: "CHAT_H"},
			Status: &struct {
				LifeCycleStatus string `json:"lifeCycleStatus"`
				PrivacyStatus   string `json:"privacyStatus"`
			}{LifeCycleStatus: "live"},
		},
	}

	got := selectActiveBroadcasts(items)
	if len(got) != 2 {
		t.Fatalf("expected 2 distinct active broadcasts, got %d: %+v", len(got), got)
	}
	if got[0].VideoID != "VID_HORIZ" || got[0].Title != "Horizontal Main" || got[0].LiveChatID != "CHAT_H" {
		t.Errorf("unexpected first broadcast: %+v", got[0])
	}
	if got[1].VideoID != "VID_VERT" || got[1].Title != "Vertical Shorts" || got[1].LiveChatID != "CHAT_V" {
		t.Errorf("unexpected second broadcast: %+v", got[1])
	}
}

func TestConnector_MultiStreamDiscoveryAndMidStreamPickup(t *testing.T) {
	var verticalLive atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/dualcreator/streams":
			items := `{"videoRenderer":{"videoId":"VID_HORIZ01","thumbnailOverlays":[{"thumbnailOverlayTimeStatusRenderer":{"style":"LIVE"}}]}}`
			if verticalLive.Load() {
				items += `,{"videoRenderer":{"videoId":"VID_VERT002","badges":[{"metadataBadgeRenderer":{"style":"BADGE_STYLE_TYPE_LIVE_NOW"}}]}}`
			}
			_, _ = fmt.Fprintf(w, `<html><script>var ytInitialData = {"items":[%s]};</script></html>`, items)
		case r.URL.Path == "/watch" && r.URL.Query().Get("v") == "VID_HORIZ01":
			_, _ = w.Write([]byte(`<html><script>var ytInitialPlayerResponse = {"videoDetails":{"videoId":"VID_HORIZ01","title":"Main Desktop Stream","isLive":true},"streamingData":{"adaptiveFormats":[{"width":1920,"height":1080}]}}; "INNERTUBE_API_KEY":"KEY_H","liveChatRenderer":{"continuations":[{"timedContinuationData":{"continuation":"CONT_H_1"}}]}</script></html>`))
		case r.URL.Path == "/watch" && r.URL.Query().Get("v") == "VID_VERT002":
			_, _ = w.Write([]byte(`<html><script>var ytInitialPlayerResponse = {"videoDetails":{"videoId":"VID_VERT002","title":"Vertical Shorts Stream","isLive":true},"streamingData":{"adaptiveFormats":[{"width":1080,"height":1920}]}}; "INNERTUBE_API_KEY":"KEY_V","liveChatRenderer":{"continuations":[{"timedContinuationData":{"continuation":"CONT_V_1"}}]}</script></html>`))
		case r.URL.Path == "/youtubei/v1/live_chat/get_live_chat":
			var req getLiveChatRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			nowUsec := strconv.FormatInt(time.Now().UnixMicro(), 10)
			switch req.Continuation {
			case "CONT_H_1":
				_, _ = fmt.Fprintf(w, `{"continuationContents":{"liveChatContinuation":{"continuations":[{"timedContinuationData":{"continuation":"CONT_H_2","timeoutMs":50}}],"actions":[{"addChatItemAction":{"item":{"liveChatTextMessageRenderer":{"id":"MSG_H_1","timestampUsec":%q,"authorName":{"simpleText":"DesktopViewer"},"authorExternalChannelId":"UC_H","message":{"runs":[{"text":"Hello horizontal!"}]}}}}}]}}}`, nowUsec)
			case "CONT_V_1":
				_, _ = fmt.Fprintf(w, `{"continuationContents":{"liveChatContinuation":{"continuations":[{"timedContinuationData":{"continuation":"CONT_V_2","timeoutMs":50}}],"actions":[{"addChatItemAction":{"item":{"liveChatTextMessageRenderer":{"id":"MSG_V_1","timestampUsec":%q,"authorName":{"simpleText":"ShortsViewer"},"authorExternalChannelId":"UC_V","message":{"runs":[{"text":"Hello vertical!"}]}}}}}]}}}`, nowUsec)
			default:
				// Keep polling quietly
				_, _ = fmt.Fprintf(w, `{"continuationContents":{"liveChatContinuation":{"continuations":[{"timedContinuationData":{"continuation":%q,"timeoutMs":50}}]}}}`, req.Continuation)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	var mu sync.Mutex
	received := make(map[string]*chat.Event)
	conn := NewConnector(
		func(ev *chat.Event) {
			mu.Lock()
			received[ev.ID] = ev
			mu.Unlock()
		},
		nil,
		nil,
	)
	conn.baseURL = srv.URL
	conn.waitInterval = 30 * time.Millisecond

	if err := conn.Start("@dualcreator"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = conn.Stop() }()

	// Wait for horizontal stream to connect and deliver its message
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		_, hasH := received["MSG_H_1"]
		mu.Unlock()
		if hasH && conn.ActiveStreamCount() == 1 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if conn.ActiveStreamCount() != 1 {
		t.Fatalf("expected 1 active stream before vertical starts, got %d", conn.ActiveStreamCount())
	}

	// Now bring vertical stream live mid-session!
	verticalLive.Store(true)

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		_, hasV := received["MSG_V_1"]
		mu.Unlock()
		if hasV && conn.ActiveStreamCount() == 2 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	streams := conn.ActiveStreams()
	if len(streams) != 2 {
		t.Fatalf("expected 2 active streams after mid-stream pickup, got %d: %+v", len(streams), streams)
	}
	if streams[0].Orientation != OrientationHorizontal || streams[0].VideoID != "VID_HORIZ01" {
		t.Errorf("expected horizontal first, got %+v", streams[0])
	}
	if streams[1].Orientation != OrientationVertical || streams[1].VideoID != "VID_VERT002" {
		t.Errorf("expected vertical second, got %+v", streams[1])
	}

	mu.Lock()
	evH := received["MSG_H_1"]
	evV := received["MSG_V_1"]
	mu.Unlock()
	if evH == nil || evH.StreamID != "VID_HORIZ01" || evH.StreamTitle != "Main Desktop Stream" || evH.StreamOrientation != "horizontal" {
		t.Errorf("unexpected horizontal event metadata: %+v", evH)
	}
	if evV == nil || evV.StreamID != "VID_VERT002" || evV.StreamTitle != "Vertical Shorts Stream" || evV.StreamOrientation != "vertical" {
		t.Errorf("unexpected vertical event metadata: %+v", evV)
	}
}

func TestModeration_MultiStreamDeleteAndBanAll(t *testing.T) {
	chatIDH := base64.RawURLEncoding.EncodeToString([]byte("chat-horizontal"))
	chatIDV := base64.RawURLEncoding.EncodeToString([]byte("chat-vertical"))

	// InnerTube short message ID (Field 2 tag 0x12, len 4, nested Field 1 tag 0x0a, len 2, "hi")
	shortBytes := []byte{0x12, 0x04, 0x0a, 0x02, 'h', 'i'}
	shortMsgID := "LCC." + base64.RawURLEncoding.EncodeToString(shortBytes)
	expectedFullVertID := NormalizeDataAPIMessageID(shortMsgID, chatIDV)

	var deletedID string
	var bannedChatIDs []string
	var mu sync.Mutex

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/liveChat/messages":
			mu.Lock()
			deletedID = r.URL.Query().Get("id")
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/liveChat/bans":
			var req youtubeLiveChatBanRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			mu.Lock()
			bannedChatIDs = append(bannedChatIDs, req.Snippet.LiveChatID)
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer apiSrv.Close()

	store := secrets.NewMemoryStore()
	oauth := NewOAuthManager("cid", "csec", store)
	_ = oauth.SaveToken(&TokenData{
		AccessToken: "tok",
		ExpiresAt:   time.Now().Add(time.Hour),
	})

	conn := NewConnector(nil, nil, nil)
	conn.SetOAuthManager(oauth)
	conn.SetAPIBaseURL(apiSrv.URL)

	// Simulate two active broadcasts and a message that came from the vertical stream
	conn.mu.Lock()
	conn.activeWorkers["VID_H"] = &broadcastWorker{info: StreamInfo{VideoID: "VID_H", LiveChatID: chatIDH, Orientation: OrientationHorizontal}}
	conn.activeWorkers["VID_V"] = &broadcastWorker{info: StreamInfo{VideoID: "VID_V", LiveChatID: chatIDV, Orientation: OrientationVertical}}
	conn.msgVideoID[shortMsgID] = "VID_V"
	conn.mu.Unlock()

	if err := conn.DeleteChatMessage(context.Background(), shortMsgID); err != nil {
		t.Fatalf("DeleteChatMessage failed: %v", err)
	}
	if deletedID != expectedFullVertID {
		t.Errorf("expected DeleteChatMessage to upgrade using vertical liveChatId (%q), got %q", expectedFullVertID, deletedID)
	}

	if err := conn.BanOrTimeoutUser(context.Background(), "UC_TROLL", 300); err != nil {
		t.Fatalf("BanOrTimeoutUser failed: %v", err)
	}
	if len(bannedChatIDs) != 2 {
		t.Fatalf("expected ban to apply to both active live chats, got %d: %v", len(bannedChatIDs), bannedChatIDs)
	}
	joined := strings.Join(bannedChatIDs, ",")
	if !strings.Contains(joined, chatIDH) || !strings.Contains(joined, chatIDV) {
		t.Errorf("expected both %q and %q in bannedChatIDs, got %v", chatIDH, chatIDV, bannedChatIDs)
	}
}
