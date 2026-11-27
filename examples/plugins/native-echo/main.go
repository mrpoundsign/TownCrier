package main

import (
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/mrpoundsign/TownCrier/pkg/pluginsdk"
	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

const nativeEchoUUID = "com.github.mrpoundsign.towncrier.examples.native-echo"

type nativeEchoPlugin struct {
	pluginsdk.BasePlugin
	mu   sync.Mutex
	host *pluginsdk.Host
}

func (p *nativeEchoPlugin) Manifest() *pluginv1.Manifest {
	return &pluginv1.Manifest{
		Uuid:                 nativeEchoUUID,
		Name:                 "Native Echo",
		Version:              "1.0.0",
		Author:               "TownCrier",
		Description:          "Reference native subprocess plugin demonstrating authenticated loopback TCP Protobuf IPC, stdout log capture, KV, and overlay broadcast.",
		ApiVersion:           &pluginv1.ProtocolVersion{Major: 1, Minor: 0, Patch: 0, MinHostMinor: 0},
		RequiredCapabilities: []string{"core.v1", "events.v1", "kv.v1", "http.v1", "overlay.v1"},
		SubscribedEvents:     []string{"message"},
		Permissions: &pluginv1.Permissions{
			ReadEvents:       true,
			KvStorage:        true,
			OverlayBroadcast: true,
			AllowedHosts:     []string{"127.0.0.1:*"},
		},
	}
}

func (p *nativeEchoPlugin) Init(h *pluginsdk.Host) error {
	p.mu.Lock()
	p.host = h
	p.mu.Unlock()
	// Intentional stdout output: proves loopback TCP framing stays isolated from stdout chatter.
	fmt.Println("native-echo stdout diagnostic ready")
	h.Logf("Native Echo initialized")
	return nil
}

func (p *nativeEchoPlugin) OnEvent(ev *pluginv1.StreamEvent) error {
	p.mu.Lock()
	h := p.host
	p.mu.Unlock()
	if h == nil || ev == nil {
		return nil
	}
	if err := h.KVSet("last_message", []byte(ev.GetText())); err != nil {
		return err
	}
	return h.OverlayBroadcast("native_echo", fmt.Sprintf(`{"user":%q,"text":%q}`, ev.GetUserName(), ev.GetText()))
}

func (p *nativeEchoPlugin) OnConfigAction(actionKey string, settings *pluginv1.SettingsMap) error {
	p.mu.Lock()
	h := p.host
	p.mu.Unlock()
	if h == nil {
		return nil
	}
	url := pluginsdk.GetSettingString(settings, "ping_url", "")
	if actionKey == "ping" && url != "" {
		return h.HTTPFetchAsync("ping-1", &pluginv1.HTTPRequest{
			Method: "GET",
			Url:    url,
		})
	}
	return nil
}

func (p *nativeEchoPlugin) OnHTTPResult(asyncID string, resp *pluginv1.HTTPResponse) error {
	p.mu.Lock()
	h := p.host
	p.mu.Unlock()
	if h != nil && resp != nil {
		_ = h.KVSet("http_"+asyncID, resp.GetBody())
		_ = h.OverlayBroadcast("http_result", fmt.Sprintf(`{"id":%q,"status":%d,"body":%q}`, asyncID, resp.GetStatus(), string(resp.GetBody())))
	}
	return nil
}

func main() {
	pluginsdk.Register(&nativeEchoPlugin{})
	if err := pluginsdk.Run(); err != nil {
		log.Printf("native-echo exiting: %v", err)
		os.Exit(1)
	}
}
