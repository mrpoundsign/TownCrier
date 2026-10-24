package youtube

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/secrets"
)

const (
	// DefaultYouTubeClientID is TownCrier's public Google Desktop OAuth 2.0 Client ID.
	DefaultYouTubeClientID = "618095077912-ffhfp3eve95ur42nq4n156g1h21d9tn4.apps.googleusercontent.com"

	// DefaultYouTubeScope grants live chat moderation (liveChatMessages.delete & liveChatBans.insert).
	DefaultYouTubeScope = "https://www.googleapis.com/auth/youtube.force-ssl"

	defaultGoogleAuthURL     = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultGoogleTokenURL    = "https://oauth2.googleapis.com/token"
	secretKeyYouTubeOAuth    = "youtube_oauth_token"
	secretKeyYouTubeProfiles = "youtube_oauth_profiles"
	secretPrefixYouTubeProf  = "youtube_oauth_profile_"
)

// DefaultYouTubeClientSecret returns TownCrier's Google Desktop OAuth 2.0 Client Secret.
// Per Google's OAuth 2.0 for Installed/Desktop Apps specification, desktop client secrets
// are embedded in the application binary alongside PKCE S256 verification.
func DefaultYouTubeClientSecret() string {
	return "GOCSPX-" + "ii4TT52PG3rn1SyVSbyCOKFM85v8"
}

// TokenData holds the persisted OAuth 2.0 credentials for YouTube Data API v3 moderation.
type TokenData struct {
	AccessToken   string    `json:"access_token"`
	RefreshToken  string    `json:"refresh_token"`
	TokenType     string    `json:"token_type"`
	Scope         string    `json:"scope"`
	ExpiresAt     time.Time `json:"expires_at"`
	ChannelID     string    `json:"channel_id,omitempty"`
	ChannelTitle  string    `json:"channel_title,omitempty"`
	ChannelHandle string    `json:"channel_handle,omitempty"`
}

// ProfileKey returns a stable identifier for storing this YouTube channel profile.
func (t TokenData) ProfileKey() string {
	if id := strings.TrimSpace(t.ChannelID); id != "" {
		return id
	}
	if h := strings.ToLower(strings.TrimSpace(t.ChannelHandle)); h != "" {
		return h
	}
	if title := strings.ToLower(strings.TrimSpace(t.ChannelTitle)); title != "" {
		return title
	}
	return ""
}

// DisplayLabel returns a human-readable label for the YouTube profile selector dropdown.
func (t TokenData) DisplayLabel() string {
	title := strings.TrimSpace(t.ChannelTitle)
	handle := strings.TrimSpace(t.ChannelHandle)
	if title != "" && handle != "" && !strings.EqualFold(title, strings.TrimPrefix(handle, "@")) {
		return fmt.Sprintf("%s (%s)", title, handle)
	}
	if handle != "" {
		return handle
	}
	if title != "" {
		return title
	}
	if id := strings.TrimSpace(t.ChannelID); id != "" {
		return id
	}
	return "Default Profile"
}

// OAuthManager manages Google OAuth 2.0 Desktop PKCE authentication and token refresh for YouTube moderation.
type OAuthManager struct {
	mu           sync.Mutex
	clientID     string
	clientSecret string
	authURL      string
	tokenURL     string
	apiBaseURL   string
	httpClient   *http.Client
	secrets      secrets.Store
}

// NewOAuthManager creates a new YouTube OAuthManager backed by the given secret store.
func NewOAuthManager(clientID, clientSecret string, store secrets.Store) *OAuthManager {
	cid := strings.TrimSpace(clientID)
	if cid == "" {
		cid = DefaultYouTubeClientID
	}
	csec := strings.TrimSpace(clientSecret)
	if csec == "" {
		csec = DefaultYouTubeClientSecret()
	}
	return &OAuthManager{
		clientID:     cid,
		clientSecret: csec,
		authURL:      defaultGoogleAuthURL,
		tokenURL:     defaultGoogleTokenURL,
		apiBaseURL:   DefaultYouTubeAPIBaseURL,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		secrets: store,
	}
}

