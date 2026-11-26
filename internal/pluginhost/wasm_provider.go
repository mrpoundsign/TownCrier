package pluginhost

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"google.golang.org/protobuf/proto"

	"github.com/mrpoundsign/TownCrier/pkg/pluginsdk"
	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

const (
	wasmHostModuleName    = "towncrier"
	defaultWasmMemoryMB   = 32
	maxWasmMemoryMB       = 256
	pagesPerMB            = 16
	defaultWasmTimeout    = 5 * time.Second
	configActionTimeout   = 20 * time.Second
	minTimerInterval      = 250 * time.Millisecond
	maxTimersPerPlugin    = 16
	maxWasmPayloadBytes   = 16 * 1024 * 1024
)

type wasmTimerHandle struct {
	cancel context.CancelFunc
}

type wasmBackend struct {
	mgr      *Manager
	entry    *pluginEntry
	path     string
	wasmData []byte

	lifeCtx    context.Context
	lifeCancel context.CancelFunc

	callMu     sync.Mutex
	rt         wazero.Runtime
	mod        api.Module
	allocFn    api.Function
	freeFn     api.Function
	dispatchFn api.Function
	closed     bool

	timersMu sync.Mutex
	timers   map[string]wasmTimerHandle
}

func newWasmBackend(mgr *Manager, entry *pluginEntry, path string, wasmBytes []byte) (*wasmBackend, *pluginv1.Manifest, *pluginv1.APIError, error) {
	b := &wasmBackend{
		mgr:      mgr,
		entry:    entry,
		path:     path,
		wasmData: wasmBytes,
		timers:   make(map[string]wasmTimerHandle),
	}

	manifest, apiErr, err := b.instantiateWithMemory(defaultWasmMemoryMB)
	if apiErr != nil || err != nil {
		_ = b.Close()
		return nil, manifest, apiErr, err
	}

	if reqMB := manifest.GetMaxMemoryMb(); reqMB > defaultWasmMemoryMB {
		clamped := min(reqMB, maxWasmMemoryMB)
		_ = b.closeRuntimeOnly()
		manifest, apiErr, err = b.instantiateWithMemory(clamped)
		if apiErr != nil || err != nil {
			_ = b.Close()
			return nil, manifest, apiErr, err
		}
	}

	return b, manifest, nil, nil
}

