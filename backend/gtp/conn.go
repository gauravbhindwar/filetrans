package gtp

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"net"
	"sync"
	"time"
)

var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

// Conn is a GTP connection over a net.Conn (TCP).
// It is safe to call SendControl from multiple goroutines,
// but SendData must be called from a single goroutine.
type Conn struct {
	raw    net.Conn
	bw     *bufio.Writer
	mu     sync.Mutex // guards bw

	LocalRole  string
	RemoteRole string
	Caps       uint32
	Window     int    // agreed window size
	DeviceID   string // peer's device ID
}

// Wrap wraps an established net.Conn in a GTP Conn.
func Wrap(c net.Conn) *Conn {
	if tc, ok := c.(*net.TCPConn); ok {
		tc.SetReadBuffer(4 << 20)
		tc.SetWriteBuffer(4 << 20)
	}
	return &Conn{
		raw: c,
		bw:  bufio.NewWriterSize(c, 4<<20),
	}
}

// Close closes the underlying connection.
func (c *Conn) Close() error { return c.raw.Close() }

// SetDeadline sets read/write deadline.
func (c *Conn) SetDeadline(t time.Time) { c.raw.SetDeadline(t) }

// RemoteAddr returns the peer's address.
func (c *Conn) RemoteAddr() net.Addr { return c.raw.RemoteAddr() }

// SendControl marshals v as JSON and sends it as a control frame.
func (c *Conn) SendControl(ft FrameType, v interface{}) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("gtp marshal %s: %w", ft, err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := WriteFrame(c.bw, ft, payload); err != nil {
		return err
	}
	return c.bw.Flush()
}

// SendData sends a DATA frame containing header JSON + raw chunk bytes in one
// syscall (via buffered writer). This is the hot path for file transfer.
//
// Layout of FrameData payload:
//   [4 bytes: JSON header length LE] [JSON header] [chunk bytes]
func (c *Conn) SendData(msg DataMsg, chunk []byte) error {
	msg.ChunkSize = len(chunk)
	msg.CRC32 = crc32.Checksum(chunk, crc32cTable)

	hdrJSON, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	// Total payload = 4 + len(hdrJSON) + len(chunk)
	totalLen := 4 + len(hdrJSON) + len(chunk)

	c.mu.Lock()
	defer c.mu.Unlock()

	// Write GTP frame header manually into the buffered writer.
	fhdr := make([]byte, headerLen)
	copy(fhdr[:4], Magic[:])
	fhdr[4] = byte(FrameData)
	binary.LittleEndian.PutUint32(fhdr[5:], uint32(totalLen))
	if _, err := c.bw.Write(fhdr); err != nil {
		return err
	}

	// 4-byte JSON length prefix.
	var jlenBuf [4]byte
	binary.LittleEndian.PutUint32(jlenBuf[:], uint32(len(hdrJSON)))
	if _, err := c.bw.Write(jlenBuf[:]); err != nil {
		return err
	}
	if _, err := c.bw.Write(hdrJSON); err != nil {
		return err
	}
	if _, err := c.bw.Write(chunk); err != nil {
		return err
	}
	return c.bw.Flush()
}

// ReadFrame reads the next frame. For DATA frames use ReadData instead.
func (c *Conn) ReadFrame() (FrameType, []byte, error) {
	return ReadFrame(c.raw)
}

// ReadControl reads the next control frame and JSON-unmarshals into v.
func (c *Conn) ReadControl(expected FrameType, v interface{}) error {
	ft, payload, err := ReadFrame(c.raw)
	if err != nil {
		return err
	}
	if ft != expected {
		// Try to decode an error frame.
		if ft == FrameError {
			var em ErrorMsg
			json.Unmarshal(payload, &em)
			return fmt.Errorf("gtp: peer error %d: %s", em.Code, em.Message)
		}
		return fmt.Errorf("gtp: expected %s, got %s", expected, ft)
	}
	return json.Unmarshal(payload, v)
}

// ReadData reads a FrameData frame and returns the header and chunk bytes.
func (c *Conn) ReadData() (DataMsg, []byte, error) {
	ft, payload, err := ReadFrame(c.raw)
	if err != nil {
		return DataMsg{}, nil, err
	}
	if ft != FrameData {
		if ft == FrameError {
			var em ErrorMsg
			json.Unmarshal(payload, &em)
			return DataMsg{}, nil, fmt.Errorf("gtp peer error: %s", em.Message)
		}
		return DataMsg{}, nil, fmt.Errorf("gtp: expected DATA, got %s", ft)
	}
	if len(payload) < 4 {
		return DataMsg{}, nil, fmt.Errorf("gtp: DATA frame too short")
	}
	jlen := binary.LittleEndian.Uint32(payload[:4])
	if int(jlen)+4 > len(payload) {
		return DataMsg{}, nil, fmt.Errorf("gtp: DATA header length overflow")
	}
	var msg DataMsg
	if err := json.Unmarshal(payload[4:4+jlen], &msg); err != nil {
		return DataMsg{}, nil, fmt.Errorf("gtp: decode DATA header: %w", err)
	}
	chunk := payload[4+jlen:]
	// Verify CRC32C.
	got := crc32.Checksum(chunk, crc32cTable)
	if got != msg.CRC32 {
		return msg, nil, fmt.Errorf("gtp: CRC32C mismatch chunk %d: want %08x got %08x",
			msg.ChunkIndex, msg.CRC32, got)
	}
	return msg, chunk, nil
}

// SendError sends an error frame and returns the original error.
func (c *Conn) SendError(code int, msg string) {
	c.SendControl(FrameError, ErrorMsg{Code: code, Message: msg})
}

// Ping sends a PING and waits for PONG with a deadline.
func (c *Conn) Ping(timeout time.Duration) error {
	c.raw.SetDeadline(time.Now().Add(timeout))
	defer c.raw.SetDeadline(time.Time{})
	if err := c.SendControl(FramePing, struct{}{}); err != nil {
		return err
	}
	ft, _, err := ReadFrame(c.raw)
	if err != nil {
		return err
	}
	if ft != FramePong {
		return fmt.Errorf("gtp: expected PONG, got %s", ft)
	}
	return nil
}

// HandlePing reads one frame; if it's a PING, replies with PONG and returns true.
func (c *Conn) HandlePing(ft FrameType) bool {
	if ft != FramePing {
		return false
	}
	c.SendControl(FramePong, struct{}{})
	return true
}

// ReadAny reads the next frame and returns its type + raw payload.
// Handles PING automatically; caller gets non-PING frames only.
func (c *Conn) ReadAny() (FrameType, []byte, error) {
	for {
		ft, payload, err := ReadFrame(c.raw)
		if err != nil {
			return 0, nil, err
		}
		if ft == FramePing {
			c.SendControl(FramePong, struct{}{})
			continue
		}
		return ft, payload, nil
	}
}

// io.Reader adapter so we can pass the raw conn to ReadFrame.
func (c *Conn) Read(p []byte) (int, error) { return c.raw.Read(p) }

// roundTripMS measures RTT (used during HELLO for window-size tuning).
func roundTripMS(conn net.Conn) (int64, error) {
	start := time.Now()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	defer conn.SetDeadline(time.Time{})

	tmp := Wrap(conn)
	if err := tmp.SendControl(FramePing, struct{}{}); err != nil {
		return 0, err
	}
	ft, _, err := ReadFrame(conn)
	if err != nil {
		return 0, err
	}
	if ft != FramePong {
		return 0, fmt.Errorf("expected PONG")
	}
	return time.Since(start).Milliseconds(), nil
}
