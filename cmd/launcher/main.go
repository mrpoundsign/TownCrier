package main

import (
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

//go:embed payload/*
var payloadFS embed.FS

const (
	AppVersion = "0.1.0"
	CoreExe    = "towncrier-core.exe"
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
	runtimeDir, err := getRuntimeDir()
	if err != nil {
		return 1, fmt.Errorf("resolve runtime dir: %w", err)
	}

	if err := extractPayloadIfNeeded(runtimeDir); err != nil {
		return 1, fmt.Errorf("extract runtime payload: %w", err)
	}

	coreExePath := filepath.Join(runtimeDir, CoreExe)
	if _, err := os.Stat(coreExePath); err != nil {
		return 1, fmt.Errorf("core executable not found at %s: %w", coreExePath, err)
	}

	cmd := exec.Command(coreExePath, os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Prepend runtime directory to PATH so DLLs are always resolved
	cmd.Env = append(os.Environ(), "PATH="+runtimeDir+";"+os.Getenv("PATH"))

	setSysProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		return 1, fmt.Errorf("start %s: %w", CoreExe, err)
	}

	// Trap SIGINT / SIGTERM so terminal detachment (Ctrl+C) exits the launcher cleanly
	// while allowing the GUI application to continue running independently on the desktop.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-sigChan:
		// Terminal interrupted: detach cleanly and let GUI app continue running
		return 0, nil
	case waitErr := <-done:
		if waitErr != nil {
			if exitErr, ok := errors.AsType[*exec.ExitError](waitErr); ok {
				return exitErr.ExitCode(), nil
			}
			return 1, waitErr
		}
		return 0, nil
	}
}

func getRuntimeDir() (string, error) {
	localApp := os.Getenv("LOCALAPPDATA")
	if localApp == "" {
		cacheDir, err := os.UserCacheDir()
		if err != nil {
			return filepath.Join(os.TempDir(), "com.github.mrpoundsign.towncrier", "app", AppVersion), nil
		}
		return filepath.Join(cacheDir, "com.github.mrpoundsign.towncrier", "app", AppVersion), nil
	}
	return filepath.Join(localApp, "com.github.mrpoundsign.towncrier", "app", AppVersion), nil
}

func extractPayloadIfNeeded(targetDir string) error {
	entries, err := fs.ReadDir(payloadFS, "payload")
	if err != nil {
		return fmt.Errorf("read embedded payload: %w", err)
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("create runtime directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".txt") {
			continue
		}

		targetPath := filepath.Join(targetDir, name)

		data, err := payloadFS.ReadFile("payload/" + name)
		if err != nil {
			return fmt.Errorf("read embedded file %s: %w", name, err)
		}

		// Check if file already exists with same size and hash
		if matchesExisting(targetPath, data) {
			continue
		}

		// Write to temp file then rename atomically
		tmpPath := targetPath + ".tmp"
		if err := os.WriteFile(tmpPath, data, 0755); err != nil {
			return fmt.Errorf("write %s: %w", tmpPath, err)
		}

		_ = os.Remove(targetPath)
		if err := os.Rename(tmpPath, targetPath); err != nil {
			// If rename fails (e.g. file in use or cross-device), try direct write
			_ = os.Remove(tmpPath)
			if err := os.WriteFile(targetPath, data, 0755); err != nil {
				return fmt.Errorf("replace %s: %w", targetPath, err)
			}
		}
	}

	return nil
}

func matchesExisting(path string, expectedData []byte) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() != int64(len(expectedData)) {
		return false
	}

	existing, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	h1 := sha256.Sum256(expectedData)
	h2 := sha256.Sum256(existing)
	return h1 == h2
}
