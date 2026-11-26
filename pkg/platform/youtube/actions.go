package youtube

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

type youtubeLiveChatMessageInsertRequest struct {
	Snippet youtubeLiveChatMessageInsertSnippet `json:"snippet"`
}

type youtubeLiveChatMessageInsertSnippet struct {
	LiveChatID         string                        `json:"liveChatId"`
	Type               string                        `json:"type"`
	TextMessageDetails youtubeTextMessageDetailsBody `json:"textMessageDetails"`
}

type youtubeTextMessageDetailsBody struct {
	MessageText string `json:"messageText"`
}

// SendChatMessage posts a chat message to the currently active YouTube live chat broadcast(s)
// via YouTube Data API v3 POST /liveChat/messages?part=snippet.
func (c *Connector) SendChatMessage(ctx context.Context, text string) error {
	msg := strings.TrimSpace(text)
	if msg == "" {
		return errors.New("empty YouTube chat message")
	}

	httpClient, apiBase, token, err := c.resolveModContext(ctx)
	if err != nil {
		return err
	}

	liveChatIDs, err := c.resolveAllActiveLiveChatIDs(ctx, httpClient, apiBase, token)
	if err != nil {
		return err
	}

	var firstErr error
	succeeded := 0
	for _, liveChatID := range liveChatIDs {
		payload := youtubeLiveChatMessageInsertRequest{
			Snippet: youtubeLiveChatMessageInsertSnippet{
				LiveChatID: liveChatID,
				Type:       "textMessageEvent",
				TextMessageDetails: youtubeTextMessageDetailsBody{
					MessageText: msg,
				},
			},
		}
		raw, mErr := json.Marshal(payload)
		if mErr != nil {
			if firstErr == nil {
				firstErr = mErr
			}
			continue
		}

		endpoint := apiBase + "/liveChat/messages?part=snippet"
		req, rErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
		if rErr != nil {
			if firstErr == nil {
				firstErr = rErr
			}
			continue
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, dErr := httpClient.Do(req)
		if dErr != nil {
			if firstErr == nil {
				firstErr = dErr
			}
			continue
		}
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
			succeeded++
			_ = resp.Body.Close()
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			c.handleUnauthorized(ctx)
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("youtube send chat HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}

	if succeeded > 0 {
		return nil
	}
	if firstErr != nil {
		return firstErr
	}
	return errors.New("no active YouTube liveChatId available")
}