func (b *wasmBackend) instantiateWithMemory(memoryMB uint32) (*pluginv1.Manifest, *pluginv1.APIError, error) {
	pages := min(max(memoryMB, defaultWasmMemoryMB), maxWasmMemoryMB) * pagesPerMB
	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	b.lifeCtx = lifeCtx
	b.lifeCancel = lifeCancel

	rtCfg := wazero.NewRuntimeConfig().
		WithMemoryLimitPages(pages).
		WithCloseOnContextDone(true)
	rt := wazero.NewRuntimeWithConfig(lifeCtx, rtCfg)
	b.rt = rt

	if _, err := wasi_snapshot_preview1.Instantiate(lifeCtx, rt); err != nil {
		return nil, nil, fmt.Errorf("init wasi: %w", err)
	}
	if err := b.instantiateHostModule(lifeCtx, rt); err != nil {
		return nil, nil, fmt.Errorf("init host module: %w", err)
	}

	compiled, err := rt.CompileModule(lifeCtx, b.wasmData)
	if err != nil {
		return nil, nil, fmt.Errorf("compile wasm: %w", err)
	}

	modCfg := wazero.NewModuleConfig().
		WithName(filepath.Base(b.path)).
		WithStartFunctions("_initialize").
		WithSysWalltime().
		WithSysNanotime().
		WithSysNanosleep().
		WithRandSource(rand.Reader)

	initCtx, initCancel := context.WithTimeout(lifeCtx, defaultWasmTimeout)
	defer initCancel()

	mod, err := rt.InstantiateModule(initCtx, compiled, modCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("instantiate wasm: %w", err)
	}
	b.mod = mod

	if mod.ExportedFunction("_initialize") == nil && mod.ExportedFunction("_start") != nil {
		return nil, &pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			Message: "WASM module exports WASI command _start instead of reactor _initialize",
			Detail:  "For Go plugins, build with: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared",
		}, errors.New("unsupported WASI command module")
	}

	abiFn := mod.ExportedFunction("tc_abi_version")
	b.allocFn = mod.ExportedFunction("tc_alloc")
	b.freeFn = mod.ExportedFunction("tc_free")
	b.dispatchFn = mod.ExportedFunction("tc_dispatch")
	if abiFn == nil || b.allocFn == nil || b.dispatchFn == nil {
		return nil, &pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_API_VERSION,
			Message: "Missing required TownCrier WASM exports (tc_abi_version, tc_alloc, tc_dispatch)",
			Detail:  "Ensure plugin uses TownCrier pkg/pluginsdk v1.0.0+",
		}, errors.New("missing required WASM exports")
	}

	abiCtx, abiCancel := context.WithTimeout(lifeCtx, defaultWasmTimeout)
	defer abiCancel()
	abiRes, err := abiFn.Call(abiCtx)
	if err != nil || len(abiRes) == 0 {
		return nil, &pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_UNSUPPORTED_API_VERSION,
			Message: "Failed calling tc_abi_version on WASM module",
			Detail:  fmt.Sprintf("%v", err),
		}, fmt.Errorf("call tc_abi_version: %w", err)
	}
	maj, minHost := pluginsdk.UnpackABIVersion(abiRes[0])
	if apiErr := CheckVersion(maj, minHost); apiErr != nil {
		return nil, apiErr, errors.New(apiErr.GetMessage())
	}

	resp, err := b.Dispatch(lifeCtx, &pluginv1.HostEnvelope{
		RequestId: 1,
		Payload:   &pluginv1.HostEnvelope_GetManifest{GetManifest: &pluginv1.GetManifestRequest{}},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("get manifest: %w", err)
	}
	mResp := resp.GetManifest()
	if mResp == nil {
		return nil, nil, errors.New("plugin returned unexpected response to GetManifest")
	}
	if mResp.GetError() != nil {
		return nil, mResp.GetError(), errors.New(mResp.GetError().GetMessage())
	}
	manifest := mResp.GetManifest()
	if manifest == nil || strings.TrimSpace(manifest.GetUuid()) == "" || strings.TrimSpace(manifest.GetName()) == "" {
		return nil, &pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			Message: "Plugin manifest must declare non-empty uuid and name",
		}, errors.New("invalid plugin manifest")
	}
	if manifest.GetApiVersion() != nil && manifest.GetApiVersion().GetMajor() > 0 {
		if apiErr := CheckVersion(manifest.GetApiVersion().GetMajor(), manifest.GetApiVersion().GetMinHostMinor()); apiErr != nil {
			return manifest, apiErr, errors.New(apiErr.GetMessage())
		}
	}
	if apiErr := CheckCapabilities(manifest.GetRequiredCapabilities()); apiErr != nil {
		return manifest, apiErr, errors.New(apiErr.GetMessage())
	}
	if apiErr := ValidateManifest(manifest); apiErr != nil {
		return manifest, apiErr, errors.New(apiErr.GetMessage())
	}

	return manifest, nil, nil
}

func (b *wasmBackend) instantiateHostModule(ctx context.Context, rt wazero.Runtime) error {
	builder := rt.NewHostModuleBuilder(wasmHostModuleName)

	builder.NewFunctionBuilder().
		WithFunc(func(callCtx context.Context, m api.Module, reqPtr, reqLen, respPtr, respCap uint32) int32 {
			reqBytes, ok := readWasmMemory(m, reqPtr, reqLen)
			if !ok {
				return -1
			}
			var pEnv pluginv1.PluginEnvelope
			var hostResp *pluginv1.HostCallResponse
			if err := proto.Unmarshal(reqBytes, &pEnv); err != nil {
				hostResp = &pluginv1.HostCallResponse{
					Ok: false,
					Error: &pluginv1.APIError{
						Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
						Message: "unmarshal PluginEnvelope: " + err.Error(),
					},
				}
			} else {
				hostResp = b.mgr.handleHostCall(callCtx, b.entry, &pEnv)
			}

			respBytes, err := proto.Marshal(hostResp)
			if err != nil {
				return -1
			}
			if uint32(len(respBytes)) > respCap {
				// Return -(needed + 1) so even 0-byte or 1-byte buffers are unambiguous vs -1 error.
				return -int32(len(respBytes) + 1)
			}
			if len(respBytes) > 0 && !m.Memory().Write(respPtr, respBytes) {
				return -1
			}
			return int32(len(respBytes))
		}).
		Export("tc_host_call")

	_, err := builder.Instantiate(ctx)
	return err
}

