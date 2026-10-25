package youtube

import (
	"context"
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
	// 1. Valid active live stream with nested structures in videoDetails
	liveHTML := `<html><script>var ytcfg = {"INNERTUBE_API_KEY":"AIzaSyTestKey123"}; var ytInitialPlayerResponse = {"videoDetails":{"videoId":"abc123xyz","title":"My Live Stream","thumbnail":{"thumbnails":[{"url":"https://example.com/thumb.jpg"}]},"isLive":true,"isLiveContent":true}}; var ytInitialData = {"contents":{"liveChatRenderer":{"continuations":[{"timedContinuationData":{"continuation":"0ofMyAModTestContToken","timeoutMs":2000}}]}}};</script></html>`
	key, cont, ok := ExtractBootstrapTokens(liveHTML)
	if !ok {
		t.Fatal("expected ExtractBootstrapTokens to succeed for active live stream")
	}
	if key != "AIzaSyTestKey123" {
		t.Errorf("got key %q, want AIzaSyTestKey123", key)
	}
	if cont != "0ofMyAModTestContToken" {
		t.Errorf("got continuation %q, want 0ofMyAModTestContToken", cont)
	}

	// 2. Scheduled stream / waiting room (isLiveContent: true, but isLive: false)
	upcomingHTML := `<html><script>var ytcfg = {"INNERTUBE_API_KEY":"AIzaSyTestKey123"}; var ytInitialPlayerResponse = {"videoDetails":{"videoId":"abc123xyz","isLive":false,"isLiveContent":true}}; var ytInitialData = {"contents":{"liveChatRenderer":{"continuations":[{"timedContinuationData":{"continuation":"waitingRoomCont","timeoutMs":2000}}]}}};</script></html>`
	_, _, ok = ExtractBootstrapTokens(upcomingHTML)
	if ok {
		t.Error("expected ExtractBootstrapTokens to fail for upcoming/scheduled waiting room")
	}

	// 3. Scheduled stream with isLive omitted
	omittedLiveHTML := `<html><script>var ytcfg = {"INNERTUBE_API_KEY":"AIzaSyTestKey123"}; var ytInitialPlayerResponse = {"videoDetails":{"videoId":"abc123xyz","isLiveContent":true}}; var ytInitialData = {"contents":{"liveChatRenderer":{"continuations":[{"timedContinuationData":{"continuation":"waitingRoomCont","timeoutMs":2000}}]}}};</script></html>`
	_, _, ok = ExtractBootstrapTokens(omittedLiveHTML)
	if ok {
		t.Error("expected ExtractBootstrapTokens to fail when isLive is omitted")
	}

	// 4. Ended stream / VOD replay with reloadContinuationData but isLive: false
	endedHTML := `<html><script>var ytcfg = {"INNERTUBE_API_KEY":"AIzaSyTestKey123"}; var ytInitialPlayerResponse = {"videoDetails":{"videoId":"abc123xyz","isLive":false}}; var ytInitialData = {"contents":{"liveChatRenderer":{"continuations":[{"reloadContinuationData":{"continuation":"0ofMyAReplayCont"}}]}}};</script></html>`
	_, _, ok = ExtractBootstrapTokens(endedHTML)
	if ok {
		t.Error("expected ExtractBootstrapTokens to fail for ended stream / VOD chat replay")
	}

	// 5. Offline channel page (no videoDetails at all)
	offlineChannelHTML := `<html><script>var ytcfg = {"INNERTUBE_API_KEY":"AIzaSyTestKey123"}; var ytInitialData = {"browseEndpoint":{"browseId":"UC123"}};</script></html>`
	_, _, ok = ExtractBootstrapTokens(offlineChannelHTML)
	if ok {
		t.Error("expected ExtractBootstrapTokens to fail for offline channel page without videoDetails")
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
									"purchaseAmountText": {"simpleText": "€10.00"},
									"sticker": {
										"thumbnails": [
											{"url": "https://lh3.googleusercontent.com/sticker_thumb_1x.png"},
											{"url": "https://lh3.googleusercontent.com/sticker_thumb_2x.png"}
										],
										"accessibility": {
											"accessibilityData": {
												"label": "Pear character cheering"
											}
										}
									}
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

	// 3. Super Sticker with inline thumbnail EmoteSpan and accessibility label
	e2 := events[2]
	if e2.Kind != chat.EventKindSuperSticker || e2.Amount != 10 || e2.Currency != "euros" {
		t.Errorf("unexpected super sticker event: %+v", e2)
	}
	if e2.ItemName != "Pear character cheering" || len(e2.EmoteSpans) != 1 || e2.EmoteSpans[0].URL != "https://lh3.googleusercontent.com/sticker_thumb_2x.png" {
		t.Errorf("expected sticker ItemName and high-res EmoteSpan, got ItemName=%q EmoteSpans=%+v", e2.ItemName, e2.EmoteSpans)
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
		_, _ = fmt.Fprint(w, `<html><script>var ytcfg = {"INNERTUBE_API_KEY":"testKey"}; var ytInitialPlayerResponse = {"videoDetails":{"videoId":"testVid","isLive":true}}; var ytInitialData = {"liveChatRenderer":{"continuations":[{"timedContinuationData":{"continuation":"cont1","timeoutMs":1000}}]}};</script></html>`)
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

	// Verify CheckLive returns true when connected
	isLive, err := conn.CheckLive(context.Background())
	if err != nil || !isLive {
		t.Fatalf("expected CheckLive=true, got isLive=%v err=%v", isLive, err)
	}
}

func TestParseYouTubeGiftRedemptionDonationAndRaid(t *testing.T) {
	payload := []byte(`{
		"continuationContents": {
			"liveChatContinuation": {
				"continuations": [],
				"actions": [
					{
						"addChatItemAction": {
							"item": {
								"liveChatSponsorshipsGiftRedemptionAnnouncementRenderer": {
									"id": "yt-gift-redemp-1",
									"timestampUsec": "1710000000000000",
									"authorName": {"simpleText": "@LuckyViewer"},
									"authorExternalChannelId": "UC_lucky",
									"message": {"simpleText": "received a gift membership by @GenerousGifter"}
								}
							}
						}
					},
					{
						"addChatItemAction": {
							"item": {
								"liveChatDonationAnnouncementRenderer": {
									"id": "yt-donation-1",
									"timestampUsec": "1710000001000000",
									"authorName": {"simpleText": "CharityHero"},
									"authorExternalChannelId": "UC_hero",
									"purchaseAmountText": {"simpleText": "$20.00"},
									"message": {"simpleText": "Keep it up!"}
								}
							}
						}
					},
					{
						"addBannerToLiveChatCommand": {
							"bannerRenderer": {
								"liveChatBannerRedirectRenderer": {
									"id": "yt-raid-1",
									"authorName": {"simpleText": "RaidingCreator"},
									"bannerMessage": {"simpleText": "RaidingCreator and their viewers just joined!"}
								}
							}
						}
					}
				]
			}
		}
	}`)

	events, _, _, _, err := ParseLiveChatResponse(payload)
	if err != nil {
		t.Fatalf("ParseLiveChatResponse: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	if events[0].Kind != chat.EventKindGiftRedemption || events[0].UserName != "LuckyViewer" || events[0].Recipient != "GenerousGifter" {
		t.Errorf("unexpected gift redemption event: %+v", events[0])
	}
	if events[1].Kind != chat.EventKindCharityDonation || events[1].Amount != 20 || events[1].Currency != "dollars" || events[1].Text != "Keep it up!" {
		t.Errorf("unexpected charity donation event: %+v", events[1])
	}
	if events[2].Kind != chat.EventKindRaid || events[2].UserName != "RaidingCreator" {
		t.Errorf("unexpected raid redirect event: %+v", events[2])
	}
}

func TestParseYouTubeGiftMessageViewModel(t *testing.T) {
	payload := []byte(`{
		"continuationContents": {
			"liveChatContinuation": {
				"continuations": [],
				"actions": [
					{
						"addChatItemAction": {
							"item": {
								"giftMessageViewModel": {
									"id": "yt-jewel-gift-100",
									"authorName": {
										"content": "@IgorB-x8u ",
										"styleRuns": [{"startIndex": 0, "length": 11}]
									},
									"authorExternalChannelId": "UC6391gIzzyKbri_pkMzFcoQ",
									"text": {
										"content": "sent 100",
										"styleRuns": [{"startIndex": 0, "length": 8}]
									},
									"giftImage": {
										"sources": [
											{"url": "//www.gstatic.com/youtube/img/pdg/gift/assets/100.png=w480-h480", "width": 480, "height": 480},
											{"url": "//www.gstatic.com/youtube/img/pdg/gift/assets/100.png=w640-h640", "width": 640, "height": 640}
										]
									},
									"giftImageA11yLabel": "@IgorB-x8u sent a gift, 100"
								}
							}
						}
					},
					{
						"addChatItemAction": {
							"item": {
								"giftMessageViewModel": {
									"id": "yt-jewel-gift-explicit",
									"authorName": {
										"content": "@GoldFan"
									},
									"text": {
										"content": "sent Gold coin for 15 Jewels"
									},
									"giftImage": {
										"sources": [
											{"url": "https://www.gstatic.com/youtube/img/pdg/gift/assets/gold_coin.png"}
										]
									}
								}
							}
						}
					},
					{
						"addChatItemAction": {
							"item": {
								"giftMessageViewModel": {
									"id": "yt-jewel-gift-a11y",
									"giftImageA11yLabel": "@A11ySender sent a gift, Mystery Box"
								}
							}
						}
					}
				]
			}
		}
	}`)

	events, _, _, _, err := ParseLiveChatResponse(payload)
	if err != nil {
		t.Fatalf("ParseLiveChatResponse: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 gift events, got %d", len(events))
	}

	// 1. Standard "sent 100" with fallback lookup (2 Jewels) and protocol-relative image URL
	ev0 := events[0]
	if ev0.Kind != chat.EventKindGift || ev0.Platform != "youtube" {
		t.Errorf("unexpected event[0] kind/platform: %+v", ev0)
	}
	if ev0.UserName != "IgorB-x8u" || ev0.UserID != "UC6391gIzzyKbri_pkMzFcoQ" {
		t.Errorf("unexpected event[0] user fields: UserName=%q UserID=%q", ev0.UserName, ev0.UserID)
	}
	if ev0.ItemName != "100" || ev0.Amount != 2 || ev0.Currency != "Jewels" || ev0.Count != 1 {
		t.Errorf("unexpected event[0] gift metadata: ItemName=%q Amount=%v Currency=%q Count=%d", ev0.ItemName, ev0.Amount, ev0.Currency, ev0.Count)
	}
	if len(ev0.EmoteSpans) != 1 || ev0.EmoteSpans[0].URL != "https://www.gstatic.com/youtube/img/pdg/gift/assets/100.png=w640-h640" {
		t.Errorf("unexpected event[0] EmoteSpans: %+v", ev0.EmoteSpans)
	}

	// 2. Explicit "sent Gold coin for 15 Jewels"
	ev1 := events[1]
	if ev1.UserName != "GoldFan" || ev1.ItemName != "Gold coin" || ev1.Amount != 15 || ev1.Currency != "Jewels" {
		t.Errorf("unexpected event[1]: %+v", ev1)
	}

	// 3. Fallback from giftImageA11yLabel
	ev2 := events[2]
	if ev2.UserName != "A11ySender" || ev2.ItemName != "Mystery Box" || ev2.Amount != 0 {
		t.Errorf("unexpected event[2]: %+v", ev2)
	}
}
