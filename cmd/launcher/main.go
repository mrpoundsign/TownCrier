package main

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"io"
	"os"

	"github.com/mrpoundsign/appinstaller/pkg/launcher"
)

//go:embed payload/*
var payloadFS embed.FS

var (
	AppVersion = "0.0.8"
)

const (
	AppID   = "com.github.mrpoundsign.towncrier"
	CoreExe = "towncrier-core"
)

func main() {
	exitCode, err := run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "TownCrier launcher error: %v\n", err)
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

func run() (int, error) {
	return launcher.Run(launcher.Config{
		AppID:          AppID,
		AppVersion:     AppVersion,
		CoreBinaryName: CoreExe,
		PayloadFS:      payloadFS,
		PayloadSubdir:  "payload",
	})
}

func getRuntimeDir() (string, error) {
	return launcher.GetRuntimeDir(AppID, AppVersion)
}

func extractPayloadIfNeeded(targetDir string) error {
	return launcher.ExtractPayloadIfNeeded(payloadFS, "payload", targetDir)
}

func cleanupOldVersions(baseDir, currentVersion string) error {
	return launcher.CleanupOldVersions(baseDir, currentVersion)
}

func matchesExisting(path string, expectedData []byte) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() != int64(len(expectedData)) {
		return false
	}

	existingFile, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = existingFile.Close() }()

	h1 := sha256.Sum256(expectedData)
	h2 := sha256.New()
	if _, err := io.Copy(h2, existingFile); err != nil {
		return false
	}
	var sum2 [sha256.Size]byte
	copy(sum2[:], h2.Sum(nil))
	return h1 == sum2
}
