package twitch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/chat"
	"github.com/mrpoundsign/TownCrier/internal/secrets"
	"golang.org/x/net/websocket"
)

func TestParseEventSubNotification(t *testing.T) {
	// 1. channel.follow
	followJSON := json.RawMessage(`{
		"user_id": "1234",
		"user_login": "newfollower",
		"user_name": "NewFollower",
		"broadcaster_user_id": "9999",
		"broadcaster_user_login": "mrpoundsign",
		"broadcaster_user_name": "MrPoundSign",
		"followed_at": "2026-10-19T12:00:00Z"
	}`)

	fEv, err := ParseEventSubNotification("channel.follow", "msg-follow-1", followJSON, "mrpoundsign")
	if err != nil {
		t.Fatalf("ParseEventSubNotification(channel.follow): %v", err)
	}
	if fEv == nil || fEv.Kind != chat.EventKindFollow || fEv.DisplayName != "NewFollower" || fEv.UserName != "newfollower" || fEv.Channel != "mrpoundsign" {
		t.Fatalf("unexpected follow event: %+v", fEv)
	}

	// 2. channel.channel_points_custom_reward_redemption.add
	redemptionJSON := json.RawMessage(`{
		"id": "redemp-777",
		"broadcaster_user_id": "9999",
		"broadcaster_user_login": "mrpoundsign",
		"broadcaster_user_name": "MrPoundSign",
		"user_id": "5678",
		"user_login": "loyalviewer",
		"user_name": "LoyalViewer",
		"user_input": "Hydrate check!",
		"status": "unfulfilled",
		"redeemed_at": "2026-10-19T12:05:00Z",
		"reward": {
			"id": "reward-abc-123",
			"title": "TTS Message",
			"cost": 500,
			"prompt": "Enter your message"
		}
	}`)

	rEv, err := ParseEventSubNotification("channel.channel_points_custom_reward_redemption.add", "msg-redemp-1", redemptionJSON, "mrpoundsign")
	if err != nil {
		t.Fatalf("ParseEventSubNotification(redemption): %v", err)
	}
	if rEv == nil || rEv.Kind != chat.EventKindRedemption {
		t.Fatalf("expected redemption event, got %+v", rEv)
	}
	if rEv.ID != "redemp-777" || rEv.DisplayName != "LoyalViewer" || rEv.RewardID != "reward-abc-123" || rEv.RewardTitle != "TTS Message" || rEv.Amount != 500 || rEv.Text != "Hydrate check!" {
		t.Errorf("unexpected redemption fields: %+v", rEv)
	}
}

