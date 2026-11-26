package ui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"fyne.io/fyne/v2/test"
	"github.com/stretchr/testify/require"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/pkg/audio"
	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/secrets"
)

func TestSettingsPluginsTabVisibilityAndTrustRefresh(t *testing.T) {
	fyneApp := test.NewApp()
	defer fyneApp.Quit()
	w := fyneApp.NewWindow("TestPluginsTab")

	tmpDir := t.TempDir()
	loc := config.TestLocations(tmpDir)
	store, err := config.NewFileStore(loc)
	require.NoError(t, err)

	cfg := config.DefaultConfig()
	sess := app.NewSession(cfg, secrets.NewMemoryStore(), store)
	defer func() { _ = sess.Close() }()

	// Seed an untrusted native plugin in isolated tempDir/plugins/native
	plugDir := filepath.Join(tmpDir, "plugins", "native", "testplug")
	require.NoError(t, os.MkdirAll(plugDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(plugDir, "testplug.bin"), []byte("dummy"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(plugDir, "plugin.json"), []byte(`{
		"uuid": "com.example.ui.native",
		"name": "UI Native Test",
		"version": "1.0.0",
		"binaries": {
			"linux/amd64": "testplug.bin",
			"windows/amd64": "testplug.bin",
			"darwin/arm64": "testplug.bin",
			"darwin/amd64": "testplug.bin"
		}
	}`), 0o644))
	require.NoError(t, sess.PluginManager().Start())

	view := NewSettingsView(w, cfg, nil, nil)
	view.SetSession(sess)
	view.SetDeviceEnumerator(func() ([]audio.DeviceInfo, error) {
		return []audio.DeviceInfo{{ID: "spk1", Name: "Mock Speakers", IsDefault: true}}, nil
	})
	_ = view.Build()
	defer func() {
		if view.pluginsTab != nil {
			view.pluginsTab.Close()
		}
	}()

	require.True(t, slices.Contains(view.subTabs.Items, view.pluginsTabItem), "expected Plugins sub-tab to always be visible")
	require.NotNil(t, view.pluginsTab)
	require.Len(t, view.pluginsTab.cardsBox.Objects, 1)
}
