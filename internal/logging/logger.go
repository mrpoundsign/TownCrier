package logging

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
)

// DefaultTailMaxBytes is the maximum number of trailing bytes read by ReadTail when maxBytes <= 0.
const DefaultTailMaxBytes int64 = 64 * 1024

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
// If customPath is empty, it also mirrors to a local towncrier.log if writable.
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
		target = filepath.Join(configDir, "com.github.mrpoundsign.towncrier", "logs", "towncrier.log")
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
		localPath, _ := filepath.Abs("towncrier.log")
		absTarget, _ := filepath.Abs(target)
		if localPath != absTarget {
			localF, _ = os.OpenFile("towncrier.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		}
	}

	logFile = f
	localFile = localF
	logPath = target

	log.SetOutput(&safeWriter{primary: f, local: localF})
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)

	return target, nil
}

// LogPath returns the currently active persistent log file path, or DefaultLogPath() if not initialized.
func LogPath() string {
	mu.Lock()
	p := logPath
	mu.Unlock()
	if p != "" {
		return p
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "towncrier.log"
	}
	return filepath.Join(configDir, "com.github.mrpoundsign.towncrier", "logs", "towncrier.log")
}

// ReadTail flushes buffered writes and reads up to the final maxBytes of path (or LogPath() if empty).
// When the file exceeds maxBytes, it discards any partial leading line and returns truncated = true.
func ReadTail(path string, maxBytes int64) (content string, truncated bool, err error) {
	target := strings.TrimSpace(path)
	if target == "" {
		target = LogPath()
	}
	Sync()
	if maxBytes <= 0 {
		maxBytes = DefaultTailMaxBytes
	}

	f, err := os.Open(target)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		return "", false, err
	}

	if fi.Size() > maxBytes {
		if _, seekErr := f.Seek(fi.Size()-maxBytes, io.SeekStart); seekErr != nil {
			return "", false, seekErr
		}
		truncated = true
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return "", false, err
	}

	if truncated {
		if idx := bytes.IndexByte(data, '\n'); idx >= 0 && idx+1 < len(data) {
			data = data[idx+1:]
		}
	}

	content = strings.ReplaceAll(string(data), "\r\n", "\n")
	return content, truncated, nil
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
