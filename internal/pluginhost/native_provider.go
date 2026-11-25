package pluginhost

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mrpoundsign/TownCrier/pkg/pluginsdk"
	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

const (
	nativeHandshakeTimeout = 5 * time.Second
	nativeCallTimeout      = 10 * time.Second
	nativeConfigTimeout    = 20 * time.Second
	nativeShutdownTimeout  = 2 * time.Second
)

// NativeDescriptor describes an external native subprocess plugin via plugin.json.
type NativeDescriptor struct {
	UUID        string            `json:"uuid"`
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Author      string            `json:"author,omitempty"`
	Description string            `json:"description,omitempty"`
	Binaries    map[string]string `json:"binaries"` // e.g. "windows/amd64": "myplugin.exe"
	Args        []string          `json:"args,omitempty"`
}

func parseNativeDescriptor(jsonPath string) (NativeDescriptor, string, string, error) {
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		return NativeDescriptor{}, "", "", err
	}
	var desc NativeDescriptor
	if err := json.Unmarshal(raw, &desc); err != nil {
		return NativeDescriptor{}, "", "", fmt.Errorf("parse plugin.json: %w", err)
	}
	desc.UUID = strings.TrimSpace(desc.UUID)
	desc.Name = strings.TrimSpace(desc.Name)
	if desc.UUID == "" || desc.Name == "" {
		return desc, "", "", errors.New("plugin.json requires non-empty uuid and name")
	}

	platformKey := runtime.GOOS + "/" + runtime.GOARCH
	relBin := strings.TrimSpace(desc.Binaries[platformKey])
	if relBin == "" {
		return desc, "", "", fmt.Errorf("plugin.json does not specify a binary for %s", platformKey)
	}

	dirAbs, err := filepath.Abs(filepath.Dir(jsonPath))
	if err != nil {
		return desc, "", "", err
	}
	if filepath.IsAbs(relBin) {
		return desc, "", "", fmt.Errorf("plugin.json binary path %q must be relative to plugin directory", relBin)
	}
	binAbs := filepath.Clean(filepath.Join(dirAbs, filepath.FromSlash(relBin)))
	if !isSubpath(dirAbs, binAbs) {
		return desc, "", "", fmt.Errorf("plugin.json binary path %q escapes plugin directory", relBin)
	}

	hash, err := fileSHA256(binAbs)
	if err != nil {
		return desc, binAbs, "", fmt.Errorf("hash native binary %s: %w", binAbs, err)
	}
	return desc, binAbs, hash, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func isSubpath(baseDir, target string) bool {
	rel, err := filepath.Rel(baseDir, target)
	if err != nil || rel == "." || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

type nativeTimerHandle struct {
	cancel context.CancelFunc
}

type nativeBackend struct {
	mgr   *Manager
	entry *pluginEntry

	cmd    *exec.Cmd
	conn   net.Conn
	doneCh chan struct{}

	writeMu sync.Mutex
	nextID  atomic.Uint64

	pendMu  sync.Mutex
	pending map[uint64]chan *pluginv1.PluginEnvelope
	closed  bool

	timersMu sync.Mutex
	timers   map[string]nativeTimerHandle

	lifeCtx    context.Context
	lifeCancel context.CancelFunc
}

func startNativeBackend(mgr *Manager, entry *pluginEntry, desc NativeDescriptor, binPath string) (*nativeBackend, *pluginv1.Manifest, *pluginv1.APIError, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, nil, nil, fmt.Errorf("generate ipc token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("listen loopback ipc: %w", err)
	}
	defer func() { _ = ln.Close() }()

	args := make([]string, 0, len(desc.Args)+1)
	args = append(args, "--towncrier-plugin")
	args = append(args, desc.Args...)

	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(lifeCtx, binPath, args...)
	cmd.Dir = filepath.Dir(binPath)
	cmd.Env = append(os.Environ(),
		pluginsdk.EnvIPCAddr+"="+ln.Addr().String(),
		pluginsdk.EnvIPCToken+"="+token,
	)
	hideConsoleWindow(cmd)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		lifeCancel()
		return nil, nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		lifeCancel()
		return nil, nil, nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		lifeCancel()
		return nil, nil, nil, fmt.Errorf("start native plugin: %w", err)
	}

	b := &nativeBackend{
		mgr:        mgr,
		entry:      entry,
		cmd:        cmd,
		doneCh:     make(chan struct{}),
		pending:    make(map[uint64]chan *pluginv1.PluginEnvelope),
		timers:     make(map[string]nativeTimerHandle),
		lifeCtx:    lifeCtx,
		lifeCancel: lifeCancel,
	}

	go pipeOutputToLogs(stdoutPipe, entry.logs, "stdout")
	go pipeOutputToLogs(stderrPipe, entry.logs, "stderr")

	acceptCh := make(chan net.Conn, 1)
	acceptErrCh := make(chan error, 1)
	go func() {
		if tcpLn, ok := ln.(*net.TCPListener); ok {
			_ = tcpLn.SetDeadline(time.Now().Add(nativeHandshakeTimeout))
		}
		c, aErr := ln.Accept()
		if aErr != nil {
			acceptErrCh <- aErr
			return
		}
		acceptCh <- c
	}()

	var conn net.Conn
	select {
	case conn = <-acceptCh:
	case aErr := <-acceptErrCh:
		_ = b.Close()
		return nil, nil, nil, fmt.Errorf("native plugin did not connect within %s: %w", nativeHandshakeTimeout, aErr)
	}
	b.conn = conn

	_ = conn.SetDeadline(time.Now().Add(nativeHandshakeTimeout))
	maj, minHost, gotToken, err := pluginsdk.ReadPreamble(conn)
	if err != nil {
		_ = b.Close()
		return nil, nil, nil, fmt.Errorf("read native plugin handshake: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(gotToken), []byte(token)) != 1 {
		_ = pluginsdk.WriteFrame(conn, NewHandshakeResponse(&pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED,
			Message: "Invalid native plugin IPC bearer token",
		}))
		_ = b.Close()
		return nil, nil, nil, errors.New("native plugin IPC token mismatch")
	}

	verErr := CheckVersion(uint32(maj), uint32(minHost))
	if writeErr := pluginsdk.WriteFrame(conn, NewHandshakeResponse(verErr)); writeErr != nil {
		_ = b.Close()
		return nil, nil, verErr, fmt.Errorf("write handshake response: %w", writeErr)
	}
	if verErr != nil {
		_ = b.Close()
		return nil, nil, verErr, errors.New(verErr.GetMessage())
	}
	_ = conn.SetDeadline(time.Time{})

	go b.readLoop()
	go b.waitProcess()

	resp, err := b.Dispatch(lifeCtx, &pluginv1.HostEnvelope{
		Payload: &pluginv1.HostEnvelope_GetManifest{GetManifest: &pluginv1.GetManifestRequest{}},
	})
	if err != nil {
		_ = b.Close()
		return nil, nil, nil, fmt.Errorf("get manifest: %w", err)
	}
	mResp := resp.GetManifest()
	if mResp == nil {
		_ = b.Close()
		return nil, nil, nil, errors.New("native plugin returned unexpected response to GetManifest")
	}
	if mResp.GetError() != nil {
		apiErr := mResp.GetError()
		_ = b.Close()
		return nil, nil, apiErr, errors.New(apiErr.GetMessage())
	}
	manifest := mResp.GetManifest()
	if manifest == nil || strings.TrimSpace(manifest.GetUuid()) == "" {
		apiErr := &pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			Message: "Native plugin returned empty manifest UUID",
		}
		_ = b.Close()
		return nil, nil, apiErr, errors.New(apiErr.GetMessage())
	}
	if apiErr := CheckCapabilities(manifest.GetRequiredCapabilities()); apiErr != nil {
		_ = b.Close()
		return nil, manifest, apiErr, errors.New(apiErr.GetMessage())
	}

	return b, manifest, nil, nil
}

