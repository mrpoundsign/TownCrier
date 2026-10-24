package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/chat"
	"github.com/mrpoundsign/TownCrier/internal/config"
	"github.com/mrpoundsign/TownCrier/internal/eventlog"
	"github.com/mrpoundsign/TownCrier/internal/platform/twitch"
)

// HeldMessages returns a snapshot of all currently pending AutoMod/Blocked-Term held messages
// in strict FIFO order (oldest at index 0, newest at the end).
func (s *Session) HeldMessages() []twitch.AutoModHoldEvent {
	s.heldMu.Lock()
	defer s.heldMu.Unlock()
	if len(s.heldMessages) == 0 {
		return nil
	}
	out := make([]twitch.AutoModHoldEvent, len(s.heldMessages))
	copy(out, s.heldMessages)
	return out
}

// SubscribeHeldMessages registers a listener invoked whenever the pending Held-for-Review queue changes.
func (s *Session) SubscribeHeldMessages(fn func()) func() {
	if fn == nil {
		return func() {}
	}
	id := s.heldSubSeq.Add(1)
	s.heldMu.Lock()
	if s.heldListeners == nil {
		s.heldListeners = make(map[uint64]func())
	}
	s.heldListeners[id] = fn
	s.heldMu.Unlock()

	return func() {
		s.heldMu.Lock()
		delete(s.heldListeners, id)
		s.heldMu.Unlock()
	}
}

func (s *Session) notifyHeldListeners() {
	s.heldMu.Lock()
	listeners := make([]func(), 0, len(s.heldListeners))
	for _, fn := range s.heldListeners {
		if fn != nil {
			listeners = append(listeners, fn)
		}
	}
	s.heldMu.Unlock()

	for _, fn := range listeners {
		fn()
	}
}

// HandleAutoModHold processes an inbound Twitch EventSub automod.message.hold or automod.message.update event.
func (s *Session) HandleAutoModHold(ev *twitch.AutoModHoldEvent) {
	if ev == nil || strings.TrimSpace(ev.MessageID) == "" {
		return
	}

	status := strings.ToUpper(strings.TrimSpace(ev.Status))
	if status == "" || status == "PENDING" {
		s.heldMu.Lock()
		exists := false
		for i := range s.heldMessages {
			if s.heldMessages[i].MessageID == ev.MessageID {
				s.heldMessages[i] = *ev
				exists = true
				break
			}
		}
		if !exists {
			// Append at the end (FIFO: oldest at top, newest at bottom)
			s.heldMessages = append(s.heldMessages, *ev)
		}
		s.heldMu.Unlock()

		if !exists {
			s.recordEventLog(&eventlog.Entry{
				ID:            "held-" + ev.MessageID,
				SessionID:     s.ActiveSessionID(),
				CreatedAt:     ev.HeldAt,
				UpdatedAt:     time.Now().UTC(),
				Platform:      "twitch",
				Channel:       ev.Channel,
				Kind:          string(chat.EventKindMessage),
				UserID:        ev.UserID,
				UserName:      ev.UserLogin,
				DisplayName:   ev.UserName,
				RawText:       ev.Text,
				PassedFilters: false,
				Spoken:        false,
				Outcome:       eventlog.OutcomeHeldForReview,
				OutcomeDetail: ev.Reason,
			})
		}
		s.notifyHeldListeners()
		return
	}

	// Resolved (APPROVED, DENIED, EXPIRED)
	removed := s.removeHeldMessageByID(ev.MessageID)
	target := ev
	if removed != nil && target.Text == "" {
		target = removed
	}

	if elog := s.EventLogger(); elog != nil {
		outcome := eventlog.OutcomeDroppedModeration
		detail := "AutoMod Denied (" + status + ")"
		if status == "APPROVED" || status == "ALLOW" {
			detail = "AutoMod Approved"
		}
		_ = elog.UpdateOutcome(context.Background(), "held-"+ev.MessageID, eventlog.OutcomeUpdate{
			Spoken:        false,
			Outcome:       outcome,
			OutcomeDetail: detail,
			UpdatedAt:     time.Now().UTC(),
		})
	}

	if status == "APPROVED" || status == "ALLOW" {
		_ = s.DispatchEvent(&chat.Event{
			ID:          target.MessageID,
			Platform:    "twitch",
			Channel:     target.Channel,
			Kind:        chat.EventKindMessage,
			Timestamp:   time.Now().UTC(),
			UserID:      target.UserID,
			UserName:    target.UserLogin,
			DisplayName: target.UserName,
			Text:        target.Text,
		})
	}
}