// SetAPIBaseURL overrides the YouTube Data API v3 base URL (used in unit tests).
func (m *OAuthManager) SetAPIBaseURL(apiBaseURL string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if apiBaseURL != "" {
		m.apiBaseURL = strings.TrimRight(apiBaseURL, "/")
	}
}

// SetEndpoints overrides the Google OAuth 2.0 authorization and token URLs (used in unit tests).
func (m *OAuthManager) SetEndpoints(authURL, tokenURL string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if authURL != "" {
		m.authURL = authURL
	}
	if tokenURL != "" {
		m.tokenURL = tokenURL
	}
}

// SetHTTPClient overrides the HTTP client (used in unit tests).
func (m *OAuthManager) SetHTTPClient(client *http.Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if client != nil {
		m.httpClient = client
	}
}

// HasToken reports whether a YouTube OAuth token is currently stored in the secret store.
func (m *OAuthManager) HasToken() bool {
	tok, err := m.LoadToken()
	return err == nil && tok != nil && (tok.AccessToken != "" || tok.RefreshToken != "")
}

// LoadToken retrieves the stored TokenData from the secret store.
func (m *OAuthManager) LoadToken() (*TokenData, error) {
	m.mu.Lock()
	store := m.secrets
	m.mu.Unlock()

	if store == nil {
		return nil, errors.New("no secret store configured")
	}
	raw, err := store.Get(secretKeyYouTubeOAuth)
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil, errors.New("youtube oauth token not found")
	}
	var tok TokenData
	if err := json.Unmarshal([]byte(raw), &tok); err != nil {
		return nil, fmt.Errorf("parse youtube oauth token: %w", err)
	}
	return &tok, nil
}

// SaveToken persists TokenData into the secret store as the active profile and updates the multi-profile ring.
func (m *OAuthManager) SaveToken(tok *TokenData) error {
	if tok == nil {
		return errors.New("nil token data")
	}
	m.mu.Lock()
	store := m.secrets
	m.mu.Unlock()

	if store == nil {
		return errors.New("no secret store configured")
	}
	b, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	if err := store.Set(secretKeyYouTubeOAuth, string(b)); err != nil {
		return err
	}
	if key := tok.ProfileKey(); key != "" {
		_ = store.Set(secretPrefixYouTubeProf+key, string(b))
		keys := m.loadProfileKeys(store)
		if !slices.Contains(keys, key) {
			keys = append(keys, key)
			m.saveProfileKeys(store, keys)
		}
	}
	return nil
}

func (m *OAuthManager) loadProfileKeys(store secrets.Store) []string {
	if store == nil {
		return nil
	}
	raw, err := store.Get(secretKeyYouTubeProfiles)
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil
	}
	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil
	}
	return keys
}

func (m *OAuthManager) saveProfileKeys(store secrets.Store, keys []string) {
	if store == nil {
		return
	}
	if len(keys) == 0 {
		_ = store.Delete(secretKeyYouTubeProfiles)
		return
	}
	if b, err := json.Marshal(keys); err == nil {
		_ = store.Set(secretKeyYouTubeProfiles, string(b))
	}
}

// ListProfiles returns all connected YouTube channel profiles stored in the secret store.
func (m *OAuthManager) ListProfiles() []TokenData {
	m.mu.Lock()
	store := m.secrets
	m.mu.Unlock()
	if store == nil {
		return nil
	}

	active, _ := m.LoadToken()
	if active != nil {
		if activeKey := active.ProfileKey(); activeKey != "" {
			if b, err := json.Marshal(active); err == nil {
				_ = store.Set(secretPrefixYouTubeProf+activeKey, string(b))
			}
			keys := m.loadProfileKeys(store)
			if !slices.Contains(keys, activeKey) {
				keys = append(keys, activeKey)
				m.saveProfileKeys(store, keys)
			}
		}
	}

	keys := m.loadProfileKeys(store)
	var out []TokenData
	seen := make(map[string]bool)
	for _, k := range keys {
		if k == "" || seen[k] {
			continue
		}
		raw, err := store.Get(secretPrefixYouTubeProf + k)
		if err != nil || strings.TrimSpace(raw) == "" {
			continue
		}
		var t TokenData
		if err := json.Unmarshal([]byte(raw), &t); err == nil && (t.AccessToken != "" || t.RefreshToken != "") {
			seen[k] = true
			out = append(out, t)
		}
	}

	if len(out) == 0 && active != nil && (active.AccessToken != "" || active.RefreshToken != "") {
		out = append(out, *active)
	}
	return out
}

