package tiktok

import (
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/steampoweredtaco/gotiktoklive"
	pb "github.com/steampoweredtaco/gotiktoklive/proto"
	"google.golang.org/protobuf/proto"

	"github.com/mrpoundsign/TownCrier/internal/chat"
)

func TestNormalizeUsername(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"@creator123", "creator123"},
		{"  @creator_name  ", "creator_name"},
		{"https://www.tiktok.com/@creator123/live", "creator123"},
		{"https://tiktok.com/@creator123?is_from_webapp=1", "creator123"},
		{"creator123", "creator123"},
		{"", ""},
		{"   ", ""},
	}

	for _, tc := range tests {
		got := NormalizeUsername(tc.input)
		if got != tc.want {
			t.Errorf("NormalizeUsername(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestGiftDebouncer_StreakRepeatEnd(t *testing.T) {
	var mu sync.Mutex
	var flushed []*chat.Event

	d := NewGiftDebouncer(500*time.Millisecond, func(ev *chat.Event) {
		mu.Lock()
		flushed = append(flushed, ev)
		mu.Unlock()
	})
	defer d.Close()

	// Send streak increment 1, 2, 3, then RepeatEnd on 3
	for i := 1; i <= 3; i++ {
		d.Submit(GiftInput{
			UserID:       "1001",
			UserName:     "viewer1",
			DisplayName:  "Viewer One",
			GiftID:       101,
			GiftName:     "Rose",
			UnitDiamonds: 1,
			RepeatCount:  i,
			RepeatEnd:    i == 3,
			IsStreakable: true,
		})
	}

	mu.Lock()
	defer mu.Unlock()
	if len(flushed) != 1 {
		t.Fatalf("expected 1 flushed gift event on RepeatEnd, got %d", len(flushed))
	}
	ev := flushed[0]
	if ev.Kind != chat.EventKindGift {
		t.Errorf("Kind = %q, want %q", ev.Kind, chat.EventKindGift)
	}
	if ev.Text != "Rose" {
		t.Errorf("Text = %q, want Rose", ev.Text)
	}
	if ev.Count != 3 {
		t.Errorf("Count = %d, want 3", ev.Count)
	}
	if ev.Amount != 3 {
		t.Errorf("Amount = %v, want 3", ev.Amount)
	}
	if ev.Currency != "Diamonds" {
		t.Errorf("Currency = %q, want Diamonds", ev.Currency)
	}
}

func TestGiftDebouncer_SlidingWindowTimeout(t *testing.T) {
	flushedCh := make(chan *chat.Event, 2)

	d := NewGiftDebouncer(60*time.Millisecond, func(ev *chat.Event) {
		flushedCh <- ev
	})
	defer d.Close()

	d.Submit(GiftInput{
		UserID:       "1002",
		UserName:     "viewer2",
		DisplayName:  "Viewer Two",
		GiftID:       202,
		GiftName:     "Finger Heart",
		UnitDiamonds: 5,
		RepeatCount:  1,
		RepeatEnd:    false,
		IsStreakable: true,
	})
	time.Sleep(20 * time.Millisecond)
	d.Submit(GiftInput{
		UserID:       "1002",
		UserName:     "viewer2",
		DisplayName:  "Viewer Two",
		GiftID:       202,
		GiftName:     "Finger Heart",
		UnitDiamonds: 5,
		RepeatCount:  4,
		RepeatEnd:    false,
		IsStreakable: true,
	})

	select {
	case ev := <-flushedCh:
		if ev.Count != 4 {
			t.Errorf("Count = %d, want 4", ev.Count)
		}
		if ev.Amount != 20 {
			t.Errorf("Amount = %v, want 20", ev.Amount)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for debounced gift flush")
	}
}

func TestGiftDebouncer_NonStreakableImmediate(t *testing.T) {
	var flushed []*chat.Event
	d := NewGiftDebouncer(5*time.Second, func(ev *chat.Event) {
		flushed = append(flushed, ev)
	})
	defer d.Close()

	d.Submit(GiftInput{
		UserID:       "9001",
		UserName:     "whale",
		DisplayName:  "Big Whale",
		GiftID:       999,
		GiftName:     "Universe",
		UnitDiamonds: 34999,
		RepeatCount:  1,
		RepeatEnd:    false,
		IsStreakable: false,
	})

	if len(flushed) != 1 {
		t.Fatalf("expected immediate flush for non-streakable gift, got %d", len(flushed))
	}
	if flushed[0].Amount != 34999 {
		t.Errorf("Amount = %v, want 34999", flushed[0].Amount)
	}
}

func TestConnector_ChatFollowAndGiftEvents(t *testing.T) {
	eventsCh := make(chan gotiktoklive.Event, 10)
	var closed sync.Once

	var mu sync.Mutex
	var gotEvents []*chat.Event
	var statuses []ConnectionStatus

	c := NewConnector(
		func(ev *chat.Event) {
			mu.Lock()
			gotEvents = append(gotEvents, ev)
			mu.Unlock()
		},
		func(s ConnectionStatus, _ string) {
			mu.Lock()
			statuses = append(statuses, s)
			mu.Unlock()
		},
	)
	c.waitInterval = 20 * time.Millisecond
	c.debounceWindow = 30 * time.Millisecond
	c.trackUserFn = func(username string) (<-chan gotiktoklive.Event, func(), error) {
		if username != "testcreator" {
			return nil, nil, errors.New("unexpected username")
		}
		return eventsCh, func() {
			closed.Do(func() { close(eventsCh) })
		}, nil
	}

	if err := c.Start("@testcreator"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = c.Stop() }()

	// Send chat event
	eventsCh <- gotiktoklive.ChatEvent{
		Comment: "Hello TikTok!",
		User: &gotiktoklive.User{
			ID:       101,
			Username: "chatter1",
			Nickname: "Chatter One",
		},
	}

	// Send follow event
	eventsCh <- gotiktoklive.UserEvent{
		Event: gotiktoklive.USER_FOLLOW,
		User: &gotiktoklive.User{
			ID:       102,
			Username: "follower1",
			Nickname: "Follower One",
		},
	}

	// Send gift event with RepeatEnd
	eventsCh <- gotiktoklive.GiftEvent{
		ID:          1,
		Name:        "Rose",
		Diamonds:    1,
		Type:        1,
		RepeatCount: 2,
		RepeatEnd:   true,
		User: &gotiktoklive.User{
			ID:       103,
			Username: "gifter1",
			Nickname: "Gifter One",
		},
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(gotEvents)
		mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(gotEvents) != 3 {
		t.Fatalf("expected 3 events, got %d", len(gotEvents))
	}
	if gotEvents[0].Kind != chat.EventKindMessage || gotEvents[0].Text != "Hello TikTok!" {
		t.Errorf("unexpected event 0: %+v", gotEvents[0])
	}
	if gotEvents[1].Kind != chat.EventKindFollow || gotEvents[1].DisplayName != "Follower One" {
		t.Errorf("unexpected event 1: %+v", gotEvents[1])
	}
	if gotEvents[2].Kind != chat.EventKindGift || gotEvents[2].Count != 2 {
		t.Errorf("unexpected event 2: %+v", gotEvents[2])
	}
	if len(statuses) == 0 {
		t.Errorf("expected status transitions")
	}
}

func TestExtractRoles_FanClubAndSuperFan(t *testing.T) {
	// 1. Fan Club member (Level 18 via CombineBadge str field)
	fanUser := &gotiktoklive.User{
		ID:       2001,
		Username: "fan18",
		Nickname: "Fan Eighteen",
		Badge: &gotiktoklive.BadgeAttributes{
			Badges: []*gotiktoklive.UserBadge{
				{
					Type: "BADGEDISPLAYTYPE_COMBINE",
					Name: `displayType:BADGEDISPLAYTYPE_COMBINE combine:{icon:{urlList:"https://p16-webcast.tiktokcdn.com/img/maliva/webcast-va/fans_badge_icon_lv18.png"} text:{key:"pm_mt_fans_club_name" defaultPattern:"Team Member"} str:"18"}`,
				},
			},
		},
	}
	ev := mapChatEvent(gotiktoklive.ChatEvent{
		Comment: "hello from fan club",
		User:    fanUser,
	})
	if ev == nil {
		t.Fatal("expected mapped chat event")
	}
	if ev.FanClubLevel != 18 {
		t.Errorf("FanClubLevel = %d, want 18", ev.FanClubLevel)
	}
	if len(ev.Roles) != 1 || ev.Roles[0] != "fan_club" {
		t.Errorf("Roles = %v, want [fan_club] (should not falsely match subscriber on 'Team Member')", ev.Roles)
	}

	// 2. Fan Club member via NumberConfig number field
	fanNumUser := &gotiktoklive.User{
		ID:       2002,
		Username: "fan32",
		Badge: &gotiktoklive.BadgeAttributes{
			Badges: []*gotiktoklive.UserBadge{
				{
					Type: "BADGEDISPLAYTYPE_COMBINE",
					Name: `combine:{icon:{uri:"webcast-va/fansclub_badge"} profileCardPanel:{profileContent:{numberConfig:{number: 32}}}}`,
				},
			},
		},
	}
	roles, lvl := extractRoles(fanNumUser, nil)
	if lvl != 32 {
		t.Errorf("lvl = %d, want 32", lvl)
	}
	if len(roles) != 1 || roles[0] != "fan_club" {
		t.Errorf("roles = %v, want [fan_club]", roles)
	}

	// 3. Super Fan (paid LIVE subscriber via UserIdentity.IsSubscriber + Level 10 Fan Club badge)
	superFanUser := &gotiktoklive.User{
		ID:       2003,
		Username: "superfan1",
		Badge: &gotiktoklive.BadgeAttributes{
			Badges: []*gotiktoklive.UserBadge{
				{
					Type: "BADGEDISPLAYTYPE_COMBINE",
					Name: `combine:{icon:{uri:"ttlive_superfan_badge"}}`,
				},
				{
					Type: "BADGEDISPLAYTYPE_COMBINE",
					Name: `combine:{icon:{uri:"fans_badge_lv12"} str: "12"}`,
				},
			},
		},
	}
	sfRoles, sfLvl := extractRoles(superFanUser, &gotiktoklive.UserIdentity{IsSubscriber: true})
	if sfLvl != 12 {
		t.Errorf("sfLvl = %d, want 12", sfLvl)
	}
	if len(sfRoles) != 2 || sfRoles[0] != "subscriber" || sfRoles[1] != "fan_club" {
		t.Errorf("sfRoles = %v, want [subscriber fan_club]", sfRoles)
	}
}

func TestGiftCatalogAndHeartMeInlineImage(t *testing.T) {
	cat := NewGiftCatalog()

	// Default Heart Me lookup by ID or Name
	hmMeta, ok := cat.Lookup(7934, "Heart Me")
	if !ok || hmMeta.ImageURL == "" {
		t.Fatalf("expected default Heart Me icon URL in catalog, got %+v (ok=%v)", hmMeta, ok)
	}

	var got *chat.Event
	d := NewGiftDebouncer(50*time.Millisecond, func(ev *chat.Event) {
		got = ev
	})
	defer d.Close()

	d.Submit(GiftInput{
		UserID:       "501",
		UserName:     "wifestreamviewer",
		DisplayName:  "Wife Stream Viewer",
		GiftID:       7934,
		GiftName:     "Heart Me",
		ImageURL:     hmMeta.ImageURL,
		UnitDiamonds: 1,
		RepeatCount:  1,
		RepeatEnd:    true,
		IsStreakable: false,
	})

	if got == nil {
		t.Fatal("expected Heart Me gift event to flush immediately")
	}
	if got.ItemName != "Heart Me" || got.UnitAmount != 1 || got.Amount != 1 || got.Count != 1 {
		t.Errorf("unexpected Heart Me metadata: ItemName=%q UnitAmount=%v Amount=%v Count=%d", got.ItemName, got.UnitAmount, got.Amount, got.Count)
	}
	if len(got.EmoteSpans) != 1 || got.EmoteSpans[0].URL != hmMeta.ImageURL {
		t.Errorf("expected inline EmoteSpan with Heart Me image URL, got %+v", got.EmoteSpans)
	}
}

func TestParseDebugProtoMessages_BarrageEmoteAndSubNotify(t *testing.T) {
	var gotEvents []*chat.Event
	c := NewConnector(func(ev *chat.Event) {
		gotEvents = append(gotEvents, ev)
	}, nil)

	// 1. WebcastBarrageMessage (FANSLEVELUPGRADE -> Heart Me gift with inline image)
	barrageMsg := &pb.WebcastBarrageMessage{
		MsgType: pb.WebcastBarrageMessage_FANSLEVELUPGRADE,
		Content: &pb.Text{
			DefaultPattern: "sent a Heart Me",
		},
		Icon: &pb.Image{
			UrlList: []string{"https://p16-webcast.tiktokcdn.com/img/maliva/webcast-va/heart_me_custom.png"},
		},
		FansLevelParam: &pb.WebcastBarrageMessage_BarrageTypeFansLevelParam{
			CurrentGrade: 5,
			User: &pb.User{
				Id:        777,
				DisplayId: "heartfan",
				Nickname:  "Heart Fan",
			},
		},
	}
	rawBarrage, err := proto.Marshal(barrageMsg)
	if err != nil {
		t.Fatalf("proto.Marshal barrage: %v", err)
	}
	debugRaw := fmt.Sprintf("unimplemented type %T\n%s", barrageMsg, base64.RawStdEncoding.EncodeToString(rawBarrage))
	decoded, err := ParseDebugProtoMessage(debugRaw)
	if err != nil || decoded == nil {
		t.Fatalf("expected ParseDebugProtoMessage to decode WebcastBarrageMessage, got err=%v", err)
	}
	c.HandleProtoMessage(decoded)

	// 2. WebcastEmoteChatMessage (subscriber custom emote)
	emoteMsg := &pb.WebcastEmoteChatMessage{
		User: &pb.User{
			Id:        888,
			DisplayId: "subuser",
			Nickname:  "Sub User",
		},
		UserIdentity: &pb.UserIdentity{
			IsSubscriberOfAnchor: true,
		},
		EmoteList: []*pb.Emote{
			{
				EmoteId: "em_heart_1",
				Image: &pb.Image{
					UrlList: []string{"https://p16-webcast.tiktokcdn.com/img/emote1.png"},
				},
			},
		},
	}
	c.HandleProtoMessage(emoteMsg)

	// 3. WebcastSubNotifyMessage (resubscription 6 months)
	subMsg := &pb.WebcastSubNotifyMessage{
		User: &pb.User{
			Id:        999,
			DisplayId: "loyalsub",
			Nickname:  "Loyal Sub",
		},
		SubMonth: 6,
	}
	c.HandleProtoMessage(subMsg)

	if len(gotEvents) != 3 {
		t.Fatalf("expected 3 events from protobuf messages, got %d", len(gotEvents))
	}

	// Verify Heart Me barrage gift
	ev0 := gotEvents[0]
	if ev0.Kind != chat.EventKindGift || ev0.ItemName != "Heart Me" || len(ev0.EmoteSpans) != 1 {
		t.Errorf("unexpected Heart Me barrage event: %+v", ev0)
	}
	if ev0.EmoteSpans[0].URL != "https://p16-webcast.tiktokcdn.com/img/maliva/webcast-va/heart_me_custom.png" {
		t.Errorf("unexpected Heart Me image URL: %q", ev0.EmoteSpans[0].URL)
	}

	// Verify EmoteChatMessage
	ev1 := gotEvents[1]
	if ev1.Kind != chat.EventKindMessage || len(ev1.EmoteSpans) != 1 || ev1.EmoteSpans[0].URL != "https://p16-webcast.tiktokcdn.com/img/emote1.png" {
		t.Errorf("unexpected EmoteChat event: %+v", ev1)
	}

	// Verify SubNotifyMessage
	ev2 := gotEvents[2]
	if ev2.Kind != chat.EventKindResub || ev2.Months != 6 {
		t.Errorf("unexpected SubNotify event: %+v", ev2)
	}
}

func TestMapUserEventShare(t *testing.T) {
	ev := mapUserEvent(gotiktoklive.UserEvent{
		Event: gotiktoklive.USER_SHARE,
		User: &gotiktoklive.User{
			ID:       404,
			Username: "sharer1",
			Nickname: "Sharer One",
		},
	})
	if ev == nil {
		t.Fatal("expected non-nil share event")
	}
	if ev.Kind != chat.EventKindShare || ev.UserName != "sharer1" || ev.DisplayName != "Sharer One" {
		t.Errorf("unexpected share event: %+v", ev)
	}
}



