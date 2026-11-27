package bot

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mrpoundsign/TownCrier/pkg/chat"
	"github.com/mrpoundsign/TownCrier/pkg/config"
)

func newVoiceMatchTestEngine(t *testing.T, cfg *config.Config, hitCount *atomic.Int32) *Engine {
	t.Helper()
	st, err := OpenStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	eng := NewEngine(st, HostBridge{
		GetConfig: func() *config.Config { return cfg },
		SpeakTTS: func(text, voiceID string, speed float32, priority int, username string) error {
			hitCount.Add(1)
			return nil
		},
	})
	eng.Start()
	t.Cleanup(func() { _ = eng.Close() })
	return eng
}

func TestVoiceCommandPunctuationAndPhoneticMatching(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Bot.Enabled = true
	cfg.Bot.Actions = []config.BotActionDef{
		{
			ID:      "act-hello",
			Name:    "Say Hello",
			Enabled: true,
			QueueID: "concurrent",
			SubActions: []config.BotSubActionDef{
				{ID: "s1", Kind: "speak_tts", Enabled: true, Text: "Hello there"},
			},
		},
	}

	var hits atomic.Int32
	eng := newVoiceMatchTestEngine(t, cfg, &hits)

	// 1. Contains & Exact normalize Whisper punctuation and casing.
	cfg.Bot.VoiceCommands = []config.BotVoiceCommandDef{
		{ID: "vc-1", Name: "Octo Hello", Enabled: true, Phrase: "octo say hello", MatchMode: "contains", ActionID: "act-hello"},
	}
	ev := eng.OnVoiceTranscript("Octo, say hello.")
	require.Len(t, ev.Matches, 1)
	require.True(t, ev.Matches[0].Fired)
	require.Eventually(t, func() bool { return hits.Load() == 1 }, 2*time.Second, 20*time.Millisecond)

	cfg.Bot.VoiceCommands[0].MatchMode = "exact"
	ev = eng.OnVoiceTranscript("Octo. Say hello!")
	require.Len(t, ev.Matches, 1)
	require.True(t, ev.Matches[0].Fired)
	require.Eventually(t, func() bool { return hits.Load() == 2 }, 2*time.Second, 20*time.Millisecond)

	// 2. Phonetic mode handles common proper-noun STT drift and split tokens without false positives.
	cfg.Bot.VoiceCommands[0].MatchMode = "phonetic"
	for _, sample := range []string{
		"Auto, say hello.",
		"Otto say hello!",
		"Octa say hello",
		"Okto say hello",
		"Ock toe, say hello.",
	} {
		dry := eng.TestVoiceTranscript(sample, false)
		require.Len(t, dry.Matches, 1, "expected phonetic match for %q (note: %s)", sample, dry.Note)
		require.False(t, dry.Matches[0].Fired, "dry run should not fire")
	}

	for _, negative := range []string{
		"about say hello",
		"potato say hello",
		"hello there friend",
	} {
		dry := eng.TestVoiceTranscript(negative, false)
		require.Empty(t, dry.Matches, "expected no phonetic match for %q", negative)
	}
}

func TestVoiceCommandWakeWordSameUtteranceWindowAndIgnoreFlag(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Bot.Enabled = true
	cfg.VoiceDetection.WakeWord = "octo"
	cfg.VoiceDetection.WakeWindowSec = 6
	cfg.Bot.Actions = []config.BotActionDef{
		{
			ID:      "act-hello",
			Name:    "Say Hello",
			Enabled: true,
			QueueID: "concurrent",
			SubActions: []config.BotSubActionDef{
				{ID: "s1", Kind: "speak_tts", Enabled: true, Text: "Hello"},
			},
		},
		{
			ID:      "act-panic",
			Name:    "Always Panic",
			Enabled: true,
			QueueID: "concurrent",
			SubActions: []config.BotSubActionDef{
				{ID: "s2", Kind: "speak_tts", Enabled: true, Text: "Emergency"},
			},
		},
	}
	cfg.Bot.VoiceCommands = []config.BotVoiceCommandDef{
		{ID: "vc-hello", Name: "Hello", Enabled: true, Phrase: "say hello", MatchMode: "contains", ActionID: "act-hello"},
		{ID: "vc-full", Name: "Legacy Octo Hello", Enabled: true, Phrase: "octo say hello", MatchMode: "phonetic", ActionID: "act-hello"},
		{ID: "vc-always", Name: "Always", Enabled: true, Phrase: "red alert", MatchMode: "contains", IgnoreWakeWord: true, ActionID: "act-panic"},
	}

	curTime := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	var hits atomic.Int32
	eng := newVoiceMatchTestEngine(t, cfg, &hits)
	eng.nowFunc = func() time.Time { return curTime }

	// Without wake word, gated command does not fire, while IgnoreWakeWord command does.
	ev := eng.OnVoiceTranscript("say hello")
	require.Equal(t, "waiting", ev.WakeState)
	require.Len(t, ev.Matches, 1)
	require.False(t, ev.Matches[0].Fired)
	require.Contains(t, ev.Matches[0].SkipReason, "wake word")

	evAlways := eng.OnVoiceTranscript("red alert!")
	require.Len(t, evAlways.Matches, 1)
	require.True(t, evAlways.Matches[0].Fired)
	require.Eventually(t, func() bool { return hits.Load() == 1 }, 2*time.Second, 20*time.Millisecond)

	// Same-utterance phonetic wake word strips wake word and triggers both stripped and legacy phrases cleanly.
	evSame := eng.OnVoiceTranscript("Otto, say hello!")
	require.Equal(t, "same_utterance", evSame.WakeState)
	require.Equal(t, "say hello", evSame.Effective)
	require.Len(t, evSame.Matches, 2)
	require.Eventually(t, func() bool { return hits.Load() == 3 }, 2*time.Second, 20*time.Millisecond)

	// Two-step wake word arms window; dry run does NOT mutate armed window!
	evDryArm := eng.TestVoiceTranscript("Auto.", false)
	require.Equal(t, "armed", evDryArm.WakeState)
	evAfterDry := eng.OnVoiceTranscript("say hello")
	require.Equal(t, "waiting", evAfterDry.WakeState)

	// Live standalone wake word arms the window for 6s.
	evArm := eng.OnVoiceTranscript("Auto.")
	require.Equal(t, "armed", evArm.WakeState)

	curTime = curTime.Add(3 * time.Second)
	evFollow := eng.OnVoiceTranscript("say hello")
	require.Equal(t, "armed_window", evFollow.WakeState)
	require.Eventually(t, func() bool { return hits.Load() == 4 }, 2*time.Second, 20*time.Millisecond)

	// Armed window is consumed once used.
	evSecond := eng.OnVoiceTranscript("say hello")
	require.Equal(t, "waiting", evSecond.WakeState)

	// Re-arm and let window expire after 6s.
	_ = eng.OnVoiceTranscript("Octo")
	curTime = curTime.Add(7 * time.Second)
	evExpired := eng.OnVoiceTranscript("say hello")
	require.Equal(t, "waiting", evExpired.WakeState)

	// Comma-separated wake word aliases match any listed trigger phrase.
	cfg.VoiceDetection.WakeWord = "octothorpe, hey octo, dr thorpe"
	evAliasSame := eng.OnVoiceTranscript("Dr. Thorpe, say hello.")
	require.Equal(t, "same_utterance", evAliasSame.WakeState)
	require.Equal(t, "say hello", evAliasSame.Effective)
	require.Len(t, evAliasSame.Matches, 1)
	require.Equal(t, "vc-hello", evAliasSame.Matches[0].CommandID)
	require.Eventually(t, func() bool { return hits.Load() == 5 }, 2*time.Second, 20*time.Millisecond)

	evAliasArm := eng.OnVoiceTranscript("Hey, Octo.")
	require.Equal(t, "armed", evAliasArm.WakeState)
}