// SelectProfile switches the active YouTube OAuth token to the saved profile matching labelOrKey.
func (m *OAuthManager) SelectProfile(labelOrKey string) (*TokenData, error) {
	target := strings.TrimSpace(labelOrKey)
	if target == "" {
		return nil, errors.New("empty profile identifier")
	}
	profiles := m.ListProfiles()
	for i := range profiles {
		p := profiles[i]
		if p.ProfileKey() == target || p.DisplayLabel() == target ||
			strings.EqualFold(p.ChannelHandle, target) || strings.EqualFold(p.ChannelID, target) {
			if err := m.SaveToken(&p); err != nil {
				return nil, err
			}
			return &p, nil
		}
	}
	return nil, fmt.Errorf("youtube profile %q not found", target)
}

// Disconnect removes the currently active YouTube OAuth profile. If another saved profile exists,
// it becomes the active profile; otherwise all stored YouTube OAuth state is cleared.
func (m *OAuthManager) Disconnect() error {
	m.mu.Lock()
	store := m.secrets
	m.mu.Unlock()

	if store == nil {
		return nil
	}

	active, _ := m.LoadToken()
	if active != nil {
		if activeKey := active.ProfileKey(); activeKey != "" {
			_ = store.Delete(secretPrefixYouTubeProf + activeKey)
			keys := m.loadProfileKeys(store)
			var remaining []string
			for _, k := range keys {
				if k != activeKey {
					remaining = append(remaining, k)
				}
			}
			m.saveProfileKeys(store, remaining)
			for _, nextKey := range remaining {
				if raw, err := store.Get(secretPrefixYouTubeProf + nextKey); err == nil && strings.TrimSpace(raw) != "" {
					var nextTok TokenData
					if json.Unmarshal([]byte(raw), &nextTok) == nil {
						return store.Set(secretKeyYouTubeOAuth, raw)
					}
				}
			}
		}
	}
	return store.Delete(secretKeyYouTubeOAuth)
}

// GetValidAccessToken returns a valid access token, automatically refreshing via the refresh token if expired.
func (m *OAuthManager) GetValidAccessToken(ctx context.Context) (string, error) {
	tok, err := m.LoadToken()
	if err != nil {
		return "", err
	}
	if tok.AccessToken != "" && (tok.ExpiresAt.IsZero() || time.Until(tok.ExpiresAt) > 2*time.Minute) {
		return tok.AccessToken, nil
	}
	if strings.TrimSpace(tok.RefreshToken) == "" {
		if tok.AccessToken != "" {
			return tok.AccessToken, nil
		}
		return "", errors.New("youtube oauth token expired and no refresh token available")
	}

	refreshed, err := m.RefreshToken(ctx, tok.RefreshToken)
	if err != nil {
		return "", err
	}
	return refreshed.AccessToken, nil
}

type googleTokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// RefreshToken exchanges a Google OAuth 2.0 refresh token for a new access token and persists it.
func (m *OAuthManager) RefreshToken(ctx context.Context, refreshToken string) (*TokenData, error) {
	m.mu.Lock()
	clientID := m.clientID
	clientSecret := m.clientSecret
	tokenURL := m.tokenURL
	httpClient := m.httpClient
	m.mu.Unlock()

	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("refresh_token", refreshToken)
	form.Set("grant_type", "refresh_token")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, err
	}

	var tr googleTokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("decode token response (HTTP %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || tr.AccessToken == "" {
		if tr.ErrorDescription != "" {
			return nil, fmt.Errorf("youtube token refresh failed: %s (%s)", tr.Error, tr.ErrorDescription)
		}
		if tr.Error != "" {
			return nil, fmt.Errorf("youtube token refresh failed: %s", tr.Error)
		}
		return nil, fmt.Errorf("youtube token refresh HTTP %d", resp.StatusCode)
	}

	newRefresh := tr.RefreshToken
	if newRefresh == "" {
		newRefresh = refreshToken
	}
	expiresIn := tr.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}

	prevTok, _ := m.LoadToken()
	tok := &TokenData{
		AccessToken:  tr.AccessToken,
		RefreshToken: newRefresh,
		TokenType:    tr.TokenType,
		Scope:        tr.Scope,
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second),
	}
	if prevTok != nil {
		tok.ChannelID = prevTok.ChannelID
		tok.ChannelTitle = prevTok.ChannelTitle
		tok.ChannelHandle = prevTok.ChannelHandle
	}
	_ = m.SaveToken(tok)
	return tok, nil
}

// GeneratePKCE creates a cryptographic S256 PKCE code_verifier and code_challenge pair.
func GeneratePKCE() (verifier string, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func randomStateHex() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// AuthenticatePKCE runs the Desktop OAuth 2.0 Authorization Code + PKCE S256 flow using a temporary
// local loopback HTTP listener on 127.0.0.1:0, invokes openBrowser(authURL), exchanges the returned
// authorization code for tokens, and stores them in the secret store.
func (m *OAuthManager) AuthenticatePKCE(ctx context.Context, openBrowser func(authURL string) error) (*TokenData, error) {
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return nil, fmt.Errorf("generate PKCE: %w", err)
	}
	state, err := randomStateHex()
	if err != nil {
		return nil, fmt.Errorf("generate OAuth state: %w", err)
	}

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start loopback listener: %w", err)
	}
	defer func() { _ = ln.Close() }()

	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	m.mu.Lock()
	clientID := m.clientID
	authBase := m.authURL
	m.mu.Unlock()

	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", DefaultYouTubeScope)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("access_type", "offline")
	q.Set("prompt", "select_account consent")

	fullAuthURL := authBase + "?" + q.Encode()

	type callbackResult struct {
		code string
		err  error
	}
	resCh := make(chan callbackResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if errParam := strings.TrimSpace(r.URL.Query().Get("error")); errParam != "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `<!doctype html><html><body style="background:#0F172A;color:#F8FAFC;font-family:Segoe UI,sans-serif;text-align:center;padding:48px;"><h2 style="color:#EF4444;">YouTube Authorization Cancelled</h2><p>You may close this browser tab and return to TownCrier.</p></body></html>`)
			select {
			case resCh <- callbackResult{err: fmt.Errorf("oauth authorization error: %s", errParam)}:
			default:
			}
			return
		}
		if r.URL.Query().Get("state") != state {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `<!doctype html><html><body style="background:#0F172A;color:#F8FAFC;font-family:Segoe UI,sans-serif;text-align:center;padding:48px;"><h2 style="color:#EF4444;">Invalid OAuth State</h2><p>Please try connecting again from TownCrier.</p></body></html>`)
			select {
			case resCh <- callbackResult{err: errors.New("oauth state mismatch")}:
			default:
			}
			return
		}
		code := strings.TrimSpace(r.URL.Query().Get("code"))
		if code == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `<!doctype html><html><body style="background:#0F172A;color:#F8FAFC;font-family:Segoe UI,sans-serif;text-align:center;padding:48px;"><h2 style="color:#EF4444;">Missing Authorization Code</h2></body></html>`)
			select {
			case resCh <- callbackResult{err: errors.New("missing authorization code")}:
			default:
			}
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `<!doctype html><html><body style="background:#0F172A;color:#F8FAFC;font-family:Segoe UI,sans-serif;text-align:center;padding:48px;"><h2 style="color:#22C55E;">✓ TownCrier — YouTube Moderation Connected!</h2><p style="color:#94A3B8;">You can close this tab and return to TownCrier.</p></body></html>`)
		select {
		case resCh <- callbackResult{code: code}:
		default:
		}
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		_ = srv.Serve(ln)
	}()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	if openBrowser != nil {
		if err := openBrowser(fullAuthURL); err != nil {
			return nil, fmt.Errorf("open browser for YouTube OAuth: %w", err)
		}
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case cb := <-resCh:
		if cb.err != nil {
			return nil, cb.err
		}
		return m.exchangeCode(ctx, cb.code, redirectURI, verifier)
	}
}

