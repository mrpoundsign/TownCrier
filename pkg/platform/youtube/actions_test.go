package youtube

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mrpoundsign/TownCrier/pkg/secrets"
)

func TestYouTubeSendChatMessage(t *testing.T) {
	var gotChatID string
	var gotText string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/liveChat/messages", r.URL.Path)
		require.Equal(t, "Bearer yt-tok", r.Header.Get("Authorization"))

		var req youtubeLiveChatMessageInsertRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		gotChatID = req.Snippet.LiveChatID
		gotText = req.Snippet.TextMessageDetails.MessageText
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store := secrets.NewMemoryStore()
	oauth := NewOAuthManager("yt-cid", "", store)
	require.NoError(t, oauth.SaveToken(&TokenData{
		AccessToken:  "yt-tok",
		RefreshToken: "yt-ref",
		ExpiresAt:    time.Now().Add(time.Hour),
	}))

	conn := NewConnector(nil, nil, nil)
	conn.SetAPIBaseURL(srv.URL)
	conn.mu.Lock()
	conn.httpClient = srv.Client()
	conn.activeLiveChatID = "LCID-123"
	conn.mu.Unlock()
	conn.SetOAuthManager(oauth)

	require.NoError(t, conn.SendChatMessage(context.Background(), "Hello YouTube chat!"))
	require.Equal(t, "LCID-123", gotChatID)
	require.Equal(t, "Hello YouTube chat!", gotText)
}