func (b *wasmBackend) Dispatch(ctx context.Context, env *pluginv1.HostEnvelope) (*pluginv1.PluginEnvelope, error) {
	if env == nil {
		return nil, errors.New("nil HostEnvelope")
	}
	reqBytes, err := proto.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("marshal HostEnvelope: %w", err)
	}
	if len(reqBytes) > maxWasmPayloadBytes {
		return nil, fmt.Errorf("request payload exceeds %d bytes", maxWasmPayloadBytes)
	}

	timeout := defaultWasmTimeout
	if _, isConfigAct := env.Payload.(*pluginv1.HostEnvelope_OnConfigAction); isConfigAct {
		timeout = configActionTimeout
	}
	if deadline, ok := ctx.Deadline(); ok {
		if rem := time.Until(deadline); rem > 0 && rem < timeout {
			timeout = rem
		}
	}

	b.callMu.Lock()
	defer b.callMu.Unlock()

	if b.closed || b.mod == nil || b.allocFn == nil || b.dispatchFn == nil {
		return nil, errors.New("wasm plugin module is closed")
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	allocSize := max(uint64(len(reqBytes)), 1)
	allocRes, err := b.allocFn.Call(callCtx, allocSize)
	if err != nil || len(allocRes) == 0 || allocRes[0] == 0 {
		return nil, b.handleCallFailure(fmt.Errorf("tc_alloc failed: %w", err))
	}
	reqPtr := uint32(allocRes[0])
	defer b.freeGuestPtr(reqPtr)

	if len(reqBytes) > 0 && !b.mod.Memory().Write(reqPtr, reqBytes) {
		return nil, errors.New("failed writing request to wasm memory")
	}

	dispRes, err := b.dispatchFn.Call(callCtx, uint64(reqPtr), uint64(len(reqBytes)))
	if err != nil {
		return nil, b.handleCallFailure(fmt.Errorf("tc_dispatch failed: %w", err))
	}
	if len(dispRes) == 0 {
		return nil, errors.New("tc_dispatch returned no result")
	}

	respPtr := uint32(dispRes[0] >> 32)
	respLen := uint32(dispRes[0] & 0xffffffff)
	if respPtr != 0 {
		defer b.freeGuestPtr(respPtr)
	}
	if respLen > maxWasmPayloadBytes {
		return nil, fmt.Errorf("wasm response size %d exceeds limit", respLen)
	}

	respBytes, ok := readWasmMemory(b.mod, respPtr, respLen)
	if !ok {
		return nil, errors.New("failed reading response from wasm memory")
	}

	var out pluginv1.PluginEnvelope
	if err := proto.Unmarshal(respBytes, &out); err != nil {
		return nil, fmt.Errorf("unmarshal PluginEnvelope: %w", err)
	}
	return &out, nil
}

func (b *wasmBackend) freeGuestPtr(ptr uint32) {
	if b.freeFn == nil || ptr == 0 || b.lifeCtx == nil || b.lifeCtx.Err() != nil {
		return
	}
	fCtx, cancel := context.WithTimeout(b.lifeCtx, 500*time.Millisecond)
	defer cancel()
	_, _ = b.freeFn.Call(fCtx, uint64(ptr))
}

func (b *wasmBackend) handleCallFailure(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		b.stopAllTimers()
		b.entry.setError(&pluginv1.APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_TIMEOUT,
			Message: "WASM plugin call timed out",
			Detail:  err.Error(),
		})
		b.mgr.notifyStatusChange()
	}
	return err
}

func (b *wasmBackend) SetTimer(timerID, ctxStr string, intervalMs uint32) *pluginv1.APIError {
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
	b.timers[cleanID] = wasmTimerHandle{cancel: cancel}

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
					b.stopAllTimers()
					return
				}
			}
		}
	}()
	return nil
}

func (b *wasmBackend) stopAllTimers() {
	b.timersMu.Lock()
	for id, h := range b.timers {
		h.cancel()
		delete(b.timers, id)
	}
	b.timersMu.Unlock()
}

func (b *wasmBackend) closeRuntimeOnly() error {
	b.stopAllTimers()
	if b.lifeCancel != nil {
		b.lifeCancel()
	}
	if b.rt != nil {
		err := b.rt.Close(context.Background())
		b.rt = nil
		b.mod = nil
		return err
	}
	return nil
}

func (b *wasmBackend) Close() error {
	b.callMu.Lock()
	if b.closed {
		b.callMu.Unlock()
		return nil
	}
	b.closed = true
	b.callMu.Unlock()

	return b.closeRuntimeOnly()
}

func readWasmMemory(m api.Module, ptr, size uint32) ([]byte, bool) {
	if size == 0 {
		return nil, true
	}
	if m == nil || m.Memory() == nil {
		return nil, false
	}
	raw, ok := m.Memory().Read(ptr, size)
	if !ok {
		return nil, false
	}
	cp := make([]byte, len(raw))
	copy(cp, raw)
	return cp, true
}
