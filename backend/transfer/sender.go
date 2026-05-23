// Package transfer implements the filetrans file-transfer engine.
package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filetrans/backend/config"
	"filetrans/backend/handshake"
	"filetrans/backend/protocol"
	"filetrans/backend/ui"
)

// Send transmits all files over conn in order, then sends SESSION_DONE.
// cb may be nil — terminal progress bar is used in that case.
func Send(conn *handshake.Conn, files []string, cfg *config.Config, baseDir string, cb *Callbacks) error {
	for _, path := range files {
		if err := sendFile(conn, path, cfg, baseDir, cb); err != nil {
			cb.fileError(filepath.Base(path), err)
			_ = conn.SendJSON(protocol.ErrorMsg{Type: protocol.MsgError, Message: err.Error()})
			return err
		}
	}
	cb.sessionDone()
	return conn.SendJSON(protocol.SessionDoneMsg{Type: protocol.MsgSessionDone})
}

func sendFile(conn *handshake.Conn, path string, cfg *config.Config, baseDir string, cb *Callbacks) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory (pass individual files)", path)
	}

	relName := relativeName(path, baseDir)
	chunkSize := cfg.ChunkSize
	totalChunks := (info.Size() + int64(chunkSize) - 1) / int64(chunkSize)
	if totalChunks == 0 {
		totalChunks = 1
	}

	offer := protocol.FileOfferMsg{
		Type:        protocol.MsgFileOffer,
		Name:        relName,
		Size:        info.Size(),
		ChunkSize:   chunkSize,
		TotalChunks: totalChunks,
	}
	if err := conn.SendJSON(offer); err != nil {
		return fmt.Errorf("send FILE_OFFER: %w", err)
	}

	msgType, raw, _, err := conn.ReadFrame()
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	switch msgType {
	case protocol.MsgFileReject:
		var msg protocol.FileRejectMsg
		json.Unmarshal(raw, &msg)
		return fmt.Errorf("receiver rejected %s: %s", relName, msg.Reason)
	case protocol.MsgFileAccept:
		var accept protocol.FileAcceptMsg
		json.Unmarshal(raw, &accept)
		cb.fileStart(relName, info.Size())
		return sendChunks(conn, path, relName, info.Size(), accept.ResumeFrom, chunkSize, cb)
	default:
		return fmt.Errorf("unexpected response to FILE_OFFER: %s", msgType)
	}
}

func sendChunks(conn *handshake.Conn, path, relName string, fileSize, resumeFrom int64, chunkSize int, cb *Callbacks) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	// Compute SHA-256 concurrently while sending.
	sha256Ch := make(chan string, 1)
	go func() {
		h := sha256.New()
		fh, err := os.Open(path)
		if err != nil {
			sha256Ch <- ""
			return
		}
		defer fh.Close()
		io.Copy(h, fh)
		sha256Ch <- hex.EncodeToString(h.Sum(nil))
	}()

	if resumeFrom > 0 {
		if _, err := f.Seek(resumeFrom, io.SeekStart); err != nil {
			return fmt.Errorf("seek: %w", err)
		}
	}

	// Terminal progress (used when no GUI callbacks supplied).
	var prog *ui.Progress
	if cb == nil {
		prog = ui.NewProgress(filepath.Base(path), fileSize)
		prog.Add(resumeFrom)
	}

	buf := make([]byte, chunkSize)
	chunkIndex := resumeFrom / int64(chunkSize)
	transferred := resumeFrom
	startTime := time.Now()

	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if err := conn.SendJSON(protocol.ChunkHeaderMsg{
				Type:  protocol.MsgChunkHeader,
				Index: chunkIndex,
				Size:  n,
			}); err != nil {
				return fmt.Errorf("send CHUNK_HEADER: %w", err)
			}
			if err := conn.SendBinary(chunk); err != nil {
				return fmt.Errorf("send chunk %d: %w", chunkIndex, err)
			}
			ackType, ackRaw, _, ackErr := conn.ReadFrame()
			if ackErr != nil {
				return fmt.Errorf("read CHUNK_ACK: %w", ackErr)
			}
			if ackType != protocol.MsgChunkAck {
				var errMsg protocol.ErrorMsg
				json.Unmarshal(ackRaw, &errMsg)
				return fmt.Errorf("expected CHUNK_ACK, got %s: %s", ackType, errMsg.Message)
			}
			transferred += int64(n)
			chunkIndex++

			if prog != nil {
				prog.Add(int64(n))
			} else {
				elapsed := time.Since(startTime).Seconds()
				var speed, etaSec float64
				if elapsed > 0 {
					speed = float64(transferred) / elapsed
					if speed > 0 {
						etaSec = float64(fileSize-transferred) / speed
					}
				}
				cb.progress(relName, transferred, speed, etaSec)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read chunk: %w", readErr)
		}
	}

	if prog != nil {
		prog.Done()
	}

	sha256Sum := <-sha256Ch
	if sha256Sum == "" {
		return fmt.Errorf("checksum computation failed")
	}

	if err := conn.SendJSON(protocol.CompleteMsg{
		Type:   protocol.MsgComplete,
		SHA256: sha256Sum,
	}); err != nil {
		return fmt.Errorf("send COMPLETE: %w", err)
	}

	msgType, ackRaw, _, err := conn.ReadFrame()
	if err != nil {
		return fmt.Errorf("read COMPLETE_ACK: %w", err)
	}
	if msgType != protocol.MsgCompleteAck {
		return fmt.Errorf("expected COMPLETE_ACK, got %s", msgType)
	}
	var ack protocol.CompleteAckMsg
	json.Unmarshal(ackRaw, &ack)
	if !ack.OK {
		return fmt.Errorf("checksum mismatch — sent %s, receiver got %s", sha256Sum, ack.SHA256)
	}

	cb.fileComplete(relName)
	if prog == nil {
		ui.Successf("Sent: %s", filepath.Base(path))
	}
	return nil
}

// relativeName computes a forward-slash relative path for use in FILE_OFFER.
func relativeName(path, baseDir string) string {
	if baseDir != "" {
		rel, err := filepath.Rel(baseDir, path)
		if err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(filepath.Base(path))
}

// CommonBaseDir finds the longest common directory prefix among file paths.
func CommonBaseDir(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	base := filepath.Dir(paths[0])
	for _, p := range paths[1:] {
		d := filepath.Dir(p)
		for !strings.HasPrefix(d, base) && base != "." && base != string(filepath.Separator) {
			base = filepath.Dir(base)
		}
	}
	return base
}
