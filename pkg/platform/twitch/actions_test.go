package twitch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mrpoundsign/TownCrier/pkg/secrets"
)

func TestBotOAuthScopesAndPrefixIsolation(t *testing.T) {
	store := secrets.NewMemoryStore()
	broadcaster := NewOAuthManager("cid-broadcaster", store)
	bot := NewBotOAuthManager("cid-bot", store)

	require.NoError(t, broadcaster.SaveTokens(&TokenResponse{
		AccessToken: "tok-broadcaster",
		Scope:       []string{"user:read:chat", "bits:read"},
		ExpiresIn:   3600,
	}))
	require.NoError(t, bot.SaveTokens(&TokenResponse{
		AccessToken: "tok-bot",
		Scope:       []string{"user:write:chat"},
		ExpiresIn:   3600,
	}))

	bTok, err := broadcaster.GetValidAccessToken(context.Background())
	require.NoError(t, err)
	require.Equal(t, "tok-broadcaster", bTok)

	botTok, err := bot.GetValidAccessToken(context.Background())
	require.NoError(t, err)
	require.Equal(t, "tok-bot", botTok)

	require.False(t, broadcaster.HasScope("user:write:chat"))
	require.NotEmpty(t, broadcaster.MissingScopes(BotWriteTwitchScopes))
	require.True(t, bot.HasScope("user:write:chat"))
}