func (s *Session) removeHeldMessageByID(msgID string) *twitch.AutoModHoldEvent {
	cleanID := strings.TrimSpace(msgID)
	if cleanID == "" {
		return nil
	}
	s.heldMu.Lock()
	var removed *twitch.AutoModHoldEvent
	for i := range s.heldMessages {
		if s.heldMessages[i].MessageID == cleanID {
			cp := s.heldMessages[i]
			removed = &cp
			s.heldMessages = append(s.heldMessages[:i], s.heldMessages[i+1:]...)
			break
		}
	}
	s.heldMu.Unlock()
	if removed != nil {
		s.notifyHeldListeners()
	}
	return removed
}

// ResolveHeldMessage calls Twitch Helix POST /moderation/automod/message ("ALLOW" or "DENY")
// and updates the local Held-for-Review queue and TTS pipeline.
func (s *Session) ResolveHeldMessage(ctx context.Context, msgID string, action string) error {
	cleanAction := strings.ToUpper(strings.TrimSpace(action))
	if cleanAction != "ALLOW" && cleanAction != "DENY" {
		return fmt.Errorf("invalid action %q", action)
	}
	if s.twitchConnector != nil && s.twitchOAuth != nil && s.twitchOAuth.HasToken() {
		if err := s.twitchConnector.ResolveAutoModMessage(ctx, msgID, cleanAction); err != nil {
			return err
		}
	}

	removed := s.removeHeldMessageByID(msgID)
	if elog := s.EventLogger(); elog != nil {
		detail := "AutoMod Denied by moderator"
		if cleanAction == "ALLOW" {
			detail = "AutoMod Approved by moderator"
		}
		_ = elog.UpdateOutcome(ctx, "held-"+msgID, eventlog.OutcomeUpdate{
			Spoken:        false,
			Outcome:       eventlog.OutcomeDroppedModeration,
			OutcomeDetail: detail,
			UpdatedAt:     time.Now().UTC(),
		})
	}

	if cleanAction == "ALLOW" && removed != nil {
		_ = s.DispatchEvent(&chat.Event{
			ID:          removed.MessageID,
			Platform:    "twitch",
			Channel:     removed.Channel,
			Kind:        chat.EventKindMessage,
			Timestamp:   time.Now().UTC(),
			UserID:      removed.UserID,
			UserName:    removed.UserLogin,
			DisplayName: removed.UserName,
			Text:        removed.Text,
		})
	}
	return nil
}

// ModerateDeleteMessage deletes a chat message on the source platform (if OAuth is connected) and immediately purges it from the TTS speech queue.
func (s *Session) ModerateDeleteMessage(ctx context.Context, entry eventlog.Entry) error {
	msgID := strings.TrimSpace(entry.ID)
	if msgID == "" {
		return errors.New("empty message ID")
	}

	switch strings.ToLower(strings.TrimSpace(entry.Platform)) {
	case "twitch":
		if s.twitchConnector != nil && s.twitchOAuth != nil && s.twitchOAuth.HasToken() {
			if err := s.twitchConnector.DeleteChatMessage(ctx, msgID); err != nil {
				return err
			}
		}
	case "youtube":
		if s.youtubeConnector != nil && s.youtubeOAuth != nil && s.youtubeOAuth.HasToken() {
			if err := s.youtubeConnector.DeleteChatMessage(ctx, msgID); err != nil {
				return err
			}
		}
	case "kick":
		if s.kickConnector != nil && s.kickOAuth != nil && s.kickOAuth.HasToken() {
			if err := s.kickConnector.DeleteChatMessage(ctx, msgID); err != nil {
				return err
			}
		}
	}

	if s.queue != nil {
		s.queue.CancelMessage(msgID)
	}
	if elog := s.EventLogger(); elog != nil {
		_ = elog.UpdateOutcome(ctx, msgID, eventlog.OutcomeUpdate{
			Spoken:        false,
			Outcome:       eventlog.OutcomeDroppedModeration,
			OutcomeDetail: "Deleted by moderator",
			UpdatedAt:     time.Now().UTC(),
		})
	}
	return nil
}

// ModerateTimeoutUser times out a user on the source platform (for durationSec seconds), purges their queued TTS speech, and updates the log outcome.
func (s *Session) ModerateTimeoutUser(ctx context.Context, entry eventlog.Entry, durationSec int, reason string) error {
	if durationSec <= 0 {
		durationSec = 600
	}
	switch strings.ToLower(strings.TrimSpace(entry.Platform)) {
	case "twitch":
		if s.twitchConnector != nil && s.twitchOAuth != nil && s.twitchOAuth.HasToken() {
			if err := s.twitchConnector.BanOrTimeoutUser(ctx, entry.UserID, entry.UserName, durationSec, reason); err != nil {
				return err
			}
		}
	case "youtube":
		if s.youtubeConnector != nil && s.youtubeOAuth != nil && s.youtubeOAuth.HasToken() {
			if err := s.youtubeConnector.BanOrTimeoutUser(ctx, entry.UserID, durationSec); err != nil {
				return err
			}
		}
	case "kick":
		if s.kickConnector != nil && s.kickOAuth != nil && s.kickOAuth.HasToken() {
			if err := s.kickConnector.BanOrTimeoutUser(ctx, entry.UserID, durationSec, reason); err != nil {
				return err
			}
		}
	}

	s.purgeUserSpeech(entry.Platform, entry.UserID, entry.UserName)
	if elog := s.EventLogger(); elog != nil && entry.ID != "" {
		_ = elog.UpdateOutcome(ctx, entry.ID, eventlog.OutcomeUpdate{
			Spoken:        false,
			Outcome:       eventlog.OutcomeDroppedModeration,
			OutcomeDetail: fmt.Sprintf("Timed out (%ds)", durationSec),
			UpdatedAt:     time.Now().UTC(),
		})
	}
	return nil
}

