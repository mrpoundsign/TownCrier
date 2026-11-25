//go:build wasip1 && wasm

package pluginsdk

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"google.golang.org/protobuf/proto"

	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

//go:wasmimport towncrier tc_host_call
func tcHostCall(reqPtr, reqLen, respPtr, respCap uint32) int32

const initialWasmHostBufSize = 4096

var (
	wasmAllocMu sync.Mutex
	wasmAllocs  = make(map[uint32][]byte)

	wasmHostOnce sync.Once
	wasmHostInst *Host
)

func getWasmHost() *Host {
	wasmHostOnce.Do(func() {
		wasmHostInst = newHost(&wasmTransport{
			buf: make([]byte, initialWasmHostBufSize),
		})
	})
	return wasmHostInst
}

// Run is a no-op for WASI reactor modules because TownCrier drives execution via tc_dispatch.
func Run() error {
	return nil
}

//go:wasmexport tc_abi_version
func tcABIVersion() uint64 {
	major := ABIMajor
	minMinor := ABIMinor
	if p := Registered(); p != nil {
		if m := p.Manifest(); m != nil && m.GetApiVersion() != nil {
			if m.GetApiVersion().GetMajor() > 0 {
				major = m.GetApiVersion().GetMajor()
			}
			minMinor = m.GetApiVersion().GetMinHostMinor()
		}
	}
	return PackABIVersion(major, minMinor)
}

//go:wasmexport tc_alloc
func tcAlloc(size uint32) uint32 {
	if size == 0 {
		size = 1
	}
	buf := make([]byte, size)
	ptr := uint32(uintptr(unsafe.Pointer(&buf[0])))
	wasmAllocMu.Lock()
	wasmAllocs[ptr] = buf
	wasmAllocMu.Unlock()
	return ptr
}

//go:wasmexport tc_free
func tcFree(ptr uint32) {
	wasmAllocMu.Lock()
	delete(wasmAllocs, ptr)
	wasmAllocMu.Unlock()
}

func readWasmAlloc(ptr, size uint32) ([]byte, bool) {
	if size == 0 {
		return nil, true
	}
	wasmAllocMu.Lock()
	buf, ok := wasmAllocs[ptr]
	if !ok || uint32(len(buf)) < size {
		wasmAllocMu.Unlock()
		return nil, false
	}
	cp := make([]byte, size)
	copy(cp, buf[:size])
	wasmAllocMu.Unlock()
	return cp, true
}

func returnWasmBytes(raw []byte) uint64 {
	if len(raw) == 0 {
		raw = []byte{}
		ptr := tcAlloc(1)
		return (uint64(ptr) << 32) | 0
	}
	ptr := tcAlloc(uint32(len(raw)))
	wasmAllocMu.Lock()
	buf := wasmAllocs[ptr]
	copy(buf, raw)
	wasmAllocMu.Unlock()
	return (uint64(ptr) << 32) | uint64(len(raw))
}

//go:wasmexport tc_dispatch
func tcDispatch(reqPtr, reqLen uint32) uint64 {
	raw, ok := readWasmAlloc(reqPtr, reqLen)
	if !ok {
		respBytes, _ := proto.Marshal(ackError(0, &APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			Message: "invalid guest allocation pointer passed to tc_dispatch",
		}))
		return returnWasmBytes(respBytes)
	}
	var env pluginv1.HostEnvelope
	if err := proto.Unmarshal(raw, &env); err != nil {
		respBytes, _ := proto.Marshal(ackError(0, &APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			Message: "unmarshal HostEnvelope: " + err.Error(),
		}))
		return returnWasmBytes(respBytes)
	}

	outEnv := dispatchEnvelope(Registered(), getWasmHost(), &env)
	respBytes, err := proto.Marshal(outEnv)
	if err != nil {
		respBytes, _ = proto.Marshal(ackError(env.GetRequestId(), &APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_INTERNAL,
			Message: "marshal PluginEnvelope: " + err.Error(),
		}))
	}
	return returnWasmBytes(respBytes)
}

type wasmTransport struct {
	mu  sync.Mutex
	buf []byte
}

func (t *wasmTransport) Call(req *pluginv1.PluginEnvelope) (*pluginv1.HostCallResponse, error) {
	rawReq, err := proto.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal PluginEnvelope: %w", err)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.buf) == 0 {
		t.buf = make([]byte, initialWasmHostBufSize)
	}

	var reqPtr uint32
	if len(rawReq) > 0 {
		reqPtr = uint32(uintptr(unsafe.Pointer(&rawReq[0])))
	}

	for attempt := 0; attempt < 2; attempt++ {
		respPtr := uint32(uintptr(unsafe.Pointer(&t.buf[0])))
		rc := tcHostCall(reqPtr, uint32(len(rawReq)), respPtr, uint32(len(t.buf)))
		runtime.KeepAlive(rawReq)
		runtime.KeepAlive(t.buf)

		if rc >= 0 {
			var resp pluginv1.HostCallResponse
			if err := proto.Unmarshal(t.buf[:rc], &resp); err != nil {
				return nil, fmt.Errorf("unmarshal HostCallResponse: %w", err)
			}
			return &resp, nil
		}
		if rc == -1 {
			return nil, errors.New("host tc_host_call failed with memory/protocol error")
		}
		needed := int(-rc - 1)
		if needed <= 0 || needed > MaxFrameBytes {
			return nil, fmt.Errorf("invalid host response length %d", needed)
		}
		t.buf = make([]byte, needed)
	}

	return nil, errors.New("host tc_host_call exceeded retry limit")
}
