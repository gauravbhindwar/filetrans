package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"filetrans/backend/config"
	"filetrans/backend/detector"
	"filetrans/backend/fallback"
	"filetrans/backend/handshake"
	"filetrans/backend/logger"
	"filetrans/backend/netconfig"
	"filetrans/backend/protocol"
	"filetrans/backend/transfer"
	"filetrans/backend/ui"
)

const version = "0.1.0"

func main() {
	for _, arg := range os.Args[1:] {
		if arg == "--version" || arg == "-version" || arg == "version" {
			fmt.Printf("filetrans %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
			return
		}
	}

	cfg := config.Parse()

	if !cfg.JSONLogs {
		ui.Banner(version)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// --peer or --no-usb: bypass USB detection.
	if cfg.PeerIP != "" || cfg.NoUSB {
		if cfg.PeerIP != "" {
			ui.Infof("Direct mode — peer=%s port=%d", cfg.PeerIP, cfg.Port)
		} else {
			ui.Infof("Network mode — scanning for peers (no USB)")
		}
		peerIP := resolveOrScanPeer(cfg)
		if peerIP == "" {
			ui.Errorf("No peer found. Exiting.")
			os.Exit(1)
		}
		cfg.PeerIP = peerIP
		runSession(cfg, "network")
		return
	}

	runUSBMode(cfg, ctx)
}

// runUSBMode watches for USB interfaces and runs transfer sessions.
func runUSBMode(cfg *config.Config, ctx context.Context) {
	det := detector.New()
	detCfg := detector.Config{
		WatchNames:   cfg.WatchNames,
		PollInterval: cfg.PollInterval,
	}
	events, cancelDet, err := det.Start(detCfg)
	if err != nil {
		ui.Errorf("Failed to start interface detector: %v", err)
		os.Exit(1)
	}
	defer cancelDet()

	ui.Infof("Waiting for USB-C cable connection...")
	ui.Infof("Tip: use --no-usb or --peer=<ip> to skip USB detection.")

	usbTimer := time.NewTimer(cfg.USBTimeout)
	defer usbTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			ui.Infof("Shutting down.")
			return

		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Added {
				logger.Info(runtime.GOOS, ev.Name, ev.LinkLocal, "INTERFACE_UP", "USB interface detected")
				usbTimer.Stop() // cancel fallback timer — we have a connection
				handleInterface(cfg, ev)
				// Reset timer for next cable-wait cycle.
				usbTimer.Reset(cfg.USBTimeout)
				ui.Infof("Session ended. Waiting for next USB connection...")
			} else {
				logger.Info(runtime.GOOS, ev.Name, "", "INTERFACE_DOWN", "USB interface removed")
				ui.Warnf("Interface %s removed.", ev.Name)
				_ = netconfig.RemoveIP(ev.Name, cfg)
			}

		case <-usbTimer.C:
			ui.Warnf("No USB connection in %.0fs.", cfg.USBTimeout.Seconds())
			offerNetworkFallback(cfg)
			usbTimer.Reset(cfg.USBTimeout)
		}
	}
}

// handleInterface configures the IP on the new USB interface and runs a session.
func handleInterface(cfg *config.Config, ev detector.InterfaceEvent) {
	has, err := netconfig.HasTargetIP(ev.Name, cfg)
	if err != nil {
		ui.Warnf("IP check: %v — attempting assignment anyway.", err)
	}
	if !has {
		ui.Infof("Configuring %s with %s ...", ev.Name, cfg.LocalIP())
		if err := netconfig.AssignIP(ev.Name, cfg); err != nil {
			ui.Errorf("IP assignment failed: %v", err)
			ui.Warnf("Re-run with elevated privileges (sudo on Linux / Run as Administrator on Windows).")
			return
		}
		logger.Info(runtime.GOOS, ev.Name, cfg.LocalIP(), "IP_ASSIGNED", "static IP configured")
	} else {
		logger.Info(runtime.GOOS, ev.Name, cfg.LocalIP(), "IP_EXISTS", "already configured")
	}
	ui.Successf("Link up: local=%s  peer=%s", cfg.LocalIP(), cfg.RemoteIP())
	runSession(cfg, ev.Name)
}