func TestTwitchOutboundActionsWithBotAccount(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	var lastChatAuth string
	var lastChatBody map[string]string
	var lastAnnounceAuth string
	var lastAnnounceModID string
	var announceRejectBot bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()

		switch {
		case r.URL.Path == "/validate":
			tok := strings.TrimPrefix(r.Header.Get("Authorization"), "OAuth ")
			if tok == "" {
				tok = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			}
			uid := "111"
			login := "streamerchan"
			if tok == "tok-bot" {
				uid = "555"
				login = "octothorpebot"
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"user_id": uid, "login": login})

		case r.Method == http.MethodGet && r.URL.Path == "/users":
			login := r.URL.Query().Get("login")
			id := "999"
			switch {
			case login == "octothorpebot":
				id = "555"
			case r.Header.Get("Authorization") == "Bearer tok-bot" && login == "":
				id = "555"
			case r.Header.Get("Authorization") == "Bearer tok-broadcaster" && login == "":
				id = "111"
			case login == "streamerchan":
				id = "111"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"id": id, "login": login}},
			})

		case r.Method == http.MethodPost && r.URL.Path == "/chat/messages":
			bodyBytes, _ := io.ReadAll(r.Body)
			mu.Lock()
			lastChatAuth = r.Header.Get("Authorization")
			_ = json.Unmarshal(bodyBytes, &lastChatBody)
			mu.Unlock()
			w.WriteHeader(http.StatusOK)

		case r.Method == http.MethodPost && r.URL.Path == "/chat/announcements":
			auth := r.Header.Get("Authorization")
			mu.Lock()
			reject := announceRejectBot && auth == "Bearer tok-bot"
			if !reject {
				lastAnnounceAuth = auth
				lastAnnounceModID = r.URL.Query().Get("moderator_id")
			}
			mu.Unlock()
			if reject {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"Forbidden","status":403,"message":"bot is not a moderator"}`))
				return
			}
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodPost && r.URL.Path == "/chat/shoutouts":
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodPatch && r.URL.Path == "/channel_points/custom_rewards/redemptions":
			w.WriteHeader(http.StatusOK)

		case r.Method == http.MethodPost && r.URL.Path == "/clips":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"id": "ClipABC", "edit_url": "https://clips.twitch.tv/ClipABC/edit"}},
			})

		case r.Method == http.MethodPost && r.URL.Path == "/streams/markers":
			w.WriteHeader(http.StatusOK)

		case r.Method == http.MethodPatch && r.URL.Path == "/channels":
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodDelete && r.URL.Path == "/moderation/bans":
			w.WriteHeader(http.StatusNoContent)

		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	store := secrets.NewMemoryStore()
	oauth := NewOAuthManager("cid-broadcaster", store)
	oauth.SetHelixBaseURL(srv.URL)
	require.NoError(t, oauth.SaveTokens(&TokenResponse{AccessToken: "tok-broadcaster", ExpiresIn: 3600}))
	require.NoError(t, store.Set(SecretTwitchUserID, "111"))
	require.NoError(t, store.Set(SecretTwitchUserLogin, "streamerchan"))

	botOAuth := NewBotOAuthManager("cid-bot", store)
	botOAuth.SetHelixBaseURL(srv.URL)
	require.NoError(t, botOAuth.SaveTokens(&TokenResponse{AccessToken: "tok-bot", ExpiresIn: 3600}))
	require.NoError(t, store.Set("twitch_bot_user_id", "555"))
	require.NoError(t, store.Set("twitch_bot_user_login", "octothorpebot"))

	conn := NewConnector(nil, nil, nil)
	conn.SetOAuthManager(oauth)
	conn.SetBotOAuthManager(botOAuth)
	conn.mu.Lock()
	conn.channel = "streamerchan"
	conn.broadcasterID = "111"
	conn.helixBaseURL = srv.URL
	conn.oauthValidateURL = srv.URL + "/validate"
	conn.httpClient = srv.Client()
	conn.mu.Unlock()

	ctx := context.Background()
	require.NoError(t, conn.ReplyChatMessage(ctx, "hello chat", "msg-42"))
	require.Equal(t, "Bearer tok-bot", lastChatAuth)
	require.Equal(t, "555", lastChatBody["sender_id"])
	require.Equal(t, "111", lastChatBody["broadcaster_id"])
	require.Equal(t, "msg-42", lastChatBody["reply_parent_message_id"])

	// 1. Default ("auto") prefers bot account for Announcements too when connected
	require.NoError(t, conn.SendAnnouncement(ctx, "Live now!", "blue"))
	mu.Lock()
	require.Equal(t, "Bearer tok-bot", lastAnnounceAuth)
	require.Equal(t, "555", lastAnnounceModID)
	mu.Unlock()

	// 2. Explicit "broadcaster" override uses primary broadcaster account even when bot is connected
	require.NoError(t, conn.SendAnnouncementAs(ctx, "Streamer announcement", "purple", "broadcaster"))
	mu.Lock()
	require.Equal(t, "Bearer tok-broadcaster", lastAnnounceAuth)
	require.Equal(t, "111", lastAnnounceModID)
	mu.Unlock()

	// 3. When bot lacks moderator permission (403), "auto" automatically falls back to broadcaster, while explicit "bot" returns the error
	mu.Lock()
	announceRejectBot = true
	mu.Unlock()
	require.Error(t, conn.SendAnnouncementAs(ctx, "Bot only", "", "bot"))
	require.NoError(t, conn.SendAnnouncementAs(ctx, "Auto fallback", "", "auto"))
	mu.Lock()
	require.Equal(t, "Bearer tok-broadcaster", lastAnnounceAuth)
	require.Equal(t, "111", lastAnnounceModID)
	announceRejectBot = false
	mu.Unlock()

	require.NoError(t, conn.SendShoutout(ctx, "", "friendchan"))
	require.NoError(t, conn.UpdateRedemptionStatus(ctx, "rew-1", "red-1", "FULFILLED"))

	clipURL, err := conn.CreateClip(ctx)
	require.NoError(t, err)
	require.Equal(t, "https://clips.twitch.tv/ClipABC/edit", clipURL)

	require.NoError(t, conn.CreateStreamMarker(ctx, "Cool moment"))
	require.NoError(t, conn.ModifyChannelInfo(ctx, "Building TownCrier", "509658", []string{"Coding"}))
	require.NoError(t, conn.UnbanUser(ctx, "999", "friendchan"))

	require.Contains(t, calls, "POST /chat/messages")
	require.Contains(t, calls, "POST /chat/announcements")
	require.Contains(t, calls, "POST /chat/shoutouts")
	require.Contains(t, calls, "PATCH /channel_points/custom_rewards/redemptions")
	require.Contains(t, calls, "POST /clips")
	require.Contains(t, calls, "POST /streams/markers")
	require.Contains(t, calls, "PATCH /channels")
	require.Contains(t, calls, "DELETE /moderation/bans")
}