func TestVoiceCommandResponseVoicePrecedence(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Bot.Enabled = true
	cfg.VoiceDetection.ResponseVoiceID = "bm_lewis"
	cfg.VoiceDetection.ResponseSpeed = 1.25

	cfg.Bot.Actions = []config.BotActionDef{
		{
			ID:      "act-default-voice",
			Name:    "Say Hello Default Step",
			Enabled: true,
			QueueID: "concurrent",
			SubActions: []config.BotSubActionDef{
				{ID: "s1", Kind: "speak_tts", Enabled: true, Text: "Hello from bot"},
			},
		},
		{
			ID:      "act-override-voice",
			Name:    "Say Hello Explicit Step",
			Enabled: true,
			QueueID: "concurrent",
			SubActions: []config.BotSubActionDef{
				{ID: "s2", Kind: "speak_tts", Enabled: true, Text: "Override voice", VoiceID: "af_bella", Speed: 0.90},
			},
		},
	}
	cfg.Bot.Commands = []config.BotCommandDef{
		{ID: "cmd-1", Name: "Chat Hello", Enabled: true, Trigger: "!hello", MatchMode: "exact", ActionID: "act-default-voice"},
	}
	cfg.Bot.VoiceCommands = []config.BotVoiceCommandDef{
		{ID: "vc-default", Name: "Voice Default", Enabled: true, Phrase: "say hello", MatchMode: "contains", ActionID: "act-default-voice"},
		{ID: "vc-override", Name: "Voice Override", Enabled: true, Phrase: "use bella", MatchMode: "contains", ActionID: "act-override-voice"},
	}

	st, err := OpenStore(":memory:")
	require.NoError(t, err)
	defer func() { _ = st.Close() }()

	type spokenCall struct {
		text    string
		voiceID string
		speed   float32
	}
	var mu sync.Mutex
	var calls []spokenCall

	eng := NewEngine(st, HostBridge{
		GetConfig: func() *config.Config { return cfg },
		SpeakTTS: func(text, voiceID string, speed float32, priority int, username string) error {
			mu.Lock()
			calls = append(calls, spokenCall{text: text, voiceID: voiceID, speed: speed})
			mu.Unlock()
			return nil
		},
	})
	eng.Start()
	defer func() { _ = eng.Close() }()

	// 1. Voice command with step left on Default uses VoiceDetection.ResponseVoiceID & ResponseSpeed.
	eng.OnVoiceTranscript("say hello")
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(calls) == 1
	}, 2*time.Second, 20*time.Millisecond)
	mu.Lock()
	require.Equal(t, spokenCall{text: "Hello from bot", voiceID: "bm_lewis", speed: 1.25}, calls[0])
	mu.Unlock()

	// 2. Voice command with explicit per-step VoiceID overrides ResponseVoiceID.
	eng.OnVoiceTranscript("use bella")
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(calls) == 2
	}, 2*time.Second, 20*time.Millisecond)
	mu.Lock()
	require.Equal(t, spokenCall{text: "Override voice", voiceID: "af_bella", speed: 0.90}, calls[1])
	mu.Unlock()

	// 3. Chat !command using the exact same action does NOT use ResponseVoiceID (keeps "" -> Global Default).
	eng.OnEvent(&chat.Event{ID: "msg-1", Platform: "twitch", Kind: chat.EventKindMessage, UserName: "alice", Text: "!hello"})
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(calls) == 3
	}, 2*time.Second, 20*time.Millisecond)
	mu.Lock()
	require.Equal(t, spokenCall{text: "Hello from bot", voiceID: "", speed: 0}, calls[2])
	mu.Unlock()
}
