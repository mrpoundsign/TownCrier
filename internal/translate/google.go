package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
)

const defaultGoogleCloudEndpoint = "https://translation.googleapis.com/language/translate/v2"

type googleCloudRequest struct {
	Q      string `json:"q"`
	Target string `json:"target"`
	Format string `json:"format"`
}

type googleCloudResponse struct {
	Data struct {
		Translations []struct {
			TranslatedText         string `json:"translatedText"`
			DetectedSourceLanguage string `json:"detectedSourceLanguage"`
		} `json:"translations"`
	} `json:"data"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func sanitizeHTTPErrorBody(body []byte) string {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return "empty response"
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype") {
		return "remote server returned an HTML error page"
	}
	if len(s) > 160 {
		return s[:160] + "..."
	}
	return s
}

func (s *Service) translateGoogle(ctx context.Context, text string, opts Options) (Result, error) {
	apiKey := strings.TrimSpace(opts.APIKey)
	if apiKey == "" {
		return Result{}, markHardQuotaOrAuth(errors.New("missing Google Cloud Translation API key (enter a key or switch Provider to MyMemory Free for zero-setup translation)"))
	}
	return s.translateGoogleCloudV2(ctx, text, opts.TargetLang, apiKey)
}

func (s *Service) translateGoogleCloudV2(ctx context.Context, text, targetLang, apiKey string) (Result, error) {
	s.mu.Lock()
	baseURL := s.googleCloudURL
	client := s.client
	s.mu.Unlock()

	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultGoogleCloudEndpoint
	}

	payload, err := json.Marshal(googleCloudRequest{
		Q:      text,
		Target: targetLang,
		Format: "text",
	})
	if err != nil {
		return Result{}, fmt.Errorf("marshal google cloud request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, bytes.NewReader(payload))
	if err != nil {
		return Result{}, fmt.Errorf("create google cloud request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("google cloud request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return Result{}, fmt.Errorf("read google cloud response: %w", err)
	}

	var parsed googleCloudResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Result{}, fmt.Errorf("google cloud HTTP %d: %s", resp.StatusCode, sanitizeHTTPErrorBody(body))
	}
	if parsed.Error != nil {
		err := fmt.Errorf("google cloud API error %d: %s", parsed.Error.Code, parsed.Error.Message)
		if parsed.Error.Code == http.StatusUnauthorized || parsed.Error.Code == http.StatusForbidden {
			return Result{}, markHardQuotaOrAuth(err)
		}
		return Result{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("google cloud HTTP %d", resp.StatusCode)
	}
	if len(parsed.Data.Translations) == 0 || strings.TrimSpace(parsed.Data.Translations[0].TranslatedText) == "" {
		return Result{}, errors.New("google cloud returned empty translation")
	}

	first := parsed.Data.Translations[0]
	return Result{
		TranslatedText: strings.TrimSpace(html.UnescapeString(first.TranslatedText)),
		SourceLang:     NormalizeLangCode(first.DetectedSourceLanguage),
	}, nil
}
