package kick

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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/secrets"
)

const (
	// DefaultKickClientID is TownCrier's Kick OAuth 2.1 application Client ID.
	DefaultKickClientID = "01M5QQNB2QMZ9M03WS4B4VQBVS"

	// DefaultKickScopes grants user/channel reading, chat moderation, and reward reading via Kick Public API v1.
	DefaultKickScopes = "user:read channel:read moderation:ban moderation:chat_message:manage chat:write channel:rewards:read"

	// DefaultKickPublicAPIBaseURL is the base URL for Kick's official Public API v1.
	DefaultKickPublicAPIBaseURL = "https://api.kick.com/public/v1"

	// DefaultKickRedirectPort is the local loopback port registered in the Kick Developer Portal
	// (http://localhost:54321/kick/callback).
	DefaultKickRedirectPort = 54321

	defaultKickAuthURL   = "https://id.kick.com/oauth/authorize"
	defaultKickTokenURL  = "https://id.kick.com/oauth/token"
	defaultKickRevokeURL = "https://id.kick.com/oauth/revoke"
	secretKeyKickOAuth   = "kick_oauth_token"
)

// DefaultKickClientSecret returns TownCrier's Kick OAuth 2.1 Client Secret.
func DefaultKickClientSecret() string {
	return "38b5b2d49f901fdb1177d7d20a7f2f5c" + "628eab864ec488ab82539f5a141f16d9"
}

// TokenData holds the persisted OAuth 2.1 credentials and authenticated channel identity for Kick Public API v1.
type TokenData struct {
	AccessToken       string    `json:"access_token"`
	RefreshToken      string    `json:"refresh_token"`
	TokenType         string    `json:"token_type"`
	Scope             string    `json:"scope"`
	ExpiresAt         time.Time `json:"expires_at"`
	BroadcasterUserID int64     `json:"broadcaster_user_id,omitempty"`
	Slug              string    `json:"slug,omitempty"`
	Username          string    `json:"username,omitempty"`
}

// OAuthManager manages Kick OAuth 2.1 Authorization Code + PKCE S256 authentication and token refresh.
type OAuthManager struct {
	mu           sync.Mutex
	clientID     string
	clientSecret string
	authURL      string
	tokenURL     string
	revokeURL    string
	publicAPIURL string
	redirectPort int
	httpClient   *http.Client
	secrets      secrets.Store
	onAuthChange func()
}

// NewOAuthManager creates a new Kick OAuthManager backed by the given secret store.
func NewOAuthManager(clientID, clientSecret string, store secrets.Store) *OAuthManager {
	cid := strings.TrimSpace(clientID)
	if cid == "" {
		cid = DefaultKickClientID
	}
	csec := strings.TrimSpace(clientSecret)
	if csec == "" {
		csec = DefaultKickClientSecret()
	}
	return &OAuthManager{
		clientID:     cid,
		clientSecret: csec,
		authURL:      defaultKickAuthURL,
		tokenURL:     defaultKickTokenURL,
		revokeURL:    defaultKickRevokeURL,
		publicAPIURL: DefaultKickPublicAPIBaseURL,
		redirectPort: DefaultKickRedirectPort,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		secrets: store,
	}
}

// SetClientCredentials updates the Kick OAuth 2.1 Client ID and Client Secret.
func (m *OAuthManager) SetClientCredentials(clientID, clientSecret string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clientID = strings.TrimSpace(clientID)
	m.clientSecret = strings.TrimSpace(clientSecret)
}

// SetEndpoints overrides the Kick OAuth 2.1 authorization, token, and Public API v1 URLs (used in unit tests).
func (m *OAuthManager) SetEndpoints(authURL, tokenURL, publicAPIURL string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if authURL != "" {
		m.authURL = authURL
	}
	if tokenURL != "" {
		m.tokenURL = tokenURL
	}
	if publicAPIURL != "" {
		m.publicAPIURL = strings.TrimRight(publicAPIURL, "/")
	}
}

// SetRedirectPort overrides the loopback callback port (set to 0 in unit tests for an ephemeral port).
func (m *OAuthManager) SetRedirectPort(port int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.redirectPort = port
}

// SetHTTPClient overrides the HTTP client (used in unit tests).
func (m *OAuthManager) SetHTTPClient(client *http.Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if client != nil {
		m.httpClient = client
	}
}

// SetOnAuthChange registers a callback invoked whenever OAuth token state changes (e.g., revoked or disconnected).
func (m *OAuthManager) SetOnAuthChange(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onAuthChange = fn
}

