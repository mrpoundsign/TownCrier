package pluginhost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
	"github.com/mrpoundsign/TownCrier/pkg/secrets"
)

func TestCheckVersionAndCapabilities(t *testing.T) {
	require.Nil(t, CheckVersion(1, 0))

	errZero := CheckVersion(0, 0)
	require.NotNil(t, errZero)
	require.Equal(t, pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_API_VERSION, errZero.GetCode())

	errMajor := CheckVersion(2, 0)
	require.NotNil(t, errMajor)
	require.Equal(t, pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_API_VERSION, errMajor.GetCode())
	require.Contains(t, errMajor.GetMessage(), "update TownCrier")

	errMinor := CheckVersion(1, 99)
	require.NotNil(t, errMinor)
	require.Equal(t, pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_API_VERSION, errMinor.GetCode())
	require.Contains(t, errMinor.GetMessage(), "v1.99+")

	require.Nil(t, CheckCapabilities([]string{"core.v1", "kv.v1"}))
	errCap := CheckCapabilities([]string{"core.v1", "future.v9"})
	require.NotNil(t, errCap)
	require.Equal(t, pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_CAPABILITY, errCap.GetCode())
	require.Equal(t, "future.v9", errCap.GetMissingCapability())
}

func TestKVStoreLifecycle(t *testing.T) {
	kv := NewKVStore(":memory:")
	defer func() { _ = kv.Close() }()
	ctx := context.Background()

	_, found, apiErr := kv.Get(ctx, "com.example.p", "count")
	require.Nil(t, apiErr)
	require.False(t, found)

	require.Nil(t, kv.Set(ctx, "com.example.p", "count", []byte("42")))
	require.Nil(t, kv.Set(ctx, "com.example.p", "user:alice", []byte("10")))
	require.Nil(t, kv.Set(ctx, "com.example.other", "count", []byte("99")))

	val, found, apiErr := kv.Get(ctx, "com.example.p", "count")
	require.Nil(t, apiErr)
	require.True(t, found)
	require.Equal(t, []byte("42"), val)

	keys, apiErr := kv.List(ctx, "com.example.p", "user:", 0)
	require.Nil(t, apiErr)
	require.Equal(t, []string{"user:alice"}, keys)

	require.Nil(t, kv.Delete(ctx, "com.example.p", "count"))
	_, found, apiErr = kv.Get(ctx, "com.example.p", "count")
	require.Nil(t, apiErr)
	require.False(t, found)

	// Other plugin namespace untouched
	valOther, foundOther, apiErr := kv.Get(ctx, "com.example.other", "count")
	require.Nil(t, apiErr)
	require.True(t, foundOther)
	require.Equal(t, []byte("99"), valOther)
}

func TestIsHostAllowedAndExecuteHTTP(t *testing.T) {
	require.True(t, IsHostAllowed("https://api.twitch.tv/helix/users", []string{"api.twitch.tv"}))
	require.True(t, IsHostAllowed("https://sub.example.com/path", []string{"*.example.com"}))
	require.False(t, IsHostAllowed("https://example.com/path", []string{"*.example.com"}))
	require.False(t, IsHostAllowed("http://user:pass@api.twitch.tv/", []string{"api.twitch.tv"}))
	require.False(t, IsHostAllowed("file:///etc/passwd", []string{"*"}))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "ok")
		_, _ = w.Write([]byte("pong"))
	}))
	defer srv.Close()

	_, apiErr := ExecuteHTTP(context.Background(), srv.Client(), []string{"example.com"}, true, &pluginv1.HTTPRequest{
		Method: "GET",
		Url:    srv.URL,
	})
	require.NotNil(t, apiErr)
	require.Equal(t, pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, apiErr.GetCode())

	resp, apiErr := ExecuteHTTP(context.Background(), srv.Client(), []string{"127.0.0.1:*"}, true, &pluginv1.HTTPRequest{
		Method: "GET",
		Url:    srv.URL,
	})
	require.Nil(t, apiErr)
	require.Equal(t, int32(200), resp.GetStatus())
	require.Equal(t, []byte("pong"), resp.GetBody())
	require.Equal(t, "ok", resp.GetHeaders()["X-Test"])
}

