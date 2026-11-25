package kick

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

type kickSendChatRequest struct {
	BroadcasterUserID int64  `json:"broadcaster_user_id"`
	Content           string `json:"content"`
	ReplyToMessageID  string `json:"reply_to_message_id,omitempty"`
	Type              string `json:"type"`
}

// SendChatMessage posts a message (with optional reply parent ID) via Kick Public API v1 POST /public/v1/chat.
func (c *Connector) SendChatMessage(ctx context.Context, text, replyToMessageID string) error {
	msg := strings.TrimSpace(text)
	if msg == "" {
		return errors.New("empty Kick chat message")
	}

	httpClient, apiBase, token, oauth, err := c.resolveModContext(ctx)
	if err != nil {
		return err
	}
	broadcasterID, err := c.resolveBroadcasterUserID(ctx, oauth)
	if err != nil {
		return fmt.Errorf("resolve Kick broadcaster_user_id: %w", err)
	}

	payload := kickSendChatRequest{
		BroadcasterUserID: broadcasterID,
		Content:           msg,
		ReplyToMessageID:  strings.TrimSpace(replyToMessageID),
		Type:              "user",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	endpoint := apiBase + "/chat"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized && oauth != nil {
		_, _ = oauth.HandleUnauthorized(ctx)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("kick send chat HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
