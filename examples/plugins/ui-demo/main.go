package main

import (
	"fmt"

	"github.com/mrpoundsign/TownCrier/pkg/pluginsdk"
	"github.com/mrpoundsign/TownCrier/pkg/pluginsdk/ui"
	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

type demoUIPlugin struct {
	pluginsdk.BasePlugin
	host  *pluginsdk.Host
	count int
	rev   uint64
}

func (p *demoUIPlugin) Manifest() *pluginv1.Manifest {
	return &pluginv1.Manifest{
		Uuid:                 "com.example.uidemo",
		Name:                 "UI Demo",
		Version:              "1.0.0",
		ApiVersion:           &pluginv1.ProtocolVersion{Major: 1, Minor: 0, Patch: 0},
		RequiredCapabilities: []string{"core.v1", "ui.v1", "overlay.v1", "overlay.routes.v1"},
		Permissions: &pluginv1.Permissions{
			UiViews:          true,
			OverlayBroadcast: true,
		},
		Views: []*pluginv1.ViewDescriptor{
			{Id: "main", Title: "Demo", IconName: "music", Placement: pluginv1.ViewPlacement_VIEW_PLACEMENT_MAIN_TAB},
		},
	}
}

func (p *demoUIPlugin) Init(h *pluginsdk.Host) error {
	p.host = h
	p.rev = 1
	return nil
}

func (p *demoUIPlugin) RenderView(viewID string) (*pluginv1.UISnapshot, error) {
	if p.rev == 0 {
		p.rev = 1
	}
	return ui.Snapshot(viewID, p.rev, ui.VBox("root",
		ui.Text("lbl", fmt.Sprintf("Count: %d", p.count)),
		ui.Button("inc", "Increment"),
	)), nil
}

func (p *demoUIPlugin) OnUIEvent(ev *pluginv1.UIEvent) (*pluginv1.UIPatchBatch, error) {
	if ev.GetNodeId() == "inc" {
		base := p.rev
		p.count++
		p.rev++
		batch := ui.PatchBatch("main", base, p.rev, ui.SetText("lbl", fmt.Sprintf("Count: %d", p.count)))
		if p.host != nil {
			_ = p.host.SendUIPatch(batch)
		}
		return batch, nil
	}
	return nil, nil
}

func (p *demoUIPlugin) GetOverlayAsset(assetPath string) (string, []byte, bool, error) {
	if assetPath == "index.html" {
		return "text/html; charset=utf-8", []byte("<h1>Demo</h1>"), true, nil
	}
	return "", nil, false, nil
}

func main() {
	pluginsdk.Register(&demoUIPlugin{})
	_ = pluginsdk.Run()
}
