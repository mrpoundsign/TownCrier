package twitch

import (
	"testing"
	"time"

	"github.com/gempir/go-twitch-irc/v4"
	"github.com/mrpoundsign/OctochorpeBot/internal/chat"
)

func TestCleanChannelName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"#mrpoundsign", "mrpoundsign"},
		{"mrpoundsign", "mrpoundsign"},
		{"  #MRPOUNDSIGN  ", "mrpoundsign"},
		{"", ""},
		{"   ", ""},
	}

	for _, tc := range tests {
		got := cleanChannelName(tc.input)
		if got != tc.expected {
			t.Errorf("cleanChannelName(%q) = %q; expected %q", tc.input, got, tc.expected)
		}
	}
}

func TestExtractRoles(t *testing.T) {
	badges := map[string]int{
		"broadcaster": 1,
		"moderator":   1,
		"vip":         1,
		"subscriber":  12,
		"partner":     1,
	}

	roles := extractRoles(badges)
	expected := map[string]bool{
		"broadcaster": true,
		"mod":         true,
		"vip":         true,
		"subscriber":  true,
	}

	if len(roles) != 4 {
		t.Fatalf("expected 4 roles, got %d: %v", len(roles), roles)
	}

	for _, r := range roles {
		if !expected[r] {
			t.Errorf("unexpected role extracted: %s", r)
		}
	}
}

func TestMapEmotes(t *testing.T) {
	twitchEmotes := []*twitch.Emote{
		{
			Name: "Kappa",
			ID:   "25",
			Positions: []twitch.EmotePosition{
				{Start: 0, End: 4},
			},
		},
		{
			Name: "PogChamp",
			ID:   "88",
			Positions: []twitch.EmotePosition{
				{Start: 6, End: 13},
			},
		},
	}

	spans := mapEmotes(twitchEmotes)
	if len(spans) != 2 {
		t.Fatalf("expected 2 emote spans, got %d", len(spans))
	}

	if spans[0].Name != "Kappa" || spans[0].Start != 0 || spans[0].End != 5 {
		t.Errorf("unexpected span[0]: %+v", spans[0])
	}
	if spans[1].Name != "PogChamp" || spans[1].Start != 6 || spans[1].End != 14 {
		t.Errorf("unexpected span[1]: %+v", spans[1])
	}
}

func TestConnectorPrivateMessageHandling(t *testing.T) {
	var dispatched *chat.Event
	conn := NewConnector(
		func(event *chat.Event) {
			dispatched = event
		},
		nil,
		nil,
	)

	now := time.Now()
	msg := twitch.PrivateMessage{
		ID:      "msg-123",
		Channel: "mrpoundsign",
		User: twitch.User{
			ID:          "1001",
			Name:        "viewer1",
			DisplayName: "ViewerOne",
			Badges:      map[string]int{"vip": 1},
		},
		Message: "Hello streamer! Kappa",
		Time:    now,
		Bits:    100,
		Emotes: []*twitch.Emote{
			{Name: "Kappa", ID: "25", Positions: []twitch.EmotePosition{{Start: 16, End: 20}}},
		},
	}

	conn.handlePrivateMessage(msg)

	if dispatched == nil {
		t.Fatal("expected dispatched event, got nil")
	}

	if dispatched.ID != "msg-123" {
		t.Errorf("expected ID 'msg-123', got %q", dispatched.ID)
	}
	if dispatched.Platform != "twitch" {
		t.Errorf("expected platform 'twitch', got %q", dispatched.Platform)
	}
	if dispatched.Kind != chat.EventKindCheer {
		t.Errorf("expected kind 'cheer', got %v", dispatched.Kind)
	}
	if dispatched.Amount != 100 {
		t.Errorf("expected amount 100, got %f", dispatched.Amount)
	}
	if dispatched.DisplayName != "ViewerOne" {
		t.Errorf("expected DisplayName 'ViewerOne', got %q", dispatched.DisplayName)
	}
	if len(dispatched.Roles) != 1 || dispatched.Roles[0] != "vip" {
		t.Errorf("expected roles ['vip'], got %v", dispatched.Roles)
	}
	if len(dispatched.EmoteSpans) != 1 || dispatched.EmoteSpans[0].Name != "Kappa" {
		t.Errorf("expected emote Kappa, got %v", dispatched.EmoteSpans)
	}
}

func TestConnectorModerationHandling(t *testing.T) {
	var modEvents []*chat.ModerationEvent
	conn := NewConnector(
		nil,
		func(modEvent *chat.ModerationEvent) {
			modEvents = append(modEvents, modEvent)
		},
		nil,
	)

	// 1. Delete message
	conn.handleClearMessage(twitch.ClearMessage{
		TargetMsgID: "bad-msg-456",
	})

	if len(modEvents) != 1 {
		t.Fatalf("expected 1 mod event, got %d", len(modEvents))
	}
	if modEvents[0].Action != chat.ModActionDeleteMessage || modEvents[0].TargetMessageID != "bad-msg-456" {
		t.Errorf("unexpected delete message event: %+v", modEvents[0])
	}

	// 2. Ban user
	conn.handleClearChat(twitch.ClearChatMessage{
		TargetUsername: "troll_user",
	})

	if len(modEvents) != 2 {
		t.Fatalf("expected 2 mod events, got %d", len(modEvents))
	}
	if modEvents[1].Action != chat.ModActionBanUser || modEvents[1].TargetUserID != "troll_user" {
		t.Errorf("unexpected ban user event: %+v", modEvents[1])
	}

	// 3. Clear entire chat
	conn.handleClearChat(twitch.ClearChatMessage{
		TargetUsername: "",
	})

	if len(modEvents) != 3 {
		t.Fatalf("expected 3 mod events, got %d", len(modEvents))
	}
	if modEvents[2].Action != chat.ModActionClearChat {
		t.Errorf("unexpected clear chat event: %+v", modEvents[2])
	}
}

func TestConnectorUserNoticeHandling(t *testing.T) {
	var dispatched *chat.Event
	conn := NewConnector(
		func(event *chat.Event) {
			dispatched = event
		},
		nil,
		nil,
	)

	// Sub message
	conn.handleUserNotice(twitch.UserNoticeMessage{
		ID:      "sub-1",
		MsgID:   "resub",
		Message: "Loving the stream!",
		Time:    time.Now(),
		User: twitch.User{
			Name:        "subscriber1",
			DisplayName: "SubOne",
		},
		MsgParams: map[string]string{
			"msg-param-cumulative-months": "5",
		},
	})

	if dispatched == nil {
		t.Fatal("expected dispatched event, got nil")
	}
	if dispatched.Kind != chat.EventKindResub {
		t.Errorf("expected KindResub, got %v", dispatched.Kind)
	}
	if dispatched.Months != 5 {
		t.Errorf("expected 5 months, got %d", dispatched.Months)
	}
	if dispatched.Text != "Loving the stream!" {
		t.Errorf("expected message text, got %q", dispatched.Text)
	}
}
