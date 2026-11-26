// Package obs implements TownCrier's pure-Go OBS Studio WebSocket v5 client.
package obs

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/websocket"
)

const (
	DefaultHost = "127.0.0.1"
	DefaultPort = 4455

	opHello           = 0
	opIdentify        = 1
	opIdentified      = 2
	opEvent           = 5
	opRequest         = 6
	opRequestResponse = 7

	defaultEventSubscriptions = 2047
)

// ConnectionState describes the OBS WebSocket client lifecycle state.
type ConnectionState string

const (
	StateDisconnected ConnectionState = "disconnected"
	StateConnecting   ConnectionState = "connecting"
	StateConnected    ConnectionState = "connected"
	StateError        ConnectionState = "error"
)

// ClientStatus reports connection state, server endpoint, version info, and any recent error.
type ClientStatus struct {
	State            ConnectionState `json:"state"`
	Address          string          `json:"address,omitempty"`
	OBSVersion       string          `json:"obsVersion,omitempty"`
	WebSocketVersion string          `json:"webSocketVersion,omitempty"`
	LastError        string          `json:"lastError,omitempty"`
}

// Event represents an asynchronous event pushed by OBS WebSocket v5.
type Event struct {
	Type string
	Data map[string]any
}

type wsEnvelope struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
}

type helloPayload struct {
	OBSWebSocketVersion string `json:"obsWebSocketVersion"`
	RPCVersion          int    `json:"rpcVersion"`
	Authentication      *struct {
		Challenge string `json:"challenge"`
		Salt      string `json:"salt"`
	} `json:"authentication,omitempty"`
}

type identifyPayload struct {
	RPCVersion         int    `json:"rpcVersion"`
	Authentication     string `json:"authentication,omitempty"`
	EventSubscriptions int    `json:"eventSubscriptions"`
}

type requestPayload struct {
	RequestType string         `json:"requestType"`
	RequestID   string         `json:"requestId"`
	RequestData map[string]any `json:"requestData,omitempty"`
}

type requestResponsePayload struct {
	RequestType   string `json:"requestType"`
	RequestID     string `json:"requestId"`
	RequestStatus struct {
		Result  bool   `json:"result"`
		Code    int    `json:"code"`
		Comment string `json:"comment,omitempty"`
	} `json:"requestStatus"`
	ResponseData map[string]any `json:"responseData,omitempty"`
}

type eventPayload struct {
	EventType string         `json:"eventType"`
	EventData map[string]any `json:"eventData,omitempty"`
}

type pendingResult struct {
	data map[string]any
	err  error
}

// ComputeAuth calculates OBS WebSocket v5 base64(sha256(base64(sha256(password + salt)) + challenge)).
func ComputeAuth(password, salt, challenge string) string {
	secretHash := sha256.Sum256([]byte(password + salt))
	secretB64 := base64.StdEncoding.EncodeToString(secretHash[:])
	authHash := sha256.Sum256([]byte(secretB64 + challenge))
	return base64.StdEncoding.EncodeToString(authHash[:])
}

// Client is a thread-safe pure-Go OBS WebSocket v5 client supporting automatic reconnection.
type Client struct {
	mu      sync.RWMutex
	writeMu sync.Mutex

	conn        *websocket.Conn
	connGen     uint64
	status      ClientStatus
	pending     map[string]chan pendingResult
	dropNotify  chan struct{}
	supCancel   context.CancelFunc
	supDone     chan struct{}
	reqCounter  atomic.Uint64
	onEvent     func(Event)
	onStatus    func(ClientStatus)
	manualClose bool
}

// NewClient creates an OBS WebSocket v5 client invoking onEvent and onStatus outside internal locks.
func NewClient(onEvent func(Event), onStatus func(ClientStatus)) *Client {
	return &Client{
		status:   ClientStatus{State: StateDisconnected},
		pending:  make(map[string]chan pendingResult),
		onEvent:  onEvent,
		onStatus: onStatus,
	}
}

// Status returns the current connection status snapshot.
func (c *Client) Status() ClientStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

// IsConnected reports whether an authenticated WebSocket session is active.
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn != nil && c.status.State == StateConnected
}