// offerNetworkFallback scans LAN and runs a session over it.
func offerNetworkFallback(cfg *config.Config) {
	if !ui.Confirm("Scan local network for filetrans peers?") {
		return
	}
	peerIP := resolveOrScanPeer(cfg)
	if peerIP == "" {
		ui.Warnf("No peer found. Will retry when USB cable is connected.")
		return
	}
	saved := cfg.PeerIP
	cfg.PeerIP = peerIP
	runSession(cfg, "network")
	cfg.PeerIP = saved
}

// runSession selects a role and dispatches to sender or receiver.
func runSession(cfg *config.Config, ifaceName string) {
	role := resolveRole(cfg, ifaceName)
	cliFiles := flag.Args() // files passed as CLI positional arguments

	switch role {
	case "sender":
		runSender(cfg, cliFiles)
	case "receiver":
		runReceiver(cfg)
	}
}

func resolveRole(cfg *config.Config, ifaceName string) string {
	if cfg.Role == "sender" || cfg.Role == "receiver" {
		return cfg.Role
	}
	return ui.SelectRole(ifaceName, cfg.RemoteIP())
}

func runSender(cfg *config.Config, cliFiles []string) {
	files := cliFiles
	if len(files) == 0 {
		files = ui.SelectFiles()
	}
	if len(files) == 0 {
		ui.Warnf("No files selected.")
		return
	}

	ui.Infof("Connecting to %s ...", cfg.PeerWSURL())
	conn, err := handshake.Connect(cfg, protocol.RoleSender)
	if err != nil {
		ui.Errorf("Connection failed: %v", err)
		return
	}
	defer conn.Close()
	ui.Successf("Connected. Starting transfer...")

	baseDir := transfer.CommonBaseDir(files)
	if err := transfer.Send(conn, files, cfg, baseDir); err != nil {
		ui.Errorf("Transfer failed: %v", err)
		return
	}
	ui.Successf("All files sent successfully.")
}

func runReceiver(cfg *config.Config) {
	dlDir := ui.SelectDownloadDir(cfg.DownloadDir)
	if err := os.MkdirAll(dlDir, 0o755); err != nil {
		ui.Errorf("Cannot create download directory: %v", err)
		return
	}

	ui.Infof("Listening on %s", cfg.ServerAddr())
	ui.Infof("Now start the sender on the other machine.")
	conn, err := handshake.Listen(cfg, protocol.RoleReceiver)
	if err != nil {
		ui.Errorf("Accept failed: %v", err)
		return
	}
	defer conn.Close()
	ui.Successf("Sender connected.")

	if err := transfer.Receive(conn, dlDir); err != nil {
		ui.Errorf("Receive error: %v", err)
	}
}

// resolveOrScanPeer returns a usable peer IP, scanning LAN if needed.
func resolveOrScanPeer(cfg *config.Config) string {
	if cfg.PeerIP != "" {
		return cfg.PeerIP
	}

	ui.Infof("Scanning for filetrans peers on port %d ...", cfg.Port)
	peers, err := fallback.Scan(cfg)
	if err != nil || len(peers) == 0 {
		ui.Warnf("No peers found automatically.")
		return ui.AskManualIP()
	}

	if len(peers) == 1 {
		ui.Successf("Found peer: %s", peers[0])
		return peers[0]
	}

	fmt.Printf("\n  Found %d peers:\n", len(peers))
	for i, p := range peers {
		fmt.Printf("    %d) %s\n", i+1, p)
	}
	ui.Infof("Using %s  (use --peer=<ip> to override)", peers[0])
	return peers[0]
}
