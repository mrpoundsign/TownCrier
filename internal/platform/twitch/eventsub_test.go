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
			if n >= 2 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		// 2. Send follow notification
		followNotif := `{
			"metadata": {"message_id":"n-1","message_type":"notification","subscription_type":"channel.follow"},
			"payload": {
				"subscription": {"id":"sub-f","type":"channel.follow","version":"2"},
				"event": {"user_id":"101","user_login":"twitchfan","user_name":"TwitchFan","broadcaster_user_login":"mrpoundsign"}
			}
		}`
		_ = websocket.Message.Send(conn, followNotif)

		// 3. Send redemption notification
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

	conn := NewConnector(
		func(ev *chat.Event) {
			evMu.Lock()
			received = append(received, ev)
			evMu.Unlock()
		},
		nil,
		nil,
	)
	conn.SetOAuthManager(oauthMgr)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn.SetEventSubEndpoints(wsURL, srv.URL+"/helix", srv.URL+"/validate", srv.Client())

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	go func() {
		_, _ = conn.connectAndStreamEventSub(ctx, wsURL, "mrpoundsign")
	}()

	deadline := time.Now().Add(800 * time.Millisecond)
	for time.Now().Before(deadline) {
		evMu.Lock()
		n := len(received)
		evMu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	subMu.Lock()
	subs := append([]string(nil), createdSubs...)
	subMu.Unlock()

	if len(subs) != 2 || subs[0] != "channel.follow:2" || subs[1] != "channel.channel_points_custom_reward_redemption.add:1" {
		t.Fatalf("expected [channel.follow:2, channel.channel_points_custom_reward_redemption.add:1], got %v", subs)
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
}
