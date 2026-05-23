package webui

import (
	"net"
	"runtime"
	"sync"

	"filetrans/backend/transfer"
)

type AppPhase string

const (
	PhaseIdle       AppPhase = "idle"
	PhaseWaitingUSB AppPhase = "waiting_usb"
	PhaseConnecting AppPhase = "connecting"
	PhaseConnected  AppPhase = "connected"
	PhaseTransfer   AppPhase = "transfer"
	PhaseDone       AppPhase = "done"
)

type FileProgress struct {
	Name        string  `json:"name"`
	Size        int64   `json:"size"`
	Transferred int64   `json:"transferred"`
	Speed       float64 `json:"speed"`
	ETASec      float64 `json:"eta_sec"`
	Done        bool    `json:"done"`
	Error       string  `json:"error,omitempty"`
}

type AppState struct {
	mu sync.RWMutex

	Phase           AppPhase `json:"phase"`
	Role            string   `json:"role"`
	PeerIP          string   `json:"peer_ip"`
	LocalIP         string   `json:"local_ip"`
	SuggestedPeerIP string   `json:"suggested_peer_ip"`
	Port            int      `json:"port"`
	DownloadDir     string   `json:"download_dir"`
	Message         string   `json:"message"`

	SelectedFiles []string `json:"selected_files"`

	Files       map[string]*FileProgress `json:"files"`
	SessionDone bool                     `json:"session_done"`
}

func newState(port int, downloadDir string) *AppState {
	local, suggested := detectLocalAndPeer()
	return &AppState{
		Phase:           PhaseIdle,
		Files:           make(map[string]*FileProgress),
		LocalIP:         local,
		SuggestedPeerIP: suggested,
		Port:            port,
		DownloadDir:     downloadDir,
	}
}

// detectLocalAndPeer returns the best local IP for a USB link interface
// and the likely peer IP.
func detectLocalAndPeer() (localIP, peerIP string) {
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipNet.IP.To4()
			if ip4 == nil {
				continue
			}
			ones, _ := ipNet.Mask.Size()
			if ones < 24 {
				continue
			}
			local := ip4.String()
			remote := make(net.IP, 4)
			copy(remote, ip4)
			if remote[3] == 1 {
				remote[3] = 2
			} else {
				remote[3] = 1
			}
			return local, remote.String()
		}
	}
	if runtime.GOOS == "linux" {
		return "192.168.7.1", "192.168.7.2"
	}
	return "192.168.7.2", "192.168.7.1"
}

// StateSnapshot is a mutex-free copy of AppState for JSON broadcast.
type StateSnapshot struct {
	Phase           AppPhase                 `json:"phase"`
	Role            string                   `json:"role"`
	PeerIP          string                   `json:"peer_ip"`
	LocalIP         string                   `json:"local_ip"`
	SuggestedPeerIP string                   `json:"suggested_peer_ip"`
	Port            int                      `json:"port"`
	DownloadDir     string                   `json:"download_dir"`
	Message         string                   `json:"message"`
	SelectedFiles   []string                 `json:"selected_files"`
	Files           map[string]*FileProgress `json:"files"`
	SessionDone     bool                     `json:"session_done"`
}

func (s *AppState) snapshot() StateSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	files := make(map[string]*FileProgress, len(s.Files))
	for k, v := range s.Files {
		fp := *v
		files[k] = &fp
	}
	return StateSnapshot{
		Phase:           s.Phase,
		Role:            s.Role,
		PeerIP:          s.PeerIP,
		LocalIP:         s.LocalIP,
		SuggestedPeerIP: s.SuggestedPeerIP,
		Port:            s.Port,
		DownloadDir:     s.DownloadDir,
		Message:         s.Message,
		SelectedFiles:   append([]string(nil), s.SelectedFiles...),
		Files:           files,
		SessionDone:     s.SessionDone,
	}
}

func (s *AppState) setPhase(phase AppPhase, msg string) {
	s.mu.Lock()
	s.Phase = phase
	s.Message = msg
	s.mu.Unlock()
}

func (s *AppState) callbacks() *transfer.Callbacks {
	return &transfer.Callbacks{
		OnFileStart: func(name string, size int64) {
			s.mu.Lock()
			s.Files[name] = &FileProgress{Name: name, Size: size}
			s.mu.Unlock()
		},
		OnProgress: func(name string, transferred int64, speed, etaSec float64) {
			s.mu.Lock()
			if fp, ok := s.Files[name]; ok {
				fp.Transferred = transferred
				fp.Speed = speed
				fp.ETASec = etaSec
			}
			s.mu.Unlock()
		},
		OnFileComplete: func(name string) {
			s.mu.Lock()
			if fp, ok := s.Files[name]; ok {
				fp.Done = true
				fp.Transferred = fp.Size
			}
			s.mu.Unlock()
		},
		OnFileError: func(name string, err error) {
			s.mu.Lock()
			if fp, ok := s.Files[name]; ok {
				fp.Error = err.Error()
			}
			s.mu.Unlock()
		},
		OnSessionDone: func() {
			s.mu.Lock()
			s.SessionDone = true
			s.Phase = PhaseDone
			s.Message = "Transfer complete"
			s.mu.Unlock()
		},
	}
}
