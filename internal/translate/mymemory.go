package translate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const defaultMyMemoryEndpoint = "https://api.mymemory.translated.net/get"

type myMemoryResponse struct {
	ResponseData struct {
		TranslatedText string  `json:"translatedText"`
		Match          float64 `json:"match"`
	} `json:"responseData"`
	QuotaFinished   bool   `json:"quotaFinished"`
	ResponseDetails string `json:"responseDetails"`
	ResponseStatus  any    `json:"responseStatus"`
}

func (s *Service) translateMyMemory(ctx context.Context, text string, opts Options) (Result, error) {
	s.mu.Lock()
	baseURL := s.myMemoryURL
	client := s.client
	s.mu.Unlock()

	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultMyMemoryEndpoint
	}

	srcLang := NormalizeLangCode(opts.SourceLang)
	if srcLang == "" || srcLang == "auto" {
		srcLang = DetectSourceLang(text, opts.TargetLang)
	}
	if srcLang == "" || srcLang == opts.TargetLang {
		// If text is already detected as the target language, no translation call is needed.
		return Result{
			TranslatedText: "",
			SourceLang:     opts.TargetLang,
		}, nil
	}

	u, err := url.Parse(baseURL)
	if err != nil {
		return Result{}, fmt.Errorf("parse mymemory url: %w", err)
	}
	q := u.Query()
	q.Set("q", text)
	q.Set("langpair", srcLang+"|"+opts.TargetLang)
	if email := strings.TrimSpace(opts.ContactEmail); email != "" {
		q.Set("de", email)
	}
	if key := strings.TrimSpace(opts.APIKey); key != "" {
		q.Set("key", key)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Result{}, fmt.Errorf("create mymemory request: %w", err)
	}
	req.Header.Set("User-Agent", "TownCrier-TTS/1.0 (https://github.com/mrpoundsign/TownCrier)")

	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("mymemory request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Result{}, fmt.Errorf("mymemory HTTP %d: %s", resp.StatusCode, sanitizeHTTPErrorBody(body))
	}

	var parsed myMemoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Result{}, fmt.Errorf("decode mymemory response: %w", err)
	}

	if parsed.QuotaFinished {
		return Result{}, markHardQuotaOrAuth(errors.New("daily MyMemory free quota reached (enter your email in Settings -> Translation for 50,000 free chars/day, or configure a Google/DeepL/local provider)"))
	}

	if details := strings.TrimSpace(parsed.ResponseDetails); details != "" && !strings.EqualFold(details, "OK") {
		if statusNum, ok := parsed.ResponseStatus.(float64); ok && int(statusNum) != http.StatusOK {
			return Result{}, fmt.Errorf("mymemory error (%d): %s", int(statusNum), details)
		}
	}

	translated := strings.TrimSpace(html.UnescapeString(parsed.ResponseData.TranslatedText))
	if translated == "" {
		return Result{}, errors.New("mymemory returned empty translation")
	}

	return Result{
		TranslatedText: translated,
		SourceLang:     srcLang,
	}, nil
}
