package youtube

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/chat"
)

func TestNormalizeTarget(t *testing.T) {
	tests := []struct {
		input       string
		wantLiveURL string
		wantDisplay string
	}{
		{
			input:       "@mrpoundsign",
			wantLiveURL: "https://www.youtube.com/@mrpoundsign/live",
			wantDisplay: "mrpoundsign",
		},
		{
			input:       "mrpoundsign",
			wantLiveURL: "https://www.youtube.com/@mrpoundsign/live",
			wantDisplay: "mrpoundsign",
		},
		{
			input:       "streamer_01",
			wantLiveURL: "https://www.youtube.com/@streamer_01/live",
			wantDisplay: "streamer_01",
		},
		{
			input:       "UC1234567890abcdefghijkl",
			wantLiveURL: "https://www.youtube.com/channel/UC1234567890abcdefghijkl/live",
			wantDisplay: "UC1234567890abcdefghijkl",
		},
		{
			input:       "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			wantLiveURL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			wantDisplay: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		},
	}

	for _, tc := range tests {
		gotURL, gotDisp := NormalizeTarget(tc.input)
		if gotURL != tc.wantLiveURL {
			t.Errorf("NormalizeTarget(%q) url = %q, want %q", tc.input, gotURL, tc.wantLiveURL)
		}
		if gotDisp != tc.wantDisplay {
			t.Errorf("NormalizeTarget(%q) display = %q, want %q", tc.input, gotDisp, tc.wantDisplay)
		}
	}
}

func TestExtractBootstrapTokens(t *testing.T) {
	sampleHTML := `<html><script>var ytcfg = {"INNERTUBE_API_KEY":"AIzaSyTestKey123"}; var ytInitialData = {"contents":{"liveChatRenderer":{"continuations":[{"timedContinuationData":{"continuation":"0ofMyAModTestContToken","timeoutMs":2000}}]}}};</script></html>`
	key, cont, ok := ExtractBootstrapTokens(sampleHTML)
	if !ok {
		t.Fatal("expected ExtractBootstrapTokens to succeed")
	}
	if key != "AIzaSyTestKey123" {
		t.Errorf("got key %q, want AIzaSyTestKey123", key)
	}
	if cont != "0ofMyAModTestContToken" {
		t.Errorf("got continuation %q, want 0ofMyAModTestContToken", cont)
	}
}

func TestParseLiveChatResponse(t *testing.T) {
	nowUsec := strconv.FormatInt(time.Now().UnixMicro(), 10)
	sampleJSON := fmt.Sprintf(`{
		"continuationContents": {
			"liveChatContinuation": {
				"continuations": [
					{
						"timedContinuationData": {
							"continuation": "next-token-456",
							"timeoutMs": 1500
						}
					}
				],
				"actions": [
					{
						"addChatItemAction": {
							"item": {
								"liveChatTextMessageRenderer": {
									"id": "msg-1",
									"timestampUsec": "%s",
									"authorName": {"simpleText": "@Alice"},
									"authorExternalChannelId": "UC_alice",
									"authorBadges": [
										{"liveChatAuthorBadgeRenderer": {"icon": {"iconType": "MODERATOR"}, "tooltip": "Moderator"}},
										{"liveChatAuthorBadgeRenderer": {"customThumbnail": {}, "tooltip": "Member (6 months)"}}
									],
									"message": {
										"runs": [
											{"text": "Hello YouTube "},
											{"text": ":wave:", "emoji": {"emojiId": "em1", "shortcuts": [":wave:"]}}
										]
									}
								}
							}
						}
					},
					{
						"addChatItemAction": {
							"item": {
								"liveChatPaidMessageRenderer": {
									"id": "sc-1",
									"timestampUsec": "%s",
									"authorName": {"simpleText": "Bob"},
									"authorExternalChannelId": "UC_bob",
									"purchaseAmountText": {"simpleText": "$5.00"},
									"message": {"simpleText": "Great stream!"}
								}
							}
						}
					},
					{
						"addChatItemAction": {
							"item": {
								"liveChatPaidStickerRenderer": {
									"id": "ss-1",
									"timestampUsec": "%s",
									"authorName": {"simpleText": "Charlie"},
									"authorExternalChannelId": "UC_charlie",
									"purchaseAmountText": {"simpleText": "€10.00"}
								}
							}
						}
					},
					{
						"addChatItemAction": {
							"item": {
								"liveChatMembershipItemRenderer": {
									"id": "mem-1",
									"timestampUsec": "%s",
									"authorName": {"simpleText": "Dana"},
									"authorExternalChannelId": "UC_dana",
									"headerPrimaryText": {"simpleText": "Member for 12 months"},
									"message": {"simpleText": "Happy anniversary!"}
								}
							}
						}
					},
					{
						"addChatItemAction": {
							"item": {
								"liveChatSponsorshipsGiftPurchaseAnnouncementRenderer": {
									"id": "gift-1",
									"timestampUsec": "%s",
									"authorExternalChannelId": "UC_gifter",
									"header": {
										"liveChatSponsorshipsHeaderRenderer": {
											"authorName": {"simpleText": "GenerousViewer"},
											"primaryText": {"simpleText": "Gifted 5 TownCrier memberships"}
										}
									}
								}
							}
						}
					},
					{
						"markChatItemAsDeletedAction": {
							"targetItemId": "msg-bad"
						}
					},
					{
						"markChatItemsByAuthorAsDeletedAction": {
							"externalChannelId": "UC_banned"
						}
					}
				]
			}
		}
	}`, nowUsec, nowUsec, nowUsec, nowUsec, nowUsec)

	events, modEvents, nextCont, delay, err := ParseLiveChatResponse([]byte(sampleJSON))
	if err != nil {
		t.Fatalf("ParseLiveChatResponse failed: %v", err)
	}
	if nextCont != "next-token-456" {
		t.Errorf("expected nextCont 'next-token-456', got %q", nextCont)
	}
	if delay != 1500*time.Millisecond {
		t.Errorf("expected delay 1.5s, got %v", delay)
	}
	if len(events) != 5 {
		t.Fatalf("expected 5 chat events, got %d", len(events))
	}

	// 1. Text message with mod + subscriber roles and emoji span
	e0 := events[0]
	if e0.Kind != chat.EventKindMessage || e0.UserName != "Alice" || e0.Text != "Hello YouTube :wave:" {
		t.Errorf("unexpected text event: %+v", e0)
	}
	if !slices.Contains(e0.Roles, "mod") || !slices.Contains(e0.Roles, "subscriber") {
		t.Errorf("expected mod and subscriber roles, got %v", e0.Roles)
	}
	if len(e0.EmoteSpans) != 1 || e0.EmoteSpans[0].Name != ":wave:" {
		t.Errorf("expected 1 emoji span, got %+v", e0.EmoteSpans)
	}

	// 2. Super Chat
	e1 := events[1]
	if e1.Kind != chat.EventKindSuperChat || e1.Amount != 5 || e1.Currency != "dollars" || e1.Text != "Great stream!" {
		t.Errorf("unexpected super chat event: %+v", e1)
	}

	// 3. Super Sticker
	e2 := events[2]
	if e2.Kind != chat.EventKindSuperSticker || e2.Amount != 10 || e2.Currency != "euros" {
		t.Errorf("unexpected super sticker event: %+v", e2)
	}

	// 4. Membership Resub
	e3 := events[3]
	if e3.Kind != chat.EventKindResub || e3.Months != 12 || e3.Text != "Happy anniversary!" {
		t.Errorf("unexpected membership resub event: %+v", e3)
	}

	// 5. Gifted Memberships
	e4 := events[4]
	if e4.Kind != chat.EventKindGiftSub || e4.Count != 5 || e4.UserName != "GenerousViewer" {
		t.Errorf("unexpected gift sub event: %+v", e4)
	}

	// Moderation events
	if len(modEvents) != 2 {
		t.Fatalf("expected 2 moderation events, got %d", len(modEvents))
	}
	if modEvents[0].Action != chat.ModActionDeleteMessage || modEvents[0].TargetMessageID != "msg-bad" {
		t.Errorf("unexpected mod event 0: %+v", modEvents[0])
	}
	if modEvents[1].Action != chat.ModActionBanUser || modEvents[1].TargetUserID != "UC_banned" {
		t.Errorf("unexpected mod event 1: %+v", modEvents[1])
	}
}

func TestConnectorLifecycleAndStream(t *testing.T) {
	nowUsec := strconv.FormatInt(time.Now().UnixMicro(), 10)

	mux := http.NewServeMux()
	mux.HandleFunc("/testchannel/live", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><script>var ytcfg = {"INNERTUBE_API_KEY":"testKey"}; var ytInitialData = {"liveChatRenderer":{"continuations":[{"timedContinuationData":{"continuation":"cont1","timeoutMs":1000}}]}};</script></html>`)
	})
	mux.HandleFunc("/youtubei/v1/live_chat/get_live_chat", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{
			"continuationContents": {
				"liveChatContinuation": {
					"continuations": [],
					"actions": [
						{
							"addChatItemAction": {
								"item": {
									"liveChatTextMessageRenderer": {
										"id": "live-1",
										"timestampUsec": "%s",
										"authorName": {"simpleText": "LiveChatter"},
										"authorExternalChannelId": "UC_live1",
										"message": {"simpleText": "Testing live connector!"}
									}
								}
							}
						}
					]
				}
			}
		}`, nowUsec)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	receivedCh := make(chan *chat.Event, 1)
	var once sync.Once

	conn := NewConnector(
		func(ev *chat.Event) {
			once.Do(func() {
				receivedCh <- ev
			})
		},
		nil,
		nil,
	)
	conn.baseURL = srv.URL
	conn.waitInterval = 50 * time.Millisecond

	if err := conn.Start("@testchannel"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = conn.Stop() }()

	select {
	case ev := <-receivedCh:
		if ev.UserName != "LiveChatter" || ev.Text != "Testing live connector!" {
			t.Errorf("unexpected event: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for YouTube live chat event")
	}
}
