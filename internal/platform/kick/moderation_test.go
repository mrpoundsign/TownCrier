package kick

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

func TestKickOAuthPKCEAndModeration(t *testing.T) {
	var deletedMsgID string
	var banBody map[string]any
	var liveQueriedSlug string

	var failRefresh atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/oauth/token":
			_ = r.ParseForm()
			grant := r.Form.Get("grant_type")
			w.Header().Set("Content-Type", "application/json")
			if grant == "refresh_token" {
				if failRefresh.Load() {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
					return
				}
				_, _ = w.Write([]byte(`{"access_token":"refreshed_tok","refresh_token":"new_ref","expires_in":3600}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"kick_tok_1","refresh_token":"kick_ref_1","expires_in":3600}`))
		case r.Method == http.MethodGet && r.URL.Path == "/public/v1/channels":
			slug := r.URL.Query().Get("slug")
			if slug != "" {
				liveQueriedSlug = slug
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"broadcaster_user_id":777888,"slug":"kickstreamer","stream":{"is_live":true}}]}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/public/v1/chat/msg-999":
			deletedMsgID = "msg-999"
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/public/v1/moderation/bans":
			_ = json.NewDecoder(r.Body).Decode(&banBody)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":{}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	store := secrets.NewMemoryStore()
	mgr := NewOAuthManager("test_cid", "test_sec", store)
	mgr.SetEndpoints(srv.URL+"/oauth/authorize", srv.URL+"/oauth/token", srv.URL+"/public/v1")
	mgr.SetRedirectPort(0)

	var authChanges atomic.Int32
	mgr.SetOnAuthChange(func() {
		authChanges.Add(1)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tok, err := mgr.AuthenticatePKCE(ctx, func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		redirectURI := u.Query().Get("redirect_uri")
		state := u.Query().Get("state")
		go func() {
			cbURL := redirectURI + "?code=auth_code_123&state=" + url.QueryEscape(state)
			resp, err := http.Get(cbURL) //nolint:gosec // test loopback callback
			if err == nil && resp != nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	})
	if err != nil {
		t.Fatalf("AuthenticatePKCE failed: %v", err)
	}
	if tok.BroadcasterUserID != 777888 || tok.Slug != "kickstreamer" {
		t.Fatalf("unexpected token channel metadata: %+v", tok)
	}

	conn := NewConnector(nil, nil, nil)
	conn.SetOAuthManager(mgr)
	conn.SetPublicAPIBaseURL(srv.URL + "/public/v1")

	// 1. DeleteChatMessage
	if err := conn.DeleteChatMessage(ctx, "msg-999"); err != nil {
		t.Fatalf("DeleteChatMessage failed: %v", err)
	}
	if deletedMsgID != "msg-999" {
		t.Errorf("deletedMsgID = %q, want msg-999", deletedMsgID)
	}

	// 2. Timeout user (600s -> 10m)
	banBody = nil
	if err := conn.BanOrTimeoutUser(ctx, "555666", 600, "spamming"); err != nil {
		t.Fatalf("BanOrTimeoutUser (timeout) failed: %v", err)
	}
	if gotDur, ok := banBody["duration"].(float64); !ok || int(gotDur) != 10 {
		t.Errorf("timeout duration = %v, want 10 minutes", banBody["duration"])
	}
	if gotUID, ok := banBody["user_id"].(float64); !ok || int(gotUID) != 555666 {
		t.Errorf("user_id = %v, want 555666", banBody["user_id"])
	}

	// 3. Permanent ban (durationSec = 0 -> no duration field)
	banBody = nil
	if err := conn.BanOrTimeoutUser(ctx, "555666", 0, "permaban"); err != nil {
		t.Fatalf("BanOrTimeoutUser (ban) failed: %v", err)
	}
	if _, hasDur := banBody["duration"]; hasDur {
		t.Errorf("expected no duration field for permanent ban, got %v", banBody["duration"])
	}

	// 4. Live check via Official Public API v1
	isLive, liveErr := conn.checkLivePublicV1(ctx, "kickstreamer")
	if liveErr != nil || !isLive || liveQueriedSlug != "kickstreamer" {
		t.Errorf("checkLivePublicV1 = (%v, %v), slug=%q; want (true, nil), kickstreamer", isLive, liveErr, liveQueriedSlug)
	}

	// 5. HandleUnauthorized auto-disconnects and fires onAuthChange when refresh token is invalid
	failRefresh.Store(true)
	before := authChanges.Load()
	_, _ = mgr.HandleUnauthorized(ctx)
	if mgr.HasToken() {
		t.Error("expected HasToken() == false after HandleUnauthorized()")
	}
	if authChanges.Load() <= before {
		t.Error("expected onAuthChange callback to fire on HandleUnauthorized()")
	}
}
