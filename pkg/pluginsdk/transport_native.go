//go:build !wasip1

package pluginsdk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

// Run connects to TownCrier over authenticated loopback TCP and serves the registered plugin until Shutdown or EOF.
func Run() error {
	p := Registered()
	if p == nil {
		return errors.New("pluginsdk: no plugin registered via pluginsdk.Register")
	}
	addr := strings.TrimSpace(os.Getenv(EnvIPCAddr))
	token := strings.TrimSpace(os.Getenv(EnvIPCToken))
	if addr == "" || token == "" {
		return fmt.Errorf("pluginsdk: missing %s or %s environment variable", EnvIPCAddr, EnvIPCToken)
	}

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("pluginsdk: dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	major, minMinor := uint16(ABIMajor), uint16(ABIMinor)
	if m := p.Manifest(); m != nil && m.GetApiVersion() != nil {
		if m.GetApiVersion().GetMajor() > 0 {
			major = uint16(m.GetApiVersion().GetMajor())
		}
		minMinor = uint16(m.GetApiVersion().GetMinHostMinor())
	}

	if err := WritePreamble(conn, major, minMinor, token); err != nil {
		return err
	}

	var hs pluginv1.HandshakeResponse
	if err := ReadFrame(conn, &hs); err != nil {
		return fmt.Errorf("pluginsdk: read handshake response: %w", err)
	}
	if !hs.GetOk() {
		if apiErr := FromProto(hs.GetError()); apiErr != nil {
			return apiErr
		}
		return errors.New("pluginsdk: host rejected handshake")
	}

	return ServeConn(conn, p)
}

// ServeConn serves a registered Plugin over an already-authenticated framed stream (exposed for testing).
func ServeConn(rw io.ReadWriteCloser, p Plugin) error {
	if rw == nil {
		return errors.New("nil connection")
	}
	if p == nil {
		return errors.New("nil plugin")
	}

	tr := &nativeTransport{
		rw:      rw,
		pending: make(map[uint64]chan *pluginv1.HostCallResponse),
		doneCh:  make(chan struct{}),
	}
	host := newHost(tr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	var readErr error
	for {
		var env pluginv1.HostEnvelope
		if err := ReadFrame(rw, &env); err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				readErr = err
			}
			break
		}

		if resp := env.GetHostCallResponse(); resp != nil {
			tr.deliverResponse(env.GetRequestId(), resp)
			continue
		}

		if _, isShutdown := env.Payload.(*pluginv1.HostEnvelope_Shutdown); isShutdown {
			out := dispatchEnvelope(p, host, &env)
			_ = tr.writeEnvelope(out)
			break
		}

		envCopy := &env
		wg.Go(func() {
			if ctx.Err() != nil {
				return
			}
			out := dispatchEnvelope(p, host, envCopy)
			_ = tr.writeEnvelope(out)
		})
	}

	cancel()
	tr.closeAllPending()
	wg.Wait()
	return readErr
}

type nativeTransport struct {
	rw io.ReadWriteCloser

	writeMu sync.Mutex
	nextID  atomic.Uint64

	pendMu  sync.Mutex
	pending map[uint64]chan *pluginv1.HostCallResponse
	closed  bool
	doneCh  chan struct{}
}

func (t *nativeTransport) writeEnvelope(env *pluginv1.PluginEnvelope) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	return WriteFrame(t.rw, env)
}

func (t *nativeTransport) deliverResponse(reqID uint64, resp *pluginv1.HostCallResponse) {
	t.pendMu.Lock()
	ch, ok := t.pending[reqID]
	if ok {
		delete(t.pending, reqID)
	}
	t.pendMu.Unlock()
	if ok && ch != nil {
		ch <- resp
	}
}

func (t *nativeTransport) closeAllPending() {
	t.pendMu.Lock()
	if t.closed {
		t.pendMu.Unlock()
		return
	}
	t.closed = true
	for id, ch := range t.pending {
		close(ch)
		delete(t.pending, id)
	}
	close(t.doneCh)
	t.pendMu.Unlock()
}

func (t *nativeTransport) Call(req *pluginv1.PluginEnvelope) (*pluginv1.HostCallResponse, error) {
	reqID := t.nextID.Add(1)
	req.RequestId = reqID

	ch := make(chan *pluginv1.HostCallResponse, 1)
	t.pendMu.Lock()
	if t.closed {
		t.pendMu.Unlock()
		return nil, &APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_HOST_NOT_READY,
			Message: "native plugin IPC connection closed",
		}
	}
	t.pending[reqID] = ch
	t.pendMu.Unlock()

	if err := t.writeEnvelope(req); err != nil {
		t.pendMu.Lock()
		delete(t.pending, reqID)
		t.pendMu.Unlock()
		return nil, err
	}

	select {
	case resp, ok := <-ch:
		if !ok || resp == nil {
			return nil, &APIError{
				Code:    pluginv1.ErrorCode_ERROR_CODE_HOST_NOT_READY,
				Message: "native plugin IPC connection closed while awaiting host response",
			}
		}
		return resp, nil
	case <-t.doneCh:
		return nil, &APIError{
			Code:    pluginv1.ErrorCode_ERROR_CODE_HOST_NOT_READY,
			Message: "native plugin IPC connection closed",
		}
	}
}
