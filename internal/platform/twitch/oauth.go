package twitch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/secrets"
)

const (
	DefaultTwitchClientID       = "7i52sdpwfxk26fdt672bsoxcoaenno"
	DefaultTwitchDeviceEndpoint = "https://id.twitch.tv/oauth2/device"
	DefaultTwitchTokenEndpoint  = "https://id.twitch.tv/oauth2/token"

	SecretTwitchAccessToken  = "twitch_access_token"
	SecretTwitchRefreshToken = "twitch_refresh_token"
	SecretTwitchTokenExpiry  = "twitch_token_expiry"
)

// DefaultTwitchScopes contains the required permissions for stream chat reading, alerts, and moderation.
var DefaultTwitchScopes = []string{
	"user:read:chat",
	"bits:read",
	"channel:read:subscriptions",
	"channel:read:redemptions",
	"moderator:read:chat_messages",
	"moderator:read:followers",
}

// DeviceCodeResponse holds the response from Twitch's device authorization endpoint.
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// TokenResponse represents the OAuth token response from Twitch.
type TokenResponse struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	ExpiresIn    int      `json:"expires_in"`
	TokenType    string   `json:"token_type"`
	Scope        []string `json:"scope"`
	Message      string   `json:"message,omitempty"`
	Status       int      `json:"status,omitempty"`
}

// OAuthManager manages the Device Code flow lifecycle, token refreshing, and secure storage.
type OAuthManager struct {
	mu             sync.RWMutex
	clientID       string
	deviceEndpoint string
	tokenEndpoint  string
	httpClient     *http.Client
	store          secrets.Store
}

// NewOAuthManager creates a new Twitch OAuthManager.
func NewOAuthManager(clientID string, store secrets.Store) *OAuthManager {
	if clientID == "" {
		clientID = DefaultTwitchClientID
	}
	return &OAuthManager{
		clientID:       clientID,
		deviceEndpoint: DefaultTwitchDeviceEndpoint,
		tokenEndpoint:  DefaultTwitchTokenEndpoint,
		httpClient:     &http.Client{Timeout: 15 * time.Second},
		store:          store,
	}
}

// SetEndpoints overrides default endpoints (primarily used for unit testing).
func (m *OAuthManager) SetEndpoints(deviceEndpoint, tokenEndpoint string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deviceEndpoint = deviceEndpoint
	m.tokenEndpoint = tokenEndpoint
}

// SetHTTPClient sets a custom HTTP client (primarily for testing).
func (m *OAuthManager) SetHTTPClient(client *http.Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.httpClient = client
}

// ClientID returns the configured Twitch client ID.
func (m *OAuthManager) ClientID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.clientID
}

// RequestDeviceCode initiates the RFC 8628 Device Authorization flow with Twitch.
func (m *OAuthManager) RequestDeviceCode(ctx context.Context, scopes []string) (*DeviceCodeResponse, error) {
	m.mu.RLock()
	endpoint := m.deviceEndpoint
	cID := m.clientID
	client := m.httpClient
	m.mu.RUnlock()

	if len(scopes) == 0 {
		scopes = DefaultTwitchScopes
	}

	formData := url.Values{
		"client_id": {cID},
		"scopes":    {strings.Join(scopes, " ")},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create device code request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute device code request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return nil, fmt.Errorf("device code error (status %d): %s", resp.StatusCode, errResp.Message)
	}

	var dResp DeviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&dResp); err != nil {
		return nil, fmt.Errorf("decode device code response: %w", err)
	}

	if dResp.Interval <= 0 {
		dResp.Interval = 5
	}

	return &dResp, nil
}

