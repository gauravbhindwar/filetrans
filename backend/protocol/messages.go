// Package protocol defines the filetrans wire protocol.
//
// Control flow uses JSON text frames over WebSocket.
// File data uses binary frames (raw bytes, no framing overhead).
//
// Session sequence:
//   Sender connects → HELLO exchange → FILE_OFFER → FILE_ACCEPT →
//   [CHUNK_HEADER + binary]… → COMPLETE → COMPLETE_ACK →
//   (repeat for more files) → SESSION_DONE
package protocol

// MsgType is the discriminator field in every JSON control frame.
type MsgType string

const (
	MsgHello        MsgType = "HELLO"
	MsgRoleOK       MsgType = "ROLE_OK"
	MsgRoleConflict MsgType = "ROLE_CONFLICT"
	MsgFileOffer    MsgType = "FILE_OFFER"
	MsgFileAccept   MsgType = "FILE_ACCEPT"
	MsgFileReject   MsgType = "FILE_REJECT"
	MsgChunkHeader  MsgType = "CHUNK_HEADER"
	MsgChunkAck     MsgType = "CHUNK_ACK"
	MsgComplete     MsgType = "COMPLETE"
	MsgCompleteAck  MsgType = "COMPLETE_ACK"
	MsgSessionDone  MsgType = "SESSION_DONE"
	MsgError        MsgType = "ERROR"
	MsgPing         MsgType = "PING"
	MsgPong         MsgType = "PONG"
)

// Role identifies which side of the transfer connection this is.
type Role string

const (
	RoleSender   Role = "sender"
	RoleReceiver Role = "receiver"
)

// Version is the protocol version sent in HELLO.
const Version = "1.0"

// Envelope is decoded first to determine message type before full decode.
type Envelope struct {
	Type MsgType `json:"type"`
}

// HelloMsg is sent by both sides immediately after WebSocket upgrade.
// The server (receiver) reads first; the client (sender) sends first.
type HelloMsg struct {
	Type    MsgType `json:"type"`
	Role    Role    `json:"role"`
	Version string  `json:"version"`
	OS      string  `json:"os"`
}

// RoleOKMsg confirms role negotiation succeeded.
type RoleOKMsg struct {
	Type     MsgType `json:"type"`
	PeerRole Role    `json:"peer_role"`
}

// RoleConflictMsg signals both sides chose the same role.
type RoleConflictMsg struct {
	Type   MsgType `json:"type"`
	Reason string  `json:"reason"`
}

// FileOfferMsg describes a file the sender wants to transfer.
// Name uses forward slashes and is relative — never absolute.
type FileOfferMsg struct {
	Type        MsgType `json:"type"`
	Name        string  `json:"name"`         // relative path, forward slashes
	Size        int64   `json:"size"`         // total file size in bytes
	ChunkSize   int     `json:"chunk_size"`   // bytes per chunk
	TotalChunks int64   `json:"total_chunks"` // ceil(size / chunk_size)
}

// FileAcceptMsg accepts the offer and optionally requests resume from an offset.
type FileAcceptMsg struct {
	Type       MsgType `json:"type"`
	ResumeFrom int64   `json:"resume_from"` // byte offset; 0 = start fresh
}

// FileRejectMsg declines the offer.
type FileRejectMsg struct {
	Type   MsgType `json:"type"`
	Reason string  `json:"reason"`
}

// ChunkHeaderMsg precedes every binary data frame.
// The receiver reads this JSON frame, then immediately reads the next
// binary frame which contains exactly Size bytes of file data.
type ChunkHeaderMsg struct {
	Type  MsgType `json:"type"`
	Index int64   `json:"index"` // 0-based chunk index
	Size  int     `json:"size"`  // bytes in the following binary frame
}

// ChunkAckMsg is sent by the receiver after each binary chunk is written.
type ChunkAckMsg struct {
	Type  MsgType `json:"type"`
	Index int64   `json:"index"`
}

// CompleteMsg is sent by the sender after all chunks, carrying the SHA-256
// of the full file (hex-encoded, lowercase).
type CompleteMsg struct {
	Type   MsgType `json:"type"`
	SHA256 string  `json:"sha256"`
}

// CompleteAckMsg is sent by the receiver after verifying the checksum.
type CompleteAckMsg struct {
	Type   MsgType `json:"type"`
	OK     bool    `json:"ok"`
	SHA256 string  `json:"sha256"` // receiver's computed hash for diagnostics
}

// SessionDoneMsg is sent by the sender when no more files will be offered.
type SessionDoneMsg struct {
	Type MsgType `json:"type"`
}

// ErrorMsg signals an unrecoverable error on one side.
type ErrorMsg struct {
	Type    MsgType `json:"type"`
	Message string  `json:"message"`
}

// PingMsg / PongMsg keep the WebSocket alive during long transfers.
type PingMsg struct{ Type MsgType `json:"type"` }
type PongMsg struct{ Type MsgType `json:"type"` }
