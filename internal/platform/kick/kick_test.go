package kick

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"github.com/mrpoundsign/TownCrier/internal/chat"
)

func TestNormalizeChannel(t *testing.T) {
	tests := []struct {
		input    string
		wantSlug string
		wantID   int64
	}{
		{"mrpoundsign", "mrpoundsign", 0},
		{"@KickStreamer", "kickstreamer", 0},
		{"https://kick.com/testchannel", "testchannel", 0},
		{"27670567", "27670567", 27670567},
		{"testchannel#27670567", "testchannel", 27670567},
		{"", "", 0},
	}

	for _, tc := range tests {
		slug, id := NormalizeChannel(tc.input)
		if slug != tc.wantSlug || id != tc.wantID {
			t.Errorf("NormalizeChannel(%q) = (%q, %d), want (%q, %d)", tc.input, slug, id, tc.wantSlug, tc.wantID)
		}
	}
}

func TestParseKickEmotes(t *testing.T) {
	raw := "Hello [emote:37226:KEKW] world [emote:12345:PogU]!"
	clean, spans := ParseKickEmotes(raw)

	if clean != "Hello KEKW world PogU!" {
		t.Fatalf("clean = %q, want %q", clean, "Hello KEKW world PogU!")
	}
	if len(spans) != 2 {
		t.Fatalf("expected 2 emote spans, got %d", len(spans))
	}
	if spans[0].Name != "KEKW" || spans[0].Start != 6 || spans[0].End != 10 {
		t.Errorf("span 0 = %+v", spans[0])
	}
	if spans[1].Name != "PogU" || spans[1].Start != 17 || spans[1].End != 21 {
		t.Errorf("span 1 = %+v", spans[1])
	}
}

func TestParseRecordedKickFixtures(t *testing.T) {
	fixtureDir := filepath.Join("..", "..", "..", "testdata", "kick")

	// 1. chat_message.json
	chatRaw, err := os.ReadFile(filepath.Join(fixtureDir, "chat_message.json"))
	if err != nil {
		t.Fatalf("read chat_message.json: %v", err)
	}
	ev, mod, _, err := ParsePusherFrame(chatRaw)
	if err != nil {
		t.Fatalf("ParsePusherFrame(chat_message): %v", err)
	}
	if mod != nil {
		t.Errorf("expected nil moderation event for chat_message")
	}
	if ev == nil {
		t.Fatal("expected non-nil chat event")
	}
	if ev.ID != "8c2e1f9a-4b3d-4e1a-9c8b-7a6f5e4d3c2b" {
		t.Errorf("ID = %q", ev.ID)
	}
	if ev.DisplayName != "KickViewer_01" || ev.UserID != "987654" {
		t.Errorf("unexpected user fields: %+v", ev)
	}
	if ev.Text != "Hello Kick stream! KEKW let's go" {
		t.Errorf("Text = %q", ev.Text)
	}
	if len(ev.EmoteSpans) != 1 || ev.EmoteSpans[0].Name != "KEKW" {
		t.Errorf("EmoteSpans = %+v", ev.EmoteSpans)
	}
	if len(ev.Roles) != 3 {
		t.Errorf("expected 3 roles (mod, subscriber, og), got %v", ev.Roles)
	}

	// 2. subscription.json
	subRaw, err := os.ReadFile(filepath.Join(fixtureDir, "subscription.json"))
	if err != nil {
		t.Fatalf("read subscription.json: %v", err)
	}
	subEv, _, _, err := ParsePusherFrame(subRaw)
	if err != nil || subEv == nil {
		t.Fatalf("ParsePusherFrame(subscription): err=%v ev=%v", err, subEv)
	}
	if subEv.Kind != chat.EventKindResub || subEv.Months != 4 || subEv.DisplayName != "LoyalKickSub" {
		t.Errorf("unexpected sub event: %+v", subEv)
	}

	// 3. gifted_subscriptions.json
	giftRaw, err := os.ReadFile(filepath.Join(fixtureDir, "gifted_subscriptions.json"))
	if err != nil {
		t.Fatalf("read gifted_subscriptions.json: %v", err)
	}
	giftEv, _, _, err := ParsePusherFrame(giftRaw)
	if err != nil || giftEv == nil {
		t.Fatalf("ParsePusherFrame(gifted_subscriptions): err=%v ev=%v", err, giftEv)
	}
	if giftEv.Kind != chat.EventKindGiftSub || giftEv.Count != 5 || giftEv.DisplayName != "GenerousGifter" {
		t.Errorf("unexpected gift_sub event: %+v", giftEv)
	}

	// 4. message_deleted.json
	delRaw, err := os.ReadFile(filepath.Join(fixtureDir, "message_deleted.json"))
	if err != nil {
		t.Fatalf("read message_deleted.json: %v", err)
	}
	_, delMod, _, err := ParsePusherFrame(delRaw)
	if err != nil || delMod == nil {
		t.Fatalf("ParsePusherFrame(message_deleted): err=%v mod=%v", err, delMod)
	}
	if delMod.Action != chat.ModActionDeleteMessage || delMod.TargetMessageID != "8c2e1f9a-4b3d-4e1a-9c8b-7a6f5e4d3c2b" {
		t.Errorf("unexpected delete mod event: %+v", delMod)
	}

	// 5. user_banned.json
	banRaw, err := os.ReadFile(filepath.Join(fixtureDir, "user_banned.json"))
	if err != nil {
		t.Fatalf("read user_banned.json: %v", err)
	}
	_, banMod, _, err := ParsePusherFrame(banRaw)
	if err != nil || banMod == nil {
		t.Fatalf("ParsePusherFrame(user_banned): err=%v mod=%v", err, banMod)
	}
	if banMod.Action != chat.ModActionBanUser || banMod.TargetUserID != "987654" {
		t.Errorf("unexpected ban mod event: %+v", banMod)
	}
}

