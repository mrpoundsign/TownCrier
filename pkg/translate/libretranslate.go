package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type libreTranslateRequest struct {
	Q      string `json:"q"`
	Source string `json:"source"`
	Target string `json:"target"`
	Format string `json:"format"`
	APIKey string `json:"api_key,omitempty"`
}

type libreTranslateResponse struct {
	TranslatedText   string `json:"translatedText"`
	DetectedLanguage *struct {
		Language   string  `json:"language"`
		Confidence float64 `json:"confidence"`
	} `json:"detectedLanguage,omitempty"`
	Error string `json:"error,omitempty"`
}

func (s *Service) translateLibreTranslate(ctx context.Context, text string, opts Options) (Result, error) {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()

	endpoint := strings.TrimSpace(opts.EndpointURL)
	if endpoint == "" {
		endpoint = DefaultLibreTranslateURL
	}
	endpoint = strings.TrimRight(endpoint, "/")
	if !strings.HasSuffix(endpoint, "/translate") {
		endpoint += "/translate"
	}

	reqBody := libreTranslateRequest{
		Q:      text,
		Source: "auto",
		Target: opts.TargetLang,
		Format: "text",
		APIKey: strings.TrimSpace(opts.APIKey),
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return Result{}, fmt.Errorf("marshal libretranslate request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return Result{}, fmt.Errorf("create libretranslate request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("libretranslate request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var parsed libreTranslateResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Result{}, fmt.Errorf("decode libretranslate response (HTTP %d): %w", resp.StatusCode, err)
	}
	if parsed.Error != "" {
		return Result{}, fmt.Errorf("libretranslate error: %s", parsed.Error)
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("libretranslate HTTP %d", resp.StatusCode)
	}
	if strings.TrimSpace(parsed.TranslatedText) == "" {
		return Result{}, errors.New("libretranslate returned empty translation")
	}

	srcLang := opts.SourceLang
	if parsed.DetectedLanguage != nil && parsed.DetectedLanguage.Language != "" {
		srcLang = NormalizeLangCode(parsed.DetectedLanguage.Language)
	}

	return Result{
		TranslatedText: strings.TrimSpace(parsed.TranslatedText),
		SourceLang:     srcLang,
	}, nil
}
