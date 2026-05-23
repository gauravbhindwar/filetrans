package config

import (
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration. Every value has a built-in default
// that can be overridden via environment variable or CLI flag.
type Config struct {
	// USB-link network identities — auto-detected if not overridden.
	LinuxIP      string
	WindowsIP    string
	SubnetPrefix int
	Port         int // 0 = auto-detect a free port
	UIPort       int // Web GUI port (0 = auto-detect)

	// Interface detection
	PollInterval time.Duration
	WatchNames   []string
	USBTimeout   time.Duration

	// File transfer
	ChunkSize   int
	DownloadDir string

	// Fallback / manual
	ScanSubnets []string
	PeerIP      string // manual override — skips detection
	NoUSB       bool   // skip USB detection entirely

	// Role
	Role string // "auto" | "sender" | "receiver"

	// Logging
	JSONLogs bool
	LogLevel string
}

func defaults() Config {
	home, _ := os.UserHomeDir()
	localIP, remoteIP := detectLinkIPs()
	return Config{
		LinuxIP:      localIP,
		WindowsIP:    remoteIP,
		SubnetPrefix: 24,
		Port:         0, // auto-detect
		UIPort:       0, // auto-detect
		PollInterval: 2 * time.Second,
		WatchNames:   []string{"usb0", "rndis0", "usb1", "enp0s20f0u1"},
		USBTimeout:   15 * time.Second,
		ChunkSize:    4 << 20, // 4 MiB — good for large files
		DownloadDir:  filepath.Join(home, "Downloads", "filetrans"),
		ScanSubnets: []string{
			"192.168.0.0/24",
			"192.168.1.0/24",
			"10.0.0.0/24",
		},
		Role:     "auto",
		LogLevel: "info",
	}
}

// detectLinkIPs guesses the USB-link IPs from existing interface addresses.
// Falls back to conventional 192.168.7.x defaults.
func detectLinkIPs() (localIP, remoteIP string) {
	// Look for any non-loopback, non-docker interface with a /24 or /30 address.
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		// Skip virtual/docker/bridge interfaces.
		name := strings.ToLower(iface.Name)
		if strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") ||
			strings.HasPrefix(name, "virbr") || strings.HasPrefix(name, "veth") {
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
				continue // skip wide subnets (LAN)
			}
			// Found a candidate link-local-ish interface.
			localStr := ip4.String()
			// Derive remote IP by flipping last octet (1↔2, 101↔102, etc.).
			remote := make(net.IP, 4)
			copy(remote, ip4)
			if remote[3] == 1 {
				remote[3] = 2
			} else {
				remote[3] = 1
			}
			return localStr, remote.String()
		}
	}
	// Fallback defaults.
	if runtime.GOOS == "linux" {
		return "192.168.7.1", "192.168.7.2"
	}
	return "192.168.7.2", "192.168.7.1"
}