func TestConnectorEndToEndWithMockPusher(t *testing.T) {
	fixtureDir := filepath.Join("..", "..", "..", "testdata", "kick")
	chatFixture, err := os.ReadFile(filepath.Join(fixtureDir, "chat_message.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var subscribedChannel string
	var subMu sync.Mutex

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/channels/testkick/chatroom", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": 27670567}`))
	})
	mux.Handle("/pusher", websocket.Handler(func(ws *websocket.Conn) {
		var subMsg string
		if err := websocket.Message.Receive(ws, &subMsg); err != nil {
			return
		}
		var env struct {
			Event string `json:"event"`
			Data  struct {
				Channel string `json:"channel"`
			} `json:"data"`
		}
		_ = json.Unmarshal([]byte(subMsg), &env)
		subMu.Lock()
		subscribedChannel = env.Data.Channel
		subMu.Unlock()

		// Send recorded chat message fixture
		_ = websocket.Message.Send(ws, string(chatFixture))
		time.Sleep(100 * time.Millisecond)
	}))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	var evMu sync.Mutex
	var events []*chat.Event

	conn := NewConnector(
		func(ev *chat.Event) {
			evMu.Lock()
			events = append(events, ev)
			evMu.Unlock()
		},
		nil,
		nil,
	)
	conn.apiBaseURL = srv.URL
	conn.wsURL = "ws" + strings.TrimPrefix(srv.URL, "http") + "/pusher"
	conn.reconnectDelay = 50 * time.Millisecond

	if err := conn.Start("testkick"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conn.Stop() }()

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		evMu.Lock()
		n := len(events)
		evMu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	subMu.Lock()
	gotSub := subscribedChannel
	subMu.Unlock()
	if gotSub != "chatrooms.27670567.v2" {
		t.Errorf("subscribedChannel = %q, want chatrooms.27670567.v2", gotSub)
	}

	evMu.Lock()
	defer evMu.Unlock()
	if len(events) != 1 {
		t.Fatalf("expected 1 chat event, got %d", len(events))
	}
	if events[0].Text != "Hello Kick stream! KEKW let's go" {
		t.Errorf("Text = %q", events[0].Text)
	}
}

func TestParseKickFollowAndGiftEvents(t *testing.T) {
	// 1. FollowEvent
	followRaw := []byte(`{"event":"App\\Events\\FollowEvent","data":"{\"username\":\"NewKickFollower\"}","channel":"channel.111222"}`)
	fEv, _, _, err := ParsePusherFrame(followRaw)
	if err != nil || fEv == nil {
		t.Fatalf("ParsePusherFrame(FollowEvent): err=%v ev=%v", err, fEv)
	}
	if fEv.Kind != chat.EventKindFollow || fEv.DisplayName != "NewKickFollower" {
		t.Errorf("unexpected follow event: %+v", fEv)
	}

	// 2. KicksGifted
	giftRaw := []byte(`{"event":"KicksGifted","data":"{\"sender\":{\"username\":\"KickSupporter\"},\"gift\":{\"name\":\"Sub Bomb\",\"amount\":250},\"message\":\"Great stream!\"}","channel":"chatrooms.27670567.v2"}`)
	gEv, _, _, err := ParsePusherFrame(giftRaw)
	if err != nil || gEv == nil {
		t.Fatalf("ParsePusherFrame(KicksGifted): err=%v ev=%v", err, gEv)
	}
	if gEv.Kind != chat.EventKindGift || gEv.DisplayName != "KickSupporter" || gEv.RewardTitle != "Sub Bomb" || gEv.Count != 250 || gEv.Amount != 250 || gEv.Text != "Great stream!" {
		t.Errorf("unexpected gift event: %+v", gEv)
	}

	// 3. RewardRedeemedEvent
	redempRaw := []byte(`{"event":"RewardRedeemedEvent","data":"{\"id\":\"kr-1\",\"username\":\"KickRedeemer\",\"reward_title\":\"Hydrate\",\"cost\":500,\"user_input\":\"Drink water!\"}","channel":"chatrooms.27670567.v2"}`)
	rEv, _, _, err := ParsePusherFrame(redempRaw)
	if err != nil || rEv == nil {
		t.Fatalf("ParsePusherFrame(RewardRedeemedEvent): err=%v ev=%v", err, rEv)
	}
	if rEv.Kind != chat.EventKindRedemption || rEv.DisplayName != "KickRedeemer" || rEv.RewardTitle != "Hydrate" || rEv.Amount != 500 || rEv.Text != "Drink water!" {
		t.Errorf("unexpected redemption event: %+v", rEv)
	}

	// 4. Multi-recipient GiftedSubscriptionsEvent
	giftSubRaw := []byte(`{"event":"GiftedSubscriptionsEvent","data":"{\"gifter_username\":\"BigGifter\",\"gifted_usernames\":[\"Alice\",\"Bob\",\"Charlie\"]}","channel":"chatrooms.27670567.v2"}`)
	gsEv, _, _, err := ParsePusherFrame(giftSubRaw)
	if err != nil || gsEv == nil {
		t.Fatalf("ParsePusherFrame(GiftedSubscriptionsEvent): err=%v ev=%v", err, gsEv)
	}
	if gsEv.Kind != chat.EventKindGiftSub || gsEv.Count != 3 || gsEv.Recipient != "Alice, Bob, Charlie" {
		t.Errorf("unexpected multi-recipient gift sub event: %+v", gsEv)
	}
}

func TestDualChannelPusherSubscription(t *testing.T) {
	var subMu sync.Mutex
	var subscribedChannels []string

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/channels/dualkick", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": 999888, "chatroom": {"id": 27670567}}`))
	})
	mux.Handle("/pusher", websocket.Handler(func(ws *websocket.Conn) {
		for range 2 {
			var subMsg string
			if err := websocket.Message.Receive(ws, &subMsg); err != nil {
				return
			}
			var env struct {
				Event string `json:"event"`
				Data  struct {
					Channel string `json:"channel"`
				} `json:"data"`
			}
			_ = json.Unmarshal([]byte(subMsg), &env)
			subMu.Lock()
			subscribedChannels = append(subscribedChannels, env.Data.Channel)
			subMu.Unlock()
		}
		followFrame := `{"event":"App\\Events\\FollowersUpdated","data":"{\"username\":\"DualFollower\"}","channel":"channel.999888"}`
		_ = websocket.Message.Send(ws, followFrame)
		time.Sleep(100 * time.Millisecond)
	}))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	var evMu sync.Mutex
	var events []*chat.Event

	conn := NewConnector(
		func(ev *chat.Event) {
			evMu.Lock()
			events = append(events, ev)
			evMu.Unlock()
		},
		nil,
		nil,
	)
	conn.apiBaseURL = srv.URL
	conn.wsURL = "ws" + strings.TrimPrefix(srv.URL, "http") + "/pusher"
	conn.reconnectDelay = 50 * time.Millisecond

	if err := conn.Start("dualkick"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conn.Stop() }()

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		evMu.Lock()
		n := len(events)
		evMu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	subMu.Lock()
	gotSubs := append([]string(nil), subscribedChannels...)
	subMu.Unlock()

	if len(gotSubs) != 2 || gotSubs[0] != "chatrooms.27670567.v2" || gotSubs[1] != "channel.999888" {
		t.Fatalf("expected [chatrooms.27670567.v2, channel.999888], got %v", gotSubs)
	}

	evMu.Lock()
	defer evMu.Unlock()
	if len(events) != 1 || events[0].Kind != chat.EventKindFollow || events[0].DisplayName != "DualFollower" {
		t.Fatalf("expected follow event from DualFollower, got %+v", events)
	}
}

