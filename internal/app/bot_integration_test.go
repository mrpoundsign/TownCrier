package app

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mrpoundsign/TownCrier/internal/eventlog"
	"github.com/mrpoundsign/TownCrier/pkg/chat"
	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/secrets"
	"github.com/mrpoundsign/TownCrier/pkg/stt"
)

func TestSessionBotRawEventBusConsumeAndVoiceTrigger(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.TTS.Enabled = true
	cfg.TTS.OutputMode = config.TTSModeAlwaysOn
	cfg.Bots.IgnoreKnownBots = true
	// IgnoredPrefixes filters "!" from TTS, but raw Event Bus must still deliver "!hug" to botEngine.
	twCfg := cfg.Platforms["twitch"]
	twCfg.Enabled = true
	twCfg.AccountName = "streamerchan"
	twCfg.ReadRules.IgnorePrefixes = true
	twCfg.ReadRules.IgnoredPrefixes = []string{"!"}
	cfg.Platforms["twitch"] = twCfg

	cfg.Bot.Enabled = true
	cfg.Bot.Commands = []config.BotCommandDef{
		{
			ID:         "cmd-hug",
			Name:       "Hug",
			Enabled:    true,
			Trigger:    "!hug",
			MatchMode:  "prefix",
			ConsumeTTS: true,
			ActionID:   "act-hug",
		},
	}
	cfg.Bot.VoiceCommands = []config.BotVoiceCommandDef{
		{
			ID:        "vc-1",
			Name:      "Voice Scene",
			Enabled:   true,
			Phrase:    "switch scene now",
			MatchMode: "contains",
			ActionID:  "act-voice",
		},
	}
	cfg.Bot.Actions = []config.BotActionDef{
		{
			ID:      "act-hug",
			Name:    "Hug Variable",
			Enabled: true,
			SubActions: []config.BotSubActionDef{
				{ID: "s1", Kind: "set_var", Enabled: true, Scope: "global_persistent", Key: "last_hugger", VarKind: "string", Value: "%user%:%arg0%"},
			},
		},
		{
			ID:      "act-voice",
			Name:    "Voice Action",
			Enabled: true,
			SubActions: []config.BotSubActionDef{
				{ID: "v1", Kind: "set_var", Enabled: true, Scope: "global_ephemeral", Key: "voice_hit", VarKind: "string", Value: "%transcript%"},
			},
		},
	}

	s := NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
	defer func() { _ = s.Close() }()

	memLog, err := eventlog.NewSQLiteBackend(":memory:")
	require.NoError(t, err)
	logMgr := eventlog.NewManager(eventlog.RetentionPolicy{}, memLog)
	defer func() { _ = logMgr.Close() }()
	s.SetEventLogger(logMgr)

	require.NoError(t, s.Start())

	// Dispatch "!hug @bob" as a message -> ConsumeTTS suppresses speech & logs OutcomeDroppedBotCommand
	require.NoError(t, s.DispatchEvent(&chat.Event{
		ID:       "ev-hug-1",
		Platform: "twitch",
		Channel:  "streamerchan",
		Kind:     chat.EventKindMessage,
		UserID:   "u1",
		UserName: "alice",
		Text:     "!hug @bob",
	}))

	require.Eventually(t, func() bool {
		v, ok := s.BotEngine().Store().GetGlobalVar("last_hugger", false)
		return ok && v.Value == "alice:@bob"
	}, 2*time.Second, 20*time.Millisecond)

	entries, err := memLog.Query(context.Background(), eventlog.QueryFilter{Limit: 10})
	require.NoError(t, err)
	require.NotEmpty(t, entries.Entries)
	require.Equal(t, eventlog.OutcomeDroppedBotCommand, entries.Entries[0].Outcome)

	// Finalized Whisper transcript triggers voice command even when VoiceSubtitles overlay is disabled.
	s.SetSTTModelDir(t.TempDir())
	s.SetSTTFactories(
		func(_, _ string, _ int, _ bool) (stt.Engine, error) {
			return &mockSTTEngine{text: "please switch scene now", lang: "en"}, nil
		},
		func(segCfg stt.SegmenterConfig) (stt.Segmenter, error) {
			return &mockSTTSegmenter{cfg: segCfg}, nil
		},
	)
	s.syncVoiceSubtitlesSync(s.Config())
	s.processUtteranceSamples([]float32{0.1, 0.2, 0.3})

	require.Eventually(t, func() bool {
		v, ok := s.BotEngine().Store().GetGlobalVar("voice_hit", true)
		return ok && v.Value == "please switch scene now"
	}, 2*time.Second, 20*time.Millisecond)

	// Panic mute clears queues safely without error
	require.True(t, s.TogglePanic())
	require.True(t, s.IsPanicked())
	require.False(t, s.TogglePanic())

	// Master Voice Detection disable prevents Bot Voice Commands from firing while preserving rules
	require.NoError(t, s.UpdateConfig(func(c *config.Config) {
		c.SetVoiceDetectionEnabled(false)
	}))
	require.NoError(t, s.BotEngine().Store().DeleteGlobalVar("voice_hit", true))
	s.processUtteranceSamples([]float32{0.1, 0.2, 0.3})
	s.BotEngine().OnVoiceTranscript("please switch scene now")
	time.Sleep(50 * time.Millisecond)
	_, ok := s.BotEngine().Store().GetGlobalVar("voice_hit", true)
	require.False(t, ok, "voice command must not fire when VoiceDetection is disabled")
	require.True(t, s.Config().Bot.HasEnabledVoiceCommands(), "saved voice commands must be preserved")
}

func TestSessionDeclaredSessionVarsResetOnStreamStart(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Bot.Enabled = true
	cfg.Bot.Variables = []config.BotVariableDef{
		{Name: "globalTotal", Scope: "global", Kind: "int", DefaultValue: "100"},
		{Name: "sessionHits", Scope: "session", Kind: "int", DefaultValue: "0"},
	}

	s := NewSession(cfg, secrets.NewMemoryStore(), config.NewMemoryStore(cfg))
	defer func() { _ = s.Close() }()

	memLog, err := eventlog.NewSQLiteBackend(":memory:")
	require.NoError(t, err)
	logMgr := eventlog.NewManager(eventlog.RetentionPolicy{}, memLog)
	defer func() { _ = logMgr.Close() }()
	s.SetEventLogger(logMgr)

	require.NoError(t, s.Start())

	st := s.BotEngine().Store()
	require.NoError(t, st.SetGlobalVar("globalTotal", "int", "105", false))
	require.NoError(t, st.SetGlobalVar("sessionHits", "int", "7", true))

	// First platform going live starts a fresh stream session -> resets Session vars, preserves Global vars.
	s.UpdatePlatformStreamState("twitch", "streamer", true)
	gv, ok := st.GetGlobalVar("globalTotal", false)
	require.True(t, ok)
	require.Equal(t, "105", gv.Value)
	sv, ok := st.GetGlobalVar("sessionHits", true)
	require.True(t, ok)
	require.Equal(t, "0", sv.Value)

	// Mutate during live stream, then bring a 2nd platform live -> must NOT reset mid-stream.
	require.NoError(t, st.SetGlobalVar("sessionHits", "int", "4", true))
	s.UpdatePlatformStreamState("youtube", "ytchan", true)
	sv, _ = st.GetGlobalVar("sessionHits", true)
	require.Equal(t, "4", sv.Value)
}