func TestSettingsKeyringRouting(t *testing.T) {
	cfg := config.DefaultConfig()
	sec := secrets.NewMemoryStore()
	mgr := NewManager(Options{
		KVDBPath: ":memory:",
		Secrets:  sec,
		Callbacks: HostCallbacks{
			GetConfig: func() *config.Config { return cfg.Clone() },
			UpdateConfig: func(fn func(c *config.Config)) error {
				fn(cfg)
				return nil
			},
		},
	})
	defer func() { _ = mgr.Close() }()

	uuid := "com.example.secret"
	mgr.entries[uuid] = &pluginEntry{
		uuid: uuid,
		manifest: &pluginv1.Manifest{
			Uuid: uuid,
			Name: "Secret Plugin",
			GlobalSettingsSchema: []*pluginv1.SettingField{
				{Key: "greeting", Type: pluginv1.SettingFieldType_SETTING_FIELD_TYPE_TEXT},
				{Key: "apiKey", Type: pluginv1.SettingFieldType_SETTING_FIELD_TYPE_PASSWORD},
			},
		},
	}

	require.NoError(t, mgr.SaveGlobalSettingsFromUI(uuid, map[string]any{
		"greeting": "hi",
		"apiKey":   "super-secret",
	}, nil))

	// Secret must NOT live in config.json map, MUST live in secrets.Store
	_, inConfig := cfg.Plugins.PluginSettings[uuid]["apiKey"]
	require.False(t, inConfig)
	require.Equal(t, "hi", cfg.Plugins.PluginSettings[uuid]["greeting"])
	require.True(t, mgr.HasSecret(uuid, "apiKey"))

	merged := mgr.GetGlobalSettings(uuid)
	require.Equal(t, "hi", merged.GetValues()["greeting"].GetStringVal())
	require.Equal(t, "super-secret", merged.GetValues()["apiKey"].GetStringVal())

	// Omitting password preserves existing secret; explicit clear removes it
	require.NoError(t, mgr.SaveGlobalSettingsFromUI(uuid, map[string]any{
		"greeting": "yo",
	}, nil))
	require.True(t, mgr.HasSecret(uuid, "apiKey"))

	require.NoError(t, mgr.SaveGlobalSettingsFromUI(uuid, map[string]any{
		"greeting": "yo",
	}, []string{"apiKey"}))
	require.False(t, mgr.HasSecret(uuid, "apiKey"))
}

func TestNativeTrustGateAndTamperDetection(t *testing.T) {
	baseDir := t.TempDir()
	plugDir := filepath.Join(baseDir, "native", "demo")
	require.NoError(t, os.MkdirAll(plugDir, 0o755))

	binName := "demo.bin"
	binPath := filepath.Join(plugDir, binName)
	require.NoError(t, os.WriteFile(binPath, []byte("binary-v1"), 0o755))

	manifestJSON := `{
		"uuid": "com.example.native.demo",
		"name": "Demo Native",
		"version": "1.0.0",
		"binaries": {
			"linux/amd64": "demo.bin",
			"windows/amd64": "demo.bin",
			"darwin/arm64": "demo.bin",
			"darwin/amd64": "demo.bin"
		}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(plugDir, "plugin.json"), []byte(manifestJSON), 0o644))

	cfg := config.DefaultConfig()
	mgr := NewManager(Options{
		BaseDir:  baseDir,
		KVDBPath: ":memory:",
		Secrets:  secrets.NewMemoryStore(),
		Callbacks: HostCallbacks{
			GetConfig: func() *config.Config { return cfg.Clone() },
			UpdateConfig: func(fn func(c *config.Config)) error {
				fn(cfg)
				return nil
			},
		},
	})
	defer func() { _ = mgr.Close() }()

	require.NoError(t, mgr.Start())
	list := mgr.Plugins()
	require.Len(t, list, 1)
	require.Equal(t, StatusAwaitingTrust, list[0].State)
	origHash := list[0].BinarySHA256
	require.NotEmpty(t, origHash)

	// Record trust manually for origHash, then tamper with binary -> must revert to StatusAwaitingTrust
	cfg.Plugins.TrustedNative["com.example.native.demo"] = config.TrustedNativePlugin{SHA256: origHash}
	require.NoError(t, os.WriteFile(binPath, []byte("binary-tampered"), 0o755))
	require.NoError(t, mgr.Reload())
	list = mgr.Plugins()
	require.Len(t, list, 1)
	require.Equal(t, StatusAwaitingTrust, list[0].State)
	require.NotEqual(t, origHash, list[0].BinarySHA256)
}