func TestKickLiveStreamStateTransitions(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/channels/livekick", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": 555666, "chatroom": {"id": 27670567}, "livestream": {"id": 1001, "is_live": true}}`))
	})
	mux.Handle("/pusher", websocket.Handler(func(ws *websocket.Conn) {
		for range 2 {
			var subMsg string
			if err := websocket.Message.Receive(ws, &subMsg); err != nil {
				return
			}
		}
		offlineFrame := `{"event":"App\\Events\\StopStreamBroadcast","data":"{}","channel":"channel.555666"}`
		_ = websocket.Message.Send(ws, offlineFrame)
		time.Sleep(50 * time.Millisecond)
		onlineFrame := `{"event":"App\\Events\\StreamerIsLive","data":"{}","channel":"channel.555666"}`
		_ = websocket.Message.Send(ws, onlineFrame)
		time.Sleep(100 * time.Millisecond)
	}))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	var stateMu sync.Mutex
	var states []bool

	conn := NewConnector(nil, nil, nil)
	conn.apiBaseURL = srv.URL
	conn.wsURL = "ws" + strings.TrimPrefix(srv.URL, "http") + "/pusher"
	conn.reconnectDelay = 50 * time.Millisecond
	conn.SetOnStreamStateChange(func(live bool) {
		stateMu.Lock()
		states = append(states, live)
		stateMu.Unlock()
	})

	if err := conn.Start("livekick"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		stateMu.Lock()
		n := len(states)
		stateMu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = conn.Stop()

	// Wait briefly for Stop() callback
	time.Sleep(30 * time.Millisecond)

	stateMu.Lock()
	validTransitions := len(states) >= 3 && states[0] && !states[1] && states[2]
	savedStates := append([]bool(nil), states...)
	stateMu.Unlock()

	if !validTransitions {
		t.Fatalf("expected live transitions [true, false, true, ...], got %v", savedStates)
	}

	// Verify CheckLive on live channel
	conn.channel = "livekick"
	conn.running = true
	isLive, err := conn.CheckLive(context.Background())
	if err != nil || !isLive || !conn.IsLive() {
		t.Fatalf("expected CheckLive=true, got isLive=%v, conn.IsLive()=%v, err=%v", isLive, conn.IsLive(), err)
	}
}

func TestParsePusherErrorFrame(t *testing.T) {
	raw := []byte(`{"event":"pusher:error","data":{"code":4001,"message":"App key 32cbd69e4b950bf97679 not in this cluster. Did you forget to specify the cluster?"}}`)
	ev, mod, evName, err := ParsePusherFrame(raw)
	if ev != nil || mod != nil {
		t.Fatalf("expected nil event/mod for pusher:error, got ev=%v mod=%v", ev, mod)
	}
	if evName != "pusher:error" {
		t.Fatalf("expected evName 'pusher:error', got %q", evName)
	}
	pusherErr, ok := errors.AsType[*PusherError](err)
	if !ok {
		t.Fatalf("expected *PusherError, got %v", err)
	}
	if pusherErr.Code != 4001 || !pusherErr.IsFatal() {
		t.Errorf("expected fatal code 4001, got code=%d fatal=%v", pusherErr.Code, pusherErr.IsFatal())
	}
	if !strings.Contains(pusherErr.Message, "32cbd69e4b950bf97679") {
		t.Errorf("unexpected error message: %q", pusherErr.Message)
	}
}

func TestConnectorPusherErrorBackoffAndStatusDedup(t *testing.T) {
	var wsAttempts atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/channels/retiredkey", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": 111222, "chatroom": {"id": 27670567}}`))
	})
	mux.Handle("/pusher", websocket.Handler(func(ws *websocket.Conn) {
		wsAttempts.Add(1)
		errFrame := `{"event":"pusher:error","data":{"code":4001,"message":"App key 32cbd69e4b950bf97679 not in this cluster."}}`
		_ = websocket.Message.Send(ws, errFrame)
		time.Sleep(20 * time.Millisecond)
		_ = ws.Close()
	}))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	type statusUpdate struct {
		status ConnectionStatus
		detail string
	}
	var statusMu sync.Mutex
	var statuses []statusUpdate

	conn := NewConnector(nil, nil, func(status ConnectionStatus, detail string) {
		statusMu.Lock()
		statuses = append(statuses, statusUpdate{status: status, detail: detail})
		statusMu.Unlock()
	})
	conn.apiBaseURL = srv.URL
	conn.wsURL = "ws" + strings.TrimPrefix(srv.URL, "http") + "/pusher"
	conn.reconnectDelay = 15 * time.Millisecond

	if err := conn.Start("retiredkey"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait long enough for initial attempt + fatal backoff (12 * 15ms = 180ms)
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if wsAttempts.Load() >= 2 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	_ = conn.Stop()
	time.Sleep(20 * time.Millisecond)

	statusMu.Lock()
	defer statusMu.Unlock()

	for _, s := range statuses {
		if s.status == StatusConnected || s.status == StatusWaitingForStream {
			t.Fatalf("expected never to enter %s on Pusher 4001 error, got statuses=%+v", s.status, statuses)
		}
	}

	// Verify status transitioned to Disconnected with the 4001 error message and did not spam duplicate Disconnected updates
	var fatalDisconnectedCount int
	for _, s := range statuses {
		if s.status == StatusDisconnected && strings.Contains(s.detail, "4001") {
			fatalDisconnectedCount++
		}
	}
	if fatalDisconnectedCount != 1 {
		t.Fatalf("expected exactly 1 deduplicated StatusDisconnected (4001) update across multiple retries, got %d (statuses=%+v)", fatalDisconnectedCount, statuses)
	}
}

