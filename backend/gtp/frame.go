// Package gtp implements the GauravTransfer Protocol — a binary-native,
// local-first file transfer protocol designed for direct links (USB-C Ethernet,
// LAN, Bluetooth PAN). No cloud. No Wi-Fi required.
//
// Wire format (all multi-byte fields little-endian):
//
//   ┌──────────┬────────┬───────────────┬─────────────────┐
//   │ Magic    │ Type   │ PayloadLen    │ Payload         │
//   │ 4 bytes  │ 1 byte │ 4 bytes       │ PayloadLen bytes │
//   │ "GTP1"   │ FrameType             │ JSON or binary  │
//   └──────────┴────────┴───────────────┴─────────────────┘
//
// Control frames carry JSON payloads. DATA frames carry raw file bytes.
// Total framing overhead: 9 bytes per frame.
package gtp

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Magic is the 4-byte protocol identifier at the start of every frame.
var Magic = [4]byte{'G', 'T', 'P', '1'}

// FrameType identifies the purpose of a frame.
type FrameType uint8

const (
	FrameHello       FrameType = 1
	FrameHelloAck    FrameType = 2
	FrameFileOffer   FrameType = 3
	FrameFileAccept  FrameType = 4
	FrameFileReject  FrameType = 5
	FrameData        FrameType = 6
	FrameDataAck     FrameType = 7
	FrameComplete    FrameType = 8
	FrameCompleteAck FrameType = 9
	FrameSessionDone FrameType = 10
	FrameError       FrameType = 11
	FramePing        FrameType = 12
	FramePong        FrameType = 13
)

func (f FrameType) String() string {
	names := map[FrameType]string{
		FrameHello: "HELLO", FrameHelloAck: "HELLO_ACK",
		FrameFileOffer: "FILE_OFFER", FrameFileAccept: "FILE_ACCEPT", FrameFileReject: "FILE_REJECT",
		FrameData: "DATA", FrameDataAck: "DATA_ACK",
		FrameComplete: "COMPLETE", FrameCompleteAck: "COMPLETE_ACK",
		FrameSessionDone: "SESSION_DONE", FrameError: "ERROR",
		FramePing: "PING", FramePong: "PONG",
	}
	if s, ok := names[f]; ok {
		return s
	}
	return fmt.Sprintf("UNKNOWN(%d)", uint8(f))
}

const (
	headerLen  = 9           // 4 magic + 1 type + 4 length
	maxPayload = 64 << 20    // 64 MiB hard cap per frame
)

// WriteFrame writes a complete GTP frame to w.
func WriteFrame(w io.Writer, ft FrameType, payload []byte) error {
	if len(payload) > maxPayload {
		return fmt.Errorf("gtp: payload %d bytes exceeds max %d", len(payload), maxPayload)
	}
	hdr := make([]byte, headerLen)
	copy(hdr[:4], Magic[:])
	hdr[4] = byte(ft)
	binary.LittleEndian.PutUint32(hdr[5:], uint32(len(payload)))
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	if len(payload) > 0 {
		_, err := w.Write(payload)
		return err
	}
	return nil
}

// ReadFrame reads the next GTP frame from r.
// Returns (frameType, payload, error).
func ReadFrame(r io.Reader) (FrameType, []byte, error) {
	hdr := make([]byte, headerLen)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return 0, nil, fmt.Errorf("gtp: read header: %w", err)
	}
	if hdr[0] != Magic[0] || hdr[1] != Magic[1] || hdr[2] != Magic[2] || hdr[3] != Magic[3] {
		return 0, nil, fmt.Errorf("gtp: bad magic: %x%x%x%x", hdr[0], hdr[1], hdr[2], hdr[3])
	}
	ft := FrameType(hdr[4])
	plen := binary.LittleEndian.Uint32(hdr[5:])
	if plen > maxPayload {
		return 0, nil, fmt.Errorf("gtp: declared payload %d bytes exceeds cap", plen)
	}
	if plen == 0 {
		return ft, nil, nil
	}
	payload := make([]byte, plen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, fmt.Errorf("gtp: read payload: %w", err)
	}
	return ft, payload, nil
}