// PollToken repeatedly polls the token endpoint until the user authorizes the device code,
// the code expires, or the context is cancelled.
func (m *OAuthManager) PollToken(ctx context.Context, deviceCode string, interval int) (*TokenResponse, error) {
	m.mu.RLock()
	endpoint := m.tokenEndpoint
	cID := m.clientID
	client := m.httpClient
	m.mu.RUnlock()

	if interval <= 0 {
		interval = 5
	}
	pollDuration := time.Duration(interval) * time.Second

	formData := url.Values{
		"client_id":   {cID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}

	ticker := time.NewTicker(pollDuration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(formData.Encode()))
			if err != nil {
				return nil, fmt.Errorf("create token poll request: %w", err)
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp, err := client.Do(req)
			if err != nil {
				return nil, fmt.Errorf("poll token request: %w", err)
			}

			var tResp TokenResponse
			decodeErr := json.NewDecoder(resp.Body).Decode(&tResp)
			_ = resp.Body.Close()

			if decodeErr != nil {
				continue
			}

			if resp.StatusCode == http.StatusOK && tResp.AccessToken != "" {
				if err := m.SaveTokens(&tResp); err != nil {
					return &tResp, fmt.Errorf("tokens received but failed to save: %w", err)
				}
				return &tResp, nil
			}

			msg := strings.ToLower(tResp.Message)
			switch {
			case strings.Contains(msg, "authorization_pending"):
				// Continue waiting
				continue
			case strings.Contains(msg, "slow_down"):
				// Increase interval by 5 seconds
				ticker.Reset(pollDuration + 5*time.Second)
				continue
			default:
				return nil, fmt.Errorf("token poll failed (status %d): %s", resp.StatusCode, tResp.Message)
			}
		}
	}
}

// RefreshToken exchanges an existing refresh token for a fresh access token.
func (m *OAuthManager) RefreshToken(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	m.mu.RLock()
	endpoint := m.tokenEndpoint
	cID := m.clientID
	client := m.httpClient
	m.mu.RUnlock()

	formData := url.Values{
		"client_id":     {cID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return nil, fmt.Errorf("refresh failed (status %d): %s", resp.StatusCode, errResp.Message)
	}

	var tResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tResp); err != nil {
		return nil, fmt.Errorf("decode refresh response: %w", err)
	}

	if err := m.SaveTokens(&tResp); err != nil {
		return nil, fmt.Errorf("save refreshed tokens: %w", err)
	}

	return &tResp, nil
}

// SaveTokens stores access and refresh tokens into the configured secret store.
func (m *OAuthManager) SaveTokens(tokens *TokenResponse) error {
	if tokens == nil || tokens.AccessToken == "" {
		return errors.New("empty tokens cannot be saved")
	}

	if err := m.store.Set(SecretTwitchAccessToken, tokens.AccessToken); err != nil {
		return fmt.Errorf("save access token: %w", err)
	}

	if tokens.RefreshToken != "" {
		if err := m.store.Set(SecretTwitchRefreshToken, tokens.RefreshToken); err != nil {
			return fmt.Errorf("save refresh token: %w", err)
		}
	}

	if tokens.ExpiresIn > 0 {
		expiryUnix := time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second).Unix()
		_ = m.store.Set(SecretTwitchTokenExpiry, strconv.FormatInt(expiryUnix, 10))
	}

	return nil
}

// GetValidAccessToken returns a valid access token, automatically refreshing if expired or expiring within 60s.
func (m *OAuthManager) GetValidAccessToken(ctx context.Context) (string, error) {
	accessTok, err := m.store.Get(SecretTwitchAccessToken)
	if err != nil || accessTok == "" {
		return "", errors.New("no Twitch access token stored; OAuth connection required")
	}

	// Check expiry
	expiryStr, err := m.store.Get(SecretTwitchTokenExpiry)
	if err == nil && expiryStr != "" {
		if expUnix, parseErr := strconv.ParseInt(expiryStr, 10, 64); parseErr == nil {
			expiryTime := time.Unix(expUnix, 0)
			// If expiring in less than 60 seconds, refresh
			if time.Until(expiryTime) < 60*time.Second {
				refreshTok, rErr := m.store.Get(SecretTwitchRefreshToken)
				if rErr == nil && refreshTok != "" {
					refreshed, refErr := m.RefreshToken(ctx, refreshTok)
					if refErr == nil {
						return refreshed.AccessToken, nil
					}
				}
			}
		}
	}

	return accessTok, nil
}

// HasToken returns true if an access token exists in the secret store.
func (m *OAuthManager) HasToken() bool {
	tok, err := m.store.Get(SecretTwitchAccessToken)
	return err == nil && tok != ""
}

// ClearTokens removes stored Twitch tokens from the secret store.
func (m *OAuthManager) ClearTokens() error {
	_ = m.store.Delete(SecretTwitchAccessToken)
	_ = m.store.Delete(SecretTwitchRefreshToken)
	_ = m.store.Delete(SecretTwitchTokenExpiry)
	return nil
}