// Connect stops any background reconnect supervisor, dials host:port, completes the OBS WebSocket v5 handshake,
// and starts reading events.
func (c *Client) Connect(ctx context.Context, host string, port int, password string) error {
	c.stopSupervisor()
	return c.connectOnce(ctx, host, port, password)
}

func (c *Client) connectOnce(ctx context.Context, host string, port int, password string) error {
	if strings.TrimSpace(host) == "" {
		host = DefaultHost
	}
	if port <= 0 || port > 65535 {
		port = DefaultPort
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	wsURL := "ws://" + addr

	c.closeActiveConn(false, nil)
	c.setStatus(ClientStatus{
		State:   StateConnecting,
		Address: addr,
	})

	dialCtx, cancelDial := context.WithTimeout(ctx, 8*time.Second)
	defer cancelDial()

	wsCfg, err := websocket.NewConfig(wsURL, "http://localhost")
	if err != nil {
		c.setStatus(ClientStatus{State: StateError, Address: addr, LastError: err.Error()})
		return err
	}

	rawConn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(dialCtx, "tcp", addr)
	if err != nil {
		c.setStatus(ClientStatus{State: StateError, Address: addr, LastError: err.Error()})
		return fmt.Errorf("dial OBS at %s: %w", addr, err)
	}

	conn, err := websocket.NewClient(wsCfg, rawConn)
	if err != nil {
		_ = rawConn.Close()
		c.setStatus(ClientStatus{State: StateError, Address: addr, LastError: err.Error()})
		return fmt.Errorf("websocket handshake %s: %w", addr, err)
	}

	handshakeDeadline := time.Now().Add(5 * time.Second)
	if dl, ok := dialCtx.Deadline(); ok && dl.Before(handshakeDeadline) {
		handshakeDeadline = dl
	}
	_ = conn.SetDeadline(handshakeDeadline)

	helloEnv, err := recvEnvelope(conn)
	if err != nil || helloEnv.Op != opHello {
		_ = conn.Close()
		if err == nil {
			err = fmt.Errorf("expected OBS Hello (op 0), got op %d", helloEnv.Op)
		}
		c.setStatus(ClientStatus{State: StateError, Address: addr, LastError: err.Error()})
		return err
	}

	var hello helloPayload
	if err := json.Unmarshal(helloEnv.D, &hello); err != nil {
		_ = conn.Close()
		c.setStatus(ClientStatus{State: StateError, Address: addr, LastError: err.Error()})
		return fmt.Errorf("decode OBS Hello: %w", err)
	}

	idPayload := identifyPayload{
		RPCVersion:         max(1, hello.RPCVersion),
		EventSubscriptions: defaultEventSubscriptions,
	}
	if hello.Authentication != nil {
		idPayload.Authentication = ComputeAuth(password, hello.Authentication.Salt, hello.Authentication.Challenge)
	}
	if err := sendEnvelope(conn, opIdentify, idPayload); err != nil {
		_ = conn.Close()
		c.setStatus(ClientStatus{State: StateError, Address: addr, LastError: err.Error()})
		return fmt.Errorf("send OBS Identify: %w", err)
	}

	identEnv, err := recvEnvelope(conn)
	if err != nil {
		_ = conn.Close()
		errMsg := "OBS authentication or handshake failed"
		if errors.Is(err, context.DeadlineExceeded) {
			errMsg = err.Error()
		}
		c.setStatus(ClientStatus{State: StateError, Address: addr, LastError: errMsg})
		return fmt.Errorf("%s: %w", errMsg, err)
	}
	if identEnv.Op != opIdentified {
		_ = conn.Close()
		msg := fmt.Sprintf("expected OBS Identified (op 2), got op %d", identEnv.Op)
		c.setStatus(ClientStatus{State: StateError, Address: addr, LastError: msg})
		return errors.New(msg)
	}
	_ = conn.SetDeadline(time.Time{})

	c.mu.Lock()
	c.connGen++
	gen := c.connGen
	c.conn = conn
	c.manualClose = false
	dropCh := make(chan struct{})
	c.dropNotify = dropCh
	c.mu.Unlock()

	go c.readLoop(conn, gen, dropCh)

	obsVer := ""
	wsVer := hello.OBSWebSocketVersion
	verData, verErr := c.Call(dialCtx, "GetVersion", nil)
	if verErr == nil {
		if s, ok := verData["obsVersion"].(string); ok {
			obsVer = s
		}
		if s, ok := verData["obsWebSocketVersion"].(string); ok && s != "" {
			wsVer = s
		}
	}

	c.setStatus(ClientStatus{
		State:            StateConnected,
		Address:          addr,
		OBSVersion:       obsVer,
		WebSocketVersion: wsVer,
	})
	return nil
}

// StartAutoReconnect maintains an active connection to host:port with exponential backoff until Disconnect is called.
func (c *Client) StartAutoReconnect(host string, port int, password string) {
	c.stopSupervisor()

	supCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	c.mu.Lock()
	c.supCancel = cancel
	c.supDone = done
	c.manualClose = false
	c.mu.Unlock()

	go func() {
		defer close(done)
		backoff := time.Second
		for {
			if supCtx.Err() != nil {
				return
			}
			err := c.connectOnce(supCtx, host, port, password)
			if supCtx.Err() != nil {
				return
			}
			if err == nil {
				backoff = time.Second
				c.mu.RLock()
				dropCh := c.dropNotify
				c.mu.RUnlock()
				if dropCh != nil {
					select {
					case <-supCtx.Done():
						return
					case <-dropCh:
					}
				}
			}

			timer := time.NewTimer(backoff)
			select {
			case <-supCtx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			backoff = min(backoff*2, 15*time.Second)
		}
	}()
}

// Disconnect stops any background reconnect loop and closes the active WebSocket connection.
func (c *Client) Disconnect() {
	c.stopSupervisor()
	c.closeActiveConn(true, errors.New("disconnected"))
	c.setStatus(ClientStatus{State: StateDisconnected})
}

// Call issues an OBS WebSocket v5 request and waits for its correlated RequestResponse.
func (c *Client) Call(ctx context.Context, requestType string, requestData map[string]any) (map[string]any, error) {
	c.mu.Lock()
	conn := c.conn
	if conn == nil {
		c.mu.Unlock()
		return nil, errors.New("OBS is not connected")
	}
	reqID := "req-" + strconv.FormatUint(c.reqCounter.Add(1), 10)
	ch := make(chan pendingResult, 1)
	c.pending[reqID] = ch
	c.mu.Unlock()

	cleanupPending := func() {
		c.mu.Lock()
		delete(c.pending, reqID)
		c.mu.Unlock()
	}

	c.writeMu.Lock()
	err := sendEnvelope(conn, opRequest, requestPayload{
		RequestType: requestType,
		RequestID:   reqID,
		RequestData: requestData,
	})
	c.writeMu.Unlock()
	if err != nil {
		cleanupPending()
		return nil, fmt.Errorf("send OBS request %s: %w", requestType, err)
	}

	select {
	case <-ctx.Done():
		cleanupPending()
		return nil, ctx.Err()
	case res := <-ch:
		return res.data, res.err
	}
}

// SetCurrentProgramScene switches OBS's active program scene.
func (c *Client) SetCurrentProgramScene(ctx context.Context, sceneName string) error {
	_, err := c.Call(ctx, "SetCurrentProgramScene", map[string]any{"sceneName": sceneName})
	return err
}

// SetSceneItemEnabled resolves sourceName inside sceneName and sets its visibility.
func (c *Client) SetSceneItemEnabled(ctx context.Context, sceneName, sourceName string, enabled bool) error {
	idData, err := c.Call(ctx, "GetSceneItemId", map[string]any{
		"sceneName":  sceneName,
		"sourceName": sourceName,
	})
	if err != nil {
		return err
	}
	itemID, ok := idData["sceneItemId"]
	if !ok {
		return fmt.Errorf("scene item %q not found in %q", sourceName, sceneName)
	}
	_, err = c.Call(ctx, "SetSceneItemEnabled", map[string]any{
		"sceneName":        sceneName,
		"sceneItemId":      itemID,
		"sceneItemEnabled": enabled,
	})
	return err
}

// SetInputMute sets mute state on an OBS audio input source.
func (c *Client) SetInputMute(ctx context.Context, inputName string, muted bool) error {
	_, err := c.Call(ctx, "SetInputMute", map[string]any{
		"inputName":  inputName,
		"inputMuted": muted,
	})
	return err
}

// ToggleRecord toggles OBS recording output.
func (c *Client) ToggleRecord(ctx context.Context) error {
	_, err := c.Call(ctx, "ToggleRecord", nil)
	return err
}

// ToggleStream toggles OBS streaming output.
func (c *Client) ToggleStream(ctx context.Context) error {
	_, err := c.Call(ctx, "ToggleStream", nil)
	return err
}

// SaveReplayBuffer saves the active OBS replay buffer.
func (c *Client) SaveReplayBuffer(ctx context.Context) error {
	_, err := c.Call(ctx, "SaveReplayBuffer", nil)
	return err
}

// RefreshBrowserSource triggers OBS's refreshnocache button on a browser source input.
func (c *Client) RefreshBrowserSource(ctx context.Context, inputName string) error {
	_, err := c.Call(ctx, "PressInputPropertiesButton", map[string]any{
		"inputName":    inputName,
		"propertyName": "refreshnocache",
	})
	return err
}

func (c *Client) readLoop(conn *websocket.Conn, gen uint64, dropCh chan struct{}) {
	defer close(dropCh)
	for {
		env, err := recvEnvelope(conn)
		if err != nil {
			c.mu.RLock()
			sameGen := c.connGen == gen
			manual := c.manualClose
			addr := c.status.Address
			c.mu.RUnlock()
			if sameGen {
				c.closeActiveConn(manual, err)
				if !manual {
					c.setStatus(ClientStatus{
						State:     StateError,
						Address:   addr,
						LastError: err.Error(),
					})
				}
			}
			return
		}

		switch env.Op {
		case opRequestResponse:
			var resp requestResponsePayload
			if err := json.Unmarshal(env.D, &resp); err != nil {
				continue
			}
			var res pendingResult
			if resp.RequestStatus.Result {
				res.data = resp.ResponseData
			} else {
				comment := resp.RequestStatus.Comment
				if comment == "" {
					comment = "request failed"
				}
				res.err = fmt.Errorf("OBS request %s error (%d): %s", resp.RequestType, resp.RequestStatus.Code, comment)
			}

			c.mu.Lock()
			ch, ok := c.pending[resp.RequestID]
			if ok {
				delete(c.pending, resp.RequestID)
			}
			c.mu.Unlock()
			if ok {
				ch <- res
			}

		case opEvent:
			var ev eventPayload
			if err := json.Unmarshal(env.D, &ev); err != nil {
				continue
			}
			if c.onEvent != nil && ev.EventType != "" {
				cb := c.onEvent
				e := Event{Type: ev.EventType, Data: ev.EventData}
				go cb(e)
			}
		}
	}
}

func (c *Client) stopSupervisor() {
	c.mu.Lock()
	cancel := c.supCancel
	done := c.supDone
	c.supCancel = nil
	c.supDone = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (c *Client) closeActiveConn(manual bool, waitErr error) {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.manualClose = manual
	if waitErr == nil {
		waitErr = errors.New("OBS connection closed")
	}
	waiters := make([]chan pendingResult, 0, len(c.pending))
	for id, ch := range c.pending {
		waiters = append(waiters, ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
	for _, ch := range waiters {
		ch <- pendingResult{err: waitErr}
	}
}

func (c *Client) setStatus(st ClientStatus) {
	c.mu.Lock()
	c.status = st
	cb := c.onStatus
	c.mu.Unlock()

	if cb != nil {
		go cb(st)
	}
}

func sendEnvelope(conn *websocket.Conn, op int, data any) error {
	rawData, err := json.Marshal(data)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(wsEnvelope{
		Op: op,
		D:  rawData,
	})
	if err != nil {
		return err
	}
	return websocket.Message.Send(conn, string(payload))
}

func recvEnvelope(conn *websocket.Conn) (wsEnvelope, error) {
	var raw string
	if err := websocket.Message.Receive(conn, &raw); err != nil {
		return wsEnvelope{}, err
	}
	var env wsEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return wsEnvelope{}, err
	}
	return env, nil
}
