package eventlog

import (
	"testing"
)

func TestQuerySummaryAddEntryAndClone(t *testing.T) {
	var s QuerySummary

	// 1. Regular chat message and moderation event should be ignored
	s.AddEntry(Entry{Kind: "message", Amount: 50})
	s.AddEntry(Entry{Kind: "moderation", Amount: 10})
	if s.TotalSubs != 0 || s.TotalBits != 0 || s.TotalRaids != 0 {
		t.Fatalf("expected non-alert entries to be ignored, got %+v", s)
	}

	// 2. Add various alert types
	s.AddEntry(Entry{Kind: "sub", Platform: "twitch"})
	s.AddEntry(Entry{Kind: "resub", Platform: "twitch"})
	s.AddEntry(Entry{Kind: "gift_sub", Platform: "twitch", Count: 5})
	s.AddEntry(Entry{Kind: "cheer", Platform: "twitch", Amount: 250})
	s.AddEntry(Entry{Kind: "gift", Platform: "kick", Amount: 100})
	s.AddEntry(Entry{Kind: "gift", Platform: "tiktok", Amount: 50})
	s.AddEntry(Entry{Kind: "super_chat", Platform: "youtube", Amount: 25, Currency: "USD"})
	s.AddEntry(Entry{Kind: "superchat", Platform: "youtube", Amount: 10, Currency: "EUR"})
	s.AddEntry(Entry{Kind: "raid", Platform: "twitch", Count: 42})
	s.AddEntry(Entry{Kind: "follow", Platform: "twitch"})
	s.AddEntry(Entry{Kind: "redemption", Platform: "twitch"})

	if s.TotalSubs != 2 {
		t.Errorf("expected 2 subs, got %d", s.TotalSubs)
	}
	if s.TotalGiftSubs != 5 {
		t.Errorf("expected 5 gift subs, got %d", s.TotalGiftSubs)
	}
	if s.TotalBits != 250 {
		t.Errorf("expected 250 bits, got %v", s.TotalBits)
	}
	if s.TotalKicks != 100 {
		t.Errorf("expected 100 kicks, got %v", s.TotalKicks)
	}
	if s.TotalDiamonds != 50 {
		t.Errorf("expected 50 diamonds, got %v", s.TotalDiamonds)
	}
	if s.SuperChatByCurr["USD"] != 25 {
		t.Errorf("expected 25 USD superchat, got %v", s.SuperChatByCurr["USD"])
	}
	if s.SuperChatByCurr["EUR"] != 10 {
		t.Errorf("expected 10 EUR superchat, got %v", s.SuperChatByCurr["EUR"])
	}
	if s.TotalRaids != 1 || s.TotalRaidViewers != 42 {
		t.Errorf("expected 1 raid with 42 viewers, got %d raids / %d viewers", s.TotalRaids, s.TotalRaidViewers)
	}
	if s.TotalFollows != 1 {
		t.Errorf("expected 1 follow, got %d", s.TotalFollows)
	}
	if s.TotalRedemptions != 1 {
		t.Errorf("expected 1 redemption, got %d", s.TotalRedemptions)
	}

	// 3. Test Clone independence
	cloned := s.Clone()
	cloned.SuperChatByCurr["USD"] = 999
	cloned.TotalSubs = 100

	if s.SuperChatByCurr["USD"] != 25 {
		t.Errorf("modifying cloned map mutated original: %v", s.SuperChatByCurr["USD"])
	}
	if s.TotalSubs != 2 {
		t.Errorf("modifying cloned struct mutated original: %d", s.TotalSubs)
	}
}
