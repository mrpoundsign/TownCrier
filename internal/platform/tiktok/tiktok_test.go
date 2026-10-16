package tiktok

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/steampoweredtaco/gotiktoklive"

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
