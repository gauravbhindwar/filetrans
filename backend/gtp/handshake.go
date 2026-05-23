package gtp

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"time"

	"filetrans/backend/config"
)

const (
	defaultWindow   = 32  // chunks in-flight without waiting for ack
	maxWindow       = 128
	dialTimeout     = 10 * time.Second
	handshakeTimeout = 20 * time.Second
)

// Listen binds a TCP listener on cfg.ServerAddr() and accepts one GTP connection.
// The accepted connection undergoes HELLO negotiation before returning.
func Listen(cfg *config.Config, role string) (*Conn, error) {
	ln, err := net.Listen("tcp", cfg.ServerAddr())
	if err != nil {
		return nil, fmt.Errorf("gtp listen %s: %w", cfg.ServerAddr(), err)
	}
	defer ln.Close()

	ln.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Minute))
	raw, err := ln.Accept()
	if err != nil {
		return nil, fmt.Errorf("gtp accept: %w", err)
	}
	return serverHandshake(raw, role)
}

// Connect dials the peer and performs HELLO negotiation.
// Retries up to maxRetries times.
func Connect(cfg *config.Config, role string) (*Conn, error) {
	const maxRetries = 10
	const retryDelay = 2 * time.Second

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		raw, err := net.DialTimeout("tcp", cfg.PeerAddr(), dialTimeout)
		if err == nil {
			conn, err := clientHandshake(raw, role)
			if err != nil {
				raw.Close()
				return nil, err
			}
			return conn, nil
		}
		lastErr = err
		if attempt < maxRetries {
			fmt.Fprintf(os.Stderr, "\r  [GTP] Waiting for receiver... (%d/%d)", attempt, maxRetries)
			time.Sleep(retryDelay)
		}
	}
	fmt.Fprintln(os.Stderr)
	return nil, fmt.Errorf("gtp: could not connect to %s after %d attempts: %w",
		cfg.PeerAddr(), maxRetries, lastErr)
}

// ListenAll binds on all interfaces (0.0.0.0:port), needed for WiFi/LAN mode
// where the local USB-link IP is not the right bind address.
func ListenAll(port int, role string) (*Conn, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("gtp listen :%d: %w", port, err)
	}
	defer ln.Close()
	ln.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Minute))
	raw, err := ln.Accept()
	if err != nil {
		return nil, fmt.Errorf("gtp accept: %w", err)
	}
	return serverHandshake(raw, role)
}

// serverHandshake: read HELLO, validate, reply HelloAck.
func serverHandshake(raw net.Conn, myRole string) (*Conn, error) {
	raw.SetDeadline(time.Now().Add(handshakeTimeout))
	defer raw.SetDeadline(time.Time{})

	ft, payload, err := ReadFrame(raw)
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("gtp: read HELLO: %w", err)
	}
	if ft != FrameHello {
		raw.Close()
		return nil, fmt.Errorf("gtp: expected HELLO, got %s", ft)
	}

	var hello HelloMsg
	if err := decodeJSON(payload, &hello); err != nil {
		raw.Close()
		return nil, err
	}
	if hello.Version != Version {
		ack := HelloAckMsg{Version: Version, Reason: fmt.Sprintf("version mismatch: peer=%s ours=%s", hello.Version, Version)}
		sendControl(raw, FrameHelloAck, ack)
		raw.Close()
		return nil, fmt.Errorf("gtp: version mismatch: %s vs %s", hello.Version, Version)
	}
	if hello.Role == myRole {
		ack := HelloAckMsg{Version: Version, Reason: fmt.Sprintf("role conflict: both sides chose %q", myRole)}
		sendControl(raw, FrameHelloAck, ack)
		raw.Close()
		return nil, fmt.Errorf("gtp: role conflict — both chose %s", myRole)
	}

	// Negotiate capabilities (intersection).
	myCaps := CapResume | CapMultiFile | CapWindow
	agreedCaps := hello.Caps & myCaps

	// Negotiate window size: take the maximum of both sides, capped at maxWindow.
	window := defaultWindow
	if hello.Window > window {
		window = hello.Window
	}
	if window > maxWindow {
		window = maxWindow
	}

	ack := HelloAckMsg{
		Version: Version,
		Role:    myRole,
		OS:      runtime.GOOS,
		Caps:    agreedCaps,
		Window:  window,
	}
	if err := sendControl(raw, FrameHelloAck, ack); err != nil {
		raw.Close()
		return nil, err
	}

	conn := Wrap(raw)
	conn.LocalRole = myRole
	conn.RemoteRole = hello.Role
	conn.Caps = agreedCaps
	conn.Window = window
	conn.DeviceID = hello.DeviceID
	return conn, nil
}

// clientHandshake: send HELLO, read HelloAck.
func clientHandshake(raw net.Conn, myRole string) (*Conn, error) {
	raw.SetDeadline(time.Now().Add(handshakeTimeout))
	defer raw.SetDeadline(time.Time{})

	hello := HelloMsg{
		Version:  Version,
		Role:     myRole,
		OS:       runtime.GOOS,
		Caps:     CapResume | CapMultiFile | CapWindow,
		DeviceID: deviceID(),
		Window:   defaultWindow,
	}
	if err := sendControl(raw, FrameHello, hello); err != nil {
		raw.Close()
		return nil, err
	}

	ft, payload, err := ReadFrame(raw)
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("gtp: read HELLO_ACK: %w", err)
	}
	if ft != FrameHelloAck {
		raw.Close()
		return nil, fmt.Errorf("gtp: expected HELLO_ACK, got %s", ft)
	}

	var ack HelloAckMsg
	if err := decodeJSON(payload, &ack); err != nil {
		raw.Close()
		return nil, err
	}
	if ack.Reason != "" {
		raw.Close()
		return nil, fmt.Errorf("gtp: rejected: %s", ack.Reason)
	}

	conn := Wrap(raw)
	conn.LocalRole = myRole
	conn.RemoteRole = ack.Role
	conn.Caps = ack.Caps
	conn.Window = ack.Window
	conn.DeviceID = ack.Role // use role as ID from server side
	return conn, nil
}
