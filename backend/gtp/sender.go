package gtp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"filetrans/backend/config"
	"filetrans/backend/transfer"
	"filetrans/backend/ui"
)

// Send transmits all files over a GTP connection, then sends SESSION_DONE.
// cb may be nil — terminal progress bar used in that case.
func Send(conn *Conn, files []string, cfg *config.Config, baseDir string, cb *transfer.Callbacks) error {
	var totalBytes int64
	for i, path := range files {
		fileID := uint32(i + 1)
		if err := sendFile(conn, fileID, path, baseDir, cb); err != nil {
			cb.FileError(filepath.Base(path), err)
			conn.SendError(ErrFileAccess, err.Error())
			return err
		}
		if info, err := os.Stat(path); err == nil {
			totalBytes += info.Size()
		}
	}

	done := SessionDoneMsg{FilesCount: len(files), TotalBytes: totalBytes}
	if err := conn.SendControl(FrameSessionDone, done); err != nil {
		return fmt.Errorf("gtp: send SESSION_DONE: %w", err)
	}
	cb.SessionDone()
	return nil
}

func sendFile(conn *Conn, fileID uint32, path, baseDir string, cb *transfer.Callbacks) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory — pass individual files", path)
	}

	relName := relativeName(path, baseDir)
	chunkSize := conn.Window * (1 << 17) // window * 128 KiB per chunk for throughput
	if chunkSize < 1<<17 {
		chunkSize = 1 << 17 // min 128 KiB
	}
	if chunkSize > 16<<20 {
		chunkSize = 16 << 20 // max 16 MiB
	}

	totalChunks := (info.Size() + int64(chunkSize) - 1) / int64(chunkSize)
	if totalChunks == 0 {
		totalChunks = 1
	}

	// Compute BLAKE3 (using SHA-256 as fallback — pure Go, no CGO).
	fileHash, err := hashFile(path)
	if err != nil {
		return fmt.Errorf("hash %s: %w", path, err)
	}

	offer := FileOfferMsg{
		ID:          fileID,
		Name:        relName,
		Size:        info.Size(),
		ChunkSize:   chunkSize,
		TotalChunks: totalChunks,
		Blake3:      fileHash, // using SHA-256 until blake3 dep added
		ModTime:     info.ModTime().Unix(),
		Mode:        uint32(info.Mode()),
	}
	if err := conn.SendControl(FrameFileOffer, offer); err != nil {
		return fmt.Errorf("gtp: send FILE_OFFER: %w", err)
	}

	// Read accept/reject.
	ft, payload, err := conn.ReadAny()
	if err != nil {
		return fmt.Errorf("gtp: read FILE_ACCEPT: %w", err)
	}
	switch ft {
	case FrameFileReject:
		var rej FileRejectMsg
		decodeJSON(payload, &rej)
		return fmt.Errorf("receiver rejected %s: %s", relName, rej.Reason)
	case FrameFileAccept:
		var acc FileAcceptMsg
		decodeJSON(payload, &acc)
		cb.FileStart(relName, info.Size())
		return sendChunks(conn, fileID, path, relName, info.Size(), acc.ResumeChunk, chunkSize, cb)
	default:
		return fmt.Errorf("gtp: unexpected response to FILE_OFFER: %s", ft)
	}
}

func sendChunks(conn *Conn, fileID uint32, path, relName string, fileSize int64, resumeChunk int64, chunkSize int, cb *transfer.Callbacks) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	resumeFrom := resumeChunk * int64(chunkSize)
	if resumeFrom > 0 {
		if _, err := f.Seek(resumeFrom, io.SeekStart); err != nil {
			return fmt.Errorf("seek: %w", err)
		}
	}

	var prog *ui.Progress
	if cb == nil {
		prog = ui.NewProgress(filepath.Base(path), fileSize)
		prog.Add(resumeFrom)
	}

	// Windowed pipeline: keep up to conn.Window chunks in flight.
	window := conn.Window
	if window <= 0 {
		window = 1
	}

	type ackResult struct {
		msg DataAckMsg
		err error
	}
	ackCh := make(chan ackResult, window)

	buf := make([]byte, chunkSize)
	chunkIndex := resumeChunk
	transferred := resumeFrom
	startTime := time.Now()
	inFlight := 0

	// goroutine reading acks
	var ackWg sync.WaitGroup
	var ackErr error
	var ackMu sync.Mutex

	ackReader := func() {
		defer ackWg.Done()
		for {
			ft, payload, err := conn.ReadAny()
			if err != nil {
				ackCh <- ackResult{err: err}
				return
			}
			if ft != FrameDataAck {
				ackCh <- ackResult{err: fmt.Errorf("gtp: expected DATA_ACK got %s", ft)}
				return
			}
			var ack DataAckMsg
			decodeJSON(payload, &ack)
			ackCh <- ackResult{msg: ack}
			if !ack.OK {
				ackCh <- ackResult{err: fmt.Errorf("gtp: CRC fail on chunk %d — receiver requested retransmit", ack.ChunkIndex)}
				return
			}
		}
	}

	ackWg.Add(1)
	go ackReader()

	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])

			msg := DataMsg{
				FileID:     fileID,
				ChunkIndex: chunkIndex,
			}
			if err := conn.SendData(msg, chunk); err != nil {
				ackMu.Lock()
				ackErr = err
				ackMu.Unlock()
				break
			}
			chunkIndex++
			inFlight++
			transferred += int64(n)

			// Drain acks when window full.
			for inFlight >= window {
				ar := <-ackCh
				if ar.err != nil {
					ackMu.Lock()
					ackErr = ar.err
					ackMu.Unlock()
					break
				}
				inFlight--
			}

			ackMu.Lock()
			if ackErr != nil {
				ackMu.Unlock()
				break
			}
			ackMu.Unlock()

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
				cb.Progress(relName, transferred, speed, etaSec)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read: %w", readErr)
		}
	}

	// Drain remaining acks.
	for inFlight > 0 {
		ar := <-ackCh
		if ar.err != nil {
			ackMu.Lock()
			ackErr = ar.err
			ackMu.Unlock()
			break
		}
		inFlight--
	}

	ackMu.Lock()
	err2 := ackErr
	ackMu.Unlock()
	if err2 != nil {
		return err2
	}

	if prog != nil {
		prog.Done()
	}

	// Re-hash and send COMPLETE.
	fileHash, err := hashFile(path)
	if err != nil {
		return fmt.Errorf("hash: %w", err)
	}
	complete := CompleteMsg{FileID: fileID, Blake3: fileHash, Bytes: transferred}
	if err := conn.SendControl(FrameComplete, complete); err != nil {
		return fmt.Errorf("gtp: send COMPLETE: %w", err)
	}

	var ack CompleteAckMsg
	if err := conn.ReadControl(FrameCompleteAck, &ack); err != nil {
		return err
	}
	if !ack.OK {
		return fmt.Errorf("gtp: checksum mismatch — sent %s, receiver got %s", fileHash, ack.Blake3)
	}

	cb.FileComplete(relName)
	if prog == nil {
		ui.Successf("Sent: %s", filepath.Base(path))
	}
	return nil
}

// hashFile computes SHA-256 of a file (used until a pure-Go BLAKE3 dep is added).
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func relativeName(path, baseDir string) string {
	if baseDir != "" {
		rel, err := filepath.Rel(baseDir, path)
		if err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(filepath.Base(path))
}
