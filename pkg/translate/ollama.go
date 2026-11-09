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

type openAIChatRequest struct {
	Model       string              `json:"model"`
	Messages    []openAIChatMessage `json:"messages"`
	Temperature float64             `json:"temperature"`
	Stream      bool                `json:"stream"`
}

type openAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message openAIChatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (s *Service) translateOllama(ctx context.Context, text string, opts Options) (Result, error) {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()

	endpoint := strings.TrimSpace(opts.EndpointURL)
	if endpoint == "" {
		endpoint = DefaultOllamaURL
	}
	endpoint = strings.TrimRight(endpoint, "/")
	switch {
	case strings.HasSuffix(endpoint, "/v1/chat/completions"):
		// already complete
	case strings.HasSuffix(endpoint, "/v1"):
		endpoint += "/chat/completions"
	default:
		endpoint += "/v1/chat/completions"
	}

	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = DefaultOllamaModel
	}

	targetName := LanguageName(opts.TargetLang)
	sysPrompt := fmt.Sprintf(
		"You are a livestream chat translator. Translate the user's message into natural %s (%s) for text-to-speech, converting internet slang and laugh expressions (like 'jaja', 'kkkk', '草', '8888') into natural equivalents. Output ONLY the translated text with no quotes or explanations.",
		targetName,
		opts.TargetLang,
	)

	reqBody := openAIChatRequest{
		Model: model,
		Messages: []openAIChatMessage{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: text},
		},
		Temperature: 0.1,
		Stream:      false,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return Result{}, fmt.Errorf("marshal ollama request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return Result{}, fmt.Errorf("create ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey := strings.TrimSpace(opts.APIKey); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("ollama request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Result{}, fmt.Errorf("ollama HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed openAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Result{}, fmt.Errorf("decode ollama response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return Result{}, fmt.Errorf("ollama API error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return Result{}, errors.New("ollama returned no choices")
	}

	out := strings.TrimSpace(parsed.Choices[0].Message.Content)
	out = strings.Trim(out, "\"“”'")
	if out == "" {
		return Result{}, errors.New("ollama returned empty translation")
	}

	return Result{
		TranslatedText: out,
		SourceLang:     opts.SourceLang,
	}, nil
}
