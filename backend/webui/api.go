package webui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filetrans/backend/fallback"
	"filetrans/backend/gtp"
	"filetrans/backend/gtp/discovery"
	"filetrans/backend/transfer"
	"filetrans/backend/ui"
)

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// handleState returns current state snapshot as JSON.
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.state.snapshot())
}

// handleSetRole sets sender/receiver role.
func (s *Server) handleSetRole(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Role != "sender" && body.Role != "receiver" {
		writeError(w, "role must be sender or receiver", http.StatusBadRequest)
		return
	}
	s.state.mu.Lock()
	s.state.Role = body.Role
	s.state.mu.Unlock()
	writeJSON(w, map[string]string{"ok": "1"})
}

// handleSetPeer sets manual peer IP and optionally scans.
func (s *Server) handleSetPeer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		PeerIP string `json:"peer_ip"`
		Port   int    `json:"port"`
		Scan   bool   `json:"scan"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	if body.Scan {
		peers, err := fallback.Scan(s.cfg)
		if err != nil || len(peers) == 0 {
			writeJSON(w, map[string]interface{}{"peers": []string{}})
			return
		}
		writeJSON(w, map[string]interface{}{"peers": peers})
		return
	}

	if body.PeerIP == "" {
		writeError(w, "peer_ip required", http.StatusBadRequest)
		return
	}
	s.cfg.PeerIP = body.PeerIP
	if body.Port > 0 {
		s.cfg.Port = body.Port
	}
	s.state.mu.Lock()
	s.state.PeerIP = body.PeerIP
	s.state.Port = s.cfg.Port
	s.state.Phase = PhaseConnected
	s.state.Message = fmt.Sprintf("Peer set: %s", body.PeerIP)
	s.state.mu.Unlock()
	writeJSON(w, map[string]string{"ok": "1"})
}

// handleSelectFiles opens a native file dialog via zenity (or fallback).
func (s *Server) handleSelectFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	files := ui.SelectFiles()
	s.state.mu.Lock()
	s.state.SelectedFiles = files
	s.state.mu.Unlock()
	writeJSON(w, map[string]interface{}{"files": files})
}

// handleSelectDir opens a native directory dialog.
func (s *Server) handleSelectDir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	dir := ui.SelectDownloadDir(s.cfg.DownloadDir)
	s.cfg.DownloadDir = dir
	writeJSON(w, map[string]interface{}{"dir": dir})
}

// handleUpload receives files dropped in the browser and queues them.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	r.ParseMultipartForm(4 << 30) // up to 4 GiB in memory hint
	tmpDir := filepath.Join(os.TempDir(), "filetrans-upload")
	os.MkdirAll(tmpDir, 0o755)

	var saved []string
	for _, headers := range r.MultipartForm.File {
		for _, header := range headers {
			f, err := header.Open()
			if err != nil {
				continue
			}
			dest := filepath.Join(tmpDir, filepath.Base(header.Filename))
			out, err := os.Create(dest)
			if err != nil {
				f.Close()
				continue
			}
			io.Copy(out, f)
			out.Close()
			f.Close()
			saved = append(saved, dest)
		}
	}

	s.state.mu.Lock()
	s.state.SelectedFiles = append(s.state.SelectedFiles, saved...)
	s.state.mu.Unlock()
	writeJSON(w, map[string]interface{}{"files": saved})
}

// handleStart kicks off a transfer session in a goroutine.
func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	s.state.mu.RLock()
	role := s.state.Role
	files := append([]string(nil), s.state.SelectedFiles...)
	s.state.mu.RUnlock()

	if role == "" {
		writeError(w, "role not set", http.StatusBadRequest)
		return
	}

	// Reset progress.
	s.state.mu.Lock()
	s.state.Files = make(map[string]*FileProgress)
	s.state.SessionDone = false
	s.state.Phase = PhaseTransfer
	s.state.Message = "Connecting..."
	s.state.mu.Unlock()

	cb := s.state.callbacks()

	go func() {
		if role == "sender" {
			if len(files) == 0 {
				s.state.setPhase(PhaseConnected, "No files selected")
				return
			}
			conn, err := gtp.Connect(s.cfg, "sender")
			if err != nil {
				s.state.setPhase(PhaseConnected, fmt.Sprintf("Connect failed: %v", err))
				return
			}
			defer conn.Close()
			s.state.setPhase(PhaseTransfer, "Sending files...")
			baseDir := transfer.CommonBaseDir(files)
			if err := gtp.Send(conn, files, s.cfg, baseDir, cb); err != nil {
				s.state.setPhase(PhaseConnected, fmt.Sprintf("Send error: %v", err))
			}
		} else {
			if err := os.MkdirAll(s.cfg.DownloadDir, 0o755); err != nil {
				s.state.setPhase(PhaseConnected, fmt.Sprintf("Cannot create download dir: %v", err))
				return
			}
			s.state.setPhase(PhaseTransfer, fmt.Sprintf("Listening on :%d...", s.cfg.Port))
			conn, err := gtp.ListenAll(s.cfg.Port, "receiver")
			if err != nil {
				s.state.setPhase(PhaseConnected, fmt.Sprintf("Listen failed: %v", err))
				return
			}
			defer conn.Close()
			s.state.setPhase(PhaseTransfer, "Receiving files...")
			if err := gtp.Receive(conn, s.cfg.DownloadDir, cb); err != nil {
				s.state.setPhase(PhaseConnected, fmt.Sprintf("Receive error: %v", err))
			}
		}
	}()

	writeJSON(w, map[string]string{"ok": "1"})
}

// handleSelectFolder opens a native directory picker and adds all files inside to the queue.
func (s *Server) handleSelectFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	dir := ui.SelectDownloadDir("") // reuse the dir-picker prompt
	if dir == "" {
		writeJSON(w, map[string]interface{}{"files": []string{}})
		return
	}
	var files []string
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	s.state.mu.Lock()
	s.state.SelectedFiles = append(s.state.SelectedFiles, files...)
	s.state.mu.Unlock()
	writeJSON(w, map[string]interface{}{"files": files})
}

// handleSetNativeFiles registers file paths chosen via the native dialog.
func (s *Server) handleSetNativeFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Files []string `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Files) == 0 {
		writeJSON(w, map[string]string{"ok": "1"})
		return
	}
	s.state.mu.Lock()
	// Merge: avoid duplicates.
	existing := make(map[string]struct{}, len(s.state.SelectedFiles))
	for _, f := range s.state.SelectedFiles {
		existing[f] = struct{}{}
	}
	for _, f := range body.Files {
		if _, dup := existing[f]; !dup {
			s.state.SelectedFiles = append(s.state.SelectedFiles, f)
		}
	}
	s.state.mu.Unlock()
	writeJSON(w, map[string]string{"ok": "1"})
}

