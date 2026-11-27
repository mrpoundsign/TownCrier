package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mrpoundsign/TownCrier/internal/poundcontrol"
	"github.com/mrpoundsign/TownCrier/internal/streamdeck"
)

func TestBuildAppSpecStreamDeckAndPoundControlComponents(t *testing.T) {
	tmpRoot := t.TempDir()
	sdDir := filepath.Join(tmpRoot, "Elgato", "StreamDeck", "Plugins")
	pcWasmDir := filepath.Join(tmpRoot, "PoundControl", "wasm")
	installDir := filepath.Join(tmpRoot, "Programs", "TownCrier")

	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sidecarWasm := filepath.Join(installDir, poundcontrol.ReleaseAssetName)
	if err := os.WriteFile(sidecarWasm, []byte("mock-wasm-binary"), 0o600); err != nil {
		t.Fatal(err)
	}

	prevSD := streamDeckPluginsDirOverride
	prevPC := poundControlWasmDirOverride
	streamDeckPluginsDirOverride = sdDir
	poundControlWasmDirOverride = pcWasmDir
	defer func() {
		streamDeckPluginsDirOverride = prevSD
		poundControlWasmDirOverride = prevPC
	}()

	// Before target directories exist, DefaultOn should be false for both optional integrations.
	specBefore := buildAppSpec()
	for _, comp := range specBefore.OptionalComponents {
		if comp.ID == "streamdeck_plugin" || comp.ID == "poundcontrol_plugin" {
			if comp.DefaultOn {
				t.Fatalf("expected %s DefaultOn=false when companion app directory does not exist", comp.ID)
			}
			if !comp.RunOnUpdate {
				t.Fatalf("expected %s RunOnUpdate=true", comp.ID)
			}
		}
	}

	// Simulate existing Elgato and PoundControl config folders.
	if err := os.MkdirAll(filepath.Dir(sdDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(pcWasmDir), 0o755); err != nil {
		t.Fatal(err)
	}

	spec := buildAppSpec()
	var foundSD, foundPC bool
	for _, comp := range spec.OptionalComponents {
		switch comp.ID {
		case "streamdeck_plugin":
			foundSD = true
			if !comp.DefaultOn {
				t.Fatal("expected streamdeck_plugin DefaultOn=true when Elgato folder exists")
			}
			if comp.IsInstalled != nil && comp.IsInstalled(installDir) {
				t.Fatal("expected streamdeck_plugin IsInstalled=false before Run")
			}
			if err := comp.Run(context.Background(), installDir, func(_, _ int64, _ string) {}); err != nil {
				t.Fatalf("streamdeck_plugin Run failed: %v", err)
			}
			if comp.IsInstalled == nil || !comp.IsInstalled(installDir) {
				t.Fatal("expected streamdeck_plugin IsInstalled=true after Run")
			}
		case "poundcontrol_plugin":
			foundPC = true
			if !comp.DefaultOn {
				t.Fatal("expected poundcontrol_plugin DefaultOn=true when PoundControl folder exists")
			}
			if comp.IsInstalled != nil && comp.IsInstalled(installDir) {
				t.Fatal("expected poundcontrol_plugin IsInstalled=false before Run")
			}
			if err := comp.Run(context.Background(), installDir, func(_, _ int64, _ string) {}); err != nil {
				t.Fatalf("poundcontrol_plugin Run failed: %v", err)
			}
			if comp.IsInstalled == nil || !comp.IsInstalled(installDir) {
				t.Fatal("expected poundcontrol_plugin IsInstalled=true after Run")
			}
		}
	}
	if !foundSD || !foundPC {
		t.Fatalf("expected both components in buildAppSpec() (sd=%v, pc=%v)", foundSD, foundPC)
	}

	sdSt := streamdeck.CheckPluginStatus(sdDir)
	if !sdSt.Installed || sdSt.NeedsUpdate {
		t.Fatalf("expected Stream Deck plugin installed and up-to-date, got %+v", sdSt)
	}

	pcSt := poundcontrol.CheckPluginStatus(pcWasmDir, AppVersion)
	if !pcSt.Installed || pcSt.NeedsUpdate {
		t.Fatalf("expected PoundControl plugin installed and up-to-date, got %+v", pcSt)
	}
}
