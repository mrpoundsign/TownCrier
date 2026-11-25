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
