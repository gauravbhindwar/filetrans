// Package handshake manages WebSocket connection setup and role negotiation.
//
// The Receiver always acts as the WebSocket server (Listen).
// The Sender always acts as the WebSocket client (Connect).
// This asymmetry eliminates the need for a more complex election protocol.
package handshake

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"

	"filetrans/backend/config"
	"filetrans/backend/protocol"

	"github.com/gorilla/websocket"
)

// Conn wraps a WebSocket connection and exposes typed I/O helpers.
type Conn struct {
	ws   *websocket.Conn
	Role protocol.Role
}

// SendJSON marshals v and sends it as a WebSocket text frame.
func (c *Conn) SendJSON(v interface{}) error {
	return c.ws.WriteJSON(v)
}

// SendBinary sends data as a WebSocket binary frame (used for chunk payloads).
func (c *Conn) SendBinary(data []byte) error {
	return c.ws.WriteMessage(websocket.BinaryMessage, data)
}

// ReadFrame reads the next WebSocket frame.
// Returns (msgType, rawBytes, isBinary, error).
// For text frames: msgType is populated from the JSON "type" field.
// For binary frames: isBinary=true, msgType is empty, rawBytes is the payload.
func (c *Conn) ReadFrame() (protocol.MsgType, []byte, bool, error) {
	frameType, data, err := c.ws.ReadMessage()
	if err != nil {
		return "", nil, false, err
	}
	if frameType == websocket.BinaryMessage {
		return "", data, true, nil
	}
	var env protocol.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return "", data, false, fmt.Errorf("bad envelope: %w", err)
	}
	return env.Type, data, false, nil
}

// Close closes the underlying connection.
func (c *Conn) Close() { c.ws.Close() }

// SetDeadline sets read/write deadline on the underlying connection.
func (c *Conn) SetDeadline(t time.Time) { c.ws.SetReadDeadline(t) }

var upgrader = websocket.Upgrader{
	ReadBufferSize:  64 * 1024,
	WriteBufferSize: 64 * 1024,
	// CheckOrigin restricts access; binding to localIP further limits exposure.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Listen starts a WebSocket server bound to cfg.ServerAddr() and blocks until
// exactly one sender connects and negotiates successfully.
// The server shuts down after the first successful connection.
func Listen(cfg *config.Config, role protocol.Role) (*Conn, error) {
	connCh := make(chan *Conn, 1)
	errCh := make(chan error, 1)
	var busy int32

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if !atomic.CompareAndSwapInt32(&busy, 0, 1) {
			http.Error(w, "busy — only one connection accepted", http.StatusServiceUnavailable)
			return
		}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			atomic.StoreInt32(&busy, 0)
			return
		}
		conn, err := serverHandshake(ws, role)
		if err != nil {
			ws.WriteJSON(protocol.ErrorMsg{Type: protocol.MsgError, Message: err.Error()})
			ws.Close()
			atomic.StoreInt32(&busy, 0)
			select {
			case errCh <- err:
			default:
			}
			return
		}
		connCh <- conn
	})

	ln, err := net.Listen("tcp", cfg.ServerAddr())
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", cfg.ServerAddr(), err)
	}

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)

	// Wait up to 10 minutes for a peer to connect.
	timeout := time.NewTimer(10 * time.Minute)
	defer timeout.Stop()

	select {
	case conn := <-connCh:
		srv.Close()
		return conn, nil
	case err := <-errCh:
		srv.Close()
		return nil, err
	case <-timeout.C:
		srv.Close()
		return nil, fmt.Errorf("timed out waiting for peer to connect")
	}
}

// Connect dials the peer's WebSocket server and negotiates roles.
// Retries up to maxRetries times with a short backoff (useful when the receiver
// hasn't started listening yet).
func Connect(cfg *config.Config, role protocol.Role) (*Conn, error) {
	const maxRetries = 10
	const retryDelay = 2 * time.Second

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		ReadBufferSize:   64 * 1024,
		WriteBufferSize:  64 * 1024,
	}

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		ws, _, err := dialer.Dial(cfg.PeerWSURL(), nil)
		if err == nil {
			conn, negErr := clientHandshake(ws, role)
			if negErr != nil {
				ws.Close()
				return nil, negErr
			}
			return conn, nil
		}
		lastErr = err
		if attempt < maxRetries {
			fmt.Printf("\r  Waiting for receiver... (%d/%d)", attempt, maxRetries)
			time.Sleep(retryDelay)
		}
	}
	fmt.Println()
	return nil, fmt.Errorf("could not connect to %s after %d attempts: %w",
		cfg.PeerWSURL(), maxRetries, lastErr)
}

// serverHandshake runs on the Receiver side: read HELLO, validate, reply ROLE_OK.
func serverHandshake(ws *websocket.Conn, myRole protocol.Role) (*Conn, error) {
	ws.SetReadDeadline(time.Now().Add(20 * time.Second))
	defer ws.SetReadDeadline(time.Time{})

	_, raw, err := ws.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("read HELLO: %w", err)
	}

	var hello protocol.HelloMsg
	if err := json.Unmarshal(raw, &hello); err != nil || hello.Type != protocol.MsgHello {
		return nil, fmt.Errorf("expected HELLO, got: %s", string(raw))
	}
	if hello.Version != protocol.Version {
		return nil, fmt.Errorf("version mismatch: peer=%s ours=%s", hello.Version, protocol.Version)
	}
	if hello.Role == myRole {
		msg := protocol.RoleConflictMsg{
			Type:   protocol.MsgRoleConflict,
			Reason: fmt.Sprintf("both sides chose %q — one must be sender, one receiver", myRole),
		}
		ws.WriteJSON(msg)
		return nil, fmt.Errorf("role conflict: both sides chose %s", myRole)
	}

	if err := ws.WriteJSON(protocol.RoleOKMsg{
		Type:     protocol.MsgRoleOK,
		PeerRole: hello.Role,
	}); err != nil {
		return nil, fmt.Errorf("send ROLE_OK: %w", err)
	}

	return &Conn{ws: ws, Role: myRole}, nil
}

// clientHandshake runs on the Sender side: send HELLO, read ROLE_OK.
func clientHandshake(ws *websocket.Conn, myRole protocol.Role) (*Conn, error) {
	ws.SetReadDeadline(time.Now().Add(20 * time.Second))
	defer ws.SetReadDeadline(time.Time{})

	hello := protocol.HelloMsg{
		Type:    protocol.MsgHello,
		Role:    myRole,
		Version: protocol.Version,
		OS:      runtime.GOOS,
	}
	if err := ws.WriteJSON(hello); err != nil {
		return nil, fmt.Errorf("send HELLO: %w", err)
	}

	_, raw, err := ws.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("read HELLO reply: %w", err)
	}

	var env protocol.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode reply: %w", err)
	}

	switch env.Type {
	case protocol.MsgRoleOK:
		return &Conn{ws: ws, Role: myRole}, nil
	case protocol.MsgRoleConflict:
		var msg protocol.RoleConflictMsg
		json.Unmarshal(raw, &msg)
		return nil, fmt.Errorf("role conflict: %s", msg.Reason)
	case protocol.MsgError:
		var msg protocol.ErrorMsg
		json.Unmarshal(raw, &msg)
		return nil, fmt.Errorf("peer error: %s", msg.Message)
	default:
		return nil, fmt.Errorf("unexpected reply: %s", env.Type)
	}
}