func TestEventSubEndToEndWebSocket(t *testing.T) {
	var subMu sync.Mutex
	var createdSubs []string

	mux := http.NewServeMux()
	mux.HandleFunc("/validate", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_id":"9999","login":"mrpoundsign"}`))
	})
	mux.HandleFunc("/helix/eventsub/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Type    string `json:"type"`
			Version string `json:"version"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		subMu.Lock()
		createdSubs = append(createdSubs, body.Type+":"+body.Version)
		subMu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"data":[{"id":"sub-1","status":"enabled"}]}`))
	})
	mux.Handle("/ws", websocket.Handler(func(conn *websocket.Conn) {
		defer func() { _ = conn.Close() }()

		// 1. Send session_welcome
		welcome := `{
			"metadata": {"message_id":"w-1","message_type":"session_welcome","message_timestamp":"2026-10-19T12:00:00Z"},
			"payload": {"session":{"id":"sess-abc","status":"connected","keepalive_timeout_seconds":10}}
		}`
		if err := websocket.Message.Send(conn, welcome); err != nil {
			return
		}

		// Wait briefly for subscriptions to be registered
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			subMu.Lock()
			n := len(createdSubs)
			subMu.Unlock()
			if n >= 4 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		// 2. Send stream.online notification
		onlineNotif := `{
			"metadata": {"message_id":"n-0","message_type":"notification","subscription_type":"stream.online"},
			"payload": {
				"subscription": {"id":"sub-on","type":"stream.online","version":"1"},
				"event": {"id":"stream-1","broadcaster_user_id":"9999","broadcaster_user_login":"mrpoundsign"}
			}
		}`
		_ = websocket.Message.Send(conn, onlineNotif)

		// 3. Send follow notification
		followNotif := `{
			"metadata": {"message_id":"n-1","message_type":"notification","subscription_type":"channel.follow"},
			"payload": {
				"subscription": {"id":"sub-f","type":"channel.follow","version":"2"},
				"event": {"user_id":"101","user_login":"twitchfan","user_name":"TwitchFan","broadcaster_user_login":"mrpoundsign"}
			}
		}`
		_ = websocket.Message.Send(conn, followNotif)

		// 4. Send redemption notification
		redempNotif := `{
			"metadata": {"message_id":"n-2","message_type":"notification","subscription_type":"channel.channel_points_custom_reward_redemption.add"},
			"payload": {
				"subscription": {"id":"sub-r","type":"channel.channel_points_custom_reward_redemption.add","version":"1"},
				"event": {
					"id":"red-202",
					"user_id":"202",
					"user_login":"pointspender",
					"user_name":"PointSpender",
					"broadcaster_user_login":"mrpoundsign",
					"user_input":"Read this aloud!",
					"reward": {"id":"rew-1","title":"Speak Message","cost":250}
				}
			}
		}`
		_ = websocket.Message.Send(conn, redempNotif)

		time.Sleep(150 * time.Millisecond)
	}))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	memStore := secrets.NewMemoryStore()
	oauthMgr := NewOAuthManager("test-client-id", memStore)
	_ = oauthMgr.SaveTokens(&TokenResponse{
		AccessToken:  "valid-access-token",
		RefreshToken: "valid-refresh-token",
		ExpiresIn:    3600,
	})

	var evMu sync.Mutex
	var received []*chat.Event
	var liveStates []bool

	conn := NewConnector(
		func(ev *chat.Event) {
			evMu.Lock()
			received = append(received, ev)
			evMu.Unlock()
		},
		nil,
		nil,
	)
	conn.SetOnStreamStateChange(func(live bool) {
		evMu.Lock()
		liveStates = append(liveStates, live)
		evMu.Unlock()
	})
	conn.SetOAuthManager(oauthMgr)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn.SetEventSubEndpoints(wsURL, srv.URL+"/helix", srv.URL+"/validate", srv.Client())
	conn.mu.Lock()
	conn.running = true
	conn.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	go func() {
		_, _ = conn.connectAndStreamEventSub(ctx, wsURL, "mrpoundsign")
	}()

	deadline := time.Now().Add(800 * time.Millisecond)
	for time.Now().Before(deadline) {
		evMu.Lock()
		n := len(received)
		ls := len(liveStates)
		evMu.Unlock()
		if n >= 2 && ls >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	subMu.Lock()
	subs := append([]string(nil), createdSubs...)
	subMu.Unlock()

	if len(subs) != 5 || subs[0] != "channel.follow:2" || subs[1] != "channel.channel_points_custom_reward_redemption.add:1" || subs[2] != "channel.chat.notification:1" || subs[3] != "stream.online:1" || subs[4] != "stream.offline:1" {
		t.Fatalf("expected 5 subscriptions, got %v", subs)
	}

	evMu.Lock()
	defer evMu.Unlock()
	if len(received) != 2 {
		t.Fatalf("expected 2 events from EventSub, got %d", len(received))
	}
	if received[0].Kind != chat.EventKindFollow || received[0].DisplayName != "TwitchFan" {
		t.Errorf("unexpected event[0]: %+v", received[0])
	}
	if received[1].Kind != chat.EventKindRedemption || received[1].RewardTitle != "Speak Message" || received[1].Text != "Read this aloud!" {
		t.Errorf("unexpected event[1]: %+v", received[1])
	}
	if len(liveStates) == 0 || !liveStates[0] || !conn.IsLive() {
		t.Errorf("expected stream.online to mark connector live, liveStates=%v isLive=%v", liveStates, conn.IsLive())
	}
}

