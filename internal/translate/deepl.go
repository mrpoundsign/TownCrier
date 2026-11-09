package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	defaultDeepLFreeEndpoint = "https://api-free.deepl.com/v2/translate"
	defaultDeepLProEndpoint  = "https://api.deepl.com/v2/translate"
)

type deepLRequest struct {
	Text       []string `json:"text"`
	TargetLang string   `json:"target_lang"`
}

type deepLResponse struct {
	Translations []struct {
		DetectedSourceLanguage string `json:"detected_source_language"`
		Text                   string `json:"text"`
	} `json:"translations"`
	Message string `json:"message,omitempty"`
}

func (s *Service) translateDeepL(ctx context.Context, text string, opts Options) (Result, error) {
	apiKey := strings.TrimSpace(opts.APIKey)
	if apiKey == "" {
		return Result{}, markHardQuotaOrAuth(errors.New("missing DeepL API key (get a free 500,000 chars/month key at deepl.com/pro-api, or switch Provider to MyMemory Free)"))
	}

	s.mu.Lock()
	baseURL := s.deeplURL
	client := s.client
	s.mu.Unlock()

	if strings.TrimSpace(baseURL) == "" {
		if strings.HasSuffix(strings.ToLower(apiKey), ":fx") {
			baseURL = defaultDeepLFreeEndpoint
		} else {
			baseURL = defaultDeepLProEndpoint
		}
	}

	payload, err := json.Marshal(deepLRequest{
		Text:       []string{text},
		TargetLang: strings.ToUpper(strings.TrimSpace(opts.TargetLang)),
	})
	if err != nil {
		return Result{}, fmt.Errorf("marshal deepl request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, bytes.NewReader(payload))
	if err != nil {
		return Result{}, fmt.Errorf("create deepl request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "DeepL-Auth-Key "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("deepl request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		var errResp deepLResponse
		var apiErr error
		if json.Unmarshal(body, &errResp) == nil && strings.TrimSpace(errResp.Message) != "" {
			apiErr = fmt.Errorf("deepl API error (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(errResp.Message))
		} else {
			apiErr = fmt.Errorf("deepl HTTP %d: %s", resp.StatusCode, sanitizeHTTPErrorBody(body))
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == 456 {
			return Result{}, markHardQuotaOrAuth(apiErr)
		}
		return Result{}, apiErr
	}

	var parsed deepLResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Result{}, fmt.Errorf("decode deepl response: %w", err)
	}
	if len(parsed.Translations) == 0 || strings.TrimSpace(parsed.Translations[0].Text) == "" {
		return Result{}, errors.New("deepl returned empty translation")
	}

	first := parsed.Translations[0]
	return Result{
		TranslatedText: strings.TrimSpace(first.Text),
		SourceLang:     NormalizeLangCode(first.DetectedSourceLanguage),
	}, nil
}
