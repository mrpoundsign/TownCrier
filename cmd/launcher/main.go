package main

import (
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
)

//go:embed payload/*
var payloadFS embed.FS

var (
	AppVersion = "0.0.5"
)

const (
	CoreExe = "towncrier-core.exe"
)

func coreExeName() string {
	if runtime.GOOS == "windows" {
		return CoreExe
	}
	return "towncrier-core"
}

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

	// Release any extraction buffers back to the OS before waiting on the child GUI process
	runtime.GC()
	debug.FreeOSMemory()

	exeName := coreExeName()
	coreExePath := filepath.Join(runtimeDir, exeName)
	if _, err := os.Stat(coreExePath); err != nil {
		return 1, fmt.Errorf("core executable not found at %s: %w", coreExePath, err)
	}

	cmd := exec.Command(coreExePath, os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Prepend runtime directory to PATH / LD_LIBRARY_PATH so shared libraries are always resolved
	if runtime.GOOS == "windows" {
		cmd.Env = append(os.Environ(), "PATH="+runtimeDir+";"+os.Getenv("PATH"))
	} else {
		ldPath := runtimeDir
		if existing := os.Getenv("LD_LIBRARY_PATH"); existing != "" {
			ldPath = runtimeDir + ":" + existing
		}
		cmd.Env = append(os.Environ(),
			"PATH="+runtimeDir+":"+os.Getenv("PATH"),
			"LD_LIBRARY_PATH="+ldPath,
		)
	}

	setSysProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		return 1, fmt.Errorf("start %s: %w", exeName, err)
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
		embeddedPath := "payload/" + name

		// Check if file already exists with same size and SHA-256 hash using streaming I/O
		if matchesExistingStream(targetPath, embeddedPath) {
			continue
		}

		if err := extractSingleFile(embeddedPath, targetPath); err != nil {
			return err
		}
	}

	return nil
}

func extractSingleFile(embeddedPath, targetPath string) error {
	src, err := payloadFS.Open(embeddedPath)
	if err != nil {
		return fmt.Errorf("open embedded file %s: %w", embeddedPath, err)
	}
	defer func() { _ = src.Close() }()

	tmpPath := targetPath + ".tmp"
	dst, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmpPath, err)
	}

	if _, copyErr := io.Copy(dst, src); copyErr != nil {
		_ = dst.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write %s: %w", tmpPath, copyErr)
	}
	if closeErr := dst.Close(); closeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close %s: %w", tmpPath, closeErr)
	}

	_ = os.Remove(targetPath)
	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		// Fallback direct copy if rename fails
		if _, seekErr := src.(io.ReadSeeker).Seek(0, io.SeekStart); seekErr != nil {
			return fmt.Errorf("rename %s: %w", targetPath, err)
		}
		directDst, openErr := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if openErr != nil {
			return fmt.Errorf("replace %s: %w", targetPath, openErr)
		}
		_, copyErr := io.Copy(directDst, src)
		_ = directDst.Close()
		if copyErr != nil {
			return fmt.Errorf("replace %s: %w", targetPath, copyErr)
		}
	}
	return nil
}

func matchesExistingStream(targetPath, embeddedPath string) bool {
	targetInfo, err := os.Stat(targetPath)
	if err != nil {
		return false
	}

	embFile, err := payloadFS.Open(embeddedPath)
	if err != nil {
		return false
	}
	defer func() { _ = embFile.Close() }()

	embInfo, err := embFile.Stat()
	if err != nil || targetInfo.Size() != embInfo.Size() {
		return false
	}

	existingFile, err := os.Open(targetPath)
	if err != nil {
		return false
	}
	defer func() { _ = existingFile.Close() }()

	h1 := sha256.New()
	if _, err := io.Copy(h1, embFile); err != nil {
		return false
	}

	h2 := sha256.New()
	if _, err := io.Copy(h2, existingFile); err != nil {
		return false
	}

	return string(h1.Sum(nil)) == string(h2.Sum(nil))
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