func (m *OAuthManager) notifyAuthChange() {
	m.mu.Lock()
	fn := m.onAuthChange
	m.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// HasToken reports whether a Kick OAuth token is currently stored in the secret store.
func (m *OAuthManager) HasToken() bool {
	tok, err := m.LoadToken()
	return err == nil && tok != nil && (tok.AccessToken != "" || tok.RefreshToken != "")
}

// LoadToken retrieves the stored Kick TokenData from the secret store.
func (m *OAuthManager) LoadToken() (*TokenData, error) {
	m.mu.Lock()
	store := m.secrets
	m.mu.Unlock()

	if store == nil {
		return nil, errors.New("no secret store configured")
	}
	raw, err := store.Get(secretKeyKickOAuth)
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil, errors.New("kick oauth token not found")
	}
	var tok TokenData
	if err := json.Unmarshal([]byte(raw), &tok); err != nil {
		return nil, fmt.Errorf("parse kick oauth token: %w", err)
	}
	return &tok, nil
}

// SaveToken persists TokenData into the secret store.
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
	return store.Set(secretKeyKickOAuth, string(b))
}

// Disconnect removes any stored Kick OAuth token from the secret store and notifies UI listeners.
func (m *OAuthManager) Disconnect() error {
	m.mu.Lock()
	store := m.secrets
	m.mu.Unlock()

	if store == nil {
		return nil
	}
	err := store.Delete(secretKeyKickOAuth)
	m.notifyAuthChange()
	return err
}

// HandleUnauthorized is called when a Kick Public API v1 request receives HTTP 401 Unauthorized.
// It attempts a single token refresh if a refresh token is stored; if refresh fails or is unavailable,
// it disconnects the invalid token and notifies UI listeners.
func (m *OAuthManager) HandleUnauthorized(ctx context.Context) (string, error) {
	tok, err := m.LoadToken()
	if err != nil || tok == nil {
		_ = m.Disconnect()
		return "", errors.New("kick oauth token unauthorized and disconnected")
	}
	if strings.TrimSpace(tok.RefreshToken) != "" {
		if refreshed, refErr := m.RefreshToken(ctx, tok.RefreshToken); refErr == nil && refreshed != nil && refreshed.AccessToken != "" {
			return refreshed.AccessToken, nil
		}
	}
	_ = m.Disconnect()
	return "", errors.New("kick oauth session expired or revoked; disconnected")
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
		return "", errors.New("kick oauth token expired and no refresh token available")
	}

	refreshed, err := m.RefreshToken(ctx, tok.RefreshToken)
	if err != nil {
		return "", err
	}
	return refreshed.AccessToken, nil
}

type kickTokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// RefreshToken exchanges a Kick OAuth 2.1 refresh token for a new access token and persists it.
func (m *OAuthManager) RefreshToken(ctx context.Context, refreshToken string) (*TokenData, error) {
	m.mu.Lock()
	clientID := m.clientID
	clientSecret := m.clientSecret
	tokenURL := m.tokenURL
	httpClient := m.httpClient
	m.mu.Unlock()

	form := url.Values{}
	form.Set("client_id", clientID)
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
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

	var tr kickTokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("decode kick token response (HTTP %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || tr.AccessToken == "" {
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized || strings.EqualFold(tr.Error, "invalid_grant") {
			_ = m.Disconnect()
		}
		if tr.ErrorDescription != "" {
			return nil, fmt.Errorf("kick token refresh failed: %s (%s)", tr.Error, tr.ErrorDescription)
		}
		if tr.Error != "" {
			return nil, fmt.Errorf("kick token refresh failed: %s", tr.Error)
		}
		return nil, fmt.Errorf("kick token refresh HTTP %d", resp.StatusCode)
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
		tok.BroadcasterUserID = prevTok.BroadcasterUserID
		tok.Slug = prevTok.Slug
		tok.Username = prevTok.Username
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

// AuthenticatePKCE runs the Kick OAuth 2.1 Authorization Code + PKCE S256 flow using a local loopback
// HTTP listener on 127.0.0.1:54321/kick/callback, invokes openBrowser(authURL), exchanges the returned
// authorization code for tokens, and resolves the authenticated Kick channel via Public API v1.
func (m *OAuthManager) AuthenticatePKCE(ctx context.Context, openBrowser func(authURL string) error) (*TokenData, error) {
	m.mu.Lock()
	clientID := m.clientID
	authBase := m.authURL
	portCfg := m.redirectPort
	m.mu.Unlock()

	if strings.TrimSpace(clientID) == "" {
		return nil, errors.New("kick OAuth Client ID is not configured yet")
	}

	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return nil, fmt.Errorf("generate PKCE: %w", err)
	}
	state, err := randomStateHex()
	if err != nil {
		return nil, fmt.Errorf("generate OAuth state: %w", err)
	}

	listenAddr := "127.0.0.1:" + strconv.Itoa(portCfg)
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("start loopback listener on %s: %w", listenAddr, err)
	}
	defer func() { _ = ln.Close() }()

	actualPort := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d/kick/callback", actualPort)

	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", DefaultKickScopes)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)

	fullAuthURL := authBase + "?" + q.Encode()

	type callbackResult struct {
		code string
		err  error
	}
	resCh := make(chan callbackResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/kick/callback", func(w http.ResponseWriter, r *http.Request) {
		if errParam := strings.TrimSpace(r.URL.Query().Get("error")); errParam != "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `<!doctype html><html><body style="background:#0F172A;color:#F8FAFC;font-family:Segoe UI,sans-serif;text-align:center;padding:48px;"><h2 style="color:#EF4444;">Kick Authorization Cancelled</h2><p>You may close this browser tab and return to TownCrier.</p></body></html>`)
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
		_, _ = io.WriteString(w, `<!doctype html><html><body style="background:#0F172A;color:#F8FAFC;font-family:Segoe UI,sans-serif;text-align:center;padding:48px;"><h2 style="color:#22C55E;">✓ TownCrier — Kick Connected!</h2><p style="color:#94A3B8;">You can close this tab and return to TownCrier.</p></body></html>`)
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
			return nil, fmt.Errorf("open browser for Kick OAuth: %w", err)
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
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
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

	var tr kickTokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("decode kick token exchange response (HTTP %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || tr.AccessToken == "" {
		if tr.ErrorDescription != "" {
			return nil, fmt.Errorf("kick token exchange failed: %s (%s)", tr.Error, tr.ErrorDescription)
		}
		if tr.Error != "" {
			return nil, fmt.Errorf("kick token exchange failed: %s", tr.Error)
		}
		return nil, fmt.Errorf("kick token exchange HTTP %d", resp.StatusCode)
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

// FetchAuthenticatedChannel queries GET /public/v1/channels (with no query parameters, returning the authenticated
// broadcaster's channel) to resolve and persist BroadcasterUserID and Slug.
func (m *OAuthManager) FetchAuthenticatedChannel(ctx context.Context) (broadcasterUserID int64, slug string, isLive bool, err error) {
	token, err := m.GetValidAccessToken(ctx)
	if err != nil || token == "" {
		return 0, "", false, err
	}

	m.mu.Lock()
	apiBase := strings.TrimRight(m.publicAPIURL, "/")
	httpClient := m.httpClient
	m.mu.Unlock()

	if apiBase == "" {
		apiBase = DefaultKickPublicAPIBaseURL
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/channels", nil)
	if err != nil {
		return 0, "", false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, "", false, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		_, _ = m.HandleUnauthorized(ctx)
		return 0, "", false, errors.New("kick /public/v1/channels HTTP 401 unauthorized")
	}
	if resp.StatusCode != http.StatusOK {
		return 0, "", false, fmt.Errorf("kick /public/v1/channels HTTP %d", resp.StatusCode)
	}

	var parsed struct {
		Data []struct {
			BroadcasterUserID int64  `json:"broadcaster_user_id"`
			Slug              string `json:"slug"`
			Stream            *struct {
				IsLive bool `json:"is_live"`
			} `json:"stream"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&parsed); err != nil {
		return 0, "", false, err
	}
	if len(parsed.Data) == 0 {
		return 0, "", false, errors.New("no kick channel returned from /public/v1/channels")
	}

	broadcasterUserID = parsed.Data[0].BroadcasterUserID
	slug = strings.ToLower(strings.TrimSpace(parsed.Data[0].Slug))
	if parsed.Data[0].Stream != nil {
		isLive = parsed.Data[0].Stream.IsLive
	}

	if tok, loadErr := m.LoadToken(); loadErr == nil && tok != nil {
		if broadcasterUserID > 0 {
			tok.BroadcasterUserID = broadcasterUserID
		}
		if slug != "" {
			tok.Slug = slug
			if tok.Username == "" {
				tok.Username = slug
			}
		}
		_ = m.SaveToken(tok)
	}
	return broadcasterUserID, slug, isLive, nil
}
