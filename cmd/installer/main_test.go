package main

import (
	"context"
	"testing"

	"github.com/mrpoundsign/TownCrier/internal/streamdeck"
)

func TestBuildAppSpecStreamDeckComponent(t *testing.T) {
	tmpDir := t.TempDir()
	prev := streamDeckPluginsDirOverride
	streamDeckPluginsDirOverride = tmpDir
	defer func() { streamDeckPluginsDirOverride = prev }()

	spec := buildAppSpec()
	var found bool
	for _, comp := range spec.OptionalComponents {
		if comp.ID == "streamdeck_plugin" {
			found = true
			if err := comp.Run(context.Background(), t.TempDir(), func(_, _ int64, _ string) {}); err != nil {
				t.Fatalf("streamdeck_plugin component Run failed: %v", err)
			}
		}
	}
	if !found {
		t.Fatal("expected streamdeck_plugin component in buildAppSpec()")
	}
	st := streamdeck.CheckPluginStatus(tmpDir)
	if !st.Installed || st.NeedsUpdate {
		t.Fatalf("expected Stream Deck plugin installed and up-to-date, got %+v", st)
	}
}
