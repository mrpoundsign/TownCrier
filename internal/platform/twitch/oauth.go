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
	SecretTwitchUserID       = "twitch_user_id"
	SecretTwitchUserLogin    = "twitch_user_login"
	SecretTwitchDisplayName  = "twitch_display_name"
)

// DefaultTwitchScopes contains the required permissions for stream chat reading, alerts, and moderation.
var DefaultTwitchScopes = []string{
	"user:read:chat",
	"bits:read",
	"channel:read:subscriptions",
	"channel:read:redemptions",
	"moderator:read:chat_messages",
	"moderator:read:followers",
	"moderator:manage:chat_messages",
	"moderator:manage:banned_users",
	"moderator:manage:automod",
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
	helixBaseURL   string
	httpClient     *http.Client
	store          secrets.Store
	onAuthChange   func()
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
		helixBaseURL:   DefaultHelixBaseURL,
		httpClient:     &http.Client{Timeout: 15 * time.Second},
		store:          store,
	}
}

// SetHelixBaseURL overrides the Helix API base URL (used in unit tests).
func (m *OAuthManager) SetHelixBaseURL(helixBaseURL string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if helixBaseURL != "" {
		m.helixBaseURL = strings.TrimRight(helixBaseURL, "/")
	}
}

// SetOnAuthChange registers a callback invoked whenever OAuth token state changes (e.g., revoked or cleared).
func (m *OAuthManager) SetOnAuthChange(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onAuthChange = fn
}

func (m *OAuthManager) notifyAuthChange() {
	m.mu.RLock()
	fn := m.onAuthChange
	m.mu.RUnlock()
	if fn != nil {
		fn()
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
				_, _, _, _ = m.FetchAuthenticatedUser(ctx)
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
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized {
			_ = m.ClearTokens()
		}
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

// ClearTokens removes stored Twitch tokens and user identity from the secret store.
func (m *OAuthManager) ClearTokens() error {
	_ = m.store.Delete(SecretTwitchAccessToken)
	_ = m.store.Delete(SecretTwitchRefreshToken)
	_ = m.store.Delete(SecretTwitchTokenExpiry)
	_ = m.store.Delete(SecretTwitchUserID)
	_ = m.store.Delete(SecretTwitchUserLogin)
	_ = m.store.Delete(SecretTwitchDisplayName)
	m.notifyAuthChange()
	return nil
}

// HandleUnauthorized is called when a Twitch Helix API or EventSub call receives HTTP 401.
// It attempts a single token refresh if a refresh token is stored; if refresh fails or is unavailable,
// it clears the invalid tokens and notifies UI listeners.
func (m *OAuthManager) HandleUnauthorized(ctx context.Context) (string, error) {
	refreshTok, err := m.store.Get(SecretTwitchRefreshToken)
	if err == nil && strings.TrimSpace(refreshTok) != "" {
		if refreshed, refErr := m.RefreshToken(ctx, refreshTok); refErr == nil && refreshed != nil && refreshed.AccessToken != "" {
			return refreshed.AccessToken, nil
		}
	}
	_ = m.ClearTokens()
	return "", errors.New("twitch oauth session expired or revoked; disconnected")
}

// StoredUser returns the cached authenticated Twitch user identity (userID, login, displayName) from the secret store.
func (m *OAuthManager) StoredUser() (userID, login, displayName string) {
	if m.store == nil {
		return "", "", ""
	}
	uid, _ := m.store.Get(SecretTwitchUserID)
	ulog, _ := m.store.Get(SecretTwitchUserLogin)
	udisp, _ := m.store.Get(SecretTwitchDisplayName)
	return strings.TrimSpace(uid), strings.TrimSpace(ulog), strings.TrimSpace(udisp)
}

// FetchAuthenticatedUser queries GET /helix/users to resolve and persist the authenticated Twitch user's ID, login, and display name.
func (m *OAuthManager) FetchAuthenticatedUser(ctx context.Context) (userID, login, displayName string, err error) {
	token, err := m.GetValidAccessToken(ctx)
	if err != nil || token == "" {
		return "", "", "", err
	}

	m.mu.RLock()
	helixBase := strings.TrimRight(m.helixBaseURL, "/")
	cID := m.clientID
	client := m.httpClient
	m.mu.RUnlock()

	if helixBase == "" {
		helixBase = DefaultHelixBaseURL
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, helixBase+"/users", nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Client-Id", cID)

	resp, err := client.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		_, _ = m.HandleUnauthorized(ctx)
		return "", "", "", errors.New("twitch /helix/users HTTP 401 unauthorized")
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("twitch /helix/users HTTP %d", resp.StatusCode)
	}

	var parsed struct {
		Data []struct {
			ID          string `json:"id"`
			Login       string `json:"login"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", "", "", err
	}
	if len(parsed.Data) == 0 {
		return "", "", "", errors.New("no twitch user returned from /helix/users")
	}

	userID = strings.TrimSpace(parsed.Data[0].ID)
	login = strings.ToLower(strings.TrimSpace(parsed.Data[0].Login))
	displayName = strings.TrimSpace(parsed.Data[0].DisplayName)
	if displayName == "" {
		displayName = login
	}

	if m.store != nil {
		_ = m.store.Set(SecretTwitchUserID, userID)
		_ = m.store.Set(SecretTwitchUserLogin, login)
		_ = m.store.Set(SecretTwitchDisplayName, displayName)
	}
	return userID, login, displayName, nil
}
