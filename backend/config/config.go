package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration. Every value has a built-in default
// that can be overridden via environment variable or CLI flag — nothing is
// hardcoded in business logic.
type Config struct {
	// USB-link network identities
	LinuxIP      string
	WindowsIP    string
	SubnetPrefix int
	Port         int

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
	return Config{
		LinuxIP:      "192.168.7.1",
		WindowsIP:    "192.168.7.2",
		SubnetPrefix: 24,
		Port:         7070,
		PollInterval: 2 * time.Second,
		WatchNames:   []string{"usb0", "rndis0", "usb1"},
		USBTimeout:   15 * time.Second,
		ChunkSize:    1 << 20, // 1 MiB
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

// Parse builds Config from CLI flags then environment variables then defaults.
// Call after flag.Parse is not yet called; Parse calls it internally.
func Parse() *Config {
	cfg := defaults()

	flag.StringVar(&cfg.LinuxIP, "linux-ip",
		envStr("FILETRANS_LINUX_IP", cfg.LinuxIP),
		"Static IP for Linux side of USB link")
	flag.StringVar(&cfg.WindowsIP, "windows-ip",
		envStr("FILETRANS_WINDOWS_IP", cfg.WindowsIP),
		"Static IP for Windows side of USB link")
	flag.IntVar(&cfg.SubnetPrefix, "subnet",
		envInt("FILETRANS_SUBNET", cfg.SubnetPrefix),
		"Subnet prefix length (e.g. 24 → /24)")
	flag.IntVar(&cfg.Port, "port",
		envInt("FILETRANS_PORT", cfg.Port),
		"TCP port for WebSocket server")
	flag.IntVar(&cfg.ChunkSize, "chunk-size",
		envInt("FILETRANS_CHUNK_SIZE", cfg.ChunkSize),
		"File transfer chunk size in bytes (default 1 MiB)")
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

	// Comma-separated watch names from env
	if v := os.Getenv("FILETRANS_WATCH_NAMES"); v != "" {
		cfg.WatchNames = strings.Split(v, ",")
	}

	return &cfg
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

// ServerAddr is the address the WebSocket server binds to.
func (c *Config) ServerAddr() string {
	return fmt.Sprintf("%s:%d", c.LocalIP(), c.Port)
}

// PeerWSURL is the WebSocket URL to dial on the peer.
func (c *Config) PeerWSURL() string {
	return fmt.Sprintf("ws://%s:%d/ws", c.RemoteIP(), c.Port)
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
