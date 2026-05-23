package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"filetrans/backend/handshake"
	"filetrans/backend/protocol"
	"filetrans/backend/ui"
)

// Receive handles the receiver-side session loop.
// It processes FILE_OFFER messages until SESSION_DONE or an error.
func Receive(conn *handshake.Conn, downloadDir string) error {
	if err := os.MkdirAll(downloadDir, 0o755); err != nil {
		return fmt.Errorf("create download dir: %w", err)
	}

	for {
		msgType, raw, _, err := conn.ReadFrame()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		switch msgType {
		case protocol.MsgFileOffer:
			var offer protocol.FileOfferMsg
			if err := json.Unmarshal(raw, &offer); err != nil {
				return fmt.Errorf("decode FILE_OFFER: %w", err)
			}
			if err := receiveFile(conn, offer, downloadDir); err != nil {
				return err
			}

		case protocol.MsgSessionDone:
			ui.Successf("Transfer session complete.")
			return nil

		case protocol.MsgError:
			var msg protocol.ErrorMsg
			json.Unmarshal(raw, &msg)
			return fmt.Errorf("sender error: %s", msg.Message)

		default:
			return fmt.Errorf("unexpected message at session level: %s", msgType)
		}
	}
}

func receiveFile(conn *handshake.Conn, offer protocol.FileOfferMsg, downloadDir string) error {
	safeName := sanitizePath(offer.Name)
	destPath := filepath.Join(downloadDir, safeName)
	partPath := destPath + ".part"

	// Check for an existing partial download to offer resume.
	resumeFrom := int64(0)
	if fi, err := os.Stat(partPath); err == nil {
		// Align resume to a chunk boundary so the sender can seek cleanly.
		aligned := (fi.Size() / int64(offer.ChunkSize)) * int64(offer.ChunkSize)
		if aligned > 0 {
			resumeFrom = aligned
		}
	}

	// Prompt user.
	if !ui.FileOfferPrompt(offer.Name, offer.Size) {
		return conn.SendJSON(protocol.FileRejectMsg{
			Type:   protocol.MsgFileReject,
			Reason: "user declined",
		})
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		conn.SendJSON(protocol.FileRejectMsg{Type: protocol.MsgFileReject, Reason: err.Error()})
		return err
	}

	// Accept and tell sender where to resume from.
	if err := conn.SendJSON(protocol.FileAcceptMsg{
		Type:       protocol.MsgFileAccept,
		ResumeFrom: resumeFrom,
	}); err != nil {
		return fmt.Errorf("send FILE_ACCEPT: %w", err)
	}

	flags := os.O_CREATE | os.O_WRONLY
	if resumeFrom > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(partPath, flags, 0o644)
	if err != nil {
		return fmt.Errorf("open part file: %w", err)
	}

	// Compute SHA-256 from byte 0 — hash the already-received portion first.
	h := sha256.New()
	if resumeFrom > 0 {
		existing, err := os.Open(partPath)
		if err == nil {
			io.CopyN(h, existing, resumeFrom)
			existing.Close()
		}
	}

	prog := ui.NewProgress(offer.Name, offer.Size)
	prog.Add(resumeFrom)

	received := resumeFrom
	for received < offer.Size {
		// Expect CHUNK_HEADER (JSON text frame).
		msgType, headerRaw, isBin, err := conn.ReadFrame()
		if err != nil {
			f.Close()
			return fmt.Errorf("read CHUNK_HEADER: %w", err)
		}
		if isBin {
			f.Close()
			return fmt.Errorf("expected CHUNK_HEADER JSON, got binary frame")
		}
		if msgType != protocol.MsgChunkHeader {
			f.Close()
			return fmt.Errorf("expected CHUNK_HEADER, got %s", msgType)
		}
		var header protocol.ChunkHeaderMsg
		if err := json.Unmarshal(headerRaw, &header); err != nil {
			f.Close()
			return fmt.Errorf("decode CHUNK_HEADER: %w", err)
		}

		// Expect binary data frame immediately after.
		_, chunkData, isBin, err := conn.ReadFrame()
		if err != nil {
			f.Close()
			return fmt.Errorf("read chunk %d data: %w", header.Index, err)
		}
		if !isBin {
			f.Close()
			return fmt.Errorf("expected binary chunk, got text frame")
		}
		if len(chunkData) != header.Size {
			f.Close()
			return fmt.Errorf("chunk %d: size mismatch (header=%d actual=%d)",
				header.Index, header.Size, len(chunkData))
		}

		if _, err := f.Write(chunkData); err != nil {
			f.Close()
			return fmt.Errorf("write chunk %d: %w", header.Index, err)
		}
		h.Write(chunkData)
		received += int64(header.Size)
		prog.Add(int64(header.Size))

		// Acknowledge chunk.
		if err := conn.SendJSON(protocol.ChunkAckMsg{
			Type:  protocol.MsgChunkAck,
			Index: header.Index,
		}); err != nil {
			f.Close()
			return fmt.Errorf("send CHUNK_ACK: %w", err)
		}
	}

	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync: %w", err)
	}
	f.Close()
	prog.Done()

	// Read COMPLETE from sender.
	msgType, raw, _, err := conn.ReadFrame()
	if err != nil {
		return fmt.Errorf("read COMPLETE: %w", err)
	}
	if msgType != protocol.MsgComplete {
		return fmt.Errorf("expected COMPLETE, got %s", msgType)
	}
	var complete protocol.CompleteMsg
	json.Unmarshal(raw, &complete)

	ourSHA := hex.EncodeToString(h.Sum(nil))
	ok := ourSHA == complete.SHA256

	ack := protocol.CompleteAckMsg{
		Type:   protocol.MsgCompleteAck,
		OK:     ok,
		SHA256: ourSHA,
	}
	if err := conn.SendJSON(ack); err != nil {
		return fmt.Errorf("send COMPLETE_ACK: %w", err)
	}

	if !ok {
		os.Remove(partPath)
		return fmt.Errorf("checksum mismatch — expected %s got %s", complete.SHA256, ourSHA)
	}

	// Atomically promote partial file to final destination.
	// Remove existing destination if present.
	os.Remove(destPath)
	if err := os.Rename(partPath, destPath); err != nil {
		return fmt.Errorf("rename to final destination: %w", err)
	}

	ui.Successf("Received: %s  →  %s", offer.Name, destPath)
	return nil
}

// sanitizePath converts a protocol-level (forward-slash, relative) path to a
// safe OS path, stripping any directory traversal components.
func sanitizePath(name string) string {
	cleaned := path.Clean("/" + strings.ReplaceAll(name, `\`, "/"))
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" || cleaned == "." {
		return "received_file"
	}
	return filepath.FromSlash(cleaned)
}
