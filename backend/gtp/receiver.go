package gtp

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"filetrans/backend/transfer"
	"filetrans/backend/ui"
)

// Receive handles the receiver-side GTP session loop.
// cb may be nil — terminal progress bar used in that case.
func Receive(conn *Conn, downloadDir string, cb *transfer.Callbacks) error {
	if err := os.MkdirAll(downloadDir, 0o755); err != nil {
		return fmt.Errorf("create download dir: %w", err)
	}

	for {
		ft, payload, err := conn.ReadAny()
		if err != nil {
			return fmt.Errorf("gtp receive: %w", err)
		}
		switch ft {
		case FrameFileOffer:
			var offer FileOfferMsg
			if err := json.Unmarshal(payload, &offer); err != nil {
				return fmt.Errorf("gtp decode FILE_OFFER: %w", err)
			}
			if err := receiveFile(conn, offer, downloadDir, cb); err != nil {
				return err
			}

		case FrameSessionDone:
			var done SessionDoneMsg
			json.Unmarshal(payload, &done)
			cb.SessionDone()
			if cb == nil {
				ui.Successf("Transfer session complete. %d file(s), %s total.",
					done.FilesCount, ui.FormatBytes(done.TotalBytes))
			}
			return nil

		case FrameError:
			var em ErrorMsg
			json.Unmarshal(payload, &em)
			return fmt.Errorf("gtp sender error %d: %s", em.Code, em.Message)

		default:
			return fmt.Errorf("gtp: unexpected frame at session level: %s", ft)
		}
	}
}

func receiveFile(conn *Conn, offer FileOfferMsg, downloadDir string, cb *transfer.Callbacks) error {
	safeName := sanitizePath(offer.Name)
	destPath := filepath.Join(downloadDir, safeName)
	partPath := destPath + ".gtpart"

	// Resume: check existing partial file.
	resumeChunk := int64(0)
	if fi, err := os.Stat(partPath); err == nil && offer.ChunkSize > 0 {
		aligned := (fi.Size() / int64(offer.ChunkSize)) * int64(offer.ChunkSize)
		resumeChunk = aligned / int64(offer.ChunkSize)
	}

	// Auto-accept in GUI mode (cb != nil); prompt in terminal mode.
	if cb == nil {
		if !ui.FileOfferPrompt(offer.Name, offer.Size) {
			return conn.SendControl(FrameFileReject, FileRejectMsg{
				ID:     offer.ID,
				Reason: "user declined",
			})
		}
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		conn.SendControl(FrameFileReject, FileRejectMsg{ID: offer.ID, Reason: err.Error()})
		return err
	}

	if err := conn.SendControl(FrameFileAccept, FileAcceptMsg{
		ID:          offer.ID,
		ResumeChunk: resumeChunk,
	}); err != nil {
		return fmt.Errorf("gtp: send FILE_ACCEPT: %w", err)
	}

	flags := os.O_CREATE | os.O_WRONLY
	if resumeChunk > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(partPath, flags, 0o644)
	if err != nil {
		return fmt.Errorf("gtp: open part file: %w", err)
	}

	var prog *ui.Progress
	if cb == nil {
		prog = ui.NewProgress(offer.Name, offer.Size)
		prog.Add(resumeChunk * int64(offer.ChunkSize))
	} else {
		cb.FileStart(offer.Name, offer.Size)
	}

	startTime := time.Now()
	received := resumeChunk * int64(offer.ChunkSize)
	expectedChunk := resumeChunk

	// Receive chunks; send DATA_ACK after each.
	for received < offer.Size {
		dataMsg, chunk, err := conn.ReadData()
		if err != nil {
			f.Close()
			// Send NACK so sender knows to retransmit.
			conn.SendControl(FrameDataAck, DataAckMsg{
				FileID:     offer.ID,
				ChunkIndex: expectedChunk,
				OK:         false,
			})
			return fmt.Errorf("gtp: read DATA: %w", err)
		}

		if dataMsg.ChunkIndex != expectedChunk {
			f.Close()
			return fmt.Errorf("gtp: out-of-order chunk: want %d got %d", expectedChunk, dataMsg.ChunkIndex)
		}

		if _, err := f.Write(chunk); err != nil {
			f.Close()
			conn.SendControl(FrameDataAck, DataAckMsg{FileID: offer.ID, ChunkIndex: expectedChunk, OK: false})
			return fmt.Errorf("gtp: write chunk %d: %w", expectedChunk, err)
		}

		// ACK this chunk.
		if err := conn.SendControl(FrameDataAck, DataAckMsg{
			FileID:     offer.ID,
			ChunkIndex: expectedChunk,
			OK:         true,
		}); err != nil {
			f.Close()
			return fmt.Errorf("gtp: send DATA_ACK: %w", err)
		}

		received += int64(len(chunk))
		expectedChunk++

		if prog != nil {
			prog.Add(int64(len(chunk)))
		} else {
			elapsed := time.Since(startTime).Seconds()
			var speed, etaSec float64
			if elapsed > 0 {
				speed = float64(received) / elapsed
				if speed > 0 {
					etaSec = float64(offer.Size-received) / speed
				}
			}
			cb.Progress(offer.Name, received, speed, etaSec)
		}
	}

	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("gtp: sync: %w", err)
	}
	f.Close()
	if prog != nil {
		prog.Done()
	}

	// Read COMPLETE from sender.
	var complete CompleteMsg
	if err := conn.ReadControl(FrameComplete, &complete); err != nil {
		return err
	}

	// Verify hash.
	ourHash, err := hashFile(partPath)
	if err != nil {
		return fmt.Errorf("gtp: hash part file: %w", err)
	}

	ackOK := ourHash == complete.Blake3
	if err := conn.SendControl(FrameCompleteAck, CompleteAckMsg{
		FileID: offer.ID,
		OK:     ackOK,
		Blake3: ourHash,
	}); err != nil {
		return fmt.Errorf("gtp: send COMPLETE_ACK: %w", err)
	}

	if !ackOK {
		os.Remove(partPath)
		return fmt.Errorf("gtp: checksum mismatch — expected %s got %s", complete.Blake3, ourHash)
	}

	// Atomically promote partial → final.
	os.Remove(destPath)
	if err := os.Rename(partPath, destPath); err != nil {
		return fmt.Errorf("gtp: rename to final: %w", err)
	}

	cb.FileComplete(offer.Name)
	if cb == nil {
		ui.Successf("Received: %s  →  %s", offer.Name, destPath)
	}
	return nil
}

func sanitizePath(name string) string {
	cleaned := path.Clean("/" + strings.ReplaceAll(name, `\`, "/"))
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" || cleaned == "." {
		return "received_file"
	}
	return filepath.FromSlash(cleaned)
}