func TestTwitchCheckChannelLiveHelixAndGQL(t *testing.T) {
	ctx := context.Background()

	mux := http.NewServeMux()
	mux.HandleFunc("/helix/streams", func(w http.ResponseWriter, r *http.Request) {
		login := r.URL.Query().Get("user_login")
		w.Header().Set("Content-Type", "application/json")
		if login == "livechannel" {
			_, _ = w.Write([]byte(`{"data":[{"id":"stream-123","type":"live"}]}`))
		} else {
			_, _ = w.Write([]byte(`{"data":[]}`))
		}
	})
	mux.HandleFunc("/gql", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables struct {
				Login string `json:"login"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body.Variables.Login == "gqllive" {
			_, _ = w.Write([]byte(`{"data":{"user":{"stream":{"id":"99999"}}}}`))
		} else {
			_, _ = w.Write([]byte(`{"data":{"user":{"stream":null}}}`))
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// 1. Anonymous GQL fallback
	conn := NewConnector(nil, nil, nil)
	conn.SetEventSubEndpoints("", srv.URL+"/helix", srv.URL+"/validate", srv.Client())
	conn.mu.Lock()
	conn.gqlURL = srv.URL + "/gql"
	conn.mu.Unlock()

	live, err := conn.checkChannelLive(ctx, "gqllive")
	if err != nil || !live {
		t.Fatalf("expected gqllive=true, got live=%v err=%v", live, err)
	}
	live, err = conn.checkChannelLive(ctx, "gqloffline")
	if err != nil || live {
		t.Fatalf("expected gqloffline=false, got live=%v err=%v", live, err)
	}

	// 2. Authenticated Helix /streams check
	memStore := secrets.NewMemoryStore()
	oauthMgr := NewOAuthManager("test-client-id", memStore)
	_ = oauthMgr.SaveTokens(&TokenResponse{
		AccessToken:  "valid-access-token",
		RefreshToken: "valid-refresh-token",
		ExpiresIn:    3600,
	})
	conn.SetOAuthManager(oauthMgr)

	live, err = conn.checkChannelLive(ctx, "livechannel")
	if err != nil || !live {
		t.Fatalf("expected Helix livechannel=true, got live=%v err=%v", live, err)
	}
}

func TestCheckUserFollowingAndTTLCache(t *testing.T) {
	var apiHits int
	var aliceFollowing = true
	var mu sync.Mutex

	mux := http.NewServeMux()
	mux.HandleFunc("/validate", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_id":"1001","login":"mrpoundsign"}`))
	})
	mux.HandleFunc("/helix/channels/followers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		userID := r.URL.Query().Get("user_id")
		mu.Lock()
		apiHits++
		fol := aliceFollowing
		mu.Unlock()
		if userID == "2001" && fol {
			_, _ = w.Write([]byte(`{
				"data":[{"user_id":"2001","user_login":"alice","user_name":"Alice","followed_at":"2026-10-18T12:00:00Z"}],
				"pagination":{}
			}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[],"pagination":{}}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	memStore := secrets.NewMemoryStore()
	oauthMgr := NewOAuthManager("test-client-id", memStore)
	_ = oauthMgr.SaveTokens(&TokenResponse{
		AccessToken:  "valid-access-token",
		RefreshToken: "valid-refresh-token",
		ExpiresIn:    3600,
	})

	conn := NewConnector(nil, nil, nil)
	conn.SetOAuthManager(oauthMgr)
	conn.SetEventSubEndpoints("", srv.URL+"/helix", srv.URL+"/validate", srv.Client())
	conn.SetFollowerCacheTTL(50 * time.Millisecond)

	conn.mu.Lock()
	conn.running = true
	conn.channel = "mrpoundsign"
	conn.mu.Unlock()

	ctx := context.Background()

	// 1. First check hits Helix API -> true
	ok, err := conn.CheckUserFollowing(ctx, "2001", "alice")
	if err != nil || !ok {
		t.Fatalf("expected alice to be following, got ok=%v err=%v", ok, err)
	}
	// 2. Second check within TTL uses cache -> 0 new API hits
	ok, err = conn.CheckUserFollowing(ctx, "2001", "alice")
	if err != nil || !ok {
		t.Fatalf("expected cached alice to be following, got ok=%v err=%v", ok, err)
	}
	mu.Lock()
	if apiHits != 1 {
		t.Fatalf("expected 1 API hit within TTL, got %d", apiHits)
	}
	// 3. Simulate Alice unfollowing and TTL expiring -> re-queries API and returns false
	aliceFollowing = false
	mu.Unlock()

	time.Sleep(60 * time.Millisecond)
	ok, err = conn.CheckUserFollowing(ctx, "2001", "alice")
	if err != nil || ok {
		t.Fatalf("expected unfollowed alice after TTL expiry to return false, got ok=%v err=%v", ok, err)
	}
}

func TestParseEventSubChatNotifications(t *testing.T) {
	// 1. Watch Streak via channel.chat.notification
	streakRaw := json.RawMessage(`{
		"broadcaster_user_id": "1001",
		"broadcaster_user_login": "mrpoundsign",
		"BroadcasterUserName": "MrPoundSign",
		"chatter_user_id": "2002",
		"chatter_user_login": "wifestreamer",
		"chatter_user_name": "WifeStreamer",
		"message_id": "notif-streak-99",
		"notice_type": "watch_streak",
		"message": {"text": "Never missed a stream!"},
		"watch_streak": {
			"streak_count": 7,
			"channel_points_awarded": 500
		}
	}`)
	ev, err := ParseEventSubNotification("channel.chat.notification", "env-1", streakRaw, "mrpoundsign")
	if err != nil || ev == nil {
		t.Fatalf("ParseEventSubNotification(watch_streak) err=%v ev=%v", err, ev)
	}
	if ev.Kind != chat.EventKindWatchStreak || ev.ID != "notif-streak-99" || ev.Count != 7 || ev.Amount != 500 || ev.Text != "Never missed a stream!" {
		t.Errorf("unexpected watch_streak event: %+v", ev)
	}

	// 2. Charity Donation via channel.chat.notification
	charityRaw := json.RawMessage(`{
		"broadcaster_user_login": "mrpoundsign",
		"chatter_user_id": "3003",
		"chatter_user_login": "kindviewer",
		"chatter_user_name": "KindViewer",
		"message_id": "notif-charity-1",
		"notice_type": "charity_donation",
		"message": {"text": "Great cause!"},
		"charity_donation": {
			"charity_name": "Direct Relief",
			"amount": {
				"value": 5000,
				"decimal_places": 2,
				"currency": "USD"
			}
		}
	}`)
	cEv, err := ParseEventSubNotification("channel.chat.notification", "env-2", charityRaw, "mrpoundsign")
	if err != nil || cEv == nil {
		t.Fatalf("ParseEventSubNotification(charity_donation) err=%v ev=%v", err, cEv)
	}
	if cEv.Kind != chat.EventKindCharityDonation || cEv.Amount != 50 || cEv.Currency != "USD" || cEv.RewardTitle != "Direct Relief" {
		t.Errorf("unexpected charity_donation event: %+v", cEv)
	}
}


