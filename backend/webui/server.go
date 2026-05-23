// Package webui provides a browser-based GUI for filetrans.
package webui

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"filetrans/backend/config"
	"filetrans/backend/detector"
	"filetrans/backend/logger"
	"filetrans/backend/netconfig"

	"github.com/gorilla/websocket"
)

//go:embed static
var staticFiles embed.FS

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  32 * 1024,
	WriteBufferSize: 32 * 1024,
	CheckOrigin:    func(r *http.Request) bool { return true },
}

// Server is the web GUI server.
type Server struct {
	cfg   *config.Config
	state *AppState
	hub   *hub
	srv   *http.Server
}

// New creates a GUI server.
func New(cfg *config.Config) *Server {
	s := &Server{
		cfg:   cfg,
		state: newState(cfg.Port, cfg.DownloadDir),
		hub:   newHub(),
	}

	mux := http.NewServeMux()

	staticFS, _ := fs.Sub(staticFiles, "static")
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	// Event stream for browser.
	mux.HandleFunc("/api/events", s.handleEvents)

	// REST API
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/select-files", s.handleSelectFiles)
	mux.HandleFunc("/api/select-folder", s.handleSelectFolder)
	mux.HandleFunc("/api/select-dir", s.handleSelectDir)
	mux.HandleFunc("/api/set-role", s.handleSetRole)
	mux.HandleFunc("/api/set-peer", s.handleSetPeer)
	mux.HandleFunc("/api/set-native-files", s.handleSetNativeFiles)
	mux.HandleFunc("/api/start", s.handleStart)
	mux.HandleFunc("/api/reset", s.handleReset)
	mux.HandleFunc("/api/reset-files", s.handleResetFiles)
	mux.HandleFunc("/api/upload", s.handleUpload)

	s.srv = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", cfg.UIPort),
		Handler: mux,
	}
	return s
}

// Addr returns the GUI URL.
func (s *Server) Addr() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.cfg.UIPort)
}

// Start launches the HTTP server and USB detector in background goroutines.
func (s *Server) Start(ctx context.Context) error {
	go s.pushLoop(ctx)

	if err := s.startUSBWatcher(ctx); err != nil {
		logger.Info("webui", "", "", "WARN", fmt.Sprintf("USB watcher: %v", err))
	}

	go func() {
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Info("webui", "", "", "ERROR", fmt.Sprintf("HTTP server: %v", err))
		}
	}()
	return nil
}

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s.srv.Shutdown(ctx)
}

// pushLoop periodically broadcasts state to all browser clients.
func (s *Server) pushLoop(ctx context.Context) {
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			snap := s.state.snapshot()
			s.hub.broadcast(snap)
		}
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.hub.add(conn)
	defer s.hub.remove(conn)

	snap := s.state.snapshot()
	conn.WriteJSON(snap)

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (s *Server) startUSBWatcher(ctx context.Context) error {
	det := detector.New()
	detCfg := detector.Config{
		WatchNames:   s.cfg.WatchNames,
		PollInterval: s.cfg.PollInterval,
	}
	events, cancelDet, err := det.Start(detCfg)
	if err != nil {
		return err
	}

	s.state.setPhase(PhaseWaitingUSB, "Waiting for USB-C cable or set peer IP above...")

	go func() {
		defer cancelDet()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				if ev.Added {
					localIP, peerIP := detectLocalAndPeer()
					s.state.mu.Lock()
					s.state.Phase = PhaseConnected
					s.state.PeerIP = s.cfg.RemoteIP()
					s.state.LocalIP = localIP
					s.state.SuggestedPeerIP = peerIP
					s.state.Message = fmt.Sprintf("USB link up: %s ↔ %s", s.cfg.LocalIP(), s.cfg.RemoteIP())
					s.state.mu.Unlock()

					has, _ := netconfig.HasTargetIP(ev.Name, s.cfg)
					if !has {
						netconfig.AssignIP(ev.Name, s.cfg)
					}
				} else {
					s.state.setPhase(PhaseWaitingUSB, fmt.Sprintf("Interface %s removed. Waiting for USB…", ev.Name))
					netconfig.RemoveIP(ev.Name, s.cfg)
				}
			}
		}
	}()
	return nil
}
