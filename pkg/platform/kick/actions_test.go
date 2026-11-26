package kick

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

func TestKickSendChatMessage(t *testing.T) {
	var gotReq kickSendChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/chat", r.URL.Path)
		require.Equal(t, "Bearer kick-tok", r.Header.Get("Authorization"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotReq))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store := secrets.NewMemoryStore()
	oauth := NewOAuthManager("kick-cid", "kick-sec", store)
	oauth.SetEndpoints(srv.URL+"/oauth/authorize", srv.URL+"/oauth/token", srv.URL)
	require.NoError(t, oauth.SaveToken(&TokenData{
		AccessToken:       "kick-tok",
		BroadcasterUserID: 7788,
		ExpiresAt:         time.Now().Add(time.Hour),
	}))

	conn := NewConnector(nil, nil, nil)
	conn.SetPublicAPIBaseURL(srv.URL)
	conn.mu.Lock()
	conn.httpClient = srv.Client()
	conn.mu.Unlock()
	conn.SetOAuthManager(oauth)

	require.NoError(t, conn.SendChatMessage(context.Background(), "Hello Kick!", "msg-9"))
	require.Equal(t, int64(7788), gotReq.BroadcasterUserID)
	require.Equal(t, "Hello Kick!", gotReq.Content)
	require.Equal(t, "msg-9", gotReq.ReplyToMessageID)
}
