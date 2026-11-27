package app

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/secrets"
)

func TestSessionPluginManagerIsolationAndLifecycle(t *testing.T) {
	memStore := config.NewMemoryStore(config.DefaultConfig())
	sess := NewSession(config.DefaultConfig(), secrets.NewMemoryStore(), memStore)
	defer func() { _ = sess.Close() }()

	mgr := sess.PluginManager()
	require.NotNil(t, mgr)
	require.Empty(t, mgr.BaseDir())

	require.NoError(t, sess.Start())
	require.Empty(t, mgr.Plugins())
	require.Equal(t, int64(0), sess.EffectiveStreamSessionID())

	// Going live without EventLogger still assigns an ephemeral EffectiveStreamSessionID until offline.
	sess.UpdatePlatformStreamState("twitch", "testchan", true)
	require.True(t, sess.IsStreamLive())
	require.Greater(t, sess.EffectiveStreamSessionID(), int64(0))

	sess.UpdatePlatformStreamState("twitch", "testchan", false)
	require.False(t, sess.IsStreamLive())
	require.Equal(t, int64(0), sess.EffectiveStreamSessionID())

	require.NoError(t, sess.Stop())

	// Explicit TestLocations uses isolated temp dir, never production %APPDATA%
	loc := config.TestLocations(t.TempDir())
	fileStore, err := config.NewFileStore(loc)
	require.NoError(t, err)
	sess2 := NewSession(config.DefaultConfig(), secrets.NewMemoryStore(), fileStore)
	defer func() { _ = sess2.Close() }()
	require.NotEmpty(t, sess2.PluginManager().BaseDir())
}

func TestSessionPluginOverlayStylePersistence(t *testing.T) {
	cfg := config.DefaultConfig()
	memStore := config.NewMemoryStore(cfg)
	sess := NewSession(cfg, secrets.NewMemoryStore(), memStore)
	defer func() { _ = sess.Close() }()

	require.Empty(t, sess.ListPluginOverlayWidgets())

	err := sess.SavePluginOverlayStyle("com.example.dj", "now_playing", config.PluginOverlayStyle{
		Values:       map[string]any{"accent_color": "#38BDF8", "bg_opacity": 85.0},
		CustomCSS:    ".card { opacity: 0.9; }",
		UseHostTheme: true,
	})
	require.NoError(t, err)

	got := sess.GetPluginOverlayStyle("com.example.dj", "now_playing")
	require.True(t, got.UseHostTheme)
	require.Equal(t, ".card { opacity: 0.9; }", got.CustomCSS)
	require.Equal(t, "#38BDF8", got.Values["accent_color"])
	require.Equal(t, 85.0, got.Values["bg_opacity"])
}
