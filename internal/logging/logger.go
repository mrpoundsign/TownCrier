package logging

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
)

var (
	mu        sync.Mutex
	logFile   *os.File
	localFile *os.File
	logPath   string
)

type safeWriter struct {
	primary *os.File
	local   *os.File
}

func (s *safeWriter) Write(p []byte) (int, error) {
	_, _ = os.Stderr.Write(p)
	if s.local != nil {
		_, _ = s.local.Write(p)
	}
	return s.primary.Write(p)
}

// Init initializes application logging to a persistent log file and stderr.
// If customPath is empty, it also mirrors to a local octochorpebot.log if writable.
func Init(customPath string) (string, error) {
	mu.Lock()
	defer mu.Unlock()

	if logFile != nil {
		return logPath, nil
	}

	target := customPath
	isDefault := false
	if target == "" {
		isDefault = true
		configDir, err := os.UserConfigDir()
		if err != nil {
			configDir = "."
		}
		target = filepath.Join(configDir, "OctochorpeBot", "logs", "octochorpebot.log")
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("create log dir: %w", err)
	}

	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", fmt.Errorf("open log file: %w", err)
	}

	var localF *os.File
	if isDefault {
		localPath, _ := filepath.Abs("octochorpebot.log")
		absTarget, _ := filepath.Abs(target)
		if localPath != absTarget {
			localF, _ = os.OpenFile("octochorpebot.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		}
	}

	logFile = f
	localFile = localF
	logPath = target

	log.SetOutput(&safeWriter{primary: f, local: localF})
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)

	return target, nil
}

// LogPath returns the active log file path, if initialized.
func LogPath() string {
	mu.Lock()
	defer mu.Unlock()
	return logPath
}

// Sync flushes pending buffered log writes to disk.
func Sync() {
	mu.Lock()
	defer mu.Unlock()
	if logFile != nil {
		_ = logFile.Sync()
	}
	if localFile != nil {
		_ = localFile.Sync()
	}
}

// Close closes the active log file(s).
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if logFile != nil {
		_ = logFile.Sync()
		_ = logFile.Close()
		logFile = nil
		logPath = ""
	}
	if localFile != nil {
		_ = localFile.Sync()
		_ = localFile.Close()
		localFile = nil
	}
}

// RecoverAndLog catches panics, writes a full stack trace to the log file,
// flushes the log, and optionally re-panics.
func RecoverAndLog(context string, rethrow bool) {
	if r := recover(); r != nil {
		stack := debug.Stack()
		log.Printf("FATAL PANIC [%s]: %v\nStack trace:\n%s", context, r, string(stack))
		Sync()
		if rethrow {
			panic(r)
		}
	}
}

// RecoverAndExit catches panics, writes a full stack trace to the log file,
// flushes the log, and exits with code 1.
func RecoverAndExit(context string) {
	if r := recover(); r != nil {
		stack := debug.Stack()
		log.Printf("FATAL PANIC [%s]: %v\nStack trace:\n%s", context, r, string(stack))
		Close()
		os.Exit(1)
	}
}
