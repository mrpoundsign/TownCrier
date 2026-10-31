package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mrpoundsign/appinstaller/pkg/installer"
	"github.com/mrpoundsign/appinstaller/pkg/installerui"

	"github.com/mrpoundsign/TownCrier/assets"
	"github.com/mrpoundsign/TownCrier/internal/tts"
)

//go:embed payload/*
var payloadFS embed.FS

var (
	AppVersion = "0.0.7"
)

func main() {
	spec := buildAppSpec()
	os.Exit(installerui.RunMain(spec, assets.AppIcon))
}

func buildAppSpec() installer.AppSpec {
	return installer.AppSpec{
		AppName:       "TownCrier",
		AppID:         "com.github.mrpoundsign.towncrier",
		Description:   "Open-source local neural TTS livestream chat & alert reader",
		Publisher:     "mrpoundsign",
		Version:       AppVersion,
		RepoOwner:     "mrpoundsign",
		RepoName:      "TownCrier",
		BinaryName:    "TownCrier.exe",
		PayloadFS:     payloadFS,
		PayloadSubdir: "payload",
		FileRenames: map[string]string{
			"towncrier-core.exe": "TownCrier.exe",
		},
		DataDirsToPurge: func() []string {
			var dirs []string
			if cfgDir, err := os.UserConfigDir(); err == nil && cfgDir != "" {
				dirs = append(dirs, filepath.Join(cfgDir, "com.github.mrpoundsign.towncrier"))
			}
			if localApp := os.Getenv("LOCALAPPDATA"); localApp != "" {
				dirs = append(dirs, filepath.Join(localApp, "com.github.mrpoundsign.towncrier"))
			} else if cacheDir, err := os.UserCacheDir(); err == nil && cacheDir != "" {
				dirs = append(dirs, filepath.Join(cacheDir, "com.github.mrpoundsign.towncrier"))
			}
			return dirs
		},
		OptionalComponents: []installer.ComponentSpec{
			{
				ID:          "supertonic",
				Title:       "Pre-download Supertonic TTS Model Pack",
				Description: "Fast & lean neural voices (~265 MB, recommended default)",
				DefaultOn:   true,
				Run: func(_ context.Context, _ string, progress func(cur, total int64, status string)) error {
					modelDir := tts.DefaultModelDir()
					if tts.IsModelFamilyInstalled(modelDir, tts.ModelSupertonic, false) {
						progress(1, 1, "Supertonic TTS models already installed.")
						return nil
					}
					return tts.DownloadModelFamily(modelDir, tts.ModelSupertonic, false, func(cur, total int64) {
						curMB := float64(cur) / (1024 * 1024)
						totMB := float64(total) / (1024 * 1024)
						progress(cur, total, fmt.Sprintf("Downloading Supertonic TTS models... %.1f / %.1f MB", curMB, totMB))
					})
				},
			},
			{
				ID:          "kokoro",
				Title:       "Pre-download Kokoro-82M English TTS Model Pack",
				Description: "Expressive English neural voices (~134 MB CPU)",
				DefaultOn:   false,
				Run: func(_ context.Context, _ string, progress func(cur, total int64, status string)) error {
					modelDir := tts.DefaultModelDir()
					if tts.IsKokoroInstalledForMode(modelDir, false) {
						progress(1, 1, "Kokoro-82M TTS models already installed.")
						return nil
					}
					return tts.DownloadKokoroModelForMode(modelDir, false, func(cur, total int64) {
						curMB := float64(cur) / (1024 * 1024)
						totMB := float64(total) / (1024 * 1024)
						progress(cur, total, fmt.Sprintf("Downloading Kokoro-82M TTS models... %.1f / %.1f MB", curMB, totMB))
					})
				},
			},
		},
	}
}
