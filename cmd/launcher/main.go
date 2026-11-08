package main

import (
	"embed"
	"fmt"
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