func pipeOutputToLogs(r io.Reader, logs *logBuffer, stream string) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			logs.Append("[" + stream + "] " + line)
		}
	}
}

func (b *nativeBackend) readLoop() {
	defer b.handleDisconnect()
	for {
		var pEnv pluginv1.PluginEnvelope
		if err := pluginsdk.ReadFrame(b.conn, &pEnv); err != nil {
			return
		}

		switch pEnv.Payload.(type) {
		case *pluginv1.PluginEnvelope_Ack,
			*pluginv1.PluginEnvelope_Manifest,
			*pluginv1.PluginEnvelope_UiSnapshot,
			*pluginv1.PluginEnvelope_UiPatch,
			*pluginv1.PluginEnvelope_RunActionResponse,
			*pluginv1.PluginEnvelope_BeforeTtsResponse:
			b.pendMu.Lock()
			ch, ok := b.pending[pEnv.GetRequestId()]
			if ok {
				delete(b.pending, pEnv.GetRequestId())
			}
			b.pendMu.Unlock()
			if ok && ch != nil {
				ch <- &pEnv
			}
		default:
			envCopy := &pEnv
			go func() {
				resp := b.mgr.handleHostCall(b.lifeCtx, b.entry, envCopy)
				_ = b.writeEnvelope(&pluginv1.HostEnvelope{
					RequestId: envCopy.GetRequestId(),
					Payload:   &pluginv1.HostEnvelope_HostCallResponse{HostCallResponse: resp},
				})
			}()
		}
	}
}