// ModerateBanUser permanently bans a user on the source platform, purges their queued TTS speech, and marks them Ignored in TownCrier.
func (s *Session) ModerateBanUser(ctx context.Context, entry eventlog.Entry, reason string) error {
	switch strings.ToLower(strings.TrimSpace(entry.Platform)) {
	case "twitch":
		if s.twitchConnector != nil && s.twitchOAuth != nil && s.twitchOAuth.HasToken() {
			if err := s.twitchConnector.BanOrTimeoutUser(ctx, entry.UserID, entry.UserName, 0, reason); err != nil {
				return err
			}
		}
	case "youtube":
		if s.youtubeConnector != nil && s.youtubeOAuth != nil && s.youtubeOAuth.HasToken() {
			if err := s.youtubeConnector.BanOrTimeoutUser(ctx, entry.UserID, 0); err != nil {
				return err
			}
		}
	case "kick":
		if s.kickConnector != nil && s.kickOAuth != nil && s.kickOAuth.HasToken() {
			if err := s.kickConnector.BanOrTimeoutUser(ctx, entry.UserID, 0, reason); err != nil {
				return err
			}
		}
	}

	s.purgeUserSpeech(entry.Platform, entry.UserID, entry.UserName)
	_, _ = s.setUserLocallyMutedTTS(entry.Platform, entry.UserID, entry.UserName, true)

	if elog := s.EventLogger(); elog != nil && entry.ID != "" {
		_ = elog.UpdateOutcome(ctx, entry.ID, eventlog.OutcomeUpdate{
			Spoken:        false,
			Outcome:       eventlog.OutcomeDroppedModeration,
			OutcomeDetail: "Permanently banned by moderator",
			UpdatedAt:     time.Now().UTC(),
		})
	}
	return nil
}

// ToggleLocalMuteUserTTS toggles a user's local TTS mute (Ignored) state in TownCrier's viewer config
// and immediately aborts/purges their queued speech when muted. Works across all platforms.
func (s *Session) ToggleLocalMuteUserTTS(platform, userID, userName string) (bool, error) {
	currentlyMuted := s.IsUserLocallyMutedTTS(platform, userID, userName)
	nextMuted := !currentlyMuted
	if nextMuted {
		s.purgeUserSpeech(platform, userID, userName)
	}
	return s.setUserLocallyMutedTTS(platform, userID, userName, nextMuted)
}

// IsUserLocallyMutedTTS reports whether the given user is marked Ignored in TownCrier's ViewerConfig.
func (s *Session) IsUserLocallyMutedTTS(platform, userID, userName string) bool {
	cfg := s.Config()
	if cfg == nil || len(cfg.Viewers) == 0 {
		return false
	}
	_, vc, ok := config.LookupViewerWithKey(cfg.Viewers, platform, userID, userName)
	return ok && vc.Ignored
}

func (s *Session) setUserLocallyMutedTTS(platform, userID, userName string, muted bool) (bool, error) {
	plat := strings.ToLower(strings.TrimSpace(platform))
	if plat == "" {
		plat = "twitch"
	}
	handle := strings.TrimSpace(userName)
	if handle == "" {
		handle = strings.TrimSpace(userID)
	}
	if handle == "" {
		return false, errors.New("empty username")
	}

	err := s.UpdateConfig(func(c *config.Config) {
		if c.Viewers == nil {
			c.Viewers = make(map[string]config.ViewerConfig)
		}
		key, vc, found := config.LookupViewerWithKey(c.Viewers, plat, userID, userName)
		if !found {
			key = config.ViewerKey(plat, handle)
			vc = config.ViewerConfig{
				Platform: plat,
				UserID:   handle,
				Accounts: []config.ViewerAccount{{Platform: plat, UserID: handle}},
			}
		}
		vc.Ignored = muted
		if muted {
			vc.Allowed = false
		}
		c.Viewers[key] = vc
	})
	return muted, err
}

func (s *Session) purgeUserSpeech(platform, userID, userName string) {
	if s.queue == nil {
		return
	}
	if id := strings.TrimSpace(userID); id != "" {
		s.queue.CancelUser(platform, id)
	}
	if name := strings.TrimSpace(userName); name != "" {
		s.queue.CancelUser(platform, name)
	}
}
