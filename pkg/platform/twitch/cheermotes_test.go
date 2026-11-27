package twitch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gempir/go-twitch-irc/v4"
	"github.com/mrpoundsign/TownCrier/pkg/chat"
	"github.com/mrpoundsign/TownCrier/pkg/secrets"
)

func TestParseHelixCheermotesAndResolve(t *testing.T) {
	payload := `{
		"data": [
			{
				"prefix": "Cheer",
				"tiers": [
					{
						"min_bits": 100,
						"id": "100",
						"color": "#9c3ee8",
						"images": {
							"dark": {
								"animated": {"1": "https://cdn.test/cheer/100/1.gif", "2": "https://cdn.test/cheer/100/2.gif"},
								"static": {"2": "https://cdn.test/cheer/100/2.png"}
							}
						}
					},
					{
						"min_bits": 1,
						"id": "1",
						"color": "#979797",
						"images": {
							"dark": {
								"animated": {"1": "https://cdn.test/cheer/1/1.gif"},
								"static": {"2": "https://cdn.test/cheer/1/2.png"}
							}
						}
					}
				]
			},
			{
				"prefix": "CustomGem",
				"tiers": [
					{
						"min_bits": 50,
						"id": "50",
						"color": "#12ab34",
						"images": {
							"dark": {
								"static": {"2": "https://cdn.test/customgem/50/2.png"}
							}
						}
					}
				]
			}
		]
	}`

	cat, err := ParseHelixCheermotes([]byte(payload))
	if err != nil {
		t.Fatalf("ParseHelixCheermotes failed: %v", err)
	}

	u, col, ok := cat.Resolve("cheer", 250)
	if !ok || u != "https://cdn.test/cheer/100/2.gif" || col != "#9c3ee8" {
		t.Fatalf("Resolve(cheer, 250) = (%q, %q, %v)", u, col, ok)
	}

	// Static fallback when animated URL is absent
	u, col, ok = cat.Resolve("CustomGem", 50)
	if !ok || u != "https://cdn.test/customgem/50/2.png" || col != "#12ab34" {
		t.Fatalf("Resolve(CustomGem, 50) = (%q, %q, %v)", u, col, ok)
	}

	// Built-in global fallback when catalog is nil
	var nilCat *CheermoteCatalog
	u, col, ok = nilCat.Resolve("Kappa", 1000)
	if !ok || u != "https://d3aqoihi2n8ty8.cloudfront.net/actions/kappa/dark/animated/1000/2.gif" || col != "#1db2a5" {
		t.Fatalf("nilCat.Resolve(Kappa, 1000) = (%q, %q, %v)", u, col, ok)
	}
}

func TestExtractIRCCheermoteSpansAndConnector(t *testing.T) {
	// Leading emoji tests UTF-8 rune offsets; Kappa is already an emote span so Kappa500 is only matched if non-overlapping.
	text := "🔥 Kappa Cheer100! 4Head500"
	existing := []chat.EmoteSpan{
		{Start: 2, End: 7, ID: "25", Name: "Kappa"},
	}

	spans := ExtractIRCCheermoteSpans(text, existing, nil)
	if len(spans) != 2 {
		t.Fatalf("expected 2 cheermote spans, got %d: %+v", len(spans), spans)
	}
	if spans[0].Name != "Cheer100" || spans[0].Bits != 100 || spans[0].Start != 8 || spans[0].End != 16 || spans[0].Color != "#9c3ee8" {
		t.Errorf("unexpected span[0]: %+v", spans[0])
	}
	if spans[1].Name != "4Head500" || spans[1].Bits != 500 || spans[1].Start != 18 || spans[1].End != 26 || spans[1].Color != "#9c3ee8" {
		t.Errorf("unexpected span[1]: %+v", spans[1])
	}

	// Verify Connector.handlePrivateMessage extracts cheermotes when Bits > 0
	var dispatched *chat.Event
	conn := NewConnector(func(ev *chat.Event) { dispatched = ev }, nil, nil)
	conn.handlePrivateMessage(twitch.PrivateMessage{
		ID:      "irc-cheer-1",
		Channel: "mrpoundsign",
		User:    twitch.User{ID: "1", Name: "donor", DisplayName: "Donor"},
		Message: "Cheer100 Kappa",
		Bits:    100,
		Time:    time.Now(),
		Emotes: []*twitch.Emote{
			{Name: "Kappa", ID: "25", Positions: []twitch.EmotePosition{{Start: 9, End: 13}}},
		},
	})
	if dispatched == nil || len(dispatched.EmoteSpans) != 2 {
		t.Fatalf("expected 2 EmoteSpans (cheermote + emote), got %+v", dispatched)
	}
	if dispatched.EmoteSpans[0].Bits != 100 || dispatched.EmoteSpans[0].Name != "Cheer100" {
		t.Errorf("expected first span to be Cheer100, got %+v", dispatched.EmoteSpans[0])
	}
	if dispatched.EmoteSpans[1].Name != "Kappa" {
		t.Errorf("expected second span to be Kappa, got %+v", dispatched.EmoteSpans[1])
	}
}

