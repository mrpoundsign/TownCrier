package twitch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/secrets"
)

func TestRequestDeviceCode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		_ = r.ParseForm()
		if r.FormValue("client_id") != "test_client_id" {
			t.Errorf("expected client_id test_client_id, got %s", r.FormValue("client_id"))
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DeviceCodeResponse{
			DeviceCode:      "dev-123",
			UserCode:        "WDJBZSSQ",
			VerificationURI: "https://www.twitch.tv/activate",
			ExpiresIn:       1800,
			Interval:        1,
		})
	}))
	defer ts.Close()

	store := secrets.NewMemoryStore()
	mgr := NewOAuthManager("test_client_id", store)
	mgr.SetEndpoints(ts.URL, ts.URL)

	resp, err := mgr.RequestDeviceCode(context.Background(), nil)
	if err != nil {
		t.Fatalf("RequestDeviceCode failed: %v", err)
	}

	if resp.DeviceCode != "dev-123" {
		t.Errorf("expected dev-123, got %q", resp.DeviceCode)
	}
	if resp.UserCode != "WDJBZSSQ" {
		t.Errorf("expected WDJBZSSQ, got %q", resp.UserCode)
	}
}

func TestPollTokenSuccess(t *testing.T) {
	var pollCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := pollCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if count == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(TokenResponse{
				Status:  400,
				Message: "authorization_pending",
			})
			return
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(TokenResponse{
			AccessToken:  "access-abc-123",
			RefreshToken: "refresh-xyz-789",
			ExpiresIn:    3600,
			TokenType:    "bearer",
		})
	}))
	defer ts.Close()

	store := secrets.NewMemoryStore()
	mgr := NewOAuthManager("test_client_id", store)
	mgr.SetEndpoints(ts.URL, ts.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := mgr.PollToken(ctx, "dev-123", 1)
	if err != nil {
		t.Fatalf("PollToken failed: %v", err)
	}

	if resp.AccessToken != "access-abc-123" {
		t.Errorf("expected access token access-abc-123, got %q", resp.AccessToken)
	}

	// Verify tokens were stored
	tok, err := mgr.GetValidAccessToken(context.Background())
	if err != nil {
		t.Fatalf("GetValidAccessToken failed: %v", err)
	}
	if tok != "access-abc-123" {
		t.Errorf("expected stored token access-abc-123, got %q", tok)
	}
}

func TestRefreshToken(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(TokenResponse{
			AccessToken:  "new-access-token",
			RefreshToken: "new-refresh-token",
			ExpiresIn:    7200,
		})
	}))
	defer ts.Close()

	store := secrets.NewMemoryStore()
	mgr := NewOAuthManager("test_client_id", store)
	mgr.SetEndpoints(ts.URL, ts.URL)

	resp, err := mgr.RefreshToken(context.Background(), "old-refresh")
	if err != nil {
		t.Fatalf("RefreshToken failed: %v", err)
	}

	if resp.AccessToken != "new-access-token" {
		t.Errorf("expected new-access-token, got %q", resp.AccessToken)
	}
	if !mgr.HasToken() {
		t.Error("expected HasToken to be true")
	}

	_ = mgr.ClearTokens()
	if mgr.HasToken() {
		t.Error("expected HasToken to be false after ClearTokens")
	}
}