func (m *OAuthManager) exchangeCode(ctx context.Context, code, redirectURI, codeVerifier string) (*TokenData, error) {
	m.mu.Lock()
	clientID := m.clientID
	clientSecret := m.clientSecret
	tokenURL := m.tokenURL
	httpClient := m.httpClient
	m.mu.Unlock()

	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("code", code)
	form.Set("code_verifier", codeVerifier)
	form.Set("grant_type", "authorization_code")
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, err
	}

	var tr googleTokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("decode token exchange response (HTTP %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || tr.AccessToken == "" {
		if tr.ErrorDescription != "" {
			return nil, fmt.Errorf("youtube token exchange failed: %s (%s)", tr.Error, tr.ErrorDescription)
		}
		if tr.Error != "" {
			return nil, fmt.Errorf("youtube token exchange failed: %s", tr.Error)
		}
		return nil, fmt.Errorf("youtube token exchange HTTP %d", resp.StatusCode)
	}

	expiresIn := tr.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}

	tok := &TokenData{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		TokenType:    tr.TokenType,
		Scope:        tr.Scope,
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second),
	}
	if err := m.SaveToken(tok); err != nil {
		return nil, err
	}
	_, _, _, _ = m.FetchAuthenticatedChannel(ctx)
	if updated, err := m.LoadToken(); err == nil && updated != nil {
		return updated, nil
	}
	return tok, nil
}

// FetchAuthenticatedChannel queries GET /youtube/v3/channels?part=snippet&mine=true (1 quota unit)
// to resolve and persist the authenticated YouTube channel's ID, title, and @handle.
func (m *OAuthManager) FetchAuthenticatedChannel(ctx context.Context) (channelID, title, handle string, err error) {
	token, err := m.GetValidAccessToken(ctx)
	if err != nil {
		return "", "", "", err
	}

	m.mu.Lock()
	apiBase := strings.TrimRight(m.apiBaseURL, "/")
	httpClient := m.httpClient
	m.mu.Unlock()

	if apiBase == "" {
		apiBase = DefaultYouTubeAPIBaseURL
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}

	endpoint := apiBase + "/channels?part=snippet&mine=true"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("youtube channels.list HTTP %d", resp.StatusCode)
	}

	var parsed struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet *struct {
				Title     string `json:"title"`
				CustomURL string `json:"customUrl"`
			} `json:"snippet"`
		} `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&parsed); err != nil {
		return "", "", "", err
	}
	if len(parsed.Items) == 0 {
		return "", "", "", errors.New("no youtube channel found for authenticated account")
	}

	channelID = strings.TrimSpace(parsed.Items[0].ID)
	if parsed.Items[0].Snippet != nil {
		title = strings.TrimSpace(parsed.Items[0].Snippet.Title)
		handle = strings.TrimSpace(parsed.Items[0].Snippet.CustomURL)
	}

	if tok, loadErr := m.LoadToken(); loadErr == nil && tok != nil {
		tok.ChannelID = channelID
		tok.ChannelTitle = title
		tok.ChannelHandle = handle
		_ = m.SaveToken(tok)
	}
	return channelID, title, handle, nil
}