// Parse builds Config from CLI flags then environment variables then defaults.
func Parse() *Config {
	cfg := defaults()

	flag.StringVar(&cfg.LinuxIP, "linux-ip",
		envStr("FILETRANS_LINUX_IP", cfg.LinuxIP),
		"Static IP for Linux side of USB link (auto-detected if not set)")
	flag.StringVar(&cfg.WindowsIP, "windows-ip",
		envStr("FILETRANS_WINDOWS_IP", cfg.WindowsIP),
		"Static IP for Windows side of USB link (auto-detected if not set)")
	flag.IntVar(&cfg.SubnetPrefix, "subnet",
		envInt("FILETRANS_SUBNET", cfg.SubnetPrefix),
		"Subnet prefix length (e.g. 24 → /24)")
	flag.IntVar(&cfg.Port, "port",
		envInt("FILETRANS_PORT", cfg.Port),
		"TCP port for transfer server (0 = auto-detect free port)")
	flag.IntVar(&cfg.UIPort, "ui-port",
		envInt("FILETRANS_UI_PORT", cfg.UIPort),
		"TCP port for the web GUI (0 = auto-detect free port)")
	flag.IntVar(&cfg.ChunkSize, "chunk-size",
		envInt("FILETRANS_CHUNK_SIZE", cfg.ChunkSize),
		"File transfer chunk size in bytes (default 4 MiB)")
	flag.StringVar(&cfg.DownloadDir, "download-dir",
		envStr("FILETRANS_DOWNLOAD_DIR", cfg.DownloadDir),
		"Directory to save received files")
	flag.StringVar(&cfg.Role, "role",
		envStr("FILETRANS_ROLE", cfg.Role),
		"Role override: auto, sender, receiver")
	flag.StringVar(&cfg.PeerIP, "peer",
		envStr("FILETRANS_PEER", ""),
		"Peer IP address — skips USB detection, connects directly")
	flag.BoolVar(&cfg.NoUSB, "no-usb", false,
		"Skip USB detection, fall back to network mode immediately")
	flag.BoolVar(&cfg.JSONLogs, "json-logs", false,
		"Emit structured JSON log lines instead of human-readable output")
	flag.StringVar(&cfg.LogLevel, "log-level",
		envStr("FILETRANS_LOG_LEVEL", cfg.LogLevel),
		"Log verbosity: debug, info, warn, error")
	flag.Parse()

	if v := os.Getenv("FILETRANS_WATCH_NAMES"); v != "" {
		cfg.WatchNames = strings.Split(v, ",")
	}

	// Resolve port 0 → first free port in preferred range.
	if cfg.Port == 0 {
		cfg.Port = findFreePort(7070, 7090)
	}
	if cfg.UIPort == 0 {
		cfg.UIPort = findFreePort(cfg.Port+1, cfg.Port+20)
	}

	return &cfg
}

// findFreePort returns the first TCP port that is free in [start, end].
// Falls back to an OS-assigned port if none in range are free.
func findFreePort(start, end int) int {
	for p := start; p <= end; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", p))
		if err == nil {
			ln.Close()
			return p
		}
	}
	// Let OS pick one.
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return start
	}
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return p
}

// LocalIP is the IP this machine should bind to based on OS.
func (c *Config) LocalIP() string {
	if runtime.GOOS == "linux" {
		return c.LinuxIP
	}
	return c.WindowsIP
}

// RemoteIP is the peer's expected IP. Manual --peer flag takes precedence.
func (c *Config) RemoteIP() string {
	if c.PeerIP != "" {
		return c.PeerIP
	}
	if runtime.GOOS == "linux" {
		return c.WindowsIP
	}
	return c.LinuxIP
}

// LocalCIDR returns local IP in CIDR notation (e.g. "192.168.7.1/24").
func (c *Config) LocalCIDR() string {
	return fmt.Sprintf("%s/%d", c.LocalIP(), c.SubnetPrefix)
}

// ServerAddr is the address the transfer WebSocket server binds to.
func (c *Config) ServerAddr() string {
	return fmt.Sprintf("%s:%d", c.LocalIP(), c.Port)
}

// PeerWSURL is the WebSocket URL to dial on the peer (legacy WebSocket transport).
func (c *Config) PeerWSURL() string {
	return fmt.Sprintf("ws://%s:%d/ws", c.RemoteIP(), c.Port)
}

// PeerAddr is the TCP address for the GauravTransfer Protocol (GTP) transport.
func (c *Config) PeerAddr() string {
	return fmt.Sprintf("%s:%d", c.RemoteIP(), c.Port)
}

// SubnetMask converts the prefix length to dotted-decimal notation.
func (c *Config) SubnetMask() string {
	masks := map[int]string{
		8: "255.0.0.0", 16: "255.255.0.0",
		24: "255.255.255.0", 30: "255.255.255.252",
	}
	if m, ok := masks[c.SubnetPrefix]; ok {
		return m
	}
	return "255.255.255.0"
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