func (b *nativeBackend) waitProcess() {
	err := b.cmd.Wait()
	b.pendMu.Lock()
	intentional := b.closed
	b.pendMu.Unlock()
	if !intentional {
		msg := "Native plugin process exited unexpectedly"
		if err != nil {
			msg = fmt.Sprintf("Native plugin process exited unexpectedly: %v", err)
		}
		b.entry.logs.Append(msg)
		b.entry.setError(&pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_HOST_NOT_READY,
			Message: msg,
		})
		b.mgr.notifyStatusChange()
	}
	close(b.doneCh)
}

func (b *nativeBackend) handleDisconnect() {
	b.pendMu.Lock()
	for id, ch := range b.pending {
		close(ch)
		delete(b.pending, id)
	}
	b.pendMu.Unlock()
}

func (b *nativeBackend) writeEnvelope(env *pluginv1.HostEnvelope) error {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	if b.conn == nil {
		return errors.New("native plugin connection is nil")
	}
	return pluginsdk.WriteFrame(b.conn, env)
}

func (b *nativeBackend) Dispatch(ctx context.Context, env *pluginv1.HostEnvelope) (*pluginv1.PluginEnvelope, error) {
	if env == nil {
		return nil, errors.New("nil HostEnvelope")
	}
	reqID := b.nextID.Add(1)
	env.RequestId = reqID

	timeout := nativeCallTimeout
	if _, isConfigAct := env.Payload.(*pluginv1.HostEnvelope_OnConfigAction); isConfigAct {
		timeout = nativeConfigTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ch := make(chan *pluginv1.PluginEnvelope, 1)
	b.pendMu.Lock()
	if b.closed {
		b.pendMu.Unlock()
		return nil, errors.New("native plugin backend is closed")
	}
	b.pending[reqID] = ch
	b.pendMu.Unlock()

	if err := b.writeEnvelope(env); err != nil {
		b.pendMu.Lock()
		delete(b.pending, reqID)
		b.pendMu.Unlock()
		return nil, err
	}

	select {
	case resp, ok := <-ch:
		if !ok || resp == nil {
			return nil, errors.New("native plugin disconnected while awaiting response")
		}
		return resp, nil
	case <-callCtx.Done():
		b.pendMu.Lock()
		delete(b.pending, reqID)
		b.pendMu.Unlock()
		return nil, callCtx.Err()
	}
}

func (b *nativeBackend) SetTimer(timerID, ctxStr string, intervalMs uint32) *pluginv1.APIError {
	cleanID := strings.TrimSpace(timerID)
	if cleanID == "" {
		return &pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			Message: "timer_id cannot be empty",
		}
	}

	b.timersMu.Lock()
	defer b.timersMu.Unlock()

	if old, exists := b.timers[cleanID]; exists {
		old.cancel()
		delete(b.timers, cleanID)
	}
	if intervalMs == 0 {
		return nil
	}
	if len(b.timers) >= maxTimersPerPlugin {
		return &pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_RATE_LIMITED,
			Message: fmt.Sprintf("plugin timer limit (%d) exceeded", maxTimersPerPlugin),
		}
	}

	interval := max(time.Duration(intervalMs)*time.Millisecond, minTimerInterval)
	tCtx, cancel := context.WithCancel(b.lifeCtx)
	b.timers[cleanID] = nativeTimerHandle{cancel: cancel}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-tCtx.Done():
				return
			case <-ticker.C:
				_, err := b.Dispatch(tCtx, &pluginv1.HostEnvelope{
					Payload: &pluginv1.HostEnvelope_OnTimer{
						OnTimer: &pluginv1.OnTimerRequest{
							TimerId: cleanID,
							Context: ctxStr,
						},
					},
				})
				if err != nil && tCtx.Err() == nil {
					b.entry.logs.Append("timer error: " + err.Error())
				}
			}
		}
	}()
	return nil
}

func (b *nativeBackend) Close() error {
	b.timersMu.Lock()
	for id, h := range b.timers {
		h.cancel()
		delete(b.timers, id)
	}
	b.timersMu.Unlock()

	b.pendMu.Lock()
	alreadyClosed := b.closed
	b.closed = true
	b.pendMu.Unlock()
	if alreadyClosed {
		return nil
	}

	if b.conn != nil {
		_ = b.writeEnvelope(&pluginv1.HostEnvelope{
			RequestId: b.nextID.Add(1),
			Payload:   &pluginv1.HostEnvelope_Shutdown{Shutdown: &pluginv1.ShutdownRequest{}},
		})
	}

	select {
	case <-b.doneCh:
	case <-time.After(nativeShutdownTimeout):
		if b.cmd != nil && b.cmd.Process != nil {
			_ = b.cmd.Process.Kill()
		}
		if b.lifeCancel != nil {
			b.lifeCancel()
		}
		select {
		case <-b.doneCh:
		case <-time.After(500 * time.Millisecond):
		}
	}

	if b.conn != nil {
		_ = b.conn.Close()
	}
	if b.lifeCancel != nil {
		b.lifeCancel()
	}
	return nil
}