// handleResetFiles clears only the file queue without resetting transfer state.
func (s *Server) handleResetFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	s.state.mu.Lock()
	s.state.SelectedFiles = nil
	s.state.mu.Unlock()
	// Remove temp uploads.
	tmpDir := filepath.Join(os.TempDir(), "filetrans-upload")
	entries, _ := os.ReadDir(tmpDir)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			os.Remove(filepath.Join(tmpDir, e.Name()))
		}
	}
	writeJSON(w, map[string]string{"ok": "1"})
}

// handleReset clears state back to connected/idle.
func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	s.state.mu.Lock()
	s.state.Files = make(map[string]*FileProgress)
	s.state.SessionDone = false
	s.state.SelectedFiles = nil
	phase := s.state.Phase
	if phase == PhaseDone || phase == PhaseTransfer {
		s.state.Phase = PhaseConnected
	}
	s.state.Message = ""

	// Remove temp uploaded files.
	tmpDir := filepath.Join(os.TempDir(), "filetrans-upload")
	entries, _ := os.ReadDir(tmpDir)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			os.Remove(filepath.Join(tmpDir, e.Name()))
		}
	}
	s.state.mu.Unlock()
	writeJSON(w, map[string]string{"ok": "1"})
}

// handleDiscover uses GTP mDNS to find peers on the LAN.
func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	devID := s.cfg.PeerIP // reuse field for device identity
	peers, err := discovery.Scan(s.cfg.Port, devID, 3*time.Second)
	if err != nil || len(peers) == 0 {
		writeJSON(w, map[string]interface{}{"peers": []string{}})
		return
	}
	ips := make([]string, len(peers))
	for i, p := range peers {
		ips[i] = p.IP
	}
	writeJSON(w, map[string]interface{}{"peers": ips})
}
