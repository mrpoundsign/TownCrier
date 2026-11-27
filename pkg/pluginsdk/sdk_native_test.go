//go:build !wasip1

package pluginsdk

import (
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

type testEchoPlugin struct {
	BasePlugin
	mu     sync.Mutex
	events []*pluginv1.StreamEvent
	host   *Host
}

func (p *testEchoPlugin) Manifest() *pluginv1.Manifest {
	return &pluginv1.Manifest{
		Uuid:    "com.example.echo",
		Name:    "Echo",
		Version: "1.0.0",
	}
}

func (p *testEchoPlugin) Init(h *Host) error {
	p.mu.Lock()
	p.host = h
	p.mu.Unlock()
	h.Logf("initialized")
	return nil
}

func (p *testEchoPlugin) OnEvent(ev *pluginv1.StreamEvent) error {
	p.mu.Lock()
	p.events = append(p.events, ev)
	h := p.host
	p.mu.Unlock()
	if h != nil {
		return h.Speak("hello "+ev.GetDisplayName(), "", 1.0, 0, ev.GetUserName())
	}
	return nil
}

func TestServeConnFullDuplexPipe(t *testing.T) {
	hostConn, pluginConn := net.Pipe()
	defer func() { _ = hostConn.Close() }()
	defer func() { _ = pluginConn.Close() }()

	plug := &testEchoPlugin{}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- ServeConn(pluginConn, plug)
	}()

	var mu sync.Mutex
	var spoken []string
	var logs []string
	ackCh := make(chan *pluginv1.PluginEnvelope, 8)
	var writeMu sync.Mutex
	sendToPlugin := func(env *pluginv1.HostEnvelope) {
		writeMu.Lock()
		defer writeMu.Unlock()
		require.NoError(t, WriteFrame(hostConn, env))
	}

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			var pEnv pluginv1.PluginEnvelope
			if err := ReadFrame(hostConn, &pEnv); err != nil {
				return
			}
			switch pay := pEnv.Payload.(type) {
			case *pluginv1.PluginEnvelope_Log:
				mu.Lock()
				logs = append(logs, pay.Log.GetMessage())
				mu.Unlock()
				sendToPlugin(&pluginv1.HostEnvelope{
					RequestId: pEnv.GetRequestId(),
					Payload:   &pluginv1.HostEnvelope_HostCallResponse{HostCallResponse: &pluginv1.HostCallResponse{Ok: true}},
				})
			case *pluginv1.PluginEnvelope_Speak:
				mu.Lock()
				spoken = append(spoken, pay.Speak.GetText())
				mu.Unlock()
				sendToPlugin(&pluginv1.HostEnvelope{
					RequestId: pEnv.GetRequestId(),
					Payload:   &pluginv1.HostEnvelope_HostCallResponse{HostCallResponse: &pluginv1.HostCallResponse{Ok: true}},
				})
			default:
				ackCh <- &pEnv
			}
		}
	}()

	sendToPlugin(&pluginv1.HostEnvelope{
		RequestId: 1,
		Payload:   &pluginv1.HostEnvelope_GetManifest{GetManifest: &pluginv1.GetManifestRequest{}},
	})
	mEnv := <-ackCh
	require.NotNil(t, mEnv.GetManifest().GetManifest())
	require.Equal(t, "com.example.echo", mEnv.GetManifest().GetManifest().GetUuid())

	sendToPlugin(&pluginv1.HostEnvelope{
		RequestId: 2,
		Payload:   &pluginv1.HostEnvelope_Init{Init: &pluginv1.InitRequest{}},
	})
	initAck := <-ackCh
	require.True(t, initAck.GetAck().GetOk())

	sendToPlugin(&pluginv1.HostEnvelope{
		RequestId: 3,
		Payload: &pluginv1.HostEnvelope_OnEvent{
			OnEvent: &pluginv1.OnEventRequest{
				Event: &pluginv1.OnEventRequest_StreamEvent{
					StreamEvent: &pluginv1.StreamEvent{
						UserName:    "alice",
						DisplayName: "Alice",
						Text:        "hi",
					},
				},
			},
		},
	})
	evAck := <-ackCh
	require.True(t, evAck.GetAck().GetOk())

	sendToPlugin(&pluginv1.HostEnvelope{
		RequestId: 4,
		Payload:   &pluginv1.HostEnvelope_Shutdown{Shutdown: &pluginv1.ShutdownRequest{}},
	})
	sdAck := <-ackCh
	require.True(t, sdAck.GetAck().GetOk())
	require.NoError(t, <-serveDone)
	_ = hostConn.Close()
	<-readerDone

	mu.Lock()
	require.Equal(t, []string{"initialized"}, logs)
	require.Equal(t, []string{"hello Alice"}, spoken)
	mu.Unlock()
}