func TestEventSubCheermoteAndNotificationFragments(t *testing.T) {
	// 1. EventSub channel.chat.message with type == "cheermote"
	cheerRaw := json.RawMessage(`{
		"broadcaster_user_id": "1001",
		"broadcaster_user_login": "mrpoundsign",
		"chatter_user_id": "4004",
		"chatter_user_login": "alice",
		"chatter_user_name": "Alice",
		"message_id": "msg-cheer-frag",
		"message_type": "text",
		"message": {
			"text": "Hype! Cheer500 Kappa",
			"fragments": [
				{"type": "text", "text": "Hype! "},
				{"type": "cheermote", "text": "Cheer500", "cheermote": {"prefix": "Cheer", "bits": 500, "tier": 100}},
				{"type": "text", "text": " "},
				{"type": "emote", "text": "Kappa", "emote": {"id": "25"}}
			]
		},
		"cheer": {"bits": 500}
	}`)
	ev, err := ParseEventSubNotification("channel.chat.message", "env-c1", cheerRaw, "mrpoundsign")
	if err != nil || ev == nil {
		t.Fatalf("ParseEventSubNotification failed: %v", err)
	}
	if len(ev.EmoteSpans) != 2 {
		t.Fatalf("expected 2 EmoteSpans, got %+v", ev.EmoteSpans)
	}
	if ev.EmoteSpans[0].Bits != 500 || ev.EmoteSpans[0].Start != 6 || ev.EmoteSpans[0].End != 14 || ev.EmoteSpans[0].Color != "#9c3ee8" {
		t.Errorf("unexpected cheermote span: %+v", ev.EmoteSpans[0])
	}
	if ev.EmoteSpans[1].Name != "Kappa" || ev.EmoteSpans[1].Start != 15 || ev.EmoteSpans[1].End != 20 {
		t.Errorf("unexpected emote span: %+v", ev.EmoteSpans[1])
	}

	// 2. EventSub channel.chat.notification (resub) with leading space + emote fragment
	resubRaw := json.RawMessage(`{
		"broadcaster_user_login": "mrpoundsign",
		"chatter_user_id": "5005",
		"chatter_user_login": "bob",
		"chatter_user_name": "Bob",
		"message_id": "notif-resub-1",
		"notice_type": "resub",
		"message": {
			"text": "  Love the stream! PogChamp ",
			"fragments": [
				{"type": "text", "text": "  Love the stream! "},
				{"type": "emote", "text": "PogChamp", "emote": {"id": "305954156"}},
				{"type": "text", "text": " "}
			]
		},
		"resub": {"cumulative_months": 6, "sub_tier": "1000"}
	}`)
	rEv, err := ParseEventSubNotification("channel.chat.notification", "env-r1", resubRaw, "mrpoundsign")
	if err != nil || rEv == nil {
		t.Fatalf("ParseEventSubNotification(resub) failed: %v", err)
	}
	if rEv.Text != "Love the stream! PogChamp" {
		t.Fatalf("unexpected trimmed resub text: %q", rEv.Text)
	}
	if len(rEv.EmoteSpans) != 1 || rEv.EmoteSpans[0].Name != "PogChamp" || rEv.EmoteSpans[0].Start != 17 || rEv.EmoteSpans[0].End != 25 {
		t.Fatalf("expected shifted PogChamp span [17,25), got %+v", rEv.EmoteSpans)
	}
}

func TestFetchCheermotesFromHelix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/helix/bits/cheermotes" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("broadcaster_id") != "9999" {
			t.Errorf("expected broadcaster_id=9999, got %q", r.URL.Query().Get("broadcaster_id"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"prefix":"ChannelBit","tiers":[{"min_bits":1,"id":"1","color":"#ff5500","images":{"dark":{"animated":{"2":"https://cdn.test/cb/1/2.gif"}}}}]}]}`))
	}))
	defer srv.Close()

	memStore := secrets.NewMemoryStore()
	oauthMgr := NewOAuthManager("test-client-id", memStore)
	_ = oauthMgr.SaveTokens(&TokenResponse{
		AccessToken: "tok-123",
		ExpiresIn:   3600,
	})

	conn := NewConnector(nil, nil, nil)
	conn.SetOAuthManager(oauthMgr)
	conn.SetEventSubEndpoints("", srv.URL+"/helix", srv.URL+"/validate", srv.Client())
	conn.mu.Lock()
	conn.channel = "mrpoundsign"
	conn.mu.Unlock()

	conn.fetchCheermotes(context.Background(), "tok-123", "9999")

	u, col, ok := conn.cheermoteCatalog().Resolve("ChannelBit", 10)
	if !ok || u != "https://cdn.test/cb/1/2.gif" || col != "#ff5500" {
		t.Fatalf("expected fetched custom cheermote, got (%q, %q, %v)", u, col, ok)
	}
}
