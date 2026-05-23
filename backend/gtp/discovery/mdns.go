// Package discovery implements mDNS-based peer discovery for GauravTransfer Protocol.
// Peers announce themselves on the multicast group 224.0.0.251:5354 (standard mDNS port).
// No configuration needed — peers on the same L2 segment find each other automatically.
//
// Announcement wire format (plain UDP, not full DNS):
//
//   "GTP1 " + version + " " + port + " " + deviceID + "\n"
//
// This is intentionally minimal — not a full mDNS implementation, but sufficient for
// LAN/USB peer discovery without any dependency.
package discovery

import (
	"fmt"
	"net"
	"strings"
	"time"
)

const (
	mdnsGroup    = "224.0.0.251"
	mdnsPort     = 5354
	announceInterval = 2 * time.Second
	listenTimeout    = 8 * time.Second
)

// Peer is a discovered GTP peer.
type Peer struct {
	IP       string
	Port     int
	DeviceID string
	Version  string
}

// Announce broadcasts this node's presence on the LAN until ctx is cancelled.
func Announce(gtpPort int, deviceID string, stop <-chan struct{}) {
	addr := net.JoinHostPort(mdnsGroup, fmt.Sprintf("%d", mdnsPort))
	conn, err := net.Dial("udp4", addr)
	if err != nil {
		return
	}
	defer conn.Close()

	msg := fmt.Sprintf("GTP1 1.0 %d %s\n", gtpPort, deviceID)
	ticker := time.NewTicker(announceInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			conn.Write([]byte(msg))
		}
	}
}

// Scan listens for GTP peer announcements for up to timeout and returns found peers.
// Also sends one announce so remote peers can discover us.
func Scan(gtpPort int, deviceID string, timeout time.Duration) ([]Peer, error) {
	// Join multicast group.
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf(":%d", mdnsPort))
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return nil, fmt.Errorf("mdns listen: %w (try running as admin on Windows)", err)
	}
	defer conn.Close()

	// Join multicast — best effort.
	joinMulticast(conn)

	// Send our own announce so peers can hear us immediately.
	maddr, _ := net.ResolveUDPAddr("udp4", net.JoinHostPort(mdnsGroup, fmt.Sprintf("%d", mdnsPort)))
	msg := fmt.Sprintf("GTP1 1.0 %d %s\n", gtpPort, deviceID)
	conn.WriteToUDP([]byte(msg), maddr)

	conn.SetDeadline(time.Now().Add(timeout))

	seen := make(map[string]bool)
	var peers []Peer
	buf := make([]byte, 256)

	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			break // timeout
		}
		line := strings.TrimSpace(string(buf[:n]))
		p, ok := parsePeerLine(line, src.IP.String(), deviceID)
		if !ok {
			continue
		}
		key := p.IP + fmt.Sprint(p.Port)
		if !seen[key] {
			seen[key] = true
			peers = append(peers, p)
		}
	}

	return peers, nil
}

func parsePeerLine(line, srcIP, myDeviceID string) (Peer, bool) {
	// Format: "GTP1 <version> <port> <deviceID>"
	parts := strings.Fields(line)
	if len(parts) != 4 || parts[0] != "GTP1" {
		return Peer{}, false
	}
	// Ignore our own announcements.
	if parts[3] == myDeviceID {
		return Peer{}, false
	}
	var port int
	fmt.Sscanf(parts[2], "%d", &port)
	if port <= 0 || port > 65535 {
		return Peer{}, false
	}
	return Peer{
		IP:       srcIP,
		Port:     port,
		Version:  parts[1],
		DeviceID: parts[3],
	}, true
}
