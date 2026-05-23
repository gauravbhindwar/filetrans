//go:build !linux && !windows

package detector

import (
	"net"
	"strings"
	"time"
)

// otherDetector is a polling-based fallback for platforms without native
// netlink or WMI support (e.g. macOS). Uses the same snapshot-diff approach
// as the Windows detector.
type otherDetector struct{}

// New returns the polling detector for this platform.
func New() Detector { return &otherDetector{} }

func (d *otherDetector) Start(cfg Config) (<-chan InterfaceEvent, func(), error) {
	events := make(chan InterfaceEvent, 16)
	stop := make(chan struct{})
	cancel := func() { close(stop) }

	go func() {
		defer close(events)
		known := snapshotInterfaces()
		ticker := time.NewTicker(cfg.PollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				current := snapshotInterfaces()
				for name, ip := range current {
					if _, exists := known[name]; !exists {
						if isUSBLike(name, cfg) {
							events <- InterfaceEvent{Name: name, Added: true, LinkLocal: ip}
						}
					}
				}
				for name := range known {
					if _, exists := current[name]; !exists {
						if isUSBLike(name, cfg) {
							events <- InterfaceEvent{Name: name, Added: false}
						}
					}
				}
				known = current
			}
		}
	}()

	return events, cancel, nil
}

func snapshotInterfaces() map[string]string {
	result := make(map[string]string)
	ifaces, err := net.Interfaces()
	if err != nil {
		return result
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.To4() != nil {
				result[iface.Name] = ipNet.IP.String()
				break
			}
		}
	}
	return result
}

func isUSBLike(name string, cfg Config) bool {
	for _, w := range cfg.WatchNames {
		if name == w {
			return true
		}
	}
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, "usb") ||
		strings.HasPrefix(lower, "rndis") ||
		strings.HasPrefix(lower, "en") // macOS USB Ethernet shows as en3, en4, etc.
}
