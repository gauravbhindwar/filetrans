package gtp

// Protocol version — bump on breaking wire-format changes.
const Version = "GTP/1.0"

// Capability flags negotiated in HELLO.
const (
	CapEncryption uint32 = 1 << 0 // AES-256-GCM per-session encryption
	CapResume     uint32 = 1 << 1 // resume interrupted transfers
	CapMultiFile  uint32 = 1 << 2 // multiple files per session
	CapWindow     uint32 = 1 << 3 // windowed (pipelined) chunk delivery
)

// HelloMsg is sent by the initiator immediately after TCP connect.
// The responder sends HelloAckMsg in reply.
type HelloMsg struct {
	Version  string `json:"version"`   // must be GTP/1.0
	Role     string `json:"role"`      // "sender" | "receiver"
	OS       string `json:"os"`        // runtime.GOOS
	Caps     uint32 `json:"caps"`      // capability bitmask
	DeviceID string `json:"device_id"` // stable random ID (persisted per machine)
	Window   int    `json:"window"`    // preferred in-flight chunk window (0 = 1)
}

// HelloAckMsg is sent by the responder confirming session parameters.
type HelloAckMsg struct {
	Version    string `json:"version"`
	Role       string `json:"role"`
	OS         string `json:"os"`
	Caps       uint32 `json:"caps"`       // intersection of both sides' caps
	Window     int    `json:"window"`     // agreed window size
	SessionKey []byte `json:"session_key,omitempty"` // encrypted session key (if CapEncryption)
	Reason     string `json:"reason,omitempty"`      // non-empty on rejection
}

// FileOfferMsg describes a file or directory entry the sender wants to transfer.
type FileOfferMsg struct {
	ID          uint32 `json:"id"`           // monotonic file ID within session
	Name        string `json:"name"`         // relative path, forward slashes
	Size        int64  `json:"size"`         // total bytes
	ChunkSize   int    `json:"chunk_size"`   // bytes per DATA frame payload
	TotalChunks int64  `json:"total_chunks"` // ceil(size / chunk_size)
	Blake3      string `json:"blake3"`       // full-file BLAKE3 hash (hex) — pre-computed
	ModTime     int64  `json:"mod_time"`     // Unix timestamp (for preservation)
	Mode        uint32 `json:"mode"`         // Unix file mode bits
}

// FileAcceptMsg accepts the offer and optionally resumes from a chunk index.
type FileAcceptMsg struct {
	ID          uint32 `json:"id"`
	ResumeChunk int64  `json:"resume_chunk"` // 0 = start fresh
}

// FileRejectMsg declines the offer.
type FileRejectMsg struct {
	ID     uint32 `json:"id"`
	Reason string `json:"reason"`
}

// DataMsg header precedes every DATA frame payload.
// The DATA frame payload is: DataMsg JSON (length-prefixed) + raw chunk bytes.
// Layout in a single FrameData payload:
//   [4 bytes JSON len LE] [JSON bytes] [chunk bytes]
type DataMsg struct {
	FileID     uint32 `json:"file_id"`
	ChunkIndex int64  `json:"chunk_index"`
	ChunkSize  int    `json:"chunk_size"` // bytes of chunk data following
	CRC32      uint32 `json:"crc32"`      // CRC32C of chunk bytes (fast corruption check)
}

// DataAckMsg acknowledges a received chunk.
type DataAckMsg struct {
	FileID     uint32 `json:"file_id"`
	ChunkIndex int64  `json:"chunk_index"`
	OK         bool   `json:"ok"` // false = CRC mismatch, please retransmit
}

// CompleteMsg is sent after all chunks, carrying the full-file BLAKE3 hash.
type CompleteMsg struct {
	FileID uint32 `json:"file_id"`
	Blake3 string `json:"blake3"` // hex BLAKE3 of entire file
	Bytes  int64  `json:"bytes"`  // total bytes sent (sanity check)
}

// CompleteAckMsg is sent by the receiver after verifying the hash.
type CompleteAckMsg struct {
	FileID uint32 `json:"file_id"`
	OK     bool   `json:"ok"`
	Blake3 string `json:"blake3"` // receiver's computed hash (for diagnostics)
}

// SessionDoneMsg signals no more files will be offered.
type SessionDoneMsg struct {
	FilesCount int   `json:"files_count"`
	TotalBytes int64 `json:"total_bytes"`
}

// ErrorMsg signals an unrecoverable error.
type ErrorMsg struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Error codes.
const (
	ErrGeneral      = 1
	ErrVersionMismatch = 2
	ErrRoleConflict = 3
	ErrFileAccess   = 4
	ErrChecksumFail = 5
	ErrDiskFull     = 6
)
