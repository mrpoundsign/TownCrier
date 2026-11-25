package pluginhost

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

const (
	defaultHTTPTimeout   = 10 * time.Second
	maxHTTPTimeout       = 15 * time.Second
	minHTTPTimeout       = 100 * time.Millisecond
	maxHTTPResponseBytes = 512 * 1024
)

// IsHostAllowed verifies rawURL uses http/https and matches at least one explicit entry in allowedHosts.
// Patterns accept exact hostnames ("api.example.com"), host:port ("127.0.0.1:8080"), wildcard ports ("127.0.0.1:*"),
// or wildcard subdomains ("*.example.com").
func IsHostAllowed(rawURL string, allowedHosts []string) bool {
	if len(allowedHosts) == 0 {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return false
	}
	hostname := strings.ToLower(parsed.Hostname())
	hostPort := strings.ToLower(parsed.Host)
	if hostname == "" || parsed.User != nil {
		return false
	}

	for _, rawPattern := range allowedHosts {
		pat := strings.ToLower(strings.TrimSpace(rawPattern))
		if pat == "" {
			continue
		}
		switch {
		case strings.HasSuffix(pat, ":*"):
			wantHost := strings.TrimSuffix(pat, ":*")
			if hostname == wantHost {
				return true
			}
		case strings.Contains(pat, ":"):
			if hostPort == pat {
				return true
			}
		case strings.HasPrefix(pat, "*."):
			suffix := pat[1:]
			if len(hostname) > len(suffix) && strings.HasSuffix(hostname, suffix) {
				return true
			}
		default:
			if hostname == pat {
				return true
			}
		}
	}
	return false
}

// ExecuteHTTP performs an outbound HTTP request enforcing allowlist rules, bounded timeouts, and response caps.
func ExecuteHTTP(
	ctx context.Context,
	client *http.Client,
	allowedHosts []string,
	enforceAllowlist bool,
	req *pluginv1.HTTPRequest,
) (*pluginv1.HTTPResponse, *pluginv1.APIError) {
	if req == nil || strings.TrimSpace(req.GetUrl()) == "" {
		return nil, &pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			Message: "HTTPRequest requires a non-empty URL",
		}
	}
	if enforceAllowlist && !IsHostAllowed(req.GetUrl(), allowedHosts) {
		return &pluginv1.HTTPResponse{
				Status:  http.StatusForbidden,
				Error:   "URL blocked by plugin permissions.allowed_hosts sandbox",
				AsyncId: req.GetAsyncId(),
			}, &pluginv1.APIError{
				Code:    pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED,
				Message: fmt.Sprintf("HTTP request to %q blocked by plugin allowed_hosts sandbox", req.GetUrl()),
				Detail:  "Declare matching hosts in manifest.permissions.allowed_hosts",
			}
	}

	if client == nil {
		client = &http.Client{Timeout: maxHTTPTimeout}
	}
	method := strings.ToUpper(strings.TrimSpace(req.GetMethod()))
	if method == "" {
		method = http.MethodGet
	}

	timeout := defaultHTTPTimeout
	if req.GetTimeoutMs() > 0 {
		timeout = min(max(time.Duration(req.GetTimeoutMs())*time.Millisecond, minHTTPTimeout), maxHTTPTimeout)
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var bodyReader io.Reader
	if len(req.GetBody()) > 0 {
		bodyReader = bytes.NewReader(req.GetBody())
	}
	httpReq, err := http.NewRequestWithContext(reqCtx, method, req.GetUrl(), bodyReader)
	if err != nil {
		return &pluginv1.HTTPResponse{
			Status:  0,
			Error:   err.Error(),
			AsyncId: req.GetAsyncId(),
		}, nil
	}
	for k, v := range req.GetHeaders() {
		httpReq.Header.Set(k, v)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return &pluginv1.HTTPResponse{
			Status:  0,
			Error:   err.Error(),
			AsyncId: req.GetAsyncId(),
		}, nil
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPResponseBytes))
	if err != nil {
		return &pluginv1.HTTPResponse{
			Status:  int32(resp.StatusCode),
			Error:   err.Error(),
			AsyncId: req.GetAsyncId(),
		}, nil
	}

	headers := make(map[string]string, len(resp.Header))
	for k, vals := range resp.Header {
		if len(vals) > 0 {
			headers[k] = vals[0]
		}
	}

	return &pluginv1.HTTPResponse{
		Status:  int32(resp.StatusCode),
		Headers: headers,
		Body:    bodyBytes,
		AsyncId: req.GetAsyncId(),
	}, nil
}
