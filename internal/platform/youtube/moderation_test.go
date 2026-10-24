package youtube

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/secrets"
)

func TestYouTubeOAuthPKCEAndRefresh(t *testing.T) {
	store := secrets.NewMemoryStore()
	mgr := NewOAuthManager("", "", store)

	if mgr.HasToken() {
		t.Fatal("expected HasToken() == false before authentication")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/channels" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id": "UC_TEST_1",
						"snippet": map[string]any{
							"title":     "MrPoundSign",
							"customUrl": "@mrpoundsign",
						},
					},
				},
			})
			return
		}
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		grantType := r.Form.Get("grant_type")
		w.Header().Set("Content-Type", "application/json")
		switch grantType {
		case "authorization_code":
			if r.Form.Get("code") != "test-auth-code" || r.Form.Get("code_verifier") == "" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "yt-access-1",
				"refresh_token": "yt-refresh-1",
				"expires_in":    3600,
				"token_type":    "Bearer",
				"scope":         DefaultYouTubeScope,
			})
		case "refresh_token":
			if r.Form.Get("refresh_token") != "yt-refresh-1" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "yt-access-refreshed",
				"expires_in":   3600,
				"token_type":   "Bearer",
				"scope":        DefaultYouTubeScope,
			})
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	mgr.SetEndpoints(srv.URL+"/auth", srv.URL+"/token")
	mgr.SetAPIBaseURL(srv.URL)
	mgr.SetHTTPClient(srv.Client())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tok, err := mgr.AuthenticatePKCE(ctx, func(authURL string) error {
		parsed, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		redirectURI := parsed.Query().Get("redirect_uri")
		state := parsed.Query().Get("state")
		go func() {
			cbURL := redirectURI + "?code=test-auth-code&state=" + url.QueryEscape(state)
			resp, err := http.Get(cbURL)
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	})
	if err != nil {
		t.Fatalf("AuthenticatePKCE failed: %v", err)
	}
	if tok.AccessToken != "yt-access-1" || tok.RefreshToken != "yt-refresh-1" {
		t.Fatalf("unexpected token: %+v", tok)
	}
	if !mgr.HasToken() {
		t.Fatal("expected HasToken() == true after AuthenticatePKCE")
	}

	// Expire access token and verify auto-refresh via GetValidAccessToken
	tok.ExpiresAt = time.Now().Add(-1 * time.Minute)
	_ = mgr.SaveToken(tok)

	validTok, err := mgr.GetValidAccessToken(ctx)
	if err != nil {
		t.Fatalf("GetValidAccessToken failed: %v", err)
	}
	if validTok != "yt-access-refreshed" {
		t.Fatalf("expected refreshed token 'yt-access-refreshed', got %q", validTok)
	}

	if err := mgr.Disconnect(); err != nil {
		t.Fatalf("Disconnect failed: %v", err)
	}
	if mgr.HasToken() {
		t.Fatal("expected HasToken() == false after Disconnect")
	}
}

func TestYouTubeModerationEndpoints(t *testing.T) {
	store := secrets.NewMemoryStore()
	oauth := NewOAuthManager("", "", store)
	_ = oauth.SaveToken(&TokenData{
		AccessToken:  "yt-mod-token",
		RefreshToken: "yt-refresh-token",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	})

	var videoLookupCalls atomic.Int32
	var deletedMsgID string
	var lastBanType string
	var lastBanDuration int
	var lastBannedChannelID string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer yt-mod-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/videos":
			videoLookupCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"liveStreamingDetails": map[string]any{
							"activeLiveChatId": "CG0KC2xpdmUtY2hhdC0x",
						},
					},
				},
			})
		case "/liveChat/messages":
			if r.Method != http.MethodDelete {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			deletedMsgID = r.URL.Query().Get("id")
			w.WriteHeader(http.StatusNoContent)
		case "/liveChat/bans":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			var req youtubeLiveChatBanRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if req.Snippet.LiveChatID != "CG0KC2xpdmUtY2hhdC0x" {
				http.Error(w, "unexpected liveChatId", http.StatusBadRequest)
				return
			}
			lastBanType = req.Snippet.Type
			lastBanDuration = req.Snippet.BanDurationSeconds
			lastBannedChannelID = req.Snippet.BannedUserDetails.ChannelID
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"ban-1"}`))
		case "/liveBroadcasts":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id": "unlisted-video-999",
						"snippet": map[string]any{
							"title":        "Unlisted Stream",
							"liveChatId":   "CG0KC3VubGlzdGVkLWNoYXQ",
							"isDefaultBroadcast": false,
						},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	conn := NewConnector(nil, nil, nil)
	conn.SetOAuthManager(oauth)
	conn.SetAPIBaseURL(srv.URL)
	conn.httpClient = srv.Client()
	conn.activeVideoID = "dQw4w9WgXcQ"

	ctx := context.Background()

	// 1. DeleteChatMessage
	if err := conn.DeleteChatMessage(ctx, "LCC.CikqJwoYVUNfVEVTVC1Nlc3NhZ2UtSUQtMTIzNDU"); err != nil {
		t.Fatalf("DeleteChatMessage failed: %v", err)
	}
	if deletedMsgID != "LCC.CikqJwoYVUNfVEVTVC1Nlc3NhZ2UtSUQtMTIzNDU" {
		t.Errorf("expected deletedMsgID to match, got %q", deletedMsgID)
	}

	// 2. Temporary Timeout (300s) -> resolves liveChatId once
	if err := conn.BanOrTimeoutUser(ctx, "UC_TROLL_CHANNEL_ID", 300); err != nil {
		t.Fatalf("BanOrTimeoutUser (timeout) failed: %v", err)
	}
	if lastBanType != "temporary" || lastBanDuration != 300 || lastBannedChannelID != "UC_TROLL_CHANNEL_ID" {
		t.Errorf("unexpected timeout payload: type=%q dur=%d chan=%q", lastBanType, lastBanDuration, lastBannedChannelID)
	}

	// 3. Permanent Ban (0s) -> reuses cached liveChatId (0 additional /videos lookups)
	if err := conn.BanOrTimeoutUser(ctx, "UC_TROLL_CHANNEL_ID", 0); err != nil {
		t.Fatalf("BanOrTimeoutUser (ban) failed: %v", err)
	}
	if lastBanType != "permanent" || lastBanDuration != 0 {
		t.Errorf("unexpected permanent ban payload: type=%q dur=%d", lastBanType, lastBanDuration)
	}
	if videoLookupCalls.Load() != 1 {
		t.Errorf("expected exactly 1 /videos lookup call (cached across stream), got %d", videoLookupCalls.Load())
	}

	// 4. Authenticated Broadcast Discovery (Unlisted stream detection)
	vidID, chatID, err := conn.discoverAuthenticatedBroadcast(ctx)
	if err != nil {
		t.Fatalf("discoverAuthenticatedBroadcast failed: %v", err)
	}
	if vidID != "unlisted-video-999" || chatID != "CG0KC3VubGlzdGVkLWNoYXQ" {
		t.Errorf("unexpected broadcast discovery result: vid=%q chat=%q", vidID, chatID)
	}
}
