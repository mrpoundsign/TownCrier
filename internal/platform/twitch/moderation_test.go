package twitch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mrpoundsign/TownCrier/internal/secrets"
)

func TestParseAutoModEvent(t *testing.T) {
	holdRaw := json.RawMessage(`{
		"broadcaster_user_id": "1001",
		"broadcaster_user_login": "mrpoundsign",
		"user_id": "2002",
		"user_login": "toxictroll99",
		"user_name": "ToxicTroll99",
		"message_id": "msg-hold-1",
		"message": {"text": "what the heck is wrong with this boss fight?!"},
		"reason": "automod",
		"automod": {"category": "aggression", "level": 2},
		"held_at": "2026-10-23T07:28:14Z"
	}`)

	ev, err := ParseAutoModEvent("automod.message.hold", holdRaw, "fallback")
	if err != nil {
		t.Fatalf("ParseAutoModEvent failed: %v", err)
	}
	if ev == nil {
		t.Fatal("expected non-nil AutoModHoldEvent")
	}
	if ev.MessageID != "msg-hold-1" || ev.UserLogin != "toxictroll99" || ev.UserName != "ToxicTroll99" {
		t.Fatalf("unexpected identity fields: %+v", ev)
	}
	if ev.Reason != "AutoMod: aggression (Lvl 2)" {
		t.Errorf("expected 'AutoMod: aggression (Lvl 2)', got %q", ev.Reason)
	}
	if ev.Status != "PENDING" {
		t.Errorf("expected PENDING status, got %q", ev.Status)
	}

	blockedRaw := json.RawMessage(`{
		"broadcaster_user_id": "1001",
		"broadcaster_user_login": "mrpoundsign",
		"user_id": "3003",
		"user_login": "cryptobot_promo",
		"user_name": "CryptoBot_Promo",
		"message_id": "msg-hold-2",
		"message": {"text": "Buy cheap followers now"},
		"reason": "blocked_term",
		"blocked_term": {
			"terms_found": [{"boundary": {"start_pos": 0, "end_pos": 8}}]
		},
		"held_at": "2026-10-23T07:27:19Z"
	}`)

	ev2, err := ParseAutoModEvent("automod.message.hold", blockedRaw, "fallback")
	if err != nil {
		t.Fatalf("ParseAutoModEvent blocked_term failed: %v", err)
	}
	if ev2.Reason != `Blocked Term: "Buy cheap"` {
		t.Errorf("expected Blocked Term reason, got %q", ev2.Reason)
	}

	updateRaw := json.RawMessage(`{
		"broadcaster_user_id": "1001",
		"broadcaster_user_login": "mrpoundsign",
		"user_id": "2002",
		"user_login": "toxictroll99",
		"user_name": "ToxicTroll99",
		"message_id": "msg-hold-1",
		"message": {"text": "what the heck is wrong with this boss fight?!"},
		"status": "APPROVED"
	}`)

	ev3, err := ParseAutoModEvent("automod.message.update", updateRaw, "fallback")
	if err != nil {
		t.Fatalf("ParseAutoModEvent update failed: %v", err)
	}
	if ev3.Status != "APPROVED" {
		t.Errorf("expected APPROVED status, got %q", ev3.Status)
	}
}

func TestHelixModerationEndpoints(t *testing.T) {
	var (
		deletedMsgID string
		banUserID    string
		banDuration  int
		banReason    string
		autoModID    string
		autoModAct   string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/validate":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"user_id": "1001",
				"login":   "mrpoundsign",
			})
		case "/helix/moderation/chat":
			if r.Method != http.MethodDelete {
				t.Errorf("expected DELETE, got %s", r.Method)
			}
			deletedMsgID = r.URL.Query().Get("message_id")
			w.WriteHeader(http.StatusNoContent)
		case "/helix/moderation/bans":
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			var body struct {
				Data struct {
					UserID   string `json:"user_id"`
					Duration int    `json:"duration"`
					Reason   string `json:"reason"`
				} `json:"data"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			banUserID = body.Data.UserID
			banDuration = body.Data.Duration
			banReason = body.Data.Reason
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":[{"user_id":"` + banUserID + `"}]}`))
		case "/helix/moderation/automod/message":
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			var body struct {
				UserID string `json:"user_id"`
				MsgID  string `json:"msg_id"`
				Action string `json:"action"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			autoModID = body.MsgID
			autoModAct = body.Action
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	store := secrets.NewMemoryStore()
	oauth := NewOAuthManager("test-client", store)
	_ = oauth.SaveTokens(&TokenResponse{
		AccessToken: "tok-123",
		ExpiresIn:   3600,
	})

	conn := NewConnector(nil, nil, nil)
	conn.SetOAuthManager(oauth)
	conn.mu.Lock()
	conn.channel = "mrpoundsign"
	conn.helixBaseURL = srv.URL + "/helix"
	conn.oauthValidateURL = srv.URL + "/validate"
	conn.httpClient = srv.Client()
	conn.mu.Unlock()

	ctx := context.Background()

	if err := conn.DeleteChatMessage(ctx, "msg-999"); err != nil {
		t.Fatalf("DeleteChatMessage failed: %v", err)
	}
	if deletedMsgID != "msg-999" {
		t.Errorf("expected deletedMsgID msg-999, got %q", deletedMsgID)
	}

	if err := conn.BanOrTimeoutUser(ctx, "2002", "toxictroll99", 600, "Aggression"); err != nil {
		t.Fatalf("BanOrTimeoutUser timeout failed: %v", err)
	}
	if banUserID != "2002" || banDuration != 600 || banReason != "Aggression" {
		t.Errorf("unexpected timeout payload: user=%q dur=%d reason=%q", banUserID, banDuration, banReason)
	}

	if err := conn.BanOrTimeoutUser(ctx, "3003", "cryptobot", 0, "Spam bot"); err != nil {
		t.Fatalf("BanOrTimeoutUser permanent ban failed: %v", err)
	}
	if banUserID != "3003" || banDuration != 0 || banReason != "Spam bot" {
		t.Errorf("unexpected ban payload: user=%q dur=%d reason=%q", banUserID, banDuration, banReason)
	}

	if err := conn.ResolveAutoModMessage(ctx, "msg-hold-1", "ALLOW"); err != nil {
		t.Fatalf("ResolveAutoModMessage failed: %v", err)
	}
	if autoModID != "msg-hold-1" || autoModAct != "ALLOW" {
		t.Errorf("unexpected automod resolve payload: id=%q action=%q", autoModID, autoModAct)
	}
}
